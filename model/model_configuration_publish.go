package model

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Publish applies only explicitly selected saved drafts. The existing price
// maps, exact routes and capacity declarations commit in the same transaction.
// A rollback is another publication, never a database or ledger restoration.
func PublishModelConfigurations(revision string, names []string, rollback string) error {
	if len(names) > 128 || (rollback == "" && len(names) == 0) || (rollback != "" && len(names) != 0) {
		return errors.New("Select models to publish")
	}
	var committed, capacityRaw string
	return mutateModelPricingOptions(func(tx *gorm.DB, prices map[string]map[string]any) error {
		doc, raw, err := readModelConfigurationDocument(tx, true)
		if err != nil {
			return err
		}
		if modelConfigurationDigest(raw) != revision {
			return ErrModelConfigurationConflict
		}
		definitions := []ModelConfiguration{}
		if rollback != "" {
			record, err := readModelConfigurationHistory(tx, rollback)
			if err != nil {
				return err
			}
			definitions = append(definitions, record.Definition)
		} else {
			seen := map[string]bool{}
			for _, name := range names {
				draft, ok := doc.Drafts[name]
				if !ok || seen[name] {
					return errors.New("Select distinct saved model drafts")
				}
				seen[name] = true
				entry, err := currentModelConfigurationEntry(tx, name, doc, prices)
				if err != nil {
					return err
				}
				routeVersion, err := modelConfigurationRouteVersion(tx, draft.Definition.Routes)
				if err != nil {
					return err
				}
				if draft.BaseVersion != entry.BaseVersion || draft.RouteVersion != routeVersion {
					return ErrModelConfigurationConflict
				}
				definitions = append(definitions, draft.Definition)
			}
		}
		var capacity Option
		capacity.Key = ModelCapacityOptionKey
		capacity.Value = `{"schema":1,"routes":[]}`
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&capacity).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where(&Option{Key: ModelCapacityOptionKey}).Take(&capacity).Error; err != nil {
			return err
		}
		capacityRaw = capacity.Value
		for _, def := range definitions {
			inheritMissingTextRouteProtocols(&def, doc.Published[def.ModelName].Definition)
			if def.OfficialReferences == nil {
				def.OfficialReferences = doc.Published[def.ModelName].Definition.OfficialReferences
			}
			if err := validateModelConfiguration(def, modelPricingValues(prices, def.ModelName)); err != nil {
				return err
			}
			if def.Enabled && !modelConfigurationHasPrice(def.Pricing) {
				return errors.New("An enabled model requires an explicit billing price or expression")
			}
			var err error
			capacityRaw, err = applyModelConfigurationRoutes(tx, def, capacityRaw)
			if err != nil {
				return err
			}
			replaceModelPricing(prices, def.ModelName, def.Pricing)
			if err := appendModelConfigurationCosts(tx, def); err != nil {
				return err
			}
			doc.Sequence++
			publication := modelConfigurationPublication(def, doc.Sequence)
			if err := saveModelConfigurationHistory(tx, publication); err != nil {
				return err
			}
			doc.Published[def.ModelName] = publication
			delete(doc.Drafts, def.ModelName)
			doc.History = append(doc.History, ModelConfigurationHistory{def.ModelName, publication.Version, publication.PublishedAt})
		}
		if len(doc.History) > 512 {
			doc.History = doc.History[len(doc.History)-512:]
		}
		if err := tx.Model(&Option{}).Where(&Option{Key: ModelCapacityOptionKey}).Update("value", capacityRaw).Error; err != nil {
			return err
		}
		committed, err = writeModelConfigurationDocument(tx, doc, raw)
		return err
	}, func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap[ModelCapacityOptionKey] = capacityRaw
		common.OptionMapRWMutex.Unlock()
		publishModelConfigurationCache(committed)
		InitChannelCache()
	})
}

