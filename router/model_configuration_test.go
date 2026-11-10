package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unifiedConfigurationFixture(t *testing.T) *h3RouterFixture {
	t.Helper()
	common.OptionMapRWMutex.RLock()
	old := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = old; common.OptionMapRWMutex.Unlock() })
	f := newVideoQueueFixture(t, 1)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	f.engine = gin.New()
	f.engine.Use(middleware.RequestId())
	SetRouter(f.engine, WebAssets{})
	require.NoError(t, f.db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}))
	require.NoError(t, f.logDB.AutoMigrate(&model.AuditLog{}))
	t.Cleanup(func() { model.InvalidatePricingCache() })
	settings := config.GlobalConfig.ExportAllConfigs()
	restorers := []struct {
		value   string
		restore func(string) error
	}{
		{ratio_setting.ModelRatio2JSONString(), ratio_setting.UpdateModelRatioByJSONString},
		{ratio_setting.CompletionRatio2JSONString(), ratio_setting.UpdateCompletionRatioByJSONString},
		{ratio_setting.CacheRatio2JSONString(), ratio_setting.UpdateCacheRatioByJSONString},
		{ratio_setting.CreateCacheRatio2JSONString(), ratio_setting.UpdateCreateCacheRatioByJSONString},
		{ratio_setting.ImageRatio2JSONString(), ratio_setting.UpdateImageRatioByJSONString},
		{ratio_setting.AudioRatio2JSONString(), ratio_setting.UpdateAudioRatioByJSONString},
		{ratio_setting.AudioCompletionRatio2JSONString(), ratio_setting.UpdateAudioCompletionRatioByJSONString},
	}
	t.Cleanup(func() {
		for _, restore := range restorers {
			require.NoError(t, restore.restore(restore.value))
		}
		require.NoError(t, config.GlobalConfig.LoadFromDB(settings))
	})
	require.NoError(t, model.UpdateModelPricingOptions(map[string]string{"ModelPrice": ratio_setting.ModelPrice2JSONString()}))
	return f
}

func publishUnifiedFixture(t *testing.T, name string, change func(*model.ModelConfiguration)) model.PublishedModelConfiguration {
	t.Helper()
	snapshot, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 1)
	entry := snapshot.Entries[0]
	def := entry.Definition
	if def.Kind == "text" {
		def.ContextWindow = common.GetPointer(32768)
		def.MaxOutputTokens = common.GetPointer(4096)
	}
	def.Pricing = model.PricingValues{"ModelPrice": float64(0.01)}
	def.Source = "Local administrator specification"
	change(&def)
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, entry.BaseVersion, def))
	draft, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(draft.Revision, []string{name}, ""))
	current, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	return current[name]
}

func unifiedRequest(t *testing.T, f *h3RouterFixture, method, path, body, version string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-h3routeradmin")
	req.Header.Set("Content-Type", "application/json")
	if path == "/v1/images/generations" {
		req.Header.Set("Prefer", "respond-async")
	}
	if version != "" {
		req.Header.Set(service.ModelConfigurationHeader, version)
	}
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, req)
	return recorder
}

func TestUnifiedModelConfigurationDraftPublicationRollbackAndIsolation(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	channel := model.Channel{Id: 1100, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Text fixture", Models: "old-model,disabled-old", Group: "default", Key: "private-fixture"}
	require.NoError(t, f.db.Create(&channel).Error)
	require.NoError(t, f.db.Create(&model.Ability{ChannelId: 1100, Group: "default", Model: "old-model", Enabled: true}).Error)
	require.NoError(t, f.db.Create(&model.Ability{ChannelId: 1100, Group: "default", Model: "disabled-old", Enabled: false}).Error)
	first := publishUnifiedFixture(t, "dynamic-text", func(def *model.ModelConfiguration) {
		def.DisplayName = "Dynamic text"
		def.Enabled = true
		def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1100, Group: "default", UpstreamModel: "native-text", Enabled: true}}
	})
	var disabled model.Ability
	require.NoError(t, f.db.Where("model = ?", "disabled-old").First(&disabled).Error)
	assert.False(t, disabled.Enabled)
	current, err := model.GetModelConfigurationSnapshot("dynamic-text")
	require.NoError(t, err)
	require.NoError(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, current.Entries[0].Definition))
	assert.ErrorIs(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, current.Entries[0].Definition), model.ErrModelConfigurationConflict)
	// A saved draft has no public effect. Publishing stale routes/prices fails.
	version := model.ModelExecutionConfigurationVersion("dynamic-text", first)
	directory := unifiedRequest(t, f, "GET", "/v1/models", "", "")
	require.Equal(t, 200, directory.Code, directory.Body.String())
	assert.Contains(t, directory.Body.String(), version)
	assert.NotContains(t, directory.Body.String(), "private-fixture")
	assert.NotContains(t, directory.Body.String(), "channel_id")
	changed := publishUnifiedFixture(t, "dynamic-text", func(def *model.ModelConfiguration) {
		def.DisplayName = "Changed text"
		def.Pricing["ModelPrice"] = float64(0.02)
		def.MaxOutputTokens = common.GetPointer(2048)
	})
	assert.NotEqual(t, version, model.ModelExecutionConfigurationVersion("dynamic-text", changed))
	state, err := model.GetModelConfigurationSnapshot("")
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(state.Revision, nil, first.Version))
	restored, err := model.GetModelConfigurationSnapshot("dynamic-text")
	require.NoError(t, err)
	assert.Equal(t, "Dynamic text", restored.Entries[0].Definition.DisplayName)
	assert.NotEqual(t, first.Version, restored.Entries[0].Published.Version)
	_, err = model.GetModelConfigurationHistory(changed.Version)
	require.NoError(t, err)
	assert.Error(t, model.UpdateOption(model.ModelConfigurationKey, "{}"))
	assert.Error(t, model.UpdateOptionsBulk(map[string]string{strings.ToLower(model.ModelConfigurationHistoryPrefix) + first.Version: "{}"}))
	// Actual dashboard authentication, rather than context-role injection.
	root := model.User{Username: "configuration-root", AffCode: "ucroot", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, f.db.Create(&root).Error)
	login, err := service.CreateLoginSession(root.Id, "password", "127.0.0.1", "local-admin")
	require.NoError(t, err)
	request := httptest.NewRequest("GET", "/api/option/model_catalog", nil)
	request.Header.Set("Authorization", "Bearer "+login.AccessToken)
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	assert.Equal(t, 200, response.Code, response.Body.String())
	unauthorized := unifiedRequest(t, f, "GET", "/api/option/model_catalog", "", "")
	assert.NotEqual(t, 200, unauthorized.Code)
}

