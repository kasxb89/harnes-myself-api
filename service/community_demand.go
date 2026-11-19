package service

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommunityActor struct {
	UserID int
	Admin  bool
}
type CommunityError struct {
	Status        int
	Code, Message string
}

func (e *CommunityError) Error() string { return e.Message }
func communityError(status int, code, msg string) error {
	return &CommunityError{status, "COMMUNITY_" + code, msg}
}
func communityMissing() error { return communityError(404, "NOT_FOUND", "Community order not found") }
func communityKey(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 80 && !strings.ContainsAny(s, "\x00\r\n")
}
func communityLocked(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() != "sqlite" {
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return tx
}
func communityClosed(s string) bool { return s == "completed" || s == "cancelled" }

var CommunityStatuses = []string{"open", "accepted", "awaiting_acceptance", "completed", "cancelled"}

// Empty legacy values have never been reviewed and must not become approved.
func CommunityApprovalStatus(o model.CommunityOrder) string {
	if o.ApprovalStatus == "approved" || o.ApprovalStatus == "rejected" {
		return o.ApprovalStatus
	}
	return "pending"
}

type CommunityConfig struct {
	Revision int64 `json:"revision"`
	Enabled  bool  `json:"enabled"`
}

func LoadCommunityConfig(db *gorm.DB) (CommunityConfig, error) {
	var row model.CommunityConfiguration
	err := db.First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CommunityConfig{}, nil
	}
	return CommunityConfig{row.Revision, row.Enabled}, err
}
func SaveCommunityConfig(db *gorm.DB, in CommunityConfig) (CommunityConfig, error) {
	if in.Revision < 0 {
		return CommunityConfig{}, communityError(400, "INVALID_INPUT", "Invalid community settings")
	}
	var res *gorm.DB
	if in.Revision == 0 {
		res = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CommunityConfiguration{ID: 1, Revision: 1, Enabled: in.Enabled})
	} else {
		res = db.Model(&model.CommunityConfiguration{}).Where("id = ? AND revision = ?", 1, in.Revision).Updates(map[string]any{"revision": in.Revision + 1, "enabled": in.Enabled})
	}
	if res.Error != nil {
		return CommunityConfig{}, res.Error
	}
	if res.RowsAffected != 1 {
		return CommunityConfig{}, communityError(409, "REVISION_CONFLICT", "Community settings changed")
	}
	return CommunityConfig{in.Revision + 1, in.Enabled}, nil
}
func communityEnabled(db *gorm.DB) error {
	cfg, err := LoadCommunityConfig(db)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return communityError(403, "DISABLED", "Community publication is disabled")
	}
	return nil
}
func CommunityOrderAccess(db *gorm.DB, a CommunityActor, id string) (model.CommunityOrder, error) {
	var o model.CommunityOrder
	q := db.Where("id = ?", id)
	if !a.Admin {
		q = q.Where("(publisher_id = ? OR acceptor_id = ?)", a.UserID, a.UserID)
	}
	err := q.First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return o, communityMissing()
	}
	return o, err
}
func CommunityListingAccess(db *gorm.DB, a CommunityActor, id string) (model.CommunityOrder, error) {
	var o model.CommunityOrder
	if err := db.First(&o, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return o, communityMissing()
		}
		return o, err
	}
	if a.Admin || o.PublisherID == a.UserID || o.AcceptorID == a.UserID {
		return o, nil
	}
	cfg, err := LoadCommunityConfig(db)
	if err != nil {
		return o, err
	}
	if !cfg.Enabled || !o.Visible || o.ApprovalStatus != "approved" || communityClosed(o.Status) {
		return o, communityMissing()
	}
	return o, nil
}

type CommunityCreate struct {
	ClientRequestID      string   `json:"client_request_id"`
	PublishToCommunity   bool     `json:"publish_to_community"`
	Title                string   `json:"title"`
	PublicSummary        string   `json:"public_summary"`
	PublicBudget         string   `json:"public_budget"`
	ServiceProfileID     string   `json:"service_profile_id"`
	PublicAttachmentIDs  []string `json:"public_attachment_ids"`
	PrivateText          string   `json:"private_text"`
	PrivateAttachmentIDs []string `json:"private_attachment_ids"`
	// Omitting the empty field preserves the exact hash of pre-approval requests.
	ExpectedDeliveryDate string `json:"expected_delivery_date,omitempty"`
}
type CommunitySend struct {
	ClientMessageID string   `json:"client_message_id"`
	Text            string   `json:"text"`
	AttachmentIDs   []string `json:"attachment_ids"`
}
type CommunityAction struct {
	ClientActionID string   `json:"client_action_id"`
	Revision       int64    `json:"revision"`
	Action         string   `json:"action"`
	Text           string   `json:"text"`
	Reason         string   `json:"reason"`
	AttachmentIDs  []string `json:"attachment_ids"`
}

