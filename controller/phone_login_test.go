package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type phoneAuthTestResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Retry   int    `json:"retry_after_seconds"`
	Data    struct {
		ChallengeID         string `json:"challenge_id"`
		DeliveryStatus      string `json:"delivery_status"`
		AccessToken         string `json:"access_token"`
		RelayKey            string `json:"relay_key"`
		FlowToken           string `json:"flow_token"`
		RequireVerification bool   `json:"require_verification"`
		ExpiresAt           int64  `json:"expires_at"`
		Resend              int    `json:"resend_after_seconds"`
		User                struct {
			ID int `json:"id"`
		} `json:"user"`
		Session service.LoginSessionView `json:"session"`
	} `json:"data"`
}

func setupPhoneAuthRecovery(t *testing.T) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	models := []any{&model.User{}, &model.AuthFlow{}, &model.Token{}, &model.UserSession{}, &model.TwoFA{}, &model.TwoFABackupCode{}, &model.PasskeyCredential{}, &model.Log{}, &model.AuditLog{}}
	var db *gorm.DB
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	if dialect == "" || dialect == common.DatabaseTypeSQLite {
		dialect = common.DatabaseTypeSQLite
		var err error
		db, err = gorm.Open(sqlite.Open(t.TempDir()+"/phone.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(models...))
		var version string
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
		t.Logf("database: sqlite %s", version)
	} else {
		db, dialect = openTaskDialectDatabase(t, models...)
	}
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousEnabled, previousRedis, previousCache, previousBatch := common.PhoneLoginEnabled, common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	previousSecret, previousSender, previousSealer := common.SessionSecret, phoneCodeSender, phoneLoginSealer
	previousGlobal, previousBonus := common.QuotaForNewUser, common.PhoneRegistrationBonusYuan
	previousLimit, previousIssuance := common.UserSessionActiveLimit, common.UserSessionIssuanceLimit
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.PhoneLoginEnabled, common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = true, false, false, false
	common.SessionSecret = "phone-auth-recovery-test-only-not-a-deployment-secret"
	common.QuotaForNewUser, common.PhoneRegistrationBonusYuan = 77, 10
	common.UserSessionActiveLimit, common.UserSessionIssuanceLimit = 100, 100
	phoneCodeSender = func(string, string) error { return nil }
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMain, previousLogType)
		common.PhoneLoginEnabled, common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = previousEnabled, previousRedis, previousCache, previousBatch
		common.SessionSecret, phoneCodeSender, phoneLoginSealer = previousSecret, previousSender, previousSealer
		common.QuotaForNewUser, common.PhoneRegistrationBonusYuan = previousGlobal, previousBonus
		common.UserSessionActiveLimit, common.UserSessionIssuanceLimit = previousLimit, previousIssuance
	})
	// No schema was added. Repeated startup migration must preserve existing
	// flow rows, users and their unique phone identity on every real engine.
	legacy := model.AuthFlow{TokenHash: strings.Repeat("a", 64), Purpose: model.AuthFlowPurposeOAuth, Provider: "existing-provider", Payload: "existing-state", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.AutoMigrate(models...))
	var preserved model.AuthFlow
	require.NoError(t, db.First(&preserved, legacy.Id).Error)
	require.Equal(t, "existing-state", preserved.Payload)
	return db, dialect
}

func phoneAuthRequest(t *testing.T, handler gin.HandlerFunc, body any) (*httptest.ResponseRecorder, phoneAuthTestResponse) {
	t.Helper()
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/phone/login", strings.NewReader(string(encoded)))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	var result phoneAuthTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	return recorder, result
}

func phoneTestChallenge(t *testing.T, phone string) (string, string) {
	t.Helper()
	var code string
	phoneCodeSender = func(_ string, value string) error { code = value; return nil }
	_, sent := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString()})
	require.True(t, sent.Success)
	require.NotEmpty(t, code)
	return sent.Data.ChallengeID, code
}

