package service

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/shopspring/decimal"
)

// These are review inputs, not an activation, execution qualification or a
// substitute for the normal revision/route-CAS model-configuration workflow.
type MediaGenerationReviewInput struct {
	Existing         model.ModelConfiguration
	ExpectedRevision string
	BaseVersion      string
	FXCNYPerUSD      string
	Rows             []MediaGenerationReviewRow
}

type MediaGenerationReviewRow struct {
	ID                  string                         `json:"id"`
	Parameters          map[string]any                 `json:"parameters"`
	LaneID              string                         `json:"lane_id"`
	ContractSHA256      string                         `json:"contract_sha256"`
	QuoteRequestSHA256  string                         `json:"quote_request_sha256"`
	QuoteResponseSHA256 string                         `json:"quote_response_sha256"`
	CostTotalCNY        string                         `json:"cost_total_cny"`
	BillingUnit         string                         `json:"billing_unit"`
	Official            *MediaGenerationReviewOfficial `json:"official,omitempty"`
}

type MediaGenerationReviewOfficial struct {
	NativeModel         string `json:"native_model"`
	TotalCNY            string `json:"total_cny"`
	SourceURL           string `json:"source_url"`
	CheckedAt           string `json:"checked_at"`
	EvidenceSHA256      string `json:"evidence_sha256"`
	SpecificationSHA256 string `json:"specification_sha256"`
}

type MediaGenerationReviewedPrice struct {
	MediaGenerationReviewRow
	Decision       string `json:"decision"`
	RetailTotalCNY string `json:"retail_total_cny"`
	UnitPriceCNY   string `json:"unit_price_cny"`
	Quantity       int    `json:"quantity"`
}

// A candidate deliberately lacks Enabled, Routes and MediaExecution. It cannot
// be submitted as a complete model definition or silently rebind qualifications.
type MediaGenerationReviewCandidate struct {
	LaneID               string                         `json:"lane_id"`
	Parameters           map[string]any                 `json:"parameters"`
	Pricing              model.PricingValues            `json:"pricing"`
	Rows                 []MediaGenerationReviewedPrice `json:"rows"`
	ReviewOnly           bool                           `json:"review_only"`
	CanApply             bool                           `json:"can_apply"`
	PendingQualification bool                           `json:"pending_qualification"`
}

type MediaGenerationReviewPlan struct {
	ModelName             string                           `json:"model_name"`
	ExpectedRevision      string                           `json:"expected_revision"`
	BaseVersion           string                           `json:"base_version"`
	ExistingDefinitionSHA string                           `json:"existing_definition_sha256"`
	FXCNYPerUSD           string                           `json:"fx_cny_per_usd"`
	ReviewOnly            bool                             `json:"review_only"`
	CanApply              bool                             `json:"can_apply"`
	Pending               []string                         `json:"pending"`
	Candidates            []MediaGenerationReviewCandidate `json:"candidates"`
}

