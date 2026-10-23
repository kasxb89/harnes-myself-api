package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WalletFundingPaid    = "paid"
	WalletFundingGift    = "gift"
	WalletFundingUnknown = "unknown"
)

var (
	ErrInsufficientWalletFunding = errors.New("wallet quota insufficient")
	ErrWalletFundingInvariant    = errors.New("wallet funding balance mismatch")
)

// WalletFundingLot retains a credit's original source and actual cash value.
// Negative unknown lots represent existing debt or an allowed final overdraw.
// They never turn an unpaid charge into cash income.
type WalletFundingLot struct {
	ID               int64  `gorm:"primaryKey"`
	UserID           int    `gorm:"index:,composite:wallet_lot_user_fifo,priority:1;not null"`
	Source           string `gorm:"type:varchar(16);not null"`
	SourceKey        string `gorm:"type:varchar(191);not null"`
	OriginalQuota    int64  `gorm:"not null"`
	RemainingQuota   int64  `gorm:"not null"`
	OriginalCashCNY  string `gorm:"type:varchar(96);not null"`
	RemainingCashCNY string `gorm:"type:varchar(96);not null"`
	CashKnown        bool   `gorm:"not null"`
	CreatedAt        int64  `gorm:"index:,composite:wallet_lot_user_fifo,priority:2;not null"`
}

type WalletFundingRequest struct {
	ID                     int64  `gorm:"primaryKey"`
	UserID                 int    `gorm:"index;uniqueIndex:,composite:wallet_request_identity,priority:1;not null"`
	IdentityHash           string `gorm:"type:varchar(64);uniqueIndex:,composite:wallet_request_identity,priority:2;not null"`
	RequestID              string `gorm:"type:varchar(191);not null"`
	Purpose                string `gorm:"type:varchar(32);not null"`
	ModelName              string `gorm:"type:varchar(255);not null"`
	InitialModelName       string `gorm:"type:varchar(255);not null"`
	LegacyBaselineImported bool   `gorm:"not null;default:false"`
	ChannelID              int    `gorm:"not null"`
	Quota                  int64  `gorm:"not null"`
	LastEventID            int64  `gorm:"not null"`
	CreatedAt              int64  `gorm:"not null"`
	UpdatedAt              int64  `gorm:"not null"`
}

type WalletFundingAllocation struct {
	ID               int64  `gorm:"primaryKey"`
	FundingRequestID int64  `gorm:"index;uniqueIndex:,composite:wallet_allocation_lot,priority:1;not null"`
	LotID            int64  `gorm:"uniqueIndex:,composite:wallet_allocation_lot,priority:2;not null"`
	Quota            int64  `gorm:"not null"`
	CashCNY          string `gorm:"type:varchar(96);not null"`
}

// WalletFundingEvent is an immutable main-database journal entry. Quota and
// source amounts are signed: a refund is the exact reverse of a prior debit.
type WalletFundingEvent struct {
	ID                  int64  `gorm:"primaryKey"`
	UserID              int    `gorm:"index;not null"`
	FundingRequestID    int64  `gorm:"index;not null"`
	RequestID           string `gorm:"type:varchar(191);not null"`
	Purpose             string `gorm:"type:varchar(32);not null"`
	Operation           string `gorm:"type:varchar(32);not null"`
	DeltaQuota          int64  `gorm:"not null"`
	PaidQuota           int64  `gorm:"not null"`
	GiftQuota           int64  `gorm:"not null"`
	UnknownQuota        int64  `gorm:"not null"`
	CashUnknownQuota    int64  `gorm:"not null"`
	KnownCashRevenueCNY string `gorm:"type:varchar(96);not null"`
	TargetQuota         int64  `gorm:"not null"`
	CreatedAt           int64  `gorm:"index;not null"`
}