func TestUnifiedModelConfigurationMediaTaskQueueAndUnsentReceipts(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	policy := func(def *model.ModelConfiguration) {
		def.Kind = "video"
		def.Parameters = map[string]any{"type": "object", "properties": map[string]any{"seconds": map[string]any{"type": "integer", "enum": []any{float64(5), float64(10)}, "default": float64(5)}}}
	}
	first := publishUnifiedFixture(t, "MiniMax-H3", policy)
	version := model.ModelExecutionConfigurationVersion("MiniMax-H3", first)
	wallet, token := f.balance(t)
	fixtures := []map[string]any{}
	capture := func(name string, r *httptest.ResponseRecorder) {
		var body any
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &body))
		fixtures = append(fixtures, map[string]any{"name": name, "status": r.Code, "headers": r.Header(), "body": body, "observations": unifiedObservations(t, f)})
	}
	invalid := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"MiniMax-H3","prompt":"local invalid duration","seconds":15}`, version)
	require.Equal(t, 400, invalid.Code, invalid.Body.String())
	assert.Contains(t, invalid.Body.String(), "model_parameters_invalid")
	assert.Zero(t, f.posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	capture("invalid-parameters", invalid)
	envelope := `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000007101","operation_id":"00000000-0000-4000-8000-000000007101","request":{"model":"MiniMax-H3","prompt":"local accepted intent","seconds":5}}`
	queued := unifiedRequest(t, f, "POST", "/v1/video/queue", envelope, version)
	require.Equal(t, 202, queued.Code, queued.Body.String())
	capture("queued-before-update", queued)
	accepted := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"MiniMax-H3","prompt":"local accepted task","seconds":5}`, version)
	require.Equal(t, 200, accepted.Code, accepted.Body.String())
	assert.Equal(t, 1, f.posts)
	capture("accepted-before-update", accepted)
	var task model.Task
	require.NoError(t, f.db.Order("id DESC").First(&task).Error)
	publishUnifiedFixture(t, "MiniMax-H3", func(def *model.ModelConfiguration) {
		policy(def)
		def.DisplayName = "Updated video"
		def.Pricing["ModelPrice"] = float64(0.02)
	})
	stale := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"MiniMax-H3","prompt":"stale","seconds":5}`, version)
	require.Equal(t, 400, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "model_config_changed")
	assert.Equal(t, "rejected-before-billing", stale.Header().Get(service.ModelConfigurationResultHeader))
	assert.Equal(t, 1, f.posts)
	capture("stale-video", stale)
	published, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	replay := unifiedRequest(t, f, "POST", "/v1/video/queue", envelope, model.ModelExecutionConfigurationVersion("MiniMax-H3", published["MiniMax-H3"]))
	require.Equal(t, 200, replay.Code, replay.Body.String())
	assert.Contains(t, replay.Body.String(), version)
	assert.Equal(t, version, replay.Header().Get(service.ModelConfigurationHeader), "recovery echoes the original item, never the newer request version")
	capture("existing-operation-replay", replay)
	summary, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Dispatched)
	assert.Equal(t, 1, f.posts)
	item, err := model.GetVideoQueueOperation(context.Background(), 791, "00000000-0000-4000-8000-000000007101")
	require.NoError(t, err)
	assert.Equal(t, "not_submitted", item.SubmissionState)
	assert.Equal(t, "model_config_changed", item.ErrorCode)
	recovered := unifiedRequest(t, f, "GET", "/v1/tasks/"+task.TaskID, "", version)
	require.Equal(t, 200, recovered.Code, recovered.Body.String())
	assert.Equal(t, 1, f.posts)
	capture("original-task-readable", recovered)
	if dir := os.Getenv("UNIFIED_CONFIG_FIXTURE_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		encoded, err := common.Marshal(fixtures)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "media-http-fixtures.json"), encoded, 0600))
	}
}

