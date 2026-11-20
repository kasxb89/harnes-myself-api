package service

import (
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var demandEmailSensitive = regexp.MustCompile(`(?i)(?:sk[-_]|oc_sk|LTAI|bearer\s|authorization|password|passwd|secret|token\s*[:=]|api.?key|密码|密钥|验证码)|[A-Za-z0-9_-]{24,}|[0-9](?:[0-9\s().+*-]{5,})[0-9]`)

// Only a short, scrubbed title/budget is copied. Chat text and attachment names/URLs never enter mail.
func demandEmailSafeLabel(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	check := norm.NFKC.String(s)
	check = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.IsDigit(r) && (r < '0' || r > '9') {
			return '0'
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.Is(unicode.Pd, r) {
			return '-'
		}
		return r
	}, check)
	if demandEmailSensitive.MatchString(check) || strings.ContainsAny(check, "/\\<>@=&") {
		return "[内容已隐藏]"
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	return string(r[:min(len(r), maxRunes)])
}
func enqueueDemandEmailEvent(tx *gorm.DB, cfg DemandEmailConfig, event model.DemandEmailEvent) error {
	if !cfg.Enabled && event.Type != "test" {
		return nil
	}
	if len(cfg.Recipients) == 0 {
		return nil
	}
	candidate := model.DemandEmailDelivery{Type: event.Type, Recipient: cfg.Recipients[0]}
	if demandEmailPauseReason(cfg, candidate) != "" {
		return nil
	}
	event.ID = uuid.NewString()
	event.Title = demandEmailSafeLabel(event.Title, 120)
	event.Budget = demandEmailSafeLabel(event.Budget, 80)
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	for _, recipient := range cfg.Recipients {
		key := demandEmailKey(recipient)
		var delivery model.DemandEmailDelivery
		merged := false
		if event.Type == "customer_message" {
			headID := demandEmailKey(event.Domain + "\x00" + event.EntityID + "\x00" + key)
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.DemandEmailMergeHead{ID: headID}).Error; err != nil {
				return err
			}
			head, err := model.LockDemandEmailHead(tx, headID)
			if err != nil {
				return err
			}
			if head.DeliveryID != "" {
				// A claimed batch stays frozen even after a definite failure or an expired pre-send lease.
				update := tx.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND due_at > ? AND attempt_count = 0 AND lease_token = ?", head.DeliveryID, "pending", event.CreatedAt, "").Updates(map[string]any{"event_count": gorm.Expr("event_count + 1"), "updated_at": event.CreatedAt})
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected == 1 {
					delivery.ID = head.DeliveryID
					merged = true
				}
			}
			if !merged {
				delivery = newDemandEmailDelivery(event, recipient, key, event.CreatedAt+cfg.MessageMergeSeconds)
				if err = tx.Create(&delivery).Error; err != nil {
					return err
				}
				if err = tx.Model(&model.DemandEmailMergeHead{}).Where("id = ?", headID).Update("delivery_id", delivery.ID).Error; err != nil {
					return err
				}
			}
		} else {
			delivery = newDemandEmailDelivery(event, recipient, key, event.CreatedAt)
			if err := tx.Create(&delivery).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&model.DemandEmailEventRecipient{EventID: event.ID, RecipientKey: key, DeliveryID: delivery.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}
func newDemandEmailDelivery(e model.DemandEmailEvent, recipient, key string, due int64) model.DemandEmailDelivery {
	return model.DemandEmailDelivery{ID: uuid.NewString(), Domain: e.Domain, EntityID: e.EntityID, Type: e.Type, Recipient: recipient, RecipientKey: key, Status: "pending", Number: e.Number, Title: e.Title, Budget: e.Budget, EventCount: 1, DueAt: due, CreatedAt: e.CreatedAt, UpdatedAt: e.CreatedAt}
}
func queueOfficialCreatedEmail(tx *gorm.DB, a DemandActor, d model.Demand) error {
	cfg, err := lockedDemandEmailConfig(tx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || !cfg.Types.OfficialCreated {
		return nil
	}
	return enqueueDemandEmailEvent(tx, cfg, model.DemandEmailEvent{EventKey: "official.created:" + d.ID, Domain: "official", EntityID: d.ID, Type: "official_created", ActorID: a.UserID, Number: d.Number, Title: d.Title, Budget: d.Budget, CreatedAt: d.CreatedAt})
}
func demandEmailOrdinarySender(tx *gorm.DB, id int) (bool, error) {
	var u model.User
	if err := tx.Select("id,role").First(&u, id).Error; err != nil {
		return false, err
	}
	return u.Role < common.RoleAdminUser, nil
}
func queueOfficialMessageEmail(tx *gorm.DB, a DemandActor, d model.Demand, m model.DemandMessage, ids []string) error {
	if a.Admin || m.Author != "user" || m.Kind != "message" {
		return nil
	}
	cfg, err := lockedDemandEmailConfig(tx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || !cfg.Types.CustomerMessage {
		return nil
	}
	ordinary, err := demandEmailOrdinarySender(tx, a.UserID)
	if err != nil || !ordinary {
		return err
	}
	e := model.DemandEmailEvent{EventKey: "official.message:" + m.ID, Domain: "official", EntityID: d.ID, Type: "customer_message", ActorID: a.UserID, Number: d.Number, Title: d.Title, Budget: d.Budget, CreatedAt: m.CreatedAt}
	if strings.TrimSpace(m.Text) != "" {
		e.TextCount = 1
	}
	if len(ids) > 0 {
		var files []model.DemandAttachment
		if err = tx.Select("kind").Where("message_id = ?", m.ID).Find(&files).Error; err != nil {
			return err
		}
		for _, f := range files {
			if f.Kind == "image" {
				e.ImageCount++
			} else if f.Kind == "video" {
				e.VideoCount++
			}
		}
	}
	return enqueueDemandEmailEvent(tx, cfg, e)
}
func queueCommunityCreatedEmail(tx *gorm.DB, a CommunityActor, o model.CommunityOrder) error {
	cfg, err := lockedDemandEmailConfig(tx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || !cfg.Types.CommunityPending {
		return nil
	}
	return enqueueDemandEmailEvent(tx, cfg, model.DemandEmailEvent{EventKey: "community.created:" + o.ID, Domain: "community", EntityID: o.ID, Type: "community_pending", ActorID: a.UserID, Number: o.Number, Title: o.Title, Budget: o.PublicBudget, CreatedAt: o.CreatedAt})
}
func queueCommunityMessageEmail(tx *gorm.DB, a CommunityActor, o model.CommunityOrder, m model.CommunityMessage, ids []string) error {
	if a.Admin || m.AuthorKind != "publisher" && m.AuthorKind != "acceptor" || m.Kind != "message" && m.Kind != "delivery" {
		return nil
	}
	cfg, err := lockedDemandEmailConfig(tx)
	if err != nil {
		return err
	}
	if !cfg.Enabled || !cfg.Types.CustomerMessage {
		return nil
	}
	ordinary, err := demandEmailOrdinarySender(tx, a.UserID)
	if err != nil || !ordinary {
		return err
	}
	e := model.DemandEmailEvent{EventKey: "community.message:" + m.ID, Domain: "community", EntityID: o.ID, Type: "customer_message", ActorID: a.UserID, Number: o.Number, Title: o.Title, Budget: o.PublicBudget, CreatedAt: m.CreatedAt}
	if strings.TrimSpace(m.Text) != "" {
		e.TextCount = 1
	}
	if len(ids) > 0 {
		var files []model.CommunityAttachment
		if err = tx.Select("kind").Where("message_id = ?", m.ID).Find(&files).Error; err != nil {
			return err
		}
		for _, f := range files {
			if f.Kind == "image" {
				e.ImageCount++
			} else if f.Kind == "video" {
				e.VideoCount++
			}
		}
	}
	return enqueueDemandEmailEvent(tx, cfg, e)
}

type DemandEmailTestReceipt struct {
	EventID     string   `json:"event_id"`
	DeliveryIDs []string `json:"delivery_ids"`
	Replayed    bool     `json:"replayed"`
}

func GetDemandEmailTestReceipt(db *gorm.DB, actor int, key string) (DemandEmailTestReceipt, error) {
	out := DemandEmailTestReceipt{DeliveryIDs: []string{}, Replayed: true}
	var event model.DemandEmailEvent
	err := db.First(&event, "event_key = ?", fmt.Sprintf("test:%d:%s", actor, key)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, demandEmailError(404, "NOT_FOUND", "Demand email test receipt not found")
	}
	if err != nil {
		return out, err
	}
	out.EventID = event.ID
	err = db.Model(&model.DemandEmailEventRecipient{}).Where("event_id = ?", event.ID).Order("delivery_id").Pluck("delivery_id", &out.DeliveryIDs).Error
	return out, err
}
func CreateDemandEmailTest(db *gorm.DB, actor int, key string, revision int64) (DemandEmailTestReceipt, error) {
	out := DemandEmailTestReceipt{}
	if _, err := uuid.Parse(key); err != nil || actor < 1 || revision < 0 {
		return out, demandEmailError(400, "INVALID_INPUT", "Invalid demand email settings")
	}
	eventKey := fmt.Sprintf("test:%d:%s", actor, key)
	hash := demandHash(struct {
		Revision int64 `json:"revision"`
	}{revision})
	err := db.Transaction(func(tx *gorm.DB) error {
		var prior model.DemandEmailEvent
		err := tx.First(&prior, "event_key = ?", eventKey).Error
		if err == nil {
			if prior.RequestHash != hash {
				return demandEmailError(409, "REQUEST_CONFLICT", "Demand email test key belongs to different settings")
			}
			out.Replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		cfg, err := lockedDemandEmailConfig(tx)
		if err != nil {
			return err
		}
		// Use a current locking read after the configuration lock; MySQL's earlier
		// repeatable-read snapshot may predate the other successful original request.
		prior, err = model.LockDemandEmailEvent(tx, eventKey)
		if err == nil {
			if prior.RequestHash != hash {
				return demandEmailError(409, "REQUEST_CONFLICT", "Demand email test key belongs to different settings")
			}
			out.Replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if cfg.Revision != revision {
			return demandEmailError(409, "REVISION_CONFLICT", "Demand email settings changed. Reload and try again")
		}
		if cfg.AdminBaseURL == "" || len(cfg.Recipients) == 0 {
			return demandEmailError(400, "INVALID_INPUT", "Invalid demand email settings")
		}
		if !DemandEmailSMTPReady() {
			return demandEmailError(409, "SMTP_UNAVAILABLE", "SMTP is not configured")
		}
		return enqueueDemandEmailEvent(tx, cfg, model.DemandEmailEvent{EventKey: eventKey, RequestHash: hash, Domain: "test", Type: "test", ActorID: actor, Title: "需求邮件测试", Number: "TEST", CreatedAt: time.Now().Unix()})
	})
	if err != nil {
		var existing model.DemandEmailEvent
		if db.First(&existing, "event_key = ?", eventKey).Error == nil {
			if existing.RequestHash != hash {
				return out, demandEmailError(409, "REQUEST_CONFLICT", "Demand email test key belongs to different settings")
			}
			return GetDemandEmailTestReceipt(db, actor, key)
		}
		return out, err
	}
	replayed := out.Replayed
	out, err = GetDemandEmailTestReceipt(db, actor, key)
	out.Replayed = replayed
	return out, err
}
