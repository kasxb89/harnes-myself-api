package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const BlackrxCreateRejectionSourceHash = "0de9432d2de2821532bf42c1a102f638264d5eb539c98f8de6fac401e1a9d596"

// MediaCreateRejection contains only the original create refusal and the exact
// reserved funding identity. It is private evidence, never a provider task ID.
// A nonempty proof alone cannot authorize a client to resubmit.
type MediaCreateRejection struct {
	SchemaVersion         int    `json:"schema_version"`
	TaskID                string `json:"task_id"`
	RequestID             string `json:"request_id"`
	RouteDigest           string `json:"route_digest"`
	AccountIdentityDigest string `json:"account_identity_digest"`
	PluginVersion         string `json:"plugin_version"`
	PluginSourceHash      string `json:"plugin_source_hash"`
	PluginGeneration      uint64 `json:"plugin_generation"`
	UpstreamModel         string `json:"upstream_model"`
	URL                   string `json:"url"`
	Method                string `json:"method"`
	HTTPStatus            int    `json:"http_status"`
	Body                  string `json:"body"`
	BodySHA256            string `json:"body_sha256"`
	SendingAt             int64  `json:"sending_at"`
	OriginalQuota         int    `json:"original_quota"`
	FundingSource         string `json:"funding_source"`
	FundingRequestID      string `json:"funding_request_id"`
	FundingReserved       int    `json:"funding_reserved"`
	SubscriptionID        int    `json:"subscription_id"`
	SubscriptionExtra     int    `json:"subscription_extra"`
	SubscriptionEpoch     *int64 `json:"subscription_epoch"`
	TokenID               int    `json:"token_id"`
	TokenReserved         int    `json:"token_reserved"`
}

// IsBlackrxModelMissingCreateBody deliberately accepts only this documented,
// finite create rejection shape. Duplicate keys, IDs, receipts, extra fields,
// HTML, truncated JSON and contradictory states cannot match it. Whitespace
// outside JSON strings is immaterial; escaped/alternative shapes remain unknown.
// Model identity is bounded by the host's existing Blackrx video input schema,
// while callers separately require the frozen, reviewed plugin source.
func IsBlackrxModelMissingCreateBody(body []byte, upstreamModel string) bool {
	if len(body) == 0 || len(body) > 4096 || !utf8.Valid(body) || len(BlackrxSupportedDurations(upstreamModel)) == 0 {
		return false
	}
	var decoded any
	if common.Unmarshal(body, &decoded) != nil {
		return false
	}
	compact := make([]byte, 0, len(body))
	quoted, escaped := false, false
	for _, c := range body {
		if quoted {
			compact = append(compact, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		}
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			compact = append(compact, c)
		}
	}
	message, _ := common.Marshal("模型不存在：" + upstreamModel)
	a := `{"error":{"message":` + string(message) + `,"type":"invalid_request_error"}}`
	b := `{"error":{"type":"invalid_request_error","message":` + string(message) + `}}`
	return string(compact) == a || string(compact) == b
}

