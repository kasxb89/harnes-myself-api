package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// WeChat Pay Native top-up. The client asks for a QR payload, the payer scans
// it in WeChat, and WeChat posts an encrypted notification here which both
// proves payment and releases the quota — in one transaction.

const wechatOrderTTL = 30 * time.Minute

type wechatNativeRequest struct {
	// Amount is the top-up amount in CNY yuan, a whole number >= 1.
	Amount int `json:"amount"`
}

// quotaFromYuan converts a CNY amount into New API quota using the same
// conversion the console displays with (quota = yuan * QuotaPerUnit / rate).
func quotaFromYuan(yuan int) int {
	rate := operation_setting.USDExchangeRate
	if rate <= 0 {
		rate = 1
	}
	return int(math.Round(float64(yuan) * common.QuotaPerUnit / rate))
}

func wechatTradeNo(userId int) string {
	return fmt.Sprintf("wx%d%s", userId, common.GetRandomString(12))
}

// RequestWechatNativePay creates a pending order and returns the QR payload.
func RequestWechatNativePay(c *gin.Context) {
	if !common.WechatPayEnabled {
		common.ApiErrorT(c, "WeChat Pay has been disabled by administrator")
		return
	}
	if !common.WechatPayConfigured() {
		common.ApiErrorT(c, "WeChat Pay is not configured")
		return
	}
	var request wechatNativeRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if request.Amount < 1 || request.Amount > 100000 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}
	userId := c.GetInt("id")
	quota := quotaFromYuan(request.Amount)
	if quota <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	tradeNo := wechatTradeNo(userId)
	topUp := &model.TopUp{
		UserId:          userId,
		Amount:          int64(quota),
		Money:           float64(request.Amount),
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWechat,
		PaymentProvider: model.PaymentProviderWechat,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	if err := model.DB.Create(topUp).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	expireAt := time.Now().Add(wechatOrderTTL)
	codeUrl, err := common.CreateWechatNativeOrder(
		tradeNo,
		fmt.Sprintf("Yunshu top-up %d CNY", request.Amount),
		int64(request.Amount)*100,
		expireAt,
	)
	if err != nil {
		common.SysError(common.LogText("wechat native order failed: %s", err.Error()))
		common.ApiErrorT(c, "WeChat Pay is temporarily unavailable, please try again later")
		return
	}
	common.ApiSuccess(c, gin.H{
		"out_trade_no": tradeNo,
		"code_url":     codeUrl,
		"expires_at":   expireAt.Unix(),
		"quota":        quota,
		"amount":       request.Amount,
	})
}

// GetWechatTopUpStatus reports whether a pending order has been paid.
func GetWechatTopUpStatus(c *gin.Context) {
	tradeNo := strings.TrimSpace(c.Query("out_trade_no"))
	if tradeNo == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	topUp, err := model.FindTopUpByTradeNo(tradeNo)
	if err != nil || topUp.UserId != c.GetInt("id") {
		common.ApiErrorT(c, "Order not found")
		return
	}
	common.ApiSuccess(c, gin.H{
		"out_trade_no": topUp.TradeNo,
		"status":       topUp.Status,
		"quota":        topUp.Amount,
		"amount":       topUp.Money,
		"paid_at":      topUp.CompleteTime,
	})
}

type wechatNotification struct {
	EventType string `json:"event_type"`
	Resource  struct {
		Algorithm      string `json:"algorithm"`
		Ciphertext     string `json:"ciphertext"`
		Nonce          string `json:"nonce"`
		AssociatedData string `json:"associated_data"`
	} `json:"resource"`
}

type wechatTransaction struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionId string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	Amount        struct {
		Total int64 `json:"total"`
	} `json:"amount"`
}

