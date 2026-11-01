package router

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is a compatibility proof using historical account metadata and a wholly
// synthetic channel 12. It neither inspects nor validates the current production
// route. The snapshot did not declare a separate native input-token ceiling.
// TEST_H3_SQL_DSN / TEST_H3_LOG_SQL_DSN select the existing disposable three-DB
// fixture; every provider request below is intercepted in memory.
func TestLegacyFlagshipCapacityProjectionDatabase(t *testing.T) {
	const publicID = "yunshu-flash"
	const upstreamID = "deepseek-flash"
	const nativeContext = 1048576
	const nativeOutput = 393216
	const sourceDate = "2026-10-31"
	const sourcePath = "output/diagnostics/model-capacity-joint-20261031/qualification/provider-account-metadata.json"
	const sourceSHA = "7918dc71e3f846822e7f08a5733e5ebec6a02e74e1ade9fb55d12b332ddc0661"
	const responseSHA = "b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f"
	groups := []string{"default", "product", "yunshu-managed"}
	common.OptionMapRWMutex.RLock()
	priorMarker, hadMarker := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadMarker {
			common.OptionMap[model.ModelCapacityOptionKey] = priorMarker
		} else {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	f := newH3RouterFixture(t, 0)
	previousGroups := ratio_setting.GroupRatio2JSONString()
	previousUsable := setting.UserUsableGroups2JSONString()
	previousRatios := ratio_setting.ModelRatio2JSONString()
	previousSelfUse := operation_setting.SelfUseModeEnabled
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"product":1,"yunshu-managed":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","product":"Product","yunshu-managed":"Managed"}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"yunshu-flash":1}`))
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		operation_setting.SelfUseModeEnabled = previousSelfUse
	})
	require.NoError(t, f.db.AutoMigrate(&model.UserAccessToken{}))
	require.NoError(t, f.logDB.AutoMigrate(&model.AuditLog{}))
	relayKeys, accessKeys := map[string]string{}, map[string]string{}
	for i, group := range groups {
		userID := 880 + i
		user := model.User{Id: userID, Username: fmt.Sprintf("flagship-%d", i), AffCode: fmt.Sprintf("flagship-%d", i), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100000000, Group: group, AuthVersion: 1}
		require.NoError(t, f.db.Create(&user).Error)
		relayKeys[group] = fmt.Sprintf("legacyflagship%d", i)
		require.NoError(t, f.db.Create(&model.Token{UserId: userID, Key: relayKeys[group], Name: "Synthetic flagship directory", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100000000, Group: group}).Error)
		accessKeys[group] = model.AccessTokenPrefix + strings.Repeat(string(rune('a'+i)), 43)
		pat := model.UserAccessToken{UserId: userID, Name: "Synthetic flagship pricing", TokenHash: model.AccessTokenFingerprint(accessKeys[group]), TokenHint: model.AccessTokenHint(accessKeys[group])}
		require.NoError(t, pat.SetScopes([]string{}))
		require.NoError(t, f.db.Create(&pat).Error)
	}
	channel := model.Channel{Id: 12, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "SYNTHETIC legacy flagship channel 12", Key: "synthetic-deepseek-account-no-production-credential", BaseURL: common.GetPointer("https://legacy-flagship.fixture"), Models: publicID, Group: strings.Join(groups, ","), ModelMapping: common.GetPointer(`{"yunshu-flash":"deepseek-flash"}`)}
	require.NoError(t, f.db.Create(&channel).Error)
	cfg := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{}}
	for _, group := range groups {
		require.NoError(t, f.db.Create(&model.Ability{Group: group, Model: publicID, ChannelId: 12, Enabled: true}).Error)
		cfg.Routes = append(cfg.Routes, model.CapacityRoute{Model: publicID, Group: group, ChannelID: 12, RouteModel: publicID, ContextWindow: common.GetPointer(nativeContext), MaxOutputTokens: common.GetPointer(nativeOutput), BudgetField: "max_tokens", Source: sourcePath + "#/results/1/requests/0/selected/0; sha256=" + sourceSHA + "; historical metadata, synthetic route only", VerifiedAt: sourceDate})
	}
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	saved, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	require.Len(t, saved.Routes, 3)
	channelHash := model.CapacityChannelConfigHash(&channel)
	for _, route := range saved.Routes {
		assert.Equal(t, channelHash, route.ChannelConfigHash)
		assert.Equal(t, 12, route.ChannelID)
	}
	model.InitChannelCache()
	model.RefreshPricing()
	posts := 0
	wires := []map[string]any{}
	client := service.GetHttpClient()
	priorTransport := client.Transport
	t.Cleanup(func() { client.Transport = priorTransport })
	client.Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "legacy-flagship.fixture" || request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			return nil, fmt.Errorf("synthetic flagship fixture blocks all other outbound requests")
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err = common.Unmarshal(payload, &body); err != nil {
			return nil, err
		}
		posts++
		wires = append(wires, body)
		response := `{"id":"synthetic-completion","object":"chat.completion","model":"deepseek-flash","choices":[{"index":0,"message":{"role":"assistant","content":"synthetic"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
	})
	projections := []map[string]any{}
	wireObservations := []map[string]any{}
	assertDirectories := func(stage string, wantOutput int) {
		t.Helper()
		before := posts
		for _, group := range groups {
			for _, path := range []string{"/v1/models", "/api/pricing"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				credential := "sk-" + relayKeys[group]
				if path == "/api/pricing" {
					credential = accessKeys[group]
				}
				request.Header.Set("Authorization", "Bearer "+credential)
				recorder := httptest.NewRecorder()
				f.engine.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var response struct {
					Data []map[string]any `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				found := false
				for _, entry := range response.Data {
					id, _ := entry["id"].(string)
					if path == "/api/pricing" {
						id, _ = entry["model_name"].(string)
					}
					if id != publicID {
						continue
					}
					found = true
					assert.EqualValues(t, nativeContext, entry["context_window"], stage+" "+path+" "+group)
					assert.EqualValues(t, wantOutput, entry["max_output_tokens"], stage+" "+path+" "+group)
					assert.NotContains(t, entry, "max_input_tokens", "native input limit was never verified")
					assert.NotContains(t, entry, "channel_config_hash")
					assert.NotContains(t, entry, "source")
					if parameters, ok := entry["parameters"].(map[string]any); ok {
						if text, ok := parameters["x-text-capabilities"].(map[string]any); ok {
							assert.NotContains(t, text, "max_input_tokens", "do not invent a separate flagship input capacity")
							assert.NotContains(t, text, "native_max_input_tokens")
							assert.NotContains(t, text, "fallback_output_tokens", "do not import the YC 8192 product fallback")
						}
					}
					projections = append(projections, map[string]any{"stage": stage, "path": path, "group": group, "model": id, "context_window": entry["context_window"], "max_output_tokens": entry["max_output_tokens"]})
				}
				assert.True(t, found, stage+" "+path+" "+group)
			}
		}
		assert.Equal(t, before, posts, "directory projection never calls an upstream")
	}
	assertWire := func(group string, requested *int, want int) {
		t.Helper()
		request := map[string]any{"model": publicID, "messages": []map[string]string{{"role": "user", "content": "hello"}}}
		if requested != nil {
			request["max_tokens"] = *requested
		}
		payload, err := common.Marshal(request)
		require.NoError(t, err)
		before := posts
		recorder := f.request(t, http.MethodPost, "/v1/chat/completions", string(payload), relayKeys[group])
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, before+1, posts)
		wire := wires[len(wires)-1]
		assert.Equal(t, upstreamID, wire["model"], "channel 12 mapping must use the historical raw identity")
		assert.EqualValues(t, want, wire["max_tokens"])
		var callerBudget any
		if requested != nil {
			callerBudget = *requested
		}
		wireObservations = append(wireObservations, map[string]any{"group": group, "caller_max_tokens": callerBudget, "observed_model": wire["model"], "observed_max_tokens": wire["max_tokens"]})
	}
	t.Run("historical_declaration_projects_to_both_real_directories", func(t *testing.T) { assertDirectories("historical_declared", nativeOutput) })
	t.Run("explicit_small_and_above_8k_outputs_are_preserved", func(t *testing.T) {
		assertWire("default", common.GetPointer(1024), 1024)
		assertWire("product", common.GetPointer(8192), 8192)
		assertWire("yunshu-managed", common.GetPointer(32768), 32768)
		assertWire("default", nil, nativeOutput)
	})
	t.Run("administrator_stricter_policy_survives_omission_and_route_restore", func(t *testing.T) {
		for i := range cfg.Routes {
			cfg.Routes[i].MaxOutputTokens = common.GetPointer(16384)
			cfg.Routes[i].Source += "; synthetic administrator stricter delivery budget"
		}
		require.NoError(t, model.SaveModelCapacityConfig(cfg))
		assertDirectories("administrator_stricter", 16384)
		assertWire("default", common.GetPointer(1024), 1024)
		assertWire("product", common.GetPointer(32768), 16384)
		require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{}}))
		require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 12).Update("model_mapping", `{"yunshu-flash":"different-synthetic-route"}`).Error)
		invalidated, err := service.ModelCapacityForGroups(publicID, []string{"default"})
		require.NoError(t, err)
		assert.True(t, invalidated.Managed)
		assert.Empty(t, invalidated.Routes, "capacity cannot move to an unreviewed mapping")
		require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 12).Update("model_mapping", *channel.ModelMapping).Error)
		model.InitChannelCache()
		assertDirectories("restored_original_route_remains_stricter", 16384)
		assertWire("yunshu-managed", nil, 16384)
	})
	require.False(t, t.Failed(), "failed observations must never publish a verified proof")
	proof := map[string]any{
		"status":            "SYNTHETIC_LEGACY_FLAGSHIP_ROUTER_PROJECTION_VERIFIED",
		"source":            map[string]any{"path": sourcePath, "sha256": sourceSHA, "snapshot_date": sourceDate, "date_basis": "historical artifact path, not a new supplier check", "json_pointer": "/results/1/requests/0/selected/0", "response_sha256": responseSHA, "upstream_model": upstreamID, "context_window": nativeContext, "max_output_tokens": nativeOutput, "native_max_input_tokens": nil},
		"synthetic_route":   map[string]any{"channel_id": 12, "public_model": publicID, "upstream_model": upstreamID, "groups": groups, "channel_config_hash": channelHash, "base_url": "https://legacy-flagship.fixture", "credentials": "dummy fixture only", "current_production_route_verified": false},
		"verification_date": "2026-11-02", "database": string(common.MainDatabaseType()), "projections": projections, "wire_observations": wireObservations, "intercepted_synthetic_posts": posts, "upstream_network_requests": 0, "paid_generations": 0, "production_access": 0,
		"caller_outputs": map[string]any{"1024": 1024, "8192": 8192, "32768": 32768, "omitted": nativeOutput, "stricter_admin_limit": 16384, "stricter_1024": 1024, "stricter_32768": 16384, "restored_route_omitted": 16384},
		"scope":          "Historical metadata and locally configured synthetic route proof only. Not a current supplier or production availability/capacity attestation. No separate original input ceiling was asserted. No YC 8k fallback copied to the legacy model.",
	}
	encoded, err := common.Marshal(proof)
	require.NoError(t, err)
	if output := os.Getenv("TEST_LEGACY_FLAGSHIP_PROOF_OUTPUT"); output != "" {
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
	t.Logf("LEGACY_FLAGSHIP_PROOF=%s", encoded)
}
