package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"net/mail"
	"strings"
	"time"
)

type DemandEmailSender func(context.Context, common.DemandSMTPConfig, common.DemandSMTPMessage, func() error) common.DemandSMTPResult

const demandEmailLeaseSeconds int64 = 90

func recoverDemandEmailLeases(db *gorm.DB, now int64) error {
	var rows []model.DemandEmailDelivery
	if err := db.Where("status IN ? AND lease_until > 0 AND lease_until <= ?", []string{"leased", "sending"}, now).Order("lease_until").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, d := range rows {
		state, code, stage := "pending", "", ""
		if d.Status == "sending" {
			state, code, stage = "unknown", "smtp_result_unknown", "recovery"
		}
		if err := db.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND lease_token = ? AND lease_until <= ?", d.ID, d.Status, d.LeaseToken, now).Updates(map[string]any{"status": state, "error_code": code, "error_stage": stage, "lease_until": 0, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}
func demandEmailCurrentPause(db *gorm.DB, cfg DemandEmailConfig, d model.DemandEmailDelivery) (string, error) {
	if reason := demandEmailPauseReason(cfg, d); reason != "" {
		return reason, nil
	}
	if d.Domain == "official" {
		var n int64
		if err := db.Model(&model.Demand{}).Where("id = ?", d.EntityID).Count(&n).Error; err != nil {
			return "", err
		}
		if n != 1 {
			return "entity_unavailable", nil
		}
	}
	if d.Domain == "community" {
		var o model.CommunityOrder
		err := db.First(&o, "id = ?", d.EntityID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "entity_unavailable", nil
		}
		if err != nil {
			return "", err
		}
		if d.Type == "community_pending" && o.ApprovalStatus != "pending" {
			return "already_reviewed", nil
		}
	}
	return "", nil
}
func demandEmailMessage(db *gorm.DB, cfg DemandEmailConfig, d model.DemandEmailDelivery) (common.DemandSMTPMessage, error) {
	type totals struct{ TextCount, ImageCount, VideoCount int64 }
	var counts totals
	err := db.Model(&model.DemandEmailEvent{}).Select("COALESCE(SUM(text_count),0) AS text_count, COALESCE(SUM(image_count),0) AS image_count, COALESCE(SUM(video_count),0) AS video_count").Where("id IN (?)", db.Model(&model.DemandEmailEventRecipient{}).Select("event_id").Where("delivery_id = ?", d.ID)).Scan(&counts).Error
	if err != nil {
		return common.DemandSMTPMessage{}, err
	}
	label := "需求邮件测试"
	summary := "这是一封需求提醒测试邮件。"
	switch d.Type {
	case "official_created":
		label = "新官方定制需求"
		summary = "客户已提交新的官方定制需求，请进入后台查看并分配。"
	case "community_pending":
		label = "新社区需求待审核"
		summary = "客户已发布新的社区需求，请进入后台审核。"
	case "customer_message":
		label = "客户新消息"
		summary = fmt.Sprintf("新增 %d 条客户消息：文字消息 %d 条、图片 %d 张、视频 %d 个。消息内容和附件请在后台查看。", d.EventCount, counts.TextCount, counts.ImageCount, counts.VideoCount)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "云枢需求提醒\n\n类型：%s\n需求编号：%s\n", label, d.Number)
	if d.Title != "" {
		fmt.Fprintf(&body, "标题：%s\n", demandEmailSafeLabel(d.Title, 120))
	}
	if d.Budget != "" {
		fmt.Fprintf(&body, "预算：%s\n", demandEmailSafeLabel(d.Budget, 80))
	}
	fmt.Fprintf(&body, "\n%s\n", summary)
	if link := DemandEmailDetailURL(cfg, d.Domain, d.EntityID); link != "" {
		fmt.Fprintf(&body, "\n后台详情：%s\n", link)
	}
	return common.DemandSMTPMessage{Recipient: d.Recipient, Subject: "云枢 · " + label, Text: body.String(), MessageID: d.MessageID}, nil
}

// One attempt has a durable fence before any network. SMTP executes outside all business/lease transactions.
func ProcessDemandEmailOnce(ctx context.Context, db *gorm.DB, now time.Time, sender DemandEmailSender) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	db = db.WithContext(ctx)
	at := now.Unix()
	if err := recoverDemandEmailLeases(db, at); err != nil {
		return false, err
	}
	var d model.DemandEmailDelivery
	err := db.Where("status = ? AND due_at <= ?", "pending", at).Order("due_at, created_at, id").First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	token := uuid.NewString()
	res := db.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND due_at <= ?", d.ID, "pending", at).Updates(map[string]any{"status": "leased", "lease_token": token, "lease_until": at + demandEmailLeaseSeconds, "updated_at": at})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected != 1 {
		return true, nil
	}
	smtpConfig := common.SnapshotDemandSMTPConfig()
	pause := ""
	begin := false
	var cfg DemandEmailConfig
	err = db.Transaction(func(tx *gorm.DB) error {
		var e error
		cfg, e = lockedDemandEmailConfig(tx)
		if e != nil {
			return e
		}
		if e = tx.First(&d, "id = ?", d.ID).Error; e != nil {
			return e
		}
		if d.Status != "leased" || d.LeaseToken != token {
			return nil
		}
		pause, e = demandEmailCurrentPause(tx, cfg, d)
		if e != nil {
			return e
		}
		if pause != "" {
			return tx.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND lease_token = ?", d.ID, "leased", token).Updates(map[string]any{"status": "paused", "pause_reason": pause, "lease_until": 0, "updated_at": at}).Error
		}
		if d.AttemptCount >= 3 {
			return tx.Model(&model.DemandEmailDelivery{}).Where("id = ? AND lease_token = ?", d.ID, token).Updates(map[string]any{"status": "failed", "lease_until": 0, "updated_at": at}).Error
		}
		if d.MessageID == "" {
			domain := "localhost"
			from, e := mail.ParseAddress(smtpConfig.From)
			if e == nil {
				if _, right, ok := strings.Cut(from.Address, "@"); ok {
					domain = right
				}
			}
			d.MessageID = "<demand-email." + d.ID + "@" + domain + ">"
		}
		startedAt := max(at, time.Now().Unix())
		r := tx.Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND lease_token = ?", d.ID, "leased", token).Updates(map[string]any{"status": "sending", "attempt_count": gorm.Expr("attempt_count + 1"), "message_id": d.MessageID, "lease_until": startedAt + demandEmailLeaseSeconds, "updated_at": startedAt})
		if r.Error != nil {
			return r.Error
		}
		begin = r.RowsAffected == 1
		d.AttemptCount++
		return nil
	})
	if err != nil || !begin {
		return true, err
	}
	attemptCtx, stopAttempt := context.WithTimeout(ctx, 20*time.Second)
	defer stopAttempt()
	attemptDB := db.WithContext(attemptCtx)
	message, err := demandEmailMessage(attemptDB, cfg, d)
	if err != nil {
		return true, err
	} // Sending fence remains if a post-fence DB read fails; recovery must not resend.
	if sender == nil {
		sender = common.SendDemandEmail
	}
	outcome := sender(attemptCtx, smtpConfig, message, func() error {
		return attemptDB.Transaction(func(tx *gorm.DB) error {
			current, e := lockedDemandEmailConfig(tx)
			if e != nil {
				return e
			}
			var active model.DemandEmailDelivery
			if e = tx.First(&active, "id = ?", d.ID).Error; e != nil {
				return e
			}
			if active.Status != "sending" || active.LeaseToken != token {
				return errors.New("mail attempt no longer owns its fence")
			}
			pause, e = demandEmailCurrentPause(tx, current, active)
			if e != nil {
				return e
			}
			if pause != "" {
				return errors.New("mail delivery paused before payload")
			}
			return nil
		})
	})
	// Canceled network work is joined by the sender. Persist its known result with a separate bounded context.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Backoff starts after the completed attempt, including a slow connection or timeout.
	finishedAt := max(at, time.Now().Unix())
	state := outcome.State
	updates := map[string]any{"lease_until": 0, "updated_at": finishedAt, "error_code": outcome.Code, "error_stage": outcome.Stage}
	switch state {
	case "accepted":
		updates["status"] = "sent"
		updates["sent_at"] = finishedAt
	case "failed":
		updates["status"] = "failed"
		if outcome.Retryable && d.AttemptCount < 3 {
			updates["status"] = "pending"
			delay := int64(5)
			if d.AttemptCount == 2 {
				delay = 30
			}
			updates["due_at"] = finishedAt + delay
		}
	case "not_sent":
		if pause != "" {
			updates["status"] = "paused"
			updates["pause_reason"] = pause
		} else {
			updates["status"] = "failed"
		}
	default:
		updates["status"] = "unknown"
		updates["error_code"] = "smtp_result_unknown"
	}
	r := db.WithContext(finishCtx).Model(&model.DemandEmailDelivery{}).Where("id = ? AND status = ? AND lease_token = ?", d.ID, "sending", token).Updates(updates)
	return true, r.Error
}
func RunDemandEmailWorker(ctx context.Context, db *gorm.DB) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := ProcessDemandEmailOnce(ctx, db, time.Now(), nil)
		if ctx.Err() != nil {
			return
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
