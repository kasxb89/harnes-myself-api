package model

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Only generation controls are editable. Transport, credentials, task recovery,
// upload URLs and plugin identity remain declarations made by the host.
var modelParameterTypes = map[string]string{
	"seconds": "integer", "duration": "integer", "resolution": "string", "ratio": "string", "aspect_ratio": "string",
	"size": "string", "quality": "string", "mode": "string", "n": "integer", "count": "integer", "image_count": "integer",
	"reference_images": "array", "reference_videos": "array", "reference_audios": "array", "frame_images": "array",
	"input_images": "array", "images": "array", "generate_audio": "boolean", "audio": "boolean", "audio_output": "boolean",
	"watermark": "boolean", "seed": "integer", "background": "string", "format": "string", "output_format": "string",
	"moderation": "string", "response_format": "string", "mix_frames_and_references": "boolean",
}

// ValidateModelParameterSchema accepts the finite JSON-schema vocabulary used
// by the existing client and native tools, rather than executable provider data.
func ValidateModelParameterSchema(kind string, schema map[string]any) error {
	if schema == nil {
		return nil
	}
	if kind != "image" && kind != "video" {
		return errors.New("Generation parameters require an image or video model")
	}
	encoded, err := common.Marshal(schema)
	if err != nil || len(encoded) > 64<<10 {
		return errors.New("Model parameter declaration is too large")
	}
	return validateModelParameterObject(schema, false)
}

func validateModelParameterObject(schema map[string]any, branch bool) error {
	for key := range schema {
		if key != "type" && key != "properties" && key != "required" && (key != "oneOf" || branch) {
			return errors.New("Only generation parameter fields and specification combinations can be configured")
		}
	}
	if value, present := schema["type"]; present && value != "object" {
		return errors.New("Model parameters must describe an object")
	}
	fields, ok := schema["properties"].(map[string]any)
	if !ok || len(fields) == 0 || len(fields) > len(modelParameterTypes) {
		return errors.New("Model parameters require supported generation fields")
	}
	for name, value := range fields {
		expected, exists := modelParameterTypes[name]
		if !exists {
			return fmt.Errorf("Unsupported generation parameter: %s", name)
		}
		if disabled, boolean := value.(bool); boolean {
			if disabled {
				return errors.New("An enabled generation control requires a typed declaration")
			}
			continue
		}
		field, ok := value.(map[string]any)
		if !ok || field["type"] != expected {
			return fmt.Errorf("Invalid generation parameter type: %s", name)
		}
		for key := range field {
			if !slices.Contains([]string{"type", "enum", "const", "default", "minimum", "maximum", "minItems", "maxItems", "items"}, key) {
				return errors.New("Unsupported generation parameter constraint")
			}
		}
		if expected == "array" {
			items, valid := field["items"].(map[string]any)
			if !valid || len(items) != 1 || items["type"] != "string" {
				return errors.New("Reference controls must contain string references")
			}
		}
		for _, bound := range []string{"minimum", "maximum", "minItems", "maxItems"} {
			if value, exists := field[bound]; exists {
				number, valid := value.(float64)
				if !valid || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
					return errors.New("Generation parameter bounds must be finite integers")
				}
				maximum := float64(math.MaxInt32)
				minimum := float64(0)
				if expected == "array" || name == "n" || name == "count" || name == "image_count" {
					maximum = dto.MaxImageN
				} else if name == "seconds" || name == "duration" {
					// The task host separately checks duration-auto capability.
					minimum, maximum = -1, common.MaxTaskDurationSeconds
				} else if name == "seed" {
					minimum = -1
				}
				if number < minimum || number > maximum {
					return errors.New("Generation parameter bound exceeds the gateway limit")
				}
			}
		}
		for _, pair := range [][2]string{{"minimum", "maximum"}, {"minItems", "maxItems"}} {
			lower, hasLower := field[pair[0]].(float64)
			upper, hasUpper := field[pair[1]].(float64)
			if hasLower && hasUpper && lower > upper {
				return errors.New("Generation parameter minimum exceeds its maximum")
			}
		}
		if values, exists := field["enum"]; exists {
			menu, valid := values.([]any)
			if !valid || len(menu) == 0 || len(menu) > 256 {
				return errors.New("Generation parameter choices must be a nonempty finite menu")
			}
			seen := make(map[string]bool)
			for _, option := range menu {
				if !modelParameterValueMatches(name, field, option, false) {
					return errors.New("Invalid generation parameter choice")
				}
				encoded, _ := common.Marshal(option)
				if seen[string(encoded)] {
					return errors.New("Duplicate generation parameter choice")
				}
				seen[string(encoded)] = true
			}
		}
		for _, key := range []string{"const", "default"} {
			if value, exists := field[key]; exists && !modelParameterValueMatches(name, field, value, true) {
				return errors.New("Generation parameter default or constant is outside its allowed values")
			}
		}
	}
	if required, exists := schema["required"]; exists {
		values, valid := required.([]any)
		if !valid || len(values) > len(fields) {
			return errors.New("Invalid required generation parameters")
		}
		seen := make(map[string]bool)
		for _, value := range values {
			name, stringValue := value.(string)
			_, exists := fields[name]
			if !stringValue || !exists || seen[name] || fields[name] == false {
				return errors.New("Invalid required generation parameter")
			}
			seen[name] = true
		}
	}
	if alternatives, exists := schema["oneOf"]; exists {
		values, valid := alternatives.([]any)
		if !valid || len(values) == 0 || len(values) > 128 {
			return errors.New("Generation specification combinations must be a finite nonempty list")
		}
		seen := make(map[string]bool)
		for _, value := range values {
			alternative, valid := value.(map[string]any)
			if !valid {
				return errors.New("Invalid generation specification combination")
			}
			if err := validateModelParameterObject(alternative, true); err != nil {
				return err
			}
			for name := range alternative["properties"].(map[string]any) {
				if _, exists := fields[name]; !exists {
					return errors.New("Specification combination uses an undeclared parameter")
				}
			}
			encoded, _ := common.Marshal(alternative)
			if seen[string(encoded)] {
				return errors.New("Duplicate generation specification combination")
			}
			seen[string(encoded)] = true
		}
	}
	return nil
}

