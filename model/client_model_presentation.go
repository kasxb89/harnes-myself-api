package model

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	keyClientModelPresentation          = "ClientModelPresentation"
	MaxClientModelPresentationBodyBytes = 1024 * 1024
)

var (
	ErrInvalidClientModelPresentation      = errors.New("Invalid client model presentation")
	ErrClientModelPresentationSave         = errors.New("Failed to save client model presentation")
	errClientModelPresentationDedicatedAPI = errors.New("Use the client model presentation page to change this setting")
)

// Fields are optional pointers: missing uses the client's fallback, while an
// explicit empty string clears it. All text is inert display data, never HTML.
type ClientModelPresentationEntry struct {
	Description *string `json:"description,omitempty"`
	Badge       *string `json:"badge,omitempty"`
}

type ClientModelPresentation struct {
	SchemaVersion int                                     `json:"schema_version"`
	Models        map[string]ClientModelPresentationEntry `json:"models"`
}

// Decode with the common codec, then inspect only this object's property names.
// A map alone loses duplicate keys (including escaped spellings). The input is
// already valid JSON, so this small scan only needs to track strings and depth.
func clientModelPresentationObject(data []byte) (map[string]common.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, ErrInvalidClientModelPresentation
	}
	var object map[string]common.RawMessage
	if err := common.Unmarshal(data, &object); err != nil || object == nil {
		return nil, ErrInvalidClientModelPresentation
	}
	seen := make(map[string]bool, len(object))
	depth, expectKey := 0, true
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 1 {
				expectKey = true
			}
		case '"':
			start := i
			for i++; i < len(data); i++ {
				if data[i] == '\\' {
					i++
				} else if data[i] == '"' {
					break
				}
			}
			if depth == 1 && expectKey {
				var key string
				if common.Unmarshal(data[start:i+1], &key) != nil || seen[key] {
					return nil, ErrInvalidClientModelPresentation
				}
				seen[key], expectKey = true, false
			}
		}
	}
	return object, nil
}

func ParseClientModelPresentation(data []byte) (ClientModelPresentation, error) {
	invalid := ClientModelPresentation{}
	if len(data) == 0 || len(data) > MaxClientModelPresentationBodyBytes || !utf8.Valid(data) {
		return invalid, ErrInvalidClientModelPresentation
	}
	object, err := clientModelPresentationObject(data)
	if err != nil || len(object) != 2 {
		return invalid, ErrInvalidClientModelPresentation
	}
	var config ClientModelPresentation
	if common.Unmarshal(object["schema_version"], &config.SchemaVersion) != nil || config.SchemaVersion != 1 {
		return invalid, ErrInvalidClientModelPresentation
	}
	models, err := clientModelPresentationObject(object["models"])
	if err != nil || len(models) > 256 {
		return invalid, ErrInvalidClientModelPresentation
	}
	config.Models = make(map[string]ClientModelPresentationEntry, len(models))
	for id, raw := range models {
		if !validClientModelID(id) {
			return invalid, ErrInvalidClientModelPresentation
		}
		fields, err := clientModelPresentationObject(raw)
		if err != nil || len(fields) > 2 {
			return invalid, ErrInvalidClientModelPresentation
		}
		entry := ClientModelPresentationEntry{}
		for key, raw := range fields {
			var text string
			if common.GetJsonType(raw) != "string" || common.Unmarshal(raw, &text) != nil {
				return invalid, ErrInvalidClientModelPresentation
			}
			switch key {
			case "description":
				if len(utf16.Encode([]rune(text))) > 500 {
					return invalid, ErrInvalidClientModelPresentation
				}
				entry.Description = &text
			case "badge":
				if len(utf16.Encode([]rune(text))) > 32 || strings.ContainsAny(text, "\r\n\u0085\u2028\u2029") {
					return invalid, ErrInvalidClientModelPresentation
				}
				entry.Badge = &text
			default:
				return invalid, ErrInvalidClientModelPresentation
			}
		}
		config.Models[id] = entry
	}
	return config, nil
}

func CurrentClientModelPresentation() ClientModelPresentation {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[keyClientModelPresentation]
	common.OptionMapRWMutex.RUnlock()
	if config, err := ParseClientModelPresentation([]byte(raw)); err == nil {
		return config
	}
	return ClientModelPresentation{SchemaVersion: 1, Models: map[string]ClientModelPresentationEntry{}}
}

func SaveClientModelPresentation(config ClientModelPresentation) error {
	data, err := common.Marshal(config)
	if err != nil {
		return ErrInvalidClientModelPresentation
	}
	if _, err := ParseClientModelPresentation(data); err != nil {
		return err
	}
	// Share the existing preference/sync lock so a periodic database read can
	// never publish a stale snapshot over a successful presentation save.
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	if DB == nil {
		return ErrClientModelPresentationSave
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&Option{Key: keyClientModelPresentation, Value: string(data)}).Error; err != nil {
			return err
		}
		var stored Option
		if err := tx.Select("key").Where(&Option{Key: keyClientModelPresentation}).Take(&stored).Error; err != nil {
			return err
		}
		if stored.Key != keyClientModelPresentation {
			return ErrClientModelPresentationSave
		}
		return nil
	}); err != nil {
		return ErrClientModelPresentationSave
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[keyClientModelPresentation] = string(data)
	common.OptionMapRWMutex.Unlock()
	return nil
}

// All generic writers reject this dedicated resource, including aliases under
// the database's own collation. Calling after a tentative insert also protects
// alias-first writes; the caller must roll the transaction back on rejection.
func validateClientModelPresentationOptionWrite(tx *gorm.DB, storedKey string) error {
	var count int64
	if err := tx.Model(&Option{}).Where(&Option{Key: storedKey}).Where(&Option{Key: keyClientModelPresentation}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errClientModelPresentationDedicatedAPI
	}
	return nil
}

func isClientModelPresentationOption(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), keyClientModelPresentation)
}
