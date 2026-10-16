package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConsumptionByDayBucketsDenseLocalDays(t *testing.T) {
	originalLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = originalLogDB })

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	rows := []Log{
		{CreatedAt: today.Add(2 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 500, UserId: 1},
		{CreatedAt: today.Add(3 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 250, UserId: 2},
		{CreatedAt: today.AddDate(0, 0, -1).Add(5 * time.Hour).Unix(), Type: LogTypeConsume, Quota: 100, UserId: 1},
		// A top-up is money in, not consumption; it must not reach the trend.
		{CreatedAt: today.Add(4 * time.Hour).Unix(), Type: LogTypeTopup, Quota: 9999, UserId: 1},
	}
	for i := range rows {
		require.NoError(t, db.Create(&rows[i]).Error)
	}

	series, err := ConsumptionByDay(14)
	require.NoError(t, err)
	require.Len(t, series, 14, "the series must stay dense so the chart has no gaps")
	require.Equal(t, today.Unix(), series[13].DayStart)
	require.Equal(t, 750, series[13].Quota)
	require.Equal(t, 2, series[13].Calls)
	require.Equal(t, 100, series[12].Quota)
	require.Equal(t, 1, series[12].Calls)
	require.Zero(t, series[0].Quota)

	active, err := CountConsumersSince(today.Unix())
	require.NoError(t, err)
	require.Equal(t, 2, active)
}
