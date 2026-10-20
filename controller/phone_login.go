package controller

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Phone sign-in mirrors the password login contract: the caller receives the
// same access_token/session bundle, so clients only swap the login call. The
// phone number is the account identity; the account is created on the first
// successful verification.

var phoneE164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// A tiny per-number cooldown so one caller cannot burn SMS budget in a loop.
var (
	phoneSendMu        sync.Mutex
	phoneLastSentAt    = map[string]time.Time{}
	phoneSendCooldown  = 60 * time.Second
	phoneSendRateLimit = 5 * time.Hour
	phoneSendCount     = map[string]int{}
)

type phoneChallengeRequest struct {
	PhoneE164 string `json:"phone_e164"`
}

type phoneLoginRequest struct {
	PhoneE164 string `json:"phone_e164"`
	Code      string `json:"code"`
}

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

func checkPhoneSendAllowed(phoneE164 string) (int, string) {
	phoneSendMu.Lock()
	defer phoneSendMu.Unlock()
	now := time.Now()
	if last, ok := phoneLastSentAt[phoneE164]; ok && now.Sub(last) < phoneSendCooldown {
		return int(phoneSendCooldown.Seconds() - now.Sub(last).Seconds()), ""
	}
	// Count only fresh windows.
	if windowStart, ok := phoneSendCountWindow[phoneE164]; !ok || now.Sub(windowStart) >= phoneSendRateLimit {
		phoneSendCountWindow[phoneE164] = now
		phoneSendCount[phoneE164] = 0
	}
	if phoneSendCount[phoneE164] >= 5 {
		return 0, "Too many verification codes requested for this number, please try again later"
	}
	phoneLastSentAt[phoneE164] = now
	phoneSendCount[phoneE164]++
	return 0, ""
}

var phoneSendCountWindow = map[string]time.Time{}

func deliverPhoneCode(phoneE164 string, code string) error {
	if common.PhoneSmsBypassAllows(phoneE164) {
		common.SysLog(common.LogText("phone login development bypass used for %s", phoneE164))
		return nil
	}
	return common.SendSmsCode(phoneE164, code)
}

// SendPhoneLoginCode issues a verification code for phone sign-in.
func SendPhoneLoginCode(c *gin.Context) {
	if !common.PhoneLoginEnabled {
		common.ApiErrorT(c, "Phone sign-in has been disabled by administrator")
		return
	}
	var request phoneChallengeRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	phone := strings.TrimSpace(request.PhoneE164)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	if !phoneE164Pattern.MatchString(phone) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if retryAfter, message := checkPhoneSendAllowed(phone); message != "" {
		common.ApiErrorT(c, message)
		return
	} else if retryAfter > 0 {
		common.ApiErrorT(c, "Please wait {{seconds}} seconds before requesting another code",
			map[string]any{"seconds": retryAfter})
		return
	}

	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		common.SysError(common.LogText("failed to generate a phone code: %s", err.Error()))
		common.ApiErrorT(c, "Verification code could not be sent, please try again later")
		return
	}
	if common.PhoneSmsBypassAllows(phone) {
		code = common.PhoneSmsDevCode
	}
	if err := deliverPhoneCode(phone, code); err != nil {
		common.SysError(common.LogText("failed to deliver phone code for %s: %s", phone, err.Error()))
		common.ApiErrorT(c, "Verification code could not be sent, please try again later")
		return
	}
	common.RegisterVerificationCodeWithKey(phone, code, common.PhoneLoginPurpose)

	data := gin.H{
		"resend_after_seconds": int(phoneSendCooldown.Seconds()),
		"expires_in_minutes":   common.VerificationValidMinutes,
	}
	// Local development only: hand the fixed code back so a client can finish
	// the flow without an SMS provider. Never set while the bypass is off.
	if common.PhoneSmsBypassAllows(phone) {
		data["dev_code"] = code
	}
	common.ApiSuccess(c, data)
}

// PhoneLogin verifies the code and signs the phone's account in, creating it on
// first use.
func PhoneLogin(c *gin.Context) {
	if !common.PhoneLoginEnabled {
		common.ApiErrorT(c, "Phone sign-in has been disabled by administrator")
		return
	}
	var request phoneLoginRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	phone := strings.TrimSpace(request.PhoneE164)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	code := strings.TrimSpace(request.Code)
	if !phoneE164Pattern.MatchString(phone) || code == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !common.VerifyCodeWithKey(phone, code, common.PhoneLoginPurpose) {
		common.ApiErrorT(c, "Verification code is incorrect or has expired")
		return
	}
	// One code, one sign-in.
	common.DeleteKey(phone, common.PhoneLoginPurpose)

	user, created, err := findOrCreatePhoneUser(phone)
	if err != nil {
		switch err {
		case errPhoneAccountConflict:
			common.ApiErrorT(c, "This phone number is already linked to a different account")
		case errPhoneAccountDisabled:
			common.ApiErrorT(c, "User has been banned")
		default:
			common.ApiError(c, err)
		}
		return
	}
	if created {
		common.SysLog(common.LogText("created phone account %s for %s", user.Username, phone))
	}
	// Hand the client a credential that belongs to this account, so the relay
	// bills the person who signed in instead of whoever owns the deployment
	// key. Missing on failure; clients fall back to their previous key.
	if key, tokenId, err := model.EnsureRelayToken(user.Id); err == nil {
		c.Set("relay_key", "sk-"+key)
		c.Set("relay_key_id", tokenId)
	} else {
		common.SysError(common.LogText("failed to prepare relay key for user %d: %s", user.Id, err.Error()))
	}
	setupLoginAtAuthVersion(user, user.AuthVersion, c)
}

var (
	errPhoneAccountConflict = fmt.Errorf("phone_account_conflict")
	errPhoneAccountDisabled = fmt.Errorf("phone_account_disabled")
)

func findOrCreatePhoneUser(phoneE164 string) (*model.User, bool, error) {
	var existing model.User
	err := model.DB.Where("phone_e164 IN ?", phoneLookupCandidates(phoneE164)).First(&existing).Error
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
	err = model.DB.Where("username = ?", username).First(&byUsername).Error
	if err == nil {
		if byUsername.PhoneE164 != nil && *byUsername.PhoneE164 != phoneE164 {
			return nil, false, errPhoneAccountConflict
		}
		if err := model.DB.Model(&model.User{}).Where("id = ?", byUsername.Id).
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
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := created.InsertWithTx(tx, 0); err != nil {
			return err
		}
		if bonusQuota <= 0 {
			return nil
		}
		if err := common.ValidateWalletQuota(bonusQuota); err != nil {
			return err
		}
		result := tx.Model(&model.User{}).
			Where("id = ? AND quota <= ?", created.Id, common.MaxWalletQuota-bonusQuota).
			Update("quota", gorm.Expr("quota + ?", bonusQuota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return model.ErrWalletQuotaLimitExceeded
		}
		return nil
	}); err != nil {
		return nil, false, err
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
			1000*common.PhoneRegistrationBonusYuan, bonusQuota, username,
		))
	}
	return &created, true, nil
}
