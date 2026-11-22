package model

import "gorm.io/gorm"

// One provider upload is owned by its durable content/credential binding. It
// never grants generation or proves an unknown provider operation was absent.
type MediaGenerationUpload struct {
	ID               string `gorm:"primaryKey;type:varchar(64)"`
	AssetID          string `gorm:"type:varchar(64);index"`
	CredentialDigest string `gorm:"type:varchar(64)"`
	State            string `gorm:"type:varchar(16)"`
	URL              string `gorm:"type:text"`
	ProviderFileID   string `gorm:"type:varchar(256)"`
	CreatedAt        int64
	UpdatedAt        int64
}

func MigrateMediaGenerationUpload(db *gorm.DB) error { return db.AutoMigrate(&MediaGenerationUpload{}) }
