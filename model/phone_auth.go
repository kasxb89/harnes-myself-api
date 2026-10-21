package model

import (
	"crypto/hmac"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	phoneChallengePurpose = "phone_challenge"
	phoneExchangePurpose  = "phone_exchange"
	phoneGuardPurpose     = "phone_guard"
	PhoneSendCooldown     = time.Minute
	PhoneSendWindow       = 5 * time.Hour
	PhoneLoginReplayTTL   = 5 * time.Minute
	phoneCodeAttemptLimit = 5
)

var (
	ErrPhoneIdempotencyConflict = errors.New("phone idempotency conflict")
	ErrPhoneCodeInvalid         = errors.New("phone code invalid or expired")
	ErrPhoneChallengeConsumed   = errors.New("phone challenge consumed")
	ErrPhoneLoginFactorInvalid  = errors.New("phone login factor invalid")
)

type PhoneRateLimitError struct{ RetryAfterSeconds int }

func (e *PhoneRateLimitError) Error() string { return "phone rate limited" }

// PhoneChallenge contains no usable verification code. Provider and token_hash
// hold purpose-separated HMACs; a database read cannot guess a six-digit code.
type PhoneChallenge struct {
	ID             string `json:"challenge_id"`
	DeliveryStatus string `json:"delivery_status"`
	ExpiresAt      int64  `json:"expires_at"`
	ResendAt       int64  `json:"resend_at"`
	CodeHash       string `json:"code_hash"`
	Attempts       int    `json:"attempts"`
	FailureCode    string `json:"failure_code,omitempty"`
}

type phoneExchangePayload struct {
	Fingerprint string `json:"fingerprint"`
	Receipt     string `json:"receipt,omitempty"`
	Invalid     bool   `json:"invalid,omitempty"`
}

func phoneAuthHash(purpose, value string) string {
	return common.GenerateHMACWithKey([]byte("phone-auth/"+purpose+"/v1:"+common.SessionSecret), value)
}

func phoneChallengeID(key string) string { return phoneAuthHash("challenge", key) }

// bindPhoneAuthRequest makes client key identity global within one purpose.
// The binding commits before business work and is never rewritten on failure.
func bindPhoneAuthRequest(flow *AuthFlow) error {
	if err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "token_hash"}}, DoNothing: true}).Create(flow).Error; err != nil {
		return err
	}
	var existing AuthFlow
	if err := DB.Where("token_hash = ?", flow.TokenHash).First(&existing).Error; err != nil {
		return err
	}
	if existing.Purpose != flow.Purpose || existing.Provider != flow.Provider {
		return ErrPhoneIdempotencyConflict
	}
	return nil
}

// WithPhoneAuthTransaction takes the first write before reading authentication
// state. The unique guard serializes a phone across processes, including SQLite
// deferred transactions, without relying on an in-memory mutex or FOR UPDATE.
func WithPhoneAuthTransaction(phone string, action func(*gorm.DB) error) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		guard := AuthFlow{TokenHash: phoneAuthHash("guard", phone), Purpose: phoneGuardPurpose, ExpiresAt: now.Add(PhoneSendWindow + time.Hour)}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "token_hash"}}, DoNothing: true}).Create(&guard).Error; err != nil {
			return err
		}
		if err := tx.Model(&AuthFlow{}).Where("token_hash = ? AND purpose = ?", guard.TokenHash, phoneGuardPurpose).Update("expires_at", guard.ExpiresAt).Error; err != nil {
			return err
		}
		return action(tx)
	})
}

