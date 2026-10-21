package controller

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Phone sign-in mirrors the password login contract: the caller receives the
// same access_token/session bundle, so clients only swap the login call. The
// phone number is the account identity; the account is created on the first
// successful verification.

var phoneE164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

type phoneChallengeRequest struct {
	PhoneE164       string `json:"phone_e164"`
	IdempotencyKey  string `json:"idempotency_key"`
	ContractVersion int    `json:"contract_version"`
}

type phoneLoginRequest struct {
	PhoneE164       string `json:"phone_e164"`
	Code            string `json:"code"`
	ChallengeID     string `json:"challenge_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ContractVersion int    `json:"contract_version"`
}

// UUIDs and random base64url/hex values can carry at least 128 bits. Entropy
// comes from the client CSPRNG; short counters and empty legacy keys are refused.
var phoneIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22,128}$`)
var phoneOTPPattern = regexp.MustCompile(`^[0-9]{6}$`)
var phoneCodeSender = deliverPhoneCode
var phoneLoginSealer = service.SealPhoneLoginResult

// phoneUsername keeps the shadow-account naming that existing tooling expects
// (ys + digits) so a phone maps to exactly one New API user.
func phoneUsername(phoneE164 string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phoneE164)
	return "ys" + digits
}

func phoneLookupCandidates(phoneE164 string) []string {
	digits := strings.TrimPrefix(phoneE164, "+")
	// Accept the canonical form and the bare national/international digits so
	// callers may send either.
	if digits == phoneE164 {
		return []string{phoneE164}
	}
	return []string{phoneE164, digits}
}

func deliverPhoneCode(phoneE164 string, code string) error {
	if common.PhoneSmsBypassAllows(phoneE164) {
		return nil
	}
	return common.SendSmsCode(phoneE164, code)
}

func phoneAuthError(c *gin.Context, status int, code, message string, retry int, data any) {
	setAuthNoStore(c)
	if retry > 0 {
		c.Header("Retry-After", strconv.Itoa(retry))
	}
	fields := gin.H{"code": code, "retry_after_seconds": retry}
	if data != nil {
		fields["data"] = data
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), fields)
}

func phoneChallengeData(challenge *model.PhoneChallenge) gin.H {
	return gin.H{"challenge_id": challenge.ID, "delivery_status": challenge.DeliveryStatus, "expires_at": challenge.ExpiresAt,
		"resend_after_seconds": max(0, int(challenge.ResendAt-time.Now().Unix())), "expires_in_minutes": common.VerificationValidMinutes}
}

