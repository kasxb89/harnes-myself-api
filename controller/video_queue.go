package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var videoQueueIDPattern = regexp.MustCompile(`^vq_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var videoQueueOperationPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{16,128}$`)

func EnqueueVideo(c *gin.Context) {
	if !constant.UpdateTask || !service.VideoQueueDispatcherReady() {
		videoQueueError(c, 503, "video_queue_feature_disabled")
		return
	}
	if c.ContentType() != "application/json" {
		videoQueueError(c, 415, "video_queue_json_required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, model.VideoQueueBodyLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		videoQueueError(c, 413, "video_queue_request_too_large")
		return
	}
	var envelope struct {
		SchemaVersion int                          `json:"schema_version"`
		QueueID       string                       `json:"queue_id"`
		OperationID   string                       `json:"operation_id"`
		Count         *int                         `json:"count"`
		Request       map[string]common.RawMessage `json:"request"`
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(body, &fields) != nil || common.Unmarshal(body, &envelope) != nil || envelope.SchemaVersion != 1 || !videoQueueIDPattern.MatchString(envelope.QueueID) || !videoQueueOperationPattern.MatchString(envelope.OperationID) || envelope.Request == nil || envelope.Count != nil && *envelope.Count != 1 {
		videoQueueError(c, 400, "video_queue_invalid_request")
		return
	}
	for key := range fields {
		if key != "schema_version" && key != "queue_id" && key != "operation_id" && key != "count" && key != "request" {
			videoQueueError(c, 400, "video_queue_invalid_request")
			return
		}
	}
	var modelName string
	if common.Unmarshal(envelope.Request["model"], &modelName) != nil || modelName == "" || len(modelName) > 191 || strings.TrimSpace(modelName) != modelName {
		videoQueueError(c, 400, "video_queue_invalid_model")
		return
	}
	// One queue item is one explicit operation. Duration never becomes a count.
	// Every provider-specific multiplier is still checked by the real plugin
	// validator at dispatch; the envelope adds an early depth/bounds check.
	requestBody, err := common.Marshal(envelope.Request)
	var value any
	if err != nil || common.Unmarshal(requestBody, &value) != nil || !validVideoQueueRequest(value, 0) {
		videoQueueError(c, 400, "video_queue_invalid_request")
		return
	}
	requestBody, err = common.Marshal(value)
	if err != nil {
		videoQueueError(c, 400, "video_queue_invalid_request")
		return
	}
	seconds := 0
	for _, key := range []string{"seconds", "duration"} {
		if raw, exists := envelope.Request[key]; exists {
			var quantity int
			if common.Unmarshal(raw, &quantity) != nil {
				var text string
				if common.Unmarshal(raw, &text) != nil {
					videoQueueError(c, 400, "video_queue_invalid_duration")
					return
				}
				quantity, err = strconv.Atoi(text)
				if err != nil {
					videoQueueError(c, 400, "video_queue_invalid_duration")
					return
				}
			}
			if quantity < 1 || quantity > relaycommon.MaxTaskDurationSeconds || seconds != 0 && seconds != quantity {
				videoQueueError(c, 400, "video_queue_invalid_duration")
				return
			}
			seconds = quantity
		}
	}
	if net.ParseIP(c.ClientIP()) == nil {
		videoQueueError(c, 400, "video_queue_client_ip_invalid")
		return
	}
	hash := sha256.Sum256(requestBody)
	service.CaptureTaskClientSource(c)
	clientSource := ""
	if snapshot := service.TaskExecutionSnapshotFromContext(c); snapshot != nil && snapshot.ClientSource == "desktop" {
		clientSource = "desktop"
	}
	item := &model.VideoQueueItem{QueueID: envelope.QueueID, UserID: c.GetInt("id"), TokenID: c.GetInt("token_id"), OperationID: envelope.OperationID, BodySHA256: hex.EncodeToString(hash[:]), RequestBody: string(requestBody), ClientIP: c.ClientIP(), Model: modelName, Count: 1, DurationSeconds: seconds, TaskID: model.GenerateTaskID()}
	item.ClientSource = clientSource
	tokenFingerprint := sha256.Sum256([]byte(c.GetString("token_key")))
	item.TokenFingerprint = hex.EncodeToString(tokenFingerprint[:])
	result, created, err := model.EnqueueVideo(c.Request.Context(), item)
	switch {
	case errors.Is(err, model.ErrVideoQueueConflict):
		videoQueueError(c, 409, "video_queue_operation_conflict")
		return
	case errors.Is(err, model.ErrVideoQueueFull):
		c.Header("Retry-After", "30")
		videoQueueError(c, 429, "video_queue_full")
		return
	case errors.Is(err, model.ErrVideoQueueCreateRate):
		c.Header("Retry-After", "3600")
		videoQueueError(c, 429, "video_queue_create_rate_exhausted")
		return
	case err != nil:
		videoQueueError(c, 503, "video_queue_unavailable")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeVideoQueueItem(c, status, result)
}

func validVideoQueueRequest(value any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		if len(value) > 128 {
			return false
		}
		for key, child := range value {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			switch normalized {
			case "authorization", "apikey", "bearer", "token", "headers", "yunshufiles", "fileref", "filepath", "localpath", "b64json", "base64", "base64data", "imagedata", "videodata", "audiodata":
				return false
			case "image", "images", "imageurl", "imageurls", "inputreference", "referenceimage", "referencevideo", "videoreference", "referenceaudio", "audioreference", "videourl", "videourls", "audiourl", "audiourls", "firstframeimage", "lastframeimage":
				if !validVideoQueueReference(child) {
					return false
				}
			case "n", "count", "videocount", "batchsize":
				number, ok := child.(float64)
				if !ok || number != 1 {
					return false
				}
			case "seconds", "duration", "durationseconds":
				quantity := 0
				switch child := child.(type) {
				case float64:
					if child < 1 || child > relaycommon.MaxTaskDurationSeconds || child != float64(int(child)) {
						return false
					}
					quantity = int(child)
				case string:
					var err error
					quantity, err = strconv.Atoi(child)
					if err != nil {
						return false
					}
				default:
					return false
				}
				if quantity < 1 || quantity > relaycommon.MaxTaskDurationSeconds {
					return false
				}
			}
			if !validVideoQueueRequest(child, depth+1) {
				return false
			}
		}
	case []any:
		if len(value) > 32 {
			return false
		}
		for _, child := range value {
			if !validVideoQueueRequest(child, depth+1) {
				return false
			}
		}
	case string:
		if len(value) > 32768 || strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:") {
			return false
		}
	}
	return true
}

func validVideoQueueReference(value any) bool {
	switch value := value.(type) {
	case string:
		parsed, err := url.Parse(value)
		return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
	case []any:
		if len(value) > 16 {
			return false
		}
		for _, child := range value {
			if !validVideoQueueReference(child) {
				return false
			}
		}
		return true
	case map[string]any:
		if len(value) != 1 {
			return false
		}
		child, exists := value["url"]
		return exists && validVideoQueueReference(child)
	default:
		return false
	}
}

func GetVideoQueue(c *gin.Context) {
	item, err := model.GetVideoQueueItem(c.Request.Context(), c.GetInt("id"), c.Param("queue_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		videoQueueError(c, 404, "video_queue_not_found")
		return
	}
	if err != nil || service.SyncVideoQueueItem(c.Request.Context(), item) != nil {
		videoQueueError(c, 503, "video_queue_unavailable")
		return
	}
	writeVideoQueueItem(c, 200, item)
}

func ListVideoQueue(c *gin.Context) {
	after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limitErr != nil || after < 0 || limit < 1 || limit > 100 {
		videoQueueError(c, 400, "video_queue_invalid_pagination")
		return
	}
	items, err := model.ListVideoQueueItems(c.Request.Context(), c.GetInt("id"), after, limit)
	if err != nil {
		videoQueueError(c, 503, "video_queue_unavailable")
		return
	}
	var next int64
	for i := range items {
		if service.SyncVideoQueueItem(c.Request.Context(), &items[i]) != nil {
			videoQueueError(c, 503, "video_queue_unavailable")
			return
		}
		next = items[i].ID
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{"schema_version": 1, "object": "video_queue_list", "data": items, "next_after": next, "has_more": len(items) == limit})
}

func CancelVideoQueue(c *gin.Context) {
	err := model.CancelVideoQueueItem(c.Request.Context(), c.GetInt("id"), c.Param("queue_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		videoQueueError(c, 404, "video_queue_not_found")
		return
	}
	if errors.Is(err, model.ErrVideoQueueState) {
		videoQueueError(c, 409, "video_queue_already_dispatching")
		return
	}
	if err != nil {
		videoQueueError(c, 503, "video_queue_unavailable")
		return
	}
	GetVideoQueue(c)
}

func writeVideoQueueItem(c *gin.Context, status int, item *model.VideoQueueItem) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Retry-After", "15")
	c.Header("Location", "/v1/video/queue/"+item.QueueID)
	c.JSON(status, gin.H{"schema_version": 1, "object": "video_queue_item", "data": item, "accepted": true, "cancelable": item.State == model.VideoQueueQueued && !item.SendFenced && !item.BoundTask, "observation_timed_out": item.State == model.VideoQueueDispatching && time.Now().Unix()-item.DispatchAt > 90, "recovery": gin.H{"method": "GET", "path": "/v1/video/queue/" + item.QueueID}})
}

func videoQueueError(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "private, no-store")
	key := i18n.MsgVideoQueueUnavailable
	switch code {
	case "video_queue_operation_conflict":
		key = i18n.MsgVideoQueueConflict
	case "video_queue_full":
		key = i18n.MsgVideoQueueFull
	case "video_queue_create_rate_exhausted":
		key = i18n.MsgVideoQueueCreateRate
	case "video_queue_feature_disabled":
		key = i18n.MsgVideoQueueDisabled
	case "video_queue_not_found":
		key = i18n.MsgVideoQueueNotFound
	case "video_queue_already_dispatching":
		key = i18n.MsgVideoQueueAlreadyDispatching
	default:
		if status >= 400 && status < 500 {
			key = i18n.MsgVideoQueueInvalidRequest
		}
	}
	c.JSON(status, gin.H{"error": gin.H{"type": "video_queue_error", "code": code, "message": i18n.T(c, key)}})
}
