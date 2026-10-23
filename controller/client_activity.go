package controller

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var clientActivityVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,79}$`)

func ClientActivityHeartbeat(c *gin.Context) {
	// The telemetry body is intentionally small and has no user identity field.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	var input model.ClientActivityHeartbeat
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	parsedID, err := uuid.Parse(input.SessionID)
	if err != nil || parsedID.Version() != 4 || parsedID.String() != input.SessionID ||
		(input.Event != "start" && input.Event != "heartbeat" && input.Event != "stop") ||
		(input.Platform != "darwin" && input.Platform != "win32" && input.Platform != "linux") ||
		!clientActivityVersionPattern.MatchString(input.AppVersion) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	now := common.GetTimestamp()
	if _, err := model.RecordClientActivity(c.GetInt("id"), input, now); err != nil {
		if errors.Is(err, model.ErrClientActivitySessionLimit) {
			common.ApiErrorStatus(c, http.StatusTooManyRequests, common.NewMessage("Too many requests"))
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to save"))
		return
	}
	common.ApiSuccess(c, gin.H{"server_time": now, "heartbeat_interval_seconds": model.ClientActivityHeartbeatSeconds, "online_timeout_seconds": model.ClientActivityTimeoutSeconds})
}

func clientActivityFilterFromQuery(c *gin.Context, now int64) (model.ClientActivityFilter, error) {
	var filter model.ClientActivityFilter
	var err error
	if value := c.Query("start_timestamp"); value != "" {
		filter.Start, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return filter, err
		}
	}
	if value := c.Query("end_timestamp"); value != "" {
		filter.End, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return filter, err
		}
	}
	filter.Start, filter.End, err = model.ValidateClientActivityRange(filter.Start, filter.End, now)
	filter.Username, filter.Status = c.Query("username"), c.Query("status")
	filter.Scope, filter.TimeBasis = c.Query("scope"), c.Query("time_basis")
	if (filter.Scope != "" && filter.Scope != "live" && filter.Scope != "history") || (filter.TimeBasis != "" && filter.TimeBasis != "started" && filter.TimeBasis != "last_seen") || (filter.Scope != "history" && filter.TimeBasis != "") || (filter.Scope == "live" && filter.Status == "offline") {
		return filter, errors.New("invalid client activity scope")
	}
	if filter.Status != "" && filter.Status != "all" && filter.Status != "online" && filter.Status != "foreground" && filter.Status != "background" && filter.Status != "offline" {
		return filter, errors.New("invalid client activity status")
	}
	return filter, err
}

func GetClientActivitySummary(c *gin.Context) {
	now := common.GetTimestamp()
	filter, err := clientActivityFilterFromQuery(c, now)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	filter.Username, filter.Status = "", ""
	summary, err := model.GetClientActivitySummaryWithWorkload(filter, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func GetClientActivitySessions(c *gin.Context) {
	now := common.GetTimestamp()
	filter, err := clientActivityFilterFromQuery(c, now)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	page := common.GetPageQuery(c)
	if page.Page < 1 || page.PageSize < 1 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	items, total, err := model.ListClientActivitySessions(filter, page.GetStartIdx(), page.GetPageSize(), now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetClientActivitySession(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	now := common.GetTimestamp()
	session, usage, workload, err := model.GetClientActivitySessionWithWorkload(id, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Client activity session not found"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"session": session, "usage": usage, "workload": workload})
}

// ClientActivityWorkloadSnapshot is an observation-only, independently limited
// interactive-login endpoint. Its failures have no media/account side effects.
func ClientActivityWorkloadSnapshot(c *gin.Context) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"code": "ACTIVITY_SNAPSHOT_INVALID"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, model.MaxClientWorkloadBodyBytes+1))
	if len(data) > model.MaxClientWorkloadBodyBytes {
		common.ApiErrorStatus(c, http.StatusRequestEntityTooLarge, common.NewMessage("Invalid parameters"), gin.H{"code": "ACTIVITY_SNAPSHOT_TOO_LARGE"})
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"code": "ACTIVITY_SNAPSHOT_INVALID"})
		return
	}
	input, err := model.ParseClientWorkloadSnapshot(data)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"code": "ACTIVITY_SNAPSHOT_INVALID"})
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized, invalid access token"))
		return
	}
	ack, err := model.RecordClientWorkload(identity, input, common.GetTimestamp())
	if err != nil {
		status, code := http.StatusInternalServerError, "ACTIVITY_SNAPSHOT_UNAVAILABLE"
		if errors.Is(err, model.ErrClientWorkloadInvalid) {
			status, code = http.StatusBadRequest, "ACTIVITY_SNAPSHOT_INVALID"
		}
		if errors.Is(err, model.ErrUserSessionInactive) {
			status, code = http.StatusUnauthorized, "AUTH_SESSION_REVOKED"
		}
		if errors.Is(err, model.ErrClientWorkloadConflict) {
			status, code = http.StatusConflict, "ACTIVITY_SEQUENCE_CONFLICT"
		}
		if errors.Is(err, model.ErrClientWorkloadCapacity) {
			status, code = http.StatusTooManyRequests, "ACTIVITY_SNAPSHOT_CAPACITY"
		}
		if errors.Is(err, model.ErrClientWorkloadRuntimeEnded) {
			status, code = http.StatusConflict, "ACTIVITY_RUNTIME_ENDED"
		}
		common.ApiErrorStatus(c, status, common.NewMessage("Failed to save"), gin.H{"code": code})
		return
	}
	common.ApiSuccess(c, ack)
}