// WalletFundingAmounts describes either a signed event or a net request. A
// nil CashRevenueCNY means some cash attribution remains unverified; the known
// portion is independently available and must not be mistaken for a full sum.
type WalletFundingAmounts struct {
	EventID             int64            `json:"event_id"`
	Quota               int64            `json:"quota"`
	PaidQuota           int64            `json:"paid_quota"`
	GiftQuota           int64            `json:"gift_quota"`
	UnknownQuota        int64            `json:"unknown_quota"`
	CashUnknownQuota    int64            `json:"cash_unknown_quota"`
	KnownCashRevenueCNY decimal.Decimal  `json:"known_cash_revenue_cny"`
	CashRevenueCNY      *decimal.Decimal `json:"cash_revenue_cny"`
}

type WalletFundingResult struct {
	Delta        WalletFundingAmounts
	Total        WalletFundingAmounts
	Applied      bool
	AppliedDelta int
	EventID      int64
}

func WalletFundingModels() []any {
	return []any{&WalletFundingLot{}, &WalletFundingRequest{}, &WalletFundingAllocation{}, &WalletFundingEvent{}, &ModelProfitBillingRecord{}}
}

func walletFundingUserForUpdate(tx *gorm.DB, userID int) (*User, error) {
	if userID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	// Obtain SQLite's write lock before reading. Its dialect deliberately has
	// no SELECT FOR UPDATE; a read-then-write transaction otherwise loses races.
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		result := tx.Model(&User{}).Where("id = ?", userID).UpdateColumn("quota", gorm.Expr("quota + ?", 0))
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, gorm.ErrRecordNotFound
		}
	}
	var user User
	if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, err
	}
	if int64(user.Quota) > common.MaxWalletQuota || int64(user.Quota) < -common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	return &user, nil
}

