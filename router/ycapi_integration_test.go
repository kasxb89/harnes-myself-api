package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	apitypes "github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ycapiFixtureTransport struct{ base *url.URL }

func (transport ycapiFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Hostname() != "ycapi.fixture" {
		return nil, fmt.Errorf("synthetic YCAPI fixture blocked an external request")
	}
	copy := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = transport.base.Scheme, transport.base.Host
	copy.URL = &target
	return http.DefaultTransport.RoundTrip(copy)
}

func TestYcapiApprovedMediaWirePlansAndDurableTerminalState(t *testing.T) {
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{})
	require.NoError(t, err)
	models, err := ycapi.Models()
	require.NoError(t, err)
	for _, m := range models {
		if m.Kind == "text" {
			continue
		}
		for index, offer := range m.Offers {
			t.Run(m.ID+"/"+offer.ID, func(t *testing.T) {
				request := map[string]any{"model": m.ID}
				if m.Kind == "audio" {
					request["input"] = "你好，Hello🎈"
					request["voice"] = m.Params["voice"].Enum[0]
					request["response_format"] = m.Params["response_format"].Enum[0]
				} else {
					parameters := ycapi.ClientParameters(m.ID)
					branch := parameters["oneOf"].([]any)[index].(map[string]any)["properties"].(map[string]any)
					for key, value := range branch {
						field := value.(map[string]any)
						switch values := field["enum"].(type) {
						case []any:
							request[key] = values[0]
						case []string:
							request[key] = values[0]
						}
					}
					request["prompt"], request["n"] = "Synthetic local generation", 1
				}
				ctx := map[string]any{"model": m.ID, "upstreamModel": m.ID, "baseUrl": "https://ycapi.fixture", "authHeader": "Bearer synthetic-fixture", "publicTaskId": "task_fixed_local", "requestBody": request}
				value, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
				require.NoError(t, err)
				wire := value.(map[string]any)["body"].(map[string]any)
				assert.Equal(t, "task_fixed_local", wire["client_request_id"])
				assert.Equal(t, m.Prices[index].LaneID, wire["lane_id"], "the selected specification must pin its approved lane")
				if m.Kind == "video" {
					assert.Equal(t, m.UpstreamID, wire["model"])
					assert.Equal(t, offer.Params["duration"], wire["duration"])
				} else {
					assert.Equal(t, m.UpstreamID, wire["model_id"])
					assert.Equal(t, m.Kind, wire["modality"])
				}
				if m.Kind == "audio" {
					usage, err := plugin.Engine.Call(context.Background(), "extractUsage", ctx)
					require.NoError(t, err)
					assert.EqualValues(t, len([]byte(request["input"].(string))), usage.(map[string]any)["input_tokens"])
				}
				unsafe := maps.Clone(request)
				unsafe["lane_id"] = "caller-override"
				ctx["requestBody"] = unsafe
				_, err = plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
				assert.Error(t, err)
			})
		}
	}
	ctx := map[string]any{"model": "ycapi-gpt-4o-mini-tts", "upstreamModel": "ycapi-gpt-4o-mini-tts", "taskId": "provider-original", "state": map[string]any{"kind": "audio", "format": "wav", "facts": map[string]any{"input_tokens": 1, "output_tokens": 8192}}}
	body := map[string]any{"task_id": "provider-original", "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/audio.wav"}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}}
	value, err := plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, body)
	require.NoError(t, err)
	terminal := value.(map[string]any)
	assert.Equal(t, "SUCCESS", terminal["status"])
	artifacts, err := plugin.Engine.Call(context.Background(), "listArtifacts", map[string]any{"status": "SUCCESS", "data": body, "state": terminal["state"]})
	require.NoError(t, err)
	assert.Equal(t, "audio/wav", artifacts.([]any)[0].(map[string]any)["mimeType"])
	body["is_final"] = false
	value, err = plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, body)
	require.NoError(t, err)
	assert.Equal(t, "IN_PROGRESS", value.(map[string]any)["status"])
	body["task_id"] = "another-task"
	_, err = plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, body)
	assert.Error(t, err)
}