func TestPhoneChallengeDeliveryAndRecovery(t *testing.T) {
	for _, outcome := range []string{"not_sent", "unknown", "pending"} {
		t.Run(outcome, func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			phone, key := "+8613800138000", uuid.NewString()
			calls := 0
			var deliveredCode string
			phoneCodeSender = func(_ string, code string) error {
				calls++
				deliveredCode = code
				if outcome == "unknown" {
					return common.ErrSmsDeliveryUnknown
				}
				return common.ErrSmsRejected
			}
			var first phoneAuthTestResponse
			if outcome == "pending" {
				reserved, created, err := model.ReservePhoneChallenge(phone, key, "123456", time.Now().Add(5*time.Minute))
				require.NoError(t, err)
				require.True(t, created)
				deliveredCode = "123456"
				_, first = phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: key})
				require.Equal(t, reserved.ID, first.Data.ChallengeID)
				require.Equal(t, "PHONE_CHALLENGE_PENDING", first.Code)
			} else {
				_, first = phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: key})
				require.Equal(t, 1, calls)
			}
			_, replayed := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: key})
			assert.Equal(t, first.Data.ChallengeID, replayed.Data.ChallengeID)
			assert.Equal(t, first.Code, replayed.Code)
			assert.LessOrEqual(t, calls, 1, "same key must never dispatch again")
			_, conflict := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: "+8613800138001", IdempotencyKey: key})
			assert.Equal(t, "PHONE_IDEMPOTENCY_CONFLICT", conflict.Code)
			phoneCodeSender = func(string, string) error { calls++; return nil }
			r, newKey := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString()})
			if outcome == "not_sent" {
				assert.True(t, newKey.Success, "known-not-sent uses no success cooldown")
				assert.Equal(t, 2, calls)
			} else {
				assert.Equal(t, http.StatusTooManyRequests, r.Code)
				assert.Equal(t, "PHONE_RATE_LIMITED", newKey.Code)
				assert.Positive(t, newKey.Retry)
				assert.Equal(t, fmt.Sprint(newKey.Retry), r.Header().Get("Retry-After"))
				login := phoneLoginRequest{PhoneE164: phone, ChallengeID: first.Data.ChallengeID, Code: deliveredCode, IdempotencyKey: uuid.NewString()}
				_, result := phoneAuthRequest(t, PhoneLogin, login)
				assert.True(t, result.Success, "a potentially delivered code remains usable")
				var sessions int64
				require.NoError(t, db.Model(&model.UserSession{}).Count(&sessions).Error)
				assert.EqualValues(t, 1, sessions)
			}
		})
	}
}

