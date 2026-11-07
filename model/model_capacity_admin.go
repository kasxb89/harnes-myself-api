package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrModelCapacityConflict = errors.New("Model capacity configuration changed; reload before saving")

// CapacityCatalogRoute describes an exact, currently enabled chat route.
// Channel credentials and the private route fingerprint never leave the model.
type CapacityCatalogRoute struct {
	Model                string `json:"model"`
	Group                string `json:"group"`
	ChannelID            int    `json:"channel_id"`
	ChannelName          string `json:"channel_name"`
	RouteModel           string `json:"route_model"`
	ContextWindow        *int   `json:"context_window,omitempty"`
	MaxOutputTokens      *int   `json:"max_output_tokens,omitempty"`
	BudgetField          string `json:"budget_field"`
	Source               string `json:"source"`
	VerifiedAt           string `json:"verified_at"`
	Configured           bool   `json:"configured"`
	Valid                bool   `json:"valid"`
	RequiresFullCapacity bool   `json:"requires_full_capacity"`
}

type ModelCapacityCatalog struct {
	Revision string                 `json:"revision"`
	Routes   []CapacityCatalogRoute `json:"routes"`
}

type ModelCapacityPatch struct {
	Revision string          `json:"revision"`
	Routes   []CapacityRoute `json:"routes"`
}

type capacityCatalogState struct {
	Catalog ModelCapacityCatalog
	Config  ModelCapacityConfig
	Raw     string
	Hashes  map[int]string
}

// ReadModelCapacityCatalog combines persisted declarations and current routing.
func ReadModelCapacityCatalog() (ModelCapacityCatalog, error) {
	var state capacityCatalogState
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		state, err = readCapacityCatalogState(tx, false)
		return err
	})
	return state.Catalog, err
}

func capacityRouteKey(route CapacityRoute) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", route.Model, route.Group, route.ChannelID, route.RouteModel)
}

func requiresFullModelCapacity(id string) bool {
	if candidate, exists := ycapi.Lookup(id); exists && candidate.Kind == "text" {
		return true
	}
	candidate, exists := codebloom.Lookup(id)
	return exists && candidate.Kind == "text"
}

// readCapacityCatalogState takes locking reads during a mutation. The revision
// binds both the exact persisted JSON and the effective channel/ability graph.
func readCapacityCatalogState(tx *gorm.DB, locked bool) (capacityCatalogState, error) {
	state := capacityCatalogState{Raw: `{"schema":1,"routes":[]}`, Hashes: make(map[int]string)}
	optionQuery := tx.Where(&Option{Key: ModelCapacityOptionKey})
	if locked {
		optionQuery = lockForUpdate(optionQuery)
	}
	var option Option
	if err := optionQuery.Take(&option).Error; err == nil {
		state.Raw = option.Value
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return state, err
	}
	if err := common.UnmarshalJsonStr(state.Raw, &state.Config); err != nil {
		return state, errors.New("Invalid model capacity configuration")
	}
	if err := validateModelCapacityConfig(state.Config); err != nil {
		return state, err
	}
	var channels []Channel
	channelQuery := tx.Where("status = ?", common.ChannelStatusEnabled).Order("id")
	if locked {
		channelQuery = lockForUpdate(channelQuery)
	}
	if err := channelQuery.Find(&channels).Error; err != nil {
		return state, err
	}
	var abilities []Ability
	abilityQuery := tx.Where("enabled = ?", true).Order(commonGroupCol + ", model, channel_id")
	if locked {
		abilityQuery = lockForUpdate(abilityQuery)
	}
	if err := abilityQuery.Find(&abilities).Error; err != nil {
		return state, err
	}
	candidates, err := listClientModelCandidates(tx)
	if err != nil {
		return state, err
	}
	chatModels := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate.ChatEligible {
			chatModels[candidate.ID] = true
		}
	}
	var metadata []Model
	if err := tx.Order("id").Find(&metadata).Error; err != nil {
		return state, err
	}
	names := make([]string, 0, len(chatModels))
	for name := range chatModels {
		names = append(names, name)
	}
	metadataByModel := resolveModelMetadata(metadata, names)
	plugins := jsplugin.DefaultRegistry.Snapshot()
	taskModels := make(map[string]struct{})
	for _, plugin := range append(plugins.Factory, plugins.Override...) {
		for _, name := range plugin.Models {
			taskModels[strings.ToLower(name)] = struct{}{}
		}
	}
	byChannel := make(map[int]Channel, len(channels))
	for _, channel := range channels {
		byChannel[channel.Id] = channel
		state.Hashes[channel.Id] = CapacityChannelConfigHash(&channel)
	}
	configured := make(map[string]CapacityRoute, len(state.Config.Routes))
	for _, route := range state.Config.Routes {
		configured[capacityRouteKey(route)] = route
	}
	state.Catalog.Routes = make([]CapacityCatalogRoute, 0)
	// Only the opaque digest incorporates private channel identities. The public
	// catalog exposes readable routing identities and administrator-entered limits.
	type revisionRoute struct {
		Model                string
		Group                string
		ChannelID            int
		ChannelName          string
		Hash                 string
		RequiresFullCapacity bool
	}
	revisionRoutes := make([]revisionRoute, 0)
	for _, ability := range abilities {
		channel, enabled := byChannel[ability.ChannelId]
		if !enabled || !chatModels[ability.Model] || !slices.Contains(channel.GetModels(), ability.Model) || !slices.Contains(channel.GetGroups(), ability.Group) {
			continue
		}
		if !clientModelSupportsChat(channel, ability.Model, metadataByModel[ability.Model], taskModels) {
			continue
		}
		key := CapacityRoute{Model: ability.Model, Group: ability.Group, ChannelID: ability.ChannelId, RouteModel: ability.Model}
		row := CapacityCatalogRoute{Model: key.Model, Group: key.Group, ChannelID: key.ChannelID, ChannelName: channel.Name, RouteModel: key.RouteModel, BudgetField: "max_tokens", RequiresFullCapacity: requiresFullModelCapacity(key.Model)}
		if prior, exists := configured[capacityRouteKey(key)]; exists {
			row.ContextWindow, row.MaxOutputTokens = prior.ContextWindow, prior.MaxOutputTokens
			row.BudgetField, row.Source, row.VerifiedAt = prior.BudgetField, prior.Source, prior.VerifiedAt
			row.Configured = true
			row.Valid = prior.ChannelConfigHash == state.Hashes[channel.Id]
		}
		state.Catalog.Routes = append(state.Catalog.Routes, row)
		revisionRoutes = append(revisionRoutes, revisionRoute{Model: key.Model, Group: key.Group, ChannelID: key.ChannelID, ChannelName: channel.Name, Hash: state.Hashes[channel.Id], RequiresFullCapacity: row.RequiresFullCapacity})
	}
	encoded, err := common.Marshal([]any{state.Raw, revisionRoutes})
	if err != nil {
		return state, err
	}
	state.Catalog.Revision = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return state, nil
}