func validMediaCreateRejection(task *Task, proof *MediaCreateRejection) bool {
	if task == nil || proof == nil || task.PrivateData.Execution == nil || task.PrivateData.Execution.TaskPlugin == nil {
		return false
	}
	plugin := task.PrivateData.Execution.TaskPlugin
	hash := sha256.Sum256([]byte(proof.Body))
	return proof.SchemaVersion == 1 && proof.TaskID == task.TaskID && proof.RequestID != "" && proof.RequestID == task.PrivateData.Execution.RequestID &&
		proof.RouteDigest != "" && proof.RouteDigest == task.PrivateData.RouteDigest && proof.AccountIdentityDigest == mediaIdentityDigest(task.PrivateData.Key, MediaVideo) && plugin.Key == "blackrxai-video" && plugin.APIVersion == 1 && plugin.Version == "1.0.2" &&
		proof.PluginVersion == plugin.Version && plugin.SourceHash == BlackrxCreateRejectionSourceHash && proof.PluginSourceHash == plugin.SourceHash && proof.PluginGeneration == plugin.Generation && proof.PluginGeneration > 0 && task.Platform == "blackrxai-video" &&
		task.PrivateData.MediaKind == MediaVideo && task.PrivateData.MediaAdmissionID == task.TaskID && task.PrivateData.Key != "" && task.PrivateData.UpstreamTaskID == "" &&
		proof.UpstreamModel == task.Properties.UpstreamModelName && proof.URL == "https://hi.blackrxai.com/v1/videos" && proof.Method == "POST" && proof.HTTPStatus == 404 &&
		proof.BodySHA256 == hex.EncodeToString(hash[:]) && IsBlackrxModelMissingCreateBody([]byte(proof.Body), proof.UpstreamModel) &&
		proof.OriginalQuota >= 0 && proof.OriginalQuota <= common.MaxWalletQuota && proof.FundingReserved >= 0 && proof.FundingReserved <= common.MaxWalletQuota &&
		proof.TokenReserved >= 0 && proof.TokenReserved <= common.MaxWalletQuota && proof.SubscriptionExtra >= 0 && proof.SubscriptionExtra <= common.MaxWalletQuota &&
		proof.FundingSource == task.PrivateData.BillingSource && proof.TokenID == task.PrivateData.TokenId && proof.SubscriptionID == task.PrivateData.SubscriptionId &&
		((proof.FundingSource == "wallet" && proof.FundingRequestID != "" && proof.FundingRequestID == task.PrivateData.FundingRequestId && proof.SubscriptionID == 0 && proof.SubscriptionExtra == 0 && proof.SubscriptionEpoch == nil) ||
			(proof.FundingSource == "subscription" && proof.FundingRequestID == proof.RequestID && proof.SubscriptionID > 0 && proof.SubscriptionEpoch != nil && *proof.SubscriptionEpoch >= 0))
}

// SaveMediaCreateRejection durably retains the original refusal before refund.
// It never fabricates a terminal provider task or erases the send fence.
func SaveMediaCreateRejection(ctx context.Context, task *Task, proof MediaCreateRejection) error {
	if task == nil {
		return ErrMediaAdmissionUnavailable
	}
	proof.AccountIdentityDigest = mediaIdentityDigest(task.PrivateData.Key, MediaVideo)
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockMediaPolicy(tx); err != nil {
			return err
		}
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		var pool MediaPool
		if err := tx.Where("id = ? AND media_kind = ? AND identity_digest = ?", admission.PoolID, MediaVideo, mediaIdentityDigest(task.PrivateData.Key, MediaVideo)).First(&pool).Error; err != nil {
			return ErrMediaAdmissionUnavailable
		}
		var persisted Task
		if err := lockForUpdate(tx).Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
			return err
		}
		if proof.FundingSource == "subscription" {
			var record SubscriptionPreConsumeRecord
			if err := lockForUpdate(tx).Where("request_id = ? AND user_id = ? AND user_subscription_id = ?", proof.FundingRequestID, persisted.UserId, proof.SubscriptionID).First(&record).Error; err != nil {
				return err
			}
			if record.QuotaEpoch == nil {
				return ErrMediaAdmissionUnavailable
			}
			epoch := *record.QuotaEpoch
			proof.SubscriptionEpoch = &epoch
		}
		if !reflect.DeepEqual(persisted.PrivateData, task.PrivateData) || persisted.Properties != task.Properties || !validMediaCreateRejection(&persisted, &proof) || admission.SendingAt <= 0 || admission.ProviderTerminal {
			return ErrMediaAdmissionUnavailable
		}
		proof.SendingAt = admission.SendingAt
		encoded, err := common.Marshal(proof)
		if err != nil {
			return err
		}
		if admission.CreateRejection != "" {
			if admission.CreateRejection != string(encoded) {
				return ErrMediaAdmissionUnavailable
			}
			if admission.BillingState == "refund_pending" || admission.State == MediaReleased && admission.BillingState == "refunded" {
				return nil
			}
			return ErrMediaAdmissionUnavailable
		}
		if (admission.State != MediaSending && admission.State != MediaUnknown) || admission.BillingState != "reserved" || persisted.Status != TaskStatusUnknown || persisted.Quota != proof.OriginalQuota {
			return ErrMediaAdmissionUnavailable
		}
		return tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"create_rejection": string(encoded), "billing_state": "refund_pending", "updated_at": time.Now().Unix()}).Error
	})
}