func modelConfigurationHasPrice(pricing PricingValues) bool {
	for _, key := range []string{"ModelPrice", "ModelRatio", "billing_setting.billing_expr", billing_setting.PluginBillingExprOption} {
		value, ok := pricing[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		if variants, ok := value.(map[string]any); ok && len(variants) == 0 {
			continue
		}
		return true
	}
	return false
}

func readModelConfigurationHistory(db *gorm.DB, version string) (PublishedModelConfiguration, error) {
	var record PublishedModelConfiguration
	if len(version) != 64 || strings.Trim(version, "0123456789abcdef") != "" {
		return record, errors.New("Invalid model configuration version")
	}
	var option Option
	if err := db.Where(&Option{Key: ModelConfigurationHistoryPrefix + version}).Take(&option).Error; err != nil {
		return record, err
	}
	if option.Key != ModelConfigurationHistoryPrefix+version || common.UnmarshalJsonStr(option.Value, &record) != nil || record.Version != version {
		return record, ErrModelConfigurationUnavailable
	}
	return record, nil
}

func applyModelConfigurationRoutes(tx *gorm.DB, def ModelConfiguration, raw string) (string, error) {
	var document map[string]common.RawMessage
	var routes []common.RawMessage
	if common.UnmarshalJsonStr(raw, &document) != nil || common.Unmarshal(document["routes"], &routes) != nil {
		return raw, ErrModelConfigurationUnavailable
	}
	hashes := map[int][2]string{}
	enabled := false
	for _, route := range def.Routes {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, route.ChannelID).Error; err != nil {
			return raw, errors.New("Model route channel does not exist")
		}
		if !slices.Contains(channel.GetGroups(), route.Group) {
			return raw, errors.New("Model route group is not configured on its channel")
		}
		if route.TextProtocol == "ycapi_fixed_lane" && channel.Type != constant.ChannelTypeOpenAI {
			return raw, errors.New("YCAPI fixed-lane text requires an OpenAI channel adapter")
		}
		if def.Enabled && route.Enabled && channel.Status == common.ChannelStatusEnabled {
			enabled = true
		}
		before := CapacityChannelConfigHash(&channel)
		h3Before := h3ConfigurationDigest(&channel)
		mapping := map[string]string{}
		if channel.ModelMapping != nil && *channel.ModelMapping != "" && common.UnmarshalJsonStr(*channel.ModelMapping, &mapping) != nil {
			return raw, errors.New("Invalid existing channel model mapping")
		}
		if previous, exists := mapping[def.ModelName]; exists && previous != route.UpstreamModel && slices.ContainsFunc(def.Routes, func(other ModelConfigurationRoute) bool {
			return other.ChannelID == route.ChannelID && other.UpstreamModel != route.UpstreamModel
		}) {
			return raw, errors.New("A channel must use one upstream mapping per public model")
		}
		// A new media ID must resolve to an already integrated host adapter. Schema
		// editing cannot invent a provider protocol or receipt recovery guarantee.
		if def.Enabled && route.Enabled && def.Kind != "text" {
			probe := channel
			nativeImage, nativeVideo := clientModelMediaEligibility(probe, route.UpstreamModel)
			directImage, directVideo := clientModelMediaEligibility(probe, def.ModelName)
			if (def.Kind == "image" && !nativeImage && !directImage) || (def.Kind == "video" && !nativeVideo && !directVideo) {
				return raw, errors.New("Integrate a compatible media adapter before enabling this model")
			}
		} else if def.Enabled && route.Enabled && def.Kind == "text" && (!clientModelSupportsChat(channel, def.ModelName, nil, map[string]struct{}{}) || len(jsplugin.DefaultRegistry.Generation().PluginsByModel(def.ModelName)) > 0) {
			return raw, errors.New("A task model cannot be configured as a text model")
		}
		if !slices.Contains(channel.GetModels(), def.ModelName) {
			if channel.Models != "" {
				channel.Models += ","
			}
			channel.Models += def.ModelName
		}
		if route.UpstreamModel != def.ModelName || mapping[def.ModelName] != "" {
			mapping[def.ModelName] = route.UpstreamModel
			// Keep unrelated mapping members, including extensions unknown to this UI.
			original := "{}"
			if channel.ModelMapping != nil && *channel.ModelMapping != "" {
				original = *channel.ModelMapping
			}
			escaped := strings.NewReplacer("\\", "\\\\", ".", "\\.", ":", "\\:", "*", "\\*", "?", "\\?").Replace(def.ModelName)
			changed, err := sjson.Set(original, escaped, route.UpstreamModel)
			if err != nil {
				return raw, err
			}
			channel.ModelMapping = &changed
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{"models": channel.Models, "model_mapping": channel.ModelMapping}).Error; err != nil {
			return raw, err
		}
		after := CapacityChannelConfigHash(&channel)
		// Only model-list/mapping changes are owned here. Rebind a previously
		// valid H3 account to that exact controlled edit without touching its
		// key, occupancy, reconciliation or admission history.
		h3After := h3ConfigurationDigest(&channel)
		if h3Before != h3After {
			if err := tx.Model(&H3Account{}).Where("channel_id = ? AND credential_digest = ? AND configuration_digest = ?", channel.Id, h3CredentialDigest(channel.Key), h3Before).Update("configuration_digest", h3After).Error; err != nil {
				return raw, err
			}
		}
		if first, ok := hashes[channel.Id]; ok {
			before = first[0]
		}
		hashes[channel.Id] = [2]string{before, after}
		ability := Ability{Model: def.ModelName, Group: route.Group, ChannelId: channel.Id, Enabled: def.Enabled && route.Enabled, Priority: channel.Priority, Tag: channel.Tag}
		if channel.Weight != nil {
			ability.Weight = *channel.Weight
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group"}, {Name: "model"}, {Name: "channel_id"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled"})}).Create(&ability).Error; err != nil {
			return raw, err
		}
	}
	if def.Enabled && !enabled {
		return raw, errors.New("An enabled model requires an active channel")
	}
	// Disable only the edited model's omitted routes. Other model abilities stay
	// byte-for-byte untouched, including manual upstream delistings.
	var abilities []Ability
	if err := tx.Where("model = ?", def.ModelName).Find(&abilities).Error; err != nil {
		return raw, err
	}
	for _, ability := range abilities {
		if slices.ContainsFunc(def.Routes, func(route ModelConfigurationRoute) bool {
			return route.ChannelID == ability.ChannelId && route.Group == ability.Group
		}) {
			continue
		}
		if err := tx.Model(&Ability{}).Where(&Ability{Model: def.ModelName, Group: ability.Group, ChannelId: ability.ChannelId}).Update("enabled", false).Error; err != nil {
			return raw, err
		}
	}
	result := []common.RawMessage{}
	for _, encoded := range routes {
		var route CapacityRoute
		if common.Unmarshal(encoded, &route) != nil {
			return raw, ErrModelConfigurationUnavailable
		}
		if route.Model == def.ModelName {
			if def.Kind == "text" && !def.Enabled && def.ContextWindow == nil && def.MaxOutputTokens == nil {
				result = append(result, encoded)
			}
			continue
		}
		if hash, ok := hashes[route.ChannelID]; ok && hash[0] != hash[1] && route.ChannelConfigHash == hash[0] {
			changed, err := sjson.SetBytes(encoded, "channel_config_hash", hash[1])
			if err != nil {
				return raw, err
			}
			encoded = changed
		}
		result = append(result, encoded)
	}
	if def.Kind == "text" && def.ContextWindow != nil && def.MaxOutputTokens != nil {
		for _, route := range def.Routes {
			declaration := CapacityRoute{Model: def.ModelName, Group: route.Group, ChannelID: route.ChannelID, RouteModel: def.ModelName, ContextWindow: def.ContextWindow, MaxOutputTokens: def.MaxOutputTokens, BudgetField: def.BudgetField, Source: def.Source, VerifiedAt: time.Now().UTC().Format(time.RFC3339), ChannelConfigHash: hashes[route.ChannelID][1]}
			encoded, err := common.Marshal(declaration)
			if err != nil {
				return raw, err
			}
			result = append(result, encoded)
		}
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		return raw, err
	}
	document["routes"] = encoded
	encoded, err = common.Marshal(document)
	return string(encoded), err
}