func modelParameterValueMatches(name string, field map[string]any, value any, menu bool) bool {
	switch field["type"] {
	case "integer":
		number, valid := value.(float64)
		if !valid || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
			return false
		}
		if name == "n" || name == "count" || name == "image_count" {
			if number < 1 || number > dto.MaxImageN {
				return false
			}
		} else if (name == "seconds" || name == "duration") && (number < -1 || number > common.MaxTaskDurationSeconds) {
			return false
		}
		if minimum, exists := field["minimum"].(float64); exists && number < minimum {
			return false
		}
		if maximum, exists := field["maximum"].(float64); exists && number > maximum {
			return false
		}
	case "string":
		text, valid := value.(string)
		if !valid || text == "" || text != strings.TrimSpace(text) || len(text) > 128 || strings.ContainsFunc(text, unicode.IsControl) {
			return false
		}
	case "boolean":
		if _, valid := value.(bool); !valid {
			return false
		}
	case "array":
		values, valid := value.([]any)
		if !valid || len(values) > dto.MaxImageN {
			return false
		}
		if minimum, exists := field["minItems"].(float64); exists && float64(len(values)) < minimum {
			return false
		}
		if maximum, exists := field["maxItems"].(float64); exists && float64(len(values)) > maximum {
			return false
		}
	default:
		return false
	}
	if !menu {
		return true
	}
	if constant, exists := field["const"]; exists && !reflect.DeepEqual(value, constant) {
		return false
	}
	if values, exists := field["enum"].([]any); exists && !slices.ContainsFunc(values, func(option any) bool { return reflect.DeepEqual(value, option) }) {
		return false
	}
	return true
}

func modelParameterAliases(name string) []string {
	switch name {
	case "seconds", "duration":
		return []string{"seconds", "duration"}
	case "ratio", "aspect_ratio":
		return []string{"ratio", "aspect_ratio", "metaso_ratio"}
	case "resolution":
		return []string{"resolution", "metaso_resolution"}
	case "reference_images":
		return []string{"reference_images", "reference_image", "input_images"}
	case "reference_videos":
		return []string{"reference_videos", "reference_video", "video_reference"}
	case "reference_audios":
		return []string{"reference_audios", "reference_audio", "audio_reference"}
	case "n", "count", "image_count":
		return []string{"n", "count", "image_count"}
	default:
		return []string{name}
	}
}

