package controller

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalanceLedgerDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("local ledger test database DSN is not configured")
			}
			for _, separate := range []bool{false, true} {
				t.Run(fmt.Sprintf("separate_logs_%t", separate), func(t *testing.T) {
					db, _ := newAuditTestDatabase(t, kind, dsn)
					logDB := db
					if separate {
						logDB, _ = newAuditTestDatabase(t, kind, dsn)
					}
					typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
					useLogExportDatabases(t, db, logDB, typ, typ)
					require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
					require.NoError(t, logDB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
					versionQuery := "SELECT version()"
					if kind == "sqlite" {
						versionQuery = "SELECT sqlite_version()"
					}
					var version string
					require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
					t.Logf("database version: %s", version)
					alice := model.User{Username: "ledger-alice", AffCode: "ledger-alice", Quota: 12345}
					bob := model.User{Username: "ledger-bob", AffCode: "ledger-bob", Quota: 54321}
					require.NoError(t, db.Create(&alice).Error)
					require.NoError(t, db.Create(&bob).Error)
					payments := []model.TopUp{
						{UserId: alice.Id, Amount: 1000, Money: 50, TradeNo: "ledger-paid-wechat", PaymentProvider: model.PaymentProviderWechat, Status: common.TopUpStatusSuccess, CreateTime: 1, CompleteTime: 100},
						{UserId: bob.Id, Amount: 2, Money: 2, TradeNo: "ledger-paid-stripe", PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusSuccess, CreateTime: 1, CompleteTime: 300},
						{UserId: alice.Id, Amount: 0, Money: 3, TradeNo: "ledger-paid-subscription", PaymentMethod: model.PaymentMethodCreem, Status: common.TopUpStatusSuccess, CreateTime: 1, CompleteTime: 250},
						{UserId: alice.Id, Amount: 999999, Money: 99999, TradeNo: "ledger-pending", PaymentProvider: model.PaymentProviderWechat, Status: common.TopUpStatusPending, CreateTime: 100, CompleteTime: 150},
						{UserId: alice.Id, Amount: 0, Money: 999, TradeNo: "ledger-balance-not-cash", PaymentProvider: model.PaymentProviderBalance, Status: common.TopUpStatusSuccess, CompleteTime: 170},
					}
					require.NoError(t, db.Create(&payments).Error)
					logRows := []model.Log{
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 110, Type: model.LogTypeSystem, Content: "New user sign-up bonus ¥10.00", Other: `{"content_parts":[{"key":"New user sign-up bonus {{quota}}","params":{"quota":"¥10.00"}}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 120, Type: model.LogTypeSystem, Content: "new recorded bonus", Other: `{"content_parts":[{"key":"New user sign-up bonus {{quota}}","params":{"quota":"¥10.00"}}],"balance_change":{"category":"registration_bonus","quota_delta":100}}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 130, Type: model.LogTypeTopup, Content: "administrator added quota", Other: `{"op":{"action":"user.quota_add","params":{"from":1000,"to":1500,"requested_quota":999999}},"admin_info":{"admin_id":999,"admin_username":"ledger-operator"}}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 140, Type: model.LogTypeTopup, Content: "administrator overwrote quota", Other: `{"op":{"action":"user.quota_override","params":{"from":2000,"to":500,"requested_quota":500}},"admin_info":{"admin_id":999,"admin_username":"ledger-operator"}}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 150, Type: model.LogTypeTopup, Content: "administrator unchanged quota", Other: `{"op":{"action":"user.quota_override","params":{"from":500,"to":500,"requested_quota":500}}}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 160, Type: model.LogTypeTopup, Content: "redemption", Other: `{"content_parts":[{"key":"Topped up {{quota}} with a redemption code, code ID {{id}}","params":{"quota":"¥20.00","id":5}}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 170, Type: model.LogTypeSystem, Content: "checkin", Other: `{"balance_change":{"category":"checkin_reward","quota_delta":25}}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 180, Type: model.LogTypeSystem, Content: "invitation", Other: `{"content_parts":[{"key":"Bonus for inviting a user {{quota}}","params":{"quota":"¥5.00"}}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 190, Type: model.LogTypeTopup, Content: "paid subscription with wallet", Other: `{"content_parts":[{"key":"Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}","params":{"quota":200,"amount":"2.00"}}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 200, Type: model.LogTypeTopup, Content: "Unstructured historical topup 999", Other: `{}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 210, Type: model.LogTypeTopup, Content: "duplicate payment log", Other: `{"content_parts":[{"key":"{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}","params":{"quota":"¥50.00","amount":"50.00"}}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 250, Type: model.LogTypeTopup, Content: "duplicate subscription payment log", Other: `{"content_parts":[{"key":"Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}"}]}`},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 400, Type: model.LogTypeConsume, Quota: 999999, Content: "not a balance source"},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 400, Type: model.LogTypeError, Content: "private provider diagnostic"},
						{UserId: alice.Id, Username: alice.Username, CreatedAt: 500, Type: model.LogTypeSystem, Content: "unrelated business entry", Other: `{"content_parts":[{"key":"unrelated business entry"}]}`},
					}
					require.NoError(t, logDB.Create(&logRows).Error)
					// An independent operation audit must never count as another credit.
					require.NoError(t, logDB.Create(&model.AuditLog{EventId: "duplicate-audit", UserId: 999, CreatedAt: 130, Action: "user.quota_add", Success: true}).Error)
					items, total, summary, err := model.GetBalanceLedger(model.BalanceLedgerFilter{}, 0, 100)
					require.NoError(t, err)
					assert.EqualValues(t, 13, total)
					require.Len(t, items, 13)
					assert.Equal(t, bob.Id, items[0].UserID)
					assert.EqualValues(t, 300, items[0].CreatedAt)
					assert.Equal(t, "subscription_purchase", items[1].Category)
					assert.EqualValues(t, 3, summary.PaidOrders)
					assert.Nil(t, summary.PaidMoney, "mixed / missing currencies must not become a single CNY amount")
					assert.Nil(t, summary.PaidCurrency)
					require.Len(t, summary.PaidTotals, 3)
					assert.EqualValues(t, 2, summary.RegistrationBonusCount)
					assert.EqualValues(t, 100, summary.RegistrationBonusQuota)
					assert.EqualValues(t, 1, summary.RegistrationBonusUnknown)
					assert.EqualValues(t, 500, summary.AdminIncreaseQuota)
					assert.EqualValues(t, 1500, summary.AdminDecreaseQuota)
					assert.EqualValues(t, 3, summary.AdminAdjustments)
					assert.EqualValues(t, 5, summary.UnknownCount)
					assert.True(t, summary.RawQuotaOnly)
					for _, item := range items {
						assert.NotContains(t, item.Content, "private provider")
						if item.CreatedAt == 110 {
							require.NotNil(t, item.DisplayAmount)
							assert.Equal(t, "¥10.00", *item.DisplayAmount)
							assert.Nil(t, item.QuotaDelta, "display strings must not be reverse-converted")
						}
						if item.CreatedAt == 140 {
							require.NotNil(t, item.QuotaDelta)
							assert.EqualValues(t, -1500, *item.QuotaDelta)
							require.NotNil(t, item.OperatorID)
							assert.Equal(t, 999, *item.OperatorID)
						}
					}
					for offset := 0; offset < len(items)+2; offset += 2 {
						page, count, _, err := model.GetBalanceLedger(model.BalanceLedgerFilter{}, offset, 2)
						require.NoError(t, err)
						assert.Equal(t, total, count)
						end := min(offset+2, len(items))
						if offset >= len(items) {
							assert.Empty(t, page)
						} else {
							assert.Equal(t, items[offset:end], page)
						}
					}
					filtered, count, filteredSummary, err := model.GetBalanceLedger(model.BalanceLedgerFilter{UserID: alice.Id, Category: "admin_adjustment"}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 3, count)
					assert.Len(t, filtered, 3)
					assert.Zero(t, filteredSummary.PaidOrders)
					_, count, _, err = model.GetBalanceLedger(model.BalanceLedgerFilter{UserID: 999}, 0, 20)
					require.NoError(t, err)
					assert.Zero(t, count, "operator ID is not the affected user's ID")
					filtered, count, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{Username: bob.Username}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 1, count)
					assert.Len(t, filtered, 1)
					assert.Nil(t, filteredSummary.PaidCurrency)
					filtered, count, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{Start: 100, End: 100}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 1, count, "payments use completion time, not order creation time")
					require.Len(t, filtered, 1)
					require.NotNil(t, filteredSummary.PaidCurrency)
					assert.Equal(t, "CNY", *filteredSummary.PaidCurrency)
					require.NotNil(t, filteredSummary.PaidMoney)
					assert.Equal(t, float64(50), *filteredSummary.PaidMoney)
					// New metadata stays in original units despite unrelated display text.
					model.RecordLog(alice.Id, model.LogTypeSystem, common.NewMessage("New user sign-up bonus {{quota}}", map[string]any{"quota": "¥10.00"}), model.BalanceChange{Category: "registration_bonus", QuotaDelta: 333})
					_, count, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{Category: "registration_bonus"}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 3, count)
					assert.EqualValues(t, 433, filteredSummary.RegistrationBonusQuota)
					// Partial / invalid quota metadata stays unknown; an explicit zero
					// is different from a missing amount and remains a known change.
					for _, metadata := range []string{
						`{"balance_change":{"category":"registration_bonus"}}`,
						`{"balance_change":{"category":"registration_bonus","quota_delta":"100"}}`,
						`{"balance_change":{"category":"registration_bonus","quota_delta":0}}`,
					} {
						require.NoError(t, logDB.Create(&model.Log{UserId: alice.Id, Username: alice.Username, Type: model.LogTypeSystem, Other: metadata}).Error)
					}
					_, count, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{Category: "registration_bonus"}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 6, count)
					assert.EqualValues(t, 3, filteredSummary.RegistrationBonusUnknown)
					assert.EqualValues(t, 433, filteredSummary.RegistrationBonusQuota)
					require.NoError(t, db.Model(&alice).Update("username", "ledger-renamed").Error)
					_, byName, _, err := model.GetBalanceLedger(model.BalanceLedgerFilter{Username: "ledger-renamed"}, 0, 20)
					require.NoError(t, err)
					_, byID, _, err := model.GetBalanceLedger(model.BalanceLedgerFilter{UserID: alice.Id}, 0, 20)
					require.NoError(t, err)
					assert.Equal(t, byID, byName, "renaming an account cannot omit its historical log snapshots")
					// Valid negative balances can make the actual difference larger
					// than JS's safe integer; the exact string must retain every unit.
					maximum := int64(common.MaxWalletQuota)
					for _, pair := range [][2]int64{{-maximum, maximum}, {maximum, -maximum + 1}} {
						metadata := fmt.Sprintf(`{"op":{"action":"user.quota_override","params":{"from":%d,"to":%d}}}`, pair[0], pair[1])
						require.NoError(t, logDB.Create(&model.Log{UserId: bob.Id, Username: bob.Username, Type: model.LogTypeTopup, Other: metadata}).Error)
					}
					filtered, count, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{UserID: bob.Id, Category: "admin_adjustment"}, 0, 20)
					require.NoError(t, err)
					assert.EqualValues(t, 2, count)
					require.Len(t, filtered, 2)
					assert.Equal(t, -2*maximum+1, *filtered[0].QuotaDelta)
					assert.Equal(t, fmt.Sprintf("%d", -2*maximum+1), *filtered[0].QuotaDeltaExact)
					assert.Equal(t, fmt.Sprintf("%d", 2*maximum), filteredSummary.AdminIncreaseQuotaExact)
					assert.Equal(t, fmt.Sprintf("%d", 2*maximum-1), filteredSummary.AdminDecreaseQuotaExact)
					// Empty provider subscription receipts retain distinct method buckets.
					require.NoError(t, db.Create(&model.TopUp{UserId: alice.Id, Amount: 0, Money: 7, TradeNo: "ledger-another-subscription", PaymentMethod: "alipay", Status: common.TopUpStatusSuccess, CompleteTime: 251}).Error)
					_, _, filteredSummary, err = model.GetBalanceLedger(model.BalanceLedgerFilter{Category: "subscription_purchase"}, 0, 20)
					require.NoError(t, err)
					require.Len(t, filteredSummary.PaidTotals, 2)
					assert.NotEqual(t, filteredSummary.PaidTotals[0].PaymentProvider, filteredSummary.PaidTotals[1].PaymentProvider)
					var saved model.User
					require.NoError(t, db.First(&saved, alice.Id).Error)
					assert.Equal(t, 12345, saved.Quota, "the ledger is read-only")
				})
			}
		})
	}
}

func TestBalanceLedgerPermissionsAndQueryValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TopUp{}))
	owner.Role = common.RoleCommonUser
	require.NoError(t, model.DB.Model(owner).Update("role", owner.Role).Error)
	_, userJWT := createAccessTokenTestSession(t, owner.Id, "balance-ledger-user")
	admin := model.User{Username: "ledger-administrator", AffCode: "ledger-administrator", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&admin).Error)
	_, adminJWT := createAccessTokenTestSession(t, admin.Id, "balance-ledger-administrator")
	router := gin.New()
	router.GET("/api/balance-ledger/", middleware.AdminAuth(), GetBalanceLedger)
	response := accessTokenRequest(router, http.MethodGet, "/api/balance-ledger/", "", "", "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	response = accessTokenRequest(router, http.MethodGet, "/api/balance-ledger/", userJWT, "", "")
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = accessTokenRequest(router, http.MethodGet, "/api/balance-ledger/", adminJWT, "", "")
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"raw_quota_only":true`)
	for _, query := range []string{"p=-1", "p=invalid", "page_size=101", "user_id=0", "user_id=no", "category=login", "start_timestamp=-1", "end_timestamp=bad", "start_timestamp=200&end_timestamp=100", "p=100000&page_size=100"} {
		response = accessTokenRequest(router, http.MethodGet, "/api/balance-ledger/?"+query, adminJWT, "", "")
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
	}
}
