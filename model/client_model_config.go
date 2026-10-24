package model

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	keyClientModelConfig          = "ClientModelConfig"
	MaxClientModelConfigBodyBytes = 256 * 1024
)

var (
	ErrInvalidClientModelConfig = errors.New("Invalid client model configuration")
	ErrUnavailableClientModel   = errors.New("Client model configuration contains an unavailable model")
	ErrClientDefaultMustChat    = errors.New("Default client model must support chat")
	ErrClientModelCatalog       = errors.New("Failed to load client model catalog")
	ErrClientModelSave          = errors.New("Failed to save client model configuration")
	clientModelConfigMutex      sync.Mutex
)

// ClientModelConfig contains display preferences only. Clients must intersect
// these IDs with the models available to the current user before applying them.
type ClientModelConfig struct {
	SchemaVersion    int      `json:"schema_version"`
	DefaultChatModel string   `json:"default_chat_model"`
	ModelOrder       []string `json:"model_order"`
}

type ClientModelCandidate struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	ChatEligible bool   `json:"chat_eligible"`
}

// modelDisplayName changes presentation only; routing and stored identities
// continue to use the exact model ID.
func modelDisplayName(id string) string {
	switch id {
	case "lec-mj-seedance-2-5-cd-720p":
		return "Seedance 2.5 · 720p · 30秒（线路1）"
	case "sd-25-900-2":
		return "Seedance 2.5 · 720p · 30秒（线路2）"
	case "k2-sd2.5-720p":
		return "Seedance 2.5 满参版 · 720p · 30秒"
	case "lec-sz-sd2-full-480p":
		return "Seedance 2.5 · 480p · 30秒"
	case "video-sd25-480p":
		return "Seedance 2.5 · 480p · 15–30秒"
	case "model_5":
		return "Seedance 2.5 完整版 · 480p · 15–30秒"
	case "sd2.5-720p-x2":
		return "Seedance 2.5 完整版 · 720p · 15–30秒"
	case "sd2-th-2.0-720p":
		return "Seedance 2.0 Fast · 720p · 15秒（线路1）"
	case "bt-seedance-2.0":
		return "Seedance 2.0 · 720p · 10/15秒"
	case "lec-rs-seedance-2-0-fast":
		return "Seedance 2.0 Fast · 720p · 15秒（线路2）"
	case "sd20-933-720p":
		return "Seedance 2.0 Fast · 720p · 15秒（线路3）"
	case "sd2-hn2-480-pro":
		return "Seedance 2.0 完整版 · 480p · 15秒"
	case "v3-sd2.0-933-720p":
		return "Seedance 2.0 完整版 · 720p · 15秒（线路1）"
	case "sd-20-9/3/3-1":
		return "Seedance 2.0 完整版 · 720p · 15秒（线路2）"
	case "sd-20-903":
		return "Seedance 2.0 Fast · 480p · 15秒"
	default:
		return id
	}
}

// Each field receives every occurrence during JSON decoding, allowing a
// duplicate key to be rejected without bypassing the common JSON codec.
type clientModelConfigField struct {
	seen bool
	raw  common.RawMessage
}

func (field *clientModelConfigField) UnmarshalJSON(data []byte) error {
	if field.seen || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidClientModelConfig
	}
	field.seen = true
	field.raw = slices.Clone(data)
	return nil
}

func ParseClientModelConfig(data []byte) (ClientModelConfig, error) {
	invalid := ClientModelConfig{}
	if len(data) == 0 || len(data) > MaxClientModelConfigBodyBytes || !utf8.Valid(data) {
		return invalid, ErrInvalidClientModelConfig
	}
	var object map[string]common.RawMessage
	if err := common.Unmarshal(data, &object); err != nil || len(object) != 3 {
		return invalid, ErrInvalidClientModelConfig
	}
	for key := range object {
		switch key {
		case "schema_version", "default_chat_model", "model_order":
		default:
			return invalid, ErrInvalidClientModelConfig
		}
	}
	var wire struct {
		SchemaVersion    clientModelConfigField `json:"schema_version"`
		DefaultChatModel clientModelConfigField `json:"default_chat_model"`
		ModelOrder       clientModelConfigField `json:"model_order"`
	}
	if err := common.Unmarshal(data, &wire); err != nil {
		return invalid, ErrInvalidClientModelConfig
	}
	var config ClientModelConfig
	if !wire.SchemaVersion.seen || !wire.DefaultChatModel.seen || !wire.ModelOrder.seen {
		return invalid, ErrInvalidClientModelConfig
	}
	if err := common.Unmarshal(wire.SchemaVersion.raw, &config.SchemaVersion); err != nil || config.SchemaVersion != 1 {
		return invalid, ErrInvalidClientModelConfig
	}
	if err := common.Unmarshal(wire.DefaultChatModel.raw, &config.DefaultChatModel); err != nil {
		return invalid, ErrInvalidClientModelConfig
	}
	if err := common.Unmarshal(wire.ModelOrder.raw, &config.ModelOrder); err != nil || config.ModelOrder == nil || len(config.ModelOrder) > 256 {
		return invalid, ErrInvalidClientModelConfig
	}
	if err := validateClientModelConfig(config); err != nil {
		return invalid, err
	}
	return config, nil
}

