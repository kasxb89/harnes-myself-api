package controller

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type bossAuthResponse struct {
	Success bool `json:"success"`
	Error   struct {
		Code string `json:"code"`
	} `json:"error"`
	Data struct {
		ChallengeID         string                             `json:"challenge_id"`
		ExpiresAt           int64                              `json:"expires_at"`
		Resend              int                                `json:"resend_after_seconds"`
		AccessToken         string                             `json:"access_token"`
		RelayKey            string                             `json:"relay_key"`
		FlowToken           string                             `json:"flow_token"`
		RequireVerification bool                               `json:"require_verification"`
		Methods             []service.VerificationMethodOption `json:"methods"`
		Session             service.LoginSessionView           `json:"session"`
		User                struct {
			ID    int    `json:"id"`
			Group string `json:"group"`
			Quota int    `json:"quota"`
		} `json:"user"`
	} `json:"data"`
}

type bossPhoneFixture struct {
	db      *gorm.DB
	dialect common.DatabaseType
	code    string
	sends   int
}

// Only the offline helper enables this fixture. The private database is checked
// before any table mutation; exact table names also exercise real audit writes.
func bossPrivatePhoneDatabase(t *testing.T, dialect common.DatabaseType, models ...any) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_" + strings.ToUpper(string(dialect)) + "_DSN")
	var driver gorm.Dialector
	if dialect == common.DatabaseTypeMySQL {
		config, err := gomysql.ParseDSN(dsn)
		require.NoError(t, err)
		require.Equal(t, "unix", config.Net)
		require.Contains(t, config.Addr, "/.data/db/")
		require.True(t, strings.HasPrefix(config.DBName, "boss_test"))
		driver = mysql.Open(dsn)
	} else {
		require.Equal(t, common.DatabaseTypePostgreSQL, dialect)
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
		require.True(t, strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "boss_test"))
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	query := "SELECT DATABASE()"
	if dialect == common.DatabaseTypePostgreSQL {
		query = "SELECT current_database()"
	}
	var database string
	require.NoError(t, db.Raw(query).Scan(&database).Error)
	require.True(t, strings.HasPrefix(database, "boss_test"))
	// Grants are added by the upgrade fixture after its initial migration.
	// Clear their previous case as well, while preserving that upgrade path.
	dropModels := append(append([]any(nil), models...), &model.PhoneLoginGrant{})
	require.NoError(t, db.Migrator().DropTable(dropModels...))
	require.NoError(t, db.AutoMigrate(models...))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(dropModels...))
		_ = sqlDB.Close()
	})
	return db
}

func setupBossPhone(t *testing.T) *bossPhoneFixture {
	t.Helper()
	db, dialect := setupPhoneAuthRecovery(t) // Real SQLite/MySQL/PostgreSQL fixture; repeated migrations included.
	requested := os.Getenv("TEST_TASK_DB_DIALECT")
	if requested == "" {
		requested = "sqlite"
	}
	require.Equal(t, requested, string(dialect), "fixture must select the requested engine")
	require.Equal(t, requested, db.Dialector.Name(), "fixture must not silently fall back to SQLite")
	query := "SELECT version()"
	if dialect == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	require.NotEmpty(t, version)
	if dialect == common.DatabaseTypePostgreSQL {
		require.True(t, strings.HasPrefix(version, "PostgreSQL "))
	}
	if dialect != common.DatabaseTypeSQLite {
		query = "SELECT DATABASE()"
		if dialect == common.DatabaseTypePostgreSQL {
			query = "SELECT current_database()"
		}
		var database string
		require.NoError(t, db.Raw(query).Scan(&database).Error)
		require.True(t, strings.HasPrefix(database, "boss_test"), "only the explicit private test database may be used")
	}
	t.Logf("BOSS_TEST_DATABASE dialect=%s version=%s", dialect, version)
	fixture := &bossPhoneFixture{db: db, dialect: dialect}
	oldSender, oldSchedule, oldSealer, oldGroup := bossPhoneCodeSender, bossPhoneSchedule, bossPhoneLoginSealer, common.PhoneLoginDefaultGroup
	common.PhoneLoginDefaultGroup = "default"
	bossPhoneSchedule = func(action func()) { action() }
	bossPhoneCodeSender = func(_ string, code string) error { fixture.sends++; fixture.code = code; return nil }
	t.Cleanup(func() {
		bossPhoneCodeSender, bossPhoneSchedule, bossPhoneLoginSealer, common.PhoneLoginDefaultGroup = oldSender, oldSchedule, oldSealer, oldGroup
	})
	return fixture
}

func bossTestUser(t *testing.T, db *gorm.DB, phone *string, group string, status int) *model.User {
	t.Helper()
	unique := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := &model.User{Username: "boss_" + unique[:10], AffCode: unique, PhoneE164: phone, Group: group, Status: status, AuthVersion: 1, Quota: 431, UsedQuota: 7}
	require.NoError(t, db.Create(user).Error)
	return user
}

func bossRequest(t *testing.T, handler gin.HandlerFunc, body any, bearer ...string) (*httptest.ResponseRecorder, bossAuthResponse) {
	t.Helper()
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/boss/test", strings.NewReader(string(encoded)))
	c.Request.Header.Set("Content-Type", "application/json")
	if len(bearer) > 0 {
		c.Request.Header.Set("Authorization", "Bearer "+bearer[0])
	}
	handler(c)
	var result bossAuthResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	return recorder, result
}

func bossTestChallenge(t *testing.T, phone string) phoneLoginRequest {
	t.Helper()
	r, challenge := bossRequest(t, BossPhoneChallenge, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
	require.Equal(t, http.StatusAccepted, r.Code, r.Body.String())
	require.True(t, challenge.Success)
	require.Len(t, challenge.Data.ChallengeID, 64)
	return phoneLoginRequest{PhoneE164: phone, ChallengeID: challenge.Data.ChallengeID, IdempotencyKey: uuid.NewString(), ContractVersion: 2}
}

func TestBossMiddlewareFailuresKeepContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"rate_limit", http.StatusTooManyRequests, "BOSS_RATE_LIMITED"},
		{"body_limit", http.StatusRequestEntityTooLarge, "BOSS_INVALID_PARAMETERS"},
		{"limiter_failure", http.StatusInternalServerError, "BOSS_AUTH_UNAVAILABLE"},
		{"controller_failure", http.StatusUnauthorized, "BOSS_SESSION_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(BossAuthEnvelope())
			router.GET("/api/boss/auth/state", func(c *gin.Context) {
				switch tc.status {
				case http.StatusTooManyRequests:
					c.Header("Retry-After", "7")
					common.ApiErrorStatus(c, tc.status, common.NewMessage("Too many requests"))
				case http.StatusUnauthorized:
					bossPhoneError(c, tc.status, tc.code, "Authentication is required", 0)
				default:
					c.AbortWithStatus(tc.status)
				}
			})
			r := httptest.NewRecorder()
			router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/boss/auth/state", nil))
			assert.Equal(t, tc.status, r.Code)
			var envelope map[string]common.RawMessage
			require.NoError(t, common.Unmarshal(r.Body.Bytes(), &envelope))
			assert.Len(t, envelope, 1)
			var failure struct {
				Code  string `json:"code"`
				Retry int    `json:"retry_after_seconds"`
			}
			require.NoError(t, common.Unmarshal(envelope["error"], &failure))
			assert.Equal(t, tc.code, failure.Code)
			assert.Contains(t, r.Header().Get("Cache-Control"), "no-store")
			if tc.status == http.StatusTooManyRequests {
				assert.Equal(t, 7, failure.Retry)
				assert.Equal(t, "7", r.Header().Get("Retry-After"))
			}
		})
	}
}

