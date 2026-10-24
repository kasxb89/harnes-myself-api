package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const mediaSubmissionContextKey = "media_durable_submission"
const MediaCapacityCode = "media_account_capacity_exhausted"
const MediaUnknownCode = "media_submission_unknown"
const MediaNotSentCode = "media_submission_not_sent"
const MediaRejectedCode = "media_submission_rejected"

type MediaSubmission struct {
	Task                *model.Task
	MediaKind           string
	MayHaveSent         bool
	BillingUncertain    bool
	Unknown             bool
	SlotReleased        bool
	Rejected            bool
	RejectionBody       []byte
	RefundConfirmed     bool
	NotSubmittedReceipt *MediaSubmissionReceipt
}

type MediaSubmissionReceipt struct {
	H3SubmissionReceipt
	SchemaVersion            int    `json:"schema_version"`
	Model                    string `json:"model"`
	MediaKind                string `json:"media_kind"`
	RejectionReason          string `json:"rejection_reason,omitempty"`
	ProviderResponseReceived bool   `json:"provider_response_received,omitempty"`
}

func MediaSubmissionFromContext(c *gin.Context) *MediaSubmission {
	if c == nil {
		return nil
	}
	value, _ := c.Get(mediaSubmissionContextKey)
	submission, _ := value.(*MediaSubmission)
	return submission
}

// NextMediaAdmissionKey is used only after an unsent capacity rejection. It
// reads enabled keys without advancing the channel's polling cursor and refuses
// a changed channel configuration instead of combining two request snapshots.
func NextMediaAdmissionKey(c *gin.Context, current *model.Channel, info *relaycommon.RelayInfo, attempted map[string]bool) (string, int, error) {
	channel, err := model.GetChannelById(current.Id, true)
	if err != nil {
		return "", 0, err
	}
	if !channel.ChannelInfo.IsMultiKey {
		return "", 0, nil
	}
	// The first getChannel call returns an identity-only stub. RelayInfo and
	// context carry the actual channel metadata frozen by the distributor.
	var settings kitdto.ChannelSettings
	var other kitdto.ChannelOtherSettings
	parameters, headers := map[string]any{}, map[string]any{}
	if channel.Setting != nil && *channel.Setting != "" && common.UnmarshalJsonStr(*channel.Setting, &settings) != nil ||
		channel.OtherSettings != "" && common.UnmarshalJsonStr(channel.OtherSettings, &other) != nil ||
		channel.ParamOverride != nil && *channel.ParamOverride != "" && common.UnmarshalJsonStr(*channel.ParamOverride, &parameters) != nil ||
		channel.HeaderOverride != nil && *channel.HeaderOverride != "" && common.UnmarshalJsonStr(*channel.HeaderOverride, &headers) != nil {
		return "", 0, model.ErrMediaAdmissionUnavailable
	}
	if info == nil || info.ChannelMeta == nil || channel.Status != common.ChannelStatusEnabled || channel.Type != info.ChannelType ||
		channel.GetBaseURL() != info.ChannelBaseUrl || channel.GetModelMapping() != common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping) ||
		!reflect.DeepEqual(settings, info.ChannelSetting) || !reflect.DeepEqual(other, info.ChannelOtherSettings) ||
		(len(parameters) != 0 || len(info.ParamOverride) != 0) && !reflect.DeepEqual(parameters, info.ParamOverride) ||
		(len(headers) != 0 || len(info.HeadersOverride) != 0) && !reflect.DeepEqual(headers, info.HeadersOverride) ||
		!model.IsChannelEnabledForGroupModel(info.UsingGroup, info.OriginModelName, channel.Id) {
		return "", 0, model.ErrMediaAdmissionUnavailable
	}
	keys := channel.GetKeys()
	if len(keys) > 64 {
		return "", 0, model.ErrMediaAdmissionUnavailable
	}
	for index, key := range keys {
		status, explicit := channel.ChannelInfo.MultiKeyStatusList[index]
		if strings.TrimSpace(key) != "" && !attempted[key] && (!explicit || status == common.ChannelStatusEnabled) {
			return key, index, nil
		}
	}
	return "", 0, nil
}

