package model

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BossPhoneGroup               = "Boss"
	BossPhoneLoginMethod         = "boss_phone"
	BossPhoneVerificationPurpose = "boss_phone_verification"
	bossPhoneSessionPurpose      = "boss_phone_session"
	bossPhoneMFAPurpose          = "boss_phone_mfa_exchange"
)

var ErrBossPhoneUnqualified = errors.New("boss phone authentication is unavailable")

// Only these two server-owned scopes exist. Scope is never accepted from JSON.
// The phone guard and rate budget remain shared; proofs and receipts do not.
type phoneAuthScope struct {
	boss             bool
	challengePurpose string
	exchangePurpose  string
	dispatchPurpose  string
}

var phoneScope = phoneAuthScope{challengePurpose: phoneChallengePurpose, exchangePurpose: phoneExchangePurpose, dispatchPurpose: "phone_dispatch_binding"}
var bossPhoneScope = phoneAuthScope{boss: true, challengePurpose: "boss_phone_challenge", exchangePurpose: "boss_phone_exchange", dispatchPurpose: "boss_phone_dispatch_binding"}

func (scope phoneAuthScope) hash(purpose, value string) string {
	if scope.boss {
		purpose = "boss/" + purpose
	}
	return phoneAuthHash(purpose, value)
}

type BossPhoneBinding struct {
	Scope       string `json:"scope"`
	UserID      int    `json:"user_id"`
	AuthVersion int64  `json:"auth_version"`
	PhoneHash   string `json:"phone_hash"`
}

type BossPhoneVerificationPayload struct {
	Binding BossPhoneBinding `json:"binding"`
}

// BossPhoneUserWithTx never resolves a username, adopts a user, or creates one.
// Check the group in Go as well: MySQL collations may be case-insensitive.
func BossPhoneUserWithTx(tx *gorm.DB, phone string) (*User, error) {
	// The unlocked lookup selects a UID only. Acquire its PRIMARY lock before
	// locking the phone index, matching state/receipt checks and mutation hooks.
	// Taking the phone-index lock first deadlocks with a concurrent state read on
	// InnoDB (phone index -> PRIMARY versus PRIMARY -> phone index).
	var hints []User
	if err := tx.Select("id").Where("phone_e164 IN ?", []string{phone, strings.TrimPrefix(phone, "+")}).Order("id ASC").Limit(2).Find(&hints).Error; err != nil {
		return nil, err
	}
	if len(hints) != 1 {
		return nil, ErrBossPhoneUnqualified
	}
	if _, err := PhoneAuthUserWithTx(tx, hints[0].Id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBossPhoneUnqualified
		}
		return nil, err
	}
	var users []User
	if err := lockForUpdate(tx).Where("phone_e164 IN ?", []string{phone, strings.TrimPrefix(phone, "+")}).Order("id ASC").Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) != 1 || users[0].Id != hints[0].Id || users[0].Status != common.UserStatusEnabled || users[0].Group != BossPhoneGroup || users[0].AuthVersion <= 0 {
		return nil, ErrBossPhoneUnqualified
	}
	return &users[0], nil
}

func ValidateBossPhoneBindingWithTx(tx *gorm.DB, binding *BossPhoneBinding) (*User, error) {
	if binding == nil || binding.Scope != BossPhoneLoginMethod || binding.UserID <= 0 || binding.AuthVersion <= 0 || binding.PhoneHash == "" {
		return nil, ErrBossPhoneUnqualified
	}
	user, err := PhoneAuthUserWithTx(tx, binding.UserID)
	if err != nil {
		return nil, err
	}
	if user.PhoneE164 == nil || *user.PhoneE164 == "" || user.Status != common.UserStatusEnabled || user.Group != BossPhoneGroup || user.AuthVersion != binding.AuthVersion {
		return nil, ErrBossPhoneUnqualified
	}
	phone := "+" + strings.TrimPrefix(*user.PhoneE164, "+")
	if binding.PhoneHash != bossPhoneScope.hash("phone", phone) {
		return nil, ErrBossPhoneUnqualified
	}
	unique, err := BossPhoneUserWithTx(tx, phone)
	if err != nil || unique.Id != binding.UserID {
		if err != nil {
			return nil, err
		}
		return nil, ErrBossPhoneUnqualified
	}
	return user, nil
}