// PatchModelCapacity updates only explicitly selected routes. Other route JSON
// and unknown extension fields retain their bytes and their original evidence.
func PatchModelCapacity(patch ModelCapacityPatch) (ModelCapacityCatalog, error) {
	if len(patch.Revision) != 64 || len(patch.Routes) == 0 || len(patch.Routes) > 10000 {
		return ModelCapacityCatalog{}, errors.New("Invalid model capacity changes")
	}
	if err := validateModelCapacityConfig(ModelCapacityConfig{Schema: 1, Routes: patch.Routes}); err != nil {
		return ModelCapacityCatalog{}, err
	}
	modelCapacityMutationMu.Lock()
	defer modelCapacityMutationMu.Unlock()
	var persisted string
	err := DB.Transaction(func(tx *gorm.DB) error {
		row := Option{Key: ModelCapacityOptionKey, Value: `{"schema":1,"routes":[]}`}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		state, err := readCapacityCatalogState(tx, true)
		if err != nil {
			return err
		}
		if state.Catalog.Revision != patch.Revision {
			return ErrModelCapacityConflict
		}
		available := make(map[string]bool, len(state.Catalog.Routes))
		for _, route := range state.Catalog.Routes {
			available[capacityRouteKey(CapacityRoute{Model: route.Model, Group: route.Group, ChannelID: route.ChannelID, RouteModel: route.RouteModel})] = true
		}
		indexes := make(map[string]int, len(state.Config.Routes))
		for i, route := range state.Config.Routes {
			indexes[capacityRouteKey(route)] = i
		}
		persisted = state.Raw
		if state.Config.Routes == nil {
			persisted, err = sjson.SetRaw(persisted, "routes", "[]")
			if err != nil {
				return err
			}
		}
		for _, change := range patch.Routes {
			key := capacityRouteKey(change)
			if !available[key] {
				return errors.New("The selected chat model route is unavailable")
			}
			change.ChannelConfigHash = state.Hashes[change.ChannelID]
			index, exists := indexes[key]
			if !exists {
				index = len(state.Config.Routes)
				state.Config.Routes = append(state.Config.Routes, change)
				indexes[key] = index
			} else {
				if change.ContextWindow == nil {
					change.ContextWindow = state.Config.Routes[index].ContextWindow
				}
				if change.MaxOutputTokens == nil {
					change.MaxOutputTokens = state.Config.Routes[index].MaxOutputTokens
				}
				state.Config.Routes[index] = change
			}
			if requiresFullModelCapacity(change.Model) && (change.ContextWindow == nil || change.MaxOutputTokens == nil) {
				return errors.New("This model requires both context window and maximum output tokens")
			}
			encoded, err := common.Marshal(change)
			if err != nil {
				return err
			}
			var fields map[string]common.RawMessage
			if err := common.Unmarshal(encoded, &fields); err != nil {
				return err
			}
			for field, value := range fields {
				persisted, err = sjson.SetRaw(persisted, "routes."+strconv.Itoa(index)+"."+field, string(value))
				if err != nil {
					return err
				}
			}
		}
		if err := validateModelCapacityConfig(state.Config); err != nil {
			return err
		}
		if persisted == state.Raw {
			return nil
		}
		result := tx.Model(&Option{}).Where(&Option{Key: ModelCapacityOptionKey, Value: state.Raw}).Update("value", persisted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelCapacityConflict
		}
		return nil
	})
	if err != nil {
		return ModelCapacityCatalog{}, err
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[ModelCapacityOptionKey] = persisted
	common.OptionMapRWMutex.Unlock()
	return ReadModelCapacityCatalog()
}