func TestPhoneLoginDownstreamFailureAndResponseLoss(t *testing.T) {
	for _, failureStage := range []string{"user", "grant", "relay", "session", "seal", "receipt"} {
		t.Run(failureStage, func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			phone := "+8613800138010"
			challenge, code := phoneTestChallenge(t, phone)
			body := phoneLoginRequest{PhoneE164: phone, ChallengeID: challenge, Code: code, IdempotencyKey: uuid.NewString()}
			forced := errors.New("injected downstream failure")
			if failureStage == "seal" {
				phoneLoginSealer = func(*service.PhoneLoginResult, string) (string, error) { return "", forced }
			} else if failureStage == "grant" || failureStage == "receipt" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("phone:test:fail", func(tx *gorm.DB) {
					if fields, ok := tx.Statement.Dest.(map[string]any); ok && (failureStage == "grant" && fields["quota"] != nil || failureStage == "receipt" && fields["session_id"] != nil) {
						tx.AddError(forced)
					}
				}))
			} else {
				target := map[string]reflect.Type{"user": reflect.TypeFor[model.User](), "relay": reflect.TypeFor[model.Token](), "session": reflect.TypeFor[model.UserSession]()}[failureStage]
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("phone:test:fail", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == target {
						tx.AddError(forced)
					}
				}))
			}
			failed, result := phoneAuthRequest(t, PhoneLogin, body)
			assert.Equal(t, "PHONE_LOGIN_UNAVAILABLE", result.Code)
			assert.Empty(t, failed.Header().Values("Set-Cookie"))
			for _, change := range []string{"phone", "challenge", "code"} {
				changed := body
				switch change {
				case "phone":
					changed.PhoneE164 = "+8613800138999"
				case "challenge":
					changed.ChallengeID = strings.Repeat("b", 64)
				case "code":
					changed.Code = "000000"
					if changed.Code == code {
						changed.Code = "999999"
					}
				}
				_, conflict := phoneAuthRequest(t, PhoneLogin, changed)
				assert.Equal(t, "PHONE_IDEMPOTENCY_CONFLICT", conflict.Code, "rollback must preserve immutable key binding")
			}
			for _, record := range []any{&model.User{}, &model.Token{}, &model.UserSession{}} {
				var count int64
				require.NoError(t, db.Model(record).Count(&count).Error)
				assert.Zero(t, count, "downstream failure must roll back identity, grant, relay and session")
			}
			if failureStage == "grant" || failureStage == "receipt" {
				require.NoError(t, db.Callback().Update().Remove("phone:test:fail"))
			} else if failureStage != "seal" {
				require.NoError(t, db.Callback().Create().Remove("phone:test:fail"))
			}
			phoneLoginSealer = service.SealPhoneLoginResult
			first, success := phoneAuthRequest(t, PhoneLogin, body)
			require.True(t, success.Success, first.Body.String())
			require.NotEmpty(t, success.Data.AccessToken)
			require.NotEmpty(t, success.Data.RelayKey)
			replayed, again := phoneAuthRequest(t, PhoneLogin, body)
			require.True(t, again.Success, replayed.Body.String())
			assert.Equal(t, success.Data.AccessToken, again.Data.AccessToken)
			assert.Equal(t, success.Data.Session.SID, again.Data.Session.SID)
			assert.Equal(t, success.Data.RelayKey, again.Data.RelayKey)
			assert.Equal(t, first.Header().Values("Set-Cookie"), replayed.Header().Values("Set-Cookie"))
			var saved model.User
			require.NoError(t, db.First(&saved, success.Data.User.ID).Error)
			assert.Equal(t, 77+quotaFromYuan(10), saved.Quota)
			for _, record := range []any{&model.User{}, &model.Token{}, &model.UserSession{}} {
				var count int64
				require.NoError(t, db.Model(record).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			}
			var flows []model.AuthFlow
			require.NoError(t, db.Find(&flows).Error)
			for _, flow := range flows {
				assert.NotContains(t, flow.Payload, success.Data.AccessToken)
				assert.NotContains(t, flow.Payload, success.Data.RelayKey)
				assert.NotContains(t, flow.Payload, code)
			}
			body.Code = "000000"
			if body.Code == code {
				body.Code = "999999"
			}
			_, conflict := phoneAuthRequest(t, PhoneLogin, body)
			assert.Equal(t, "PHONE_IDEMPOTENCY_CONFLICT", conflict.Code)
			body.Code, body.IdempotencyKey = code, uuid.NewString()
			_, consumed := phoneAuthRequest(t, PhoneLogin, body)
			assert.Equal(t, "PHONE_CHALLENGE_CONSUMED", consumed.Code)
		})
	}
}

