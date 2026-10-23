package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const maxProfitBillingEvents = 100000

type ModelProfitFilter struct {
	Start, End int64
	ModelName  string
}

type ModelProfitTotals struct {
	Requests             int64    `json:"requests"`
	BillingEvents        int64    `json:"billing_events"`
	RevenueCNY           float64  `json:"revenue_cny"`
	RefundCNY            float64  `json:"refund_cny"`
	KnownCostRevenueCNY  float64  `json:"known_cost_revenue_cny"`
	UpstreamCostCNY      *float64 `json:"upstream_cost_cny"`
	GrossProfitCNY       *float64 `json:"gross_profit_cny"`
	ProfitMargin         *float64 `json:"profit_margin"`
	KnownCostRequests    int64    `json:"known_cost_requests"`
	UnknownCostRequests  int64    `json:"unknown_cost_requests"`
	KnownCostEvents      int64    `json:"known_cost_events"`
	UnknownCostEvents    int64    `json:"unknown_cost_events"`
	CostCoverage         float64  `json:"cost_coverage"`
	PendingRequests      int64    `json:"pending_requests"`
	PendingRevenueCNY    float64  `json:"pending_revenue_cny"`
	PendingBillingEvents int64    `json:"pending_billing_events"`
	// Cash attribution is backed by the main-database funding journal. Legacy
	// billing logs alone cannot establish a paid/gift split.
	CashRevenueCNY            *float64         `json:"cash_revenue_cny"`
	GiftConsumedCNY           *float64         `json:"gift_consumed_cny"`
	CashProfitCNY             *float64         `json:"cash_profit_cny"`
	FundingStatus             string           `json:"funding_status"`
	FundingUnknownEvents      int64            `json:"funding_unknown_events"`
	FundingCoverage           float64          `json:"funding_coverage"`
	FundingPolicy             string           `json:"funding_policy"`
	ConfirmedCashRevenueCNY   float64          `json:"confirmed_cash_revenue_cny"`
	ConfirmedPaidConsumedCNY  float64          `json:"confirmed_paid_consumed_cny"`
	ConfirmedGiftConsumedCNY  float64          `json:"confirmed_gift_consumed_cny"`
	UnknownFundingConsumedCNY float64          `json:"unknown_funding_consumed_cny"`
	UnknownCostReasons        map[string]int64 `json:"unknown_cost_reasons,omitempty"`
	UnconfirmedFundingReasons map[string]int64 `json:"unconfirmed_funding_reasons,omitempty"`
	fundingKnownEvents        int64
	cashSum                   decimal.Decimal
	costSum                   float64
	invalidAmount             bool
	priceUnconfirmed          bool
}

type ModelProfitRow struct {
	ModelName string `json:"model_name"`
	ModelProfitTotals
	CustomerAveragePriceCNY *float64 `json:"customer_average_price_cny"`
	UpstreamAverageCostCNY  *float64 `json:"upstream_average_cost_cny"`
	AveragePriceUnit        string   `json:"average_price_unit"`
}

type ModelProfitTrendPoint struct {
	BucketStart int64 `json:"bucket_start"`
	ModelProfitTotals
}

type ModelProfitReport struct {
	Rows                            []ModelProfitRow        `json:"rows"`
	Summary                         ModelProfitTotals       `json:"summary"`
	ModelOptions                    []string                `json:"model_options"`
	Trend                           []ModelProfitTrendPoint `json:"trend"`
	TrendGranularity                string                  `json:"trend_granularity"`
	StartTimestamp                  int64                   `json:"start_timestamp"`
	EndTimestamp                    int64                   `json:"end_timestamp"`
	Timezone                        string                  `json:"timezone"`
	Currency                        string                  `json:"currency"`
	CurrentRate                     float64                 `json:"current_rate"`
	QuotaPerUnit                    float64                 `json:"quota_per_unit"`
	HistoricalConversionApproximate bool                    `json:"historical_conversion_approximate"`
	ProfitBasis                     string                  `json:"profit_basis"`
	CostRulesRevision               int64                   `json:"cost_rules_revision"`
	ConsumptionLoggingEnabled       bool                    `json:"consumption_logging_enabled"`
	CashProfitBasis                 string                  `json:"cash_profit_basis"`
	FundingCollectionStatus         string                  `json:"funding_collection_status"`
	FundingPolicy                   string                  `json:"funding_policy"`
}