// SendPhoneLoginCode issues a verification code for phone sign-in.
func SendPhoneLoginCode(c *gin.Context) {
	setAuthNoStore(c)
	if !common.PhoneLoginEnabled {
		phoneAuthError(c, http.StatusForbidden, "PHONE_LOGIN_DISABLED", "Phone sign-in has been disabled by administrator", 0, nil)
		return
	}
	var request phoneChallengeRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if common.DecodeJson(c.Request.Body, &request) != nil {
		phoneAuthError(c, http.StatusBadRequest, "PHONE_INVALID_PARAMETERS", "Invalid parameters", 0, nil)
		return
	}
	phone := strings.TrimSpace(request.PhoneE164)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	legacy := request.IdempotencyKey == "" && (request.ContractVersion == 0 || request.ContractVersion == 1)
	if !phoneE164Pattern.MatchString(phone) || !legacy && (!phoneIdempotencyKeyPattern.MatchString(request.IdempotencyKey) || request.ContractVersion != 0 && request.ContractVersion != 2) {
		phoneAuthError(c, http.StatusBadRequest, "PHONE_INVALID_PARAMETERS", "Invalid parameters", 0, nil)
		return
	}
	if legacy {
		key, err := common.GenerateRandomCharsKey(32)
		if err != nil {
			phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_CHALLENGE_UNAVAILABLE", "Verification code could not be sent, please try again later", 0, nil)
			return
		}
		request.IdempotencyKey = key
	}
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_CHALLENGE_UNAVAILABLE", "Verification code could not be sent, please try again later", 0, nil)
		return
	}
	if common.PhoneSmsBypassAllows(phone) {
		code = common.PhoneSmsDevCode
	}
	challenge, created, err := model.ReservePhoneChallenge(phone, request.IdempotencyKey, code, time.Now().Add(time.Duration(common.VerificationValidMinutes)*time.Minute))
	if err != nil {
		var limited *model.PhoneRateLimitError
		switch {
		case errors.Is(err, model.ErrPhoneIdempotencyConflict):
			phoneAuthError(c, http.StatusConflict, "PHONE_IDEMPOTENCY_CONFLICT", "Invalid parameters", 0, nil)
		case errors.As(err, &limited):
			phoneAuthError(c, http.StatusTooManyRequests, "PHONE_RATE_LIMITED", "Too many verification codes requested for this number, please try again later", limited.RetryAfterSeconds, nil)
		default:
			phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_CHALLENGE_UNAVAILABLE", "Verification code could not be sent, please try again later", 0, nil)
		}
		return
	}
	if created {
		delivery := "accepted"
		providerRetry := 0
		if err := phoneCodeSender(phone, code); err != nil {
			delivery = "unknown"
			if errors.Is(err, common.ErrSmsNotConfigured) || errors.Is(err, common.ErrSmsRequest) || errors.Is(err, common.ErrSmsRejected) || errors.Is(err, common.ErrSmsThrottled) || errors.Is(err, common.ErrSmsUnavailable) {
				delivery = "not_sent"
			}
			if errors.Is(err, common.ErrSmsThrottled) {
				providerRetry = 60
			}
		}
		finished, err := model.FinishPhoneChallenge(phone, challenge.ID, delivery, providerRetry)
		if err == nil {
			challenge = finished
		} else {
			// The durable sending reservation still protects an accepted but unrecorded
			// delivery. Its code remains usable; never dispatch to repair a DB error.
			challenge.DeliveryStatus = "unknown"
		}
	}
	data := phoneChallengeData(challenge)
	switch challenge.DeliveryStatus {
	case "accepted":
		if common.PhoneSmsBypassAllows(phone) {
			data["dev_code"] = common.PhoneSmsDevCode
		}
		common.ApiSuccess(c, data)
	case "not_sent":
		if challenge.FailureCode == "PHONE_RATE_LIMITED" {
			phoneAuthError(c, http.StatusTooManyRequests, "PHONE_RATE_LIMITED", "Too many verification codes requested for this number, please try again later", data["resend_after_seconds"].(int), data)
		} else {
			phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_CHALLENGE_UNAVAILABLE", "Verification code could not be sent, please try again later", 0, data)
		}
	case "unknown":
		phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_DELIVERY_UNKNOWN", "Verification code delivery is uncertain, please check for the message before trying again", data["resend_after_seconds"].(int), data)
	default:
		phoneAuthError(c, http.StatusConflict, "PHONE_CHALLENGE_PENDING", "Verification code request is still being processed", max(1, min(15, data["resend_after_seconds"].(int))), data)
	}
}

