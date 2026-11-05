package ycapi

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageRatiosKeepOriginalPriceTiersAndExactLaneCapabilities(t *testing.T) {
	require.NoError(t, imageCapabilityError)
	before, err := Snapshot()
	require.NoError(t, err)
	var exported []map[string]any
	combinations := 0
	for _, m := range approved.Models {
		if m.Kind != "image" {
			continue
		}
		t.Run(m.ID, func(t *testing.T) {
			capability, ok := ImageRatios(m.ID)
			require.True(t, ok)
			legacy := LegacyClientPrices(m.ID)
			prices := ClientPrices(m.ID)
			require.GreaterOrEqual(t, len(prices), len(legacy))
			assert.Equal(t, legacy, prices[:len(legacy)], "original square references must stay byte-equivalent")
			parameters := ClientParameters(m.ID)
			branches := parameters["oneOf"].([]any)
			require.Len(t, branches, len(m.Offers))
			legacyBranches := LegacyClientParameters(m.ID)["oneOf"].([]any)
			expectedRows := 0
			for i, offer := range m.Offers {
				ratios := ImageOfferRatios(m.ID, offer.ID)
				require.NotEmpty(t, ratios)
				fields := branches[i].(map[string]any)["properties"].(map[string]any)
				assert.Equal(t, ratios, fields["size"].(map[string]any)["enum"])
				assert.Equal(t, []string{"1:1"}, legacyBranches[i].(map[string]any)["properties"].(map[string]any)["size"].(map[string]any)["enum"])
				expectedRows += len(ratios)
				for _, ratio := range ratios {
					matched := 0
					for _, row := range prices {
						params := row["params"].(map[string]any)
						if params[capability.RatioField] != ratio {
							continue
						}
						sameTier := true
						for key, value := range offer.Params {
							if key != capability.RatioField && params[key] != value {
								sameTier = false
							}
						}
						if !sameTier {
							continue
						}
						matched++
						assert.Equal(t, legacy[i]["retail_cny"], row["retail_cny"])
						if ratio != "1:1" {
							assert.NotContains(t, row, "official_reference", "a square native reference is not a wide-image quote")
						}
						assert.Equal(t, "1:1", offer.Params[capability.RatioField])
					}
					assert.Equal(t, 1, matched, "one exact price per qualified ratio/tier")
				}
			}
			assert.Len(t, prices, expectedRows)
			combinations += expectedRows
			exported = append(exported, map[string]any{"model_name": m.ID, "display_name": m.Name, "kind": "image", "parameters": parameters, "retail_price_rows": prices})
		})
	}
	assert.Equal(t, 151, combinations)
	after, err := Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	if output := os.Getenv("TEST_YCAPI_IMAGE_PARAMETERS_OUTPUT"); output != "" {
		body, err := common.Marshal(map[string]any{"data": exported})
		require.NoError(t, err)
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, err = file.Write(body)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}
}

func TestImageRatioCapabilitiesDoNotBroadenOtherTiersOrSharedState(t *testing.T) {
	const model = "ycapi-gemini-3-pro-image"
	capability, ok := ImageRatios(model)
	require.True(t, ok)
	assert.Contains(t, capability.Ratios, "21:9")
	assert.Contains(t, ImageOfferRatios(model, "ed67fe2877e5345e"), "21:9")
	assert.NotContains(t, ImageOfferRatios(model, "7cffccab262c7e46"), "21:9", "the fixed 2K lane supports fewer ratios")
	assert.Equal(t, []string{"1:1"}, ImageOfferRatios("ycapi-gemini-3.1-flash-lite-image", "ed67fe2877e5345e"))
	assert.NotContains(t, ImageOfferRatios("ycapi-doubao-seedream-5-0-pro", "54646ea3fa521240"), "auto")
	capability.Ratios[0] = "changed"
	capability.OfferRatios["ed67fe2877e5345e"][0] = "changed"
	assert.Equal(t, "1:1", ImageOfferRatios(model, "ed67fe2877e5345e")[0])
	assert.Empty(t, ImageOfferRatios(model, "not-a-price-tier"))
	_, ok = ImageRatios("ycapi-grok-imagine-video")
	assert.False(t, ok)
}

