package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelConfigurationKey = "DynamicModelConfiguration"
const ModelConfigurationHistoryPrefix = "ModelConfigurationHistory_"
const MaxModelConfigurationBytes = 4 << 20

var ErrModelConfigurationConflict = errors.New("Model configuration changed. Reload before saving")
var ErrModelConfigurationUnavailable = errors.New("Model configuration is unavailable")
var ErrModelConfigurationDedicatedAPI = errors.New("Use the unified model configuration page to change this setting")

// The lock covers local configuration publication and billing-snapshot capture,
// never provider requests, task polling or result delivery.
var modelConfigurationMu sync.RWMutex

func ModelConfigurationReadLock() func() {
	modelConfigurationMu.RLock()
	return modelConfigurationMu.RUnlock
}

func isModelExecutionGlobalOption(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return slices.Contains([]string{"groupratio", "groupgroupratio", "quotaperunit", "usdexchangerate", "displayincurrencyenabled"}, key) || strings.HasPrefix(key, "group_ratio_setting.") || strings.HasPrefix(key, "general_setting.")
}

type ModelConfigurationRoute struct {
	ChannelID     int    `json:"channel_id"`
	Group         string `json:"group"`
	UpstreamModel string `json:"upstream_model"`
	Enabled       bool   `json:"enabled"`
	TextProtocol  string `json:"text_protocol,omitempty"`
}

// Pricing is the existing engine's complete editable draft. Parameters contain
// generation controls only, never a transport or a second pricing language.
type ModelConfiguration struct {
	ModelName           string                               `json:"model_name"`
	Kind                string                               `json:"kind"`
	DisplayName         string                               `json:"display_name"`
	Enabled             bool                                 `json:"enabled"`
	Routes              []ModelConfigurationRoute            `json:"routes"`
	ContextWindow       *int                                 `json:"context_window,omitempty"`
	MaxOutputTokens     *int                                 `json:"max_output_tokens,omitempty"`
	DefaultOutputTokens *int                                 `json:"default_output_tokens,omitempty"`
	BudgetField         string                               `json:"budget_field,omitempty"`
	Parameters          map[string]any                       `json:"parameters,omitempty"`
	Pricing             PricingValues                        `json:"pricing"`
	CostRules           []ModelProfitCostRule                `json:"cost_rules,omitempty"`
	OfficialReferences  *ModelOfficialReferenceConfiguration `json:"official_references,omitempty"`
	Source              string                               `json:"source"`
}

type PublishedModelConfiguration struct {
	Version     string             `json:"version"`
	PublishedAt int64              `json:"published_at"`
	Definition  ModelConfiguration `json:"definition"`
}

type ModelConfigurationDraft struct {
	RouteVersion string             `json:"route_version"`
	BaseVersion  string             `json:"base_version"`
	Definition   ModelConfiguration `json:"definition"`
}

type ModelConfigurationHistory struct {
	ModelName   string `json:"model_name"`
	Version     string `json:"version"`
	PublishedAt int64  `json:"published_at"`
}

type ModelConfigurationDocument struct {
	SchemaVersion int                                    `json:"schema_version"`
	Sequence      int64                                  `json:"sequence"`
	Published     map[string]PublishedModelConfiguration `json:"published"`
	Drafts        map[string]ModelConfigurationDraft     `json:"drafts"`
	History       []ModelConfigurationHistory            `json:"history"`
}

type ModelConfigurationEntry struct {
	ModelName               string                       `json:"model_name"`
	BaseVersion             string                       `json:"base_version"`
	Definition              ModelConfiguration           `json:"definition"`
	Published               *PublishedModelConfiguration `json:"published,omitempty"`
	Draft                   *ModelConfigurationDraft     `json:"draft,omitempty"`
	LegacyParameters        map[string]any               `json:"legacy_parameters,omitempty"`
	ReferenceSpecifications []ModelOfficialReferenceRow  `json:"reference_specifications,omitempty"`
}

type ModelConfigurationChannel struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Type   int      `json:"type"`
	Status int      `json:"status"`
	Groups []string `json:"groups"`
}

