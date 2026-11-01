package model

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	keyClientMediaDefaults          = "ClientMediaDefaults"
	keyClientTopBanner              = "ClientAnnouncementTopBanner"
	MaxClientProductPreferenceBytes = 4096
)

var errClientProductDedicatedAPI = errors.New("Use the dedicated client preference page to change this setting")

// Separate from the original three-field chat/order object so old dashboard
// saves and old clients cannot clear or reject the new media preferences.
type ClientMediaDefaults struct {
	SchemaVersion     int    `json:"schema_version"`
	DefaultImageModel string `json:"default_image_model"`
	DefaultVideoModel string `json:"default_video_model"`
}
type ClientTopBannerConfig struct {
	SchemaVersion int  `json:"schema_version"`
	Enabled       bool `json:"enabled"`
}

func ParseClientMediaDefaults(data []byte) (ClientMediaDefaults, error) {
	invalid := ClientMediaDefaults{}
	if len(data) > MaxClientProductPreferenceBytes || !utf8.Valid(data) {
		return invalid, ErrInvalidClientModelConfig
	}
	object, err := clientModelPresentationObject(data)
	if err != nil || len(object) != 3 || common.GetJsonType(object["default_image_model"]) != "string" || common.GetJsonType(object["default_video_model"]) != "string" {
		return invalid, ErrInvalidClientModelConfig
	}
	var cfg ClientMediaDefaults
	if common.Unmarshal(data, &cfg) != nil || cfg.SchemaVersion != 1 {
		return invalid, ErrInvalidClientModelConfig
	}
	for _, id := range []string{cfg.DefaultImageModel, cfg.DefaultVideoModel} {
		if id != "" && !validClientModelID(id) {
			return invalid, ErrInvalidClientModelConfig
		}
	}
	return cfg, nil
}
func ParseClientTopBannerConfig(data []byte) (ClientTopBannerConfig, error) {
	invalid := ClientTopBannerConfig{}
	if len(data) > MaxClientProductPreferenceBytes || !utf8.Valid(data) {
		return invalid, ErrInvalidClientModelConfig
	}
	object, err := clientModelPresentationObject(data)
	if err != nil || len(object) != 2 || common.GetJsonType(object["enabled"]) != "boolean" {
		return invalid, ErrInvalidClientModelConfig
	}
	var cfg ClientTopBannerConfig
	if common.Unmarshal(data, &cfg) != nil || cfg.SchemaVersion != 1 {
		return invalid, ErrInvalidClientModelConfig
	}
	return cfg, nil
}
func CurrentClientMediaDefaults() ClientMediaDefaults {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[keyClientMediaDefaults]
	common.OptionMapRWMutex.RUnlock()
	if cfg, err := ParseClientMediaDefaults([]byte(raw)); err == nil {
		return cfg
	}
	return ClientMediaDefaults{SchemaVersion: 1}
}
func CurrentClientTopBannerConfig() ClientTopBannerConfig {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[keyClientTopBanner]
	common.OptionMapRWMutex.RUnlock()
	if cfg, err := ParseClientTopBannerConfig([]byte(raw)); err == nil {
		return cfg
	}
	return ClientTopBannerConfig{SchemaVersion: 1}
}
func SaveClientMediaDefaults(cfg ClientMediaDefaults) error {
	data, err := common.Marshal(cfg)
	if err != nil {
		return ErrInvalidClientModelConfig
	}
	if _, err := ParseClientMediaDefaults(data); err != nil {
		return err
	}
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	candidates, err := ListClientModelCandidates()
	if err != nil {
		return err
	}
	for _, choice := range []struct {
		id    string
		image bool
	}{{cfg.DefaultImageModel, true}, {cfg.DefaultVideoModel, false}} {
		if choice.id == "" {
			continue
		}
		i := slices.IndexFunc(candidates, func(c ClientModelCandidate) bool {
			return c.ID == choice.id && (choice.image && c.ImageEligible || !choice.image && c.VideoEligible)
		})
		if i < 0 {
			return ErrUnavailableClientModel
		}
	}
	return saveClientProductPreference(keyClientMediaDefaults, string(data))
}
func SaveClientTopBannerConfig(cfg ClientTopBannerConfig) error {
	data, err := common.Marshal(cfg)
	if err != nil {
		return ErrInvalidClientModelConfig
	}
	if _, err := ParseClientTopBannerConfig(data); err != nil {
		return err
	}
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	return saveClientProductPreference(keyClientTopBanner, string(data))
}
func saveClientProductPreference(key, value string) error {
	if DB == nil {
		return ErrClientModelSave
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: key, Value: value}).Error; err != nil {
			return err
		}
		var stored Option
		if err := tx.Select("key").Where(&Option{Key: key}).Take(&stored).Error; err != nil {
			return err
		}
		if stored.Key != key {
			return ErrClientModelSave
		}
		return nil
	}); err != nil {
		return ErrClientModelSave
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
	return nil
}
func isClientProductPreference(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), keyClientMediaDefaults) || strings.EqualFold(strings.TrimSpace(key), keyClientTopBanner)
}
func validateClientProductPreferenceWrite(tx *gorm.DB, storedKey string) error {
	var count int64
	if err := tx.Model(&Option{}).Where(&Option{Key: storedKey}).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{keyClientMediaDefaults, keyClientTopBanner}}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errClientProductDedicatedAPI
	}
	return nil
}

