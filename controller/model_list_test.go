package controller

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type listModelsResponse struct {
	Success bool               `json:"success"`
	Data    []dto.OpenAIModels `json:"data"`
	Object  string             `json:"object"`
}

type userModelsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

func setupModelListControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	initModelListColumnNames(t)

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	// Directory fixtures declare real enabled channels, rather than relying on
	// orphaned abilities being kept by the previous permissive display filter.
	for _, id := range []int{1, 2} {
		require.NoError(t, db.Create(&model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Model list fixture", Key: "synthetic-list-key", Group: "default,vip,unavailable"}).Error)
	}

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func initModelListColumnNames(t *testing.T) {
	t.Helper()

	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	defer func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	}()

	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s_init?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	require.NoError(t, model.InitDB())
	if model.DB != nil {
		sqlDB, err := model.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
}

func withTieredBillingConfig(t *testing.T, modes map[string]string, exprs map[string]string) {
	t.Helper()

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if strings.HasPrefix(key, "billing_setting.") {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		model.InvalidatePricingCache()
	})

	modeBytes, err := common.Marshal(modes)
	require.NoError(t, err)
	exprBytes, err := common.Marshal(exprs)
	require.NoError(t, err)

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(modeBytes),
		"billing_setting.billing_expr": string(exprBytes),
	}))
	model.InvalidatePricingCache()
}

func withSelfUseModeDisabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func withSelfUseModeEnabled(t *testing.T) {
	t.Helper()

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = original
	})
}

func withModelListFixtureTariffs(t *testing.T, names ...string) {
	t.Helper()
	previous := ratio_setting.ModelPrice2JSONString()
	prices := ratio_setting.GetModelPriceCopy()
	for _, name := range names {
		prices[name] = 0.01
	}
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encoded)))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previous))
		model.InvalidatePricingCache()
	})
}

func decodeListModelsPayload(t *testing.T, recorder *httptest.ResponseRecorder) listModelsResponse {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload listModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "list", payload.Object)
	return payload
}

func decodeListModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]struct{} {
	t.Helper()

	payload := decodeListModelsPayload(t, recorder)
	ids := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		ids[item.Id] = struct{}{}
	}
	return ids
}

func pricingByModelName(pricings []model.Pricing) map[string]model.Pricing {
	byName := make(map[string]model.Pricing, len(pricings))
	for _, pricing := range pricings {
		byName[pricing.ModelName] = pricing
	}
	return byName
}

func decodeUserModelsResponse(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload userModelsResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload.Data
}

func TestGetUserModelsFiltersByRequestedGroup(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1002,
		Username: "playground-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-default-only-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-disabled-model", ChannelId: 1, Enabled: false},
	}).Error)

	defaultRecorder := httptest.NewRecorder()
	defaultContext, _ := gin.CreateTestContext(defaultRecorder)
	defaultContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=default", nil)
	defaultContext.Set("id", 1002)

	GetUserModels(defaultContext)

	defaultModels := decodeUserModelsResponse(t, defaultRecorder)
	require.ElementsMatch(t, []string{"zz-default-only-model"}, defaultModels)

	vipRecorder := httptest.NewRecorder()
	vipContext, _ := gin.CreateTestContext(vipRecorder)
	vipContext.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=vip", nil)
	vipContext.Set("id", 1002)

	GetUserModels(vipContext)

	require.Empty(t, decodeUserModelsResponse(t, vipRecorder))
}