// RefundMediaCreateRejection is an explicit, repeatable recovery operation for
// an already verified rejection. All original funding, token, task and slot
// changes commit together. No provider requests or legacy funding adoption run.
func RefundMediaCreateRejection(ctx context.Context, task *Task) error {
	if task == nil || task.ID <= 0 {
		return ErrMediaAdmissionUnavailable
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockMediaPolicy(tx); err != nil {
			return err
		}
		var admission MediaAdmission
		if err := lockForUpdate(tx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error; err != nil {
			return err
		}
		var pool MediaPool
		if err := tx.Where("id = ? AND media_kind = ? AND identity_digest = ?", admission.PoolID, MediaVideo, mediaIdentityDigest(task.PrivateData.Key, MediaVideo)).First(&pool).Error; err != nil {
			return ErrMediaAdmissionUnavailable
		}
		var persisted Task
		if err := lockForUpdate(tx).Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
			return err
		}
		var proof MediaCreateRejection
		if !decodeMediaCreateRejection(admission.CreateRejection, &proof) || !validMediaCreateRejection(&persisted, &proof) || proof.SendingAt != admission.SendingAt || admission.SendingAt <= 0 || admission.ProviderTerminal {
			return ErrMediaAdmissionUnavailable
		}
		if admission.State == MediaReleased && admission.BillingState == "refunded" && persisted.Status == TaskStatusFailure && persisted.Quota == 0 {
			return nil
		}
		if (admission.State != MediaSending && admission.State != MediaUnknown) || admission.BillingState != "refund_pending" || persisted.Status != TaskStatusUnknown || persisted.Quota != proof.OriginalQuota {
			return ErrMediaAdmissionUnavailable
		}
		if proof.FundingSource == "wallet" {
			var request WalletFundingRequest
			if err := tx.Where("user_id = ? AND request_id = ? AND purpose = ?", persisted.UserId, proof.FundingRequestID, "relay").First(&request).Error; err != nil {
				return err
			}
			if request.Quota != int64(proof.FundingReserved) || request.ChannelID != persisted.ChannelId || request.ModelName != persisted.Properties.OriginModelName || request.LegacyBaselineImported {
				return ErrMediaAdmissionUnavailable
			}
			funding, err := ApplyWalletFundingTargetWithTx(tx, persisted.UserId, proof.FundingRequestID, "relay", 0, false)
			if err != nil {
				return err
			}
			if proof.TokenReserved != proof.FundingReserved {
				return ErrMediaAdmissionUnavailable
			}
			if proof.FundingReserved > 0 && (!funding.Applied || funding.AppliedDelta != -proof.FundingReserved) {
				return ErrMediaAdmissionUnavailable
			}
		} else {
			var record SubscriptionPreConsumeRecord
			if err := lockForUpdate(tx).Where("request_id = ? AND user_id = ? AND user_subscription_id = ?", proof.FundingRequestID, persisted.UserId, proof.SubscriptionID).First(&record).Error; err != nil {
				return err
			}
			if record.Status != "consumed" || record.PreConsumed != int64(proof.FundingReserved) || record.QuotaEpoch == nil || proof.SubscriptionEpoch == nil || *record.QuotaEpoch != *proof.SubscriptionEpoch {
				return ErrMediaAdmissionUnavailable
			}
			var sub UserSubscription
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", proof.SubscriptionID, persisted.UserId).First(&sub).Error; err != nil {
				return err
			}
			total := int64(proof.FundingReserved) + int64(proof.SubscriptionExtra)
			if sub.AmountUsed < total || sub.QuotaEpoch != *proof.SubscriptionEpoch {
				return ErrMediaAdmissionUnavailable
			}
			if err := tx.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("amount_used", sub.AmountUsed-total).Error; err != nil {
				return err
			}
			if err := tx.Model(&SubscriptionPreConsumeRecord{}).Where("id = ?", record.Id).Update("status", "refunded").Error; err != nil {
				return err
			}
		}
		if proof.TokenReserved > 0 && proof.TokenID > 0 {
			result := tx.Model(&Token{}).Where("id = ? AND user_id = ? AND used_quota >= ?", proof.TokenID, persisted.UserId, proof.TokenReserved).Updates(map[string]any{"remain_quota": gorm.Expr("remain_quota + ?", proof.TokenReserved), "used_quota": gorm.Expr("used_quota - ?", proof.TokenReserved)})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("original media token reservation is unconfirmed")
			}
		}
		if err := tx.Model(&Task{}).Where("id = ?", persisted.ID).Updates(map[string]any{"status": TaskStatusFailure, "progress": "100%", "finish_time": time.Now().Unix(), "quota": 0, "fail_reason": "Upstream rejected video creation because the model is unavailable"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&MediaAdmission{}).Where("id = ?", admission.ID).Updates(map[string]any{"billing_state": "refunded", "updated_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return releaseMediaAdmission(tx, &admission)
	})
	if err == nil {
		InvalidateWalletFundingCache(task.UserId)
		_ = InvalidateUserTokensCache(task.UserId)
	}
	return err
}

