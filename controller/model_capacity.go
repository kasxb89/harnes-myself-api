package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"net/http"
)

func GetModelCapacity(c *gin.Context) {
	cfg, err := model.ReadModelCapacityConfig()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

func SaveModelCapacity(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var cfg model.ModelCapacityConfig
	if err := common.DecodeJson(c.Request.Body, &cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid model capacity configuration"})
		return
	}
	if err := model.SaveModelCapacityConfig(cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	recordManageAudit(c, "model.capacity.update", map[string]any{"route_count": len(cfg.Routes)})
	c.JSON(http.StatusOK, gin.H{"success": true})
}