// Version 2 requires a distinct stable exchange key and durable challenge.
// Legacy callers still use the durable guard and transaction, but cannot replay
// a successful response using only an OTP after its challenge was consumed.
func PhoneLogin(c *gin.Context) {
	setAuthNoStore(c)
	if !common.PhoneLoginEnabled {
		phoneAuthError(c, http.StatusForbidden, "PHONE_LOGIN_DISABLED", "Phone sign-in has been disabled by administrator", 0, nil)
		return
	}
	var request phoneLoginRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if common.DecodeJson(c.Request.Body, &request) != nil {
		phoneAuthError(c, http.StatusBadRequest, "PHONE_INVALID_PARAMETERS", "Invalid parameters", 0, nil)
		return
	}
	phone := strings.TrimSpace(request.PhoneE164)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	request.Code = strings.TrimSpace(request.Code)
	legacy := request.ChallengeID == "" && request.IdempotencyKey == "" && (request.ContractVersion == 0 || request.ContractVersion == 1)
	if !phoneE164Pattern.MatchString(phone) || !phoneOTPPattern.MatchString(request.Code) || !legacy && (len(request.ChallengeID) != 64 || !phoneIdempotencyKeyPattern.MatchString(request.IdempotencyKey) || request.ContractVersion != 0 && request.ContractVersion != 2) {
		phoneAuthError(c, http.StatusBadRequest, "PHONE_INVALID_PARAMETERS", "Invalid parameters", 0, nil)
		return
	}
	if legacy {
		id, err := model.LegacyPhoneChallenge(phone)
		if err != nil {
			if errors.Is(err, model.ErrPhoneChallengeConsumed) {
				phoneAuthError(c, http.StatusConflict, "PHONE_CHALLENGE_CONSUMED", "Verification code has already been used, please request a new code", 0, nil)
			} else {
				phoneAuthError(c, http.StatusUnauthorized, "PHONE_CODE_INVALID_OR_EXPIRED", "Verification code is incorrect or has expired", 0, nil)
			}
			return
		}
		request.ChallengeID = id
		key, err := common.GenerateRandomCharsKey(32)
		if err != nil {
			phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_LOGIN_UNAVAILABLE", "Sign-in could not be completed, please try again", 1, nil)
			return
		}
		request.IdempotencyKey = key
	}
	var result *service.PhoneLoginResult
	var createdUser *model.User
	_, err := model.ExchangePhoneChallenge(phone, request.ChallengeID, request.IdempotencyKey, request.Code,
		func(tx *gorm.DB) (string, int, string, error) {
			user, created, err := findOrCreatePhoneUserWithTx(tx, phone)
			if err != nil {
				return "", 0, "", err
			}
			if created {
				createdUser = user
			}
			result, err = service.PreparePhoneLoginWithTx(tx, user, c.ClientIP(), c.Request.UserAgent())
			if err != nil {
				return "", 0, "", err
			}
			sealed, err := phoneLoginSealer(result, request.IdempotencyKey)
			if err != nil {
				return "", 0, "", err
			}
			sid := ""
			if result.Bundle != nil {
				sid = result.Bundle.Session.SID
			}
			return sealed, user.Id, sid, nil
		}, func(tx *gorm.DB, flow *model.AuthFlow, sealed string) error {
			var err error
			result, err = service.ReplayPhoneLoginResultWithTx(tx, flow, sealed, request.IdempotencyKey)
			return err
		})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrPhoneIdempotencyConflict):
			phoneAuthError(c, http.StatusConflict, "PHONE_IDEMPOTENCY_CONFLICT", "Invalid parameters", 0, nil)
		case errors.Is(err, model.ErrPhoneCodeInvalid):
			phoneAuthError(c, http.StatusUnauthorized, "PHONE_CODE_INVALID_OR_EXPIRED", "Verification code is incorrect or has expired", 0, nil)
		case errors.Is(err, model.ErrPhoneChallengeConsumed):
			phoneAuthError(c, http.StatusConflict, "PHONE_CHALLENGE_CONSUMED", "Verification code has already been used, please request a new code", 0, nil)
		default:
			phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_LOGIN_UNAVAILABLE", "Sign-in could not be completed, please try again", 1, nil)
		}
		return
	}
	if createdUser != nil {
		finishPhoneUserCreation(createdUser)
	}
	if result.Verification != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "code": "PHONE_LOGIN_VERIFICATION_REQUIRED", "retry_after_seconds": 0, "data": result.Verification})
		return
	}
	user, err := model.GetSelfUserById(result.UserID)
	if err != nil {
		phoneAuthError(c, http.StatusServiceUnavailable, "PHONE_LOGIN_UNAVAILABLE", "Sign-in could not be completed, please try again", 1, nil)
		return
	}
	c.Set("relay_key", result.RelayKey)
	c.Set("relay_key_id", result.RelayKeyID)
	writeLoginResponse(c, user, result.Bundle)
}

var (
	errPhoneAccountConflict = fmt.Errorf("phone_account_conflict")
	errPhoneAccountDisabled = fmt.Errorf("phone_account_disabled")
)

