package model

import (
	"errors"
	"regexp"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MediaGenerationAsset is a private immutable receipt for actual uploaded bytes.
// Public URLs, credentials and client-supplied media measurements are never its identity.
type MediaGenerationAsset struct {
	ID              string  `gorm:"primaryKey;type:varchar(64)" json:"asset_id"`
	OperationKey    string  `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	ClientAssetID   string  `gorm:"type:varchar(128)" json:"client_asset_id"`
	UserID          int     `gorm:"index" json:"-"`
	TokenID         int     `json:"-"`
	OwnerDigest     string  `gorm:"type:varchar(64)" json:"-"`
	Model           string  `gorm:"type:varchar(191)" json:"model"`
	State           string  `gorm:"type:varchar(16)" json:"state"`
	Kind            string  `gorm:"type:varchar(8)" json:"media_kind"`
	MIMEType        string  `gorm:"type:varchar(128)" json:"mime_type"`
	Bytes           int64   `json:"bytes"`
	SHA256          string  `gorm:"type:varchar(64)" json:"sha256"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	FPS             float64 `json:"fps,omitempty"`
	Format          string  `gorm:"type:varchar(64)" json:"-"`
	ObjectKey       string  `gorm:"type:varchar(128)" json:"-"`
	CreatedAt       int64   `json:"created_at"`
	ExpiresAt       int64   `gorm:"index" json:"expires_at"`
}

var ErrMediaGenerationAssetConflict = errors.New("media asset upload conflicts with its original bytes")
var mediaAssetUUID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{16,128}$`)

func MigrateMediaGenerationAsset(db *gorm.DB) error {
	if db == nil {
		return ErrMediaGenerationQuoteUnavailable
	}
	return db.AutoMigrate(&MediaGenerationAsset{})
}

func MediaGenerationAssetOperationKey(userID, tokenID int, ownerDigest, modelID, clientID string) (string, error) {
	if userID <= 0 || tokenID <= 0 || !mediaGenerationQuoteDigest.MatchString(ownerDigest) || !validClientModelID(modelID) || !mediaAssetUUID.MatchString(clientID) {
		return "", ErrMediaGenerationQuoteInvalid
	}
	b, _ := common.Marshal([]any{userID, tokenID, ownerDigest, modelID, clientID})
	return common.GenerateHMAC("media-v2-asset:" + string(b)), nil
}

// InsertMediaGenerationAsset stores only ready, locally verified content. A
// concurrent same upload returns its original receipt; different bytes conflict.
func InsertMediaGenerationAsset(db *gorm.DB, v MediaGenerationAsset) (*MediaGenerationAsset, error) {
	key, err := MediaGenerationAssetOperationKey(v.UserID, v.TokenID, v.OwnerDigest, v.Model, v.ClientAssetID)
	if err != nil || !mediaGenerationQuoteDigest.MatchString(v.SHA256) || v.Bytes <= 0 || v.Bytes > 50<<20 || v.ObjectKey == "" || v.State != "ready" || (v.Kind != "image" && v.Kind != "video" && v.Kind != "audio") {
		return nil, ErrMediaGenerationQuoteInvalid
	}
	v.OperationKey = key
	v.ID = "ma2_" + uuid.NewString()
	v.CreatedAt = time.Now().Unix()
	v.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&v).Error; err != nil {
		return nil, err
	}
	var actual MediaGenerationAsset
	if err := db.Where("operation_key = ?", key).Take(&actual).Error; err != nil {
		return nil, err
	}
	if actual.SHA256 != v.SHA256 || actual.Bytes != v.Bytes || actual.MIMEType != v.MIMEType || actual.Kind != v.Kind {
		return nil, ErrMediaGenerationAssetConflict
	}
	return &actual, nil
}
