package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelProfitImageResolutionDatabase(t *testing.T) {
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
			config, err := model.ParseModelProfitCostRules([]byte(`{"schema_version":1,"revision":0,"rules":[{"channel_id":18,"model_name":"resolution-image","effective_from":50,"kind":"image","unit_cost_cny":0.105,"source":"old default estimate"},{"channel_id":18,"model_name":"resolution-image","effective_from":200,"kind":"image_resolution","resolution_costs_cny":{"1k":0.1,"2k":0.15,"4k":0.2},"source":"supplier tariff by submitted resolution"}]}`))
			require.NoError(t, err)
			saved, err := model.SaveModelProfitCostRules(context.Background(), config)
			require.NoError(t, err)
			roundTrip, err := model.GetModelProfitCostRules(context.Background())
			require.NoError(t, err)
			assert.Equal(t, saved, roundTrip)
			for i, tc := range []struct {
				id, resolution string
				submitted      int64
			}{
				{"old-submit", "4k", 199}, {"one", "1k", 201}, {"two", "2k", 201}, {"four", "4k", 201}, {"unknown", "", 201},
			} {
				task := model.Task{TaskID: tc.id, UserId: 1, ChannelId: 18, SubmitTime: tc.submitted, Status: model.TaskStatusSuccess}
				require.NoError(t, db.Create(&task).Error)
				usage := map[string]any{"image_count": 1}
				if tc.resolution != "" {
					usage["resolution"] = tc.resolution
				}
				other, err := common.Marshal(map[string]any{"task_id": tc.id, "is_task": true, "usage_facts": usage})
				require.NoError(t, err)
				require.NoError(t, logDB.Create(&model.Log{CreatedAt: 230, UserId: 1, ChannelId: 18, Type: model.LogTypeConsume, ModelName: "resolution-image", Quota: 3, RequestId: fmt.Sprintf("resolution-%d", i), Other: string(other)}).Error)
			}
			report, err := model.GetModelProfits(context.Background(), model.ModelProfitFilter{Start: 220, End: 250, ModelName: "resolution-image"})
			require.NoError(t, err)
			assert.EqualValues(t, 4, report.Summary.KnownCostRequests)
			assert.EqualValues(t, 1, report.Summary.UnknownCostRequests, "missing resolution must not revive old fixed cost")
			require.NotNil(t, report.Summary.UpstreamCostCNY)
			assert.InDelta(t, .555, *report.Summary.UpstreamCostCNY, 1e-10, "old submit keeps .105; new 1K/2K/4K cost .10/.15/.20")
			var tasks []model.Task
			require.NoError(t, db.Find(&tasks).Error)
			require.Len(t, tasks, 5)
			for _, task := range tasks {
				assert.EqualValues(t, model.TaskStatusSuccess, task.Status)
			}
		})
	}
}

