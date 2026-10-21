package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// PhoneLoginResult is sealed for a short replay window. Only ciphertext is
// persisted; a replay must still match the current authoritative security state.
type PhoneLoginResult struct {
	UserID       int             `json:"user_id"`
	AuthVersion  int64           `json:"auth_version"`
	Bundle       *AuthBundle     `json:"bundle,omitempty"`
	RefreshToken string          `json:"refresh,omitempty"`
	RelayKey     string          `json:"relay_key,omitempty"`
	RelayKeyID   int             `json:"relay_key_id,omitempty"`
	Verification *LoginChallenge `json:"verification,omitempty"`
}

func PreparePhoneLoginWithTx(tx *gorm.DB, user *model.User, ip, userAgent string) (*PhoneLoginResult, error) {
	current, err := model.PhoneAuthUserWithTx(tx, user.Id)
	if err != nil {
		return nil, err
	}
	if current.Status != common.UserStatusEnabled || current.AuthVersion <= 0 || current.AuthVersion != user.AuthVersion {
		return nil, ErrLoginSessionRevoked
	}
	state, err := model.PhoneAuthVerificationStateWithTx(tx, user.Id)
	if err != nil {
		return nil, err
	}
	methods, err := securityVerificationPolicy(VerificationScopeLogin, *state)
	if err != nil {
		return nil, err
	}
	result := &PhoneLoginResult{UserID: user.Id, AuthVersion: user.AuthVersion}
	if len(methods) > 0 {
		available := false
		for _, method := range methods {
			available = available || method.Available
		}
		if !available {
			return nil, ErrVerificationUnavailable
		}
		encoded, err := common.Marshal(loginFlowPayload{AuthVersion: user.AuthVersion, LoginMethod: "phone"})
		if err != nil {
			return nil, err
		}
		expires := time.Now().Add(LoginVerificationTTL)
		token, _, err := model.CreatePhoneLoginVerificationWithTx(tx, model.AuthFlowCreate{Purpose: model.AuthFlowPurposeLoginVerification, UserId: user.Id, Payload: string(encoded), ExpiresAt: expires})
		if err != nil {
			return nil, err
		}
		result.Verification = &LoginChallenge{RequireVerification: true, FlowToken: token, ExpiresAt: expires.Unix(), Methods: methods}
		return result, nil
	}
	key, keyID, err := model.EnsurePhoneRelayTokenWithTx(tx, user.Id)
	if err != nil {
		return nil, err
	}
	session, secret, err := newLoginSession(user.Id, user.AuthVersion, "phone", ip, userAgent)
	if err != nil {
		return nil, err
	}
	if err := model.CreatePhoneAuthSessionWithTx(tx, session); err != nil {
		return nil, err
	}
	bundle, err := issueAuthBundle(session, session.SID+"."+secret, true)
	if err != nil {
		return nil, err
	}
	result.Bundle, result.RefreshToken, result.RelayKey, result.RelayKeyID = bundle, bundle.RefreshToken, "sk-"+key, keyID
	return result, nil
}

// SealPhoneLoginResult binds the encrypted receipt to the client's high-entropy
// exchange key as well as the server secret. Its storage is not a login token.
func SealPhoneLoginResult(result *PhoneLoginResult, key string) (string, error) {
	encoded, err := common.Marshal(result)
	if err != nil {
		return "", err
	}
	aead, err := phoneReceiptCipher(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, encoded, []byte("phone-login-result-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func ReplayPhoneLoginResultWithTx(tx *gorm.DB, flow *model.AuthFlow, sealed, key string) (*PhoneLoginResult, error) {
	aead, err := phoneReceiptCipher(key)
	if err != nil {
		return nil, err
	}
	encoded, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(encoded) < aead.NonceSize() {
		return nil, model.ErrPhoneChallengeConsumed
	}
	plain, err := aead.Open(nil, encoded[:aead.NonceSize()], encoded[aead.NonceSize():], []byte("phone-login-result-v1"))
	if err != nil {
		return nil, model.ErrPhoneChallengeConsumed
	}
	var result PhoneLoginResult
	if err := common.Unmarshal(plain, &result); err != nil {
		return nil, err
	}
	user, err := model.PhoneAuthUserWithTx(tx, flow.UserId)
	if err != nil {
		return nil, err
	}
	if result.UserID != user.Id || user.Status != common.UserStatusEnabled || user.AuthVersion != result.AuthVersion {
		return nil, model.ErrPhoneChallengeConsumed
	}
	if result.Verification != nil {
		pending, err := model.GetPhoneLoginVerificationWithTx(tx, result.Verification.FlowToken, user.Id)
		if err != nil || pending.ConsumedAt != nil || !pending.ExpiresAt.After(time.Now()) {
			return nil, model.ErrPhoneChallengeConsumed
		}
		var bound loginFlowPayload
		if common.UnmarshalJsonStr(pending.Payload, &bound) != nil || bound.AuthVersion != user.AuthVersion || bound.LoginMethod != "phone" {
			return nil, model.ErrPhoneChallengeConsumed
		}
		state, err := model.PhoneAuthVerificationStateWithTx(tx, user.Id)
		if err != nil {
			return nil, err
		}
		methods, err := securityVerificationPolicy(VerificationScopeLogin, *state)
		if err != nil || len(methods) == 0 {
			return nil, model.ErrPhoneChallengeConsumed
		}
		available := false
		for _, method := range methods {
			available = available || method.Available
		}
		if !available {
			return nil, model.ErrPhoneChallengeConsumed
		}
		result.Verification.Methods = methods
		return &result, nil
	}
	if result.Bundle == nil || result.RefreshToken == "" || flow.SessionId != result.Bundle.Session.SID {
		return nil, model.ErrPhoneChallengeConsumed
	}
	session, err := model.PhoneAuthSessionWithTx(tx, flow.SessionId)
	if err != nil {
		return nil, err
	}
	identity, err := ParseAccessToken(result.Bundle.AccessToken)
	if err != nil || identity.UserID != user.Id || identity.SessionID != session.SID || identity.UserAuthVersion != user.AuthVersion || identity.SessionVersion != session.Version {
		return nil, model.ErrPhoneChallengeConsumed
	}
	sid, secret, ok := splitRefreshToken(result.RefreshToken)
	if !ok || sid != session.SID || session.UserID != user.Id || session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() || session.UserAuthVersion != user.AuthVersion || !hmac.Equal([]byte(session.RefreshHash), []byte(hashRefreshSecret(secret))) {
		return nil, model.ErrPhoneChallengeConsumed
	}
	var token model.Token
	if err := tx.Where("id = ? AND user_id = ?", result.RelayKeyID, user.Id).First(&token).Error; err != nil {
		return nil, model.ErrPhoneChallengeConsumed
	}
	if token.Status != common.TokenStatusEnabled || token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix() || !hmac.Equal([]byte("sk-"+token.Key), []byte(result.RelayKey)) {
		return nil, model.ErrPhoneChallengeConsumed
	}
	result.Bundle.RefreshToken = result.RefreshToken
	return &result, nil
}

func phoneReceiptCipher(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errors.New("missing phone exchange key")
	}
	mac := hmac.New(sha256.New, authSigningKey("phone-login-receipt"))
	_, _ = mac.Write([]byte(key))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
