package modelpricebook

import (
	_ "embed"
	"github.com/QuantumNous/new-api/common"
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
	RetailCNY      float64        `json:"retail_cny"`
	LaneID         string         `json:"lane_id,omitempty"`
}
type Quote struct {
	ModelName string  `json:"model_name"`
	Name      string  `json:"name"`
	Provider  string  `json:"provider"`
	Basis     string  `json:"basis"`
	CheckedAt string  `json:"checked_at"`
	Source    string  `json:"source"`
	Prices    []Price `json:"prices"`
}

func Snapshot() ([]Quote, error) {
	var snapshot struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := common.Unmarshal(approved, &snapshot); err != nil {
		return nil, err
	}
	return snapshot.Quotes, nil
}