func TestUnifiedModelConfigurationParameterAuthoringSafety(t *testing.T) {
	for _, key := range []string{"x-task-plugin", "x-image-receipt-recovery", "headers", "base_url", "transport"} {
		schema := map[string]any{"type": "object", "properties": map[string]any{}, key: map[string]any{"enabled": true}}
		assert.Error(t, model.ValidateModelParameterSchema("image", schema), fmt.Sprintf("host authority %s", key))
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"reference_videos": false, "frame_images": false}}
	for _, raw := range []string{
		`{"metadata":{"reference_video":["https://fixture.example/video.mp4"]}}`,
		`{"metadata":{"content":[{"type":"video_url","video_url":{"url":"https://fixture.example/video.mp4"}}]}}`,
		`{"metadata":{"first_frame_image":"https://fixture.example/frame.png"}}`,
	} {
		var request map[string]any
		require.NoError(t, common.UnmarshalJsonStr(raw, &request))
		assert.Error(t, model.ModelParameterValues(schema, request), raw)
	}
	assert.NoError(t, model.ModelParameterValues(schema, map[string]any{"prompt": "text only"}))
	var base, controls map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"type":"object","properties":{"seconds":{"type":"integer","enum":[5,10],"default":10},"resolution":{"type":"string","enum":["768P"]}},"oneOf":[{"properties":{"seconds":{"type":"integer","const":5}}},{"properties":{"seconds":{"type":"integer","const":10}}}],"x-reference-limits":{"images":9,"videos":3,"audios":3},"x-task-plugin":{"key":"fixture"}}`, &base))
	require.NoError(t, common.UnmarshalJsonStr(`{"type":"object","properties":{"seconds":{"type":"integer","enum":[5]},"reference_videos":false}}`, &controls))
	projected := model.ProjectModelConfigurationParameters(base, controls)
	assert.Len(t, projected["oneOf"], 1)
	assert.EqualValues(t, 0, projected["x-reference-limits"].(map[string]any)["videos"])
	assert.Equal(t, "fixture", projected["x-task-plugin"].(map[string]any)["key"])
	assert.Contains(t, projected["properties"], "resolution")
	assert.NotContains(t, projected["properties"].(map[string]any)["seconds"], "default")
	assert.Len(t, base["oneOf"], 2, "projection cannot mutate the adapter cache")
}

func TestUnifiedModelConfigurationCostHistoryAndSelectedRouteCAS(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	require.NoError(t, f.db.Create(&model.Channel{Id: 1110, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "cost-test", Group: "default", Name: "Cost fixture", Key: "synthetic-before"}).Error)
	old := model.ModelProfitCostRule{ChannelID: 1110, ModelName: "cost-test", Kind: "request", EffectiveFrom: time.Now().Unix() - 60, UnitCostCNY: common.GetPointer(0.05), Source: "Local synthetic cost evidence"}
	_, err := model.SaveModelProfitCostRules(context.Background(), model.ModelProfitCostRules{SchemaVersion: 1, Rules: []model.ModelProfitCostRule{old}})
	require.NoError(t, err)
	first := publishUnifiedFixture(t, "cost-test", func(def *model.ModelConfiguration) {
		def.Enabled = true
		def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1110, Group: "default", UpstreamModel: "native-cost", Enabled: true}}
		def.CostRules = []model.ModelProfitCostRule{old}
	})
	publishUnifiedFixture(t, "cost-test", func(def *model.ModelConfiguration) { def.CostRules[0].UnitCostCNY = common.GetPointer(0.10) })
	costs, err := model.GetModelProfitCostRules(context.Background())
	require.NoError(t, err)
	require.Len(t, costs.Rules, 2)
	assert.Equal(t, old, costs.Rules[0], "old procurement evidence is append-only")
	// A rollback creates a new effective record; it does not restore old rows.
	time.Sleep(time.Until(time.Unix(costs.Rules[1].EffectiveFrom+1, 0)))
	snapshot, err := model.GetModelConfigurationSnapshot("cost-test")
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, nil, first.Version))
	costs, err = model.GetModelProfitCostRules(context.Background())
	require.NoError(t, err)
	require.Len(t, costs.Rules, 3)
	assert.Equal(t, old, costs.Rules[0])
	assert.Equal(t, 0.05, *costs.Rules[2].UnitCostCNY)
	snapshot, err = model.GetModelConfigurationSnapshot("cost-test")
	require.NoError(t, err)
	entry := snapshot.Entries[0]
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, entry.BaseVersion, entry.Definition))
	before := model.ModelExecutionConfigurationVersion("cost-test", *entry.Published)
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 1110).Update("key", "synthetic-after").Error)
	assert.NotEqual(t, before, model.ModelExecutionConfigurationVersion("cost-test", *entry.Published))
	snapshot, err = model.GetModelConfigurationSnapshot("cost-test")
	require.NoError(t, err)
	assert.ErrorIs(t, model.PublishModelConfigurations(snapshot.Revision, []string{"cost-test"}, ""), model.ErrModelConfigurationConflict)
	assert.Zero(t, f.posts)
}

func writeUnifiedFixtures(t *testing.T, name string, fixtures []map[string]any) {
	t.Helper()
	if dir := os.Getenv("UNIFIED_CONFIG_FIXTURE_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		encoded, err := common.Marshal(fixtures)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), encoded, 0600))
	}
}

func unifiedObservations(t *testing.T, f *h3RouterFixture) map[string]any {
	t.Helper()
	wallet, token := f.balance(t)
	var consumes, tasks int64
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumes).Error)
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	return map[string]any{"provider_post_count": f.posts, "consume_count": consumes, "user_quota": wallet, "token_quota": token, "task_count": tasks}
}

func unifiedHTTPFixture(t *testing.T, f *h3RouterFixture, name string, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body any
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	return map[string]any{"name": name, "status": response.Code, "headers": response.Header(), "body": body, "observations": unifiedObservations(t, f)}
}

func TestUnifiedModelConfigurationCatalogTransitionFixtures(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	require.NoError(t, f.db.Create(&model.Channel{Id: 1105, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "legacy-only", Group: "default", Name: "Catalog transition fixture", Key: "synthetic-catalog-key"}).Error)
	records := []map[string]any{}
	capture := func(stage string) {
		for _, endpoint := range []struct{ name, path string }{{"models", "/v1/models"}, {"pricing", "/api/pricing"}} {
			response := unifiedRequest(t, f, "GET", endpoint.path, "", "")
			require.Equal(t, 200, response.Code, response.Body.String())
			var body any
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			records = append(records, map[string]any{"name": "catalog-" + stage + "-" + endpoint.name, "method": "GET", "path": endpoint.path, "status": response.Code, "headers": response.Header(), "body": body, "observations": unifiedObservations(t, f)})
		}
	}
	capture("before-publish")
	initial, err := model.GetModelConfigurationSnapshot("catalog-new-text")
	require.NoError(t, err)
	entry := initial.Entries[0]
	def := entry.Definition
	def.DisplayName = "Catalog new text"
	def.Enabled = true
	def.Source = "Synthetic local capability evidence; not a supplier maximum"
	def.ContextWindow = common.GetPointer(20480)
	def.MaxOutputTokens = common.GetPointer(2048)
	def.Pricing = model.PricingValues{"ModelPrice": float64(0.01)}
	def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1105, Group: "default", UpstreamModel: "native-catalog-text", Enabled: true}}
	require.NoError(t, model.SaveModelConfigurationDraft(initial.Revision, entry.BaseVersion, def))
	capture("draft")
	snapshot, err := model.GetModelConfigurationSnapshot(def.ModelName)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{def.ModelName}, ""))
	capture("published")
	updated, err := model.GetModelConfigurationSnapshot(def.ModelName)
	require.NoError(t, err)
	def = updated.Entries[0].Definition
	def.MaxOutputTokens = common.GetPointer(1024)
	def.Pricing["ModelPrice"] = float64(0.02)
	require.NoError(t, model.SaveModelConfigurationDraft(updated.Revision, updated.Entries[0].BaseVersion, def))
	snapshot, err = model.GetModelConfigurationSnapshot(def.ModelName)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{def.ModelName}, ""))
	capture("updated")
	writeUnifiedFixtures(t, "catalog-http-fixtures.json", records)
	assert.Zero(t, f.posts)
}

func TestUnifiedModelConfigurationNewMediaOnIntegratedAdapters(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	t.Cleanup(func() { operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay })
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	nativeImage := "ycapi-doubao-seedream-5-0-pro"
	base := "https://ycapi.fixture"
	imageChannel := model.Channel{Id: 1106, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "Integrated image fixture", Models: nativeImage, Group: "default", Key: "synthetic-integrated-image", BaseURL: &base}
	imageChannel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	require.NoError(t, imageChannel.Insert())
	require.NoError(t, f.db.Create(&model.Channel{Id: 1107, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "legacy-text", Group: "default", Name: "Combined text fixture", Key: "synthetic-combined-text"}).Error)
	publishUnifiedFixture(t, "catalog-new-text", func(def *model.ModelConfiguration) {
		def.DisplayName = "Catalog new text"
		def.Enabled = true
		def.ContextWindow = common.GetPointer(131072)
		def.MaxOutputTokens = common.GetPointer(4096)
		def.Source = "Synthetic local provider accepts 131072/4096 for full-tool interop; not product or native supplier capacity"
		def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1107, Group: "default", UpstreamModel: "native-catalog-text", Enabled: true}}
	})
	for _, entry := range []struct {
		name, kind, native string
		channel            int
	}{{"catalog-new-image", "image", nativeImage, 1106}, {"catalog-new-video", "video", "MiniMax-H3", 790}} {
		publishUnifiedFixture(t, entry.name, func(def *model.ModelConfiguration) {
			def.Kind = entry.kind
			def.ContextWindow = nil
			def.MaxOutputTokens = nil
			def.DisplayName = entry.name
			def.Enabled = true
			def.Routes = []model.ModelConfigurationRoute{{ChannelID: entry.channel, Group: "default", UpstreamModel: entry.native, Enabled: true}}
			if entry.kind == "video" {
				def.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("local-video",u("seconds")*0.01)`}
				def.Parameters = map[string]any{"type": "object", "properties": map[string]any{
					"seconds":    map[string]any{"type": "integer", "enum": []any{float64(5), float64(10)}},
					"resolution": map[string]any{"type": "string", "enum": []any{"768P"}},
					"ratio":      map[string]any{"type": "string", "enum": []any{"16:9"}},
				}, "oneOf": []any{map[string]any{"type": "object", "properties": map[string]any{
					"seconds":    map[string]any{"type": "integer", "enum": []any{float64(5), float64(10)}},
					"resolution": map[string]any{"type": "string", "const": "768P"},
					"ratio":      map[string]any{"type": "string", "const": "16:9"},
				}}}}
			}
		})
	}
	require.NoError(t, model.SaveClientModelConfig(model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "catalog-new-text", ModelOrder: []string{}}))
	require.NoError(t, model.SaveClientMediaDefaults(model.ClientMediaDefaults{SchemaVersion: 1, DefaultImageModel: "catalog-new-image", DefaultVideoModel: "catalog-new-video"}))
	require.NoError(t, model.RefreshMediaConcurrencyPools(context.Background()))
	records := []map[string]any{}
	for _, path := range []string{"/v1/models", "/api/pricing", "/api/status"} {
		response := unifiedRequest(t, f, "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		if path != "/api/status" {
			assert.Contains(t, response.Body.String(), "catalog-new-image")
			assert.Contains(t, response.Body.String(), "catalog-new-video")
			assert.Contains(t, response.Body.String(), "catalog-new-text")
			assert.Contains(t, response.Body.String(), "x-image-receipt-recovery")
			assert.Contains(t, response.Body.String(), "x-task-plugin")
			var catalog struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &catalog))
			for _, entry := range catalog.Data {
				id, _ := entry["id"].(string)
				if id == "" {
					id, _ = entry["model_name"].(string)
				}
				if id != "catalog-new-image" && id != "catalog-new-video" {
					continue
				}
				rows, _ := entry["retail_price_rows"].([]any)
				want := 16
				if id == "catalog-new-video" {
					want = 2
				}
				require.Len(t, rows, want, "every declared complete specification has an actual engine-derived tariff")
				for _, raw := range rows {
					price := raw.(map[string]any)
					assert.Equal(t, fmt.Sprint(0.01*operation_setting.USDExchangeRate), price["retail_cny"])
					params := price["params"].(map[string]any)
					assert.Contains(t, params, "resolution")
					if id == "catalog-new-image" {
						assert.Contains(t, params, "size")
					} else {
						assert.Contains(t, params, "seconds")
						assert.Equal(t, "16:9", params["ratio"])
					}
				}
			}
		}
		record := unifiedHTTPFixture(t, f, "new-media-"+strings.ReplaceAll(path, "/", "-"), response)
		record["path"] = path
		records = append(records, record)
	}
	videoRecords, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	videoVersion := model.ModelExecutionConfigurationVersion("catalog-new-video", videoRecords["catalog-new-video"])

	// Display currency is part of the effective version, exactly as at runtime.
	videoVersion = model.ModelExecutionConfigurationVersion("catalog-new-video", videoRecords["catalog-new-video"])
	quote := unifiedRequest(t, f, "POST", "/v1/video/quote", `{"model":"catalog-new-video","works_count":1,"segments":[{"id":"new-video","parameters":{"seconds":5,"duration":5,"resolution":"768P","size":"768P","ratio":"16:9","aspect_ratio":"16:9","metadata":{"ratio":"16:9"}},"reference_counts":{"images":0,"videos":0,"audios":0}}]}`, videoVersion)
	require.Equal(t, 200, quote.Code, quote.Body.String())
	records = append(records, unifiedHTTPFixture(t, f, "new-video-quote", quote))
	created := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"catalog-new-video","prompt":"new video local fixture","seconds":5,"duration":5,"resolution":"768P","size":"768P","ratio":"16:9","aspect_ratio":"16:9","metadata":{"ratio":"16:9"}}`, videoVersion)
	require.Equal(t, 200, created.Code, created.Body.String())
	assert.Equal(t, 1, f.posts)
	createdRecord := unifiedHTTPFixture(t, f, "new-video-created", created)
	createdRecord["native_request"] = f.bodies[0]
	records = append(records, createdRecord)
	assert.Equal(t, "MiniMax-H3", f.bodies[0]["model"])
	assert.Equal(t, "768P", f.bodies[0]["resolution"])
	assert.Equal(t, "16:9", f.bodies[0]["ratio"])
	assert.EqualValues(t, 5, f.bodies[0]["duration"])
	id := created.Header().Get("X-New-Api-Task-Id")
	read := unifiedRequest(t, f, "GET", "/v1/tasks/"+id, "", "")
	require.Equal(t, 200, read.Code, read.Body.String())
	records = append(records, unifiedHTTPFixture(t, f, "new-video-canonical-task-read", read))
	// Adding a model on the H3 channel must retain its original admission binding.
	before := f.occupied(t)
	original := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"MiniMax-H3","prompt":"old model after channel extension","seconds":5}`, "")
	require.Equal(t, 200, original.Code, original.Body.String())
	assert.Equal(t, before+1, f.occupied(t))
	assert.Equal(t, 2, f.posts)
	// The client's generic alias DTO uses metadata reference lists rather than
	// hard-coded MiniMax model detection. Verify the actual native body.
	ref := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"catalog-new-video","prompt":"native alias references","seconds":5,"duration":5,"resolution":"768P","size":"768P","ratio":"16:9","aspect_ratio":"16:9","metadata":{"ratio":"16:9","content":[{"type":"text","text":"native alias references"},{"type":"image_url","image_url":{"url":"https://fixture.example/ref.png"},"role":"reference_image"}],"reference_video":["https://fixture.example/ref.mp4"],"reference_audio":["https://fixture.example/ref.mp3"]}}`, videoVersion)
	require.Equal(t, 200, ref.Code, ref.Body.String())
	require.Len(t, f.bodies, 3)
	encodedNative, err := common.Marshal(f.bodies[2])
	require.NoError(t, err)
	for _, expected := range []string{"reference_image", "reference_video", "reference_audio", "https://fixture.example/ref.png", "https://fixture.example/ref.mp4", "https://fixture.example/ref.mp3"} {
		assert.Contains(t, string(encodedNative), expected)
	}
	referenceRecord := unifiedHTTPFixture(t, f, "new-video-generic-reference-grammar", ref)
	referenceRecord["native_request"] = f.bodies[2]
	records = append(records, referenceRecord)
	upstream := service.GetHttpClient().Transport
	var nativeImageBody map[string]any
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	protected := service.GetSSRFProtectedHTTPClient()
	priorProtectedTransport := protected.Transport
	t.Cleanup(func() { protected.Transport = priorProtectedTransport })
	protected.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "https://8.8.8.8/new-alias.png", request.URL.String())
		require.Equal(t, "GET", request.Method)
		assert.Empty(t, request.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}, "Content-Length": []string{fmt.Sprint(imageBytes.Len())}}, Body: io.NopCloser(bytes.NewReader(imageBytes.Bytes())), ContentLength: int64(imageBytes.Len()), Request: request}, nil
	})
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "ycapi.fixture" {
			return upstream.RoundTrip(request)
		}
		var body map[string]any
		var id any
		if request.Method == "POST" {
			f.posts++
			require.NoError(t, common.DecodeJson(request.Body, &body))
			assert.Equal(t, "doubao-seedream-5-0-pro", body["model_id"])
			nativeImageBody = body
			id = body["client_request_id"]
		} else {
			parts := strings.Split(request.URL.Path, "/")
			id = parts[len(parts)-1]
		}
		encoded, err := common.Marshal(map[string]any{"task_id": id, "model": "doubao-seedream-5-0-pro", "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://8.8.8.8/new-alias.png"}}})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})
	imageRecord := videoRecords["catalog-new-image"]
	imageVersion := model.ModelExecutionConfigurationVersion("catalog-new-image", imageRecord)
	image := unifiedRequest(t, f, "POST", "/v1/images/generations", `{"model":"catalog-new-image","prompt":"new image local fixture","resolution":"1k","size":"1:1","n":1}`, imageVersion)
	require.Equal(t, 202, image.Code, image.Body.String())
	createdImageRecord := unifiedHTTPFixture(t, f, "new-image-created", image)
	createdImageRecord["native_request"] = nativeImageBody
	records = append(records, createdImageRecord)
	imageID := image.Header().Get("X-New-Api-Task-Id")
	require.True(t, strings.HasPrefix(imageID, "task_"))
	for range 2 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	imageRead := unifiedRequest(t, f, "GET", "/v1/tasks/"+imageID, "", "")
	require.Equal(t, 200, imageRead.Code, imageRead.Body.String())
	assert.Contains(t, imageRead.Body.String(), "SUCCESS")
	assert.Equal(t, 4, f.posts, "polling never resubmits a generated task")
	records = append(records, unifiedHTTPFixture(t, f, "new-image-completed-canonical-task", imageRead))
	artifacts := unifiedRequest(t, f, "GET", "/v1/tasks/"+imageID+"/artifacts", "", "")
	require.Equal(t, 200, artifacts.Code, artifacts.Body.String())
	artifactRecord := unifiedHTTPFixture(t, f, "new-image-artifacts", artifacts)
	artifactRecord["path"] = "/v1/tasks/" + imageID + "/artifacts"
	records = append(records, artifactRecord)
	content := unifiedRequest(t, f, "GET", "/v1/tasks/"+imageID+"/artifacts/image/content", "", "")
	require.Equal(t, 200, content.Code, content.Body.String())
	assert.Equal(t, imageBytes.Bytes(), content.Body.Bytes())
	records = append(records, map[string]any{"name": "new-image-artifact-content", "path": "/v1/tasks/" + imageID + "/artifacts/image/content", "status": content.Code, "headers": content.Header(), "body_base64": base64.StdEncoding.EncodeToString(content.Body.Bytes()), "observations": unifiedObservations(t, f)})
	publishUnifiedFixture(t, "catalog-new-text", func(def *model.ModelConfiguration) {
		def.ContextWindow = common.GetPointer(131072)
		def.MaxOutputTokens = common.GetPointer(3072)
		def.Source = "Synthetic local provider accepts 131072/3072 for second-turn interop; not a product or native supplier limit"
	})
	for _, path := range []string{"/v1/models", "/api/pricing", "/api/status"} {
		response := unifiedRequest(t, f, "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		record := unifiedHTTPFixture(t, f, "combined-after-update-"+strings.ReplaceAll(path, "/", "-"), response)
		record["path"] = path
		records = append(records, record)
	}
	assert.Equal(t, 4, f.posts)
	publishUnifiedFixture(t, "catalog-new-image", func(def *model.ModelConfiguration) { def.Pricing["ModelPrice"] = float64(0.02) })
	publishUnifiedFixture(t, "catalog-new-video", func(def *model.ModelConfiguration) {
		def.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("local-video",u("seconds")*0.02)`}
	})
	for _, path := range []string{"/v1/models", "/api/pricing"} {
		response := unifiedRequest(t, f, "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		var catalog struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &catalog))
		for _, entry := range catalog.Data {
			id, _ := entry["id"].(string)
			if id == "" {
				id, _ = entry["model_name"].(string)
			}
			if id != "catalog-new-image" && id != "catalog-new-video" {
				continue
			}
			prices, _ := entry["retail_price_rows"].([]any)
			require.NotEmpty(t, prices)
			for _, raw := range prices {
				assert.Equal(t, fmt.Sprint(0.02*operation_setting.USDExchangeRate), raw.(map[string]any)["retail_cny"])
			}
		}
		record := unifiedHTTPFixture(t, f, "combined-after-price-update-"+strings.ReplaceAll(path, "/", "-"), response)
		record["path"] = path
		records = append(records, record)
	}
	current, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	currentVersion := model.ModelExecutionConfigurationVersion("catalog-new-video", current["catalog-new-video"])
	updatedQuote := unifiedRequest(t, f, "POST", "/v1/video/quote", `{"model":"catalog-new-video","works_count":1,"segments":[{"id":"after-price","parameters":{"seconds":5,"resolution":"768P","ratio":"16:9"},"reference_counts":{"images":0,"videos":0,"audios":0}}]}`, currentVersion)
	require.Equal(t, 200, updatedQuote.Code, updatedQuote.Body.String())
	records = append(records, unifiedHTTPFixture(t, f, "new-video-quote-after-price-update", updatedQuote))
	assert.Equal(t, 4, f.posts, "catalog tariff calculation and changes never submit a generation")
	writeUnifiedFixtures(t, "new-media-catalog-http-fixtures.json", records)
}

