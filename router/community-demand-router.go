package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetCommunityDemandRoutes(api *gin.RouterGroup) {
	public := api.Group("/product/community-demands", middleware.DisableCache())
	public.GET("/attachments/:attachment_id/content", controller.ServeCommunityAttachment)
	public.HEAD("/attachments/:attachment_id/content", controller.ServeCommunityAttachment)
	// Signed content checks the viewer's current account, permission and order ACL.
	signedAdmin := api.Group("/community-demand-management", middleware.DisableCache())
	signedAdmin.GET("/attachments/:attachment_id/content", controller.ServeCommunityAttachment)
	signedAdmin.HEAD("/attachments/:attachment_id/content", controller.ServeCommunityAttachment)
	user := public.Group("", middleware.UserAuth(), controller.CommunitySessionOnly, controller.DemandOriginGuard)
	user.GET("/config", controller.GetCommunityConfig)
	user.GET("/listings", func(c *gin.Context) { c.Set("community_listings", true); controller.ListCommunityOrders(c) })
	user.GET("/listings/:id", controller.GetCommunityListing)
	user.GET("/orders", controller.ListCommunityOrders)
	user.POST("/orders", middleware.UserCriticalRateLimit("community-create"), controller.CreateCommunityOrder)
	user.POST("/orders/:id/accept", middleware.UserCriticalRateLimit("community-accept"), controller.AcceptCommunityOrder)
	user.GET("/orders/:id", controller.GetCommunityOrder)
	user.GET("/orders/:id/messages", controller.GetCommunityMessages)
	user.POST("/orders/:id/messages", middleware.UserCriticalRateLimit("community-message"), controller.SendCommunityMessage)
	user.POST("/orders/:id/actions", middleware.UserCriticalRateLimit("community-action"), controller.PostCommunityAction)
	user.POST("/orders/:id/read", controller.PostCommunityRead)
	communityUploads(user)
	admin := api.Group("/community-demand-management", middleware.DisableCache(), middleware.AdminAuth(), controller.CommunitySessionOnly, controller.CommunityAdminScope)
	read := admin.Group("", middleware.RequirePermission(authz.CommunityDemandsRead))
	read.GET("/config", controller.GetCommunityConfig)
	read.GET("/orders", controller.ListCommunityOrders)
	read.GET("/orders/:id", controller.GetCommunityOrder)
	read.GET("/orders/:id/messages", controller.GetCommunityMessages)
	read.GET("/orders/:id/audit", controller.GetCommunityAudit)
	read.POST("/orders/:id/read", controller.DemandOriginGuard, controller.PostCommunityRead)
	read.POST("/attachments/:attachment_id/link", controller.DemandOriginGuard, controller.GetCommunityAttachmentLink)
	write := admin.Group("", middleware.RequirePermission(authz.CommunityDemandsWrite), controller.DemandOriginGuard)
	write.PUT("/config", controller.PutCommunityConfig)
	write.POST("/orders/:id/messages", controller.SendCommunityMessage)
	write.POST("/orders/:id/actions", controller.PostCommunityAction)
	write.POST("/uploads", controller.CreateCommunityUpload)
	write.GET("/uploads/:upload_id", controller.GetCommunityUpload)
	write.PUT("/uploads/:upload_id/content", controller.PutCommunityUploadChunk)
	write.POST("/uploads/:upload_id/complete", controller.CompleteCommunityUpload)
}
func communityUploads(g *gin.RouterGroup) {
	g.POST("/uploads", middleware.UserCriticalRateLimit("community-upload"), controller.CreateCommunityUpload)
	g.GET("/uploads/:upload_id", controller.GetCommunityUpload)
	g.PUT("/uploads/:upload_id/content", controller.PutCommunityUploadChunk)
	g.POST("/uploads/:upload_id/complete", controller.CompleteCommunityUpload)
	g.POST("/attachments/:attachment_id/link", controller.GetCommunityAttachmentLink)
}