func walletFundingOpening(tx *gorm.DB, user *User) error {
	// The user's locked initialization flag avoids a MySQL missing-range gap
	// lock: unrelated legacy users may create their opening lots concurrently.
	if !user.WalletFundingInitialized {
		if err := tx.Create(&WalletFundingLot{UserID: user.Id, Source: WalletFundingUnknown, SourceKey: "legacy-opening", OriginalQuota: int64(user.Quota), RemainingQuota: int64(user.Quota), OriginalCashCNY: "0", RemainingCashCNY: "0", CreatedAt: common.GetTimestamp()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("wallet_funding_initialized", true).Error; err != nil {
			return err
		}
		user.WalletFundingInitialized = true
		return nil
	}
	// Use current reads, including when an outer MySQL REPEATABLE READ
	// transaction took a snapshot before it acquired the user's row lock.
	var lots []WalletFundingLot
	if err := lockForUpdate(tx).Select("id", "remaining_quota").Where("user_id = ?", user.Id).Order("id ASC").Find(&lots).Error; err != nil {
		return err
	}
	if len(lots) == 0 {
		return ErrWalletFundingInvariant
	}
	remaining := decimal.Zero
	for _, lot := range lots {
		remaining = remaining.Add(decimal.NewFromInt(lot.RemainingQuota))
	}
	if !remaining.Equal(decimal.NewFromInt(int64(user.Quota))) {
		return ErrWalletFundingInvariant
	}
	return nil
}

// InitializeUserWalletFundingWithTx is called immediately after creating a
// new user. It records the creation grant, rather than opening it as old money.
func InitializeUserWalletFundingWithTx(tx *gorm.DB, user *User, source string) error {
	if source != WalletFundingGift && source != WalletFundingUnknown {
		return errors.New("invalid initial wallet funding source")
	}
	if user.Quota < 0 || user.Quota > common.MaxWalletQuota {
		return ErrWalletQuotaLimitExceeded
	}
	if err := tx.Create(&WalletFundingLot{UserID: user.Id, Source: source, SourceKey: "user-created", OriginalQuota: int64(user.Quota), RemainingQuota: int64(user.Quota), OriginalCashCNY: "0", RemainingCashCNY: "0", CashKnown: source == WalletFundingGift, CreatedAt: common.GetTimestamp()}).Error; err != nil {
		return err
	}
	if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("wallet_funding_initialized", true).Error; err != nil {
		return err
	}
	user.WalletFundingInitialized = true
	return nil
}

func validateWalletFundingIdentity(requestID, purpose string) error {
	if strings.TrimSpace(requestID) == "" || len(requestID) > 191 || strings.TrimSpace(purpose) == "" || len(purpose) > 32 || strings.ContainsAny(requestID+purpose, "\x00\r\n") {
		return errors.New("invalid wallet funding identity")
	}
	return nil
}

func walletFundingIdentity(requestID, purpose string) string {
	identity := sha256.Sum256([]byte(requestID + "\x00" + purpose))
	return fmt.Sprintf("%x", identity)
}

func walletFundingRequestForUpdate(tx *gorm.DB, userID int, requestID, purpose string) (*WalletFundingRequest, error) {
	identity := walletFundingIdentity(requestID, purpose)
	request := WalletFundingRequest{UserID: userID, IdentityHash: identity, RequestID: requestID, Purpose: purpose, CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp()}
	// Insert before reading an absent identity: locking a shared empty MySQL
	// index gap first can deadlock independent users on their first request.
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "identity_hash"}}, DoNothing: true}).Create(&request).Error; err != nil {
		return nil, err
	}
	request.ID = 0
	if err := lockForUpdate(tx).Where("user_id = ? AND identity_hash = ?", userID, identity).First(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func walletFundingCash(value string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(value)
	if err != nil || amount.IsNegative() {
		return decimal.Zero, errors.New("invalid wallet funding cash")
	}
	return amount, nil
}

func walletFundingAmounts(tx *gorm.DB, request *WalletFundingRequest) (WalletFundingAmounts, error) {
	result := WalletFundingAmounts{EventID: request.LastEventID, KnownCashRevenueCNY: decimal.Zero}
	if request.Quota == 0 && request.LastEventID == 0 && !request.LegacyBaselineImported {
		cash := decimal.Zero
		result.CashRevenueCNY = &cash
		return result, nil
	}
	var allocations []WalletFundingAllocation
	if err := lockForUpdate(tx).Where("funding_request_id = ? AND quota > ?", request.ID, 0).Find(&allocations).Error; err != nil {
		return result, err
	}
	for _, allocation := range allocations {
		var lot WalletFundingLot
		if err := lockForUpdate(tx).First(&lot, allocation.LotID).Error; err != nil {
			return result, err
		}
		result.Quota += allocation.Quota
		switch lot.Source {
		case WalletFundingPaid:
			result.PaidQuota += allocation.Quota
		case WalletFundingGift:
			result.GiftQuota += allocation.Quota
		default:
			result.UnknownQuota += allocation.Quota
		}
		if !lot.CashKnown {
			result.CashUnknownQuota += allocation.Quota
			continue
		}
		cash, err := walletFundingCash(allocation.CashCNY)
		if err != nil {
			return result, err
		}
		result.KnownCashRevenueCNY = result.KnownCashRevenueCNY.Add(cash)
	}
	if result.Quota != request.Quota {
		return result, ErrWalletFundingInvariant
	}
	if result.CashUnknownQuota == 0 {
		cash := result.KnownCashRevenueCNY
		result.CashRevenueCNY = &cash
	}
	return result, nil
}

func walletFundingConsume(tx *gorm.DB, request *WalletFundingRequest, amount int64) error {
	var lots []WalletFundingLot
	if err := lockForUpdate(tx).Where("user_id = ? AND remaining_quota > ?", request.UserID, 0).Order("id ASC").Find(&lots).Error; err != nil {
		return err
	}
	remaining := amount
	for i := range lots {
		if remaining == 0 {
			break
		}
		lot := &lots[i]
		quantity := min(remaining, lot.RemainingQuota)
		cash, err := walletFundingCash(lot.RemainingCashCNY)
		if err != nil {
			return err
		}
		allocatedCash := cash
		if quantity < lot.RemainingQuota {
			allocatedCash = cash.Mul(decimal.NewFromInt(quantity)).DivRound(decimal.NewFromInt(lot.RemainingQuota), 24)
			if allocatedCash.GreaterThan(cash) {
				allocatedCash = cash
			}
		}
		lot.RemainingQuota -= quantity
		lot.RemainingCashCNY = cash.Sub(allocatedCash).String()
		if err := tx.Model(lot).Select("remaining_quota", "remaining_cash_cny").Updates(lot).Error; err != nil {
			return err
		}
		allocation := WalletFundingAllocation{FundingRequestID: request.ID, LotID: lot.ID, CashCNY: "0"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "funding_request_id"}, {Name: "lot_id"}}, DoNothing: true}).Create(&allocation).Error; err != nil {
			return err
		}
		allocation.ID = 0
		if err := lockForUpdate(tx).Where("funding_request_id = ? AND lot_id = ?", request.ID, lot.ID).First(&allocation).Error; err != nil {
			return err
		}
		previousCash, err := walletFundingCash(allocation.CashCNY)
		if err != nil {
			return err
		}
		allocation.Quota += quantity
		allocation.CashCNY = previousCash.Add(allocatedCash).String()
		if err := tx.Model(&allocation).Select("quota", "cash_cny").Updates(&allocation).Error; err != nil {
			return err
		}
		remaining -= quantity
	}
	if remaining > 0 {
		lot := WalletFundingLot{UserID: request.UserID, Source: WalletFundingUnknown, SourceKey: fmt.Sprintf("debt:%d", request.ID), OriginalQuota: 0, RemainingQuota: -remaining, OriginalCashCNY: "0", RemainingCashCNY: "0", CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&lot).Error; err != nil {
			return err
		}
		if err := tx.Create(&WalletFundingAllocation{FundingRequestID: request.ID, LotID: lot.ID, Quota: remaining, CashCNY: "0"}).Error; err != nil {
			return err
		}
	}
	return nil
}