func newYcapiRouterFixture(t *testing.T) *h3RouterFixture {
	t.Helper()
	f := newH3RouterFixture(t, 1)
	common.OptionMapRWMutex.RLock()
	previousCapacity := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.RUnlock()
	previousBilling, err := config.ConfigToMap(config.GlobalConfig.Get("billing_setting"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(config.GlobalConfig.Get("billing_setting"), previousBilling))
		common.OptionMapRWMutex.Lock()
		if previousCapacity == "" {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		} else {
			common.OptionMap[model.ModelCapacityOptionKey] = previousCapacity
		}
		common.OptionMapRWMutex.Unlock()
	})
	previousStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousStreamingTimeout })
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	models, err := ycapi.Models()
	require.NoError(t, err)
	capacities := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{}}
	var names, media []string
	for i, m := range models {
		names = append(names, m.ID)
		if m.Kind != "text" {
			media = append(media, m.ID)
			continue
		}
		mapping, err := common.Marshal(map[string]string{m.ID: m.UpstreamID})
		require.NoError(t, err)
		override, err := common.Marshal(map[string]string{"lane_id": m.Prices[0].LaneID})
		require.NoError(t, err)
		base, mapped, params := "https://ycapi.fixture", string(mapping), string(override)
		channel := model.Channel{Id: 900 + i, Type: constant.ChannelTypeOpenAI, Key: "synthetic-ycapi-only", Name: m.ID, Models: m.ID, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base, ModelMapping: &mapped, ParamOverride: &params}
		require.NoError(t, channel.Insert())
		capacities.Routes = append(capacities.Routes, model.CapacityRoute{Model: m.ID, Group: "default", ChannelID: channel.Id, RouteModel: m.ID, ContextWindow: common.GetPointer(m.ContextWindow), MaxOutputTokens: common.GetPointer(m.MaxOutputTokens), BudgetField: "max_tokens", Source: "approved local product defaults; supplier context declaration", VerifiedAt: "2026-10-31"})
	}
	base := "https://ycapi.fixture"
	channel := model.Channel{Id: 999, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-ycapi-only", Name: "YCAPI media fixture", Models: strings.Join(media, ","), Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	channel.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, channel.Insert())
	model.InitChannelCache()
	require.NoError(t, model.SaveModelCapacityConfig(capacities))
	config, err := model.GetModelPricingSnapshot(names)
	require.NoError(t, err)
	var changes []model.ModelPricingChange
	for _, entry := range config.Entries {
		expr, err := ycapi.BillingExpression(entry.ModelName, decimal.NewFromFloat(operation_setting.USDExchangeRate))
		require.NoError(t, err)
		changes = append(changes, model.ModelPricingChange{ModelName: entry.ModelName, ExpectedVersion: entry.Version, Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expr}})
	}
	require.NoError(t, model.UpdateModelPricing(changes))
	model.RefreshPricing()
	return f
}

func TestYcapiApprovedCatalogPricesAndSafeOrdinaryDiscovery(t *testing.T) {
	f := newYcapiRouterFixture(t)
	models := f.request(t, http.MethodGet, "/v1/models", "", "h3routermember")
	require.Equal(t, http.StatusOK, models.Code, models.Body.String())
	var catalog struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(models.Body.Bytes(), &catalog))
	count, priceRows := 0, 0
	for _, entry := range catalog.Data {
		id, _ := entry["id"].(string)
		if !strings.HasPrefix(id, "ycapi-") {
			continue
		}
		count++
		approved, ok := ycapi.Lookup(id)
		require.True(t, ok)
		assert.Equal(t, approved.Kind, entry["kind"])
		prices, ok := entry["retail_price_rows"].([]any)
		require.True(t, ok)
		priceRows += len(prices)
		if approved.Kind == "text" {
			assert.EqualValues(t, approved.ContextWindow, entry["context_window"])
			assert.EqualValues(t, 8192, entry["max_output_tokens"])
		}
	}
	assert.Equal(t, 43, count)
	assert.Equal(t, 113, priceRows)
	for _, key := range []string{"procurement", "lane_id", "channel_id", "synthetic-ycapi-only", "ycapi-flux-2\"", "ycapi-seedance-2-5-special\""} {
		assert.NotContains(t, models.Body.String(), key)
	}
	pricing := f.request(t, http.MethodGet, "/api/pricing", "", "h3routermember")
	require.Equal(t, http.StatusOK, pricing.Code, pricing.Body.String())
	assert.Contains(t, pricing.Body.String(), `"retail_price_rows"`)
	assert.NotContains(t, pricing.Body.String(), `"lane_id"`)
	assert.NotContains(t, pricing.Body.String(), `"procurementCny"`)
	protected := f.request(t, http.MethodGet, "/api/ycapi/catalog", "", "h3routermember")
	assert.NotContains(t, protected.Body.String(), `"procurementCny"`)
}