func TestBossContractVersionsAndStrictFields(t *testing.T) {
	f := setupBossPhone(t)
	r, _ := bossRequest(t, BossCapabilities, nil)
	var capabilities map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(r.Body.Bytes(), &capabilities))
	assert.Len(t, capabilities, 2)
	var data map[string]any
	require.NoError(t, common.Unmarshal(capabilities["data"], &data))
	assert.Equal(t, map[string]any{"contract_version": float64(1), "phone_contract_version": float64(2), "required_group": "Boss", "registration_enabled": false, "max_active_sessions": float64(1)}, data)
	for _, endpoint := range []struct {
		name    string
		handler gin.HandlerFunc
		body    map[string]any
	}{
		{"challenge", BossPhoneChallenge, map[string]any{"phone_e164": "+8613800101090", "idempotency_key": uuid.NewString(), "contract_version": 2}},
		{"login", BossPhoneLogin, map[string]any{"phone_e164": "+8613800101090", "code": "123456", "challenge_id": strings.Repeat("a", 64), "idempotency_key": uuid.NewString(), "contract_version": 2}},
		{"verify", BossPhoneVerify, map[string]any{"flow_token": strings.Repeat("a", 43), "method": "totp", "code": "123456", "idempotency_key": uuid.NewString(), "contract_version": 2}},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, version := range []int{0, 1, 3} {
				endpoint.body["contract_version"] = version
				r, result := bossRequest(t, endpoint.handler, endpoint.body)
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Equal(t, "BOSS_INVALID_PARAMETERS", result.Error.Code)
			}
			endpoint.body["contract_version"] = 2
			encoded, err := common.Marshal(endpoint.body)
			require.NoError(t, err)
			for _, invalid := range []string{
				string(encoded) + " {}",
				strings.TrimSuffix(string(encoded), "}") + `,"user_id":1}`,
				strings.Replace(string(encoded), `"contract_version"`, `"ContractVersion"`, 1),
				strings.Replace(string(encoded), `"contract_version":2`, `"contract_version":null`, 1),
				strings.Replace(string(encoded), `"contract_version":2`, `"contract_version":"2"`, 1),
				`{"padding":"` + strings.Repeat("x", 8<<10) + `"}`,
			} {
				r := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(r)
				c.Request = httptest.NewRequest(http.MethodPost, "/api/boss/phone/"+endpoint.name, strings.NewReader(invalid))
				endpoint.handler(c)
				assert.Equal(t, http.StatusBadRequest, r.Code)
				var failure map[string]common.RawMessage
				require.NoError(t, common.Unmarshal(r.Body.Bytes(), &failure))
				assert.Len(t, failure, 1)
				assert.Contains(t, failure, "error")
				assert.Empty(t, r.Header().Values("Set-Cookie"))
			}
			r, _ := bossRequest(t, endpoint.handler, endpoint.body)
			assert.NotEqual(t, http.StatusBadRequest, r.Code, "only phone contract version 2 is accepted")
		})
	}
	assert.Zero(t, f.sends)
	var count int64
	require.NoError(t, f.db.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBossFenceTargetsAndOrdinaryWrites(t *testing.T) {
	f := setupBossPhone(t)
	phone, otherPhone := "+8613800101091", "+8613800101092"
	user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
	other := bossTestUser(t, f.db, &otherPhone, "Boss", common.UserStatusEnabled)
	var sessions []bossAuthResponse
	for _, number := range []string{phone, otherPhone} {
		request := bossTestChallenge(t, number)
		request.Code = f.code
		r, session := bossRequest(t, BossPhoneLogin, request)
		require.True(t, session.Success, r.Body.String())
		sessions = append(sessions, session)
	}
	model.UpdateUserLastLoginAt(user.Id)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 432).Error)
	require.NoError(t, f.db.Model(user).UpdateColumn("used_quota", gorm.Expr("used_quota + 1")).Error)
	copy := *user
	copy.Group, copy.Status, copy.Quota = "not-Boss", common.UserStatusDisabled, 433
	require.NoError(t, f.db.Model(user).Select("quota", "last_login_at").Updates(&copy).Error)
	require.NoError(t, f.db.Model(user).Omit("group", "status", "phone_e164", "auth_version", "deleted_at").Updates(&copy).Error)
	var saved model.User
	require.NoError(t, f.db.First(&saved, user.Id).Error)
	saved.Quota++
	require.NoError(t, f.db.Save(&saved).Error, "Save must compare authoritative values, not treat every full struct as an identity change")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return saved.UpdateWithTx(tx, false) }))
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{"group": "Boss", "status": common.UserStatusEnabled, "phone_e164": phone, "auth_version": 1}).Error)
	noMatch := f.db.Model(&model.User{}).Where("id = ? AND status = ?", user.Id, common.UserStatusDisabled).Update("group", "default")
	require.NoError(t, noMatch.Error)
	assert.Zero(t, noMatch.RowsAffected)
	noMatch = f.db.Model(&model.User{Id: user.Id}).Where("id = ?", other.Id).Update("group", "default")
	require.NoError(t, noMatch.Error)
	assert.Zero(t, noMatch.RowsAffected)
	assert.ErrorIs(t, f.db.Model(&model.User{}).Update("group", "default").Error, gorm.ErrMissingWhereClause)
	for _, session := range sessions {
		r, state := bossRequest(t, BossAuthState, nil, session.Data.AccessToken)
		assert.Equal(t, http.StatusOK, r.Code)
		assert.True(t, state.Success, r.Body.String())
	}
	// Use the exact Model(&User{}).Where(...).Update form used by subscriptions,
	// not User.UpdateWithTx. Its zero model ID must fence only the target UID.
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "default").Error)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "Boss").Error)
	r, rejected := bossRequest(t, BossAuthState, nil, sessions[0].Data.AccessToken)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.False(t, rejected.Success)
	r, unaffected := bossRequest(t, BossAuthState, nil, sessions[1].Data.AccessToken)
	assert.Equal(t, http.StatusOK, r.Code)
	assert.Equal(t, other.Id, unaffected.Data.User.ID)
	noMatch = f.db.Where("id = ? AND status = ?", other.Id, common.UserStatusDisabled).Delete(&model.User{})
	require.NoError(t, noMatch.Error)
	assert.Zero(t, noMatch.RowsAffected)
	r, unaffected = bossRequest(t, BossAuthState, nil, sessions[1].Data.AccessToken)
	assert.Equal(t, http.StatusOK, r.Code)
	require.NoError(t, f.db.Where("id = ?", other.Id).Delete(&model.User{}).Error)
	r, rejected = bossRequest(t, BossAuthState, nil, sessions[1].Data.AccessToken)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.False(t, rejected.Success)
}

