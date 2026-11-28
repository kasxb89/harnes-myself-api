package service

import (
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaGenerationThirtyGatePreservesConditionalLegacyDefaults(t *testing.T) {
	contract, ok := ycapi.LookupMediaContract("ycapi-kling-v4")
	require.True(t, ok)
	var controls map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"duration":{"type":"integer","enum":[15,30]},"mode":{"type":"string","enum":["text-to-video","extend"]},"aspect_ratio":false,"count":{"type":"integer","const":1}},"oneOf":[{"properties":{"mode":{"type":"string","const":"extend"},"duration":{"type":"integer","const":15},"aspect_ratio":false}}]}`, &controls))
	params := map[string]any{"mode": "extend", "duration": "15", "count": float64(1)}
	require.NoError(t, validateMediaGenerationControls(contract, controls, params), "the existing administrator control accepts the original missing aspect ratio")
	before, err := common.Marshal(params)
	require.NoError(t, err)
	def := model.ModelConfiguration{ModelName: contract.ID, Kind: "video", Parameters: controls}
	require.NoError(t, validateMediaGenerationReviewedSpecification(def, contract, params, nil), "the new text30 review cannot inject a default unavailable in extend mode")
	after, err := common.Marshal(params)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestMediaGenerationReviewedThirtyConfigProjection(t *testing.T) {
	previousQuota, previousFX := common.QuotaPerUnit, operation_setting.USDExchangeRate
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 500000, 7.3
	t.Cleanup(func() { common.QuotaPerUnit, operation_setting.USDExchangeRate = previousQuota, previousFX })
	rows, err := mediaGenerationThirtySourceRows()
	require.NoError(t, err)
	models := []string{}
	for _, row := range rows {
		if len(models) == 0 || models[len(models)-1] != row.Model {
			models = append(models, row.Model)
		}
	}
	require.Len(t, models, 11)
	baselineCatalog, alternateCatalogs, plans := []any{}, []any{}, []any{}
	definitions := map[string]model.ModelConfiguration{}
	positive := 0
	for _, id := range models {
		contract, ok := ycapi.LookupMediaContract(id)
		require.True(t, ok)
		reviewRows, err := MediaGenerationThirtyReviewRows(id)
		require.NoError(t, err)
		existing := model.ModelConfiguration{ModelName: id, DisplayName: contract.Name, Kind: "video", Source: "synthetic review fixture, never production qualification", Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("original-synthetic-non30", u("seconds") * 0.01)`}}
		input := MediaGenerationReviewInput{Existing: existing, ExpectedRevision: strings.Repeat("a", 64), BaseVersion: strings.Repeat("b", 64), FXCNYPerUSD: "7.3", Rows: reviewRows}
		before, err := common.Marshal(input)
		require.NoError(t, err)
		plan, err := PrepareMediaGenerationReviewPlan(input)
		require.NoError(t, err, id)
		assert.False(t, plan.CanApply)
		assert.True(t, plan.ReviewOnly)
		plans = append(plans, plan)
		for candidateIndex, candidate := range plan.Candidates {
			def := existing
			def.Parameters, def.Pricing = candidate.Parameters, candidate.Pricing
			def.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: candidate.LaneID, Intents: ycapi.MediaContractIntents(contract), ContractSHA256: MediaGenerationDigest(contract), QualificationSHA256: strings.Repeat("c", 64), CheckedAt: "2026-11-28"}
			// This binding exists only in this pure Go test; it is never saved or
			// exported as an executable production configuration.
			definitions[id+"|"+candidate.LaneID] = def
			schema, err := mediaGenerationConfiguredSchema(contract, def.Parameters)
			require.NoError(t, err, id)
			pricing, err := mediaGenerationSpecificationPricing(def, contract, def.Pricing["billing_setting.billing_expr"].(string), 1)
			require.NoError(t, err)
			require.NotNil(t, pricing)
			pricing["display"] = "按所选规格显示费用"
			pricing["quote_required"] = true
			priceRows := pricing["specification_rows"].([]map[string]any)
			require.Len(t, priceRows, len(candidate.Rows))
			for _, row := range candidate.Rows {
				positive++
				params := maps.Clone(row.Parameters)
				params["prompt"] = "A local review, not a generation"
				require.NoError(t, validateMediaGenerationReviewedSpecification(def, contract, params, nil), row.ID)
				assert.Contains(t, def.Pricing["billing_setting.billing_expr"], existing.Pricing["billing_setting.billing_expr"])
				quantity := 1
				if row.BillingUnit == "per_second" {
					quantity = 30
				}
				for _, projected := range priceRows {
					if projected["parameters"].(map[string]any)["resolution"] != row.Parameters["resolution"] {
						continue
					}
					assert.EqualValues(t, quantity, projected["quantity"])
					actual, err := decimal.NewFromString(projected["total_cny"].(string))
					require.NoError(t, err)
					nominal := decimal.RequireFromString(row.RetailTotalCNY)
					assert.True(t, actual.Sub(nominal).Abs().LessThan(decimal.RequireFromString("0.00002")), "normal existing quota quantization, never a second count/duration multiplier")
				}
			}
			encoded, err := common.Marshal(pricing)
			require.NoError(t, err)
			for _, private := range []string{"cost_total_cny", "lane_id", "quote_request_sha256", "qualification_sha256", "credential"} {
				assert.NotContains(t, string(encoded), private)
			}
			fields, err := mediaGenerationCatalogAssetFields(contract, schema)
			require.NoError(t, err)
			intents := mediaGenerationCatalogIntents(contract.Kind, def.MediaExecution.Intents, fields)
			entry := mediaGenerationCatalogEntry(def, contract, MediaGenerationDigest(def), schema, fields, intents, pricing)
			require.NotNil(t, entry, id)
			assert.False(t, mediaSelectionHasCombinator(entry["parameters_schema"]), "native public schema is flat at every depth")
			require.Contains(t, entry, "selection_constraints")
			for _, row := range candidate.Rows {
				assert.True(t, mediaSelectionFixtureMatches(entry["selection_constraints"].(map[string]any), row.Parameters), row.ID)
			}
			if candidateIndex == 0 {
				baselineCatalog = append(baselineCatalog, entry)
			} else {
				alternateCatalogs = append(alternateCatalogs, map[string]any{"scenario": "separately-qualified-lane-required", "models": []any{entry}})
			}
		}
		after, err := common.Marshal(input)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	}
	require.Equal(t, 22, positive)
	require.Len(t, baselineCatalog, 11)
	require.Len(t, alternateCatalogs, 1)
	negative := []map[string]any{}
	for _, tc := range []struct{ id, resolution string }{{"ycapi-seedance-2-5-special", "480p"}, {"ycapi-kling-v4", "4k"}, {"ycapi-minimax-h3", "480p"}, {"ycapi-minimax-h3", "1080p"}, {"ycapi-minimax-h3", "2k"}} {
		contract, ok := ycapi.LookupMediaContract(tc.id)
		require.True(t, ok)
		for key, def := range definitions {
			if def.ModelName != tc.id {
				continue
			}
			params := map[string]any{"mode": "text-to-video", "duration": "30", "count": 1, "aspect_ratio": "16:9", "resolution": tc.resolution, "prompt": "Keep the original 30-second intent"}
			if _, exists := contract.Parameters["quality"]; exists {
				params["quality"] = "标准"
				if tc.id == "ycapi-kling-v4" {
					params["quality"] = "standard"
				}
			}
			before, err := common.Marshal(params)
			require.NoError(t, err)
			require.Error(t, validateMediaGenerationReviewedSpecification(def, contract, params, nil), key)
			schema, err := mediaGenerationConfiguredSchema(contract, def.Parameters)
			require.NoError(t, err)
			entry := mediaGenerationCatalogEntry(def, contract, MediaGenerationDigest(def), schema, nil, nil, nil)
			require.NotNil(t, entry)
			assert.False(t, mediaSelectionFixtureMatches(entry["selection_constraints"].(map[string]any), params), "the public finite selection rejects the same blocked specification")
			after, err := common.Marshal(params)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			negative = append(negative, map[string]any{"model": tc.id, "parameters": params, "allowed": false, "vendor_IO": 0, "billing": 0})
		}
	}
	require.Len(t, negative, 5)
	if output := os.Getenv("YC30_CONFIG_CATALOG_OUTPUT"); output != "" {
		require.True(t, filepath.IsAbs(output))
		maxUploadBytes, err := MediaGenerationMaxUploadBytes()
		require.NoError(t, err)
		fixture := map[string]any{"success": true, "schema": 1, "request_id": "4d206f8d-3cb6-40ff-95a8-bcab65801e30", "synthetic": true, "execution_qualified": false, "source_worktree": "uncommitted-R04-selection-constraints-until-owner-final-source-binding", "fixture_kind": "actual-Go-review-plan-and-projection-helpers-not-continuous-service", "conversion": map[string]any{"synthetic_FX_CNY_per_USD": "7.3", "synthetic_quota_per_USD": 500000, "not_production_FX_or_credit_rate": true}, "data": map[string]any{"models": baselineCatalog, "capabilities": MediaGenerationCatalogCapabilities(maxUploadBytes)}, "alternate_catalogs": alternateCatalogs, "review_only_plans": plans, "negative_cases": negative, "model_count": 11, "allowed_specifications": positive, "blocked_specifications": 5, "selection_projection_checks": map[string]any{"flat_schema_no_combinators": true, "positive_selection_matches": positive, "negative_selection_rejections": len(negative), "legacy_case_test": "TestMediaGenerationSelectionConstraintsFiniteProjection/old15-reference", "conditional_legacy_test": "TestMediaGenerationThirtyGatePreservesConditionalLegacyDefaults"}, "provider_requests": 0, "database_writes": 0}
		raw, err := common.Marshal(fixture)
		require.NoError(t, err)
		file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = file.Write(raw)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		for name, data := range map[string]any{
			"ACTUAL-GO-HTTP-CATALOG-BASELINE-R04.json": fixture["data"],
			"ACTUAL-GO-HTTP-CATALOG-PROMO4K-R04.json":  map[string]any{"models": alternateCatalogs[0].(map[string]any)["models"], "capabilities": MediaGenerationCatalogCapabilities(maxUploadBytes)},
		} {
			encoded, err := common.Marshal(map[string]any{"success": true, "data": data})
			require.NoError(t, err)
			publicFile, err := os.OpenFile(filepath.Join(filepath.Dir(output), name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			require.NoError(t, err)
			_, err = publicFile.Write(encoded)
			require.NoError(t, err)
			require.NoError(t, publicFile.Close())
		}
		t.Logf("review catalog fixture exported models=11 allowed=22 blocked=5 path=%s", output)
	}
}

func TestMediaGenerationSelectionConstraintsFiniteProjection(t *testing.T) {
	var schema map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"type":"object","properties":{"mode":{"type":"string","enum":["text-to-video","reference"]},"duration":{"type":"string","enum":["15","30"],"default":"15"},"count":{"type":"integer","minimum":1,"maximum":2},"images":{"type":"array","items":{"type":"string"},"maxItems":2}},"x-media-assets":[{"name":"images"}],"oneOf":[{"properties":{"mode":{"type":"string","const":"reference"},"duration":{"type":"string","const":"15"},"count":{"type":"integer","minimum":1,"maximum":2},"images":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2}},"required":["mode","duration","images"]},{"properties":{"mode":{"type":"string","const":"text-to-video"},"duration":{"type":"string","enum":["30"],"const":"30","default":"30"},"count":{"type":"integer","const":1},"images":false},"required":["mode","duration","count"]}]}`, &schema))
	before, err := common.Marshal(schema)
	require.NoError(t, err)
	flat, selection, err := mediaGenerationCatalogSelection(schema)
	require.NoError(t, err)
	assert.False(t, mediaSelectionHasCombinator(flat))
	require.Len(t, selection["combinations"], 2)
	for _, tc := range []struct {
		name    string
		params  map[string]any
		allowed bool
	}{
		{"old15-reference", map[string]any{"mode": "reference", "duration": "15", "count": float64(2), "images": []any{"local-owned-image"}}, true},
		{"old15-reference-lower-count", map[string]any{"mode": "reference", "duration": "15", "count": float64(1), "images": []any{"a", "b"}}, true},
		{"missing-reference", map[string]any{"mode": "reference", "duration": "15", "count": float64(1)}, false},
		{"too-many-references", map[string]any{"mode": "reference", "duration": "15", "count": float64(1), "images": []any{"a", "b", "c"}}, false},
		{"new30-text", map[string]any{"mode": "text-to-video", "duration": "30", "count": float64(1)}, true},
		{"new30-empty-reference", map[string]any{"mode": "text-to-video", "duration": "30", "count": float64(1), "images": []any{}}, true},
		{"new30-never-discard-reference", map[string]any{"mode": "text-to-video", "duration": "30", "count": float64(1), "images": []any{"owned"}}, false},
		{"new30-count2", map[string]any{"mode": "text-to-video", "duration": "30", "count": float64(2)}, false},
		{"native-scalar-type", map[string]any{"mode": "text-to-video", "duration": "30", "count": "1"}, false},
		{"no-branch-default-injection", map[string]any{"mode": "text-to-video", "count": float64(1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.allowed, mediaSelectionFixtureMatches(selection, tc.params)) })
	}
	encoded, err := common.Marshal(selection)
	require.NoError(t, err)
	for _, excluded := range []string{"default", "x-ycapi-contract", "\"items\":", "oneOf", "allOf", "anyOf", "local-owned-image"} {
		assert.NotContains(t, string(encoded), excluded)
	}
	after, err := common.Marshal(schema)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	flat["properties"].(map[string]any)["duration"].(map[string]any)["default"] = "30"
	assert.Equal(t, "15", schema["properties"].(map[string]any)["duration"].(map[string]any)["default"], "public projection is detached")
}

func TestMediaGenerationSelectionConstraintsFailClosed(t *testing.T) {
	for name, fragment := range map[string]string{
		"unknown-field":               `{"properties":{"not_declared":{"type":"integer","const":1}}}`,
		"nested-combinator":           `{"properties":{"count":{"type":"integer","anyOf":[{}]}}}`,
		"wrong-scalar-type":           `{"properties":{"count":{"type":"integer","const":"1"}}}`,
		"duplicate-choice":            `{"properties":{"count":{"type":"integer","enum":[1,1]}}}`,
		"contradictory-enum-constant": `{"properties":{"count":{"type":"integer","enum":[1],"const":2}}}`,
		"fractional-count":            `{"properties":{"count":{"type":"integer","const":1.5}}}`,
		"array-value-privacy":         `{"properties":{"images":{"type":"array","items":{"type":"string"},"const":["https://private.invalid/image"]}}}`,
		"negative-asset-bound":        `{"properties":{"images":{"type":"array","items":{"type":"string"},"minItems":-1}}}`,
		"fractional-asset-bound":      `{"properties":{"images":{"type":"array","items":{"type":"string"},"maxItems":1.5}}}`,
		"wrong-bound-type":            `{"properties":{"count":{"type":"integer","maxItems":1}}}`,
		"reversed-bound":              `{"properties":{"count":{"type":"integer","minimum":2,"maximum":1}}}`,
		"unsupported-constraint":      `{"properties":{"count":{"type":"integer","multipleOf":2}}}`,
		"required-disabled":           `{"properties":{"images":false},"required":["images"]}`,
		"required-unknown":            `{"properties":{},"required":["not_declared"]}`,
		"required-duplicate":          `{"properties":{"count":{"type":"integer"}},"required":["count","count"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var schema map[string]any
			require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer"},"images":{"type":"array","items":{"type":"string"}}},"x-media-assets":[{"name":"images"}],"oneOf":[{"properties":{"count":{"type":"integer","const":1}}},`+fragment+`]}`, &schema))
			flat, selection, err := mediaGenerationCatalogSelection(schema)
			require.Error(t, err, "a malformed second branch cannot be discarded in favor of the first")
			assert.Nil(t, flat)
			assert.Nil(t, selection)
			assert.Nil(t, mediaGenerationCatalogEntry(model.ModelConfiguration{}, ycapi.MediaContract{}, "", schema, nil, nil, nil))
		})
	}
	var nested map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer","x-native":{"allOf":[]}}}}`, &nested))
	_, _, err := mediaGenerationCatalogSelection(nested)
	require.Error(t, err)
	_, _, err = mediaGenerationCatalogSelection(map[string]any{"properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": math.NaN()}}})
	require.Error(t, err)
	assert.Equal(t, true, MediaGenerationCatalogCapabilities(50 << 20)["selection_constraints_v1"])
}

