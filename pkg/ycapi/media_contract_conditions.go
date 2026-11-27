package ycapi

import "slices"

// MediaMaterialConditions describes reviewed material relationships that the
// supplier states only in prose. It is gateway policy, not part of the native
// MediaContract or its qualification hash. Omit it for unaffected models.
type MediaMaterialConditions struct {
	SchemaVersion int                 `json:"schema_version"`
	Rules         []MediaMaterialRule `json:"rules"`
}

// MediaMaterialRule activates when every parameter matches an allowed literal.
// Exactly one constraint is present. Missing material roles have count zero.
type MediaMaterialRule struct {
	ID                string           `json:"id"`
	When              map[string][]any `json:"when"`
	AssetField        string           `json:"asset_field"`
	DisallowedCounts  []int            `json:"disallowed_counts,omitempty"`
	RequiresAnyAssets []string         `json:"requires_any_assets,omitempty"`
	Message           string           `json:"message"`
}

// MediaContractMaterialConditions returns a detached, versioned policy for both
// the public catalog and the free-quote validator. Clients need no model IDs.
func MediaContractMaterialConditions(contract MediaContract) *MediaMaterialConditions {
	var rule MediaMaterialRule
	switch contract.NativeID {
	case "magi-2-special":
		// Its mode description says a single image is always a first frame.
		rule = MediaMaterialRule{ID: "reference-image-count", When: map[string][]any{"mode": {"reference"}},
			AssetField: "images", DisallowedCounts: []int{1}, Message: "A single source image requires explicit first-frame mode"}
	case "minimax-h3", "wan-3-0":
		// Its audio description requires at least one image or video reference.
		rule = MediaMaterialRule{ID: "reference-audio-requires-visual", When: map[string][]any{"mode": {"reference"}},
			AssetField: "audios", RequiresAnyAssets: []string{"images", "videos"}, Message: "Reference audio requires a source image or video"}
	default:
		return nil
	}
	return &MediaMaterialConditions{SchemaVersion: 1, Rules: []MediaMaterialRule{rule}}
}

// validateMediaMaterialConditions applies the same rules published to clients
// after normal parameter defaults and verified material bindings are resolved.
// It never changes a parameter, material, intent, UUID, quote or billing fact.
func validateMediaMaterialConditions(conditions *MediaMaterialConditions, params map[string]any, counts map[string]int) error {
	if conditions == nil {
		return nil
	}
	if conditions.SchemaVersion != 1 {
		return mediaInvalid("conditions", "material condition version is unavailable")
	}
	for _, rule := range conditions.Rules {
		if rule.ID == "" || rule.AssetField == "" || rule.Message == "" ||
			(len(rule.DisallowedCounts) > 0) == (len(rule.RequiresAnyAssets) > 0) ||
			slices.ContainsFunc(rule.DisallowedCounts, func(count int) bool { return count < 0 }) {
			return mediaInvalid("conditions", "material condition is unavailable")
		}
		active := true
		for parameter, allowed := range rule.When {
			if !mediaEnumContains(allowed, params[parameter]) {
				active = false
				break
			}
		}
		if !active {
			continue
		}
		if slices.Contains(rule.DisallowedCounts, counts[rule.AssetField]) {
			return mediaInvalid(rule.AssetField, rule.Message)
		}
		if len(rule.RequiresAnyAssets) > 0 && counts[rule.AssetField] > 0 &&
			!slices.ContainsFunc(rule.RequiresAnyAssets, func(field string) bool { return counts[field] > 0 }) {
			return mediaInvalid(rule.AssetField, rule.Message)
		}
	}
	return nil
}