func walletFundingRestore(tx *gorm.DB, request *WalletFundingRequest, amount int64) error {
	var allocations []WalletFundingAllocation
	if err := lockForUpdate(tx).Where("funding_request_id = ? AND quota > ?", request.ID, 0).Order("id DESC").Find(&allocations).Error; err != nil {
		return err
	}
	remaining := amount
	for i := range allocations {
		if remaining == 0 {
			break
		}
		allocation := &allocations[i]
		var lot WalletFundingLot
		if err := lockForUpdate(tx).First(&lot, allocation.LotID).Error; err != nil {
			return err
		}
		quantity := min(remaining, allocation.Quota)
		cash, err := walletFundingCash(allocation.CashCNY)
		if err != nil {
			return err
		}
		restoredCash := cash
		if quantity < allocation.Quota {
			restoredCash = cash.Mul(decimal.NewFromInt(quantity)).DivRound(decimal.NewFromInt(allocation.Quota), 24)
			if restoredCash.GreaterThan(cash) {
				restoredCash = cash
			}
		}
		lotCash, err := walletFundingCash(lot.RemainingCashCNY)
		if err != nil {
			return err
		}
		lot.RemainingQuota += quantity
		lot.RemainingCashCNY = lotCash.Add(restoredCash).String()
		if err := tx.Model(&lot).Select("remaining_quota", "remaining_cash_cny").Updates(&lot).Error; err != nil {
			return err
		}
		allocation.Quota -= quantity
		allocation.CashCNY = cash.Sub(restoredCash).String()
		if err := tx.Model(allocation).Select("quota", "cash_cny").Updates(allocation).Error; err != nil {
			return err
		}
		remaining -= quantity
	}
	if remaining != 0 {
		return ErrWalletFundingInvariant
	}
	return nil
}