func TestBossClosedProvisioningAndUniformChallenges(t *testing.T) {
	for _, kind := range []string{"missing", "unbound", "disabled", "wrong_group", "case_group", "ambiguous", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			f := setupBossPhone(t)
			phone := "+8613800101000"
			var user *model.User
			switch kind {
			case "unbound":
				user = bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
				require.NoError(t, f.db.Model(user).Update("username", phoneUsername(phone)).Error)
			case "disabled":
				user = bossTestUser(t, f.db, &phone, "Boss", common.UserStatusDisabled)
			case "wrong_group", "case_group":
				group := "default"
				if kind == "case_group" {
					group = "boss"
				}
				user = bossTestUser(t, f.db, &phone, group, common.UserStatusEnabled)
			case "ambiguous":
				user = bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				bare := strings.TrimPrefix(phone, "+")
				bossTestUser(t, f.db, &bare, "Boss", common.UserStatusEnabled)
			case "deleted":
				user = bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				require.NoError(t, f.db.Delete(user).Error)
			}
			var before int64
			require.NoError(t, f.db.Unscoped().Model(&model.User{}).Count(&before).Error)
			key := uuid.NewString()
			body := phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: key, ContractVersion: 2}
			first, challenge := bossRequest(t, BossPhoneChallenge, body)
			require.Equal(t, http.StatusAccepted, first.Code)
			assert.True(t, challenge.Success)
			assert.Zero(t, f.sends)
			assert.Equal(t, 60, challenge.Data.Resend)
			assert.Greater(t, challenge.Data.ExpiresAt, time.Now().Unix())
			assert.Contains(t, first.Header().Get("Cache-Control"), "no-store")
			var envelope struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(first.Body.Bytes(), &envelope))
			assert.Len(t, envelope.Data, 3, "eligibility and provider details must not leak")
			_, again := bossRequest(t, BossPhoneChallenge, body)
			assert.Equal(t, challenge.Data.ChallengeID, again.Data.ChallengeID)
			assert.Equal(t, challenge.Data.ExpiresAt, again.Data.ExpiresAt)
			denied, failed := bossRequest(t, BossPhoneLogin, phoneLoginRequest{PhoneE164: phone, ChallengeID: challenge.Data.ChallengeID, Code: "123456", IdempotencyKey: uuid.NewString(), ContractVersion: 2})
			assert.False(t, failed.Success)
			assert.Empty(t, failed.Data.AccessToken)
			assert.Empty(t, denied.Header().Values("Set-Cookie"))
			var after int64
			require.NoError(t, f.db.Unscoped().Model(&model.User{}).Count(&after).Error)
			assert.Equal(t, before, after, "Boss may never create or adopt a user")
			for _, record := range []any{&model.Token{}, &model.UserSession{}, &model.UserOnboardingSurvey{}} {
				var count int64
				require.NoError(t, f.db.Model(record).Count(&count).Error)
				assert.Zero(t, count)
			}
			if user != nil {
				var saved model.User
				require.NoError(t, f.db.Unscoped().First(&saved, user.Id).Error)
				assert.Equal(t, 431, saved.Quota, "neither global nor phone registration grants are allowed")
				assert.Equal(t, user.PhoneE164, saved.PhoneE164)
			}
		})
	}
}

func TestBossNoLaterAdoptionAndNoPublicBossDefault(t *testing.T) {
	f := setupBossPhone(t)
	phone := "+8613800101001"
	request := bossTestChallenge(t, phone) // Synthetic challenge for a nonexistent user.
	bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
	request.Code = "123456"
	r, result := bossRequest(t, BossPhoneLogin, request)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.False(t, result.Success)
	assert.Zero(t, f.sends)
	common.PhoneLoginDefaultGroup = "Boss"
	r, capabilities := bossRequest(t, BossCapabilities, nil)
	assert.Equal(t, http.StatusServiceUnavailable, r.Code)
	assert.False(t, capabilities.Success)
	other := "+8613800101002"
	_, _, err := findOrCreatePhoneUser(other)
	assert.ErrorIs(t, err, model.ErrBossPhoneUnqualified)
	unbound := bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
	require.NoError(t, f.db.Model(unbound).Update("username", phoneUsername(other)).Error)
	common.PhoneLoginDefaultGroup = "default"
	_, _, err = findOrCreatePhoneUser(other)
	assert.ErrorIs(t, err, model.ErrBossPhoneUnqualified, "ordinary phone login must not bind a reserved Boss shadow account")
}

func TestBossQueuedDispatchRejectsBindingABA(t *testing.T) {
	f := setupBossPhone(t)
	phone := "+8613800101093"
	user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
	var dispatch func()
	bossPhoneSchedule = func(action func()) { dispatch = action }
	request := bossTestChallenge(t, phone)
	require.NotNil(t, dispatch)
	assert.Zero(t, f.sends)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "default").Error)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "Boss").Error)
	dispatch()
	assert.Zero(t, f.sends, "eligibility at reservation cannot authorize a delayed SMS after an ABA mutation")
	request.Code = "123456"
	r, denied := bossRequest(t, BossPhoneLogin, request)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.False(t, denied.Success)
}

func TestBossDeliveryShapesScopeAndSharedRateBudget(t *testing.T) {
	for _, delivery := range []string{"accepted", "unknown", "not_sent"} {
		t.Run(delivery, func(t *testing.T) {
			f := setupBossPhone(t)
			phone := "+8613800101003"
			bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
			bossPhoneCodeSender = func(_ string, code string) error {
				f.sends++
				f.code = code
				if delivery == "unknown" {
					return common.ErrSmsDeliveryUnknown
				}
				if delivery == "not_sent" {
					return common.ErrSmsRejected
				}
				return nil
			}
			request := bossTestChallenge(t, phone)
			require.Equal(t, 1, f.sends)
			request.Code = f.code
			_, generic := phoneAuthRequest(t, PhoneLogin, request)
			assert.False(t, generic.Success, "generic exchange cannot consume a Boss proof")
			if delivery != "not_sent" {
				_, _, err := model.ReservePhoneChallenge(phone, uuid.NewString(), "654321", time.Now().Add(time.Minute))
				var limited *model.PhoneRateLimitError
				assert.ErrorAs(t, err, &limited, "another product must share the same per-phone budget")
			}
			r, result := bossRequest(t, BossPhoneLogin, request)
			if delivery == "not_sent" {
				assert.False(t, result.Success)
				return
			}
			require.True(t, result.Success, r.Body.String())
			assert.Equal(t, "boss_phone", result.Data.Session.LoginMethod)
			second, replay := bossRequest(t, BossPhoneLogin, request)
			assert.Equal(t, result.Data.AccessToken, replay.Data.AccessToken)
			assert.Equal(t, r.Header().Values("Set-Cookie"), second.Header().Values("Set-Cookie"))
			assert.Equal(t, 431, replay.Data.User.Quota)
			r, state := bossRequest(t, BossAuthState, nil, result.Data.AccessToken)
			assert.Equal(t, http.StatusOK, r.Code)
			assert.Equal(t, "Boss", state.Data.User.Group)
		})
	}
	t.Run("generic_to_boss", func(t *testing.T) {
		f := setupBossPhone(t)
		phone, key := "+8613800101004", uuid.NewString()
		bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		generic, created, err := model.ReservePhoneChallenge(phone, key, "654321", time.Now().Add(time.Minute))
		require.NoError(t, err)
		require.True(t, created)
		_, err = model.FinishPhoneChallenge(phone, generic.ID, "accepted")
		require.NoError(t, err)
		r, masked := bossRequest(t, BossPhoneChallenge, phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: key, ContractVersion: 2})
		assert.Equal(t, http.StatusAccepted, r.Code)
		assert.NotEqual(t, generic.ID, masked.Data.ChallengeID, "same client key has product-separated hashes")
		assert.Zero(t, f.sends)
		_, denied := bossRequest(t, BossPhoneLogin, phoneLoginRequest{PhoneE164: phone, Code: "654321", ChallengeID: generic.ID, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
		assert.False(t, denied.Success)
	})
}

func TestBossMaskedChallengesPreserveActualSmsBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accepted  int
		pending   string
		badLegacy bool
		wantSends int
	}{
		{name: "four_shared_sends_allow_one_more", accepted: 4, wantSends: 1},
		{name: "five_shared_sends_remain_limited", accepted: 5},
		{name: "unknown_dispatch_still_waits", pending: "unknown"},
		{name: "sending_dispatch_still_waits", pending: "sending"},
		{name: "unreadable_legacy_record_still_counts", accepted: 4, badLegacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupBossPhone(t)
			phone, now := "+8613800101088", time.Now()
			var history []model.AuthFlow
			// Both legacy accepted/masked and the new masked intent must leave
			// room for genuine SMS attempts without rewriting old challenge rows.
			for i := range 3 {
				request := phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString(), ContractVersion: 2}
				response, challenge := bossRequest(t, BossPhoneChallenge, request)
				require.Equal(t, http.StatusAccepted, response.Code)
				require.True(t, challenge.Success)
				require.Equal(t, 60, challenge.Data.Resend)
				var row model.AuthFlow
				require.NoError(t, f.db.Where("purpose = ?", "boss_phone_challenge").Order("id DESC").First(&row).Error)
				require.Equal(t, "masked", row.Intent)
				require.Zero(t, row.UserId)
				if i < 2 {
					updates := map[string]any{"intent": "accepted", "expires_at": now.Add(-time.Minute)}
					require.NoError(t, f.db.Model(&row).Updates(updates).Error)
				} else {
					require.NoError(t, f.db.Model(&row).Update("expires_at", now.Add(-time.Minute)).Error)
				}
				require.NoError(t, f.db.First(&row, row.Id).Error)
				history = append(history, row)
				_, replay := bossRequest(t, BossPhoneChallenge, request)
				assert.Equal(t, challenge.Data.ChallengeID, replay.Data.ChallengeID)
			}
			require.Zero(t, f.sends, "masked and replayed challenges must never dispatch SMS")
			bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
			for i := range tc.accepted {
				history = append(history, bossSmsBudgetHistory(t, f, phone, i%2 == 1, "accepted", now.Add(-time.Duration(i+2)*time.Minute)))
			}
			if tc.pending != "" {
				history = append(history, bossSmsBudgetHistory(t, f, phone, false, tc.pending, now.Add(-2*time.Minute)))
			}
			if tc.badLegacy {
				require.NoError(t, f.db.Model(&history[0]).Update("payload", "invalid historical payload").Error)
				require.NoError(t, f.db.First(&history[0], history[0].Id).Error)
			}
			request := phoneChallengeRequest{PhoneE164: phone, IdempotencyKey: uuid.NewString(), ContractVersion: 2}
			response, challenge := bossRequest(t, BossPhoneChallenge, request)
			require.Equal(t, http.StatusAccepted, response.Code)
			require.True(t, challenge.Success)
			assert.Equal(t, tc.wantSends, f.sends)
			_, replay := bossRequest(t, BossPhoneChallenge, request)
			assert.Equal(t, challenge.Data.ChallengeID, replay.Data.ChallengeID)
			assert.Equal(t, tc.wantSends, f.sends, "idempotent replay cannot dispatch another SMS")
			if tc.wantSends == 1 {
				_, _, err := model.ReservePhoneChallenge(phone, uuid.NewString(), "654321", now.Add(time.Minute))
				var limited *model.PhoneRateLimitError
				assert.ErrorAs(t, err, &limited, "ordinary phone login cannot add a sixth shared SMS")
			}
			for _, before := range history {
				var after model.AuthFlow
				require.NoError(t, f.db.First(&after, before.Id).Error)
				assert.Equal(t, before, after, "budget admission must not rewrite historical challenges")
			}
		})
	}
}

