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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/plugins"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/tokenkit"
	apitypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
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
				ctx := map[string]any{"model": m.ID, "upstreamModel": m.ID, "baseUrl": "https://ycapi.fixture", "apiKey": "synthetic-fixture", "authHeader": "synthetic-fixture", "publicTaskId": "task_fixed_local", "requestBody": request}
				value, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
				require.NoError(t, err)
				assert.Equal(t, "Bearer synthetic-fixture", value.(map[string]any)["headers"].(map[string]any)["Authorization"])
				queryCtx := maps.Clone(ctx)
				queryCtx["taskId"] = "provider-original"
				query, err := plugin.Engine.Call(context.Background(), "buildQueryRequest", queryCtx)
				require.NoError(t, err)
				assert.Equal(t, "Bearer synthetic-fixture", query.(map[string]any)["headers"].(map[string]any)["Authorization"])
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
				if m.Kind == "image" {
					capability, ok := ycapi.ImageRatios(m.ID)
					require.True(t, ok)
					offerRatios := ycapi.ImageOfferRatios(m.ID, offer.ID)
					require.NotEmpty(t, offerRatios)
					for _, ratio := range offerRatios {
						t.Run(ratio, func(t *testing.T) {
							selected := maps.Clone(request)
							selected["size"] = ratio
							selectedCtx := maps.Clone(ctx)
							selectedCtx["requestBody"] = selected
							value, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", selectedCtx)
							require.NoError(t, err)
							wire := value.(map[string]any)["body"].(map[string]any)
							assert.Equal(t, m.Prices[index].LaneID, wire["lane_id"], "shape cannot select another procurement route")
							params := wire["params"].(map[string]any)
							expected := maps.Clone(offer.Params)
							expected[capability.RatioField], expected["prompt"] = ratio, request["prompt"]
							expectedJSON, err := common.Marshal(expected)
							require.NoError(t, err)
							paramsJSON, err := common.Marshal(params)
							require.NoError(t, err)
							assert.JSONEq(t, string(expectedJSON), string(paramsJSON), "only the supplier's actual shape field may change")
							usage, err := plugin.Engine.Call(context.Background(), "extractUsage", selectedCtx)
							require.NoError(t, err)
							assert.Equal(t, offer.ID, usage.(map[string]any)["spec"], "all shapes retain the original priced specification")
							assert.EqualValues(t, 1, usage.(map[string]any)["image_count"])
						})
					}
					aliasRatio := offerRatios[len(offerRatios)-1]
					for _, name := range []string{"ratio", "aspect_ratio"} {
						selected := maps.Clone(request)
						delete(selected, "size")
						selected[name] = aliasRatio
						ctx["requestBody"] = selected
						value, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
						require.NoError(t, err)
						wire := value.(map[string]any)["body"].(map[string]any)
						assert.Equal(t, aliasRatio, wire["params"].(map[string]any)[capability.RatioField])
					}
					for _, ratio := range capability.Ratios {
						if slices.Contains(offerRatios, ratio) {
							continue
						}
						selected := maps.Clone(request)
						selected["size"] = ratio
						ctx["requestBody"] = selected
						_, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
						assert.Error(t, err, "a model-wide shape cannot bypass its selected procurement tier's verified controls")
					}
					for _, patch := range []map[string]any{
						{"size": "99:1"}, {"size": "16:9", "ratio": "9:16"},
						{"size": "16:9", "aspect_ratio": "9:16"}, {"size": "16:9", "metadata": map[string]any{"size": "9:16"}},
						{"size": "16:9", "resolution": "999K"}, {"size": "16:9", "n": 2}, {"size": "16:9", "count": 2},
					} {
						selected := maps.Clone(request)
						maps.Copy(selected, patch)
						ctx["requestBody"] = selected
						_, err := plugin.Engine.Call(context.Background(), "buildSubmitRequest", ctx)
						assert.Error(t, err, "invalid shape aliases, tiers and counts must fail before any submission")
					}
					ctx["requestBody"] = request
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
	count, originalPriceRows := 0, 0
	for _, entry := range catalog.Data {
		id, _ := entry["id"].(string)
		if !strings.HasPrefix(id, "ycapi-") {
			continue
		}
		count++
		approved, ok := ycapi.Lookup(id)
		require.True(t, ok)
		assert.Equal(t, approved.Kind, entry["kind"])
		if approved.Kind == "image" {
			parameters := entry["parameters"].(map[string]any)
			assert.Equal(t, map[string]any{"schema_version": float64(1), "submit_model_metadata": true, "original_task_artifacts": true}, parameters["x-image-receipt-recovery"])
		}
		prices, ok := entry["retail_price_rows"].([]any)
		require.True(t, ok)
		originalPriceRows += len(approved.Prices)
		expectedRows := len(approved.Prices)
		if approved.Kind == "image" {
			expectedRows = 0
			for _, offer := range approved.Offers {
				expectedRows += len(ycapi.ImageOfferRatios(id, offer.ID))
			}
		}
		assert.Len(t, prices, expectedRows)
		for i, value := range prices {
			price, ok := value.(map[string]any)
			require.True(t, ok)
			if i >= len(approved.Prices) {
				assert.NotContains(t, price, "official_reference", "non-square retail tiers do not imply a verified native comparison")
				capability, ok := ycapi.ImageRatios(id)
				require.True(t, ok)
				params := maps.Clone(price["params"].(map[string]any))
				delete(params, capability.RatioField)
				var matches []int
				for index, candidate := range approved.Prices {
					originalParams := maps.Clone(candidate.Params)
					delete(originalParams, capability.RatioField)
					if maps.EqualFunc(originalParams, params, func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
						matches = append(matches, index)
					}
				}
				require.Len(t, matches, 1, "a non-square price must identify exactly one original resolution/quality tier")
				original := prices[matches[0]].(map[string]any)
				assert.Equal(t, original["retail_cny"], price["retail_cny"])
				assert.Equal(t, original["retail_total_cny"], price["retail_total_cny"])
				continue
			}
			reference, ok := price["official_reference"].(map[string]any)
			require.True(t, ok, "every approved row has a same-unit official reference")
			assert.EqualValues(t, 1, reference["schema_version"])
			assert.Equal(t, "CNY", reference["currency"])
			assert.Equal(t, price["unit"], reference["unit"])
			assert.Equal(t, approved.Prices[i].Official.String(), reference["official_cny"])
			assert.Equal(t, approved.Prices[i].OfficialModel, reference["model"])
			assert.Equal(t, approved.Prices[i].OfficialSource, reference["source_url"])
			assert.Equal(t, "2026-10-31", reference["checked_at"])
			assert.Equal(t, approved.Prices[i].Retail.String(), price["retail_cny"])
			if approved.Kind == "video" || approved.Kind == "image" {
				assert.Equal(t, approved.Prices[i].OfficialTotal.String(), reference["official_total_cny"])
			}
		}
		if approved.Kind == "text" {
			assert.EqualValues(t, approved.ContextWindow, entry["context_window"])
			assert.EqualValues(t, 8192, entry["max_output_tokens"])
		}
	}
	assert.Equal(t, 43, count)
	assert.Equal(t, 113, originalPriceRows)
	for _, key := range []string{"procurement", "lane_id", "channel_id", "synthetic-ycapi-only", "ycapi-flux-2\"", "ycapi-seedance-2-5-special\""} {
		assert.NotContains(t, models.Body.String(), key)
	}
	pricing := f.request(t, http.MethodGet, "/api/pricing", "", "h3routermember")
	require.Equal(t, http.StatusOK, pricing.Code, pricing.Body.String())
	assert.Contains(t, pricing.Body.String(), `"retail_price_rows"`)
	assert.NotContains(t, pricing.Body.String(), `"lane_id"`)
	assert.NotContains(t, pricing.Body.String(), `"procurementCny"`)
	assert.Contains(t, pricing.Body.String(), `"official_reference"`)
	var priced struct {
		Data []struct {
			Model string           `json:"model_name"`
			Rows  []map[string]any `json:"retail_price_rows"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(pricing.Body.Bytes(), &priced))
	for _, item := range priced.Data {
		if !strings.HasPrefix(item.Model, "ycapi-") {
			continue
		}
		for _, entry := range catalog.Data {
			if entry["id"] == item.Model {
				data, err := common.Marshal(entry["retail_price_rows"])
				require.NoError(t, err)
				var expected []map[string]any
				require.NoError(t, common.Unmarshal(data, &expected))
				assert.Equal(t, expected, item.Rows, "both public endpoints must expose the same reference")
			}
		}
	}
	if directory := os.Getenv("TEST_YCAPI_PUBLIC_COMPARE_OUTPUT"); directory != "" {
		for name, response := range map[string]*httptest.ResponseRecorder{"models": models, "pricing": pricing} {
			file, err := os.OpenFile(filepath.Join(directory, name+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			require.NoError(t, err)
			_, err = file.Write(response.Body.Bytes())
			require.NoError(t, err)
			require.NoError(t, file.Close())
		}
	}
	protected := f.request(t, http.MethodGet, "/api/ycapi/catalog", "", "h3routermember")
	assert.NotContains(t, protected.Body.String(), `"procurementCny"`)
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	for _, version := range []string{"1.0.0", "1.0.1", "1.0.2", "1.0.3"} {
		t.Run("executing-"+version, func(t *testing.T) {
			versionSource := strings.Replace(source, `version: "1.0.3"`, `version: "`+version+`"`, 1)
			_, err := jsplugin.DefaultRegistry.Register(versionSource, jsplugin.Options{})
			require.NoError(t, err)
			model.InvalidatePricingCache()
			models := f.request(t, http.MethodGet, "/v1/models", "", "h3routermember")
			pricing := f.request(t, http.MethodGet, "/api/pricing", "", "h3routermember")
			require.Equal(t, http.StatusOK, models.Code, models.Body.String())
			require.Equal(t, http.StatusOK, pricing.Code, pricing.Body.String())
			var catalog struct {
				Data []struct {
					ID         string           `json:"id"`
					Kind       string           `json:"kind"`
					Parameters map[string]any   `json:"parameters"`
					Rows       []map[string]any `json:"retail_price_rows"`
				} `json:"data"`
			}
			var priced struct {
				Data []struct {
					ID         string           `json:"model_name"`
					Parameters map[string]any   `json:"parameters"`
					Rows       []map[string]any `json:"retail_price_rows"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(models.Body.Bytes(), &catalog))
			require.NoError(t, common.Unmarshal(pricing.Body.Bytes(), &priced))
			matched := 0
			for _, item := range catalog.Data {
				if !strings.HasPrefix(item.ID, "ycapi-") || item.Kind != "image" {
					continue
				}
				for _, price := range priced.Data {
					if price.ID != item.ID {
						continue
					}
					matched++
					assert.Equal(t, price.Parameters, item.Parameters, "both endpoints must declare the executing plugin's identical controls")
					assert.Equal(t, price.Rows, item.Rows, "both endpoints must publish prices for exactly those controls")
				}
				assert.Equal(t, version, item.Parameters["x-task-plugin-runtime"].(map[string]any)["version"])
				_, recovery := item.Parameters["x-image-receipt-recovery"]
				assert.Equal(t, version != "1.0.0", recovery)
				if version != "1.0.2" && version != "1.0.3" {
					for _, branch := range item.Parameters["oneOf"].([]any) {
						properties := branch.(map[string]any)["properties"].(map[string]any)
						assert.Equal(t, []any{"1:1"}, properties["size"].(map[string]any)["enum"])
					}
				}
			}
			assert.Equal(t, 8, matched)
		})
	}
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
						encoded, _ := common.Marshal(map[string]any{"task_id": providerID, "status": "queued", "is_final": false,
							"model": map[string]any{"public_model": upstreamModel, "upstream_model": "supplier-private-alias", "category": kind}})
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