func TestYcapiRemovedModelRejectsStaleChannelBeforeQuotaOrUpstream(t *testing.T) {
	f := newYcapiRouterFixture(t)
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Close)
	base, err := url.Parse(provider.URL)
	require.NoError(t, err)
	service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
	staleBase := "https://ycapi.fixture"
	channel := model.Channel{Id: 998, Type: constant.ChannelTypeOpenAI, Key: "synthetic-ycapi-only", Name: "stale removed model", Models: "ycapi-grok-4.5", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &staleBase}
	require.NoError(t, channel.Insert())
	model.InitChannelCache()
	var before, after model.User
	require.NoError(t, f.db.First(&before, 792).Error)
	response := f.request(t, http.MethodPost, "/v1/chat/completions", `{"model":"ycapi-grok-4.5","messages":[{"role":"user","content":"stale selection"}],"stream":true}`, "h3routermember")
	assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "model_not_available")
	assert.Zero(t, posts)
	require.NoError(t, f.db.First(&after, 792).Error)
	assert.Equal(t, before.Quota, after.Quota)
	var charged int64
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("model_name = ? AND type = ?", "ycapi-grok-4.5", model.LogTypeConsume).Count(&charged).Error)
	assert.Zero(t, charged)
}

func TestYcapiTextPinsRuntimeLaneClampsBudgetAndRejectsRouteOverrides(t *testing.T) {
	f := newYcapiRouterFixture(t)
	var wire map[string]any
	posts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		require.Equal(t, "/api/v1/model-runtime/invoke", r.URL.Path)
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(data, &wire))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"synthetic\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":30,\"total_tokens\":80}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	base, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
	response := f.request(t, http.MethodPost, "/v1/chat/completions", `{"model":"ycapi-gpt-6.1-sol","messages":[{"role":"system","content":"Local fixture"},{"role":"user","content":"Hello"}],"max_tokens":16384,"stream":true,"stream_options":{"include_usage":true}}`, "h3routermember")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.EqualValues(t, 8192, wire["params"].(map[string]any)["max_tokens"])
	assert.Equal(t, "gpt-6.1-sol", wire["model_id"])
	assert.Equal(t, "gpt-6.1-sol@952:codex", wire["lane_id"])
	assert.Contains(t, response.Body.String(), "[DONE]")
	rejected := f.request(t, http.MethodPost, "/v1/chat/completions", `{"model":"ycapi-gpt-6.1-sol","messages":[{"role":"user","content":"Hello"}],"lane_id":"forged-lane","stream":true}`, "h3routermember")
	assert.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
	assert.Equal(t, 1, posts)
	var pending int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("platform = ?", "ycapi").Count(&pending).Error)
	assert.Zero(t, pending)
	// Force no upstream access in the free-audio quote path.
	quote := f.request(t, http.MethodPost, "/v1/audio/quote", `{"model":"ycapi-gpt-4o-mini-tts","input":"Hello","voice":"alloy","response_format":"mp3"}`, "h3routermember")
	require.Equal(t, http.StatusOK, quote.Code, quote.Body.String())
	assert.Contains(t, quote.Body.String(), `"estimate_only":true`)
	assert.Equal(t, 1, posts)
	var responseQuote map[string]any
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &responseQuote))
	assert.NotEmpty(t, responseQuote["request_fingerprint"])
	assert.NotContains(t, quote.Body.String(), "synthetic-ycapi-only")
}

