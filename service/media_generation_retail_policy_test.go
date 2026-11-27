package service

import (
	"fmt"
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mediaRetailPolicyFixture(t *testing.T) (model.ModelConfiguration, ycapi.MediaContract, map[string]any, map[string]any, map[string]ycapi.MediaAssetMetadata, string) {
	t.Helper()
	contract, ok := ycapi.LookupMediaContract("ycapi-gpt-image-2.6-flare")
	require.True(t, ok)
	metadata := map[string]ycapi.MediaAssetMetadata{"asset:synthetic-image": {Kind: "image", MIMEType: "image/png", Width: 512, Height: 512, Bytes: 1234}}
	params, err := ycapi.ValidateMediaContractParams(contract.ID, map[string]any{"mode": "image-edit", "prompt": "Synthetic blue cup", "images": []string{"asset:synthetic-image"}}, metadata)
	require.NoError(t, err)
	facts := mediaGenerationUsage(contract, params, metadata, ycapi.MediaIntentReference)
	expr := fmt.Sprintf(`tier("reviewed", u("image_count") * (0.2 / %g))`, operation_setting.USDExchangeRate)
	execution := &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: contract.NativeID + "@synthetic:only", Intents: []string{"reference"}, QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: MediaGenerationDigest(contract), CheckedAt: "2026-11-26"}
	execution.RetailPolicy = &model.ModelMediaRetailPolicy{SchemaVersion: 1, Policy: "official_cap", ExpressionSHA256: billingexpr.ExprHashString(expr), LaneID: execution.LaneID, ContractSHA256: execution.ContractSHA256, QualificationSHA256: execution.QualificationSHA256, EvidenceSHA256: strings.Repeat("b", 64), CheckedAt: "2026-11-26", Rules: []model.ModelMediaRetailPolicyRule{{SpecificationSHA256: MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata), OfficialTotalCNY: "0.2", OfficialModel: contract.NativeID, SourceURL: "https://example.com/synthetic-official-prices"}}}
	def := model.ModelConfiguration{ModelName: contract.ID, Kind: contract.Kind, MediaExecution: execution, Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expr}}
	require.NoError(t, model.ValidateModelMediaExecutionConfiguration(def))
	return def, contract, params, facts, metadata, expr
}

