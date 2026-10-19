package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

// DecideModelFallback reports whether the relay may advance to the next
// configured fallback model after this attempt failed.
//
// Only a failure that says something about the upstream advances the candidate:
// the requested model had no usable channel, or a channel/upstream error. A local
// rejection (oversized body, quota, sensitive words, unresolved price) stays
// terminal — another model would fail the same way, and the operator's own error
// must not be reported to the client as congestion. A pinned channel or session
// affinity suppresses the fallback exactly as it suppresses a retry.
// @param c - request context carrying the channel constraints.
// @param err - the failed attempt's error.
// @param candidate - the index of the candidate about to be tried.
// @param total - the number of candidates the request may try.
// @returns true when the next candidate should be attempted.
func DecideModelFallback(c *gin.Context, err *types.NewAPIError, candidate int, total int) bool {
	if err == nil || candidate >= total || c.Request.Context().Err() != nil {
		return false
	}
	if GetChannelConstraints(c).SuppressesRetry() || ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	return IsUpstreamSideFailure(c, err)
}

// IsUpstreamSideFailure reports whether an error says the upstream is at fault —
// no usable channel, a channel failure, or a failure the retry policy would retry
// given a budget — as opposed to one the client's request or the deployment
// caused. Fallback candidates advance on these, and clients read congestion
// rather than an upstream detail.
func IsUpstreamSideFailure(c *gin.Context, err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if err.GetErrorCode() == types.ErrorCodeGetChannelFailed {
		return true
	}
	// A failure the relay refuses to retry is the client's or the deployment's,
	// not the upstream's: another model would answer the same way.
	if types.IsSkipRetryError(err) {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	// The remaining judgeable failures are the ones the retry policy would retry
	// given a budget: the upstream-side class the fallback exists for.
	return DecideRelayRetry(c, err, 1).Action == "retry"
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, common.LogText("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	recordRelayError(c, channelError.ChannelId, err, relayInfo)
}

const recordedRelayErrorKey = "recorded_relay_error"

// RecordRelayError fills the paths that fail before an upstream attempt. An
// attempt already recorded by ProcessChannelError is not recorded again when
// the controller presents the same terminal failure.
func RecordRelayError(c *gin.Context, err *types.NewAPIError, info *relaycommon.RelayInfo) {
	if c == nil || err == nil {
		return
	}
	if previous, exists := c.Get(recordedRelayErrorKey); exists && previous == err.ErrorWithStatusCode() {
		return
	}
	channelID := c.GetInt("channel_id")
	if err.GetErrorCode() == types.ErrorCodeGetChannelFailed {
		channelID = 0
	}
	recordRelayError(c, channelID, err, info)
}

func recordRelayError(c *gin.Context, channelID int, err *types.NewAPIError, info *relaycommon.RelayInfo) {
	if c == nil || !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	other := model.NewLogOther()
	if c.Request != nil && c.Request.URL != nil {
		other.SetPublic("request_path", c.Request.URL.Path)
	}
	taskID := c.Param("task_id")
	if taskID == "" {
		taskID = c.GetString("task_id")
	}
	if taskID != "" {
		other.SetPublic("task_id", taskID)
	}
	other.SetPublic("error_type", err.GetErrorType())
	other.SetPublic("error_code", err.GetErrorCode())
	other.SetPublic("status_code", err.StatusCode)
	AppendRelayLogAdminInfo(c, info, other)
	AppendResponseModelLogInfo(info, other)
	AppendTaskPluginContextAuditInfo(c, other)
	modelName := c.GetString("original_model")
	if info != nil && info.OriginModelName != "" {
		modelName = info.OriginModelName
	}
	if taskID != "" && modelName == "" {
		if task, exists, taskErr := model.GetByTaskId(c.GetInt("id"), taskID); taskErr == nil && exists {
			modelName = taskModelName(task)
			channelID = task.ChannelId
		}
	}
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	isStream := common.GetContextKeyBool(c, constant.ContextKeyIsStream) || info != nil && info.IsStream
	model.RecordErrorLog(c, c.GetInt("id"), channelID, modelName, c.GetString("token_name"), err.ErrorWithStatusCode(), c.GetInt("token_id"), int(time.Since(startTime).Seconds()), isStream, c.GetString("group"), other)
	c.Set(recordedRelayErrorKey, err.ErrorWithStatusCode())
}

// RecordStreamError preserves a protocol or transport failure discovered after
// the response headers were already sent. Client cancellation is not an
// upstream error, and a clean completed stream creates no error row.
func RecordStreamError(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil || info.StreamStatus == nil {
		return
	}
	stream := info.StreamStatus
	outcome := stream.OutcomeSnapshot()
	if outcome.Response != relaycommon.ResponseOutcomeFailed && !outcome.HasErrors &&
		(outcome.EndReason == relaycommon.StreamEndReasonClientGone || outcome.Response == relaycommon.ResponseOutcomeCancelled ||
			stream.IsNormalEnd() && (!outcome.ExpectsTerminal || outcome.Response == relaycommon.ResponseOutcomeCompleted)) {
		return
	}
	detail := stream.Summary()
	for _, message := range stream.ErrorMessages() {
		detail += "; " + message
	}
	code := types.ErrorCode(outcome.ErrorCode)
	if code == "" {
		code = types.ErrorCodeBadResponse
	}
	status := outcome.ErrorStatus
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	RecordRelayError(c, types.NewOpenAIError(errors.New(detail), code, status), info)
}

// RecordTaskFailure is called only by the durable insert/status-transition
// winner. It uses submission provenance rather than the last polling request.
func RecordTaskFailure(task *model.Task, stage, code string, status int, detail string) {
	if !constant.ErrorLogEnabled || task == nil || task.Status != model.TaskStatusFailure {
		return
	}
	c := &gin.Context{}
	username, _ := model.GetUsernameById(task.UserId, false)
	c.Set("username", username)
	other := model.NewLogOther()
	other.SetPublic("task_id", task.TaskID)
	other.SetPublic("error_stage", stage)
	other.SetPublic("error_type", "task_error")
	other.SetPublic("error_code", code)
	other.SetPublic("status_code", status)
	if execution := task.PrivateData.Execution; execution != nil {
		c.Set(common.RequestIdKey, execution.RequestID)
		other.SetPublic("request_path", execution.RequestPath)
		AppendTaskPluginAuditInfo(other, execution.TaskPlugin)
	}
	if task.Properties.UpstreamModelName != "" && task.Properties.UpstreamModelName != taskModelName(task) {
		other.SetAdmin("upstream_model_name", task.Properties.UpstreamModelName)
	}
	message := task.FailReason
	if strings.TrimSpace(detail) != "" && detail != message {
		message += "; " + detail
	}
	model.RecordErrorLog(c, task.UserId, task.ChannelId, taskModelName(task), "", fmt.Sprintf("status_code=%d, %s", status, message), task.PrivateData.TokenId, 0, false, task.Group, other)
}
