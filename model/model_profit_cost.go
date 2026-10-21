package model

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelProfitCostRulesKey = "ModelProfitCostRules"
const MaxModelProfitCostRulesBytes = 1024 * 1024

var (
	ErrModelProfitParameters   = errors.New("Invalid parameters")
	ErrModelProfitConflict     = errors.New("Profit cost rules changed. Reload before saving")
	ErrModelProfitHistory      = errors.New("Existing profit cost rules cannot be changed or removed. Append a new effective date")
	ErrModelProfitTooLarge     = errors.New("Too many billing records. Select a shorter time range")
	ErrModelProfitCostRulesAPI = errors.New("Use the profit cost rules API to preserve price history")
)

// Profit cost rules are accounting evidence only. Relay billing never reads them.
// Prices remain CNY, independently of the gateway's USD quota conversion.
type ModelProfitCostRule struct {
	ChannelID               int                `json:"channel_id"`
	ModelName               string             `json:"model_name"`
	EffectiveFrom           int64              `json:"effective_from"`
	EffectiveUntil          *int64             `json:"effective_until,omitempty"`
	Kind                    string             `json:"kind"`
	UnitCostCNY             *float64           `json:"unit_cost_cny,omitempty"`
	ResolutionCostsCNY      map[string]float64 `json:"resolution_costs_cny,omitempty"`
	InputCostPerMillionCNY  *float64           `json:"input_cost_per_million_cny,omitempty"`
	CacheCostPerMillionCNY  *float64           `json:"cache_cost_per_million_cny,omitempty"`
	OutputCostPerMillionCNY *float64           `json:"output_cost_per_million_cny,omitempty"`
	DeepSeekPeakMultiplier  *float64           `json:"deepseek_peak_multiplier,omitempty"`
	Source                  string             `json:"source"`
	FailureRefunded         bool               `json:"failure_refunded"`
}

type ModelProfitCostRules struct {
	SchemaVersion int                   `json:"schema_version"`
	Revision      int64                 `json:"revision"`
	Rules         []ModelProfitCostRule `json:"rules"`
}

func ParseModelProfitCostRules(body []byte) (ModelProfitCostRules, error) {
	var config ModelProfitCostRules
	var object map[string]common.RawMessage
	if len(body) == 0 || len(body) > MaxModelProfitCostRulesBytes || common.Unmarshal(body, &object) != nil || len(object) != 3 || object["schema_version"] == nil || object["revision"] == nil || object["rules"] == nil {
		return config, ErrModelProfitParameters
	}
	var rules []map[string]common.RawMessage
	if common.Unmarshal(object["rules"], &rules) != nil || len(rules) > 2000 {
		return config, ErrModelProfitParameters
	}
	for _, rule := range rules {
		for key := range rule {
			if bytes.Equal(bytes.TrimSpace(rule[key]), []byte("null")) {
				return config, ErrModelProfitParameters
			}
			switch key {
			case "channel_id", "model_name", "effective_from", "effective_until", "kind", "unit_cost_cny", "input_cost_per_million_cny", "cache_cost_per_million_cny", "output_cost_per_million_cny", "deepseek_peak_multiplier", "resolution_costs_cny", "source", "failure_refunded":
			default:
				return config, ErrModelProfitParameters
			}
		}
		if encoded, exists := rule["resolution_costs_cny"]; exists {
			var prices map[string]common.RawMessage
			if common.Unmarshal(encoded, &prices) != nil {
				return config, ErrModelProfitParameters
			}
			for _, price := range prices {
				if bytes.Equal(bytes.TrimSpace(price), []byte("null")) {
					return config, ErrModelProfitParameters
				}
			}
		}
		for _, key := range []string{"channel_id", "model_name", "effective_from", "kind", "source"} {
			if rule[key] == nil {
				return config, ErrModelProfitParameters
			}
		}
	}
	if common.Unmarshal(body, &config) != nil {
		return config, ErrModelProfitParameters
	}
	return config, ValidateModelProfitCostRules(config)
}