func bossSmsBudgetHistory(t *testing.T, f *bossPhoneFixture, phone string, boss bool, status string, createdAt time.Time) model.AuthFlow {
	t.Helper()
	var challenge *model.PhoneChallenge
	var created bool
	var err error
	purpose := "phone_challenge"
	if boss {
		purpose = "boss_phone_challenge"
		challenge, created, err = model.ReserveBossPhoneChallenge(phone, uuid.NewString(), "654321", time.Now().Add(10*time.Minute))
	} else {
		challenge, created, err = model.ReservePhoneChallenge(phone, uuid.NewString(), "654321", time.Now().Add(10*time.Minute))
	}
	require.NoError(t, err)
	require.True(t, created)
	if status != "sending" {
		if boss {
			require.NoError(t, model.DispatchBossPhoneChallenge(phone, challenge.ID, func() (string, int) { return status, 0 }))
		} else {
			_, err = model.FinishPhoneChallenge(phone, challenge.ID, status)
			require.NoError(t, err)
		}
	}
	var row model.AuthFlow
	require.NoError(t, f.db.Where("purpose = ?", purpose).Order("id DESC").First(&row).Error)
	updates := map[string]any{"created_at": createdAt}
	if status == "accepted" {
		// Expired or consumed accepted sends still consume the five-hour quota.
		updates["expires_at"] = time.Now().Add(-time.Minute)
		updates["consumed_at"] = time.Now().Add(-time.Minute)
	}
	require.NoError(t, f.db.Model(&row).Updates(updates).Error)
	require.NoError(t, f.db.First(&row, row.Id).Error)
	return row
}

func TestBossOTPAndMFADownstreamRollbackAndReplay(t *testing.T) {
	for _, mfa := range []bool{false, true} {
		for _, failure := range []string{"relay", "session", "seal", "receipt"} {
			t.Run(fmt.Sprintf("mfa_%t/%s", mfa, failure), func(t *testing.T) {
				f := setupBossPhone(t)
				phone := "+8613800101005"
				user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				factor := model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}
				if mfa {
					require.NoError(t, f.db.Create(&factor).Error)
				}
				request := bossTestChallenge(t, phone)
				request.Code = f.code
				handler, body := gin.HandlerFunc(BossPhoneLogin), any(request)
				var pending bossAuthResponse
				if mfa {
					_, pending = bossRequest(t, BossPhoneLogin, request)
					require.True(t, pending.Data.RequireVerification)
					require.Equal(t, []service.VerificationMethodOption{{Method: "totp", Available: true}}, pending.Data.Methods)
					_, generic := phoneAuthRequest(t, VerifyLogin, map[string]string{"flow_token": pending.Data.FlowToken, "code": "123456"})
					assert.False(t, generic.Success)
					code, err := totp.GenerateCode(factor.Secret, time.Now())
					require.NoError(t, err)
					handler, body = BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: code, IdempotencyKey: uuid.NewString(), ContractVersion: 2}
				}
				forced := errors.New("isolated Boss downstream failure")
				if failure == "seal" {
					bossPhoneLoginSealer = func(*service.PhoneLoginResult, string) (string, error) { return "", forced }
				} else if failure == "receipt" {
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("boss:test:failure", func(tx *gorm.DB) {
						if fields, ok := tx.Statement.Dest.(map[string]any); ok && fields["session_id"] != nil {
							tx.AddError(forced)
						}
					}))
				} else {
					target := reflect.TypeFor[model.Token]()
					if failure == "session" {
						target = reflect.TypeFor[model.UserSession]()
					}
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("boss:test:failure", func(tx *gorm.DB) {
						if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == target {
							tx.AddError(forced)
						}
					}))
				}
				r, failed := bossRequest(t, handler, body)
				assert.Equal(t, http.StatusServiceUnavailable, r.Code)
				assert.False(t, failed.Success)
				assert.Empty(t, r.Header().Values("Set-Cookie"))
				for _, record := range []any{&model.Token{}, &model.UserSession{}, &model.UserOnboardingSurvey{}} {
					var count int64
					require.NoError(t, f.db.Model(record).Count(&count).Error)
					assert.Zero(t, count)
				}
				if mfa {
					var saved model.TwoFA
					require.NoError(t, f.db.First(&saved, factor.Id).Error)
					assert.Nil(t, saved.LastUsedAt)
					var flow model.AuthFlow
					require.NoError(t, f.db.Where("purpose = ?", model.BossPhoneVerificationPurpose).First(&flow).Error)
					assert.Nil(t, flow.ConsumedAt)
				}
				if failure == "receipt" {
					require.NoError(t, f.db.Callback().Update().Remove("boss:test:failure"))
				} else if failure != "seal" {
					require.NoError(t, f.db.Callback().Create().Remove("boss:test:failure"))
				}
				bossPhoneLoginSealer = service.SealBossPhoneLoginResult
				r, success := bossRequest(t, handler, body)
				require.True(t, success.Success, r.Body.String())
				second, replay := bossRequest(t, handler, body)
				require.True(t, replay.Success, second.Body.String())
				assert.Equal(t, success.Data.AccessToken, replay.Data.AccessToken)
				assert.Equal(t, success.Data.Session.SID, replay.Data.Session.SID)
				assert.Equal(t, r.Header().Values("Set-Cookie"), second.Header().Values("Set-Cookie"))
				assert.Equal(t, user.Id, replay.Data.User.ID)
				assert.Equal(t, 431, replay.Data.User.Quota)
				var count int64
				require.NoError(t, f.db.Model(&model.UserSession{}).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				if mfa {
					_, old := bossRequest(t, BossPhoneLogin, request)
					assert.False(t, old.Success, "consumed SMS must not start a new MFA flow")
					other := body.(bossPhoneVerifyRequest)
					other.IdempotencyKey = uuid.NewString()
					_, used := bossRequest(t, handler, other)
					assert.False(t, used.Success)
				}
			})
		}
	}
}

