package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func officialReferenceFixture(price, source, date string) *model.ModelOfficialReferenceConfiguration {
	return &model.ModelOfficialReferenceConfiguration{Mode: "override", Rows: []model.ModelOfficialReferenceRow{{
		Params: map[string]any{}, Direction: "input", Unit: "CNY / million tokens",
		Reference: &model.OfficialPriceReference{SchemaVersion: 1, OfficialCNY: price, Currency: "CNY", Model: "Reviewed fixture model", Unit: "CNY / million tokens", SourceURL: source, CheckedAt: date, Conditions: "Standard text input", SourceCurrency: "USD", USDCNY: "7.3"},
	}}}
}

func TestOfficialReferencePublicationHTTPAndClientConsumers(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	const name = "official-reference-text"
	require.NoError(t, (&model.Channel{Id: 1150, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Reference fixture", Models: name, Group: "default", Key: "private-reference-fixture"}).Insert())
	root := model.User{Username: "reference-root", AffCode: "refroot", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, f.db.Create(&root).Error)
	login, err := service.CreateLoginSession(root.Id, "password", "127.0.0.1", "reference-local")
	require.NoError(t, err)
	request := func(method, path string, payload any) *httptest.ResponseRecorder {
		encoded, err := common.Marshal(payload)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+login.AccessToken)
		response := httptest.NewRecorder()
		f.engine.ServeHTTP(response, req)
		return response
	}
	snapshot := func() model.ModelConfigurationSnapshot {
		response := request("GET", "/api/option/model_catalog?model="+name, nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		var result struct {
			Success bool                             `json:"success"`
			Data    model.ModelConfigurationSnapshot `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success, response.Body.String())
		require.Len(t, result.Data.Entries, 1)
		return result.Data
	}
	save := func(def model.ModelConfiguration) {
		state := snapshot()
		response := request("PUT", "/api/option/model_catalog/draft", map[string]any{"revision": state.Revision, "base_version": state.Entries[0].BaseVersion, "definition": def})
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	publish := func() {
		response := request("POST", "/api/option/model_catalog/publish", map[string]any{"revision": snapshot().Revision, "models": []string{name}})
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	state := snapshot()
	def := state.Entries[0].Definition
	def.DisplayName, def.Enabled, def.Source = "Reference fixture text", true, "Synthetic reviewed reference fixture"
	def.ContextWindow, def.MaxOutputTokens = common.GetPointer(32768), common.GetPointer(4096)
	def.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("base", p * 1 + c * 2 + cr * 0.1)`}
	def.OfficialReferences = officialReferenceFixture("20", "https://example.com/pricing-a", "2026-11-09")
	outputReference := officialReferenceFixture("50", "https://example.com/pricing-a", "2026-11-09").Rows[0]
	outputReference.Direction = "output"
	def.OfficialReferences.Rows = append(def.OfficialReferences.Rows, outputReference)
	save(def)
	publish()
	wallet, token := f.balance(t)
	public := func() []byte {
		response := unifiedRequest(t, f, "GET", "/api/pricing", "", "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "private-reference-fixture")
		assert.NotContains(t, response.Body.String(), "official_references")
		return response.Body.Bytes()
	}
	server := httptest.NewServer(f.engine)
	defer server.Close()
	output := os.Getenv("OFFICIAL_REFERENCE_CONSUMER_OUTPUT")
	consume := func(stage, expectation string) {
		if output == "" {
			return
		}
		client := os.Getenv("OFFICIAL_REFERENCE_CLIENT_WORKTREE")
		require.NotEmpty(t, client)
		require.Regexp(t, "^[0-9a-f]{40}$", os.Getenv("OFFICIAL_REFERENCE_CLIENT_HEAD"))
		for _, mode := range []string{"source", "compiled"} {
			args := []string{"--import", "tsx/esm", os.Getenv("OFFICIAL_REFERENCE_CONSUMER"), "--worktree", client, "--expected-client-head", os.Getenv("OFFICIAL_REFERENCE_CLIENT_HEAD"), "--url", server.URL + "/api/pricing", "--model", name, "--mode", mode, "--stage", mode + "-" + stage, "--expect", expectation, "--output", filepath.Join(output, mode+"-"+stage+".json")}
			if candidate := os.Getenv("OFFICIAL_REFERENCE_API_CANDIDATE"); candidate != "" {
				head, err := exec.Command("git", "rev-parse", "HEAD").Output()
				require.NoError(t, err)
				require.Equal(t, candidate, strings.TrimSpace(string(head)))
				status, err := exec.Command("git", "status", "--porcelain").Output()
				require.NoError(t, err)
				require.Empty(t, strings.TrimSpace(string(status)), "a final candidate must be an unmodified checkout")
				args = append(args, "--api-candidate", candidate)
			}
			if expectation == "unchanged" || expectation == "reference-updated" {
				args = append(args, "--baseline", filepath.Join(output, mode+"-baseline.json"))
			}
			if mode == "compiled" {
				args = append(args, "--expected-host-module-sha256", os.Getenv("OFFICIAL_REFERENCE_HOST_MODULE_SHA"), "--expected-client-module-sha256", os.Getenv("OFFICIAL_REFERENCE_CLIENT_MODULE_SHA"), "--build-evidence", os.Getenv("OFFICIAL_REFERENCE_CLIENT_BUILD_EVIDENCE"))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			cmd := exec.CommandContext(ctx, os.Getenv("OFFICIAL_REFERENCE_NODE"), args...)
			cmd.Dir = client
			cmd.Env = append(os.Environ(), "pnpm_config_verify_deps_before_run=false")
			result, err := cmd.CombinedOutput()
			cancel()
			require.NoError(t, err, "%s %s: %s", mode, stage, result)
			t.Logf("Go HTTP consumer %s/%s passed", mode, stage)
		}
	}
	baseline := public()
	assert.Contains(t, string(baseline), `"official_cny":"20"`)
	var baselinePrices struct {
		Data []struct {
			Name string `json:"model_name"`
			Rows []struct {
				Direction string                        `json:"direction"`
				Retail    string                        `json:"retail_cny"`
				Reference *model.OfficialPriceReference `json:"official_reference"`
			} `json:"retail_price_rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(baseline, &baselinePrices))
	directions := map[string]string{}
	for _, item := range baselinePrices.Data {
		if item.Name != name {
			continue
		}
		for _, row := range item.Rows {
			require.NotNil(t, row.Reference)
			directions[row.Direction] = row.Reference.OfficialCNY
			multiplier := int64(1)
			if row.Direction == "output" {
				multiplier = 2
			}
			assert.Equal(t, decimal.NewFromFloat(operation_setting.USDExchangeRate).Mul(decimal.NewFromInt(multiplier)).String(), row.Retail)
		}
	}
	assert.Equal(t, map[string]string{"input": "20", "output": "50"}, directions)
	consume("baseline", "baseline")
	state = snapshot()
	def = state.Entries[0].Definition
	def.OfficialReferences = officialReferenceFixture("30", "https://example.com/pricing-b", "2026-11-10")
	def.OfficialReferences.Rows = append(def.OfficialReferences.Rows, outputReference)
	def.OfficialReferences.Rows[0].Reference.Conditions = "Revised standard text input"
	def.OfficialReferences.Rows[0].Reference.USDCNY = "7.2"
	save(def)
	assert.Equal(t, baseline, public(), "an unpublished reference must not change any public price")
	consume("draft", "unchanged")
	// An older admin payload omits the optional field. Its save must preserve B,
	// including an unpublished draft, rather than restore published A.
	def.OfficialReferences = nil
	save(def)
	require.Equal(t, "30", snapshot().Entries[0].Draft.Definition.OfficialReferences.Rows[0].Reference.OfficialCNY)
	publish()
	assert.Contains(t, string(public()), `"official_cny":"30"`)
	consume("published", "reference-updated")
	state = snapshot()
	def = state.Entries[0].Definition
	def.OfficialReferences = &model.ModelOfficialReferenceConfiguration{Mode: "disabled"}
	save(def)
	def.OfficialReferences = nil
	save(def)
	require.Equal(t, "disabled", snapshot().Entries[0].Draft.Definition.OfficialReferences.Mode)
	publish()
	assert.NotContains(t, string(public()), `"official_cny":"30"`)
	consume("disabled", "absent")
	// Actual public/PAT authorization cannot edit the root reference draft.
	denied := unifiedRequest(t, f, "PUT", "/api/option/model_catalog/draft", `{}`, "")
	assert.NotEqual(t, 200, denied.Code)
	stale := request("POST", "/api/option/model_catalog/publish", map[string]any{"revision": state.Revision, "models": []string{name}})
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Equal(t, http.StatusOK, request("GET", "/api/user/self", nil).Code, "the original authenticated session remains valid")
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	var after model.User
	require.NoError(t, f.db.First(&after, root.Id).Error)
	assert.Equal(t, root.AuthVersion, after.AuthVersion)
	assert.Zero(t, f.posts, "all prices are projected locally; no generation is sent")
}

func TestOfficialReferenceValidationAndExactSpecifications(t *testing.T) {
	valid := officialReferenceFixture("0.123456789012345678901234", "https://example.com/pricing", "2026-11-10").Rows[0]
	require.NoError(t, model.ValidateOfficialReferenceRow("text", valid))
	for _, tc := range []struct {
		name   string
		change func(*model.ModelOfficialReferenceRow)
	}{
		{"leading-zero", func(r *model.ModelOfficialReferenceRow) { r.Reference.OfficialCNY = "00.5" }},
		{"zero", func(r *model.ModelOfficialReferenceRow) { r.Reference.OfficialCNY = "0" }},
		{"model-whitespace", func(r *model.ModelOfficialReferenceRow) { r.Reference.Model = " model " }},
		{"conditions-whitespace", func(r *model.ModelOfficialReferenceRow) { r.Reference.Conditions = " terms " }},
		{"empty-param", func(r *model.ModelOfficialReferenceRow) { r.Params["quality"] = "" }},
		{"oversized-param", func(r *model.ModelOfficialReferenceRow) { r.Params["seconds"] = float64(1e10) }},
		{"unknown-param", func(r *model.ModelOfficialReferenceRow) { r.Params["secret"] = "hidden" }},
		{"source-token", func(r *model.ModelOfficialReferenceRow) {
			r.Reference.SourceURL = "https://example.com/pricing?token=private"
		}},
		{"private-source", func(r *model.ModelOfficialReferenceRow) { r.Reference.SourceURL = "https://127.0.0.1/pricing" }},
		{"invalid-date", func(r *model.ModelOfficialReferenceRow) { r.Reference.CheckedAt = "2026-02-30" }},
		{"future-date", func(r *model.ModelOfficialReferenceRow) { r.Reference.CheckedAt = "9999-01-01" }},
		{"missing-fx", func(r *model.ModelOfficialReferenceRow) { r.Reference.USDCNY = "" }},
		{"text-total", func(r *model.ModelOfficialReferenceRow) { r.Reference.OfficialTotalCNY = "1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := common.Marshal(valid)
			require.NoError(t, err)
			var row model.ModelOfficialReferenceRow
			require.NoError(t, common.Unmarshal(encoded, &row))
			tc.change(&row)
			assert.Error(t, model.ValidateOfficialReferenceRow("text", row))
		})
	}
	key := func(params map[string]any) string {
		return model.OfficialReferenceRowKey("video", params, "", "CNY / second")
	}
	assert.Equal(t, key(map[string]any{"seconds": float64(15), "mode": "text-to-video", "n": float64(1)}), key(map[string]any{"seconds": float64(15), "count": 1}))
	assert.NotEqual(t, key(map[string]any{"seconds": 15}), key(map[string]any{"seconds": 30}))
	assert.NotEqual(t, key(map[string]any{"resolution": "1K"}), key(map[string]any{"resolution": "2K"}))
	assert.NotEqual(t, key(map[string]any{"ratio": "1:1"}), key(map[string]any{"ratio": "16:9"}))
	for _, name := range []string{"ycapi-gemini-3-pro-image", "ycapi-doubao-seedream-5-0-pro"} {
		rows := model.BuiltinModelOfficialReferenceRows(name)
		require.NotEmpty(t, rows, name)
		for _, row := range rows {
			assert.NotNil(t, row.Params["size"], "%s must retain the exact declared ratio", name)
			assert.NotEqual(t, "1K", row.Params["size"])
		}
	}
}

func TestOfficialReferenceDraftLimitsAndAtomicRejection(t *testing.T) {
	_ = unifiedConfigurationFixture(t)
	const name = "reference-boundary-text"
	state, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	def := state.Entries[0].Definition
	def.Source = "Synthetic local reference boundary"
	def.Pricing = model.PricingValues{"ModelPrice": float64(0)}
	def.OfficialReferences = &model.ModelOfficialReferenceConfiguration{Mode: "override"}
	for i := range 256 {
		row := officialReferenceFixture("1", "https://example.com/pricing", "2026-11-10").Rows[0]
		row.Params["thinking_level"] = fmt.Sprintf("spec-%d", i)
		def.OfficialReferences.Rows = append(def.OfficialReferences.Rows, row)
	}
	require.NoError(t, model.SaveModelConfigurationDraft(state.Revision, state.Entries[0].BaseVersion, def))
	state, err = model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Len(t, state.Entries[0].Draft.Definition.OfficialReferences.Rows, 256)
	for _, change := range []func(*model.ModelConfiguration){
		func(def *model.ModelConfiguration) {
			def.OfficialReferences.Rows = append(def.OfficialReferences.Rows, def.OfficialReferences.Rows[0])
		},
		func(def *model.ModelConfiguration) { def.OfficialReferences.Rows[1] = def.OfficialReferences.Rows[0] },
		func(def *model.ModelConfiguration) { def.OfficialReferences.Mode = "disabled" },
	} {
		encoded, err := common.Marshal(state.Entries[0].Draft.Definition)
		require.NoError(t, err)
		var invalid model.ModelConfiguration
		require.NoError(t, common.Unmarshal(encoded, &invalid))
		change(&invalid)
		assert.Error(t, model.SaveModelConfigurationDraft(state.Revision, state.Entries[0].BaseVersion, invalid))
		after, err := model.GetModelConfigurationSnapshot(name)
		require.NoError(t, err)
		assert.Equal(t, state.Revision, after.Revision)
		assert.Equal(t, state.Entries[0].Draft, after.Entries[0].Draft)
	}
}

func TestModelExecutionVersionBatchQueriesAndInvalidation(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	ch := model.Channel{Id: 1151, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Batch fixture", Group: "default", Models: "batch-fixture", Key: "private-batch-fixture"}
	require.NoError(t, f.db.Create(&ch).Error)
	records := map[string]model.PublishedModelConfiguration{}
	for i := range 16 {
		name := fmt.Sprintf("batch-%d", i)
		records[name] = model.PublishedModelConfiguration{Version: fmt.Sprintf("version-%d", i), Definition: model.ModelConfiguration{ModelName: name, Routes: []model.ModelConfigurationRoute{{ChannelID: ch.Id, Group: "default", UpstreamModel: name, Enabled: true}}}}
		require.NoError(t, f.db.Create(&model.Ability{ChannelId: ch.Id, Group: "default", Model: name, Enabled: true}).Error)
	}
	var queries atomic.Int64
	require.NoError(t, f.db.Callback().Query().After("gorm:query").Register("reference_query_count", func(*gorm.DB) { queries.Add(1) }))
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove("reference_query_count") })
	singles := map[string]string{}
	for name, record := range records {
		singles[name] = model.ModelExecutionConfigurationVersion(name, record)
	}
	singleCount := queries.Swap(0)
	batch := model.ModelExecutionConfigurationVersions(records)
	batchCount := queries.Swap(0)
	assert.Equal(t, singles, batch, "the batched route/pricing digest preserves the original single-model contract")
	assert.EqualValues(t, 32, singleCount)
	assert.EqualValues(t, 2, batchCount)
	t.Logf("16 model version SQL queries: single=%d batch=%d", singleCount, batchCount)
	require.NoError(t, f.db.Model(&model.Ability{}).Where("model = ?", "batch-0").Update("enabled", false).Error)
	changed := model.ModelExecutionConfigurationVersions(records)
	assert.NotEqual(t, batch["batch-0"], changed["batch-0"], "committed route changes are visible in the very next request")
	assert.Equal(t, batch["batch-1"], changed["batch-1"])
	policy := model.ModelCapacityConfig{Schema: 1}
	candidates := map[string][]string{}
	for name := range records {
		candidates[name] = []string{name}
		policy.Routes = append(policy.Routes, model.CapacityRoute{Model: name, Group: "default", ChannelID: ch.Id, RouteModel: name, ContextWindow: common.GetPointer(32768), MaxOutputTokens: common.GetPointer(4096), BudgetField: "max_tokens", Source: "Synthetic local capacity", VerifiedAt: "2026-11-10", ChannelConfigHash: model.CapacityChannelConfigHash(&ch)})
	}
	require.NoError(t, model.SaveModelCapacityConfig(policy))
	queries.Store(0)
	singleCapacities := map[string]model.EffectiveModelCapacity{}
	for name := range records {
		capacity, err := model.ResolveModelCapacity(name, []string{"default"}, []string{name})
		require.NoError(t, err)
		singleCapacities[name] = capacity
	}
	singleCapacityQueries := queries.Swap(0)
	batchCapacities, err := model.ResolveModelCapacities(candidates, []string{"default"})
	require.NoError(t, err)
	batchCapacityQueries := queries.Swap(0)
	assert.Equal(t, singleCapacities, batchCapacities)
	// batch-0 has no enabled ability, so its single lookup stops after 2 SQL.
	assert.EqualValues(t, 47, singleCapacityQueries)
	assert.EqualValues(t, 3, batchCapacityQueries)
	assert.True(t, batchCapacities["batch-0"].Managed)
	assert.Empty(t, batchCapacities["batch-0"].Routes)
	assert.Equal(t, 32768, *batchCapacities["batch-1"].ContextWindow)
	t.Logf("16 model capacity SQL queries: single=%d batch=%d", singleCapacityQueries, batchCapacityQueries)
	require.NoError(t, f.db.Model(&ch).Update("key", "rotated-local-fixture").Error)
	rotated, err := model.ResolveModelCapacities(candidates, []string{"default"})
	require.NoError(t, err)
	assert.Empty(t, rotated["batch-1"].Routes, "rotated credentials invalidate capacity evidence immediately")
}

func TestUnifiedModelConfigurationStableTextIDSwitchesHostProtocols(t *testing.T) {
	for _, name := range []string{"stable-text-protocol", "ycapi-gpt-6.1-sol"} {
		t.Run(name, func(t *testing.T) {
			f := unifiedConfigurationFixture(t)
			for i, base := range []string{"https://ycapi-protocol.fixture", "https://openai-protocol.fixture"} {
				require.NoError(t, (&model.Channel{Id: 1152 + i, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Protocol fixture", Models: name, Group: "default", Key: "private-protocol-fixture", BaseURL: &base}).Insert())
			}
			first := publishUnifiedFixture(t, name, func(def *model.ModelConfiguration) {
				def.Enabled = true
				def.Pricing = model.PricingValues{"ModelRatio": float64(0.5), "CompletionRatio": float64(2)}
				def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1152, Group: "default", UpstreamModel: "ycapi-gpt-6.1-sol", Enabled: true, TextProtocol: "ycapi_fixed_lane"}}
			})
			state, err := model.GetModelConfigurationSnapshot(name)
			require.NoError(t, err)
			require.Equal(t, "ycapi_fixed_lane", state.Entries[0].Definition.Routes[0].TextProtocol)
			// Older save payloads omit the optional route protocol.
			def := state.Entries[0].Definition
			for i := range def.Routes {
				def.Routes[i].TextProtocol = ""
			}
			require.NoError(t, model.SaveModelConfigurationDraft(state.Revision, state.Entries[0].BaseVersion, def))
			state, err = model.GetModelConfigurationSnapshot(name)
			require.NoError(t, err)
			require.Equal(t, "ycapi_fixed_lane", state.Entries[0].Draft.Definition.Routes[0].TextProtocol)
			posts := 0
			service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
				posts++
				var body map[string]any
				require.NoError(t, common.DecodeJson(request.Body, &body))
				switch request.URL.Host {
				case "ycapi-protocol.fixture":
					assert.Equal(t, "/api/v1/model-runtime/invoke", request.URL.Path)
					assert.Equal(t, "gpt-6.1-sol", body["model_id"])
					assert.Equal(t, "gpt-6.1-sol@952:codex", body["lane_id"])
					assert.EqualValues(t, 4096, body["params"].(map[string]any)["max_tokens"])
				case "openai-protocol.fixture":
					assert.Equal(t, "/v1/chat/completions", request.URL.Path)
					assert.Equal(t, "native-openai-text", body["model"])
					assert.Nil(t, body["model_id"])
					assert.Nil(t, body["params"])
				default:
					t.Fatalf("unexpected supplier host %s", request.URL.Host)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"protocol-local","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"local result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`))}, nil
			})
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"local protocol fixture"}]}`, name)
			version := model.ModelExecutionConfigurationVersion(name, first)
			response := unifiedRequest(t, f, "POST", "/v1/chat/completions", body, version)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Equal(t, 1, posts)
			publishUnifiedFixture(t, name, func(def *model.ModelConfiguration) {
				def.Enabled = true
				def.Pricing = model.PricingValues{"ModelRatio": float64(0.5), "CompletionRatio": float64(2)}
				def.Routes = []model.ModelConfigurationRoute{{ChannelID: 1153, Group: "default", UpstreamModel: "native-openai-text", Enabled: true, TextProtocol: "openai"}}
			})
			stale := unifiedRequest(t, f, "POST", "/v1/chat/completions", body, version)
			assert.Equal(t, 400, stale.Code, stale.Body.String())
			assert.Equal(t, 1, posts, "a stale catalog is rejected before any supplier call")
			legacy := unifiedRequest(t, f, "POST", "/v1/chat/completions", body, "")
			require.Equal(t, 200, legacy.Code, legacy.Body.String())
			assert.Equal(t, 2, posts, "old clients use the stable model ID on the newly configured supplier exactly once")
			var charged int64
			require.NoError(t, f.logDB.Model(&model.Log{}).Where("model_name = ? AND type = ?", name, model.LogTypeConsume).Count(&charged).Error)
			assert.EqualValues(t, 2, charged)
		})
	}
}

func TestUnifiedModelConfigurationMissingPriceHiddenAndFreePricePreserved(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	const missing, free = "reference-unpriced-pending", "reference-explicit-free"
	base := "https://price-admission.fixture"
	require.NoError(t, (&model.Channel{Id: 1154, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "Price admission fixture", Models: missing + "," + free, Group: "default", Key: "synthetic-price-guard", BaseURL: &base}).Insert())
	prices, err := model.GetModelPricingSnapshot([]string{free})
	require.NoError(t, err)
	require.NoError(t, model.UpdateModelPricing([]model.ModelPricingChange{{ModelName: free, ExpectedVersion: prices.Entries[0].Version, Pricing: model.PricingValues{"ModelPrice": float64(0)}}}))
	previousSelfUse := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = previousSelfUse })
	assert.False(t, model.ModelBillingConfigured(missing))
	assert.True(t, model.ModelBillingConfigured(free))
	for _, path := range []string{"/v1/models", "/api/pricing"} {
		response := unifiedRequest(t, f, "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), missing)
		assert.Contains(t, response.Body.String(), free)
	}
	posts := 0
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(request *http.Request) (*http.Response, error) {
		posts++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"free-local","choices":[{"index":0,"message":{"role":"assistant","content":"free result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`))}, nil
	})
	wallet, token := f.balance(t)
	blocked := unifiedRequest(t, f, "POST", "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"must not send"}]}`, missing), "")
	assert.Equal(t, 400, blocked.Code, blocked.Body.String())
	assert.Contains(t, blocked.Body.String(), "model_price_unavailable")
	assert.Zero(t, posts)
	response := unifiedRequest(t, f, "POST", "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"explicit free"}]}`, free), "")
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Equal(t, 1, posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, wallet, afterWallet)
	assert.Equal(t, token, afterToken)
	// No price-cache invalidation here: a just-disabled route must disappear
	// immediately, even while a previous public pricing snapshot is warm.
	require.NoError(t, f.db.Model(&model.Ability{}).Where("model = ?", free).Update("enabled", false).Error)
	response = unifiedRequest(t, f, "GET", "/api/pricing", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), free)
}