type profitLogFacts struct {
	FundingPending          bool               `json:"funding_pending,omitempty"`
	FundingPendingInitial   bool               `json:"funding_pending_initial,omitempty"`
	AdminInfo               profitFundingFacts `json:"admin_info"`
	TaskID                  string             `json:"task_id"`
	IsTask                  bool               `json:"is_task"`
	ActualQuota             *int64             `json:"actual_quota"`
	PreConsumedQuota        *int64             `json:"pre_consumed_quota"`
	UsageFacts              map[string]any     `json:"usage_facts"`
	ImageCount              *float64           `json:"image_count"`
	CacheTokens             *float64           `json:"cache_tokens"`
	CacheCreationTokens     *float64           `json:"cache_creation_tokens"`
	CacheCreationTokens5m   *float64           `json:"cache_creation_tokens_5m"`
	CacheCreationTokens1h   *float64           `json:"cache_creation_tokens_1h"`
	AudioInput              float64            `json:"audio_input"`
	AudioOutput             float64            `json:"audio_output"`
	AudioInputTokenCount    float64            `json:"audio_input_token_count"`
	AudioInputSeparatePrice bool               `json:"audio_input_seperate_price"`
	ToolSurcharges          []any              `json:"tool_surcharges"`
	UsageSemantic           string             `json:"usage_semantic"`
	BillingTokens           map[string]float64 `json:"billing_tokens"`
	MatchedTier             string             `json:"matched_tier"`
}

type profitFundingFacts struct {
	Funding          *WalletFundingAmounts `json:"funding,omitempty"`
	FundingRequestID string                `json:"funding_request_id,omitempty"`
	FundingPurpose   string                `json:"funding_purpose,omitempty"`
	FundingSigned    bool                  `json:"funding_signed,omitempty"`
	ProfitRecordKey  string                `json:"profit_record_key,omitempty"`
}

type profitTaskKey struct {
	Channel, User int
	ID            string
}
type profitCostEvent struct {
	cost                    float64
	known, initial, pending bool
	priceUnconfirmed        bool
	unknownReason           string
	funding                 *WalletFundingAmounts
}

func (totals *ModelProfitTotals) add(log Log, event profitCostEvent, conversion float64) {
	totals.priceUnconfirmed = totals.priceUnconfirmed || event.priceUnconfirmed
	if log.Quota < 0 {
		totals.invalidAmount = true
	}
	amount := float64(log.Quota) * conversion
	if log.Type == LogTypeRefund {
		totals.RefundCNY += amount
		amount = -amount
	}
	totals.RevenueCNY += amount
	totals.BillingEvents++
	totals.FundingPolicy = "fifo"
	fundingReason := ""
	if event.pending {
		totals.FundingUnknownEvents++
		fundingReason = "pending_settlement"
	} else if event.funding == nil {
		totals.FundingUnknownEvents++
		totals.UnknownFundingConsumedCNY += amount
		fundingReason = "historical_funding_evidence_missing"
	} else {
		funding := event.funding
		totals.cashSum = totals.cashSum.Add(funding.KnownCashRevenueCNY)
		totals.ConfirmedCashRevenueCNY = totals.cashSum.InexactFloat64()
		totals.ConfirmedPaidConsumedCNY += float64(funding.PaidQuota) * conversion
		totals.ConfirmedGiftConsumedCNY += float64(funding.GiftQuota) * conversion
		totals.UnknownFundingConsumedCNY += float64(funding.UnknownQuota) * conversion
		if funding.CashRevenueCNY == nil {
			totals.FundingUnknownEvents++
			fundingReason = "payment_receipt_or_source_unverified"
		} else {
			totals.fundingKnownEvents++
		}
	}
	if fundingReason != "" {
		if totals.UnconfirmedFundingReasons == nil {
			totals.UnconfirmedFundingReasons = make(map[string]int64)
		}
		totals.UnconfirmedFundingReasons[fundingReason]++
	}
	if event.initial {
		totals.Requests++
	}
	if event.pending {
		totals.PendingBillingEvents++
		totals.PendingRevenueCNY += amount
		if event.initial {
			totals.PendingRequests++
		}
	}
	if event.known {
		totals.KnownCostEvents++
		totals.KnownCostRevenueCNY += amount
		totals.costSum += event.cost
		if event.initial {
			totals.KnownCostRequests++
		}
	} else {
		totals.UnknownCostEvents++
		reason := event.unknownReason
		if reason == "" {
			reason = "usage_or_settlement_evidence_missing"
		}
		if totals.UnknownCostReasons == nil {
			totals.UnknownCostReasons = make(map[string]int64)
		}
		totals.UnknownCostReasons[reason]++
		if event.initial {
			totals.UnknownCostRequests++
		}
	}
}

