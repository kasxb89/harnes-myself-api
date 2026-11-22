package model

import (
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/pkg/ycapi"
)

// ModelMediaExecutionConfiguration adds a qualified native execution binding to
// the existing unified model record. Retail prices remain solely in Pricing.
// This private administrative structure is never a client parameter schema.
type ModelMediaExecutionConfiguration struct {
	SchemaVersion       int      `json:"schema_version"`
	LaneID              string   `json:"lane_id"`
	Intents             []string `json:"intents"`
	QualificationSHA256 string   `json:"qualification_sha256"`
	ContractSHA256      string   `json:"contract_sha256"`
	CheckedAt           string   `json:"checked_at"`
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
	return nil
}
