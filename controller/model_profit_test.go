package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelProfitHistoricalFundingDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_logs_%t", separate), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB := db
			if separate {
				logDB, _ = newAuditTestDatabase(t, kind, dsn)
			}
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, logDB, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Option{}, &model.Task{}, &model.MediaAdmission{}, &model.H3Admission{}))
			require.NoError(t, db.AutoMigrate(model.WalletFundingModels()...))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			oldQuota, oldRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
			common.QuotaPerUnit, operation_setting.USDExchangeRate = 100, 10
			t.Cleanup(func() { common.QuotaPerUnit, operation_setting.USDExchangeRate = oldQuota, oldRate })
			ctx := context.Background()
			at := common.GetTimestamp() - 1000
			user := model.User{Username: "historical-funding-user", Quota: 1000}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, logDB.Create(&model.Log{UserId: user.Id, Type: model.LogTypeSystem, CreatedAt: at - 10, Other: `{}`}).Error)
			registration := model.Log{UserId: user.Id, Type: model.LogTypeSystem, CreatedAt: at, RequestId: "history-registration", Other: `{"balance_change":{"category":"registration_bonus","quota_delta":100,"quota_before":0,"quota_after":100}}`}
			require.NoError(t, logDB.Create(&registration).Error)
			topup := model.TopUp{UserId: user.Id, Amount: 1000, Money: 80, TradeNo: "history-wechat", PaymentProvider: model.PaymentProviderWechat, CompleteTime: at + 1, Status: common.TopUpStatusSuccess}
			require.NoError(t, db.Create(&topup).Error)
			consume := model.Log{UserId: user.Id, Type: model.LogTypeConsume, CreatedAt: at + 2, ModelName: "historical-image", ChannelId: 18, Quota: 150, RequestId: "history-charge", Other: `{"task_id":"historical-task"}`}
			require.NoError(t, logDB.Create(&consume).Error)
			refund := model.Log{UserId: user.Id, Type: model.LogTypeRefund, CreatedAt: at + 3, ModelName: "historical-image", ChannelId: 18, Quota: 50, RequestId: "history-charge", Other: `{"task_id":"historical-task","pre_consumed_quota":150,"actual_quota":100}`}
			require.NoError(t, logDB.Create(&refund).Error)
			require.NoError(t, db.Create(&model.Task{TaskID: "historical-task", UserId: user.Id, ChannelId: 18, Status: model.TaskStatusSuccess, Quota: 100, SubmitTime: at + 2, CreatedAt: at + 2}).Error)
			cost := 2.0
			_, err := model.SaveModelProfitCostRules(ctx, model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{{ChannelID: 18, ModelName: "historical-image", EffectiveFrom: 1, Kind: "request", UnitCostCNY: &cost, Source: "historical test procurement quote", FailureRefunded: true}}})
			require.NoError(t, err)
			sentinel := model.ModelProfitBillingRecord{EventKey: "history-schema-sentinel", UserID: user.Id, CreatedAt: at - 100, ModelName: "sentinel", Other: `{}`, Quota: 23}
			require.NoError(t, db.Create(&sentinel).Error)
			require.NoError(t, db.Migrator().DropColumn(&model.ModelProfitBillingRecord{}, "HistoricalLogID"))
			require.NoError(t, db.Migrator().DropColumn(&model.ModelProfitBillingRecord{}, "HistoricalLogHash"))
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.ModelProfitBillingRecord{}))
			}
			var preserved model.ModelProfitBillingRecord
			require.NoError(t, db.First(&preserved, sentinel.ID).Error)
			assert.Equal(t, sentinel.EventKey, preserved.EventKey)
			assert.Equal(t, sentinel.Quota, preserved.Quota)
			assert.Empty(t, preserved.HistoricalLogHash)
			assert.Zero(t, preserved.HistoricalLogID)
			beforeImport, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at, End: at + 10})
			require.NoError(t, err)
			require.NotNil(t, beforeImport.Summary.UpstreamCostCNY)
			assert.Equal(t, 2.0, *beforeImport.Summary.UpstreamCostCNY)
			before := user.Quota
			plan, err := model.BuildHistoricalProfitFunding(ctx, at+10, 10000)
			require.NoError(t, err)
			require.Len(t, plan.Records, 2)
			assert.Empty(t, plan.Reasons)
			inserted, err := model.ApplyHistoricalProfitFunding(ctx, plan)
			require.NoError(t, err)
			assert.EqualValues(t, 2, inserted)
			var imported model.ModelProfitBillingRecord
			require.NoError(t, db.Where("historical_log_id = ?", consume.Id).First(&imported).Error)
			assert.NotEqualValues(t, consume.Id, imported.ID, "historical usage IDs differ from the new projection sequence")
			inserted, err = model.ApplyHistoricalProfitFunding(ctx, plan)
			require.NoError(t, err)
			assert.Zero(t, inserted, "history replay must not duplicate financial events")
			var unchanged model.User
			require.NoError(t, db.First(&unchanged, user.Id).Error)
			assert.Equal(t, before, unchanged.Quota)
			assert.True(t, unchanged.WalletFundingInitialized, "a fully proven cold opening preserves its source metadata")
			require.Len(t, plan.OpeningBalances, 1)
			assert.EqualValues(t, 1000, plan.OpeningBalances[0].Lots[0].RemainingQuota)
			assert.Equal(t, "80", plan.OpeningBalances[0].Lots[0].RemainingCashCNY)
			var original model.Log
			require.NoError(t, logDB.First(&original, consume.Id).Error)
			assert.Equal(t, consume.Other, original.Other, "separate retained usage evidence stays immutable")
			// A refund reverses the paid tail of this original FIFO allocation:
			// gross cash 4, refund cash -4, remaining gift consumption 100.
			report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at, End: at + 10})
			require.NoError(t, err)
			assert.EqualValues(t, 2, report.Summary.BillingEvents, "old usage logs and reconstructed projections are counted once")
			require.NotNil(t, report.Summary.UpstreamCostCNY)
			assert.Equal(t, 2.0, *report.Summary.UpstreamCostCNY, "imported task cost and partial refund keep the original historical log identity")
			assert.EqualValues(t, 2, report.Summary.KnownCostEvents)
			require.NotNil(t, report.Summary.CashRevenueCNY)
			assert.Zero(t, *report.Summary.CashRevenueCNY)
			assert.InDelta(t, 10, report.Summary.ConfirmedGiftConsumedCNY, 1e-12)
			refundOnly, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at + 3, End: at + 4})
			require.NoError(t, err)
			require.NotNil(t, refundOnly.Summary.CashRevenueCNY)
			assert.InDelta(t, -4, *refundOnly.Summary.CashRevenueCNY, 1e-12)
			// Retained source edits invalidate the plan instead of silently
			// overwriting financial evidence or inventing a revised receipt.
			require.NoError(t, logDB.Model(&model.Log{}).Where("id = ?", consume.Id).Update("quota", 151).Error)
			_, err = model.ApplyHistoricalProfitFunding(ctx, plan)
			require.EqualError(t, err, "historical source evidence changed")
			gap, err := model.BuildHistoricalProfitFunding(ctx, at+10, 10000)
			require.NoError(t, err)
			assert.Empty(t, gap.Records, "an unexplained one-quota balance gap must prevent cash attribution")
			assert.Greater(t, gap.Reasons["snapshot_balance_mismatch"], 0)
			require.NoError(t, logDB.Model(&model.Log{}).Where("id = ?", consume.Id).Update("quota", 150).Error)
			// An administrator's exact balance anchor establishes a suffix;
			// its pre-existing balance is unknown, its newly granted amount
			// is a gift, and neither can become cash just because sums match.
			anchored := model.User{Username: "historical-anchored", AffCode: "history-aff-anchor", Quota: 50}
			require.NoError(t, db.Create(&anchored).Error)
			adjust := model.Log{UserId: anchored.Id, Type: model.LogTypeTopup, CreatedAt: at + 20, Other: `{"op":{"action":"user.quota_add","params":{"from":100,"to":200}}}`}
			require.NoError(t, logDB.Create(&adjust).Error)
			oldBalanceUse := model.Log{UserId: anchored.Id, Type: model.LogTypeConsume, CreatedAt: at + 21, ModelName: "anchored-history", ChannelId: 18, Quota: 150, RequestId: "anchored-charge", Other: `{}`}
			require.NoError(t, logDB.Create(&oldBalanceUse).Error)
			partial, err := model.BuildHistoricalProfitFunding(ctx, at+30, 10000)
			require.NoError(t, err)
			require.Len(t, partial.Records, 3)
			_, err = model.ApplyHistoricalProfitFunding(ctx, partial)
			require.NoError(t, err)
			partialReport, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at + 20, End: at + 30, ModelName: "anchored-history"})
			require.NoError(t, err)
			assert.Nil(t, partialReport.Summary.CashRevenueCNY)
			assert.InDelta(t, 5, partialReport.Summary.ConfirmedGiftConsumedCNY, 1e-12)
			assert.InDelta(t, 10, partialReport.Summary.UnknownFundingConsumedCNY, 1e-12)
			odd := model.User{Username: "historical-odd-ratio", AffCode: "history-odd-aff", Quota: 3}
			require.NoError(t, db.Create(&odd).Error)
			zeroAnchor := model.Log{UserId: odd.Id, Type: model.LogTypeSystem, CreatedAt: at + 40, Other: `{"balance_change":{"category":"registration_bonus","quota_delta":0,"quota_before":0,"quota_after":0}}`}
			require.NoError(t, logDB.Create(&zeroAnchor).Error)
			oddTopup := model.TopUp{UserId: odd.Id, Amount: 3, Money: 1, TradeNo: "history-odd-topup", PaymentProvider: model.PaymentProviderWechat, CompleteTime: at + 41, Status: common.TopUpStatusSuccess}
			require.NoError(t, db.Create(&oddTopup).Error)
			for i, quota := range []int{1, 2, 3} {
				kind := model.LogTypeConsume
				if i == 2 {
					kind = model.LogTypeRefund
				}
				entry := model.Log{UserId: odd.Id, Type: kind, CreatedAt: at + 42 + int64(i), ModelName: "odd-ratio-history", ChannelId: 18, Quota: quota, RequestId: "odd-original", Other: `{}`}
				require.NoError(t, logDB.Create(&entry).Error)
			}
			blocked := model.User{Username: "historical-pending-user", AffCode: "history-pending-aff", Quota: 50}
			require.NoError(t, db.Create(&blocked).Error)
			require.NoError(t, logDB.Create(&model.Log{UserId: blocked.Id, Type: model.LogTypeSystem, CreatedAt: at + 60, Other: `{"balance_change":{"category":"registration_bonus","quota_delta":100,"quota_before":0,"quota_after":100}}`}).Error)
			require.NoError(t, logDB.Create(&model.Log{UserId: blocked.Id, Type: model.LogTypeConsume, CreatedAt: at + 61, ModelName: "pending-history", ChannelId: 18, Quota: 50, RequestId: "pending-history-request", Other: `{}`}).Error)
			require.NoError(t, db.Create(&model.Task{TaskID: "old-result-unknown", UserId: blocked.Id, Status: model.TaskStatusUnknown}).Error)
			oddPlan, err := model.BuildHistoricalProfitFunding(ctx, at+70, 10000)
			require.NoError(t, err)
			assert.Equal(t, 1, oddPlan.OpeningSkippedReasons["pending_or_unknown_task"])
			for _, opening := range oddPlan.OpeningBalances {
				assert.NotEqual(t, blocked.Id, opening.UserID, "an unresolved old submission must prevent seeding its user's opening lots")
			}
			var beforeFailure int64
			require.NoError(t, db.Model(&model.ModelProfitBillingRecord{}).Count(&beforeFailure).Error)
			const fault = "test:historical_opening_seed_failure"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(fault, func(tx *gorm.DB) {
				if lot, ok := tx.Statement.Dest.(*model.WalletFundingLot); ok && lot.UserID == odd.Id {
					tx.AddError(errors.New("historical seed write failed"))
				}
			}))
			_, err = model.ApplyHistoricalProfitFunding(ctx, oddPlan)
			require.EqualError(t, err, "historical seed write failed")
			require.NoError(t, db.Callback().Create().Remove(fault))
			var afterFailure int64
			require.NoError(t, db.Model(&model.ModelProfitBillingRecord{}).Count(&afterFailure).Error)
			assert.Equal(t, beforeFailure, afterFailure, "seed failure rolls back financial projections in the same transaction")
			var oddAfterFailure model.User
			require.NoError(t, db.First(&oddAfterFailure, odd.Id).Error)
			assert.False(t, oddAfterFailure.WalletFundingInitialized)
			assert.Equal(t, odd.Quota, oddAfterFailure.Quota)
			_, err = model.ApplyHistoricalProfitFunding(ctx, oddPlan)
			require.NoError(t, err)
			oddRefund, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at + 44, End: at + 45, ModelName: "odd-ratio-history"})
			require.NoError(t, err)
			require.NotNil(t, oddRefund.Summary.CashRevenueCNY)
			assert.Equal(t, -1.0, *oddRefund.Summary.CashRevenueCNY, "full refund restores the original cash residual exactly")
			var blockedTask model.Task
			require.NoError(t, db.Where("task_id = ?", "old-result-unknown").First(&blockedTask).Error)
			require.NoError(t, db.Model(&model.Task{}).Where("id = ?", blockedTask.ID).Update("status", model.TaskStatusSuccess).Error)
			admission := model.MediaAdmission{ID: "history-released-uncertain", TaskDBID: blockedTask.ID, State: model.MediaReleased, BillingState: "refund_pending"}
			require.NoError(t, db.Create(&admission).Error)
			uncertain, err := model.BuildHistoricalProfitFunding(ctx, at+70, 10000)
			require.NoError(t, err)
			assert.Equal(t, 1, uncertain.OpeningSkippedReasons["pending_or_unknown_task"], "released does not prove its pending refund settled")
			require.NoError(t, db.Model(&model.MediaAdmission{}).Where("id = ?", admission.ID).Update("billing_state", "reserved").Error)
			reserved, err := model.BuildHistoricalProfitFunding(ctx, at+70, 10000)
			require.NoError(t, err)
			blockedEligible := false
			for _, opening := range reserved.OpeningBalances {
				blockedEligible = blockedEligible || opening.UserID == blocked.Id
			}
			assert.True(t, blockedEligible, "released plus reserved is an ordinary fully charged success, not an unsettled refund")
			require.NoError(t, db.Model(&model.MediaAdmission{}).Where("id = ?", admission.ID).UpdateColumn("state", nil).Error)
			nullState, err := model.BuildHistoricalProfitFunding(ctx, at+70, 10000)
			require.NoError(t, err)
			assert.Equal(t, 1, nullState.OpeningSkippedReasons["pending_or_unknown_task"], "a NULL legacy admission state must fail closed")
			require.NoError(t, db.Model(&model.MediaAdmission{}).Where("id = ?", admission.ID).UpdateColumn("state", model.MediaReleased).Error)
			require.NoError(t, db.Model(&model.Task{}).Where("id = ?", blockedTask.ID).UpdateColumn("status", nil).Error)
			nullStatus, err := model.BuildHistoricalProfitFunding(ctx, at+70, 10000)
			require.NoError(t, err)
			assert.Equal(t, 1, nullStatus.OpeningSkippedReasons["pending_or_unknown_task"], "a NULL legacy task status must fail closed")
			require.NoError(t, db.Model(&model.Task{}).Where("id = ?", blockedTask.ID).UpdateColumn("status", model.TaskStatusSuccess).Error)
			future, err := model.ApplyWalletFundingTarget(user.Id, "after-proven-history", "relay", 100, true)
			require.NoError(t, err)
			assert.EqualValues(t, 100, future.Total.PaidQuota)
			require.NotNil(t, future.Total.CashRevenueCNY)
			assert.Equal(t, "8", future.Total.CashRevenueCNY.String(), "new requests consume the proven original 80/100 discount rather than an unknown legacy balance")
			_, err = model.ApplyHistoricalProfitFunding(ctx, oddPlan)
			require.Error(t, err, "a live funding request must prevent replaying the cold migration")
		})
	}
}

