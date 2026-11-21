package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetDemandServiceMediaRoutes(api *gin.RouterGroup) {
	content := api.Group("/product/demand-service-media", middleware.DisableCache())
	content.GET("/:profile_id/:attachment_id/content", controller.ServeDemandServiceMedia)
	content.HEAD("/:profile_id/:attachment_id/content", controller.ServeDemandServiceMedia)
	user := api.Group("/product/demands/service-profiles", middleware.DisableCache(), middleware.UserAuth(), controller.DemandSessionOnly, controller.DemandOriginGuard)
	user.GET("/:profile_id/media", controller.GetDemandServiceGallery)
	user.POST("/:profile_id/media/:attachment_id/link", controller.GetDemandServiceMediaLink)
	admin := api.Group("/demand-management", middleware.DisableCache(), middleware.AdminAuth(), controller.DemandSessionOnly, controller.DemandAdminScope)
	read := admin.Group("", middleware.RequirePermission(authz.DemandsRead))
	read.GET("/service-profiles/:profile_id/media", controller.GetDemandServiceGallery)
	read.POST("/service-profiles/:profile_id/media/:attachment_id/link", controller.DemandOriginGuard, controller.GetDemandServiceMediaLink)
	write := admin.Group("", middleware.RequirePermission(authz.DemandsWrite), controller.DemandOriginGuard)
	write.PUT("/service-profiles/:profile_id/media", controller.PutDemandServiceGallery)
	write.POST("/service-media/uploads", controller.CreateDemandServiceMediaUpload)
	write.GET("/service-media/uploads/:upload_id", controller.GetDemandServiceMediaUpload)
	write.PUT("/service-media/uploads/:upload_id/chunks", controller.PutDemandServiceMediaChunk)
	write.POST("/service-media/uploads/:upload_id/complete", controller.CompleteDemandServiceMedia)
}