func ReserveBossPhoneChallenge(phone, key, code string, expiresAt time.Time) (*PhoneChallenge, bool, error) {
	return reservePhoneChallenge(bossPhoneScope, phone, key, code, expiresAt, false)
}

func ExchangeBossPhoneChallenge(phone, id, key, code string, action func(*gorm.DB, *User, *BossPhoneBinding) (string, int, string, error), replay func(*gorm.DB, *AuthFlow, string) error) (bool, error) {
	return exchangePhoneChallenge(bossPhoneScope, phone, id, key, code, action, replay)
}

// Dispatch rechecks eligibility under both the phone guard and user lock. The
// digest was committed before this function: a crash or commit error cannot
// cause an idempotent retry to send twice. External delivery is never retried.
func DispatchBossPhoneChallenge(phone, id string, send func() (string, int)) error {
	return WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		user, err := BossPhoneUserWithTx(tx, phone)
		if err != nil {
			return err
		}
		var flow AuthFlow
		if err := lockForUpdate(tx).Where("token_hash = ? AND purpose = ? AND provider = ?", authFlowTokenHash(id), bossPhoneScope.challengePurpose, bossPhoneScope.hash("phone", phone)).First(&flow).Error; err != nil {
			return err
		}
		var challenge PhoneChallenge
		if common.UnmarshalJsonStr(flow.Payload, &challenge) != nil || challenge.BossBinding == nil || challenge.BossBinding.UserID != user.Id || challenge.BossBinding.AuthVersion != user.AuthVersion || flow.UserId != user.Id || !flow.ExpiresAt.After(time.Now()) || flow.ConsumedAt != nil || challenge.DeliveryStatus != "sending" {
			return ErrBossPhoneUnqualified
		}
		status, retry := send()
		challenge.DeliveryStatus = status
		if status == "accepted" {
			if err := tx.Model(&AuthFlow{}).Where("purpose = ? AND provider = ? AND id < ? AND consumed_at IS NULL AND intent IN ? AND expires_at > ?", bossPhoneScope.challengePurpose, flow.Provider, flow.Id, []string{"accepted", "unknown"}, time.Now()).Update("expires_at", time.Now()).Error; err != nil {
				return err
			}
		}
		if retry > 0 {
			challenge.ResendAt = time.Now().Add(time.Duration(retry) * time.Second).Unix()
		}
		encoded, err := common.Marshal(challenge)
		if err != nil {
			return err
		}
		return tx.Model(&flow).Updates(map[string]any{"intent": status, "payload": string(encoded)}).Error
	})
}

func GetBossPhoneVerificationWithTx(tx *gorm.DB, token string, userID int) (*AuthFlow, error) {
	var flow AuthFlow
	err := applyAuthFlowMatch(lockForUpdate(tx), token, AuthFlowMatch{Purpose: BossPhoneVerificationPurpose, Provider: BossPhoneLoginMethod, UserId: userID}).First(&flow).Error
	return &flow, err
}

func CreateBossPhoneSessionBindingWithTx(tx *gorm.DB, session *UserSession, binding *BossPhoneBinding) error {
	if binding == nil || session.LoginMethod != BossPhoneLoginMethod || session.UserID != binding.UserID || session.UserAuthVersion != binding.AuthVersion {
		return ErrBossPhoneUnqualified
	}
	encoded, err := common.Marshal(binding)
	if err != nil {
		return err
	}
	return tx.Create(&AuthFlow{TokenHash: bossPhoneScope.hash("session", session.SID), Purpose: bossPhoneSessionPurpose, Provider: binding.PhoneHash, UserId: session.UserID, SessionId: session.SID, Payload: string(encoded), ExpiresAt: time.Unix(session.ExpiresAt, 0)}).Error
}

