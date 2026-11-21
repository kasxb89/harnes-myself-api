package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func forumAvatarV1(c *gin.Context) bool {
	return c.Query("profile_avatar_version") == "1" || c.GetHeader("X-Yunshu-Forum-Profile-Avatar-Version") == "1"
}
func forumAvatarViewers(c *gin.Context) []service.ForumActor {
	if forumAvatarV1(c) {
		return []service.ForumActor{forumActor(c)}
	}
	return nil
}
func CreateForumAvatarUpload(c *gin.Context) {
	var in service.ForumUploadCreate
	if !forumBind(c, &in) {
		return
	}
	v, err := service.CreateForumAvatarUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), in)
	forumUploadReply(c, v, err)
}
func ForumAvatarUploadScope(c *gin.Context) {
	if err := service.ForumAvatarUploadAccess(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id")); err != nil {
		forumRespond(c, nil, err)
		c.Abort()
		return
	}
	c.Next()
}
func PutForumProfileAvatar(c *gin.Context) {
	var in service.ForumProfileAvatarInput
	if !forumBind(c, &in) {
		return
	}
	v, replay, err := service.SaveForumProfileAvatar(model.DB.WithContext(c.Request.Context()), forumActor(c), in)
	forumRespond(c, gin.H{"profile": v, "replayed": replay}, err)
}
