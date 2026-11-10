package service

import (
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const ModelConfigurationHeader = "X-Yunshu-Model-Config-Version"
const ModelConfigurationResultHeader = "X-Yunshu-Model-Config-Result"
const modelConfigurationCaptureKey = "model_configuration_capture"

func CheckModelBillingConfiguration(c *gin.Context, name string) *types.NewAPIError {
	if model.ModelBillingConfigured(name) {
		return nil
	}
	return modelConfigurationRejection(c, "model_price_unavailable")
}

func BindCapturedTextRoutes(c *gin.Context, info *relaycommon.RelayInfo) {
	info.TextRouteBindings = nil
	value, exists := c.Get(modelConfigurationCaptureKey)
	if !exists {
		return
	}
	capture := value.(modelConfigurationCapture)
	if capture.Record == nil || capture.Record.Definition.Kind != "text" {
		return
	}
	info.TextRouteBindings = make([]relaycommon.TextRouteBinding, 0, len(capture.Record.Definition.Routes))
	for _, route := range capture.Record.Definition.Routes {
		if route.Enabled {
			info.TextRouteBindings = append(info.TextRouteBindings, relaycommon.TextRouteBinding{ChannelID: route.ChannelID, Group: route.Group, UpstreamModel: route.UpstreamModel, Protocol: route.TextProtocol})
		}
	}
}

// An explicit binding chooses the existing host adapter, independently of the
// stable public model ID. Legacy records retain their original prefix policy.
// The captured routes survive configuration changes and channel retries.
func YCAPITextModel(info *relaycommon.RelayInfo) (ycapi.Model, bool) {
	if info.TextRouteBindings != nil {
		for _, route := range info.TextRouteBindings {
			if info.ChannelMeta == nil || route.ChannelID != info.ChannelId || info.UsingGroup != "" && info.UsingGroup != "auto" && route.Group != info.UsingGroup {
				continue
			}
			switch route.Protocol {
			case "openai":
				return ycapi.Model{}, false
			case "ycapi_fixed_lane":
				entry, ok := ycapi.Lookup(route.UpstreamModel)
				if !ok {
					entry, ok = ycapi.Lookup("ycapi-" + route.UpstreamModel)
				}
				return entry, ok && entry.Kind == "text"
			}
		}
	}
	entry, ok := ycapi.Lookup(info.OriginModelName)
	return entry, ok && entry.Kind == "text"
}

type modelConfigurationCapture struct {
	Name, Version string
	Record        *model.PublishedModelConfiguration
}

func modelConfigurationRejection(c *gin.Context, code string) *types.NewAPIError {
	if !c.GetBool("model_configuration_billing_started") && H3SubmissionFromContext(c) == nil && MediaSubmissionFromContext(c) == nil {
		c.Set("model_configuration_unsent", true)
		c.Header("X-New-Api-Task-Id", "")
		c.Header(ModelConfigurationResultHeader, "rejected-before-billing")
	}
	return types.WithOpenAIError(types.OpenAIError{Code: code, Type: "invalid_request_error", Message: "Model configuration changed or the selected parameters are unavailable. Refresh the model list before a new operation."}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// Capture is request-local. Existing task retrieval never calls it. An absent
// header is compatible with old clients, but a supplied stale header fails shut.
func CaptureModelConfiguration(c *gin.Context, name string) *types.NewAPIError {
	if value, ok := c.Get(modelConfigurationCaptureKey); ok {
		capture := value.(modelConfigurationCapture)
		if capture.Name == name {
			return nil
		}
		if capture.Version != "" || c.GetHeader(ModelConfigurationHeader) != "" {
			return modelConfigurationRejection(c, "model_config_changed")
		}
	}
	release := model.ModelConfigurationReadLock()
	defer release()
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return modelConfigurationRejection(c, "model_config_unavailable")
	}
	capture := modelConfigurationCapture{Name: name}
	if record, ok := records[name]; ok {
		if !record.Definition.Enabled {
			return modelConfigurationRejection(c, "model_not_available")
		}
		capture.Record = &record
		capture.Version = model.ModelExecutionConfigurationVersion(name, record)
		if capture.Version == "" {
			return modelConfigurationRejection(c, "model_config_unavailable")
		}
	}
	requested := c.GetHeader(ModelConfigurationHeader)
	if requested != "" && (capture.Version == "" || requested != capture.Version) {
		return modelConfigurationRejection(c, "model_config_changed")
	}
	c.Set(modelConfigurationCaptureKey, capture)
	if capture.Version != "" {
		c.Header(ModelConfigurationHeader, capture.Version)
	}
	return nil
}

// LockModelConfiguration freezes the pricing read and reservation boundary.
// The caller must release before any provider HTTP request.
func LockModelConfiguration(c *gin.Context, name string, request any, usage ...map[string]any) (func(), *types.NewAPIError) {
	if err := CaptureModelConfiguration(c, name); err != nil {
		return func() {}, err
	}
	release := model.ModelConfigurationReadLock()
	fail := func(code string) (func(), *types.NewAPIError) {
		release()
		return func() {}, modelConfigurationRejection(c, code)
	}
	value, _ := c.Get(modelConfigurationCaptureKey)
	capture := value.(modelConfigurationCapture)
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return fail("model_config_unavailable")
	}
	record, managed := records[name]
	current := ""
	if managed {
		current = model.ModelExecutionConfigurationVersion(name, record)
	}
	if current != capture.Version || managed && !record.Definition.Enabled {
		return fail("model_config_changed")
	}
	if managed && common.GetContextKeyInt(c, constant.ContextKeyChannelId) > 0 {
		channel := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		if !slices.ContainsFunc(record.Definition.Routes, func(route model.ModelConfigurationRoute) bool {
			return route.Enabled && route.ChannelID == channel && (group == "" || group == "auto" || route.Group == group) && model.IsChannelEnabledForGroupModel(route.Group, name, channel)
		}) {
			return fail("model_config_changed")
		}
	}
	if capture.Record != nil && capture.Record.Definition.Parameters != nil && request != nil {
		encoded, err := common.Marshal(request)
		if err != nil {
			return fail("model_parameters_invalid")
		}
		var parameters map[string]any
		if common.Unmarshal(encoded, &parameters) != nil {
			return fail("model_parameters_invalid")
		}
		if len(usage) > 0 {
			parameters = model.ModelConfigurationUsageParameters(parameters, usage[0])
		}
		if model.ModelParameterValues(capture.Record.Definition.Parameters, parameters) != nil {
			return fail("model_parameters_invalid")
		}
	}
	return release, nil
}

func CapturedModelConfigurationVersion(c *gin.Context) string {
	value, ok := c.Get(modelConfigurationCaptureKey)
	if !ok {
		return ""
	}
	return value.(modelConfigurationCapture).Version
}
