package controller

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetClientAnnouncements(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	feed, err := model.GetClientAnnouncements(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, model.ErrClientAnnouncementsUnavailable, gin.H{"code": "client_announcements_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": feed})
}

func GetAdminAnnouncements(c *gin.Context) {
	config, err := model.GetAdminAnnouncementsConfig(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, model.ErrClientAnnouncementsUnavailable)
		return
	}
	common.ApiSuccess(c, config)
}

func SaveAdminAnnouncements(c *gin.Context) {
	if c.ContentType() != "application/json" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, model.MaxAdminAnnouncementsBodyBytes+1))
	if err != nil || len(data) > model.MaxAdminAnnouncementsBodyBytes {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	config, err := model.ParseAdminAnnouncementsConfig(data)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	if err = model.SaveAdminAnnouncementsConfig(config); err != nil {
		common.ApiError(c, err)
		return
	}
	GetAdminAnnouncements(c)
}
