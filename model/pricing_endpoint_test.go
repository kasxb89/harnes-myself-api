package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPricingBlackrxParametersEnableReleasedClientsWithoutChangingBilling(t *testing.T) {
	resetPricingEndpointTestTables(t)
	insertPricingEndpointChannel(t, 503, constant.ChannelTypeTaskPlugin, dto.ChannelOtherSettings{})
	for _, id := range []string{"model_5", "lec-mj-seedance-2-5-cd-720p", "bt-seedance-2.0", "unrelated-video"} {
		insertPricingEndpointAbility(t, 503, id)
	}
	InitChannelCache()
	for _, row := range GetPricing() {
		assert.Nil(t, row.Parameters, "unregistered plugins cannot advertise input contracts")
	}
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("blackrxai-video") })
	InvalidatePricingCache()
	rows := pricingByModel(GetPricing())
	assert.Nil(t, rows["unrelated-video"].Parameters)
	for _, id := range []string{"model_5", "lec-mj-seedance-2-5-cd-720p", "bt-seedance-2.0"} {
		row := rows[id]
		assert.Equal(t, id, row.ModelName)
		properties := row.Parameters["properties"].(map[string]any)
		seconds := properties["seconds"].(map[string]any)["enum"].([]int)
		switch id {
		case "model_5":
			assert.Equal(t, []int{15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}, seconds)
		case "lec-mj-seedance-2-5-cd-720p":
			assert.Equal(t, []int{30}, seconds)
		case "bt-seedance-2.0":
			assert.Equal(t, []int{10, 15}, seconds)
		}
		schema, examples := plugin.Meta.UsageForModel(id)
		assert.Equal(t, jsplugin.CloneUsageSchema(schema), row.BillingUsageSchema)
		assert.Equal(t, jsplugin.CloneUsageExamples(examples), row.BillingUsageExamples)
		encoded, err := common.Marshal(row)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(encoded, &decoded))
		assert.Contains(t, decoded, "parameters")
		assert.Equal(t, id, decoded["model_name"])
	}
	// Every advertised model's examples remain within its input contract, while
	// numeric input enums never enter the billing usage schema.
	for _, id := range plugin.Meta.Models {
		parameters := blackrxVideoClientParameters(id)
		require.NotNil(t, parameters, id)
		seconds := parameters["properties"].(map[string]any)["seconds"].(map[string]any)["enum"].([]int)
		limits := parameters["x-reference-limits"].(map[string]any)
		assert.Equal(t, true, limits["counts_include_files_and_urls"])
		assert.Equal(t, 1, parameters["properties"].(map[string]any)["n"].(map[string]any)["default"])
		_, examples := plugin.Meta.UsageForModel(id)
		for _, example := range examples {
			value, ok := example.Facts["seconds"].(int64)
			require.True(t, ok, "integer plugin metadata uses int64")
			assert.Contains(t, seconds, int(value), id)
		}
	}
}

