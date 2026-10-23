package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const h3SubmissionContextKey = "h3_durable_submission"
const H3CapacityCode = "h3_account_capacity_exhausted"
const H3UnknownCode = "h3_submission_unknown"
const H3NotSentCode = "h3_submission_not_sent"

type H3SubmissionReceipt struct {
	ID                string `json:"id"`
	TaskID            string `json:"task_id"`
	SubmissionCode    string `json:"submission_code"`
	SubmissionState   string `json:"submission_state"`
	BillingState      string `json:"billing_state"`
	UpstreamSubmitted bool   `json:"upstream_submitted"`
	Retryable         bool   `json:"retryable"`
	Resubmittable     bool   `json:"resubmittable"`
	RefundConfirmed   bool   `json:"refund_confirmed,omitempty"`
	Recovery          struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"recovery"`
}

// H3NotSubmittedReceipt is a read-only projection of the durable send and
// billing ledgers. It never releases capacity, changes status, or refunds.
func H3NotSubmittedReceipt(ctx context.Context, task *model.Task) (*H3SubmissionReceipt, error) {
	receipt, _, err := h3FailureReceipt(ctx, task)
	return receipt, err
}

// ProjectH3TaskSubmission changes only the caller's read-only response copy.
// A local FAILURE must not tell old clients to unlock an uncertain operation.
// ProviderTerminalConfirmed is evidence of the original supplier task ending,
// not evidence of a complete refund and not an instruction to retry.
func ProjectH3TaskSubmission(ctx context.Context, task *model.Task) (*H3SubmissionReceipt, bool, error) {
	receipt, providerTerminal, err := h3FailureReceipt(ctx, task)
	if err != nil {
		return nil, false, err
	}
	if model.IsH3Task(task) && (task.Status == model.TaskStatusFailure || task.Status == model.TaskStatusUnknown) && receipt == nil && !providerTerminal {
		task.Status = model.TaskStatusUnknown
		task.Progress = "0%"
		task.FinishTime = 0
		task.FailReason = ""
		task.Data = nil
		task.PrivateData.ResultURL = ""
	}
	return receipt, providerTerminal, nil
}

func h3FailureReceipt(ctx context.Context, task *model.Task) (*H3SubmissionReceipt, bool, error) {
	billingState, providerTerminal, err := model.H3ConfirmedFailureState(ctx, task)
	if err != nil || billingState == "" {
		return nil, providerTerminal, err
	}
	receipt := &H3SubmissionReceipt{ID: task.TaskID, TaskID: task.TaskID, SubmissionCode: H3NotSentCode, SubmissionState: "not_submitted", BillingState: billingState, Resubmittable: true, RefundConfirmed: billingState == "refunded"}
	receipt.Recovery.Method = http.MethodGet
	receipt.Recovery.Path = "/v1/tasks/" + task.TaskID
	return receipt, false, nil
}

type H3Submission struct {
	Rejected            bool
	RejectedHTTPStatus  int
	SlotReleased        bool
	RefundConfirmed     bool
	Task                *model.Task
	NotSubmittedReceipt *H3SubmissionReceipt
	// Set before attempting the durable send fence. Even a lost DB response
	// may mean it committed; uncertainty must not turn into a refund or retry.
	MayHaveSent      bool
	BillingUncertain bool
	Unknown          bool
}

func H3SubmissionFromContext(c *gin.Context) *H3Submission {
	if c == nil {
		return nil
	}
	value, _ := c.Get(h3SubmissionContextKey)
	submission, _ := value.(*H3Submission)
	return submission
}

func PrepareH3Submission(c *gin.Context, info *relaycommon.RelayInfo, platform constant.TaskPlatform) *dto.TaskError {
	name := info.UpstreamModelName
	if name == "" {
		name = info.OriginModelName
	}
	if !strings.EqualFold(name, "MiniMax-H3") || (platform != "hailuo" && platform != "35") {
		return nil
	}
	if H3SubmissionFromContext(c) != nil {
		return nil
	}
	task := model.InitTask(platform, info)
	task.PrivateData.Execution = TaskExecutionSnapshotFromContext(c)
	task.PrivateData.RouteDigest = mediaSubmissionRouteDigest(c, info)
	if task.PrivateData.RouteDigest == "" {
		return TaskErrorWrapperLocal(errors.New("Media execution route is unconfirmed"), "media_admission_unavailable", http.StatusServiceUnavailable)
	}
	if err := model.AcquireH3Admission(c.Request.Context(), task, info.ApiKey); err != nil {
		if errors.Is(err, model.ErrH3Capacity) {
			return TaskErrorWrapperLocal(err, H3CapacityCode, http.StatusTooManyRequests)
		}
		return TaskErrorWrapperLocal(errors.New("H3 account admission is unavailable"), "h3_admission_unavailable", http.StatusServiceUnavailable)
	}
	c.Set(h3SubmissionContextKey, &H3Submission{Task: task})
	c.Header("X-New-Api-Task-Id", task.TaskID)
	return nil
}

// PopulateTaskBillingSnapshot records the pre-send H3 funding identity using
// the same facts as the ordinary post-receipt path; it performs no payment.
func PopulateTaskBillingSnapshot(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	task.PrivateData.Execution = TaskExecutionSnapshotFromContext(c)
	task.PrivateData.BillingSource = info.BillingSource
	task.PrivateData.SubscriptionId = info.SubscriptionId
	task.PrivateData.TokenId = info.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{ModelPrice: info.PriceData.ModelPrice, GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio, ModelRatio: info.PriceData.ModelRatio, OtherRatios: info.PriceData.OtherRatios(), OriginModelName: info.OriginModelName, PerCallBilling: common.StringsContains(constant.TaskPricePatches, info.OriginModelName) || info.PriceData.UsePrice, TieredSnapshot: info.TieredBillingSnapshot}
	task.Action = info.Action
}

func SaveH3SubmissionBilling(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	submission := H3SubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	PopulateTaskBillingSnapshot(c, info, submission.Task)
	submission.Task.Quota = info.PriceData.Quota
	if err := model.SaveH3BillingReservation(c.Request.Context(), submission.Task); err != nil {
		return TaskErrorWrapperLocal(errors.New("failed to persist H3 billing reservation"), "h3_billing_snapshot_failed", http.StatusServiceUnavailable)
	}
	submission.BillingUncertain = false
	return nil
}

func BeginH3SubmissionSend(c *gin.Context) *dto.TaskError {
	submission := H3SubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	submission.MayHaveSent = true
	if err := model.MarkH3Sending(c.Request.Context(), submission.Task.PrivateData.H3AdmissionID); err != nil {
		return TaskErrorWrapperLocal(errors.New("H3 submission fence is uncertain"), H3UnknownCode, http.StatusServiceUnavailable)
	}
	return nil
}

func PreserveH3Unknown(submission *H3Submission) {
	if submission == nil {
		return
	}
	firstUnknown := !submission.Unknown
	submission.Unknown = true
	submission.Task.Status = model.TaskStatusUnknown
	submission.Task.Progress = "0%"
	submission.Task.FinishTime = 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A failed write is still safe: the precreated UNKNOWN task and the send
	// fence were durable before the network operation was permitted.
	_ = model.HoldH3Unknown(ctx, submission.Task)
	if firstUnknown {
		RecordTaskFailure(submission.Task, "task_submit", H3UnknownCode, http.StatusServiceUnavailable, "")
	}
}

func BeginH3Billing(c *gin.Context) error {
	submission := H3SubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	if err := model.SetH3BillingState(c.Request.Context(), submission.Task.PrivateData.H3AdmissionID, "preparing"); err != nil {
		return err
	}
	submission.BillingUncertain = true
	return nil
}

func H3BillingProvenUncharged(c *gin.Context) {
	if submission := H3SubmissionFromContext(c); submission != nil {
		submission.BillingUncertain = false
		// Live execution has proved zero charge; failure to label it must not
		// manufacture a charge. A crash before this label remains conservative.
		_ = model.SetH3BillingState(c.Request.Context(), submission.Task.PrivateData.H3AdmissionID, "proven_uncharged")
	}
}

func SaveH3ProviderReceipt(c *gin.Context, providerID string, data []byte, state []byte) *dto.TaskError {
	submission := H3SubmissionFromContext(c)
	if submission == nil {
		return nil
	}
	submission.Task.PrivateData.UpstreamTaskID = providerID
	submission.Task.PrivateData.PluginState = state
	submission.Task.Data = data
	submission.Task.SubmitTime = time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := model.SaveH3Receipt(ctx, submission.Task); err != nil {
		return TaskErrorWrapperLocal(errors.New("failed to persist H3 provider receipt"), H3UnknownCode, http.StatusServiceUnavailable)
	}
	return nil
}

const H3AuthRejectedCode = "h3_upstream_auth_rejected"

// ClassifyH3SubmitAuthRejection is deliberately narrower than generic 4xx.
// RFC 9110 15.5.2/15.5.4 define 401/403 as an unapplied/refused request. We
// additionally require the documented API-error shape to agree with HTTP and
// reject any receipt/contradictory envelope. This is not a billing observation.
func ClassifyH3SubmitAuthRejection(c *gin.Context, status int, body []byte) bool {
	submission := H3SubmissionFromContext(c)
	if submission == nil || !submission.MayHaveSent || submission.Task.PrivateData.UpstreamTaskID != "" || (status != http.StatusUnauthorized && status != http.StatusForbidden) || len(body) > 1<<20 {
		return false
	}
	var envelope map[string]any
	if common.Unmarshal(body, &envelope) != nil || envelope == nil {
		return false
	}
	errorValue, ok := envelope["error"].(map[string]any)
	if !ok {
		return false
	}
	code, ok := errorValue["http_code"].(float64)
	if !ok || code != float64(status) {
		return false
	}
	message, ok := errorValue["message"].(string)
	if !ok || strings.TrimSpace(message) == "" {
		return false
	}
	nodes := 0
	if h3EnvelopeHasReceipt(envelope, 0, &nodes) {
		return false
	}
	submission.Rejected = true
	submission.RejectedHTTPStatus = status
	return true
}

func h3EnvelopeHasReceipt(value any, depth int, nodes *int) bool {
	*nodes++
	if depth > 32 || *nodes > 32768 {
		return true
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			switch normalized {
			case "id", "task", "taskid", "job", "jobid", "receipt", "upstreamtaskid", "providertaskid":
				return true
			}
			if h3EnvelopeHasReceipt(child, depth+1, nodes) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if h3EnvelopeHasReceipt(child, depth+1, nodes) {
				return true
			}
		}
	}
	return false
}
