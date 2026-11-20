package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetForumRoutes(api *gin.RouterGroup) {
	public := api.Group("/product/forum", middleware.DisableCache())
	public.GET("/attachments/:attachment_id/content", controller.ServeForumAttachment)
	public.HEAD("/attachments/:attachment_id/content", controller.ServeForumAttachment)
	user := public.Group("", middleware.UserAuth(), controller.ForumSessionOnly)
	user.GET("/config", controller.GetForumConfig)
	user.GET("/posts", controller.ListForumPosts)
	user.GET("/posts/:post_id", controller.GetForumPost)
	user.GET("/posts/:post_id/comments", controller.ListForumComments)
	user.GET("/posts/:post_id/comments/:comment_id", controller.GetForumComment)
	user.GET("/profile", controller.GetForumProfile)
	user.GET("/receipts", controller.GetForumReceipt)
	user.GET("/me/posts", func(c *gin.Context) { c.Set("forum_me", true); controller.ListForumPosts(c) })
	user.GET("/me/comments", func(c *gin.Context) { c.Set("forum_me", true); controller.ListForumComments(c) })
	user.GET("/notifications", controller.ListForumNotifications)
	user.GET("/notifications/unread", controller.GetForumUnread)
	user.GET("/uploads/:upload_id", controller.GetForumUpload)
	write := user.Group("", controller.DemandOriginGuard)
	write.PATCH("/profile", controller.PutForumProfile)
	write.POST("/notifications/read", controller.ReadForumNotifications)
	write.POST("/uploads", controller.CreateForumUpload)
	write.PUT("/uploads/:upload_id/chunks", controller.PutForumUploadChunk)
	write.POST("/uploads/:upload_id/complete", controller.CompleteForumUpload)
	write.POST("/attachments/:attachment_id/link", controller.GetForumAttachmentLink)
	write.POST("/posts", middleware.UserCriticalRateLimit("forum-post"), controller.CreateForumPost)
	write.POST("/posts/:post_id/comments", middleware.UserCriticalRateLimit("forum-comment"), controller.CreateForumComment)
	write.PUT("/posts/:post_id/like", controller.PutForumLike)
	admin := api.Group("/forum-management", middleware.DisableCache(), middleware.AdminAuth(), controller.ForumSessionOnly, controller.ForumAdminScope)
	read := admin.Group("", middleware.RequirePermission(authz.ForumRead))
	read.GET("/config", controller.GetForumConfig)
	read.GET("/posts", controller.ListForumPosts)
	read.GET("/posts/:post_id", controller.GetForumPost)
	read.GET("/comments", controller.ListForumComments)
	read.GET("/audit", controller.GetForumAudit)
	read.POST("/attachments/:attachment_id/link", controller.DemandOriginGuard, controller.GetForumAttachmentLink)
	moderate := admin.Group("", middleware.RequirePermission(authz.ForumWrite), controller.DemandOriginGuard)
	moderate.PUT("/config", controller.PutForumConfig)
	moderate.PUT("/posts/:post_id/review", controller.ModerateForumContent)
	moderate.DELETE("/posts/:post_id", controller.ModerateForumContent)
	moderate.PUT("/comments/:comment_id/review", controller.ModerateForumContent)
	moderate.DELETE("/comments/:comment_id", controller.ModerateForumContent)
}