// ReservePhoneChallenge commits the code digest before dispatch. Once reserved,
// the same dispatch key only reads state, even after a definite not-sent error.
func ReservePhoneChallenge(phone, key, code string, expiresAt time.Time) (*PhoneChallenge, bool, error) {
	id := phoneChallengeID(key)
	provider := phoneAuthHash("phone", phone)
	binding := AuthFlow{TokenHash: phoneAuthHash("dispatch-key", key), Purpose: "phone_dispatch_binding", Provider: provider, ExpiresAt: time.Now().Add(PhoneSendWindow + PhoneLoginReplayTTL)}
	if err := bindPhoneAuthRequest(&binding); err != nil {
		return nil, false, err
	}
	var challenge PhoneChallenge
	created := false
	err := WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		var flow AuthFlow
		err := tx.Where("token_hash = ?", authFlowTokenHash(id)).First(&flow).Error
		if err == nil {
			if flow.Purpose != phoneChallengePurpose || flow.Provider != provider {
				return ErrPhoneIdempotencyConflict
			}
			if err := common.UnmarshalJsonStr(flow.Payload, &challenge); err != nil {
				return err
			}
			challenge.ExpiresAt = flow.ExpiresAt.Unix()
			// A crashed sender cannot safely decide whether the provider accepted.
			if challenge.DeliveryStatus == "sending" && time.Since(flow.CreatedAt) > 15*time.Second {
				challenge.DeliveryStatus = "unknown"
				encoded, err := common.Marshal(challenge)
				if err != nil {
					return err
				}
				return tx.Model(&flow).Updates(map[string]any{"intent": "unknown", "payload": string(encoded)}).Error
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now()
		var prior []AuthFlow
		if err := tx.Where("purpose = ? AND provider = ? AND created_at > ? AND intent IN ?", phoneChallengePurpose, provider, now.Add(-PhoneSendWindow), []string{"sending", "accepted", "unknown"}).Order("created_at DESC").Find(&prior).Error; err != nil {
			return err
		}
		var retryAt time.Time
		if len(prior) >= 5 {
			retryAt = prior[len(prior)-1].CreatedAt.Add(PhoneSendWindow)
		}
		for _, previous := range prior {
			until := previous.CreatedAt.Add(PhoneSendCooldown)
			if previous.Intent == "sending" || previous.Intent == "unknown" {
				until = previous.ExpiresAt
			}
			if until.After(retryAt) {
				retryAt = until
			}
		}
		if retryAt.After(now) {
			return &PhoneRateLimitError{RetryAfterSeconds: max(1, int(time.Until(retryAt).Seconds())+1)}
		}
		challenge = PhoneChallenge{ID: id, DeliveryStatus: "sending", ExpiresAt: expiresAt.Unix(), ResendAt: expiresAt.Unix(), CodeHash: phoneAuthHash("code", phone+":"+id+":"+code)}
		encoded, err := common.Marshal(challenge)
		if err != nil {
			return err
		}
		flow = AuthFlow{TokenHash: authFlowTokenHash(id), Purpose: phoneChallengePurpose, Provider: provider, Intent: "sending", Payload: string(encoded), ExpiresAt: expiresAt}
		if err := tx.Create(&flow).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return &challenge, created, err
}

func FinishPhoneChallenge(phone, id, status string, retryAfterSeconds ...int) (*PhoneChallenge, error) {
	var challenge PhoneChallenge
	err := WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		var flow AuthFlow
		if err := tx.Where("token_hash = ? AND purpose = ? AND provider = ?", authFlowTokenHash(id), phoneChallengePurpose, phoneAuthHash("phone", phone)).First(&flow).Error; err != nil {
			return err
		}
		if err := common.UnmarshalJsonStr(flow.Payload, &challenge); err != nil {
			return err
		}
		if challenge.DeliveryStatus != "sending" {
			return nil
		}
		challenge.DeliveryStatus = status
		switch status {
		case "accepted":
			challenge.ResendAt = flow.CreatedAt.Add(PhoneSendCooldown).Unix()
			// Only a provider-accepted replacement supersedes an older code.
			// Definite not-sent failures keep the previous challenge usable.
			if err := tx.Model(&AuthFlow{}).Where("purpose = ? AND provider = ? AND id < ? AND consumed_at IS NULL AND intent IN ? AND expires_at > ?", phoneChallengePurpose, flow.Provider, flow.Id, []string{"accepted", "unknown"}, time.Now()).Update("expires_at", time.Now()).Error; err != nil {
				return err
			}
		case "not_sent":
			challenge.ResendAt = 0
			challenge.FailureCode = "PHONE_CHALLENGE_UNAVAILABLE"
			if len(retryAfterSeconds) > 0 && retryAfterSeconds[0] > 0 {
				challenge.FailureCode = "PHONE_RATE_LIMITED"
				challenge.ResendAt = time.Now().Add(time.Duration(retryAfterSeconds[0]) * time.Second).Unix()
			}
		}
		encoded, err := common.Marshal(challenge)
		if err != nil {
			return err
		}
		return tx.Model(&flow).Updates(map[string]any{"intent": status, "payload": string(encoded)}).Error
	})
	return &challenge, err
}