func TestPhoneLegacyClientsUseDurableChallengesWithoutReceiptReplay(t *testing.T) {
	for _, outcome := range []string{"accepted", "unknown", "not_sent", "downstream failure", "five guesses"} {
		t.Run(outcome, func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			phone := "+8613800138060"
			var code string
			calls := 0
			phoneCodeSender = func(_ string, value string) error {
				calls++
				code = value
				switch outcome {
				case "unknown":
					return common.ErrSmsDeliveryUnknown
				case "not_sent":
					return common.ErrSmsNotConfigured
				}
				return nil
			}
			_, sent := phoneAuthRequest(t, SendPhoneLoginCode, map[string]string{"phone_e164": phone})
			require.NotEmpty(t, sent.Data.ChallengeID)
			if outcome == "not_sent" {
				for range 5 {
					_, failed := phoneAuthRequest(t, SendPhoneLoginCode, map[string]string{"phone_e164": phone})
					assert.Equal(t, "PHONE_CHALLENGE_UNAVAILABLE", failed.Code)
				}
				assert.Equal(t, 6, calls, "known-not-sent dispatches never spend the success rate budget")
				return
			}
			_, cooldown := phoneAuthRequest(t, SendPhoneLoginCode, map[string]string{"phone_e164": phone})
			assert.Equal(t, "PHONE_RATE_LIMITED", cooldown.Code)
			assert.Equal(t, 1, calls, "a legacy retry cannot bypass accepted/unknown reservations")
			if outcome == "five guesses" {
				wrong := "000000"
				if wrong == code {
					wrong = "999999"
				}
				for range 5 {
					_, invalid := phoneAuthRequest(t, PhoneLogin, map[string]string{"phone_e164": phone, "code": wrong})
					assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", invalid.Code)
				}
				_, limited := phoneAuthRequest(t, PhoneLogin, map[string]string{"phone_e164": phone, "code": code})
				assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", limited.Code)
				return
			}
			if outcome == "downstream failure" {
				phoneLoginSealer = func(*service.PhoneLoginResult, string) (string, error) { return "", errors.New("fixture seal failure") }
				failed, result := phoneAuthRequest(t, PhoneLogin, map[string]string{"phone_e164": phone, "code": code})
				assert.Equal(t, "PHONE_LOGIN_UNAVAILABLE", result.Code)
				assert.Empty(t, failed.Header().Values("Set-Cookie"))
				phoneLoginSealer = service.SealPhoneLoginResult
			}
			first, result := phoneAuthRequest(t, PhoneLogin, map[string]string{"phone_e164": phone, "code": code})
			require.True(t, result.Success, first.Body.String())
			assert.NotEmpty(t, result.Data.RelayKey)
			second, repeated := phoneAuthRequest(t, PhoneLogin, map[string]string{"phone_e164": phone, "code": code})
			assert.Equal(t, "PHONE_CHALLENGE_CONSUMED", repeated.Code, "legacy OTP alone never recovers already-issued credentials")
			assert.Empty(t, second.Header().Values("Set-Cookie"))
			var sessions int64
			require.NoError(t, db.Model(&model.UserSession{}).Count(&sessions).Error)
			assert.EqualValues(t, 1, sessions)
			_, partial := phoneAuthRequest(t, PhoneLogin, map[string]any{"phone_e164": phone, "code": code, "challenge_id": sent.Data.ChallengeID})
			assert.Equal(t, "PHONE_INVALID_PARAMETERS", partial.Code)
			_, partial = phoneAuthRequest(t, SendPhoneLoginCode, map[string]any{"phone_e164": phone, "contract_version": 2})
			assert.Equal(t, "PHONE_INVALID_PARAMETERS", partial.Code)
		})
	}
}

