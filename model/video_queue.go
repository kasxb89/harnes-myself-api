package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	VideoQueueQueued            = "queued"
	VideoQueueDispatching       = "dispatching"
	VideoQueueRunning           = "running"
	VideoQueueSucceeded         = "succeeded"
	VideoQueueFailed            = "failed"
	VideoQueueCanceled          = "canceled"
	VideoQueueUserLimit         = 32
	VideoQueueGlobalLimit       = 512
	VideoQueueHourlyCreateLimit = 120
	VideoQueueBodyLimit         = 64 << 10
)

var ErrVideoQueueConflict = errors.New("video queue operation conflicts with an existing request")
var ErrVideoQueueFull = errors.New("video queue is full")
var ErrVideoQueueCreateRate = errors.New("video queue hourly create budget exhausted")
var ErrVideoQueueState = errors.New("video queue dispatch state is unconfirmed")

// VideoQueueItem persists intent, not an authentication credential. TaskID is
// allocated before dispatch and never changed, even after an unsent capacity
// refusal. A crash after dispatch is never permission to repeat a provider POST.
type VideoQueueItem struct {
	ID               int64  `json:"-" gorm:"primaryKey"`
	QueueID          string `json:"id" gorm:"type:varchar(64);uniqueIndex"`
	UserID           int    `json:"-" gorm:"uniqueIndex:idx_video_queue_operation,priority:1;index;index:idx_video_queue_user_created,priority:1"`
	TokenID          int    `json:"-"`
	TokenFingerprint string `json:"-" gorm:"type:varchar(64)"`
	OperationID      string `json:"operation_id" gorm:"type:varchar(128)"`
	OperationHash    string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_video_queue_operation,priority:2"`
	BodySHA256       string `json:"-" gorm:"type:varchar(64)"`
	RequestBody      string `json:"-" gorm:"type:text"`
	ClientIP         string `json:"-" gorm:"type:varchar(64)"`
	ClientSource     string `json:"-" gorm:"type:varchar(16)"`
	Model            string `json:"model" gorm:"type:varchar(191)"`
	Count            int    `json:"count"`
	DurationSeconds  int    `json:"duration_seconds"`
	TaskID           string `json:"task_id" gorm:"type:varchar(191);uniqueIndex"`
	State            string `json:"state" gorm:"type:varchar(24);index"`
	SubmissionState  string `json:"submission_state" gorm:"type:varchar(24)"`
	ErrorCode        string `json:"error_code,omitempty" gorm:"type:varchar(128)"`
	FailureCode      string `json:"failure_code,omitempty" gorm:"-"`
	FailureReason    string `json:"failure_reason,omitempty" gorm:"-"`
	AttemptID        string `json:"-" gorm:"type:varchar(64)"`
	BoundTask        bool   `json:"-"`
	SendFenced       bool   `json:"-"`
	Attempts         int    `json:"-"`
	NextAttemptAt    int64  `json:"-" gorm:"index"`
	CreatedAt        int64  `json:"created_at" gorm:"index;index:idx_video_queue_user_created,priority:2"`
	UpdatedAt        int64  `json:"updated_at"`
	DispatchAt       int64  `json:"-"`
}

// A single row serializes bounded enqueue and fair claiming across processes.
// UPDATE obtains a write lock on SQLite as well as a row lock on MySQL/PG.
type VideoQueueGuard struct {
	ID         int `gorm:"primaryKey"`
	Revision   int64
	LastUserID int
}

func (item *VideoQueueItem) BeforeCreate(_ *gorm.DB) error {
	digest := sha256.Sum256([]byte(item.OperationID))
	item.OperationHash = hex.EncodeToString(digest[:])
	return nil
}

func videoQueueLock(tx *gorm.DB) (*VideoQueueGuard, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&VideoQueueGuard{ID: 1}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&VideoQueueGuard{}).Where("id = ?", 1).UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
		return nil, err
	}
	var guard VideoQueueGuard
	if err := tx.Where("id = ?", 1).First(&guard).Error; err != nil {
		return nil, err
	}
	return &guard, nil
}