func TestGetUserModelsExpandsAutoGroupsInConfiguredOrder(t *testing.T) {
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalSpecialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		specialGroups.Clear()
		specialGroups.AddAll(originalSpecialGroups)
	})

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","unavailable"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"auto":"自动分组","default":"默认分组","unavailable":"不可用分组"}`))
	specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	specialGroups.Clear()
	specialGroups.Set("default", map[string]string{
		"+:vip":         "VIP 分组",
		"-:unavailable": "",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "playground-auto-model-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-model", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-shared-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-shared-model", ChannelId: 2, Enabled: true},
		{Group: "unavailable", Model: "zz-unavailable-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/models?group=auto", nil)
	context.Set("id", 1003)

	GetUserModels(context)

	models := decodeUserModelsResponse(t, recorder)
	require.Len(t, models, 3)
	assert.ElementsMatch(t, []string{"zz-vip-model", "zz-shared-model"}, models[:2])
	assert.Equal(t, "zz-default-model", models[2])
}

func TestListModelsIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-tiered-visible-model":      "tiered_expr",
		"zz-tiered-empty-expr-model":   "tiered_expr",
		"zz-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-tiered-empty-expr-model": "   ",
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1001,
		Username: "model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1001)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-tiered-visible-model")
	require.NotContains(t, ids, "zz-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-unpriced-model")

	pricingByName := pricingByModelName(model.GetPricing())
	visiblePricing, ok := pricingByName["zz-tiered-visible-model"]
	require.True(t, ok)
	require.Equal(t, "tiered_expr", visiblePricing.BillingMode)
	require.NotEmpty(t, visiblePricing.BillingExpr)

	emptyExprPricing, ok := pricingByName["zz-tiered-empty-expr-model"]
	require.True(t, ok)
	require.Empty(t, emptyExprPricing.BillingMode)
	require.Empty(t, emptyExprPricing.BillingExpr)

	missingExprPricing, ok := pricingByName["zz-tiered-missing-expr-model"]
	require.True(t, ok)
	require.Empty(t, missingExprPricing.BillingMode)
	require.Empty(t, missingExprPricing.BillingExpr)
}

func TestMediaV2OnlyCatalogClassificationPreservesLegacyModels(t *testing.T) {
	record := model.PublishedModelConfiguration{Definition: model.ModelConfiguration{
		MediaExecution: &model.ModelMediaExecutionConfiguration{SchemaVersion: 1},
	}}
	contracts, err := ycapi.MediaContracts()
	require.NoError(t, err)
	strictNew := 0
	for _, contract := range contracts {
		_, legacy := ycapi.Lookup(contract.ID)
		assert.Equal(t, !legacy, mediaV2OnlyCatalogModel(contract.ID, record), contract.ID)
		assert.False(t, mediaV2OnlyCatalogModel(contract.ID, model.PublishedModelConfiguration{}), "no published execution declaration: %s", contract.ID)
		if !legacy {
			strictNew++
		}
	}
	assert.Positive(t, strictNew)
	legacyModels, err := ycapi.Models()
	require.NoError(t, err)
	for _, legacy := range legacyModels {
		assert.False(t, mediaV2OnlyCatalogModel(legacy.ID, record), legacy.ID)
	}
	for _, name := range []string{"ys-gpt-image-2", "qq-gpt-image-2", "gpt-image-2", "MiniMax-H3", "ycapi-not-a-declared-model", "ycapi-deepseek-flash"} {
		assert.False(t, mediaV2OnlyCatalogModel(name, record), name)
	}
}