// TaskMediaKind identifies task-backed media only. Ordinary Go image HTTP/SSE
// relays do not use this durable lifecycle and must not advertise its hint.
func TaskMediaKind(c *gin.Context, info *relaycommon.RelayInfo) string {
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		if endpoint, ok := value.(jsplugin.PinnedEndpoint); ok {
			switch endpoint.Protocol {
			case "openai_video":
				return model.MediaVideo
			case jsplugin.ProtocolOpenAIImage:
				return model.MediaImage
			}
		}
	}
	value, _ := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, _ := value.(jsplugin.PinnedPlugin)
	if pinned.Plugin == nil {
		return ""
	}
	schema, _ := pinned.Plugin.Meta.UsageForModels(info.UpstreamModelName, info.OriginModelName)
	if field, exists := schema["image_count"]; exists && field.Type == "number" && field.Unit == "count" {
		return model.MediaImage
	}
	for _, claim := range pinned.Plugin.Meta.Protocols {
		if claim.Name == "openai_video" {
			return model.MediaVideo
		}
	}
	return ""
}

func PrepareMediaSubmission(c *gin.Context, info *relaycommon.RelayInfo, platform constant.TaskPlatform) *dto.TaskError {
	if H3SubmissionFromContext(c) != nil || MediaSubmissionFromContext(c) != nil {
		return nil
	}
	mediaKind := TaskMediaKind(c, info)
	if mediaKind == "" {
		return nil
	}
	task := model.InitTask(platform, info)
	task.PrivateData.Execution = TaskExecutionSnapshotFromContext(c)
	task.PrivateData.RouteDigest = mediaSubmissionRouteDigest(c, info)
	if task.PrivateData.RouteDigest == "" {
		return TaskErrorWrapperLocal(errors.New("Media execution route is unconfirmed"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	if err := BindVideoQueueTask(c, task); err != nil {
		return TaskErrorWrapperLocal(err, "video_queue_state_unconfirmed", http.StatusServiceUnavailable)
	}
	if err := model.AcquireMediaAdmission(c.Request.Context(), task, info.ApiKey, mediaKind); err != nil {
		if errors.Is(err, model.ErrMediaLegacyIdentity) {
			return TaskErrorWrapperLocal(errors.New("Historical media account identity requires administrator verification"), "media_legacy_identity_unconfirmed", http.StatusServiceUnavailable)
		}
		if errors.Is(err, model.ErrMediaCapacity) {
			RecordVideoQueueCapacityCandidate(c, task)
			task.PrivateData.Key = info.ApiKey
			c.Set("media_capacity_candidate", &MediaSubmission{Task: task, MediaKind: mediaKind})
			return TaskErrorWrapperLocal(err, MediaCapacityCode, http.StatusTooManyRequests)
		}
		return TaskErrorWrapperLocal(errors.New("Media account admission is unavailable"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	c.Set(mediaSubmissionContextKey, &MediaSubmission{Task: task, MediaKind: mediaKind})
	c.Header("X-New-Api-Task-Id", task.TaskID)
	return nil
}

// RecordMediaCapacityRejection runs only after the controller has exhausted
// allowed pool candidates. The failed record never acquires a slot or funds.
func RecordMediaCapacityRejection(c *gin.Context) *dto.TaskError {
	if dispatch := VideoQueueDispatchFromContext(c); dispatch != nil && dispatch.CapacityRefused && !dispatch.SendFenced {
		// The queue retains the original operation. No failed task or financial
		// reservation is created merely because its supplier pool is full.
		return nil
	}
	value, _ := c.Get("media_capacity_candidate")
	submission, _ := value.(*MediaSubmission)
	if submission == nil || submission.Task == nil {
		return TaskErrorWrapperLocal(errors.New("Failed to verify media capacity rejection"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := model.RecordMediaCapacityRejection(ctx, submission.Task, submission.Task.PrivateData.Key, submission.MediaKind); err != nil {
		return TaskErrorWrapperLocal(errors.New("Failed to persist media capacity rejection"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	receipt, _, err := ProjectMediaTaskSubmission(ctx, submission.Task)
	if err != nil || receipt == nil {
		return TaskErrorWrapperLocal(errors.New("Failed to verify media capacity rejection"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	submission.SlotReleased, submission.NotSubmittedReceipt = true, receipt
	c.Set(mediaSubmissionContextKey, submission)
	c.Header("X-New-Api-Task-Id", submission.Task.TaskID)
	return nil
}

func SaveMediaSubmissionBilling(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	submission := MediaSubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	PopulateTaskBillingSnapshot(c, info, submission.Task)
	submission.Task.Quota = info.PriceData.Quota
	if err := model.SaveMediaBillingReservation(c.Request.Context(), submission.Task); err != nil {
		return TaskErrorWrapperLocal(errors.New("Failed to persist media billing reservation"), "media_billing_snapshot_failed", http.StatusServiceUnavailable)
	}
	submission.BillingUncertain = false
	return nil
}

func BeginMediaBilling(c *gin.Context) error {
	submission := MediaSubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	if err := model.SetMediaBillingState(c.Request.Context(), submission.Task.PrivateData.MediaAdmissionID, "preparing"); err != nil {
		return err
	}
	submission.BillingUncertain = true
	return nil
}

func MediaBillingProvenUncharged(c *gin.Context) {
	if submission := MediaSubmissionFromContext(c); submission != nil {
		submission.BillingUncertain = false
		_ = model.SetMediaBillingState(c.Request.Context(), submission.Task.PrivateData.MediaAdmissionID, "not_charged")
	}
}

func BeginMediaSubmissionSend(c *gin.Context) *dto.TaskError {
	submission := MediaSubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	if err := VerifyVideoQueueSending(c, submission.Task); err != nil {
		return TaskErrorWrapperLocal(err, "video_queue_state_unconfirmed", http.StatusServiceUnavailable)
	}
	// A lost response to this write might mean it committed. From here on no
	// error, cancellation or retry policy may authorize another provider POST.
	submission.MayHaveSent = true
	if err := model.MarkMediaSending(c.Request.Context(), submission.Task.PrivateData.MediaAdmissionID); err != nil {
		return TaskErrorWrapperLocal(errors.New("Media submission fence is uncertain"), MediaUnknownCode, http.StatusServiceUnavailable)
	}
	return nil
}

func SaveMediaProviderReceipt(c *gin.Context, providerID string, data, state []byte, discardData bool) *dto.TaskError {
	submission := MediaSubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	submission.Task.PrivateData.UpstreamTaskID = providerID
	submission.Task.PrivateData.PluginState = state
	submission.Task.Data = data
	if discardData {
		submission.Task.Data = nil
	}
	submission.Task.SubmitTime = time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := model.SaveMediaReceipt(ctx, submission.Task); err != nil {
		return TaskErrorWrapperLocal(errors.New("Failed to persist media provider receipt"), MediaUnknownCode, http.StatusServiceUnavailable)
	}
	return nil
}

func PreserveMediaUnknown(submission *MediaSubmission) {
	if submission == nil {
		return
	}
	first := !submission.Unknown
	submission.Unknown = true
	submission.Task.Status = model.TaskStatusUnknown
	submission.Task.Progress = "0%"
	submission.Task.FinishTime = 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = model.HoldMediaUnknown(ctx, submission.Task)
	if first {
		RecordTaskFailure(submission.Task, "task_submit", MediaUnknownCode, http.StatusServiceUnavailable, "")
	}
}

// RecordMediaAttemptFailure retains the failed provider attempt without taking
// a retry or channel-health action after the durable sending fence. The copied
// context shares this request's policy history but cannot request an auto-ban.
func RecordMediaAttemptFailure(c *gin.Context, info *relaycommon.RelayInfo, channelID int, taskErr *dto.TaskError) *dto.TaskError {
	if taskErr == nil {
		taskErr = TaskErrorWrapperLocal(errors.New("Media submission returned no provider receipt"), "task_submit_missing_receipt", http.StatusBadGateway)
	}
	safe := *taskErr
	redact := func(value string, limit int) string {
		if info != nil && info.ApiKey != "" {
			value = strings.ReplaceAll(value, info.ApiKey, "[REDACTED]")
		}
		value = common.MaskCredentials(value)
		if len(value) > limit {
			value = value[:limit] + " [truncated]"
		}
		return value
	}
	safe.Code = redact(safe.Code, 128)
	safe.Message = redact(safe.Message, 4096)
	if safe.Error != nil {
		safe.Error = errors.New(redact(safe.Error.Error(), 4096))
	} else {
		safe.Error = errors.New(safe.Message)
	}
	safe.Data = nil
	recording := c.Copy()
	recording.Set("auto_ban", false)
	if submission := MediaSubmissionFromContext(c); submission != nil && submission.Task != nil {
		recording.Set("task_id", submission.Task.TaskID)
	}
	apiErr := types.NewOpenAIError(safe.Error, types.ErrorCode(safe.Code), safe.StatusCode)
	RecordPolicyFailure(recording, channelID, apiErr, PolicyDecision{Action: "stop", Reason: MediaUnknownCode, Source: "system"})
	RecordRelayError(recording, apiErr, info)
	return &safe
}

// ProjectMediaTaskSubmission delegates existing H3 projections unchanged. The
// generic proof is published as media_submission_receipt by API presenters;
// old video clients reserve submission_receipt for exact H3 evidence.
func ProjectMediaTaskSubmission(ctx context.Context, task *model.Task) (*MediaSubmissionReceipt, bool, error) {
	if task == nil || task.PrivateData.MediaAdmissionID == "" {
		if model.MediaLegacyIdentityUnknown(task) {
			task.Status, task.Progress, task.FinishTime, task.FailReason = model.TaskStatusUnknown, "0%", 0, ""
			task.Data, task.PrivateData.ResultURL = nil, ""
			return nil, false, nil
		}
		_, terminal, err := ProjectH3TaskSubmission(ctx, task)
		return nil, terminal, err
	}
	rejected, rejectionErr := model.MediaConfirmedCreateRejectionState(ctx, task)
	if rejectionErr != nil {
		return nil, false, rejectionErr
	}
	if rejected {
		// This request crossed the durable send fence and received a verified
		// refusal. It must never masquerade as the older not-sent proof.
		receipt := &MediaSubmissionReceipt{H3SubmissionReceipt: H3SubmissionReceipt{ID: task.TaskID, TaskID: task.TaskID, SubmissionCode: MediaRejectedCode, SubmissionState: "not_submitted", BillingState: "refunded", UpstreamSubmitted: true, Resubmittable: true, RefundConfirmed: true}, SchemaVersion: 1, Model: task.Properties.OriginModelName, MediaKind: task.PrivateData.MediaKind, RejectionReason: "upstream_model_not_available", ProviderResponseReceived: true}
		receipt.Recovery.Method, receipt.Recovery.Path = http.MethodGet, "/v1/tasks/"+task.TaskID
		return receipt, false, nil
	}
	billingState, terminal, err := model.MediaConfirmedFailureState(ctx, task)
	if err != nil {
		return nil, false, err
	}
	if billingState != "" {
		receipt := &MediaSubmissionReceipt{H3SubmissionReceipt: H3SubmissionReceipt{ID: task.TaskID, TaskID: task.TaskID, SubmissionCode: MediaNotSentCode,
			SubmissionState: "not_submitted", BillingState: billingState, Resubmittable: true, RefundConfirmed: billingState == "refunded"}, SchemaVersion: 1, Model: task.Properties.OriginModelName, MediaKind: task.PrivateData.MediaKind}
		receipt.Recovery.Method, receipt.Recovery.Path = http.MethodGet, "/v1/tasks/"+task.TaskID
		return receipt, false, nil
	}
	if !terminal && (task.Status == model.TaskStatusUnknown || task.Status == model.TaskStatusFailure) {
		task.Status, task.Progress, task.FinishTime, task.FailReason = model.TaskStatusUnknown, "0%", 0, ""
		task.Data, task.PrivateData.ResultURL = nil, ""
	}
	return nil, terminal, nil
}

// SanitizeMediaProofFields gives presenters an owned JSON value with all
// gateway proof fields removed. Only the caller's verified database projection
// may add them afterwards; plugin/vendor JSON is never financial evidence.
func SanitizeMediaProofFields(value any) (any, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	var owned any
	if err := common.Unmarshal(encoded, &owned); err != nil {
		return nil, err
	}
	if err := stripMediaProofFields(owned, 0); err != nil {
		return nil, err
	}
	return owned, nil
}

func stripMediaProofFields(value any, depth int) error {
	if depth > 64 {
		return errors.New("Task response is too deeply nested")
	}
	switch value := value.(type) {
	case map[string]any:
		delete(value, "media_submission_receipt")
		delete(value, "submission_receipt")
		delete(value, "provider_terminal_confirmed")
		for _, item := range value {
			if err := stripMediaProofFields(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range value {
			if err := stripMediaProofFields(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func mediaSubmissionRouteDigest(c *gin.Context, info *relaycommon.RelayInfo) string {
	value, _ := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, _ := value.(jsplugin.PinnedPlugin)
	if pinned.Plugin == nil || info == nil {
		return ""
	}
	return model.MediaTaskPluginRouteDigest(info.ChannelMeta, pinned.Plugin.Meta)
}

// ClassifyMediaCreateRejection examines only the response to this request's
// original vendor POST. A GET 404 and plugin-authored receipt never reach it.
func ClassifyMediaCreateRejection(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte) bool {
	submission := MediaSubmissionFromContext(c)
	if submission == nil || !submission.MayHaveSent || submission.Task == nil || info == nil || info.ChannelMeta == nil || resp == nil || resp.StatusCode != http.StatusNotFound || resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	if info.ApiKey != submission.Task.PrivateData.Key {
		return false
	}
	authorization := resp.Request.Header.Values("Authorization")
	if len(authorization) != 1 || authorization[0] != "Bearer "+submission.Task.PrivateData.Key {
		return false
	}
	contentType, _, contentErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentErr != nil || contentType != "application/json" {
		return false
	}
	for name, values := range resp.Header {
		normalized := strings.ToLower(strings.ReplaceAll(name, "-", ""))
		if normalized == "location" || normalized == "operationlocation" || normalized == "xtaskid" || normalized == "xjobid" || normalized == "xgenerationid" {
			for _, value := range values {
				if strings.TrimSpace(value) != "" {
					return false
				}
			}
		}
	}
	value, _ := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, _ := value.(jsplugin.PinnedPlugin)
	if pinned.Plugin == nil || pinned.Generation == nil || pinned.Plugin.Meta.Key != "blackrxai-video" || pinned.Plugin.Meta.Version != "1.0.2" || pinned.Plugin.Meta.APIVersion != 1 || pinned.Plugin.SourceHash != model.BlackrxCreateRejectionSourceHash ||
		info.ChannelBaseUrl != "https://hi.blackrxai.com" || info.ChannelSetting.TLSInsecureSkipVerify || info.ChannelType != constant.ChannelTypeTaskPlugin ||
		resp.Request.Method != http.MethodPost || resp.Request.URL.String() != "https://hi.blackrxai.com/v1/videos" ||
		submission.Task.PrivateData.Execution == nil || submission.Task.PrivateData.Execution.TaskPlugin == nil ||
		submission.Task.PrivateData.Execution.TaskPlugin.Generation != pinned.Generation.Number ||
		submission.Task.PrivateData.RouteDigest != model.MediaTaskPluginRouteDigest(info.ChannelMeta, pinned.Plugin.Meta) ||
		!model.IsBlackrxModelMissingCreateBody(body, info.UpstreamModelName) {
		return false
	}
	submission.Rejected = true
	submission.RejectionBody = append([]byte(nil), body...)
	return true
}

// ResolveMediaCreateRejection uses the exact original BillingSession. Funds,
// token reservation, task failure and slot release commit atomically. Failed
// recovery is left pending; it never falls back to the ordinary Refund path.
func ResolveMediaCreateRejection(c *gin.Context, info *relaycommon.RelayInfo) error {
	submission := MediaSubmissionFromContext(c)
	if submission == nil || !submission.Rejected || info == nil {
		return model.ErrMediaAdmissionUnavailable
	}
	session, ok := info.Billing.(*BillingSession)
	if !ok {
		return model.ErrMediaAdmissionUnavailable
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.settled || session.fundingSettled || session.relayInfo != info || submission.Task.PrivateData.Execution == nil || submission.Task.PrivateData.Execution.TaskPlugin == nil {
		return model.ErrMediaAdmissionUnavailable
	}
	task := submission.Task
	plugin := task.PrivateData.Execution.TaskPlugin
	hash := sha256.Sum256(submission.RejectionBody)
	proof := model.MediaCreateRejection{SchemaVersion: 1, TaskID: task.TaskID, RequestID: task.PrivateData.Execution.RequestID, RouteDigest: task.PrivateData.RouteDigest, PluginVersion: plugin.Version, PluginSourceHash: plugin.SourceHash, PluginGeneration: plugin.Generation, UpstreamModel: info.UpstreamModelName, URL: "https://hi.blackrxai.com/v1/videos", Method: http.MethodPost, HTTPStatus: http.StatusNotFound, Body: string(submission.RejectionBody), BodySHA256: hex.EncodeToString(hash[:]), OriginalQuota: task.Quota, FundingSource: session.funding.Source(), TokenID: task.PrivateData.TokenId, TokenReserved: session.tokenConsumed, SubscriptionID: task.PrivateData.SubscriptionId, SubscriptionExtra: session.extraReserved}
	switch funding := session.funding.(type) {
	case *WalletFunding:
		if funding.userId != task.UserId || funding.purpose != "relay" || funding.requestId != task.PrivateData.FundingRequestId || funding.tokenID != task.PrivateData.TokenId {
			return model.ErrMediaAdmissionUnavailable
		}
		proof.FundingRequestID, proof.FundingReserved = funding.requestId, funding.consumed
	case *SubscriptionFunding:
		if funding.userId != task.UserId || funding.subscriptionId != task.PrivateData.SubscriptionId || funding.preConsumed < 0 || funding.preConsumed > int64(common.MaxWalletQuota) {
			return model.ErrMediaAdmissionUnavailable
		}
		proof.FundingRequestID, proof.FundingReserved = funding.requestId, int(funding.preConsumed)
	default:
		return model.ErrMediaAdmissionUnavailable
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := model.SaveMediaCreateRejection(ctx, task, proof); err != nil {
		return err
	}
	if err := model.RefundMediaCreateRejection(ctx, task); err != nil {
		return err
	}
	session.refunded = true
	session.tokenConsumed = 0
	session.extraReserved = 0
	if wallet, ok := session.funding.(*WalletFunding); ok {
		wallet.consumed = 0
	}
	var persisted model.Task
	if err := model.DB.WithContext(ctx).Where("id = ? AND task_id = ? AND user_id = ?", task.ID, task.TaskID, task.UserId).First(&persisted).Error; err != nil {
		return err
	}
	receipt, _, err := ProjectMediaTaskSubmission(ctx, &persisted)
	if err != nil {
		return err
	}
	if receipt == nil {
		return model.ErrMediaAdmissionUnavailable
	}
	*task = persisted
	submission.SlotReleased, submission.RefundConfirmed, submission.NotSubmittedReceipt = true, true, receipt
	return nil
}
