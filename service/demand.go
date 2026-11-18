package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DemandError struct {
	Status  int
	Code    string
	Message string
}

func (e *DemandError) Error() string { return e.Message }
func demandError(status int, code, message string) error {
	return &DemandError{status, "DEMAND_" + code, message}
}

var demandKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)
var demandCategoryKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var demandDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

const demandMaxSequence int64 = 9007199254740990

type DemandActor struct {
	UserID int
	Admin  bool
}
type DemandCategory struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
type DemandUploadRules struct {
	MaxImageBytes    int64    `json:"max_image_bytes"`
	MaxVideoBytes    int64    `json:"max_video_bytes"`
	MaxAttachments   int      `json:"max_attachments"`
	ChunkBytes       int64    `json:"chunk_bytes"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
	UploadTTLSeconds int64    `json:"upload_ttl_seconds"`
}
type DemandConfig struct {
	Enabled     bool              `json:"enabled"`
	Categories  []DemandCategory  `json:"categories"`
	UploadRules DemandUploadRules `json:"upload_rules"`
}
type DemandConfigVersion struct {
	Revision int64        `json:"revision"`
	Config   DemandConfig `json:"config"`
}

func DefaultDemandConfig() DemandConfig {
	return DemandConfig{Categories: []DemandCategory{}, UploadRules: DemandUploadRules{20 << 20, 512 << 20, 8, 4 << 20, []string{"image/jpeg", "image/png", "image/webp", "video/mp4", "video/webm"}, 86400}}
}
func LoadDemandConfig(db *gorm.DB) (DemandConfigVersion, error) {
	var r model.DemandConfiguration
	err := db.First(&r, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DemandConfigVersion{Config: DefaultDemandConfig()}, nil
	}
	if err != nil {
		return DemandConfigVersion{}, err
	}
	v := DemandConfigVersion{Revision: r.Revision}
	err = common.Unmarshal([]byte(r.Document), &v.Config)
	return v, err
}
func SaveDemandConfig(db *gorm.DB, v DemandConfigVersion) (DemandConfigVersion, error) {
	r := v.Config.UploadRules
	if len(v.Config.Categories) > 100 || r.MaxImageBytes < 1 || r.MaxImageBytes > 50<<20 || r.MaxVideoBytes < 1 || r.MaxVideoBytes > 2<<30 || r.MaxAttachments < 1 || r.MaxAttachments > 20 || r.ChunkBytes < 64<<10 || r.ChunkBytes > 8<<20 || r.UploadTTLSeconds < 300 || r.UploadTTLSeconds > 7*86400 || len(r.AllowedMimeTypes) == 0 {
		return v, demandError(400, "INVALID_INPUT", "Invalid demand configuration")
	}
	seen := map[string]bool{}
	for _, c := range v.Config.Categories {
		if !demandCategoryKey.MatchString(c.ID) || len(c.Name) == 0 || utf8.RuneCountInString(c.Name) > 100 || seen[c.ID] {
			return v, demandError(400, "INVALID_INPUT", "Invalid demand category")
		}
		seen[c.ID] = true
	}
	for _, m := range r.AllowedMimeTypes {
		if !slices.Contains(DefaultDemandConfig().UploadRules.AllowedMimeTypes, m) {
			return v, demandError(400, "UPLOAD_UNSUPPORTED", "Unsupported demand attachment type")
		}
	}
	raw, _ := common.Marshal(v.Config)
	err := db.Transaction(func(tx *gorm.DB) error {
		if v.Revision == 0 {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.DemandConfiguration{ID: 1, Revision: 1, Document: string(raw)})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return demandError(409, "REVISION_CONFLICT", "Demand configuration changed; reload before saving")
			}
			return nil
		}
		res := tx.Model(&model.DemandConfiguration{}).Where("id = ? AND revision = ?", 1, v.Revision).Updates(map[string]any{"document": string(raw), "revision": v.Revision + 1})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return demandError(409, "REVISION_CONFLICT", "Demand configuration changed; reload before saving")
		}
		return nil
	})
	if err == nil {
		v.Revision++
	}
	return v, err
}

type DemandEngineerView struct {
	ID                 string   `json:"id"`
	UserID             int      `json:"user_id,omitempty"`
	DisplayName        string   `json:"display_name"`
	AvatarAttachmentID string   `json:"avatar_attachment_id"`
	CategoryIDs        []string `json:"category_ids"`
	Introduction       string   `json:"introduction"`
	ServiceHours       string   `json:"service_hours"`
	Published          bool     `json:"published"`
	Enabled            bool     `json:"enabled"`
	Accepting          bool     `json:"accepting"`
	Capacity           int      `json:"capacity"`
	Sort               int      `json:"sort"`
	Revision           int64    `json:"revision"`
	Presence           string   `json:"presence"`
	CanStart           bool     `json:"can_start"`
	OpenDemandCount    int      `json:"open_demand_count"`
}

func DemandEngineerPublic(e model.DemandEngineer, admin bool) DemandEngineerView {
	categories := []string{}
	_ = common.Unmarshal([]byte(e.CategoryJSON), &categories)
	if categories == nil {
		categories = []string{}
	}
	presence := "offline"
	if e.Enabled && e.LastHeartbeat > time.Now().Unix()-90 {
		presence = "busy"
		if e.Accepting && e.OpenDemandCount < e.Capacity {
			presence = "online"
		}
	}
	v := DemandEngineerView{ID: e.ID, DisplayName: e.DisplayName, AvatarAttachmentID: e.AvatarAttachmentID, CategoryIDs: categories, Introduction: e.Introduction, ServiceHours: e.ServiceHours, Published: e.Published, Enabled: e.Enabled, Accepting: e.Accepting, Capacity: e.Capacity, Sort: e.Sort, Revision: e.Revision, Presence: presence, CanStart: e.Enabled && e.Published && e.Accepting && e.OpenDemandCount < e.Capacity, OpenDemandCount: e.OpenDemandCount}
	if admin {
		v.UserID = e.UserID
	}
	return v
}
func SaveDemandEngineer(db *gorm.DB, id string, in DemandEngineerView) (DemandEngineerView, error) {
	if utf8.RuneCountInString(in.DisplayName) < 1 || utf8.RuneCountInString(in.DisplayName) > 100 || utf8.RuneCountInString(in.Introduction) > 2000 || len(in.ServiceHours) > 300 || in.Capacity < 1 || in.Capacity > 1000 || len(in.CategoryIDs) > 30 {
		return in, demandError(400, "INVALID_INPUT", "Invalid engineer profile")
	}
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return in, err
	}
	for _, category := range in.CategoryIDs {
		if !slices.ContainsFunc(cfg.Config.Categories, func(c DemandCategory) bool { return c.ID == category }) {
			return in, demandError(400, "INVALID_INPUT", "Invalid demand category")
		}
	}
	var user model.User
	if err = db.Select("id", "role", "status").First(&user, in.UserID).Error; err != nil || user.Status != common.UserStatusEnabled || user.Role != common.RoleCommonUser {
		return in, demandError(400, "INVALID_INPUT", "Engineer must be an enabled ordinary user")
	}
	raw, _ := common.Marshal(in.CategoryIDs)
	e := model.DemandEngineer{ID: id, UserID: in.UserID, DisplayName: in.DisplayName, AvatarAttachmentID: in.AvatarAttachmentID, CategoryJSON: string(raw), Introduction: in.Introduction, ServiceHours: in.ServiceHours, Published: in.Published, Enabled: in.Enabled, Accepting: in.Accepting, Capacity: in.Capacity, Sort: in.Sort, Revision: in.Revision + 1}
	if err = validateDemandProfileAvatar(db, in.AvatarAttachmentID, "public"); err != nil {
		return in, err
	}

	if id == "" {
		e.ID = uuid.NewString()
		err = db.Create(&e).Error
	} else {
		var old model.DemandEngineer
		if err = db.First(&old, "id = ?", id).Error; err != nil {
			return in, demandError(404, "NOT_FOUND", "Engineer not found")
		}
		if old.UserID != in.UserID {
			return in, demandError(400, "INVALID_INPUT", "Engineer account cannot be replaced")
		}
		res := db.Model(&model.DemandEngineer{}).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{"display_name": e.DisplayName, "avatar_attachment_id": e.AvatarAttachmentID, "category_json": e.CategoryJSON, "introduction": e.Introduction, "service_hours": e.ServiceHours, "published": e.Published, "enabled": e.Enabled, "accepting": e.Accepting, "capacity": e.Capacity, "sort": e.Sort, "revision": e.Revision})
		err = res.Error
		if err == nil && res.RowsAffected != 1 {
			err = demandError(409, "REVISION_CONFLICT", "Engineer profile changed; reload before saving")
		}
		e.OpenDemandCount = old.OpenDemandCount
		e.LastHeartbeat = old.LastHeartbeat
	}
	return DemandEngineerPublic(e, true), err
}
func DemandEngineerForUser(db *gorm.DB, userID int) (model.DemandEngineer, error) {
	var e model.DemandEngineer
	err := db.Where("user_id = ? AND enabled = ?", userID, true).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = demandError(403, "ENGINEER_REQUIRED", "Engineer access is not enabled")
	}
	return e, err
}
func DemandAccess(db *gorm.DB, a DemandActor, id string) (model.Demand, error) {
	var d model.Demand
	err := db.First(&d, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return d, demandError(404, "NOT_FOUND", "Demand not found")
	}
	if err != nil {
		return d, err
	}
	if a.Admin || d.UserID == a.UserID {
		return d, nil
	}
	e, err := DemandEngineerForUser(db, a.UserID)
	if err == nil && e.ID == d.EngineerID {
		return d, nil
	}
	return d, demandError(404, "NOT_FOUND", "Demand not found")
}

type DemandView struct {
	model.Demand
	Engineer    DemandEngineerView `json:"engineer"`
	IntakeMode  string             `json:"intake_mode"`
	Assigned    bool               `json:"assigned"`
	LastReadSeq int64              `json:"last_read_seq"`
	UnreadCount int64              `json:"unread_count"`
	Owner       *DemandOwner       `json:"owner,omitempty"`
}
type DemandOwner struct {
	ID       int     `json:"id"`
	Nickname string  `json:"nickname"`
	Phone    *string `json:"phone"`
}

func ProjectDemand(db *gorm.DB, a DemandActor, d model.Demand) (DemandView, error) {
	v := DemandView{Demand: d}
	v.IntakeMode = "direct"
	v.Assigned = d.EngineerID != ""
	if d.ServiceProfileID != "" {
		v.IntakeMode = "platform"
	}
	if d.EngineerID == "" {
		v.Demand.EngineerID = "platform-intake"
		v.Engineer = DemandEngineerView{ID: "platform-intake", DisplayName: "平台统一接待", CategoryIDs: []string{}, Introduction: "由平台审核需求后分派工程师", Published: true, Enabled: true, Revision: 1, Presence: "offline"}
	} else {
		var e model.DemandEngineer
		if err := db.First(&e, "id = ?", d.EngineerID).Error; err != nil {
			return v, err
		}
		v.Engineer = DemandEngineerPublic(e, a.Admin)
	}
	var cursor model.DemandReadCursor
	err := db.Where("demand_id = ? AND user_id = ?", d.ID, a.UserID).Find(&cursor).Error
	if err != nil {
		return v, err
	}
	v.LastReadSeq = cursor.LastReadSeq
	if err = db.Model(&model.DemandMessage{}).Where("demand_id = ? AND seq > ? AND sender_id <> ?", d.ID, cursor.LastReadSeq, a.UserID).Count(&v.UnreadCount).Error; err != nil {
		return v, err
	}
	if a.Admin || a.UserID != d.UserID {
		var u model.User
		if err = db.Select("id", "display_name", "phone_e164").First(&u, d.UserID).Error; err != nil {
			return v, err
		}
		v.Owner = &DemandOwner{u.Id, u.DisplayName, u.PhoneE164}
	}
	return v, nil
}

type DemandMessageView struct {
	model.DemandMessage
	Attachments []model.DemandAttachment `json:"attachments"`
	Read        bool                     `json:"read"`
	AuthorName  string                   `json:"author_name,omitempty"`
}

// ProjectDemandMessages batches attachments, author profiles and read cursors for one page.
func ProjectDemandMessages(db *gorm.DB, a DemandActor, d model.Demand, messages []model.DemandMessage) ([]DemandMessageView, int64, error) {
	recipient := d.UserID
	if recipient == a.UserID && d.EngineerID == "" {
		recipient = 0
	}
	if recipient == a.UserID {
		var engineer model.DemandEngineer
		if err := db.Select("user_id").First(&engineer, "id = ?", d.EngineerID).Error; err != nil {
			return nil, 0, err
		}
		recipient = engineer.UserID
	}
	var cursor model.DemandReadCursor
	if err := db.Where("demand_id = ? AND user_id = ?", d.ID, recipient).Find(&cursor).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	authorIDs := make([]int, 0)
	seenAuthors := make(map[int]bool)
	for _, message := range messages {
		if message.Author == "engineer" && message.SenderID > 0 && !seenAuthors[message.SenderID] {
			seenAuthors[message.SenderID] = true
			authorIDs = append(authorIDs, message.SenderID)
		}
	}
	authorNames := make(map[int]string)
	if len(authorIDs) > 0 {
		var profiles []model.DemandEngineer
		// History follows its sender, including an unpublished/disabled former
		// assignee. Select public profile names only, never user contact fields.
		if err := db.Select("user_id", "display_name").Where("user_id IN ?", authorIDs).Find(&profiles).Error; err != nil {
			return nil, 0, err
		}
		for _, profile := range profiles {
			name := strings.TrimSpace(profile.DisplayName)
			if name == "" || !utf8.ValidString(name) || len(utf16.Encode([]rune(name))) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
				continue
			}
			authorNames[profile.UserID] = name
		}
	}
	attachments := []model.DemandAttachment{}
	if len(ids) > 0 {
		if err := db.Where("message_id IN ?", ids).Order("id").Find(&attachments).Error; err != nil {
			return nil, 0, err
		}
	}
	byMessage := make(map[string][]model.DemandAttachment)
	for _, attachment := range attachments {
		byMessage[attachment.MessageID] = append(byMessage[attachment.MessageID], attachment)
	}
	result := make([]DemandMessageView, 0, len(messages))
	for _, message := range messages {
		files := byMessage[message.ID]
		if files == nil {
			files = []model.DemandAttachment{}
		}
		name := ""
		if message.Author == "engineer" {
			name = authorNames[message.SenderID]
		}
		result = append(result, DemandMessageView{DemandMessage: message, Attachments: files, Read: message.SenderID == a.UserID && cursor.LastReadSeq >= message.Seq, AuthorName: name})
	}
	return result, cursor.LastReadSeq, nil
}
func ProjectDemandMessage(db *gorm.DB, a DemandActor, d model.Demand, m model.DemandMessage) (DemandMessageView, error) {
	messages, _, err := ProjectDemandMessages(db, a, d, []model.DemandMessage{m})
	if err != nil {
		return DemandMessageView{}, err
	}
	return messages[0], nil
}

type DemandSend struct {
	ClientMessageID string   `json:"client_message_id"`
	Text            string   `json:"text"`
	AttachmentIDs   []string `json:"attachment_ids"`
}
type DemandCreate struct {
	ClientRequestID  string   `json:"client_request_id"`
	EngineerID       string   `json:"engineer_id"`
	ServiceProfileID string   `json:"service_profile_id,omitempty"`
	Text             string   `json:"text"`
	AttachmentIDs    []string `json:"attachment_ids"`
	Title            string   `json:"title"`
	Budget           string   `json:"budget"`
	ExpectedDate     string   `json:"expected_date"`
	ClientVersion    string   `json:"client_version"`
	ClientPlatform   string   `json:"client_platform"`
}
type DemandSendReceipt struct {
	Demand   DemandView        `json:"demand"`
	Message  DemandMessageView `json:"message"`
	Replayed bool              `json:"replayed"`
}

func demandHash(v any) string {
	b, _ := common.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validateDemandSend(in DemandSend, cfg DemandConfig) error {
	if !demandKey.MatchString(in.ClientMessageID) || utf8.RuneCountInString(in.Text) > 10000 || len(in.AttachmentIDs) > cfg.UploadRules.MaxAttachments {
		return demandError(400, "INVALID_INPUT", "Invalid demand message")
	}
	if strings.TrimSpace(in.Text) == "" && len(in.AttachmentIDs) == 0 {
		return demandError(400, "EMPTY_MESSAGE", "Enter a message or attach an image or video")
	}
	seen := map[string]bool{}
	for _, id := range in.AttachmentIDs {
		if seen[id] {
			return demandError(400, "UPLOAD_INVALID", "Duplicate demand attachment")
		}
		seen[id] = true
	}
	return nil
}
func bindDemandAttachments(tx *gorm.DB, a DemandActor, d model.Demand, messageID string, ids []string) error {
	for _, id := range ids {
		var v model.DemandAttachment
		if err := tx.First(&v, "id = ?", id).Error; err != nil || v.OwnerID != a.UserID || v.Status != "ready" {
			return demandError(400, "UPLOAD_INVALID", "Attachment is not ready or not owned by this account")
		}
		if v.DemandID != "" || v.MessageID != "" {
			return demandError(409, "ATTACHMENT_IN_USE", "Attachment is already attached to a message")
		}
		reserved, err := demandAvatarReserved(tx, id)
		if err != nil {
			return err
		}
		if reserved {
			return demandError(409, "ATTACHMENT_IN_USE", "Profile avatar cannot be attached to a message")
		}
		res := tx.Model(&model.DemandAttachment{}).Where("id = ? AND message_id = ? AND demand_id = ? AND (avatar_reserved = ? OR avatar_reserved IS NULL)", id, "", "", false).Updates(map[string]any{"demand_id": d.ID, "message_id": messageID})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return demandError(409, "ATTACHMENT_IN_USE", "Attachment is already attached to a message")
		}
	}
	return nil
}
func demandAudit(tx *gorm.DB, a DemandActor, id, action, detail string) error {
	return tx.Create(&model.DemandAudit{DemandID: id, ActorID: a.UserID, Action: action, Detail: detail, CreatedAt: time.Now().Unix()}).Error
}
func CreateDemand(db *gorm.DB, a DemandActor, in DemandCreate) (DemandSendReceipt, error) {
	var result DemandSendReceipt
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return result, err
	}
	send := DemandSend{in.ClientRequestID, in.Text, in.AttachmentIDs}
	if utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Budget) > 100 || len(in.ClientVersion) > 100 || len(in.ClientPlatform) > 100 || in.ExpectedDate != "" && !demandDate.MatchString(in.ExpectedDate) {
		return result, demandError(400, "INVALID_INPUT", "Invalid demand details")
	}
	if in.ExpectedDate != "" {
		if _, err := time.Parse("2006-01-02", in.ExpectedDate); err != nil {
			return result, demandError(400, "INVALID_INPUT", "Invalid demand details")
		}
	}
	hash := demandHash(in)
	var d model.Demand
	var m model.DemandMessage
	err = db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&d).Error
		if err == nil {
			if d.CreateHash != hash {
				return demandError(409, "IDEMPOTENCY_CONFLICT", "The submission key was already used for different content")
			}
			result.Replayed = true
			return tx.Where("demand_id = ? AND seq = ?", d.ID, 1).First(&m).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := validateDemandSend(send, cfg.Config); err != nil {
			return err
		}
		if !cfg.Config.Enabled {
			return demandError(503, "DISABLED", "Demand service is not enabled")
		}
		if (in.EngineerID == "") == (in.ServiceProfileID == "") || len(in.EngineerID) > 40 || len(in.ServiceProfileID) > 40 {
			return demandError(400, "INVALID_INPUT", "Choose one engineer or service profile")
		}
		var profile model.DemandServiceProfile
		var e model.DemandEngineer
		if in.ServiceProfileID != "" {
			profile, err = demandServiceIntake(tx, in.ServiceProfileID)
			if err != nil {
				return err
			}
		} else {
			if err := tx.First(&e, "id = ?", in.EngineerID).Error; err != nil {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
			var enabledUser int64
			if err := tx.Model(&model.User{}).Where("id = ? AND status = ?", e.UserID, common.UserStatusEnabled).Count(&enabledUser).Error; err != nil {
				return err
			}
			if enabledUser != 1 {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
			res := tx.Model(&model.DemandEngineer{}).Where("id = ? AND enabled = ? AND published = ? AND accepting = ? AND open_demand_count < capacity", e.ID, true, true, true).UpdateColumn("open_demand_count", gorm.Expr("open_demand_count + 1"))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
		}
		id := uuid.NewString()
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = string([]rune(strings.TrimSpace(in.Text))[:min(80, utf8.RuneCountInString(strings.TrimSpace(in.Text)))])
		}
		if title == "" {
			title = "Image or video request"
		}
		now := time.Now().Unix()
		d = model.Demand{ID: id, Number: "YS-" + strings.ToUpper(strings.ReplaceAll(id, "-", "")), UserID: a.UserID, ClientRequestID: in.ClientRequestID, CreateHash: hash, EngineerID: e.ID, ServiceProfileID: profile.ID, ServiceProfileName: profile.DisplayName, Title: title, Budget: in.Budget, ExpectedDate: in.ExpectedDate, Status: "pending", Priority: "normal", Revision: 1, LastSeq: 1, Preview: title, ClientVersion: in.ClientVersion, ClientPlatform: in.ClientPlatform, CreatedAt: now, UpdatedAt: now, LastMessageAt: now}
		if err := tx.Create(&d).Error; err != nil {
			return err
		}
		m = model.DemandMessage{ID: uuid.NewString(), DemandID: d.ID, Seq: 1, SenderID: a.UserID, ClientMessageID: in.ClientRequestID, PayloadHash: demandHash(send), Author: "user", Kind: "message", Text: in.Text, CreatedAt: now}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		if err := bindDemandAttachments(tx, a, d, m.ID, in.AttachmentIDs); err != nil {
			return err
		}
		return demandAudit(tx, a, d.ID, "created", e.ID+profile.ID)
	})
	// A concurrent identical first submission may have won a unique constraint.
	if err != nil {
		var existing model.Demand
		if db.Where("user_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&existing).Error == nil {
			if existing.CreateHash != hash {
				return result, demandError(409, "IDEMPOTENCY_CONFLICT", "The submission key was already used for different content")
			}
			d = existing
			result.Replayed = true
			err = db.Where("demand_id = ? AND seq = ?", d.ID, 1).First(&m).Error
		}
	}
	if err != nil {
		return result, err
	}
	result.Demand, err = ProjectDemand(db, a, d)
	if err == nil {
		result.Message, err = ProjectDemandMessage(db, a, d, m)
	}
	return result, err
}
func SendDemandMessage(db *gorm.DB, a DemandActor, id string, in DemandSend) (DemandSendReceipt, error) {
	var result DemandSendReceipt
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return result, err
	}
	hash := demandHash(in)
	var d model.Demand
	var m model.DemandMessage
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		d, err = DemandAccess(tx, a, id)
		if err != nil {
			return err
		}
		err = tx.Where("demand_id = ? AND sender_id = ? AND client_message_id = ?", id, a.UserID, in.ClientMessageID).First(&m).Error
		if err == nil {
			if m.PayloadHash != hash {
				return demandError(409, "IDEMPOTENCY_CONFLICT", "The submission key was already used for different content")
			}
			result.Replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := validateDemandSend(in, cfg.Config); err != nil {
			return err
		}
		if slices.Contains([]string{"completed", "cancelled", "closed"}, d.Status) {
			return demandError(409, "STATUS_CONFLICT", "This demand is read only")
		}
		if d.LastSeq >= demandMaxSequence {
			return demandError(409, "STATUS_CONFLICT", "Demand message limit reached")
		}
		preview := string([]rune(in.Text)[:min(200, utf8.RuneCountInString(in.Text))])
		res := tx.Model(&model.Demand{}).Where("id = ? AND revision = ?", id, d.Revision).Updates(map[string]any{"revision": d.Revision + 1, "last_seq": d.LastSeq + 1, "updated_at": time.Now().Unix(), "preview": preview, "last_message_at": time.Now().Unix()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return demandError(409, "REVISION_CONFLICT", "Demand changed; reload before sending")
		}
		d.Revision++
		d.LastSeq++
		d.UpdatedAt = time.Now().Unix()
		d.Preview = preview
		d.LastMessageAt = d.UpdatedAt
		author := "engineer"
		if d.UserID == a.UserID {
			author = "user"
		}
		if a.Admin {
			author = "system"
		}
		m = model.DemandMessage{ID: uuid.NewString(), DemandID: id, Seq: d.LastSeq, SenderID: a.UserID, ClientMessageID: in.ClientMessageID, PayloadHash: hash, Author: author, Kind: "message", Text: in.Text, CreatedAt: d.UpdatedAt}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		return bindDemandAttachments(tx, a, d, m.ID, in.AttachmentIDs)
	})
	if err != nil {
		return result, err
	}
	result.Demand, err = ProjectDemand(db, a, d)
	if err == nil {
		result.Message, err = ProjectDemandMessage(db, a, d, m)
	}
	return result, err
}

type DemandAction struct {
	ClientActionID string   `json:"client_action_id"`
	Action         string   `json:"action"`
	Revision       int64    `json:"revision"`
	EngineerID     string   `json:"engineer_id"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	Text           string   `json:"text"`
	AttachmentIDs  []string `json:"attachment_ids"`
	DeliveryID     string   `json:"delivery_id"`
}
type DemandActionResult struct {
	Demand   DemandView `json:"demand"`
	Replayed bool       `json:"replayed"`
}