func TestPricingNativePreferenceIsAdditiveToLegacyVideoContract(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	withSelfUseModeDisabled(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })

	// Select a genuinely shared legacy/native video contract with the original
	// five- and ten-second choices, rather than special-casing an upstream ID.
	legacyModels, err := ycapi.Models()
	require.NoError(t, err)
	var legacy ycapi.Model
	var contract ycapi.MediaContract
	for _, candidate := range legacyModels {
		if candidate.Kind != "video" {
			continue
		}
		durations := map[any]bool{}
		for _, offer := range candidate.Offers {
			durations[offer.Params["duration"]] = true
		}
		current, known := ycapi.LookupMediaContract(candidate.ID)
		if known && current.Available && durations["5"] && durations["10"] {
			legacy, contract = candidate, current
			break
		}
	}
	require.NotEmpty(t, legacy.ID)
	withTieredBillingConfig(t, map[string]string{legacy.ID: "tiered_expr"}, map[string]string{legacy.ID: `tier("synthetic", u("seconds") * 0.1)`})
	withModelListFixtureTariffs(t, legacy.ID, "zz-preserved-legacy")
	channel := model.Channel{Id: 3, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "Synthetic native preference", Key: "synthetic-only-no-network", Models: legacy.ID, Group: "default"}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "ycapi"})
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: legacy.ID, ChannelId: 3, Enabled: true},
		{Group: "default", Model: "zz-preserved-legacy", ChannelId: 1, Enabled: true},
	}).Error)
	model.InitChannelCache()
	model.InvalidatePricingCache()

	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	definition := model.ModelConfiguration{ModelName: legacy.ID, Kind: "video", DisplayName: legacy.Name, Enabled: true,
		Routes:  []model.ModelConfigurationRoute{{ChannelID: channel.Id, Group: "default", UpstreamModel: legacy.ID, Enabled: true}},
		Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("synthetic", u("seconds") * 0.1)`}}
	var baseline map[string]any
	outputs := map[string]any{}
	for _, stage := range []string{"legacy", "qualified", "unqualified"} {
		if stage == "qualified" {
			definition.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: "synthetic-local-only", Intents: []string{"text"}, QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: service.MediaGenerationDigest(contract), CheckedAt: "2026-11-28"}
		} else if stage == "unqualified" {
			definition.MediaExecution.QualificationSHA256 = ""
		}
		document := model.ModelConfigurationDocument{SchemaVersion: 1, Published: map[string]model.PublishedModelConfiguration{legacy.ID: {Version: "synthetic-only", Definition: definition}}, Drafts: map[string]model.ModelConfigurationDraft{}, History: []model.ModelConfigurationHistory{}}
		encoded, e := common.Marshal(document)
		require.NoError(t, e)
		common.OptionMapRWMutex.Lock()
		common.OptionMap[model.ModelConfigurationKey] = string(encoded)
		common.OptionMapRWMutex.Unlock()
		cachedBefore, e := common.Marshal(model.GetPricing())
		require.NoError(t, e)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
		GetPricing(ctx)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var envelope struct {
			Success bool             `json:"success"`
			Data    []map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		byName := map[string]map[string]any{}
		for _, row := range envelope.Data {
			byName[row["model_name"].(string)] = row
		}
		require.Contains(t, byName, legacy.ID, stage)
		require.Contains(t, byName, "zz-preserved-legacy", stage)
		assert.NotContains(t, byName["zz-preserved-legacy"], "media_runtime")
		row := byName[legacy.ID]
		if stage == "qualified" {
			assert.Equal(t, map[string]any{"schema_version": float64(1), "preferred": "media-v2"}, row["media_runtime"])
		} else {
			assert.NotContains(t, row, "media_runtime", stage)
		}
		outputs[stage] = envelope
		if stage == "qualified" {
			nativeRecorder := httptest.NewRecorder()
			nativeContext, _ := gin.CreateTestContext(nativeRecorder)
			nativeContext.Request = httptest.NewRequest(http.MethodGet, "/v1/media-v2/models", nil)
			common.SetContextKey(nativeContext, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(nativeContext, constant.ContextKeyUserGroup, "default")
			MediaGenerationModels(nativeContext)
			require.Equal(t, http.StatusOK, nativeRecorder.Code, nativeRecorder.Body.String())
			var nativeEnvelope map[string]any
			require.NoError(t, common.Unmarshal(nativeRecorder.Body.Bytes(), &nativeEnvelope))
			outputs["native_qualified"] = nativeEnvelope
			nativeModels := nativeEnvelope["data"].(map[string]any)["models"].([]any)
			require.Len(t, nativeModels, 1)
			assert.Equal(t, legacy.ID, nativeModels[0].(map[string]any)["id"])
			records, e := model.CurrentPublishedModelConfigurations()
			require.NoError(t, e)
			versions := model.ModelExecutionConfigurationVersions(records)
			for _, boundary := range []string{"disabled", "unqualified", "unpriced", "group_disabled"} {
				t.Run(boundary, func(t *testing.T) {
					changed := maps.Clone(records)
					current := changed[legacy.ID]
					groups := []string{"default"}
					switch boundary {
					case "disabled":
						current.Definition.Enabled = false
					case "unqualified":
						binding := *current.Definition.MediaExecution
						binding.QualificationSHA256 = ""
						current.Definition.MediaExecution = &binding
					case "unpriced":
						withTieredBillingConfig(t, map[string]string{legacy.ID: "tiered_expr"}, map[string]string{})
					case "group_disabled":
						groups = []string{"unavailable"}
					}
					changed[legacy.ID] = current
					eligible, e := service.MediaGenerationPreferredLegacyModels(nativeContext, changed, groups, versions)
					require.NoError(t, e)
					assert.False(t, eligible[legacy.ID], "an advisory marker cannot turn an unavailable native route into a new-work choice")
				})
			}
		}
		comparison := maps.Clone(row)
		delete(comparison, "model_config_version")
		delete(comparison, "media_runtime")
		if stage == "legacy" {
			baseline = comparison
			seconds := map[float64]bool{}
			for _, raw := range comparison["retail_price_rows"].([]any) {
				seconds[raw.(map[string]any)["params"].(map[string]any)["seconds"].(float64)] = true
			}
			assert.Equal(t, map[float64]bool{5: true, 10: true}, seconds, "the original five- and ten-second legacy offers remain available")
		} else {
			assert.Equal(t, baseline, comparison, "marker cannot change legacy parameters, prices, units or routing fields")
		}
		cachedAfter, e := common.Marshal(model.GetPricing())
		require.NoError(t, e)
		assert.JSONEq(t, string(cachedBefore), string(cachedAfter), "projection cannot mutate cached legacy pricing")
	}
	if output := os.Getenv("TEST_NATIVE_PREFERENCE_PRICING_OUTPUT"); output != "" {
		encoded, e := common.Marshal(map[string]any{"synthetic": true, "model": legacy.ID, "responses": outputs})
		require.NoError(t, e)
		file, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		require.NoError(t, e)
		_, e = file.Write(encoded)
		require.NoError(t, e)
		require.NoError(t, file.Close())
	}
}

func TestLegacySelectableCatalogsHideOnlyPublishedMediaV2Contracts(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	const newID = "ycapi-gpt-image-2.6-flare"
	const draftID = "ycapi-gpt-image-2.5-flare"
	_, legacy := ycapi.Lookup(newID)
	require.False(t, legacy)
	_, declared := ycapi.LookupMediaContract(newID)
	require.True(t, declared)
	names := []string{newID, draftID, "ycapi-flux-kontext", "ys-gpt-image-2", "gpt-image-2", "MiniMax-H3", "zz-ordinary-text"}
	withModelListFixtureTariffs(t, names...)
	require.NoError(t, db.Create(&model.User{Id: 1001, Username: "legacy-media-list-user", Password: "password", Group: "default", Status: common.UserStatusEnabled}).Error)
	for _, name := range names {
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: name, ChannelId: 1, Enabled: true}).Error)
	}
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("models", strings.Join(names, ",")).Error)
	definition := model.ModelConfiguration{ModelName: newID, Kind: "image", DisplayName: "New V2 image", Enabled: true, MediaExecution: &model.ModelMediaExecutionConfiguration{SchemaVersion: 1}}
	document := model.ModelConfigurationDocument{
		SchemaVersion: 1,
		Published:     map[string]model.PublishedModelConfiguration{newID: {Version: "synthetic-published", Definition: definition}},
		Drafts:        map[string]model.ModelConfigurationDraft{draftID: {Definition: model.ModelConfiguration{ModelName: draftID, MediaExecution: definition.MediaExecution}}},
		History:       []model.ModelConfigurationHistory{},
	}
	encoded, err := common.Marshal(document)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{model.ModelConfigurationKey: string(encoded)}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	model.InvalidatePricingCache()
	beforePricing := model.GetPricing()
	require.Contains(t, pricingByModelName(beforePricing), newID, "the unfiltered cache remains available to V2/admin projections")
	filtered, err := legacySelectableModelNames(names)
	require.NoError(t, err)
	assert.NotContains(t, filtered, newID)
	assert.ElementsMatch(t, names[1:], filtered, "draft-only and all original identities remain")
	assert.Equal(t, newID, names[0], "filtering must not mutate its input")

	for _, format := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		ctx.Request.Header.Set(service.ModelLimitsVersionHeader, "2")
		ctx.Set("id", 1001)
		ListModels(ctx, format)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.NotContains(t, recorder.Body.String(), newID)
		for _, name := range names[1:] {
			assert.Contains(t, recorder.Body.String(), name)
		}
	}

	pricingRecorder := httptest.NewRecorder()
	pricingContext, _ := gin.CreateTestContext(pricingRecorder)
	pricingContext.Request = httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	pricingContext.Request.Header.Set(service.ModelLimitsVersionHeader, "2")
	pricingContext.Set("id", 1001)
	GetPricing(pricingContext)
	require.Equal(t, http.StatusOK, pricingRecorder.Code, pricingRecorder.Body.String())
	var pricingPayload struct {
		Success bool            `json:"success"`
		Data    []model.Pricing `json:"data"`
	}
	require.NoError(t, common.Unmarshal(pricingRecorder.Body.Bytes(), &pricingPayload))
	require.True(t, pricingPayload.Success)
	assert.NotContains(t, pricingByModelName(pricingPayload.Data), newID)
	for _, name := range names[1:] {
		assert.Contains(t, pricingByModelName(pricingPayload.Data), name)
	}
	assert.Equal(t, beforePricing, model.GetPricing(), "public projections must not change cached prices")

	pricebookRecorder := httptest.NewRecorder()
	pricebookContext, _ := gin.CreateTestContext(pricebookRecorder)
	pricebookContext.Request = httptest.NewRequest(http.MethodGet, "/api/model-pricebook", nil)
	GetConfiguredModelPricebook(pricebookContext)
	require.Equal(t, http.StatusOK, pricebookRecorder.Code, pricebookRecorder.Body.String())
	var pricebookPayload struct {
		Success bool `json:"success"`
		Data    struct {
			Rows []configuredPricebookRow `json:"rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(pricebookRecorder.Body.Bytes(), &pricebookPayload))
	require.True(t, pricebookPayload.Success)
	assert.Contains(t, pricebookRecorder.Body.String(), newID, "backend inventory still shows the configured V2-only model")
	rows := make(map[string]configuredPricebookRow, len(pricebookPayload.Data.Rows))
	for _, row := range pricebookPayload.Data.Rows {
		rows[row.ModelName] = row
	}
	require.Contains(t, rows, newID)
	assert.NotNil(t, rows[newID].Pricing)
}