func TestBossAttemptsAndUnsupportedFactorsCannotDowngrade(t *testing.T) {
	t.Run("otp_budget", func(t *testing.T) {
		f := setupBossPhone(t)
		phone := "+8613800101006"
		bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		request := bossTestChallenge(t, phone)
		correct := f.code
		request.Code = "000000"
		if request.Code == correct {
			request.Code = "999999"
		}
		_, first := bossRequest(t, BossPhoneLogin, request)
		assert.False(t, first.Success)
		_, replay := bossRequest(t, BossPhoneLogin, request)
		assert.False(t, replay.Success)
		for range 4 {
			request.IdempotencyKey = uuid.NewString()
			_, result := bossRequest(t, BossPhoneLogin, request)
			assert.False(t, result.Success)
		}
		request.Code, request.IdempotencyKey = correct, uuid.NewString()
		_, exhausted := bossRequest(t, BossPhoneLogin, request)
		assert.False(t, exhausted.Success)
	})
	t.Run("mfa_budget", func(t *testing.T) {
		f := setupBossPhone(t)
		phone := "+8613800101007"
		user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		factor := model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}
		require.NoError(t, f.db.Create(&factor).Error)
		request := bossTestChallenge(t, phone)
		request.Code = f.code
		_, pending := bossRequest(t, BossPhoneLogin, request)
		require.True(t, pending.Data.RequireVerification)
		wrong := "000000"
		for _, candidate := range []string{"000000", "111111", "222222", "333333"} {
			valid := false
			for _, offset := range []int{-1, 0, 1} {
				code, err := totp.GenerateCode(factor.Secret, time.Now().Add(time.Duration(offset)*30*time.Second))
				require.NoError(t, err)
				valid = valid || code == candidate
			}
			if !valid {
				wrong = candidate
				break
			}
		}
		body := bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: wrong, IdempotencyKey: uuid.NewString(), ContractVersion: 2}
		for range 2 {
			_, result := bossRequest(t, BossPhoneVerify, body)
			assert.False(t, result.Success)
		}
		var saved model.TwoFA
		require.NoError(t, f.db.First(&saved, factor.Id).Error)
		assert.Equal(t, 1, saved.FailedAttempts, "an immutable failed key spends the factor budget only once")
		body.Code, _ = totp.GenerateCode(factor.Secret, time.Now())
		r, conflict := bossRequest(t, BossPhoneVerify, body)
		assert.Equal(t, http.StatusConflict, r.Code)
		assert.False(t, conflict.Success)
		body.IdempotencyKey, body.Method = uuid.NewString(), "sms"
		r, unsupported := bossRequest(t, BossPhoneVerify, body)
		assert.Equal(t, http.StatusForbidden, r.Code)
		assert.False(t, unsupported.Success)
	})
	t.Run("passkey_only", func(t *testing.T) {
		f := setupBossPhone(t)
		phone := "+8613800101008"
		user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		require.NoError(t, f.db.Create(&model.PasskeyCredential{UserID: user.Id, CredentialID: "isolated-passkey-fixture", PublicKey: "not-a-real-key"}).Error)
		request := bossTestChallenge(t, phone)
		request.Code = f.code
		_, pending := bossRequest(t, BossPhoneLogin, request)
		require.True(t, pending.Data.RequireVerification)
		assert.Empty(t, pending.Data.AccessToken)
		require.Len(t, pending.Data.Methods, 1)
		assert.Equal(t, "passkey", pending.Data.Methods[0].Method)
		assert.False(t, pending.Data.Methods[0].Available)
		_, denied := bossRequest(t, BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: "123456", IdempotencyKey: uuid.NewString(), ContractVersion: 2})
		assert.False(t, denied.Success)
		_, err := service.RequireLoginVerification(pending.Data.FlowToken, service.VerificationMethodPasskey)
		assert.Error(t, err, "ordinary RP flow must not consume a Boss scope")
	})
}

func TestBossProofAndSessionBindingFencesSurviveABA(t *testing.T) {
	for _, stage := range []string{"otp", "mfa", "session"} {
		for _, change := range []string{"phone", "group", "status", "struct_status", "save_phone", "auth_version", "deleted_at", "delete_recreate", "hard_delete_same_id"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				f := setupBossPhone(t)
				phone := "+8613800101009"
				user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				if stage == "mfa" {
					require.NoError(t, f.db.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}).Error)
				}
				request := bossTestChallenge(t, phone)
				request.Code = f.code
				var established bossAuthResponse
				if stage != "otp" {
					r, result := bossRequest(t, BossPhoneLogin, request)
					require.True(t, result.Success, r.Body.String())
					established = result
				}
				switch change {
				case "phone":
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("phone_e164", nil).Error)
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("phone_e164", phone).Error)
				case "group":
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "default").Error)
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "Boss").Error)
				case "status":
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusEnabled).Error)
				case "struct_status":
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Select("status").Updates(struct{ Status int }{common.UserStatusDisabled}).Error)
					require.NoError(t, f.db.Model(user).Updates(model.User{Status: common.UserStatusEnabled}).Error)
				case "save_phone":
					changed := *user
					replacement := "+8613800101020"
					changed.PhoneE164 = &replacement
					require.NoError(t, f.db.Save(&changed).Error)
					changed.PhoneE164 = &phone
					require.NoError(t, f.db.Save(&changed).Error)
				case "auth_version":
					require.NoError(t, f.db.Model(user).Update("auth_version", 2).Error)
					require.NoError(t, f.db.Model(user).Update("auth_version", 1).Error)
				case "deleted_at":
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("deleted_at", time.Now()).Error)
					require.NoError(t, f.db.Unscoped().Model(&model.User{}).Where("id = ?", user.Id).Update("deleted_at", nil).Error)
				case "delete_recreate":
					require.NoError(t, f.db.Delete(user).Error)
					require.NoError(t, f.db.Unscoped().Model(&model.User{}).Where("id = ?", user.Id).Update("phone_e164", nil).Error)
					bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				case "hard_delete_same_id":
					require.NoError(t, f.db.Unscoped().Delete(user).Error)
					replacement := *user
					replacement.AuthVersion, replacement.DeletedAt = 1, gorm.DeletedAt{}
					require.NoError(t, f.db.Create(&replacement).Error)
				}
				r, rejected := bossRequest(t, BossPhoneLogin, request)
				assert.False(t, rejected.Success, "a current string match must not revive an earlier proof")
				assert.Empty(t, r.Header().Values("Set-Cookie"))
				if stage == "mfa" {
					code, err := totp.GenerateCode("JBSWY3DPEHPK3PXP", time.Now())
					require.NoError(t, err)
					_, rejected := bossRequest(t, BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: established.Data.FlowToken, Method: "totp", Code: code, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
					assert.False(t, rejected.Success)
				}
				if stage == "session" {
					r, state := bossRequest(t, BossAuthState, nil, established.Data.AccessToken)
					assert.Equal(t, http.StatusUnauthorized, r.Code)
					assert.False(t, state.Success)
				}
			})
		}
	}
	t.Run("mutation_rollback", func(t *testing.T) {
		f := setupBossPhone(t)
		phone := "+8613800101010"
		user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		request := bossTestChallenge(t, phone)
		request.Code = f.code
		_, session := bossRequest(t, BossPhoneLogin, request)
		require.True(t, session.Success)
		forced := errors.New("rollback account change")
		err := f.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "default").Error; err != nil {
				return err
			}
			return forced
		})
		require.ErrorIs(t, err, forced)
		r, state := bossRequest(t, BossAuthState, nil, session.Data.AccessToken)
		assert.Equal(t, http.StatusOK, r.Code)
		assert.True(t, state.Success, "proof expiry/session revocation must roll back with the account change")
	})
}