func TestApprovedSaleRowsUseTheExistingQuotaConversion(t *testing.T) {
	rate := decimal.RequireFromString("7.3")
	const quotaPerUnit = 500000
	rows := 0
	for _, m := range approved.Models {
		expression, err := BillingExpression(m.ID, rate)
		require.NoError(t, err)
		for index, price := range m.Prices {
			t.Run(fmt.Sprintf("%s/%d", m.ID, index), func(t *testing.T) {
				snapshot := &billingexpr.BillingSnapshot{ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), QuotaPerUnit: quotaPerUnit, GroupRatio: 1, TaskUsageBilling: m.Kind != "text"}
				params := billingexpr.TokenParams{}
				usage := map[string]any{}
				expectedCNY := price.RetailTotal
				switch m.Kind {
				case "text":
					if price.Direction == "input" {
						params.P = 1000000
					} else {
						params.C = 1000000
					}
					expectedCNY = price.Retail
				case "audio":
					usage["input_tokens"], usage["output_tokens"] = 0, 0
					usage[price.Direction+"_tokens"] = 1000000
					expectedCNY = price.Retail
				case "image":
					usage["image_count"], usage["spec"] = 1, m.Offers[index].ID
				case "video":
					seconds := decimal.RequireFromString(fmt.Sprint(m.Offers[index].Params["duration"]))
					usage["seconds"], usage["spec"] = seconds.InexactFloat64(), m.Offers[index].ID
					assert.True(t, price.Retail.Mul(seconds).Equal(expectedCNY), "per-second sale must reproduce this exact clip total")
				}
				result, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, params, billingexpr.RequestInput{Usage: usage})
				require.NoError(t, err)
				assert.Nil(t, result.Clamp)
				expectedQuota, clamp := common.QuotaRoundChecked(expectedCNY.Div(rate).Mul(decimal.NewFromInt(quotaPerUnit)).InexactFloat64())
				require.Nil(t, clamp)
				assert.Equal(t, expectedQuota, result.ActualQuotaAfterGroup)
			})
			rows++
		}
	}
	assert.Equal(t, 113, rows)
}

func TestApprovedCatalogPreviews(t *testing.T) {
	require.NoError(t, catalogError)
	counts := map[string]int{}
	specs := 0
	for _, m := range approved.Models {
		counts[m.Kind]++
		for _, o := range m.Offers {
			t.Run(m.ID+"/"+o.ID, func(t *testing.T) {
				p, err := BuildPreview(PreviewRequest{ModelID: m.ID, SpecID: o.ID, Prompt: "A white ceramic cup"})
				require.NoError(t, err)
				assert.False(t, p.GenerationEnabled)
				assert.Len(t, p.Fingerprint, 64)
				assert.NotContains(t, p.Body, "api_key")
				assert.NotContains(t, p.Body, "retail_spec")
				assert.True(t, strings.HasPrefix(p.Endpoint, "/"))
				assert.NotEmpty(t, p.Retail)
			})
			specs++
		}
	}
	assert.Equal(t, map[string]int{"text": 22, "image": 8, "video": 10, "audio": 3}, counts)
	assert.Equal(t, 88, specs)
}
func TestPreviewUsesNativeClipTotalAndPinnedLane(t *testing.T) {
	p, err := BuildPreview(PreviewRequest{ModelID: "ycapi-grok-imagine-video", SpecID: "ca70bc25c56f6312", Prompt: "Cup"})
	require.NoError(t, err)
	assert.Equal(t, "0.7275", p.Retail)
	assert.Equal(t, "5", p.Body["duration"])
	assert.Equal(t, "grok-imagine-video@ROU:default", p.Body["lane_id"])
}
func TestPreviewRejectsUnlistedOrUnsafeInputs(t *testing.T) {
	neg, huge, zero, allowed := -1, common.MaxRelayOutputTokens+1, 0, 8192
	cases := []PreviewRequest{
		{ModelID: "ycapi-unknown", SpecID: "tokens", Prompt: "ok"},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: ""},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "ok", InputTokens: &neg},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "ok", InputTokens: &huge},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "ok", MaxOutputTokens: &zero},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: strings.Repeat("a", 32000)},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "ok", OutputTokens: &allowed, MaxOutputTokens: &zero},
		{ModelID: "ycapi-grok-imagine-video", SpecID: "new-resolution", Prompt: "ok"},
		{ModelID: "ycapi-gpt-4o-mini-tts", SpecID: "tokens", Prompt: strings.Repeat("字", 2001)},
		{ModelID: "ycapi-gpt-4o-mini-tts", SpecID: "tokens", Prompt: "ok", Voice: "not-a-voice"},
	}
	for _, c := range cases {
		_, err := BuildPreview(c)
		assert.Error(t, err)
	}
}
func TestPreviewFingerprintChangesAndCatalogRemainsImmutable(t *testing.T) {
	before, err := Snapshot()
	require.NoError(t, err)
	a, err := BuildPreview(PreviewRequest{ModelID: "ycapi-gpt-4o-mini-tts", SpecID: "tokens", Prompt: "Hello"})
	require.NoError(t, err)
	b, err := BuildPreview(PreviewRequest{ModelID: "ycapi-gpt-4o-mini-tts", SpecID: "tokens", Prompt: "Another"})
	require.NoError(t, err)
	assert.NotEqual(t, a.Fingerprint, b.Fingerprint)
	a.Body["params"].(map[string]any)["input"] = "mutated"
	after, err := Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	before[0] = 'x'
	after, err = Snapshot()
	require.NoError(t, err)
	var v any
	assert.NoError(t, common.Unmarshal(after, &v))
}

