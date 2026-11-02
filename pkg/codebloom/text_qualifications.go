package codebloom

import (
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Text qualification supplements the dated procurement snapshot. Actual route
// tests, including a tool result round trip, are required before availability.
//
//go:embed text_qualifications.json
var textQualificationData []byte

type textQualification struct {
	ID                  string `json:"id"`
	CheckedAt           string `json:"checked_at"`
	Vision              string `json:"vision"`
	Tools               string `json:"tools"`
	ToolRoundtrip       string `json:"tool_roundtrip"`
	BillableUsage       string `json:"billable_usage"`
	ReasoningObserved   bool   `json:"reasoning_observed"`
	RequestSHA256       string `json:"request_sha256"`
	ResponseSHA256      string `json:"response_sha256"`
	MeterEvidenceSHA256 string `json:"meter_evidence_sha256"`
	AdditionalModel     *Model `json:"additional_model,omitempty"`
}

type textQualificationSnapshot struct {
	SchemaVersion    int                 `json:"schema_version"`
	ReviewedModelIDs []string            `json:"reviewed_model_ids"`
	Models           []textQualification `json:"models"`
}

var liveText, textQualificationError = loadTextQualifications(textQualificationData)

func loadTextQualifications(data []byte) (textQualificationSnapshot, error) {
	var snapshot textQualificationSnapshot
	if err := common.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.Models == nil || snapshot.ReviewedModelIDs == nil || len(snapshot.ReviewedModelIDs) > 64 {
		return snapshot, fmt.Errorf("invalid live text qualification schema")
	}
	seen := map[string]bool{}
	for _, id := range snapshot.ReviewedModelIDs {
		known := slices.ContainsFunc(approved.Models, func(m Model) bool { return m.ID == id && m.Kind == "text" }) || slices.ContainsFunc(approved.Excluded, func(m excludedModel) bool { return "ys-"+m.ID == id })
		if !strings.HasPrefix(id, "ys-") || len(id) > 128 || seen[id] || !known {
			return snapshot, fmt.Errorf("invalid reviewed text identity")
		}
		seen[id] = true
	}
	qualified := map[string]bool{}
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, proof := range snapshot.Models {
		if !seen[proof.ID] || qualified[proof.ID] || proof.CheckedAt == "" || proof.Vision != "verified" || proof.Tools != "verified" || proof.ToolRoundtrip != "verified" || proof.BillableUsage != "verified" {
			return snapshot, fmt.Errorf("incomplete exact-route text qualification")
		}
		qualified[proof.ID] = true
		for _, digest := range []string{proof.RequestSHA256, proof.ResponseSHA256, proof.MeterEvidenceSHA256} {
			if !hex.MatchString(digest) {
				return snapshot, fmt.Errorf("missing text qualification evidence digest")
			}
		}
		i := slices.IndexFunc(approved.Models, func(m Model) bool { return m.ID == proof.ID })
		if i >= 0 {
			if approved.Models[i].Kind != "text" || proof.AdditionalModel != nil || strings.Contains(approved.Models[i].BaseBillingExpression, "cc12h") {
				return snapshot, fmt.Errorf("text qualification conflicts with the reviewed price contract")
			}
			continue
		}
		m := proof.AdditionalModel
		if m == nil || m.ID != proof.ID || m.ID != "ys-"+m.UpstreamID || m.Name != m.ID || m.Kind != "text" || m.Transport != "chat_completions" || m.CredentialEnv != "CODEBLOOM_CN_KEY" || m.AccountMultiplier != .5 || m.RetailAccountMultiplier == nil || *m.RetailAccountMultiplier != .7 || m.Currency != "CNY_ACCOUNT_CREDIT" || m.CostBasis != "account_credit_snapshot" || m.CashCostCNY != nil || m.BaseBillingExpression == "" || strings.Contains(m.BaseBillingExpression, "cc12h") || len(m.CostComponents) < 2 || m.CapabilityStatus == nil {
			return snapshot, fmt.Errorf("invalid additional text procurement contract")
		}
		directions := map[string]bool{}
		for _, cost := range m.CostComponents {
			if !slices.Contains([]string{"input", "output", "cache_read", "cache_write", "cache_write_1h"}, cost.Direction) || directions[cost.Direction] || cost.Unit != "CNY / million tokens" || cost.Params == nil || cost.RetailCNY == nil || math.IsNaN(cost.AccountCreditCNY) || math.IsInf(cost.AccountCreditCNY, 0) || cost.AccountCreditCNY < 0 || math.IsNaN(*cost.RetailCNY) || math.IsInf(*cost.RetailCNY, 0) || math.Abs(*cost.RetailCNY-cost.AccountCreditCNY/.5*.7) > 1e-10 {
				return snapshot, fmt.Errorf("additional text retail policy differs from the approved markup")
			}
			directions[cost.Direction] = true
		}
		if !directions["input"] || !directions["output"] {
			return snapshot, fmt.Errorf("additional text input or output price is missing")
		}
	}
	return snapshot, nil
}

func withTextQualification(m Model) Model {
	if textQualificationError != nil || m.Kind != "text" {
		return m
	}
	for _, proof := range liveText.Models {
		if proof.ID != m.ID {
			continue
		}
		m.ActivationReady = true
		m.PendingReasons = []string{"cash_procurement_conversion_unknown"}
		m.CapabilityStatus["vision_input"] = "verified"
		m.CapabilityStatus["tools"] = "verified"
		m.CapabilityStatus["tool_roundtrip"] = "verified"
		m.CapabilityStatus["billable_usage"] = "verified"
		m.CapabilityStatus["complete_generation_contract"] = "verified"
		m.ReasoningObserved = proof.ReasoningObserved
		return m
	}
	return m
}