func TestBossConcurrentOTPAndMFAExchangeRollback(t *testing.T) {
	for _, mfa := range []bool{false, true} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("mfa_%t/rollback_%t", mfa, rollback), func(t *testing.T) {
				f := setupBossPhone(t)
				sqlDB, err := f.db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(8)
				phone := "+8613800101011"
				user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
				if mfa {
					require.NoError(t, f.db.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}).Error)
				}
				request := bossTestChallenge(t, phone)
				request.Code = f.code
				handler, body, purpose := gin.HandlerFunc(BossPhoneLogin), any(request), "boss_phone_exchange"
				if mfa {
					_, pending := bossRequest(t, BossPhoneLogin, request)
					require.True(t, pending.Data.RequireVerification)
					code, err := totp.GenerateCode("JBSWY3DPEHPK3PXP", time.Now())
					require.NoError(t, err)
					handler, body, purpose = BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: code, IdempotencyKey: uuid.NewString(), ContractVersion: 2}, "boss_phone_mfa_exchange"
				}
				atIssue, releaseFirst, secondAtBinding := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var bindings atomic.Int32
				var issued atomic.Bool
				var release sync.Once
				defer release.Do(func() { close(releaseFirst) })
				require.NoError(t, f.db.Callback().Create().Before("gorm:begin_transaction").Register("boss:test:overlap", func(tx *gorm.DB) {
					if tx.Statement.Schema == nil {
						return
					}
					if flow, ok := tx.Statement.Dest.(*model.AuthFlow); ok && flow.Purpose == purpose && bindings.Add(1) == 2 {
						close(secondAtBinding)
					}
					if tx.Statement.Schema.ModelType == reflect.TypeFor[model.UserSession]() && issued.CompareAndSwap(false, true) {
						close(atIssue)
						<-releaseFirst
						if rollback {
							tx.AddError(errors.New("first issuance rollback"))
						}
					}
				}))
				defer func() { require.NoError(t, f.db.Callback().Create().Remove("boss:test:overlap")) }()
				var responses [2]bossAuthResponse
				var workers sync.WaitGroup
				workers.Go(func() { _, responses[0] = bossRequest(t, handler, body) })
				select {
				case <-atIssue:
				case <-time.After(10 * time.Second):
					t.Fatal("first exchange did not reach issuance")
				}
				workers.Go(func() { _, responses[1] = bossRequest(t, handler, body) })
				select {
				case <-secondAtBinding:
				case <-time.After(10 * time.Second):
					t.Fatal("second request did not overlap the transaction")
				}
				release.Do(func() { close(releaseFirst) })
				workers.Wait()
				require.True(t, responses[1].Success)
				if rollback {
					assert.False(t, responses[0].Success)
				} else {
					assert.True(t, responses[0].Success)
					assert.Equal(t, responses[0].Data.AccessToken, responses[1].Data.AccessToken)
				}
				_, replay := bossRequest(t, handler, body)
				assert.Equal(t, responses[1].Data.Session.SID, replay.Data.Session.SID)
				var count int64
				require.NoError(t, f.db.Model(&model.UserSession{}).Count(&count).Error)
				assert.EqualValues(t, 1, count)
				var saved model.User
				require.NoError(t, f.db.First(&saved, user.Id).Error)
				assert.Equal(t, 431, saved.Quota)
			})
		}
	}
}

func TestBossStateRejectsOtherCredentialsAndStaleCaches(t *testing.T) {
	for _, change := range []string{"session", "scope_fence", "group"} {
		t.Run(change, func(t *testing.T) {
			f := setupBossPhone(t)
			phone := "+8613800101012"
			user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
			request := bossTestChallenge(t, phone)
			request.Code = f.code
			_, login := bossRequest(t, BossPhoneLogin, request)
			require.True(t, login.Success)
			server := miniredis.RunT(t)
			oldRDB := common.RDB
			common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			common.RedisEnabled = true
			t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = oldRDB })
			_, err := model.GetUserCache(user.Id)
			require.NoError(t, err)
			_, err = model.GetUserSessionCached(login.Data.Session.SID)
			require.NoError(t, err)
			switch change {
			case "session":
				require.NoError(t, f.db.Model(&model.UserSession{}).Where("sid = ?", login.Data.Session.SID).Update("status", model.UserSessionStatusRevoked).Error)
			case "scope_fence":
				require.NoError(t, f.db.Model(&model.AuthFlow{}).Where("purpose = ?", "boss_phone_session").Update("expires_at", time.Now().Add(-time.Second)).Error)
			case "group":
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "default").Error)
			}
			cached, err := model.GetUserSessionCached(login.Data.Session.SID)
			require.NoError(t, err)
			assert.Equal(t, model.UserSessionStatusActive, cached.Status, "fixture deliberately retains a stale active cache")
			r, denied := bossRequest(t, BossAuthState, nil, login.Data.AccessToken)
			assert.Equal(t, http.StatusUnauthorized, r.Code)
			assert.False(t, denied.Success)
		})
	}
	t.Run("token_types_and_scope", func(t *testing.T) {
		f := setupBossPhone(t)
		phone := "+8613800101013"
		user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
		request := bossTestChallenge(t, phone)
		request.Code = f.code
		_, login := bossRequest(t, BossPhoneLogin, request)
		require.True(t, login.Success)
		generic, err := service.CreateLoginSession(user.Id, "phone", "127.0.0.1", "test")
		require.NoError(t, err)
		pat, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: "pat:1", UserAuthVersion: 1, SessionVersion: 1})
		require.NoError(t, err)
		identity, err := service.ParseAccessToken(login.Data.AccessToken)
		require.NoError(t, err)
		proof, _, err := service.IssueSecurityProof(identity, "totp", service.VerificationBinding{Scope: "account.delete", ContextHash: "test-context"})
		require.NoError(t, err)
		ambiguous := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(ambiguous)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/boss/auth/state", nil)
		c.Request.Header.Add("Authorization", "Bearer "+login.Data.AccessToken)
		c.Request.Header.Add("Authorization", "Bearer "+generic.AccessToken)
		BossAuthState(c)
		assert.Equal(t, http.StatusUnauthorized, ambiguous.Code, "multiple Authorization headers must not select an arbitrary credential")
		for _, credential := range []string{login.Data.RelayKey, model.AccessTokenPrefix + strings.Repeat("a", 43), generic.AccessToken, pat, proof} {
			r, denied := bossRequest(t, BossAuthState, nil, credential)
			assert.Equal(t, http.StatusUnauthorized, r.Code)
			assert.False(t, denied.Success)
		}
	})
}

