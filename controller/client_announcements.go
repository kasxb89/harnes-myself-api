package controller

import (
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
