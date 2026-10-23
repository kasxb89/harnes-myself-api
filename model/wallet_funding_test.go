package model

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Run with TEST_WALLET_FUNDING_DIALECT=mysql|postgres and its TEST_*_DSN to
// execute this same accounting contract against a real external database.
func TestWalletFundingDatabase(t *testing.T) {
	kind := os.Getenv("TEST_WALLET_FUNDING_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	var driver gorm.Dialector
	switch kind {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "wallet.db") + "?_pragma=busy_timeout(30000)&_txlock=immediate")
	case "mysql":
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported database %q", kind)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("wf_%d_", time.Now().UnixNano())}, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	oldDB, oldLogDB, oldType, oldRedis, oldBatch := DB, LOG_DB, common.MainDatabaseType(), common.RedisEnabled, common.BatchUpdateEnabled
	DB, LOG_DB, common.RedisEnabled, common.BatchUpdateEnabled = db, db, false, true
	common.SetMainDatabaseType(common.DatabaseType(kind))
	models := append([]any{&User{}, &TopUp{}, &Checkin{}, &Redemption{}, &Log{}}, WalletFundingModels()...)
	t.Cleanup(func() {
		for i := len(models) - 1; i >= 0; i-- {
			require.NoError(t, db.Migrator().DropTable(models[i]))
		}
		DB, LOG_DB, common.RedisEnabled, common.BatchUpdateEnabled = oldDB, oldLogDB, oldRedis, oldBatch
		common.SetMainDatabaseType(oldType)
		require.NoError(t, sqlDB.Close())
	})
	// Simulate upgrading an existing users-only database. Repeated migration
	// preserves the pre-existing balance and does not guess its funding source.
	require.NoError(t, db.AutoMigrate(&User{}))
	legacy := createWalletFundingUser(t, 20)
	// The released User schema has every existing field but no source-ledger
	// initialization flag. Upgrade that populated layout, rather than merely
	// opening another fresh database with the current struct.
	require.NoError(t, db.Migrator().DropColumn(&User{}, "wallet_funding_initialized"))
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.AutoMigrate(models...))
	var upgraded User
	require.NoError(t, db.First(&upgraded, legacy.Id).Error)
	assert.Equal(t, 20, upgraded.Quota)
	assert.False(t, upgraded.WalletFundingInitialized)
	requestIndex := db.NamingStrategy.IndexName(db.NamingStrategy.TableName("wallet_funding_requests"), "wallet_request_identity")
	allocationIndex := db.NamingStrategy.IndexName(db.NamingStrategy.TableName("wallet_funding_allocations"), "wallet_allocation_lot")
	require.True(t, db.Migrator().HasIndex(&WalletFundingRequest{}, requestIndex))
	require.True(t, db.Migrator().HasIndex(&WalletFundingAllocation{}, allocationIndex))
	query := "SELECT version()"
	if kind == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("wallet funding database: %s", version)

	if kind == "mysql" {
		t.Run("first_touch_of_distinct_users_does_not_deadlock", func(t *testing.T) {
			first, second := createWalletFundingUser(t, 100), createWalletFundingUser(t, 100)
			ready, release := make(chan struct{}, 2), make(chan struct{})
			var releaseOnce sync.Once
			releaseBoth := func() { releaseOnce.Do(func() { close(release) }) }
			var reached sync.Map
			barrier := func(userID int) {
				if userID != first.Id && userID != second.Id {
					return
				}
				if _, loaded := reached.LoadOrStore(userID, true); loaded {
					return
				}
				ready <- struct{}{}
				<-release
			}
			legacyProof := os.Getenv("TEST_WALLET_GAP_BARRIER") == "legacy-query"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("wallet_gap_query_barrier", func(tx *gorm.DB) {
				if !legacyProof || len(tx.Statement.Selects) != 2 || tx.Statement.Selects[0] != "id" || tx.Statement.Selects[1] != "remaining_quota" {
					return
				}
				// This branch is only used with the archived pre-fix engine to
				// reproduce two shared missing-range gap locks deterministically.
				if lots, ok := tx.Statement.Dest.(*[]WalletFundingLot); ok && len(*lots) == 0 {
					if len(tx.Statement.Vars) > 0 {
						if userID, ok := tx.Statement.Vars[0].(int); ok {
							barrier(userID)
						}
					}
				}
			}))
			require.NoError(t, db.Callback().Create().After("gorm:create").Register("wallet_gap_create_barrier", func(tx *gorm.DB) {
				if legacyProof {
					return
				}
				if lot, ok := tx.Statement.Dest.(*WalletFundingLot); ok && lot.SourceKey == "legacy-opening" {
					barrier(lot.UserID)
				}
			}))
			defer db.Callback().Query().Remove("wallet_gap_query_barrier")
			defer db.Callback().Create().Remove("wallet_gap_create_barrier")
			outcomes := make(chan error, 2)
			var wg sync.WaitGroup
			defer wg.Wait()
			defer releaseBoth()
			for _, userID := range []int{first.Id, second.Id} {
				wg.Go(func() { _, err := ApplyWalletFundingTarget(userID, "first-use", "relay", 10, true); outcomes <- err })
			}
			for range 2 {
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatal("both independent first-touch transactions did not reach the controlled barrier")
				}
			}
			releaseBoth()
			wg.Wait()
			close(outcomes)
			for err := range outcomes {
				require.NoError(t, err)
			}
			assert.Equal(t, 90, getUserQuotaFromDB(t, first.Id))
			assert.Equal(t, 90, getUserQuotaFromDB(t, second.Id))
		})

		t.Run("repeatable_read_snapshot_before_wallet_lock_uses_current_lots", func(t *testing.T) {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("existing_lots_%t", existing), func(t *testing.T) {
					user := createWalletFundingUser(t, 100)
					if existing {
						_, err := ApplyWalletFundingTarget(user.Id, "prepare-opening", "relay", 0, true)
						require.NoError(t, err)
					}
					tx := db.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					require.NoError(t, tx.Error)
					t.Cleanup(func() { _ = tx.Rollback().Error })
					var snapshot User
					require.NoError(t, tx.First(&snapshot, user.Id).Error, "establish an old consistent-read snapshot before locking user")
					_, err := ApplyWalletFundingTarget(user.Id, "concurrent-charge", "relay", 10, true)
					require.NoError(t, err)
					if existing {
						require.NoError(t, DebitWalletFundingWithTx(tx, user.Id, 5, "outer-debit", true))
					} else {
						require.NoError(t, CreditWalletFundingWithTx(tx, user.Id, 5, WalletFundingGift, "outer-credit", nil))
					}
					require.NoError(t, tx.Commit().Error)
					expected := 95
					if existing {
						expected = 85
					}
					assert.Equal(t, expected, getUserQuotaFromDB(t, user.Id))
					var lots []WalletFundingLot
					require.NoError(t, db.Where("user_id = ?", user.Id).Find(&lots).Error)
					var remaining int64
					for _, lot := range lots {
						remaining += lot.RemainingQuota
					}
					assert.EqualValues(t, expected, remaining)
					openingCount := 0
					for _, lot := range lots {
						if lot.SourceKey == "legacy-opening" {
							openingCount++
						}
					}
					assert.Equal(t, 1, openingCount, "an old snapshot must not insert a duplicate opening")
				})
			}
		})
	}

	t.Run("prepare_tracks_fallback_origin_without_charging", func(t *testing.T) {
		user := createWalletFundingUser(t, 20)
		require.NoError(t, PrepareWalletFundingRequest(user.Id, "fallback-request", "relay", "first-model", 18))
		require.NoError(t, PrepareWalletFundingRequest(user.Id, "fallback-request", "relay", "fallback-model", 19))
		assert.Equal(t, 20, getUserQuotaFromDB(t, user.Id))
		var request WalletFundingRequest
		require.NoError(t, db.Where("user_id = ? AND request_id = ?", user.Id, "fallback-request").First(&request).Error)
		assert.Equal(t, "first-model", request.InitialModelName)
		assert.Equal(t, "fallback-model", request.ModelName)
		assert.Equal(t, 19, request.ChannelID)
		assert.Zero(t, request.Quota)
		assert.Zero(t, request.LastEventID)
		var events int64
		require.NoError(t, db.Model(&WalletFundingEvent{}).Where("user_id = ?", user.Id).Count(&events).Error)
		assert.Zero(t, events)
		result, err := ApplyWalletFundingTarget(user.Id, "fallback-request", "relay", 5, true)
		require.NoError(t, err)
		assert.Positive(t, result.EventID)
		assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))
	})

	t.Run("payment_reward_and_admin_entry_points_keep_sources", func(t *testing.T) {
		user := createWalletFundingUser(t, 0)
		order := TopUp{UserId: user.Id, Amount: 100, Money: 5, TradeNo: "wallet-verified-cny", PaymentMethod: PaymentMethodWechat, PaymentProvider: PaymentProviderWechat, Status: common.TopUpStatusPending}
		require.NoError(t, db.Create(&order).Error)
		require.NoError(t, RechargeWechat(order.TradeNo, "local-test"))
		require.NoError(t, RechargeWechat(order.TradeNo, "local-test"))
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
		charged, err := ApplyWalletFundingTarget(user.Id, "payment-use", "relay", 40, true)
		require.NoError(t, err)
		assert.EqualValues(t, 40, charged.Total.PaidQuota)
		require.NotNil(t, charged.Total.CashRevenueCNY)
		assert.Equal(t, "2", charged.Total.CashRevenueCNY.String())
		_, err = ApplyWalletFundingTarget(user.Id, "payment-use", "relay", 0, false)
		require.NoError(t, err)
		giftUser := createWalletFundingUser(t, 0)
		adjustment, err := AdjustUserQuota(giftUser.Id, common.RoleRootUser, "add", 20)
		require.NoError(t, err)
		assert.Equal(t, 20, adjustment.After)
		checkin := Checkin{UserId: giftUser.Id, CheckinDate: "2026-10-24", QuotaAwarded: 10, CreatedAt: common.GetTimestamp()}
		_, err = userCheckinWithTransaction(&checkin, giftUser.Id, 10)
		require.NoError(t, err)
		giftUse, err := ApplyWalletFundingTarget(giftUser.Id, "gifts-use", "relay", 25, true)
		require.NoError(t, err)
		assert.EqualValues(t, 25, giftUse.Total.GiftQuota)
		require.NotNil(t, giftUse.Total.CashRevenueCNY)
		assert.True(t, giftUse.Total.CashRevenueCNY.IsZero())
		_, err = AdjustUserQuota(giftUser.Id, common.RoleRootUser, "override", 0)
		require.NoError(t, err)
		assert.Zero(t, getUserQuotaFromDB(t, giftUser.Id))
		code := Redemption{Key: "wallet-test-code", Name: "local-test", Status: common.RedemptionCodeStatusEnabled, Quota: 12}
		require.NoError(t, db.Create(&code).Error)
		_, err = Redeem(code.Key, giftUser.Id)
		require.NoError(t, err)
		redemptionUse, err := ApplyWalletFundingTarget(giftUser.Id, "redemption-use", "relay", 12, true)
		require.NoError(t, err)
		assert.EqualValues(t, 12, redemptionUse.Total.UnknownQuota)
		assert.Nil(t, redemptionUse.Total.CashRevenueCNY)
	})

	t.Run("legacy_opening_fifo_discount_gift_and_exact_refund", func(t *testing.T) {
		require.NoError(t, CreditUserGiftQuota(legacy.Id, 30, "test-gift"))
		paid := decimal.RequireFromString("4.8")
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return CreditWalletFundingWithTx(tx, legacy.Id, 100, WalletFundingPaid, "discounted-topup", &paid)
		}))
		result, err := ApplyWalletFundingTarget(legacy.Id, "request-a", "relay", 100, true)
		require.NoError(t, err)
		assert.EqualValues(t, 20, result.Total.UnknownQuota)
		assert.EqualValues(t, 30, result.Total.GiftQuota)
		assert.EqualValues(t, 50, result.Total.PaidQuota)
		assert.Equal(t, "2.4", result.Total.KnownCashRevenueCNY.String())
		assert.Nil(t, result.Total.CashRevenueCNY)
		assert.Equal(t, 50, getUserQuotaFromDB(t, legacy.Id))
		replayed, err := ApplyWalletFundingTarget(legacy.Id, "request-a", "relay", 100, true)
		require.NoError(t, err)
		assert.False(t, replayed.Applied)
		assert.Zero(t, replayed.AppliedDelta)
		assert.Equal(t, result.EventID, replayed.EventID)
		partial, err := ApplyWalletFundingTarget(legacy.Id, "request-a", "relay", 70, false)
		require.NoError(t, err)
		assert.EqualValues(t, -30, partial.Delta.PaidQuota)
		assert.Equal(t, "-1.44", partial.Delta.KnownCashRevenueCNY.String())
		refunded, err := ApplyWalletFundingTarget(legacy.Id, "request-a", "relay", 0, false)
		require.NoError(t, err)
		assert.Equal(t, 150, getUserQuotaFromDB(t, legacy.Id))
		assert.Zero(t, refunded.Total.Quota)
		require.NotNil(t, refunded.Total.CashRevenueCNY)
		var lot WalletFundingLot
		require.NoError(t, db.Where("user_id = ? AND source_key = ?", legacy.Id, "discounted-topup").First(&lot).Error)
		assert.EqualValues(t, 100, lot.RemainingQuota)
		assert.Equal(t, "4.8", lot.RemainingCashCNY)
	})

	t.Run("all_gift_is_confirmed_zero_income_paid_unknown_is_not_zero", func(t *testing.T) {
		user := createWalletFundingUser(t, 0)
		require.NoError(t, CreditUserGiftQuota(user.Id, 40, "gift"))
		result, err := ApplyWalletFundingTarget(user.Id, "gift-use", "relay", 30, true)
		require.NoError(t, err)
		require.NotNil(t, result.Total.CashRevenueCNY)
		assert.True(t, result.Total.CashRevenueCNY.IsZero())
		assert.EqualValues(t, 30, result.Total.GiftQuota)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return CreditWalletFundingWithTx(tx, user.Id, 50, WalletFundingPaid, "currency-not-recorded", nil)
		}))
		result, err = ApplyWalletFundingTarget(user.Id, "unknown-cash-use", "relay", 20, true)
		require.NoError(t, err)
		assert.EqualValues(t, 10, result.Total.GiftQuota)
		assert.EqualValues(t, 10, result.Total.PaidQuota)
		assert.EqualValues(t, 10, result.Total.CashUnknownQuota)
		assert.Nil(t, result.Total.CashRevenueCNY)
	})

	t.Run("rounding_restores_original_cash_without_minting", func(t *testing.T) {
		user := createWalletFundingUser(t, 0)
		cash := decimal.RequireFromString("1")
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return CreditWalletFundingWithTx(tx, user.Id, 3, WalletFundingPaid, "thirds", &cash)
		}))
		for target := 1; target <= 3; target++ {
			_, err := ApplyWalletFundingTarget(user.Id, "thirds-use", "relay", target, true)
			require.NoError(t, err)
		}
		full, err := GetWalletFundingSnapshot(user.Id, "thirds-use", "relay")
		require.NoError(t, err)
		require.NotNil(t, full.CashRevenueCNY)
		assert.Equal(t, "1", full.CashRevenueCNY.String())
		for target := 2; target >= 0; target-- {
			_, err := ApplyWalletFundingTarget(user.Id, "thirds-use", "relay", target, false)
			require.NoError(t, err)
		}
		var lot WalletFundingLot
		require.NoError(t, db.Where("user_id = ? AND source_key = ?", user.Id, "thirds").First(&lot).Error)
		assert.Equal(t, "1", lot.RemainingCashCNY)
		assert.EqualValues(t, 3, lot.RemainingQuota)
	})

	t.Run("allowed_debt_and_legacy_refund_stay_unknown", func(t *testing.T) {
		user := createWalletFundingUser(t, 5)
		result, err := ApplyWalletFundingTarget(user.Id, "debt-use", "relay", 10, false)
		require.NoError(t, err)
		assert.EqualValues(t, 10, result.Total.UnknownQuota)
		assert.Equal(t, -5, getUserQuotaFromDB(t, user.Id))
		_, err = ApplyWalletFundingTarget(user.Id, "debt-use", "relay", 0, false)
		require.NoError(t, err)
		assert.Equal(t, 5, getUserQuotaFromDB(t, user.Id))
		_, err = AdoptLegacyWalletFunding(user.Id, "legacy-task:7", "relay", 8)
		require.NoError(t, err)
		assert.Equal(t, 5, getUserQuotaFromDB(t, user.Id), "adopting an old deduction does not deduct again")
		result, err = ApplyWalletFundingTarget(user.Id, "legacy-task:7", "relay", 0, false)
		require.NoError(t, err)
		assert.EqualValues(t, -8, result.Delta.UnknownQuota)
		assert.Equal(t, 13, getUserQuotaFromDB(t, user.Id))
		_, err = AdoptLegacyWalletFunding(user.Id, "legacy-task:7", "relay", 8)
		require.NoError(t, err)
		result, err = ApplyWalletFundingTarget(user.Id, "legacy-task:7", "relay", 0, false)
		require.NoError(t, err)
		assert.False(t, result.Applied)
		assert.Equal(t, 13, getUserQuotaFromDB(t, user.Id))
	})

	t.Run("concurrent_target_and_independent_reservations", func(t *testing.T) {
		user := createWalletFundingUser(t, 100)
		var wg sync.WaitGroup
		outcomes := make(chan error, 4)
		for range 4 {
			wg.Go(func() { _, err := ApplyWalletFundingTarget(user.Id, "same-target", "relay", 60, true); outcomes <- err })
		}
		wg.Wait()
		close(outcomes)
		for err := range outcomes {
			require.NoError(t, err)
		}
		assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
		var events int64
		require.NoError(t, db.Model(&WalletFundingEvent{}).Where("user_id = ? AND request_id = ?", user.Id, "same-target").Count(&events).Error)
		assert.EqualValues(t, 1, events)
		_, err := ApplyWalletFundingTarget(user.Id, "second-target", "relay", 41, true)
		assert.ErrorIs(t, err, ErrInsufficientWalletFunding)
		assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
	})

	t.Run("transaction_rollback_and_missing_schema_fail_closed", func(t *testing.T) {
		user := createWalletFundingUser(t, 4)
		sentinel := errors.New("cancel grant")
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := CreditWalletFundingWithTx(tx, user.Id, 7, WalletFundingGift, "rolled-back", nil); err != nil {
				return err
			}
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
		assert.Equal(t, 4, getUserQuotaFromDB(t, user.Id))
		var count int64
		require.NoError(t, db.Model(&WalletFundingLot{}).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.Zero(t, count)
		_, err = ApplyWalletFundingTarget(user.Id, "invalid-negative", "relay", -1, false)
		assert.Error(t, err)
		require.NoError(t, db.Migrator().DropTable(&WalletFundingAllocation{}))
		_, err = ApplyWalletFundingTarget(user.Id, "missing-table", "relay", 1, true)
		assert.Error(t, err)
		assert.Equal(t, 4, getUserQuotaFromDB(t, user.Id))
		require.NoError(t, db.AutoMigrate(&WalletFundingAllocation{}))
	})
}

func createWalletFundingUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{Username: "wf-" + common.GetRandomString(12), Password: "unused-hash", Quota: quota, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AffCode: common.GetRandomString(16), AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	return user
}
