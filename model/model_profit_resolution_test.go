package model

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfitImageResolutionRuleValidation(t *testing.T) {
	for _, tc := range []struct {
		name, prices string
		valid        bool
	}{
		{"three tariffs", `{"1k":0.1,"2k":0.15,"4k":0.2}`, true},
		{"explicit zero", `{"1k":0,"2k":0.15,"4k":0.2}`, true},
		{"missing tier", `{"1k":0.1,"2k":0.15}`, false},
		{"extra tier", `{"1k":0.1,"2k":0.15,"4k":0.2,"8k":1}`, false},
		{"noncanonical tier", `{"1K":0.1,"2k":0.15,"4k":0.2}`, false},
		{"null price", `{"1k":null,"2k":0.15,"4k":0.2}`, false},
		{"negative price", `{"1k":-0.1,"2k":0.15,"4k":0.2}`, false},
		{"unbounded price", `{"1k":1e300,"2k":0.15,"4k":0.2}`, false},
		{"string price", `{"1k":"0.1","2k":0.15,"4k":0.2}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"schema_version":1,"revision":0,"rules":[{"channel_id":18,"model_name":"image","effective_from":0,"kind":"image_resolution","resolution_costs_cny":%s,"source":"user supplier quote"}]}`, tc.prices)
			_, err := ParseModelProfitCostRules([]byte(body))
			if tc.valid {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrModelProfitParameters)
			}
		})
	}
	rule := ModelProfitCostRule{ChannelID: 18, ModelName: "image", EffectiveFrom: 0, Kind: "image_resolution", ResolutionCostsCNY: map[string]float64{"1k": .1, "2k": .15, "4k": .2}, Source: "supplier quote"}
	rule.ResolutionCostsCNY["1k"] = math.Inf(1)
	assert.ErrorIs(t, ValidateModelProfitCostRules(ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{rule}}), ErrModelProfitParameters)
	rule.ResolutionCostsCNY["1k"] = math.NaN()
	assert.ErrorIs(t, ValidateModelProfitCostRules(ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{rule}}), ErrModelProfitParameters)
}

func TestProfitImageResolutionFacts(t *testing.T) {
	rule := ModelProfitCostRule{Kind: "image_resolution", ResolutionCostsCNY: map[string]float64{"1k": .1, "2k": .15, "4k": .2}}
	for _, tc := range []struct {
		name  string
		facts map[string]any
		cost  float64
		known bool
	}{
		{"1K", map[string]any{"image_count": float64(1), "resolution": "1k"}, .1, true},
		{"2K", map[string]any{"image_count": float64(1), "resolution": "2k"}, .15, true},
		{"4K", map[string]any{"image_count": float64(1), "resolution": "4k"}, .2, true},
		{"missing resolution", map[string]any{"image_count": float64(1)}, 0, false},
		{"unsupported resolution", map[string]any{"image_count": float64(1), "resolution": "8k"}, 0, false},
		{"missing quantity", map[string]any{"resolution": "2k"}, 0, false},
		{"fractional quantity", map[string]any{"image_count": 1.5, "resolution": "2k"}, 0, false},
		{"zero quantity", map[string]any{"image_count": float64(0), "resolution": "2k"}, 0, false},
		{"unbounded quantity", map[string]any{"image_count": math.Inf(1), "resolution": "2k"}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, known := modelProfitCost(rule, Log{}, profitLogFacts{UsageFacts: tc.facts})
			assert.Equal(t, tc.known, known)
			assert.InDelta(t, tc.cost, cost, 1e-10)
		})
	}
}