func TestUnifiedModelConfigurationExistingH3AdmissionCompletesAfterChannelExtension(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	accepted := unifiedRequest(t, f, "POST", "/v1/videos", `{"model":"MiniMax-H3","prompt":"original paid task before local catalog edit","seconds":5}`, "")
	require.Equal(t, 200, accepted.Code, accepted.Body.String())
	id := accepted.Header().Get("X-New-Api-Task-Id")
	task := f.task(t, id)
	require.Equal(t, 1, f.posts)
	require.Equal(t, 1, f.occupied(t))
	originalBill, err := common.Marshal(task.PrivateData.BillingContext)
	require.NoError(t, err)
	wallet, token := f.balance(t)
	records := []map[string]any{unifiedHTTPFixture(t, f, "old-h3-accepted-before-model-addition", accepted)}
	publishUnifiedFixture(t, "local-later-video", func(def *model.ModelConfiguration) {
		def.Kind = "video"
		def.ContextWindow = nil
		def.MaxOutputTokens = nil
		def.Enabled = true
		def.Routes = []model.ModelConfigurationRoute{{ChannelID: 790, Group: "default", UpstreamModel: "MiniMax-H3", Enabled: true}}
	})
	assert.Equal(t, 1, f.occupied(t))
	assert.Equal(t, 1, f.posts)
	readable := unifiedRequest(t, f, "GET", "/v1/tasks/"+id, "", "")
	require.Equal(t, 200, readable.Code, readable.Body.String())
	records = append(records, unifiedHTTPFixture(t, f, "old-h3-readable-after-model-addition", readable))
	originalTransport := service.GetHttpClient().Transport
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != "GET" {
			return originalTransport.RoundTrip(request)
		}
		require.Equal(t, "metaso.cn", request.URL.Host)
		body, err := common.Marshal(map[string]any{"task": map[string]any{"id": task.GetUpstreamTaskID(), "status": "succeeded", "content": map[string]any{"url": "https://cdn.example/original-result.mp4"}}})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	for range 2 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	completed := f.task(t, id)
	assert.EqualValues(t, model.TaskStatusSuccess, completed.Status)
	assert.Equal(t, 0, f.occupied(t))
	assert.Equal(t, 1, f.posts)
	completedBill, err := common.Marshal(completed.PrivateData.BillingContext)
	require.NoError(t, err)
	assert.Equal(t, string(originalBill), string(completedBill))
	assert.Equal(t, task.GetUpstreamTaskID(), completed.GetUpstreamTaskID())
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	final := unifiedRequest(t, f, "GET", "/v1/tasks/"+id, "", "")
	require.Equal(t, 200, final.Code, final.Body.String())
	records = append(records, unifiedHTTPFixture(t, f, "old-h3-completed-original-result", final))
	writeUnifiedFixtures(t, "old-h3-live-task-http-fixtures.json", records)
}