type ModelConfigurationSnapshot struct {
	SchemaVersion int                         `json:"schema_version"`
	Revision      string                      `json:"revision"`
	Entries       []ModelConfigurationEntry   `json:"entries"`
	Channels      []ModelConfigurationChannel `json:"channels"`
	History       []ModelConfigurationHistory `json:"history"`
}

func emptyModelConfigurationDocument() ModelConfigurationDocument {
	return ModelConfigurationDocument{SchemaVersion: 1, Published: map[string]PublishedModelConfiguration{}, Drafts: map[string]ModelConfigurationDraft{}, History: []ModelConfigurationHistory{}}
}

func modelConfigurationDigest(value any) string {
	encoded, _ := common.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func readModelConfigurationDocument(db *gorm.DB, locked bool) (ModelConfigurationDocument, string, error) {
	doc := emptyModelConfigurationDocument()
	empty, _ := common.Marshal(doc)
	query := db.Where(&Option{Key: ModelConfigurationKey})
	if locked {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: ModelConfigurationKey, Value: string(empty)}).Error; err != nil {
			return doc, "", err
		}
		query = lockForUpdate(query)
	}
	var row Option
	if err := query.Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return doc, string(empty), nil
	} else if err != nil {
		return doc, "", err
	}
	if row.Key != ModelConfigurationKey || len(row.Value) > MaxModelConfigurationBytes || common.UnmarshalJsonStr(row.Value, &doc) != nil || doc.SchemaVersion != 1 || doc.Published == nil || doc.Drafts == nil || doc.Sequence < 0 {
		return doc, "", ErrModelConfigurationUnavailable
	}
	return doc, row.Value, nil
}

// CurrentPublishedModelConfigurations reads the same immutable option value as
// existing settings. A corrupt declared resource fails closed, not to defaults.
func CurrentPublishedModelConfigurations() (map[string]PublishedModelConfiguration, error) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[ModelConfigurationKey]
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return map[string]PublishedModelConfiguration{}, nil
	}
	var doc ModelConfigurationDocument
	if len(raw) > MaxModelConfigurationBytes || common.UnmarshalJsonStr(raw, &doc) != nil || doc.SchemaVersion != 1 || doc.Published == nil {
		return nil, ErrModelConfigurationUnavailable
	}
	return doc.Published, nil
}

// Version inputs use canonical common JSON (sorted map keys), actual legacy
// engine values and relevant global multipliers. Drafts and cost evidence do
// not leak into public data. The digest remains stable across process restarts.
func ModelExecutionConfigurationVersion(name string, record PublishedModelConfiguration) string {
	return ModelExecutionConfigurationVersions(map[string]PublishedModelConfiguration{name: record})[name]
}

