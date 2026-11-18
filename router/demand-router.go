package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

// SetDemandRoutes is also used by the loopback-only synthetic fixture service.
// It never installs AI relay, billing, SMS, or task dispatch routes.
func SetDemandRoutes(api *gin.RouterGroup) {
	public := api.Group("/product/demands", middleware.DisableCache())
	public.GET("/attachments/:attachment_id/content", controller.ServeDemandAttachment)
	public.HEAD("/attachments/:attachment_id/content", controller.ServeDemandAttachment)
	user := public.Group("", middleware.UserAuth(), controller.DemandSessionOnly, controller.DemandOriginGuard)
	user.GET("/config", controller.GetDemandConfiguration)
	user.GET("/engineers", controller.GetDemandEngineers)
	user.GET("/unread", controller.GetDemandUnread)
	user.GET("", controller.ListDemands)
	user.POST("", middleware.UserCriticalRateLimit("demand-create"), controller.CreateUserDemand)
	user.POST("/uploads", middleware.UserCriticalRateLimit("demand-upload"), controller.CreateDemandUpload)
	user.GET("/uploads/:upload_id", controller.GetDemandUpload)
	user.PUT("/uploads/:upload_id/chunks", controller.PutDemandUploadChunk)
	user.POST("/uploads/:upload_id/complete", controller.CompleteDemandUpload)
	user.POST("/attachments/:attachment_id/link", controller.GetDemandAttachmentLink)
	registerDemandConversation(user)
	engineer := api.Group("/engineer/demands", middleware.DisableCache(), middleware.UserAuth(), controller.DemandSessionOnly, controller.DemandEngineerScope, controller.DemandOriginGuard)
	engineer.GET("", controller.ListDemands)
	engineer.GET("/unread", controller.GetDemandUnread)
	engineer.POST("/heartbeat", controller.DemandEngineerHeartbeat)
	registerDemandConversation(engineer)
	admin := api.Group("/demand-management", middleware.DisableCache(), middleware.AdminAuth(), controller.DemandSessionOnly, controller.DemandAdminScope)
	read := admin.Group("", middleware.RequirePermission(authz.DemandsRead))
	read.GET("", controller.ListDemands)
	read.GET("/config", controller.GetDemandConfiguration)
	read.GET("/engineers", controller.GetDemandEngineers)
	read.GET("/:id", controller.GetDemand)
	read.GET("/:id/messages", controller.GetDemandMessages)
	read.GET("/:id/audit", controller.GetDemandAudit)
	write := admin.Group("", middleware.RequirePermission(authz.DemandsWrite), controller.DemandOriginGuard)
	write.PUT("/config", controller.SaveDemandConfiguration)
	write.POST("/engineers", controller.SaveDemandEngineer)
	write.PUT("/engineers/:engineer_id", controller.SaveDemandEngineer)
	write.POST("/:id/actions", controller.PostDemandAction)
	write.POST("/uploads", controller.CreateDemandUpload)
	write.GET("/uploads/:upload_id", controller.GetDemandUpload)
	write.PUT("/uploads/:upload_id/chunks", controller.PutDemandUploadChunk)
	write.POST("/uploads/:upload_id/complete", controller.CompleteDemandUpload)
	read.POST("/attachments/:attachment_id/link", controller.DemandOriginGuard, controller.GetDemandAttachmentLink)
}
func registerDemandConversation(group *gin.RouterGroup) {
	group.GET("/:id", controller.GetDemand)
	group.GET("/:id/messages", controller.GetDemandMessages)
	group.POST("/:id/messages", middleware.UserCriticalRateLimit("demand-message"), controller.SendDemandMessage)
	group.POST("/:id/read", controller.PostDemandRead)
	group.POST("/:id/actions", middleware.UserCriticalRateLimit("demand-action"), controller.PostDemandAction)
}
