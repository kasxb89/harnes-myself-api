package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
)

// UpstreamTopup records one payment this deployment made to an upstream
// provider. It is bookkeeping only — nothing in the relay reads it — and it
// exists so the profit view can subtract real money paid from real money
// received.
type UpstreamTopup struct {
	Id int `json:"id"`
	// Upstream names the provider that received the payment, e.g. the host of
	// the channel's base URL.
	Upstream string `json:"upstream" gorm:"type:varchar(100);index"`
	// AmountCents is the paid amount in the currency's minor unit, so no rounding
	// is lost when an administrator types a decimal amount.
	AmountCents int64 `json:"amount_cents"`
	// Currency is the ISO code the amount was paid in.
	Currency string `json:"currency" gorm:"type:varchar(8);default:'CNY'"`
	// PaidAt is when the payment was made, as a Unix timestamp in seconds.
	PaidAt int64 `json:"paid_at" gorm:"index"`
	// Note carries whatever the administrator needs to find the payment again.
	Note string `json:"note" gorm:"type:varchar(255);default:''"`
	// CreateTime is when this record was entered.
	CreateTime int64 `json:"create_time"`
}

// TableName keeps the ledger's table name explicit across all three databases.
func (UpstreamTopup) TableName() string {
	return "upstream_topups"
}

// ListUpstreamTopups returns every recorded payment, newest first.
func ListUpstreamTopups() ([]*UpstreamTopup, error) {
	topups := make([]*UpstreamTopup, 0)
	err := DB.Order("paid_at desc, id desc").Find(&topups).Error
	return topups, err
}

// CreateUpstreamTopup records one payment.
func CreateUpstreamTopup(topup *UpstreamTopup) error {
	topup.CreateTime = common.GetTimestamp()
	return DB.Create(topup).Error
}

// UpdateUpstreamTopup rewrites one recorded payment.
func UpdateUpstreamTopup(topup *UpstreamTopup) error {
	return DB.Model(&UpstreamTopup{}).Where("id = ?", topup.Id).Updates(map[string]any{
		"upstream":     topup.Upstream,
		"amount_cents": topup.AmountCents,
		"currency":     topup.Currency,
		"paid_at":      topup.PaidAt,
		"note":         topup.Note,
	}).Error
}

// DeleteUpstreamTopup removes one recorded payment.
func DeleteUpstreamTopup(id int) error {
	if id <= 0 {
		return errors.New("invalid upstream top-up id")
	}
	return DB.Delete(&UpstreamTopup{}, id).Error
}

// SumUpstreamTopupCents totals the recorded payments per currency, in the
// currency's minor unit.
func SumUpstreamTopupCents() (map[string]int64, error) {
	rows := make([]struct {
		Currency string
		Total    int64
	}, 0)
	err := DB.Model(&UpstreamTopup{}).
		Select("currency, coalesce(sum(amount_cents), 0) as total").
		Group("currency").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	totals := make(map[string]int64, len(rows))
	for _, row := range rows {
		totals[row.Currency] += row.Total
	}
	return totals, nil
}

// SumCompletedTopUpMoney totals what users actually paid this deployment, per
// currency, from the completed top-up records the payment providers confirm.
func SumCompletedTopUpMoney() (map[string]float64, error) {
	rows := make([]struct {
		PaymentProvider string
		Total           float64
	}, 0)
	// The top-up table predates per-currency accounting: every completed record
	// is in the deployment's own currency, so the provider column only groups.
	err := DB.Model(&TopUp{}).
		Select("payment_provider, coalesce(sum(money), 0) as total").
		Where("status = ?", common.TopUpStatusSuccess).
		Group("payment_provider").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	revenue := make(map[string]float64, len(rows))
	for _, row := range rows {
		revenue[row.PaymentProvider] += row.Total
	}
	return revenue, nil
}