// LegacyPhoneChallenge resolves a challenge only; it never validates an OTP or
// restores a receipt. ExchangePhoneChallenge performs the authoritative check
// and atomic consumption, so legacy requests cannot bypass the version 2 guard.
func LegacyPhoneChallenge(phone string) (string, error) {
	var challengeID string
	err := WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		var flow AuthFlow
		if err := tx.Where("purpose = ? AND provider = ? AND intent IN ?", phoneChallengePurpose, phoneAuthHash("phone", phone), []string{"accepted", "unknown", "sending"}).Order("id DESC").First(&flow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPhoneCodeInvalid
			}
			return err
		}
		if flow.ConsumedAt != nil {
			return ErrPhoneChallengeConsumed
		}
		if !flow.ExpiresAt.After(time.Now()) {
			return ErrPhoneCodeInvalid
		}
		var challenge PhoneChallenge
		if err := common.UnmarshalJsonStr(flow.Payload, &challenge); err != nil {
			return err
		}
		if challenge.Attempts >= phoneCodeAttemptLimit {
			return ErrPhoneCodeInvalid
		}
		challengeID = challenge.ID
		return nil
	})
	return challengeID, err
}

// ExchangePhoneChallenge atomically consumes the OTP and commits the complete
// login action. A downstream failure rolls all writes back, leaving the code
// retryable. Receipt contains authenticated ciphertext, never bearer plaintext.
func ExchangePhoneChallenge(phone, id, key, code string, action func(*gorm.DB) (string, int, string, error), replay func(*gorm.DB, *AuthFlow, string) error) (bool, error) {
	provider := phoneAuthHash("phone", phone)
	fingerprint := phoneAuthHash("exchange-request", phone+":"+id+":"+code)
	exchangeHash := phoneAuthHash("exchange-key", key)
	// Reserve the immutable request binding in its own short transaction. It
	// survives a subsequent business rollback: the same key cannot change its
	// phone, challenge or code after a transient account/session failure.
	initial, err := common.Marshal(phoneExchangePayload{Fingerprint: fingerprint})
	if err != nil {
		return false, err
	}
	bound := AuthFlow{TokenHash: exchangeHash, Purpose: phoneExchangePurpose, Provider: provider, Payload: string(initial), ExpiresAt: time.Now().Add(PhoneLoginReplayTTL)}
	if err := bindPhoneAuthRequest(&bound); err != nil {
		return false, err
	}
	replayed := false
	var outcome error
	err = WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		var exchange AuthFlow
		err := tx.Where("token_hash = ?", exchangeHash).First(&exchange).Error
		if err == nil {
			var payload phoneExchangePayload
			if err := common.UnmarshalJsonStr(exchange.Payload, &payload); err != nil {
				return err
			}
			if exchange.Purpose != phoneExchangePurpose || exchange.Provider != provider || payload.Fingerprint != fingerprint {
				return ErrPhoneIdempotencyConflict
			}
			if payload.Invalid {
				return ErrPhoneCodeInvalid
			}
			if !exchange.ExpiresAt.After(time.Now()) {
				return ErrPhoneChallengeConsumed
			}
			if payload.Receipt != "" {
				if err := replay(tx, &exchange, payload.Receipt); err != nil {
					return err
				}
				replayed = true
				return nil
			}
		}
		if err != nil {
			return err
		}
		var flow AuthFlow
		if err := tx.Where("token_hash = ? AND purpose = ? AND provider = ?", authFlowTokenHash(id), phoneChallengePurpose, provider).First(&flow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPhoneCodeInvalid
			}
			return err
		}
		if flow.ConsumedAt != nil {
			return ErrPhoneChallengeConsumed
		}
		var challenge PhoneChallenge
		if err := common.UnmarshalJsonStr(flow.Payload, &challenge); err != nil {
			return err
		}
		if !flow.ExpiresAt.After(time.Now()) || challenge.Attempts >= phoneCodeAttemptLimit || challenge.DeliveryStatus == "not_sent" {
			return ErrPhoneCodeInvalid
		}
		payload := phoneExchangePayload{Fingerprint: fingerprint}
		if !hmac.Equal([]byte(challenge.CodeHash), []byte(phoneAuthHash("code", phone+":"+id+":"+code))) {
			challenge.Attempts++
			encoded, err := common.Marshal(challenge)
			if err != nil {
				return err
			}
			if err := tx.Model(&flow).Update("payload", string(encoded)).Error; err != nil {
				return err
			}
			payload.Invalid = true
			outcome = ErrPhoneCodeInvalid
		} else {
			receipt, userID, sid, err := action(tx)
			if err != nil {
				return err
			}
			payload.Receipt = receipt
			exchange.UserId, exchange.SessionId = userID, sid
			now := time.Now()
			if err := tx.Model(&flow).Where("consumed_at IS NULL").Update("consumed_at", now).Error; err != nil {
				return err
			}
		}
		encoded, err := common.Marshal(payload)
		if err != nil {
			return err
		}
		return tx.Model(&exchange).Updates(map[string]any{"payload": string(encoded), "user_id": exchange.UserId, "session_id": exchange.SessionId, "expires_at": time.Now().Add(PhoneLoginReplayTTL)}).Error
	})
	if err != nil {
		return false, err
	}
	return replayed, outcome
}