func (totals *ModelProfitTotals) finish() {
	totals.FundingStatus = "unattributed"
	totals.FundingPolicy = "fifo"
	if totals.BillingEvents > 0 {
		totals.CostCoverage = float64(totals.KnownCostEvents) / float64(totals.BillingEvents)
		totals.FundingCoverage = float64(totals.fundingKnownEvents) / float64(totals.BillingEvents)
		if totals.FundingUnknownEvents == 0 {
			totals.FundingStatus = "attributed"
			if totals.PendingBillingEvents == 0 && !totals.invalidAmount && !totals.priceUnconfirmed {
				cash, gift := totals.ConfirmedCashRevenueCNY, totals.ConfirmedGiftConsumedCNY
				totals.CashRevenueCNY, totals.GiftConsumedCNY = &cash, &gift
				if totals.UnknownCostEvents == 0 {
					profit := cash - totals.costSum
					totals.CashProfitCNY = &profit
				}
			}
		} else if totals.fundingKnownEvents > 0 {
			totals.FundingStatus = "partial"
		}
	}
	if totals.KnownCostEvents == 0 {
		return
	}
	cost, profit := totals.costSum, totals.KnownCostRevenueCNY-totals.costSum
	totals.UpstreamCostCNY, totals.GrossProfitCNY = &cost, &profit
	if totals.KnownCostRevenueCNY > 0 {
		margin := profit / totals.KnownCostRevenueCNY
		totals.ProfitMargin = &margin
	}
}