func ValidateModelProfitCostRules(config ModelProfitCostRules) error {
	if config.SchemaVersion != 1 || config.Revision < 0 || config.Revision >= 9007199254740991 || config.Rules == nil || len(config.Rules) > 2000 {
		return ErrModelProfitParameters
	}
	for i, rule := range config.Rules {
		if rule.EffectiveUntil != nil && (*rule.EffectiveUntil <= rule.EffectiveFrom || *rule.EffectiveUntil > 253402300799) {
			return ErrModelProfitParameters
		}
		if rule.ChannelID < 1 || rule.ChannelID > 1000000000 || !validClientModelID(rule.ModelName) || rule.EffectiveFrom < 0 || rule.EffectiveFrom > 253402300799 || len(rule.Source) == 0 || len(rule.Source) > 512 || strings.TrimSpace(rule.Source) != rule.Source || strings.ContainsAny(rule.Source, "\r\n\x00") {
			return ErrModelProfitParameters
		}
		for _, previous := range config.Rules[:i] {
			if previous.ChannelID == rule.ChannelID && previous.ModelName == rule.ModelName && previous.EffectiveFrom == rule.EffectiveFrom {
				return ErrModelProfitParameters
			}
		}
		for _, value := range []*float64{rule.UnitCostCNY, rule.InputCostPerMillionCNY, rule.CacheCostPerMillionCNY, rule.OutputCostPerMillionCNY, rule.DeepSeekPeakMultiplier} {
			if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1000000) {
				return ErrModelProfitParameters
			}
		}
		switch rule.Kind {
		case "request", "second", "image":
			if rule.UnitCostCNY == nil || rule.ResolutionCostsCNY != nil || rule.InputCostPerMillionCNY != nil || rule.CacheCostPerMillionCNY != nil || rule.OutputCostPerMillionCNY != nil || rule.DeepSeekPeakMultiplier != nil {
				return ErrModelProfitParameters
			}
		case "image_resolution":
			if rule.UnitCostCNY != nil || rule.InputCostPerMillionCNY != nil || rule.CacheCostPerMillionCNY != nil || rule.OutputCostPerMillionCNY != nil || rule.DeepSeekPeakMultiplier != nil || len(rule.ResolutionCostsCNY) != 3 {
				return ErrModelProfitParameters
			}
			for _, resolution := range []string{"1k", "2k", "4k"} {
				price, exists := rule.ResolutionCostsCNY[resolution]
				if !exists || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > 1000000 {
					return ErrModelProfitParameters
				}
			}
		case "token":
			if rule.UnitCostCNY != nil || rule.ResolutionCostsCNY != nil || rule.InputCostPerMillionCNY == nil || rule.CacheCostPerMillionCNY == nil || rule.OutputCostPerMillionCNY == nil || (rule.DeepSeekPeakMultiplier != nil && (*rule.DeepSeekPeakMultiplier < 1 || *rule.DeepSeekPeakMultiplier > 100)) {
				return ErrModelProfitParameters
			}
		default:
			return ErrModelProfitParameters
		}
	}
	return nil
}

func GetModelProfitCostRules(ctx context.Context) (ModelProfitCostRules, error) {
	config := ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{}}
	var option Option
	err := DB.WithContext(ctx).Where(&Option{Key: ModelProfitCostRulesKey}).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if err = common.UnmarshalJsonStr(option.Value, &config); err != nil {
		return config, err
	}
	return config, ValidateModelProfitCostRules(config)
}

// Compare the entire old option value, rather than a process cache. This CAS
// works across processes and with SQLite, which has no SELECT FOR UPDATE.
func SaveModelProfitCostRules(ctx context.Context, proposed ModelProfitCostRules) (ModelProfitCostRules, error) {
	if err := ValidateModelProfitCostRules(proposed); err != nil {
		return proposed, err
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		empty, _ := common.Marshal(ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{}})
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: ModelProfitCostRulesKey, Value: string(empty)}).Error; err != nil {
			return err
		}
		var option Option
		if err := tx.Where(&Option{Key: ModelProfitCostRulesKey}).Take(&option).Error; err != nil {
			return err
		}
		var previous ModelProfitCostRules
		if err := common.UnmarshalJsonStr(option.Value, &previous); err != nil {
			return err
		}
		if previous.Revision != proposed.Revision {
			return ErrModelProfitConflict
		}
		for _, old := range previous.Rules {
			if !slices.ContainsFunc(proposed.Rules, func(rule ModelProfitCostRule) bool { return reflect.DeepEqual(old, rule) }) {
				return ErrModelProfitHistory
			}
		}
		for _, rule := range proposed.Rules {
			if slices.ContainsFunc(previous.Rules, func(old ModelProfitCostRule) bool { return reflect.DeepEqual(old, rule) }) {
				continue
			}
			for _, old := range previous.Rules {
				if old.ChannelID == rule.ChannelID && old.ModelName == rule.ModelName && rule.EffectiveFrom <= old.EffectiveFrom {
					return ErrModelProfitHistory
				}
			}
		}
		proposed.Revision++
		encoded, err := common.Marshal(proposed)
		if err != nil {
			return err
		}
		result := tx.Model(&Option{}).Where(&Option{Key: ModelProfitCostRulesKey}).Where("value = ?", option.Value).Update("value", string(encoded))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelProfitConflict
		}
		return nil
	})
	return proposed, err
}