// PhoneAuthUserWithTx and verification/session helpers keep authoritative locks
// in the model layer, using the shared cross-database locking policy.
func PhoneAuthUserWithTx(tx *gorm.DB, id int) (*User, error) {
	var user User
	err := lockForUpdate(tx).First(&user, id).Error
	return &user, err
}

func PhoneAuthSessionWithTx(tx *gorm.DB, sid string) (*UserSession, error) {
	var session UserSession
	err := lockForUpdate(tx).Where("sid = ?", sid).First(&session).Error
	return &session, err
}

func PhoneAuthVerificationStateWithTx(tx *gorm.DB, userID int) (*UserVerificationState, error) {
	return getUserVerificationState(tx, userID, true)
}

func CreatePhoneAuthSessionWithTx(tx *gorm.DB, session *UserSession) error {
	now := time.Now().Unix()
	var active, issued int64
	if err := tx.Model(&UserSession{}).Where("user_id = ? AND status = ? AND expires_at > ?", session.UserID, UserSessionStatusActive, now).Count(&active).Error; err != nil {
		return err
	}
	if active >= int64(common.UserSessionActiveLimit) {
		return ErrUserSessionLimit
	}
	if err := tx.Model(&UserSession{}).Where("user_id = ? AND created_at > ?", session.UserID, now-common.UserSessionIssuanceWindowSeconds).Count(&issued).Error; err != nil {
		return err
	}
	if issued >= int64(common.UserSessionIssuanceLimit) {
		return ErrUserSessionIssuanceLimit
	}
	return createUserSessionWithTx(tx, session)
}

// CreatePhoneLoginVerificationWithTx is restricted to a server-created flow.
func CreatePhoneLoginVerificationWithTx(tx *gorm.DB, input AuthFlowCreate) (string, *AuthFlow, error) {
	return createAuthFlowWithTx(tx, input)
}