func TestMediaGenerationRetailPolicyRequiresExactReviewedOfficialCap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*model.ModelConfiguration, map[string]any, map[string]any, *string)
		ratio  float64
		cost   string
		allow  bool
		proof  bool
	}{
		{name: "same specification knowingly capped below procurement", ratio: 1, cost: "0.3", allow: true, proof: true},
		{name: "no policy cannot authorize a loss", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) { d.MediaExecution.RetailPolicy = nil }},
		{name: "explicit disable restores the default guard", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.RetailPolicy = &model.ModelMediaRetailPolicy{SchemaVersion: 1, Policy: "disabled"}
		}},
		{name: "different resolution has no wildcard exception", ratio: 1, cost: "0.3", modify: func(_ *model.ModelConfiguration, p, _ map[string]any, _ *string) { p["resolution"] = "2K" }},
		{name: "different count facts have no wildcard exception", ratio: 1, cost: "0.6", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ *string) { f["image_count"] = float64(2) }},
		{name: "an unmetered native format still belongs to the specification", ratio: 1, cost: "0.3", modify: func(_ *model.ModelConfiguration, p, _ map[string]any, _ *string) { p["output_format"] = "png" }},
		{name: "different actual provider expression cannot borrow evidence", ratio: 1, cost: "0.3", modify: func(_ *model.ModelConfiguration, _, _ map[string]any, e *string) { *e += " + 0" }},
		{name: "changed draft expression invalidates old evidence", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, e *string) {
			*e += " + 0"
			d.Pricing["billing_setting.billing_expr"] = *e
		}},
		{name: "changed supplier lane invalidates old evidence", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.LaneID += ":changed"
		}},
		{name: "changed native contract invalidates old evidence", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.ContractSHA256 = strings.Repeat("c", 64)
		}},
		{name: "changed execution qualification invalidates old evidence", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.QualificationSHA256 = strings.Repeat("c", 64)
		}},
		{name: "another official model is not same-model evidence", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.RetailPolicy.Rules[0].OfficialModel = "another-model"
		}},
		{name: "below official is not the approved official cap", ratio: 1, cost: "0.4", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.RetailPolicy.Rules[0].OfficialTotalCNY = "0.3"
		}},
		{name: "group discount cannot authorize an extra loss", ratio: 0.5, cost: "0.3"},
		{name: "a nonloss tariff needs no policy exception", ratio: 1, cost: "0.1", allow: true},
		{name: "group markup retains original expression and rounding", ratio: 2, cost: "0.3", allow: true},
		{name: "markup still below procurement uses only reviewed base cap", ratio: 1.1, cost: "0.4", allow: true, proof: true},
		{name: "missing official evidence is not invented", ratio: 1, cost: "0.3", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.RetailPolicy.Rules[0].OfficialTotalCNY = ""
		}},
		{name: "oversized official amount cannot overflow into acceptance", ratio: 1, cost: "1000000000000000000", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ *string) {
			d.MediaExecution.RetailPolicy.Rules[0].OfficialTotalCNY = "999999999999999999"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def, contract, params, facts, metadata, expr := mediaRetailPolicyFixture(t)
			if tc.modify != nil {
				tc.modify(&def, params, facts, &expr)
			}
			before, err := common.Marshal(def)
			require.NoError(t, err)
			_, retail, _, err := mediaGenerationRetail(expr, facts, tc.ratio)
			require.NoError(t, err)
			proof, err := validateMediaGenerationRetailCost(def, contract, params, facts, metadata, expr, decimal.RequireFromString(tc.cost), retail, tc.ratio)
			if tc.allow {
				require.NoError(t, err)
				if tc.proof {
					assert.Equal(t, MediaGenerationDigest(def.MediaExecution.RetailPolicy), proof)
				} else {
					assert.Empty(t, proof)
				}
			} else {
				require.EqualError(t, err, "model_price_unavailable")
				assert.Empty(t, proof)
			}
			after, err := common.Marshal(def)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "admission never rewrites prices, cost evidence, or execution configuration")
		})
	}
}

