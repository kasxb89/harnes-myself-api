package modelpricebook

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
)

// Kept separately from the active model catalog: displaying historical approved
// quotes never enables a model, changes its price, or creates a relay route.
//
//go:embed approved_quotes.json
var approved []byte

type Price struct {
	Params         map[string]any `json:"params"`
	Direction      string         `json:"direction"`
	Unit           string         `json:"unit"`
	ProcurementCNY float64        `json:"procurement_cny"`
	RetailCNY      *float64       `json:"retail_cny"`
	LaneID         string         `json:"lane_id,omitempty"`
}
type Quote struct {
	ModelName       string  `json:"model_name"`
	Name            string  `json:"name"`
	Provider        string  `json:"provider"`
	Basis           string  `json:"basis"`
	CheckedAt       string  `json:"checked_at"`
	RetailCheckedAt string  `json:"retail_checked_at,omitempty"`
	Source          string  `json:"source"`
	Currency        string  `json:"currency,omitempty"`
	Prices          []Price `json:"prices"`
}

func Snapshot() ([]Quote, error) {
	var snapshot struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := common.Unmarshal(approved, &snapshot); err != nil {
		return nil, err
	}
	models, err := codebloom.Models()
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		prices := make([]Price, 0, len(model.CostComponents))
		for _, component := range model.CostComponents {
			prices = append(prices, Price{Params: component.Params, Direction: component.Direction, Unit: component.Unit, ProcurementCNY: component.AccountCreditCNY, RetailCNY: component.RetailCNY})
		}
		conditions, err := common.Marshal(model.CostConditions)
		if err != nil {
			return nil, err
		}
		source := fmt.Sprintf("https://codebloom.club/api/pricing ; account multiplier %g applied once; upstream expression: %s ; cost conditions: %s ; retail policy: %s, approved %s ; %s", model.AccountMultiplier, model.BaseBillingExpression, conditions, model.RetailPricingStatus, model.RetailCheckedAt, strings.Join(model.CostNotes, "; "))
		snapshot.Quotes = append(snapshot.Quotes, Quote{ModelName: model.ID, Name: "CodeBloom · " + model.Name, Provider: "CodeBloom", Basis: model.CostBasis, CheckedAt: model.CheckedAt, RetailCheckedAt: model.RetailCheckedAt, Source: source, Currency: model.Currency, Prices: prices})
	}
	return snapshot.Quotes, nil
}
