package model

import "gorm.io/gorm"

// Mail outbox rows belong to the business database and never contain SMTP secrets or chat bodies.
type DemandEmailConfiguration struct {
	ID         int `gorm:"primaryKey"`
	Revision   int64
	ConfigJSON string `gorm:"type:text"`
	UpdatedBy  int
	UpdatedAt  int64 `gorm:"autoUpdateTime:false"`
}
type DemandEmailEvent struct {
	ID          string `gorm:"type:varchar(40);primaryKey"`
	EventKey    string `gorm:"type:varchar(128);uniqueIndex"`
	RequestHash string `gorm:"type:varchar(64)"`
	Domain      string `gorm:"type:varchar(16)"`
	EntityID    string `gorm:"type:varchar(40);index"`
	Type        string `gorm:"type:varchar(32)"`
	ActorID     int
	Number      string `gorm:"type:varchar(64)"`
	Title       string `gorm:"type:varchar(240)"`
	Budget      string `gorm:"type:varchar(160)"`
	TextCount   int64
	ImageCount  int64
	VideoCount  int64
	CreatedAt   int64 `gorm:"autoCreateTime:false"`
}
type DemandEmailDelivery struct {
	ID           string `gorm:"type:varchar(40);primaryKey"`
	Domain       string `gorm:"type:varchar(16)"`
	EntityID     string `gorm:"type:varchar(40);index"`
	Type         string `gorm:"type:varchar(32)"`
	Recipient    string `gorm:"type:varchar(254)"`
	RecipientKey string `gorm:"type:varchar(64);index"`
	Status       string `gorm:"type:varchar(16);index:idx_demand_email_due,priority:1;index:idx_demand_email_lease,priority:1"`
	Number       string `gorm:"type:varchar(64)"`
	Title        string `gorm:"type:varchar(240)"`
	Budget       string `gorm:"type:varchar(160)"`
	EventCount   int64
	AttemptCount int
	DueAt        int64  `gorm:"index:idx_demand_email_due,priority:2"`
	LeaseToken   string `gorm:"type:varchar(40)"`
	LeaseUntil   int64  `gorm:"index:idx_demand_email_lease,priority:2"`
	MessageID    string `gorm:"type:varchar(254)"`
	ErrorCode    string `gorm:"type:varchar(64)"`
	ErrorStage   string `gorm:"type:varchar(32)"`
	PauseReason  string `gorm:"type:varchar(40)"`
	SentAt       int64
	CreatedAt    int64 `gorm:"autoCreateTime:false;index"`
	UpdatedAt    int64 `gorm:"autoUpdateTime:false"`
}

// A committed event has one association per normalized recipient, even when events share a batch.
type DemandEmailEventRecipient struct {
	EventID      string `gorm:"type:varchar(40);primaryKey"`
	RecipientKey string `gorm:"type:varchar(64);primaryKey"`
	DeliveryID   string `gorm:"type:varchar(40);index"`
}
type DemandEmailMergeHead struct {
	ID         string `gorm:"type:varchar(64);primaryKey"`
	DeliveryID string `gorm:"type:varchar(40)"`
}

func DemandEmailTables() []any {
	return []any{&DemandEmailConfiguration{}, &DemandEmailEvent{}, &DemandEmailDelivery{}, &DemandEmailEventRecipient{}, &DemandEmailMergeHead{}}
}
func LockDemandEmailConfig(tx *gorm.DB) (DemandEmailConfiguration, error) {
	var v DemandEmailConfiguration
	err := lockForUpdate(tx).First(&v, 1).Error
	return v, err
}
func LockDemandEmailHead(tx *gorm.DB, id string) (DemandEmailMergeHead, error) {
	var v DemandEmailMergeHead
	err := lockForUpdate(tx).First(&v, "id = ?", id).Error
	return v, err
}

func LockDemandEmailEvent(tx *gorm.DB, key string) (DemandEmailEvent, error) {
	var v DemandEmailEvent
	err := lockForUpdate(tx).First(&v, "event_key = ?", key).Error
	return v, err
}