func EnqueueVideo(ctx context.Context, item *VideoQueueItem) (*VideoQueueItem, bool, error) {
	if item == nil || item.UserID <= 0 || item.TokenID <= 0 || len(item.TokenFingerprint) != 64 || item.OperationID == "" || len(item.OperationID) > 128 || len(item.RequestBody) > VideoQueueBodyLimit || item.Count != 1 || item.QueueID == "" || item.TaskID == "" {
		return nil, false, ErrVideoQueueState
	}
	created := false
	digest := sha256.Sum256([]byte(item.OperationID))
	item.OperationHash = hex.EncodeToString(digest[:])
	var result VideoQueueItem
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := videoQueueLock(tx); err != nil {
			return err
		}
		err := tx.Where("user_id = ? AND operation_hash = ?", item.UserID, item.OperationHash).First(&result).Error
		if err == nil {
			if result.BodySHA256 != item.BodySHA256 || result.QueueID != item.QueueID {
				return ErrVideoQueueConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var duplicate int64
		if err := tx.Model(&VideoQueueItem{}).Where("queue_id = ?", item.QueueID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate != 0 {
			return ErrVideoQueueConflict
		}
		active := []string{VideoQueueQueued, VideoQueueDispatching, VideoQueueRunning}
		var global, user, recent int64
		if err := tx.Model(&VideoQueueItem{}).Where("state IN ?", active).Count(&global).Error; err != nil {
			return err
		}
		if err := tx.Model(&VideoQueueItem{}).Where("user_id = ? AND state IN ?", item.UserID, active).Count(&user).Error; err != nil {
			return err
		}
		if err := tx.Model(&VideoQueueItem{}).Where("user_id = ? AND created_at >= ?", item.UserID, time.Now().Unix()-3600).Count(&recent).Error; err != nil {
			return err
		}
		if recent >= VideoQueueHourlyCreateLimit {
			return ErrVideoQueueCreateRate
		}
		if global >= VideoQueueGlobalLimit || user >= VideoQueueUserLimit {
			return ErrVideoQueueFull
		}
		item.State, item.SubmissionState = VideoQueueQueued, "not_submitted"
		item.CreatedAt, item.UpdatedAt = time.Now().Unix(), time.Now().Unix()
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		result, created = *item, true
		return nil
	})
	return &result, created, err
}

func GetVideoQueueItem(ctx context.Context, userID int, queueID string) (*VideoQueueItem, error) {
	var item VideoQueueItem
	err := DB.WithContext(ctx).Where("queue_id = ? AND user_id = ?", queueID, userID).First(&item).Error
	return &item, err
}

// GetVideoQueueOperation resolves accepted intent before an advisory balance
// check. Spending the balance later must not hide an already accepted operation.
func GetVideoQueueOperation(ctx context.Context, userID int, operationID string) (*VideoQueueItem, error) {
	digest := sha256.Sum256([]byte(operationID))
	var item VideoQueueItem
	err := DB.WithContext(ctx).Where("user_id = ? AND operation_hash = ?", userID, hex.EncodeToString(digest[:])).First(&item).Error
	return &item, err
}

func ListVideoQueueItems(ctx context.Context, userID int, after int64, limit int) ([]VideoQueueItem, error) {
	var items []VideoQueueItem
	err := DB.WithContext(ctx).Where("user_id = ? AND id > ?", userID, after).Order("id asc").Limit(min(max(limit, 1), 100)).Find(&items).Error
	return items, err
}

func CancelVideoQueueItem(ctx context.Context, userID int, queueID string) error {
	result := DB.WithContext(ctx).Model(&VideoQueueItem{}).Where("queue_id = ? AND user_id = ? AND state = ? AND send_fenced = ? AND bound_task = ?", queueID, userID, VideoQueueQueued, false, false).
		Updates(map[string]any{"state": VideoQueueCanceled, "submission_state": "not_submitted", "request_body": "", "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	item, err := GetVideoQueueItem(ctx, userID, queueID)
	if err != nil {
		return err
	}
	if item.State == VideoQueueCanceled {
		return nil
	}
	return ErrVideoQueueState
}

func ClaimVideoQueueItem(ctx context.Context) (*VideoQueueItem, error) {
	var item VideoQueueItem
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		guard, err := videoQueueLock(tx)
		if err != nil {
			return err
		}
		query := tx.Where("state = ? AND next_attempt_at <= ?", VideoQueueQueued, time.Now().Unix())
		err = query.Where("user_id > ?", guard.LastUserID).Order("user_id asc, id asc").First(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Where("state = ? AND next_attempt_at <= ?", VideoQueueQueued, time.Now().Unix()).Order("user_id asc, id asc").First(&item).Error
		}
		if err != nil {
			return err
		}
		item.AttemptID = GenerateTaskID()
		item.State, item.SubmissionState, item.DispatchAt = VideoQueueDispatching, "dispatching", time.Now().Unix()
		item.Attempts++
		if err := tx.Model(&VideoQueueItem{}).Where("id = ? AND state = ?", item.ID, VideoQueueQueued).
			Updates(map[string]any{"state": item.State, "submission_state": item.SubmissionState, "attempt_id": item.AttemptID, "attempts": item.Attempts, "dispatch_at": item.DispatchAt, "updated_at": item.DispatchAt}).Error; err != nil {
			return err
		}
		return tx.Model(&VideoQueueGuard{}).Where("id = ?", 1).Update("last_user_id", item.UserID).Error
	})
	return &item, err
}

// BindVideoQueueTask is called before acquiring a supplier slot or money. The
// attempt fence is immutable outside an explicitly proven unsent capacity retry.
func BindVideoQueueTask(ctx context.Context, queueID, attemptID string, task *Task) error {
	if task == nil || task.ID != 0 {
		return ErrVideoQueueState
	}
	result := DB.WithContext(ctx).Model(&VideoQueueItem{}).
		Where("queue_id = ? AND attempt_id = ? AND task_id = ? AND user_id = ? AND state = ? AND send_fenced = ?", queueID, attemptID, task.TaskID, task.UserId, VideoQueueDispatching, false).
		Update("bound_task", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		var count int64
		if err := DB.WithContext(ctx).Model(&VideoQueueItem{}).Where("queue_id = ? AND attempt_id = ? AND task_id = ? AND user_id = ? AND state = ? AND send_fenced = ? AND bound_task = ?", queueID, attemptID, task.TaskID, task.UserId, VideoQueueDispatching, false, true).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrVideoQueueState
		}
	}
	return nil
}

