package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetVideoUsage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	filter := model.VideoUsageFilter{ClientSource: "all", TaskScope: "history", Page: 1, PageSize: 20}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			err = model.ErrVideoUsageQueryFailed
			break
		}
		value := values[0]
		switch key {
		case "start_timestamp":
			filter.Start, err = strconv.ParseInt(value, 10, 64)
			if filter.Start <= 0 {
				err = model.ErrVideoUsageQueryFailed
			}
		case "end_timestamp":
			filter.End, err = strconv.ParseInt(value, 10, 64)
			if filter.End <= 0 {
				err = model.ErrVideoUsageQueryFailed
			}
		case "model":
			filter.Model = value
			if len(value) > 255 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
				err = model.ErrVideoUsageQueryFailed
			}
		case "user_id":
			filter.UserID, err = strconv.Atoi(value)
			if filter.UserID <= 0 {
				err = model.ErrVideoUsageQueryFailed
			}
		case "client_source":
			filter.ClientSource = value
			if value != "all" && value != "desktop" && value != "unknown" {
				err = model.ErrVideoUsageQueryFailed
			}
		case "task_scope":
			filter.TaskScope = value
			if value != "history" && value != "current" {
				err = model.ErrVideoUsageQueryFailed
			}
		case "p":
			filter.Page, err = strconv.Atoi(value)
		case "page_size":
			filter.PageSize, err = strconv.Atoi(value)
		default:
			err = model.ErrVideoUsageQueryFailed
		}
		if err != nil {
			break
		}
	}
	now := common.GetTimestamp()
	if filter.End == 0 {
		filter.End = now
	}
	if filter.Start == 0 {
		filter.Start = filter.End - 24*60*60
	}
	if err != nil || filter.Start < 0 || filter.End <= filter.Start || filter.End > now || filter.End-filter.Start > 31*24*60*60 || filter.Page < 1 || filter.Page > 1000000 || filter.PageSize < 1 || filter.PageSize > 100 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	result, err := model.GetVideoUsage(c.Request.Context(), filter, mediaOperationsCatalog())
	if err != nil {
		status, code, message := http.StatusInternalServerError, "VIDEO_USAGE_QUERY_FAILED", "Failed to query logs"
		if errors.Is(err, model.ErrVideoUsageQueryTooBroad) {
			status, code, message = http.StatusUnprocessableEntity, "VIDEO_USAGE_QUERY_TOO_BROAD", "Narrow the time range or choose a model"
		}
		c.JSON(status, gin.H{"success": false, "message": message, "code": code})
		return
	}
	common.ApiSuccess(c, result)
}