// Boss state deliberately bypasses user/session caches and all PAT/model-key
// authentication. Its transaction reads only the primary model.DB handle.
func BossPhoneAuthState(identity AuthSessionIdentity) (*User, *UserSession, error) {
	if identity.UserID <= 0 || identity.SessionID == "" || identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 {
		return nil, nil, ErrBossPhoneUnqualified
	}
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return nil, nil, ErrBossPhoneUnqualified
	}
	var user *User
	var session *UserSession
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		user, err = PhoneAuthUserWithTx(tx, identity.UserID)
		if err != nil {
			return err
		}
		session, err = PhoneAuthSessionWithTx(tx, identity.SessionID)
		if err != nil {
			return err
		}
		if session.UserID != user.Id || session.LoginMethod != BossPhoneLoginMethod || session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() || session.Version != identity.SessionVersion || session.UserAuthVersion != identity.UserAuthVersion || user.AuthVersion != identity.UserAuthVersion {
			return ErrBossPhoneUnqualified
		}
		return ValidateBossPhoneSessionBindingWithTx(tx, user, session)
	})
	return user, session, err
}

func ValidateBossPhoneSessionBindingWithTx(tx *gorm.DB, user *User, session *UserSession) error {
	if session.LoginMethod != BossPhoneLoginMethod || session.UserID != user.Id || session.UserAuthVersion != user.AuthVersion {
		return ErrBossPhoneUnqualified
	}
	var flow AuthFlow
	if err := lockForUpdate(tx).Where("token_hash = ? AND purpose = ? AND user_id = ? AND session_id = ?", bossPhoneScope.hash("session", session.SID), bossPhoneSessionPurpose, user.Id, session.SID).First(&flow).Error; err != nil {
		return err
	}
	var binding BossPhoneBinding
	if !flow.ExpiresAt.After(time.Now()) || flow.ConsumedAt != nil || common.UnmarshalJsonStr(flow.Payload, &binding) != nil || binding.UserID != user.Id || binding.AuthVersion != session.UserAuthVersion {
		return ErrBossPhoneUnqualified
	}
	_, err := ValidateBossPhoneBindingWithTx(tx, &binding)
	return err
}

