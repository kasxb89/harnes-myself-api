package codebloom

// ImageProtocolCandidate projects the three current-directory additions. Only
// their exact qualified model entries may supply availability and account cost.
type ImageProtocolCandidate struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	UpstreamID              string            `json:"upstream_id"`
	Kind                    string            `json:"kind"`
	Transport               string            `json:"transport"`
	CheckedAt               string            `json:"checked_at"`
	ActivationReady         bool              `json:"activation_ready"`
	CapabilityStatus        map[string]string `json:"capability_status"`
	PendingReasons          []string          `json:"pending_reasons"`
	PublicBillingExpression string            `json:"public_billing_expression"`
	AccountMultiplier       *float64          `json:"account_multiplier"`
	AccountCreditCostCNY    *float64          `json:"account_credit_cost_cny"`
	CashCostCNY             *float64          `json:"cash_cost_cny"`
	RetailCNY               *float64          `json:"retail_cny"`
	RetailMarkupCNY         float64           `json:"retail_markup_cny"`
}

func isFreshImageProtocolModel(id string) bool {
	switch id {
	case "ys-gpt-image-2.6", "ys-gpt-image-2.6-sunburst", "ys-gpt-image-2.6-flare":
		return true
	}
	return false
}

// ImageProtocolCandidates returns detached declarations with exact-route
// qualification overlays. A directory/public price alone never activates one.
func ImageProtocolCandidates() []ImageProtocolCandidate {
	models := make([]ImageProtocolCandidate, 0, 3)
	for _, upstream := range []string{"gpt-image-2.6", "gpt-image-2.6-sunburst", "gpt-image-2.6-flare"} {
		candidate := ImageProtocolCandidate{
			ID: "ys-" + upstream, Name: "ys-" + upstream, UpstreamID: upstream,
			Kind: "image", Transport: "images_generations", CheckedAt: "2026-11-23",
			CapabilityStatus: map[string]string{
				"supplier_directory": "observed", "text_to_image": "protocol_candidate_unverified",
				"image_edit": "multipart_protocol_candidate_unverified", "billable_usage": "unverified",
				"image_decoded": "unverified", "retained_protocol": "unverified",
			},
			PendingReasons:          []string{"exact_generation_unverified", "exact_multipart_edit_unverified", "current_account_charge_unverified", "retail_price_pending", "retained_protocol_unverified"},
			PublicBillingExpression: `tier("image", fixed(0.1)) * image_count`, RetailMarkupCNY: .10,
		}
		if qualified, ok := Lookup(candidate.ID); ok && qualified.ActivationReady {
			candidate.ActivationReady = true
			candidate.CapabilityStatus, candidate.PendingReasons = qualified.CapabilityStatus, qualified.PendingReasons
			candidate.AccountMultiplier = &qualified.AccountMultiplier
			candidate.RetailCNY = qualified.RetailCNY
			if len(qualified.CostComponents) == 1 {
				candidate.AccountCreditCostCNY = &qualified.CostComponents[0].AccountCreditCNY
			}
		}
		models = append(models, candidate)
	}
	return models
}

func LookupImageProtocolCandidate(id string) (ImageProtocolCandidate, bool) {
	for _, candidate := range ImageProtocolCandidates() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return ImageProtocolCandidate{}, false
}
