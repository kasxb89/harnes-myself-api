// Package codebloom holds an offline account-credit cost snapshot for candidate
// models and explicitly approved retail references. It does not configure
// credentials, routes, billing options or balances.
package codebloom

import (
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

//go:embed catalog.json
var catalogData []byte

type CostComponent struct {
	Params           map[string]any `json:"params"`
	Direction        string         `json:"direction"`
	Unit             string         `json:"unit"`
	AccountCreditCNY float64        `json:"account_credit_cny"`
	RetailCNY        *float64       `json:"retail_cny"`
}

// OutputSpec records the downloaded sample. It is not a menu of controllable
// output sizes; in particular the model named gpt-image-2k yielded 1024x1024.
type OutputSpec struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

type Model struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	UpstreamID              string            `json:"upstream_id"`
	Kind                    string            `json:"kind"`
	Transport               string            `json:"transport"`
	CredentialEnv           string            `json:"credential_env"`
	AccountMultiplier       float64           `json:"account_multiplier"`
	BaseBillingExpression   string            `json:"base_billing_expression"`
	CheckedAt               string            `json:"checked_at"`
	Currency                string            `json:"currency"`
	CostBasis               string            `json:"cost_basis"`
	CashCostCNY             *float64          `json:"cash_cost_cny"`
	RetailCNY               *float64          `json:"retail_cny"`
	RetailPricingStatus     string            `json:"retail_pricing_status"`
	RetailAccountMultiplier *float64          `json:"retail_account_multiplier"`
	RetailMarkupCNY         *float64          `json:"retail_markup_cny"`
	RetailCheckedAt         string            `json:"retail_checked_at"`
	ActivationReady         bool              `json:"activation_ready"`
	PendingReasons          []string          `json:"pending_reasons"`
	ReasoningObserved       bool              `json:"reasoning_observed"`
	CapabilityStatus        map[string]string `json:"capability_status"`
	VerifiedRequestOptions  map[string]any    `json:"verified_request_options"`
	ObservedOutput          *OutputSpec       `json:"observed_output"`
	ContextWindow           *int              `json:"context_window"`
	MaxOutputTokens         *int              `json:"max_output_tokens"`
	CostComponents          []CostComponent   `json:"cost_components"`
	CostConditions          map[string]any    `json:"cost_conditions"`
	CostNotes               []string          `json:"cost_notes"`
}

type excludedModel struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type catalog struct {
	SchemaVersion         int             `json:"schema_version"`
	Count                 int             `json:"count"`
	StrictMultimodalGate  bool            `json:"strict_multimodal_gate"`
	GenerationEnabled     bool            `json:"generation_enabled"`
	RetailPricingStatus   string          `json:"retail_pricing_status"`
	CashProcurementStatus string          `json:"cash_procurement_status"`
	Models                []Model         `json:"models"`
	Excluded              []excludedModel `json:"excluded"`
}

var approved, catalogError = loadCatalog(catalogData)

