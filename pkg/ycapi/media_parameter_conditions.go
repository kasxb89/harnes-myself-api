package ycapi

import (
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MediaParameterConditions is reviewed gateway policy, separate from the native
// declaration/hash. Version 2 adds parameter predicates without changing the
// version 1 material-rule wire format or interpreting supplier prose at runtime.
type MediaParameterConditions struct {
	SchemaVersion int                  `json:"schema_version"`
	Rules         []MediaParameterRule `json:"rules"`
}

type MediaParameterRule struct {
	ID               string               `json:"id"`
	When             map[string][]any     `json:"when,omitempty"`
	Parameter        string               `json:"parameter"`
	DisallowedValues []any                `json:"disallowed_values,omitempty"`
	TextFlags        *MediaTextFlagPolicy `json:"text_flags,omitempty"`
	Message          string               `json:"message"`
}

// MediaTextFlagPolicy names a finite, non-executable lexer profile. Advanced
// flags are zero-argument exact lowercase tokens; unsupported syntax is rejected
// visibly, never stripped, rewritten, split into extra jobs, or guessed.
type MediaTextFlagPolicy struct {
	Profile           string            `json:"profile"`
	AllowedNames      []string          `json:"allowed_names,omitempty"`
	ForbiddenNames    []string          `json:"forbidden_names,omitempty"`
	ReservedAliases   map[string]string `json:"reserved_aliases,omitempty"`
	RequireDoubleDash bool              `json:"require_double_dash,omitempty"`
}

// MediaContractParameterConditions publishes detached, model-independent rules
// that clients can apply without hardcoding MJ IDs. This deliberately supports a
// reviewed subset of advanced syntax, not every Midjourney website parameter.
func MediaContractParameterConditions(contract MediaContract) *MediaParameterConditions {
	if contract.NativeID != "mj_imagine" {
		return nil
	}
	return &MediaParameterConditions{SchemaVersion: 2, Rules: []MediaParameterRule{
		{ID: "niji-explicit-version", When: map[string][]any{"bot_type": {"NIJI_JOURNEY"}}, Parameter: "version",
			DisallowedValues: []any{"8.2", "8.1", "6.1", "5.2", "5.1"}, Message: "This explicit version is unavailable for the selected engine"},
		{ID: "extra-native-flags", Parameter: "extra", TextFlags: &MediaTextFlagPolicy{
			Profile: "reviewed-native-flags-v1", AllowedNames: []string{"sameseed", "fast", "turbo", "relax"},
			ForbiddenNames: []string{"repeat", "r", "video", "bs", "edit"}, RequireDoubleDash: true,
			ReservedAliases: map[string]string{"aspect": "aspect_ratio", "ar": "aspect_ratio", "chaos": "chaos", "c": "chaos", "quality": "quality", "q": "quality", "stylize": "stylize", "s": "stylize", "version": "version", "v": "version", "weird": "weird", "w": "weird", "seed": "seed", "no": "negative_prompt", "raw": "style", "style": "style", "sref": "sref", "tile": "tile", "draft": "draft", "iw": "iw", "niji": "bot_type", "sw": "sw", "hd": "hd", "sd": "hd", "stop": "stop", "cref": "cref", "cw": "cw"},
		}, Message: "Use the declared controls or supported zero-argument advanced flags"},
		{ID: "prompt-native-flags", Parameter: "prompt", TextFlags: &MediaTextFlagPolicy{Profile: "text-no-native-parameter-token-v1"}, Message: "Use the declared controls and advanced-parameter field instead of native flags in the prompt"},
		{ID: "negative-prompt-content", Parameter: "negative_prompt", TextFlags: &MediaTextFlagPolicy{Profile: "text-no-native-parameter-token-v1"}, Message: "Enter unwanted content without native parameter tokens"},
	}}
}

// ValidateMediaParameterConditions also guards cached-but-unaccepted requests.
// Accepted receipt-first recovery must be handled by the caller before invoking
// new-request validation. No field/default/material/request identity is changed.
func ValidateMediaParameterConditions(contract MediaContract, params map[string]any) error {
	return validateMediaParameterConditions(MediaContractParameterConditions(contract), params)
}

func validateMediaParameterConditions(conditions *MediaParameterConditions, params map[string]any) error {
	if conditions == nil {
		return nil
	}
	if conditions.SchemaVersion != 2 {
		return mediaInvalid("conditions", "parameter condition version is unavailable")
	}
	if len(conditions.Rules) == 0 || len(conditions.Rules) > 32 {
		return mediaInvalid("conditions", "parameter conditions are unavailable")
	}
	// Check the complete policy before branch evaluation; inactive malformed
	// rules cannot become silently accepted after a control changes.
	ids := map[string]bool{}
	for _, rule := range conditions.Rules {
		if !mediaConditionName(rule.ID, true) || ids[rule.ID] || !mediaConditionName(rule.Parameter, false) ||
			rule.Message == "" || len(rule.Message) > 1024 || !utf8.ValidString(rule.Message) ||
			(len(rule.DisallowedValues) > 0) == (rule.TextFlags != nil) || len(rule.When) > 16 {
			return mediaInvalid("conditions", "parameter condition is unavailable")
		}
		ids[rule.ID] = true
		for name, allowed := range rule.When {
			if !mediaConditionName(name, false) || !mediaConditionLiterals(allowed) {
				return mediaInvalid("conditions", "parameter condition predicate is unavailable")
			}
		}
		if len(rule.DisallowedValues) > 0 && !mediaConditionLiterals(rule.DisallowedValues) {
			return mediaInvalid("conditions", "parameter condition values are unavailable")
		}
		if rule.TextFlags != nil && !mediaTextFlagPolicyAvailable(*rule.TextFlags) {
			return mediaInvalid("conditions", "text flag profile is unavailable")
		}
	}
	for _, rule := range conditions.Rules {
		active := true
		for name, allowed := range rule.When {
			value, present := params[name]
			if !present || !mediaEnumContains(allowed, value) {
				active = false
				break
			}
		}
		value, present := params[rule.Parameter]
		if !active || !present {
			continue
		}
		if value == nil {
			return mediaInvalid(rule.Parameter, "null is not a declared value")
		}
		if mediaEnumContains(rule.DisallowedValues, value) {
			return mediaInvalid(rule.Parameter, rule.Message)
		}
		if rule.TextFlags != nil {
			text, ok := value.(string)
			if !ok || !mediaTextFlagsValid(*rule.TextFlags, text) {
				return mediaInvalid(rule.Parameter, rule.Message)
			}
		}
	}
	return nil
}

func mediaConditionName(value string, hyphen bool) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, ch := range value {
		if ch >= 'a' && ch <= 'z' || i > 0 && (ch >= '0' && ch <= '9' || ch == '_' || hyphen && ch == '-') {
			continue
		}
		return false
	}
	return true
}

