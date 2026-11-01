package service

import (
	"errors"
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
)

// WithEffectiveTextCatalogCapacity projects the same route limits used by the
// relay into the YC schema, without changing a cached parameter map. The input
// field is the total context's mathematical upper bound for old schema2 clients,
// not a separately measured supplier input maximum.
func WithEffectiveTextCatalogCapacity(id string, parameters map[string]any, capacity model.EffectiveModelCapacity) (map[string]any, error) {
	approved, ok := ycapi.Lookup(id)
	if !ok || approved.Kind != "text" {
		return parameters, nil
	}
	if capacity.ContextWindow == nil || *capacity.ContextWindow <= 0 || capacity.MaxOutputTokens == nil || *capacity.MaxOutputTokens <= 0 {
		return nil, errors.New("effective text capacity is unavailable")
	}
	text, ok := parameters["x-text-capabilities"].(map[string]any)
	if !ok {
		return nil, errors.New("text capability configuration is unavailable")
	}
	copy := maps.Clone(parameters)
	text = maps.Clone(text)
	text["context_window"] = *capacity.ContextWindow
	text["max_input_tokens"] = *capacity.ContextWindow
	text["input_limit_source"] = "context_window_bound"
	text["native_max_input_tokens"] = nil
	text["max_output_tokens"] = *capacity.MaxOutputTokens
	text["default_output_tokens"] = *capacity.MaxOutputTokens
	text["default_output_tokens_source"] = "gateway_policy"
	text["fallback_output_tokens"] = approved.MaxOutputTokens
	text["fallback_output_tokens_source"] = "user_approved_product_budget"
	text["output_limit_source"] = "gateway_policy"
	text["native_max_output_tokens"] = nil
	copy["x-text-capabilities"] = text
	return copy, nil
}

// ProjectPricingModelCapacities returns per-request values. model.GetPricing is
// shared across users, so neither its slice nor its parameter maps are edited.
func ProjectPricingModelCapacities(pricing []model.Pricing, groups []string) ([]model.Pricing, error) {
	result := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		capacity, err := ModelCapacityForGroups(item.ModelName, groups)
		if err != nil {
			return nil, err
		}
		if capacity.Managed && len(capacity.Routes) == 0 {
			continue
		}
		if capacity.ContextWindow != nil {
			item.ContextWindow = common.GetPointer(*capacity.ContextWindow)
		}
		if capacity.MaxOutputTokens != nil {
			item.MaxOutputTokens = common.GetPointer(*capacity.MaxOutputTokens)
		}
		if approved, ok := ycapi.Lookup(item.ModelName); ok {
			if approved.Kind == "text" {
				item.Parameters, err = WithEffectiveTextCatalogCapacity(item.ModelName, item.Parameters, capacity)
				if err != nil {
					return nil, err
				}
			} else {
				item.Parameters = model.WithMediaClientConcurrencyHint(item.Parameters, "ycapi", item.ModelName, approved.Kind)
			}
		}
		result = append(result, item)
	}
	return result, nil
}
