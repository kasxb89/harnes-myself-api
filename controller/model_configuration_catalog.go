package controller

import (
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
	visible := map[string]model.PublishedModelConfiguration{}
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
			capacity, err := service.ModelCapacityForGroups(name, groups)
			if err != nil {
				return nil, "", err
			}
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
		if parameters, ok := row["parameters"].(map[string]any); ok {
			if prices := unifiedModelRetailRows(c.Request.Context(), name, record, parameters, groups); len(prices) > 0 {
				row["retail_price_rows"] = prices
			}
		}
		row["model_config_version"] = model.ModelExecutionConfigurationVersion(name, record)
		visible[name] = record
		result = append(result, row)
	}
	return result, model.ModelConfigurationRevision(visible), nil
}
