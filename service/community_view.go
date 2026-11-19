package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type CommunityUser struct {
	ID          int     `json:"id"`
	DisplayName string  `json:"display_name"`
	PhoneE164   *string `json:"phone_e164,omitempty"`
	Status      *int    `json:"status,omitempty"`
}
type CommunityAttachmentView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	Status    string `json:"status"`
	SHA256    string `json:"sha256,omitempty"`
	Scope     string `json:"scope"`
}

func ProjectCommunityAttachment(f model.CommunityAttachment) CommunityAttachmentView {
	status := "uploading"
	if f.Status == "ready" {
		status = "ready"
	}
	return CommunityAttachmentView{f.ID, f.Name, f.Kind, f.MimeType, f.SizeBytes, status, f.SHA256, f.Scope}
}

type CommunityListingView struct {
	SchemaVersion        int                       `json:"schema_version"`
	ID                   string                    `json:"id"`
	Number               string                    `json:"number"`
	Title                string                    `json:"title"`
	PublicSummary        string                    `json:"public_summary"`
	PublicBudget         string                    `json:"public_budget"`
	ExpectedDeliveryDate string                    `json:"expected_delivery_date"`
	ApprovalStatus       string                    `json:"approval_status"`
	ApprovalReason       string                    `json:"approval_reason,omitempty"`
	ServiceProfileID     string                    `json:"service_profile_id"`
	ServiceProfileName   string                    `json:"service_profile_name"`
	Publisher            CommunityUser             `json:"publisher"`
	Status               string                    `json:"status"`
	PublicAttachments    []CommunityAttachmentView `json:"public_attachments"`
	CreatedAt            int64                     `json:"created_at"`
	UpdatedAt            int64                     `json:"updated_at"`
	CanAccept            bool                      `json:"can_accept"`
}
type CommunityOrderView struct {
	SchemaVersion     int                  `json:"schema_version"`
	ID                string               `json:"id"`
	Number            string               `json:"number"`
	Title             string               `json:"title"`
	Status            string               `json:"status"`
	Revision          int64                `json:"revision"`
	ListingVisible    bool                 `json:"listing_visible"`
	Publisher         CommunityUser        `json:"publisher"`
	Acceptor          *CommunityUser       `json:"acceptor"`
	MyRole            string               `json:"my_role"`
	Listing           CommunityListingView `json:"listing"`
	PrivateText       string               `json:"private_text"`
	LastSeq           int64                `json:"last_seq"`
	LastReadSeq       int64                `json:"last_read_seq"`
	UnreadCount       int64                `json:"unread_count"`
	DeliveryMessageID string               `json:"delivery_message_id"`
	CreatedAt         int64                `json:"created_at"`
	UpdatedAt         int64                `json:"updated_at"`
	AcceptedAt        int64                `json:"accepted_at"`
	ClosedAt          int64                `json:"closed_at"`
	CanSend           bool                 `json:"can_send"`
	CanDeliver        bool                 `json:"can_deliver"`
	CanAcceptDelivery bool                 `json:"can_accept_delivery"`
	CanCancel         bool                 `json:"can_cancel"`
	CanReopen         bool                 `json:"can_reopen"`
}
type CommunityMessageView struct {
	ID              string                    `json:"id"`
	OrderID         string                    `json:"order_id"`
	Seq             int64                     `json:"seq"`
	SenderID        int                       `json:"sender_id"`
	AuthorKind      string                    `json:"author_kind"`
	AuthorName      string                    `json:"author_name"`
	Kind            string                    `json:"kind"`
	Text            string                    `json:"text"`
	Attachments     []CommunityAttachmentView `json:"attachments"`
	CreatedAt       int64                     `json:"created_at"`
	ClientMessageID string                    `json:"client_message_id"`
	Read            bool                      `json:"read"`
}