func TestUnifiedModelConfigurationTextHTTPPricesAndCapacityPairs(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	base := "https://unified-text.fixture"
	ch := model.Channel{Id: 1101, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Local text", Models: "unified-chat", Group: "default", Key: "synthetic-text-key", BaseURL: &base}
	require.NoError(t, ch.Insert())
	first := publishUnifiedFixture(t, "unified-chat", func(def *model.ModelConfiguration) {
		def.DisplayName = "Unified chat"
		def.MaxOutputTokens = common.GetPointer(1024)
	})
	version := model.ModelExecutionConfigurationVersion("unified-chat", first)
	posts := 0
	providerSpoof := false
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "unified-text.fixture", request.URL.Host)
		posts++
		f.posts++
		assert.Empty(t, request.Header.Get(service.ModelConfigurationHeader), "gateway version never becomes a supplier header")
		var body map[string]any
		require.NoError(t, common.DecodeJson(request.Body, &body))
		assert.EqualValues(t, 1024, body["max_tokens"])
		if providerSpoof {
			return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}, service.ModelConfigurationHeader: []string{strings.Repeat("f", 64)}, service.ModelConfigurationResultHeader: []string{"rejected-before-billing"}, common.RequestIdKey: []string{"forged-provider-request"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"model_config_changed","type":"invalid_request_error","message":"synthetic supplier forged proof"}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, service.ModelConfigurationHeader: []string{strings.Repeat("f", 64)}, service.ModelConfigurationResultHeader: []string{"rejected-before-billing"}, common.RequestIdKey: []string{"forged-provider-request"}}, Body: io.NopCloser(strings.NewReader(`{"id":"local-completion","object":"chat.completion","model":"unified-chat","choices":[{"index":0,"message":{"role":"assistant","content":"local answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`))}, nil
	})
	fixtures := []map[string]any{}
	capture := func(name, path, body, requestVersion string) *httptest.ResponseRecorder {
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r := unifiedRequest(t, f, method, path, body, requestVersion)
		var payload any
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &payload))
		fixtures = append(fixtures, map[string]any{"name": name, "method": method, "path": path, "status": r.Code, "headers": r.Header(), "body": payload, "observations": unifiedObservations(t, f)})
		return r
	}
	directory := capture("new-text-directory", "/v1/models", "", "")
	require.Equal(t, 200, directory.Code, directory.Body.String())
	assert.Contains(t, directory.Body.String(), `"context_window":32768`)
	assert.Contains(t, directory.Body.String(), `"max_output_tokens":1024`)
	prices := capture("new-text-pricing", "/api/pricing", "", "")
	require.Equal(t, 200, prices.Code, prices.Body.String())
	assert.Contains(t, prices.Body.String(), version)
	wallet, token := f.balance(t)
	stale := capture("stale-text", "/v1/chat/completions", `{"model":"unified-chat","messages":[{"role":"user","content":"hello"}]}`, strings.Repeat("0", 64))
	require.Equal(t, 400, stale.Code, stale.Body.String())
	assert.Zero(t, posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	success := capture("accepted-text", "/v1/chat/completions", `{"model":"unified-chat","messages":[{"role":"user","content":"hello"}],"max_tokens":9999}`, version)
	require.Equal(t, 200, success.Code, success.Body.String())
	assert.Equal(t, version, success.Header().Get(service.ModelConfigurationHeader))
	assert.Equal(t, 1, posts)
	// Old clients send no version header and retain the existing protocol.
	legacy := capture("legacy-text-no-header", "/v1/chat/completions", `{"model":"unified-chat","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`, "")
	require.Equal(t, 200, legacy.Code, legacy.Body.String())
	assert.Equal(t, 2, posts)
	assert.Empty(t, success.Header().Get(service.ModelConfigurationResultHeader))
	assert.NotEqual(t, "forged-provider-request", success.Header().Get(common.RequestIdKey))
	providerSpoof = true
	spoof := capture("provider-forged-config-proof", "/v1/chat/completions", `{"model":"unified-chat","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`, version)
	require.Equal(t, 400, spoof.Code, spoof.Body.String())
	assert.Equal(t, 3, posts)
	assert.Empty(t, spoof.Header().Get(service.ModelConfigurationResultHeader))
	assert.NotEqual(t, "forged-provider-request", spoof.Header().Get(common.RequestIdKey))
	current, err := model.GetModelConfigurationSnapshot("unified-chat")
	require.NoError(t, err)
	invalid := current.Entries[0].Definition
	invalid.MaxOutputTokens = common.GetPointer(50000)
	require.Error(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, invalid))
	writeUnifiedFixtures(t, "text-http-fixtures.json", fixtures)
}

