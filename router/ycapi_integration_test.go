package router

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
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
	body := map[string]any{"task_id": "provider-original", "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/audio.wav"}}}
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
	assert.Equal(t, 61, count)
	assert.Equal(t, 149, priceRows)
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

func TestYcapiTextPinsLaneClampsBudgetAndRejectsToolsBeforeCharging(t *testing.T) {
	f := newYcapiRouterFixture(t)
	var wire map[string]any
	posts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
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
	assert.EqualValues(t, 8192, wire["max_tokens"])
	assert.Equal(t, "gpt-6.1-sol", wire["model"])
	assert.Equal(t, "gpt-6.1-sol@952:codex", wire["lane_id"])
	assert.Contains(t, response.Body.String(), "[DONE]")
	rejected := f.request(t, http.MethodPost, "/v1/chat/completions", `{"model":"ycapi-gpt-6.1-sol","messages":[{"role":"user","content":"Hello"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}],"stream":true}`, "h3routermember")
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
		for _, scenario := range []string{"success", "lost-create-response"} {
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
						encoded, _ := common.Marshal(map[string]any{"task_id": providerID, "status": "queued", "is_final": false})
						_, _ = w.Write(encoded)
						return
					}
					require.True(t, strings.HasSuffix(r.URL.Path, "/"+providerID))
					encoded, _ := common.Marshal(map[string]any{"task_id": providerID, "model": upstreamModel, "status": "completed", "is_final": true, "outputs": []any{map[string]any{"url": "https://cdn.example/synthetic-result"}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 10}})
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
				for range 2 {
					service.RunTaskPollingOnce(context.Background(), nil)
				}
				task = *f.task(t, task.TaskID)
				var admission model.MediaAdmission
				require.NoError(t, f.db.First(&admission, "id = ?", task.PrivateData.MediaAdmissionID).Error)
				foreign := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routeradmin")
				assert.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
				assert.NotContains(t, foreign.Body.String(), "cdn.example")
				if scenario == "success" {
					assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
					assert.EqualValues(t, model.TaskStatusSuccess, task.Status)
					assert.Equal(t, model.MediaReleased, admission.State)
					assert.True(t, admission.ProviderTerminal)
					artifacts := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", "", "h3routermember")
					assert.Equal(t, http.StatusOK, artifacts.Code, artifacts.Body.String())
					assert.Contains(t, artifacts.Body.String(), `"key":"`+kind+`"`)
					assert.NotContains(t, artifacts.Body.String(), "cdn.example")
					if kind == "image" {
						owner := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routermember")
						assert.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
						assert.Contains(t, owner.Body.String(), `"result":{"data":[{"url":`)
						assert.Contains(t, owner.Body.String(), "/artifacts/image/content")
						assert.NotContains(t, owner.Body.String(), "cdn.example")
					}
				} else {
					assert.EqualValues(t, model.TaskStatusUnknown, task.Status)
					assert.Equal(t, model.MediaUnknown, admission.State)
					assert.False(t, admission.ProviderTerminal)
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

func TestYcapiAll40TextRoutesKeepApprovedBudgetAndLane(t *testing.T) {
	f := newYcapiRouterFixture(t)
	var wire map[string]any
	posts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		wire = nil
		require.NoError(t, common.Unmarshal(data, &wire))
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":30,\"total_tokens\":80}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	base, err := url.Parse(provider.URL)
	require.NoError(t, err)
	service.GetHttpClient().Transport = ycapiFixtureTransport{base: base}
	models, err := ycapi.Models()
	require.NoError(t, err)
	for _, m := range models {
		if m.Kind != "text" {
			continue
		}
		t.Run(m.ID, func(t *testing.T) {
			encoded, err := common.Marshal(map[string]any{"model": m.ID, "messages": []map[string]string{{"role": "user", "content": "Synthetic plain text fixture"}}, "max_tokens": 16384, "stream": true, "stream_options": map[string]bool{"include_usage": true}})
			require.NoError(t, err)
			response := f.request(t, http.MethodPost, "/v1/chat/completions", string(encoded), "h3routermember")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "[DONE]")
			assert.Equal(t, m.UpstreamID, wire["model"])
			assert.Equal(t, m.Prices[0].LaneID, wire["lane_id"])
			budget := wire["max_tokens"]
			if budget == nil {
				budget = wire["max_completion_tokens"]
			}
			assert.EqualValues(t, 8192, budget)
			assert.Nil(t, wire["tools"])
			assert.Nil(t, wire["reasoning"])
			assert.True(t, wire["reasoning_effort"] == nil || wire["reasoning_effort"] == "none")
		})
	}
	assert.Equal(t, 40, posts)
}
