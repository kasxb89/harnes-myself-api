package controller

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strings"
)

func forumUploadReply(c *gin.Context, v service.ForumUploadResult, err error) {
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	if v.Attachment.Kind == "video" && !forumMediaV2(c) {
		forumRespond(c, nil, &service.ForumError{Status: 415, Code: "FORUM_UPLOAD_UNSUPPORTED", Message: "Unsupported forum attachment"})
		return
	}
	f := service.ProjectForumAttachment(v.Attachment)
	var ready *service.ForumAttachmentView
	if f.Status == "ready" {
		ready = &f
	}
	forumRespond(c, gin.H{"id": f.ID, "attachment_id": f.ID, "offset": v.Offset, "chunk_bytes": v.ChunkBytes, "status": f.Status, "expires_at": v.ExpiresAt, "attachment": ready}, nil)
}
func CreateForumUpload(c *gin.Context) {
	var in service.ForumUploadCreate
	if !forumBind(c, &in) {
		return
	}
	if strings.HasPrefix(in.MimeType, "video/") && !forumMediaV2(c) {
		forumRespond(c, nil, &service.ForumError{Status: 415, Code: "FORUM_UPLOAD_UNSUPPORTED", Message: "Unsupported forum attachment"})
		return
	}
	v, err := service.CreateForumUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), in)
	forumUploadReply(c, v, err)
}
func GetForumUpload(c *gin.Context) {
	v, err := service.GetForumUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id"))
	forumUploadReply(c, v, err)
}
func PutForumUploadChunk(c *gin.Context) {
	if !forumUploadVersionBeforeWrite(c) {
		return
	}
	offset, err := strconv.ParseInt(c.GetHeader("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/octet-stream") {
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (8<<20)+1)
	v, err := service.PutForumUploadChunk(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id"), offset, c.Request.Body)
	var conflict *service.ForumError
	if errors.As(err, &conflict) && conflict.Code == "FORUM_UPLOAD_OFFSET_CONFLICT" {
		current, readErr := service.GetForumUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id"))
		if readErr != nil {
			forumRespond(c, nil, readErr)
			return
		}
		c.JSON(conflict.Status, gin.H{"success": false, "code": conflict.Code, "message": conflict.Message, "request_id": c.GetString(common.RequestIdKey), "offset": current.Offset})
		return
	}
	forumUploadReply(c, v, err)
}
func CompleteForumUpload(c *gin.Context) {
	if !forumUploadVersionBeforeWrite(c) {
		return
	}
	var in struct {
		SHA256 string `json:"sha256"`
	}
	if !forumBind(c, &in) {
		return
	}
	v, err := service.CompleteForumUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id"), in.SHA256)
	forumUploadReply(c, v, err)
}
func GetForumAttachmentLink(c *gin.Context) {
	var in struct {
		Disposition string `json:"disposition"`
	}
	if !forumBind(c, &in) {
		return
	}
	v, err := service.SignForumAttachmentLink(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("attachment_id"), in.Disposition)
	forumRespond(c, v, err)
}
func ServeForumAttachment(c *gin.Context) {
	v, f, disposition, err := service.OpenForumAttachment(model.DB.WithContext(c.Request.Context()), c.Param("attachment_id"), c.Query("token"))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	c.Header("Content-Type", v.MimeType)
	c.Header("Content-Disposition", disposition)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("ETag", `"`+v.SHA256+`"`)
	http.ServeContent(c.Writer, c.Request, service.ProjectForumAttachment(v).Name, info.ModTime(), f)
}

func forumUploadVersionBeforeWrite(c *gin.Context) bool {
	if forumMediaV2(c) {
		return true
	}
	v, err := service.GetForumUpload(model.DB.WithContext(c.Request.Context()), forumActor(c), c.Param("upload_id"))
	if err != nil {
		forumRespond(c, nil, err)
		return false
	}
	if v.Attachment.Kind == "video" {
		forumUploadReply(c, v, nil)
		return false
	}
	return true
}
