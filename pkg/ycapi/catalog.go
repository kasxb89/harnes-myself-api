// Package ycapi holds the approved retail catalog and offline request previews.
// Previewing never sends an upstream request or modifies a channel or wallet.
package ycapi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

//go:embed catalog.json
var catalogData []byte

type PriceRow struct {
	ModelID          string          `json:"modelId"`
	Direction        string          `json:"direction"`
	Params           map[string]any  `json:"params"`
	LaneID           string          `json:"laneId"`
	Procurement      decimal.Decimal `json:"procurementCny"`
	Retail           decimal.Decimal `json:"retailCny"`
	Official         decimal.Decimal `json:"officialCny"`
	ProcurementTotal decimal.Decimal `json:"procurementTotalCny"`
	RetailTotal      decimal.Decimal `json:"retailTotalCny"`
	OfficialTotal    decimal.Decimal `json:"officialTotalCny"`
}
type Offer struct {
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}
type Model struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextWindow   int    `json:"contextWindow,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	UpstreamID      string `json:"upstreamModelId"`
	Kind            string `json:"kind"`
	Params          map[string]struct {
		Default any      `json:"default"`
		Enum    []any    `json:"enum"`
		Type    string   `json:"type"`
		Min     *float64 `json:"min"`
		Max     *float64 `json:"max"`
	} `json:"paramSchema"`
	Offers []Offer    `json:"retailOffers"`
	Prices []PriceRow `json:"priceRows"`
}
type Catalog struct {
	Version string  `json:"version"`
	Count   int     `json:"count"`
	Models  []Model `json:"models"`
}

var approved, catalogError = loadCatalog()

func loadCatalog() (Catalog, error) {
	var c Catalog
	if err := common.Unmarshal(catalogData, &c); err != nil {
		return c, err
	}
	if c.Count != 61 || len(c.Models) != c.Count {
		return c, fmt.Errorf("invalid YCAPI catalog size")
	}
	ids := map[string]bool{}
	rows := 0
	for _, m := range c.Models {
		if m.ID != "ycapi-"+m.UpstreamID || ids[m.ID] || !slices.Contains([]string{"text", "image", "video", "audio"}, m.Kind) || len(m.Offers) == 0 {
			return c, fmt.Errorf("invalid YCAPI catalog identity")
		}
		ids[m.ID] = true
		for _, p := range m.Prices {
			if p.ModelID != m.UpstreamID || p.LaneID == "" || p.Procurement.IsNegative() || !p.Procurement.LessThan(p.Retail) || !p.Retail.LessThan(p.Official) {
				return c, fmt.Errorf("invalid YCAPI retail price")
			}
			rows++
		}
	}
	if rows != 149 {
		return c, fmt.Errorf("invalid YCAPI price count")
	}
	return c, nil
}
func Snapshot() (common.RawMessage, error) {
	if catalogError != nil {
		return nil, catalogError
	}
	return slices.Clone(catalogData), nil
}

type PreviewRequest struct {
	ModelID         string   `json:"modelId"`
	SpecID          string   `json:"specId"`
	Prompt          string   `json:"prompt"`
	InputTokens     *int     `json:"inputTokens,omitempty"`
	OutputTokens    *int     `json:"outputTokens,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Voice           string   `json:"voice,omitempty"`
	ResponseFormat  string   `json:"responseFormat,omitempty"`
	Speed           *float64 `json:"speed,omitempty"`
}
type Preview struct {
	ModelID           string         `json:"modelId"`
	SpecID            string         `json:"specId"`
	Retail            string         `json:"retailCny"`
	Procurement       string         `json:"procurementCny"`
	Official          string         `json:"officialCny"`
	Fingerprint       string         `json:"fingerprint"`
	Endpoint          string         `json:"endpoint"`
	Body              map[string]any `json:"upstreamBody"`
	GenerationEnabled bool           `json:"generationEnabled"`
	Estimated         bool           `json:"estimated"`
}