func TestYcapiRuntimeModelMetadataKeepsOriginalImageReceiptAndArtifacts(t *testing.T) {
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{})
	require.NoError(t, err)
	ctx := map[string]any{"model": "ycapi-flux-kontext", "upstreamModel": "ycapi-flux-kontext", "taskId": "provider-original",
		"requestBody": map[string]any{"model": "ycapi-flux-kontext", "prompt": "Synthetic image", "quality": "pro", "size": "1:1", "n": 1}}
	accepted := map[string]any{"task_id": "provider-original", "status": "pending", "model": map[string]any{
		"public_model": "flux-kontext", "upstream_model": "", "provider": "", "category": "image"}}
	value, err := plugin.Engine.Call(context.Background(), "parseSubmitResponse", ctx, map[string]any{"body": accepted})
	require.NoError(t, err, "the runtime invoke endpoint returns model metadata rather than a model string")
	parsed := value.(map[string]any)
	assert.Equal(t, "provider-original", parsed["taskId"])
	ctx["state"] = parsed["state"]
	legacyFacts := parsed["state"].(map[string]any)["facts"]
	completed := map[string]any{"id": "provider-original", "model": "flux-kontext", "status": "completed", "is_final": true,
		"result": map[string]any{"images": []any{map[string]any{"url": "https://cdn.example/original.png"}}}}
	value, err = plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, completed)
	require.NoError(t, err)
	result := value.(map[string]any)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, legacyFacts, result["state"].(map[string]any)["facts"], "upgrading shape controls cannot replace an existing task's priced specification")
	usage, err := plugin.Engine.Call(context.Background(), "extractUsageOnComplete", ctx, result, completed)
	require.NoError(t, err)
	assert.Empty(t, usage, "image completion keeps the submission's frozen count and specification")
	artifacts, err := plugin.Engine.Call(context.Background(), "listArtifacts", map[string]any{"status": "SUCCESS", "state": result["state"]})
	require.NoError(t, err)
	assert.Equal(t, "image", artifacts.([]any)[0].(map[string]any)["key"])
	wrongModel := maps.Clone(accepted)
	wrongModel["model"] = map[string]any{"public_model": "another-model"}
	_, err = plugin.Engine.Call(context.Background(), "parseSubmitResponse", ctx, map[string]any{"body": wrongModel})
	assert.Error(t, err, "the provider's public model still has to match the submitted model")
	wrongTask := maps.Clone(completed)
	wrongTask["id"] = "another-task"
	_, err = plugin.Engine.Call(context.Background(), "parseTaskResult", ctx, wrongTask)
	assert.Error(t, err, "polling still binds to the exact original provider task")
}

