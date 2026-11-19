package model

// Community orders are separate from legacy private demands and engineer profiles.
type CommunityOrder struct {
	ID                    string `gorm:"type:varchar(40);primaryKey"`
	Number                string `gorm:"type:varchar(64);uniqueIndex"`
	PublisherID           int    `gorm:"uniqueIndex:idx_community_create,priority:1;index"`
	ClientRequestID       string `gorm:"type:varchar(80);uniqueIndex:idx_community_create,priority:2"`
	PayloadHash           string `gorm:"type:varchar(64)"`
	AcceptorID            int    `gorm:"index"`
	Title                 string `gorm:"type:varchar(200)"`
	PublicSummary         string `gorm:"type:text"`
	PublicBudget          string `gorm:"type:varchar(100)"`
	ServiceProfileID      string `gorm:"type:varchar(40);index"`
	ServiceProfileName    string `gorm:"type:varchar(100)"`
	PublicAttachmentsJSON string `gorm:"type:text"`
	PrivateText           string `gorm:"type:text"`
	Status                string `gorm:"type:varchar(32);index:idx_community_feed,priority:2"`
	Visible               bool   `gorm:"index:idx_community_feed,priority:1"`
	Revision              int64
	LastSeq               int64
	DeliveryMessageID     string `gorm:"type:varchar(40)"`
	CreatedAt             int64  `gorm:"autoCreateTime:false;index:idx_community_feed,priority:3"`
	UpdatedAt             int64  `gorm:"autoUpdateTime:false"`
	AcceptedAt            int64
	ClosedAt              int64
}

type CommunityMessage struct {
	ID              string `gorm:"type:varchar(40);primaryKey"`
	OrderID         string `gorm:"type:varchar(40);uniqueIndex:idx_community_seq,priority:1"`
	Seq             int64  `gorm:"uniqueIndex:idx_community_seq,priority:2"`
	SenderID        int    `gorm:"uniqueIndex:idx_community_message_key,priority:1"`
	ClientMessageID string `gorm:"type:varchar(128);uniqueIndex:idx_community_message_key,priority:2"`
	PayloadHash     string `gorm:"type:varchar(64)"`
	AuthorKind      string `gorm:"type:varchar(24)"`
	Kind            string `gorm:"type:varchar(24)"`
	Text            string `gorm:"type:text"`
	AttachmentsJSON string `gorm:"type:text"`
	CreatedAt       int64  `gorm:"autoCreateTime:false"`
}

type CommunityReadCursor struct {
	OrderID     string `gorm:"type:varchar(40);primaryKey"`
	UserID      int    `gorm:"primaryKey"`
	LastReadSeq int64
}

type CommunityActionReceipt struct {
	ID             string `gorm:"type:varchar(40);primaryKey"`
	ActorID        int    `gorm:"uniqueIndex:idx_community_action,priority:1"`
	ClientActionID string `gorm:"type:varchar(80);uniqueIndex:idx_community_action,priority:2"`
	OrderID        string `gorm:"type:varchar(40);index"`
	PayloadHash    string `gorm:"type:varchar(64)"`
	MessageID      string `gorm:"type:varchar(40)"`
}

type CommunityAudit struct {
	ID        uint64 `json:"id" gorm:"primaryKey"`
	OrderID   string `json:"order_id" gorm:"type:varchar(40);index"`
	ActorID   int    `json:"actor_id"`
	Action    string `json:"action" gorm:"type:varchar(40)"`
	Reason    string `json:"reason" gorm:"type:text"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type CommunityConfiguration struct {
	ID       int `gorm:"primaryKey"`
	Revision int64
	Enabled  bool
}

type CommunityAttachment struct {
	ID             string `gorm:"type:varchar(40);primaryKey"`
	OwnerID        int    `gorm:"uniqueIndex:idx_community_upload,priority:1"`
	ClientUploadID string `gorm:"type:varchar(80);uniqueIndex:idx_community_upload,priority:2"`
	MetadataHash   string `gorm:"type:varchar(64)"`
	Scope          string `gorm:"type:varchar(24)"`
	OrderID        string `gorm:"type:varchar(40);index"`
	MessageID      string `gorm:"type:varchar(40);index"`
	Name           string `gorm:"type:varchar(255)"`
	Kind           string `gorm:"type:varchar(16)"`
	MimeType       string `gorm:"type:varchar(80)"`
	SizeBytes      int64
	UploadedBytes  int64
	Status         string `gorm:"type:varchar(16)"`
	SHA256         string `gorm:"type:varchar(64)"`
	ObjectKey      string `gorm:"type:varchar(80)"`
	ExpiresAt      int64
	AssemblyToken  string `gorm:"type:varchar(40)"`
	AssemblyUntil  int64
}

type CommunityUploadPart struct {
	ID        string `gorm:"type:varchar(40);primaryKey"`
	UploadID  string `gorm:"type:varchar(40);uniqueIndex:idx_community_part,priority:1"`
	Offset    int64  `gorm:"uniqueIndex:idx_community_part,priority:2"`
	Size      int64
	ObjectKey string `gorm:"type:varchar(80)"`
}

func CommunityTables() []any {
	return []any{&CommunityOrder{}, &CommunityMessage{}, &CommunityReadCursor{}, &CommunityActionReceipt{}, &CommunityAudit{}, &CommunityConfiguration{}, &CommunityAttachment{}, &CommunityUploadPart{}}
}