func mediaConditionLiterals(values []any) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	for _, value := range values {
		switch value := value.(type) {
		case string:
			if !utf8.ValidString(value) || len(value) > 256 {
				return false
			}
		case bool:
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func mediaTextFlagPolicyAvailable(policy MediaTextFlagPolicy) bool {
	if policy.Profile == "text-no-native-parameter-token-v1" {
		return len(policy.AllowedNames) == 0 && len(policy.ForbiddenNames) == 0 && len(policy.ReservedAliases) == 0 && !policy.RequireDoubleDash
	}
	if policy.Profile != "reviewed-native-flags-v1" || !policy.RequireDoubleDash || len(policy.AllowedNames) == 0 ||
		len(policy.AllowedNames) > 64 || len(policy.ForbiddenNames) > 64 || len(policy.ReservedAliases) > 64 {
		return false
	}
	names := map[string]bool{}
	for _, name := range append(slices.Clone(policy.AllowedNames), policy.ForbiddenNames...) {
		if !mediaConditionName(name, false) || names[name] {
			return false
		}
		names[name] = true
	}
	for alias, parameter := range policy.ReservedAliases {
		if !mediaConditionName(alias, false) || !mediaConditionName(parameter, false) || names[alias] {
			return false
		}
	}
	return true
}

func mediaTextFlagsValid(policy MediaTextFlagPolicy, text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	if policy.Profile == "text-no-native-parameter-token-v1" {
		// A native parameter begins at the reviewed token boundaries: start,
		// whitespace, or comma. Natural embedded
		// hyphens (black-and-white, letters--numbers) remain literal content.
		runes := []rune(text)
		for i := 0; i+1 < len(runes); i++ {
			if runes[i] == '-' && runes[i+1] == '-' && (i == 0 || unicode.IsSpace(runes[i-1]) || runes[i-1] == ',') {
				return false
			}
		}
		return true
	}
	// No shell grammar, normalization, aliases, quoting, escaping, equals,
	// arguments, Unicode separators, or arbitrary passthrough is supported.
	tokens := strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	seen := map[string]bool{}
	for _, token := range tokens {
		if !strings.HasPrefix(token, "--") {
			return false
		}
		name := strings.TrimPrefix(token, "--")
		if !mediaConditionName(name, false) || seen[name] || !slices.Contains(policy.AllowedNames, name) {
			return false
		}
		seen[name] = true
	}
	return true
}
