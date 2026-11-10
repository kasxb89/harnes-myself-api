package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelCapacityOptionKey = "ModelCapacityPolicies"

var modelCapacityMutationMu sync.Mutex

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
		if r.MaxOutputTokens != nil && *r.MaxOutputTokens > common.MaxRelayOutputTokens {
			return errors.New("Output capacity exceeds the gateway request budget limit")
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
	}
	return nil
}

// Save retains previously declared fields/routes: omission is not revocation.
// Existing channel/ability switches remain the authority for availability.
func SaveModelCapacityConfig(cfg ModelCapacityConfig) error {
	modelConfigurationMu.Lock()
	defer modelConfigurationMu.Unlock()
	modelCapacityMutationMu.Lock()
	defer modelCapacityMutationMu.Unlock()
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
	results, err := ResolveModelCapacities(map[string][]string{publicModel: candidateModels}, groups)
	return results[publicModel], err
}

// Resolve an entire catalog against one fresh database view. No capacity or
// credential-dependent routing evidence is cached between requests.
func ResolveModelCapacities(models map[string][]string, groups []string) (map[string]EffectiveModelCapacity, error) {
	results := make(map[string]EffectiveModelCapacity, len(models))
	for name := range models {
		results[name] = EffectiveModelCapacity{}
	}
	common.OptionMapRWMutex.RLock()
	marker := common.OptionMap[ModelCapacityOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if marker == "" || len(models) == 0 {
		return results, nil
	}
	cfg, err := ReadModelCapacityConfig()
	if err != nil {
		return nil, err
	}
	groupSet := map[string]bool{}
	for _, group := range groups {
		groupSet[group] = true
	}
	candidates := make(map[string]map[string]bool, len(models))
	wanted := map[string]bool{}
	for _, route := range cfg.Routes {
		if names, exists := models[route.Model]; exists {
			results[route.Model] = EffectiveModelCapacity{Managed: true}
			if candidates[route.Model] == nil {
				candidates[route.Model] = map[string]bool{}
				for _, name := range names {
					candidates[route.Model][name] = true
					wanted[name] = true
				}
			}
		}
	}
	if len(wanted) == 0 {
		return results, nil
	}
	var abilities []Ability
	if err := DB.Where(commonGroupCol+" IN ? AND model IN ? AND enabled = ?", groups, slices.Sorted(maps.Keys(wanted)), true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	ids := []int{}
	type routeKey struct {
		group, model string
		channel      int
	}
	usable := map[routeKey]bool{}
	for _, ability := range abilities {
		ids = append(ids, ability.ChannelId)
		usable[routeKey{ability.Group, ability.Model, ability.ChannelId}] = true
	}
	if len(ids) == 0 {
		return results, nil
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var channels []Channel
	if err := DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	active := map[int]string{}
	for _, channel := range channels {
		active[channel.Id] = CapacityChannelConfigHash(&channel)
	}
	for _, route := range cfg.Routes {
		if !groupSet[route.Group] || !candidates[route.Model][route.RouteModel] || active[route.ChannelID] == "" || active[route.ChannelID] != route.ChannelConfigHash || !usable[routeKey{route.Group, route.RouteModel, route.ChannelID}] {
			continue
		}
		result := results[route.Model]
		result.Routes = append(result.Routes, route)
		if route.ContextWindow != nil && (result.ContextWindow == nil || *route.ContextWindow < *result.ContextWindow) {
			result.ContextWindow = route.ContextWindow
		}
		if route.MaxOutputTokens != nil && (result.MaxOutputTokens == nil || *route.MaxOutputTokens < *result.MaxOutputTokens) {
			result.MaxOutputTokens = route.MaxOutputTokens
		}
		results[route.Model] = result
	}
	for name, result := range results {
		for _, route := range result.Routes {
			if route.ContextWindow == nil {
				result.ContextWindow = nil
				break
			}
		}
		for _, route := range result.Routes {
			if route.MaxOutputTokens == nil {
				result.MaxOutputTokens = nil
				break
			}
		}
		if result.ContextWindow != nil && result.MaxOutputTokens != nil && *result.MaxOutputTokens > *result.ContextWindow {
			result.MaxOutputTokens = common.GetPointer(*result.ContextWindow)
		}
		results[name] = result
	}
	return results, nil
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