func TestModelProfitH3CostRules(t *testing.T) {
	const body = `{"schema_version":1,"revision":0,"rules":[{"channel_id":16,"model_name":"MiniMax-H3","effective_from":200,"kind":"h3_video","resolution_costs_cny":{"768P":0.0897396,"2K":0.149566},"extra_image_cost_cny":0.0501486,"free_input_images":5,"source":"Metaso standard reference points; not an account invoice"}]}`
	config, err := model.ParseModelProfitCostRules([]byte(body))
	require.NoError(t, err)
	for _, tc := range []struct{ name, old, replacement string }{
		{"missing tier", `"2K":0.149566`, `"8K":0.149566`},
		{"negative image rate", `"extra_image_cost_cny":0.0501486`, `"extra_image_cost_cny":-1`},
		{"missing image rate", `"extra_image_cost_cny":0.0501486,`, ``},
		{"null image rate", `"extra_image_cost_cny":0.0501486`, `"extra_image_cost_cny":null`},
		{"fractional allowance", `"free_input_images":5`, `"free_input_images":1.5`},
		{"oversized allowance", `"free_input_images":5`, `"free_input_images":129`},
		{"missing allowance", `"free_input_images":5,`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := model.ParseModelProfitCostRules([]byte(strings.Replace(body, tc.old, tc.replacement, 1)))
			assert.ErrorIs(t, err, model.ErrModelProfitParameters)
		})
	}
	kind := os.Getenv("TEST_MODEL_PROFIT_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	require.Contains(t, []string{"sqlite", "mysql", "postgres"}, kind)
	dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s_separate_%t", kind, separate), func(t *testing.T) {
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logs := db
			if separate {
				logs, _ = newAuditTestDatabase(t, kind, dsn)
			}
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, logs, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Task{}, &model.ModelProfitBillingRecord{}))
			require.NoError(t, logs.AutoMigrate(&model.Log{}))
			old := model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{}}
			saved, err := model.SaveModelProfitCostRules(context.Background(), old)
			require.NoError(t, err)
			config.Revision = saved.Revision
			config.Rules = config.Rules[:1]
			for _, channel := range []int{21, 22, 23} {
				rule := config.Rules[0]
				rule.ChannelID = channel
				config.Rules = append(config.Rules, rule)
			}
			saved, err = model.SaveModelProfitCostRules(context.Background(), config)
			require.NoError(t, err)
			roundTrip, err := model.GetModelProfitCostRules(context.Background())
			require.NoError(t, err)
			assert.Equal(t, saved, roundTrip)
			for i, tc := range []struct {
				channel, images int
				resolution      string
				seconds, input  float64
				remove          string
				submitted       int64
			}{
				{16, 9, "2K", 5, 15, "", 201},
				{21, 0, "768P", 5, 0, "", 201},
				{22, 5, "768P", 5, 0, "", 201},
				{23, 6, "2K", 4, 1.5, "", 201},
				{16, 0, "768P", 5, 0, "resolution", 201},
				{16, 0, "768P", 5, 0, "input_video_seconds", 201},
				{16, 0, "768P", 5, 0, "input_images", 201},
				{16, 0, "1080P", 5, 0, "", 201},
				{16, 0, "768P", 16, 0, "", 201},
				{16, 0, "768P", 5, 16, "", 201},
				{16, 10, "768P", 5, 0, "", 201},
				{16, 0, "768P", 5, 0, "", 199},
			} {
				id := fmt.Sprintf("h3-cost-%d", i)
				task := model.Task{TaskID: id, UserId: 1, ChannelId: tc.channel, SubmitTime: tc.submitted, Status: model.TaskStatusSuccess}
				require.NoError(t, db.Create(&task).Error)
				usage := map[string]any{"seconds": tc.seconds, "resolution": tc.resolution, "input_video_seconds": tc.input, "input_images": tc.images}
				delete(usage, tc.remove)
				other, err := common.Marshal(map[string]any{"task_id": id, "is_task": true, "usage_facts": usage})
				require.NoError(t, err)
				require.NoError(t, logs.Create(&model.Log{CreatedAt: 230, UserId: 1, ChannelId: tc.channel, Type: model.LogTypeConsume, ModelName: "MiniMax-H3", Quota: 100, RequestId: id, Other: string(other)}).Error)
			}
			measured := model.Task{TaskID: "h3-measured", UserId: 1, ChannelId: 16, SubmitTime: 201, Status: model.TaskStatusSuccess}
			require.NoError(t, db.Create(&measured).Error)
			for _, entry := range []model.Log{
				{CreatedAt: 231, UserId: 1, ChannelId: 16, Type: model.LogTypeConsume, ModelName: "MiniMax-H3", Quota: 100, RequestId: "measured-initial", Other: `{"task_id":"h3-measured","is_task":true,"usage_facts":{"seconds":5,"resolution":"2K","input_video_seconds":15,"input_images":9}}`},
				{CreatedAt: 232, UserId: 1, ChannelId: 16, Type: model.LogTypeRefund, ModelName: "MiniMax-H3", Quota: 40, RequestId: "measured-actual", Other: `{"task_id":"h3-measured","is_task":true,"pre_consumed_quota":100,"actual_quota":60,"usage_facts":{"seconds":5,"resolution":"2K","input_video_seconds":3,"input_images":6}}`},
			} {
				require.NoError(t, logs.Create(&entry).Error)
			}
			report, err := model.GetModelProfits(context.Background(), model.ModelProfitFilter{Start: 220, End: 250, ModelName: "MiniMax-H3"})
			require.NoError(t, err)
			assert.EqualValues(t, 5, report.Summary.KnownCostRequests)
			assert.EqualValues(t, 8, report.Summary.UnknownCostRequests, "missing usage and pre-effective tasks must remain unknown")
			require.NotNil(t, report.Summary.UpstreamCostCNY)
			assert.InDelta(t, 6.2087486, *report.Summary.UpstreamCostCNY, 1e-8, "measured completion replaces the initial material reservation")
			withoutHistory := saved
			withoutHistory.Rules = saved.Rules[1:]
			_, err = model.SaveModelProfitCostRules(context.Background(), withoutHistory)
			assert.ErrorIs(t, err, model.ErrModelProfitHistory)
			// Append a new effective rule; old costs and historical unknowns stay unchanged.
			two := float64(2)
			longRule := saved.Rules[0]
			longRule.EffectiveFrom, longRule.LongVideoMultiplier = 400, &two
			longRule.ResolutionCostsCNY = map[string]float64{"480P": 0.06000236, "768P": 0.0897396, "1080P": 0.13997618, "2K": 0.149566}
			longRule.Source = "Metaso long video public estimate, not invoice"
			config = saved
			config.Rules = append(append([]model.ModelProfitCostRule{}, saved.Rules...), longRule)
			updated, err := model.SaveModelProfitCostRules(context.Background(), config)
			require.NoError(t, err)
			reloaded, err := model.GetModelProfitCostRules(context.Background())
			require.NoError(t, err)
			assert.Equal(t, updated, reloaded)
			for i, item := range []struct {
				submitted      int64
				resolution     string
				seconds, input float64
			}{
				{201, "768P", 30, 15}, {401, "768P", 30, 15}, {401, "768P", 14, 0}, {401, "2K", 16, 0},
			} {
				id := fmt.Sprintf("h3-long-cost-%d", i)
				require.NoError(t, db.Create(&model.Task{TaskID: id, UserId: 1, ChannelId: 16, SubmitTime: item.submitted, Status: model.TaskStatusSuccess}).Error)
				images := 0
				if i < 2 {
					images = 6
				}
				other, err := common.Marshal(map[string]any{"task_id": id, "is_task": true, "usage_facts": map[string]any{"seconds": item.seconds, "resolution": item.resolution, "input_video_seconds": item.input, "input_images": images, "long_video": true}})
				require.NoError(t, err)
				require.NoError(t, logs.Create(&model.Log{CreatedAt: 435, UserId: 1, ChannelId: 16, Type: model.LogTypeConsume, ModelName: "MiniMax-H3", Quota: 100, RequestId: id, Other: string(other)}).Error)
			}
			longReport, err := model.GetModelProfits(context.Background(), model.ModelProfitFilter{Start: 430, End: 440, ModelName: "MiniMax-H3"})
			require.NoError(t, err)
			assert.EqualValues(t, 2, longReport.Summary.KnownCostRequests)
			assert.EqualValues(t, 2, longReport.Summary.UnknownCostRequests)
			require.NotNil(t, longReport.Summary.UpstreamCostCNY)
			assert.InDelta(t, 10.68957, *longReport.Summary.UpstreamCostCNY, 1e-8)
		})
	}
}

