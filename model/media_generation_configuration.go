package model

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// ModelMediaExecutionConfiguration adds a qualified native execution binding to
// the existing unified model record. Retail prices remain solely in Pricing.
// This private administrative structure is never a client parameter schema.
type ModelMediaExecutionConfiguration struct {
	SchemaVersion       int                     `json:"schema_version"`
	LaneID              string                  `json:"lane_id"`
	Intents             []string                `json:"intents"`
	QualificationSHA256 string                  `json:"qualification_sha256"`
	ContractSHA256      string                  `json:"contract_sha256"`
	CheckedAt           string                  `json:"checked_at"`
	RetailPolicy        *ModelMediaRetailPolicy `json:"retail_policy,omitempty"`
	BatchPolicy         *ModelMediaBatchPolicy  `json:"batch_policy,omitempty"`
}

// ModelMediaBatchPolicy is an explicit qualification of one native operation.
// It never activates by source deployment or by a supplier count declaration.
// A native-operation capacity unit must be reviewed for this exact lane; an
// unproved per-output supplier cannot use the existing one-slot admission.
type ModelMediaBatchPolicy struct {
	SchemaVersion       int      `json:"schema_version"`
	Policy              string   `json:"policy"`
	MaxCount            int      `json:"max_count,omitempty"`
	Meter               string   `json:"meter,omitempty"`
	Modes               []string `json:"modes,omitempty"`
	AdmissionUnit       string   `json:"admission_unit,omitempty"`
	DurationBasis       string   `json:"duration_basis,omitempty"`
	OutputContract      string   `json:"output_contract,omitempty"`
	ExpressionSHA256    string   `json:"expression_sha256,omitempty"`
	LaneID              string   `json:"lane_id,omitempty"`
	ContractSHA256      string   `json:"contract_sha256,omitempty"`
	QualificationSHA256 string   `json:"qualification_sha256,omitempty"`
	EvidenceSHA256      string   `json:"evidence_sha256,omitempty"`
	CheckedAt           string   `json:"checked_at,omitempty"`
}

func validateModelMediaBatchPolicy(def ModelConfiguration, contract ycapi.MediaContract) error {
	p := def.MediaExecution.BatchPolicy
	if p == nil {
		return nil
	}
	if p.SchemaVersion != 1 {
		return errors.New("Select a supported native video batch policy")
	}
	if p.Policy == "disabled" {
		if p.MaxCount != 0 || p.Meter != "" || len(p.Modes) != 0 || p.AdmissionUnit != "" || p.DurationBasis != "" || p.OutputContract != "" || p.ExpressionSHA256 != "" || p.LaneID != "" || p.ContractSHA256 != "" || p.QualificationSHA256 != "" || p.EvidenceSHA256 != "" || p.CheckedAt != "" {
			return errors.New("A disabled native video batch policy cannot retain a qualification")
		}
		return nil
	}
	expression, _ := def.Pricing["billing_setting.billing_expr"].(string)
	if mode, _ := def.Pricing["billing_setting.billing_mode"].(string); mode != billing_setting.BillingModeTieredExpr {
		expression = ""
	}
	if variants, ok := def.Pricing[billing_setting.PluginBillingExprOption].(map[string]any); ok {
		if override, ok := variants["ycapi"].(string); ok {
			expression = override
		}
	}
	count, exists := contract.Parameters["count"]
	date, err := time.Parse("2006-01-02", p.CheckedAt)
	today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	keys := billingexpr.UsedUsageKeys(expression)
	if p.Policy != "qualified_native_batch" || contract.Kind != "video" || !exists || count.Maximum == nil || count.Default != float64(1) || p.MaxCount < 2 || p.MaxCount > 50 || float64(p.MaxCount) > *count.Maximum || !slices.Contains([]string{"output_count", "total_output_seconds"}, p.Meter) || !keys[p.Meter] || keys["seconds"] || p.AdmissionUnit != "native_operation" || !slices.Contains([]string{"requested_uniform", "unmetered"}, p.DurationBasis) || keys["total_output_seconds"] && p.DurationBasis != "requested_uniform" || p.OutputContract != "ordered_unique_video_payloads_v1" || expression == "" || p.ExpressionSHA256 != billingexpr.ExprHashString(expression) || p.LaneID != def.MediaExecution.LaneID || p.ContractSHA256 != def.MediaExecution.ContractSHA256 || p.QualificationSHA256 != def.MediaExecution.QualificationSHA256 || !mediaGenerationQuoteDigest.MatchString(p.EvidenceSHA256) || err != nil || date.Format("2006-01-02") != p.CheckedAt || p.CheckedAt > today || len(p.Modes) == 0 || len(p.Modes) > 32 {
		return errors.New("Bind native video batch pricing, output completeness, and capacity to the reviewed lane evidence")
	}
	seen := map[string]bool{}
	for _, mode := range p.Modes {
		if mode == "" || !slices.Contains(contract.Parameters["mode"].Enum, any(mode)) || seen[mode] {
			return errors.New("Select distinct reviewed native video batch modes")
		}
		for _, option := range contract.Parameters["mode"].Options {
			if option.Value == mode && option.Available != nil && !*option.Available {
				return errors.New("Select an available reviewed native video batch mode")
			}
		}
		seen[mode] = true
	}
	return nil
}

