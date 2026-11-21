package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetDemandShowcaseRoutes(api *gin.RouterGroup) {
	public := api.Group("/product/demand-showcases", middleware.DisableCache())
	public.GET("/config", controller.GetShowcaseConfig)
	public.GET("", controller.GetShowcaseCatalog)
	public.GET("/:showcase_id", controller.GetShowcaseDetail)
	public.GET("/assets/:showcase_id/:attachment_id/content", controller.ServeShowcaseAsset)
	public.HEAD("/assets/:showcase_id/:attachment_id/content", controller.ServeShowcaseAsset)
	admin := api.Group("/demand-showcase-management", middleware.DisableCache(), middleware.AdminAuth(), controller.DemandSessionOnly, controller.DemandAdminScope)
	read := admin.Group("", middleware.RequirePermission(authz.DemandsRead))
	read.GET("", controller.GetShowcaseCatalog)
	read.GET("/:showcase_id", controller.GetShowcaseDetail)
	write := admin.Group("", middleware.RequirePermission(authz.DemandsWrite), controller.DemandOriginGuard)
	write.POST("", controller.CreateShowcase)
	write.PUT("/:showcase_id", controller.UpdateShowcase)
	write.POST("/uploads", controller.CreateShowcaseUpload)
	uploads := write.Group("/uploads/:upload_id", controller.ShowcaseUploadScope)
	uploads.GET("", controller.GetDemandUpload)
	uploads.PUT("/chunks", controller.PutDemandUploadChunk)
	uploads.POST("/complete", controller.CompleteDemandUpload)
}
