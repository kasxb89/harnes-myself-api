package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/gin-gonic/gin"
)

func registerForumProfileAvatar(user *gin.RouterGroup) {
	avatar := user.Group("/profile/avatar", controller.DemandOriginGuard)
	avatar.PUT("", controller.PutForumProfileAvatar)
	avatar.POST("/uploads", controller.CreateForumAvatarUpload)
	upload := avatar.Group("/uploads/:upload_id", controller.ForumAvatarUploadScope)
	upload.GET("", controller.GetForumUpload)
	upload.PUT("/chunks", controller.PutForumUploadChunk)
	upload.POST("/complete", controller.CompleteForumUpload)
}
