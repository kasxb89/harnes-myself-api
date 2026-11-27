package ycapi

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

//go:embed media-contracts.json
var mediaContractData []byte

// MediaContract is supplier-declared generation metadata, not a route grant or
// a successful generation qualification. It contains no credentials or lanes.
type MediaContract struct {
	ID                      string                    `json:"id"`
	NativeID                string                    `json:"native_id"`
	UpstreamPublicID        string                    `json:"upstream_public_id"`
	Kind                    string                    `json:"kind"`
	Name                    string                    `json:"name"`
	Available               bool                      `json:"available"`
	Capabilities            []string                  `json:"capabilities"`
	Parameters              map[string]MediaParameter `json:"param_schema"`
	FrameReferenceExclusive bool                      `json:"frame_reference_exclusive"`
	MaxPromptChars          int                       `json:"max_prompt_chars"`
	MaxPromptBytes          int                       `json:"max_prompt_bytes"`
	MaskedEdit              bool                      `json:"masked_edit"`
	PromptSyntax            string                    `json:"prompt_syntax"`
}

// MediaParameter retains every native field constraint while providing typed
// access to the validation vocabulary. Native is detached in exported reads.
type MediaParameter struct {
	Type                string
	Required            bool
	Default             any
	HasDefault          bool
	Enum                []any
	Options             []MediaParameterOption
	Minimum             *float64
	Maximum             *float64
	MaxItems            *int
	MaxBytes            *int64
	AvailableWhen       map[string][]any
	MaxItemsByMode      map[string]int
	MaxItemsByQuality   map[string]int
	EnumValuesByQuality map[string][]any
	ExclusiveOptions    []any
	ExclusiveWithByMode map[string][]string
	RequiresMedia       []string
	RequiresVisual      bool
	CombinedDuration    bool
	Native              map[string]any
}

// MediaParameterOption binds one literal enum option to its declared inputs.
type MediaParameterOption struct {
	Value          any              `json:"value"`
	Available      *bool            `json:"available"`
	AvailableWhen  map[string][]any `json:"available_when"`
	RefMin         *int             `json:"ref_min"`
	RefMax         *int             `json:"ref_max"`
	RefMinVideos   *int             `json:"ref_min_videos"`
	PromptOptional bool             `json:"prompt_optional"`
}

// UnmarshalJSON keeps the source schema losslessly alongside typed constraints.
func (p *MediaParameter) UnmarshalJSON(data []byte) error {
	var native map[string]any
	if err := common.Unmarshal(data, &native); err != nil {
		return err
	}
	var fields struct {
		Type                string                 `json:"type"`
		Required            bool                   `json:"required"`
		Default             any                    `json:"default"`
		Enum                []any                  `json:"enum"`
		Options             []MediaParameterOption `json:"options"`
		Minimum             *float64               `json:"min"`
		Maximum             *float64               `json:"max"`
		MaxItems            *int                   `json:"max_items"`
		MaxBytes            *int64                 `json:"max_bytes"`
		AvailableWhen       map[string][]any       `json:"available_when"`
		MaxItemsByMode      map[string]int         `json:"max_items_by_mode"`
		MaxItemsByQuality   map[string]int         `json:"max_items_by_quality"`
		EnumValuesByQuality map[string][]any       `json:"enum_values_by_quality"`
		ExclusiveOptions    []any                  `json:"exclusive_options"`
		ExclusiveWithByMode map[string][]string    `json:"exclusive_with_by_mode"`
		RequiresMedia       []string               `json:"requires_media"`
		RequiresVisual      bool                   `json:"requires_visual_reference"`
		CombinedDuration    bool                   `json:"combined_duration_budget"`
	}
	if err := common.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, hasDefault := native["default"]
	*p = MediaParameter{Type: fields.Type, Required: fields.Required, Default: fields.Default, HasDefault: hasDefault,
		Enum: fields.Enum, Options: fields.Options, Minimum: fields.Minimum, Maximum: fields.Maximum,
		MaxItems: fields.MaxItems, MaxBytes: fields.MaxBytes, AvailableWhen: fields.AvailableWhen,
		MaxItemsByMode: fields.MaxItemsByMode, MaxItemsByQuality: fields.MaxItemsByQuality,
		EnumValuesByQuality: fields.EnumValuesByQuality, ExclusiveOptions: fields.ExclusiveOptions,
		ExclusiveWithByMode: fields.ExclusiveWithByMode, RequiresMedia: fields.RequiresMedia,
		RequiresVisual: fields.RequiresVisual, CombinedDuration: fields.CombinedDuration, Native: native}
	return nil
}