// ModelParameterValues validates only explicitly declared controls. It never
// rewrites a request or changes an accepted task's defaults or quantities.
func ModelParameterValues(schema map[string]any, request map[string]any) error {
	if schema == nil {
		return nil
	}
	fields, _ := schema["properties"].(map[string]any)
	values := make(map[string]any)
	for name := range fields {
		for _, object := range []map[string]any{request, objectParameterMap(request["parameters"]), objectParameterMap(request["metadata"])} {
			for _, alias := range modelParameterAliases(name) {
				value, exists := object[alias]
				if modelParameterTypes[name] == "array" {
					if scalar, ok := value.(string); ok {
						value = []any{scalar}
					}
				}
				if !exists {
					continue
				}
				if modelParameterTypes[name] == "integer" {
					if text, ok := value.(string); ok {
						parsed, err := strconv.ParseFloat(text, 64)
						if err != nil {
							return errors.New("Invalid generation parameter value")
						}
						value = parsed
					}
					// Existing OpenAI Images n=0 means one image.
					if (name == "n" || name == "count" || name == "image_count") && value == float64(0) {
						value = float64(1)
					}
				}
				if old, present := values[name]; present && !reflect.DeepEqual(old, value) {
					return errors.New("Conflicting generation parameter aliases")
				}
				values[name] = value
			}
		}
	}
	typed := map[string][]any{}
	for _, object := range []map[string]any{request, objectParameterMap(request["metadata"])} {
		for _, key := range []string{"content", "metaso_content"} {
			items, _ := object[key].([]any)
			for _, raw := range items {
				item, _ := raw.(map[string]any)
				kind, _ := item["type"].(string)
				role, _ := item["role"].(string)
				field := ""
				switch kind {
				case "image_url":
					if role == "first_frame" || role == "last_frame" || role == "middle_frame" {
						field = "frame_images"
					} else if role == "reference_image" {
						field = "reference_images"
					}
				case "video_url":
					field = "reference_videos"
				case "audio_url":
					field = "reference_audios"
				}
				if field != "" {
					typed[field] = append(typed[field], raw)
				}
			}
		}
		for _, key := range []string{"first_frame_image", "last_frame_image"} {
			if value, ok := object[key]; ok && value != nil && value != "" {
				typed["frame_images"] = append(typed["frame_images"], value)
			}
		}
	}
	for name, items := range typed {
		if _, declared := fields[name]; !declared {
			continue
		}
		if existing, ok := values[name].([]any); ok {
			items = append(items, existing...)
		}
		values[name] = items
	}
	if !modelParameterObjectMatches(schema, values, false) {
		return errors.New("Generation parameters are outside the published model specifications")
	}
	return nil
}

