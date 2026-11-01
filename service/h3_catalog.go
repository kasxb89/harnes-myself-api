package service

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

func isMetasoH3BaseURL(value string) bool {
	base, err := url.Parse(value)
	return err == nil && base.Scheme == "https" && base.User == nil && strings.EqualFold(base.Hostname(), "metaso.cn") && (base.Port() == "" || base.Port() == "443") && strings.TrimSuffix(base.Path, "/") == "/api/minimax" && base.RawQuery == "" && base.Fragment == "" && base.RawPath == ""
}

// NarrowH3PricingParameters publishes the intersection of the caller's actual
// enabled routes. Unknown routing or pricing retains the legacy conservative
// schema. Shared pricing cache values are never changed, and channel secrets
// are neither selected nor needed.
func NarrowH3PricingParameters(ctx context.Context, pricing []model.Pricing, groups []string) []model.Pricing {
	index := slices.IndexFunc(pricing, func(row model.Pricing) bool { return row.ModelName == "MiniMax-H3" && row.Parameters != nil })
	if index < 0 || model.DB == nil || len(groups) == 0 {
		return pricing
	}
	generation := jsplugin.DefaultRegistry.Generation()
	plugin, ok := generation.Get("hailuo")
	if !ok || plugin.Meta.Version != "1.3.0" {
		return pricing
	}
	expression, configured := billing_setting.ResolveTaskBillingExpr("hailuo", "MiniMax-H3", "MiniMax-H3")
	if !configured || !billingexpr.UsedUsageKeys(expression)["long_video"] {
		return pricing
	}
	abilities, err := model.GetVideoQuoteAbilities(ctx, groups, []string{"MiniMax-H3"})
	if err != nil || len(abilities) == 0 {
		return pricing
	}
	ids := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		ids = append(ids, ability.ChannelId)
	}
	var channels []model.Channel
	if model.DB.WithContext(ctx).Select("id", "type", "status", "models", "base_url", "model_mapping", "setting", "param_override", "header_override").Where("id IN ?", ids).Find(&channels).Error != nil {
		return pricing
	}
	found := false
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		found = true
		if channel.BaseURL == nil || !isMetasoH3BaseURL(*channel.BaseURL) {
			return pricing
		}
		mapped, mapErr := videoQuoteMappedModel("MiniMax-H3", channel.GetModelMapping())
		if mapErr != nil || mapped != "MiniMax-H3" {
			return pricing
		}
		for _, override := range []*string{channel.ParamOverride, channel.HeaderOverride} {
			if override != nil && strings.TrimSpace(*override) != "" && strings.TrimSpace(*override) != "{}" && strings.TrimSpace(*override) != "null" {
				return pricing
			}
		}
		bound := false
		if channel.Type == constant.ChannelTypeMiniMax {
			owner, _ := generation.GetByChannelType(channel.Type)
			bound = owner == plugin
		} else if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
			var settings kitdto.ChannelSettings
			if channel.Setting != nil && *channel.Setting != "" && common.UnmarshalJsonStr(*channel.Setting, &settings) != nil {
				return pricing
			}
			bound = settings.BindsTaskPlugin("hailuo")
		}
		if !bound {
			return pricing
		}
	}
	if !found {
		return pricing
	}
	// Verify the currently loaded protocol binding too, rather than relying on
	// a same-named row in the catalog or a version in stored configuration.
	bound := false
	for _, candidate := range generation.LookupEndpointCandidates(http.MethodPost, "/v1/videos", "MiniMax-H3") {
		if candidate.Plugin == plugin {
			bound = true
		}
	}
	if !bound {
		return pricing
	}
	original, ok := pricing[index].Parameters["properties"].(map[string]any)
	if !ok {
		return pricing
	}
	result := slices.Clone(pricing)
	parameters := maps.Clone(pricing[index].Parameters)
	properties := maps.Clone(original)
	var seconds []int
	for value := 4; value <= 30; value++ {
		seconds = append(seconds, value)
	}
	properties["seconds"] = map[string]any{"type": "integer", "minimum": 4, "maximum": 30, "enum": seconds, "default": 5}
	properties["resolution"] = map[string]any{"type": "string", "enum": []string{"480P", "768P", "1080P", "2K"}, "default": "768P"}
	properties["context_ir_enabled"] = map[string]any{"type": "boolean", "enum": []bool{false}, "default": false}
	properties["fast_mode"] = map[string]any{"type": "boolean", "enum": []bool{false}, "default": false}
	var combinations []any
	for _, resolution := range []string{"768P", "480P", "1080P", "2K"} {
		maximum := 30
		if resolution == "2K" {
			maximum = 15
		}
		allowed := seconds[:maximum-3]
		branch := map[string]any{"properties": map[string]any{"resolution": map[string]any{"enum": []string{resolution}}, "seconds": map[string]any{"enum": allowed, "minimum": 4, "maximum": maximum}}}
		if resolution != "768P" {
			branch["required"] = []string{"resolution"}
		}
		combinations = append(combinations, branch)
	}
	parameters["properties"] = properties
	parameters["oneOf"] = combinations
	parameters["x-reference-input-max-seconds"] = map[string]int{"video": 15, "audio": 15}
	result[index].Parameters = parameters
	return result
}
