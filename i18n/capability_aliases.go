package i18n

// Existing response sanitizers also accept an English fallback when no translator
// is supplied. Resolve those exact source messages to stable locale identifiers.
var capabilityMessageAliases = map[string]string{
	"Model capability configuration is unavailable":                      MsgCapabilityConfigUnavailable,
	"The configured upstream route is temporarily unavailable":           MsgCapabilityRouteUnavailable,
	"The model procurement route cannot be overridden":                   MsgCapabilityRouteOverrideForbidden,
	"Tool calling is unavailable on the selected model route":            MsgCapabilityToolsUnavailable,
	"The reasoning control is unavailable on the selected model route":   MsgCapabilityReasoningUnavailable,
	"The input modality is unavailable on the selected model route":      MsgCapabilityModalityUnavailable,
	"The input size could not be determined for the selected model":      MsgCapabilityInputSizeUnknown,
	"This model route accepts at most 32000 input tokens":                MsgCapabilityLegacyInputLimit,
	"The outgoing request does not match its verified model route":       MsgCapabilityOutboundMismatch,
	"The upstream provider rejected the model request":                   MsgCapabilityUpstreamRejected,
	"The selected upstream route is temporarily unavailable":             MsgCapabilitySelectedRouteUnavailable,
	"The upstream provider account has insufficient funds":               MsgCapabilityUpstreamInsufficientFunds,
	"The selected upstream model route does not accept image input":      MsgCapabilityImageUnsupported,
	"Image input is temporarily unavailable on the selected model route": MsgCapabilityImageUnavailable,
	"The selected model is not included in the current catalog":          MsgCapabilityModelExcluded,
}