func FenceVideoQueueSend(ctx context.Context, queueID, attemptID string, task *Task) error {
	if task == nil || task.ID <= 0 {
		return ErrVideoQueueState
	}
	result := DB.WithContext(ctx).Model(&VideoQueueItem{}).
		Where("queue_id = ? AND attempt_id = ? AND task_id = ? AND user_id = ? AND state = ? AND bound_task = ? AND send_fenced = ?", queueID, attemptID, task.TaskID, task.UserId, VideoQueueDispatching, true, false).
		Updates(map[string]any{"send_fenced": true, "submission_state": "unknown", "updated_at": time.Now().Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoQueueState
	}
	return nil
}

// RequeueVideoCapacity requires the host's private capacity classification AND
// independently proves that no task/admission/billing row was ever created.
func RequeueVideoCapacity(ctx context.Context, item *VideoQueueItem) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var persisted VideoQueueItem
		if err := lockForUpdate(tx).Where("id = ? AND attempt_id = ? AND state = ? AND send_fenced = ?", item.ID, item.AttemptID, VideoQueueDispatching, false).First(&persisted).Error; err != nil {
			return err
		}
		var count int64
		for _, table := range []any{&Task{}, &H3Admission{}, &MediaAdmission{}} {
			column := "id"
			if _, ok := table.(*Task); ok {
				column = "task_id"
			}
			if err := tx.Model(table).Where(column+" = ?", item.TaskID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrVideoQueueState
			}
		}
		return tx.Model(&VideoQueueItem{}).Where("id = ? AND attempt_id = ?", item.ID, item.AttemptID).
			Updates(map[string]any{"state": VideoQueueQueued, "submission_state": "not_submitted", "bound_task": false, "error_code": "video_queue_waiting_capacity", "next_attempt_at": time.Now().Unix() + 15, "updated_at": time.Now().Unix()}).Error
	})
}

// RecoverVideoQueueUnbound revokes the old attempt under the same row lock
// used by Bind. A delayed old worker can no longer Bind or cross Send. This is
// deliberately unavailable once Bind succeeded, even when no receipt exists.
func RecoverVideoQueueUnbound(ctx context.Context, item *VideoQueueItem, olderThan int64) (bool, error) {
	recovered := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current VideoQueueItem
		err := lockForUpdate(tx).Where("id = ? AND state = ? AND attempt_id = ? AND bound_task = ? AND send_fenced = ? AND dispatch_at <= ?", item.ID, VideoQueueDispatching, item.AttemptID, false, false, olderThan).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var count int64
		for _, table := range []any{&Task{}, &H3Admission{}, &MediaAdmission{}} {
			column := "id"
			if _, ok := table.(*Task); ok {
				column = "task_id"
			}
			if err := tx.Model(table).Where(column+" = ?", item.TaskID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrVideoQueueState
			}
		}
		result := tx.Model(&VideoQueueItem{}).Where("id = ? AND attempt_id = ? AND state = ? AND bound_task = ? AND send_fenced = ?", item.ID, item.AttemptID, VideoQueueDispatching, false, false).
			Updates(map[string]any{"state": VideoQueueQueued, "submission_state": "not_submitted", "attempt_id": "", "error_code": "video_queue_waiting_dispatch", "next_attempt_at": time.Now().Unix() + 15, "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		recovered = result.RowsAffected == 1
		return nil
	})
	return recovered, err
}

func UpdateVideoQueueState(ctx context.Context, item *VideoQueueItem, state, submission, code string) error {
	if item.State != VideoQueueDispatching && item.State != VideoQueueRunning {
		return ErrVideoQueueState
	}
	updates := map[string]any{"state": state, "submission_state": submission, "error_code": code, "updated_at": time.Now().Unix()}
	if state == VideoQueueSucceeded || state == VideoQueueFailed || state == VideoQueueCanceled {
		updates["request_body"] = ""
	}
	result := DB.WithContext(ctx).Model(&VideoQueueItem{}).Where("id = ? AND state = ? AND attempt_id = ?", item.ID, item.State, item.AttemptID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		persisted, err := GetVideoQueueItem(ctx, item.UserID, item.QueueID)
		if err != nil {
			return err
		}
		*item = *persisted
		return nil
	}
	item.State, item.SubmissionState, item.ErrorCode = state, submission, code
	return nil
}

func VideoQueueUnfinished(ctx context.Context) ([]VideoQueueItem, error) {
	var items []VideoQueueItem
	err := DB.WithContext(ctx).Where("state IN ?", []string{VideoQueueDispatching, VideoQueueRunning}).Order("id asc").Limit(VideoQueueGlobalLimit).Find(&items).Error
	return items, err
}