func TestH3ReferenceMaterialSaleAndSettlement(t *testing.T) {
	const expression = `u("resolution") == "2K" ? tier("2K", u("seconds") * 0.0410959 + u("input_video_seconds") * (0.15 / 7.3) + max(u("input_images") - 5, 0) * (0.05 / 7.3)) : tier("768P", u("seconds") * 0.0410959 + u("input_video_seconds") * (0.09 / 7.3) + max(u("input_images") - 5, 0) * (0.05 / 7.3))`
	for _, tc := range []struct {
		name, resolution            string
		seconds, input, images, cny float64
	}{
		{"old basic price retained", "768P", 5, 0, 0, 1.50000035},
		{"2K basic retained", "2K", 5, 0, 0, 1.50000035},
		{"five reference images free", "2K", 5, 0, 5, 1.50000035},
		{"2K reference video", "2K", 5, 15, 0, 3.75000035},
		{"768P reference video", "768P", 5, 15, 0, 2.85000035},
		{"video and nine images", "2K", 5, 15, 9, 3.95000035},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := map[string]any{"seconds": tc.seconds, "resolution": tc.resolution, "input_video_seconds": tc.input, "input_images": tc.images}
			cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
			require.NoError(t, err)
			assert.InDelta(t, tc.cny, cost*7.3, 1e-8)
		})
	}
	initial := map[string]any{"seconds": float64(5), "resolution": "2K", "input_video_seconds": float64(15), "input_images": float64(9)}
	snapshot := &billingexpr.BillingSnapshot{ExprString: expression, ExprVersion: 1, TaskUsageBilling: true, QuotaPerUnit: 500000, GroupRatio: 1, UsageFacts: initial}
	settled, facts, err := service.EvaluateTaskCompletionUsage(snapshot, map[string]any{"input_video_seconds": float64(3), "input_images": float64(6)})
	require.NoError(t, err)
	assert.Equal(t, float64(15), snapshot.UsageFacts["input_video_seconds"], "original reservation stays frozen")
	assert.Equal(t, float64(3), facts["input_video_seconds"])
	assert.InDelta(t, 2.00000035, settled.ActualQuotaBeforeGroup/500000*7.3, 1e-8)
}