func TestYcapiTextToolRoundtripImageControlsAndPrivateUsage(t *testing.T) {
	for _, includeUsage := range []bool{true, false} {
		t.Run(fmt.Sprintf("include_usage_%t", includeUsage), func(t *testing.T) {
			f := newYcapiRouterFixture(t)
			var pngBytes bytes.Buffer
			require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))))
			imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
			posts := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				require.Equal(t, "/api/v1/model-runtime/invoke", r.URL.Path)
				var wire map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &wire))
				assert.Equal(t, "qwen3.6-plus", wire["model_id"])
				assert.Equal(t, "qwen3.6-plus@VST:zq-qwen", wire["lane_id"])
				params := wire["params"].(map[string]any)
				assert.EqualValues(t, 2048, params["max_tokens"])
				assert.Len(t, params["tools"], 1)
				assert.NotContains(t, params, "messages")
				messages := wire["messages"].([]any)
				parts := messages[0].(map[string]any)["content"].([]any)
				assert.Equal(t, imageURL, parts[1].(map[string]any)["image_url"].(map[string]any)["url"])
				w.Header().Set("Content-Type", "text/event-stream")
				if posts == 1 {
					assert.Equal(t, false, params["enable_thinking"], "the explicit off value must survive the gateway")
					_, _ = io.WriteString(w, "data: {\"id\":\"local-tool\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_fixture_echo\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"fixture\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":23,\"total_tokens\":40,\"completion_tokens_details\":{\"reasoning_tokens\":7}}}\n\n")
				} else {
					assert.Equal(t, true, params["enable_thinking"])
					assert.Equal(t, "call_fixture_echo", messages[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["id"])
					assert.Equal(t, "tool", messages[2].(map[string]any)["role"])
					assert.Equal(t, "call_fixture_echo", messages[2].(map[string]any)["tool_call_id"])
					assert.Equal(t, "fixture-echo-ack", messages[2].(map[string]any)["content"])
					_, _ = io.WriteString(w, "data: {\"id\":\"local-tool\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture-echo-ack\",\"reasoning_content\":\"synthetic reasoning\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":23,\"total_tokens\":40,\"completion_tokens_details\":{\"reasoning_tokens\":7}}}\n\n")
				}
				// Real supplier compatibility: this separate last usage frame is private
				// procurement accounting, not a zero-token usage or a customer charge.
				_, _ = io.WriteString(w, "data: {\"id\":\"local-tool\",\"choices\":[],\"usage\":{\"cost_rmb\":9123,\"balance_rmb\":19876,\"cost_currency\":\"CNY\"}}\n\ndata: {\"omf_billing\":{\"actual_cost_credits\":9123,\"channel_id\":\"private-supplier-route\"}}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(provider.Close)
			base, err := url.Parse(provider.URL)
			require.NoError(t, err)
			service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
			messages := []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": "Read the image using echo"}, {"type": "image_url", "image_url": map[string]string{"url": imageURL}}}}}
			request := map[string]any{"model": "ycapi-qwen3.6-plus", "messages": messages, "max_tokens": 2048, "stream": true, "stream_options": map[string]bool{"include_usage": includeUsage}, "enable_thinking": false, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "echo", "parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]string{"type": "string"}}}}}}}
			raw, err := common.Marshal(request)
			require.NoError(t, err)
			first := f.request(t, http.MethodPost, "/v1/chat/completions", string(raw), "h3routermember")
			require.Equal(t, http.StatusOK, first.Code, first.Body.String())
			assert.Contains(t, first.Body.String(), "call_fixture_echo")
			assert.Contains(t, first.Body.String(), "tool_calls")
			request["messages"] = append(messages, map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"id": "call_fixture_echo", "type": "function", "function": map[string]string{"name": "echo", "arguments": `{"value":"fixture"}`}}}}, map[string]any{"role": "tool", "tool_call_id": "call_fixture_echo", "content": "fixture-echo-ack"})
			request["enable_thinking"] = true
			raw, err = common.Marshal(request)
			require.NoError(t, err)
			second := f.request(t, http.MethodPost, "/v1/chat/completions", string(raw), "h3routermember")
			require.Equal(t, http.StatusOK, second.Code, second.Body.String())
			assert.Contains(t, second.Body.String(), "fixture-echo-ack")
			assert.Contains(t, second.Body.String(), "reasoning_content")
			for _, response := range []*httptest.ResponseRecorder{first, second} {
				assert.NotContains(t, response.Body.String(), "cost_rmb")
				assert.NotContains(t, response.Body.String(), "balance_rmb")
				assert.NotContains(t, response.Body.String(), "19876")
				assert.NotContains(t, response.Body.String(), "omf_billing")
				assert.NotContains(t, response.Body.String(), "private-supplier-route")
				assert.NotContains(t, response.Body.String(), `"usage":{}`)
				usageFrames := 0
				for _, line := range strings.Split(response.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") || strings.TrimSpace(line) == "data: [DONE]" {
						continue
					}
					var frame map[string]any
					require.NoError(t, common.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame))
					if usage, present := frame["usage"]; present {
						values := usage.(map[string]any)
						assert.EqualValues(t, 17, values["prompt_tokens"])
						assert.EqualValues(t, 23, values["completion_tokens"])
						assert.EqualValues(t, 40, values["total_tokens"])
						usageFrames++
					}
				}
				assert.Positive(t, usageFrames, "ordinary SDKs must receive the actual token usage")
			}
			assert.Equal(t, 2, posts)
			var logs []model.Log
			require.NoError(t, f.logDB.Where("model_name = ? AND type = ?", "ycapi-qwen3.6-plus", model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 2)
			m, ok := ycapi.Lookup("ycapi-qwen3.6-plus")
			require.True(t, ok)
			cny := m.Prices[0].Retail.Mul(decimal.NewFromInt(17)).Add(m.Prices[1].Retail.Mul(decimal.NewFromInt(23))).Div(decimal.NewFromInt(1000000))
			expected, clamp := common.QuotaRoundChecked(cny.Div(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64())
			require.Nil(t, clamp)
			for _, log := range logs {
				assert.Equal(t, 17, log.PromptTokens)
				assert.Equal(t, 23, log.CompletionTokens)
				assert.Equal(t, expected, log.Quota, "reasoning tokens included in completion must not be charged twice")
			}
		})
	}
}

