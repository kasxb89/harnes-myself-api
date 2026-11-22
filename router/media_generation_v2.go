package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func SetMediaGenerationV2Router(router *gin.Engine) {
	group := router.Group("/v1/media-v2")
	group.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth())
	group.GET("/models", controller.MediaGenerationModels)
	group.POST("/assets", controller.UploadMediaGenerationAsset)
	group.GET("/assets/by-client-id/:client_asset_id", controller.GetMediaGenerationAsset)
	group.POST("/quotes", controller.QuoteMediaGeneration)
	group.POST("/tasks", middleware.ModelRequestRateLimit(), controller.SubmitMediaGeneration)
	group.GET("/operations/:client_operation_id", controller.GetMediaGenerationOperation)
	group.GET("/tasks/:task_id", controller.GetMediaGenerationTask)
}