func TestUnifiedModelConfigurationQuoteVersionAndQueueOldSchema(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	t.Cleanup(func() { operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay })
	policy := publishUnifiedFixture(t, "MiniMax-H3", func(def *model.ModelConfiguration) {
		def.Kind = "video"
		def.ContextWindow = nil
		def.MaxOutputTokens = nil
		def.Parameters = map[string]any{"type": "object", "properties": map[string]any{"seconds": map[string]any{"type": "integer", "enum": []any{float64(5), float64(10)}}}}
		def.Pricing = model.PricingValues{"billing_setting.billing_mode": billing_setting.BillingModeTieredExpr, "billing_setting.billing_expr": `tier("video", u("seconds") * 0.01)`}
	})
	version := model.ModelExecutionConfigurationVersion("MiniMax-H3", policy)
	quote := unifiedRequest(t, f, "POST", "/v1/video/quote", `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5,"resolution":"768P"},"reference_counts":{"images":0,"videos":0,"audios":0}}]}`, version)
	require.Equal(t, 200, quote.Code, quote.Body.String())
	assert.Contains(t, quote.Body.String(), `"model_config_version":"`+version+`"`)
	assert.Zero(t, f.posts)
	var payload any
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &payload))
	writeUnifiedFixtures(t, "quote-http-fixtures.json", []map[string]any{unifiedHTTPFixture(t, f, "managed-video-quote", quote)})
	// Recreate the released queue schema by removing only the new optional
	// column, retain an old accepted intent, then run the real migration twice.
	require.NoError(t, f.db.Migrator().DropColumn(&model.VideoQueueItem{}, "ModelConfigVersion"))
	old := model.VideoQueueItem{QueueID: "vq_00000000-0000-4000-8000-000000007201", UserID: 791, TokenID: 791, OperationID: "old-schema-intent", BodySHA256: strings.Repeat("0", 64), RequestBody: `{"model":"MiniMax-H3","seconds":5}`, Model: "MiniMax-H3", Count: 1, DurationSeconds: 5, TaskID: model.GenerateTaskID(), State: model.VideoQueueQueued, SubmissionState: "not_submitted"}
	require.NoError(t, f.db.Omit("ModelConfigVersion").Create(&old).Error)
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(&model.VideoQueueItem{}, &model.VideoQueueGuard{}))
	}
	restored, err := model.GetVideoQueueOperation(context.Background(), 791, old.OperationID)
	require.NoError(t, err)
	assert.Equal(t, old.TaskID, restored.TaskID)
	assert.Empty(t, restored.ModelConfigVersion)
	assert.False(t, restored.SendFenced)
}

