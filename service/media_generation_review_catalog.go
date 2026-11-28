package service

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/shopspring/decimal"
)

//go:embed media_generation_thirty_review.json
var mediaGenerationThirtyReviewData []byte

func MediaGenerationCatalogCapabilities(maxUploadBytes int64) map[string]any {
	return map[string]any{"media_generation_v2": true, "quote_required": true, "durable_operation_receipt": true, "all_artifacts": true, "same_operation_configuration_requote": true, "material_conditions_v1": true, "parameter_conditions_v1": true, "native_video_batch_v1": true, "exact_specification_pricing_v1": true, "explicit_requote_v1": true, "selection_constraints_v1": true, "max_upload_bytes": maxUploadBytes}
}

func mediaGenerationCatalogEntry(def model.ModelConfiguration, contract ycapi.MediaContract, version string, schema map[string]any, fields, intents []map[string]any, pricing map[string]any) map[string]any {
	flat, selection, err := mediaGenerationCatalogSelection(schema)
	if err != nil {
		return nil
	}
	publicPrice := map[string]any{"display": "按所选规格显示费用", "quote_required": true}
	maps.Copy(publicPrice, pricing)
	entry := map[string]any{"id": def.ModelName, "display_name": mediaGenerationDisplayName(def.DisplayName, contract, fields, intents), "kind": contract.Kind, "description": strings.Join(contract.Capabilities, " · "), "enabled": true, "model_version": version, "parameters_schema": flat, "asset_fields": fields, "intents": intents, "pricing": publicPrice}
	if selection != nil {
		entry["selection_constraints"] = selection
	}
	if conditions := ycapi.MediaContractMaterialConditions(contract); conditions != nil {
		entry["conditions"] = conditions
	}
	if conditions := ycapi.MediaContractParameterConditions(contract); conditions != nil {
		entry["parameter_conditions"] = conditions
	}
	return entry
}

type mediaGenerationThirtyReviewRow struct {
	MediaGenerationReviewRow
	Model          string `json:"model"`
	NativeID       string `json:"native_id"`
	RetailTotalCNY string `json:"retail_total_cny"`
	Decision       string `json:"decision"`
}

// MediaGenerationThirtyReviewRows returns detached administrative review inputs,
// never an execution qualification or a production configuration to publish.
func MediaGenerationThirtyReviewRows(modelID string) ([]MediaGenerationReviewRow, error) {
	rows, err := mediaGenerationThirtySourceRows()
	if err != nil {
		return nil, err
	}
	contract, exists := ycapi.LookupMediaContract(modelID)
	if !exists {
		return nil, errors.New("model_not_available")
	}
	result := []MediaGenerationReviewRow{}
	for _, row := range rows {
		if row.Model == contract.ID {
			if row.ContractSHA256 != mediaGenerationContractSHA(contract) {
				return nil, errors.New("reviewed media contract changed")
			}
			result = append(result, row.MediaGenerationReviewRow)
		}
	}
	return result, nil
}

func mediaGenerationThirtySourceRows() ([]mediaGenerationThirtyReviewRow, error) {
	var document struct {
		SchemaVersion int                              `json:"schema_version"`
		ReviewOnly    bool                             `json:"review_only"`
		Rows          []mediaGenerationThirtyReviewRow `json:"rows"`
	}
	if common.Unmarshal(mediaGenerationThirtyReviewData, &document) != nil || document.SchemaVersion != 1 || !document.ReviewOnly || len(document.Rows) != 22 {
		return nil, errors.New("reviewed media specifications unavailable")
	}
	seen := map[string]bool{}
	for _, row := range document.Rows {
		cost, e := decimal.NewFromString(row.CostTotalCNY)
		retail, r := decimal.NewFromString(row.RetailTotalCNY)
		if row.ID == "" || seen[row.ID] || e != nil || r != nil || !cost.IsPositive() || !retail.IsPositive() || !validMediaReviewDigest(row.ContractSHA256) || !validMediaReviewDigest(row.QuoteRequestSHA256) || !validMediaReviewDigest(row.QuoteResponseSHA256) || row.LaneID == "" || !slices.Contains([]string{"per_call", "per_second"}, row.BillingUnit) || row.Parameters["mode"] != "text-to-video" || row.Parameters["duration"] != "30" || row.Parameters["count"] != float64(1) || row.Parameters["aspect_ratio"] != "16:9" {
			return nil, errors.New("invalid reviewed media specification")
		}
		seen[row.ID] = true
		if row.Official == nil {
			if row.Decision != "actual_yc_cost_times_1_2" || !retail.Equal(cost.Mul(decimal.RequireFromString("1.2"))) {
				return nil, errors.New("invalid reviewed media markup")
			}
		} else if row.Decision != "official_exact_cny" || row.Official.NativeModel != row.NativeID || row.Official.TotalCNY != row.RetailTotalCNY {
			return nil, errors.New("invalid reviewed official reference")
		}
	}
	return document.Rows, nil
}

func mediaGenerationReviewedSelection(contract ycapi.MediaContract, parameters map[string]any) (map[string]any, error) {
	input := maps.Clone(parameters)
	if input == nil {
		input = map[string]any{}
	}
	input["prompt"] = "Local specification validation only"
	delete(input, "negative_prompt")
	normalized, err := ycapi.ValidateMediaContractParams(contract.ID, input, nil)
	if err != nil {
		return nil, err
	}
	delete(normalized, "prompt")
	delete(normalized, "negative_prompt")
	return normalized, nil
}