func GetPhoneLoginVerificationWithTx(tx *gorm.DB, token string, userID int) (*AuthFlow, error) {
	var flow AuthFlow
	err := applyAuthFlowMatch(lockForUpdate(tx), token, AuthFlowMatch{Purpose: AuthFlowPurposeLoginVerification, UserId: userID}).First(&flow).Error
	return &flow, err
}

// Caller holds the authoritative user lock. A disabled credential is never
// silently re-enabled by either initial exchange or response-loss recovery.
func EnsurePhoneRelayTokenWithTx(tx *gorm.DB, userID int) (string, int, error) {
	var token Token
	err := tx.Where("user_id = ? AND name = ?", userID, RelayKeyName).Order("id ASC").First(&token).Error
	if err == nil {
		if token.Key == "" || token.Status != common.TokenStatusEnabled || token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix() {
			return "", 0, ErrUserSessionInactive
		}
		return token.Key, token.Id, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, err
	}
	key, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		return "", 0, err
	}
	now := common.GetTimestamp()
	token = Token{UserId: userID, Key: key, Name: RelayKeyName, Status: common.TokenStatusEnabled, CreatedTime: now, AccessedTime: now, ExpiredTime: -1, UnlimitedQuota: true, AllowUserQuotaQuery: true}
	if err := tx.Create(&token).Error; err != nil {
		return "", 0, err
	}
	return token.Key, token.Id, nil
}

// UsePhoneLoginFactorWithTx consumes a valid factor only in the transaction
// that commits the login flow, relay and session. Invalid guesses are counted
// separately by the service after rollback so a database failure cannot erase
// the brute-force budget or burn a valid recovery code.
func UsePhoneLoginFactorWithTx(tx *gorm.DB, userID int, code string) error {
	var factor TwoFA
	if err := lockForUpdate(tx).Where("user_id = ? AND is_enabled = ?", userID, true).First(&factor).Error; err != nil {
		return err
	}
	if factor.IsLocked() {
		return ErrPhoneLoginFactorInvalid
	}
	now := time.Now()
	valid := false
	if numeric, err := common.ValidateNumericCode(code); err == nil {
		// Match the library's current +/- one time-step tolerance and claim the
		// matching counter, rather than retaining a six-digit OTP in the DB.
		for _, offset := range []int{0, -1, 1} {
			at := now.Add(time.Duration(offset) * 30 * time.Second)
			expected, err := totp.GenerateCode(factor.Secret, at)
			if err != nil {
				return err
			}
			if !hmac.Equal([]byte(expected), []byte(numeric)) {
				continue
			}
			assertion := strconv.Itoa(factor.Id) + ":" + strconv.FormatInt(at.Unix()/30, 10)
			if err := ClaimExternalAuthAssertionWithTx(tx, "phone_totp_login", assertion, now.Add(2*time.Minute)); err != nil {
				if errors.Is(err, ErrAuthFlowConsumed) {
					return ErrPhoneLoginFactorInvalid
				}
				return err
			}
			valid = true
			break
		}
	} else if common.ValidateBackupCode(code) {
		var backups []TwoFABackupCode
		if err := tx.Where("user_id = ? AND is_used = ?", userID, false).Find(&backups).Error; err != nil {
			return err
		}
		for _, backup := range backups {
			if !common.ValidatePasswordAndHash(common.NormalizeBackupCode(code), backup.CodeHash) {
				continue
			}
			result := tx.Model(&TwoFABackupCode{}).Where("id = ? AND is_used = ?", backup.Id, false).Updates(map[string]any{"is_used": true, "used_at": now})
			if result.Error != nil {
				return result.Error
			}
			valid = result.RowsAffected == 1
			break
		}
	}
	if !valid {
		return ErrPhoneLoginFactorInvalid
	}
	return tx.Model(&TwoFA{}).Where("id = ?", factor.Id).Updates(map[string]any{"failed_attempts": 0, "locked_until": nil, "last_used_at": now}).Error
}
