package codebloom

import (
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expectedIDs = `deepseek-v4.1-flash deepseek-v4.1-flash-1017 gpt-5.6-terra gpt-5.6-sol gpt-6.1-terra gpt-6-sol minimax-m3.1-flash step-5-preview mimo-v2.6-pro glm-5.3-flash deepseek-v4-pro gpt-6-astra glm-5.3-flashx kimi-k2.7-code gpt-6.1-sol hy3 qwen3.8-max deepseek-v4-flash-0731 deepseek-v4-pro-0813 kimi-k3.1-pro kimi-k2.6 deepseek-v4-flash kimi-k3 qwen3.7-max kimi-k2.7 glm-5.1 qwen3.8-flash grok-imagine-image-2.0 grok-imagine-image gemini-3.1-flash-image-preview nano-banana-2 gpt-image-2.5-sunburst gemini-nano-banana-2.1 gpt-image-2.5-flare gpt-image-2k nano-banana-pro gemini-3-pro-image-preview`

func TestReviewedCandidatesStayIsolatedAndUnavailable(t *testing.T) {
	models, err := Models()
	require.NoError(t, err)
	var actual []string
	components := 0
	for _, m := range models {
		actual = append(actual, m.UpstreamID)
		components += len(m.CostComponents)
		assert.Equal(t, "ys-"+m.UpstreamID, m.ID)
		assert.Equal(t, m.ID, m.Name)
		_, imageQualified := liveImages[m.ID]
		assert.Equal(t, imageQualified, m.ActivationReady)
		assert.Nil(t, m.CashCostCNY)
		assert.Nil(t, m.ContextWindow)
		assert.Nil(t, m.MaxOutputTokens)
		if !m.ActivationReady {
			assert.GreaterOrEqual(t, len(m.PendingReasons), 3)
		} else {
			assert.Equal(t, "image", m.Kind)
			assert.Equal(t, []string{"cash_procurement_conversion_unknown"}, m.PendingReasons)
		}
		assert.Equal(t, "unverified", m.CapabilityStatus["vision_input"])
		assert.Equal(t, "unverified", m.CapabilityStatus["tools"])
		_, rawExists := Lookup(m.UpstreamID)
		assert.False(t, rawExists, "raw supplier IDs must not alias old providers")
		_, retiredExists := Lookup("codebloom-" + m.UpstreamID)
		assert.False(t, retiredExists, "retired candidate IDs must not alias the public model")
		assert.False(t, PubliclyAvailable("codebloom-"+m.UpstreamID))
		assert.Equal(t, "account_credit_snapshot", m.CostBasis)
		assert.Equal(t, "CNY_ACCOUNT_CREDIT", m.Currency)
		if m.Kind == "text" {
			assert.Nil(t, m.RetailCNY, "text has a price vector, not one flat charge")
		} else {
			require.NotNil(t, m.RetailCNY)
		}
		for _, component := range m.CostComponents {
			require.NotNil(t, component.RetailCNY)
		}
	}
	assert.Equal(t, strings.Fields(expectedIDs), actual)
	assert.Len(t, models, 37)
	assert.Equal(t, 141, components)
	for _, excluded := range strings.Fields(`MiniMax-M3 MiniMax-M2.7 MiniMax-M2.7-highspeed mimo-v2.5 hy4 qwen3.8-max-0902 glm-5.2 glm-5.3 hy4-preview`) {
		_, ok := Lookup("ys-" + excluded)
		assert.False(t, ok, "excluded candidate: %s", excluded)
	}
}

func candidateCostComponent(t *testing.T, id, direction, tier string) CostComponent {
	t.Helper()
	m, ok := Lookup("ys-" + id)
	require.True(t, ok, "model missing: %s", id)
	index := slices.IndexFunc(m.CostComponents, func(cost CostComponent) bool {
		return cost.Direction == direction && cost.Params["pricing_tier"] == tier
	})
	// Flat components use an empty parameter map, not an invented empty tier.
	if tier == "" {
		index = slices.IndexFunc(m.CostComponents, func(cost CostComponent) bool {
			_, hasTier := cost.Params["pricing_tier"]
			return cost.Direction == direction && !hasTier
		})
	}
	require.GreaterOrEqual(t, index, 0, "component missing: %s/%s/%s", id, direction, tier)
	return m.CostComponents[index]
}

func TestAccountAndApprovedRetailMultipliersPreserveEveryConditionalCategory(t *testing.T) {
	for _, c := range []struct {
		id, direction, tier string
		cost, retail        float64
	}{
		{"gpt-6-sol", "input", "short", .5, .9},
		{"gpt-6.1-terra", "input", "short", .25, .45},
		{"gpt-6.1-terra", "output", "long", 1.875, 3.375},
		{"gpt-6.1-terra", "cache_write", "short", .3125, .5625},
		{"gpt-6.1-terra", "cache_write_12h", "short", .625, 1.125},
		{"gpt-6.1-terra", "cache_write_12h", "long", 1.25, 2.25},
		{"gpt-5.6-sol", "output", "short", 7.5, 13.5},
		{"gpt-5.6-sol", "output", "long", 11.25, 20.25},
		{"kimi-k3", "cache_write_1h", "", 20, 28},
		{"kimi-k3.1-pro", "cache_write_1h", "", 13, 18.2},
		{"qwen3.8-max", "input", "", 6, 8.4},
		{"qwen3.8-max", "cache_read", "", 7.5, 10.5},
		{"gpt-image-2.5-flare", "image", "", .075, .175},
		{"grok-imagine-image", "request", "", .1125, .2125},
	} {
		cost := candidateCostComponent(t, c.id, c.direction, c.tier)
		assert.InDelta(t, c.cost, cost.AccountCreditCNY, 1e-12, "%s/%s/%s cost", c.id, c.direction, c.tier)
		require.NotNil(t, cost.RetailCNY)
		assert.InDelta(t, c.retail, *cost.RetailCNY, 1e-12, "%s/%s/%s retail", c.id, c.direction, c.tier)
	}
	m, ok := Lookup("ys-gpt-6.1-terra")
	require.True(t, ok)
	condition, ok := m.CostConditions["context_pricing"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(272000), condition["threshold"])
	assert.Equal(t, false, condition["is_capacity_limit"])
	assert.Contains(t, m.BaseBillingExpression, "len < 272000")
	assert.Nil(t, m.ContextWindow)
	sol, ok := Lookup("ys-gpt-6.1-sol")
	require.True(t, ok)
	assert.False(t, slices.ContainsFunc(sol.CostComponents, func(cost CostComponent) bool {
		return cost.Direction == "cache_write_12h" && cost.Params["pricing_tier"] == "long"
	}), "unlisted long12h cache fee must remain unknown")
}

func TestDeepSeekPeakKeepsOriginalTimezoneWindowsAndBothPriceVectors(t *testing.T) {
	for _, raw := range []string{"deepseek-v4-flash", "deepseek-v4-flash-0731"} {
		m, ok := Lookup("ys-" + raw)
		require.True(t, ok)
		condition, ok := m.CostConditions["shanghai_weekday_peak"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Asia/Shanghai", condition["timezone"])
		assert.Equal(t, float64(1), condition["weekday_min_inclusive"])
		assert.Equal(t, float64(6), condition["weekday_max_exclusive"])
		assert.Equal(t, false, condition["holiday_exclusion"])
		assert.Equal(t, float64(2), condition["multiplier"])
		require.Len(t, condition["hour_windows"], 2)
		for _, direction := range []string{"input", "output", "cache_read"} {
			base := candidateCostComponent(t, raw, direction, "off_peak")
			peak := candidateCostComponent(t, raw, direction, "peak")
			assert.InDelta(t, base.AccountCreditCNY*2, peak.AccountCreditCNY, 1e-12)
			require.NotNil(t, base.RetailCNY)
			require.NotNil(t, peak.RetailCNY)
			assert.InDelta(t, *base.RetailCNY*2, *peak.RetailCNY, 1e-12)
		}
		assert.Contains(t, m.BaseBillingExpression, `hour("Asia/Shanghai") >= 9`)
		assert.Contains(t, m.BaseBillingExpression, `hour("Asia/Shanghai") < 18`)
	}
	ordinary, ok := Lookup("ys-deepseek-v4.1-flash")
	require.True(t, ok)
	assert.Empty(t, ordinary.CostConditions, "other variants must not inherit an unconfigured peak price")
}

func TestImageEvidenceNeverExpandsNameIntoParameters(t *testing.T) {
	m, ok := Lookup("ys-gpt-image-2k")
	require.True(t, ok)
	require.NotNil(t, m.ObservedOutput)
	assert.Equal(t, 1024, m.ObservedOutput.Width)
	assert.Equal(t, 1024, m.ObservedOutput.Height)
	assert.Equal(t, "1024x1024", m.VerifiedRequestOptions["size"])
	assert.Equal(t, float64(1), m.VerifiedRequestOptions["n"])
	chat, ok := Lookup("ys-grok-imagine-image")
	require.True(t, ok)
	require.NotNil(t, chat.ObservedOutput)
	assert.Equal(t, "chat_completions", chat.Transport)
	assert.Equal(t, 768, chat.ObservedOutput.Width)
	assert.Equal(t, 1152, chat.ObservedOutput.Height)
	assert.Equal(t, map[string]any{"stream": false}, chat.VerifiedRequestOptions)
	for _, capability := range []string{"reference_image_input", "image_edit", "async_polling"} {
		assert.Equal(t, "unverified", chat.CapabilityStatus[capability])
	}
	assert.Equal(t, "approved_image_cost_plus_0_10", chat.RetailPricingStatus)
	assert.NotContains(t, chat.PendingReasons, "retail_price_pending")
	assert.True(t, chat.ActivationReady, "this exact route has separate live generation, billing, decode and retained-handler proofs")
	assert.False(t, m.ActivationReady, "a different image's known price and synthetic handler fixture cannot qualify its route")
}

func TestSnapshotCopiesCannotMutateLaterRequests(t *testing.T) {
	first, err := Models()
	require.NoError(t, err)
	first[0].PendingReasons[0] = "changed"
	first[0].CapabilityStatus["vision_input"] = "verified"
	first[0].VerifiedRequestOptions["stream_options"].(map[string]any)["include_usage"] = false
	first[0].CostComponents[0].Params["injected"] = true
	*first[0].CostComponents[0].RetailCNY = 1000
	next, err := Models()
	require.NoError(t, err)
	assert.NotEqual(t, "changed", next[0].PendingReasons[0])
	assert.Equal(t, "unverified", next[0].CapabilityStatus["vision_input"])
	assert.Equal(t, true, next[0].VerifiedRequestOptions["stream_options"].(map[string]any)["include_usage"])
	assert.NotContains(t, next[0].CostComponents[0].Params, "injected")
	require.NotNil(t, next[0].CostComponents[0].RetailCNY)
	assert.NotEqual(t, 1000., *next[0].CostComponents[0].RetailCNY)
	m, ok := Lookup("ys-gpt-image-2k")
	require.True(t, ok)
	m.ObservedOutput.Width = 2048
	m.CapabilityStatus["image_edit"] = "verified"
	again, ok := Lookup(m.ID)
	require.True(t, ok)
	assert.Equal(t, 1024, again.ObservedOutput.Width)
	assert.Equal(t, "unverified", again.CapabilityStatus["image_edit"])
}

func TestInvalidCatalogFailsClosed(t *testing.T) {
	for name, mutate := range map[string]func(*catalog){
		"text_flat_retail":   func(c *catalog) { price := 1.; c.Models[0].RetailCNY = &price },
		"activation":         func(c *catalog) { c.Models[0].ActivationReady = true },
		"prefix":             func(c *catalog) { c.Models[0].ID = c.Models[0].UpstreamID },
		"display_name":       func(c *catalog) { c.Models[0].Name = c.Models[0].UpstreamID },
		"duplicate":          func(c *catalog) { c.Models[1] = c.Models[0] },
		"vision":             func(c *catalog) { c.Models[0].CapabilityStatus["vision_input"] = "verified" },
		"cash":               func(c *catalog) { cost := .1; c.Models[0].CashCostCNY = &cost },
		"capacity":           func(c *catalog) { cap := 272000; c.Models[0].ContextWindow = &cap },
		"account_multiplier": func(c *catalog) { c.Models[0].AccountMultiplier = .25 },
		"retail_multiplier":  func(c *catalog) { multiplier := .6; c.Models[0].RetailAccountMultiplier = &multiplier },
		"missing_retail":     func(c *catalog) { c.Models[0].CostComponents[0].RetailCNY = nil },
		"wrong_retail":       func(c *catalog) { retail := 9.; c.Models[0].CostComponents[0].RetailCNY = &retail },
		"direction":          func(c *catalog) { c.Models[0].CostComponents[0].Direction = "guessed" },
		"pending":            func(c *catalog) { c.Models[0].PendingReasons = slices.Delete(c.Models[0].PendingReasons, 0, 1) },
		"excluded":           func(c *catalog) { c.Excluded[0].ID = c.Models[0].UpstreamID },
	} {
		t.Run(name, func(t *testing.T) {
			var c catalog
			require.NoError(t, common.Unmarshal(catalogData, &c))
			mutate(&c)
			data, err := common.Marshal(c)
			require.NoError(t, err)
			_, err = loadCatalog(data)
			require.Error(t, err)
		})
	}
}