// PrepareMediaGenerationReviewPlan has no database, HTTP or publication side
// effects. Every lane gets a separate proposal: one lane's quote is never proof
// for another lane. Existing tariffs remain the exact expression fallback.
func PrepareMediaGenerationReviewPlan(input MediaGenerationReviewInput) (MediaGenerationReviewPlan, error) {
	contract, known := ycapi.LookupMediaContract(input.Existing.ModelName)
	if !known || contract.Kind != "video" || input.Existing.Kind != "video" || !validMediaReviewDigest(input.ExpectedRevision) || !validMediaReviewDigest(input.BaseVersion) || len(input.Rows) == 0 || len(input.Rows) > 128 {
		return MediaGenerationReviewPlan{}, errors.New("review requires an exact video model, snapshot and finite price rows")
	}
	fx, err := mediaReviewDecimal(input.FXCNYPerUSD)
	if err != nil {
		return MediaGenerationReviewPlan{}, errors.New("review requires an explicit valid CNY exchange rate")
	}
	pricingRaw, err := common.Marshal(input.Existing.Pricing)
	if err != nil {
		return MediaGenerationReviewPlan{}, err
	}
	var originalPricing model.PricingValues
	if common.Unmarshal(pricingRaw, &originalPricing) != nil {
		return MediaGenerationReviewPlan{}, errors.New("review requires configured existing pricing")
	}
	expression, _ := originalPricing["billing_setting.billing_expr"].(string)
	if mode, _ := originalPricing["billing_setting.billing_mode"].(string); mode != billing_setting.BillingModeTieredExpr {
		expression = ""
	}
	providerOverride := false
	if variants, ok := originalPricing[billing_setting.PluginBillingExprOption].(map[string]any); ok {
		if value, exists := variants["ycapi"].(string); exists {
			expression, providerOverride = value, true
		}
	}
	if expression == "" || billingexpr.UsesFixedPricing(expression) {
		return MediaGenerationReviewPlan{}, errors.New("review requires an existing task expression; no free fallback is created")
	}
	if _, err := billingexpr.CompileFromCache(expression); err != nil {
		return MediaGenerationReviewPlan{}, err
	}
	plan := MediaGenerationReviewPlan{ModelName: contract.ID, ExpectedRevision: input.ExpectedRevision, BaseVersion: input.BaseVersion, ExistingDefinitionSHA: MediaGenerationDigest(input.Existing), FXCNYPerUSD: fx.String(), ReviewOnly: true, CanApply: false, Pending: []string{"recheck current revision, base version and exchange rate through the normal CAS workflow", "review exact lane and execution qualifications; none are rebound by this proposal", "review existing retail and batch policy expression bindings before applying", "preserve independently qualified existing modes and financial history"}}
	rowsByLane := map[string][]MediaGenerationReviewedPrice{}
	seenID, seenSpec, seenRequest := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range input.Rows {
		if row.ID == "" || len(row.ID) > 64 || strings.ContainsFunc(row.ID, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' }) || seenID[row.ID] || row.ContractSHA256 != mediaGenerationContractSHA(contract) || !validMediaReviewDigest(row.QuoteRequestSHA256) || !validMediaReviewDigest(row.QuoteResponseSHA256) || !strings.HasPrefix(row.LaneID, contract.NativeID+"@") || len(row.LaneID) > 512 || strings.ContainsFunc(row.LaneID, unicode.IsControl) || strings.TrimSpace(row.LaneID) != row.LaneID || !slices.Contains([]string{"per_call", "per_second"}, row.BillingUnit) {
			return MediaGenerationReviewPlan{}, errors.New("review price row has an invalid exact identity, lane or evidence binding")
		}
		seenID[row.ID] = true
		paramsRaw, err := common.Marshal(row.Parameters)
		if err != nil || common.Unmarshal(paramsRaw, &row.Parameters) != nil {
			return MediaGenerationReviewPlan{}, errors.New("review price parameters are invalid")
		}
		for name := range row.Parameters {
			if !slices.Contains([]string{"mode", "duration", "count", "resolution", "aspect_ratio", "quality"}, name) {
				return MediaGenerationReviewPlan{}, errors.New("review rows require exact text-only comparison parameters")
			}
		}
		if row.Parameters["mode"] != "text-to-video" || fmt.Sprint(row.Parameters["duration"]) != "30" || row.Parameters["count"] != float64(1) || row.Parameters["aspect_ratio"] != "16:9" || row.Parameters["resolution"] == nil {
			return MediaGenerationReviewPlan{}, errors.New("review rows require single text-only 30-second comparison specifications")
		}
		row.Parameters["duration"] = "30"
		if value, exists := row.Parameters["quality"]; exists && value != "标准" && value != "standard" {
			return MediaGenerationReviewPlan{}, errors.New("review rows require the exact standard quality value")
		}
		validate := maps.Clone(row.Parameters)
		validate["prompt"] = "Review exact native tariff only"
		if _, err := ycapi.ValidateMediaContractParams(contract.ID, validate, nil); err != nil {
			return MediaGenerationReviewPlan{}, err
		}
		spec := MediaGenerationDigest([]any{row.LaneID, row.Parameters})
		if seenSpec[spec] {
			return MediaGenerationReviewPlan{}, errors.New("review price specifications must be distinct")
		}
		seenSpec[spec] = true
		if seenRequest[row.QuoteRequestSHA256] {
			return MediaGenerationReviewPlan{}, errors.New("review specifications cannot reuse a different quote request's evidence")
		}
		seenRequest[row.QuoteRequestSHA256] = true
		cost, err := mediaReviewDecimal(row.CostTotalCNY)
		if err != nil {
			return MediaGenerationReviewPlan{}, errors.New("review requires the original positive CNY total")
		}
		price, decision := cost.Mul(decimal.RequireFromString("1.2")), "actual_cost_plus_20_percent"
		if official := row.Official; official != nil {
			date, dateErr := time.Parse("2006-01-02", official.CheckedAt)
			source, sourceErr := url.Parse(official.SourceURL)
			host := ""
			if sourceErr == nil {
				host = strings.ToLower(strings.TrimSuffix(source.Hostname(), "."))
			}
			if official.NativeModel != contract.NativeID || official.SpecificationSHA256 != MediaGenerationReviewSpecificationSHA(contract.ID, row.Parameters) || !validMediaReviewDigest(official.EvidenceSHA256) || dateErr != nil || date.Format("2006-01-02") != official.CheckedAt || official.CheckedAt > time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02") || sourceErr != nil || len(official.SourceURL) > 2048 || strings.TrimSpace(official.SourceURL) != official.SourceURL || strings.ContainsFunc(official.SourceURL, unicode.IsControl) || source.Scheme != "https" || !strings.Contains(host, ".") || net.ParseIP(host) != nil || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || source.Port() != "" && source.Port() != "443" || source.User != nil || source.RawQuery != "" || source.ForceQuery || source.Fragment != "" {
				return MediaGenerationReviewPlan{}, errors.New("review requires exact same-model dated official evidence")
			}
			price, err = mediaReviewDecimal(official.TotalCNY)
			if err != nil {
				return MediaGenerationReviewPlan{}, errors.New("review official total is invalid")
			}
			decision = "verified_official"
		}
		quantity, unitPrice := 1, price
		if row.BillingUnit == "per_second" {
			quantity, unitPrice = 30, price.DivRound(decimal.NewFromInt(30), 32)
		}
		rowsByLane[row.LaneID] = append(rowsByLane[row.LaneID], MediaGenerationReviewedPrice{MediaGenerationReviewRow: row, Decision: decision, RetailTotalCNY: price.String(), UnitPriceCNY: unitPrice.String(), Quantity: quantity})
	}
	lanes := make([]string, 0, len(rowsByLane))
	for lane := range rowsByLane {
		lanes = append(lanes, lane)
	}
	slices.Sort(lanes)
	for _, lane := range lanes {
		rows := rowsByLane[lane]
		controls, err := mediaReviewControls(contract, input.Existing.Parameters, rows)
		if err != nil {
			return MediaGenerationReviewPlan{}, err
		}
		candidateExpression := expression
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			conditions := []string{`u("seconds") == 30`, `u("intent") == "text"`, `u("input_image_count") == 0`, `u("input_video_seconds") == 0`, `u("input_audio_seconds") == 0`}
			for _, field := range []string{"mode", "resolution", "aspect_ratio", "quality"} {
				if value, present := row.Parameters[field]; present {
					conditions = append(conditions, "u("+strconv.Quote(field)+") == "+strconv.Quote(fmt.Sprint(value)))
				}
			}
			total := decimal.RequireFromString(row.RetailTotalCNY).DivRound(fx, 32)
			if value := total.InexactFloat64(); value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
				return MediaGenerationReviewPlan{}, errors.New("review price conversion is unavailable")
			}
			candidateExpression = "(" + strings.Join(conditions, " && ") + ") ? tier(" + strconv.Quote("yc30_"+row.ID) + ", " + total.String() + ") : (" + candidateExpression + ")"
		}
		if _, err := billingexpr.CompileFromCache(candidateExpression); err != nil {
			return MediaGenerationReviewPlan{}, err
		}
		var pricing model.PricingValues
		if common.Unmarshal(pricingRaw, &pricing) != nil {
			return MediaGenerationReviewPlan{}, errors.New("review pricing clone unavailable")
		}
		if providerOverride {
			pricing[billing_setting.PluginBillingExprOption].(map[string]any)["ycapi"] = candidateExpression
		} else {
			pricing["billing_setting.billing_expr"] = candidateExpression
		}
		plan.Candidates = append(plan.Candidates, MediaGenerationReviewCandidate{LaneID: lane, Parameters: controls, Pricing: pricing, Rows: rows, ReviewOnly: true, CanApply: false, PendingQualification: true})
	}
	return plan, nil
}

