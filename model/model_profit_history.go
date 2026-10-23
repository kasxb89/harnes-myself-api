package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// HistoricalProfitFundingPlan is an offline reconstruction, never a wallet
// adjustment. Run it against an immutable, transactionally consistent snapshot.
// Unknown opening balances and gaps are not inferred from lifetime totals.
type HistoricalProfitFundingPlan struct {
	SchemaVersion         int                        `json:"schema_version"`
	Cutoff                int64                      `json:"cutoff"`
	SourceHash            string                     `json:"source_hash"`
	Users                 int                        `json:"users"`
	BillingEvents         int                        `json:"billing_events"`
	Reconstructed         int                        `json:"reconstructed_events"`
	Reasons               map[string]int             `json:"pending_reasons"`
	Records               []ModelProfitBillingRecord `json:"records"`
	OpeningBalances       []HistoricalProfitOpening  `json:"opening_balances"`
	OpeningSkippedReasons map[string]int             `json:"opening_skipped_reasons"`
}

type HistoricalProfitOpening struct {
	UserID       int                `json:"user_id"`
	Quota        int64              `json:"quota"`
	EvidenceHash string             `json:"evidence_hash"`
	Lots         []WalletFundingLot `json:"lots"`
}

type historicalFundingEvent struct {
	At        int64
	Order     int64
	Delta     int64
	Source    string
	Cash      *decimal.Decimal
	Before    *int64
	After     *int64
	Log       *Log
	Known     bool
	SourceKey string
}

type historicalFundingLot struct {
	Source        string
	Quota         int64
	Remaining     int64
	Cash          decimal.Decimal
	RemainingCash decimal.Decimal
	CashKnown     bool
	CreatedAt     int64
}

type historicalFundingAllocation struct {
	Lot   int
	Quota int64
	Cash  decimal.Decimal
}

