package codebloom

import (
	"strconv"
	"strings"
)

// PubliclyAvailable is the sale fence for exact reviewed candidates and the
// retired CodeBloom namespace. The ys prefix is shared with existing models.
// Approved user prices do not imply verified credentials or capabilities.
// Existing providers retain their ordinary availability rules.
func PubliclyAvailable(id string) bool {
	for _, candidate := range approved.Models {
		if candidate.ID == id {
			m, ok := Lookup(id)
			return ok && m.ActivationReady
		}
	}
	return !strings.HasPrefix(id, "codebloom-")
}

// ClientParameters describes reviewed request controls for offline candidate
// previews. Availability is checked separately before either public catalog.
func ClientParameters(id string) map[string]any {
	m, ok := Lookup(id)
	if !ok {
		return nil
	}
	if m.Kind == "text" {
		return map[string]any{"type": "object", "properties": map[string]any{}, "x-model-kind": "text", "x-text-capabilities": map[string]any{
			"schema_version": 2, "tools": false, "vision": false, "reasoning": false, "stream": true,
			"verification":     map[string]string{"tools": "unverified", "vision": "unverified", "reasoning": "unverified"},
			"input_modalities": []string{"text"}, "reasoning_controls": map[string]any{}, "operational_status": "pending_verification",
		}}
	}
	properties := map[string]any{
		"n":    map[string]any{"type": "integer", "enum": []int{1}, "default": 1},
		"size": false, "resolution": false, "quality": false, "aspect_ratio": false, "seed": false,
		"output_format": false, "moderation": false, "format": false,
		"reference_images": map[string]any{"type": "array", "maxItems": 0},
		"image":            false, "mask": false, "background": false, "response_format": map[string]any{"type": "string", "enum": []string{"url", "b64_json"}, "default": "url"},
	}
	if m.Transport == "images_generations" {
		properties["size"] = map[string]any{"type": "string", "enum": []string{"1024x1024"}, "default": "1024x1024"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties,
		"x-model-kind": "image", "x-task-plugin": map[string]any{"key": "codebloom-image", "version": "1.0.0", "api_version": 1},
		"x-reference-limits": map[string]any{"images": 0, "videos": 0, "audios": 0, "counts_include_files_and_urls": true},
	}
}

// ClientPrices projects approved user prices only. Account credit procurement,
// supplier multipliers and credentials never enter a client response.
func ClientPrices(id string) []map[string]any {
	m, ok := Lookup(id)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(m.CostComponents))
	for _, price := range m.CostComponents {
		if price.RetailCNY == nil {
			return nil
		}
		rows = append(rows, map[string]any{"params": price.Params, "direction": price.Direction, "unit": price.Unit, "retail_cny": strconv.FormatFloat(*price.RetailCNY, 'f', -1, 64)})
	}
	return rows
}
