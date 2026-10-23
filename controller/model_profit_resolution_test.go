package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
