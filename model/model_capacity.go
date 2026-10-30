package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelCapacityOptionKey = "ModelCapacityPolicies"

// CapacityRoute is administrator evidence for one exact public ID and route.
// Empty metadata never opts an existing model into capacity management.
type CapacityRoute struct {
	Model             string `json:"model"`
	Group             string `json:"group"`
	ChannelID         int    `json:"channel_id"`
	RouteModel        string `json:"route_model"`
	ContextWindow     *int   `json:"context_window,omitempty"`
	MaxOutputTokens   *int   `json:"max_output_tokens,omitempty"`
	BudgetField       string `json:"budget_field"`
	Source            string `json:"source"`
	VerifiedAt        string `json:"verified_at"`
	ChannelConfigHash string `json:"channel_config_hash,omitempty"`
}

type ModelCapacityConfig struct {
	Schema int             `json:"schema"`
	Routes []CapacityRoute `json:"routes"`
}

type EffectiveModelCapacity struct {
	Managed         bool
	ContextWindow   *int
	MaxOutputTokens *int
	Routes          []CapacityRoute
}

func ReadModelCapacityConfig() (ModelCapacityConfig, error) {
	cfg := ModelCapacityConfig{Schema: 1, Routes: []CapacityRoute{}}
	var option Option
	err := DB.Where(&Option{Key: ModelCapacityOptionKey}).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.OptionMapRWMutex.RLock()
		declared := common.OptionMap[ModelCapacityOptionKey] != ""
		common.OptionMapRWMutex.RUnlock()
		if declared {
			return cfg, errors.New("persisted model capacity configuration is missing")
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := common.UnmarshalJsonStr(option.Value, &cfg); err != nil {
		return cfg, errors.New("invalid model capacity configuration")
	}
	return cfg, validateModelCapacityConfig(cfg)
}

func validateModelCapacityConfig(cfg ModelCapacityConfig) error {
	if cfg.Schema != 1 || len(cfg.Routes) > 10000 {
		return errors.New("invalid model capacity schema or size")
	}
	seen := map[string]bool{}
	fields := map[string]string{}
	for _, r := range cfg.Routes {
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%s", r.Model, r.Group, r.ChannelID, r.RouteModel)
		if seen[key] {
			return errors.New("duplicate model capacity route")
		}
		seen[key] = true
		if r.Model == "" || r.Group == "" || r.RouteModel == "" || r.ChannelID <= 0 || strings.TrimSpace(r.Model) != r.Model || len(r.Model) > 255 || len(r.Group) > 64 || len(r.RouteModel) > 255 {
			return errors.New("invalid exact model capacity route")
		}
		if r.ContextWindow == nil && r.MaxOutputTokens == nil {
			return errors.New("capacity route has no verified limits")
		}
		for _, n := range []*int{r.ContextWindow, r.MaxOutputTokens} {
			if n != nil && (*n <= 0 || *n > math.MaxInt32) {
				return errors.New("model capacity must be a positive int32")
			}
		}
		if r.ContextWindow != nil && r.MaxOutputTokens != nil && *r.MaxOutputTokens > *r.ContextWindow {
			return errors.New("output capacity exceeds context window")
		}
		if r.Source == "" || len(r.Source) > 2048 || r.VerifiedAt == "" {
			return errors.New("model capacity requires source and verification date")
		}
		switch r.BudgetField {
		case "max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens":
		default:
			return errors.New("unsupported model capacity budget field")
		}
		groupKey := r.Model
		presence := fmt.Sprintf("%t/%t", r.ContextWindow != nil, r.MaxOutputTokens != nil)
		if prior, ok := fields[groupKey]; ok && prior != presence {
			return errors.New("all managed routes for an exact public model must verify the same capacity fields")
		}
		fields[groupKey] = presence
	}
	return nil
}

// Save retains previously declared fields/routes: omission is not revocation.
// Existing channel/ability switches remain the authority for availability.
func SaveModelCapacityConfig(cfg ModelCapacityConfig) error {
	if err := validateModelCapacityConfig(cfg); err != nil {
		return err
	}
	var persisted string
	err := DB.Transaction(func(tx *gorm.DB) error {
		row := Option{Key: ModelCapacityOptionKey, Value: `{"schema":1,"routes":[]}`}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where(&Option{Key: ModelCapacityOptionKey}).Take(&row).Error; err != nil {
			return err
		}
		var old ModelCapacityConfig
		if err := common.UnmarshalJsonStr(row.Value, &old); err != nil {
			return errors.New("invalid persisted model capacity configuration")
		}
		// Bind reviewed capacity to the actual channel configuration, including
		// its credential identity. The public model listing never exposes this hash.
		for i := range cfg.Routes {
			var ch Channel
			if err := tx.First(&ch, cfg.Routes[i].ChannelID).Error; err != nil {
				return errors.New("capacity channel does not exist")
			}
			cfg.Routes[i].ChannelConfigHash = CapacityChannelConfigHash(&ch)
		}
		for _, prior := range old.Routes {
			found := false
			for i := range cfg.Routes {
				r := &cfg.Routes[i]
				if r.Model == prior.Model && r.Group == prior.Group && r.ChannelID == prior.ChannelID && r.RouteModel == prior.RouteModel {
					if r.ContextWindow == nil {
						r.ContextWindow = prior.ContextWindow
					}
					if r.MaxOutputTokens == nil {
						r.MaxOutputTokens = prior.MaxOutputTokens
					}
					found = true
					break
				}
			}
			if !found {
				cfg.Routes = append(cfg.Routes, prior)
			}
		}
		if err := validateModelCapacityConfig(cfg); err != nil {
			return err
		}
		b, err := common.Marshal(cfg)
		if err != nil {
			return err
		}
		persisted = string(b)
		return tx.Model(&Option{}).Where(&Option{Key: ModelCapacityOptionKey}).Update("value", persisted).Error
	})
	if err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	// A non-empty marker enables live DB reads. The persisted JSON remains the
	// authority; no capacity values are served from this process-local marker.
	common.OptionMap[ModelCapacityOptionKey] = persisted
	common.OptionMapRWMutex.Unlock()
	return nil
}

func ResolveModelCapacity(publicModel string, groups, candidateModels []string) (EffectiveModelCapacity, error) {
	result := EffectiveModelCapacity{}
	common.OptionMapRWMutex.RLock()
	marker := common.OptionMap[ModelCapacityOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if marker == "" {
		return result, nil
	}
	cfg, err := ReadModelCapacityConfig()
	if err != nil {
		return result, err
	}
	groupSet := map[string]bool{}
	for _, g := range groups {
		groupSet[g] = true
	}
	candidates := map[string]bool{}
	for _, m := range candidateModels {
		candidates[m] = true
	}
	for _, r := range cfg.Routes {
		if r.Model == publicModel {
			result.Managed = true
		}
	}
	if !result.Managed {
		return result, nil
	}
	var abilities []Ability
	if err := DB.Where(commonGroupCol+" IN ? AND model IN ? AND enabled = ?", groups, candidateModels, true).Find(&abilities).Error; err != nil {
		return result, err
	}
	var channels []Channel
	ids := []int{}
	for _, a := range abilities {
		ids = append(ids, a.ChannelId)
	}
	if len(ids) == 0 {
		return result, nil
	}
	if err := DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return result, err
	}
	active := map[int]string{}
	for _, ch := range channels {
		active[ch.Id] = CapacityChannelConfigHash(&ch)
	}
	for _, r := range cfg.Routes {
		if r.Model != publicModel || !groupSet[r.Group] || !candidates[r.RouteModel] || active[r.ChannelID] == "" || active[r.ChannelID] != r.ChannelConfigHash {
			continue
		}
		for _, a := range abilities {
			if a.Group == r.Group && a.Model == r.RouteModel && a.ChannelId == r.ChannelID {
				result.Routes = append(result.Routes, r)
				if r.ContextWindow != nil && (result.ContextWindow == nil || *r.ContextWindow < *result.ContextWindow) {
					result.ContextWindow = r.ContextWindow
				}
				if r.MaxOutputTokens != nil && (result.MaxOutputTokens == nil || *r.MaxOutputTokens < *result.MaxOutputTokens) {
					result.MaxOutputTokens = r.MaxOutputTokens
				}
				break
			}
		}
	}
	// Across auto groups, an unknown field must not inflate the declaration.
	for _, r := range result.Routes {
		if r.ContextWindow == nil {
			result.ContextWindow = nil
			break
		}
	}
	for _, r := range result.Routes {
		if r.MaxOutputTokens == nil {
			result.MaxOutputTokens = nil
			break
		}
	}
	if result.ContextWindow != nil && result.MaxOutputTokens != nil && *result.MaxOutputTokens > *result.ContextWindow {
		result.MaxOutputTokens = common.GetPointer(*result.ContextWindow)
	}
	return result, nil
}

// CapacityChannelConfigHash invalidates evidence when a channel is repointed,
// remapped, reconfigured or rotated. Operational counters/status are excluded.
func CapacityChannelConfigHash(ch *Channel) string {
	evidence := []any{ch.Type, ch.Key, ch.BaseURL, ch.Models, ch.Group, ch.ModelMapping, ch.ParamOverride, ch.HeaderOverride, ch.Setting, ch.Other, ch.OtherSettings}
	b, err := common.Marshal(evidence)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