// Capture the routing and pricing inputs once per catalog request. This avoids
// per-model SQL and repeated JSON decoding without a stale cross-request cache.
// Admission still captures a fresh version through the single-model wrapper.
func ModelExecutionConfigurationVersions(records map[string]PublishedModelConfiguration) map[string]string {
	versions := make(map[string]string, len(records))
	if len(records) == 0 {
		return versions
	}
	common.OptionMapRWMutex.RLock()
	options := make(map[string]string)
	for _, key := range append(append([]string{}, modelPricingOptionKeys...), ModelCapacityOptionKey, "GroupRatio", "GroupGroupRatio", "QuotaPerUnit", "USDExchangeRate", "DisplayInCurrencyEnabled", "group_ratio_setting.group_ratio", "group_ratio_setting.group_group_ratio", "console_setting.usd_exchange_rate") {
		options[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.RUnlock()
	pricing := defaultPricingMaps()
	for _, key := range modelPricingOptionKeys {
		if options[key] == "" {
			continue
		}
		var value map[string]any
		if common.UnmarshalJsonStr(options[key], &value) == nil && value != nil {
			pricing[key] = value
		}
		delete(options, key)
	}
	var capacities ModelCapacityConfig
	_ = common.UnmarshalJsonStr(options[ModelCapacityOptionKey], &capacities)
	delete(options, ModelCapacityOptionKey)
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	abilitiesByModel := make(map[string][]Ability)
	channels := make(map[int]Channel)
	if DB != nil {
		var abilities []Ability
		if DB.Where("model IN ?", names).Order(commonGroupCol+", channel_id").Find(&abilities).Error != nil {
			return versions
		}
		ids := []int{}
		for _, ability := range abilities {
			abilitiesByModel[ability.Model] = append(abilitiesByModel[ability.Model], ability)
			ids = append(ids, ability.ChannelId)
		}
		if len(ids) > 0 {
			slices.Sort(ids)
			ids = slices.Compact(ids)
			var rows []Channel
			if DB.Where("id IN ?", ids).Find(&rows).Error != nil {
				return versions
			}
			for _, channel := range rows {
				channels[channel.Id] = channel
			}
		}
	}
	for name, record := range records {
		selected := make([]CapacityRoute, 0)
		for _, route := range capacities.Routes {
			if route.Model == name {
				selected = append(selected, route)
			}
		}
		plugins := make(map[string]string)
		generation := jsplugin.DefaultRegistry.Generation()
		declaredModels := []string{name}
		for _, route := range record.Definition.Routes {
			declaredModels = append(declaredModels, route.UpstreamModel)
		}
		for _, declared := range declaredModels {
			for _, plugin := range generation.PluginsByModel(declared) {
				plugins[plugin.Meta.Key] = plugin.Meta.Version + ":" + plugin.SourceHash
			}
		}
		var routeGraph []string
		for _, ability := range abilitiesByModel[name] {
			if ch, ok := channels[ability.ChannelId]; ok {
				routeGraph = append(routeGraph, fmt.Sprintf("%s:%t:%d:%s", ability.Group, ability.Enabled, ch.Status, CapacityChannelConfigHash(&ch)))
			}
		}
		versions[name] = modelConfigurationDigest(struct {
			Routes          []string
			QuotaPerUnit    float64
			USDExchangeRate float64
			Currency        string
			GroupRatio      string
			GroupGroupRatio string
			Version         string
			Pricing         PricingValues
			Capacities      []CapacityRoute
			Globals         map[string]string
			Plugins         map[string]string
		}{routeGraph, common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.GetQuotaDisplayType(), ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString(), record.Version, effectiveModelPricing(pricing, name), selected, options, plugins})
	}
	return versions
}

func ModelConfigurationRevisionFromVersions(versions map[string]string) string {
	if len(versions) == 0 {
		return ""
	}
	return modelConfigurationDigest(versions)
}

func ModelConfigurationRevision(records map[string]PublishedModelConfiguration) string {
	return ModelConfigurationRevisionFromVersions(ModelExecutionConfigurationVersions(records))
}

func validateModelConfiguration(def ModelConfiguration, previous PricingValues) error {
	if !validClientModelID(def.ModelName) || len(def.ModelName) > 128 || !slices.Contains([]string{"text", "image", "video"}, def.Kind) || def.DisplayName == "" || len(def.DisplayName) > 200 || strings.TrimSpace(def.DisplayName) != def.DisplayName || strings.ContainsAny(def.DisplayName, "\r\n\x00") || def.Pricing == nil || def.Source == "" || len(def.Source) > 512 || len(def.Routes) > 128 {
		return errors.New("Invalid unified model configuration")
	}
	nativeModels := []string{}
	if def.Kind != "text" {
		for _, route := range def.Routes {
			nativeModels = append(nativeModels, route.UpstreamModel)
		}
	}
	if err := validateModelPricing(def.ModelName, def.Pricing, previous, nativeModels...); err != nil {
		return err
	}
	if err := validateOfficialReferenceConfiguration(def.Kind, def.OfficialReferences); err != nil {
		return err
	}
	if def.Kind == "text" {
		if def.Parameters != nil {
			return errors.New("Text models use context and output limits instead of generation parameters")
		}
		if def.Enabled || def.ContextWindow != nil || def.MaxOutputTokens != nil || def.DefaultOutputTokens != nil {
			if def.ContextWindow == nil || def.MaxOutputTokens == nil {
				return errors.New("Text models require context window and maximum output tokens")
			}
			if err := validateModelCapacityConfig(ModelCapacityConfig{Schema: 1, Routes: []CapacityRoute{{Model: def.ModelName, Group: "default", ChannelID: 1, RouteModel: def.ModelName, ContextWindow: def.ContextWindow, MaxOutputTokens: def.MaxOutputTokens, DefaultOutputTokens: def.DefaultOutputTokens, BudgetField: def.BudgetField, Source: def.Source, VerifiedAt: "administrator declaration"}}}); err != nil {
				return err
			}
		}
	} else if def.ContextWindow != nil || def.MaxOutputTokens != nil || def.DefaultOutputTokens != nil {
		return errors.New("Image and video models use generation parameters instead of token capacities")
	}
	if err := ValidateModelParameterSchema(def.Kind, def.Parameters); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, route := range def.Routes {
		key := fmt.Sprintf("%d:%s", route.ChannelID, route.Group)
		if route.ChannelID <= 0 || route.Group == "" || len(route.Group) > 64 || !validClientModelID(route.UpstreamModel) || seen[key] {
			return errors.New("Invalid or duplicate model route")
		}
		seen[key] = true
		if route.TextProtocol != "" && (def.Kind != "text" || !slices.Contains([]string{"legacy", "openai", "ycapi_fixed_lane"}, route.TextProtocol)) {
			return errors.New("Select a supported text route protocol")
		}
		if route.TextProtocol == "ycapi_fixed_lane" {
			approved, ok := ycapi.Lookup(route.UpstreamModel)
			if !ok {
				approved, ok = ycapi.Lookup("ycapi-" + route.UpstreamModel)
			}
			if !ok || approved.Kind != "text" {
				return errors.New("Select an integrated YCAPI fixed-lane text model")
			}
		}
	}
	if def.Enabled && !slices.ContainsFunc(def.Routes, func(route ModelConfigurationRoute) bool { return route.Enabled }) {
		return errors.New("An enabled model requires an enabled route")
	}
	for _, cost := range def.CostRules {
		if cost.ModelName != def.ModelName || !slices.ContainsFunc(def.Routes, func(route ModelConfigurationRoute) bool { return route.ChannelID == cost.ChannelID }) {
			return errors.New("Cost rule does not match a configured model route")
		}
		if err := ValidateModelProfitCostRules(ModelProfitCostRules{SchemaVersion: 1, Rules: []ModelProfitCostRule{cost}}); err != nil {
			return err
		}
	}
	return nil
}

func modelConfigurationBaseVersion(def ModelConfiguration, record *PublishedModelConfiguration, routes []string) string {
	return modelConfigurationDigest(struct {
		Routes     []string
		Definition ModelConfiguration
		Published  *PublishedModelConfiguration
	}{routes, def, record})
}

// Bind saved drafts to the exact selected channels, including identity changes
// hidden from the public catalog. Only a digest leaves this layer.
func modelConfigurationRouteVersion(tx *gorm.DB, routes []ModelConfigurationRoute) (string, error) {
	selected := map[int]string{}
	for _, route := range routes {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, route.ChannelID).Error; err != nil {
			return "", errors.New("Model route channel does not exist")
		}
		selected[channel.Id] = fmt.Sprintf("%d:%s", channel.Status, CapacityChannelConfigHash(&channel))
	}
	return modelConfigurationDigest(selected), nil
}

func currentModelConfigurationEntry(db *gorm.DB, name string, doc ModelConfigurationDocument, prices map[string]map[string]any) (ModelConfigurationEntry, error) {
	entry := ModelConfigurationEntry{ModelName: name, Definition: ModelConfiguration{ModelName: name, Kind: "text", DisplayName: ClientModelDisplayName(name), Routes: []ModelConfigurationRoute{}, Pricing: modelPricingValues(prices, name), BudgetField: "max_tokens", Source: ""}}
	if record, exists := doc.Published[name]; exists {
		entry.Published = &record
		entry.Definition = record.Definition
		entry.Definition.Pricing = modelPricingValues(prices, name)
	}
	if draft, exists := doc.Drafts[name]; exists {
		entry.Draft = &draft
	}
	var abilities []Ability
	if err := db.Where("model = ?", name).Order(commonGroupCol + ", channel_id").Find(&abilities).Error; err != nil {
		return entry, err
	}
	previousRoutes := entry.Definition.Routes
	entry.Definition.Routes = []ModelConfigurationRoute{}
	var routeEvidence []string
	for _, ability := range abilities {
		var channel Channel
		if err := db.Where("id = ?", ability.ChannelId).Take(&channel).Error; err != nil {
			continue
		}
		routeEvidence = append(routeEvidence, fmt.Sprintf("%d:%d:%s", channel.Id, channel.Status, CapacityChannelConfigHash(&channel)))
		mapping := map[string]string{}
		if channel.ModelMapping != nil {
			_ = common.UnmarshalJsonStr(*channel.ModelMapping, &mapping)
		}
		upstream := mapping[name]
		if upstream == "" {
			upstream = name
		}
		route := ModelConfigurationRoute{ChannelID: channel.Id, Group: ability.Group, UpstreamModel: upstream, Enabled: ability.Enabled}
		for _, previous := range previousRoutes {
			if previous.ChannelID == route.ChannelID && previous.Group == route.Group {
				route.TextProtocol = previous.TextProtocol
				break
			}
		}
		entry.Definition.Routes = append(entry.Definition.Routes, route)
		if entry.Published == nil && ability.Enabled && channel.Status == common.ChannelStatusEnabled {
			entry.Definition.Enabled = true
		}
	}
	if entry.Published == nil {
		if candidate, ok := ycapi.Lookup(name); ok {
			entry.Definition.Kind = candidate.Kind
		}
		if candidate, ok := codebloom.Lookup(name); ok {
			entry.Definition.Kind = candidate.Kind
		}
	}
	for _, plugin := range jsplugin.DefaultRegistry.Generation().PluginsByModel(name) {
		if entry.Published == nil {
			kind := TaskMediaKind(&Task{Platform: constant.TaskPlatform(plugin.Meta.Key), Properties: Properties{OriginModelName: name, UpstreamModelName: name}})
			if kind == "image" || kind == "video" {
				entry.Definition.Kind = kind
			}
		}
		parameters := taskPluginParameterSchema(plugin.Meta.Key, plugin.Meta.Version, name)
		if parameters == nil {
			continue
		}
		entry.LegacyParameters = parameters
		if kind, ok := parameters["x-model-kind"].(string); ok && entry.Published == nil {
			entry.Definition.Kind = kind
		}
		break
	}
	if entry.Definition.Parameters != nil {
		entry.LegacyParameters = entry.Definition.Parameters
	}
	if entry.Definition.Kind == "text" {
		entry.Definition.ContextWindow, entry.Definition.MaxOutputTokens = nil, nil
		entry.Definition.DefaultOutputTokens = nil
		var capacity Option
		if db.Where(&Option{Key: ModelCapacityOptionKey}).Take(&capacity).Error == nil {
			var cfg ModelCapacityConfig
			if common.UnmarshalJsonStr(capacity.Value, &cfg) == nil {
				for _, route := range cfg.Routes {
					if route.Model != name {
						continue
					}
					if entry.Published == nil && entry.Definition.Source == "" {
						entry.Definition.Source = route.Source
						entry.Definition.BudgetField = route.BudgetField
					}
					if route.ContextWindow != nil && (entry.Definition.ContextWindow == nil || *route.ContextWindow < *entry.Definition.ContextWindow) {
						entry.Definition.ContextWindow = route.ContextWindow
					}
					if route.MaxOutputTokens != nil && (entry.Definition.MaxOutputTokens == nil || *route.MaxOutputTokens < *entry.Definition.MaxOutputTokens) {
						entry.Definition.MaxOutputTokens = route.MaxOutputTokens
					}
					if route.DefaultOutputTokens != nil && (entry.Definition.DefaultOutputTokens == nil || *route.DefaultOutputTokens < *entry.Definition.DefaultOutputTokens) {
						entry.Definition.DefaultOutputTokens = route.DefaultOutputTokens
					}
				}
			}
		}
	}
	if entry.Definition.DefaultOutputTokens != nil && entry.Definition.MaxOutputTokens != nil && *entry.Definition.DefaultOutputTokens > *entry.Definition.MaxOutputTokens {
		entry.Definition.DefaultOutputTokens = entry.Definition.MaxOutputTokens
	}
	var costOption Option
	if db.Where(&Option{Key: ModelProfitCostRulesKey}).Take(&costOption).Error == nil {
		var costs ModelProfitCostRules
		if common.UnmarshalJsonStr(costOption.Value, &costs) == nil {
			entry.Definition.CostRules = nil
			for _, cost := range costs.Rules {
				if cost.ModelName == name && cost.EffectiveFrom <= time.Now().Unix() && (cost.EffectiveUntil == nil || *cost.EffectiveUntil > time.Now().Unix()) {
					found := -1
					for i, previous := range entry.Definition.CostRules {
						if previous.ChannelID == cost.ChannelID {
							found = i
							break
						}
					}
					if found < 0 {
						entry.Definition.CostRules = append(entry.Definition.CostRules, cost)
					} else if entry.Definition.CostRules[found].EffectiveFrom < cost.EffectiveFrom {
						entry.Definition.CostRules[found] = cost
					}
				}
			}
		}
	}
	entry.BaseVersion = modelConfigurationBaseVersion(entry.Definition, entry.Published, routeEvidence)
	entry.ReferenceSpecifications = BuiltinModelOfficialReferenceRows(name)
	return entry, nil
}

func GetModelConfigurationSnapshot(name string) (ModelConfigurationSnapshot, error) {
	release := ModelConfigurationReadLock()
	defer release()
	doc, raw, err := readModelConfigurationDocument(DB, false)
	if err != nil {
		return ModelConfigurationSnapshot{}, err
	}
	prices, _, _, err := readModelPricingMaps(DB)
	if err != nil {
		return ModelConfigurationSnapshot{}, err
	}
	var channels []Channel
	if err := DB.Select("id", "name", "type", "status", "models", "group").Order("id").Find(&channels).Error; err != nil {
		return ModelConfigurationSnapshot{}, err
	}
	result := ModelConfigurationSnapshot{SchemaVersion: 1, Revision: modelConfigurationDigest(raw), Entries: []ModelConfigurationEntry{}, Channels: []ModelConfigurationChannel{}, History: doc.History}
	names := make(map[string]bool)
	for _, channel := range channels {
		result.Channels = append(result.Channels, ModelConfigurationChannel{channel.Id, channel.Name, channel.Type, channel.Status, channel.GetGroups()})
		for _, id := range channel.GetModels() {
			names[id] = true
		}
	}
	var metadata []Model
	if err := DB.Select("model_name").Find(&metadata).Error; err != nil {
		return result, err
	}
	for _, row := range metadata {
		names[row.ModelName] = true
	}
	for id := range doc.Published {
		names[id] = true
	}
	for id := range doc.Drafts {
		names[id] = true
	}
	if name != "" {
		if !validClientModelID(name) || len(name) > 128 {
			return result, errors.New("Invalid model name")
		}
		names = map[string]bool{name: true}
	}
	for _, id := range slices.Sorted(maps.Keys(names)) {
		entry, err := currentModelConfigurationEntry(DB, id, doc, prices)
		if err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func writeModelConfigurationDocument(tx *gorm.DB, doc ModelConfigurationDocument, previous string) (string, error) {
	encoded, err := common.Marshal(doc)
	if err != nil || len(encoded) > MaxModelConfigurationBytes || len(doc.Published) > 512 || len(doc.Drafts) > 512 {
		return "", errors.New("Model configuration storage limit reached")
	}
	if string(encoded) == previous {
		return previous, nil
	}
	result := tx.Model(&Option{}).Where(&Option{Key: ModelConfigurationKey}).Where("value = ?", previous).Update("value", string(encoded))
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrModelConfigurationConflict
	}
	return string(encoded), nil
}

func publishModelConfigurationCache(raw string) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[ModelConfigurationKey] = raw
	common.OptionMapRWMutex.Unlock()
}

func SaveModelConfigurationDraft(revision, baseVersion string, def ModelConfiguration) error {
	modelConfigurationMu.Lock()
	defer modelConfigurationMu.Unlock()
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	var committed string
	err := DB.Transaction(func(tx *gorm.DB) error {
		doc, raw, err := readModelConfigurationDocument(tx, true)
		if err != nil {
			return err
		}
		if modelConfigurationDigest(raw) != revision {
			return ErrModelConfigurationConflict
		}
		prices, _, _, err := readModelPricingMaps(tx)
		if err != nil {
			return err
		}
		entry, err := currentModelConfigurationEntry(tx, def.ModelName, doc, prices)
		if err != nil {
			return err
		}
		if entry.BaseVersion != baseVersion {
			return ErrModelConfigurationConflict
		}
		// Older administrators do not send the new optional display-evidence
		// field. Omission preserves it; an explicit inherit/disabled selection
		// is required to remove an override.
		if def.OfficialReferences == nil {
			if entry.Draft != nil {
				def.OfficialReferences = entry.Draft.Definition.OfficialReferences
			} else {
				def.OfficialReferences = entry.Definition.OfficialReferences
			}
		}
		previous := entry.Definition
		if entry.Draft != nil {
			previous = entry.Draft.Definition
		}
		inheritMissingTextRouteProtocols(&def, previous)
		if def.Kind == "text" && def.DefaultOutputTokens == nil && previous.DefaultOutputTokens != nil {
			value := *previous.DefaultOutputTokens
			if def.MaxOutputTokens != nil {
				value = min(value, *def.MaxOutputTokens)
			}
			def.DefaultOutputTokens = common.GetPointer(value)
		}
		if err := validateModelConfiguration(def, entry.Definition.Pricing); err != nil {
			return err
		}
		routeVersion, err := modelConfigurationRouteVersion(tx, def.Routes)
		if err != nil {
			return err
		}
		doc.Drafts[def.ModelName] = ModelConfigurationDraft{BaseVersion: baseVersion, Definition: def, RouteVersion: routeVersion}
		committed, err = writeModelConfigurationDocument(tx, doc, raw)
		return err
	})
	if err == nil {
		publishModelConfigurationCache(committed)
	}
	return err
}

// Omitted optional route fields from old dashboards preserve existing bindings.
// An explicit legacy choice can restore the original selection policy.
func inheritMissingTextRouteProtocols(def *ModelConfiguration, previous ModelConfiguration) {
	for i := range def.Routes {
		if def.Routes[i].TextProtocol != "" {
			continue
		}
		for _, route := range previous.Routes {
			if route.ChannelID == def.Routes[i].ChannelID && route.Group == def.Routes[i].Group {
				def.Routes[i].TextProtocol = route.TextProtocol
				break
			}
		}
	}
}

func isModelConfigurationOption(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == strings.ToLower(ModelConfigurationKey) || strings.HasPrefix(key, strings.ToLower(ModelConfigurationHistoryPrefix))
}

// Administrative history is append-only. Rollback creates another current
// publication; task, wallet and cost-history rows are never restored.
func saveModelConfigurationHistory(tx *gorm.DB, record PublishedModelConfiguration) error {
	encoded, err := common.Marshal(record)
	if err != nil {
		return err
	}
	key := ModelConfigurationHistoryPrefix + record.Version
	var existing Option
	err = tx.Where(&Option{Key: key}).Take(&existing).Error
	if err == nil {
		if existing.Key != key || existing.Value != string(encoded) {
			return ErrModelConfigurationConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&Option{Key: key, Value: string(encoded)}).Error
}

func GetModelConfigurationHistory(version string) (PublishedModelConfiguration, error) {
	return readModelConfigurationHistory(DB, version)
}

func modelConfigurationPublication(record ModelConfiguration, sequence int64) PublishedModelConfiguration {
	return PublishedModelConfiguration{Version: modelConfigurationDigest(struct {
		Sequence int64
		Value    ModelConfiguration
	}{sequence, record}), PublishedAt: time.Now().Unix(), Definition: record}
}