// MediaGenerationReviewSpecificationSHA identifies only the exact public price
// specification, independently of the supplier lane and credential identity.
func MediaGenerationReviewSpecificationSHA(modelName string, parameters map[string]any) string {
	return MediaGenerationDigest([]any{modelName, parameters})
}

func validMediaReviewDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func mediaReviewDecimal(value string) (decimal.Decimal, error) {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || strings.Count(value, ".") > 1 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.ContainsFunc(value, func(r rune) bool { return r != '.' && (r < '0' || r > '9') }) {
		return decimal.Zero, errors.New("invalid review decimal")
	}
	amount, err := decimal.NewFromString(value)
	if err != nil || !amount.IsPositive() {
		return decimal.Zero, errors.New("invalid review decimal")
	}
	return amount, nil
}

// Normalize editable aliases, never native provider values or request content.
func mediaReviewControlName(contract ycapi.MediaContract, name string) string {
	native := mediaGenerationControlField(contract, name)
	switch native {
	case "videos":
		return "reference_videos"
	case "audios":
		return "reference_audios"
	default:
		return native
	}
}

func mediaReviewControls(contract ycapi.MediaContract, existing map[string]any, rows []MediaGenerationReviewedPrice) (map[string]any, error) {
	native, ok := ycapi.MediaContractSchema(contract.ID)
	if !ok {
		return nil, errors.New("review native schema unavailable")
	}
	fields := map[string]any{}
	for _, name := range []string{"mode", "duration", "count", "resolution", "aspect_ratio", "quality"} {
		raw, exists := native["properties"].(map[string]any)[name].(map[string]any)
		if !exists {
			continue
		}
		field := map[string]any{"type": raw["type"]}
		if name == "duration" {
			field["type"] = "integer"
		}
		for _, key := range []string{"minimum", "maximum", "enum"} {
			if value, exists := raw[key]; exists {
				field[key] = value
			}
		}
		if name == "duration" {
			menu := []any{}
			for _, value := range anySlice(field["enum"]) {
				n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
				if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
					return nil, errors.New("review native duration menu unavailable")
				}
				menu = append(menu, n)
			}
			field["enum"] = menu
		}
		fields[name] = field
	}
	assetFields, err := ycapi.AssetFields(contract)
	if err != nil {
		return nil, err
	}
	for _, asset := range assetFields {
		name := mediaReviewControlName(contract, asset.Name)
		field := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		if asset.Maximum != nil {
			field["maxItems"] = float64(*asset.Maximum)
		}
		fields[name] = field
	}
	oldFields := map[string]any{}
	oldRequired := []any{}
	oldBranches := []any{map[string]any{"properties": map[string]any{}}}
	if existing != nil {
		if err := model.ValidateModelParameterSchema("video", existing); err != nil {
			return nil, err
		}
		for name, raw := range existing["properties"].(map[string]any) {
			canonical := mediaReviewControlName(contract, name)
			if canonical == "" {
				return nil, errors.New("review existing native control unavailable")
			}
			if prior, exists := oldFields[canonical]; exists && !reflect.DeepEqual(prior, raw) {
				return nil, errors.New("review existing aliases conflict")
			}
			oldFields[canonical] = raw
			if expanded, standard := fields[canonical].(map[string]any); standard {
				if old, typed := raw.(map[string]any); typed {
					if value, explicit := old["default"]; explicit {
						expanded["default"] = value
					}
				}
			} else {
				fields[canonical] = raw
			}
		}
		for _, required := range anySlice(existing["required"]) {
			oldRequired = append(oldRequired, mediaReviewControlName(contract, fmt.Sprint(required)))
		}
		if branches, explicit := existing["oneOf"].([]any); explicit {
			oldBranches = branches
		}
	}
	for name, field := range fields {
		if _, present := oldFields[name]; !present {
			oldFields[name] = field
		}
	}
	branches := []any{}
	for _, raw := range oldBranches {
		oldBranch := raw.(map[string]any)
		combined := maps.Clone(oldFields)
		required := slices.Clone(oldRequired)
		for name, rawField := range oldBranch["properties"].(map[string]any) {
			name = mediaReviewControlName(contract, name)
			merged, err := mediaReviewIntersectField(combined[name], rawField)
			if err != nil {
				return nil, err
			}
			combined[name] = merged
		}
		for _, value := range anySlice(oldBranch["required"]) {
			name := any(mediaReviewControlName(contract, fmt.Sprint(value)))
			if !slices.Contains(required, name) {
				required = append(required, name)
			}
		}
		modes := mediaReviewAllowed(combined["mode"], anySlice(fields["mode"].(map[string]any)["enum"]))
		durations := mediaReviewAllowed(combined["duration"], anySlice(fields["duration"].(map[string]any)["enum"]))
		if !slices.Contains(modes, any("text-to-video")) || !slices.Contains(durations, any(float64(30))) {
			branches = append(branches, map[string]any{"properties": combined, "required": required})
			continue
		}
		for _, partition := range []struct {
			modes, durations []any
		}{
			{slices.DeleteFunc(slices.Clone(modes), func(v any) bool { return v == "text-to-video" }), durations},
			{[]any{"text-to-video"}, slices.DeleteFunc(slices.Clone(durations), func(v any) bool { return v == float64(30) })},
		} {
			if len(partition.modes) == 0 || len(partition.durations) == 0 {
				continue
			}
			properties := maps.Clone(combined)
			properties["mode"] = map[string]any{"type": "string", "enum": partition.modes}
			properties["duration"] = map[string]any{"type": "integer", "enum": partition.durations}
			branchRequired := slices.Clone(required)
			for _, name := range []any{"mode", "duration"} {
				if !slices.Contains(branchRequired, name) {
					branchRequired = append(branchRequired, name)
				}
			}
			branches = append(branches, map[string]any{"properties": properties, "required": branchRequired})
		}
	}
	for _, row := range rows {
		properties := map[string]any{}
		required := []any{}
		for name, value := range row.Parameters {
			if name == "duration" {
				value = float64(30)
			}
			properties[name] = map[string]any{"type": fields[name].(map[string]any)["type"], "const": value}
			required = append(required, name)
		}
		slices.SortFunc(required, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
		for _, asset := range assetFields {
			properties[mediaReviewControlName(contract, asset.Name)] = false
		}
		branches = append(branches, map[string]any{"properties": properties, "required": required})
	}
	controls := map[string]any{"type": "object", "properties": fields, "oneOf": branches}
	encoded, err := common.Marshal(controls)
	if err != nil || common.Unmarshal(encoded, &controls) != nil {
		return nil, errors.New("review parameter clone unavailable")
	}
	if err := model.ValidateModelParameterSchema("video", controls); err != nil {
		return nil, err
	}
	return controls, nil
}