func TestListModelsAppliesSavedDisplayNameWithoutChangingIdentity(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	const id = "zz-editable-label"
	withModelListFixtureTariffs(t, id)
	require.NoError(t, db.Create(&model.User{Id: 1001, Username: "label-user", Password: "password", Group: "default", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: id, ChannelId: 1, Enabled: true}).Error)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	model.InvalidatePricingCache()
	baseline := pricingByModelName(model.GetPricing())[id]
	require.Equal(t, id, baseline.DisplayName)
	for _, label := range []string{"First label", "Second label"} {
		badge := "Retained badge"
		require.NoError(t, model.SaveClientModelPresentation(model.ClientModelPresentation{SchemaVersion: 1, Models: map[string]model.ClientModelPresentationEntry{
			id: {DisplayName: &label, Badge: &badge}, "unavailable-id": {DisplayName: &label},
		}}))
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		ctx.Set("id", 1001)
		ListModels(ctx, constant.ChannelTypeOpenAI)
		assert.Equal(t, map[string]struct{}{id: {}}, decodeListModelsResponse(t, recorder))
		assert.Contains(t, recorder.Body.String(), `"display_name":"`+label+`"`)
		expected := baseline
		expected.DisplayName = label
		assert.Equal(t, expected, pricingByModelName(model.GetPricing())[id])
		public := model.PublicClientModelPresentation()
		assert.Equal(t, map[string]model.ClientModelPresentationEntry{id: {Badge: &badge}}, public.Models)
	}
	require.NoError(t, model.SaveClientModelPresentation(model.ClientModelPresentation{
		SchemaVersion: 1, Models: map[string]model.ClientModelPresentationEntry{},
	}))
	assert.Equal(t, baseline, pricingByModelName(model.GetPricing())[id])
}

