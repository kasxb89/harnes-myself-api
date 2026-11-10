package model

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/shopspring/decimal"
)

// References are display evidence, not a second billing configuration. The
// public fields intentionally preserve the installed schema-1 reader contract.
type OfficialPriceReference struct {
	SchemaVersion    int    `json:"schema_version"`
	OfficialCNY      string `json:"official_cny"`
	Currency         string `json:"currency"`
	Model            string `json:"model"`
	Unit             string `json:"unit"`
	SourceURL        string `json:"source_url"`
	CheckedAt        string `json:"checked_at"`
	Conditions       string `json:"conditions"`
	SourceCurrency   string `json:"source_currency"`
	USDCNY           string `json:"usd_cny,omitempty"`
	OfficialTotalCNY string `json:"official_total_cny,omitempty"`
}

type ModelOfficialReferenceRow struct {
	Params    map[string]any          `json:"params"`
	Direction string                  `json:"direction,omitempty"`
	Unit      string                  `json:"unit"`
	Reference *OfficialPriceReference `json:"official_reference,omitempty"`
}

type ModelOfficialReferenceConfiguration struct {
	Mode string                      `json:"mode"`
	Rows []ModelOfficialReferenceRow `json:"rows,omitempty"`
}

var referenceDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,24})?$`)

func validReferenceDecimal(value string) bool {
	if !referenceDecimal.MatchString(value) {
		return false
	}
	number, err := decimal.NewFromString(value)
	return err == nil && number.IsPositive()
}

func validReferenceText(value string, maximum int, required bool) bool {
	return (!required || value != "") && strings.TrimSpace(value) == value && len(value) <= maximum && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateOfficialReferenceConfiguration(kind string, config *ModelOfficialReferenceConfiguration) error {
	if config == nil {
		return nil
	}
	if !slices.Contains([]string{"inherit", "override", "disabled"}, config.Mode) || len(config.Rows) > 256 || (config.Mode != "override" && len(config.Rows) != 0) || (config.Mode == "override" && len(config.Rows) == 0) {
		return errors.New("Select inherited, custom, or disabled official references")
	}
	seen := map[string]bool{}
	for _, row := range config.Rows {
		if err := ValidateOfficialReferenceRow(kind, row); err != nil {
			return err
		}
		key := OfficialReferenceRowKey(kind, row.Params, row.Direction, row.Unit)
		if key == "" || seen[key] {
			return errors.New("Official references require distinct complete specifications and directions")
		}
		seen[key] = true
	}
	return nil
}

func ValidateOfficialReferenceRow(kind string, row ModelOfficialReferenceRow) error {
	unit := map[string]string{"text": "CNY / million tokens", "image": "CNY / image", "video": "CNY / second"}[kind]
	if unit == "" || row.Unit != unit || (kind == "text" && !slices.Contains([]string{"input", "output"}, row.Direction)) || (kind != "text" && row.Direction != "") || len(row.Params) > 16 {
		return errors.New("Official reference unit and direction must match the model kind")
	}
	for name, raw := range row.Params {
		if !slices.Contains([]string{"duration", "seconds", "resolution", "size", "quality", "mode", "ratio", "aspect_ratio", "count", "n", "audio", "voice", "speed", "response_format", "thinking_level"}, name) {
			return errors.New("Unsupported official reference specification field")
		}
		switch value := raw.(type) {
		case nil, bool:
		case string:
			if !validReferenceText(value, 128, true) {
				return errors.New("Invalid official reference specification value")
			}
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e9 {
				return errors.New("Invalid official reference specification value")
			}
		default:
			// All API/storage rows are decoded through common JSON before validation.
			return errors.New("Official reference specifications require scalar values")
		}
	}
	ref := row.Reference
	if ref == nil || ref.SchemaVersion != 1 || ref.Currency != "CNY" || ref.Unit != row.Unit || !validReferenceDecimal(ref.OfficialCNY) || !validReferenceText(ref.Model, 191, true) || !validReferenceText(ref.Conditions, 4096, false) || !slices.Contains([]string{"CNY", "USD"}, ref.SourceCurrency) {
		return errors.New("Complete the official reference price, model, unit, and conditions")
	}
	if (ref.SourceCurrency == "USD" && !validReferenceDecimal(ref.USDCNY)) || (ref.USDCNY != "" && !validReferenceDecimal(ref.USDCNY)) || (ref.OfficialTotalCNY != "" && (!validReferenceDecimal(ref.OfficialTotalCNY) || kind == "text")) {
		return errors.New("Official reference conversion and total prices must be positive decimals")
	}
	date, err := time.Parse("2006-01-02", ref.CheckedAt)
	today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	if err != nil || date.Format("2006-01-02") != ref.CheckedAt || ref.CheckedAt > today {
		return errors.New("Official reference checked date must be a valid date no later than today")
	}
	source, err := url.Parse(ref.SourceURL)
	if err != nil || len(ref.SourceURL) > 2048 || !validReferenceText(ref.SourceURL, 2048, true) || strings.TrimSpace(ref.SourceURL) != ref.SourceURL || source.Scheme != "https" || source.Hostname() == "" || source.User != nil || (source.Port() != "" && source.Port() != "443") {
		return errors.New("Official reference source must be a public HTTPS URL without credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(source.Hostname(), "."))
	if !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("Official reference source must use a public host")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
		return errors.New("Official reference source must use a public host")
	}
	for key := range source.Query() {
		if slices.Contains([]string{"key", "api_key", "apikey", "access_token", "token", "authorization", "password", "secret", "signature"}, strings.ToLower(key)) {
			return errors.New("Official reference source cannot contain authentication parameters")
		}
	}
	return nil
}

// Normalize only host-known default single-output counts and spelling variants
// of a declared mode. Keep every other specification field: an omitted ratio,
// resolution, duration or quality can never act as a wildcard.
func OfficialReferenceRowKey(kind string, params map[string]any, direction, unit string) string {
	copy := maps.Clone(params)
	if copy == nil {
		copy = map[string]any{}
	}
	if kind == "image" || kind == "video" {
		if mode, ok := copy["mode"].(string); ok {
			copy["mode"] = strings.ReplaceAll(mode, "-", "_")
		} else if _, exists := copy["mode"]; !exists {
			copy["mode"] = "text_to_" + kind
		}
		count, counted := copy["count"]
		n, numbered := copy["n"]
		if counted && numbered && fmt.Sprint(count) != fmt.Sprint(n) {
			return ""
		}
		if !counted && numbered {
			copy["count"] = n
		} else if !counted {
			copy["count"] = 1
		}
		delete(copy, "n")
	}
	encoded, err := common.Marshal([]any{copy, direction, unit})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// No database migration or catalog rewrite: inherited references continue to
// come from the original reviewed catalog, with its recorded conversion/date.
func BuiltinModelOfficialReferenceRows(name string) []ModelOfficialReferenceRow {
	rows := ycapi.ClientPrices(name)
	if rows == nil {
		rows = codebloom.ClientPrices(name)
	}
	encoded, err := common.Marshal(rows)
	if err != nil {
		return nil
	}
	var result []ModelOfficialReferenceRow
	if common.Unmarshal(encoded, &result) != nil {
		return nil
	}
	if entry, ok := ycapi.Lookup(name); ok && (entry.Kind == "image" || entry.Kind == "video") {
		for i := range result {
			// Apply only this adapter's documented catalog→client mapping
			// (the same conversion as ycapi.LegacyClientParameters). Do not
			// globally reinterpret arbitrary provider size or duration fields.
			params := result[i].Params
			if value, exists := params["duration"]; exists {
				if duration, err := decimal.NewFromString(fmt.Sprint(value)); err == nil {
					params["seconds"] = duration.InexactFloat64()
					delete(params, "duration")
				}
			}
			if entry.Kind == "video" {
				if ratio, exists := params["aspect_ratio"]; exists {
					params["ratio"] = ratio
					delete(params, "aspect_ratio")
				}
			} else {
				if size, exists := params["size"]; exists && !strings.Contains(fmt.Sprint(size), ":") {
					params["resolution"] = size
					delete(params, "size")
				}
				if ratio, exists := params["aspect_ratio"]; exists {
					params["size"] = ratio
					delete(params, "aspect_ratio")
				} else if ratio, exists := params["ratio"]; exists {
					params["size"] = ratio
					delete(params, "ratio")
				} else if _, exists := params["size"]; !exists {
					params["size"] = "1:1"
				}
			}
		}
	}
	return result
}