func ActOnDemand(db *gorm.DB, a DemandActor, id string, in DemandAction) (DemandActionResult, error) {
	var result DemandActionResult
	var d model.Demand
	if !demandKey.MatchString(in.ClientActionID) || in.Revision < 1 || utf8.RuneCountInString(in.Text) > 10000 {
		return result, demandError(400, "INVALID_INPUT", "Invalid demand action")
	}
	hash := demandHash(in)
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		d, err = DemandAccess(tx, a, id)
		if err != nil {
			return err
		}
		var receipt model.DemandActionReceipt
		err = tx.Where("demand_id = ? AND actor_id = ? AND client_action_id = ?", id, a.UserID, in.ClientActionID).First(&receipt).Error
		if err == nil {
			if receipt.PayloadHash != hash {
				return demandError(409, "IDEMPOTENCY_CONFLICT", "The submission key was already used for different content")
			}
			result.Replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if d.Revision != in.Revision {
			return demandError(409, "REVISION_CONFLICT", "Demand changed; reload before applying this action")
		}
		oldStatus := d.Status
		oldEngineer := d.EngineerID
		terminal := slices.Contains([]string{"completed", "closed", "cancelled"}, d.Status)
		isOwner := d.UserID == a.UserID
		engineer := !a.Admin && !isOwner
		switch in.Action {
		case "accept":
			if !engineer || d.Status != "pending" {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be accepted in its current state")
			}
			d.Status = "communicating"
		case "start":
			if !engineer || d.Status != "communicating" {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be started in its current state")
			}
			d.Status = "in_progress"
		case "deliver":
			if !engineer || !slices.Contains([]string{"communicating", "in_progress"}, d.Status) {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be delivered in its current state")
			}
			cfg, err := LoadDemandConfig(tx)
			if err != nil {
				return err
			}
			send := DemandSend{in.ClientActionID, in.Text, in.AttachmentIDs}
			if err := validateDemandSend(send, cfg.Config); err != nil {
				return err
			}
			if d.LastSeq >= demandMaxSequence {
				return demandError(409, "STATUS_CONFLICT", "Demand message limit reached")
			}
			m := model.DemandMessage{ID: uuid.NewString(), DemandID: id, Seq: d.LastSeq + 1, SenderID: a.UserID, ClientMessageID: in.ClientActionID, PayloadHash: hash, Author: "engineer", Kind: "delivery", Text: in.Text, CreatedAt: time.Now().Unix()}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
			if err := bindDemandAttachments(tx, a, d, m.ID, in.AttachmentIDs); err != nil {
				return err
			}
			d.LastSeq++
			d.DeliveryID = m.ID
			d.LastMessageAt = m.CreatedAt
			d.Preview = string([]rune(in.Text)[:min(200, utf8.RuneCountInString(in.Text))])
			d.Status = "awaiting_acceptance"
		case "accept_delivery":
			if !isOwner || d.Status != "awaiting_acceptance" || in.DeliveryID == "" || in.DeliveryID != d.DeliveryID {
				return demandError(409, "STATUS_CONFLICT", "Delivery is not awaiting your acceptance")
			}
			d.Status = "completed"
		case "cancel":
			if !isOwner || terminal || d.Status == "awaiting_acceptance" {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be cancelled in its current state")
			}
			d.Status = "cancelled"
		case "request_transfer":
			if !engineer || terminal {
				return demandError(403, "PERMISSION_DENIED", "Demand action is not permitted")
			}
		case "assign":
			if !a.Admin || terminal || in.EngineerID == d.EngineerID {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be assigned in its current state")
			}
			var e model.DemandEngineer
			if err := tx.First(&e, "id = ?", in.EngineerID).Error; err != nil {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
			var enabledUser int64
			if err := tx.Model(&model.User{}).Where("id = ? AND status = ?", e.UserID, common.UserStatusEnabled).Count(&enabledUser).Error; err != nil {
				return err
			}
			if enabledUser != 1 {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
			res := tx.Model(&model.DemandEngineer{}).Where("id = ? AND enabled = ? AND open_demand_count < capacity", e.ID, true).UpdateColumn("open_demand_count", gorm.Expr("open_demand_count + 1"))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return demandError(409, "ENGINEER_UNAVAILABLE", "Engineer is not accepting new demands")
			}
			d.EngineerID = e.ID
			d.Status = "pending"
			d.DeliveryID = ""
		case "internal_note":
			if !a.Admin || strings.TrimSpace(in.Text) == "" {
				return demandError(403, "PERMISSION_DENIED", "Demand action is not permitted")
			}
		case "priority":
			if !a.Admin || !slices.Contains([]string{"normal", "high", "urgent"}, in.Priority) {
				return demandError(403, "PERMISSION_DENIED", "Demand action is not permitted")
			}
			d.Priority = in.Priority
		case "close":
			if !a.Admin || terminal {
				return demandError(409, "STATUS_CONFLICT", "Demand cannot be closed in its current state")
			}
			d.Status = "closed"
		case "status":
			if !a.Admin || terminal || !slices.Contains([]string{"pending", "communicating", "in_progress", "cancelled", "closed"}, in.Status) || d.EngineerID == "" && !slices.Contains([]string{"pending", "cancelled", "closed"}, in.Status) {
				return demandError(409, "STATUS_CONFLICT", "Invalid demand status transition")
			}
			d.Status = in.Status
		default:
			return demandError(400, "INVALID_INPUT", "Invalid demand action")
		}
		d.Revision++
		d.UpdatedAt = time.Now().Unix()
		res := tx.Model(&model.Demand{}).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{"revision": d.Revision, "updated_at": d.UpdatedAt, "status": d.Status, "engineer_id": d.EngineerID, "priority": d.Priority, "delivery_id": d.DeliveryID, "last_seq": d.LastSeq, "last_message_at": d.LastMessageAt, "preview": d.Preview})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return demandError(409, "REVISION_CONFLICT", "Demand changed; reload before applying this action")
		}
		if oldEngineer != d.EngineerID || oldStatus != d.Status && slices.Contains([]string{"completed", "cancelled", "closed"}, d.Status) {
			if err := tx.Model(&model.DemandEngineer{}).Where("id = ? AND open_demand_count > 0", oldEngineer).UpdateColumn("open_demand_count", gorm.Expr("open_demand_count - 1")).Error; err != nil {
				return err
			}
		}
		detail := in.Action
		if in.Action == "internal_note" || in.Action == "request_transfer" {
			detail = in.Text
		} else if in.Action == "assign" {
			detail = oldEngineer + " -> " + d.EngineerID
		} else if oldStatus != d.Status {
			detail = oldStatus + " -> " + d.Status
		}
		if err := demandAudit(tx, a, id, in.Action, detail); err != nil {
			return err
		}
		return tx.Create(&model.DemandActionReceipt{DemandID: id, ActorID: a.UserID, ClientActionID: in.ClientActionID, PayloadHash: hash}).Error
	})
	if err != nil {
		return result, err
	}
	result.Demand, err = ProjectDemand(db, a, d)
	return result, err
}

func MarkDemandRead(db *gorm.DB, a DemandActor, id string, seq int64) (map[string]int64, error) {
	d, err := DemandAccess(db, a, id)
	if err != nil {
		return nil, err
	}
	if seq < 0 || seq > d.LastSeq {
		return nil, demandError(http.StatusBadRequest, "CURSOR_INVALID", "Invalid demand read cursor")
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		r := model.DemandReadCursor{DemandID: id, UserID: a.UserID, LastReadSeq: seq}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; err != nil {
			return err
		}
		return tx.Model(&model.DemandReadCursor{}).Where("demand_id = ? AND user_id = ? AND last_read_seq < ?", id, a.UserID, seq).UpdateColumn("last_read_seq", seq).Error
	})
	if err != nil {
		return nil, err
	}
	v, err := ProjectDemand(db, a, d)
	return map[string]int64{"last_read_seq": v.LastReadSeq, "unread_count": v.UnreadCount}, err
}