func TestPricingMediaContractTracksInstalledPluginAndKeepsBillingFacts(t *testing.T) {
	resetPricingEndpointTestTables(t)
	require.NoError(t, MigrateH3Admission(DB))
	for _, table := range []string{"h3_admissions", "h3_accounts"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"h3_admissions", "h3_accounts"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
	})
	models := []struct {
		key, id, source, version string
		channelType              int
	}{
		{"toapis-image", "yunshu-internal-gpt-image-2", "../deploy/instance/toapis-image-plugin.js", "1.2.1", constant.ChannelTypeTaskPlugin},
		{"hailuo", "MiniMax-H3", "../plugins/tasks/hailuo/plugin.js", "1.2.3", constant.ChannelTypeMiniMax},
	}
	for i, fixture := range models {
		insertPricingEndpointChannel(t, 610+i, fixture.channelType, dto.ChannelOtherSettings{})
		insertPricingEndpointAbility(t, 610+i, fixture.id)
		source, err := os.ReadFile(fixture.source)
		require.NoError(t, err)
		plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
		require.NoError(t, err)
		require.Equal(t, fixture.version, plugin.Meta.Version)
		t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(fixture.key) })
	}
	blackrxSource, err := os.ReadFile("../deploy/instance/blackrxai-video-plugin.js")
	require.NoError(t, err)
	blackrx, err := jsplugin.DefaultRegistry.Register(string(blackrxSource), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("blackrxai-video") })
	insertPricingEndpointChannel(t, 612, constant.ChannelTypeTaskPlugin, dto.ChannelOtherSettings{})
	for _, id := range blackrx.Meta.Models {
		insertPricingEndpointAbility(t, 612, id)
	}
	InitChannelCache()
	projection := GetPricing()
	rows := pricingByModel(projection)
	require.Len(t, rows, 17)
	image := rows[models[0].id]
	require.NotNil(t, image.Parameters)
	imageProperties := image.Parameters["properties"].(map[string]any)
	assert.Equal(t, []string{"1k", "2k", "4k"}, imageProperties["resolution"].(map[string]any)["enum"])
	ratios := imageProperties["size"].(map[string]any)["enum"].([]string)
	assert.Len(t, ratios, 13)
	assert.Contains(t, ratios, "16:9")
	assert.NotContains(t, ratios, "4096x4096")
	assert.Equal(t, "size", imageProperties["aspect_ratio"].(map[string]any)["x-alias-for"])
	assert.Equal(t, 6, imageProperties["reference_images"].(map[string]any)["maxItems"])
	assert.Equal(t, "tariff_tier_not_exact_pixels", image.Parameters["x-resolution-semantics"])
	video := rows[models[1].id]
	require.NotNil(t, video.Parameters)
	videoProperties := video.Parameters["properties"].(map[string]any)
	assert.Equal(t, 4, videoProperties["seconds"].(map[string]any)["minimum"])
	assert.Equal(t, 15, videoProperties["seconds"].(map[string]any)["maximum"])
	assert.Equal(t, []string{"768P", "2K"}, videoProperties["resolution"].(map[string]any)["enum"])
	assert.Equal(t, referenceLimits(9, 3, 3), video.Parameters["x-reference-limits"])
	for _, fixture := range models {
		plugin, ok := jsplugin.DefaultRegistry.Get(fixture.key)
		require.True(t, ok)
		schema, examples := plugin.Meta.UsageForModel(fixture.id)
		assert.Equal(t, jsplugin.CloneUsageSchema(schema), rows[fixture.id].BillingUsageSchema)
		assert.Equal(t, jsplugin.CloneUsageExamples(examples), rows[fixture.id].BillingUsageExamples)
	}
	assert.NotContains(t, video.Parameters, "x-client-concurrency", "unconfigured admission cannot promise parallelism")
	// Configure synthetic independent accounts through the production operator
	// API. A ceiling is advertised only after explicit reconciliation/enabling;
	// adding capacity never changes the model's billing contract or price.
	baselineBilling := make(map[string]Pricing, len(rows))
	for id, row := range rows {
		row.Parameters = nil
		row.PricingVersion = "" // The response-level version is stored on the first, unordered row.
		baselineBilling[id] = row
	}
	for i := range 4 {
		channelID := 611
		if i > 0 {
			channelID = 612 + i
			insertPricingEndpointChannel(t, channelID, constant.ChannelTypeMiniMax, dto.ChannelOtherSettings{})
			insertPricingEndpointAbility(t, channelID, "MiniMax-H3")
		}
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channelID).Updates(map[string]any{
			"base_url": "https://metaso.cn/api/openai", "models": "MiniMax-H3",
		}).Error)
		accountID := fmt.Sprintf("pricing-fixture-%d", i)
		require.NoError(t, ProvisionH3Account(context.Background(), accountID, channelID))
		InvalidatePricingCache()
		if i == 0 {
			assert.NotContains(t, pricingByModel(GetPricing())["MiniMax-H3"].Parameters, "x-client-concurrency", "a provisioned but unreconciled account is unavailable")
		}
		require.NoError(t, ReconcileH3Account(context.Background(), accountID))
		require.NoError(t, SetH3AccountEnabled(context.Background(), accountID, true))
		InvalidatePricingCache()
		configuredProjection := GetPricing()
		require.Equal(t, projection[0].PricingVersion, configuredProjection[0].PricingVersion)
		configured := pricingByModel(configuredProjection)
		assert.Equal(t, map[string]any{
			"schema": 1, "atomic_admission": true, "max_parallelism": 4 * (i + 1),
			"model": "MiniMax-H3", "scope": "shared_account_pool", "reservation": false,
		}, configured["MiniMax-H3"].Parameters["x-client-concurrency"])
		for id, row := range configured {
			if id != "MiniMax-H3" && row.Parameters != nil {
				assert.NotContains(t, row.Parameters, "x-client-concurrency", id)
			}
			row.Parameters = nil
			row.PricingVersion = ""
			assert.Equal(t, baselineBilling[id], row, "capacity metadata must not change billing: %s", id)
		}
	}
	// Fail only the account read: the real pricing projection must remain
	// readable and retain its input schema while omitting the unsafe hint.
	callback := "pricing:h3-account-read-failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "h3_accounts" {
			tx.AddError(errors.New("synthetic private account query failure"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	InvalidatePricingCache()
	failed := pricingByModel(GetPricing())
	require.Len(t, failed, 17)
	require.NotNil(t, failed["MiniMax-H3"].Parameters)
	assert.NotContains(t, failed["MiniMax-H3"].Parameters, "x-client-concurrency")
	encodedFailure, err := common.Marshal(failed)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedFailure), "synthetic private")
	require.NoError(t, DB.Callback().Query().Remove(callback))
	InvalidatePricingCache()
	projection = GetPricing()
	for _, row := range projection {
		if row.ModelName == "MiniMax-H3" {
			require.Equal(t, 16, row.Parameters["x-client-concurrency"].(map[string]any)["max_parallelism"])
		}
	}
	if output := os.Getenv("TEST_MEDIA_CATALOG_OUTPUT"); output != "" {
		encoded, err := common.Marshal(projection)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
	// A retained pre-upgrade plugin override must not advertise a host-only
	// contract that its actual selected source cannot fulfill.
	for _, fixture := range models {
		source, err := os.ReadFile(fixture.source)
		require.NoError(t, err)
		oldVersion := "1.2.0"
		old := strings.Replace(string(source), fixture.version, oldVersion, 1)
		_, err = jsplugin.DefaultRegistry.Register(old, jsplugin.Options{})
		require.NoError(t, err)
	}
	InvalidatePricingCache()
	for _, row := range GetPricing() {
		if row.ModelName == models[0].id || row.ModelName == models[1].id {
			assert.Nil(t, row.Parameters, "retained older plugin version must not claim the new controls")
		}
	}
}