// A policy authorizes an audited loss at an official cap, never a second price.
// The existing Pricing expression remains the only amount charged to the user.
type ModelMediaRetailPolicy struct {
	SchemaVersion       int                          `json:"schema_version"`
	Policy              string                       `json:"policy"`
	ExpressionSHA256    string                       `json:"expression_sha256,omitempty"`
	LaneID              string                       `json:"lane_id,omitempty"`
	ContractSHA256      string                       `json:"contract_sha256,omitempty"`
	QualificationSHA256 string                       `json:"qualification_sha256,omitempty"`
	EvidenceSHA256      string                       `json:"evidence_sha256,omitempty"`
	CheckedAt           string                       `json:"checked_at,omitempty"`
	Rules               []ModelMediaRetailPolicyRule `json:"rules,omitempty"`
}

type ModelMediaRetailPolicyRule struct {
	SpecificationSHA256 string `json:"specification_sha256"`
	OfficialTotalCNY    string `json:"official_total_cny"`
	OfficialModel       string `json:"official_model"`
	SourceURL           string `json:"source_url"`
}

func ValidateModelMediaExecutionConfiguration(def ModelConfiguration) error {
	v := def.MediaExecution
	if v == nil {
		return nil
	}
	contract, ok := ycapi.LookupMediaContract(def.ModelName)
	if !ok || contract.Kind != def.Kind || v.SchemaVersion != 1 || v.LaneID == "" || len(v.LaneID) > 512 || strings.TrimSpace(v.LaneID) != v.LaneID || strings.ContainsFunc(v.LaneID, unicode.IsControl) || !mediaGenerationQuoteDigest.MatchString(v.QualificationSHA256) || !mediaGenerationQuoteDigest.MatchString(v.ContractSHA256) || len(v.Intents) == 0 || len(v.Intents) > 4 || len(v.CheckedAt) < 10 || len(v.CheckedAt) > 40 {
		return errors.New("完整填写已核实的多模态执行渠道和能力证据")
	}
	seen := map[string]bool{}
	for _, intent := range v.Intents {
		if !slices.Contains(ycapi.MediaContractIntents(contract), intent) || seen[intent] {
			return errors.New("选择已核实的多模态任务类型")
		}
		seen[intent] = true
	}
	if err := validateModelMediaBatchPolicy(def, contract); err != nil {
		return err
	}
	policy := v.RetailPolicy
	if policy == nil {
		return nil
	}
	if policy.SchemaVersion != 1 {
		return errors.New("Select a supported media retail policy")
	}
	if policy.Policy == "disabled" {
		if policy.ExpressionSHA256 != "" || policy.LaneID != "" || policy.ContractSHA256 != "" || policy.QualificationSHA256 != "" || policy.EvidenceSHA256 != "" || policy.CheckedAt != "" || len(policy.Rules) != 0 {
			return errors.New("A disabled media retail policy cannot retain an exception")
		}
		return nil
	}
	expression, _ := def.Pricing["billing_setting.billing_expr"].(string)
	if mode, _ := def.Pricing["billing_setting.billing_mode"].(string); mode != billing_setting.BillingModeTieredExpr {
		expression = ""
	}
	if variants, ok := def.Pricing[billing_setting.PluginBillingExprOption].(map[string]any); ok {
		if override, ok := variants["ycapi"].(string); ok {
			expression = override
		}
	}
	date, err := time.Parse("2006-01-02", policy.CheckedAt)
	today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	if policy.Policy != "official_cap" || def.ModelName != contract.ID || expression == "" || policy.ExpressionSHA256 != billingexpr.ExprHashString(expression) || policy.LaneID != v.LaneID || policy.ContractSHA256 != v.ContractSHA256 || policy.QualificationSHA256 != v.QualificationSHA256 || !mediaGenerationQuoteDigest.MatchString(policy.EvidenceSHA256) || err != nil || date.Format("2006-01-02") != policy.CheckedAt || policy.CheckedAt > today || len(policy.Rules) == 0 || len(policy.Rules) > 256 {
		return errors.New("Bind the media retail policy to the current price, route, qualification, and reviewed evidence")
	}
	specifications := map[string]bool{}
	for _, rule := range policy.Rules {
		if !mediaGenerationQuoteDigest.MatchString(rule.SpecificationSHA256) || specifications[rule.SpecificationSHA256] || !validReferenceDecimal(rule.OfficialTotalCNY) || rule.OfficialModel != contract.NativeID {
			return errors.New("Media retail exceptions require distinct exact specifications and a same-model official total")
		}
		if err := validateOfficialReferenceSourceURL(rule.SourceURL); err != nil {
			return err
		}
		specifications[rule.SpecificationSHA256] = true
	}
	return nil
}
