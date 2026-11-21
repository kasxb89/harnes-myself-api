package model

// DemandShowcase is an explicitly curated public case, independent of customer orders.
// Control/provenance fields are projected separately from the public catalog DTO.
type DemandShowcase struct {
	ID                string `gorm:"type:varchar(40);primaryKey"`
	CreatorID         int    `gorm:"uniqueIndex:idx_showcase_create,priority:1"`
	ClientRequestID   string `gorm:"type:varchar(40);uniqueIndex:idx_showcase_create,priority:2"`
	PayloadHash       string `gorm:"type:varchar(64)"`
	Title             string `gorm:"type:varchar(200)"`
	Description       string `gorm:"type:text"`
	Category          string `gorm:"type:varchar(16);index"`
	CoverAttachmentID string `gorm:"type:varchar(40)"`
	MediaJSON         string `gorm:"type:text"`
	SourceName        string `gorm:"type:varchar(100)"`
	SourceURL         string `gorm:"type:text"`
	Attribution       string `gorm:"type:text"`
	Published         bool   `gorm:"index"`
	Synthetic         bool
	SeedBatch         string `gorm:"type:varchar(80);index"`
	Provenance        string `gorm:"type:text"`
	Revision          int64
	CreatedAt         int64 `gorm:"autoCreateTime:false"`
	UpdatedAt         int64 `gorm:"autoUpdateTime:false"`
}

type DemandShowcaseAction struct {
	ShowcaseID     string `gorm:"type:varchar(40);primaryKey"`
	ActorID        int    `gorm:"primaryKey"`
	ClientActionID string `gorm:"type:varchar(40);primaryKey"`
	PayloadHash    string `gorm:"type:varchar(64)"`
}

func DemandShowcaseTables() []any { return []any{&DemandShowcase{}, &DemandShowcaseAction{}} }