func TestPricingVideoDisplayNamesPreserveModelIDs(t *testing.T) {
	resetPricingEndpointTestTables(t)
	expected := map[string]string{
		"lec-mj-seedance-2-5-cd-720p": "Seedance 2.5 · 720p · 30秒（线路1）",
		"sd-25-900-2":                 "Seedance 2.5 · 720p · 30秒（线路2）",
		"k2-sd2.5-720p":               "Seedance 2.5 满参版 · 720p · 30秒",
		"lec-sz-sd2-full-480p":        "Seedance 2.5 · 480p · 30秒",
		"video-sd25-480p":             "Seedance 2.5 · 480p · 15–30秒",
		"model_5":                     "Seedance 2.5 完整版 · 480p · 15–30秒",
		"sd2.5-720p-x2":               "Seedance 2.5 完整版 · 720p · 15–30秒",
		"sd2-th-2.0-720p":             "Seedance 2.0 Fast · 720p · 15秒（线路1）",
		"bt-seedance-2.0":             "Seedance 2.0 · 720p · 10/15秒",
		"lec-rs-seedance-2-0-fast":    "Seedance 2.0 Fast · 720p · 15秒（线路2）",
		"sd20-933-720p":               "Seedance 2.0 Fast · 720p · 15秒（线路3）",
		"sd2-hn2-480-pro":             "Seedance 2.0 完整版 · 480p · 15秒",
		"v3-sd2.0-933-720p":           "Seedance 2.0 完整版 · 720p · 15秒（线路1）",
		"sd-20-9/3/3-1":               "Seedance 2.0 完整版 · 720p · 15秒（线路2）",
		"sd-20-903":                   "Seedance 2.0 Fast · 480p · 15秒",
	}
	source, err := os.ReadFile("../deploy/instance/blackrxai-video-pricing.py")
	require.NoError(t, err)
	declared := regexp.MustCompile(`(?m)^\s*\("([^"]+)",`).FindAllStringSubmatch(string(source), -1)
	require.Len(t, declared, len(expected))
	labels := make(map[string]string, len(expected))
	insertPricingEndpointChannel(t, 501, constant.ChannelTypeTaskPlugin, dto.ChannelOtherSettings{})
	for _, entry := range declared {
		id := entry[1]
		label, ok := expected[id]
		require.True(t, ok, "supplier model %q needs a display name", id)
		if previous, duplicate := labels[label]; duplicate {
			t.Fatalf("models %q and %q share display name %q", previous, id, label)
		}
		labels[label] = id
		insertPricingEndpointAbility(t, 501, id)
	}
	insertPricingEndpointChannel(t, 502, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	for _, id := range []string{"deepseek-flash", "MiniMax-H3", "model_50", "BT-seedance-2.0", "bt-seedance-2.0-extra"} {
		expected[id] = id
		insertPricingEndpointAbility(t, 502, id)
	}
	InitChannelCache()
	pricing := GetPricing()
	candidates, err := ListClientModelCandidates()
	require.NoError(t, err)
	require.Len(t, pricing, len(expected))
	require.Len(t, candidates, len(expected))

	// Assert the public JSON fields as well as the catalog objects. A label
	// must never replace a machine identity on either response.
	for _, response := range []struct {
		name, idField string
		value         any
	}{
		{"pricing", "model_name", pricing},
		{"client model candidates", "id", candidates},
	} {
		t.Run(response.name, func(t *testing.T) {
			encoded, err := common.Marshal(response.value)
			require.NoError(t, err)
			var rows []map[string]any
			require.NoError(t, common.Unmarshal(encoded, &rows))
			seen := make(map[string]bool, len(expected))
			for _, row := range rows {
				id, ok := row[response.idField].(string)
				require.True(t, ok)
				label, ok := expected[id]
				require.True(t, ok, "unexpected model identity %q", id)
				assert.False(t, seen[id], "duplicate identity %q", id)
				assert.Equal(t, label, row["display_name"], "model %q", id)
				seen[id] = true
			}
			assert.Len(t, seen, len(expected))
		})
	}
}

func resetPricingEndpointTestTables(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &Vendor{}))
	for _, table := range []string{"abilities", "channels", "models", "vendors"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	InitChannelCache()
	InvalidatePricingCache()
	t.Cleanup(func() {
		for _, table := range []string{"abilities", "channels", "models", "vendors"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
		InitChannelCache()
		InvalidatePricingCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

func insertPricingEndpointChannel(t *testing.T, channelID int, channelType int, settings dto.ChannelOtherSettings) {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   channelType,
		Key:    fmt.Sprintf("key-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", channelID),
	}
	if settings.AdvancedCustom != nil {
		channel.SetOtherSettings(settings)
	}
	require.NoError(t, DB.Create(channel).Error)
}

func insertPricingEndpointAbility(t *testing.T, channelID int, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
}

func pricingEndpointAdvancedCustomConfig(routes ...dto.AdvancedCustomRoute) dto.ChannelOtherSettings {
	return dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: routes,
		},
	}
}

func pricingEndpointTypesByModel(t *testing.T) map[string][]constant.EndpointType {
	t.Helper()
	InitChannelCache()
	return pricingEndpointTypesFromPricing(GetPricing())
}

func pricingEndpointTypesFromPricing(pricings []Pricing) map[string][]constant.EndpointType {
	byModel := make(map[string][]constant.EndpointType)
	for _, pricing := range pricings {
		byModel[pricing.ModelName] = pricing.SupportedEndpointTypes
	}
	return byModel
}

func TestPricingAdvancedCustomUsesConfiguredEndpointTypes(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 101, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 101, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 101, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
	}, byModel["gpt-4o"])
}

