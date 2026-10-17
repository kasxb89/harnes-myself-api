package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOperationsByDayBucketsDenseLocalDays(t *testing.T) {
	originalLogDB, originalDB := LOG_DB, DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}, &User{}, &TopUp{}))
	LOG_DB, DB = db, db
	t.Cleanup(func() { LOG_DB, DB = originalLogDB, originalDB })

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	logs := []Log{
		{CreatedAt: today.Add(2 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 500, UserId: 1},
		{CreatedAt: today.Add(3 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 250, UserId: 2},
		{CreatedAt: yesterday.Add(5 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 100, UserId: 1},
		// A top-up is money in, not consumption; it must not reach the trend.
		{CreatedAt: today.Add(4 * time.Hour).Unix(), Type: LogTypeTopup, Quota: 9999, UserId: 1},
	}
	for i := range logs {
		require.NoError(t, db.Create(&logs[i]).Error)
	}

	users := []User{
		{Username: "u1", AffCode: "aff1", CreatedAt: today.Add(time.Hour).Unix(), Quota: 1000},
		{Username: "u2", AffCode: "aff2", CreatedAt: today.Add(2 * time.Hour).Unix(), Quota: 500},
		{Username: "u3", AffCode: "aff3", CreatedAt: yesterday.Add(time.Hour).Unix(), Quota: 250},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}

	topups := []TopUp{
		{UserId: 1, TradeNo: "t1", Money: 10, Status: common.TopUpStatusSuccess, CompleteTime: today.Add(time.Hour).Unix()},
		{UserId: 2, TradeNo: "t2", Money: 5, Status: common.TopUpStatusSuccess, CompleteTime: yesterday.Add(time.Hour).Unix()},
		// A pending order is not money received yet.
		{UserId: 1, TradeNo: "t3", Money: 99, Status: common.TopUpStatusPending, CompleteTime: today.Add(time.Hour).Unix()},
	}
	for i := range topups {
		require.NoError(t, db.Create(&topups[i]).Error)
	}

	series, err := OperationsByDay(14)
	require.NoError(t, err)
	require.Len(t, series, 14, "the series must stay dense so the chart has no gaps")
	require.Equal(t, today.Unix(), series[13].DayStart)
	require.Equal(t, 750, series[13].Quota)
	require.Equal(t, 2, series[13].Calls)
	require.Equal(t, 2, series[13].NewUsers)
	require.InDelta(t, 10, series[13].TopUpMoney, 0.001)
	require.Equal(t, 100, series[12].Quota)
	require.Equal(t, 1, series[12].Calls)
	require.Equal(t, 1, series[12].NewUsers)
	require.InDelta(t, 5, series[12].TopUpMoney, 0.001)
	require.Zero(t, series[0].Quota)

	active, err := CountConsumersSince(today.Unix())
	require.NoError(t, err)
	require.Equal(t, 2, active)

	growth, err := GetUserGrowth(today.Unix())
	require.NoError(t, err)
	require.Equal(t, int64(3), growth.Total)
	require.Equal(t, int64(2), growth.NewToday)
	require.Equal(t, int64(1750), growth.BalanceQuota)

	stats, err := GetTopUpStats(today.Unix())
	require.NoError(t, err)
	require.InDelta(t, 15, stats.MoneyTotal, 0.001)
	require.Equal(t, int64(2), stats.CountTotal)
	require.Equal(t, int64(2), stats.UsersTotal)
	require.InDelta(t, 10, stats.MoneyToday, 0.001)
	require.Equal(t, int64(1), stats.CountToday)
	require.Equal(t, int64(1), stats.UsersToday)

	perUser, err := ListUserTopUpTotals(10)
	require.NoError(t, err)
	require.Len(t, perUser, 2, "only accounts with a successful payment are listed")
	require.Equal(t, 1, perUser[0].UserId)
	require.Equal(t, "u1", perUser[0].Username)
	require.InDelta(t, 10, perUser[0].Money, 0.001)
	require.Equal(t, int64(1), perUser[0].Count)
	require.Equal(t, today.Add(time.Hour).Unix(), perUser[0].LastPaidAt)
	require.Equal(t, 2, perUser[1].UserId)
	require.InDelta(t, 5, perUser[1].Money, 0.001)

	recent, err := ListRecentTopUps(10)
	require.NoError(t, err)
	require.Len(t, recent, 2, "a pending order is not a payment")
	require.Equal(t, 1, recent[0].UserId)
	require.InDelta(t, 10, recent[0].Money, 0.001)
	require.Equal(t, 2, recent[1].UserId)
}
