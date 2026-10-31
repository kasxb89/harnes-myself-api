package ycapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Equal(t, 149, rows)
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
	assert.Equal(t, map[string]int{"text": 40, "image": 8, "video": 10, "audio": 3}, counts)
	assert.Equal(t, 106, specs)
}
func TestPreviewUsesNativeClipTotalAndPinnedLane(t *testing.T) {
	p, err := BuildPreview(PreviewRequest{ModelID: "ycapi-grok-imagine-video", SpecID: "ca70bc25c56f6312", Prompt: "Cup"})
	require.NoError(t, err)
	assert.Equal(t, "0.7275", p.Retail)
	assert.Equal(t, "5", p.Body["duration"])
	assert.Equal(t, "grok-imagine-video@ROU:default", p.Body["lane_id"])
}
func TestPreviewRejectsUnlistedOrUnsafeInputs(t *testing.T) {
	neg, huge, zero, allowed := -1, 32001, 0, 8192
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
