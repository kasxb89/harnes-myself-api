package ycapi

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

func Lookup(id string) (Model, bool) {
	if catalogError != nil {
		return Model{}, false
	}
	i := slices.IndexFunc(approved.Models, func(m Model) bool { return m.ID == id })
	if i < 0 {
		return Model{}, false
	}
	return approved.Models[i], true
}

func Models() ([]Model, error) {
	return slices.Clone(approved.Models), catalogError
}

func ClientPrices(id string) []map[string]any {
	m, ok := Lookup(id)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(m.Prices))
	for _, p := range m.Prices {
		unit := "CNY / million tokens"
		if m.Kind == "video" {
			unit = "CNY / second"
		} else if m.Kind == "image" {
			unit = "CNY / image"
		}
		row := map[string]any{"params": p.Params, "retail_cny": p.Retail.String(), "unit": unit}
		if p.Direction != "" {
			row["direction"] = p.Direction
		}
		if m.Kind == "video" || m.Kind == "image" {
			row["retail_total_cny"] = p.RetailTotal.String()
		}
		rows = append(rows, row)
	}
	return rows
}

// PluginCatalog stays on the server. Public discovery uses ClientParameters,
// which deliberately does not serialize prices, procurement routes or keys.
func PluginCatalog() ([]byte, error) {
	if catalogError != nil {
		return nil, catalogError
	}
	var models []map[string]any
	for _, m := range approved.Models {
		if m.Kind == "text" {
			continue
		}
		offers := make([]map[string]any, 0, len(m.Offers))
		for _, o := range m.Offers {
			var lane string
			for _, p := range m.Prices {
				if m.Kind == "audio" || maps.EqualFunc(o.Params, p.Params, func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
					lane = p.LaneID
					break
				}
			}
			offers = append(offers, map[string]any{"id": o.ID, "params": o.Params, "lane": lane})
		}
		models = append(models, map[string]any{"id": m.ID, "upstream": m.UpstreamID, "kind": m.Kind, "fields": m.Params, "offers": offers})
	}
	return common.Marshal(models)
}

// BillingExpression converts the approved CNY sale to this instance's existing
// USD accounting rate. It never changes USDExchangeRate or quota units.
func BillingExpression(id string, usdCny decimal.Decimal) (string, error) {
	m, ok := Lookup(id)
	if !ok || !usdCny.IsPositive() || usdCny.GreaterThan(decimal.NewFromInt(1000000)) {
		return "", fmt.Errorf("invalid approved model or exchange rate")
	}
	if m.Kind == "text" || m.Kind == "audio" {
		prices := map[string]string{}
		for _, p := range m.Prices {
			prices[p.Direction] = "(" + p.Retail.String() + " / " + usdCny.String() + ")"
		}
		if m.Kind == "text" {
			return `tier("approved", p * ` + prices["input"] + ` + c * ` + prices["output"] + `)`, nil
		}
		return `tier("approved", u("input_tokens") * ` + prices["input"] + ` / 1000000 + u("output_tokens") * ` + prices["output"] + ` / 1000000)`, nil
	}
	var expression strings.Builder
	for i, o := range m.Offers {
		p := m.Prices[i]
		if i < len(m.Offers)-1 {
			expression.WriteString(`u("spec") == "` + o.ID + `" ? `)
		}
		quantity := `u("image_count")`
		if m.Kind == "video" {
			quantity = `u("seconds")`
		}
		expression.WriteString(`tier("` + o.ID + `", ` + quantity + ` * (` + p.Retail.String() + ` / ` + usdCny.String() + `))`)
		if i < len(m.Offers)-1 {
			expression.WriteString(" : ")
		}
	}
	return expression.String(), nil
}

func ClientParameters(id string) map[string]any {
	m, ok := Lookup(id)
	if !ok {
		return nil
	}
	properties := map[string]any{
		"n":                map[string]any{"type": "integer", "enum": []int{1}, "default": 1},
		"reference_images": map[string]any{"type": "array", "maxItems": 0},
		"reference_videos": map[string]any{"type": "array", "maxItems": 0},
		"reference_audios": map[string]any{"type": "array", "maxItems": 0},
		"frame_images":     map[string]any{"type": "array", "maxItems": 0},
		"seed":             false, "generate_audio": false, "first_frame": false, "last_frame": false,
	}
	parameters := map[string]any{"type": "object", "additionalProperties": false, "properties": properties,
		"x-model-kind": m.Kind, "x-reference-limits": map[string]any{"images": 0, "videos": 0, "audios": 0, "counts_include_files_and_urls": true}}
	if m.Kind == "text" {
		parameters["x-text-capabilities"] = map[string]any{"tools": false, "vision": false, "reasoning": false, "stream": true, "max_input_tokens": 32000}
		return parameters
	}
	parameters["x-task-plugin"] = map[string]any{"key": "ycapi", "version": "1.0.0", "api_version": 1}
	if m.Kind == "audio" {
		properties = map[string]any{"input": map[string]any{"type": "string", "maxLength": 2000}, "instructions": false}
		for key, field := range m.Params {
			if !slices.Contains([]string{"voice", "response_format", "speed"}, key) {
				continue
			}
			value := map[string]any{"default": field.Default}
			if len(field.Enum) > 0 {
				value["enum"] = field.Enum
			}
			if field.Min != nil {
				value["minimum"] = *field.Min
			}
			if field.Max != nil {
				value["maximum"] = *field.Max
			}
			properties[key] = value
		}
		parameters["properties"] = properties
		parameters["x_client_audio_execution"] = map[string]string{"protocol": "openai-speech-v1", "quoteRoute": "/v1/audio/quote", "taskRoute": "/v1/tasks"}
		return parameters
	}
	var branches []any
	for _, offer := range m.Offers {
		fields := map[string]any{}
		for key, value := range offer.Params {
			name := key
			switch key {
			case "mode", "thinking_level", "count":
				continue
			case "duration":
				name = "seconds"
				n, _ := decimal.NewFromString(fmt.Sprint(value))
				value = n.IntPart() // Approved, bounded catalog quantity; not a quota conversion.
			case "aspect_ratio":
				name = "ratio"
			case "size":
				if m.Kind == "image" && !strings.Contains(fmt.Sprint(value), ":") {
					name = "resolution"
				}
			}
			fields[name] = map[string]any{"enum": []any{value}}
		}
		if m.Kind == "image" {
			fields["size"] = map[string]any{"enum": []string{"1:1"}}
			delete(fields, "ratio")
		}
		branches = append(branches, map[string]any{"properties": fields})
	}
	parameters["oneOf"] = branches
	return parameters
}