func validateClientModelConfig(config ClientModelConfig) error {
	if config.SchemaVersion != 1 || config.ModelOrder == nil || len(config.ModelOrder) > 256 {
		return ErrInvalidClientModelConfig
	}
	if config.DefaultChatModel != "" && !validClientModelID(config.DefaultChatModel) {
		return ErrInvalidClientModelConfig
	}
	seen := make(map[string]struct{}, len(config.ModelOrder))
	for _, id := range config.ModelOrder {
		if !validClientModelID(id) {
			return ErrInvalidClientModelConfig
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrInvalidClientModelConfig
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validClientModelID(id string) bool {
	if id == "" || len(id) > 255 || !utf8.ValidString(id) || strings.TrimSpace(id) != id {
		return false
	}
	return !strings.ContainsFunc(id, unicode.IsControl)
}

func CurrentClientModelConfig() ClientModelConfig {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[keyClientModelConfig]
	common.OptionMapRWMutex.RUnlock()
	if config, err := ParseClientModelConfig([]byte(raw)); err == nil {
		return config
	}
	return ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{}}
}

// ListClientModelCandidates reads live abilities and channels instead of the
// pricing cache. No channel credentials, group names, or channel IDs are exposed.
func ListClientModelCandidates() ([]ClientModelCandidate, error) {
	candidates := make([]ClientModelCandidate, 0)
	if DB == nil {
		return nil, ErrClientModelCatalog
	}
	var abilities []Ability
	if err := DB.Model(&Ability{}).Select("model", "channel_id").Distinct().Where("enabled = ?", true).
		Order("model, channel_id").Find(&abilities).Error; err != nil {
		return nil, ErrClientModelCatalog
	}
	if len(abilities) == 0 {
		return candidates, nil
	}
	channelIDs := make([]int, 0)
	seenChannels := make(map[int]struct{})
	for _, ability := range abilities {
		if _, seen := seenChannels[ability.ChannelId]; !seen {
			seenChannels[ability.ChannelId] = struct{}{}
			channelIDs = append(channelIDs, ability.ChannelId)
		}
	}
	channels := make(map[int]Channel, len(channelIDs))
	for start := 0; start < len(channelIDs); start += 500 {
		var batch []Channel
		if err := DB.Model(&Channel{}).Select("id", "type", "settings", "setting", "model_mapping", "key", "base_url", "header_override", "channel_info").
			Where("id IN ? AND status = ?", channelIDs[start:min(start+500, len(channelIDs))], common.ChannelStatusEnabled).
			Find(&batch).Error; err != nil {
			return nil, ErrClientModelCatalog
		}
		for _, channel := range batch {
			channels[channel.Id] = channel
		}
	}
	var metadata []Model
	if err := DB.Select("id", "model_name", "name_rule", "endpoints").Order("id").Find(&metadata).Error; err != nil {
		return nil, ErrClientModelCatalog
	}
	names := make([]string, 0, len(abilities))
	for _, ability := range abilities {
		names = append(names, ability.Model)
	}
	metadataByModel := resolveModelMetadata(metadata, names)
	// Include registered declarations even while the plugin master switch is
	// off: disabling a media route must not turn its model into a chat default.
	plugins := jsplugin.DefaultRegistry.Snapshot()
	taskModels := make(map[string]struct{})
	for _, metadata := range append(plugins.Factory, plugins.Override...) {
		for _, name := range metadata.Models {
			taskModels[strings.ToLower(name)] = struct{}{}
		}
	}
	byID := make(map[string]ClientModelCandidate)
	for _, ability := range abilities {
		channel, enabled := channels[ability.ChannelId]
		if !enabled || !validClientModelID(ability.Model) || !BlackrxCatalogAllowsModel(&channel, ability.Model) {
			continue
		}
		candidate := byID[ability.Model]
		candidate.ID = ability.Model
		candidate.DisplayName = modelDisplayName(ability.Model)
		candidate.ChatEligible = candidate.ChatEligible || clientModelSupportsChat(channel, ability.Model, metadataByModel[ability.Model], taskModels)
		byID[ability.Model] = candidate
	}
	names = names[:0]
	for name := range byID {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		candidates = append(candidates, byID[name])
	}
	return candidates, nil
}

func clientModelSupportsChat(channel Channel, id string, metadata *Model, taskModels map[string]struct{}) bool {
	switch channel.Type {
	case constant.ChannelTypeTaskPlugin, constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus,
		constant.ChannelTypeSunoAPI, constant.ChannelTypeKling, constant.ChannelTypeJimeng, constant.ChannelTypeVidu,
		constant.ChannelTypeDoubaoVideo, constant.ChannelTypeSora, constant.ChannelTypeReplicate:
		return false
	}
	upstreamModel := id
	if channel.ModelMapping != nil && strings.TrimSpace(*channel.ModelMapping) != "" {
		var mappings map[string]string
		if err := common.UnmarshalJsonStr(*channel.ModelMapping, &mappings); err != nil {
			return false
		}
		if mapped, ok := mappings[id]; ok {
			upstreamModel = mapped
		}
	}
	for _, name := range []string{id, upstreamModel} {
		lower := strings.ToLower(name)
		if _, taskModel := taskModels[lower]; taskModel || common.IsImageGenerationModel(name) {
			return false
		}
		for _, marker := range []string{"embed", "rerank", "whisper", "tts", "speech", "audio", "sora", "veo-", "video", "image", "hailuo", "minimax-h", "kling", "vidu", "suno", "seedream", "seedance", "stable-diffusion", "sdxl", "cogview", "wan2", "wanx"} {
			if strings.Contains(lower, marker) {
				return false
			}
		}
	}
	if metadata != nil && strings.TrimSpace(metadata.Endpoints) != "" {
		if err := ValidateModelEndpoints(metadata.Endpoints); err != nil {
			return false
		}
		var names []string
		if common.GetJsonType(common.RawMessage(metadata.Endpoints)) == "array" {
			if err := common.UnmarshalJsonStr(metadata.Endpoints, &names); err != nil {
				return false
			}
		} else {
			var endpoints map[string]common.RawMessage
			if err := common.UnmarshalJsonStr(metadata.Endpoints, &endpoints); err != nil {
				return false
			}
			for endpoint := range endpoints {
				names = append(names, endpoint)
			}
		}
		chat := false
		for _, endpoint := range names {
			if clientModelChatEndpoint(constant.EndpointType(strings.TrimSpace(endpoint))) {
				chat = true
			}
		}
		if !chat {
			return false
		}
	}
	if constant.IsAdvancedCustomChannel(channel.Type) {
		var config *dto.AdvancedCustomConfig
		if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
			config = preset
		} else {
			var settings dto.ChannelOtherSettings
			if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
				return false
			}
			config = settings.AdvancedCustom
		}
		if config == nil {
			return false
		}
		for _, endpoint := range config.SupportedEndpointTypesForModel(id) {
			if clientModelChatEndpoint(endpoint) {
				return true
			}
		}
		return false
	}
	for _, endpoint := range common.GetEndpointTypesByChannelType(channel.Type, upstreamModel) {
		if clientModelChatEndpoint(endpoint) {
			return true
		}
	}
	return false
}

func clientModelChatEndpoint(endpoint constant.EndpointType) bool {
	switch endpoint {
	case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic, constant.EndpointTypeGemini:
		return true
	default:
		return false
	}
}

func SaveClientModelConfig(config ClientModelConfig) error {
	if err := validateClientModelConfig(config); err != nil {
		return err
	}
	data, err := common.Marshal(config)
	if err != nil {
		return ErrInvalidClientModelConfig
	}
	validated, err := ParseClientModelConfig(data)
	if err != nil {
		return err
	}
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	candidates, err := ListClientModelCandidates()
	if err != nil {
		return err
	}
	byID := make(map[string]ClientModelCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	if validated.DefaultChatModel != "" {
		candidate, available := byID[validated.DefaultChatModel]
		if !available {
			return ErrUnavailableClientModel
		}
		if !candidate.ChatEligible {
			return ErrClientDefaultMustChat
		}
	}
	for _, id := range validated.ModelOrder {
		if _, available := byID[id]; !available {
			return ErrUnavailableClientModel
		}
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&Option{Key: keyClientModelConfig, Value: string(data)}).Error
	}); err != nil {
		return ErrClientModelSave
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[keyClientModelConfig] = string(data)
	common.OptionMapRWMutex.Unlock()
	return nil
}