func TestModelProfitHistoricalConcurrentSecondDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, tc := range []struct {
		name                   string
		gift, paid, secondGift int
		known                  bool
	}{
		{"mixed_gift_paid", 50, 150, 0, false},
		{"single_gift_batch", 200, 0, 0, true},
		{"contiguous_gift_batches", 50, 0, 150, true},
		{"paid_batch_conservative", 0, 200, 0, false},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_reverse_%t", tc.name, reverse), func(t *testing.T) {
				db, _ := newAuditTestDatabase(t, kind, dsn)
				typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
				useLogExportDatabases(t, db, db, typ, typ)
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Option{}, &model.Task{}, &model.Log{}))
				require.NoError(t, db.AutoMigrate(model.WalletFundingModels()...))
				ctx := context.Background()
				at := common.GetTimestamp() - 100
				user := model.User{Username: "same-second-user", Quota: tc.gift + tc.paid + tc.secondGift - 150}
				require.NoError(t, db.Create(&user).Error)
				registration := fmt.Sprintf(`{"balance_change":{"category":"registration_bonus","quota_delta":%d,"quota_before":0,"quota_after":%d}}`, tc.gift, tc.gift)
				require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeSystem, CreatedAt: at, Other: registration}).Error)
				if tc.paid > 0 {
					require.NoError(t, db.Create(&model.TopUp{UserId: user.Id, Amount: int64(tc.paid), Money: float64(tc.paid) / 10, TradeNo: "same-second-payment", PaymentProvider: model.PaymentProviderWechat, CompleteTime: at + 1, Status: common.TopUpStatusSuccess}).Error)
				}
				if tc.secondGift > 0 {
					grant := fmt.Sprintf(`{"op":{"action":"user.quota_add","params":{"from":%d,"to":%d}}}`, tc.gift+tc.paid, tc.gift+tc.paid+tc.secondGift)
					require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeTopup, CreatedAt: at + 2, Other: grant}).Error)
				}
				models := []string{"same-second-a", "same-second-b"}
				if reverse {
					models[0], models[1] = models[1], models[0]
				}
				// Both commit orders produce the exact same final wallet. Legacy
				// append IDs may be reversed by response/logging completion, so
				// neither insertion order can establish per-model cash attribution.
				for _, name := range models {
					require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeConsume, CreatedAt: at + 10, ModelName: name, ChannelId: 18, Quota: 75, RequestId: name, Other: `{}`}).Error)
				}
				plan, err := model.BuildHistoricalProfitFunding(ctx, at+20, 10000)
				require.NoError(t, err)
				if tc.known {
					require.Len(t, plan.Records, 2)
					require.Len(t, plan.OpeningBalances, 1)
					assert.Empty(t, plan.Reasons)
				} else {
					assert.Empty(t, plan.Records)
					assert.Empty(t, plan.OpeningBalances)
					assert.Equal(t, 2, plan.Reasons["ambiguous_same_second_order"])
				}
				_, err = model.ApplyHistoricalProfitFunding(ctx, plan)
				require.NoError(t, err)
				for _, name := range models {
					report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at, End: at + 20, ModelName: name})
					require.NoError(t, err)
					assert.EqualValues(t, 1, report.Summary.BillingEvents)
					if tc.known {
						require.NotNil(t, report.Summary.CashRevenueCNY)
						assert.Zero(t, *report.Summary.CashRevenueCNY)
						assert.Positive(t, report.Summary.ConfirmedGiftConsumedCNY)
					} else {
						assert.Nil(t, report.Summary.CashRevenueCNY)
						assert.Zero(t, report.Summary.ConfirmedCashRevenueCNY)
					}
				}
				if tc.name == "mixed_gift_paid" && !reverse {
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota+75).Error)
					require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeRefund, CreatedAt: at + 11, ModelName: "same-second-a", ChannelId: 18, Quota: 75, RequestId: "same-second-a", Other: `{}`}).Error)
					refunded, err := model.BuildHistoricalProfitFunding(ctx, at+20, 10000)
					require.NoError(t, err)
					assert.Empty(t, refunded.Records, "a later original refund cannot use the arbitrary mixed-source append-ID allocation")
					assert.Empty(t, refunded.OpeningBalances)
				}
			})
		}
	}
}

func TestModelProfitCollectedFundingDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_logs_%t", separate), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB := db
			if separate {
				logDB, _ = newAuditTestDatabase(t, kind, dsn)
			}
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, logDB, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.Task{}))
			require.NoError(t, db.AutoMigrate(model.WalletFundingModels()...))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			oldQuota, oldRate, oldLogging := common.QuotaPerUnit, operation_setting.USDExchangeRate, common.LogConsumeEnabled
			common.QuotaPerUnit, operation_setting.USDExchangeRate, common.LogConsumeEnabled = 100, 10, false
			t.Cleanup(func() {
				common.QuotaPerUnit, operation_setting.USDExchangeRate, common.LogConsumeEnabled = oldQuota, oldRate, oldLogging
			})
			ctx := context.Background()
			now := common.GetTimestamp()
			cost := 2.0
			_, err := model.SaveModelProfitCostRules(ctx, model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{
				{ChannelID: 19, ModelName: "funded-image", EffectiveFrom: 1, Kind: "request", UnitCostCNY: &cost, Source: "agreed procurement price", FailureRefunded: true},
			}})
			require.NoError(t, err)
			user := model.User{Username: "funded-user", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			cash := decimal.NewFromInt(80)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				if err := model.CreditWalletFundingWithTx(tx, user.Id, 100, model.WalletFundingGift, "registration-test", nil); err != nil {
					return err
				}
				return model.CreditWalletFundingWithTx(tx, user.Id, 1000, model.WalletFundingPaid, "discounted-topup-test", &cash)
			}))
			funding, err := model.ApplyWalletFundingTarget(user.Id, "funded-request", "relay", 150, true)
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(common.RequestIdKey, "funded-api-request")
			c.Set("wallet_funding", &funding.Total)
			c.Set("wallet_funding_request_id", "funded-request")
			c.Set("wallet_funding_purpose", "relay")
			params := model.RecordConsumeLogParams{ChannelId: 19, ModelName: "funded-image", Quota: 150, Other: model.NewLogOther()}
			model.RecordConsumeLog(c, user.Id, params)
			model.RecordConsumeLog(c, user.Id, params) // replay cannot add financial income twice
			var count int64
			require.NoError(t, logDB.Model(&model.Log{}).Count(&count).Error)
			assert.Zero(t, count, "disabling optional usage logs must not disable financial collection")
			require.NoError(t, db.Model(&model.ModelProfitBillingRecord{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30})
			require.NoError(t, err)
			assert.Equal(t, "collected", report.FundingCollectionStatus)
			assert.Equal(t, "fifo", report.FundingPolicy)
			assert.Equal(t, "attributed", report.Summary.FundingStatus)
			require.NotNil(t, report.Summary.CashRevenueCNY)
			assert.InDelta(t, 4, *report.Summary.CashRevenueCNY, 1e-12, "paid face value 5 is only 4 actual cash at the frozen 80/100 ratio")
			assert.InDelta(t, 5, report.Summary.ConfirmedPaidConsumedCNY, 1e-12)
			assert.InDelta(t, 10, report.Summary.ConfirmedGiftConsumedCNY, 1e-12)
			require.NotNil(t, report.Summary.CashProfitCNY)
			assert.InDelta(t, 2, *report.Summary.CashProfitCNY, 1e-12, "gift-funded use still bears its full procurement cost")
			assert.EqualValues(t, 1, report.Summary.Requests)
			assert.EqualValues(t, 1, report.Summary.BillingEvents)

			common.LogConsumeEnabled = true
			model.RecordConsumeLog(c, user.Id, params)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30})
			require.NoError(t, err)
			assert.EqualValues(t, 1, report.Summary.BillingEvents, "the usage-log projection and main record are one bill")
			require.NoError(t, logDB.Model(&model.Log{}).Where("request_id = ?", "funded-api-request").Update("created_at", now+2).Error)
			replayedWindow, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now + 2, End: now + 3})
			require.NoError(t, err)
			assert.Zero(t, replayedWindow.Summary.BillingEvents, "a later usage-log replay is not a new financial event in that reporting window")
			require.NoError(t, logDB.Where("type = ?", model.LogTypeConsume).Delete(&model.Log{}).Error)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30})
			require.NoError(t, err)
			assert.InDelta(t, 4, *report.Summary.CashRevenueCNY, 1e-12, "usage-log retention cannot erase new financial evidence")

			legacy := model.Log{UserId: user.Id, CreatedAt: now, Type: model.LogTypeConsume, ChannelId: 19,
				ModelName: "funded-image", Quota: 100, RequestId: "legacy-unknown", Other: `{}`}
			require.NoError(t, logDB.Create(&legacy).Error)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30})
			require.NoError(t, err)
			assert.Equal(t, "partial", report.Summary.FundingStatus)
			assert.Nil(t, report.Summary.CashRevenueCNY, "the confirmed part must not masquerade as the entire period")
			assert.Nil(t, report.Summary.CashProfitCNY)

			day := (now+8*3600)/86400*86400 - 8*3600
			require.NoError(t, model.PrepareWalletFundingRequest(user.Id, "pending-trend", "relay", "pending-trend", 19))
			trendStart, err := model.ApplyWalletFundingTarget(user.Id, "pending-trend", "relay", 100, true)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.WalletFundingEvent{}).Where("id = ?", trendStart.EventID).Update("created_at", day-86400+100).Error)
			trendChange, err := model.ApplyWalletFundingTarget(user.Id, "pending-trend", "relay", 80, false)
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.WalletFundingEvent{}).Where("id = ?", trendChange.EventID).Update("created_at", day+100).Error)
			trendReport, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: day - 86400, End: day + 200, ModelName: "pending-trend"})
			require.NoError(t, err)
			require.Len(t, trendReport.Trend, 2)
			assert.InDelta(t, 10, trendReport.Trend[0].RevenueCNY, 1e-12)
			assert.InDelta(t, -2, trendReport.Trend[1].RevenueCNY, 1e-12, "pending adjustments retain their own accounting day")
			assert.InDelta(t, 8, trendReport.Summary.RevenueCNY, 1e-12)
			assert.EqualValues(t, 1, trendReport.Summary.Requests)
			assert.Nil(t, trendReport.Summary.CashProfitCNY)
			assert.InDelta(t, 4, report.Summary.ConfirmedCashRevenueCNY, 1e-12)
			assert.InDelta(t, 10, report.Summary.UnknownFundingConsumedCNY, 1e-12)
			assert.EqualValues(t, 1, report.Summary.FundingUnknownEvents)
			assert.InDelta(t, .5, report.Summary.FundingCoverage, 1e-12)

			// A queued task has a durable funding split, but its reservation is
			// still not confirmed consumption or earned cash income.
			pendingTask := model.Task{TaskID: "pending-funded-task", UserId: user.Id, ChannelId: 19, Status: model.TaskStatusNotStart, SubmitTime: now, Quota: 25}
			require.NoError(t, db.Create(&pendingTask).Error)
			require.NoError(t, model.PrepareWalletFundingRequest(user.Id, "pending-funded-request", "relay", "pending-funded-image", 19))
			pendingFunding, err := model.ApplyWalletFundingTarget(user.Id, "pending-funded-request", "relay", 25, true)
			require.NoError(t, err)
			c.Set("wallet_funding", &pendingFunding.Total)
			c.Set("wallet_funding_request_id", "pending-funded-request")
			c.Set(common.RequestIdKey, "pending-funded-api")
			pendingOther := model.NewLogOther()
			pendingOther.SetPublic("task_id", pendingTask.TaskID)
			pendingOther.SetPublic("is_task", true)
			model.RecordConsumeLog(c, user.Id, model.RecordConsumeLogParams{ChannelId: 19, ModelName: "pending-funded-image", Quota: 25, Other: pendingOther})
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "pending-funded-image"})
			require.NoError(t, err)
			assert.EqualValues(t, 1, report.Summary.PendingBillingEvents)
			assert.Zero(t, report.Summary.ConfirmedCashRevenueCNY)
			assert.Zero(t, report.Summary.ConfirmedPaidConsumedCNY)
			assert.Zero(t, report.Summary.ConfirmedGiftConsumedCNY)
			assert.Nil(t, report.Summary.CashProfitCNY)

			task := model.Task{TaskID: "funded-task", UserId: user.Id, ChannelId: 19, Status: model.TaskStatusSuccess, SubmitTime: now, Quota: 100}
			require.NoError(t, db.Create(&task).Error)
			fundedTask, err := model.ApplyWalletFundingTarget(user.Id, "funded-task-request", "relay", 100, true)
			require.NoError(t, err)
			c.Set("wallet_funding", &fundedTask.Total)
			c.Set("wallet_funding_request_id", "funded-task-request")
			c.Set(common.RequestIdKey, "funded-task-api")
			taskOther := model.NewLogOther()
			taskOther.SetPublic("task_id", task.TaskID)
			taskOther.SetPublic("is_task", true)
			model.RecordConsumeLog(c, user.Id, model.RecordConsumeLogParams{ChannelId: 19, ModelName: "task-image", Quota: 100, Other: taskOther})
			config, err := model.GetModelProfitCostRules(ctx)
			require.NoError(t, err)
			config.Rules = append(config.Rules, model.ModelProfitCostRule{ChannelID: 19, ModelName: "task-image", EffectiveFrom: 1, Kind: "request", UnitCostCNY: &cost, Source: "agreed procurement price", FailureRefunded: true})
			_, err = model.SaveModelProfitCostRules(ctx, config)
			require.NoError(t, err)
			partial, err := model.ApplyWalletFundingTarget(user.Id, "funded-task-request", "relay", 40, false)
			require.NoError(t, err)
			partialOther := model.NewLogOther()
			partialOther.SetPublic("task_id", task.TaskID)
			partialOther.SetPublic("is_task", true)
			partialOther.SetPublic("actual_quota", 40)
			partialOther.SetPublic("pre_consumed_quota", 100)
			partialOther.SetAdmin("funding", &partial.Delta)
			partialOther.SetAdmin("funding_signed", true)
			model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: user.Id, LogType: model.LogTypeRefund, ChannelId: 19, ModelName: "task-image", Quota: 60, Other: partialOther})
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "task-image"})
			require.NoError(t, err)
			require.NotNil(t, report.Summary.CashRevenueCNY)
			assert.InDelta(t, 3.2, *report.Summary.CashRevenueCNY, 1e-12)
			require.NotNil(t, report.Summary.CashProfitCNY)
			assert.InDelta(t, 1.2, *report.Summary.CashProfitCNY, 1e-12)

			fullRefund, err := model.ApplyWalletFundingTarget(user.Id, "funded-task-request", "relay", 0, false)
			require.NoError(t, err)
			require.NoError(t, db.Model(&task).Updates(map[string]any{"status": model.TaskStatusFailure, "quota": 0}).Error)
			refundOther := model.NewLogOther()
			refundOther.SetPublic("task_id", task.TaskID)
			refundOther.SetPublic("is_task", true)
			refundOther.SetAdmin("funding", &fullRefund.Delta)
			refundOther.SetAdmin("funding_signed", true)
			refundParams := model.RecordTaskBillingLogParams{UserId: user.Id, LogType: model.LogTypeRefund, ChannelId: 19, ModelName: "task-image", Quota: 40, Other: refundOther}
			model.RecordTaskBillingLog(refundParams)
			model.RecordTaskBillingLog(refundParams)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "task-image"})
			require.NoError(t, err)
			require.NotNil(t, report.Summary.CashProfitCNY)
			assert.Zero(t, *report.Summary.CashProfitCNY)
			assert.EqualValues(t, 3, report.Summary.BillingEvents, "the repeated refund log projection is one financial event")
			require.NoError(t, db.Model(&model.ModelProfitBillingRecord{}).Where("model_name = ? AND type = ? AND quota = ?", "task-image", model.LogTypeRefund, 40).Update("created_at", now+10).Error)
			require.NoError(t, logDB.Model(&model.Log{}).Where("model_name = ? AND type = ? AND quota = ?", "task-image", model.LogTypeRefund, 40).Update("created_at", now+10).Error)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now + 10, End: now + 30, ModelName: "task-image"})
			require.NoError(t, err)
			require.NotNil(t, report.Summary.CashProfitCNY)
			assert.InDelta(t, -1.2, *report.Summary.CashProfitCNY, 1e-12, "cross-period refunds reverse original cash and procurement cost")
			assert.InDelta(t, -3.2, *report.Summary.CashRevenueCNY, 1e-12)
			assert.InDelta(t, -2, *report.Summary.UpstreamCostCNY, 1e-12)
			assert.Zero(t, report.Summary.Requests)

			require.NoError(t, model.PrepareWalletFundingRequest(user.Id, "missing-result", "relay", "orphan-image", 19))
			_, err = model.ApplyWalletFundingTarget(user.Id, "missing-result", "relay", 25, true)
			require.NoError(t, err)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "orphan-image"})
			require.NoError(t, err)
			assert.EqualValues(t, 1, report.Summary.PendingBillingEvents, "a committed reservation without a financial result must remain visible")
			assert.Nil(t, report.Summary.CashProfitCNY)
			assert.Zero(t, report.Summary.ConfirmedCashRevenueCNY, "a pending reservation is not earned revenue")
			_, err = model.ApplyWalletFundingTarget(user.Id, "missing-result", "relay", 0, false)
			require.NoError(t, err)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "orphan-image"})
			require.NoError(t, err)
			assert.Zero(t, report.Summary.BillingEvents, "an entirely cancelled reservation with no recognized income is not consumption")

			require.NoError(t, model.PrepareWalletFundingRequest(user.Id, "refund-gap", "relay", "funded-image", 19))
			gap, err := model.ApplyWalletFundingTarget(user.Id, "refund-gap", "relay", 25, true)
			require.NoError(t, err)
			c.Set("wallet_funding", &gap.Total)
			c.Set("wallet_funding_request_id", "refund-gap")
			c.Set(common.RequestIdKey, "refund-gap-api")
			model.RecordConsumeLog(c, user.Id, model.RecordConsumeLogParams{ChannelId: 19, ModelName: "funded-image", Quota: 25, Other: model.NewLogOther()})
			_, err = model.ApplyWalletFundingTarget(user.Id, "refund-gap", "relay", 0, false)
			require.NoError(t, err)
			report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: now + 30, ModelName: "funded-image"})
			require.NoError(t, err)
			assert.EqualValues(t, 1, report.Summary.PendingBillingEvents, "a committed full refund cannot disappear just because the current target is zero")
			assert.Nil(t, report.Summary.CashProfitCNY)

			// A zero wallet charge can still incur procurement cost. A zero
			// subscription debit cannot establish the missing allocation of
			// subscription receipts to individual models.
			config, err = model.GetModelProfitCostRules(ctx)
			require.NoError(t, err)
			for _, name := range []string{"zero-wallet", "zero-subscription"} {
				config.Rules = append(config.Rules, model.ModelProfitCostRule{ChannelID: 19, ModelName: name, EffectiveFrom: 1, Kind: "request", UnitCostCNY: &cost, Source: "agreed procurement price"})
			}
			_, err = model.SaveModelProfitCostRules(ctx, config)
			require.NoError(t, err)
			zero, err := model.ApplyWalletFundingTarget(user.Id, "zero-wallet", "relay", 0, false)
			require.NoError(t, err)
			zeroContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			zeroContext.Set("wallet_funding", &zero.Total)
			model.RecordConsumeLog(zeroContext, user.Id, model.RecordConsumeLogParams{ChannelId: 19, ModelName: "zero-wallet", Other: model.NewLogOther()})
			zeroReport, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: common.GetTimestamp() + 1, ModelName: "zero-wallet"})
			require.NoError(t, err)
			require.NotNil(t, zeroReport.Summary.CashProfitCNY)
			assert.InDelta(t, -2, *zeroReport.Summary.CashProfitCNY, 1e-12)
			subscriptionContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			subscriptionOther := model.NewLogOther()
			subscriptionOther.SetPublic("billing_source", "subscription")
			model.RecordConsumeLog(subscriptionContext, user.Id, model.RecordConsumeLogParams{ChannelId: 19, ModelName: "zero-subscription", Other: subscriptionOther})
			subscriptionReport, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: now - 1, End: common.GetTimestamp() + 1, ModelName: "zero-subscription"})
			require.NoError(t, err)
			assert.Nil(t, subscriptionReport.Summary.CashRevenueCNY)
			assert.Nil(t, subscriptionReport.Summary.CashProfitCNY)
		})
	}
}

func TestModelProfitAccountingDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, kind)
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_logs_%t", separate), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB := db
			if separate {
				logDB, _ = newAuditTestDatabase(t, kind, dsn)
			}
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, logDB, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Task{}, &model.ModelProfitBillingRecord{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			versionQuery := "SELECT version()"
			if kind == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)
			oldQuota, oldRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
			common.QuotaPerUnit, operation_setting.USDExchangeRate = 100, 10
			t.Cleanup(func() { common.QuotaPerUnit, operation_setting.USDExchangeRate = oldQuota, oldRate })
			ctx := context.Background()
			two, one, zero, half := 2.0, 1.0, 0.0, 0.5
			until := int64(200)
			config := model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{
				{ChannelID: 19, ModelName: "video", EffectiveFrom: 50, Kind: "request", UnitCostCNY: &two, Source: "verified vendor tariff", FailureRefunded: true},
				{ChannelID: 19, ModelName: "seconds", EffectiveFrom: 50, Kind: "second", UnitCostCNY: &half, Source: "verified vendor tariff"},
				{ChannelID: 18, ModelName: "image", EffectiveFrom: 50, Kind: "image", UnitCostCNY: &one, Source: "published estimate"},
				{ChannelID: 12, ModelName: "deepseek-flash", EffectiveFrom: 50, Kind: "token", InputCostPerMillionCNY: &one, CacheCostPerMillionCNY: &half, OutputCostPerMillionCNY: &two, DeepSeekPeakMultiplier: &two, Source: "official tariff"},
				{ChannelID: 14, ModelName: "free", EffectiveFrom: 50, Kind: "request", UnitCostCNY: &zero, Source: "explicit verified zero"},
				{ChannelID: 16, ModelName: "no-refund-proof", EffectiveFrom: 50, Kind: "request", UnitCostCNY: &two, Source: "unverified failure settlement"},
				{ChannelID: 14, ModelName: "expires", EffectiveFrom: 0, Kind: "request", UnitCostCNY: &one, Source: "old rule must not resume"},
				{ChannelID: 14, ModelName: "expires", EffectiveFrom: 50, EffectiveUntil: &until, Kind: "request", UnitCostCNY: &two, Source: "expires when tariff changes"},
			}}
			saved, err := model.SaveModelProfitCostRules(ctx, config)
			require.NoError(t, err)
			assert.EqualValues(t, 1, saved.Revision)
			_, err = model.SaveModelProfitCostRules(ctx, config)
			assert.ErrorIs(t, err, model.ErrModelProfitConflict)
			mutated := saved
			mutated.Rules = append([]model.ModelProfitCostRule{}, saved.Rules...)
			mutated.Rules[0].UnitCostCNY = &one
			_, err = model.SaveModelProfitCostRules(ctx, mutated)
			assert.ErrorIs(t, err, model.ErrModelProfitHistory)
			backdated := saved
			backdated.Rules = append(append([]model.ModelProfitCostRule{}, saved.Rules...), model.ModelProfitCostRule{ChannelID: 19, ModelName: "video", EffectiveFrom: 40, Kind: "request", UnitCostCNY: &one, Source: "backdated"})
			_, err = model.SaveModelProfitCostRules(ctx, backdated)
			assert.ErrorIs(t, err, model.ErrModelProfitHistory)
			newVersion := saved
			newVersion.Rules = append(append([]model.ModelProfitCostRule{}, saved.Rules...), model.ModelProfitCostRule{ChannelID: 19, ModelName: "video", EffectiveFrom: 200, Kind: "request", UnitCostCNY: &one, Source: "new tariff", FailureRefunded: true})
			saved, err = model.SaveModelProfitCostRules(ctx, newVersion)
			require.NoError(t, err)
			assert.EqualValues(t, 2, saved.Revision)
			tasks := []model.Task{
				{TaskID: "task_good", UserId: 1, ChannelId: 19, SubmitTime: 100, Status: model.TaskStatusSuccess},
				{TaskID: "task_fail", UserId: 1, ChannelId: 19, SubmitTime: 100, Status: model.TaskStatusFailure},
				{TaskID: "task_pending", UserId: 1, ChannelId: 19, SubmitTime: 100, Status: model.TaskStatusInProgress},
				{TaskID: "task_seconds", UserId: 1, ChannelId: 19, SubmitTime: 100, Status: model.TaskStatusSuccess},
				{TaskID: "task_unverified", UserId: 1, ChannelId: 16, SubmitTime: 100, Status: model.TaskStatusFailure},
			}
			require.NoError(t, db.Create(&tasks).Error)
			logs := []model.Log{
				{CreatedAt: 100, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 50, RequestId: "good", Other: `{"task_id":"task_good","is_task":true,"usage_facts":{"seconds":10,"mode":"normal"}}`},
				{CreatedAt: 210, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 10, RequestId: "good-delta", Other: `{"task_id":"task_good","is_task":true,"actual_quota":60,"pre_consumed_quota":50,"usage_facts":{"seconds":10}}`},
				{CreatedAt: 110, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 50, RequestId: "fail", Other: `{"task_id":"task_fail","is_task":true}`},
				{CreatedAt: 220, UserId: 1, ChannelId: 19, Type: model.LogTypeRefund, ModelName: "video", Quota: 50, RequestId: "fail-refund", Other: `{ "task_id" : "task_fail", "is_task":true}`},
				{CreatedAt: 120, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 50, RequestId: "pending", Other: `{"task_id":"task_pending","is_task":true}`},
				{CreatedAt: 130, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "seconds", Quota: 100, RequestId: "seconds", Other: `{"task_id":"task_seconds","is_task":true,"usage_facts":{"seconds":10}}`},
				{CreatedAt: 230, UserId: 1, ChannelId: 19, Type: model.LogTypeRefund, ModelName: "seconds", Quota: 20, RequestId: "seconds-delta", Other: `{"task_id":"task_seconds","is_task":true,"actual_quota":80,"pre_consumed_quota":100,"usage_facts":{"seconds":8}}`},
				{CreatedAt: 140, UserId: 1, ChannelId: 16, Type: model.LogTypeConsume, ModelName: "no-refund-proof", Quota: 50, RequestId: "unverified", Other: `{"task_id":"task_unverified","is_task":true}`},
				{CreatedAt: 240, UserId: 1, ChannelId: 16, Type: model.LogTypeRefund, ModelName: "no-refund-proof", Quota: 50, RequestId: "unverified-refund", Other: `{"task_id":"task_unverified","is_task":true}`},
				{CreatedAt: 150, UserId: 1, ChannelId: 18, Type: model.LogTypeConsume, ModelName: "image", Quota: 30, RequestId: "image", Other: `{"image_count":2}`},
				{CreatedAt: 151, UserId: 1, ChannelId: 18, Type: model.LogTypeConsume, ModelName: "image", Quota: 30, RequestId: "missing-quantity", Other: `{}`},
				{CreatedAt: 160, UserId: 1, ChannelId: 12, Type: model.LogTypeConsume, ModelName: "deepseek-flash", Quota: 50, PromptTokens: 1000000, CompletionTokens: 1000000, RequestId: "tokens", Other: `{"cache_tokens":200000,"matched_tier":"peak"}`},
				{CreatedAt: 161, UserId: 1, ChannelId: 12, Type: model.LogTypeConsume, ModelName: "deepseek-flash", Quota: 50, PromptTokens: 1000000, RequestId: "unknown-tier", Other: `{"cache_tokens":0}`},
				{CreatedAt: 170, UserId: 1, ChannelId: 14, Type: model.LogTypeConsume, ModelName: "free", Quota: 0, RequestId: "zero", Other: `{}`},
				{CreatedAt: 175, UserId: 1, ChannelId: 9, Type: model.LogTypeConsume, ModelName: "unknown-model", Quota: 50, RequestId: "unknown", Other: `{}`},
				{CreatedAt: 180, UserId: 1, ChannelId: 19, Type: model.LogTypeTopup, ModelName: "video", Quota: 999999, RequestId: "topup"},
				{CreatedAt: 180, UserId: 1, ChannelId: 19, Type: model.LogTypeSystem, ModelName: "video", Quota: 999999, RequestId: "grant"},
				{CreatedAt: 180, UserId: 1, ChannelId: 19, Type: model.LogTypeError, ModelName: "video", Quota: 999999, RequestId: "error"},
				{CreatedAt: 300, UserId: 1, ChannelId: 9, Type: model.LogTypeConsume, ModelName: "excluded-boundary", Quota: 999999, RequestId: "boundary"},
			}
			require.NoError(t, logDB.Create(&logs).Error)
			t.Run("net_ledger_and_coverage", func(t *testing.T) {
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 90, End: 300})
				require.NoError(t, err)
				assert.EqualValues(t, 11, report.Summary.Requests)
				assert.EqualValues(t, 15, report.Summary.BillingEvents)
				assert.InDelta(t, 40, report.Summary.RevenueCNY, 1e-9)
				assert.InDelta(t, 12, report.Summary.RefundCNY, 1e-9)
				assert.EqualValues(t, 2, report.CostRulesRevision)
				assert.True(t, report.HistoricalConversionApproximate)
				assert.Equal(t, 10.0, report.CurrentRate)
				rows := map[string]model.ModelProfitRow{}
				for _, row := range report.Rows {
					rows[row.ModelName] = row
				}
				video := rows["video"]
				assert.EqualValues(t, 3, video.Requests)
				assert.EqualValues(t, 1, video.PendingRequests)
				require.NotNil(t, video.UpstreamCostCNY)
				assert.InDelta(t, 2, *video.UpstreamCostCNY, 1e-9)
				assert.InDelta(t, 6, video.KnownCostRevenueCNY, 1e-9)
				require.NotNil(t, video.GrossProfitCNY)
				assert.InDelta(t, 4, *video.GrossProfitCNY, 1e-9)
				assert.InDelta(t, 4, *rows["seconds"].UpstreamCostCNY, 1e-9)
				assert.Nil(t, rows["no-refund-proof"].UpstreamCostCNY, "a customer refund does not prove the supplier refunded")
				assert.Nil(t, rows["unknown-model"].GrossProfitCNY, "unknown costs must not turn into zero")
				assert.EqualValues(t, 1, rows["image"].KnownCostRequests)
				assert.EqualValues(t, 1, rows["image"].UnknownCostRequests)
				assert.InDelta(t, 5.8, *rows["deepseek-flash"].UpstreamCostCNY, 1e-9)
				require.NotNil(t, rows["free"].UpstreamCostCNY)
				assert.Zero(t, *rows["free"].UpstreamCostCNY, "explicit zero is known")
			})
			t.Run("cash_profit_requires_funding_evidence", func(t *testing.T) {
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 90, End: 300})
				require.NoError(t, err)
				encoded, err := common.Marshal(report)
				require.NoError(t, err)
				var body map[string]any
				require.NoError(t, common.Unmarshal(encoded, &body))
				assert.Equal(t, "collected", body["funding_collection_status"])
				assert.Equal(t, "cash_attributed_revenue_minus_full_upstream_cost", body["cash_profit_basis"])
				summary := body["summary"].(map[string]any)
				for _, key := range []string{"cash_revenue_cny", "gift_consumed_cny", "cash_profit_cny"} {
					assert.Contains(t, summary, key)
					assert.Nil(t, summary[key], "unattributed funds are not zero cash or gift consumption")
				}
				assert.Equal(t, float64(15), summary["funding_unknown_events"])
				assert.Equal(t, float64(0), summary["funding_coverage"])
				rows := make(map[string]map[string]any)
				for _, value := range body["rows"].([]any) {
					row := value.(map[string]any)
					rows[row["model_name"].(string)] = row
					assert.Equal(t, "request", row["average_price_unit"])
					assert.Nil(t, row["cash_profit_cny"])
				}
				assert.Equal(t, float64(8), rows["seconds"]["customer_average_price_cny"])
				assert.Equal(t, float64(4), rows["seconds"]["upstream_average_cost_cny"])
				assert.Equal(t, float64(3), rows["image"]["customer_average_price_cny"])
				assert.Nil(t, rows["image"]["upstream_average_cost_cny"], "partial costs must not become a whole-row unit price")
				assert.Nil(t, rows["video"]["customer_average_price_cny"], "pending reservations are not final unit prices")
				assert.Nil(t, rows["video"]["upstream_average_cost_cny"])
				assert.Equal(t, float64(0), rows["free"]["customer_average_price_cny"], "an explicit zero price remains known")
				assert.Equal(t, float64(0), rows["free"]["upstream_average_cost_cny"])
				refundOnly, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 200, End: 300})
				require.NoError(t, err)
				encoded, err = common.Marshal(refundOnly)
				require.NoError(t, err)
				require.NoError(t, common.Unmarshal(encoded, &body))
				for _, value := range body["rows"].([]any) {
					row := value.(map[string]any)
					assert.Nil(t, row["customer_average_price_cny"], "a refund-only period has no request denominator")
					assert.Nil(t, row["upstream_average_cost_cny"])
				}
			})
			t.Run("cross_period_refund_and_no_duplicate_calls", func(t *testing.T) {
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 200, End: 300, ModelName: "video"})
				require.NoError(t, err)
				assert.Zero(t, report.Summary.Requests)
				assert.InDelta(t, -4, report.Summary.RevenueCNY, 1e-9)
				require.NotNil(t, report.Summary.UpstreamCostCNY)
				assert.InDelta(t, -2, *report.Summary.UpstreamCostCNY, 1e-9, "reverse the old rate, not the new tariff")
				assert.InDelta(t, -2, *report.Summary.GrossProfitCNY, 1e-9)
			})
			t.Run("invalid_range_cancelled_context_and_read_only", func(t *testing.T) {
				_, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 200, End: 100})
				assert.ErrorIs(t, err, model.ErrModelProfitParameters)
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				_, err = model.GetModelProfits(cancelled, model.ModelProfitFilter{Start: 90, End: 300})
				require.Error(t, err)
				var after []model.Task
				require.NoError(t, db.Order("id").Find(&after).Error)
				require.Len(t, after, len(tasks))
				for i := range after {
					assert.Equal(t, tasks[i].Status, after[i].Status)
				}
			})
			t.Run("generic_options_cannot_replace_price_history", func(t *testing.T) {
				variants := []string{model.ModelProfitCostRulesKey, strings.ToLower(model.ModelProfitCostRulesKey), strings.ToUpper(model.ModelProfitCostRulesKey)}
				if kind == "mysql" {
					variants = append(variants, model.ModelProfitCostRulesKey+" ", "MódelProfitCostRules")
				}
				for _, key := range variants {
					assert.ErrorIs(t, model.UpdateOption(key, `{}`), model.ErrModelProfitCostRulesAPI)
					assert.ErrorIs(t, model.UpdateOptionsBulk(map[string]string{key: `{}`, "UnrelatedKey": "must not be written"}), model.ErrModelProfitCostRulesAPI)
				}
				current, err := model.GetModelProfitCostRules(ctx)
				require.NoError(t, err)
				assert.Equal(t, saved, current)
			})
			t.Run("same_vendor_id_across_task_batch_boundary", func(t *testing.T) {
				collisionTasks := make([]model.Task, 201)
				collisionLogs := make([]model.Log, 201)
				for i := range 201 {
					collisionTasks[i] = model.Task{TaskID: "vendor-reused-id", UserId: 100 + i, ChannelId: 19, SubmitTime: 400, Status: model.TaskStatusSuccess}
					collisionLogs[i] = model.Log{CreatedAt: 400, UserId: 100 + i, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "collisions", Quota: 1, RequestId: fmt.Sprintf("collision-%d", i), Other: `{"task_id":"vendor-reused-id","is_task":true}`}
				}
				require.NoError(t, db.Create(&collisionTasks).Error)
				require.NoError(t, logDB.Create(&collisionLogs).Error)
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 400, End: 500})
				require.NoError(t, err)
				assert.EqualValues(t, 201, report.Summary.Requests)
				assert.EqualValues(t, 201, report.Summary.UnknownCostRequests)
			})
			t.Run("negative_original_cannot_create_known_refund_cost", func(t *testing.T) {
				task := model.Task{TaskID: "negative-original", UserId: 5000, ChannelId: 19, SubmitTime: 100, Status: model.TaskStatusFailure}
				require.NoError(t, db.Create(&task).Error)
				for _, log := range []model.Log{{CreatedAt: 600, UserId: 5000, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: -50, RequestId: "negative-original", Other: `{"task_id":"negative-original","is_task":true}`}, {CreatedAt: 610, UserId: 5000, ChannelId: 19, Type: model.LogTypeRefund, ModelName: "video", Quota: 50, RequestId: "negative-refund", Other: `{"task_id":"negative-original","is_task":true}`}} {
					require.NoError(t, logDB.Create(&log).Error)
				}
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 600, End: 700})
				require.NoError(t, err)
				assert.Zero(t, report.Summary.KnownCostEvents)
				assert.Nil(t, report.Summary.UpstreamCostCNY)
				require.Len(t, report.Rows, 1)
				assert.Nil(t, report.Rows[0].CustomerAveragePriceCNY)
				assert.Nil(t, report.Rows[0].UpstreamAverageCostCNY)
			})
			t.Run("expired_rule_does_not_resume_old_tariff", func(t *testing.T) {
				for _, at := range []int64{199, 200} {
					require.NoError(t, logDB.Create(&model.Log{CreatedAt: at, UserId: 1, ChannelId: 14, Type: model.LogTypeConsume, ModelName: "expires", Quota: 30, RequestId: fmt.Sprintf("expires-%d", at), Other: `{}`}).Error)
				}
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 199, End: 201, ModelName: "expires"})
				require.NoError(t, err)
				assert.EqualValues(t, 1, report.Summary.KnownCostRequests)
				assert.EqualValues(t, 1, report.Summary.UnknownCostRequests)
				require.NotNil(t, report.Summary.UpstreamCostCNY)
				assert.Equal(t, 2.0, *report.Summary.UpstreamCostCNY)
			})
			t.Run("period_unit_averages_require_final_billing_events", func(t *testing.T) {
				for i, quota := range []int{20, 60} {
					require.NoError(t, logDB.Create(&model.Log{CreatedAt: 800 + int64(i), UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: quota, RequestId: fmt.Sprintf("average-%d", i), Other: `{}`}).Error)
				}
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 800, End: 900})
				require.NoError(t, err)
				require.Len(t, report.Rows, 1)
				require.NotNil(t, report.Rows[0].CustomerAveragePriceCNY)
				assert.Equal(t, 4.0, *report.Rows[0].CustomerAveragePriceCNY)
				require.NotNil(t, report.Rows[0].UpstreamAverageCostCNY)
				assert.Equal(t, 1.0, *report.Rows[0].UpstreamAverageCostCNY)
				require.NoError(t, db.Create(&model.Task{TaskID: "unit-price-pending", UserId: 1, ChannelId: 19, SubmitTime: 790, Status: model.TaskStatusInProgress}).Error)
				for _, log := range []model.Log{
					{CreatedAt: 790, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 5, RequestId: "pending-original", Other: `{"task_id":"unit-price-pending","is_task":true}`},
					{CreatedAt: 820, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 0, RequestId: "pending-zero-delta", Other: `{"task_id":"unit-price-pending","is_task":true,"actual_quota":5,"pre_consumed_quota":5}`},
				} {
					require.NoError(t, logDB.Create(&log).Error)
				}
				report, err = model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 800, End: 900})
				require.NoError(t, err)
				require.Len(t, report.Rows, 1)
				assert.Zero(t, report.Rows[0].PendingRequests, "the pending original call is outside the period")
				assert.EqualValues(t, 1, report.Rows[0].PendingBillingEvents)
				assert.Zero(t, report.Rows[0].PendingRevenueCNY, "zero-delta reservations are still pending")
				assert.Nil(t, report.Rows[0].CustomerAveragePriceCNY)
				assert.Nil(t, report.Rows[0].UpstreamAverageCostCNY)
			})
			t.Run("unresolved_task_billing_cannot_be_a_final_unit_price", func(t *testing.T) {
				for i, other := range []string{`{"is_task":true}`, `{"task_id":"missing-task","is_task":true}`, `invalid-json`} {
					at := int64(1000 + i)
					require.NoError(t, logDB.Create(&model.Log{CreatedAt: at, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 50, RequestId: fmt.Sprintf("unknown-finality-%d", i), Other: other}).Error)
					report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: at, End: at + 1})
					require.NoError(t, err)
					require.Len(t, report.Rows, 1)
					assert.EqualValues(t, 1, report.Rows[0].Requests, "legacy call counts remain compatible")
					assert.Zero(t, report.Rows[0].PendingRequests, "unknown task state does not mean known in-progress")
					assert.Nil(t, report.Rows[0].CustomerAveragePriceCNY, other)
					assert.Nil(t, report.Rows[0].UpstreamAverageCostCNY, other)
				}
				require.NoError(t, db.Create(&model.Task{TaskID: "missing-original-charge", UserId: 1, ChannelId: 19, SubmitTime: 1100, Status: model.TaskStatusSuccess}).Error)
				require.NoError(t, logDB.Create(&[]model.Log{
					{CreatedAt: 1100, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 10, RequestId: "orphan-settlement", Other: `{"task_id":"missing-original-charge","is_task":true,"actual_quota":50,"pre_consumed_quota":40}`},
					{CreatedAt: 1100, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "video", Quota: 50, RequestId: "complete-other-call", Other: `{}`},
				}).Error)
				report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: 1100, End: 1101})
				require.NoError(t, err)
				require.Len(t, report.Rows, 1)
				assert.EqualValues(t, 1, report.Rows[0].Requests)
				assert.Nil(t, report.Rows[0].CustomerAveragePriceCNY, "one complete call cannot supply a denominator for an orphan settlement")
			})
		})
	}
}