func walletFundingApplyTargetTx(tx *gorm.DB, user *User, request *WalletFundingRequest, targetQuota int, requireAvailable bool) (*WalletFundingResult, error) {
	before, err := walletFundingAmounts(tx, request)
	if err != nil {
		return nil, err
	}
	delta := int64(targetQuota) - request.Quota
	result := &WalletFundingResult{Total: before, EventID: request.LastEventID}
	if delta == 0 {
		return result, nil
	}
	if requireAvailable && delta > 0 && delta > int64(user.Quota) {
		return nil, ErrInsufficientWalletFunding
	}
	afterQuota := int64(user.Quota) - delta
	if afterQuota > common.MaxWalletQuota || afterQuota < -common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	if delta > 0 {
		err = walletFundingConsume(tx, request, delta)
	} else {
		err = walletFundingRestore(tx, request, -delta)
	}
	if err != nil {
		return nil, err
	}
	request.Quota = int64(targetQuota)
	request.UpdatedAt = common.GetTimestamp()
	if err := tx.Model(request).Select("quota", "updated_at").Updates(request).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("quota", afterQuota).Error; err != nil {
		return nil, err
	}
	after, err := walletFundingAmounts(tx, request)
	if err != nil {
		return nil, err
	}
	difference := WalletFundingAmounts{Quota: delta, PaidQuota: after.PaidQuota - before.PaidQuota, GiftQuota: after.GiftQuota - before.GiftQuota, UnknownQuota: after.UnknownQuota - before.UnknownQuota, CashUnknownQuota: after.CashUnknownQuota - before.CashUnknownQuota, KnownCashRevenueCNY: after.KnownCashRevenueCNY.Sub(before.KnownCashRevenueCNY)}
	if difference.CashUnknownQuota == 0 {
		cash := difference.KnownCashRevenueCNY
		difference.CashRevenueCNY = &cash
	}
	event := WalletFundingEvent{UserID: user.Id, FundingRequestID: request.ID, RequestID: request.RequestID, Purpose: request.Purpose, Operation: "target", DeltaQuota: delta, PaidQuota: difference.PaidQuota, GiftQuota: difference.GiftQuota, UnknownQuota: difference.UnknownQuota, CashUnknownQuota: difference.CashUnknownQuota, KnownCashRevenueCNY: difference.KnownCashRevenueCNY.String(), TargetQuota: request.Quota, CreatedAt: common.GetTimestamp()}
	if err := tx.Create(&event).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(request).Update("last_event_id", event.ID).Error; err != nil {
		return nil, err
	}
	after.EventID, difference.EventID = event.ID, event.ID
	return &WalletFundingResult{Delta: difference, Total: after, Applied: true, AppliedDelta: int(delta), EventID: event.ID}, nil
}

// PrepareWalletFundingRequest persists enough origin metadata to expose a
// committed debit whose usage projection was interrupted by a process crash.
// It never reserves money or creates a charge event.
func PrepareWalletFundingRequest(userID int, requestID, purpose, modelName string, channelID int) error {
	if err := validateWalletFundingIdentity(requestID, purpose); err != nil {
		return err
	}
	if len(modelName) > 255 || channelID < 0 {
		return errors.New("invalid wallet funding request metadata")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		user, err := walletFundingUserForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if err := walletFundingOpening(tx, user); err != nil {
			return err
		}
		request, err := walletFundingRequestForUpdate(tx, userID, requestID, purpose)
		if err != nil {
			return err
		}
		if modelName != "" {
			if request.InitialModelName == "" {
				request.InitialModelName = modelName
			}
			request.ModelName = modelName
		}
		if channelID > 0 {
			request.ChannelID = channelID
		}
		return tx.Model(request).Select("model_name", "initial_model_name", "channel_id").Updates(request).Error
	})
}

