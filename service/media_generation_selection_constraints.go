package service

import (
	"errors"
	"math"
	"reflect"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

// The administrator's finite alternatives remain private authority. Catalogs
// expose only these non-executable selection predicates, never JSON Schema
// combinators or copies of native upload/transport declarations.
func mediaGenerationCatalogSelection(schema map[string]any) (map[string]any, map[string]any, error) {
	raw, err := common.Marshal(schema)
	if err != nil {
		return nil, nil, err
	}
	var flat map[string]any
	if common.Unmarshal(raw, &flat) != nil || flat == nil {
		return nil, nil, errors.New("invalid media selection schema")
	}
	alternatives, explicit := flat["oneOf"]
	delete(flat, "oneOf")
	if mediaSelectionHasCombinator(flat) {
		return nil, nil, errors.New("unsupported media selection combinator")
	}
	if !explicit {
		return flat, nil, nil
	}
	branches, valid := alternatives.([]any)
	properties, fieldsValid := flat["properties"].(map[string]any)
	if !valid || len(branches) == 0 || len(branches) > 128 || !fieldsValid {
		return nil, nil, errors.New("invalid media selection combinations")
	}
	assets := map[string]bool{}
	for _, rawAsset := range anySlice(flat["x-media-assets"]) {
		asset, _ := rawAsset.(map[string]any)
		name, _ := asset["name"].(string)
		assets[name] = name != ""
	}
	combinations := []any{}
	for _, rawBranch := range branches {
		branch, valid := rawBranch.(map[string]any)
		if !valid || mediaSelectionHasCombinator(branch) {
			return nil, nil, errors.New("unsupported media selection branch")
		}
		for key := range branch {
			if key != "type" && key != "properties" && key != "required" {
				return nil, nil, errors.New("unsupported media selection branch constraint")
			}
		}
		if kind, present := branch["type"]; present && kind != "object" {
			return nil, nil, errors.New("invalid media selection branch type")
		}
		fields, valid := branch["properties"].(map[string]any)
		if !valid || len(fields) > 64 {
			return nil, nil, errors.New("invalid media selection fields")
		}
		predicates := map[string]any{}
		for name, value := range fields {
			base, declared := properties[name].(map[string]any)
			if !declared {
				return nil, nil, errors.New("undeclared media selection field")
			}
			if value == false {
				predicates[name] = map[string]any{"disabled": true}
				continue
			}
			field, valid := value.(map[string]any)
			if !valid {
				return nil, nil, errors.New("invalid media selection predicate")
			}
			predicate, err := mediaSelectionPredicate(base, field, assets[name])
			if err != nil {
				return nil, nil, err
			}
			if len(predicate) > 0 {
				predicates[name] = predicate
			}
		}
		required := []any{}
		if rawRequired, present := branch["required"]; present {
			list, valid := rawRequired.([]any)
			if !valid || len(list) > 64 {
				return nil, nil, errors.New("invalid media selection required fields")
			}
			for _, rawName := range list {
				name, valid := rawName.(string)
				_, declared := properties[name]
				if !valid || !declared || fields[name] == false || slices.Contains(required, rawName) {
					return nil, nil, errors.New("invalid media selection required field")
				}
				required = append(required, name)
			}
		}
		combination := map[string]any{"parameters": predicates}
		if len(required) > 0 {
			combination["required_parameters"] = required
		}
		combinations = append(combinations, combination)
	}
	selection := map[string]any{"schema_version": 1, "combinations": combinations}
	encoded, err := common.Marshal(selection)
	if err != nil || len(encoded) > 262144 {
		return nil, nil, errors.New("media selection combinations exceed catalog limit")
	}
	return flat, selection, nil
}

func mediaSelectionPredicate(base, field map[string]any, asset bool) (map[string]any, error) {
	kind, valid := base["type"].(string)
	if !valid || field["type"] != kind {
		return nil, errors.New("invalid media selection field type")
	}
	predicate := map[string]any{}
	for key, value := range field {
		switch key {
		case "type", "default": // Defaults are not branch predicates.
		case "description", "x-ycapi-contract", "x-media-native-shape":
			if !reflect.DeepEqual(base[key], value) {
				return nil, errors.New("unsupported media selection metadata override")
			}
		case "items":
			items, valid := value.(map[string]any)
			if !asset || kind != "array" || !valid || len(items) != 1 || items["type"] != "string" || !reflect.DeepEqual(base[key], value) {
				return nil, errors.New("unsupported media selection item constraint")
			}
		case "enum", "const":
			if kind == "array" {
				return nil, errors.New("asset values cannot be published as selection constraints")
			}
			values := []any{value}
			if key == "enum" {
				values, valid = value.([]any)
				if !valid {
					return nil, errors.New("invalid media selection allowed values")
				}
			}
			if len(values) == 0 || len(values) > 128 {
				return nil, errors.New("invalid media selection allowed values")
			}
			for i, value := range values {
				if !mediaSelectionScalar(kind, value) || slices.ContainsFunc(values[:i], func(prior any) bool { return reflect.DeepEqual(prior, value) }) {
					return nil, errors.New("invalid media selection scalar")
				}
			}
			if prior, present := predicate["allowed_values"].([]any); present {
				values = slices.DeleteFunc(slices.Clone(values), func(value any) bool {
					return !slices.ContainsFunc(prior, func(other any) bool { return reflect.DeepEqual(value, other) })
				})
				if len(values) == 0 {
					return nil, errors.New("conflicting media selection values")
				}
			}
			predicate["allowed_values"] = values
		case "minimum", "maximum", "minItems", "maxItems":
			number, valid := value.(float64)
			if !valid || math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, errors.New("invalid media selection bound")
			}
			if key == "minItems" || key == "maxItems" {
				if !asset || kind != "array" || number < 0 || math.Trunc(number) != number {
					return nil, errors.New("invalid media selection asset bound")
				}
				key = map[string]string{"minItems": "min_items", "maxItems": "max_items"}[key]
			} else if kind != "integer" && kind != "number" {
				return nil, errors.New("invalid media selection numeric bound")
			}
			predicate[key] = value
		default:
			return nil, errors.New("unsupported media selection field constraint")
		}
	}
	for _, pair := range [][2]string{{"minimum", "maximum"}, {"min_items", "max_items"}} {
		lower, hasLower := predicate[pair[0]].(float64)
		upper, hasUpper := predicate[pair[1]].(float64)
		if hasLower && hasUpper && lower > upper {
			return nil, errors.New("invalid media selection bound order")
		}
	}
	return predicate, nil
}

func mediaSelectionScalar(kind string, value any) bool {
	switch kind {
	case "string":
		_, valid := value.(string)
		return valid
	case "boolean":
		_, valid := value.(bool)
		return valid
	case "number", "integer":
		number, valid := value.(float64)
		return valid && !math.IsNaN(number) && !math.IsInf(number, 0) && (kind != "integer" || math.Trunc(number) == number)
	}
	return false
}

func mediaSelectionHasCombinator(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "oneOf" || key == "allOf" || key == "anyOf" || mediaSelectionHasCombinator(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(value, mediaSelectionHasCombinator)
	}
	return false
}
