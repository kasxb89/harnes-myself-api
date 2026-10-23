package model

import (
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxClientWorkloadGlobalBytes = int64(512 * 1024 * 1024)
	MaxClientWorkloadUserBytes   = int64(16 * 1024 * 1024)

	clientWorkloadRuntimeReservedBytes     = int64(1024)
	clientWorkloadRecordReservedBytes      = int64(4096)
	clientWorkloadObservationReservedBytes = int64(512)
	clientWorkloadShotReservedBytes        = int64(1024)
)

var ErrClientWorkloadCapacity = errors.New("client workload storage capacity reached")

// ClientWorkloadBudget accounts for conservative reserved telemetry bytes. It
// does not measure filesystem usage and is unrelated to customer funds or quota.
type ClientWorkloadBudget struct {
	Key           string `gorm:"type:varchar(64);primaryKey" json:"-"`
	ReservedBytes int64  `gorm:"not null" json:"-"`
	UpdatedAt     int64  `gorm:"not null;autoUpdateTime:false" json:"-"`
}

type clientWorkloadBudgets struct {
	Global ClientWorkloadBudget
	User   ClientWorkloadBudget
}

// lockClientWorkloadBudgets must run inside the caller's publication transaction
// before telemetry reads or writes. Every publisher reserves the global guard
// first, then its user guard. The no-op write also obtains SQLite's write
// reservation before the first budget read; the shared row lock covers the
// other supported databases without dialect-specific SQL.
func lockClientWorkloadBudgets(tx *gorm.DB, userID int) (*clientWorkloadBudgets, error) {
	if tx == nil || userID <= 0 {
		return nil, ErrClientWorkloadCapacity
	}
	budgets := &clientWorkloadBudgets{}
	keys := []string{"global", "user:" + strconv.Itoa(userID)}
	rows := []*ClientWorkloadBudget{&budgets.Global, &budgets.User}
	for i, key := range keys {
		candidate := ClientWorkloadBudget{Key: key}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return nil, err
		}
		if err := tx.Model(&ClientWorkloadBudget{}).Where(clause.Eq{Column: "key", Value: key}).UpdateColumn("reserved_bytes", gorm.Expr("reserved_bytes")).Error; err != nil {
			return nil, err
		}
		if err := lockForUpdate(tx).Where(clause.Eq{Column: "key", Value: key}).Take(rows[i]).Error; err != nil {
			return nil, err
		}
		if rows[i].Key != key || rows[i].ReservedBytes < 0 {
			return nil, ErrClientWorkloadCapacity
		}
	}
	return budgets, nil
}

// applyClientWorkloadBudget checks both counters before changing either. A
// positive delta must fit both limits; zero or a valid refund is still accepted
// for an existing over-cap row. The caller rolls back all telemetry publication
// if either database update fails, so reservation and observations commit once.
func applyClientWorkloadBudget(tx *gorm.DB, budgets *clientWorkloadBudgets, delta int64, now int64) error {
	if tx == nil || budgets == nil || budgets.Global.Key != "global" {
		return ErrClientWorkloadCapacity
	}
	userText, found := strings.CutPrefix(budgets.User.Key, "user:")
	userID, err := strconv.Atoi(userText)
	if !found || err != nil || userID <= 0 || userText != strconv.Itoa(userID) {
		return ErrClientWorkloadCapacity
	}
	for _, budget := range []struct {
		current int64
		limit   int64
	}{{budgets.Global.ReservedBytes, MaxClientWorkloadGlobalBytes}, {budgets.User.ReservedBytes, MaxClientWorkloadUserBytes}} {
		if budget.current < 0 {
			return ErrClientWorkloadCapacity
		}
		if delta > 0 && (budget.current > budget.limit || delta > budget.limit-budget.current) {
			return ErrClientWorkloadCapacity
		}
		// Negate only the nonnegative current counter, never delta: MinInt64
		// is therefore rejected without overflowing before the comparison.
		if delta < 0 && delta < -budget.current {
			return ErrClientWorkloadCapacity
		}
	}
	if delta == 0 {
		return nil
	}
	for _, budget := range []*ClientWorkloadBudget{&budgets.Global, &budgets.User} {
		result := tx.Model(&ClientWorkloadBudget{}).Where(clause.Eq{Column: "key", Value: budget.Key}).Updates(map[string]any{
			"reserved_bytes": budget.ReservedBytes + delta,
			"updated_at":     now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
	}
	budgets.Global.ReservedBytes += delta
	budgets.User.ReservedBytes += delta
	budgets.Global.UpdatedAt, budgets.User.UpdatedAt = now, now
	return nil
}