func TestChatEnvelopePreservesToolsImagesControlsAndSmallerBudgets(t *testing.T) {
	request := map[string]any{
		"model": "ycapi-gpt-6.1-sol", "max_completion_tokens": 2048, "stream": true,
		"messages":    []map[string]any{{"role": "user", "content": []map[string]any{{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64,c3ludGhldGlj"}}, {"type": "text", "text": "Use the echo tool"}}}},
		"tools":       []map[string]any{{"type": "function", "function": map[string]any{"name": "echo", "parameters": map[string]any{"type": "object"}}}},
		"tool_choice": "auto", "enable_thinking": false,
	}
	envelope, err := ChatEnvelope("ycapi-gpt-6.1-sol", request)
	require.NoError(t, err)
	assert.Equal(t, "gpt-6.1-sol", envelope["model_id"])
	assert.Equal(t, "gpt-6.1-sol@952:codex", envelope["lane_id"])
	assert.Equal(t, "chat", envelope["modality"])
	assert.Equal(t, true, envelope["stream"])
	params := envelope["params"].(map[string]any)
	assert.EqualValues(t, 2048, params["max_tokens"])
	assert.Equal(t, false, params["enable_thinking"], "explicit false must not disappear")
	assert.Equal(t, "auto", params["tool_choice"])
	assert.Len(t, params["tools"], 1)
	assert.Nil(t, params["messages"], "supplier only reads messages at envelope level")
	encoded, err := common.Marshal(envelope["messages"])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "data:image/png;base64,c3ludGhldGlj")
	assert.Contains(t, string(encoded), "Use the echo tool")
	assert.NotContains(t, envelope, "max_completion_tokens")
	for _, patch := range []map[string]any{
		{"model": "another-native-model"}, {"lane_id": "another-lane"},
		{"pinned_lane_id": "a-lane"}, {"params": map[string]any{"max_tokens": 1}},
		{"max_tokens": -1}, {"max_tokens": 0}, {"max_tokens": 1.5}, {"max_tokens": "2048"},
	} {
		copy := map[string]any{}
		for key, value := range request {
			copy[key] = value
		}
		for key, value := range patch {
			copy[key] = value
		}
		_, err := ChatEnvelope("ycapi-gpt-6.1-sol", copy)
		assert.Error(t, err)
	}
}

func TestChatEnvelopeUsesDefaultsOnlyWhenOutputBudgetsAreOmitted(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]any
		want   int
	}{
		{name: "omitted", want: 8192},
		{name: "null is omitted", fields: map[string]any{"max_tokens": nil}, want: 8192},
		{name: "max tokens", fields: map[string]any{"max_tokens": 16384}, want: 16384},
		{name: "completion alias", fields: map[string]any{"max_completion_tokens": 16384}, want: 16384},
		{name: "output alias", fields: map[string]any{"max_output_tokens": 16384}, want: 16384},
		{name: "conflicting aliases", fields: map[string]any{"max_tokens": 32768, "max_completion_tokens": 16384, "max_output_tokens": 24576}, want: 16384},
		{name: "smaller explicit budget", fields: map[string]any{"max_tokens": 512, "max_output_tokens": 16384}, want: 512},
		{name: "shared safety boundary", fields: map[string]any{"max_tokens": common.MaxRelayOutputTokens}, want: common.MaxRelayOutputTokens},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := map[string]any{"messages": []map[string]string{{"role": "user", "content": "hello"}}}
			for key, value := range test.fields {
				request[key] = value
			}
			envelope, err := ChatEnvelope("ycapi-gpt-6.1-sol", request)
			require.NoError(t, err)
			params := envelope["params"].(map[string]any)
			assert.EqualValues(t, test.want, params["max_tokens"])
			assert.NotContains(t, params, "max_completion_tokens")
			assert.NotContains(t, params, "max_output_tokens")
		})
	}
	for _, invalid := range []any{0, -1, 1.5, "16384", true, math.NaN(), math.Inf(1), common.MaxRelayOutputTokens + 1, uint64(math.MaxInt32) + 1} {
		_, err := ChatEnvelope("ycapi-gpt-6.1-sol", map[string]any{
			"messages":   []map[string]string{{"role": "user", "content": "hello"}},
			"max_tokens": 16384, "max_output_tokens": invalid,
		})
		assert.Error(t, err, "invalid alias must not disappear behind a valid budget: %v", invalid)
	}
}

