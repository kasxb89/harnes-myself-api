package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelProfitBillingRecord is the financial projection of a billing event in
// the primary database. Usage-log retention, a separate LOG_DB, and disabling
// consumption logs must not erase newly collected financial evidence. It
// deliberately excludes prompts, credentials, IPs, and routing diagnostics.
type ModelProfitBillingRecord struct {
	ID                int64  `gorm:"primaryKey"`
	EventKey          string `gorm:"type:varchar(64);uniqueIndex"`
	FundingEventID    int64  `gorm:"index"`
	FundingRequestID  int64  `gorm:"index"`
	FundingIsDelta    bool
	HistoricalLogID   int64
	HistoricalLogHash string `gorm:"type:varchar(64);index"`
	UserID            int    `gorm:"index"`
	CreatedAt         int64  `gorm:"index:idx_profit_record_time_model,priority:1"`
	Type              int
	ModelName         string `gorm:"type:varchar(255);index:idx_profit_record_time_model,priority:2"`
	ChannelID         int
	RequestID         string `gorm:"type:varchar(64)"`
	Quota             int
	PromptTokens      int
	CompletionTokens  int
	Other             string `gorm:"type:text"`
}

func modelProfitRecordLog(record ModelProfitBillingRecord) Log {
	id := record.ID
	if record.HistoricalLogHash != "" {
		id = record.HistoricalLogID
	}
	return Log{Id: int(id), UserId: record.UserID, CreatedAt: record.CreatedAt, Type: record.Type, ModelName: record.ModelName, ChannelId: record.ChannelID, RequestId: record.RequestID, Quota: record.Quota, PromptTokens: record.PromptTokens, CompletionTokens: record.CompletionTokens, Other: record.Other}
}

// recordModelProfitBilling persists before the optional usage-log write. Only
// this server-side path marks a row as collected financial evidence.
func recordModelProfitBilling(log *Log, other *LogOther) error {
	if log == nil || log.ModelName == "" || (log.Type != LogTypeConsume && log.Type != LogTypeRefund) {
		return nil
	}
	ensureLogRequestId(log)
	var facts profitLogFacts
	if err := common.UnmarshalJsonStr(log.Other, &facts); err != nil {
		return err
	}
	eventKey := common.NewRequestId()
	if facts.AdminInfo.Funding != nil && facts.AdminInfo.Funding.EventID > 0 {
		mode := "total"
		if facts.AdminInfo.FundingSigned {
			mode = "delta"
		}
		eventKey = fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%d:%d:%d:%s", log.UserId, facts.AdminInfo.Funding.EventID, log.Type, mode)))
	}
	facts.AdminInfo.ProfitRecordKey = eventKey
	data, err := common.Marshal(facts)
	if err != nil {
		return err
	}
	record := ModelProfitBillingRecord{EventKey: eventKey, UserID: log.UserId, CreatedAt: log.CreatedAt,
		Type: log.Type, ModelName: log.ModelName, ChannelID: log.ChannelId, RequestID: log.RequestId,
		Quota: log.Quota, PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens, Other: string(data)}
	if facts.AdminInfo.Funding != nil {
		record.FundingEventID = facts.AdminInfo.Funding.EventID
		record.FundingIsDelta = facts.AdminInfo.FundingSigned
		if record.FundingEventID > 0 {
			var event WalletFundingEvent
			if err := DB.Where("id = ? AND user_id = ?", record.FundingEventID, log.UserId).First(&event).Error; err != nil {
				return err
			}
			record.FundingRequestID = event.FundingRequestID
		}
	}
	if err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&record).Error; err != nil {
		return err
	}
	other.SetAdmin("profit_record_key", eventKey)
	log.Other = other.JSONString()
	return nil
}

