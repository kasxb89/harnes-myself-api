package service

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	hostdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const capacityPublicModelKey = "model_capacity_public_model"

func ModelCapacityForGroups(name string, groups []string) (model.EffectiveModelCapacity, error) {
	return model.ResolveModelCapacity(name, groups, append([]string{name}, operation_setting.GetModelFallbackCandidates(name)...))
}

func CapacityAccountGroups(c *gin.Context) []string {
	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if group == "auto" {
		return GetRequestAutoGroups(c, userGroup)
	}
	if group == "" {
		group = userGroup
	}
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	}
	return []string{group}
}

func capacityForRequest(c *gin.Context, name string) (model.EffectiveModelCapacity, error) {
	public := c.GetString(capacityPublicModelKey)
	if public == "" {
		public = name
		c.Set(capacityPublicModelKey, public)
	}
	return ModelCapacityForGroups(public, CapacityAccountGroups(c))
}

func CapacityChannelFilters(c *gin.Context, routeModel, group string, filters []hostdto.ChannelFilter) []hostdto.ChannelFilter {
	caps, err := capacityForRequest(c, routeModel)
	if err == nil && !caps.Managed {
		return filters
	}
	allowed := []int{}
	if err == nil {
		for _, r := range caps.Routes {
			if r.RouteModel == routeModel && (group == "auto" || r.Group == group) {
				allowed = append(allowed, r.ChannelID)
			}
		}
	}
	return append(append([]hostdto.ChannelFilter{}, filters...), hostdto.ChannelFilter{Kind: hostdto.FilterModelCapacity, AllowedChannelIDs: allowed})
}