func bossRelayRequest(t *testing.T, key string, readOnly bool, sids ...string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	auth := middleware.TokenAuth()
	if readOnly {
		auth = middleware.TokenAuthReadOnly()
	}
	router.POST("/v1/test", auth, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	for _, sid := range sids {
		request.Header.Add("X-Auth-Session", sid)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestBossSingleClientLoginReplacesOnlyAfterSuccessfulMFA(t *testing.T) {
	f := setupBossPhone(t)
	require.NoError(t, i18n.Init())
	phone := "+8613800101021"
	user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
	request := bossTestChallenge(t, phone)
	request.Code = f.code
	firstResponse, first := bossRequest(t, BossPhoneLogin, request)
	require.True(t, first.Success)
	var oldRefresh string
	for _, cookie := range firstResponse.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName {
			oldRefresh = cookie.Value
		}
	}
	require.NotEmpty(t, oldRefresh)
	identity, err := service.ParseAccessToken(first.Data.AccessToken)
	require.NoError(t, err)
	originalToken, err := model.GetTokenByKey(strings.TrimPrefix(first.Data.RelayKey, "sk-"), false)
	require.NoError(t, err)

	server := miniredis.RunT(t)
	previousRDB := common.RDB
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = previousRDB })
	_, err = model.GetUserCache(user.Id)
	require.NoError(t, err)
	_, err = model.GetUserSessionCached(first.Data.Session.SID)
	require.NoError(t, err)
	factor := model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "JBSWY3DPEHPK3PXP"}
	require.NoError(t, f.db.Create(&factor).Error)
	// Advance only this fixture's completed SMS cooldown, not production policy.
	require.NoError(t, f.db.Model(&model.AuthFlow{}).Where("purpose = ? AND user_id = ?", "boss_phone_challenge", user.Id).Update("created_at", time.Now().Add(-2*time.Minute)).Error)
	next := bossTestChallenge(t, phone)
	next.Code = f.code
	wrong := next
	wrong.Code = "000000"
	if wrong.Code == next.Code {
		wrong.Code = "111111"
	}
	wrong.IdempotencyKey = uuid.NewString()
	_, failedOTP := bossRequest(t, BossPhoneLogin, wrong)
	assert.False(t, failedOTP.Success)
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err, "failed OTP must not replace the signed-in client")
	_, pending := bossRequest(t, BossPhoneLogin, next)
	require.True(t, pending.Data.RequireVerification)
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err, "an unfinished MFA flow must not replace the signed-in client")
	code, err := totp.GenerateCode(factor.Secret, time.Now())
	require.NoError(t, err)
	badCode := "000000"
	if badCode == code {
		badCode = "111111"
	}
	_, failedMFA := bossRequest(t, BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: badCode, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
	assert.False(t, failedMFA.Success)
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err, "failed MFA must preserve the current client")
	originalSealer := bossPhoneLoginSealer
	bossPhoneLoginSealer = func(*service.PhoneLoginResult, string) (string, error) {
		return "", errors.New("single-client fixture rollback after session creation")
	}
	_, rolledBack := bossRequest(t, BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: code, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
	bossPhoneLoginSealer = originalSealer
	assert.False(t, rolledBack.Success)
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err, "downstream receipt failure must roll back the new SID and the old SID revocation")
	common.UserSessionActiveLimit = 1
	secondResponse, second := bossRequest(t, BossPhoneVerify, bossPhoneVerifyRequest{FlowToken: pending.Data.FlowToken, Method: "totp", Code: code, IdempotencyKey: uuid.NewString(), ContractVersion: 2})
	require.True(t, second.Success)
	assert.NotEqual(t, first.Data.Session.SID, second.Data.Session.SID)
	assert.Equal(t, first.Data.RelayKey, second.Data.RelayKey, "the existing account relay key is preserved")
	active, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, active)
	stale, err := model.GetUserSessionCached(first.Data.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusActive, stale.Status, "fixture retains the old active cache intentionally")
	_, _, err = service.ValidateLoginSession(identity)
	assert.ErrorIs(t, err, service.ErrLoginSessionRevoked)
	stateResponse, oldState := bossRequest(t, BossAuthState, nil, first.Data.AccessToken)
	assert.Equal(t, http.StatusUnauthorized, stateResponse.Code)
	assert.False(t, oldState.Success)
	_, _, err = service.RefreshLoginSession(oldRefresh, first.Data.Session.SID, "", "")
	assert.ErrorIs(t, err, service.ErrLoginSessionRevoked)
	stateResponse, newState := bossRequest(t, BossAuthState, nil, second.Data.AccessToken)
	assert.Equal(t, http.StatusOK, stateResponse.Code)
	assert.True(t, newState.Success)
	self := gin.New()
	self.GET("/api/user/self", middleware.UserAuth(), GetSelf)
	for _, item := range []struct {
		token  string
		status int
	}{{first.Data.AccessToken, http.StatusUnauthorized}, {second.Data.AccessToken, http.StatusOK}} {
		request := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
		request.Header.Set("Authorization", "Bearer "+item.token)
		response := httptest.NewRecorder()
		self.ServeHTTP(response, request)
		assert.Equal(t, item.status, response.Code)
		if item.status == http.StatusOK {
			var body struct {
				Data struct {
					Role     int    `json:"role"`
					Group    string `json:"group"`
					Username string `json:"username"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, user.Role, body.Data.Role)
			assert.Equal(t, user.Username, body.Data.Username)
			assert.Equal(t, model.BossPhoneGroup, body.Data.Group)
		}
	}
	for _, readOnly := range []bool{false, true} {
		assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, first.Data.RelayKey, readOnly, first.Data.Session.SID).Code)
		assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, first.Data.RelayKey, readOnly).Code)
		assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, first.Data.RelayKey, readOnly, second.Data.Session.SID, second.Data.Session.SID).Code)
		// The normal account default group is empty on its persistent relay key.
		assert.Equal(t, http.StatusNoContent, bossRelayRequest(t, first.Data.RelayKey, readOnly, second.Data.Session.SID).Code)
	}
	for _, cookie := range secondResponse.Result().Cookies() {
		if cookie.Name == service.RefreshCookieName {
			refreshed, _, refreshErr := service.RefreshLoginSession(cookie.Value, second.Data.Session.SID, "", "")
			require.NoError(t, refreshErr)
			assert.Equal(t, second.Data.Session.SID, refreshed.Session.SID, "refresh preserves the same active client")
		}
	}
	var unchanged model.User
	require.NoError(t, f.db.First(&unchanged, user.Id).Error)
	assert.Equal(t, user.AuthVersion, unchanged.AuthVersion)
	assert.Equal(t, user.Quota, unchanged.Quota)
	assert.Equal(t, user.UsedQuota, unchanged.UsedQuota)
	currentToken, err := model.GetTokenByKey(strings.TrimPrefix(first.Data.RelayKey, "sk-"), false)
	require.NoError(t, err)
	assert.Equal(t, originalToken.Id, currentToken.Id)
	assert.Equal(t, originalToken.Key, currentToken.Key)
}

func TestBossSingleClientConcurrentNativeLoginsAndProductCompatibility(t *testing.T) {
	f := setupBossPhone(t)
	phone := "+8613800101022"
	user := bossTestUser(t, f.db, &phone, "Boss", common.UserStatusEnabled)
	first := bossTestChallenge(t, phone)
	first.Code = f.code
	require.NoError(t, f.db.Model(&model.AuthFlow{}).Where("purpose = ? AND user_id = ?", "boss_phone_challenge", user.Id).Update("created_at", time.Now().Add(-2*time.Minute)).Error)
	second := bossTestChallenge(t, phone)
	second.Code = f.code
	requests := []phoneLoginRequest{first, second}
	type outcome struct {
		status int
		result bossAuthResponse
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, input := range requests {
		encoded, err := common.Marshal(input)
		require.NoError(t, err)
		go func() {
			<-start
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/boss/phone/login", strings.NewReader(string(encoded)))
			c.Request.Header.Set("Content-Type", "application/json")
			BossPhoneLogin(c)
			var response bossAuthResponse
			err := common.Unmarshal(recorder.Body.Bytes(), &response)
			results <- outcome{status: recorder.Code, result: response, err: err}
		}()
	}
	close(start)
	observed := make([]outcome, 0, 2)
	for range 2 {
		observed = append(observed, <-results)
	}
	var issued []bossAuthResponse
	for _, result := range observed {
		require.NoError(t, result.err)
		require.Contains(t, []int{http.StatusOK, http.StatusUnauthorized}, result.status)
		t.Logf("BOSS_SINGLE_NATIVE_OUTCOME status=%d success=%t code=%s", result.status, result.result.Success, result.result.Error.Code)
		if result.result.Success {
			issued = append(issued, result.result)
		}
	}
	require.NotEmpty(t, issued)
	var sessions, tokens int64
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("user_id = ?", user.Id).Count(&sessions).Error)
	require.NoError(t, f.db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokens).Error)
	assert.EqualValues(t, 1, sessions, "the provider-accepted newer challenge invalidates the predecessor under the existing OTP policy")
	assert.EqualValues(t, 1, tokens)
	active, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, active)
	valid := 0
	for _, result := range issued {
		_, _, err := service.BossPhoneAuthState(result.Data.AccessToken)
		if err == nil {
			valid++
		}
	}
	assert.Equal(t, 1, valid)
	direct := bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
	type loginOutcome struct {
		bundle *service.AuthBundle
		err    error
	}
	directStart := make(chan struct{})
	directResults := make(chan loginOutcome, 2)
	for range 2 {
		go func() {
			<-directStart
			bundle, err := service.CreateLoginSession(direct.Id, "password", "", "")
			directResults <- loginOutcome{bundle, err}
		}()
	}
	close(directStart)
	directOutcomes := []loginOutcome{<-directResults, <-directResults}
	valid = 0
	for _, result := range directOutcomes {
		require.NoError(t, result.err)
		require.NotNil(t, result.bundle)
		identity, err := service.ParseAccessToken(result.bundle.AccessToken)
		require.NoError(t, err)
		_, _, err = service.ValidateLoginSession(identity)
		if err == nil {
			valid++
		} else {
			assert.ErrorIs(t, err, service.ErrLoginSessionRevoked)
		}
	}
	assert.Equal(t, 1, valid)
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("user_id = ?", direct.Id).Count(&sessions).Error)
	assert.EqualValues(t, 2, sessions, "both independent successful login ceremonies commit serially")
	active, err = model.CountActiveUserSessions(direct.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, active)
	ordinary := bossTestUser(t, f.db, nil, "product", common.UserStatusEnabled)
	for range 2 {
		_, err := service.CreateLoginSession(ordinary.Id, "phone", "", "")
		require.NoError(t, err)
	}
	active, err = model.CountActiveUserSessions(ordinary.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 2, active, "ordinary product clients retain multiple sessions")
	limited := bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
	common.UserSessionIssuanceLimit = 1
	budgetStart := make(chan struct{})
	budgetResults := make(chan error, 2)
	for range 2 {
		go func() {
			<-budgetStart
			_, err := service.CreateLoginSession(limited.Id, "phone", "", "")
			budgetResults <- err
		}()
	}
	close(budgetStart)
	budgetErrors := []error{<-budgetResults, <-budgetResults}
	succeeded := 0
	for _, err := range budgetErrors {
		if err == nil {
			succeeded++
		} else {
			assert.ErrorIs(t, err, model.ErrUserSessionIssuanceLimit)
		}
	}
	assert.Equal(t, 1, succeeded, "Boss replacement cannot bypass the issuance budget")
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("user_id = ?", limited.Id).Count(&sessions).Error)
	assert.EqualValues(t, 1, sessions)
}

func TestBossSingleClientRelayRejectsGroupOverrideAndForgedSID(t *testing.T) {
	f := setupBossPhone(t)
	require.NoError(t, i18n.Init())
	originalGroups := ratio_setting.GroupRatio2JSONString()
	originalUsable := setting.UserUsableGroups2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"Boss":1,"product":1,"default":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"Boss":"Boss","product":"Product","default":"Default"}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
	})
	boss := bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
	other := bossTestUser(t, f.db, nil, "Boss", common.UserStatusEnabled)
	ordinary := bossTestUser(t, f.db, nil, "product", common.UserStatusEnabled)
	current, err := service.CreateLoginSession(boss.Id, "phone", "", "")
	require.NoError(t, err)
	foreign, err := service.CreateLoginSession(other.Id, "phone", "", "")
	require.NoError(t, err)
	bossKey, ordinaryKey := strings.Repeat("b", 48), strings.Repeat("c", 48)
	for _, token := range []*model.Token{
		{UserId: boss.Id, Name: "boss-product-override", Key: bossKey, Group: "product", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
		{UserId: ordinary.Id, Name: "ordinary-product", Key: ordinaryKey, Group: "product", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
	} {
		require.NoError(t, f.db.Create(token).Error)
	}
	for _, readOnly := range []bool{false, true} {
		assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, "sk-"+bossKey, readOnly).Code)
		assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, "sk-"+bossKey, readOnly, foreign.Session.SID).Code)
		assert.Equal(t, http.StatusNoContent, bossRelayRequest(t, "sk-"+bossKey, readOnly, current.Session.SID).Code)
		assert.Equal(t, http.StatusNoContent, bossRelayRequest(t, "sk-"+ordinaryKey, readOnly).Code)
	}
	self := gin.New()
	self.GET("/api/user/self", middleware.UserAuth(), GetSelf)
	for _, item := range []struct {
		user   *model.User
		suffix string
		status int
	}{{boss, "d", http.StatusUnauthorized}, {ordinary, "e", http.StatusOK}} {
		raw := model.AccessTokenPrefix + strings.Repeat(item.suffix, 43)
		pat := &model.UserAccessToken{UserId: item.user.Id, Name: "single-session-fixture", TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw), ExpiresAt: time.Now().Add(time.Hour).Unix()}
		require.NoError(t, pat.SetScopes([]string{"profile:read"}))
		require.NoError(t, f.db.Create(pat).Error)
		request := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
		request.Header.Set("Authorization", "Bearer "+raw)
		request.Header.Set("X-Auth-Session", current.Session.SID)
		response := httptest.NewRecorder()
		self.ServeHTTP(response, request)
		assert.Equal(t, item.status, response.Code, "Boss PAT cannot replace native session authentication; ordinary PAT remains compatible")
	}
	server := miniredis.RunT(t)
	previousRDB := common.RDB
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = previousRDB })
	_, err = model.GetUserCache(ordinary.Id)
	require.NoError(t, err)
	// Deliberately bypass cache hooks in the fixture: classification still reads
	// the actual account group, rather than the warm ordinary-group cache.
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", ordinary.Id).UpdateColumn("group", model.BossPhoneGroup).Error)
	assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, "sk-"+ordinaryKey, false).Code)
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("sid = ?", current.Session.SID).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
	assert.Equal(t, http.StatusUnauthorized, bossRelayRequest(t, "sk-"+bossKey, false, current.Session.SID).Code)
}
