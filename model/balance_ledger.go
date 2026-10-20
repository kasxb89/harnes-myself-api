package model

import (
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var BalanceLedgerCategories = []string{"registration_bonus", "online_topup", "admin_adjustment", "redemption", "invitation_reward", "checkin_reward", "subscription_purchase", "other"}

// BalanceChange retains original quota units independently of localized display
// strings and exchange rates. It describes a committed change, never a request.
type BalanceChange struct {
	Category    string `json:"category"`
	QuotaDelta  int64  `json:"quota_delta"`
	QuotaBefore *int64 `json:"quota_before,omitempty"`
	QuotaAfter  *int64 `json:"quota_after,omitempty"`
}

type BalanceLedgerFilter struct {
	Category string
	UserID   int
	Username string
	Start    int64
	End      int64
}

type BalanceLedgerItem struct {
	ID               string   `json:"id"`
	CreatedAt        int64    `json:"created_at"`
	UserID           int      `json:"user_id"`
	Username         string   `json:"username"`
	Category         string   `json:"category"`
	Action           string   `json:"action"`
	QuotaDelta       *int64   `json:"quota_delta"`
	QuotaDeltaExact  *string  `json:"quota_delta_exact"`
	QuotaBefore      *int64   `json:"quota_before"`
	QuotaAfter       *int64   `json:"quota_after"`
	QuotaBeforeExact *string  `json:"quota_before_exact"`
	QuotaAfterExact  *string  `json:"quota_after_exact"`
	PaidMoney        *float64 `json:"paid_money"`
	PaidCurrency     *string  `json:"paid_currency"`
	PaymentProvider  string   `json:"payment_provider"`
	OperatorID       *int     `json:"operator_id"`
	OperatorUsername *string  `json:"operator_username"`
	Source           string   `json:"source"`
	AmountKnown      bool     `json:"amount_known"`
	DisplayAmount    *string  `json:"display_amount"`
	Content          string   `json:"content"`
	Legacy           bool     `json:"legacy"`
}

type BalanceLedgerCategoryTotal struct {
	Category             string `json:"category"`
	Count                int64  `json:"count"`
	KnownQuotaDelta      int64  `json:"known_quota_delta"`
	KnownQuotaDeltaExact string `json:"known_quota_delta_exact"`
	UnknownCount         int64  `json:"unknown_count"`
}

type BalanceLedgerPaidTotal struct {
	Currency        *string `json:"currency"`
	PaymentProvider string  `json:"payment_provider"`
	Amount          float64 `json:"amount"`
	Orders          int64   `json:"orders"`
}

type BalanceLedgerSummary struct {
	PaidMoney                   *float64                     `json:"paid_money"`
	PaidCurrency                *string                      `json:"paid_currency"`
	PaidOrders                  int64                        `json:"paid_orders"`
	PaidTotals                  []BalanceLedgerPaidTotal     `json:"paid_totals"`
	RegistrationBonusQuota      int64                        `json:"registration_bonus_quota"`
	RegistrationBonusQuotaExact string                       `json:"registration_bonus_quota_exact"`
	RegistrationBonusCount      int64                        `json:"registration_bonus_count"`
	RegistrationBonusUnknown    int64                        `json:"registration_bonus_unknown"`
	AdminIncreaseQuota          int64                        `json:"admin_increase_quota"`
	AdminDecreaseQuota          int64                        `json:"admin_decrease_quota"`
	AdminIncreaseQuotaExact     string                       `json:"admin_increase_quota_exact"`
	AdminDecreaseQuotaExact     string                       `json:"admin_decrease_quota_exact"`
	AdminAdjustments            int64                        `json:"admin_adjustments"`
	UnknownCount                int64                        `json:"unknown_count"`
	OtherUnknown                int64                        `json:"other_unknown"`
	RawQuotaOnly                bool                         `json:"raw_quota_only"`
	Categories                  []BalanceLedgerCategoryTotal `json:"categories"`
}

// The heap retains only the requested pagination prefix, while summary and count
// cover every matching record. Its root is the oldest retained item.
type balanceLedgerHeap []BalanceLedgerItem

func (h balanceLedgerHeap) Len() int { return len(h) }
func (h balanceLedgerHeap) Less(i, j int) bool {
	return compareBalanceLedgerItems(h[i], h[j]) < 0
}
func (h balanceLedgerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *balanceLedgerHeap) Push(value any) {
	*h = append(*h, value.(BalanceLedgerItem))
}
func (h *balanceLedgerHeap) Pop() any {
	last := len(*h) - 1
	item := (*h)[last]
	*h = (*h)[:last]
	return item
}

func compareBalanceLedgerItems(a, b BalanceLedgerItem) int {
	if a.CreatedAt < b.CreatedAt {
		return -1
	}
	if a.CreatedAt > b.CreatedAt {
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

func (f BalanceLedgerFilter) apply(query *gorm.DB, table, timestamp, username string) *gorm.DB {
	if f.UserID > 0 {
		query = query.Where(table+".user_id = ?", f.UserID)
	}
	if f.Username != "" {
		query = query.Where(username+" = ?", f.Username)
	}
	if f.Start > 0 {
		query = query.Where(timestamp+" >= ?", f.Start)
	}
	if f.End > 0 {
		query = query.Where(timestamp+" <= ?", f.End)
	}
	return query
}

func balanceLedgerInteger(value json.RawMessage) (*int64, bool) {
	var number int64
	if len(value) == 0 || string(value) == "null" || common.Unmarshal(value, &number) != nil || number > int64(common.MaxWalletQuota) || number < -int64(common.MaxWalletQuota) {
		return nil, false
	}
	return &number, true
}

func balanceLedgerLogItem(log Log) (BalanceLedgerItem, bool) {
	item := BalanceLedgerItem{
		ID: fmt.Sprintf("log:%020d:%s", log.Id, log.RequestId), CreatedAt: log.CreatedAt,
		UserID: log.UserId, Username: log.Username, Category: "other", Source: "log", Legacy: true,
		Content: common.MaskCredentials(log.Content),
	}
	var other struct {
		BalanceChange *struct {
			Category    string          `json:"category"`
			QuotaDelta  json.RawMessage `json:"quota_delta"`
			QuotaBefore json.RawMessage `json:"quota_before"`
			QuotaAfter  json.RawMessage `json:"quota_after"`
		} `json:"balance_change"`
		Op *struct {
			Action string                     `json:"action"`
			Params map[string]json.RawMessage `json:"params"`
		} `json:"op"`
		AdminInfo    *AuditAdminInfo `json:"admin_info"`
		ContentParts []struct {
			Key    string                     `json:"key"`
			Params map[string]json.RawMessage `json:"params"`
		} `json:"content_parts"`
	}
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		return item, log.Type == LogTypeTopup
	}
	if other.Op != nil {
		item.Action = other.Op.Action
		if item.Action == "user.quota_add" || item.Action == "user.quota_subtract" || item.Action == "user.quota_override" {
			item.Category = "admin_adjustment"
			before, beforeOK := balanceLedgerInteger(other.Op.Params["from"])
			after, afterOK := balanceLedgerInteger(other.Op.Params["to"])
			if beforeOK && afterOK {
				delta := *after - *before
				item.QuotaBefore, item.QuotaAfter, item.QuotaDelta = before, after, &delta
				item.AmountKnown, item.Legacy = true, false
			}
			if other.AdminInfo != nil && other.AdminInfo.AdminID > 0 {
				item.OperatorID = &other.AdminInfo.AdminID
				item.OperatorUsername = &other.AdminInfo.AdminUsername
			}
			return item, true
		}
	}
	for _, part := range other.ContentParts {
		switch part.Key {
		case "Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", "Administrator completed the order, amount added: {{quota}}, payment amount: {{amount}}", "{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", "Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}":
			// Their canonical successful order is read once from top_ups.
			return item, false
		case "New user sign-up bonus {{quota}}":
			item.Category = "registration_bonus"
		case "Invitation code bonus {{quota}}", "Bonus for inviting a user {{quota}}":
			item.Category = "invitation_reward"
		case "Daily check-in, received {{quota}}":
			item.Category = "checkin_reward"
		case "Topped up {{quota}} with a redemption code, code ID {{id}}":
			item.Category = "redemption"
		case "Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}":
			item.Category = "subscription_purchase"
			if quota, ok := balanceLedgerInteger(part.Params["quota"]); ok && *quota >= 0 {
				delta := -*quota
				item.QuotaDelta, item.AmountKnown, item.Legacy = &delta, true, false
			}
		}
		if value := part.Params["quota"]; len(value) > 0 {
			var display string
			if common.Unmarshal(value, &display) == nil && len(display) <= 128 {
				display = common.MaskCredentials(display)
				item.DisplayAmount = &display
			}
		}
	}
	hasBalanceChange := false
	if change := other.BalanceChange; change != nil && slices.Contains(BalanceLedgerCategories, change.Category) {
		item.Category = change.Category
		if delta, ok := balanceLedgerInteger(change.QuotaDelta); ok {
			item.QuotaDelta = delta
			item.QuotaBefore, _ = balanceLedgerInteger(change.QuotaBefore)
			item.QuotaAfter, _ = balanceLedgerInteger(change.QuotaAfter)
			item.AmountKnown, item.Legacy = true, false
		}
		hasBalanceChange = true
	}
	return item, log.Type == LogTypeTopup || item.Category != "other" || hasBalanceChange
}

// GetBalanceLedger merges the main-database payment records with only credit /
// system log types. It never reads consume/error content, joins separate DBs,
// parses formatted money back into quota, or sums audit duplicates.
func GetBalanceLedger(filter BalanceLedgerFilter, start, limit int) ([]BalanceLedgerItem, int64, BalanceLedgerSummary, error) {
	summary := BalanceLedgerSummary{RawQuotaOnly: true, PaidTotals: make([]BalanceLedgerPaidTotal, 0), Categories: make([]BalanceLedgerCategoryTotal, len(BalanceLedgerCategories)), RegistrationBonusQuotaExact: "0", AdminIncreaseQuotaExact: "0", AdminDecreaseQuotaExact: "0"}
	for index, category := range BalanceLedgerCategories {
		summary.Categories[index].Category = category
		summary.Categories[index].KnownQuotaDeltaExact = "0"
	}
	if start < 0 || limit < 1 || start+limit > 100000 {
		return nil, 0, summary, errors.New("invalid ledger pagination")
	}
	// Resolve a current username once to the affected account ID. Log usernames
	// are historical snapshots and may differ after an account is renamed.
	if filter.Username != "" {
		var user User
		err := DB.Select("id").Where("username = ?", filter.Username).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && filter.UserID > 0 && filter.UserID != user.Id) {
			return []BalanceLedgerItem{}, 0, summary, nil
		}
		if err != nil {
			return nil, 0, summary, err
		}
		filter.UserID, filter.Username = user.Id, ""
	}
	kept := make(balanceLedgerHeap, 0, start+limit)
	var total int64
	paidSums := make(map[string]decimal.Decimal)
	paidIndexes := make(map[string]int)
	add := func(item BalanceLedgerItem) error {
		if filter.Category != "" && filter.Category != "all" && item.Category != filter.Category {
			return nil
		}
		total++
		categoryIndex := slices.Index(BalanceLedgerCategories, item.Category)
		categoryTotal := &summary.Categories[categoryIndex]
		categoryTotal.Count++
		if item.QuotaDelta == nil {
			categoryTotal.UnknownCount++
			summary.UnknownCount++
			if item.Category == "other" {
				summary.OtherUnknown++
			}
		} else {
			delta := *item.QuotaDelta
			exact := fmt.Sprintf("%d", delta)
			item.QuotaDeltaExact = &exact
			if item.QuotaBefore != nil {
				before := fmt.Sprintf("%d", *item.QuotaBefore)
				item.QuotaBeforeExact = &before
			}
			if item.QuotaAfter != nil {
				after := fmt.Sprintf("%d", *item.QuotaAfter)
				item.QuotaAfterExact = &after
			}
			if (delta > 0 && categoryTotal.KnownQuotaDelta > math.MaxInt64-delta) || (delta < 0 && categoryTotal.KnownQuotaDelta < math.MinInt64-delta) {
				return errors.New("ledger quota total exceeds supported range")
			}
			categoryTotal.KnownQuotaDelta += delta
			categoryTotal.KnownQuotaDeltaExact = fmt.Sprintf("%d", categoryTotal.KnownQuotaDelta)
			if item.Category == "admin_adjustment" {
				if delta > 0 {
					if summary.AdminIncreaseQuota > math.MaxInt64-delta {
						return errors.New("ledger quota total exceeds supported range")
					}
					summary.AdminIncreaseQuota += delta
					summary.AdminIncreaseQuotaExact = fmt.Sprintf("%d", summary.AdminIncreaseQuota)
				} else {
					if summary.AdminDecreaseQuota > math.MaxInt64+delta {
						return errors.New("ledger quota total exceeds supported range")
					}
					summary.AdminDecreaseQuota -= delta
					summary.AdminDecreaseQuotaExact = fmt.Sprintf("%d", summary.AdminDecreaseQuota)
				}
			}
		}
		if item.Category == "registration_bonus" {
			summary.RegistrationBonusCount++
			if item.QuotaDelta == nil {
				summary.RegistrationBonusUnknown++
			} else {
				summary.RegistrationBonusQuota = categoryTotal.KnownQuotaDelta
				summary.RegistrationBonusQuotaExact = fmt.Sprintf("%d", summary.RegistrationBonusQuota)
			}
		}
		if item.Category == "admin_adjustment" {
			summary.AdminAdjustments++
		}
		if item.PaidMoney != nil {
			summary.PaidOrders++
			key := "unknown:" + item.PaymentProvider
			provider := item.PaymentProvider
			if item.PaidCurrency != nil {
				key, provider = *item.PaidCurrency, ""
			}
			index, exists := paidIndexes[key]
			if !exists {
				index = len(summary.PaidTotals)
				paidIndexes[key] = index
				summary.PaidTotals = append(summary.PaidTotals, BalanceLedgerPaidTotal{Currency: item.PaidCurrency, PaymentProvider: provider})
			}
			paidSums[key] = paidSums[key].Add(decimal.NewFromFloat(*item.PaidMoney))
			summary.PaidTotals[index].Amount = paidSums[key].InexactFloat64()
			summary.PaidTotals[index].Orders++
		}
		if kept.Len() < start+limit {
			heap.Push(&kept, item)
		} else if compareBalanceLedgerItems(item, kept[0]) > 0 {
			kept[0] = item
			heap.Fix(&kept, 0)
		}
		return nil
	}

	// Stream rows without retaining the full source; no order reference is
	// selected, and soft-deleted account names remain available in log snapshots.
	topupQuery := DB.Model(&TopUp{}).Select("top_ups.id, top_ups.user_id, top_ups.amount, top_ups.money, top_ups.payment_provider, top_ups.payment_method, top_ups.complete_time, COALESCE(users.username, '') AS username").
		Joins("LEFT JOIN users ON users.id = top_ups.user_id").Where("top_ups.status = ?", common.TopUpStatusSuccess)
	topupQuery = filter.apply(topupQuery, "top_ups", "top_ups.complete_time", "users.username")
	rows, err := topupQuery.Rows()
	if err != nil {
		return nil, 0, summary, err
	}
	for rows.Next() {
		var row struct {
			TopUp
			Username string
		}
		if err := DB.ScanRows(rows, &row); err != nil {
			rows.Close()
			return nil, 0, summary, err
		}
		if row.PaymentProvider == PaymentProviderBalance || row.PaymentMethod == PaymentMethodBalance {
			continue
		}
		if math.IsNaN(row.Money) || math.IsInf(row.Money, 0) || row.Money < 0 {
			rows.Close()
			return nil, 0, summary, errors.New("invalid recorded payment amount")
		}
		item := BalanceLedgerItem{ID: fmt.Sprintf("top_up:%020d", row.Id), CreatedAt: row.CompleteTime, UserID: row.UserId, Username: row.Username,
			Category: "online_topup", Action: "topup.completed", Source: "top_up", PaidMoney: &row.Money, PaymentProvider: row.PaymentProvider, Legacy: true}
		if item.PaymentProvider == "" {
			// Legacy subscription receipts retain only the payment method.
			// Keep those channels separate without inferring their currency.
			item.PaymentProvider = row.PaymentMethod
		}
		if row.Amount == 0 {
			item.Category, item.Action = "subscription_purchase", "subscription.paid"
			// A cash subscription receipt does not change the wallet balance.
			zero := int64(0)
			item.QuotaDelta, item.AmountKnown, item.Legacy = &zero, true, false
		}
		// WeChat Native orders are denominated in CNY by protocol. Other
		// providers can use configurable currencies not stored on legacy rows.
		if row.PaymentProvider == PaymentProviderWechat {
			currency := "CNY"
			item.PaidCurrency = &currency
		}
		if item.Category == "online_topup" && (row.PaymentProvider == PaymentProviderWechat || row.PaymentProvider == PaymentProviderCreem) && row.Amount > 0 && row.Amount <= int64(common.MaxWalletQuota) {
			item.QuotaDelta, item.AmountKnown, item.Legacy = &row.Amount, true, false
		}
		if err := add(item); err != nil {
			rows.Close()
			return nil, 0, summary, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, summary, err
	}

	logQuery := LOG_DB.Model(&Log{}).Select("id, user_id, username, created_at, type, content, other, request_id").Where("type IN ?", []int{LogTypeTopup, LogTypeSystem})
	logQuery = filter.apply(logQuery, "logs", "logs.created_at", "logs.username")
	rows, err = logQuery.Rows()
	if err != nil {
		return nil, 0, summary, err
	}
	for rows.Next() {
		var row Log
		if err := LOG_DB.ScanRows(rows, &row); err != nil {
			rows.Close()
			return nil, 0, summary, err
		}
		if item, include := balanceLedgerLogItem(row); include {
			if err := add(item); err != nil {
				rows.Close()
				return nil, 0, summary, err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, summary, err
	}
	if len(summary.PaidTotals) == 1 && summary.PaidTotals[0].Currency != nil {
		summary.PaidMoney, summary.PaidCurrency = &summary.PaidTotals[0].Amount, summary.PaidTotals[0].Currency
	}
	slices.SortFunc(summary.PaidTotals, func(a, b BalanceLedgerPaidTotal) int {
		aKey, bKey := a.PaymentProvider, b.PaymentProvider
		if a.Currency != nil {
			aKey = *a.Currency
		}
		if b.Currency != nil {
			bKey = *b.Currency
		}
		return strings.Compare(aKey, bKey)
	})
	slices.SortFunc(kept, func(a, b BalanceLedgerItem) int { return -compareBalanceLedgerItems(a, b) })
	if start >= len(kept) {
		return []BalanceLedgerItem{}, total, summary, nil
	}
	return kept[start:], total, summary, nil
}