func TestYcapiSpeechQuoteRejectsChangedExpiredAndForeignRequestsBeforeAdmission(t *testing.T) {
	f := newYcapiRouterFixture(t)
	userBefore, tokenBefore := f.balance(t)
	body := map[string]any{"model": "ycapi-gpt-4o-mini-tts", "input": "你好，Hello", "voice": "alloy", "response_format": "mp3"}
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	quote := f.request(t, http.MethodPost, "/v1/audio/quote", string(encoded), "h3routermember")
	require.Equal(t, http.StatusOK, quote.Code, quote.Body.String())
	var quoted map[string]any
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &quoted))
	for _, scenario := range []string{"missing", "changed-input", "changed-voice", "expired", "foreign-account"} {
		t.Run(scenario, func(t *testing.T) {
			request := maps.Clone(body)
			request["quote_id"] = quoted["quote_id"]
			credential := "h3routermember"
			switch scenario {
			case "missing":
				delete(request, "quote_id")
			case "changed-input":
				request["input"] = "Different input"
			case "changed-voice":
				request["voice"] = "nova"
			case "expired":
				_, signature, _ := strings.Cut(quoted["quote_id"].(string), ".")
				request["quote_id"] = fmt.Sprintf("%d.%s", time.Now().Unix()-61, signature)
			case "foreign-account":
				credential = "h3routeradmin"
			}
			encoded, err := common.Marshal(request)
			require.NoError(t, err)
			response := f.request(t, http.MethodPost, "/v1/audio/speech", string(encoded), credential)
			assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "quote_changed")
		})
	}
	var tasks, admissions int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Count(&admissions).Error)
	assert.Zero(t, tasks)
	assert.Zero(t, admissions)
	f.mu.Lock()
	assert.Zero(t, f.posts)
	f.mu.Unlock()
	userAfter, tokenAfter := f.balance(t)
	assert.Equal(t, userBefore, userAfter)
	assert.Equal(t, tokenBefore, tokenAfter)
	var member model.User
	require.NoError(t, f.db.First(&member, 792).Error)
	assert.Equal(t, 100000000, member.Quota)
}