// MediaConfirmedCreateRejectionState is read-only and deliberately separate
// from the historical not-sent proof, whose sending_at=0 invariant stays intact.
func MediaConfirmedCreateRejectionState(ctx context.Context, task *Task) (bool, error) {
	if task == nil || task.ID <= 0 || task.PrivateData.MediaAdmissionID == "" {
		return false, nil
	}
	var admission MediaAdmission
	err := DB.WithContext(ctx).Where("id = ? AND task_db_id = ? AND channel_id = ?", task.PrivateData.MediaAdmissionID, task.ID, task.ChannelId).First(&admission).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(admission.CreateRejection) == "" {
		return false, nil
	}
	var pool MediaPool
	if err := DB.WithContext(ctx).Where("id = ? AND media_kind = ? AND identity_digest = ?", admission.PoolID, MediaVideo, mediaIdentityDigest(task.PrivateData.Key, MediaVideo)).First(&pool).Error; err != nil {
		return false, nil
	}
	var persisted Task
	if err := DB.WithContext(ctx).Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
		return false, err
	}
	var proof MediaCreateRejection
	return decodeMediaCreateRejection(admission.CreateRejection, &proof) && validMediaCreateRejection(&persisted, &proof) && admission.SendingAt > 0 && proof.SendingAt == admission.SendingAt && !admission.ProviderTerminal && admission.State == MediaReleased && admission.BillingState == "refunded" && persisted.Status == TaskStatusFailure && persisted.Quota == 0, nil
}

func decodeMediaCreateRejection(encoded string, proof *MediaCreateRejection) bool {
	if common.UnmarshalJsonStr(encoded, proof) != nil {
		return false
	}
	canonical, err := common.Marshal(proof)
	return err == nil && string(canonical) == encoded
}

// PendingMediaCreateRejectionTasks returns only bounded, already host-verified
// refund work. Ordinary unknown submissions and provider-ID lookups are absent.
func PendingMediaCreateRejectionTasks(ctx context.Context, limit int) ([]*Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var tasks []*Task
	taskTable := DB.NamingStrategy.TableName("Task")
	admissionTable := DB.NamingStrategy.TableName("MediaAdmission")
	err := DB.WithContext(ctx).Model(&Task{}).Table(taskTable+" AS rejection_task").Select("rejection_task.*").
		Joins("JOIN "+admissionTable+" AS rejection_admission ON rejection_admission.task_db_id = rejection_task.id").
		Where("rejection_admission.state IN ? AND rejection_admission.billing_state = ? AND rejection_admission.create_rejection IS NOT NULL AND rejection_admission.create_rejection <> ? AND rejection_task.status = ?", []string{MediaSending, MediaUnknown}, "refund_pending", "", TaskStatusUnknown).
		Order("rejection_task.id ASC").Limit(limit).Find(&tasks).Error
	return tasks, err
}