func communityBind(tx *gorm.DB, a CommunityActor, orderID, messageID, scope string, ids []string) error {
	cfg, err := LoadDemandConfig(tx)
	if err != nil {
		return err
	}
	if len(ids) > min(8, cfg.Config.UploadRules.MaxAttachments) {
		return communityError(400, "INVALID_INPUT", "Too many attachments")
	}
	ordered := append([]string{}, ids...)
	sort.Strings(ordered)
	for i, id := range ordered {
		if i > 0 && id == ordered[i-1] {
			return communityError(400, "INVALID_INPUT", "Duplicate attachment")
		}
		var f model.CommunityAttachment
		if err = communityLocked(tx).Where("id = ?", id).First(&f).Error; err != nil {
			return communityError(409, "ATTACHMENT_NOT_READY", "Attachment is unavailable")
		}
		if f.OwnerID != a.UserID || f.Scope != scope || f.Status != "ready" {
			return communityError(409, "ATTACHMENT_NOT_READY", "Attachment is unavailable")
		}
		if f.MessageID != "" || f.OrderID != "" && f.OrderID != orderID {
			return communityError(409, "ATTACHMENT_IN_USE", "Attachment belongs to another submission")
		}
		r := tx.Model(&model.CommunityAttachment{}).Where("id = ? AND message_id = ?", id, "").Updates(map[string]any{"order_id": orderID, "message_id": messageID})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return communityError(409, "ATTACHMENT_IN_USE", "Attachment was already submitted")
		}
	}
	return nil
}
func communityAudit(tx *gorm.DB, a CommunityActor, id, action, reason string) error {
	return tx.Create(&model.CommunityAudit{OrderID: id, ActorID: a.UserID, Action: action, Reason: reason, CreatedAt: time.Now().Unix()}).Error
}
func communityAppend(tx *gorm.DB, a CommunityActor, o *model.CommunityOrder, key, text, kind, author string, ids []string) (model.CommunityMessage, error) {
	o.LastSeq++
	raw, _ := common.Marshal(ids)
	senderID := a.UserID
	if author == "system" {
		senderID = 0
	}
	m := model.CommunityMessage{ID: uuid.NewString(), OrderID: o.ID, Seq: o.LastSeq, SenderID: senderID, ClientMessageID: key, PayloadHash: demandHash(struct {
		OrderID string
		Input   CommunitySend
	}{o.ID, CommunitySend{key, text, ids}}), AuthorKind: author, Kind: kind, Text: text, AttachmentsJSON: string(raw), CreatedAt: time.Now().Unix()}
	if err := tx.Create(&m).Error; err != nil {
		return m, err
	}
	if err := communityBind(tx, a, o.ID, m.ID, "conversation", ids); err != nil {
		return m, err
	}
	if err := tx.Model(o).Updates(map[string]any{"last_seq": o.LastSeq, "updated_at": m.CreatedAt}).Error; err != nil {
		return m, err
	}
	return m, nil
}
func CreateCommunityOrder(db *gorm.DB, a CommunityActor, in CommunityCreate) (model.CommunityOrder, *model.CommunityMessage, bool, error) {
	var out model.CommunityOrder
	var first *model.CommunityMessage
	if !communityKey(in.ClientRequestID) {
		return out, nil, false, communityError(400, "INVALID_INPUT", "Invalid submission key")
	}
	hash := demandHash(in)
	priorErr := db.Where("publisher_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&out).Error
	if priorErr == nil {
		if out.PayloadHash != hash {
			return out, nil, false, communityError(409, "IDEMPOTENCY_CONFLICT", "Submission key belongs to different content")
		}
		var m model.CommunityMessage
		if db.Where("order_id = ? AND client_message_id = ?", out.ID, communityInternalKey("create", out.ID, in.ClientRequestID)).First(&m).Error == nil {
			first = &m
		}
		return out, first, true, nil
	}
	if !errors.Is(priorErr, gorm.ErrRecordNotFound) {
		return out, nil, false, priorErr
	}
	if !in.PublishToCommunity {
		return out, nil, false, communityError(400, "EXPLICIT_PUBLICATION_REQUIRED", "Explicit community publication is required")
	}
	if strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || strings.TrimSpace(in.PublicSummary) == "" || utf8.RuneCountInString(in.PublicSummary) > 4000 || utf8.RuneCountInString(in.PublicBudget) > 100 || utf8.RuneCountInString(in.PrivateText) > 12000 {
		return out, nil, false, communityError(400, "INVALID_INPUT", "Invalid community order")
	}
	if in.ExpectedDeliveryDate != "" {
		date, err := time.Parse(time.DateOnly, in.ExpectedDeliveryDate)
		if err != nil || date.Year() < 1 || date.Format(time.DateOnly) != in.ExpectedDeliveryDate {
			return out, nil, false, communityError(400, "INVALID_INPUT", "Invalid expected delivery date")
		}
	}
	now := time.Now().Unix()
	out = model.CommunityOrder{ID: uuid.NewString(), Number: "YC-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:20]), PublisherID: a.UserID, ClientRequestID: in.ClientRequestID, PayloadHash: hash, Title: strings.TrimSpace(in.Title), PublicSummary: strings.TrimSpace(in.PublicSummary), PublicBudget: in.PublicBudget, ServiceProfileID: in.ServiceProfileID, PrivateText: in.PrivateText, Status: "open", Visible: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	out.ExpectedDeliveryDate = in.ExpectedDeliveryDate
	out.ApprovalStatus = "pending"
	ids, _ := common.Marshal(in.PublicAttachmentIDs)
	out.PublicAttachmentsJSON = string(ids)
	replay := false
	err := db.Transaction(func(tx *gorm.DB) error {
		r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&out)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			var p model.CommunityOrder
			if err := tx.Where("publisher_id = ? AND client_request_id = ?", a.UserID, in.ClientRequestID).First(&p).Error; err != nil {
				return err
			}
			if p.PayloadHash != hash {
				return communityError(409, "IDEMPOTENCY_CONFLICT", "Submission key belongs to different content")
			}
			out = p
			replay = true
			return nil
		}
		if err := communityEnabled(tx); err != nil {
			return err
		}
		if in.ServiceProfileID != "" {
			var profile model.DemandServiceProfile
			if tx.Where("id = ? AND enabled = ? AND published = ?", in.ServiceProfileID, true, true).First(&profile).Error != nil {
				return communityError(400, "INVALID_INPUT", "Service profile is not available")
			}
			out.ServiceProfileName = profile.DisplayName
			if err := tx.Model(&out).Update("service_profile_name", profile.DisplayName).Error; err != nil {
				return err
			}
		}
		if err := communityBind(tx, a, out.ID, "listing", "listing", in.PublicAttachmentIDs); err != nil {
			return err
		}
		if in.PrivateText != "" || len(in.PrivateAttachmentIDs) > 0 {
			m, err := communityAppend(tx, a, &out, communityInternalKey("create", out.ID, in.ClientRequestID), in.PrivateText, "message", "publisher", in.PrivateAttachmentIDs)
			if err != nil {
				return err
			}
			first = &m
		}
		return communityAudit(tx, a, out.ID, "published", "")
	})
	if err == nil && replay && first == nil {
		var m model.CommunityMessage
		e := db.Where("order_id = ? AND client_message_id = ?", out.ID, communityInternalKey("create", out.ID, in.ClientRequestID)).First(&m).Error
		if e == nil {
			first = &m
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			err = e
		}
	}
	return out, first, replay, err
}