func loadCatalog(data []byte) (catalog, error) {
	var c catalog
	if err := common.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.SchemaVersion != 1 || c.Count != 37 || len(c.Models) != c.Count || len(c.Excluded) != 9 || !c.StrictMultimodalGate || c.GenerationEnabled || c.RetailPricingStatus != "approved" || c.CashProcurementStatus != "credit_purchase_exchange_rate_unknown" {
		return c, fmt.Errorf("invalid CodeBloom candidate catalog policy")
	}
	seen := make(map[string]bool, c.Count)
	kinds := map[string]int{}
	credentials := map[string]int{}
	for _, m := range c.Models {
		if m.ID != "codebloom-"+m.UpstreamID || strings.TrimSpace(m.UpstreamID) != m.UpstreamID || m.UpstreamID == "" || seen[m.ID] || !slices.Contains([]string{"text", "image"}, m.Kind) {
			return c, fmt.Errorf("invalid CodeBloom candidate identity")
		}
		seen[m.ID] = true
		kinds[m.Kind]++
		credentials[m.CredentialEnv]++
		expectedMultiplier := map[string]float64{"CODEBLOOM_GPT_KEY": .25, "CODEBLOOM_CN_KEY": .5, "CODEBLOOM_IMAGE_KEY": .75}[m.CredentialEnv]
		if expectedMultiplier == 0 || m.AccountMultiplier != expectedMultiplier || m.Currency != "CNY_ACCOUNT_CREDIT" || m.CostBasis != "account_credit_snapshot" || m.BaseBillingExpression == "" || m.CheckedAt != "2026-11-01" {
			return c, fmt.Errorf("invalid CodeBloom account-credit cost basis")
		}
		if m.ActivationReady || m.CashCostCNY != nil || m.ContextWindow != nil || m.MaxOutputTokens != nil || m.RetailCheckedAt != "2026-11-02" {
			return c, fmt.Errorf("CodeBloom unverified cash cost or capacity cannot be activated")
		}
		if m.Kind == "text" {
			if m.RetailCNY != nil || m.RetailPricingStatus != "approved_account_multiplier_plus_0_20" || m.RetailAccountMultiplier == nil || math.Abs(*m.RetailAccountMultiplier-(m.AccountMultiplier+.20)) > 1e-12 || m.RetailMarkupCNY != nil {
				return c, fmt.Errorf("invalid CodeBloom approved text retail policy")
			}
		} else if m.RetailCNY == nil || m.RetailPricingStatus != "approved_image_cost_plus_0_10" || m.RetailAccountMultiplier != nil || m.RetailMarkupCNY == nil || *m.RetailMarkupCNY != .10 {
			return c, fmt.Errorf("invalid CodeBloom approved image retail policy")
		}
		for _, reason := range []string{"credentials_not_configured", "multimodal_input_unverified", "complete_generation_contract_unverified"} {
			if !slices.Contains(m.PendingReasons, reason) {
				return c, fmt.Errorf("CodeBloom candidate is missing a pending qualification")
			}
		}
		for _, feature := range []string{"vision_input", "audio_input", "tools", "reasoning_controls", "context_window", "max_output_tokens", "async_polling"} {
			if m.CapabilityStatus[feature] != "unverified" {
				return c, fmt.Errorf("CodeBloom capability has no verification evidence")
			}
		}
		if len(m.CostComponents) == 0 {
			return c, fmt.Errorf("CodeBloom candidate cost is missing")
		}
		for _, cost := range m.CostComponents {
			if cost.Params == nil || cost.Unit == "" || !slices.Contains([]string{"input", "output", "cache_read", "cache_write", "cache_write_1h", "cache_write_12h", "image", "request"}, cost.Direction) || math.IsNaN(cost.AccountCreditCNY) || math.IsInf(cost.AccountCreditCNY, 0) || cost.AccountCreditCNY < 0 {
				return c, fmt.Errorf("invalid CodeBloom cost component")
			}
			if cost.RetailCNY == nil || math.IsNaN(*cost.RetailCNY) || math.IsInf(*cost.RetailCNY, 0) || *cost.RetailCNY < 0 {
				return c, fmt.Errorf("CodeBloom approved retail component is missing")
			}
			want := cost.AccountCreditCNY + .10
			if m.Kind == "text" {
				want = cost.AccountCreditCNY / m.AccountMultiplier * *m.RetailAccountMultiplier
			}
			if math.Abs(*cost.RetailCNY-want) > 1e-10 {
				return c, fmt.Errorf("CodeBloom approved retail policy changed")
			}
		}
		if m.Kind == "text" {
			if m.Transport != "chat_completions" || m.CredentialEnv == "CODEBLOOM_IMAGE_KEY" || m.ObservedOutput != nil || m.VerifiedRequestOptions["stream"] != true || m.CapabilityStatus["text_output"] != "verified" || (m.ReasoningObserved && m.CapabilityStatus["reasoning_output"] != "observed") {
				return c, fmt.Errorf("invalid CodeBloom text evidence")
			}
		} else {
			if len(m.CostComponents) != 1 || math.Abs(*m.RetailCNY-*m.CostComponents[0].RetailCNY) > 1e-12 {
				return c, fmt.Errorf("invalid CodeBloom fixed image retail reference")
			}
			if m.CredentialEnv != "CODEBLOOM_IMAGE_KEY" || !slices.Contains([]string{"chat_completions", "images_generations"}, m.Transport) || m.ObservedOutput == nil || m.ObservedOutput.Width <= 0 || m.ObservedOutput.Height <= 0 || m.CapabilityStatus["text_to_image"] != "verified" {
				return c, fmt.Errorf("invalid CodeBloom image evidence")
			}
			if m.Transport == "images_generations" {
				if m.VerifiedRequestOptions["size"] != "1024x1024" || m.VerifiedRequestOptions["n"] != float64(1) || m.ObservedOutput.Width != 1024 || m.ObservedOutput.Height != 1024 {
					return c, fmt.Errorf("invalid CodeBloom verified image payload")
				}
			} else {
				if m.VerifiedRequestOptions["stream"] != false {
					return c, fmt.Errorf("invalid CodeBloom verified chat image payload")
				}
				for _, option := range []string{"size", "resolution", "quality", "n"} {
					if _, exists := m.VerifiedRequestOptions[option]; exists {
						return c, fmt.Errorf("unverified CodeBloom image parameter")
					}
				}
			}
		}
	}
	if kinds["text"] != 27 || kinds["image"] != 10 || credentials["CODEBLOOM_GPT_KEY"] != 6 || credentials["CODEBLOOM_CN_KEY"] != 21 || credentials["CODEBLOOM_IMAGE_KEY"] != 10 {
		return c, fmt.Errorf("invalid CodeBloom candidate groups")
	}
	excludedIDs := map[string]bool{}
	for _, m := range c.Excluded {
		if m.ID == "" || m.Status == "" || m.Reason == "" || seen["codebloom-"+m.ID] || excludedIDs[m.ID] {
			return c, fmt.Errorf("excluded CodeBloom candidate cannot be restored")
		}
		excludedIDs[m.ID] = true
	}
	return c, nil
}

// Models returns independent copies of the reviewed candidates. Their presence
// is not permission to create a route, quote a retail price or accept a task.
func Models() ([]Model, error) {
	if catalogError != nil {
		return nil, catalogError
	}
	var c catalog
	if err := common.Unmarshal(catalogData, &c); err != nil {
		return nil, err
	}
	return c.Models, nil
}

// Lookup requires the isolated public ID; raw supplier IDs never alias existing
// providers. Returned nested maps and slices cannot mutate the shared snapshot.
func Lookup(id string) (Model, bool) {
	if catalogError != nil {
		return Model{}, false
	}
	for _, m := range approved.Models {
		if m.ID != id {
			continue
		}
		data, err := common.Marshal(m)
		if err != nil {
			return Model{}, false
		}
		var copy Model
		if err := common.Unmarshal(data, &copy); err != nil {
			return Model{}, false
		}
		return copy, true
	}
	return Model{}, false
}