// Accounting uses billing-event time [start,end): a refund in a later period
// reverses revenue and the attributable cost in that later period. Requests
// count original charges only; asynchronous settlement deltas are not calls.
func GetModelProfits(ctx context.Context, filter ModelProfitFilter) (ModelProfitReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report := ModelProfitReport{Rows: []ModelProfitRow{}, ModelOptions: []string{}, Trend: []ModelProfitTrendPoint{}, TrendGranularity: "day", StartTimestamp: filter.Start, EndTimestamp: filter.End,
		Timezone: "Asia/Shanghai", Currency: "CNY", CurrentRate: operation_setting.USDExchangeRate, QuotaPerUnit: common.QuotaPerUnit,
		HistoricalConversionApproximate: true, ProfitBasis: "estimated_billed_consumption_margin_not_cash_profit", ConsumptionLoggingEnabled: common.LogConsumeEnabled,
		CashProfitBasis: "cash_attributed_revenue_minus_full_upstream_cost", FundingCollectionStatus: "collected", FundingPolicy: "fifo"}
	if filter.Start < 0 || filter.End <= filter.Start || filter.End > 253402300799 || filter.End-filter.Start > 366*86400 || (filter.ModelName != "" && !validClientModelID(filter.ModelName)) || math.IsNaN(report.CurrentRate) || math.IsInf(report.CurrentRate, 0) || report.CurrentRate <= 0 || report.QuotaPerUnit <= 0 {
		return report, ErrModelProfitParameters
	}
	config, err := GetModelProfitCostRules(ctx)
	if err != nil {
		return report, err
	}
	report.CostRulesRevision = config.Revision
	query := LOG_DB.WithContext(ctx).Model(&Log{}).Where("type IN ? AND created_at >= ? AND created_at < ?", []int{LogTypeConsume, LogTypeRefund}, filter.Start, filter.End)
	if err := query.Session(&gorm.Session{}).Where("model_name <> ?", "").Distinct("model_name").Order("model_name").Limit(maxProfitBillingEvents+1).Pluck("model_name", &report.ModelOptions).Error; err != nil {
		return report, err
	}
	if len(report.ModelOptions) > maxProfitBillingEvents {
		return report, ErrModelProfitTooLarge
	}
	if filter.ModelName != "" {
		query = query.Where("model_name = ?", filter.ModelName)
	}
	logs, err := readProfitLogs(query, maxProfitBillingEvents)
	if err != nil {
		return report, err
	}
	collected, err := readModelProfitBillingRecords(ctx, filter, maxProfitBillingEvents)
	if err != nil {
		return report, err
	}
	logs, collectedIdentities, err := mergeModelProfitBillingLogs(ctx, logs, collected)
	if err != nil {
		return report, err
	}
	unreconciled, err := readUnreconciledProfitFunding(ctx, filter)
	if err != nil {
		return report, err
	}
	logs = append(logs, unreconciled...)
	if len(logs) > maxProfitBillingEvents {
		return report, ErrModelProfitTooLarge
	}
	var collectedModels []string
	if err := mainProfitRecordQuery(ctx).Where("created_at >= ? AND created_at < ?", filter.Start, filter.End).Distinct("model_name").Limit(maxProfitBillingEvents+1).Pluck("model_name", &collectedModels).Error; err != nil {
		return report, err
	}
	report.ModelOptions = append(report.ModelOptions, collectedModels...)
	for _, log := range unreconciled {
		report.ModelOptions = append(report.ModelOptions, log.ModelName)
	}
	slices.Sort(report.ModelOptions)
	report.ModelOptions = slices.Compact(report.ModelOptions)
	if len(report.ModelOptions) > maxProfitBillingEvents {
		return report, ErrModelProfitTooLarge
	}
	taskKeys := make(map[profitTaskKey]struct{})
	for _, log := range logs {
		var facts profitLogFacts
		if common.UnmarshalJsonStr(log.Other, &facts) == nil && facts.TaskID != "" {
			taskKeys[profitTaskKey{log.ChannelId, log.UserId, facts.TaskID}] = struct{}{}
		}
	}
	if len(taskKeys) > 2048 {
		return report, ErrModelProfitTooLarge
	}
	taskEvents, err := modelProfitTaskEvents(ctx, taskKeys, config.Rules)
	if err != nil {
		return report, err
	}
	models := make(map[string]*ModelProfitRow)
	// Match the existing report's Beijing accounting zone. Build calendar
	// buckets in Go rather than dialect-specific SQL, and attribute refunds
	// to their own billing-event day, exactly as rows and summary do.
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	start := time.Unix(filter.Start, 0).In(zone)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, zone)
	for day.Unix() < filter.End {
		report.Trend = append(report.Trend, ModelProfitTrendPoint{BucketStart: day.Unix()})
		day = day.AddDate(0, 0, 1)
	}
	conversion := report.CurrentRate / report.QuotaPerUnit
	for _, log := range logs {
		row := models[log.ModelName]
		if row == nil {
			row = &ModelProfitRow{ModelName: log.ModelName}
			models[log.ModelName] = row
		}
		var facts profitLogFacts
		valid := common.UnmarshalJsonStr(log.Other, &facts) == nil
		event := profitCostEvent{initial: log.Type == LogTypeConsume && (!facts.FundingPending || facts.FundingPendingInitial), pending: facts.FundingPending, priceUnconfirmed: !valid || facts.FundingPending || (facts.IsTask && facts.TaskID == "")}
		if valid && facts.TaskID != "" {
			if stored, exists := taskEvents[profitLogIdentity(log)]; exists {
				event = stored
			} else {
				event.initial = log.Type == LogTypeConsume && facts.ActualQuota == nil
				event.priceUnconfirmed = true
			}
		} else if valid && !facts.IsTask && !facts.FundingPending && log.Type == LogTypeConsume {
			if rule := selectProfitCostRule(config.Rules, log.ChannelId, log.ModelName, log.CreatedAt); rule != nil {
				event.cost, event.known = modelProfitCost(*rule, log, facts)
			}
		}
		if log.Quota < 0 {
			event.known = false
		}
		if !event.known && event.unknownReason == "" {
			switch {
			case event.pending:
				event.unknownReason = "pending_settlement"
			case !valid || log.Quota < 0:
				event.unknownReason = "invalid_usage_facts"
			case facts.TaskID == "" && selectProfitCostRule(config.Rules, log.ChannelId, log.ModelName, log.CreatedAt) == nil:
				event.unknownReason = "procurement_price_missing"
			default:
				if rule := selectProfitCostRule(config.Rules, log.ChannelId, log.ModelName, log.CreatedAt); rule != nil && rule.Kind == "image_resolution" {
					if resolution, ok := facts.UsageFacts["resolution"].(string); !ok || (resolution != "1k" && resolution != "2k" && resolution != "4k") {
						event.unknownReason = "image_resolution_missing"
					}
				}
			}
		}
		if collectedIdentities[profitLogIdentity(log)] && valid {
			funding := facts.AdminInfo.Funding
			signedQuota := int64(log.Quota)
			if log.Type == LogTypeRefund {
				signedQuota = -signedQuota
			}
			if validProfitFunding(funding, signedQuota) {
				event.funding = funding
			}
		}
		row.add(log, event, conversion)
		report.Summary.add(log, event, conversion)
		bucket := (log.CreatedAt - report.Trend[0].BucketStart) / (24 * 60 * 60)
		report.Trend[bucket].add(log, event, conversion)
	}
	for _, row := range models {
		row.finish()
		row.AveragePriceUnit = "request"
		// These are observed period averages per original call, including net
		// settlement/refunds. They are not advertised token/second/image tariffs.
		// A refund-only period has no denominator. Pending or partial costs
		// must not be presented as a final whole-row unit price.
		if row.Requests > 0 && row.PendingBillingEvents == 0 && !row.invalidAmount && !row.priceUnconfirmed {
			price := row.RevenueCNY / float64(row.Requests)
			if !math.IsNaN(price) && !math.IsInf(price, 0) {
				row.CustomerAveragePriceCNY = &price
			}
			if row.BillingEvents > 0 && row.UnknownCostEvents == 0 && row.UpstreamCostCNY != nil {
				cost := *row.UpstreamCostCNY / float64(row.Requests)
				if !math.IsNaN(cost) && !math.IsInf(cost, 0) {
					row.UpstreamAverageCostCNY = &cost
				}
			}
		}
		report.Rows = append(report.Rows, *row)
	}
	slices.SortFunc(report.Rows, func(a, b ModelProfitRow) int { return strings.Compare(a.ModelName, b.ModelName) })
	for i := range report.Trend {
		report.Trend[i].finish()
	}
	report.Summary.finish()
	return report, nil
}