func mediaReviewIntersectField(base, branch any) (any, error) {
	if base == false || branch == false {
		return false, nil
	}
	b, bok := base.(map[string]any)
	c, cok := branch.(map[string]any)
	if !bok || !cok || b["type"] != c["type"] {
		return nil, errors.New("review existing specification field is unavailable")
	}
	result := maps.Clone(b)
	for key, value := range c {
		if prior, present := result[key]; present {
			switch key {
			case "enum":
				intersection := []any{}
				for _, item := range anySlice(value) {
					if slices.Contains(anySlice(prior), item) {
						intersection = append(intersection, item)
					}
				}
				if len(intersection) == 0 {
					return nil, errors.New("review existing specification has an empty intersection")
				}
				value = intersection
			case "minimum", "minItems":
				value = max(prior.(float64), value.(float64))
			case "maximum", "maxItems":
				value = min(prior.(float64), value.(float64))
			case "const":
				if !reflect.DeepEqual(prior, value) {
					return nil, errors.New("review existing specification constants conflict")
				}
			}
		}
		result[key] = value
	}
	return result, nil
}

func mediaReviewAllowed(declaration any, values []any) []any {
	field, ok := declaration.(map[string]any)
	if !ok {
		return nil
	}
	result := []any{}
	for _, value := range values {
		if constant, present := field["const"]; present && !reflect.DeepEqual(constant, value) {
			continue
		}
		if menu, present := field["enum"].([]any); present && !slices.Contains(menu, value) {
			continue
		}
		if number, numeric := value.(float64); numeric {
			if minimum, present := field["minimum"].(float64); present && number < minimum {
				continue
			}
			if maximum, present := field["maximum"].(float64); present && number > maximum {
				continue
			}
		}
		result = append(result, value)
	}
	return result
}
