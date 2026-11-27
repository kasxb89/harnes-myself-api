package ycapi

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MediaRuntimeIntent records the caller's requested use of source material.
// It is validated locally and is never forwarded as an upstream parameter.
type MediaRuntimeIntent string

const (
	MediaIntentText      MediaRuntimeIntent = "text"
	MediaIntentReference MediaRuntimeIntent = "reference"
	MediaIntentFrames    MediaRuntimeIntent = "frames"
	MediaIntentTransform MediaRuntimeIntent = "transform"
)

// MediaContractIntents is the reviewed operation vocabulary, not a generation
// qualification. A published execution binding may narrow this set further.
func MediaContractIntents(contract MediaContract) []string {
	allowed := map[string]bool{}
	for _, raw := range contract.Parameters["mode"].Enum {
		mode := mediaString(raw)
		switch mode {
		case "text-to-image", "text-to-video":
			allowed["text"] = true
		case "image-edit":
			allowed["reference"], allowed["transform"] = true, true
		case "multi-reference", "reference":
			allowed["reference"] = true
		case "first-frame", "first-last", "keyframes":
			allowed["frames"] = contract.Kind == "video"
		case "edit", "extend", "layer-decompose", "avatar", "lip-sync":
			allowed["transform"] = true
		}
	}
	switch contract.NativeID {
	case "kling-v3-omni-cankao":
		allowed["text"], allowed["reference"] = true, true
	case "grok-imagine-1.5-special":
		allowed["frames"] = true
	case "flux-3-video":
		// This supplier uses reference for ordered keyframes.
		delete(allowed, "reference")
		allowed["frames"] = true
	case "aliyun-image-superres", "aliyun-video-superres", "video-desubtitle", "face-repaint", "kling-v3-motion-control", "veo-4-omni-v2v":
		allowed = map[string]bool{"transform": true}
	}
	intents := []string{}
	for _, intent := range []string{"text", "reference", "frames", "transform"} {
		if allowed[intent] {
			intents = append(intents, intent)
		}
	}
	return intents
}

// MediaRuntimeRequest is the documented native runtime envelope. Its lane and
// durable public task identity are server-owned values, never catalog controls.
type MediaRuntimeRequest struct {
	ModelID         string         `json:"model_id"`
	Modality        string         `json:"modality"`
	Operation       string         `json:"operation,omitempty"`
	Params          map[string]any `json:"params"`
	LaneID          string         `json:"lane_id"`
	ClientRequestID string         `json:"client_request_id"`
}

// MediaAssetField declares one native asset role. Shape is the native envelope
// shape, while the shared validator normalizes references to []string. Empty
// MIMETypes means the source contract did not declare a MIME/format allowlist.
type MediaAssetField struct {
	Name      string   `json:"name"`
	MediaKind string   `json:"media_kind"`
	Shape     string   `json:"shape"`
	Minimum   int      `json:"min"`
	Maximum   *int     `json:"max,omitempty"`
	MIMETypes []string `json:"mime_types"`
	Ordered   bool     `json:"ordered"`
}

// AssetFields returns reviewed native asset roles without inferring a kind
// from a URL, a file extension, or a maximum item count of one. Mode-specific
// limits remain in the original parameter contract and shared validator.
func AssetFields(contract MediaContract) ([]MediaAssetField, error) {
	fields := []MediaAssetField{}
	for name, parameter := range contract.Parameters {
		if !mediaParameterIsAsset(contract, name, parameter) {
			continue
		}
		kind, ok := mediaAssetKind(contract, name)
		if !ok {
			return nil, mediaInvalid(name, "source asset role has no reviewed native binding")
		}
		field := MediaAssetField{Name: name, MediaKind: kind, Shape: "multiple", Maximum: parameter.MaxItems,
			MIMETypes: []string{}, Ordered: true}
		if parameter.Required {
			field.Minimum = 1
		}
		if mediaAssetSingle(name) {
			maximum := 1
			field.Shape, field.Maximum, field.Ordered = "single", &maximum, false
		}
		if formats, ok := parameter.Native["accept_formats"].([]any); ok {
			for _, format := range formats {
				mime := mediaMIMEFromFormat(mediaString(format))
				if mime != "" && !slices.Contains(field.MIMETypes, mime) {
					field.MIMETypes = append(field.MIMETypes, mime)
				}
			}
		}
		slices.Sort(field.MIMETypes)
		fields = append(fields, field)
	}
	slices.SortFunc(fields, func(left, right MediaAssetField) int { return strings.Compare(left.Name, right.Name) })
	return fields, nil
}

func mediaParameterIsAsset(contract MediaContract, name string, field MediaParameter) bool {
	return field.Type == "upload" || contract.NativeID == "mj_imagine" && (name == "cref" || name == "sref")
}

func mediaAssetSingle(name string) bool {
	return name == "url" || name == "cref" || name == "sref"
}

func mediaAssetKind(contract MediaContract, name string) (string, bool) {
	switch name {
	case "images":
		return "image", true
	case "videos":
		return "video", true
	case "audios":
		return "audio", true
	case "cref", "sref":
		return "image", contract.NativeID == "mj_imagine"
	case "url":
		// These are the exact source-only processing cards in this snapshot.
		// A future url field requires a reviewed binding, not a kind guess.
		switch contract.NativeID {
		case "aliyun-image-superres":
			return "image", contract.Kind == "image"
		case "aliyun-video-superres", "video-desubtitle":
			return "video", contract.Kind == "video"
		}
	}
	return "", false
}