// Rows() avoids loading arbitrary payloads through dialect-specific JSON SQL;
// the same projection works when the log database is separately configured.
func readProfitLogs(query *gorm.DB, limit int) ([]Log, error) {
	columns := []string{"user_id", "created_at", "type", "model_name", "quota", "prompt_tokens", "completion_tokens", "channel_id", "request_id", "other"}
	if !common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		columns = append(columns, "id")
	}
	rows, err := query.Select(columns).Limit(limit + 1).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]Log, 0)
	for rows.Next() {
		var log Log
		if err := LOG_DB.ScanRows(rows, &log); err != nil {
			return nil, err
		}
		logs = append(logs, log)
		if len(logs) > limit {
			return nil, ErrModelProfitTooLarge
		}
	}
	return logs, rows.Err()
}

func profitLogIdentity(log Log) string {
	// ClickHouse does not persist auto-increment IDs. Request IDs plus the
	// immutable accounting fields also identify those log events.
	return fmt.Sprintf("%d:%s:%d:%d:%d:%d:%d:%x", log.Id, log.RequestId, log.CreatedAt, log.Type, log.ChannelId, log.UserId, log.Quota, sha256.Sum256([]byte(log.Other)))
}

func validProfitFunding(funding *WalletFundingAmounts, quota int64) bool {
	if funding == nil || funding.Quota != quota || funding.EventID < 0 || quota < -common.MaxWalletQuota || quota > common.MaxWalletQuota {
		return false
	}
	bound := quota
	if bound < 0 {
		bound = -bound
	}
	for _, value := range []int64{funding.PaidQuota, funding.GiftQuota, funding.UnknownQuota, funding.CashUnknownQuota} {
		if value < -bound || value > bound || (quota >= 0 && value < 0) || (quota < 0 && value > 0) {
			return false
		}
	}
	if funding.PaidQuota+funding.GiftQuota+funding.UnknownQuota != quota {
		return false
	}
	if quota >= 0 && funding.KnownCashRevenueCNY.IsNegative() || quota < 0 && funding.KnownCashRevenueCNY.IsPositive() {
		return false
	}
	if funding.CashRevenueCNY != nil && (funding.CashUnknownQuota != 0 || funding.UnknownQuota != 0 || !funding.CashRevenueCNY.Equal(funding.KnownCashRevenueCNY)) {
		return false
	}
	return true
}

