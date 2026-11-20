package model

import "gorm.io/gorm"

// Forum content is independent of AI sessions, tasks, wallets and login state.
type ForumPost struct {
	ID              string `gorm:"type:varchar(40);primaryKey"`
	AuthorID        int    `gorm:"uniqueIndex:idx_forum_post_request,priority:1;index"`
	ClientRequestID string `gorm:"type:varchar(80);uniqueIndex:idx_forum_post_request,priority:2"`
	PayloadHash     string `gorm:"type:varchar(64)"`
	Title           string `gorm:"type:varchar(200)"`
	Body            string `gorm:"type:text"`
	CreatedAt       int64  `gorm:"autoCreateTime:false;index:idx_forum_post_feed,priority:2"`
	ReviewedAt      int64  `gorm:"index"`
	ReviewedBy      int
	DeletedAt       int64 `gorm:"index:idx_forum_post_feed,priority:1"`
	DeletedBy       int
	DeleteReason    string `gorm:"type:varchar(1000)"`
}

type ForumComment struct {
	ID               string `gorm:"type:varchar(40);primaryKey"`
	PostID           string `gorm:"type:varchar(40);index:idx_forum_comment_feed,priority:1"`
	AuthorID         int    `gorm:"uniqueIndex:idx_forum_comment_request,priority:1"`
	ClientRequestID  string `gorm:"type:varchar(80);uniqueIndex:idx_forum_comment_request,priority:2"`
	PayloadHash      string `gorm:"type:varchar(64)"`
	Body             string `gorm:"type:text"`
	ReplyToCommentID string `gorm:"type:varchar(40)"`
	CreatedAt        int64  `gorm:"autoCreateTime:false;index:idx_forum_comment_feed,priority:3"`
	ReviewedAt       int64  `gorm:"index"`
	ReviewedBy       int
	DeletedAt        int64 `gorm:"index:idx_forum_comment_feed,priority:2"`
	DeletedBy        int
	DeleteReason     string `gorm:"type:varchar(1000)"`
}

type ForumLike struct {
	PostID    string `gorm:"type:varchar(40);primaryKey"`
	UserID    int    `gorm:"primaryKey"`
	CreatedAt int64  `gorm:"autoCreateTime:false"`
}

type ForumAudit struct {
	ID         uint64 `json:"id" gorm:"primaryKey"`
	TargetType string `json:"target_type" gorm:"type:varchar(20);index:idx_forum_audit_target,priority:1"`
	TargetID   string `json:"target_id" gorm:"type:varchar(40);index:idx_forum_audit_target,priority:2"`
	ActorID    int    `json:"actor_id"`
	Action     string `json:"action" gorm:"type:varchar(32)"`
	Reason     string `json:"reason" gorm:"type:varchar(1000)"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type ForumConfiguration struct {
	ID       int `gorm:"primaryKey"`
	Revision int64
	Enabled  bool
}

func ForumTables() []any {
	return []any{&ForumPost{}, &ForumComment{}, &ForumLike{}, &ForumAudit{}, &ForumConfiguration{}, &ForumProfile{}, &ForumNotification{}, &ForumAttachment{}, &ForumUploadPart{}}
}

// Forum mutations lock the parent before changing child comments or likes.
func LockForumPost(tx *gorm.DB, id string) (ForumPost, error) {
	var p ForumPost
	err := lockForUpdate(tx).Where("id = ?", id).First(&p).Error
	return p, err
}

// Forum profiles are public identities, independent of account/login profiles.
type ForumProfile struct {
	UserID      int    `gorm:"primaryKey"`
	DisplayName string `gorm:"type:varchar(160)"`
	Revision    int64
}

type ForumNotification struct {
	ID          uint64 `gorm:"primaryKey"`
	RecipientID int    `gorm:"uniqueIndex:idx_forum_notice_event,priority:1;index:idx_forum_notice_feed,priority:1"`
	EventKey    string `gorm:"type:varchar(100);uniqueIndex:idx_forum_notice_event,priority:2"`
	Kind        string `gorm:"type:varchar(16)"`
	ActorID     int
	PostID      string `gorm:"type:varchar(40);index"`
	CommentID   string `gorm:"type:varchar(40)"`
	CreatedAt   int64  `gorm:"autoCreateTime:false"`
	ReadAt      int64
	Active      bool
}

type ForumAttachment struct {
	ID             string `gorm:"type:varchar(40);primaryKey"`
	OwnerID        int    `gorm:"uniqueIndex:idx_forum_upload,priority:1"`
	ClientUploadID string `gorm:"type:varchar(80);uniqueIndex:idx_forum_upload,priority:2"`
	MetadataHash   string `gorm:"type:varchar(64)"`
	TargetKind     string `gorm:"type:varchar(16)"`
	PostID         string `gorm:"type:varchar(40);index"`
	CommentID      string `gorm:"type:varchar(40)"`
	Position       int
	Bound          bool
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
type ForumUploadPart struct {
	ID        string `gorm:"type:varchar(40);primaryKey"`
	UploadID  string `gorm:"type:varchar(40);uniqueIndex:idx_forum_part,priority:1"`
	Offset    int64  `gorm:"uniqueIndex:idx_forum_part,priority:2"`
	Size      int64
	ObjectKey string `gorm:"type:varchar(80)"`
}