func TestPhoneMFAFactorSurvivesRelayOrSessionFailure(t *testing.T) {
	for _, kind := range []string{"backup", "totp"} {
		for _, stage := range []string{"relay", "session"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				db, _ := setupPhoneAuthRecovery(t)
				phone := "+8613800138070"
				challenge, smsCode := phoneTestChallenge(t, phone)
				user, _, err := findOrCreatePhoneUser(phone)
				require.NoError(t, err)
				factor := model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
				require.NoError(t, db.Create(&factor).Error)
				code, err := totp.GenerateCode(factor.Secret, time.Now())
				require.NoError(t, err)
				if kind == "backup" {
					code = "ABCD-1234"
					hash, err := common.HashBackupCode(code)
					require.NoError(t, err)
					require.NoError(t, db.Create(&model.TwoFABackupCode{UserId: user.Id, CodeHash: hash}).Error)
				}
				_, pending := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: smsCode, ChallengeID: challenge, IdempotencyKey: uuid.NewString()})
				require.Equal(t, "PHONE_LOGIN_VERIFICATION_REQUIRED", pending.Code)
				target := reflect.TypeFor[model.Token]()
				if stage == "session" {
					target = reflect.TypeFor[model.UserSession]()
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("phone:test:mfa-failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == target {
						tx.AddError(errors.New("injected MFA downstream failure"))
					}
				}))
				body := map[string]string{"flow_token": pending.Data.FlowToken, "method": "2fa", "code": code}
				response, failed := phoneAuthRequest(t, VerifyLogin, body)
				assert.False(t, failed.Success)
				assert.Empty(t, response.Header().Values("Set-Cookie"))
				var savedFactor model.TwoFA
				require.NoError(t, db.First(&savedFactor, factor.Id).Error)
				assert.Nil(t, savedFactor.LastUsedAt)
				var pendingFlow model.AuthFlow
				require.NoError(t, db.Where("purpose = ?", model.AuthFlowPurposeLoginVerification).First(&pendingFlow).Error)
				assert.Nil(t, pendingFlow.ConsumedAt)
				if kind == "backup" {
					var backup model.TwoFABackupCode
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&backup).Error)
					assert.False(t, backup.IsUsed)
				}
				for _, record := range []any{&model.Token{}, &model.UserSession{}} {
					var count int64
					require.NoError(t, db.Model(record).Count(&count).Error)
					assert.Zero(t, count)
				}
				require.NoError(t, db.Callback().Create().Remove("phone:test:mfa-failure"))
				response, completed := phoneAuthRequest(t, VerifyLogin, body)
				require.True(t, completed.Success, response.Body.String())
				assert.NotEmpty(t, completed.Data.AccessToken)
				assert.NotEmpty(t, completed.Data.RelayKey)
				assert.NotEmpty(t, response.Header().Values("Set-Cookie"))
				if kind == "backup" {
					var backup model.TwoFABackupCode
					require.NoError(t, db.Where("user_id = ?", user.Id).First(&backup).Error)
					assert.True(t, backup.IsUsed)
				}
				response, reused := phoneAuthRequest(t, VerifyLogin, body)
				assert.False(t, reused.Success)
				assert.Empty(t, response.Header().Values("Set-Cookie"))
				var count int64
				require.NoError(t, db.Model(&model.UserSession{}).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})
		}
	}
}

func TestPhoneMFAInvalidAttemptsPersistAfterFlowRollback(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	phone := "+8613800138071"
	challenge, code := phoneTestChallenge(t, phone)
	user, _, err := findOrCreatePhoneUser(phone)
	require.NoError(t, err)
	factor := model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, db.Create(&factor).Error)
	_, pending := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: code, ChallengeID: challenge, IdempotencyKey: uuid.NewString()})
	require.Equal(t, "PHONE_LOGIN_VERIFICATION_REQUIRED", pending.Code)
	for range 3 {
		_, failed := phoneAuthRequest(t, VerifyLogin, map[string]string{"flow_token": pending.Data.FlowToken, "code": "not-a-code"})
		assert.False(t, failed.Success)
	}
	var saved model.TwoFA
	require.NoError(t, db.First(&saved, factor.Id).Error)
	assert.Equal(t, 3, saved.FailedAttempts)
	var flow model.AuthFlow
	require.NoError(t, db.Where("purpose = ?", model.AuthFlowPurposeLoginVerification).First(&flow).Error)
	assert.Nil(t, flow.ConsumedAt)
}