func findOrCreatePhoneUser(phoneE164 string) (*model.User, bool, error) {
	var user *model.User
	var created bool
	err := model.WithPhoneAuthTransaction(phoneE164, func(tx *gorm.DB) error {
		var err error
		user, created, err = findOrCreatePhoneUserWithTx(tx, phoneE164)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		finishPhoneUserCreation(user)
	}
	return user, created, nil
}

func findOrCreatePhoneUserWithTx(tx *gorm.DB, phoneE164 string) (*model.User, bool, error) {
	var existing model.User
	err := tx.Where("phone_e164 IN ?", phoneLookupCandidates(phoneE164)).First(&existing).Error
	if err == nil {
		if existing.Status != common.UserStatusEnabled {
			return nil, false, errPhoneAccountDisabled
		}
		return &existing, false, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, false, err
	}

	// A shadow account created earlier for this number may exist under the
	// derived username; adopt it instead of creating a duplicate identity.
	username := phoneUsername(phoneE164)
	var byUsername model.User
	err = tx.Where("username = ?", username).First(&byUsername).Error
	if err == nil {
		if byUsername.Status != common.UserStatusEnabled {
			return nil, false, errPhoneAccountDisabled
		}
		if byUsername.PhoneE164 != nil && *byUsername.PhoneE164 != phoneE164 {
			return nil, false, errPhoneAccountConflict
		}
		if err := tx.Model(&model.User{}).Where("id = ?", byUsername.Id).
			Update("phone_e164", phoneE164).Error; err != nil {
			return nil, false, err
		}
		byUsername.PhoneE164 = &phoneE164
		return &byUsername, false, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, false, err
	}

	password := common.GenerateVerificationCode(12)
	hashed, err := common.HashAccountPassword(password)
	if err != nil {
		return nil, false, err
	}
	phone := phoneE164
	// First verification creates the account, so the one-off registration
	// bonus is applied exactly once — later sign-ins reuse this row untouched.
	bonusQuota := 0
	if common.PhoneRegistrationBonusYuan > 0 {
		bonusQuota = quotaFromYuan(common.PhoneRegistrationBonusYuan)
	}
	created := model.User{
		Username:    username,
		Password:    hashed,
		DisplayName: username,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       common.PhoneLoginDefaultGroup,
		PhoneE164:   &phone,
	}
	// Creation and its phone grant commit together, including when batching is
	// enabled. Only the transaction that created this unique identity may grant
	// the bonus; retries and existing/adopted accounts return above unchanged.
	if err := created.InsertWithTx(tx, 0); err != nil {
		return nil, false, err
	}
	if bonusQuota > 0 {
		if err := common.ValidateWalletQuota(bonusQuota); err != nil {
			return nil, false, err
		}
		result := tx.Model(&model.User{}).Where("id = ? AND quota <= ?", created.Id, common.MaxWalletQuota-bonusQuota).Update("quota", gorm.Expr("quota + ?", bonusQuota))
		if result.Error != nil {
			return nil, false, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, false, model.ErrWalletQuotaLimitExceeded
		}
	}
	return &created, true, nil
}

func finishPhoneUserCreation(created *model.User) {
	bonusQuota := 0
	if common.PhoneRegistrationBonusYuan > 0 {
		bonusQuota = quotaFromYuan(common.PhoneRegistrationBonusYuan)
	}
	// Preserve the initial quota while finalizing global registration logging.
	// A separate phone event records only the additional, committed grant.
	created.FinishInsert(0)
	if bonusQuota > 0 {
		before := int64(created.Quota)
		created.Quota += bonusQuota
		after := int64(created.Quota)
		model.RecordLog(created.Id, model.LogTypeSystem, common.NewMessage("New user sign-up bonus {{quota}}", map[string]any{"quota": logger.FormatQuota(bonusQuota)}), model.BalanceChange{Category: "registration_bonus", RegistrationSource: "phone", QuotaDelta: int64(bonusQuota), QuotaBefore: &before, QuotaAfter: &after})
		common.SysLog(common.LogText(
			"granted registration bonus of %d points (%d quota) to %s",
			1000*common.PhoneRegistrationBonusYuan, bonusQuota, created.Username,
		))
	}

}
