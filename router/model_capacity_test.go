package router

import (
	"bytes"
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestSeparateOutputDefaultCatalogAndSingleCharge(t *testing.T) {
	captured := map[string]common.RawMessage{}
	f := unifiedConfigurationFixture(t)
	base := "https://output-budget.fixture"
	require.NoError(t, (&model.Channel{Id: 1212, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Output budget fixture", Models: "split-output", Group: "default", Key: "synthetic-output-budget", BaseURL: &base}).Insert())
	publishUnifiedFixture(t, "split-output", func(def *model.ModelConfiguration) {
		def.ContextWindow = common.GetPointer(1048576)
		def.MaxOutputTokens = common.GetPointer(1048576)
		require.NoError(t, common.Unmarshal([]byte(`{"default_output_tokens":131072}`), def))
	})
	for _, path := range []string{"/v1/models", "/api/pricing"} {
		for _, modern := range []bool{false, true} {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("Authorization", "Bearer sk-h3routeradmin")
			if modern {
				req.Header.Set("X-Yunshu-Model-Limits-Version", "2")
			}
			response := httptest.NewRecorder()
			f.engine.ServeHTTP(response, req)
			require.Equal(t, 200, response.Code, response.Body.String())
			assert.Contains(t, response.Header().Get("Vary"), "X-Yunshu-Model-Limits-Version")
			captured[fmt.Sprintf("%s:separate=%t", path, modern)] = append(common.RawMessage(nil), response.Body.Bytes()...)
			var body struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			found := false
			for _, row := range body.Data {
				if row["id"] != "split-output" && row["model_name"] != "split-output" {
					continue
				}
				found = true
				assert.EqualValues(t, 1048576, row["context_window"])
				if modern {
					assert.EqualValues(t, 1048576, row["max_output_tokens"])
					assert.EqualValues(t, 131072, row["default_output_tokens"])
				} else {
					assert.EqualValues(t, 131072, row["max_output_tokens"], "old clients must not reserve the entire context")
				}
			}
			assert.True(t, found)
		}
	}
	if output := os.Getenv("TEST_OUTPUT_BUDGET_CATALOG_CAPTURE"); output != "" {
		raw, err := common.Marshal(captured)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, raw, 0600))
	}
	var budgets []float64
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "output-budget.fixture", req.URL.Host)
		var body map[string]any
		require.NoError(t, common.DecodeJson(req.Body, &body))
		budgets = append(budgets, body["max_tokens"].(float64))
		result := `{"id":"local-output","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(result)), Request: req}, nil
	})
	wallet, token := f.balance(t)
	for _, output := range []string{"", `,"max_tokens":500000`} {
		response := unifiedRequest(t, f, "POST", "/v1/chat/completions", `{"model":"split-output","messages":[{"role":"user","content":"hello"}]`+output+`}`, "")
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	assert.Equal(t, []float64{131072, 500000}, budgets, "default reserve is separate from the explicitly requested output ceiling")
	finalWallet, finalToken := f.balance(t)
	expectedCharge := common.QuotaFromFloat(0.02 * common.QuotaPerUnit)
	assert.EqualValues(t, expectedCharge, wallet-finalWallet)
	assert.EqualValues(t, expectedCharge, token-finalToken)
	var consumes int64
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("model_name = ? AND type = ?", "split-output", model.LogTypeConsume).Count(&consumes).Error)
	assert.EqualValues(t, 2, consumes, "one upstream call and one consume per accepted request")
	configured, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	index := -1
	for i, route := range configured.Routes {
		if route.Model == "split-output" {
			index = i
			break
		}
	}
	require.NotEqual(t, -1, index)
	for _, invalid := range []int{0, 1048577} {
		bad := configured
		bad.Routes = append([]model.CapacityRoute(nil), configured.Routes...)
		bad.Routes[index].DefaultOutputTokens = common.GetPointer(invalid)
		require.Error(t, model.SaveModelCapacityConfig(bad))
	}
	readback, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	assert.Equal(t, configured, readback, "invalid defaults must not change existing configuration")
	published, err := model.CurrentPublishedModelConfigurations()
	require.NoError(t, err)
	beforeVersion := model.ModelExecutionConfigurationVersion("split-output", published["split-output"])
	defaultOnly := configured
	defaultOnly.Routes = append([]model.CapacityRoute(nil), configured.Routes...)
	defaultOnly.Routes[index].DefaultOutputTokens = common.GetPointer(32768)
	require.NoError(t, model.SaveModelCapacityConfig(defaultOnly))
	assert.NotEqual(t, beforeVersion, model.ModelExecutionConfigurationVersion("split-output", published["split-output"]), "default-only changes invalidate prepared model versions")
	require.NoError(t, model.SaveModelCapacityConfig(configured))
	catalog, err := model.ReadModelCapacityCatalog()
	require.NoError(t, err)
	change := configured.Routes[index]
	change.MaxOutputTokens, change.DefaultOutputTokens = common.GetPointer(65536), nil
	_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{change}})
	require.NoError(t, err)
	readback, err = model.ReadModelCapacityConfig()
	require.NoError(t, err)
	assert.EqualValues(t, 65536, *readback.Routes[index].DefaultOutputTokens, "old PATCH omission preserves the default within a smaller cap")
	current, err := model.GetModelConfigurationSnapshot("split-output")
	require.NoError(t, err)
	oldDashboard := current.Entries[0].Definition
	oldDashboard.DefaultOutputTokens = nil
	require.NoError(t, model.SaveModelConfigurationDraft(current.Revision, current.Entries[0].BaseVersion, oldDashboard))
	current, err = model.GetModelConfigurationSnapshot("split-output")
	require.NoError(t, err)
	require.NotNil(t, current.Entries[0].Draft)
	assert.EqualValues(t, 65536, *current.Entries[0].Draft.Definition.DefaultOutputTokens, "old dashboard saves retain the separate default")
}

func TestModelCapacityDirectoryAndPersistence(t *testing.T) {
	setupRelayRouterTestDB(t)
	common.OptionMapRWMutex.Lock()
	oldMarker := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if oldMarker == "" {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		} else {
			common.OptionMap[model.ModelCapacityOptionKey] = oldMarker
		}
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Option{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Channel{}, &model.Option{})) })
	previous := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = previous })
	// This test exercises capacity, not the unpriced self-use fallback. A
	// selectable model now needs an actual tariff, including an explicit zero.
	previousRatios := ratio_setting.ModelRatio2JSONString()
	ratios := ratio_setting.GetModelRatioCopy()
	ratios["joint-small"] = 0.5
	encodedRatios, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encodedRatios)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })
	previousMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemory })
	u := model.User{Username: "capacity-user", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&u).Error)
	require.NoError(t, model.DB.Create(&model.Token{UserId: u.Id, Key: "capacityfixturekey", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	for _, id := range []int{1, 2, 3} {
		require.NoError(t, model.DB.Create(&model.Channel{Id: id, Type: 1, Status: 1, Name: "synthetic", Group: "default", Models: "joint-small"}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "joint-small", ChannelId: id, Enabled: true}).Error)
	}
	makeRoute := func(id, window, output int) model.CapacityRoute {
		return model.CapacityRoute{Model: "joint-small", Group: "default", RouteModel: "joint-small", ChannelID: id, ContextWindow: &window, MaxOutputTokens: &output, BudgetField: "max_tokens", Source: "local synthetic upstream specification", VerifiedAt: "2026-10-31"}
	}
	cfg := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{makeRoute(1, 32768, 8192), makeRoute(2, 16384, 4096)}}
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	e, err := service.ModelCapacityForGroups("joint-small", []string{"default"})
	require.NoError(t, err)
	assert.Len(t, e.Routes, 2)
	assert.Equal(t, 4096, *e.MaxOutputTokens)
	assert.Equal(t, 16384, *e.ContextWindow)
	engine := gin.New()
	SetRelayRouter(engine)
	get := func() map[string]any {
		r := httptest.NewRecorder()
		q := httptest.NewRequest("GET", "/v1/models", nil)
		q.Header.Set("Authorization", "Bearer capacityfixturekey")
		engine.ServeHTTP(r, q)
		require.Equal(t, 200, r.Code, r.Body.String())
		var v map[string]any
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &v))
		assert.NotContains(t, r.Body.String(), "channel_id")
		assert.NotContains(t, r.Body.String(), "source")
		return v
	}
	v := get()
	entries := v["data"].([]any)
	require.Len(t, entries, 1)
	entry := entries[0].(map[string]any)
	assert.Equal(t, float64(4096), entry["max_output_tokens"])
	cfg.Routes[0] = makeRoute(1, 8000, 1000)
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	entries = get()["data"].([]any)
	assert.Equal(t, float64(1000), entries[0].(map[string]any)["max_output_tokens"])
	require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{}}))
	read, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	assert.Len(t, read.Routes, 2, "omission must not revoke old declarations")
	cfg.Routes[0] = makeRoute(1, 100, 200)
	assert.Error(t, model.SaveModelCapacityConfig(cfg))
	cfg.Routes[0] = makeRoute(1, 2147483647, 2147483647)
	assert.Error(t, model.SaveModelCapacityConfig(cfg))
	assert.Error(t, model.UpdateOption(model.ModelCapacityOptionKey, `{}`))
	// A missing declaration never bans an old model.
	c, err := service.ModelCapacityForGroups("legacy", []string{"default"})
	require.NoError(t, err)
	assert.False(t, c.Managed)

	// Repointing a channel invalidates its old proof while the other reviewed
	// route remains available; another group cannot borrow this route's proof.
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("model_mapping", `{"joint-small":"changed-native"}`).Error)
	changed, err := service.ModelCapacityForGroups("joint-small", []string{"default"})
	require.NoError(t, err)
	assert.Len(t, changed.Routes, 1)
	assert.Equal(t, 4096, *changed.MaxOutputTokens)
	other, err := service.ModelCapacityForGroups("joint-small", []string{"other"})
	require.NoError(t, err)
	assert.True(t, other.Managed)
	assert.Empty(t, other.Routes)
	// Ordinary credentials cannot mutate the root-only management endpoint.
	api := gin.New()
	SetApiRouter(api)
	body, _ := common.Marshal(cfg)
	q := httptest.NewRequest("PUT", "/api/option/model_capacity", bytes.NewReader(body))
	q.Header.Set("Authorization", "Bearer capacityfixturekey")
	r := httptest.NewRecorder()
	api.ServeHTTP(r, q)
	assert.NotEqual(t, http.StatusOK, r.Code)
}

func TestModelCapacityAdminCatalogAndPatch(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	previousSecret, previousSecure := common.SessionSecret, common.SessionCookieSecure
	previousGlobal, previousCritical := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	common.SessionSecret, common.SessionCookieSecure = "isolated-model-capacity-admin", true
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false
	t.Cleanup(func() {
		common.SessionSecret, common.SessionCookieSecure = previousSecret, previousSecure
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = previousGlobal, previousCritical
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, f.db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}))
	require.NoError(t, f.logDB.AutoMigrate(&model.AuditLog{}))
	credentials := map[int]string{}
	for _, role := range []int{common.RoleRootUser, common.RoleAdminUser, common.RoleCommonUser} {
		user := model.User{Username: fmt.Sprintf("capacity-admin-%d", role), AffCode: fmt.Sprintf("ca%d", role), Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
		require.NoError(t, f.db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "capacity-admin-test")
		require.NoError(t, err)
		credentials[role] = bundle.AccessToken
	}
	channels := []model.Channel{
		{Id: 101, Type: 1, Key: "synthetic-capacity-secret", Status: 1, Name: "Chat gateway", Models: "capacity-small,capacity-other,opaque-media", Group: "default,product", ModelMapping: common.GetPointer(`{"opaque-media":"gpt-image-2"}`)},
		{Id: 102, Type: 1, Key: "synthetic-capacity-other", Status: 1, Name: "Smaller gateway", Models: "capacity-small", Group: "default"},
		{Id: 103, Type: 1, Status: 2, Name: "Archived gateway", Models: "archived-chat", Group: "default"},
	}
	for i := range channels {
		require.NoError(t, f.db.Create(&channels[i]).Error)
		for _, group := range channels[i].GetGroups() {
			for _, name := range channels[i].GetModels() {
				require.NoError(t, f.db.Create(&model.Ability{Group: group, Model: name, ChannelId: channels[i].Id, Enabled: true}).Error)
			}
		}
	}
	route := func(name, group string, channel, window, output int) model.CapacityRoute {
		return model.CapacityRoute{Model: name, Group: group, ChannelID: channel, RouteModel: name, ContextWindow: common.GetPointer(window), MaxOutputTokens: common.GetPointer(output), BudgetField: "max_tokens", Source: "Synthetic administrator fixture", VerifiedAt: "2026-11-07"}
	}
	require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{
		route("capacity-small", "default", 101, 32768, 8192),
		route("capacity-small", "product", 101, 32768, 8192),
		route("capacity-small", "default", 102, 16384, 4096),
		route("archived-chat", "default", 103, 8192, 1024),
	}}))
	var initial model.Option
	require.NoError(t, f.db.Where(&model.Option{Key: model.ModelCapacityOptionKey}).Take(&initial).Error)
	raw, err := sjson.SetRaw(initial.Value, "future", `{ "preserve": [1,2] }`)
	require.NoError(t, err)
	raw, err = sjson.SetRaw(raw, "routes.0.extension", `{ "keep": true }`)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: initial.Key}).Update("value", raw).Error)
	engine := gin.New()
	SetApiRouter(engine)
	request := func(method, path, token, origin string, body any) *httptest.ResponseRecorder {
		encoded, err := common.Marshal(body)
		require.NoError(t, err)
		q := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		q.Header.Set("Content-Type", "application/json")
		if token != "" {
			q.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			q.Header.Set("Origin", origin)
		}
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, q)
		return r
	}
	decode := func(response *httptest.ResponseRecorder) model.ModelCapacityCatalog {
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload struct {
			Success bool                       `json:"success"`
			Data    model.ModelCapacityCatalog `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
		require.True(t, payload.Success, response.Body.String())
		return payload.Data
	}
	catalog := decode(request("GET", "/api/option/model_capacity/catalog", credentials[common.RoleRootUser], "", nil))
	require.Len(t, catalog.Routes, 5)
	require.Len(t, catalog.Revision, 64)
	encoded, err := common.Marshal(catalog)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "synthetic-capacity-secret")
	assert.NotContains(t, string(encoded), "channel_config_hash")
	assert.NotContains(t, string(encoded), "opaque-media")
	assert.NotContains(t, string(encoded), "archived-chat")
	for _, row := range catalog.Routes {
		if row.Model == "capacity-other" {
			assert.False(t, row.Configured)
			assert.False(t, row.Valid)
			assert.Nil(t, row.ContextWindow)
		} else {
			assert.True(t, row.Valid)
		}
	}
	for _, token := range []string{"", credentials[common.RoleAdminUser], credentials[common.RoleCommonUser], "h3routermember"} {
		assert.NotEqual(t, http.StatusOK, request("GET", "/api/option/model_capacity/catalog", token, "", nil).Code)
		assert.NotEqual(t, http.StatusOK, request("PATCH", "/api/option/model_capacity", token, "http://example.com", model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{route("capacity-small", "default", 101, 65536, 16384)}}).Code)
	}
	patch := model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{
		route("capacity-small", "default", 101, 65536, 16384),
		route("capacity-small", "product", 101, 65536, 16384),
	}}
	assert.Equal(t, http.StatusForbidden, request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "https://evil.example", patch).Code)
	updated := decode(request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch))
	assert.NotEqual(t, catalog.Revision, updated.Revision)
	var stored model.Option
	require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&stored).Error)
	assert.Equal(t, gjson.Get(raw, "future").Raw, gjson.Get(stored.Value, "future").Raw)
	assert.Equal(t, gjson.Get(raw, "routes.0.extension").Raw, gjson.Get(stored.Value, "routes.0.extension").Raw)
	assert.Equal(t, gjson.Get(raw, "routes.3").Raw, gjson.Get(stored.Value, "routes.3").Raw)
	assert.Equal(t, gjson.Get(raw, "routes.2").Raw, gjson.Get(stored.Value, "routes.2").Raw)
	assert.Equal(t, http.StatusConflict, request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch).Code)
	capacity, err := service.ModelCapacityForGroups("capacity-small", []string{"default"})
	require.NoError(t, err)
	assert.Equal(t, 16384, *capacity.ContextWindow)
	assert.Equal(t, 4096, *capacity.MaxOutputTokens)

	// Editing one channel does not refresh evidence on the other route.
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 101).Update("key", "synthetic-rotated-key").Error)
	patch.Revision = updated.Revision
	assert.Equal(t, http.StatusConflict, request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch).Code)
	changed := decode(request("GET", "/api/option/model_capacity/catalog", credentials[common.RoleRootUser], "", nil))
	assert.NotEqual(t, updated.Revision, changed.Revision)
	patch.Revision = changed.Revision
	patch.Routes = []model.CapacityRoute{route("capacity-small", "default", 101, 131072, 32768)}
	changed = decode(request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch))
	for _, row := range changed.Routes {
		if row.ChannelID == 101 && row.Model == "capacity-small" {
			assert.Equal(t, row.Group == "default", row.Valid)
		}
	}
	// A catalog revision also covers an enabled ability changing independently.
	require.NoError(t, f.db.Model(&model.Ability{}).Where("channel_id = ? AND model = ?", 102, "capacity-small").Update("enabled", false).Error)
	patch.Revision = changed.Revision
	assert.Equal(t, http.StatusConflict, request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch).Code)
	changed = decode(request("GET", "/api/option/model_capacity/catalog", credentials[common.RoleRootUser], "", nil))
	patch.Revision = changed.Revision
	for _, invalid := range []model.CapacityRoute{
		route("capacity-small", "default", 101, 1024, 2048),
		route("capacity-small", "default", 101, 0, 1024),
		route("opaque-media", "default", 101, 8192, 1024),
		route("invented-chat", "default", 101, 8192, 1024),
		route("archived-chat", "default", 103, 8192, 1024),
	} {
		patch.Routes = []model.CapacityRoute{invalid}
		assert.Equal(t, http.StatusBadRequest, request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", patch).Code)
	}
	// Concurrent administrators cannot silently overwrite the same snapshot.
	var writers sync.WaitGroup
	results := make(chan error, 2)
	for _, output := range []int{24576, 28672} {
		writers.Go(func() {
			_, err := model.PatchModelCapacity(model.ModelCapacityPatch{Revision: changed.Revision, Routes: []model.CapacityRoute{route("capacity-small", "default", 101, 131072, output)}})
			results <- err
		})
	}
	writers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		if result == nil {
			successes++
		} else {
			assert.ErrorIs(t, result, model.ErrModelCapacityConflict)
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)

	// First-time configuration and legacy null route arrays remain editable.
	require.NoError(t, f.db.Where(&model.Option{Key: model.ModelCapacityOptionKey}).Delete(&model.Option{}).Error)
	common.OptionMapRWMutex.Lock()
	delete(common.OptionMap, model.ModelCapacityOptionKey)
	common.OptionMapRWMutex.Unlock()
	unconfigured, err := model.ReadModelCapacityCatalog()
	require.NoError(t, err)
	_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: unconfigured.Revision, Routes: []model.CapacityRoute{
		route("capacity-other", "default", 101, 65536, 8192),
		route("capacity-other", "product", 101, 65536, 8192),
	}})
	require.NoError(t, err)
	require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1}))
	legacyRaw := `{"schema":1,"routes":null,"future":{"kept":true}}`
	require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: initial.Key}).Update("value", legacyRaw).Error)
	unconfigured, err = model.ReadModelCapacityCatalog()
	require.NoError(t, err)
	_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: unconfigured.Revision, Routes: []model.CapacityRoute{
		route("capacity-other", "default", 101, 65536, 8192),
		route("invented-chat", "default", 101, 65536, 8192),
	}})
	require.Error(t, err)
	require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&stored).Error)
	assert.Equal(t, legacyRaw, stored.Value, "an invalid batch rolls back every route")
	_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: unconfigured.Revision, Routes: []model.CapacityRoute{
		route("capacity-other", "default", 101, 65536, 8192),
		route("capacity-other", "product", 101, 65536, 8192),
	}})
	require.NoError(t, err)
	require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&stored).Error)
	assert.True(t, gjson.Get(stored.Value, "future.kept").Bool())
	assert.Len(t, gjson.Get(stored.Value, "routes").Array(), 2)

	for _, knownField := range []string{"context", "output"} {
		t.Run("historical_partial_"+knownField, func(t *testing.T) {
			var activeChannel, archivedChannel model.Channel
			require.NoError(t, f.db.First(&activeChannel, 101).Error)
			require.NoError(t, f.db.First(&archivedChannel, 103).Error)
			partial := []model.CapacityRoute{
				route("capacity-small", "default", 101, 65536, 8192),
				route("capacity-small", "product", 101, 65536, 8192),
				route("capacity-small", "default", 103, 65536, 8192),
			}
			for i := range partial {
				partial[i].ChannelConfigHash = model.CapacityChannelConfigHash(&activeChannel)
				if i == 2 {
					partial[i].ChannelConfigHash = model.CapacityChannelConfigHash(&archivedChannel)
				}
				if knownField == "context" {
					partial[i].MaxOutputTokens = nil
				} else {
					partial[i].ContextWindow = nil
				}
			}
			encoded, err := common.Marshal(model.ModelCapacityConfig{Schema: 1, Routes: partial})
			require.NoError(t, err)
			require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: initial.Key}).Update("value", string(encoded)).Error)
			before := string(encoded)
			partialCatalog, err := model.ReadModelCapacityCatalog()
			require.NoError(t, err)
			full := []model.CapacityRoute{
				route("capacity-small", "default", 101, 131072, 16384),
				route("capacity-small", "product", 101, 131072, 16384),
			}
			// Groups can gain fields separately without claiming a shared limit.
			partialCatalog, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: partialCatalog.Revision, Routes: full[:1]})
			require.NoError(t, err)
			mixed, err := service.ModelCapacityForGroups("capacity-small", []string{"default", "product"})
			require.NoError(t, err)
			if knownField == "context" {
				assert.Nil(t, mixed.MaxOutputTokens)
			} else {
				assert.Nil(t, mixed.ContextWindow)
			}
			for i := range full {
				if knownField == "context" {
					full[i].MaxOutputTokens = nil
				} else {
					full[i].ContextWindow = nil
				}
			}
			partialCatalog, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: partialCatalog.Revision, Routes: full})
			require.NoError(t, err)
			resolved, err := service.ModelCapacityForGroups("capacity-small", []string{"default", "product"})
			require.NoError(t, err)
			if knownField == "context" {
				assert.Nil(t, resolved.MaxOutputTokens)
			} else {
				assert.Nil(t, resolved.ContextWindow)
			}
			full = []model.CapacityRoute{
				route("capacity-small", "default", 101, 131072, 16384),
				route("capacity-small", "product", 101, 131072, 16384),
			}
			_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: partialCatalog.Revision, Routes: full})
			require.NoError(t, err)
			require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&stored).Error)
			assert.Equal(t, gjson.Get(before, "routes.2").Raw, gjson.Get(stored.Value, "routes.2").Raw)
			_, err = model.ReadModelCapacityConfig()
			require.NoError(t, err, "historical partial routes remain readable after active routes gain both fields")
			resolved, err = service.ModelCapacityForGroups("capacity-small", []string{"default"})
			require.NoError(t, err)
			assert.Equal(t, 131072, *resolved.ContextWindow)
			assert.Equal(t, 16384, *resolved.MaxOutputTokens)
			latest, err := model.ReadModelCapacityCatalog()
			require.NoError(t, err)
			for i := range full {
				full[i].ContextWindow = nil
			}
			_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: latest.Revision, Routes: full})
			require.NoError(t, err)
			resolved, err = service.ModelCapacityForGroups("capacity-small", []string{"default"})
			require.NoError(t, err)
			assert.Equal(t, 131072, *resolved.ContextWindow, "omission never clears an already declared limit")
		})
	}

	t.Run("official_partial_channels_can_be_completed_separately", func(t *testing.T) {
		const name = "yunshu-flash"
		partial := []model.CapacityRoute{}
		for _, id := range []int{201, 202} {
			channel := model.Channel{Id: id, Type: 1, Key: "synthetic-only", Status: 1, Name: "Official partial fixture", Models: name, Group: "default"}
			require.NoError(t, f.db.Create(&channel).Error)
			require.NoError(t, f.db.Create(&model.Ability{Group: "default", Model: name, ChannelId: id, Enabled: true}).Error)
			row := route(name, "default", id, 131072, 16384)
			if id == 202 {
				row.ContextWindow = common.GetPointer(65536)
			}
			row.MaxOutputTokens = nil
			partial = append(partial, row)
		}
		require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: partial}))
		catalog, err := model.ReadModelCapacityCatalog()
		require.NoError(t, err)
		for _, row := range catalog.Routes {
			if row.Model == name {
				assert.False(t, row.RequiresFullCapacity)
			}
		}
		catalog, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{route(name, "default", 201, 131072, 16384)}})
		require.NoError(t, err)
		mixed, err := service.ModelCapacityForGroups(name, []string{"default"})
		require.NoError(t, err)
		require.Len(t, mixed.Routes, 2)
		assert.Equal(t, 65536, *mixed.ContextWindow)
		assert.Nil(t, mixed.MaxOutputTokens, "one channel's verified output must not be published for the unknown channel")
		_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{route(name, "default", 202, 65536, 8192)}})
		require.NoError(t, err)
		complete, err := service.ModelCapacityForGroups(name, []string{"default"})
		require.NoError(t, err)
		assert.Equal(t, 65536, *complete.ContextWindow)
		assert.Equal(t, 8192, *complete.MaxOutputTokens)
		for _, row := range complete.Routes {
			if row.ChannelID == 201 {
				assert.Equal(t, 16384, *row.MaxOutputTokens, "separate upstream limits stay distinct")
			}
		}
	})

	for index, name := range []string{"ycapi-qwen3.6-plus", "ys-deepseek-v4.1-flash-1017"} {
		t.Run("schema2_requires_complete_"+name, func(t *testing.T) {
			firstID := 301 + index*10
			for _, id := range []int{firstID, firstID + 1} {
				channel := model.Channel{Id: id, Type: 1, Key: "synthetic-only", Status: 1, Name: "Schema2 fixture", Models: name, Group: "default"}
				require.NoError(t, f.db.Create(&channel).Error)
				require.NoError(t, f.db.Create(&model.Ability{Group: "default", Model: name, ChannelId: id, Enabled: true}).Error)
			}
			require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{route(name, "default", firstID, 131072, 16384)}}))
			catalog, err := model.ReadModelCapacityCatalog()
			require.NoError(t, err)
			matched := 0
			for _, row := range catalog.Routes {
				if row.Model == name {
					matched++
					assert.True(t, row.RequiresFullCapacity)
				}
			}
			require.Equal(t, 2, matched)
			var before model.Option
			require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&before).Error)
			for _, omitted := range []string{"context_window", "max_output_tokens"} {
				partial := route(name, "default", firstID+1, 65536, 8192)
				if omitted == "context_window" {
					partial.ContextWindow = nil
				} else {
					partial.MaxOutputTokens = nil
				}
				encoded, err := common.Marshal(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{partial}})
				require.NoError(t, err)
				// A caller cannot disable the server-owned schema2 requirement.
				encodedJSON, err := sjson.SetBytes(encoded, "routes.0.requires_full_capacity", false)
				require.NoError(t, err)
				var bypass map[string]any
				require.NoError(t, common.Unmarshal(encodedJSON, &bypass))
				response := request("PATCH", "/api/option/model_capacity", credentials[common.RoleRootUser], "http://example.com", bypass)
				assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), "both context window and maximum output tokens")
				require.NoError(t, f.db.Where(&model.Option{Key: initial.Key}).Take(&stored).Error)
				assert.Equal(t, before.Value, stored.Value)
			}
			parameters := ycapi.ClientParameters(name)
			if parameters == nil {
				parameters = codebloom.ClientParameters(name)
			}
			project := func() {
				projected, err := service.ProjectPricingModelCapacities([]model.Pricing{{ModelName: name, Parameters: parameters}}, []string{"default"}, false)
				require.NoError(t, err, "rejected partial changes must not interrupt the model directory projection")
				require.Len(t, projected, 1)
			}
			project()
			catalog, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{route(name, "default", firstID+1, 65536, 8192)}})
			require.NoError(t, err)
			project()
			complete, err := service.ModelCapacityForGroups(name, []string{"default"})
			require.NoError(t, err)
			assert.Equal(t, 65536, *complete.ContextWindow)
			assert.Equal(t, 8192, *complete.MaxOutputTokens)
			change := route(name, "default", firstID+1, 65536, 4096)
			change.ContextWindow = nil
			_, err = model.PatchModelCapacity(model.ModelCapacityPatch{Revision: catalog.Revision, Routes: []model.CapacityRoute{change}})
			require.NoError(t, err, "a known field is retained when omitted")
			project()
		})
	}
}