func TestTextCapabilitiesDoNotAdvertiseByteGuardsAsInputTokenLimits(t *testing.T) {
	for _, model := range approved.Models {
		if model.Kind != "text" {
			continue
		}
		capability := PublicTextCapabilities(model.ID)
		assert.NotContains(t, capability, "max_input_tokens", model.ID)
		assert.Positive(t, model.ContextWindow, "verified per-model context must remain available")
		assert.Equal(t, 8192, model.MaxOutputTokens, "approved product default must remain available")
	}
}

func TestTextPreviewSeparatesTokenEstimatesFromBodyByteProtection(t *testing.T) {
	input, output, maxOutput := 65536, 8192, 16384
	preview, err := BuildPreview(PreviewRequest{
		ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: strings.Repeat("字", 10000),
		InputTokens: &input, OutputTokens: &output, MaxOutputTokens: &maxOutput,
	})
	require.NoError(t, err)
	assert.EqualValues(t, maxOutput, preview.Body["params"].(map[string]any)["max_tokens"])
	assert.True(t, preview.Estimated)
	assert.False(t, preview.GenerationEnabled)
	for _, request := range []PreviewRequest{
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: strings.Repeat("😀", 16000)},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "hello", InputTokens: common.GetPointer(common.MaxRelayOutputTokens + 1)},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "hello", MaxOutputTokens: common.GetPointer(common.MaxRelayOutputTokens + 1)},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "hello", OutputTokens: common.GetPointer(1000001)},
		{ModelID: "ycapi-gpt-6.1-sol", SpecID: "tokens", Prompt: "hello", OutputTokens: common.GetPointer(1025), MaxOutputTokens: common.GetPointer(1024)},
	} {
		_, err := BuildPreview(request)
		assert.Error(t, err)
	}
}