// Opt-in local browser fixture; no production credentials, data or transport.
func TestUnifiedModelConfigurationPreview(t *testing.T) {
	address := os.Getenv("UNIFIED_CONFIG_PREVIEW_ADDR")
	if address == "" {
		t.Skip("local preview not requested")
	}
	require.True(t, strings.HasPrefix(address, "127.0.0.1:"))
	f := unifiedConfigurationFixture(t)
	oldPhone := common.PhoneLoginEnabled
	common.PhoneLoginEnabled = true
	t.Cleanup(func() { common.PhoneLoginEnabled = oldPhone })
	oldSetup, oldSecure := constant.Setup, common.SessionCookieSecure
	constant.Setup, common.SessionCookieSecure = true, false
	t.Cleanup(func() { constant.Setup, common.SessionCookieSecure = oldSetup, oldSecure })
	require.NoError(t, f.db.AutoMigrate(&model.TwoFA{}, &model.PasskeyCredential{}, &model.AuthFlow{}, &model.PhoneLoginGrant{}, &model.UserOnboardingSurvey{}, &model.OnboardingSurveyConfigVersion{}, &model.OnboardingSurveyConfigState{}))
	require.NoError(t, model.InitializeSurveyConfiguration())
	hash, err := common.HashAccountPassword("LocalPreview-2026!39")
	require.NoError(t, err)
	require.NoError(t, f.db.Create(&model.User{Username: "unified-preview", Password: hash, AffCode: "uprev", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}).Error)
	publishUnifiedFixture(t, "MiniMax-H3", func(def *model.ModelConfiguration) {
		def.Kind = "video"
		def.ContextWindow = nil
		def.MaxOutputTokens = nil
		def.Parameters = map[string]any{"type": "object", "properties": map[string]any{"seconds": map[string]any{"type": "integer", "enum": []any{float64(5), float64(10)}}}}
	})
	if file := os.Getenv("UNIFIED_CONFIG_PREVIEW_PRICING_FILE"); file != "" {
		type publicPriceRow struct {
			ModelName string `json:"model_name"`
			Variants  []struct {
				Key        string `json:"plugin_key"`
				Expression string `json:"billing_expr"`
			} `json:"billing_plugin_variants"`
		}
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		var rows []publicPriceRow
		if common.Unmarshal(data, &rows) != nil {
			var envelope struct {
				Data []publicPriceRow `json:"data"`
			}
			require.NoError(t, common.Unmarshal(data, &envelope))
			rows = envelope.Data
		}
		variants := map[string]any{}
		for _, row := range rows {
			if row.ModelName == "MiniMax-H3" {
				for _, price := range row.Variants {
					variants[price.Key] = price.Expression
				}
			}
		}
		require.NotEmpty(t, variants, "preview must load an explicit tariff, never substitute a zero template")
		publishUnifiedFixture(t, "MiniMax-H3", func(def *model.ModelConfiguration) {
			def.Kind = "video"
			def.ContextWindow, def.MaxOutputTokens, def.Parameters = nil, nil, nil
			def.Pricing = model.PricingValues{"billing_setting.plugin_billing_expr": variants}
			def.Source = "Local preview only: existing tariff copied from a read-only historical public catalog; accounts and task transport are synthetic"
		})
	}
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	server := &http.Server{Handler: f.engine, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	t.Logf("Local disposable preview listening on %s", address)
	time.Sleep(30 * time.Minute)
}

func TestUnifiedModelConfigurationImageSubmitAndChangedBeforeAdmission(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	name := "ycapi-doubao-seedream-5-0-pro"
	base := "https://ycapi.fixture"
	ch := model.Channel{Id: 1102, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "Local image", Models: name, Group: "default", Key: "synthetic-image-key", BaseURL: &base}
	ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	require.NoError(t, ch.Insert())
	require.NoError(t, model.RefreshMediaConcurrencyPools(context.Background()))
	first := publishUnifiedFixture(t, name, func(def *model.ModelConfiguration) {
		def.Kind = "image"
		def.ContextWindow = nil
		def.MaxOutputTokens = nil
		def.Parameters = map[string]any{"type": "object", "properties": map[string]any{"resolution": map[string]any{"type": "string", "enum": []any{"1k"}}, "n": map[string]any{"type": "integer", "enum": []any{float64(1)}}}}
	})
	version := model.ModelExecutionConfigurationVersion(name, first)
	posts := 0
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "ycapi.fixture", request.URL.Host)
		if request.Method == "POST" {
			posts++
			f.posts++
		}
		var body map[string]any
		if request.Method == "POST" {
			require.NoError(t, common.DecodeJson(request.Body, &body))
		}
		id := body["client_request_id"]
		if request.Method != "POST" {
			parts := strings.Split(request.URL.Path, "/")
			id = parts[len(parts)-1]
		}
		encoded, err := common.Marshal(map[string]any{"task_id": id, "model": "doubao-seedream-5-0-pro", "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/local-image.png"}}})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})
	var records []map[string]any
	invalid := unifiedRequest(t, f, "POST", "/v1/images/generations", `{"model":"ycapi-doubao-seedream-5-0-pro","prompt":"local invalid spec","resolution":"2k","size":"1:1","n":1}`, version)
	records = append(records, unifiedHTTPFixture(t, f, "invalid-image-parameters", invalid))
	require.Equal(t, 400, invalid.Code, invalid.Body.String())
	assert.Zero(t, posts)
	accepted := unifiedRequest(t, f, "POST", "/v1/images/generations", `{"model":"ycapi-doubao-seedream-5-0-pro","prompt":"local image","resolution":"1k","size":"1:1","n":1}`, version)
	records = append(records, unifiedHTTPFixture(t, f, "accepted-image", accepted))
	require.Equal(t, 202, accepted.Code, accepted.Body.String())
	assert.Equal(t, 1, posts)
	var task model.Task
	require.NoError(t, f.db.Order("id DESC").First(&task).Error)
	require.NotEmpty(t, task.PrivateData.MediaAdmissionID)
	require.NotEmpty(t, task.PrivateData.Key)
	require.NotEmpty(t, task.PrivateData.RouteDigest)
	wallet, token := f.balance(t)
	originalEngine := f.engine
	engine := gin.New()
	engine.Use(middleware.RequestId())
	engine.Use(func(c *gin.Context) {
		if c.Request.Method == "POST" {
			require.Nil(t, service.CaptureModelConfiguration(c, name))
			publishUnifiedFixture(t, name, func(def *model.ModelConfiguration) {
				def.DisplayName = "Changed during preparation"
				def.Pricing["ModelPrice"] = float64(0.02)
			})
		}
		c.Next()
	})
	SetRouter(engine, WebAssets{})
	f.engine = engine
	changed := unifiedRequest(t, f, "POST", "/v1/images/generations", `{"model":"ycapi-doubao-seedream-5-0-pro","prompt":"configuration changed while preparing","resolution":"1k","size":"1:1","n":1}`, version)
	records = append(records, unifiedHTTPFixture(t, f, "changed-during-image-preparation", changed))
	require.Equal(t, 400, changed.Code, changed.Body.String())
	assert.Contains(t, changed.Body.String(), "model_config_changed")
	assert.Empty(t, changed.Header().Get(service.ModelConfigurationResultHeader), "an allocated task keeps its original full settlement receipt")
	assert.Equal(t, 1, posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	currentChannel, err := model.GetChannelById(ch.Id, true)
	require.NoError(t, err)
	require.True(t, model.MediaTaskRouteMatches(&task, currentChannel), "original route identity must survive unified publication")
	for range 2 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	require.NoError(t, f.db.First(&task, task.ID).Error)
	require.EqualValues(t, model.TaskStatusSuccess, task.Status)
	f.engine = originalEngine
	stale := unifiedRequest(t, f, "POST", "/v1/images/generations", `{"model":"ycapi-doubao-seedream-5-0-pro","prompt":"stale directory","resolution":"1k","size":"1:1","n":1}`, version)
	require.Equal(t, 400, stale.Code, stale.Body.String())
	assert.Equal(t, "rejected-before-billing", stale.Header().Get(service.ModelConfigurationResultHeader))
	assert.Contains(t, stale.Body.String(), `"accepted":false`)
	assert.Equal(t, 1, posts)
	records = append(records, unifiedHTTPFixture(t, f, "stale-image", stale))
	writeUnifiedFixtures(t, "image-http-fixtures.json", records)
}