// BuildPreview compiles only an approved indivisible offer. Returned amounts
// are CNY examples, never gateway quota, wallet reservations or settlement.
func BuildPreview(req PreviewRequest) (Preview, error) {
	var result Preview
	if catalogError != nil {
		return result, catalogError
	}
	index := slices.IndexFunc(approved.Models, func(m Model) bool { return m.ID == req.ModelID })
	if index < 0 {
		return result, fmt.Errorf("YCAPI model is not listed")
	}
	m := approved.Models[index]
	if strings.TrimSpace(req.Prompt) == "" || utf8.RuneCountInString(req.Prompt) > 16000 {
		return result, fmt.Errorf("invalid YCAPI preview prompt")
	}
	result.ModelID = m.ID
	result.SpecID = req.SpecID
	var retail, cost, official decimal.Decimal
	if m.Kind == "image" || m.Kind == "video" {
		i := slices.IndexFunc(m.Offers, func(o Offer) bool { return o.ID == req.SpecID })
		if i < 0 {
			return result, fmt.Errorf("YCAPI specification is not listed")
		}
		offer := m.Offers[i]
		p := slices.IndexFunc(m.Prices, func(p PriceRow) bool { return reflect.DeepEqual(p.Params, offer.Params) })
		if p < 0 {
			return result, fmt.Errorf("YCAPI specification price is unavailable")
		}
		price := m.Prices[p]
		params := maps.Clone(offer.Params)
		params["prompt"] = req.Prompt
		retail, cost, official = price.RetailTotal, price.ProcurementTotal, price.OfficialTotal
		if m.Kind == "video" {
			result.Endpoint = "/v1/videos/generations"
			result.Body = params
			result.Body["model"] = m.UpstreamID
			result.Body["lane_id"] = price.LaneID
		} else {
			result.Endpoint = "/api/v1/model-runtime/invoke"
			result.Body = map[string]any{"model_id": m.UpstreamID, "modality": "image", "operation": "generate", "params": params, "lane_id": price.LaneID}
		}
	} else {
		if req.SpecID != "tokens" || len(m.Prices) != 2 {
			return result, fmt.Errorf("invalid YCAPI token offer")
		}
		var input, output *PriceRow
		for i := range m.Prices {
			p := &m.Prices[i]
			if p.Direction == "input" {
				input = p
			}
			if p.Direction == "output" {
				output = p
			}
		}
		if input == nil || output == nil || input.LaneID != output.LaneID {
			return result, fmt.Errorf("YCAPI paired rates are unavailable")
		}
		in, out, maxOut := 1000, 1024, 4096
		if req.InputTokens != nil {
			in = *req.InputTokens
		}
		if req.OutputTokens != nil {
			out = *req.OutputTokens
		}
		if req.MaxOutputTokens != nil {
			maxOut = *req.MaxOutputTokens
		}
		if in < 0 || in > 32000 || out < 0 || out > 1000000 || !slices.Contains([]int{1024, 2048, 4096, 8192}, maxOut) {
			return result, fmt.Errorf("invalid YCAPI preview token budget")
		}
		if m.Kind == "text" && (out > maxOut || len([]byte(req.Prompt))+4096 > 32000) {
			return result, fmt.Errorf("YCAPI preview exceeds the listed text budget")
		}
		scale := decimal.NewFromInt(1000000)
		retail = input.Retail.Mul(decimal.NewFromInt(int64(in))).Add(output.Retail.Mul(decimal.NewFromInt(int64(out)))).Div(scale)
		cost = input.Procurement.Mul(decimal.NewFromInt(int64(in))).Add(output.Procurement.Mul(decimal.NewFromInt(int64(out)))).Div(scale)
		official = input.Official.Mul(decimal.NewFromInt(int64(in))).Add(output.Official.Mul(decimal.NewFromInt(int64(out)))).Div(scale)
		result.Estimated = true
		if m.Kind == "text" {
			result.Endpoint = "/api/v1/model-runtime/invoke"
			var err error
			result.Body, err = ChatEnvelope(m.ID, map[string]any{"model": m.UpstreamID, "messages": []map[string]string{{"role": "user", "content": req.Prompt}}, "max_tokens": maxOut, "stream": true, "stream_options": map[string]bool{"include_usage": true}})
			if err != nil {
				return result, err
			}
		} else {
			if utf8.RuneCountInString(req.Prompt) > 2000 {
				return result, fmt.Errorf("YCAPI audio text exceeds 2000 characters")
			}
			params := map[string]any{"input": req.Prompt}
			for _, key := range []string{"voice", "response_format", "speed"} {
				field, ok := m.Params[key]
				if !ok {
					continue
				}
				value := field.Default
				if key == "voice" && req.Voice != "" {
					value = req.Voice
				}
				if key == "response_format" && req.ResponseFormat != "" {
					value = req.ResponseFormat
				}
				if key == "speed" && req.Speed != nil {
					value = *req.Speed
				}
				if value == nil {
					continue
				}
				if len(field.Enum) > 0 && !slices.ContainsFunc(field.Enum, func(v any) bool { return reflect.DeepEqual(v, value) }) {
					return result, fmt.Errorf("invalid YCAPI audio option")
				}
				if key == "speed" {
					v, ok := value.(float64)
					if !ok || v != v || field.Min != nil && v < *field.Min || field.Max != nil && v > *field.Max {
						return result, fmt.Errorf("invalid YCAPI audio speed")
					}
				}
				params[key] = value
			}
			result.Endpoint = "/api/v1/model-runtime/invoke"
			result.Body = map[string]any{"model_id": m.UpstreamID, "modality": "audio", "operation": "speech", "params": params, "lane_id": input.LaneID}
		}
	}
	result.Retail = retail.String()
	result.Procurement = cost.String()
	result.Official = official.String()
	raw, err := common.Marshal(struct {
		Version string
		Request PreviewRequest
		Result  Preview
	}{approved.Version, req, result})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(raw)
	result.Fingerprint = hex.EncodeToString(sum[:])
	return result, nil
}