// ApplyWalletFundingTarget atomically sets a request's cumulative wallet
// charge. Replaying an identical target cannot charge or refund it twice.
func ApplyWalletFundingTarget(userID int, requestID, purpose string, targetQuota int, requireAvailable bool) (*WalletFundingResult, error) {
	var result *WalletFundingResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = ApplyWalletFundingTargetWithTx(tx, userID, requestID, purpose, targetQuota, requireAvailable)
		return err
	})
	if err != nil {
		return nil, err
	}
	InvalidateWalletFundingCache(userID)
	return result, nil
}

// ApplyWalletFundingTargetWithTx shares the caller's task/token/accounting
// commit. The caller invalidates caches only after that transaction succeeds.
func ApplyWalletFundingTargetWithTx(tx *gorm.DB, userID int, requestID, purpose string, targetQuota int, requireAvailable bool) (*WalletFundingResult, error) {
	if tx == nil {
		return nil, errors.New("wallet funding transaction is required")
	}
	if err := validateWalletFundingIdentity(requestID, purpose); err != nil {
		return nil, err
	}
	if targetQuota < 0 || targetQuota > common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	user, err := walletFundingUserForUpdate(tx, userID)
	if err != nil {
		return nil, err
	}
	if err := walletFundingOpening(tx, user); err != nil {
		return nil, err
	}
	request, err := walletFundingRequestForUpdate(tx, userID, requestID, purpose)
	if err != nil {
		return nil, err
	}
	return walletFundingApplyTargetTx(tx, user, request, targetQuota, requireAvailable)
}

// AdoptLegacyWalletFunding records an already deducted legacy task as unknown
// without changing the wallet. Its original quota can then be refunded safely.
func AdoptLegacyWalletFunding(userID int, requestID, purpose string, alreadyChargedQuota int) (*WalletFundingResult, error) {
	if err := validateWalletFundingIdentity(requestID, purpose); err != nil {
		return nil, err
	}
	if alreadyChargedQuota < 0 || alreadyChargedQuota > common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	var result *WalletFundingResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		user, err := walletFundingUserForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if err := walletFundingOpening(tx, user); err != nil {
			return err
		}
		request, err := walletFundingRequestForUpdate(tx, userID, requestID, purpose)
		if err != nil {
			return err
		}
		if request.LegacyBaselineImported || request.LastEventID != 0 || request.Quota != 0 {
			total, err := walletFundingAmounts(tx, request)
			result = &WalletFundingResult{Total: total, EventID: request.LastEventID}
			return err
		}
		lot := WalletFundingLot{UserID: userID, Source: WalletFundingUnknown, SourceKey: fmt.Sprintf("legacy-consumption:%d", request.ID), OriginalQuota: int64(alreadyChargedQuota), RemainingQuota: 0, OriginalCashCNY: "0", RemainingCashCNY: "0", CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&lot).Error; err != nil {
			return err
		}
		if err := tx.Create(&WalletFundingAllocation{FundingRequestID: request.ID, LotID: lot.ID, Quota: int64(alreadyChargedQuota), CashCNY: "0"}).Error; err != nil {
			return err
		}
		request.Quota = int64(alreadyChargedQuota)
		request.LegacyBaselineImported = true
		if err := tx.Model(request).Select("quota", "legacy_baseline_imported").Updates(request).Error; err != nil {
			return err
		}
		total, err := walletFundingAmounts(tx, request)
		result = &WalletFundingResult{Total: total}
		return err
	})
	return result, err
}

func GetWalletFundingSnapshot(userID int, requestID, purpose string) (*WalletFundingAmounts, error) {
	if err := validateWalletFundingIdentity(requestID, purpose); err != nil {
		return nil, err
	}
	var result WalletFundingAmounts
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := walletFundingUserForUpdate(tx, userID); err != nil {
			return err
		}
		var request WalletFundingRequest
		if err := lockForUpdate(tx).Where("user_id = ? AND identity_hash = ?", userID, walletFundingIdentity(requestID, purpose)).First(&request).Error; err != nil {
			return err
		}
		var err error
		result, err = walletFundingAmounts(tx, &request)
		return err
	})
	return &result, err
}

