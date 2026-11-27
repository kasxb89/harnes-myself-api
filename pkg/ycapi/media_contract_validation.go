package ycapi

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// MediaAssetMetadata must come from the caller's trusted upload/media probe,
// never request-supplied metadata or an inference from a URL or file extension.
// Format is an observed container format (for example mp4), not a URL suffix.
type MediaAssetMetadata struct {
	Kind            string  `json:"kind"`
	Bytes           int64   `json:"bytes"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	DurationSeconds float64 `json:"duration_seconds"`
	FPS             float64 `json:"fps"`
	MIMEType        string  `json:"mime_type"`
	Format          string  `json:"format"`
}

// ValidateMediaContractParams applies the same native logical contract before
// quoting or submitting. Assets is indexed by each exact submitted reference.
// It returns detached params with applicable defaults; excess inputs are
// rejected, never truncated. No request, finance, task, or configuration is
// created. Successful validation is not upstream execution qualification.
func ValidateMediaContractParams(id string, params map[string]any, assets map[string]MediaAssetMetadata) (map[string]any, error) {
	return validateMediaContractParams(id, params, assets, true)
}

func validateMediaContractParams(id string, params map[string]any, assets map[string]MediaAssetMetadata, verifyAssets bool) (map[string]any, error) {
	contract, ok := LookupMediaContract(id)
	if !ok || !contract.Available {
		return nil, mediaInvalid("model", "model is not in the available supplier contract")
	}
	encoded, err := common.Marshal(params)
	if err != nil {
		return nil, mediaInvalid("params", "parameters must be finite JSON values")
	}
	var out map[string]any
	if common.Unmarshal(encoded, &out) != nil || out == nil {
		return nil, mediaInvalid("params", "an object is required")
	}
	for name := range out {
		if _, known := contract.Parameters[name]; !known && !(name == "prompt" && mediaAllowsGenericPrompt(contract)) {
			return nil, mediaInvalid(name, "field is not declared for this model")
		}
	}
	// Resolve unconditional dependencies first; do not invent a compatible
	// alternate option when a caller changes quality, mode, or output format.
	for name, field := range contract.Parameters {
		if _, present := out[name]; !present && field.HasDefault && len(field.AvailableWhen) == 0 {
			out[name] = field.Default
		}
	}
	for name, field := range contract.Parameters {
		if _, present := out[name]; !present && field.HasDefault && mediaFieldAvailable(field, out) {
			out[name] = field.Default
		}
	}
	mode, quality := mediaString(out["mode"]), mediaString(out["quality"])
	counts, durations := map[string]int{}, map[string]float64{}
	for name, field := range contract.Parameters {
		value, present := out[name]
		available := mediaFieldAvailable(field, out)
		if !available {
			if present {
				return nil, mediaInvalid(name, "field is unavailable for the selected controls")
			}
			continue
		}
		if !present || value == nil || (field.Required && !mediaPresent(value)) {
			if field.Required {
				return nil, mediaInvalid(name, "a value is required")
			}
			if present {
				return nil, mediaInvalid(name, "null is not a declared value")
			}
			continue
		}
		if mediaParameterIsAsset(contract, name, field) {
			refs, err := mediaReferences(name, value)
			if err != nil {
				return nil, err
			}
			limit := dto.MaxImageN
			if mediaAssetSingle(name) {
				limit = 1
			}
			if field.MaxItems != nil {
				limit = min(limit, *field.MaxItems)
			}
			if n, exists := field.MaxItemsByMode[mode]; exists {
				limit = min(limit, n)
			}
			if n, exists := field.MaxItemsByQuality[quality]; exists {
				limit = min(limit, n)
			}
			if len(refs) > limit {
				return nil, mediaInvalid(name, "too many source assets for the selected controls")
			}
			for _, ref := range refs {
				if !verifyAssets {
					continue
				}
				meta, exists := assets[ref]
				if !exists {
					return nil, mediaInvalid(name, "verified source asset metadata is required")
				}
				if err := validateMediaAsset(contract, name, field, meta, mode); err != nil {
					return nil, err
				}
				durations[name] += meta.DurationSeconds
			}
			counts[name] = len(refs)
			if maximum, exists := mediaNativeNumber(field.Native, "max_total_duration_sec"); exists && durations[name] > maximum {
				return nil, mediaInvalid(name, "source assets exceed the total duration limit")
			}
			out[name] = refs
			continue
		}
		if err := validateMediaValue(name, field, value, quality, out); err != nil {
			return nil, err
		}
	}
	if err := validateMediaPrompt(contract, out); err != nil {
		return nil, err
	}
	if err := validateMediaRelationships(contract, out, counts, durations, verifyAssets); err != nil {
		return nil, err
	}
	return out, nil
}

func mediaFieldAvailable(field MediaParameter, params map[string]any) bool {
	for dependency, allowed := range field.AvailableWhen {
		if !mediaEnumContains(allowed, params[dependency]) {
			return false
		}
	}
	return true
}

func mediaReferences(name string, value any) ([]string, error) {
	var refs []string
	switch value := value.(type) {
	case string:
		refs = []string{value}
	case []string:
		refs = append([]string{}, value...)
	case []any:
		for _, entry := range value {
			ref, ok := entry.(string)
			if !ok {
				return nil, mediaInvalid(name, "source references must be strings")
			}
			refs = append(refs, ref)
		}
	default:
		return nil, mediaInvalid(name, "source references must be a string or array")
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return nil, mediaInvalid(name, "source reference must not be empty")
		}
	}
	return refs, nil
}

func validateMediaValue(name string, field MediaParameter, value any, quality string, params map[string]any) error {
	switch field.Type {
	case "int":
		number, ok := value.(float64)
		if !ok || !mediaFinite(number) || math.Trunc(number) != number ||
			field.Minimum != nil && number < *field.Minimum || field.Maximum != nil && number > *field.Maximum {
			return mediaInvalid(name, "an integer within the declared bounds is required")
		}
		if name == "count" && (number < 1 || number > dto.MaxImageN) {
			return mediaInvalid(name, "image count exceeds the gateway limit")
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return mediaInvalid(name, "a boolean is required")
		}
	case "string", "enum":
		if _, ok := value.(string); !ok {
			return mediaInvalid(name, "a string is required")
		}
	case "multi_enum":
		values, ok := value.([]any)
		if !ok || field.MaxItems != nil && len(values) > *field.MaxItems {
			return mediaInvalid(name, "a bounded array of choices is required")
		}
		for i, entry := range values {
			if !mediaEnumContains(field.Enum, entry) || !mediaSelectedOptionAvailable(field, entry, params) {
				return mediaInvalid(name, "choice is unavailable")
			}
			if mediaEnumContains(values[:i], entry) {
				return mediaInvalid(name, "duplicate choices are not permitted")
			}
			if len(values) > 1 && mediaEnumContains(field.ExclusiveOptions, entry) {
				return mediaInvalid(name, "exclusive choice cannot be combined")
			}
		}
	default:
		return mediaInvalid(name, "unsupported parameter type")
	}
	if field.Type == "enum" && (!mediaEnumContains(field.Enum, value) || !mediaSelectedOptionAvailable(field, value, params)) {
		return mediaInvalid(name, "choice is unavailable")
	}
	if allowed, exists := field.EnumValuesByQuality[quality]; exists && !mediaEnumContains(allowed, value) {
		return mediaInvalid(name, "choice is unavailable for this quality")
	}
	if name == "duration" {
		duration, err := strconv.ParseFloat(mediaString(value), 64)
		if err != nil || !mediaFinite(duration) || duration <= 0 || duration > common.MaxTaskDurationSeconds {
			return mediaInvalid(name, "duration exceeds the gateway limit")
		}
	}
	return nil
}

func validateMediaPrompt(contract MediaContract, params map[string]any) error {
	value, present := params["prompt"]
	if !present && !mediaAllowsGenericPrompt(contract) {
		return nil
	}
	optional := false
	if field, declared := contract.Parameters["prompt"]; declared {
		optional = !field.Required
	}
	for _, option := range contract.Parameters["mode"].Options {
		if mediaValuesEqual(option.Value, params["mode"]) && option.PromptOptional {
			optional = true
		}
	}
	text, ok := value.(string)
	if present && !ok {
		return mediaInvalid("prompt", "a string is required")
	}
	if (!present || strings.TrimSpace(text) == "") && optional {
		return nil
	}
	if !ok || strings.TrimSpace(text) == "" {
		return mediaInvalid("prompt", "a nonempty prompt is required")
	}
	if !utf8.ValidString(text) || contract.MaxPromptChars > 0 && utf8.RuneCountInString(text) > contract.MaxPromptChars ||
		contract.MaxPromptBytes > 0 && len(text) > contract.MaxPromptBytes {
		return mediaInvalid("prompt", "prompt exceeds the model limit")
	}
	return nil
}

func validateMediaAsset(contract MediaContract, name string, field MediaParameter, meta MediaAssetMetadata, mode string) error {
	expected, ok := mediaAssetKind(contract, name)
	if !ok {
		return mediaInvalid(name, "source asset role has no reviewed native binding")
	}
	if meta.Kind != expected || meta.Bytes <= 0 || field.MaxBytes != nil && meta.Bytes > *field.MaxBytes {
		return mediaInvalid(name, "source asset kind or byte size is invalid")
	}
	if formats, ok := field.Native["accept_formats"].([]any); ok {
		format := strings.ToLower(meta.Format)
		if format == "" {
			format = mediaFormatFromMIME(meta.MIMEType)
		}
		if !mediaEnumContains(formats, format) {
			return mediaInvalid(name, "verified source format is not supported")
		}
	}
	if expected != "audio" {
		if meta.Width <= 0 || meta.Height <= 0 {
			return mediaInvalid(name, "verified source dimensions are required")
		}
		edge, pixels := float64(max(meta.Width, meta.Height)), float64(meta.Width)*float64(meta.Height)
		ratio := float64(meta.Width) / float64(meta.Height)
		for _, check := range []struct {
			key    string
			actual float64
			lower  bool
		}{
			{"min_edge_px", float64(min(meta.Width, meta.Height)), true}, {"max_edge_px", edge, false},
			{"min_pixels", pixels, true}, {"max_pixels", pixels, false}, {"aspect_min", ratio, true}, {"aspect_max", ratio, false},
		} {
			if limit, exists := mediaNativeNumber(field.Native, check.key); exists && (check.lower && check.actual < limit || !check.lower && check.actual > limit) {
				return mediaInvalid(name, "source dimensions exceed the declared limits")
			}
		}
	}
	if expected == "audio" || expected == "video" {
		if !mediaFinite(meta.DurationSeconds) || meta.DurationSeconds <= 0 || meta.DurationSeconds > common.MaxTaskDurationSeconds {
			return mediaInvalid(name, "verified bounded source duration is required")
		}
		minimum, _ := mediaNativeNumber(field.Native, "min_duration_sec")
		if byMode, ok := field.Native["min_duration_sec_by_mode"].(map[string]any); ok {
			if value, exists := mediaNativeNumber(byMode, mode); exists {
				minimum = max(minimum, value)
			}
		}
		if meta.DurationSeconds < minimum {
			return mediaInvalid(name, "source clip is shorter than the selected mode permits")
		}
		if byMode, ok := field.Native["max_clip_duration_sec_by_mode"].(map[string]any); ok {
			if maximum, exists := mediaNativeNumber(byMode, mode); exists && meta.DurationSeconds > maximum {
				return mediaInvalid(name, "source clip is longer than the selected mode permits")
			}
		}
		// duration_tolerance_sec is retained, not guessed into a relaxed bound.
		for _, key := range []string{"min_fps", "max_fps"} {
			if limit, exists := mediaNativeNumber(field.Native, key); exists && (!mediaFinite(meta.FPS) || meta.FPS <= 0 || key == "min_fps" && meta.FPS < limit || key == "max_fps" && meta.FPS > limit) {
				return mediaInvalid(name, "verified source frame rate exceeds the declared limits")
			}
		}
	}
	return nil
}

func validateMediaRelationships(contract MediaContract, params map[string]any, counts map[string]int, durations map[string]float64, verifyDurations bool) error {
	mode := mediaString(params["mode"])
	// This card documents that one image is always treated as a first frame.
	// Do not let an explicit reference request silently acquire frame intent.
	if contract.NativeID == "magi-2-special" && mode == "reference" && counts["images"] == 1 {
		return mediaInvalid("images", "a single source image requires explicit first-frame mode on this model")
	}
	for _, option := range contract.Parameters["mode"].Options {
		if !mediaValuesEqual(option.Value, params["mode"]) {
			continue
		}
		if option.RefMin != nil && counts["images"] < *option.RefMin || option.RefMax != nil && counts["images"] > *option.RefMax {
			return mediaInvalid("images", "source image count does not match the selected mode")
		}
		if option.RefMinVideos != nil && counts["videos"] < *option.RefMinVideos {
			return mediaInvalid("videos", "source video is required for the selected mode")
		}
	}
	if mode == "image-edit" && counts["images"] == 0 {
		return mediaInvalid("images", "image editing requires a source image")
	}
	if contract.FrameReferenceExclusive && slices.Contains([]string{"first-frame", "first-last", "keyframes"}, mode) && counts["videos"] > 0 {
		return mediaInvalid("videos", "frame inputs and reference video cannot be combined")
	}
	totalAssets, combinedDuration, combinedLimit := 0, float64(0), math.Inf(1)
	for _, count := range counts {
		totalAssets += count
	}
	for name, field := range contract.Parameters {
		if maximum, exists := mediaNativeNumber(field.Native, "max_combined_items"); exists && float64(totalAssets) > maximum {
			return mediaInvalid(name, "combined source asset count exceeds the limit")
		}
		if counts[name] > 0 && field.RequiresVisual && counts["images"]+counts["videos"] == 0 {
			return mediaInvalid(name, "source audio requires a visual reference")
		}
		if mediaPresent(params[name]) {
			for _, dependency := range field.RequiresMedia {
				if counts[dependency] == 0 {
					return mediaInvalid(name, "required source media is missing")
				}
			}
		}
		if counts[name] > 0 {
			for _, other := range field.ExclusiveWithByMode[mode] {
				if counts[other] > 0 {
					return mediaInvalid(name, "source media types are mutually exclusive for this mode")
				}
			}
		}
		if verifyDurations && field.CombinedDuration {
			combinedDuration += durations[name]
			if maximum, exists := mediaNativeNumber(field.Native, "max_total_duration_sec"); exists {
				combinedLimit = min(combinedLimit, maximum)
			}
		}
		if maximum, exists := mediaNativeNumber(field.Native, "max_ref_plus_output_sec"); verifyDurations && exists && counts[name] > 0 {
			outputDuration, err := strconv.ParseFloat(mediaString(params["duration"]), 64)
			if err != nil || durations[name]+outputDuration > maximum {
				return mediaInvalid(name, "reference plus output duration exceeds the limit")
			}
		}
	}
	// The marked source fields share the conservative declared budget. Never
	// treat each flagged field as an independent allowance and double it.
	if combinedDuration > combinedLimit {
		return mediaInvalid("videos/audios", "combined reference duration exceeds the shared limit")
	}
	return nil
}

func mediaFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func mediaNativeNumber(values map[string]any, key string) (float64, bool) {
	value, ok := values[key].(float64)
	return value, ok && mediaFinite(value)
}

func mediaFormatFromMIME(mime string) string {
	formats := map[string]string{"image/jpeg": "jpeg", "image/png": "png", "image/webp": "webp", "video/mp4": "mp4",
		"video/quicktime": "mov", "video/webm": "webm", "audio/mpeg": "mp3", "audio/wav": "wav", "audio/x-wav": "wav", "audio/mp4": "m4a", "audio/aac": "aac"}
	return formats[strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))]
}