func TestYcapiMediaDurableAdmissionPollingAndOwnerIsolation(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio"} {
		scenarios := []string{"success", "lost-create-response"}
		if kind == "audio" {
			scenarios = append(scenarios, "immediate-with-usage", "completed-without-usage")
		}
		for _, scenario := range scenarios {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				f := newYcapiRouterFixture(t)
				posts := 0
				var providerID, upstreamModel string
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodPost {
						posts++
						var body map[string]any
						data, err := io.ReadAll(r.Body)
						require.NoError(t, err)
						require.NoError(t, common.Unmarshal(data, &body))
						providerID, _ = body["client_request_id"].(string)
						upstreamModel, _ = body["model_id"].(string)
						if kind == "video" {
							upstreamModel, _ = body["model"].(string)
						}
						require.NotEmpty(t, providerID)
						require.NotEmpty(t, body["lane_id"])
						if scenario == "lost-create-response" {
							_, _ = io.WriteString(w, `{"task_id":`)
							return
						}
						if scenario == "immediate-with-usage" {
							encoded, _ := common.Marshal(map[string]any{"task_id": providerID, "model": upstreamModel, "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/synthetic-result"}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}})
							_, _ = w.Write(encoded)
							return
						}
						encoded, _ := common.Marshal(map[string]any{"task_id": providerID, "status": "queued", "is_final": false})
						_, _ = w.Write(encoded)
						return
					}
					require.True(t, strings.HasSuffix(r.URL.Path, "/"+providerID))
					body := map[string]any{"task_id": providerID, "model": upstreamModel, "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/synthetic-result"}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}}
					if scenario == "completed-without-usage" {
						delete(body, "usage")
					}
					encoded, _ := common.Marshal(body)
					_, _ = w.Write(encoded)
				}))
				t.Cleanup(provider.Close)
				base, err := url.Parse(provider.URL)
				require.NoError(t, err)
				service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
				path := "/v1/images/generations"
				body := map[string]any{"model": "ycapi-doubao-seedream-5-0-pro", "prompt": "Synthetic router fixture", "resolution": "1k", "size": "1:1", "n": 1}
				if kind == "video" {
					path = "/v1/videos"
					body = map[string]any{"model": "ycapi-grok-imagine-1.5", "prompt": "Synthetic router fixture", "resolution": "1080p", "ratio": "16:9", "seconds": 10, "n": 1}
				}
				if kind == "audio" {
					path = "/v1/audio/speech"
					body = map[string]any{"model": "ycapi-gpt-4o-mini-tts", "input": "Synthetic router fixture", "voice": "alloy", "response_format": "mp3"}
					encoded, err := common.Marshal(body)
					require.NoError(t, err)
					quote := f.request(t, http.MethodPost, "/v1/audio/quote", string(encoded), "h3routermember")
					require.Equal(t, http.StatusOK, quote.Code, quote.Body.String())
					var value map[string]any
					require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &value))
					body["quote_id"] = value["quote_id"]
				}
				encoded, err := common.Marshal(body)
				require.NoError(t, err)
				response := f.request(t, http.MethodPost, path, string(encoded), "h3routermember")
				require.Equal(t, 1, posts, response.Body.String())
				var tasks []model.Task
				require.NoError(t, f.db.Where("platform = ?", "ycapi").Find(&tasks).Error)
				require.Len(t, tasks, 1, response.Body.String())
				task := tasks[0]
				require.NotEmpty(t, task.PrivateData.MediaAdmissionID)
				assert.Equal(t, kind, task.PrivateData.MediaKind)
				var beforePoll model.User
				require.NoError(t, f.db.First(&beforePoll, 792).Error)
				for range 2 {
					service.RunTaskPollingOnce(context.Background(), nil)
				}
				task = *f.task(t, task.TaskID)
				var admission model.MediaAdmission
				require.NoError(t, f.db.First(&admission, "id = ?", task.PrivateData.MediaAdmissionID).Error)
				foreign := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routeradmin")
				assert.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
				assert.NotContains(t, foreign.Body.String(), "cdn.example")
				if scenario == "success" || scenario == "immediate-with-usage" {
					assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
					assert.EqualValues(t, model.TaskStatusSuccess, task.Status)
					assert.Equal(t, model.MediaReleased, admission.State)
					assert.True(t, admission.ProviderTerminal)
					artifacts := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", "", "h3routermember")
					assert.Equal(t, http.StatusOK, artifacts.Code, artifacts.Body.String())
					assert.Contains(t, artifacts.Body.String(), `"key":"`+kind+`"`)
					assert.NotContains(t, artifacts.Body.String(), "cdn.example")
					if kind == "audio" {
						m, _ := ycapi.Lookup(task.Properties.OriginModelName)
						cost := decimal.Zero
						for _, price := range m.Prices {
							quantity := int64(5)
							if price.Direction == "output" {
								quantity = 10
							}
							cost = cost.Add(price.Retail.Mul(decimal.NewFromInt(quantity)).Div(decimal.NewFromInt(1000000)))
						}
						expected, clamp := common.QuotaRoundChecked(cost.Div(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64())
						require.Nil(t, clamp)
						assert.Equal(t, expected, task.Quota, "actual supplier usage must replace the 8192-token estimate")
					}
					if kind == "image" {
						owner := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routermember")
						assert.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
						assert.Contains(t, owner.Body.String(), `"result":{"data":[{"url":`)
						assert.Contains(t, owner.Body.String(), "/artifacts/image/content")
						assert.NotContains(t, owner.Body.String(), "cdn.example")
					}
				} else {
					assert.EqualValues(t, model.TaskStatusUnknown, task.Status)
					if scenario == "lost-create-response" {
						assert.Equal(t, model.MediaUnknown, admission.State)
					} else {
						assert.NotEqual(t, model.MediaReleased, admission.State)
					}
					assert.False(t, admission.ProviderTerminal)
					if scenario == "completed-without-usage" {
						assert.Equal(t, "reserved", admission.BillingState)
						var afterPoll model.User
						require.NoError(t, f.db.First(&afterPoll, 792).Error)
						assert.Equal(t, beforePoll.Quota, afterPoll.Quota, "missing actual usage must not trigger a final charge or refund")
					}
					owner := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routermember")
					assert.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
					assert.Contains(t, strings.ToLower(owner.Body.String()), "unknown")
					assert.NotContains(t, owner.Body.String(), "cdn.example")
				}
				assert.Equal(t, 1, posts, "GET and polling must never replay a creation")
			})
		}
	}
}