func readModelProfitBillingRecords(ctx context.Context, filter ModelProfitFilter, limit int) ([]Log, error) {
	query := DB.WithContext(ctx).Where("created_at >= ? AND created_at < ? AND type IN ?", filter.Start, filter.End, []int{LogTypeConsume, LogTypeRefund})
	if filter.ModelName != "" {
		query = query.Where("model_name = ?", filter.ModelName)
	}
	var records []ModelProfitBillingRecord
	if err := query.Order("created_at, id").Limit(limit + 1).Find(&records).Error; err != nil {
		return nil, err
	}
	if len(records) > limit {
		return nil, ErrModelProfitTooLarge
	}
	logs := make([]Log, 0, len(records))
	for _, record := range records {
		logs = append(logs, modelProfitRecordLog(record))
	}
	return logs, nil
}

func mergeModelProfitBillingLogs(ctx context.Context, legacy, collected []Log) ([]Log, map[string]bool, error) {
	keys := make(map[string]bool, len(collected))
	collectedIdentities := make(map[string]bool, len(collected))
	for _, log := range collected {
		var facts profitLogFacts
		if common.UnmarshalJsonStr(log.Other, &facts) == nil && facts.AdminInfo.ProfitRecordKey != "" {
			keys[facts.AdminInfo.ProfitRecordKey] = true
			collectedIdentities[profitLogIdentity(log)] = true
		}
	}
	// A usage-log replay can occur in a later reporting window than the
	// immutable financial record it projects. Deduplicate against primary
	// evidence globally, not just records inside this query's time range.
	projectionKeys := make([]string, 0)
	historicalHashes := make([]string, 0, len(legacy))
	for _, log := range legacy {
		historicalHashes = append(historicalHashes, historicalProfitLogHash(log))
		var facts profitLogFacts
		if common.UnmarshalJsonStr(log.Other, &facts) == nil && facts.AdminInfo.ProfitRecordKey != "" && !keys[facts.AdminInfo.ProfitRecordKey] {
			projectionKeys = append(projectionKeys, facts.AdminInfo.ProfitRecordKey)
		}
	}
	historical := make(map[string]bool)
	for start := 0; start < len(historicalHashes); start += 200 {
		var persisted []string
		if err := DB.WithContext(ctx).Model(&ModelProfitBillingRecord{}).Where("historical_log_hash IN ?", historicalHashes[start:min(start+200, len(historicalHashes))]).Pluck("historical_log_hash", &persisted).Error; err != nil {
			return nil, nil, err
		}
		for _, hash := range persisted {
			historical[hash] = true
		}
	}
	slices.Sort(projectionKeys)
	projectionKeys = slices.Compact(projectionKeys)
	for start := 0; start < len(projectionKeys); start += 200 {
		var persisted []string
		if err := DB.WithContext(ctx).Model(&ModelProfitBillingRecord{}).Where("event_key IN ?", projectionKeys[start:min(start+200, len(projectionKeys))]).Pluck("event_key", &persisted).Error; err != nil {
			return nil, nil, err
		}
		for _, key := range persisted {
			keys[key] = true
		}
	}
	for _, log := range legacy {
		if historical[historicalProfitLogHash(log)] {
			continue
		}
		var facts profitLogFacts
		if common.UnmarshalJsonStr(log.Other, &facts) == nil && keys[facts.AdminInfo.ProfitRecordKey] {
			continue
		}
		collected = append(collected, log)
	}
	if len(collected) > maxProfitBillingEvents {
		return nil, nil, ErrModelProfitTooLarge
	}
	slices.SortStableFunc(collected, func(a, b Log) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return 0
	})
	return collected, collectedIdentities, nil
}

// mainProfitRecordQuery is shared by history recovery and the model selector.
// The primary database remains authoritative even if usage logs were deleted.
func mainProfitRecordQuery(ctx context.Context) *gorm.DB {
	return DB.WithContext(ctx).Model(&ModelProfitBillingRecord{})
}