func TestChatRuntimeUnwrapKeepsUsageWithoutSupplierFinance(t *testing.T) {
	clean, status, err := PublicChatResponse([]byte(`{"object":"model_runtime.invoke","status":"succeeded","lane":{"provider":"private-route"},"billing":{"settled_amount_credits":99},"routing_receipt":{"selected_lane_id":"private-lane"},"result":{"id":"chat1","object":"chat.completion","choices":[{"message":{"content":"OK","tool_calls":[{"id":"call1","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":23,"completion_tokens_details":{"reasoning_tokens":17},"cost_rmb":9,"balance_rmb":99}}}`), nil)
	require.NoError(t, err)
	assert.Zero(t, status)
	for _, private := range []string{"private-route", "private-lane", "settled_amount", "cost_rmb", "balance_rmb", "routing_receipt"} {
		assert.NotContains(t, string(clean), private)
	}
	var value map[string]any
	require.NoError(t, common.Unmarshal(clean, &value))
	usage := value["usage"].(map[string]any)
	assert.EqualValues(t, 11, usage["prompt_tokens"])
	assert.EqualValues(t, 23, usage["completion_tokens"])
	assert.EqualValues(t, 17, usage["completion_tokens_details"].(map[string]any)["reasoning_tokens"])
	assert.Contains(t, string(clean), "call1")
	assert.Contains(t, string(clean), "tool_calls")
	clean, status, err = PublicChatResponse([]byte(`{"object":"model_runtime.invoke","status":"failed","error_code":"insufficient_balance","error":"private supplier account balance"}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 503, status)
	assert.Contains(t, string(clean), "upstream_balance_unavailable")
	assert.NotContains(t, string(clean), "private supplier account balance")
	clean, _, err = PublicChatResponse([]byte(`{"error":{"message":"stream HTTP403: no access to secret-supplier-group","error_class":"vendor_model_unavailable","type":"stream_error","fallback_safe":true,"billed_or_processing":false}}`), nil)
	require.NoError(t, err)
	assert.NotContains(t, string(clean), "secret-supplier-group")
	assert.Contains(t, string(clean), "model_route_unavailable")
	assert.Contains(t, string(clean), `"billed_or_processing":false`)
}

func TestChatStreamFinanceTailsCannotResetPublicUsage(t *testing.T) {
	for _, raw := range []string{
		`{"object":"chat.completion.chunk","choices":[],"usage":{"cost_rmb":9,"balance_rmb":99}}`,
		`{"omf_billing":{"actual_cost_credits":9,"channel_id":"private-lane"}}`,
	} {
		clean, skip, err := PublicChatStreamResponse([]byte(raw), nil)
		require.NoError(t, err)
		assert.True(t, skip)
		assert.Empty(t, clean)
	}
	for _, raw := range []string{
		`{"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":30,"total_tokens":80,"cost_rmb":9}}`,
		`{"choices":[{"delta":{"content":"OK"}}],"usage":{"cost_rmb":9}}`,
		`{"error":{"code":"insufficient_quota","message":"private account"},"usage":{"cost_rmb":9}}`,
		`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
	} {
		clean, skip, err := PublicChatStreamResponse([]byte(raw), nil)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.NotEmpty(t, clean)
		assert.NotContains(t, string(clean), "cost_rmb")
		assert.NotContains(t, string(clean), `"usage":{}`)
	}
}

func TestTextCapabilityFactsAndPublicCopyStaySeparate(t *testing.T) {
	require.NoError(t, textCapabilityError)
	// Host and Pi validate these as required booleans when a mapping is active.
	// An omitted false value invalidates the entire discovery catalog there.
	for _, model := range textCapabilities.Models {
		mapping := PublicTextCapabilities(model.ModelID)["reasoning_control_mapping"].(map[string]any)
		if len(mapping) > 0 {
			assert.IsType(t, false, mapping["enable_thinking_with_effort"], model.ModelID)
			assert.IsType(t, false, mapping["omit_effort_on_none"], model.ModelID)
		}
	}
	_, removedErr := LookupTextCapability("ycapi-claude-fable-5-1")
	assert.Error(t, removedErr)
	capability := PublicTextCapabilities("ycapi-claude-haiku-4-5")
	assert.Equal(t, true, capability["tools"])
	assert.Equal(t, true, capability["vision"])
	assert.Equal(t, "supported", capability["verification"].(map[string]any)["vision"])
	encoded, err := common.Marshal(capability)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "lane_id")
	assert.NotContains(t, string(encoded), "procurement")
	controls := capability["reasoning_controls"].(map[string]any)["properties"].(map[string]any)["reasoning_effort"].(map[string]any)
	controls["enum"].([]any)[0] = "forged"
	unchanged := PublicTextCapabilities("ycapi-claude-haiku-4-5")["reasoning_controls"].(map[string]any)["properties"].(map[string]any)["reasoning_effort"].(map[string]any)
	assert.Equal(t, "none", unchanged["enum"].([]any)[0])
	for _, effort := range []string{"none", "high"} {
		envelope, err := ChatEnvelope("ycapi-claude-haiku-4-5", map[string]any{"model": "ycapi-claude-haiku-4-5", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "max_tokens": 2048, "reasoning_effort": effort})
		require.NoError(t, err)
		params := envelope["params"].(map[string]any)
		assert.Equal(t, effort != "none", params["enable_thinking"])
		assert.Equal(t, effort, params["reasoning_effort"])
	}
}
