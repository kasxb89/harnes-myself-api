package service

import (
	"errors"
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/ycapi"
)

// ModelLimitsVersionHeader opts catalogs into separate output ceilings and defaults.
const ModelLimitsVersionHeader = "X-Yunshu-Model-Limits-Version"

// CatalogModelCapacity keeps older clients on the safe default output budget.
func CatalogModelCapacity(capacity model.EffectiveModelCapacity, separate bool) model.EffectiveModelCapacity {
	if capacity.MaxOutputTokens == nil {
		return capacity
	}
	if capacity.DefaultOutputTokens == nil {
		capacity.DefaultOutputTokens = capacity.MaxOutputTokens
	}
	if !separate {
		capacity.MaxOutputTokens = capacity.DefaultOutputTokens
		capacity.DefaultOutputTokens = nil
	}
	return capacity
}

// WithEffectiveTextCatalogCapacity projects the same route limits used by the
// relay into the YC schema, without changing a cached parameter map. The input
// field is the total context's mathematical upper bound for old schema2 clients,
// not a separately measured supplier input maximum.
func WithEffectiveTextCatalogCapacity(id string, parameters map[string]any, capacity model.EffectiveModelCapacity) (map[string]any, error) {
	approved, ok := ycapi.Lookup(id)
	fallbackOutput := 8192 // User-approved product default; never a supplier maximum.
	codeBloomText := false
	if !ok || approved.Kind != "text" {
		candidate, found := codebloom.Lookup(id)
		if !found || candidate.Kind != "text" || !candidate.ActivationReady {
			return parameters, nil
		}
		codeBloomText = true
	} else {
		fallbackOutput = approved.MaxOutputTokens
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
	outputDefault := capacity.DefaultOutputTokens
	if outputDefault == nil {
		outputDefault = capacity.MaxOutputTokens
	}
	text["default_output_tokens"] = *outputDefault
	text["default_output_tokens_source"] = "gateway_policy"
	text["fallback_output_tokens"] = fallbackOutput
	text["fallback_output_tokens_source"] = "user_approved_product_budget"
	text["output_limit_source"] = "gateway_policy"
	text["native_max_output_tokens"] = nil
	if codeBloomText {
		text["context_window_source"] = "gateway_policy"
		text["native_context_window"] = nil
	}
	copy["x-text-capabilities"] = text
	return copy, nil
}

// ProjectPricingModelCapacities returns per-request values. model.GetPricing is
// shared across users, so neither its slice nor its parameter maps are edited.
func ProjectPricingModelCapacities(pricing []model.Pricing, groups []string, separateOutput bool) ([]model.Pricing, error) {
	result := make([]model.Pricing, 0, len(pricing))
	names := make([]string, 0, len(pricing))
	for _, item := range pricing {
		names = append(names, item.ModelName)
	}
	capacities, err := ModelCapacitiesForGroups(names, groups)
	if err != nil {
		return nil, err
	}
	for _, item := range pricing {
		capacity := CatalogModelCapacity(capacities[item.ModelName], separateOutput)
		if capacity.Managed && len(capacity.Routes) == 0 {
			continue
		}
		if capacity.ContextWindow != nil {
			item.ContextWindow = common.GetPointer(*capacity.ContextWindow)
		}
		if capacity.MaxOutputTokens != nil {
			item.MaxOutputTokens = common.GetPointer(*capacity.MaxOutputTokens)
		}
		item.DefaultOutputTokens = capacity.DefaultOutputTokens
		if approved, ok := ycapi.Lookup(item.ModelName); ok {
			if approved.Kind == "text" {
				item.Parameters, err = WithEffectiveTextCatalogCapacity(item.ModelName, item.Parameters, capacity)
				if err != nil {
					return nil, err
				}
			} else {
				item.Parameters = model.WithMediaClientConcurrencyHint(item.Parameters, "ycapi", item.ModelName, approved.Kind)
			}
		} else if candidate, ok := codebloom.Lookup(item.ModelName); ok {
			if candidate.Kind == "text" {
				item.Parameters, err = WithEffectiveTextCatalogCapacity(item.ModelName, item.Parameters, capacity)
				if err != nil {
					return nil, err
				}
			} else if candidate.Kind == "image" {
				item.Parameters = model.WithMediaClientConcurrencyHint(item.Parameters, "codebloom-image", item.ModelName, candidate.Kind)
			}
		}
		result = append(result, item)
	}
	return result, nil
}