// This independent finite-wire consumer deliberately reads no server-private
// controls; fixture cases verify both halves without granting execution.
func mediaSelectionFixtureMatches(selection, params map[string]any) bool {
	for _, raw := range selection["combinations"].([]any) {
		combination := raw.(map[string]any)
		matches := true
		for _, name := range anySlice(combination["required_parameters"]) {
			if _, present := params[name.(string)]; !present {
				matches = false
			}
		}
		for name, rawPredicate := range combination["parameters"].(map[string]any) {
			value, present := params[name]
			if !present {
				continue
			}
			predicate := rawPredicate.(map[string]any)
			if predicate["disabled"] == true {
				if array, ok := value.([]any); value != nil && value != false && (!ok || len(array) != 0) {
					matches = false
				}
				continue
			}
			if allowed, present := predicate["allowed_values"].([]any); present && !slices.ContainsFunc(allowed, func(candidate any) bool { return reflect.DeepEqual(candidate, value) }) {
				matches = false
			}
			for _, bound := range []struct {
				name         string
				lower, count bool
			}{{"minimum", true, false}, {"maximum", false, false}, {"min_items", true, true}, {"max_items", false, true}} {
				limit, bounded := predicate[bound.name].(float64)
				if !bounded {
					continue
				}
				number, valid := value.(float64)
				if bound.count {
					var array []any
					array, valid = value.([]any)
					number = float64(len(array))
				}
				if !valid || bound.lower && number < limit || !bound.lower && number > limit {
					matches = false
				}
			}
		}
		if matches {
			return true
		}
	}
	return false
}
