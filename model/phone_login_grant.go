package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PhoneLoginGrant is an operator-approved, exact-number alternative to SMS.
// Only a purpose-bound, peppered bcrypt digest is persisted. Issuing a new
// grant invalidates the previous code for this phone, not its sessions.
type PhoneLoginGrant struct {
	PhoneE164 string `json:"phone_e164" gorm:"primaryKey;type:varchar(32)"`
	GrantID   string `json:"-" gorm:"type:varchar(64);not null"`
	CodeHash  string `json:"-" gorm:"type:varchar(128);not null"`
	IssuedBy  int    `json:"issued_by"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at"`
	RevokedAt int64  `json:"revoked_at"`
	Attempts  int    `json:"attempts"`
}

const PhoneLoginGrantTTL = 10 * time.Minute

// Public dummy bcrypt digest; never an account credential. Rejected or unknown
// numbers still perform the same work instead of exposing grant existence.
const phoneGrantDummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

var ErrPhoneGrantNotAllowed = errors.New("manual phone login is restricted to ordinary international accounts")

func IssuePhoneLoginGrant(phone string, identity AuthSessionIdentity) (*PhoneLoginGrant, string, error) {
	if !common.IsManualLoginPhone(phone) {
		return nil, "", ErrPhoneGrantNotAllowed
	}
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		return nil, "", err
	}
	id, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return nil, "", err
	}
	digest, err := common.Password2Hash(phoneAuthHash("manual-code", phone+":"+id+":"+code))
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	grant := PhoneLoginGrant{PhoneE164: phone, GrantID: id, CodeHash: digest, IssuedBy: identity.UserID, IssuedAt: now.Unix(), ExpiresAt: now.Add(PhoneLoginGrantTTL).Unix()}
	err = WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		if err := validatePhoneGrantOperator(tx, identity); err != nil {
			return err
		}
		var user User
		err := lockForUpdate(tx).Where("phone_e164 = ?", phone).First(&user).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && user.Role != common.RoleCommonUser {
			return ErrPhoneGrantNotAllowed
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "phone_e164"}}, UpdateAll: true}).Create(&grant).Error
	})
	if err != nil {
		return nil, "", err
	}
	return &grant, code, nil
}

func RevokePhoneLoginGrant(phone string, identity AuthSessionIdentity) error {
	return WithPhoneAuthTransaction(phone, func(tx *gorm.DB) error {
		if err := validatePhoneGrantOperator(tx, identity); err != nil {
			return err
		}
		return tx.Model(&PhoneLoginGrant{}).Where("phone_e164 = ?", phone).Updates(map[string]any{"revoked_at": time.Now().Unix(), "code_hash": ""}).Error
	})
}

// consumePhoneLoginGrant runs under the same per-phone database guard as the
// challenge and session transaction. Attempts cannot reset with a new challenge.
func consumePhoneLoginGrant(tx *gorm.DB, phone, code string) (bool, error) {
	var grant PhoneLoginGrant
	err := tx.Where("phone_e164 = ?", phone).First(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ValidatePasswordAndHash(phoneAuthHash("manual-code", phone+":"+code), phoneGrantDummyHash)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if grant.RevokedAt != 0 || grant.UsedAt != 0 || grant.ExpiresAt <= time.Now().Unix() || grant.Attempts >= phoneCodeAttemptLimit {
		common.ValidatePasswordAndHash(phoneAuthHash("manual-code", phone+":"+code), phoneGrantDummyHash)
		return false, nil
	}
	valid := common.ValidatePasswordAndHash(phoneAuthHash("manual-code", phone+":"+grant.GrantID+":"+code), grant.CodeHash)
	updates := map[string]any{"attempts": grant.Attempts + 1}
	if !valid {
		return false, tx.Model(&grant).Updates(updates).Error
	}
	var user User
	err = lockForUpdate(tx).Where("phone_e164 = ?", phone).First(&user).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err == nil && user.Role != common.RoleCommonUser {
		return false, nil
	}
	updates["used_at"] = time.Now().Unix()
	return true, tx.Model(&grant).Updates(updates).Error
}

func validatePhoneGrantOperator(tx *gorm.DB, identity AuthSessionIdentity) error {
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	var user User
	if err := lockForUpdate(tx).First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if user.Role != common.RoleRootUser {
		return ErrPhoneGrantNotAllowed
	}
	return nil
}