func TestYcapiSpeechLeavesExistingProvidersOnLegacyRoute(t *testing.T) {
	f := newYcapiRouterFixture(t)
	base := "https://ycapi.fixture"
	legacy := model.Channel{Id: 1000, Type: constant.ChannelTypeOpenAI, Key: "synthetic-legacy-speech", Name: "Existing speech fixture", Models: "tts-1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	require.NoError(t, legacy.Insert())
	model.InitChannelCache()
	wav := bytes.NewBuffer(nil)
	_, _ = wav.WriteString("RIFF")
	require.NoError(t, binary.Write(wav, binary.LittleEndian, uint32(68)))
	_, _ = wav.WriteString("WAVEfmt ")
	for _, field := range []any{uint32(16), uint16(1), uint16(1), uint32(16000), uint32(32000), uint16(2), uint16(16)} {
		require.NoError(t, binary.Write(wav, binary.LittleEndian, field))
	}
	_, _ = wav.WriteString("data")
	require.NoError(t, binary.Write(wav, binary.LittleEndian, uint32(32)))
	_, _ = wav.Write(make([]byte, 32))
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		assert.Equal(t, "/v1/audio/speech", r.URL.Path)
		var body map[string]any
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(data, &body))
		assert.Equal(t, "tts-1", body["model"])
		assert.Equal(t, "alloy", body["voice"])
		assert.NotContains(t, body, "quote_id")
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav.Bytes())
	}))
	t.Cleanup(provider.Close)
	url, err := url.Parse(provider.URL)
	require.NoError(t, err)
	service.GetHttpClient().Transport = ycapiFixtureTransport{base: url}
	response := f.request(t, http.MethodPost, "/v1/audio/speech", `{"model":"tts-1","input":"Legacy local speech","voice":"alloy","response_format":"wav"}`, "h3routermember")
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "audio/wav", response.Header().Get("Content-Type"))
	assert.Equal(t, wav.Bytes(), response.Body.Bytes())
	assert.Equal(t, 1, posts)
	var admissions int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Count(&admissions).Error)
	assert.Zero(t, admissions, "legacy speech must not enter the YCAPI task lifecycle")
}

func TestYcapiAll44VideoQuotesUseExactApprovedQualityAndCNY(t *testing.T) {
	f := newYcapiRouterFixture(t)
	settings := operation_setting.GetGeneralSetting()
	previous := settings.QuotaDisplayType
	settings.QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	t.Cleanup(func() { settings.QuotaDisplayType = previous })
	models, err := ycapi.Models()
	require.NoError(t, err)
	rows := 0
	for _, m := range models {
		if m.Kind != "video" {
			continue
		}
		for index, offer := range m.Offers {
			t.Run(m.ID+"/"+offer.ID, func(t *testing.T) {
				parameters := map[string]any{"seconds": decimal.RequireFromString(fmt.Sprint(offer.Params["duration"])).IntPart()}
				for _, key := range []string{"quality", "resolution", "aspect_ratio"} {
					if value, present := offer.Params[key]; present {
						parameters[key] = value
					}
				}
				request := apitypes.VideoQuoteRequest{Model: m.ID, WorksCount: 1, Segments: []apitypes.VideoQuoteSegmentRequest{{ID: "clip-1", Parameters: parameters}}}
				encoded, err := common.Marshal(request)
				require.NoError(t, err)
				response := f.request(t, http.MethodPost, "/v1/video/quote", string(encoded), "h3routermember")
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var quote apitypes.VideoQuoteResponse
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
				assert.Equal(t, "CNY", quote.Currency)
				assert.False(t, quote.Charged)
				assert.False(t, quote.Reserved)
				assert.False(t, quote.Submitted)
				actual := decimal.RequireFromString(quote.EstimatedTotal)
				tolerance := decimal.NewFromFloat(2 * quote.USDExchangeRate / quote.QuotaPerUSD)
				assert.True(t, actual.Sub(m.Prices[index].RetailTotal).Abs().LessThanOrEqual(tolerance), "%s differs from the exact approved total %s", actual, m.Prices[index].RetailTotal)
			})
			rows++
		}
	}
	assert.Equal(t, 44, rows)
	for _, request := range []string{
		`{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"clip-1","parameters":{"seconds":5,"quality":"标准"}}]}`,
		`{"model":"ycapi-seedance-2-0-official","works_count":1,"segments":[{"id":"clip-1","parameters":{"seconds":5,"resolution":"1080p","quality":{},"ratio":"16:9"}}]}`,
		`{"model":"ycapi-seedance-2-0-official","works_count":1,"segments":[{"id":"clip-1","parameters":{"seconds":5,"resolution":"1080p","quality":"unknown","ratio":"16:9"}}]}`,
	} {
		response := f.request(t, http.MethodPost, "/v1/video/quote", request, "h3routermember")
		assert.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
	}
	var tasks int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	assert.Zero(t, tasks)
	assert.Zero(t, f.posts)
}