func TestMediaGenerationRetailPolicyEvidenceValidationAndPrivateSpecification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*model.ModelMediaRetailPolicy)
	}{
		{"missing provenance", func(p *model.ModelMediaRetailPolicy) { p.EvidenceSHA256 = "" }},
		{"invalid real calendar date", func(p *model.ModelMediaRetailPolicy) { p.CheckedAt = "2026-02-30" }},
		{"future review date", func(p *model.ModelMediaRetailPolicy) { p.CheckedAt = "9999-01-01" }},
		{"credential in source authority", func(p *model.ModelMediaRetailPolicy) { p.Rules[0].SourceURL = "https://secret@example.com/prices" }},
		{"credential in source query", func(p *model.ModelMediaRetailPolicy) {
			p.Rules[0].SourceURL = "https://example.com/prices?api_key=secret"
		}},
		{"private source host", func(p *model.ModelMediaRetailPolicy) { p.Rules[0].SourceURL = "https://127.0.0.1/prices" }},
		{"duplicate exact specification", func(p *model.ModelMediaRetailPolicy) { p.Rules = append(p.Rules, p.Rules[0]) }},
		{"disabled cannot smuggle active evidence", func(p *model.ModelMediaRetailPolicy) { p.Policy = "disabled" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def, _, _, _, _, _ := mediaRetailPolicyFixture(t)
			tc.modify(def.MediaExecution.RetailPolicy)
			require.Error(t, model.ValidateModelMediaExecutionConfiguration(def))
		})
	}
	t.Run("normal provider override is the actual expression binding", func(t *testing.T) {
		def, _, _, _, _, expr := mediaRetailPolicyFixture(t)
		def.Pricing["billing_setting.billing_expr"] = "outdated inherited price"
		def.Pricing[billing_setting.PluginBillingExprOption] = map[string]any{"ycapi": expr}
		require.NoError(t, model.ValidateModelMediaExecutionConfiguration(def))
		def.Pricing[billing_setting.PluginBillingExprOption] = map[string]any{"ycapi": expr + " + 0"}
		require.Error(t, model.ValidateModelMediaExecutionConfiguration(def))
	})
	t.Run("prompt and authorized asset URLs are absent but quantities remain bound", func(t *testing.T) {
		_, contract, params, facts, metadata, _ := mediaRetailPolicyFixture(t)
		original := MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata)
		changed := maps.Clone(params)
		changed["prompt"] = "A different private request"
		changed["negative_prompt"] = "Private excluded content"
		changed["images"] = []string{"https://example.invalid/other-private-asset?token=secret"}
		changedMetadata := map[string]ycapi.MediaAssetMetadata{"https://example.invalid/other-private-asset?token=secret": metadata["asset:synthetic-image"]}
		assert.Equal(t, original, MediaGenerationRetailSpecificationSHA(contract, changed, facts, changedMetadata))
		newFacts := maps.Clone(facts)
		newFacts["input_image_count"] = float64(2)
		assert.NotEqual(t, original, MediaGenerationRetailSpecificationSHA(contract, changed, newFacts, changedMetadata))
		assert.Empty(t, MediaGenerationRetailSpecificationSHA(contract, params, facts, nil), "an asset URL is not evidence of its probed input specification")
		larger := maps.Clone(metadata)
		measured := larger["asset:synthetic-image"]
		measured.Width = 1024
		larger["asset:synthetic-image"] = measured
		assert.NotEqual(t, original, MediaGenerationRetailSpecificationSHA(contract, params, facts, larger))
		changed["extra"] = "opaque supplier extension"
		assert.Empty(t, MediaGenerationRetailSpecificationSHA(contract, changed, facts, changedMetadata), "unknown opaque controls cannot borrow a reviewed specification")
	})
	t.Run("missing or changed input measurements cannot authorize a loss", func(t *testing.T) {
		def, contract, params, facts, metadata, expr := mediaRetailPolicyFixture(t)
		_, retail, _, err := mediaGenerationRetail(expr, facts, 1)
		require.NoError(t, err)
		for _, measurements := range []map[string]ycapi.MediaAssetMetadata{nil, {"asset:synthetic-image": {Kind: "image", MIMEType: "image/png", Width: 1024, Height: 512}}} {
			proof, err := validateMediaGenerationRetailCost(def, contract, params, facts, measurements, expr, decimal.RequireFromString("0.3"), retail, 1)
			require.EqualError(t, err, "model_price_unavailable")
			assert.Empty(t, proof)
		}
		second := metadata["asset:synthetic-image"]
		second.Width = 1024
		metadata["asset:second-image"] = second
		params["mode"], params["images"] = "multi-reference", []string{"asset:synthetic-image", "asset:second-image"}
		facts["input_image_count"] = float64(2)
		ordered := MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata)
		require.NotEmpty(t, ordered)
		params["images"] = []string{"asset:second-image", "asset:synthetic-image"}
		assert.NotEqual(t, ordered, MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata), "reference role and order must remain part of the approved input specification")
	})
	t.Run("missing official uses the existing explicit cost plus twenty percent tariff", func(t *testing.T) {
		def, contract, params, facts, metadata, _ := mediaRetailPolicyFixture(t)
		def.MediaExecution.RetailPolicy = nil
		expr := fmt.Sprintf(`tier("cost_plus_twenty_percent", u("image_count") * (0.36 / %g))`, operation_setting.USDExchangeRate)
		def.Pricing["billing_setting.billing_expr"] = expr
		quota, retail, _, err := mediaGenerationRetail(expr, facts, 1)
		require.NoError(t, err)
		expected, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(0.36).Div(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
		require.Nil(t, clamp)
		assert.Equal(t, expected, quota)
		proof, err := validateMediaGenerationRetailCost(def, contract, params, facts, metadata, expr, decimal.RequireFromString("0.3"), retail, 1)
		require.NoError(t, err)
		assert.Empty(t, proof, "no official source or new monetary rule is fabricated")
	})
	t.Run("invalid conversion cannot produce a quota or panic", func(t *testing.T) {
		_, _, _, facts, _, expr := mediaRetailPolicyFixture(t)
		originalFX, originalUnit := operation_setting.USDExchangeRate, common.QuotaPerUnit
		t.Cleanup(func() { operation_setting.USDExchangeRate, common.QuotaPerUnit = originalFX, originalUnit })
		for _, invalid := range []float64{math.NaN(), math.Inf(1), 0, -1} {
			operation_setting.USDExchangeRate = invalid
			_, _, _, err := mediaGenerationRetail(expr, facts, 1)
			require.Error(t, err)
			operation_setting.USDExchangeRate = originalFX
			common.QuotaPerUnit = invalid
			_, _, _, err = mediaGenerationRetail(expr, facts, 1)
			require.Error(t, err)
			common.QuotaPerUnit = originalUnit
		}
	})
}