func selectProfitCostRule(rules []ModelProfitCostRule, channel int, model string, at int64) *ModelProfitCostRule {
	var result *ModelProfitCostRule
	for i := range rules {
		rule := &rules[i]
		if rule.ChannelID == channel && rule.ModelName == model && rule.EffectiveFrom <= at && (result == nil || rule.EffectiveFrom > result.EffectiveFrom) {
			result = rule
		}
	}
	if result != nil && result.EffectiveUntil != nil && at >= *result.EffectiveUntil {
		return nil
	}
	return result
}

func modelProfitCost(rule ModelProfitCostRule, log Log, facts profitLogFacts) (float64, bool) {
	validQuantity := func(value float64, bound float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= bound
	}
	var cost float64
	switch rule.Kind {
	case "request":
		cost = *rule.UnitCostCNY
	case "second":
		quantity, exists := facts.UsageFacts["seconds"].(float64)
		if !exists || !validQuantity(quantity, 3600) || quantity <= 0 {
			return 0, false
		}
		cost = quantity * *rule.UnitCostCNY
	case "image", "image_resolution":
		quantity, exists := facts.UsageFacts["image_count"].(float64)
		if !exists && facts.ImageCount != nil {
			quantity, exists = *facts.ImageCount, true
		}
		if !exists || !validQuantity(quantity, 128) || quantity <= 0 || math.Trunc(quantity) != quantity {
			return 0, false
		}
		if rule.Kind == "image_resolution" {
			resolution, exists := facts.UsageFacts["resolution"].(string)
			if !exists {
				return 0, false
			}
			price, exists := rule.ResolutionCostsCNY[resolution]
			if !exists {
				return 0, false
			}
			cost = quantity * price
		} else {
			cost = quantity * *rule.UnitCostCNY
		}
	case "token":
		// This intentionally handles only input/cache-read/output tariffs. Cache
		// writes and modality-specific prices require a separately verified rule.
		if facts.UsageSemantic == "anthropic" || facts.AudioInput != 0 || facts.AudioOutput != 0 || facts.AudioInputTokenCount != 0 || facts.AudioInputSeparatePrice || len(facts.ToolSurcharges) > 0 {
			return 0, false
		}
		for _, quantity := range []*float64{facts.CacheCreationTokens, facts.CacheCreationTokens5m, facts.CacheCreationTokens1h} {
			if quantity != nil && *quantity != 0 {
				return 0, false
			}
		}
		for _, key := range []string{"ai", "ao", "cc", "cc1h"} {
			if facts.BillingTokens[key] != 0 {
				return 0, false
			}
		}
		p, c, cr := float64(log.PromptTokens), float64(log.CompletionTokens), 0.0
		if facts.CacheTokens != nil {
			cr = *facts.CacheTokens
		} else {
			return 0, false
		}
		if !validQuantity(p, 2147483647) || !validQuantity(c, 2147483647) || !validQuantity(cr, p) {
			return 0, false
		}
		p -= cr
		cost = (p**rule.InputCostPerMillionCNY + cr**rule.CacheCostPerMillionCNY + c**rule.OutputCostPerMillionCNY) / 1000000
		if rule.DeepSeekPeakMultiplier != nil {
			// matched_tier was frozen by the shared DeepSeek calendar when the
			// gateway reserved the request. Completion time is not a substitute.
			switch facts.MatchedTier {
			case "peak":
				cost *= *rule.DeepSeekPeakMultiplier
			case "off_peak":
			default:
				return 0, false
			}
		}
	default:
		return 0, false
	}
	return cost, !math.IsNaN(cost) && !math.IsInf(cost, 0) && cost >= 0
}

