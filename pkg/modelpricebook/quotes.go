package modelpricebook

import (
	_ "embed"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/ycapi"
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
	ycModels, err := ycapi.Models()
	if err != nil {
		return nil, err
	}
	for _, candidate := range ycModels {
		if candidate.Kind != "text" || len(candidate.Prices) == 0 || candidate.Prices[0].OfficialCheckedAt == "" {
			continue
		}
		quote := Quote{ModelName: candidate.ID, Name: candidate.Name, Provider: "YCAPI", Basis: "approved_snapshot", CheckedAt: candidate.Prices[0].OfficialCheckedAt, RetailCheckedAt: candidate.Prices[0].OfficialCheckedAt, Source: candidate.Prices[0].OfficialSource, Currency: "CNY", Prices: []Price{}}
		for _, price := range candidate.Prices {
			retail := price.Retail.InexactFloat64()
			quote.Prices = append(quote.Prices, Price{Params: price.Params, Direction: price.Direction, Unit: "CNY / million tokens", ProcurementCNY: price.Procurement.InexactFloat64(), RetailCNY: &retail, LaneID: price.LaneID})
		}
		i := slices.IndexFunc(snapshot.Quotes, func(q Quote) bool { return q.ModelName == candidate.ID })
		if i >= 0 {
			snapshot.Quotes[i] = quote
		} else {
			snapshot.Quotes = append(snapshot.Quotes, quote)
		}
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
		if model.CapabilityStatus["billable_usage_scope"] == "standard_short_context_input_output" {
			prices = nil
			for _, retailRow := range codebloom.ClientPrices(model.ID) {
				direction := retailRow["direction"].(string)
				i := slices.IndexFunc(model.CostComponents, func(c codebloom.CostComponent) bool { return c.Direction == direction })
				if i < 0 {
					return nil, fmt.Errorf("missing reviewed standard cost")
				}
				retail, err := strconv.ParseFloat(retailRow["retail_cny"].(string), 64)
				if err != nil {
					return nil, err
				}
				prices = append(prices, Price{Params: map[string]any{}, Direction: direction, Unit: "CNY / million tokens", ProcurementCNY: model.CostComponents[i].AccountCreditCNY, RetailCNY: &retail})
			}
		}
		conditions, err := common.Marshal(model.CostConditions)
		if err != nil {
			return nil, err
		}
		source := fmt.Sprintf("https://codebloom.club/api/pricing ; account multiplier %g applied once; upstream expression: %s ; cost conditions: %s ; retail policy: %s, approved %s ; %s", model.AccountMultiplier, model.BaseBillingExpression, conditions, model.RetailPricingStatus, model.RetailCheckedAt, strings.Join(model.CostNotes, "; "))
		snapshot.Quotes = append(snapshot.Quotes, Quote{ModelName: model.ID, Name: "qq-" + model.UpstreamID, Provider: "CodeBloom", Basis: model.CostBasis, CheckedAt: model.CheckedAt, RetailCheckedAt: model.RetailCheckedAt, Source: source, Currency: model.Currency, Prices: prices})
	}
	return snapshot.Quotes, nil
}