// MarshalJSON publishes the original native schema, including unknown metadata.
func (p MediaParameter) MarshalJSON() ([]byte, error) { return common.Marshal(p.Native) }

type mediaContractSnapshot struct {
	SchemaVersion int                   `json:"schema_version"`
	CheckedAt     string                `json:"checked_at"`
	SourceSHA256  string                `json:"source_sha256"`
	Sources       []mediaContractSource `json:"sources"`
	Models        []MediaContract       `json:"models"`
}

type mediaContractSource struct {
	URL       string `json:"url"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	CheckedAt string `json:"checked_at"`
}

// Binds the reviewed declarations to SHA256(image response || video response),
// preserving the original response bytes in that order. This is source
// evidence, not account availability or a paid-generation qualification.
const reviewedMediaContractSourceSHA256 = "4773ab714964864c114ba8b6005ee6a48a869c809e247dc6c3003c67b56a3d84"

var mediaContracts, mediaContractError = loadMediaContracts()

func loadMediaContracts() (mediaContractSnapshot, error) {
	var snapshot mediaContractSnapshot
	if err := common.Unmarshal(mediaContractData, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.CheckedAt == "" || snapshot.SourceSHA256 != reviewedMediaContractSourceSHA256 || len(snapshot.Sources) != 2 || len(snapshot.Models) != 75 {
		return snapshot, errors.New("invalid YCAPI media contract snapshot")
	}
	for _, source := range snapshot.Sources {
		if source.URL == "" || source.Bytes <= 0 || len(source.SHA256) != 64 || source.CheckedAt == "" {
			return snapshot, errors.New("invalid YCAPI media contract source")
		}
	}
	seen, kinds := map[string]bool{}, map[string]int{}
	for _, contract := range snapshot.Models {
		if contract.ID != "ycapi-"+contract.NativeID || seen[contract.ID] || contract.UpstreamPublicID == "" ||
			!slices.Contains([]string{"image", "video"}, contract.Kind) || len(contract.Parameters) == 0 {
			return snapshot, errors.New("invalid YCAPI media contract identity")
		}
		seen[contract.ID], kinds[contract.Kind] = true, kinds[contract.Kind]+1
		for _, field := range contract.Parameters {
			if !slices.Contains([]string{"enum", "multi_enum", "int", "bool", "string", "upload"}, field.Type) {
				return snapshot, errors.New("unsupported YCAPI media contract field type")
			}
		}
	}
	if kinds["image"] != 31 || kinds["video"] != 44 {
		return snapshot, errors.New("invalid YCAPI media contract kinds")
	}
	return snapshot, nil
}

// MediaContracts returns independent snapshots of all supplier-declared media.
// It never enables a model, reads an account, or makes a generation request.
func MediaContracts() ([]MediaContract, error) {
	if mediaContractError != nil {
		return nil, mediaContractError
	}
	var copy mediaContractSnapshot
	if err := common.Unmarshal(mediaContractData, &copy); err != nil {
		return nil, err
	}
	return copy.Models, nil
}

// LookupMediaContract accepts an existing gateway ID or either source identity.
// The result is detached; callers cannot mutate the shared embedded snapshot.
func LookupMediaContract(id string) (MediaContract, bool) {
	if mediaContractError != nil {
		return MediaContract{}, false
	}
	for _, contract := range mediaContracts.Models {
		if id != contract.ID && id != contract.NativeID && id != contract.UpstreamPublicID {
			continue
		}
		encoded, err := common.Marshal(contract)
		if err != nil {
			return MediaContract{}, false
		}
		var copy MediaContract
		if common.Unmarshal(encoded, &copy) != nil {
			return MediaContract{}, false
		}
		return copy, true
	}
	return MediaContract{}, false
}

// MediaContractSchema projects native generation controls as JSON Schema.
// Nonstandard native constraints remain under x-ycapi-contract; no transport,
// lane, credential, or execution-qualification claim is included.
func MediaContractSchema(id string) (map[string]any, bool) {
	contract, ok := LookupMediaContract(id)
	if !ok {
		return nil, false
	}
	properties := map[string]any{}
	assets, err := AssetFields(contract)
	if err != nil {
		return nil, false
	}
	var required []string
	for name, field := range contract.Parameters {
		projected := map[string]any{"x-ycapi-contract": field.Native}
		switch field.Type {
		case "int":
			projected["type"] = "integer"
		case "bool":
			projected["type"] = "boolean"
		case "upload", "multi_enum":
			projected["type"], projected["items"] = "array", map[string]any{"type": "string"}
			if field.MaxItems != nil {
				projected["maxItems"] = *field.MaxItems
			}
		default:
			projected["type"] = "string"
		}
		if field.Type == "enum" {
			var choices []any
			for _, value := range field.Enum {
				if mediaOptionAvailable(field, value) {
					choices = append(choices, value)
				}
			}
			projected["enum"] = choices
		}
		if field.HasDefault && (field.Type != "enum" || mediaOptionAvailable(field, field.Default)) {
			projected["default"] = field.Default
		}
		if field.Minimum != nil {
			projected["minimum"] = *field.Minimum
		}
		if field.Maximum != nil {
			projected["maximum"] = *field.Maximum
		}
		if description, ok := field.Native["description"].(string); ok {
			projected["description"] = description
		}
		if mediaParameterIsAsset(contract, name, field) {
			projected["type"], projected["items"] = "array", map[string]any{"type": "string"}
			for _, asset := range assets {
				if asset.Name == name {
					projected["x-media-native-shape"] = asset.Shape
					if asset.Maximum != nil {
						projected["maxItems"] = *asset.Maximum
					}
				}
			}
		}
		properties[name] = projected
		if field.Required && len(field.AvailableWhen) == 0 {
			required = append(required, name)
		}
	}
	if mediaAllowsGenericPrompt(contract) {
		if _, present := properties["prompt"]; !present {
			properties["prompt"] = map[string]any{"type": "string"}
		}
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties,
		"required": required, "x-model-kind": contract.Kind, "x-media-assets": assets}, true
}

func mediaAllowsGenericPrompt(contract MediaContract) bool {
	// Source-only processing cards describe url, not a generative prompt.
	_, sourceURL := contract.Parameters["url"]
	return !sourceURL
}

func mediaOptionAvailable(field MediaParameter, value any) bool {
	for _, option := range field.Options {
		if mediaValuesEqual(option.Value, value) && option.Available != nil && !*option.Available {
			return false
		}
	}
	return true
}

func mediaSelectedOptionAvailable(field MediaParameter, value any, params map[string]any) bool {
	if !mediaOptionAvailable(field, value) {
		return false
	}
	for _, option := range field.Options {
		if !mediaValuesEqual(option.Value, value) {
			continue
		}
		for dependency, allowed := range option.AvailableWhen {
			if !mediaEnumContains(allowed, params[dependency]) {
				return false
			}
		}
	}
	return true
}

func mediaValuesEqual(left, right any) bool {
	l, errL := json.Marshal(left)
	r, errR := json.Marshal(right)
	return errL == nil && errR == nil && string(l) == string(r)
}

func mediaEnumContains(values []any, value any) bool {
	return slices.ContainsFunc(values, func(candidate any) bool { return mediaValuesEqual(candidate, value) })
}

// MediaContractValidationError identifies a field without echoing asset URLs,
// prompts, or other request content into logs or user-facing error messages.
type MediaContractValidationError struct{ Field, Reason string }

func (e *MediaContractValidationError) Error() string {
	return fmt.Sprintf("invalid YCAPI media parameter %s: %s", e.Field, e.Reason)
}

func mediaInvalid(field, reason string) error {
	return &MediaContractValidationError{Field: field, Reason: reason}
}

func mediaString(value any) string {
	text, _ := value.(string)
	return text
}

func mediaPresent(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		return len(value) > 0
	case bool:
		return value
	default:
		return true
	}
}