func TestYcapiAll22MultimodalRoutesKeepApprovedBudgetAndLane(t *testing.T) {
	f := newYcapiRouterFixture(t)
	var wire map[string]any
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		wire = nil
		require.NoError(t, common.Unmarshal(data, &wire))
		assert.Equal(t, "/api/v1/model-runtime/invoke", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":30,\"total_tokens\":80}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	base, err := url.Parse(provider.URL)
	require.NoError(t, err)
	service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
	models, err := ycapi.Models()
	require.NoError(t, err)
	expectedPosts := 0
	for _, m := range models {
		if m.Kind != "text" {
			continue
		}
		t.Run(m.ID, func(t *testing.T) {
			encoded, err := common.Marshal(map[string]any{"model": m.ID, "messages": []map[string]string{{"role": "user", "content": "Synthetic plain text fixture"}}, "max_tokens": 16384, "stream": true, "stream_options": map[string]bool{"include_usage": true}})
			require.NoError(t, err)
			response := f.request(t, http.MethodPost, "/v1/chat/completions", string(encoded), "h3routermember")
			capability, err := ycapi.LookupTextCapability(m.ID)
			require.NoError(t, err)
			if capability.OperationalStatus == "fixed_lane_unavailable" || capability.OperationalStatus == "upstream_quota_exhausted" {
				assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
				return
			}
			expectedPosts++
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "[DONE]")
			assert.Equal(t, m.UpstreamID, wire["model_id"])
			assert.Equal(t, m.Prices[0].LaneID, wire["lane_id"])
			params := wire["params"].(map[string]any)
			budget := params["max_tokens"]
			if budget == nil {
				budget = params["max_completion_tokens"]
			}
			assert.EqualValues(t, 8192, budget)
			assert.Nil(t, params["tools"])
			assert.Nil(t, params["reasoning"])
			assert.True(t, params["reasoning_effort"] == nil || params["reasoning_effort"] == "none")
		})
	}
	assert.Equal(t, expectedPosts, posts)
}

func TestYcapiAudioRequiresActualMeteringBeforeFinalSettlement(t *testing.T) {
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{})
	require.NoError(t, err)
	ctx := map[string]any{"model": "ycapi-gpt-4o-mini-tts", "upstreamModel": "ycapi-gpt-4o-mini-tts", "taskId": "provider-audio", "requestBody": map[string]any{"model": "ycapi-gpt-4o-mini-tts", "input": "Hello", "voice": "alloy", "response_format": "mp3"}}
	body := map[string]any{"task_id": "provider-audio", "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/audio.mp3"}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}}
	parsed, err := plugin.Engine.Call(context.Background(), "parseSubmitResponse", ctx, map[string]any{"body": body})
	require.NoError(t, err)
	assert.NotContains(t, parsed.(map[string]any), "immediate", "audio must pass the completion usage hook even when invoke returns a ready asset")
	ctx["state"] = parsed.(map[string]any)["state"]
	for _, scenario := range []string{"missing", "invalid"} {
		unknown := maps.Clone(body)
		delete(unknown, "usage")
		if scenario == "invalid" {
			unknown["usage"] = map[string]int64{"input_tokens": 5, "output_tokens": 2147483648}
		}
		result, err := plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, unknown)
		require.NoError(t, err)
		assert.Equal(t, "UNKNOWN", result.(map[string]any)["status"], "an estimated output budget is not an actual usage statement")
	}
	result, err := plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, body)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", result.(map[string]any)["status"])
}