func communityName(s string) string {
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200])
	}
	return s
}
func communityUsers(db *gorm.DB, ids []int, admin bool) (map[int]CommunityUser, error) {
	var users []struct {
		ID          int
		DisplayName string
		PhoneE164   *string
		Status      int
	}
	fields := "id,display_name"
	if admin {
		fields += ",phone_e164,status"
	}
	if err := db.Model(&model.User{}).Select(fields).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	result := map[int]CommunityUser{}
	for _, id := range ids {
		result[id] = CommunityUser{ID: id}
	}
	for _, u := range users {
		v := CommunityUser{ID: u.ID, DisplayName: communityName(u.DisplayName)}
		if admin {
			v.PhoneE164 = u.PhoneE164
			status := u.Status
			v.Status = &status
		}
		result[u.ID] = v
	}
	return result, nil
}
func communityIDs(raw string) ([]string, error) {
	out := []string{}
	if raw == "" {
		return out, nil
	}
	err := common.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out, err
}
func ProjectCommunityListings(db *gorm.DB, a CommunityActor, orders []model.CommunityOrder) ([]CommunityListingView, error) {
	out := make([]CommunityListingView, 0, len(orders))
	if len(orders) == 0 {
		return out, nil
	}
	cfg, err := LoadCommunityConfig(db)
	if err != nil {
		return nil, err
	}
	users := []int{}
	orderIDs := []string{}
	for _, o := range orders {
		users = append(users, o.PublisherID)
		orderIDs = append(orderIDs, o.ID)
	}
	names, err := communityUsers(db, users, false)
	if err != nil {
		return nil, err
	}
	var files []model.CommunityAttachment
	if err = db.Where("order_id IN ? AND scope = ? AND message_id = ? AND status = ?", orderIDs, "listing", "listing", "ready").Find(&files).Error; err != nil {
		return nil, err
	}
	attachments := map[string]map[string]CommunityAttachmentView{}
	for _, f := range files {
		if attachments[f.OrderID] == nil {
			attachments[f.OrderID] = map[string]CommunityAttachmentView{}
		}
		attachments[f.OrderID][f.ID] = ProjectCommunityAttachment(f)
	}
	for _, o := range orders {
		ids, err := communityIDs(o.PublicAttachmentsJSON)
		if err != nil {
			return nil, err
		}
		assets := []CommunityAttachmentView{}
		for _, id := range ids {
			if f, ok := attachments[o.ID][id]; ok {
				assets = append(assets, f)
			}
		}
		reason := ""
		if o.ApprovalStatus == "rejected" && (a.Admin || o.PublisherID == a.UserID) {
			reason = o.ApprovalReason
		}
		out = append(out, CommunityListingView{SchemaVersion: 1, ID: o.ID, Number: o.Number, Title: o.Title, PublicSummary: o.PublicSummary, PublicBudget: o.PublicBudget, ExpectedDeliveryDate: o.ExpectedDeliveryDate, ApprovalStatus: CommunityApprovalStatus(o), ApprovalReason: reason, ServiceProfileID: o.ServiceProfileID, ServiceProfileName: o.ServiceProfileName, Publisher: names[o.PublisherID], Status: o.Status, PublicAttachments: assets, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, CanAccept: cfg.Enabled && o.Visible && o.ApprovalStatus == "approved" && o.Status == "open" && o.AcceptorID == 0 && o.PublisherID != a.UserID})
	}
	return out, nil
}
func ProjectCommunityOrders(db *gorm.DB, a CommunityActor, orders []model.CommunityOrder) ([]CommunityOrderView, error) {
	out := make([]CommunityOrderView, 0, len(orders))
	if len(orders) == 0 {
		return out, nil
	}
	listings, err := ProjectCommunityListings(db, a, orders)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadCommunityConfig(db)
	if err != nil {
		return nil, err
	}
	ids, users := []string{}, []int{}
	for _, o := range orders {
		ids = append(ids, o.ID)
		users = append(users, o.PublisherID)
		if o.AcceptorID != 0 {
			users = append(users, o.AcceptorID)
		}
	}
	names, err := communityUsers(db, users, a.Admin)
	if err != nil {
		return nil, err
	}
	var cursors []model.CommunityReadCursor
	if err = db.Where("order_id IN ? AND user_id = ?", ids, a.UserID).Find(&cursors).Error; err != nil {
		return nil, err
	}
	read := map[string]int64{}
	for _, c := range cursors {
		read[c.OrderID] = c.LastReadSeq
	}
	// Group unread counts in one query rather than querying per order.
	type unreadRow struct {
		OrderID string
		Total   int64
	}
	var counts []unreadRow
	q := db.Table("community_messages AS m").Select("m.order_id,COUNT(*) AS total").Joins("LEFT JOIN community_read_cursors AS c ON c.order_id = m.order_id AND c.user_id = ?", a.UserID).Where("m.order_id IN ? AND m.seq > COALESCE(c.last_read_seq,0) AND m.sender_id <> ?", ids, a.UserID).Group("m.order_id")
	if err = q.Scan(&counts).Error; err != nil {
		return nil, err
	}
	unread := map[string]int64{}
	for _, c := range counts {
		unread[c.OrderID] = c.Total
	}
	for i, o := range orders {
		role := "publisher"
		if a.Admin {
			role = "admin"
		} else if o.AcceptorID == a.UserID {
			role = "acceptor"
		}
		var acceptor *CommunityUser
		if o.AcceptorID != 0 {
			v := names[o.AcceptorID]
			acceptor = &v
		}
		out = append(out, CommunityOrderView{SchemaVersion: 1, ID: o.ID, Number: o.Number, Title: o.Title, Status: o.Status, Revision: o.Revision, ListingVisible: o.Visible, Publisher: names[o.PublisherID], Acceptor: acceptor, MyRole: role, Listing: listings[i], PrivateText: o.PrivateText, LastSeq: o.LastSeq, LastReadSeq: read[o.ID], UnreadCount: unread[o.ID], DeliveryMessageID: o.DeliveryMessageID, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, AcceptedAt: o.AcceptedAt, ClosedAt: o.ClosedAt, CanSend: !communityClosed(o.Status), CanDeliver: o.AcceptorID == a.UserID && o.Status == "accepted", CanAcceptDelivery: o.PublisherID == a.UserID && o.Status == "awaiting_acceptance", CanCancel: !communityClosed(o.Status) && (a.Admin || o.PublisherID == a.UserID && o.AcceptorID == 0), CanReopen: cfg.Enabled && o.Status == "cancelled" && o.AcceptorID == 0 && o.AcceptedAt == 0 && (a.Admin || o.PublisherID == a.UserID) && o.Visible})
	}
	return out, nil
}
func ProjectCommunityMessages(db *gorm.DB, a CommunityActor, o model.CommunityOrder, messages []model.CommunityMessage) ([]CommunityMessageView, error) {
	out := make([]CommunityMessageView, 0, len(messages))
	if len(messages) == 0 {
		return out, nil
	}
	users, messageIDs := []int{}, []string{}
	for _, m := range messages {
		if m.SenderID != 0 {
			users = append(users, m.SenderID)
		}
		messageIDs = append(messageIDs, m.ID)
	}
	names, err := communityUsers(db, users, false)
	if err != nil {
		return nil, err
	}
	var files []model.CommunityAttachment
	if err = db.Where("order_id = ? AND message_id IN ? AND scope = ? AND status = ?", o.ID, messageIDs, "conversation", "ready").Find(&files).Error; err != nil {
		return nil, err
	}
	byMessage := map[string]map[string]CommunityAttachmentView{}
	for _, f := range files {
		if byMessage[f.MessageID] == nil {
			byMessage[f.MessageID] = map[string]CommunityAttachmentView{}
		}
		byMessage[f.MessageID][f.ID] = ProjectCommunityAttachment(f)
	}
	var cursors []model.CommunityReadCursor
	if err = db.Where("order_id = ?", o.ID).Find(&cursors).Error; err != nil {
		return nil, err
	}
	read := map[int]int64{}
	for _, c := range cursors {
		read[c.UserID] = c.LastReadSeq
	}
	for _, m := range messages {
		assets := []CommunityAttachmentView{}
		ids, err := communityIDs(m.AttachmentsJSON)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if f, ok := byMessage[m.ID][id]; ok {
				assets = append(assets, f)
			}
		}
		other := o.AcceptorID
		if m.SenderID == o.AcceptorID {
			other = o.PublisherID
		}
		seen := other != 0 && read[other] >= m.Seq
		key := m.ClientMessageID
		if m.Kind == "system" || len(key) > 80 {
			key = ""
		}
		if m.ClientMessageID == communityInternalKey("create", o.ID, o.ClientRequestID) {
			key = "create:" + o.ClientRequestID
			if len(key) > 80 {
				key = ""
			}
		}
		out = append(out, CommunityMessageView{ID: m.ID, OrderID: m.OrderID, Seq: m.Seq, SenderID: m.SenderID, AuthorKind: m.AuthorKind, AuthorName: names[m.SenderID].DisplayName, Kind: m.Kind, Text: m.Text, Attachments: assets, CreatedAt: m.CreatedAt, ClientMessageID: key, Read: seen})
	}
	return out, nil
}
