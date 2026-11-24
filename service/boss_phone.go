package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func PrepareBossPhoneLoginWithTx(tx *gorm.DB, user *model.User, binding *model.BossPhoneBinding, ip, userAgent string) (*PhoneLoginResult, error) {
	current, err := model.ValidateBossPhoneBindingWithTx(tx, binding)
	if err != nil {
		return nil, err
	}
	if current.Id != user.Id || current.AuthVersion != user.AuthVersion {
		return nil, model.ErrBossPhoneUnqualified
	}
	return preparePhoneLoginWithTx(tx, current, binding, ip, userAgent)
}

// These wrappers cryptographically separate Boss receipts from generic phone
// receipts without exposing or exporting the gateway's signing master key.
func SealBossPhoneLoginResult(result *PhoneLoginResult, key string) (string, error) {
	if result.BossBinding == nil || result.BossBinding.Scope != model.BossPhoneLoginMethod {
		return "", model.ErrBossPhoneUnqualified
	}
	return SealPhoneLoginResult(result, "boss-phone:"+key)
}

func ReplayBossPhoneLoginResultWithTx(tx *gorm.DB, flow *model.AuthFlow, sealed, key string) (*PhoneLoginResult, error) {
	return replayPhoneLoginResultWithTx(tx, flow, sealed, "boss-phone:"+key, true)
}

// CompleteBossPhoneVerificationWithTx is called only from the model's Boss MFA
// exchange. No ordinary /login/verify or Passkey flow can consume its purpose.
func CompleteBossPhoneVerificationWithTx(tx *gorm.DB, flow *model.AuthFlow, user *model.User, binding *model.BossPhoneBinding, code, ip, userAgent string) (*PhoneLoginResult, error) {
	if flow.Purpose != model.BossPhoneVerificationPurpose || flow.UserId != user.Id || flow.Provider != model.BossPhoneLoginMethod || flow.ConsumedAt != nil || !flow.ExpiresAt.After(time.Now()) {
		return nil, model.ErrAuthFlowInvalid
	}
	current, err := model.ValidateBossPhoneBindingWithTx(tx, binding)
	if err != nil {
		return nil, err
	}
	if current.Id != user.Id || current.AuthVersion != user.AuthVersion {
		return nil, model.ErrBossPhoneUnqualified
	}
	state, err := model.PhoneAuthVerificationStateWithTx(tx, user.Id)
	if err != nil {
		return nil, err
	}
	if err := requireLoginVerificationMethod(state, VerificationMethodTwoFA); err != nil {
		return nil, err
	}
	if err := model.UsePhoneLoginFactorWithTx(tx, user.Id, code); err != nil {
		return nil, err
	}
	return issuePhoneLoginSessionWithTx(tx, current, binding, ip, userAgent)
}

// Preserve the single existing factor policy. Unsupported Passkeys stay in the
// method set but cannot be downgraded to SMS or accepted without their RP.
func bossPhoneVerificationMethods(methods []VerificationMethodOption) []VerificationMethodOption {
	for i := range methods {
		if methods[i].Method == VerificationMethodTwoFA {
			methods[i].Method = "totp"
		} else {
			methods[i].Available = false
			methods[i].Reason = "This factor requires verification at its original relying party."
		}
	}
	return methods
}

func BossPhoneAuthState(raw string) (*model.User, LoginSessionView, error) {
	identity, err := ParseAccessToken(raw)
	if err != nil {
		return nil, LoginSessionView{}, err
	}
	user, session, err := model.BossPhoneAuthState(identity)
	if err != nil {
		return nil, LoginSessionView{}, err
	}
	return user, sessionView(session, true), nil
}