func historicalProfitLogHash(log Log) string {
	// Deliberately exclude prompts, addresses, credentials and display strings.
	value := struct {
		ID, User, Kind, Channel, Quota, Prompt, Completion int
		At                                                 int64
		Model, Request, Other                              string
	}{log.Id, log.UserId, log.Type, log.ChannelId, log.Quota, log.PromptTokens, log.CompletionTokens, log.CreatedAt, log.ModelName, log.RequestId, log.Other}
	data, _ := common.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// BuildHistoricalProfitFunding reads in bounded per-user batches. Its only
// sources are exact original quota, successful payment receipts and committed
// structured balance changes. Displayed money is never parsed back into quota.
func BuildHistoricalProfitFunding(ctx context.Context, cutoff int64, limit int) (HistoricalProfitFundingPlan, error) {
	plan := HistoricalProfitFundingPlan{SchemaVersion: 1, Cutoff: cutoff, Reasons: make(map[string]int), Records: make([]ModelProfitBillingRecord, 0), OpeningSkippedReasons: make(map[string]int), OpeningBalances: make([]HistoricalProfitOpening, 0)}
	if cutoff <= 0 || limit < 1 || limit > maxProfitBillingEvents {
		return plan, ErrModelProfitParameters
	}
	var users []User
	userColumns := []string{"id", "quota"}
	if DB.Migrator().HasColumn(&User{}, "wallet_funding_initialized") {
		userColumns = append(userColumns, "wallet_funding_initialized")
	}
	if err := DB.WithContext(ctx).Unscoped().Select(userColumns).Order("id").Limit(limit + 1).Find(&users).Error; err != nil {
		return plan, err
	}
	if len(users) > limit {
		return plan, ErrModelProfitTooLarge
	}
	digest := sha256.New()
	for _, user := range users {
		plan.Users++
		if user.WalletFundingInitialized {
			var lots []WalletFundingLot
			if err := DB.WithContext(ctx).Where("user_id = ?", user.Id).Find(&lots).Error; err != nil {
				return plan, err
			}
			seedOnly := len(lots) > 0
			for _, lot := range lots {
				if !strings.HasPrefix(lot.SourceKey, "historical-seed:") {
					seedOnly = false
				}
			}
			var count int64
			if err := DB.WithContext(ctx).Model(&WalletFundingRequest{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
				return plan, err
			}
			if !seedOnly || count > 0 {
				plan.Reasons["new_ledger_already_active"]++
				continue
			}
			if err := DB.WithContext(ctx).Model(&WalletFundingEvent{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
				return plan, err
			}
			if count > 0 {
				plan.Reasons["new_ledger_already_active"]++
				continue
			}
		}
		var logs []Log
		query := LOG_DB.WithContext(ctx).Model(&Log{}).Select("id", "user_id", "created_at", "type", "model_name", "quota", "prompt_tokens", "completion_tokens", "channel_id", "request_id", "other").Where("user_id = ? AND created_at < ? AND type IN ?", user.Id, cutoff, []int{LogTypeTopup, LogTypeSystem, LogTypeConsume, LogTypeRefund}).Order("created_at, id").Limit(limit + 1)
		rows, err := query.Rows()
		if err != nil {
			return plan, err
		}
		bytes := 0
		for rows.Next() {
			var log Log
			if err := LOG_DB.ScanRows(rows, &log); err != nil {
				rows.Close()
				return plan, err
			}
			bytes += len(log.Other)
			if len(log.Other) > 256*1024 || bytes > 32*1024*1024 || len(logs) >= limit {
				rows.Close()
				return plan, ErrModelProfitTooLarge
			}
			logs = append(logs, log)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return plan, err
		}
		rows.Close()
		var topups []TopUp
		if err := DB.WithContext(ctx).Select("id", "user_id", "amount", "money", "payment_provider", "payment_method", "complete_time", "status").Where("user_id = ? AND status = ? AND complete_time < ?", user.Id, common.TopUpStatusSuccess, cutoff).Order("complete_time, id").Limit(limit + 1).Find(&topups).Error; err != nil {
			return plan, err
		}
		if len(logs)+len(topups) > limit {
			return plan, ErrModelProfitTooLarge
		}
		var pendingTasks int64
		if DB.Migrator().HasTable(&Task{}) {
			if err := DB.WithContext(ctx).Model(&Task{}).Where("user_id = ? AND (status NOT IN ? OR status IS NULL)", user.Id, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Count(&pendingTasks).Error; err != nil {
				return plan, err
			}
		}
		if DB.Migrator().HasTable(&Midjourney{}) {
			var pendingMJ int64
			if err := DB.WithContext(ctx).Model(&Midjourney{}).Where("user_id = ? AND (status NOT IN ? OR status IS NULL)", user.Id, []string{"SUCCESS", "FAILURE"}).Count(&pendingMJ).Error; err != nil {
				return plan, err
			}
			pendingTasks += pendingMJ
		}
		if DB.Migrator().HasTable(&Task{}) {
			for _, admission := range []any{&MediaAdmission{}, &H3Admission{}} {
				if !DB.Migrator().HasTable(admission) {
					continue
				}
				var count int64
				if err := DB.WithContext(ctx).Model(admission).Where("task_db_id IN (?) AND (state <> ? OR state IS NULL OR billing_state NOT IN ? OR billing_state IS NULL)", DB.Model(&Task{}).Select("id").Where("user_id = ?", user.Id), MediaReleased, []string{"reserved", "refunded", "not_charged"}).Count(&count).Error; err != nil {
					return plan, err
				}
				pendingTasks += count
			}
		}
		source, err := common.Marshal(struct {
			UserID       int
			Quota        int
			Logs         []Log
			Topups       []TopUp
			PendingTasks int64
		}{user.Id, user.Quota, logs, topups, pendingTasks})
		if err != nil {
			return plan, err
		}
		_, _ = digest.Write(source)
		events := make([]historicalFundingEvent, 0, len(logs)+len(topups))
		for i := range logs {
			log := &logs[i]
			if log.Type == LogTypeConsume || log.Type == LogTypeRefund {
				if log.ModelName == "" {
					continue
				}
				plan.BillingEvents++
				delta := -int64(log.Quota)
				if log.Type == LogTypeRefund {
					delta = -delta
				}
				events = append(events, historicalFundingEvent{At: log.CreatedAt, Order: int64(log.Id), Delta: delta, Log: log, Known: log.Quota >= 0})
				continue
			}
			item, include := balanceLedgerLogItem(*log)
			if !include {
				continue
			}
			event := historicalFundingEvent{At: log.CreatedAt, Order: int64(log.Id), Source: WalletFundingUnknown, Before: item.QuotaBefore, After: item.QuotaAfter, Known: item.QuotaDelta != nil, SourceKey: fmt.Sprintf("balance-log:%d", log.Id)}
			if item.QuotaDelta != nil {
				event.Delta = *item.QuotaDelta
			}
			switch item.Category {
			case "registration_bonus", "admin_adjustment", "invitation_reward", "checkin_reward":
				event.Source = WalletFundingGift
			}
			events = append(events, event)
		}
		for _, topup := range topups {
			if topup.Amount == 0 || topup.PaymentProvider == PaymentProviderBalance || topup.PaymentMethod == PaymentMethodBalance {
				continue
			}
			event := historicalFundingEvent{At: topup.CompleteTime, Order: -int64(topup.Id), Source: WalletFundingPaid, SourceKey: fmt.Sprintf("topup:%d", topup.Id)}
			// These two order formats persist original credited quota. Other
			// formats used mutable unit/exchange settings, not frozen on rows.
			if (topup.PaymentProvider == PaymentProviderWechat || topup.PaymentProvider == PaymentProviderCreem) && topup.Amount > 0 && topup.Amount <= common.MaxWalletQuota {
				event.Delta, event.Known = topup.Amount, true
			}
			if topup.PaymentProvider == PaymentProviderWechat && topup.Money > 0 && !math.IsNaN(topup.Money) && !math.IsInf(topup.Money, 0) {
				cash := decimal.NewFromFloat(topup.Money)
				event.Cash = &cash
			}
			events = append(events, event)
		}
		slices.SortStableFunc(events, func(a, b historicalFundingEvent) int {
			if a.At < b.At {
				return -1
			}
			if a.At > b.At {
				return 1
			}
			if a.Order < b.Order {
				return -1
			}
			if a.Order > b.Order {
				return 1
			}
			return 0
		})
		records, reasons, remaining := reconstructHistoricalFundingUser(user, events)
		for reason, count := range reasons {
			plan.Reasons[reason] += count
		}
		plan.Records = append(plan.Records, records...)
		openingReason := ""
		switch {
		case pendingTasks > 0:
			openingReason = "pending_or_unknown_task"
		case user.Quota <= 0:
			openingReason = "nonpositive_opening_balance"
		case len(reasons) > 0:
			openingReason = "historical_evidence_gap"
		case len(remaining) == 0:
			openingReason = "remaining_lots_unverified"
		}
		var total int64
		for _, lot := range remaining {
			if lot.Source == WalletFundingUnknown || lot.Remaining < 0 {
				openingReason = "remaining_source_unknown"
			}
			total += lot.Remaining
		}
		if total != int64(user.Quota) {
			openingReason = "remaining_balance_mismatch"
		}
		if openingReason != "" {
			plan.OpeningSkippedReasons[openingReason]++
		} else {
			hash := fmt.Sprintf("%x", sha256.Sum256(source))
			opening := HistoricalProfitOpening{UserID: user.Id, Quota: int64(user.Quota), EvidenceHash: hash, Lots: make([]WalletFundingLot, 0, len(remaining))}
			for i, lot := range remaining {
				if lot.Remaining <= 0 {
					continue
				}
				opening.Lots = append(opening.Lots, WalletFundingLot{UserID: user.Id, Source: lot.Source, SourceKey: fmt.Sprintf("historical-seed:%s:%d", hash, i), OriginalQuota: lot.Quota, RemainingQuota: lot.Remaining, OriginalCashCNY: lot.Cash.String(), RemainingCashCNY: lot.RemainingCash.String(), CashKnown: lot.CashKnown, CreatedAt: lot.CreatedAt})
			}
			plan.OpeningBalances = append(plan.OpeningBalances, opening)
		}
		if plan.BillingEvents > limit || len(plan.Records) > limit {
			return plan, ErrModelProfitTooLarge
		}
	}
	plan.SourceHash = fmt.Sprintf("%x", digest.Sum(nil))
	plan.Reconstructed = len(plan.Records)
	return plan, nil
}

func reconstructHistoricalFundingUser(user User, events []historicalFundingEvent) ([]ModelProfitBillingRecord, map[string]int, []historicalFundingLot) {
	var lots []historicalFundingLot
	allocations := make(map[string][]historicalFundingAllocation)
	ambiguousRequests := make(map[string]bool)
	var records []ModelProfitBillingRecord
	reasons := make(map[string]int)
	pending := make(map[int64]string)
	blockedReason := "missing_balanced_history"
	var currentLogID int64
	var balance int64
	anchored, complete := false, false
	segment := 0
	ambiguous := make(map[int64]bool)
	pureConsumption := make(map[int64]bool)
	groupQuota := make(map[int64]int64)
	giftGroup := make(map[int64]bool)
	checkedGroup := make(map[int64]bool)
	for start := 0; start < len(events); {
		end, credits, debits := start, 0, 0
		pure, total := true, int64(0)
		for end < len(events) && events[end].At == events[start].At {
			if events[end].Log == nil && events[end].Delta > 0 {
				credits++
			}
			if events[end].Log != nil {
				debits++
				if events[end].Log.Type != LogTypeConsume || !events[end].Known || events[end].Delta > 0 {
					pure = false
				}
				quantity := -events[end].Delta
				if quantity < 0 || quantity > common.MaxWalletQuota || total > common.MaxWalletQuota-quantity {
					pure = false
				} else {
					total += quantity
				}
			} else {
				pure = false
				if events[end].Delta < 0 {
					debits++
				}
			}
			end++
		}
		pureConsumption[events[start].At] = debits > 1 && pure
		groupQuota[events[start].At] = total
		ambiguous[events[start].At] = credits > 1 || credits > 0 && debits > 0 || debits > 1 && !pure
		start = end
	}
	invalidate := func(reason string) {
		for i := segment; i < len(records); i++ {
			records[i].Other = ""
			pending[records[i].HistoricalLogID] = reason
		}
		if currentLogID != 0 {
			pending[currentLogID] = reason
		}
		blockedReason = reason
		complete = false
	}
	for _, event := range events {
		currentLogID = 0
		if event.Log != nil {
			currentLogID = int64(event.Log.Id)
			pending[currentLogID] = blockedReason
		}
		if event.Before != nil {
			if *event.Before < 0 || *event.Before > common.MaxWalletQuota {
				invalidate("unfunded_or_invalid_opening_balance")
				continue
			}
			if !anchored || !complete || balance != *event.Before {
				if anchored {
					invalidate("balance_history_gap")
				}
				lots = []historicalFundingLot{{Source: WalletFundingUnknown, Quota: *event.Before, Remaining: *event.Before}}
				allocations = make(map[string][]historicalFundingAllocation)
				ambiguousRequests = make(map[string]bool)
			}
			balance, anchored, complete = *event.Before, true, true
			blockedReason = "missing_balanced_history"
			segment = len(records)
		}
		if !event.Known {
			invalidate("missing_original_quota")
			continue
		}
		// Legacy seconds and log append IDs do not prove the order in which
		// independent wallet commits consumed two credits or raced a credit.
		if ambiguous[event.At] {
			invalidate("ambiguous_same_second_order")
		}
		if !anchored || !complete {
			continue
		}
		if pureConsumption[event.At] && !checkedGroup[event.At] {
			checkedGroup[event.At] = true
			// Only a contiguous gift prefix proves every same-second charge
			// has zero cash income regardless of wallet commit order. A spent
			// paid/unknown lot inside the prefix also breaks the proof: a later
			// original refund could resurrect it between the gift batches.
			var giftQuota int64
			started := false
			for _, lot := range lots {
				if !started && lot.Remaining <= 0 {
					continue
				}
				started = true
				if lot.Source != WalletFundingGift {
					break
				}
				giftQuota += lot.Remaining
				if giftQuota >= groupQuota[event.At] {
					giftGroup[event.At] = true
					break
				}
			}
		}
		if event.Delta > common.MaxWalletQuota || event.Delta < -common.MaxWalletQuota || balance > common.MaxWalletQuota-event.Delta || balance < -common.MaxWalletQuota-event.Delta {
			invalidate("invalid_quota_history")
			continue
		}
		if event.Log == nil && event.Delta > 0 {
			lot := historicalFundingLot{Source: event.Source, Quota: event.Delta, Remaining: event.Delta, CashKnown: event.Source == WalletFundingGift, CreatedAt: event.At}
			if event.Cash != nil {
				lot.Cash, lot.CashKnown = *event.Cash, true
				lot.RemainingCash = *event.Cash
			}
			lots = append(lots, lot)
			balance += event.Delta
			if event.After != nil && balance != *event.After {
				invalidate("invalid_balance_anchor")
			}
			continue
		}
		var facts profitLogFacts
		key := ""
		if event.Log != nil {
			if common.UnmarshalJsonStr(event.Log.Other, &facts) != nil {
				invalidate("invalid_usage_facts")
				continue
			}
			var billing struct {
				BillingSource string `json:"billing_source"`
			}
			_ = common.UnmarshalJsonStr(event.Log.Other, &billing)
			if billing.BillingSource == "subscription" {
				pending[currentLogID] = "subscription_cash_unattributed"
				continue
			}
			key = fmt.Sprintf("%d:%s", event.Log.ChannelId, event.Log.RequestId)
			if facts.TaskID != "" {
				key = fmt.Sprintf("%d:task:%s", event.Log.ChannelId, facts.TaskID)
			}
			if event.Log.RequestId == "" && facts.TaskID == "" {
				key = fmt.Sprintf("log:%d", event.Log.Id)
			}
		}
		funding := WalletFundingAmounts{Quota: -event.Delta}
		quantity := -event.Delta
		if quantity >= 0 {
			for lotID := range lots {
				lot := &lots[lotID]
				if quantity == 0 {
					break
				}
				if lot.Remaining <= 0 {
					continue
				}
				take := min(quantity, lot.Remaining)
				cash := decimal.Zero
				if lot.Source == WalletFundingPaid && lot.CashKnown {
					if take == lot.Remaining {
						cash = lot.RemainingCash
					} else {
						cash = lot.Cash.Mul(decimal.NewFromInt(take)).Div(decimal.NewFromInt(lot.Quota))
					}
					lot.RemainingCash = lot.RemainingCash.Sub(cash)
				}
				lot.Remaining -= take
				quantity -= take
				if event.Log != nil {
					allocations[key] = append(allocations[key], historicalFundingAllocation{Lot: lotID, Quota: take, Cash: cash})
				}
				addHistoricalFundingAmount(&funding, *lot, take, cash)
			}
			if quantity != 0 {
				invalidate("debit_exceeds_proven_balance")
				continue
			}
		} else {
			if ambiguousRequests[key] {
				invalidate("ambiguous_original_refund_allocation")
				continue
			}
			quantity = -quantity
			previous := allocations[key]
			for j := len(previous) - 1; j >= 0 && quantity > 0; j-- {
				allocation := &previous[j]
				take := min(quantity, allocation.Quota)
				cash := decimal.Zero
				if take == allocation.Quota {
					cash = allocation.Cash
				} else if allocation.Quota > 0 {
					cash = allocation.Cash.Mul(decimal.NewFromInt(take)).Div(decimal.NewFromInt(allocation.Quota))
				}
				allocation.Cash = allocation.Cash.Sub(cash)
				allocation.Quota -= take
				quantity -= take
				lot := &lots[allocation.Lot]
				lot.Remaining += take
				lot.RemainingCash = lot.RemainingCash.Add(cash)
				addHistoricalFundingAmount(&funding, *lot, -take, cash.Neg())
			}
			allocations[key] = previous
			if quantity != 0 {
				invalidate("refund_original_allocation_missing")
				continue
			}
		}
		balance += event.Delta
		if event.After != nil && balance != *event.After {
			invalidate("invalid_balance_anchor")
			continue
		}
		if event.Log == nil {
			continue
		}
		if pureConsumption[event.At] && !giftGroup[event.At] {
			// Pure debits commute for the ending FIFO balance, but not for
			// assigning cash/gifts to individual requests or models. Keep the
			// exact aggregate state for later independent events; never publish
			// the arbitrary log-ID allocation or use it for a later refund.
			ambiguousRequests[key] = true
			pending[currentLogID] = "ambiguous_same_second_order"
			continue
		}
		if funding.CashUnknownQuota == 0 {
			cash := funding.KnownCashRevenueCNY
			funding.CashRevenueCNY = &cash
		}
		if !validProfitFunding(&funding, -event.Delta) {
			invalidate("invalid_reconstructed_funding")
			continue
		}
		log := event.Log
		hash := historicalProfitLogHash(*log)
		eventKey := fmt.Sprintf("%x", sha256.Sum256([]byte("historical-fifo-v1:"+hash)))
		facts.AdminInfo.Funding, facts.AdminInfo.FundingSigned, facts.AdminInfo.ProfitRecordKey = &funding, true, eventKey
		data, err := common.Marshal(facts)
		if err != nil {
			invalidate("invalid_usage_facts")
			continue
		}
		records = append(records, ModelProfitBillingRecord{EventKey: eventKey, HistoricalLogID: int64(log.Id), HistoricalLogHash: hash, FundingIsDelta: true,
			UserID: user.Id, CreatedAt: log.CreatedAt, Type: log.Type, ModelName: log.ModelName, ChannelID: log.ChannelId, RequestID: log.RequestId,
			Quota: log.Quota, PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens, Other: string(data)})
	}
	if !anchored || !complete || balance != int64(user.Quota) {
		invalidate("snapshot_balance_mismatch")
	}
	result := records[:0]
	for _, record := range records {
		if record.Other != "" {
			result = append(result, record)
			delete(pending, record.HistoricalLogID)
		}
	}
	for _, reason := range pending {
		reasons[reason]++
	}
	if !anchored || !complete || balance != int64(user.Quota) {
		lots = nil
	}
	remaining := make([]historicalFundingLot, 0, len(lots))
	for _, lot := range lots {
		if lot.Remaining != 0 {
			remaining = append(remaining, lot)
		}
	}
	return result, reasons, remaining
}

func addHistoricalFundingAmount(funding *WalletFundingAmounts, lot historicalFundingLot, quota int64, cash decimal.Decimal) {
	switch lot.Source {
	case WalletFundingGift:
		funding.GiftQuota += quota
	case WalletFundingPaid:
		funding.PaidQuota += quota
	default:
		funding.UnknownQuota += quota
	}
	if lot.CashKnown {
		funding.KnownCashRevenueCNY = funding.KnownCashRevenueCNY.Add(cash)
	} else {
		funding.CashUnknownQuota += quota
	}
}

// ApplyHistoricalProfitFunding only adds immutable reporting projections. It
// neither changes a wallet nor rewrites historical usage logs or procurement
// tariffs. Re-applying the same plan is idempotent; conflicting evidence fails.
func ApplyHistoricalProfitFunding(ctx context.Context, plan HistoricalProfitFundingPlan) (int64, error) {
	if plan.SchemaVersion != 1 || plan.Cutoff <= 0 || len(plan.SourceHash) != 64 || plan.Reconstructed != len(plan.Records) || len(plan.Records) > maxProfitBillingEvents {
		return 0, ErrModelProfitParameters
	}
	// Receipts, opening/end balances and credit audit evidence are part of
	// the proof too. Checking only the consumption row would accept a stale
	// plan after its original payment or account history had changed.
	rebuilt, err := BuildHistoricalProfitFunding(ctx, plan.Cutoff, maxProfitBillingEvents)
	if err != nil {
		return 0, err
	}
	if rebuilt.SourceHash != plan.SourceHash {
		return 0, errors.New("historical source evidence changed")
	}
	expectedRecords, err := common.Marshal(rebuilt.Records)
	if err != nil {
		return 0, err
	}
	actualRecords, err := common.Marshal(plan.Records)
	if err != nil {
		return 0, err
	}
	if string(expectedRecords) != string(actualRecords) {
		return 0, errors.New("historical funding plan differs from verified reconstruction")
	}
	expectedOpenings, err := common.Marshal(rebuilt.OpeningBalances)
	if err != nil {
		return 0, err
	}
	actualOpenings, err := common.Marshal(plan.OpeningBalances)
	if err != nil {
		return 0, err
	}
	if string(expectedOpenings) != string(actualOpenings) {
		return 0, errors.New("historical opening plan differs from verified reconstruction")
	}
	var inserted int64
	// Validate LOG_DB evidence before the primary write transaction. The two
	// handles may be one SQLite database with a single allowed connection.
	for _, record := range plan.Records {
		var source Log
		if err := LOG_DB.WithContext(ctx).Where("id = ?", record.HistoricalLogID).First(&source).Error; err != nil {
			return 0, err
		}
		if historicalProfitLogHash(source) != record.HistoricalLogHash || source.UserId != record.UserID || source.ModelName != record.ModelName || source.Quota != record.Quota || source.Type != record.Type || source.ChannelId != record.ChannelID || source.CreatedAt != record.CreatedAt || source.RequestId != record.RequestID || source.PromptTokens != record.PromptTokens || source.CompletionTokens != record.CompletionTokens {
			return 0, errors.New("historical source log changed")
		}
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(plan.OpeningBalances) > 0 {
			for _, table := range []any{&WalletFundingRequest{}, &WalletFundingEvent{}, &WalletFundingAllocation{}} {
				var count int64
				if err := tx.Model(table).Count(&count).Error; err != nil {
					return err
				}
				if count != 0 {
					return errors.New("historical opening requires a cold unused funding ledger")
				}
			}
			var foreignLots int64
			if err := tx.Model(&WalletFundingLot{}).Where("source_key NOT LIKE ?", "historical-seed:%").Count(&foreignLots).Error; err != nil {
				return err
			}
			if foreignLots != 0 {
				return errors.New("historical opening refuses existing live funding lots")
			}
		}
		for _, record := range plan.Records {
			if record.ID != 0 || len(record.EventKey) != 64 || len(record.HistoricalLogHash) != 64 || record.HistoricalLogID < 0 || record.CreatedAt >= plan.Cutoff {
				return ErrModelProfitParameters
			}
			var facts profitLogFacts
			if common.UnmarshalJsonStr(record.Other, &facts) != nil {
				return ErrModelProfitParameters
			}
			quota := int64(record.Quota)
			if record.Type == LogTypeRefund {
				quota = -quota
			}
			if !validProfitFunding(facts.AdminInfo.Funding, quota) {
				return ErrModelProfitParameters
			}
			var existing ModelProfitBillingRecord
			err := tx.Where("event_key = ?", record.EventKey).Take(&existing).Error
			if err == nil {
				if existing.HistoricalLogHash != record.HistoricalLogHash || existing.Other != record.Other {
					return errors.New("historical funding evidence conflicts")
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// Plain unique insert makes concurrent conflicting operators fail
			// atomically. MySQL CLIENT_FOUND_ROWS reports a no-op upsert as one
			// affected row, so its count cannot establish newly inserted bills.
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			inserted++
		}
		for _, opening := range plan.OpeningBalances {
			if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
				if err := tx.Model(&User{}).Where("id = ?", opening.UserID).UpdateColumn("wallet_funding_initialized", gorm.Expr("wallet_funding_initialized")).Error; err != nil {
					return err
				}
			}
			var user User
			if err := lockForUpdate(tx).Unscoped().Select("id", "quota", "wallet_funding_initialized").First(&user, opening.UserID).Error; err != nil {
				return err
			}
			if int64(user.Quota) != opening.Quota {
				return errors.New("historical opening balance changed")
			}
			var existing []WalletFundingLot
			if err := lockForUpdate(tx).Where("user_id = ?", opening.UserID).Order("created_at,id").Find(&existing).Error; err != nil {
				return err
			}
			if user.WalletFundingInitialized {
				for i := range existing {
					existing[i].ID = 0
				}
				before, err := common.Marshal(existing)
				if err != nil {
					return err
				}
				after, err := common.Marshal(opening.Lots)
				if err != nil {
					return err
				}
				if string(before) != string(after) {
					return errors.New("historical opening seed conflicts")
				}
				continue
			}
			if len(existing) > 0 {
				return errors.New("historical opening refuses uninitialized existing lots")
			}
			for _, lot := range opening.Lots {
				if err := tx.Create(&lot).Error; err != nil {
					return err
				}
			}
			result := tx.Model(&User{}).Where("id = ? AND wallet_funding_initialized = ?", opening.UserID, false).UpdateColumn("wallet_funding_initialized", true)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("historical opening flag changed")
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}