func TestPhoneLoginRecoveryNeverResurrectsCredentials(t *testing.T) {
	for _, change := range []string{"logout", "refresh rotation", "auth version", "disabled user", "disabled relay", "expired receipt"} {
		t.Run(change, func(t *testing.T) {
			db, _ := setupPhoneAuthRecovery(t)
			phone := "+8613800138020"
			challenge, code := phoneTestChallenge(t, phone)
			body := phoneLoginRequest{PhoneE164: phone, ChallengeID: challenge, Code: code, IdempotencyKey: uuid.NewString()}
			first, result := phoneAuthRequest(t, PhoneLogin, body)
			require.True(t, result.Success)
			switch change {
			case "logout":
				_, err := model.RevokeUserSession(result.Data.User.ID, result.Data.Session.SID, "test_logout")
				require.NoError(t, err)
			case "refresh rotation":
				var refresh string
				for _, cookie := range first.Result().Cookies() {
					if cookie.Name == service.RefreshCookieName {
						refresh = cookie.Value
					}
				}
				_, _, err := service.RefreshLoginSession(refresh, "", "127.0.0.1", "test")
				require.NoError(t, err)
			case "auth version":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", result.Data.User.ID).Update("auth_version", gorm.Expr("auth_version + 1")).Error)
			case "disabled user":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", result.Data.User.ID).Update("status", common.UserStatusDisabled).Error)
			case "disabled relay":
				require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", result.Data.User.ID).Update("status", common.TokenStatusDisabled).Error)
			case "expired receipt":
				require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ?", "phone_exchange").Update("expires_at", time.Now().Add(-time.Second)).Error)
			}
			replay, blocked := phoneAuthRequest(t, PhoneLogin, body)
			assert.Equal(t, "PHONE_CHALLENGE_CONSUMED", blocked.Code)
			assert.Empty(t, replay.Header().Values("Set-Cookie"))
			var count int64
			require.NoError(t, db.Model(&model.UserSession{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestPhoneLoginLimitsAndMFACannotBeBypassed(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	phone := "+8613800138030"
	challenge, code := phoneTestChallenge(t, phone)
	_, legacy := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: code, ContractVersion: 2})
	assert.Equal(t, "PHONE_INVALID_PARAMETERS", legacy.Code)
	wrong := "000000"
	if wrong == code {
		wrong = "999999"
	}
	wrongRequest := phoneLoginRequest{PhoneE164: phone, Code: wrong, ChallengeID: challenge, IdempotencyKey: uuid.NewString()}
	for range 2 {
		_, invalid := phoneAuthRequest(t, PhoneLogin, wrongRequest)
		assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", invalid.Code)
	}
	changedWrong := wrongRequest
	changedWrong.Code = code
	_, conflict := phoneAuthRequest(t, PhoneLogin, changedWrong)
	assert.Equal(t, "PHONE_IDEMPOTENCY_CONFLICT", conflict.Code)
	for range 4 {
		wrong := "000000"
		if wrong == code {
			wrong = "999999"
		}
		_, invalid := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: wrong, ChallengeID: challenge, IdempotencyKey: uuid.NewString()})
		assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", invalid.Code)
	}
	_, exhausted := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: code, ChallengeID: challenge, IdempotencyKey: uuid.NewString()})
	assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", exhausted.Code)
	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count)

	phone = "+8613800138031"
	challenge, code = phoneTestChallenge(t, phone)
	user, _, err := findOrCreatePhoneUser(phone)
	require.NoError(t, err)
	factor := model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, db.Create(&factor).Error)
	body := phoneLoginRequest{PhoneE164: phone, Code: code, ChallengeID: challenge, IdempotencyKey: uuid.NewString()}
	response, pending := phoneAuthRequest(t, PhoneLogin, body)
	require.True(t, pending.Success, response.Body.String())
	assert.Equal(t, "PHONE_LOGIN_VERIFICATION_REQUIRED", pending.Code)
	assert.True(t, pending.Data.RequireVerification)
	assert.Empty(t, pending.Data.AccessToken)
	assert.Empty(t, pending.Data.RelayKey)
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.UserSession{}).Count(&count).Error)
	assert.Zero(t, count)
	_, replayed := phoneAuthRequest(t, PhoneLogin, body)
	assert.Equal(t, pending.Data.FlowToken, replayed.Data.FlowToken)
	mfaCode, err := totp.GenerateCode(factor.Secret, time.Now())
	require.NoError(t, err)
	verified, complete := phoneAuthRequest(t, VerifyLogin, map[string]string{"flow_token": pending.Data.FlowToken, "code": mfaCode})
	require.True(t, complete.Success, verified.Body.String())
	assert.NotEmpty(t, complete.Data.AccessToken)
	assert.NotEmpty(t, complete.Data.RelayKey)
	assert.NotEmpty(t, verified.Header().Values("Set-Cookie"))
	_, reused := phoneAuthRequest(t, PhoneLogin, body)
	assert.Equal(t, "PHONE_CHALLENGE_CONSUMED", reused.Code, "consumed SMS cannot start another factor flow")
}