// Match the immutable routing generation and channel's actual plugin binding.
// A model name containing "image" or "video" is not a media declaration.
func clientModelMediaEligibility(channel Channel, id string) (bool, bool) {
	image, video := false, false
	generation := jsplugin.DefaultRegistry.Generation()
	// Legacy task plugins can expose their own exact submit route rather than
	// a standard protocol. Reuse the host's media classification, but only for
	// an enabled bound plugin that explicitly owns this model and route.
	keys := []string{}
	if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
		keys = channel.GetSetting().TaskPluginBindings()
	}
	if plugin, ok := generation.GetByChannelType(channel.Type); ok {
		keys = append(keys, plugin.Meta.Key)
	}
	for _, key := range keys {
		plugin, ok := generation.Get(key)
		if !ok || !slices.Contains(plugin.Meta.Models, id) {
			continue
		}
		for _, route := range plugin.Meta.Routes {
			if route.Method != "POST" || route.Type != jsplugin.RouteTypeSubmit || len(route.Models) > 0 && !slices.Contains(route.Models, id) {
				continue
			}
			task := &Task{Platform: constant.TaskPlatform(plugin.Meta.Key), Properties: Properties{OriginModelName: id, UpstreamModelName: id}}
			switch TaskMediaKind(task) {
			case MediaImage:
				image = true
			case MediaVideo:
				video = true
			}
		}
	}
	for _, operation := range []struct {
		path  string
		image bool
	}{{"/v1/images/generations", true}, {"/v1/videos", false}} {
		for _, binding := range generation.LookupEndpointCandidates("POST", operation.path, id) {
			if binding.Plugin == nil {
				continue
			}
			bound := false
			if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
				bound = channel.GetSetting().BindsTaskPlugin(binding.Plugin.Meta.Key)
			} else {
				plugin, ok := generation.GetByChannelType(channel.Type)
				bound = ok && plugin == binding.Plugin
			}
			if bound {
				if operation.image {
					image = true
				} else {
					video = true
				}
				break
			}
		}
	}
	if channel.Type != constant.ChannelTypeTaskPlugin && channel.Type != constant.ChannelTypeNewAPI {
		for _, endpoint := range common.GetEndpointTypesByChannelType(channel.Type, id) {
			if endpoint == constant.EndpointTypeImageGeneration {
				image = true
			}
		}
	}
	return image, video
}
