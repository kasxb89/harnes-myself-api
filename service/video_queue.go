package service

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type videoQueueDispatchKey struct{}

// VideoQueueDispatch exists only in a host-created in-memory request context.
// No HTTP header, plugin response or persisted client JSON can create it.
type VideoQueueDispatch struct {
	Item            model.VideoQueueItem
	CapacityRefused bool
	Prepared        bool
	SendFenced      bool
	PreflightCode   string
}

type VideoQueueDispatchFunc func(context.Context, *VideoQueueDispatch) (int, error)
type videoQueueDispatcherValue struct{ dispatch VideoQueueDispatchFunc }

var videoQueueDispatcher atomic.Pointer[videoQueueDispatcherValue]

// Registration races safely with the already-started system scheduler. No
// handler can see a partially initialized routing chain during startup.
func SetVideoQueueDispatcher(dispatch VideoQueueDispatchFunc) {
	if dispatch == nil {
		videoQueueDispatcher.Store(nil)
		return
	}
	videoQueueDispatcher.Store(&videoQueueDispatcherValue{dispatch: dispatch})
}
func CurrentVideoQueueDispatcher() VideoQueueDispatchFunc {
	if current := videoQueueDispatcher.Load(); current != nil {
		return current.dispatch
	}
	return nil
}
func VideoQueueDispatcherReady() bool { return videoQueueDispatcher.Load() != nil }

func VideoQueueRequestContext(ctx context.Context, dispatch *VideoQueueDispatch) context.Context {
	return context.WithValue(ctx, videoQueueDispatchKey{}, dispatch)
}

func VideoQueueDispatchFromContext(c *gin.Context) *VideoQueueDispatch {
	if c == nil || c.Request == nil {
		return nil
	}
	value, _ := c.Request.Context().Value(videoQueueDispatchKey{}).(*VideoQueueDispatch)
	return value
}

func VideoQueueTaskID(c *gin.Context) string {
	if dispatch := VideoQueueDispatchFromContext(c); dispatch != nil {
		return dispatch.Item.TaskID
	}
	return ""
}

func BindVideoQueueTask(c *gin.Context, task *model.Task) error {
	dispatch := VideoQueueDispatchFromContext(c)
	if dispatch == nil {
		return nil
	}
	if task == nil || task.TaskID != dispatch.Item.TaskID || task.UserId != dispatch.Item.UserID || task.Properties.OriginModelName != dispatch.Item.Model || c.GetInt("token_id") != dispatch.Item.TokenID {
		return model.ErrVideoQueueState
	}
	if err := model.BindVideoQueueTask(c.Request.Context(), dispatch.Item.QueueID, dispatch.Item.AttemptID, task); err != nil {
		return err
	}
	dispatch.Prepared = true
	return nil
}

func RecordVideoQueueCapacityCandidate(c *gin.Context, task *model.Task) {
	dispatch := VideoQueueDispatchFromContext(c)
	if dispatch != nil && dispatch.Prepared && task != nil && task.ID == 0 && task.TaskID == dispatch.Item.TaskID && task.UserId == dispatch.Item.UserID && !dispatch.SendFenced {
		dispatch.CapacityRefused = true
	}
}

func VerifyVideoQueueSending(c *gin.Context, task *model.Task) error {
	dispatch := VideoQueueDispatchFromContext(c)
	if dispatch == nil {
		return nil
	}
	if !dispatch.Prepared || dispatch.SendFenced {
		return model.ErrVideoQueueState
	}
	if err := model.FenceVideoQueueSend(c.Request.Context(), dispatch.Item.QueueID, dispatch.Item.AttemptID, task); err != nil {
		return err
	}
	dispatch.SendFenced = true
	dispatch.CapacityRefused = false
	return nil
}