// Controls are checked before opening/probing/uploading any referenced file.
// Only the reviewed text/30s slice is narrowed; older durations and reference
// modes continue through their independently configured controls.
func validateMediaGenerationReviewedSpecification(def model.ModelConfiguration, contract ycapi.MediaContract, parameters map[string]any, bindings map[string][]string) error {
	effective := maps.Clone(parameters)
	if effective == nil {
		effective = map[string]any{}
	}
	configured, _ := def.Parameters["properties"].(map[string]any)
	for _, name := range []string{"mode", "duration"} {
		if _, explicit := effective[name]; explicit {
			continue
		}
		if value := contract.Parameters[name].Default; value != nil {
			effective[name] = value
		}
		for alias, raw := range configured {
			if mediaGenerationControlField(contract, alias) == name {
				if field, ok := raw.(map[string]any); ok {
					if value, exists := field["default"]; exists {
						effective[name] = value
					}
				}
			}
		}
	}
	seconds, err := decimal.NewFromString(fmt.Sprint(effective["duration"]))
	if contract.Kind != "video" || err != nil || !seconds.Equal(decimal.NewFromInt(30)) || effective["mode"] != "text-to-video" {
		return nil
	}
	rows, err := mediaGenerationThirtySourceRows()
	if err != nil {
		return err
	}
	applicable := []mediaGenerationThirtyReviewRow{}
	for _, row := range rows {
		if row.Model == contract.ID {
			applicable = append(applicable, row)
		}
	}
	if len(applicable) == 0 {
		return nil
	}
	for _, ids := range bindings {
		if len(ids) > 0 {
			return errors.New("invalid_parameters")
		}
	}
	// Only this new text/30s slice needs the review's early checks. Older modes
	// retain their original conditional-default handling in native preparation.
	schema, err := mediaGenerationConfiguredSchema(contract, def.Parameters)
	if err != nil {
		return errors.New("invalid_parameters")
	}
	properties := schema["properties"].(map[string]any)
	for name, raw := range configured {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		native := mediaGenerationControlField(contract, name)
		if _, explicit := parameters[native]; explicit || contract.Parameters[native].Type == "upload" {
			continue
		}
		if _, explicitDefault := field["default"]; explicitDefault {
			if projected, ok := properties[native].(map[string]any); ok {
				if value, exists := projected["default"]; exists {
					effective[native] = value
				}
			}
		}
	}
	selection, err := mediaGenerationReviewedSelection(contract, effective)
	if err != nil {
		return errors.New("invalid_parameters")
	}
	if validateMediaGenerationControls(contract, def.Parameters, selection) != nil {
		return errors.New("invalid_parameters")
	}
	for _, row := range applicable {
		if row.ContractSHA256 != mediaGenerationContractSHA(contract) || def.MediaExecution == nil || row.LaneID != def.MediaExecution.LaneID {
			continue
		}
		allowed, err := mediaGenerationReviewedSelection(contract, row.Parameters)
		if err == nil && MediaGenerationDigest(selection) == MediaGenerationDigest(allowed) {
			return nil
		}
	}
	return errors.New("invalid_parameters")
}

// This is a view of the current expression, not a second table used to charge.
// Private procurement evidence and supplier lane names never enter its DTO.
func mediaGenerationSpecificationPricing(def model.ModelConfiguration, contract ycapi.MediaContract, expression string, ratio float64) (map[string]any, error) {
	rows, err := mediaGenerationThirtySourceRows()
	if err != nil {
		return nil, err
	}
	if def.MediaExecution == nil {
		return nil, nil
	}
	items := []map[string]any{}
	versions := []string{}
	for _, row := range rows {
		if row.Model != contract.ID || row.LaneID != def.MediaExecution.LaneID || row.ContractSHA256 != mediaGenerationContractSHA(contract) {
			continue
		}
		selection, err := mediaGenerationReviewedSelection(contract, row.Parameters)
		if err != nil || validateMediaGenerationReviewedSpecification(def, contract, selection, nil) != nil {
			continue
		}
		facts := mediaGenerationUsage(contract, selection, nil, ycapi.MediaIntentText)
		_, total, version, err := mediaGenerationRetail(expression, facts, ratio)
		if err != nil {
			return nil, err
		}
		quantity, unit := int64(1), "per_video"
		if row.BillingUnit == "per_second" {
			quantity, unit = 30, "per_second"
		}
		items = append(items, map[string]any{"id": MediaGenerationDigest([]any{contract.ID, selection, row.LaneID}), "parameters": selection, "billing_unit": unit, "quantity": quantity, "unit_price_cny": total.Div(decimal.NewFromInt(quantity)).String(), "total_cny": total.String(), "quote_required": true})
		versions = append(versions, version)
	}
	if len(items) == 0 {
		return nil, nil
	}
	policy := MediaGenerationDigest([]any{fmt.Sprintf("%x", sha256.Sum256(mediaGenerationThirtyReviewData)), def, versions, ratio})
	for _, item := range items {
		item["policy_revision"] = policy
	}
	return map[string]any{"currency": "CNY", "policy_revision": policy, "specification_rows": items}, nil
}
