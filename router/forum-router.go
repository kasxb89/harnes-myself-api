package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func SetForumRoutes(api *gin.RouterGroup) {
	user := api.Group("/product/forum", middleware.DisableCache(), middleware.UserAuth(), controller.ForumSessionOnly)
	user.GET("/config", controller.GetForumConfig)
	user.GET("/posts", controller.ListForumPosts)
	user.GET("/posts/:post_id", controller.GetForumPost)
	user.GET("/posts/:post_id/comments", controller.ListForumComments)
	write := user.Group("", controller.DemandOriginGuard)
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
	moderate := admin.Group("", middleware.RequirePermission(authz.ForumWrite), controller.DemandOriginGuard)
	moderate.PUT("/config", controller.PutForumConfig)
	moderate.PUT("/posts/:post_id/review", controller.ModerateForumContent)
	moderate.DELETE("/posts/:post_id", controller.ModerateForumContent)
	moderate.PUT("/comments/:comment_id/review", controller.ModerateForumContent)
	moderate.DELETE("/comments/:comment_id", controller.ModerateForumContent)
}
