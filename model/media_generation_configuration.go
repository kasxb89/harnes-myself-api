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
