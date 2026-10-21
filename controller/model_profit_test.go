package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Task{}))
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
		})
	}
}

func TestModelProfitPermissionsAndRuleValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.Task{}))
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