func appendModelConfigurationCosts(tx *gorm.DB, def ModelConfiguration) error {
	if len(def.CostRules) == 0 {
		return nil
	}
	option := Option{Key: ModelProfitCostRulesKey, Value: `{"schema_version":1,"revision":0,"rules":[]}`}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
		return err
	}
	if err := lockForUpdate(tx).Where(&Option{Key: ModelProfitCostRulesKey}).Take(&option).Error; err != nil {
		return err
	}
	var costs ModelProfitCostRules
	if common.UnmarshalJsonStr(option.Value, &costs) != nil {
		return ErrModelProfitParameters
	}
	changed := false
	for _, rule := range def.CostRules {
		candidate := rule
		candidate.EffectiveFrom = 0
		candidate.EffectiveUntil = nil
		latest := int64(-1)
		var prior *ModelProfitCostRule
		for i := range costs.Rules {
			old := &costs.Rules[i]
			if old.ModelName == rule.ModelName && old.ChannelID == rule.ChannelID && old.EffectiveFrom > latest {
				latest = old.EffectiveFrom
				prior = old
			}
		}
		if prior != nil {
			normalized := *prior
			normalized.EffectiveFrom = 0
			normalized.EffectiveUntil = nil
			if reflect.DeepEqual(normalized, candidate) {
				continue
			}
		}
		candidate.EffectiveFrom = time.Now().Unix()
		if candidate.EffectiveFrom <= latest {
			return fmt.Errorf("Cost change must follow the previous effective date for %s", rule.ModelName)
		}
		costs.Rules = append(costs.Rules, candidate)
		changed = true
	}
	if !changed {
		return nil
	}
	costs.Revision++
	if err := ValidateModelProfitCostRules(costs); err != nil {
		return err
	}
	encoded, err := common.Marshal(costs)
	if err != nil || len(encoded) > MaxModelProfitCostRulesBytes {
		return ErrModelProfitParameters
	}
	return tx.Model(&Option{}).Where(&Option{Key: ModelProfitCostRulesKey}).Update("value", string(encoded)).Error
}
