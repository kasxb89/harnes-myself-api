package model

// Service-gallery assets are an explicitly public-capable upload domain. The
// existing profile remains a platform service entry, never a user identity.
type DemandServiceGallery struct {
	ProfileID         string `gorm:"type:varchar(40);primaryKey"`
	Revision          int64
	CoverAttachmentID string `gorm:"type:varchar(40)"`
	MediaJSON         string `gorm:"type:text"`
}

type DemandServiceGalleryReceipt struct {
	ActorID        int    `gorm:"primaryKey"`
	ClientActionID string `gorm:"type:varchar(80);primaryKey"`
	ProfileID      string `gorm:"type:varchar(40)"`
	PayloadHash    string `gorm:"type:varchar(64)"`
}

func DemandServiceGalleryTables() []any {
	return []any{&DemandServiceGallery{}, &DemandServiceGalleryReceipt{}}
}
