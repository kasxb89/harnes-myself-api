package controller

import (
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// upstreamTopupRequest is one hand-entered payment to an upstream provider.
type upstreamTopupRequest struct {
	Id          int    `json:"id"`
	Upstream    string `json:"upstream"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	PaidAt      int64  `json:"paid_at"`
	Note        string `json:"note"`
}

// normalize validates one entered payment and fills its defaults, so both the
// create and the update path accept the same body.
func (request *upstreamTopupRequest) normalize() *model.UpstreamTopup {
	upstream := strings.TrimSpace(request.Upstream)
	if upstream == "" {
		return nil
	}
	if len([]rune(upstream)) > 100 {
		upstream = string([]rune(upstream)[:100])
	}
	if request.AmountCents <= 0 {
		return nil
	}
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if currency == "" {
		currency = "CNY"
	}
	if len(currency) > 8 {
		return nil
	}
	paidAt := request.PaidAt
	if paidAt <= 0 {
		paidAt = common.GetTimestamp()
	}
	note := strings.TrimSpace(request.Note)
	if len([]rune(note)) > 255 {
		note = string([]rune(note)[:255])
	}
	return &model.UpstreamTopup{
		Id:          request.Id,
		Upstream:    upstream,
		AmountCents: request.AmountCents,
		Currency:    currency,
		PaidAt:      paidAt,
		Note:        note,
	}
}

// GetUpstreamTopups returns the recorded upstream payments with the three
// totals the profit view needs: what users paid this deployment, what users
// actually consumed (all time and today), and what it paid upstream.
func GetUpstreamTopups(c *gin.Context) {
	topups, err := model.ListUpstreamTopups()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	spent, err := model.SumUpstreamTopupCents()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	received, err := model.SumCompletedTopUpMoney()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Consumption is the recognised revenue: a prepaid balance is only earned
	// once the user spends it, so the profit view cannot read top-ups alone.
	consumed, err := model.SumUsedQuota(0, 0, 0, "", "", "", 0, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	consumedToday, err := model.SumUsedQuota(0, todayStart, 0, "", "", "", 0, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	activeToday, err := model.CountConsumersSince(todayStart)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	users, err := model.GetUserGrowth(todayStart)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	topupStats, err := model.GetTopUpStats(todayStart)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	topupUsers, err := model.ListUserTopUpTotals(100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recentTopUps, err := model.ListRecentTopUps(30)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	daily, err := model.OperationsByDay(14)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// The series is dense and ends today, so its last point is today's row.
	callsToday := 0
	if len(daily) > 0 {
		callsToday = daily[len(daily)-1].Calls
	}
	common.ApiSuccess(c, gin.H{
		"items":                topups,
		"spent_cents":          spent,
		"received":             received,
		"consumed_quota":       consumed.Quota,
		"consumed_quota_today": consumedToday.Quota,
		"active_users_today":   activeToday,
		"calls_today":          callsToday,
		"users":                users,
		"topups":               topupStats,
		"topup_users":          topupUsers,
		"recent_topups":        recentTopUps,
		"daily":                daily,
	})
}

// AddUpstreamTopup records one payment an administrator made to an upstream.
func AddUpstreamTopup(c *gin.Context) {
	request := upstreamTopupRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	topup := request.normalize()
	if topup == nil {
		common.ApiErrorT(c, "Enter the upstream, and an amount greater than zero")
		return
	}
	if err := model.CreateUpstreamTopup(topup); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, topup)
}

// UpdateUpstreamTopup rewrites one recorded payment.
func UpdateUpstreamTopup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid upstream top-up id")
		return
	}
	request := upstreamTopupRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	request.Id = id
	topup := request.normalize()
	if topup == nil {
		common.ApiErrorT(c, "Enter the upstream, and an amount greater than zero")
		return
	}
	if err := model.UpdateUpstreamTopup(topup); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, topup)
}

// DeleteUpstreamTopup removes one recorded payment.
func DeleteUpstreamTopup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid upstream top-up id")
		return
	}
	if err := model.DeleteUpstreamTopup(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