// ExchangeBossPhoneVerification has the same durable request binding and
// encrypted replay contract as the OTP kernel. It consumes the MFA flow, TOTP
// assertion, relay and session only when the complete action commits.
func ExchangeBossPhoneVerification(token, key, code string, action func(*gorm.DB, *AuthFlow, *User, *BossPhoneBinding) (string, int, string, error), replay func(*gorm.DB, *AuthFlow, string) error) (bool, error) {
	var initial AuthFlow
	if err := DB.Where("token_hash = ? AND purpose = ?", authFlowTokenHash(token), BossPhoneVerificationPurpose).First(&initial).Error; err != nil || initial.UserId <= 0 {
		return false, ErrAuthFlowInvalid
	}
	provider := bossPhoneScope.hash("mfa-flow", token)
	fingerprint := bossPhoneScope.hash("mfa-request", token+":totp:"+code)
	exchangeHash := bossPhoneScope.hash("mfa-key", key)
	encoded, err := common.Marshal(phoneExchangePayload{Fingerprint: fingerprint})
	if err != nil {
		return false, err
	}
	if err := bindPhoneAuthRequest(&AuthFlow{TokenHash: exchangeHash, Purpose: bossPhoneMFAPurpose, Provider: provider, UserId: initial.UserId, Payload: string(encoded), ExpiresAt: time.Now().Add(PhoneLoginReplayTTL)}); err != nil {
		return false, err
	}
	var outcome error
	replayed := false
	err = WithPhoneAuthTransaction("boss-mfa:"+strconv.Itoa(initial.UserId), func(tx *gorm.DB) error {
		// Match the mutation-fence lock order: user, then proof/receipt.
		if _, err := PhoneAuthUserWithTx(tx, initial.UserId); err != nil {
			return err
		}
		var exchange AuthFlow
		if err := lockForUpdate(tx).Where("token_hash = ?", exchangeHash).First(&exchange).Error; err != nil {
			return err
		}
		var payload phoneExchangePayload
		if common.UnmarshalJsonStr(exchange.Payload, &payload) != nil || exchange.Purpose != bossPhoneMFAPurpose || exchange.Provider != provider || payload.Fingerprint != fingerprint {
			return ErrPhoneIdempotencyConflict
		}
		if payload.Invalid {
			return ErrPhoneLoginFactorInvalid
		}
		if !exchange.ExpiresAt.After(time.Now()) {
			return ErrPhoneChallengeConsumed
		}
		if payload.Receipt != "" {
			if exchange.UserId != initial.UserId {
				return ErrAuthFlowInvalid
			}
			if err := replay(tx, &exchange, payload.Receipt); err != nil {
				return err
			}
			replayed = true
			return nil
		}
		var flow AuthFlow
		if err := applyAuthFlowMatch(lockForUpdate(tx), token, AuthFlowMatch{Purpose: BossPhoneVerificationPurpose, Provider: BossPhoneLoginMethod, UserId: initial.UserId}).First(&flow).Error; err != nil {
			return err
		}
		var pending BossPhoneVerificationPayload
		if flow.ConsumedAt != nil || !flow.ExpiresAt.After(time.Now()) || common.UnmarshalJsonStr(flow.Payload, &pending) != nil || pending.Binding.UserID != flow.UserId {
			return ErrAuthFlowInvalid
		}
		user, err := ValidateBossPhoneBindingWithTx(tx, &pending.Binding)
		if err != nil {
			return err
		}
		receipt, userID, sid, err := action(tx, &flow, user, &pending.Binding)
		if errors.Is(err, ErrPhoneLoginFactorInvalid) {
			// Invalid factors make no credential writes. Count once per immutable
			// key in this transaction; downstream failures instead roll back.
			var factor TwoFA
			if err := lockForUpdate(tx).Where("user_id = ? AND is_enabled = ?", user.Id, true).First(&factor).Error; err != nil {
				return err
			}
			if !factor.IsLocked() {
				factor.FailedAttempts++
				if factor.FailedAttempts >= common.MaxFailAttempts {
					until := time.Now().Add(time.Duration(common.LockoutDuration) * time.Second)
					factor.LockedUntil = &until
				}
				if err := tx.Model(&factor).Updates(map[string]any{"failed_attempts": factor.FailedAttempts, "locked_until": factor.LockedUntil}).Error; err != nil {
					return err
				}
			}
			payload.Invalid, outcome = true, ErrPhoneLoginFactorInvalid
		} else if err != nil {
			return err
		} else {
			if userID != user.Id {
				return ErrAuthFlowInvalid
			}
			now := time.Now()
			claimed := tx.Model(&flow).Where("consumed_at IS NULL AND expires_at > ?", now).Update("consumed_at", now)
			if claimed.Error != nil {
				return claimed.Error
			}
			if claimed.RowsAffected != 1 {
				return ErrAuthFlowConsumed
			}
			payload.Receipt, exchange.UserId, exchange.SessionId = receipt, userID, sid
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

// AuthFlow expiry is the Boss phone-binding fence. It commits with the actual
// User mutation, including subscription group changes and hard deletion. The
// existing raw SQL writes are migrations, not user identity changes; existing
// User.UpdateColumn calls change only wallet counters/initialization markers.
// Authentication-sensitive writes must retain these hooks (not SkipHooks/Exec).
func (user *User) BeforeUpdate(tx *gorm.DB) error {
	columns, restricted := tx.Statement.SelectAndOmitColumns(false, true)
	changes := make(map[string]any)
	value := reflect.ValueOf(tx.Statement.Dest)
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	var updates map[string]any
	if value.Kind() == reflect.Map {
		updates, _ = value.Interface().(map[string]any)
	}
	var destination *gorm.Statement
	if value.Kind() == reflect.Struct {
		destination = &gorm.Statement{DB: tx}
		if err := destination.Parse(tx.Statement.Dest); err != nil {
			return err
		}
	}
	for _, name := range []string{"phone_e164", "group", "status", "auth_version", "deleted_at"} {
		selected, explicit := columns[name]
		if explicit && !selected || !explicit && restricted {
			continue
		}
		field := tx.Statement.Schema.LookUpField(name)
		if updates != nil {
			for key, update := range updates {
				if key == field.Name || key == field.DBName {
					if _, duplicate := changes[name]; duplicate {
						// Multiple aliases are conservatively treated like SQL expressions.
						changes[name] = gorm.Expr("NULL")
					} else {
						changes[name] = update
					}
				}
			}
		} else if destination != nil {
			if field := destination.Schema.LookUpField(name); field != nil {
				update, zero := field.ValueOf(tx.Statement.Context, value)
				if selected || !zero {
					changes[name] = update
				}
			}
		}
	}
	if len(changes) == 0 {
		return nil // quota, last_login_at and other ordinary writes are not fences.
	}
	return user.invalidateBossPhoneBindingsWithTx(tx, changes)
}

func (user *User) BeforeDelete(tx *gorm.DB) error {
	return user.invalidateBossPhoneBindingsWithTx(tx, nil)
}

func (user *User) invalidateBossPhoneBindingsWithTx(tx *gorm.DB, changes map[string]any) error {
	query := tx.Session(&gorm.Session{NewDB: true}).Model(&User{})
	if tx.Statement.Unscoped {
		query = query.Unscoped()
	}
	if where, ok := tx.Statement.Clauses["WHERE"]; ok && where.Expression != nil {
		query = query.Clauses(where.Expression)
	} else if user.Id == 0 && !tx.AllowGlobalUpdate {
		return gorm.ErrMissingWhereClause
	}
	if user.Id > 0 {
		query = query.Where("id = ?", user.Id)
	}
	var users []User
	if err := lockForUpdate(query).Select("id", "group", "phone_e164", "status", "auth_version", "deleted_at").Order("id ASC").Find(&users).Error; err != nil {
		return err
	}
	ids := make([]any, 0, len(users))
	for _, current := range users {
		ids = append(ids, current.Id)
		if current.Group != BossPhoneGroup {
			group, writingGroup := changes["group"]
			nextGroup, literal := group.(string)
			if !writingGroup || group == nil || literal && nextGroup != BossPhoneGroup {
				continue
			}
		}
		changed := changes == nil
		for name, update := range changes {
			field := tx.Statement.Schema.LookUpField(name)
			var next User
			if field.Set(tx.Statement.Context, reflect.ValueOf(&next).Elem(), update) != nil {
				changed = true // SQL expressions cannot safely be compared in Go.
				break
			}
			before, _ := field.ValueOf(tx.Statement.Context, reflect.ValueOf(current))
			after, _ := field.ValueOf(tx.Statement.Context, reflect.ValueOf(next))
			changed = changed || !reflect.DeepEqual(before, after)
		}
		if !changed {
			continue
		}
		now := time.Now()
		if err := tx.Session(&gorm.Session{NewDB: true}).Model(&AuthFlow{}).Where("user_id = ? AND purpose IN ? AND expires_at > ?", current.Id, []string{bossPhoneScope.challengePurpose, bossPhoneScope.exchangePurpose, BossPhoneVerificationPurpose, bossPhoneMFAPurpose, bossPhoneSessionPurpose}, now).Update("expires_at", now).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{NewDB: true}).Model(&UserSession{}).Where("user_id = ? AND login_method = ? AND status = ?", current.Id, BossPhoneLoginMethod, UserSessionStatusActive).Updates(map[string]any{"status": UserSessionStatusRevoked, "revoked_at": now.Unix(), "revoked_reason": "boss_binding_changed"}).Error; err != nil {
			return err
		}
	}
	// Pin the write to exactly the rows whose primary-store state was locked,
	// retaining the caller's original predicate even for Model(&User{}) batches.
	tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.IN{Column: clause.Column{Table: clause.CurrentTable, Name: "id"}, Values: ids}}})
	return nil
}