func objectParameterMap(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

// Usage comes from the pinned host adapter's validated meter. Validate the
// effective default quantities as well as explicit wire controls; do not infer
// an image count from token usage or rewrite the provider request.
func ModelConfigurationUsageParameters(request, usage map[string]any) map[string]any {
	if len(usage) == 0 {
		return request
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return request
	}
	var result map[string]any
	if common.Unmarshal(encoded, &result) != nil {
		return request
	}
	normalized, err := common.Marshal(usage)
	if err != nil {
		return request
	}
	var facts map[string]any
	if common.Unmarshal(normalized, &facts) != nil {
		return request
	}
	for fact, control := range map[string]string{"seconds": "seconds", "resolution": "resolution", "image_count": "n"} {
		value, present := facts[fact]
		if !present {
			continue
		}
		for _, object := range []map[string]any{result, objectParameterMap(result["metadata"]), objectParameterMap(result["parameters"])} {
			for _, alias := range modelParameterAliases(control) {
				delete(object, alias)
			}
		}
		result[control] = value
	}
	return result
}

// Published controls narrow or replace their named fields; untouched adapter
// controls and host-only declarations remain available to both catalogs. Alias
// siblings and existing finite specification branches are narrowed together.
func ProjectModelConfigurationParameters(base, controls map[string]any) map[string]any {
	if controls == nil {
		return base
	}
	result := map[string]any{}
	if encoded, err := common.Marshal(base); err == nil {
		_ = common.Unmarshal(encoded, &result)
	}
	if result == nil {
		result = map[string]any{}
	}
	fields, _ := result["properties"].(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}
	changed := map[string]any{}
	configured, _ := controls["properties"].(map[string]any)
	for name, declaration := range configured {
		changed[name] = declaration
		for _, alias := range modelParameterAliases(name) {
			if _, exists := fields[alias]; exists {
				changed[alias] = declaration
			}
		}
	}
	for name, declaration := range changed {
		if declaration == false {
			fields[name] = false
			continue
		}
		prior, _ := fields[name].(map[string]any)
		copy := map[string]any{}
		for key, value := range prior {
			copy[key] = value
		}
		for key, value := range declaration.(map[string]any) {
			copy[key] = value
		}
		if value, exists := copy["default"]; exists && !modelParameterValueMatches(name, copy, value, true) {
			delete(copy, "default")
		}
		fields[name] = copy
	}
	for key, value := range controls {
		if key != "properties" {
			result[key] = value
		}
	}
	result["properties"] = fields
	if limits, ok := result["x-reference-limits"].(map[string]any); ok {
		for field, key := range map[string]string{"reference_images": "images", "reference_videos": "videos", "reference_audios": "audios"} {
			if control, changed := configured[field]; changed {
				if control == false {
					limits[key] = float64(0)
				} else if maximum, ok := control.(map[string]any)["maxItems"].(float64); ok {
					if native, ok := limits[key].(float64); !ok || maximum < native {
						limits[key] = maximum
					}
				}
			}
		}
	}
	if control, changed := configured["frame_images"]; changed {
		if control == false {
			result["x-reference-frame-max-images"] = float64(0)
		} else if maximum, ok := control.(map[string]any)["maxItems"].(float64); ok {
			if native, ok := result["x-reference-frame-max-images"].(float64); !ok || maximum < native {
				result["x-reference-frame-max-images"] = maximum
			}
		}
	}
	if _, explicit := controls["oneOf"]; !explicit {
		if branches, ok := result["oneOf"].([]any); ok {
			retained := []any{}
			for _, raw := range branches {
				branch, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				properties, _ := branch["properties"].(map[string]any)
				valid := true
				for name, rawField := range properties {
					constraint, present := changed[name]
					if !present {
						continue
					}
					if constraint == false {
						valid = false
						break
					}
					field, ok := rawField.(map[string]any)
					if !ok {
						valid = false
						break
					}
					control := constraint.(map[string]any)
					if value, constant := field["const"]; constant && !modelParameterValueMatches(name, control, value, true) {
						valid = false
						break
					}
					if menu, exists := field["enum"].([]any); exists {
						choices := []any{}
						for _, value := range menu {
							if modelParameterValueMatches(name, control, value, true) {
								choices = append(choices, value)
							}
						}
						if len(choices) == 0 {
							valid = false
							break
						}
						field["enum"] = choices
					}
					for _, key := range []string{"minimum", "minItems"} {
						if bound, ok := control[key].(float64); ok {
							previous, exists := field[key].(float64)
							if !exists || bound > previous {
								field[key] = bound
							}
						}
					}
					for _, key := range []string{"maximum", "maxItems"} {
						if bound, ok := control[key].(float64); ok {
							previous, exists := field[key].(float64)
							if !exists || bound < previous {
								field[key] = bound
							}
						}
					}
					if minimum, ok := field["minimum"].(float64); ok {
						if maximum, ok := field["maximum"].(float64); ok && minimum > maximum {
							valid = false
							break
						}
					}
					if value, exists := field["default"]; exists && !modelParameterValueMatches(name, field, value, true) {
						delete(field, "default")
					}
				}
				if valid {
					retained = append(retained, branch)
				}
			}
			result["oneOf"] = retained
		}
	}
	return result
}

func modelParameterObjectMatches(schema map[string]any, values map[string]any, branch bool) bool {
	fields, _ := schema["properties"].(map[string]any)
	for name, declaration := range fields {
		value, present := values[name]
		if !present {
			continue
		}
		if declaration == false {
			if value == false || value == nil {
				continue
			}
			if array, ok := value.([]any); ok && len(array) == 0 {
				continue
			}
			return false
		}
		field, _ := declaration.(map[string]any)
		if !modelParameterValueMatches(name, field, value, true) {
			return false
		}
	}
	if required, exists := schema["required"].([]any); exists {
		for _, value := range required {
			if _, exists := values[value.(string)]; !exists {
				return false
			}
		}
	}
	if alternatives, exists := schema["oneOf"].([]any); exists && !branch {
		for _, value := range alternatives {
			if modelParameterObjectMatches(value.(map[string]any), values, true) {
				return true
			}
		}
		return false
	}
	return true
}