func TestModelCapacityWireBudgetBoundaries(t *testing.T) {
	require.NoError(t, i18n.Init())
	setupRelayRouterTestDB(t)
	common.OptionMapRWMutex.Lock()
	oldMarker := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if oldMarker == "" {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		} else {
			common.OptionMap[model.ModelCapacityOptionKey] = oldMarker
		}
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Option{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Channel{}, &model.Option{})) })
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Type: 1, Status: 1, Name: "local", Group: "default", Models: "joint"}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "joint", ChannelId: 1, Enabled: true}).Error)
	cfg := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{{Model: "joint", Group: "default", RouteModel: "joint", ChannelID: 1, ContextWindow: common.GetPointer(32768), MaxOutputTokens: common.GetPointer(8192), BudgetField: "max_tokens", Source: "synthetic only", VerifiedAt: "2026-10-31"}}}
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	cases := []struct {
		name, body, path string
		expected         int
		bad              bool
	}{
		{"large legacy", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":384000}`, "/v1/chat/completions", 8192, false},
		{"smaller caller", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`, "/v1/chat/completions", 1024, false},
		{"completion spelling", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":384000}`, "/v1/chat/completions", 8192, false},
		{"missing budget", `{"model":"joint","messages":[{"role":"user","content":"hello"}]}`, "/v1/chat/completions", 8192, false},
		{"native responses", `{"model":"joint","input":"hello","max_output_tokens":384000}`, "/v1/responses", 8192, false},
		{"native gemini", `{"generationConfig":{"maxOutputTokens":384000},"contents":[]}`, "/v1beta/models/joint:generateContent", 8192, false},
		{"nested override", `{"model":"joint","max_tokens":384000,"extra_body":{"max_tokens":999999}}`, "/v1/chat/completions", 8192, false},
		{"zero", `{"model":"joint","max_tokens":0}`, "/v1/chat/completions", 0, true},
		{"negative", `{"model":"joint","max_tokens":-1}`, "/v1/chat/completions", 0, true},
		{"fraction", `{"model":"joint","max_tokens":1.5}`, "/v1/chat/completions", 0, true},
		{"wrong type", `{"model":"joint","max_tokens":"100"}`, "/v1/chat/completions", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			info := &relaycommon.RelayInfo{OriginModelName: "joint", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, UpstreamModelName: "gpt-4"}}
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Request = req
			err := service.EnforceModelCapacityOutbound(c, req, info)
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			wire, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, common.Unmarshal(wire, &got))
			field := "max_tokens"
			if tc.path == "/v1/responses" {
				field = "max_output_tokens"
			}
			if strings.Contains(tc.path, ":generateContent") {
				assert.Equal(t, float64(tc.expected), got["generationConfig"].(map[string]any)["maxOutputTokens"])
			} else {
				assert.Equal(t, float64(tc.expected), got[field])
			}
			if tc.name == "nested override" {
				assert.Equal(t, float64(8192), got["extra_body"].(map[string]any)["max_tokens"])
			}
			assert.Equal(t, int64(len(wire)), req.ContentLength)
		})
	}
	// Capacity is applied before param()-aware billing reads the incoming body.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":384000}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	r := &kitdto.GeneralOpenAIRequest{Model: "joint", Messages: []kitdto.Message{{Role: "user", Content: "hello"}}, MaxTokens: common.GetPointer(uint(384000))}
	info := &relaycommon.RelayInfo{OriginModelName: "joint", Request: r}
	require.Nil(t, service.ClampModelCapacityRequest(c, info))
	require.NotNil(t, info.BillingRequestInput)
	assert.Contains(t, string(info.BillingRequestInput.Body), `"max_tokens":8192`)
}