func TestPricingModelMetadataEndpointsMergeWithAdvancedCustomInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 103, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 103, "gemini-2.5-flash")
	require.NoError(t, DB.Create(&Model{
		ModelName: "gemini-2.5-flash",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAI,
	}, byModel["gemini-2.5-flash"])
}

func TestPricingModelMetadataEndpointsCanProvideEndpointWithoutChannelInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 104, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 104, "metadata-only-model")
	require.NoError(t, DB.Create(&Model{
		ModelName: "metadata-only-model",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["metadata-only-model"])
}

func TestPricingAdvancedCustomMissingConfigFallsBackToChannelType(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 102, constant.ChannelTypeAdvancedCustom, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 102, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
}

func TestPricingNativeChannelEndpointTypesUnchanged(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 201, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 202, constant.ChannelTypeGemini, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 203, constant.ChannelTypeAnthropic, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 201, "gpt-4o")
	insertPricingEndpointAbility(t, 202, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 203, "claude-3-5-sonnet")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}, byModel["claude-3-5-sonnet"])
}

func TestInitChannelCacheInvalidatesPricingCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 301, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
	))
	insertPricingEndpointAbility(t, 301, "gemini-3.5-flash")
	InitChannelCache()

	initial := pricingEndpointTypesByModel(t)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, initial["gemini-3.5-flash"])

	var channel Channel
	require.NoError(t, DB.First(&channel, "id = ?", 301).Error)
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 301).Update("settings", channel.OtherSettings).Error)
	InitChannelCache()

	updated := pricingEndpointTypesByModel(t)
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, updated["gemini-3.5-flash"])
}

func TestInitChannelCacheInvalidatesStartupPricingBuiltBeforeChannelCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 302, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 302, "gemini-3.5-flash")

	staleByModel := pricingEndpointTypesFromPricing(GetPricing())
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, staleByModel["gemini-3.5-flash"])

	InitChannelCache()

	rebuiltByModel := pricingEndpointTypesFromPricing(GetPricing())
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, rebuiltByModel["gemini-3.5-flash"])
}

func TestCacheUpdateChannelSyncsAdvancedCustomConfig(t *testing.T) {
	resetPricingEndpointTestTables(t)

	channel := &Channel{
		Id:     401,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "key-401",
		Status: common.ChannelStatusEnabled,
		Name:   "channel-401",
	}
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/responses",
		UpstreamPath: "/v1beta/models/{model}:generateContent",
		Converter:    "openai_responses_to_gemini_generate_content",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIResponse}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.Type = constant.ChannelTypeOpenAI
	CacheUpdateChannel(channel)

	assert.Nil(t, channel2advancedCustomConfig[401])
}