func TestListModelsUsesAdvancedCustomEndpointTypesFromPricingCache(t *testing.T) {
	withSelfUseModeEnabled(t)
	withModelListFixtureTariffs(t, "gemini-3.5-flash")
	db := setupModelListControllerTestDB(t)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.InvalidatePricingCache()
	})

	require.NoError(t, db.Create(&model.User{
		Id:       1003,
		Username: "advanced-custom-model-list-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)

	channel := &model.Channel{
		Id:     701,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "advanced-custom-key",
		Status: common.ChannelStatusEnabled,
		Name:   "advanced-custom-channel",
		Group:  "default",
		Models: "gemini-3.5-flash",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: []dto.AdvancedCustomRoute{
				{
					IncomingPath: "/v1/chat/completions",
					UpstreamPath: "/v1/chat/completions",
				},
				{
					IncomingPath: "/v1/responses",
					UpstreamPath: "/v1beta/models/{model}:generateContent",
					Converter:    "openai_responses_to_gemini_generate_content",
					Models:       []string{"re:^gemini-"},
				},
			},
		},
	})
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "gemini-3.5-flash",
		ChannelId: 701,
		Enabled:   true,
	}).Error)

	model.InitChannelCache()
	model.GetPricing()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1003)

	ListModels(ctx, constant.ChannelTypeOpenAI)

	payload := decodeListModelsPayload(t, recorder)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "gemini-3.5-flash", payload.Data[0].Id)
	require.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, payload.Data[0].SupportedEndpointTypes)
}

