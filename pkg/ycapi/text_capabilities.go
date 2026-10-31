package ycapi

import (
	_ "embed"
	"fmt"
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
)

// Text capabilities are bound to the same private procurement lane as the
// approved prices. Only PublicTextCapabilities may serialize them for clients.
//
//go:embed text-capabilities.json
var textCapabilityData []byte

type CapabilityVerification struct {
	Tools     string `json:"tools"`
	Vision    string `json:"vision"`
	Reasoning string `json:"reasoning"`
}

type ReasoningControl struct {
	Type string   `json:"type"`
	Enum []string `json:"enum,omitempty"`
}

type ReasoningControlSchema struct {
	Type                 string                      `json:"type"`
	Properties           map[string]ReasoningControl `json:"properties"`
	AdditionalProperties bool                        `json:"additionalProperties"`
}

type TextCapability struct {
	ModelID                 string                 `json:"model_id"`
	LaneID                  string                 `json:"lane_id"`
	Tools                   bool                   `json:"tools"`
	Vision                  bool                   `json:"vision"`
	Reasoning               bool                   `json:"reasoning"`
	Verification            CapabilityVerification `json:"verification"`
	InputModalities         []string               `json:"input_modalities"`
	ToolsConfig             map[string]any         `json:"tools_config"`
	VisionConfig            map[string]any         `json:"vision_config"`
	ReasoningMode           string                 `json:"reasoning_mode"`
	ReasoningControls       ReasoningControlSchema `json:"reasoning_controls"`
	ReasoningOmitBehavior   string                 `json:"reasoning_omit_behavior"`
	ReasoningControlMapping map[string]bool        `json:"reasoning_control_mapping"`
	OperationalStatus       string                 `json:"operational_status"`
	OperationalReason       string                 `json:"operational_reason,omitempty"`
}

type textCapabilitySnapshot struct {
	SchemaVersion int              `json:"schema_version"`
	Revision      string           `json:"revision"`
	CheckedAt     string           `json:"checked_at"`
	Models        []TextCapability `json:"models"`
}

var textCapabilities, textCapabilityError = loadTextCapabilities()

func loadTextCapabilities() (textCapabilitySnapshot, error) {
	var snapshot textCapabilitySnapshot
	if err := common.Unmarshal(textCapabilityData, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.SchemaVersion != 2 || snapshot.Revision == "" || snapshot.CheckedAt == "" || len(snapshot.Models) != 40 {
		return snapshot, fmt.Errorf("invalid text capability snapshot")
	}
	seen := map[string]bool{}
	for _, capability := range snapshot.Models {
		m, ok := Lookup(capability.ModelID)
		if !ok || m.Kind != "text" || seen[m.ID] || len(m.Prices) != 2 || capability.LaneID != m.Prices[0].LaneID || capability.LaneID != m.Prices[1].LaneID {
			return snapshot, fmt.Errorf("text capability and approved procurement route differ")
		}
		seen[m.ID] = true
		for _, state := range []string{capability.Verification.Tools, capability.Verification.Vision, capability.Verification.Reasoning} {
			if !slices.Contains([]string{"supported", "unsupported", "unverified"}, state) {
				return snapshot, fmt.Errorf("invalid text capability verification state")
			}
		}
		if !slices.Contains([]string{"configurable", "always_on", "automatic", "unsupported", "unknown"}, capability.ReasoningMode) || capability.ReasoningControls.Type != "object" || capability.ReasoningControls.AdditionalProperties {
			return snapshot, fmt.Errorf("invalid text reasoning control schema")
		}
		for name, field := range capability.ReasoningControls.Properties {
			if name == "reasoning_effort" && field.Type == "string" && len(field.Enum) > 0 {
				continue
			}
			if name == "enable_thinking" && field.Type == "boolean" && len(field.Enum) == 0 {
				continue
			}
			return snapshot, fmt.Errorf("unsupported text reasoning control")
		}
	}
	return snapshot, nil
}

func LookupTextCapability(id string) (TextCapability, error) {
	if textCapabilityError != nil {
		return TextCapability{}, textCapabilityError
	}
	i := slices.IndexFunc(textCapabilities.Models, func(c TextCapability) bool { return c.ModelID == id })
	if i < 0 {
		return TextCapability{}, fmt.Errorf("text capability is not configured")
	}
	return textCapabilities.Models[i], nil
}

func PublicTextCapabilities(id string) map[string]any {
	c, err := LookupTextCapability(id)
	if err != nil {
		return map[string]any{"schema_version": 2, "verification": CapabilityVerification{Tools: "unverified", Vision: "unverified", Reasoning: "unverified"}, "operational_status": "configuration_unavailable"}
	}
	public := map[string]any{
		"schema_version": 2, "revision": textCapabilities.Revision, "checked_at": textCapabilities.CheckedAt,
		"tools": c.Tools, "vision": c.Vision, "reasoning": c.Reasoning, "stream": true,
		"max_input_tokens": 32000, "verification": c.Verification, "input_modalities": slices.Clone(c.InputModalities),
		"tools_config": maps.Clone(c.ToolsConfig), "vision_config": maps.Clone(c.VisionConfig),
		"reasoning_mode": c.ReasoningMode, "reasoning_controls": c.ReasoningControls,
		"reasoning_omit_behavior": c.ReasoningOmitBehavior, "operational_status": c.OperationalStatus,
		"reasoning_control_mapping": maps.Clone(c.ReasoningControlMapping),
		"operational_reason":        c.OperationalReason,
	}
	// The registry is immutable even if a discovery consumer edits a nested
	// schema or enum while preparing its own response.
	raw, err := common.Marshal(public)
	if err != nil {
		return nil
	}
	var copy map[string]any
	if err = common.Unmarshal(raw, &copy); err != nil {
		return nil
	}
	return copy
}