func TestUnifiedModelConfigurationFreeMediaEvidencePrecedesQuotaRounding(t *testing.T) {
	f := unifiedConfigurationFixture(t)
	for _, tc := range []struct {
		expression string
		free       bool
	}{{`u("seconds") * 0`, true}, {`u("seconds") * 0.000000000000000001`, false}} {
		publishUnifiedFixture(t, "MiniMax-H3", func(def *model.ModelConfiguration) {
			def.Kind, def.Enabled = "video", true
			def.ContextWindow, def.MaxOutputTokens = nil, nil
			def.Parameters = map[string]any{"type": "object", "properties": map[string]any{
				"seconds":    map[string]any{"type": "integer", "enum": []any{float64(5)}},
				"resolution": map[string]any{"type": "string", "enum": []any{"768P"}},
				"ratio":      map[string]any{"type": "string", "enum": []any{"16:9"}},
			}}
			def.Routes = []model.ModelConfigurationRoute{{ChannelID: 790, Group: "default", UpstreamModel: "MiniMax-H3", Enabled: true}}
			def.Pricing = model.PricingValues{billing_setting.PluginBillingExprOption: map[string]any{"hailuo": tc.expression}}
		})
		response := unifiedRequest(t, f, "GET", "/api/pricing", "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		var document struct {
			Data []struct {
				Name string           `json:"model_name"`
				Rows []map[string]any `json:"retail_price_rows"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &document))
		matched := false
		for _, item := range document.Data {
			if item.Name != "MiniMax-H3" {
				continue
			}
			require.NotEmpty(t, item.Rows)
			for _, row := range item.Rows {
				assert.Equal(t, "0", row["retail_cny"])
				assert.Equal(t, tc.free, row["is_free"] == true)
			}
			matched = true
		}
		assert.Equal(t, tc.free, matched, "an unrepresentable positive tariff must not be advertised as free or left selectable pending pricing")
	}
	assert.Zero(t, f.posts, "free-price evidence is computed locally without any provider submission")
}