// TEST_H3_SQL_DSN and TEST_H3_LOG_SQL_DSN select the existing disposable
// SQLite/MySQL/PostgreSQL fixture. The provider transport stays in memory.
func TestYcapiEffectiveCapacityDirectoryAndWireDatabase(t *testing.T) {
	f := newYcapiRouterFixture(t)
	const modelID = "ycapi-gpt-6.1-sol"
	approvedModel, found := ycapi.Lookup(modelID)
	require.True(t, found)
	previousGroups := ratio_setting.GroupRatio2JSONString()
	previousUsable := setting.UserUsableGroups2JSONString()
	previousSelfUse := operation_setting.SelfUseModeEnabled
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
		operation_setting.SelfUseModeEnabled = previousSelfUse
	})
	// These extra tables are outside the base H3 fixture's reset list. Reset
	// them explicitly so a repeated external-database run cannot reuse PATs.
	require.NoError(t, f.db.Migrator().DropTable(&model.UserAccessToken{}))
	require.NoError(t, f.db.AutoMigrate(&model.UserAccessToken{}))
	require.NoError(t, f.logDB.Migrator().DropTable(&model.AuditLog{}))
	require.NoError(t, f.logDB.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, f.db.Create(&model.User{Id: 793, Username: "capacity-vip", AffCode: "cap-vip", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100000000, Group: "vip", AuthVersion: 1}).Error)
	require.NoError(t, f.db.Create(&model.Token{Id: 793, UserId: 793, Key: "capacityviprelay", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100000000}).Error)
	credentials := map[string]string{"default": model.AccessTokenPrefix + strings.Repeat("d", 43), "vip": model.AccessTokenPrefix + strings.Repeat("v", 43)}
	for group, id := range map[string]int{"default": 792, "vip": 793} {
		pat := model.UserAccessToken{UserId: id, Name: "capacity directory fixture", TokenHash: model.AccessTokenFingerprint(credentials[group]), TokenHint: model.AccessTokenHint(credentials[group])}
		require.NoError(t, pat.SetScopes([]string{}))
		require.NoError(t, f.db.Create(&pat).Error)
	}
	var channel model.Channel
	require.NoError(t, f.db.Where("models = ?", modelID).First(&channel).Error)
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("group", "default,vip").Error)
	require.NoError(t, f.db.Create(&model.Ability{Group: "vip", Model: modelID, ChannelId: channel.Id, Enabled: true}).Error)
	legacyBase := "https://legacy.fixture"
	require.NoError(t, (&model.Channel{Id: 8890, Type: constant.ChannelTypeOpenAI, Key: "synthetic-legacy", Name: "legacy without capacity", Models: "capacity-legacy,ys-1.0", Group: "default,vip", Status: common.ChannelStatusEnabled, BaseURL: &legacyBase}).Insert())
	// Legacy capacity remains optional, while selectable billing is explicit.
	previousPrices := ratio_setting.ModelPrice2JSONString()
	legacyPrices := ratio_setting.GetModelPriceCopy()
	legacyPrices["capacity-legacy"], legacyPrices["ys-1.0"] = 0.01, 0.01
	encodedPrices, err := common.Marshal(legacyPrices)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(encodedPrices)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	candidates, err := codebloom.Models()
	require.NoError(t, err)
	var candidateIDs []string
	for _, candidate := range candidates {
		if !candidate.ActivationReady {
			candidateIDs = append(candidateIDs, candidate.ID)
		}
	}
	require.NoError(t, (&model.Channel{Id: 8891, Type: constant.ChannelTypeOpenAI, Key: "synthetic-inactive-candidates", Name: "stale CodeBloom activation", Models: strings.Join(candidateIDs, ","), Group: "default", Status: common.ChannelStatusEnabled}).Insert())
	policy, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	for i := range policy.Routes {
		if policy.Routes[i].Model == modelID {
			policy.Routes[i].MaxOutputTokens = common.GetPointer(16384)
		}
	}
	policy.Routes = append(policy.Routes, model.CapacityRoute{Model: modelID, Group: "vip", RouteModel: modelID, ChannelID: channel.Id, ContextWindow: common.GetPointer(65536), MaxOutputTokens: common.GetPointer(4096), BudgetField: "max_tokens", Source: "synthetic group capacity contract", VerifiedAt: "2026-11-02"})
	require.NoError(t, model.SaveModelCapacityConfig(policy))
	model.InitChannelCache()
	model.RefreshPricing()
	var cachedBefore []byte
	for _, price := range model.GetPricing() {
		if price.ModelName == modelID {
			cachedBefore, err = common.Marshal(price)
			require.NoError(t, err)
		}
	}
	require.NotEmpty(t, cachedBefore)
	posts := 0
	var wire map[string]any
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "ycapi.fixture" || r.URL.Path != "/api/v1/model-runtime/invoke" {
			return nil, fmt.Errorf("capacity fixture blocks external requests")
		}
		posts++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err := common.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		body := "data: {\"id\":\"capacity-local\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":30,\"total_tokens\":80}}\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Run("effective group directories and immutable pricing cache", func(t *testing.T) {
		for _, group := range []string{"default", "vip", "default"} {
			wantWindow, wantOutput, relayKey := approvedModel.ContextWindow, 16384, "h3routermember"
			if group == "vip" {
				wantWindow, wantOutput, relayKey = 65536, 4096, "capacityviprelay"
			}
			for _, path := range []string{"/v1/models", "/api/pricing"} {
				credential := "sk-" + relayKey
				if path == "/api/pricing" {
					credential = credentials[group]
				}
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Authorization", "Bearer "+credential)
				response := httptest.NewRecorder()
				f.engine.ServeHTTP(response, request)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var result struct {
					Data []map[string]any `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				found, foundLegacyYs := false, false
				for _, entry := range result.Data {
					id, _ := entry["id"].(string)
					if path == "/api/pricing" {
						id, _ = entry["model_name"].(string)
					}
					_, candidate := codebloom.Lookup(id)
					assert.False(t, candidate, "unavailable candidates must not leak into either public directory")
					if id == "ys-1.0" {
						foundLegacyYs = true
					}
					if id == "capacity-legacy" {
						assert.NotContains(t, entry, "context_window")
						assert.NotContains(t, entry, "max_output_tokens")
						if parameters, ok := entry["parameters"].(map[string]any); ok {
							assert.NotContains(t, parameters, "x-client-concurrency")
						}
					}
					if id != modelID {
						continue
					}
					found = true
					assert.EqualValues(t, wantWindow, entry["context_window"], path+" "+group)
					assert.EqualValues(t, wantOutput, entry["max_output_tokens"], path+" "+group)
					params, ok := entry["parameters"].(map[string]any)
					require.True(t, ok)
					text, ok := params["x-text-capabilities"].(map[string]any)
					require.True(t, ok)
					assert.EqualValues(t, wantWindow, text["context_window"])
					assert.EqualValues(t, wantOutput, text["max_output_tokens"])
					assert.EqualValues(t, wantOutput, text["default_output_tokens"])
					assert.Equal(t, "gateway_policy", text["default_output_tokens_source"])
					assert.EqualValues(t, 8192, text["fallback_output_tokens"])
					assert.Equal(t, "user_approved_product_budget", text["fallback_output_tokens_source"])
					assert.Equal(t, "gateway_policy", text["output_limit_source"])
					assert.Contains(t, text, "native_max_output_tokens")
					assert.Nil(t, text["native_max_output_tokens"])
					assert.EqualValues(t, wantWindow, text["max_input_tokens"])
					assert.Equal(t, "context_window_bound", text["input_limit_source"])
					assert.Contains(t, text, "native_max_input_tokens")
					assert.Nil(t, text["native_max_input_tokens"])
				}
				assert.True(t, found, path+" "+group)
				assert.True(t, foundLegacyYs, "the existing ys-1.0 model must remain discoverable: "+path+" "+group)
			}
		}
		for _, price := range model.GetPricing() {
			if price.ModelName == modelID {
				after, err := common.Marshal(price)
				require.NoError(t, err)
				assert.Equal(t, string(cachedBefore), string(after))
			}
		}
		assert.Zero(t, posts, "discovery must not call a supplier")
	})
	t.Run("large legitimate input and explicit budgets reach final wire", func(t *testing.T) {
		prompt := strings.Repeat("word ", 33000)
		assert.Greater(t, tokenkit.Count("gpt-6.1-sol", prompt), 32000)
		for _, budget := range []int{16384, 1024} {
			body, err := common.Marshal(map[string]any{"model": modelID, "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_completion_tokens": budget, "stream": true})
			require.NoError(t, err)
			response := f.request(t, http.MethodPost, "/v1/chat/completions", string(body), "h3routermember")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.EqualValues(t, budget, wire["params"].(map[string]any)["max_tokens"])
		}
		assert.Equal(t, 2, posts)
	})
	t.Run("small context remains enforced", func(t *testing.T) {
		for i := range policy.Routes {
			if policy.Routes[i].Model == modelID && policy.Routes[i].Group == "default" {
				policy.Routes[i].ContextWindow = common.GetPointer(256)
				policy.Routes[i].MaxOutputTokens = common.GetPointer(128)
			}
		}
		require.NoError(t, model.SaveModelCapacityConfig(policy))
		body, err := common.Marshal(map[string]any{"model": modelID, "messages": []map[string]string{{"role": "user", "content": strings.Repeat("word ", 400)}}, "stream": true})
		require.NoError(t, err)
		response := f.request(t, http.MethodPost, "/v1/chat/completions", string(body), "h3routermember")
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "context_length_exceeded")
		assert.Equal(t, 2, posts)
	})
	t.Run("unverified candidate models stay fenced at final outbound boundary", func(t *testing.T) {
		for _, candidate := range candidates {
			if candidate.ActivationReady {
				continue
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			c.Request = request
			err := service.EnforceModelCapacityOutbound(c, request, &relaycommon.RelayInfo{OriginModelName: candidate.ID})
			var apiErr *kittypes.NewAPIError
			require.ErrorAs(t, err, &apiErr, candidate.ID)
			assert.Equal(t, "model_not_available", string(apiErr.GetErrorCode()))
			assert.True(t, kittypes.IsSkipRetryError(apiErr))
		}
		assert.Equal(t, 2, posts)
	})
	t.Run("missing effective schema2 capacity fails closed", func(t *testing.T) {
		require.NoError(t, f.db.Model(&model.Option{}).Where(&model.Option{Key: model.ModelCapacityOptionKey}).Update("value", `{"schema":1,"routes":[]}`).Error)
		for _, path := range []string{"/v1/models", "/api/pricing"} {
			response := f.request(t, http.MethodGet, path, "", "h3routermember")
			assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "model_capacity_unavailable")
		}
		assert.Equal(t, 2, posts)
	})
}

func TestYcapiMediaConcurrencyDirectoryDatabase(t *testing.T) {
	f := newYcapiRouterFixture(t)
	var before int64
	require.NoError(t, f.db.Model(&model.MediaPool{}).Count(&before).Error)
	read := func() {
		for _, path := range []string{"/v1/models", "/api/pricing"} {
			response := f.request(t, http.MethodGet, path, "", "h3routermember")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var result struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			kinds := map[string]bool{}
			for _, entry := range result.Data {
				id, _ := entry["id"].(string)
				if path == "/api/pricing" {
					id, _ = entry["model_name"].(string)
				}
				if id == "MiniMax-H3" {
					if parameters, ok := entry["parameters"].(map[string]any); ok {
						if hint, ok := parameters["x-client-concurrency"].(map[string]any); ok {
							assert.NotEqualValues(t, 20, hint["max_parallelism"], "the H3 account declaration must not inherit YC defaults")
						}
					}
				}
				approved, ok := ycapi.Lookup(id)
				if !ok || approved.Kind == "text" {
					continue
				}
				params, ok := entry["parameters"].(map[string]any)
				require.True(t, ok)
				if before == 0 {
					assert.NotContains(t, params, "x-client-concurrency")
					continue
				}
				kinds[approved.Kind] = true
				hint, ok := params["x-client-concurrency"].(map[string]any)
				require.True(t, ok, path+" "+id)
				assert.EqualValues(t, 20, hint["max_parallelism"])
				assert.Equal(t, "shared_account_pool", hint["scope"])
				assert.Equal(t, false, hint["reservation"])
			}
			if before > 0 {
				assert.Len(t, kinds, 3, path)
			}
		}
	}
	read()
	var after int64
	require.NoError(t, f.db.Model(&model.MediaPool{}).Count(&after).Error)
	assert.Equal(t, before, after, "directory GETs must never create admission pools")
	require.NoError(t, model.RefreshMediaConcurrencyPools(context.Background()))
	require.NoError(t, f.db.Model(&model.MediaPool{}).Count(&before).Error)
	require.Positive(t, before)
	read()
	require.NoError(t, f.db.Model(&model.MediaPool{}).Count(&after).Error)
	assert.Equal(t, before, after)
	var admissions int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Count(&admissions).Error)
	assert.Zero(t, admissions, "discovery never reserves work")
}