func TestListModelsTokenLimitIncludesTieredBillingModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-token-tiered-visible-model":      "tiered_expr",
		"zz-token-tiered-empty-expr-model":   "tiered_expr",
		"zz-token-tiered-missing-expr-model": "tiered_expr",
	}, map[string]string{
		"zz-token-tiered-visible-model":    `tier("base", p * 1 + c * 2)`,
		"zz-token-tiered-empty-expr-model": "",
	})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-token-tiered-visible-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-empty-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-tiered-missing-expr-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-token-unpriced-model", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-token-tiered-visible-model":      true,
		"zz-token-tiered-empty-expr-model":   true,
		"zz-token-tiered-missing-expr-model": true,
		"zz-token-unpriced-model":            true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-token-tiered-visible-model")
	require.NotContains(t, ids, "zz-token-tiered-empty-expr-model")
	require.NotContains(t, ids, "zz-token-tiered-missing-expr-model")
	require.NotContains(t, ids, "zz-token-unpriced-model")
}

func TestListModelsTokenLimitUsesResolvedCustomAutoGroups(t *testing.T) {
	withModelListFixtureTariffs(t, "zz-vip-allowed", "zz-vip-denied", "zz-default-outside-snapshot")
	withSelfUseModeEnabled(t)
	originalMax := setting.GetMaxTokenAutoGroups()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("5"))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "zz-vip-allowed", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "zz-vip-denied", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-default-outside-snapshot", ChannelId: 1, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vip-allowed":              true,
		"zz-default-outside-snapshot": true,
		"zz-not-enabled":              true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)
	ids := decodeListModelsResponse(t, recorder)
	require.Equal(t, map[string]struct{}{"zz-vip-allowed": {}}, ids)

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	emptyRecorder := httptest.NewRecorder()
	emptyCtx, _ := gin.CreateTestContext(emptyRecorder)
	emptyCtx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(emptyCtx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(emptyCtx, constant.ContextKeyTokenModelLimit, map[string]bool{"zz-vip-allowed": true})

	require.NotPanics(t, func() {
		ListModels(emptyCtx, constant.ChannelTypeAnthropic)
	})
	var anthropicResponse struct {
		Data    []dto.AnthropicModel `json:"data"`
		FirstID string               `json:"first_id"`
		LastID  string               `json:"last_id"`
	}
	require.NoError(t, common.Unmarshal(emptyRecorder.Body.Bytes(), &anthropicResponse))
	require.Empty(t, anthropicResponse.Data)
	require.Empty(t, anthropicResponse.FirstID)
	require.Empty(t, anthropicResponse.LastID)
}

func TestSetupLoginDoesNotTouchPasswordWhenPasswordFieldOmitted(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.AuditLog{}, &model.UserSession{}, &model.TwoFA{}, &model.PasskeyCredential{}))

	hashedPassword, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := &model.User{
		Username: "twofa-user",
		Password: hashedPassword,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)

	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		setupLogin(&model.User{
			Id:          user.Id,
			AuthVersion: user.AuthVersion,
			Username:    user.Username,
			Role:        user.Role,
			Status:      user.Status,
			Group:       user.Group,
		}, nil, c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, hashedPassword, stored.Password)
}