// SyncVideoQueueItem is a database-only observation. It never queries or
// submits to a provider. Unknown or timed-out observation is not a failed job.
func SyncVideoQueueItem(ctx context.Context, item *model.VideoQueueItem) error {
	if item.State != model.VideoQueueDispatching && item.State != model.VideoQueueRunning {
		return nil
	}
	var task model.Task
	err := model.DB.WithContext(ctx).Where("task_id = ? AND user_id = ?", item.TaskID, item.UserID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.PrivateData.TokenId > 0 && task.PrivateData.TokenId != item.TokenID {
		return model.ErrVideoQueueState
	}
	receipt, providerTerminal, err := ProjectMediaTaskSubmission(ctx, &task)
	if err != nil {
		return err
	}
	state, submission, code := model.VideoQueueDispatching, "unknown", "video_queue_submission_unknown"
	switch task.Status {
	case model.TaskStatusSuccess:
		state, submission, code = model.VideoQueueSucceeded, "accepted", ""
	case model.TaskStatusFailure:
		if receipt != nil {
			state, submission, code = model.VideoQueueFailed, "not_submitted", receipt.SubmissionCode
		} else if providerTerminal {
			state, submission, code = model.VideoQueueFailed, "accepted", "video_task_failed"
		} else if h3, terminal, h3err := ProjectH3TaskSubmission(ctx, &task); h3err != nil {
			return h3err
		} else if h3 != nil {
			state, submission, code = model.VideoQueueFailed, "not_submitted", h3.SubmissionCode
		} else if terminal {
			state, submission, code = model.VideoQueueFailed, "accepted", "video_task_failed"
		}
	default:
		if task.PrivateData.UpstreamTaskID != "" {
			state, submission, code = model.VideoQueueRunning, "accepted", ""
		}
	}
	if state == item.State && submission == item.SubmissionState && code == item.ErrorCode {
		return nil
	}
	if err := model.UpdateVideoQueueState(ctx, item, state, submission, code); err != nil {
		return err
	}
	return nil
}

type VideoQueueRunSummary struct {
	Observed        int `json:"observed"`
	Dispatched      int `json:"dispatched"`
	WaitingCapacity int `json:"waiting_capacity"`
}

// RunVideoQueueOnce is bounded and serial under the existing system-task DB
// lease. Each item also has a durable CAS fence, so losing that lease cannot
// authorize two provider POSTs. No generation occurs on an enqueue HTTP call.
func RunVideoQueueOnce(ctx context.Context) (VideoQueueRunSummary, error) {
	summary := VideoQueueRunSummary{}
	items, err := model.VideoQueueUnfinished(ctx)
	if err != nil {
		return summary, err
	}
	for i := range items {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if items[i].State == model.VideoQueueDispatching && !items[i].BoundTask && !items[i].SendFenced && items[i].DispatchAt <= time.Now().Unix()-120 {
			if recovered, err := model.RecoverVideoQueueUnbound(ctx, &items[i], time.Now().Unix()-120); err != nil {
				return summary, err
			} else if recovered {
				summary.Observed++
				continue
			}
		}
		if err := SyncVideoQueueItem(ctx, &items[i]); err != nil {
			return summary, err
		}
		summary.Observed++
	}
	dispatcher := CurrentVideoQueueDispatcher()
	if dispatcher == nil {
		return summary, nil
	}
	for range 4 {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		item, err := model.ClaimVideoQueueItem(ctx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return summary, err
		}
		dispatch := &VideoQueueDispatch{Item: *item}
		attemptContext, cancel := context.WithTimeout(ctx, 90*time.Second)
		status, dispatchErr := dispatcher(attemptContext, dispatch)
		cancel()
		summary.Dispatched++
		// Only a private host admission result plus an empty persisted task and
		// both admission ledgers can return this item to the waiting queue.
		if dispatchErr == nil && status == http.StatusTooManyRequests && dispatch.CapacityRefused && !dispatch.SendFenced {
			if err := model.RequeueVideoCapacity(ctx, item); err != nil {
				return summary, err
			}
			summary.WaitingCapacity++
			continue
		}
		if err := SyncVideoQueueItem(ctx, item); err != nil {
			return summary, err
		}
		if item.State != model.VideoQueueDispatching || item.SubmissionState == "accepted" {
			continue
		}
		var count int64
		if err := model.DB.WithContext(ctx).Model(&model.Task{}).Where("task_id = ?", item.TaskID).Count(&count).Error; err != nil {
			return summary, err
		}
		if count == 0 && !dispatch.Prepared && !dispatch.SendFenced && dispatchErr == nil && status >= 400 && status < 500 {
			code := "video_queue_preflight_rejected"
			if dispatch.PreflightCode == "video_queue_authorization_revoked" {
				code = dispatch.PreflightCode
			}
			if err := model.UpdateVideoQueueState(ctx, item, model.VideoQueueFailed, "not_submitted", code); err != nil {
				return summary, err
			}
		} else if count == 0 && !dispatch.Prepared && !dispatch.SendFenced && dispatchErr == nil && status == http.StatusServiceUnavailable {
			if _, err := model.RecoverVideoQueueUnbound(ctx, item, time.Now().Unix()); err != nil {
				return summary, err
			}
		} else {
			// Exceptions, transport loss and an interrupted worker are never
			// retried. Existing task GET/poller remains the recovery authority.
			if err := model.UpdateVideoQueueState(ctx, item, model.VideoQueueDispatching, "unknown", "video_queue_submission_unknown"); err != nil {
				return summary, err
			}
		}
	}
	return summary, nil
}

type videoQueueHandler struct{}

func (videoQueueHandler) Type() string { return "video_queue_dispatch" }
func (videoQueueHandler) Enabled() bool {
	if !constant.UpdateTask || !VideoQueueDispatcherReady() || model.DB == nil {
		return false
	}
	var count int64
	return model.DB.Model(&model.VideoQueueItem{}).Where("state IN ?", []string{model.VideoQueueQueued, model.VideoQueueDispatching, model.VideoQueueRunning}).Limit(1).Count(&count).Error == nil && count > 0
}
func (videoQueueHandler) Interval() time.Duration { return 15 * time.Second }
func (videoQueueHandler) NewPayload() any         { return nil }
func (videoQueueHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := RunVideoQueueOnce(ctx)
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, "Video queue reconciliation failed"
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, result, message); finishErr != nil {
		common.SysError("Failed to persist video queue reconciliation")
	}
}
func init() { RegisterSystemTaskHandler(videoQueueHandler{}) }
