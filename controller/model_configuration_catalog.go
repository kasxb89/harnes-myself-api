package controller

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func projectUnifiedModelCatalog(c *gin.Context, rows []any, idKey string, groups []string) ([]any, string, error) {
	release := model.ModelConfigurationReadLock()
	defer release()
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return nil, "", err
	}
	if len(records) == 0 {
		return rows, "", nil
	}
	currentPrices := map[string]model.Pricing{}
	{
		for _, price := range model.GetPricing() {
			currentPrices[price.ModelName] = price
		}
	}
	result := make([]any, 0, len(rows))
	versions := model.ModelExecutionConfigurationVersions(records)
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	capacities, err := service.ModelCapacitiesForGroups(names, groups)
	if err != nil {
		return nil, "", err
	}
	visible := map[string]string{}
	for _, raw := range rows {
		encoded, err := common.Marshal(raw)
		if err != nil {
			return nil, "", err
		}
		var row map[string]any
		if err := common.Unmarshal(encoded, &row); err != nil {
			return nil, "", err
		}
		name, _ := row[idKey].(string)
		record, managed := records[name]
		if !managed {
			result = append(result, raw)
			continue
		}
		if !record.Definition.Enabled {
			continue
		}
		if current, ok := currentPrices[name]; ok && idKey == "model_name" {
			encoded, _ = common.Marshal(current)
			var currentRow map[string]any
			if err := common.Unmarshal(encoded, &currentRow); err != nil {
				return nil, "", err
			}
			for key, value := range currentRow {
				switch key {
				case "parameters", "retail_price_rows", "context_window", "max_output_tokens", "display_name", "kind":
					continue
				}
				row[key] = value
			}
		}
		row["display_name"] = record.Definition.DisplayName
		row["kind"] = record.Definition.Kind
		base, _ := row["parameters"].(map[string]any)
		if base == nil {
			base = currentPrices[name].Parameters
		}
		if record.Definition.Kind != "text" {
			if target, ok := model.ResolveTaskModelAlias(jsplugin.DefaultRegistry.Generation(), name); ok {
				base = model.WithMediaClientConcurrencyHint(base, target.PluginKey, name, record.Definition.Kind)
			}
		}
		if parameters := model.ProjectModelConfigurationParameters(base, record.Definition.Parameters); parameters != nil {
			row["parameters"] = parameters
		}
		if record.Definition.Kind == "text" {
			capacity := capacities[name]
			if capacity.Managed && len(capacity.Routes) == 0 {
				continue
			}
			delete(row, "context_window")
			delete(row, "max_output_tokens")
			if capacity.ContextWindow != nil {
				row["context_window"] = *capacity.ContextWindow
			}
			if capacity.MaxOutputTokens != nil {
				row["max_output_tokens"] = *capacity.MaxOutputTokens
			}
		}
		// Original supplier display tables are fixed snapshots. The editable engine
		// fields/usage expression above are authoritative; regenerate finite text-only
		// specification rows with the same current engine instead of keeping stale tables.
		delete(row, "retail_price_rows")
		prices := unifiedTextReferenceRetailRows(record, currentPrices[name])
		if parameters, ok := row["parameters"].(map[string]any); ok {
			if record.Definition.Kind != "text" {
				prices = unifiedModelRetailRows(c.Request.Context(), name, record, parameters, groups, versions[name])
			}
		}
		if len(prices) > 0 {
			row["retail_price_rows"] = applyOfficialPriceReferences(name, record.Definition.Kind, record.Definition.OfficialReferences, prices)
		}
		if record.Definition.Kind != "text" && len(prices) == 0 {
			continue
		}
		if versions[name] == "" {
			return nil, "", errors.New("Model configuration routing snapshot is unavailable")
		}
		row["model_config_version"] = versions[name]
		visible[name] = versions[name]
		result = append(result, row)
	}
	return result, model.ModelConfigurationRevisionFromVersions(visible), nil
}
