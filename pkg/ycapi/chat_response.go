package ycapi

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
)

// PublicChatResponse unwraps the runtime's nonstream envelope and removes only
// supplier accounting/route metadata. Token usage and assistant/tool content
// remain authoritative; supplier wallet costs are not the customer's bill.
func PublicChatResponse(raw []byte, translate func(string) string) ([]byte, int, error) {
	var value map[string]any
	if err := common.Unmarshal(raw, &value); err != nil {
		return nil, 0, err
	}
	status := 0
	runtimeEnvelope := value["object"] == "model_runtime.invoke"
	if runtimeEnvelope {
		if value["status"] == "succeeded" || value["status"] == "completed" {
			result, ok := value["result"].(map[string]any)
			if !ok {
				return nil, 0, fmt.Errorf("upstream chat result is missing")
			}
			value = result
		} else {
			code, _ := value["error_code"].(string)
			message := "The upstream provider rejected the model request"
			status = http.StatusBadGateway
			switch code {
			case "pinned_channel_unavailable", "vendor_model_unavailable", "upstream_outage":
				code, message, status = "model_route_unavailable", "The selected upstream route is temporarily unavailable", http.StatusServiceUnavailable
			case "insufficient_balance", "insufficient_quota", "balance_insufficient":
				code, message, status = "upstream_balance_unavailable", "The upstream provider account has insufficient funds", http.StatusServiceUnavailable
			case "model_not_multimodal", "chat_media_unsupported":
				message, status = "The selected upstream model route does not accept image input", http.StatusBadRequest
			}
			if code == "" {
				code = "upstream_chat_failed"
			}
			if status == http.StatusBadGateway {
				code = "upstream_chat_failed"
			}
			if translate != nil {
				message = translate(message)
			}
			value = map[string]any{"error": map[string]any{"type": "upstream_error", "code": code, "message": message}}
		}
	}
	if upstream, ok := value["error"].(map[string]any); ok && !runtimeEnvelope {
		class, _ := upstream["error_class"].(string)
		code, _ := upstream["code"].(string)
		message := "The upstream provider rejected the model request"
		publicCode := "upstream_chat_failed"
		switch class {
		case "upstream_outage", "vendor_model_unavailable", "upstream_timeout", "vendor_auth_failed":
			publicCode, message = "model_route_unavailable", "The selected upstream route is temporarily unavailable"
		default:
			class = "upstream_error"
		}
		switch code {
		case "insufficient_balance", "insufficient_quota", "upstream_balance_unavailable":
			publicCode, message = "upstream_balance_unavailable", "The upstream provider account has insufficient funds"
		case "model_not_multimodal", "chat_media_unsupported":
			publicCode, message = code, "The selected upstream model route does not accept image input"
		}
		if translate != nil {
			message = translate(message)
		}
		public := map[string]any{"type": "upstream_error", "code": publicCode, "message": message, "error_class": class}
		for _, flag := range []string{"fallback_safe", "billed_or_processing"} {
			if v, present := upstream[flag].(bool); present {
				public[flag] = v
			}
		}
		value["error"] = public
	}
	for _, field := range []string{"billing", "lane", "routing_receipt", "provider", "upstream_model"} {
		delete(value, field)
	}
	if usage, ok := value["usage"].(map[string]any); ok {
		for _, field := range []string{"cost_rmb", "cost_currency", "balance_rmb", "cost", "cost_micro_usd", "actual_cost_micro_usd"} {
			delete(usage, field)
		}
	}
	clean, err := common.Marshal(value)
	return clean, status, err
}