// A write before any transaction reads prevents SQLite snapshot-upgrade races.
func communityLock(tx *gorm.DB, id string) (model.CommunityOrder, error) {
	if err := tx.Model(&model.CommunityOrder{}).Where("id = ?", id).UpdateColumn("last_seq", gorm.Expr("last_seq")).Error; err != nil {
		return model.CommunityOrder{}, err
	}
	var o model.CommunityOrder
	err := communityLocked(tx).Where("id = ?", id).First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return o, communityMissing()
	}
	return o, err
}
func communityOwn(a CommunityActor, o model.CommunityOrder) bool {
	return a.Admin || o.PublisherID == a.UserID || o.AcceptorID == a.UserID
}
func communityReceipt(tx *gorm.DB, a CommunityActor, id, key string, payload any) (model.CommunityActionReceipt, bool, error) {
	var r model.CommunityActionReceipt
	if !communityKey(key) {
		return r, false, communityError(400, "INVALID_INPUT", "Invalid action key")
	}
	hash := demandHash(payload)
	r = model.CommunityActionReceipt{ID: uuid.NewString(), ActorID: a.UserID, ClientActionID: key, OrderID: id, PayloadHash: hash}
	inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&r)
	if inserted.Error != nil {
		return r, false, inserted.Error
	}
	if inserted.RowsAffected == 1 {
		return r, false, nil
	}
	var prior model.CommunityActionReceipt
	if err := tx.Where("actor_id = ? AND client_action_id = ?", a.UserID, key).First(&prior).Error; err != nil {
		return prior, false, err
	}
	if prior.OrderID != id || prior.PayloadHash != hash {
		return prior, false, communityError(409, "IDEMPOTENCY_CONFLICT", "Action key belongs to different content")
	}
	return prior, true, nil
}
func AcceptCommunityOrder(db *gorm.DB, a CommunityActor, id, key string) (model.CommunityOrder, *model.CommunityMessage, bool, error) {
	var out model.CommunityOrder
	var msg *model.CommunityMessage
	replay := false
	err := db.Transaction(func(tx *gorm.DB) error {
		receipt, exists, err := communityReceipt(tx, a, id, key, struct{ ID, Action string }{id, "accept"})
		if err != nil {
			return err
		}
		// The receipt insertion acquires the writer before the conditional claim.
		if !exists {
			if err = communityEnabled(tx); err != nil {
				return err
			}
			r := tx.Model(&model.CommunityOrder{}).Where("id = ? AND status = ? AND acceptor_id = 0 AND publisher_id <> ? AND visible = ? AND approval_status = ?", id, "open", a.UserID, true, "approved").Updates(map[string]any{"acceptor_id": a.UserID, "status": "accepted", "accepted_at": time.Now().Unix(), "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().Unix()})
			if r.Error != nil {
				return r.Error
			}
			out, err = communityLock(tx, id)
			if err != nil {
				return err
			}
			if out.PublisherID == a.UserID {
				return communityError(403, "SELF_ACCEPT_FORBIDDEN", "You cannot accept your own order")
			}
			if r.RowsAffected == 0 {
				if out.AcceptorID == a.UserID {
					replay = true
					return nil
				}
				if out.AcceptorID != 0 {
					return communityError(409, "ALREADY_ACCEPTED", "Another user already accepted this order")
				}
				if out.ApprovalStatus != "approved" {
					return communityError(409, "APPROVAL_REQUIRED", "Administrator approval is required before acceptance")
				}
				return communityError(409, "ORDER_NOT_OPEN", "This order is not open")
			}
			m, err := communityAppend(tx, a, &out, communityInternalKey("accept", out.ID, key), "", "system", "system", nil)
			if err != nil {
				return err
			}
			msg = &m
			if err = tx.Model(&receipt).Update("message_id", m.ID).Error; err != nil {
				return err
			}
			return communityAudit(tx, a, id, "accepted", "")
		}
		out, err = communityLock(tx, id)
		if err != nil {
			return err
		}
		if !communityOwn(a, out) {
			return communityMissing()
		}
		replay = true
		if receipt.MessageID != "" {
			var m model.CommunityMessage
			if err = tx.First(&m, "id = ?", receipt.MessageID).Error; err != nil {
				return err
			}
			msg = &m
		}
		return nil
	})
	return out, msg, replay, err
}

// Internal receipts use keys longer than the public 80-character key limit.
func communityInternalKey(kind, orderID, key string) string {
	return "internal:" + orderID + ":" + demandHash([]string{kind, key})
}

func SendCommunityMessage(db *gorm.DB, a CommunityActor, id string, in CommunitySend) (model.CommunityOrder, model.CommunityMessage, bool, error) {
	var out model.CommunityOrder
	var message model.CommunityMessage
	replay := false
	if !communityKey(in.ClientMessageID) || utf8.RuneCountInString(in.Text) > 12000 || strings.TrimSpace(in.Text) == "" && len(in.AttachmentIDs) == 0 {
		return out, message, false, communityError(400, "INVALID_INPUT", "A message or attachment is required")
	}
	hash := demandHash(struct {
		OrderID string
		Input   CommunitySend
	}{id, in})
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = communityLock(tx, id)
		if err != nil {
			return err
		}
		if !communityOwn(a, out) {
			return communityMissing()
		}
		err = tx.Where("sender_id = ? AND client_message_id = ?", a.UserID, in.ClientMessageID).First(&message).Error
		if err == nil {
			if message.OrderID != id || message.PayloadHash != hash {
				return communityError(409, "IDEMPOTENCY_CONFLICT", "Message key belongs to different content")
			}
			replay = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if communityClosed(out.Status) {
			return communityError(409, "READ_ONLY", "This order is read-only")
		}
		author := "publisher"
		if a.Admin {
			author = "admin"
		} else if out.AcceptorID == a.UserID {
			author = "acceptor"
		}
		message, err = communityAppend(tx, a, &out, in.ClientMessageID, in.Text, "message", author, in.AttachmentIDs)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		// A competing transaction may have committed this global message key.
		// Resolve its durable receipt by reading; never re-run the write.
		var existing model.CommunityMessage
		if db.Where("sender_id = ? AND client_message_id = ?", a.UserID, in.ClientMessageID).First(&existing).Error == nil {
			if existing.OrderID != id || existing.PayloadHash != hash {
				return out, message, false, communityError(409, "IDEMPOTENCY_CONFLICT", "Message key belongs to different content")
			}
			if current, e := CommunityOrderAccess(db, a, id); e == nil {
				return current, existing, true, nil
			}
		}
	}
	return out, message, replay, err
}
func ActCommunityOrder(db *gorm.DB, a CommunityActor, id string, in CommunityAction) (model.CommunityOrder, *model.CommunityMessage, bool, error) {
	var out model.CommunityOrder
	var msg *model.CommunityMessage
	replay := false
	if utf8.RuneCountInString(in.Text) > 12000 || utf8.RuneCountInString(in.Reason) > 1000 {
		return out, nil, false, communityError(400, "INVALID_INPUT", "Invalid order action")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		rec, exists, err := communityReceipt(tx, a, id, in.ClientActionID, in)
		if err != nil {
			return err
		}
		out, err = communityLock(tx, id)
		if err != nil {
			return err
		}
		if !communityOwn(a, out) {
			return communityMissing()
		}
		if exists {
			replay = true
			if rec.MessageID != "" {
				var m model.CommunityMessage
				if err = tx.First(&m, "id = ?", rec.MessageID).Error; err != nil {
					return err
				}
				msg = &m
			}
			return nil
		}
		if in.Revision != out.Revision {
			return communityError(409, "REVISION_CONFLICT", "Order changed; refresh and try again")
		}
		next := out.Status
		adminAction := false
		switch in.Action {
		case "approve_listing", "reject_listing":
			if !a.Admin {
				return communityError(403, "FORBIDDEN", "Administrator permission is required")
			}
			out.ApprovalStatus, out.ApprovalReason = "approved", ""
			if in.Action == "reject_listing" {
				if strings.TrimSpace(in.Reason) == "" {
					return communityError(400, "INVALID_INPUT", "A moderation reason is required")
				}
				out.ApprovalStatus, out.ApprovalReason = "rejected", in.Reason
			}
			adminAction = true
		case "hide_listing", "show_listing":
			if !a.Admin {
				return communityError(403, "FORBIDDEN", "Administrator permission is required")
			}
			if strings.TrimSpace(in.Reason) == "" {
				return communityError(400, "INVALID_INPUT", "A moderation reason is required")
			}
			out.Visible = in.Action == "show_listing"
			adminAction = true
		case "cancel":
			if communityClosed(out.Status) {
				return communityError(409, "STATE_CONFLICT", "Order is already closed")
			}
			if !a.Admin && (a.UserID != out.PublisherID || out.AcceptorID != 0) {
				return communityError(403, "FORBIDDEN", "Only an administrator can cancel an accepted order")
			}
			if a.Admin && strings.TrimSpace(in.Reason) == "" {
				return communityError(400, "INVALID_INPUT", "A cancellation reason is required")
			}
			next = "cancelled"
		case "reopen":
			if out.Status != "cancelled" || out.AcceptorID != 0 || out.AcceptedAt != 0 {
				return communityError(409, "REOPEN_FORBIDDEN", "An order that was accepted cannot be reopened")
			}
			if a.UserID != out.PublisherID && !a.Admin {
				return communityError(403, "FORBIDDEN", "Only the publisher can reopen an order")
			}
			if err = communityEnabled(tx); err != nil {
				return err
			}
			next = "open"
		case "deliver":
			if a.UserID != out.AcceptorID || out.Status != "accepted" {
				return communityError(409, "STATE_CONFLICT", "Only the accepted user can deliver active work")
			}
			if strings.TrimSpace(in.Text) == "" && len(in.AttachmentIDs) == 0 {
				return communityError(400, "INVALID_INPUT", "Delivery text or attachments are required")
			}
			next = "awaiting_acceptance"
		case "accept_delivery":
			if a.UserID != out.PublisherID || out.Status != "awaiting_acceptance" {
				return communityError(409, "STATE_CONFLICT", "Only the publisher can accept a delivery")
			}
			next = "completed"
		case "request_changes":
			if a.UserID != out.PublisherID || out.Status != "awaiting_acceptance" || strings.TrimSpace(in.Text) == "" {
				return communityError(409, "STATE_CONFLICT", "Delivery feedback is required")
			}
			next = "accepted"
		default:
			return communityError(400, "INVALID_INPUT", "Unknown order action")
		}
		author, kind := "system", "system"
		text := in.Reason
		ids := []string(nil)
		if in.Action == "deliver" {
			author = "acceptor"
			kind = "delivery"
			text = in.Text
			ids = in.AttachmentIDs
		} else if in.Action == "request_changes" {
			author = "publisher"
			kind = "message"
			text = in.Text
		}
		if !adminAction {
			m, err := communityAppend(tx, a, &out, communityInternalKey("action", out.ID, in.ClientActionID), text, kind, author, ids)
			if err != nil {
				return err
			}
			msg = &m
			if in.Action == "deliver" {
				out.DeliveryMessageID = m.ID
			}
			if err = tx.Model(&rec).Update("message_id", m.ID).Error; err != nil {
				return err
			}
		}
		out.Status = next
		out.Revision++
		out.UpdatedAt = time.Now().Unix()
		if !adminAction {
			out.ClosedAt = 0
			if communityClosed(next) {
				out.ClosedAt = out.UpdatedAt
			}
		}
		if err = tx.Model(&out).Updates(map[string]any{"status": next, "visible": out.Visible, "approval_status": out.ApprovalStatus, "approval_reason": out.ApprovalReason, "revision": out.Revision, "updated_at": out.UpdatedAt, "closed_at": out.ClosedAt, "delivery_message_id": out.DeliveryMessageID}).Error; err != nil {
			return err
		}
		return communityAudit(tx, a, id, in.Action, in.Reason)
	})
	return out, msg, replay, err
}
func MarkCommunityRead(db *gorm.DB, a CommunityActor, id string, seq int64) (map[string]int64, error) {
	o, err := CommunityOrderAccess(db, a, id)
	if err != nil {
		return nil, err
	}
	if seq < 0 || seq > o.LastSeq {
		return nil, communityError(400, "INVALID_INPUT", "Invalid read cursor")
	}
	row := model.CommunityReadCursor{OrderID: id, UserID: a.UserID, LastReadSeq: seq}
	err = db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "order_id"}, {Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"last_read_seq": gorm.Expr("CASE WHEN community_read_cursors.last_read_seq < ? THEN ? ELSE community_read_cursors.last_read_seq END", seq, seq)})}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	if err = db.First(&row, "order_id = ? AND user_id = ?", id, a.UserID).Error; err != nil {
		return nil, err
	}
	var unread int64
	err = db.Model(&model.CommunityMessage{}).Where("order_id = ? AND seq > ? AND sender_id <> ?", id, row.LastReadSeq, a.UserID).Count(&unread).Error
	return map[string]int64{"last_read_seq": row.LastReadSeq, "unread_count": unread}, err
}

func CommunityStatusValid(s string) bool { return slices.Contains(CommunityStatuses, s) }
