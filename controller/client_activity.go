package controller

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/QuantumNous/new-api/common"
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

func clientActivityFilterFromQuery(c *gin.Context) (model.ClientActivityFilter, error) {
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
	filter.Start, filter.End, err = model.ValidateClientActivityRange(filter.Start, filter.End, common.GetTimestamp())
	filter.Username, filter.Status = c.Query("username"), c.Query("status")
	if filter.Status != "" && filter.Status != "all" && filter.Status != "online" && filter.Status != "foreground" && filter.Status != "background" && filter.Status != "offline" {
		return filter, errors.New("invalid client activity status")
	}
	return filter, err
}

func GetClientActivitySummary(c *gin.Context) {
	filter, err := clientActivityFilterFromQuery(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	filter.Username, filter.Status = "", ""
	summary, err := model.GetClientActivitySummary(filter, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func GetClientActivitySessions(c *gin.Context) {
	filter, err := clientActivityFilterFromQuery(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	page := common.GetPageQuery(c)
	if page.Page < 1 || page.PageSize < 1 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	items, total, err := model.ListClientActivitySessions(filter, page.GetStartIdx(), page.GetPageSize(), common.GetTimestamp())
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
	session, usage, err := model.GetClientActivitySession(id, common.GetTimestamp())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Client activity session not found"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"session": session, "usage": usage})
}