// ValidateMediaRuntimeQuantity separates public native quantity support from
// the gateway's qualified meter. Video batch settlement is not qualified yet.
func ValidateMediaRuntimeQuantity(contract MediaContract, params map[string]any) error {
	if contract.Kind != "video" {
		return nil
	}
	value, present := params["count"]
	if !present {
		return nil
	}
	// Normal request JSON numbers are decoded as float64. Strings, booleans,
	// null and every quantity other than one must fail before asset uploads.
	if count, ok := value.(float64); !ok || count != 1 {
		return mediaInvalid("count", "video batch billing is not qualified; exactly one output is required")
	}
	return nil
}

// BuildMediaRuntimeRequest creates a native V2 envelope without sending it.
// normalizedParams must first pass ValidateMediaContractParams with trusted
// asset metadata. This builder repeats logical checks and enforces intent and
// native shapes; it cannot replace source-byte/duration verification. It keeps
// every reference in submitted order and never invents or rewrites mode/frames.
func BuildMediaRuntimeRequest(modelID string, normalizedParams map[string]any, laneID, publicTaskID string, intent MediaRuntimeIntent) (MediaRuntimeRequest, error) {
	invalid := MediaRuntimeRequest{}
	contract, ok := LookupMediaContract(modelID)
	if !ok {
		return invalid, mediaInvalid("model", "model is not in the supplier contract")
	}
	if strings.TrimSpace(laneID) == "" || laneID != strings.TrimSpace(laneID) || strings.ContainsFunc(laneID, unicode.IsControl) {
		return invalid, mediaInvalid("lane_id", "an exact server-selected lane is required")
	}
	if strings.TrimSpace(publicTaskID) == "" || publicTaskID != strings.TrimSpace(publicTaskID) ||
		!utf8.ValidString(publicTaskID) || utf8.RuneCountInString(publicTaskID) > 128 || strings.ContainsFunc(publicTaskID, unicode.IsControl) {
		return invalid, mediaInvalid("client_request_id", "a bounded durable task identity is required")
	}
	if !slices.Contains([]MediaRuntimeIntent{MediaIntentText, MediaIntentReference, MediaIntentFrames, MediaIntentTransform}, intent) {
		return invalid, mediaInvalid("intent", "intent must be text, reference, frames, or transform")
	}
	params, err := validateMediaContractParams(modelID, normalizedParams, nil, false)
	if err != nil {
		return invalid, err
	}
	if err := ValidateMediaRuntimeQuantity(contract, params); err != nil {
		return invalid, err
	}
	fields, err := AssetFields(contract)
	if err != nil {
		return invalid, err
	}
	counts, total := map[string]int{}, 0
	for _, field := range fields {
		value, present := params[field.Name]
		if !present {
			continue
		}
		refs, err := mediaReferences(field.Name, value)
		if err != nil {
			return invalid, err
		}
		counts[field.Name], total = len(refs), total+len(refs)
		if field.Shape == "single" {
			if len(refs) != 1 {
				return invalid, mediaInvalid(field.Name, "the native role requires exactly one source asset")
			}
			params[field.Name] = refs[0]
		} else {
			params[field.Name] = refs
		}
	}
	if intent == MediaIntentText && total != 0 {
		return invalid, mediaInvalid("intent", "text intent cannot include source assets")
	}
	if intent != MediaIntentText && total == 0 {
		return invalid, mediaInvalid("intent", "source assets are required; text fallback is forbidden")
	}
	mode := mediaString(params["mode"])
	frames := slices.Contains([]string{"first-frame", "first-last", "keyframes"}, mode) ||
		contract.NativeID == "flux-3-video" && mode == "reference" || contract.NativeID == "grok-imagine-1.5-special"
	transform := slices.Contains([]string{"edit", "extend", "layer-decompose", "avatar", "lip-sync"}, mode) ||
		slices.Contains([]string{"aliyun-image-superres", "aliyun-video-superres", "video-desubtitle", "face-repaint", "kling-v3-motion-control", "veo-4-omni-v2v"}, contract.NativeID)
	if slices.Contains([]string{"reference", "multi-reference"}, mode) && !frames && intent != MediaIntentReference {
		return invalid, mediaInvalid("intent", "reference intent must match the declared native reference operation")
	}
	if intent == MediaIntentFrames && (!frames || counts["images"] == 0) || frames && intent != MediaIntentFrames {
		return invalid, mediaInvalid("intent", "frame intent must match the declared native frame operation")
	}
	if intent == MediaIntentTransform && !transform && mode != "image-edit" || transform && intent != MediaIntentTransform {
		return invalid, mediaInvalid("intent", "transform intent must match the declared native transformation")
	}
	operation := ""
	if contract.Kind == "image" {
		operation = "generate"
		if total > 0 {
			operation = "edit"
		}
	}
	return MediaRuntimeRequest{ModelID: contract.NativeID, Modality: contract.Kind, Operation: operation,
		Params: params, LaneID: laneID, ClientRequestID: publicTaskID}, nil
}

func mediaMIMEFromFormat(format string) string {
	formats := map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp",
		"mp4": "video/mp4", "mov": "video/quicktime", "webm": "video/webm", "mp3": "audio/mpeg", "wav": "audio/wav",
		"m4a": "audio/mp4", "aac": "audio/aac"}
	return formats[strings.ToLower(format)]
}
