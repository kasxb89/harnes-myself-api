package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
// ---------------------------------------------------------------------------

// ErrInsufficientWalletQuota 钱包原子预扣失败（余额不足），未发生任何扣减。
// BillingSession 据此映射为 ErrorCodeInsufficientUserQuota，
// 使 wallet_first 等计费偏好可以回退到订阅。
var ErrInsufficientWalletQuota = errors.New("wallet quota insufficient")

var errInsufficientWalletTokenQuota = errors.New("token quota is not enough, insufficient remaining quota")

type WalletFunding struct {
	userId    int
	requestId string
	purpose   string
	modelName string
	channelID int
	tokenID   int
	unlimited bool
	consumed  int // 当前原请求已扣的累计净额度
	lastDelta int
	snapshot  *model.WalletFundingAmounts
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return w.applyTarget(w.consumed, true, true)
	}
	return w.applyTarget(w.consumed+amount, true, true)
}

func (w *WalletFunding) Settle(delta int) error {
	return w.applyTarget(w.consumed+delta, false, false)
}

func (w *WalletFunding) Refund() error {
	return w.applyTarget(0, false, false)
}

// applyTarget keeps wallet quota and its original funding allocation in one
// durable transaction. A retry of the same cumulative target cannot debit or
// credit the wallet a second time.
func (w *WalletFunding) applyTarget(target int, requireAvailable, requireTokenAvailable bool) error {
	if w.requestId == "" {
		w.requestId = common.NewRequestId()
		if w.purpose == "" {
			w.purpose = "relay"
		}
		// Compatibility with in-flight/legacy sessions whose initial reserve
		// predates the journal. Imported funds remain explicitly unknown.
		if w.consumed > 0 {
			if _, err := model.AdoptLegacyWalletFunding(w.userId, w.requestId, w.purpose, w.consumed); err != nil {
				return err
			}
		}
	}
	if w.purpose == "" {
		w.purpose = "relay"
	}
	if err := model.PrepareWalletFundingRequest(w.userId, w.requestId, w.purpose, w.modelName, w.channelID); err != nil {
		return err
	}
	if err := model.InvalidateUserTokensCache(w.userId); err != nil {
		common.SysError(common.LogText("failed to invalidate token quota cache: %s", err.Error()))
	}
	var result *model.WalletFundingResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = model.ApplyWalletFundingTargetWithTx(tx, w.userId, w.requestId, w.purpose, target, requireAvailable)
		if err != nil {
			return err
		}
		if result.Applied && w.tokenID > 0 {
			return adjustWalletTokenQuota(tx, w.userId, w.tokenID, result.AppliedDelta, requireTokenAvailable, w.unlimited, false)
		}
		return nil
	})
	if errors.Is(err, model.ErrInsufficientWalletFunding) {
		return ErrInsufficientWalletQuota
	}
	if err != nil {
		return err
	}
	model.InvalidateWalletFundingCache(w.userId)
	if err := model.InvalidateUserTokensCache(w.userId); err != nil {
		common.SysError(common.LogText("failed to invalidate token cache after task billing: %s", err.Error()))
	}
	w.consumed = int(result.Total.Quota)
	w.lastDelta = result.AppliedDelta
	w.snapshot = &result.Total
	return nil
}

func (w *WalletFunding) setRelayMetadata(info *relaycommon.RelayInfo) {
	w.modelName = info.OriginModelName
	w.tokenID, w.unlimited = info.TokenId, info.TokenUnlimited
	if info.IsPlayground {
		w.tokenID = 0
	}
	if info.ChannelMeta != nil {
		w.channelID = info.ChannelId
	}
}

// adjustWalletTokenQuota shares the wallet transaction. Missing historical
// task tokens can be skipped; an active request's missing token cannot charge.
func adjustWalletTokenQuota(tx *gorm.DB, userID, tokenID, delta int, requireAvailable, unlimited, allowMissing bool) error {
	if tokenID <= 0 || delta == 0 {
		return nil
	}
	query := tx.Model(&model.Token{}).Where("id = ? AND user_id = ?", tokenID, userID)
	if requireAvailable && delta > 0 && !unlimited {
		query = query.Where("remain_quota >= ?", delta)
	}
	result := query.Updates(map[string]any{
		"remain_quota":  gorm.Expr("remain_quota - ?", delta),
		"used_quota":    gorm.Expr("used_quota + ?", delta),
		"accessed_time": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 && !allowMissing {
		return errInsufficientWalletTokenQuota
	}
	return nil
}

func (w *WalletFunding) syncRelayInfo(info *relaycommon.RelayInfo) {
	info.FundingRequestId = w.requestId
	info.FundingPurpose = w.purpose
	info.FundingConsumedQuota = w.consumed
	info.FundingSnapshot = w.snapshot
}

// BindWalletFundingLogContext supplies an explicitly verified wallet snapshot
// to the host log/financial projection. A missing snapshot stays unknown.
func BindWalletFundingLogContext(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || info == nil {
		return
	}
	c.Set("wallet_funding", info.FundingSnapshot)
	c.Set("wallet_funding_request_id", info.FundingRequestId)
	c.Set("wallet_funding_purpose", info.FundingPurpose)
	c.Set("wallet_funding_event_id", int64(0))
	if snapshot, ok := info.FundingSnapshot.(*model.WalletFundingAmounts); ok && snapshot != nil {
		c.Set("wallet_funding_event_id", snapshot.EventID)
	}
}

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	preConsumed    int64
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscription(s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	return model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta))
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.RefundSubscriptionPreConsume(s.requestId)
	})
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := range maxAttempts {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