func TestModelProfitDashboardDatabase(t *testing.T) {
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, kind)
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate_logs_%t", separate), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB := db
			if separate {
				logDB, _ = newAuditTestDatabase(t, kind, dsn)
			}
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, logDB, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Task{}, &model.ModelProfitBillingRecord{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			oldQuota, oldRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
			common.QuotaPerUnit, operation_setting.USDExchangeRate = 100, 10
			t.Cleanup(func() { common.QuotaPerUnit, operation_setting.USDExchangeRate = oldQuota, oldRate })
			zone := time.FixedZone("Asia/Shanghai", 8*60*60)
			day := time.Date(2026, 10, 21, 0, 0, 0, 0, zone).Unix()
			start, end := day+23*3600+30*60, day+3*86400
			charge, refund := day+23*3600+45*60, day+86400+15*60
			one, two := 1.0, 2.0
			ctx := context.Background()
			_, err := model.SaveModelProfitCostRules(ctx, model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{
				{ChannelID: 19, ModelName: "retired-video", EffectiveFrom: day, Kind: "request", UnitCostCNY: &two, Source: "verified refunded tariff", FailureRefunded: true},
				{ChannelID: 12, ModelName: "known-text", EffectiveFrom: day, Kind: "request", UnitCostCNY: &one, Source: "verified tariff"},
			}})
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.Task{TaskID: "cross-day-refund", UserId: 1, ChannelId: 19, SubmitTime: charge, Status: model.TaskStatusFailure}).Error)
			logs := []model.Log{
				{CreatedAt: charge, UserId: 1, ChannelId: 19, Type: model.LogTypeConsume, ModelName: "retired-video", Quota: 50, RequestId: "charge", Other: `{"task_id":"cross-day-refund","is_task":true}`},
				{CreatedAt: refund, UserId: 1, ChannelId: 19, Type: model.LogTypeRefund, ModelName: "retired-video", Quota: 50, RequestId: "refund", Other: `{"task_id":"cross-day-refund","is_task":true}`},
				{CreatedAt: refund, UserId: 1, ChannelId: 12, Type: model.LogTypeConsume, ModelName: "known-text", Quota: 30, RequestId: "known", Other: `{}`},
				{CreatedAt: refund, UserId: 1, ChannelId: 9, Type: model.LogTypeConsume, ModelName: "unknown-text", Quota: 20, RequestId: "unknown", Other: `{}`},
				{CreatedAt: start - 1, Type: model.LogTypeConsume, ModelName: "before-start", Quota: 10000, Other: `{}`},
				{CreatedAt: end, Type: model.LogTypeConsume, ModelName: "at-end", Quota: 10000, Other: `{}`},
				{CreatedAt: refund, Type: model.LogTypeTopup, ModelName: "not-a-consumption", Quota: 10000},
			}
			require.NoError(t, logDB.Create(&logs).Error)
			report, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: start, End: end})
			require.NoError(t, err)
			assert.Equal(t, []string{"known-text", "retired-video", "unknown-text"}, report.ModelOptions)
			assert.Equal(t, "day", report.TrendGranularity)
			assert.Equal(t, "Asia/Shanghai", report.Timezone)
			require.Len(t, report.Trend, 3)
			assert.EqualValues(t, 3, report.Summary.Requests)
			assert.InDelta(t, 5, report.Summary.RevenueCNY, 1e-9)
			assert.InDelta(t, 3, report.Summary.KnownCostRevenueCNY, 1e-9)
			require.NotNil(t, report.Summary.UpstreamCostCNY)
			require.NotNil(t, report.Summary.GrossProfitCNY)
			assert.InDelta(t, 1, *report.Summary.UpstreamCostCNY, 1e-9)
			assert.InDelta(t, 2, *report.Summary.GrossProfitCNY, 1e-9, "unknown revenue must not be counted as profit")
			assert.Equal(t, []int64{day, day + 86400, day + 2*86400}, []int64{report.Trend[0].BucketStart, report.Trend[1].BucketStart, report.Trend[2].BucketStart})
			assert.InDelta(t, 5, report.Trend[0].RevenueCNY, 1e-9)
			require.NotNil(t, report.Trend[0].UpstreamCostCNY)
			assert.InDelta(t, 2, *report.Trend[0].UpstreamCostCNY, 1e-9)
			assert.InDelta(t, 3, *report.Trend[0].GrossProfitCNY, 1e-9)
			assert.InDelta(t, 0, report.Trend[1].RevenueCNY, 1e-9)
			assert.InDelta(t, 5, report.Trend[1].RefundCNY, 1e-9)
			require.NotNil(t, report.Trend[1].UpstreamCostCNY)
			assert.InDelta(t, -1, *report.Trend[1].UpstreamCostCNY, 1e-9)
			assert.InDelta(t, -1, *report.Trend[1].GrossProfitCNY, 1e-9)
			assert.Zero(t, report.Trend[2].BillingEvents)
			assert.Zero(t, report.Trend[2].RevenueCNY)
			assert.Nil(t, report.Trend[2].UpstreamCostCNY, "empty days do not establish a known zero cost")
			assert.Nil(t, report.Trend[2].GrossProfitCNY)
			for _, filter := range []model.ModelProfitFilter{
				{Start: start, End: end, ModelName: "retired-video"},
				{Start: start, End: end, ModelName: "missing-model"},
			} {
				filtered, err := model.GetModelProfits(ctx, filter)
				require.NoError(t, err)
				assert.Equal(t, report.ModelOptions, filtered.ModelOptions, "a model filter must not hide other choices or historical models")
				require.Len(t, filtered.Trend, 3)
				assert.Zero(t, filtered.Summary.RevenueCNY)
				if filter.ModelName == "missing-model" {
					assert.Empty(t, filtered.Rows)
					assert.Nil(t, filtered.Summary.UpstreamCostCNY)
				} else {
					require.Len(t, filtered.Rows, 1)
					assert.Equal(t, "retired-video", filtered.Rows[0].ModelName)
					require.NotNil(t, filtered.Summary.UpstreamCostCNY)
					assert.Zero(t, *filtered.Summary.UpstreamCostCNY)
				}
			}
			refunded, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: day + 86400, End: end, ModelName: "retired-video"})
			require.NoError(t, err)
			assert.Zero(t, refunded.Summary.Requests)
			assert.InDelta(t, -5, refunded.Trend[0].RevenueCNY, 1e-9)
			require.NotNil(t, refunded.Trend[0].UpstreamCostCNY)
			assert.InDelta(t, -2, *refunded.Trend[0].UpstreamCostCNY, 1e-9, "find the original charge outside the selected range")
			empty, err := model.GetModelProfits(ctx, model.ModelProfitFilter{Start: end + 1, End: end + 1 + 366*86400})
			require.NoError(t, err)
			require.Len(t, empty.Trend, 367, "a partial first day can span 367 calendar dates")
			assert.Empty(t, empty.ModelOptions)
			assert.Empty(t, empty.Rows)
		})
	}
}

func TestModelProfitPermissionsAndRuleValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.Task{}, &model.ModelProfitBillingRecord{}))
	owner.Role = common.RoleCommonUser
	require.NoError(t, model.DB.Model(owner).Update("role", owner.Role).Error)
	_, userJWT := createAccessTokenTestSession(t, owner.Id, "profit-user")
	admin := model.User{Username: "profit-admin", AffCode: "profit-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&admin).Error)
	_, adminJWT := createAccessTokenTestSession(t, admin.Id, "profit-admin")
	root := model.User{Username: "profit-root", AffCode: "profit-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&root).Error)
	_, rootJWT := createAccessTokenTestSession(t, root.Id, "profit-root")
	router := gin.New()
	router.GET("/api/model-profit/", middleware.AdminAuth(), GetModelProfits)
	router.GET("/api/model-profit/cost-rules", middleware.AdminAuth(), GetModelProfitCostRules)
	router.PUT("/api/model-profit/cost-rules", middleware.RootAuth(), SaveModelProfitCostRules)
	router.PUT("/api/option", middleware.RootAuth(), UpdateOption)
	path := "/api/model-profit/?start_timestamp=1&end_timestamp=300"
	assert.Equal(t, http.StatusUnauthorized, accessTokenRequest(router, http.MethodGet, path, "", "", "").Code)
	assert.Equal(t, http.StatusForbidden, accessTokenRequest(router, http.MethodGet, path, userJWT, "", "").Code)
	assert.Equal(t, http.StatusOK, accessTokenRequest(router, http.MethodGet, path, adminJWT, "", "").Code)
	assert.Equal(t, http.StatusForbidden, accessTokenRequest(router, http.MethodPut, "/api/model-profit/cost-rules", adminJWT, "", `{"schema_version":1,"revision":0,"rules":[]}`).Code)
	response := accessTokenRequest(router, http.MethodPut, "/api/model-profit/cost-rules", rootJWT, "", `{"schema_version":1,"revision":0,"rules":[]}`)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"revision":1`)
	assert.Equal(t, http.StatusConflict, accessTokenRequest(router, http.MethodPut, "/api/model-profit/cost-rules", rootJWT, "", `{"schema_version":1,"revision":0,"rules":[]}`).Code)
	assert.Equal(t, http.StatusBadRequest, accessTokenRequest(router, http.MethodPut, "/api/option", rootJWT, "", `{"key":"modelprofitcostrules","value":"{}"}`).Code)
	for _, body := range []string{
		`{"schema_version":1,"revision":1,"rules":[],"unexpected":1}`,
		`{"schema_version":1,"revision":1,"rules":[{"channel_id":19,"model_name":"x","effective_from":0,"kind":"request","source":"x"}]}`,
		`{"schema_version":1,"revision":1,"rules":[{"channel_id":19,"model_name":"x","effective_from":null,"kind":"request","unit_cost_cny":0,"source":"x"}]}`,
		`{"schema_version":1,"revision":1,"rules":[{"channel_id":19,"model_name":"x","effective_from":0,"kind":"request","unit_cost_cny":-1,"source":"x"}]}`,
		`{"schema_version":1,"revision":1,"rules":[{"channel_id":19,"model_name":"x","effective_from":0,"kind":"request","unit_cost_cny":0,"source":"x","typo":1}]}`,
	} {
		assert.Equal(t, http.StatusBadRequest, accessTokenRequest(router, http.MethodPut, "/api/model-profit/cost-rules", rootJWT, "", body).Code, body)
	}
	for _, query := range []string{"start_timestamp=0&end_timestamp=bad", "start_timestamp=-1&end_timestamp=10", "start_timestamp=2&end_timestamp=1", "start_timestamp=1&end_timestamp=31622402"} {
		assert.Equal(t, http.StatusBadRequest, accessTokenRequest(router, http.MethodGet, "/api/model-profit/?"+query, adminJWT, "", "").Code, query)
	}
}