func TestPhoneChallengeCooldownAndReplacement(t *testing.T) {
	db, _ := setupPhoneAuthRecovery(t)
	phone := "+8613800138050"
	first, firstCode := phoneTestChallenge(t, phone)
	require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ?", "phone_challenge").Update("created_at", time.Now().Add(-59900*time.Millisecond)).Error)
	response, blocked := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString()})
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Positive(t, blocked.Retry, "a fractional remaining cooldown must not round down to zero")
	require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ?", "phone_challenge").Update("created_at", time.Now().Add(-61*time.Second)).Error)
	phoneCodeSender = func(string, string) error { return common.ErrSmsNotConfigured }
	_, failed := phoneAuthRequest(t, SendPhoneLoginCode, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString()})
	assert.Equal(t, "not_sent", failed.Data.DeliveryStatus)
	// A failed replacement leaves the earlier delivered code usable.
	_, old := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, ChallengeID: first, Code: firstCode, IdempotencyKey: uuid.NewString()})
	assert.True(t, old.Success)

	phone = "+8613800138051"
	first, firstCode = phoneTestChallenge(t, phone)
	require.NoError(t, db.Model(&model.AuthFlow{}).Where("purpose = ?", "phone_challenge").Update("created_at", time.Now().Add(-61*time.Second)).Error)
	second, secondCode := phoneTestChallenge(t, phone)
	_, superseded := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, ChallengeID: first, Code: firstCode, IdempotencyKey: uuid.NewString()})
	assert.Equal(t, "PHONE_CODE_INVALID_OR_EXPIRED", superseded.Code)
	_, current := phoneAuthRequest(t, PhoneLogin, phoneLoginRequest{PhoneE164: phone, ChallengeID: second, Code: secondCode, IdempotencyKey: uuid.NewString()})
	assert.True(t, current.Success)
}

func TestPhoneLoginConcurrentExchangeCommitsOneIdentity(t *testing.T) {
	db, dialect := setupPhoneAuthRecovery(t)
	if dialect != common.DatabaseTypeSQLite {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(8)
	}
	phone := "+8613800138040"
	challenge, code := phoneTestChallenge(t, phone)
	body := phoneLoginRequest{PhoneE164: phone, Code: code, ChallengeID: challenge, IdempotencyKey: uuid.NewString()}
	firstAtUser, releaseFirst, secondAtBinding := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var bindings atomic.Int32
	var release sync.Once
	defer release.Do(func() { close(releaseFirst) })
	require.NoError(t, db.Callback().Create().Before("gorm:begin_transaction").Register("phone:test:concurrency", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil {
			return
		}
		if tx.Statement.Schema.ModelType == reflect.TypeFor[model.AuthFlow]() {
			if flow, ok := tx.Statement.Dest.(*model.AuthFlow); ok && flow.Purpose == "phone_exchange" && bindings.Add(1) == 2 {
				close(secondAtBinding)
			}
		}
		if tx.Statement.Schema.ModelType == reflect.TypeFor[model.User]() {
			close(firstAtUser)
			<-releaseFirst
		}
	}))
	defer func() { require.NoError(t, db.Callback().Create().Remove("phone:test:concurrency")) }()
	var responses [2]phoneAuthTestResponse
	var workers sync.WaitGroup
	workers.Go(func() { _, responses[0] = phoneAuthRequest(t, PhoneLogin, body) })
	select {
	case <-firstAtUser:
	case <-time.After(10 * time.Second):
		t.Fatal("first exchange never reached user creation")
	}
	workers.Go(func() { _, responses[1] = phoneAuthRequest(t, PhoneLogin, body) })
	select {
	case <-secondAtBinding:
	case <-time.After(10 * time.Second):
		t.Fatal("second exchange never overlapped the database transaction")
	}
	release.Do(func() { close(releaseFirst) })
	workers.Wait()
	require.True(t, responses[0].Success)
	require.True(t, responses[1].Success)
	assert.Equal(t, responses[0].Data.AccessToken, responses[1].Data.AccessToken)
	assert.Equal(t, responses[0].Data.Session.SID, responses[1].Data.Session.SID)
	for _, record := range []any{&model.User{}, &model.Token{}, &model.UserSession{}} {
		var count int64
		require.NoError(t, db.Model(record).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
	var saved model.User
	require.NoError(t, db.First(&saved, responses[0].Data.User.ID).Error)
	assert.Equal(t, 77+quotaFromYuan(10), saved.Quota)
}