// CreditWalletFundingWithTx must share the transaction that commits the
// payment/reward's idempotency marker. A nil paidCashCNY never implies zero.
func CreditWalletFundingWithTx(tx *gorm.DB, userID, quota int, source, sourceKey string, paidCashCNY *decimal.Decimal) error {
	if quota == 0 {
		return nil
	}
	if quota < 0 || quota > common.MaxWalletQuota {
		return ErrInvalidTopUpQuota
	}
	if source != WalletFundingPaid && source != WalletFundingGift && source != WalletFundingUnknown {
		return errors.New("invalid wallet funding source")
	}
	if len(sourceKey) > 191 {
		return errors.New("wallet funding source key too long")
	}
	user, err := walletFundingUserForUpdate(tx, userID)
	if err != nil {
		return err
	}
	if err := walletFundingOpening(tx, user); err != nil {
		return err
	}
	if user.Quota > common.MaxWalletQuota-quota {
		return ErrWalletQuotaLimitExceeded
	}
	cash, known := decimal.Zero, source == WalletFundingGift
	if source == WalletFundingPaid && paidCashCNY != nil {
		if paidCashCNY.IsNegative() || len(paidCashCNY.String()) > 96 {
			return errors.New("invalid wallet funding cash")
		}
		cash, known = *paidCashCNY, true
	}
	lot := WalletFundingLot{UserID: userID, Source: source, SourceKey: sourceKey, OriginalQuota: int64(quota), RemainingQuota: int64(quota), OriginalCashCNY: cash.String(), RemainingCashCNY: cash.String(), CashKnown: known, CreatedAt: common.GetTimestamp()}
	if err := tx.Create(&lot).Error; err != nil {
		return err
	}
	if err := tx.Model(&User{}).Where("id = ?", userID).Update("quota", int64(user.Quota)+int64(quota)).Error; err != nil {
		return err
	}
	event := WalletFundingEvent{UserID: userID, RequestID: sourceKey, Purpose: "wallet_credit", Operation: source, DeltaQuota: -int64(quota), KnownCashRevenueCNY: "0", CreatedAt: common.GetTimestamp()}
	switch source {
	case WalletFundingPaid:
		event.PaidQuota = -int64(quota)
	case WalletFundingGift:
		event.GiftQuota = -int64(quota)
	default:
		event.UnknownQuota = -int64(quota)
	}
	if !known {
		event.CashUnknownQuota = -int64(quota)
	}
	return tx.Create(&event).Error
}

func CreditUserGiftQuota(userID, quota int, sourceKey string) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		return CreditWalletFundingWithTx(tx, userID, quota, WalletFundingGift, sourceKey, nil)
	})
	if err == nil {
		InvalidateWalletFundingCache(userID)
	}
	return err
}

// DebitWalletFundingWithTx removes funds for a non-model operation, such as a
// manual adjustment or subscription purchase. It is never model cash income.
func DebitWalletFundingWithTx(tx *gorm.DB, userID, quota int, sourceKey string, requireAvailable bool) error {
	if quota < 0 || quota > common.MaxWalletQuota {
		return ErrWalletQuotaLimitExceeded
	}
	user, err := walletFundingUserForUpdate(tx, userID)
	if err != nil {
		return err
	}
	if err := walletFundingOpening(tx, user); err != nil {
		return err
	}
	request, err := walletFundingRequestForUpdate(tx, userID, sourceKey, "balance_adjustment")
	if err != nil {
		return err
	}
	_, err = walletFundingApplyTargetTx(tx, user, request, quota, requireAvailable)
	return err
}

func InvalidateWalletFundingCache(userID int) {
	if err := invalidateUserCache(userID); err != nil {
		common.SysError(fmt.Sprintf("failed to invalidate committed wallet cache for user %d: %v", userID, err))
	}
}