// A wallet debit can commit immediately before the process loses its usage
// result. Never interpret that missing financial projection as zero work or
// complete cash profit. Outstanding requests remain visible as pending.
func readUnreconciledProfitFunding(ctx context.Context, filter ModelProfitFilter) ([]Log, error) {
	statement := &gorm.Statement{DB: DB}
	eventTable := statement.Quote(DB.NamingStrategy.TableName("WalletFundingEvent"))
	requestTable := statement.Quote(DB.NamingStrategy.TableName("WalletFundingRequest"))
	recordTable := statement.Quote(DB.NamingStrategy.TableName("ModelProfitBillingRecord"))
	query := DB.WithContext(ctx).Table(eventTable+" AS funding_event").
		Joins("JOIN "+requestTable+" AS funding_request ON funding_request.id = funding_event.funding_request_id").
		Where("funding_event.delta_quota <> ? AND funding_event.purpose IN ?", 0, []string{"relay", "realtime", "violation_fee"}).
		Where("funding_event.created_at >= ? AND funding_event.created_at < ?", filter.Start, filter.End).
		Where("NOT EXISTS (SELECT 1 FROM "+recordTable+" AS financial_record WHERE financial_record.funding_request_id = funding_event.funding_request_id AND (financial_record.funding_event_id = funding_event.id OR (financial_record.funding_is_delta = ? AND financial_record.funding_event_id > funding_event.id)))", false).
		Where("funding_request.quota > ? OR EXISTS (SELECT 1 FROM "+recordTable+" AS previous_record WHERE previous_record.funding_request_id = funding_request.id)", 0)
	if filter.ModelName != "" {
		query = query.Where("funding_request.model_name = ?", filter.ModelName)
	}
	var events []struct {
		WalletFundingEvent
		ModelName string
		ChannelID int
		Recorded  int64
		Earlier   int64
	}
	if err := query.Select("funding_event.*, funding_request.model_name, funding_request.channel_id, CASE WHEN EXISTS (SELECT 1 FROM " + recordTable + " AS prior_record WHERE prior_record.funding_request_id = funding_request.id) THEN 1 ELSE 0 END AS recorded, CASE WHEN EXISTS (SELECT 1 FROM " + eventTable + " AS earlier_event WHERE earlier_event.funding_request_id = funding_request.id AND earlier_event.id < funding_event.id AND earlier_event.delta_quota <> 0) THEN 1 ELSE 0 END AS earlier").Order("funding_event.id").Limit(maxProfitBillingEvents + 1).Find(&events).Error; err != nil {
		return nil, err
	}
	if len(events) > maxProfitBillingEvents {
		return nil, ErrModelProfitTooLarge
	}
	logs := make([]Log, 0, len(events))
	type pendingBucket struct {
		RequestID int64
		Day       int64
	}
	positions := make(map[pendingBucket]int)
	for _, event := range events {
		bucket := pendingBucket{RequestID: event.FundingRequestID, Day: (event.CreatedAt + 8*3600) / 86400}
		if index, exists := positions[bucket]; exists {
			value := int64(logs[index].Quota)
			if logs[index].Type == LogTypeRefund {
				value = -value
			}
			value += event.DeltaQuota
			logs[index].Type = LogTypeConsume
			if value < 0 {
				logs[index].Type = LogTypeRefund
				value = -value
			}
			logs[index].Quota = int(value)
			continue
		}
		name := event.ModelName
		if name == "" {
			name = "unreconciled-wallet"
		}
		quota, kind := event.DeltaQuota, LogTypeConsume
		if quota < 0 {
			quota, kind = -quota, LogTypeRefund
		}
		positions[bucket] = len(logs)
		facts, err := common.Marshal(profitLogFacts{FundingPending: true, FundingPendingInitial: event.Recorded == 0 && event.Earlier == 0})
		if err != nil {
			return nil, err
		}
		logs = append(logs, Log{UserId: event.UserID, CreatedAt: event.CreatedAt, Type: kind,
			ModelName: name, ChannelId: event.ChannelID, RequestId: event.RequestID, Quota: int(quota), Other: string(facts)})
	}
	return logs, nil
}
