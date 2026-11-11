package model

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStandardTokenCostDoesNotInventAnUnknownCacheTariff(t *testing.T) {
	rule := ModelProfitCostRule{ChannelID: 24, ModelName: "qualified-standard-text", EffectiveFrom: 1, Kind: "token", InputCostPerMillionCNY: common.GetPointer(2.0), OutputCostPerMillionCNY: common.GetPointer(10.0), Source: "Verified standard non-cache quote"}
	require.NoError(t, ValidateModelProfitCostRules(ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{rule}}))
	log := Log{PromptTokens: 1000, CompletionTokens: 100}
	cost, known := modelProfitCost(rule, log, profitLogFacts{CacheTokens: common.GetPointer(0.0)})
	assert.True(t, known)
	assert.InDelta(t, .003, cost, 1e-12)
	_, known = modelProfitCost(rule, log, profitLogFacts{CacheTokens: common.GetPointer(100.0)})
	assert.False(t, known)
	_, known = modelProfitCost(rule, log, profitLogFacts{})
	assert.False(t, known)
	rule.CacheCostPerMillionCNY = common.GetPointer(.2)
	cost, known = modelProfitCost(rule, log, profitLogFacts{CacheTokens: common.GetPointer(100.0)})
	assert.True(t, known)
	assert.InDelta(t, .00282, cost, 1e-12)
}

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

func TestProfitH3LongVideoRuleAndFacts(t *testing.T) {
	body := `{"schema_version":1,"revision":0,"rules":[{"channel_id":16,"model_name":"MiniMax-H3","effective_from":200,"kind":"h3_video","resolution_costs_cny":{"480P":0.06000236,"768P":0.0897396,"1080P":0.13997618,"2K":0.149566},"extra_image_cost_cny":0.0501486,"free_input_images":5,"long_video_multiplier":2,"source":"Metaso public reference estimate"}]}`
	config, err := ParseModelProfitCostRules([]byte(body))
	require.NoError(t, err)
	for _, value := range []string{"null", "1", "3", "-2", `"2"`} {
		_, err := ParseModelProfitCostRules([]byte(strings.Replace(body, `"long_video_multiplier":2`, `"long_video_multiplier":`+value, 1)))
		assert.ErrorIs(t, err, ErrModelProfitParameters)
	}
	for _, tc := range []struct {
		name, resolution string
		seconds          float64
		long             any
		cost             float64
		known            bool
	}{
		{"long768", "768P", 30, true, 8.1768612, true},
		{"long1080", "1080P", 30, true, 12.6981534, true},
		{"long480", "480P", 30, true, 5.5005096, true},
		{"measured short keeps requested long band", "768P", 14, true, 5.305194, true},
		{"2K cannot be long", "2K", 16, true, 0, false},
		{"missing band is not inferred", "768P", 30, nil, 0, false},
		{"contradictory band", "768P", 30, false, 0, false},
		{"unbounded", "768P", 31, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := map[string]any{"resolution": tc.resolution, "seconds": tc.seconds, "input_video_seconds": float64(15), "input_images": float64(6)}
			if tc.long != nil {
				facts["long_video"] = tc.long
			}
			cost, known := modelProfitCost(config.Rules[0], Log{}, profitLogFacts{UsageFacts: facts})
			assert.Equal(t, tc.known, known)
			assert.InDelta(t, tc.cost, cost, 1e-8)
		})
	}
}