// WechatPayNotify is the public callback WeChat calls after payment. It is
// idempotent: a replayed notification finds the order already settled.
func WechatPayNotify(c *gin.Context) {
	if !common.WechatPayEnabled || !common.WechatPayConfigured() {
		c.JSON(200, gin.H{"code": "FAIL", "message": "wechat pay not enabled"})
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"code": "FAIL", "message": "invalid body"})
		return
	}
	if err := common.VerifyWechatNotification(
		c.GetHeader("Wechatpay-Timestamp"),
		c.GetHeader("Wechatpay-Nonce"),
		string(raw),
		c.GetHeader("Wechatpay-Signature"),
	); err != nil {
		common.SysError(common.LogText("wechat notify signature rejected: %s", err.Error()))
		c.JSON(401, gin.H{"code": "FAIL", "message": "invalid signature"})
		return
	}
	if !common.WechatNotificationSerialMatches(c.GetHeader("Wechatpay-Serial")) {
		common.SysError(common.LogText(
			"wechat notify rejected: serial %s does not match the configured platform key",
			c.GetHeader("Wechatpay-Serial"),
		))
		c.JSON(401, gin.H{"code": "FAIL", "message": "unknown platform serial"})
		return
	}
	if !common.WechatNotificationTimestampFresh(c.GetHeader("Wechatpay-Timestamp")) {
		common.SysError(common.LogText("wechat notify rejected: timestamp outside the accepted window"))
		c.JSON(401, gin.H{"code": "FAIL", "message": "stale notification"})
		return
	}

	var notification wechatNotification
	if err := json.Unmarshal(raw, &notification); err != nil {
		c.JSON(400, gin.H{"code": "FAIL", "message": "invalid body"})
		return
	}
	if notification.EventType != "TRANSACTION.SUCCESS" {
		// Not a success event: acknowledge so WeChat stops retrying.
		c.JSON(200, gin.H{"code": "SUCCESS", "message": ""})
		return
	}
	plaintext, err := common.DecryptWechatResource(
		notification.Resource.Ciphertext,
		notification.Resource.Nonce,
		notification.Resource.AssociatedData,
	)
	if err != nil {
		common.SysError(common.LogText("wechat notify decryption failed: %s", err.Error()))
		c.JSON(400, gin.H{"code": "FAIL", "message": "invalid resource"})
		return
	}
	var transaction wechatTransaction
	if err := json.Unmarshal(plaintext, &transaction); err != nil {
		c.JSON(400, gin.H{"code": "FAIL", "message": "invalid resource"})
		return
	}
	if transaction.TradeState != "SUCCESS" {
		c.JSON(200, gin.H{"code": "SUCCESS", "message": ""})
		return
	}
	topUp, err := model.FindTopUpByTradeNo(transaction.OutTradeNo)
	if err != nil {
		common.SysError(common.LogText("wechat notify unknown order %s", transaction.OutTradeNo))
		c.JSON(404, gin.H{"code": "FAIL", "message": "order not found"})
		return
	}
	// The paid amount must match the order before any quota is released.
	if transaction.Amount.Total != int64(topUp.Money)*100 {
		common.SysError(common.LogText(
			"wechat notify amount mismatch for %s: paid %d, expected %s",
			transaction.OutTradeNo, transaction.Amount.Total, strconv.FormatFloat(topUp.Money, 'f', 2, 64),
		))
		c.JSON(400, gin.H{"code": "FAIL", "message": "amount mismatch"})
		return
	}
	if err := model.RechargeWechat(transaction.OutTradeNo, c.ClientIP()); err != nil {
		common.SysError(common.LogText("wechat notify recharge failed: %s", err.Error()))
		c.JSON(500, gin.H{"code": "FAIL", "message": "settlement failed"})
		return
	}
	c.JSON(200, gin.H{"code": "SUCCESS", "message": ""})
}

// RunWechatReconcileOnce settles orders whose payment notification never
// arrived. Without it a lost callback leaves a paid order pending forever.
// Safe to run repeatedly: settlement itself is idempotent.
func RunWechatReconcileOnce() (settled int, closed int) {
	if !common.WechatPayEnabled || !common.WechatPayConfigured() {
		return 0, 0
	}
	minAge := common.WechatReconcileMinAgeSeconds
	if minAge <= 0 {
		minAge = int(wechatOrderTTL.Seconds())
	}
	orders, err := model.ListPendingWechatTopUps(int64(minAge), 100)
	if err != nil {
		common.SysError(common.LogText("wechat reconcile: cannot list pending orders: %s", err.Error()))
		return 0, 0
	}
	for _, order := range orders {
		reply, err := common.QueryWechatOrder(order.TradeNo)
		if err != nil {
			// WeChat keeps an order queryable; a transient error is retried next round.
			common.SysError(common.LogText("wechat reconcile: query failed for %s: %s", order.TradeNo, err.Error()))
			continue
		}
		switch reply.TradeState {
		case "SUCCESS":
			if reply.Amount.Total != int64(order.Money)*100 {
				common.SysError(common.LogText(
					"wechat reconcile: amount mismatch for %s, refusing to settle", order.TradeNo))
				continue
			}
			if err := model.RechargeWechat(order.TradeNo, "reconcile"); err != nil {
				common.SysError(common.LogText("wechat reconcile: settle failed for %s: %s", order.TradeNo, err.Error()))
				continue
			}
			common.SysLog(common.LogText("wechat reconcile: settled %s without a notification", order.TradeNo))
			settled++
		case "CLOSED", "REVOKED", "PAYERROR":
			if err := model.UpdatePendingTopUpStatus(order.TradeNo, model.PaymentProviderWechat, common.TopUpStatusFailed); err != nil {
				common.SysError(common.LogText("wechat reconcile: close failed for %s: %s", order.TradeNo, err.Error()))
				continue
			}
			closed++
		}
	}
	return settled, closed
}

// StartWechatReconcile runs the reconciliation loop in the background.
func StartWechatReconcile() {
	interval := common.WechatReconcileIntervalSeconds
	if interval <= 0 {
		interval = 120
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if settled, closed := RunWechatReconcileOnce(); settled > 0 || closed > 0 {
			common.SysLog(common.LogText("wechat reconcile finished: settled=%d closed=%d", settled, closed))
		}
	}
}

// TriggerWechatReconcile lets an operator run one reconciliation round.
func TriggerWechatReconcile(c *gin.Context) {
	if !common.WechatPayEnabled {
		common.ApiErrorT(c, "WeChat Pay has been disabled by administrator")
		return
	}
	settled, closed := RunWechatReconcileOnce()
	common.ApiSuccess(c, gin.H{"settled": settled, "closed": closed})
}