func modelProfitTaskEvents(ctx context.Context, keys map[profitTaskKey]struct{}, rules []ModelProfitCostRule) (map[string]profitCostEvent, error) {
	events := make(map[string]profitCostEvent)
	if len(keys) == 0 {
		return events, nil
	}
	ids := make([]string, 0, len(keys))
	seenIDs := make(map[string]struct{}, len(keys))
	for key := range keys {
		if _, exists := seenIDs[key.ID]; exists {
			continue
		}
		seenIDs[key.ID] = struct{}{}
		ids = append(ids, key.ID)
	}
	tasks := make(map[profitTaskKey]Task)
	for start := 0; start < len(ids); start += 200 {
		var batch []Task
		if err := DB.WithContext(ctx).Select("id", "task_id", "user_id", "channel_id", "status", "submit_time", "created_at", "private_data").Where("task_id IN ?", ids[start:min(start+200, len(ids))]).Find(&batch).Error; err != nil {
			return nil, err
		}
		for _, task := range batch {
			key := profitTaskKey{task.ChannelId, task.UserId, task.TaskID}
			if _, duplicate := tasks[key]; duplicate {
				return nil, fmt.Errorf("duplicate task accounting identity")
			}
			tasks[key] = task
		}
	}
	historyRead := 0
	for key := range keys {
		task, exists := tasks[key]
		if !exists {
			continue
		}
		encoded, _ := common.Marshal(key.ID)
		condition, pattern, err := buildLogLikeCondition("other", "%"+string(encoded)+"%")
		if err != nil {
			return nil, err
		}
		history, err := readProfitLogs(LOG_DB.WithContext(ctx).Model(&Log{}).Where("type IN ? AND channel_id = ? AND user_id = ?", []int{LogTypeConsume, LogTypeRefund}, key.Channel, key.User).Where(condition, pattern), maxProfitBillingEvents-historyRead)
		if err != nil {
			return nil, err
		}
		var records []ModelProfitBillingRecord
		if err := mainProfitRecordQuery(ctx).Where("channel_id = ? AND user_id = ?", key.Channel, key.User).Where(condition, pattern).Order("created_at, id").Limit(maxProfitBillingEvents - historyRead + 1).Find(&records).Error; err != nil {
			return nil, err
		}
		collected := make([]Log, 0, len(records))
		for _, record := range records {
			collected = append(collected, modelProfitRecordLog(record))
		}
		history, _, err = mergeModelProfitBillingLogs(ctx, history, collected)
		if err != nil {
			return nil, err
		}
		historyRead += len(history)
		if historyRead > maxProfitBillingEvents {
			return nil, ErrModelProfitTooLarge
		}
		history = slices.DeleteFunc(history, func(log Log) bool {
			var facts profitLogFacts
			return common.UnmarshalJsonStr(log.Other, &facts) != nil || facts.TaskID != key.ID
		})
		slices.SortFunc(history, func(a, b Log) int {
			if a.CreatedAt < b.CreatedAt {
				return -1
			}
			if a.CreatedAt > b.CreatedAt {
				return 1
			}
			if a.Id == 0 && b.Id == 0 {
				var af, bf profitLogFacts
				_ = common.UnmarshalJsonStr(a.Other, &af)
				_ = common.UnmarshalJsonStr(b.Other, &bf)
				ai, bi := a.Type == LogTypeConsume && af.ActualQuota == nil, b.Type == LogTypeConsume && bf.ActualQuota == nil
				if ai && !bi {
					return -1
				}
				if bi && !ai {
					return 1
				}
			}
			if a.Id < b.Id {
				return -1
			}
			if a.Id > b.Id {
				return 1
			}
			return strings.Compare(a.RequestId, b.RequestId)
		})
		initial := -1
		for i, log := range history {
			var facts profitLogFacts
			if common.UnmarshalJsonStr(log.Other, &facts) != nil || facts.TaskID != key.ID {
				continue
			}
			if log.Type == LogTypeConsume && facts.ActualQuota == nil {
				initial = i
				break
			}
		}
		pending := task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure
		for i, log := range history {
			events[profitLogIdentity(log)] = profitCostEvent{initial: i == initial, pending: pending, priceUnconfirmed: initial < 0}
		}
		if initial < 0 || pending {
			continue
		}
		at := task.SubmitTime
		if at == 0 {
			at = task.CreatedAt
		}
		if at == 0 {
			at = history[initial].CreatedAt
		}
		rule := selectProfitCostRule(rules, key.Channel, history[initial].ModelName, at)
		if rule == nil || (task.Status == TaskStatusFailure && !rule.FailureRefunded) {
			if rule == nil {
				for _, log := range history {
					event := events[profitLogIdentity(log)]
					event.unknownReason = "procurement_price_missing"
					events[profitLogIdentity(log)] = event
				}
			}
			continue
		}
		previousCost, valid := 0.0, true
		var previousQuota int64
		for i, log := range history {
			if i < initial {
				continue
			}
			var facts profitLogFacts
			if common.UnmarshalJsonStr(log.Other, &facts) != nil || facts.TaskID != key.ID {
				continue
			}
			event := profitCostEvent{initial: i == initial}
			if log.Quota < 0 || (facts.ActualQuota != nil && *facts.ActualQuota < 0) || (facts.PreConsumedQuota != nil && *facts.PreConsumedQuota < 0) {
				valid = false
			}
			if i == initial {
				previousQuota = int64(log.Quota)
			} else if facts.ActualQuota != nil {
				delta := int64(log.Quota)
				if log.Type == LogTypeRefund {
					delta = -delta
				}
				if facts.PreConsumedQuota == nil || *facts.PreConsumedQuota != previousQuota || *facts.ActualQuota-previousQuota != delta {
					valid = false
				}
				previousQuota = *facts.ActualQuota
			} else if log.Type == LogTypeRefund {
				if int64(log.Quota) != previousQuota {
					valid = false
				}
				previousQuota = 0
			} else {
				valid = false
			}
			if !valid {
				event.priceUnconfirmed = true
				events[profitLogIdentity(log)] = event
				continue
			}
			if log.Type == LogTypeRefund && facts.ActualQuota == nil {
				if task.Status == TaskStatusFailure && rule.FailureRefunded {
					event.cost, event.known = -previousCost, true
					previousCost = 0
				} else {
					valid = false
				}
			} else {
				current, known := modelProfitCost(*rule, log, facts)
				if rule.Kind == "request" && i != initial {
					current, known = previousCost, true
				}
				if known {
					event.cost, event.known = current-previousCost, true
					previousCost = current
				} else {
					valid = false
				}
			}
			events[profitLogIdentity(log)] = event
		}
	}
	return events, nil
}