func TestMediaGenerationRetailPolicyPreservesExactLegacySingleVideoEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		allow  bool
		modify func(*model.ModelConfiguration, map[string]any, map[string]any, map[string]ycapi.MediaAssetMetadata, *string)
	}{
		{name: "host aggregate defaults retain the original single video rule", allow: true},
		{name: "current exact specification remains valid", allow: true, modify: func(d *model.ModelConfiguration, p, f map[string]any, m map[string]ycapi.MediaAssetMetadata, _ *string) {
			c, _ := ycapi.LookupMediaContract(d.ModelName)
			d.MediaExecution.RetailPolicy.Rules[0].SpecificationSHA256 = MediaGenerationRetailSpecificationSHA(c, p, f, m)
		}},
		{name: "an explicit disabled batch policy remains single", allow: true, modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			d.MediaExecution.BatchPolicy = &model.ModelMediaBatchPolicy{SchemaVersion: 1, Policy: "disabled"}
		}},
		{name: "changed resolution cannot borrow old evidence", modify: func(_ *model.ModelConfiguration, p, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			p["resolution"] = "720p"
		}},
		{name: "changed measured image cannot borrow old evidence", modify: func(_ *model.ModelConfiguration, _, _ map[string]any, m map[string]ycapi.MediaAssetMetadata, _ *string) {
			image := m["asset:synthetic-image"]
			image.Width = 1024
			m["asset:synthetic-image"] = image
		}},
		{name: "changed reference quantity stays bound", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["input_image_count"] = float64(2)
		}},
		{name: "all other facts remain bound", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["generate_audio"] = true
		}},
		{name: "unknown facts are not stripped", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["new_unreviewed_fact"] = true
		}},
		{name: "output count two cannot inherit a single rule", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["output_count"] = float64(2)
		}},
		{name: "native count two cannot inherit a single rule", modify: func(_ *model.ModelConfiguration, p, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			p["count"] = float64(2)
		}},
		{name: "a known aggregate duration is not the host zero default", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["total_output_seconds"] = float64(5)
		}},
		{name: "missing only one aggregate fact is not the host pair", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			delete(f, "output_count")
		}},
		{name: "strings are not host numeric defaults", modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			f["output_count"] = "1"
		}},
		{name: "legacy facts without either new key still match directly", allow: true, modify: func(_ *model.ModelConfiguration, _, f map[string]any, _ map[string]ycapi.MediaAssetMetadata, _ *string) {
			delete(f, "output_count")
			delete(f, "total_output_seconds")
		}},
		{name: "the new aggregate seconds meter cannot inherit old evidence", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, e *string) {
			*e += ` + u("total_output_seconds") * 0.01`
			d.Pricing["billing_setting.billing_expr"] = *e
			d.MediaExecution.RetailPolicy.ExpressionSHA256 = billingexpr.ExprHashString(*e)
		}},
		{name: "a new meter cannot inherit the old facts", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, e *string) {
			*e = fmt.Sprintf(`tier("reviewed", u("output_count") * (0.2 / %g))`, operation_setting.USDExchangeRate)
			d.Pricing["billing_setting.billing_expr"] = *e
			d.MediaExecution.RetailPolicy.ExpressionSHA256 = billingexpr.ExprHashString(*e)
		}},
		{name: "an active qualified batch cannot inherit the old facts", modify: func(d *model.ModelConfiguration, _, _ map[string]any, _ map[string]ycapi.MediaAssetMetadata, e *string) {
			*e = fmt.Sprintf(`tier("reviewed", u("output_count") * (0.2 / %g))`, operation_setting.USDExchangeRate)
			d.Pricing["billing_setting.billing_expr"] = *e
			d.MediaExecution.RetailPolicy.ExpressionSHA256 = billingexpr.ExprHashString(*e)
			v := d.MediaExecution
			v.BatchPolicy = &model.ModelMediaBatchPolicy{SchemaVersion: 1, Policy: "qualified_native_batch", MaxCount: 2, Meter: "output_count", Modes: []string{"first-frame"}, AdmissionUnit: "native_operation", DurationBasis: "unmetered", OutputContract: "ordered_unique_video_payloads_v1", ExpressionSHA256: billingexpr.ExprHashString(*e), LaneID: v.LaneID, ContractSHA256: v.ContractSHA256, QualificationSHA256: v.QualificationSHA256, EvidenceSHA256: strings.Repeat("c", 64), CheckedAt: "2026-11-27"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contract, ok := ycapi.LookupMediaContract("ycapi-seedance-2-5")
			require.True(t, ok)
			metadata := map[string]ycapi.MediaAssetMetadata{"asset:synthetic-image": {Kind: "image", MIMEType: "image/png", Width: 512, Height: 512, Bytes: 1234}}
			params, err := ycapi.ValidateMediaContractParams(contract.ID, map[string]any{"mode": "first-frame", "prompt": "Synthetic blue cup", "duration": "5", "images": []string{"asset:synthetic-image"}}, metadata)
			require.NoError(t, err)
			legacyFacts := mediaGenerationUsage(contract, params, metadata, ycapi.MediaIntentFrames)
			require.NotContains(t, legacyFacts, "output_count")
			require.NotContains(t, legacyFacts, "total_output_seconds")
			expr := fmt.Sprintf(`tier("reviewed", u("seconds") * (0.04 / %g))`, operation_setting.USDExchangeRate)
			execution := &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: contract.NativeID + "@synthetic:legacy", Intents: []string{"frames"}, QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: MediaGenerationDigest(contract), CheckedAt: "2026-11-27"}
			oldSHA := MediaGenerationRetailSpecificationSHA(contract, params, legacyFacts, metadata)
			require.NotEmpty(t, oldSHA)
			execution.RetailPolicy = &model.ModelMediaRetailPolicy{SchemaVersion: 1, Policy: "official_cap", ExpressionSHA256: billingexpr.ExprHashString(expr), LaneID: execution.LaneID, ContractSHA256: execution.ContractSHA256, QualificationSHA256: execution.QualificationSHA256, EvidenceSHA256: strings.Repeat("b", 64), CheckedAt: "2026-11-27", Rules: []model.ModelMediaRetailPolicyRule{{SpecificationSHA256: oldSHA, OfficialTotalCNY: "0.2", OfficialModel: contract.NativeID, SourceURL: "https://example.com/synthetic-official-prices"}}}
			def := model.ModelConfiguration{ModelName: contract.ID, Kind: "video", MediaExecution: execution, Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expr}}
			facts := maps.Clone(legacyFacts)
			facts["output_count"], facts["total_output_seconds"] = float64(1), float64(0)
			require.NotEqual(t, oldSHA, MediaGenerationRetailSpecificationSHA(contract, params, facts, metadata), "public specification hashing keeps all current facts")
			if tc.modify != nil {
				tc.modify(&def, params, facts, metadata, &expr)
			}
			require.NoError(t, model.ValidateModelMediaExecutionConfiguration(def), "negative cases must have otherwise valid evidence/configuration")
			before := MediaGenerationDigest([]any{def, params, facts, metadata})
			_, retail, _, err := mediaGenerationRetail(expr, facts, 1)
			require.NoError(t, err)
			proof, err := validateMediaGenerationRetailCost(def, contract, params, facts, metadata, expr, decimal.RequireFromString("0.3"), retail, 1)
			if tc.allow {
				require.NoError(t, err)
				assert.Equal(t, MediaGenerationDigest(def.MediaExecution.RetailPolicy), proof)
			} else {
				require.EqualError(t, err, "model_price_unavailable")
				assert.Empty(t, proof)
			}
			assert.Equal(t, before, MediaGenerationDigest([]any{def, params, facts, metadata}), "admission cannot rewrite old rules, prices, facts or measured assets")
		})
	}
}