func capacityError(c *gin.Context, code, message string) *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{Message: i18n.T(c, message), Type: "invalid_request_error", Code: code}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// Clamp before quota reservation. Unknown metadata leaves the old path alone.
func ClampModelCapacityRequest(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	approved, catalogued := ycapi.Lookup(info.OriginModelName)
	if ycapi.IsExcludedChatModel(info.OriginModelName) {
		return capacityError(c, "model_not_available", "The selected model is not included in the current catalog")
	}
	if catalogued {
		request, isText := info.Request.(*dto.GeneralOpenAIRequest)
		if approved.Kind != "text" || !isText {
			return types.NewErrorWithStatusCode(errors.New("This model requires its declared generation endpoint"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		capability, err := ycapi.LookupTextCapability(approved.ID)
		if err != nil {
			return capacityError(c, "model_capability_unavailable", "Model capability configuration is unavailable")
		}
		if capability.OperationalStatus == "fixed_lane_unavailable" || capability.OperationalStatus == "upstream_quota_exhausted" {
			return types.WithOpenAIError(types.OpenAIError{Message: i18n.T(c, "The configured upstream route is temporarily unavailable"), Type: "service_unavailable", Code: "model_route_unavailable"}, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
		}
		var body map[string]any
		if common.UnmarshalBodyReusable(c, &body) != nil {
			return types.NewErrorWithStatusCode(errors.New("Invalid text request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		for _, key := range []string{"lane_id", "pinned_lane_id"} {
			value, present := body[key]
			if !present || value == nil {
				continue
			}
			return capacityError(c, "model_route_override_not_allowed", "The model procurement route cannot be overridden")
		}
		for _, key := range []string{"tools", "functions", "tool_choice", "function_call"} {
			value, present := body[key]
			if !present || value == nil || capability.Tools {
				continue
			}
			if entries, ok := value.([]any); ok && len(entries) == 0 {
				continue
			}
			if value == "none" {
				continue
			}
			return capacityError(c, "model_tools_unavailable", "Tool calling is unavailable on the selected model route")
		}
		for _, key := range []string{"reasoning", "reasoning_effort", "enable_thinking", "thinking_budget", "thinking", "chat_template_kwargs", "extra_body"} {
			value, present := body[key]
			if !present || value == nil {
				continue
			}
			field, declared := capability.ReasoningControls.Properties[key]
			valid := false
			if declared && capability.Reasoning {
				if field.Type == "boolean" {
					_, valid = value.(bool)
				} else if text, ok := value.(string); ok {
					valid = slices.Contains(field.Enum, text)
				}
			}
			if !valid {
				return capacityError(c, "model_reasoning_control_unavailable", "The reasoning control is unavailable on the selected model route")
			}
		}
		messages, _ := body["messages"].([]any)
		for _, value := range messages {
			message, ok := value.(map[string]any)
			if !ok {
				return types.NewErrorWithStatusCode(errors.New("Text messages are required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			if !capability.Tools && (message["role"] == "tool" || message["role"] == "function" || message["tool_calls"] != nil || message["function_call"] != nil) {
				return capacityError(c, "model_tools_unavailable", "Tool calling is unavailable on the selected model route")
			}
			if content, ok := message["content"].([]any); ok {
				for _, part := range content {
					item, ok := part.(map[string]any)
					if ok && item["type"] == "image_url" && !capability.Vision {
						if capability.Verification.Vision == "unsupported" {
							return capacityError(c, "model_image_input_unsupported", "The selected upstream model route does not accept image input")
						}
						return capacityError(c, "model_image_input_temporarily_unavailable", "Image input is temporarily unavailable on the selected model route")
					}
					if !ok || (item["type"] != "text" && (item["type"] != "image_url" || !capability.Vision)) {
						return capacityError(c, "model_input_modality_unavailable", "The input modality is unavailable on the selected model route")
					}
				}
			}
		}
		meta := request.GetTokenCountMeta()
		estimate, err := CountRequestToken(c, meta, info)
		if err != nil {
			return capacityError(c, "model_input_size_unavailable", "The input size could not be determined for the selected model")
		}
		if estimate > 32000 {
			return capacityError(c, "model_input_limit_exceeded", "This model route accepts at most 32000 input tokens")
		}
	}
	caps, err := capacityForRequest(c, info.OriginModelName)
	if err != nil {
		return capacityError(c, "model_capacity_unavailable", i18n.MsgCapacityConfigUnavailable)
	}
	if !caps.Managed {
		return nil
	}
	if len(caps.Routes) == 0 {
		return capacityError(c, "model_capacity_unavailable", i18n.MsgCapacityNoVerifiedRoute)
	}
	limit := caps.MaxOutputTokens
	if r, ok := info.Request.(*dto.GeneralOpenAIRequest); ok && caps.ContextWindow != nil {
		meta := r.GetTokenCountMeta()
		if len(meta.Files) == 0 {
			tokenizerModel := info.OriginModelName
			if info.ChannelMeta != nil && info.UpstreamModelName != "" {
				tokenizerModel = info.UpstreamModelName
			}
			estimate := tokenkit.Count(tokenizerModel, meta.CombineText) + meta.MessagesCount*3 + meta.NameCount*3 + meta.ToolsCount*8 + 3
			remaining := *caps.ContextWindow - estimate
			if remaining <= 0 {
				return capacityError(c, "context_length_exceeded", i18n.MsgCapacityContextExceeded)
			}
			if limit == nil || remaining < *limit {
				limit = common.GetPointer(remaining)
			}
		}
	}
	if limit == nil {
		return nil
	}
	n := uint(*limit)
	switch r := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		if r.MaxTokens != nil && *r.MaxTokens == 0 || r.MaxCompletionTokens != nil && *r.MaxCompletionTokens == 0 {
			return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
		}
		if r.MaxTokens != nil && *r.MaxTokens > n {
			r.MaxTokens = common.GetPointer(n)
		}
		if r.MaxCompletionTokens != nil && *r.MaxCompletionTokens > n {
			r.MaxCompletionTokens = common.GetPointer(n)
		}
		if r.MaxTokens == nil && r.MaxCompletionTokens == nil {
			r.MaxTokens = common.GetPointer(n)
		}
		if r.MaxTokens != nil && r.MaxCompletionTokens != nil {
			effective := min(*r.MaxTokens, *r.MaxCompletionTokens)
			r.MaxTokens = common.GetPointer(effective)
			r.MaxCompletionTokens = common.GetPointer(effective)
		}
	case *dto.OpenAIResponsesRequest:
		if r.MaxOutputTokens != nil && *r.MaxOutputTokens == 0 {
			return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
		}
		if r.MaxOutputTokens == nil || *r.MaxOutputTokens > n {
			r.MaxOutputTokens = common.GetPointer(n)
		}
	case *dto.ClaudeRequest:
		if r.MaxTokens != nil && *r.MaxTokens == 0 {
			return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
		}
		if r.MaxTokens == nil || *r.MaxTokens > n {
			r.MaxTokens = common.GetPointer(n)
		}
	case *dto.GeminiChatRequest:
		if r.GenerationConfig.MaxOutputTokens != nil && *r.GenerationConfig.MaxOutputTokens == 0 {
			return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
		}
		if r.GenerationConfig.MaxOutputTokens == nil || *r.GenerationConfig.MaxOutputTokens > n {
			r.GenerationConfig.MaxOutputTokens = common.GetPointer(n)
		}
	}
	// Freeze the effective budget for parameter-aware pricing as well as DTO
	// estimation; the incoming raw JSON may still contain the old client default.
	storage, e := common.GetBodyStorage(c)
	if e != nil {
		return capacityError(c, "invalid_capacity_request", i18n.MsgCapacityReadParameters)
	}
	body, e := storage.Bytes()
	if e != nil {
		return capacityError(c, "invalid_capacity_request", i18n.MsgCapacityReadParameters)
	}
	switch r := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		if r.MaxTokens != nil {
			body, e = sjson.SetBytes(body, "max_tokens", *r.MaxTokens)
		}
		if r.MaxCompletionTokens != nil {
			body, e = sjson.SetBytes(body, "max_completion_tokens", *r.MaxCompletionTokens)
		}
	case *dto.OpenAIResponsesRequest:
		if r.MaxOutputTokens != nil {
			body, e = sjson.SetBytes(body, "max_output_tokens", *r.MaxOutputTokens)
		}
	case *dto.ClaudeRequest:
		if r.MaxTokens != nil {
			body, e = sjson.SetBytes(body, "max_tokens", *r.MaxTokens)
		}
	}
	if r, ok := info.Request.(*dto.GeminiChatRequest); ok && r.GenerationConfig.MaxOutputTokens != nil {
		body, e = sjson.DeleteBytes(body, "generationConfig.max_output_tokens")
		if e == nil {
			body, e = sjson.SetBytes(body, "generationConfig.maxOutputTokens", *r.GenerationConfig.MaxOutputTokens)
		}
	}
	if e == nil {
		body, e = clampNestedCapacityBudgets(body, int(n))
	}
	if e != nil {
		return capacityError(c, "invalid_output_budget", i18n.MsgCapacityNestedBudget)
	}
	info.BillingRequestInput = &billingexpr.RequestInput{Body: body, Headers: info.RequestHeaders, EvaluationTime: info.StartTime}
	return nil
}

// Runs at the last HTTP boundary, after model conversion, system prompt,
// channel overrides and passthrough. Native output hard caps include reasoning.
// Input counts below are tokenizer ESTIMATES, not provider-exact guarantees.
func EnforceModelCapacityOutbound(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	caps, err := capacityForRequest(c, info.OriginModelName)
	if err != nil {
		return capacityError(c, "model_capacity_unavailable", i18n.MsgCapacityConfigUnavailable)
	}
	if !caps.Managed {
		return nil
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if auto := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); auto != "" {
		group = auto
	}
	var route *model.CapacityRoute
	for i := range caps.Routes {
		r := &caps.Routes[i]
		if r.ChannelID == info.ChannelId && r.RouteModel == info.OriginModelName && r.Group == group {
			route = r
			break
		}
	}
	if route == nil {
		return capacityError(c, "model_capacity_unavailable", i18n.MsgCapacityRouteUnverified)
	}
	ch, err := model.CacheGetChannel(info.ChannelId)
	if err != nil || ch == nil || model.CapacityChannelConfigHash(ch) != route.ChannelConfigHash {
		return capacityError(c, "model_capacity_unavailable", i18n.MsgCapacityRouteChanged)
	}
	if req.Body == nil {
		return capacityError(c, "invalid_capacity_request", i18n.MsgCapacityJsonRequired)
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}
	var object map[string]any
	if err := common.Unmarshal(body, &object); err != nil {
		return capacityError(c, "invalid_capacity_request", i18n.MsgCapacityJsonRequired)
	}
	var chatEnvelope map[string]any
	if approved, ok := ycapi.Lookup(info.OriginModelName); ok && approved.Kind == "text" {
		params, valid := object["params"].(map[string]any)
		if !valid || object["model_id"] != approved.UpstreamID || object["modality"] != "chat" || object["lane_id"] != approved.Prices[0].LaneID {
			return capacityError(c, "model_route_unavailable", "The outgoing request does not match its verified model route")
		}
		chatEnvelope = object
		object = maps.Clone(params)
		object["messages"] = chatEnvelope["messages"]
		body, err = common.Marshal(object)
		if err != nil {
			return err
		}
	}
	limit := caps.MaxOutputTokens
	if route.MaxOutputTokens != nil && (limit == nil || *route.MaxOutputTokens < *limit) {
		limit = route.MaxOutputTokens
	}
	window := caps.ContextWindow
	if route.ContextWindow != nil && (window == nil || *route.ContextWindow < *window) {
		window = route.ContextWindow
	}
	if window != nil {
		// The existing tokenkit tokenizer includes message roles, history and tools.
		// It does not prove exact provider framing or multimodal token accounting.
		var chat dto.GeneralOpenAIRequest
		if _, ok := object["messages"]; ok && common.Unmarshal(body, &chat) == nil {
			meta := chat.GetTokenCountMeta()
			estimate := tokenkit.Count(info.UpstreamModelName, meta.CombineText) + meta.MessagesCount*3 + meta.NameCount*3 + meta.ToolsCount*8 + 3
			if len(meta.Files) == 0 {
				remaining := *window - estimate
				if remaining <= 0 {
					return capacityError(c, "context_length_exceeded", i18n.MsgCapacityContextExceeded)
				}
				if limit == nil || remaining < *limit {
					limit = common.GetPointer(remaining)
				}
			}
		}
	}
	if limit != nil {
		effective := *limit
		// Preserve an explicitly smaller caller/converted budget, including both
		// Chat Completions spellings. Invalid values cannot be used as bypasses.
		for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if value, ok := object[field]; ok {
				n, ok := value.(float64)
				if !ok || n <= 0 || n != float64(int64(n)) {
					return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
				}
				if n < float64(effective) {
					effective = int(n)
				}
			}
		}
		if gc, ok := object["generationConfig"].(map[string]any); ok {
			if value, exists := gc["maxOutputTokens"]; exists {
				n, ok := value.(float64)
				if !ok || n <= 0 || n != float64(int64(n)) {
					return capacityError(c, "invalid_output_budget", i18n.MsgCapacityOutputBudget)
				}
				if n < float64(effective) {
					effective = int(n)
				}
			}
		}
		for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generationConfig.max_output_tokens"} {
			body, err = sjson.DeleteBytes(body, field)
			if err != nil {
				return err
			}
		}
		budgetField := route.BudgetField
		if chatEnvelope != nil {
			budgetField = "max_tokens"
		} else if strings.Contains(req.URL.Path, "/responses") {
			budgetField = "max_output_tokens"
		} else if strings.Contains(req.URL.Path, "/messages") {
			budgetField = "max_tokens"
		} else if strings.Contains(req.URL.Path, ":generateContent") || strings.Contains(req.URL.Path, ":streamGenerateContent") {
			budgetField = "generationConfig.maxOutputTokens"
		}
		body, err = clampNestedCapacityBudgets(body, effective)
		if err != nil {
			return capacityError(c, "invalid_output_budget", i18n.MsgCapacityNestedBudget)
		}
		body, err = sjson.SetBytes(body, budgetField, effective)
		if err != nil {
			return err
		}
		c.Set("model_capacity_effective_output_tokens", effective)
	}
	if chatEnvelope != nil {
		var params map[string]any
		if err = common.Unmarshal(body, &params); err != nil {
			return err
		}
		delete(params, "messages")
		chatEnvelope["params"] = params
		body, err = common.Marshal(chatEnvelope)
		if err != nil {
			return err
		}
	}
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	req.ContentLength = int64(len(body))
	req.Header.Del("Content-Length")
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(body))), nil }
	return nil
}

// Only recognized request envelopes are inspected; schema/tool declarations
// and message contents must never be rewritten as if they were relay budgets.
func clampNestedCapacityBudgets(body []byte, limit int) ([]byte, error) {
	for _, envelope := range []string{"extra_body", "parameters"} {
		for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generationConfig.max_output_tokens"} {
			path := envelope + "." + field
			value := gjson.GetBytes(body, path)
			if !value.Exists() {
				continue
			}
			n := value.Float()
			if value.Type != gjson.Number || n <= 0 || n != float64(int64(n)) {
				return nil, fmt.Errorf("invalid nested output budget")
			}
			if n > float64(limit) {
				var err error
				body, err = sjson.SetBytes(body, path, limit)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return body, nil
}
