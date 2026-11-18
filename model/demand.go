package model

// Demand tables are independent of AI sessions, tasks, quotas and authentication.
// String identifiers are opaque; sequence/revision fields are bounded by the API.
type Demand struct {
	ID              string `json:"id" gorm:"type:varchar(40);primaryKey"`
	Number          string `json:"number" gorm:"type:varchar(50);uniqueIndex"`
	UserID          int    `json:"user_id" gorm:"index;uniqueIndex:idx_demand_create,priority:1"`
	ClientRequestID string `json:"-" gorm:"type:varchar(80);uniqueIndex:idx_demand_create,priority:2"`
	CreateHash      string `json:"-" gorm:"type:varchar(64)"`
	EngineerID      string `json:"engineer_id" gorm:"type:varchar(40);index"`
	Title           string `json:"title" gorm:"type:varchar(200)"`
	Budget          string `json:"budget" gorm:"type:varchar(100)"`
	ExpectedDate    string `json:"expected_date" gorm:"type:varchar(10)"`
	Status          string `json:"status" gorm:"type:varchar(32);index"`
	Priority        string `json:"priority" gorm:"type:varchar(16)"`
	Revision        int64  `json:"revision"`
	LastSeq         int64  `json:"last_seq"`
	Preview         string `json:"preview" gorm:"type:varchar(300)"`
	LastMessageAt   int64  `json:"last_message_at"`
	DeliveryID      string `json:"delivery_id" gorm:"type:varchar(40)"`
	ClientVersion   string `json:"client_version,omitempty" gorm:"type:varchar(100)"`
	ClientPlatform  string `json:"client_platform,omitempty" gorm:"type:varchar(100)"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime:false;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime:false;index"`
}

type DemandEngineer struct {
	ID                 string `json:"id" gorm:"type:varchar(40);primaryKey"`
	UserID             int    `json:"user_id" gorm:"uniqueIndex"`
	DisplayName        string `json:"display_name" gorm:"type:varchar(100)"`
	AvatarAttachmentID string `json:"avatar_attachment_id" gorm:"type:varchar(40)"`
	CategoryJSON       string `json:"-" gorm:"type:text"`
	Introduction       string `json:"introduction" gorm:"type:text"`
	ServiceHours       string `json:"service_hours" gorm:"type:varchar(300)"`
	Published          bool   `json:"published"`
	Enabled            bool   `json:"enabled"`
	Accepting          bool   `json:"accepting"`
	Capacity           int    `json:"capacity"`
	OpenDemandCount    int    `json:"open_demand_count"`
	Sort               int    `json:"sort"`
	LastHeartbeat      int64  `json:"-"`
	Revision           int64  `json:"revision"`
}

type DemandMessage struct {
	ID              string `json:"id" gorm:"type:varchar(40);primaryKey"`
	DemandID        string `json:"demand_id" gorm:"type:varchar(40);uniqueIndex:idx_demand_seq,priority:1;uniqueIndex:idx_demand_send,priority:1"`
	Seq             int64  `json:"seq" gorm:"uniqueIndex:idx_demand_seq,priority:2"`
	SenderID        int    `json:"sender_id" gorm:"uniqueIndex:idx_demand_send,priority:2"`
	ClientMessageID string `json:"client_message_id" gorm:"type:varchar(80);uniqueIndex:idx_demand_send,priority:3"`
	PayloadHash     string `json:"-" gorm:"type:varchar(64)"`
	Author          string `json:"author" gorm:"type:varchar(16)"`
	Kind            string `json:"kind" gorm:"type:varchar(16)"`
	Text            string `json:"text" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type DemandReadCursor struct {
	DemandID    string `json:"demand_id" gorm:"type:varchar(40);primaryKey"`
	UserID      int    `json:"user_id" gorm:"primaryKey"`
	LastReadSeq int64  `json:"last_read_seq"`
}

type DemandAudit struct {
	ID        uint64 `json:"id" gorm:"primaryKey"`
	DemandID  string `json:"demand_id" gorm:"type:varchar(40);index"`
	ActorID   int    `json:"actor_id"`
	Action    string `json:"action" gorm:"type:varchar(40)"`
	Detail    string `json:"detail" gorm:"type:text"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type DemandActionReceipt struct {
	DemandID       string `gorm:"type:varchar(40);primaryKey"`
	ActorID        int    `gorm:"primaryKey"`
	ClientActionID string `gorm:"type:varchar(80);primaryKey"`
	PayloadHash    string `gorm:"type:varchar(64)"`
}

type DemandConfiguration struct {
	ID       int `gorm:"primaryKey"`
	Revision int64
	Document string `gorm:"type:text"`
}

type DemandAttachment struct {
	ID             string `json:"id" gorm:"type:varchar(40);primaryKey"`
	OwnerID        int    `json:"-" gorm:"uniqueIndex:idx_demand_upload,priority:1"`
	ClientUploadID string `json:"-" gorm:"type:varchar(80);uniqueIndex:idx_demand_upload,priority:2"`
	MetadataHash   string `json:"-" gorm:"type:varchar(64)"`
	DemandID       string `json:"demand_id,omitempty" gorm:"type:varchar(40);index"`
	MessageID      string `json:"-" gorm:"type:varchar(40);index"`
	Name           string `json:"name" gorm:"type:varchar(240)"`
	Kind           string `json:"kind" gorm:"type:varchar(16)"`
	MimeType       string `json:"mime_type" gorm:"type:varchar(80)"`
	SizeBytes      int64  `json:"size_bytes"`
	UploadedBytes  int64  `json:"uploaded_bytes"`
	Status         string `json:"status" gorm:"type:varchar(16)"`
	SHA256         string `json:"sha256,omitempty" gorm:"type:varchar(64)"`
	ObjectKey      string `json:"-" gorm:"type:varchar(80)"`
	ExpiresAt      int64  `json:"expires_at"`
	AssemblyToken  string `json:"-" gorm:"type:varchar(40)"`
	AssemblyUntil  int64  `json:"-"`
}

type DemandUploadPart struct {
	ID        string `gorm:"type:varchar(40);primaryKey"`
	UploadID  string `gorm:"type:varchar(40);uniqueIndex:idx_demand_part,priority:1"`
	Offset    int64  `gorm:"uniqueIndex:idx_demand_part,priority:2"`
	Size      int64
	ObjectKey string `gorm:"type:varchar(80)"`
}

func DemandTables() []interface{} {
	return []interface{}{&Demand{}, &DemandEngineer{}, &DemandMessage{}, &DemandReadCursor{}, &DemandAudit{}, &DemandActionReceipt{}, &DemandConfiguration{}, &DemandAttachment{}, &DemandUploadPart{}}
}
