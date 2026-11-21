package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ForumSessionOnly(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		forumRespond(c, nil, &service.ForumError{Status: 403, Code: "FORUM_AUTH_REQUIRED", Message: "A login session is required"})
		c.Abort()
		return
	}
	c.Next()
}
func ForumAdminScope(c *gin.Context) { c.Set("forum_admin", true); c.Next() }
func forumActor(c *gin.Context) service.ForumActor {
	return service.ForumActor{UserID: c.GetInt("id"), Admin: c.GetBool("forum_admin")}
}
func forumRespond(c *gin.Context, data any, err error) {
	if err == nil {
		common.ApiSuccess(c, data)
		return
	}
	var problem *service.ForumError
	if !errors.As(err, &problem) {
		problem = &service.ForumError{Status: 503, Code: "FORUM_UNAVAILABLE", Message: "The forum is temporarily unavailable"}
	}
	c.JSON(problem.Status, gin.H{"success": false, "code": problem.Code, "message": problem.Message, "request_id": c.GetString(common.RequestIdKey)})
}
func forumBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if err := common.DecodeJson(c.Request.Body, v); err != nil {
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
		return false
	}
	return true
}
func forumMediaV2(c *gin.Context) bool {
	return c.Query("media_version") == "2" || c.GetHeader("X-Yunshu-Forum-Media-Version") == "2"
}
func GetForumConfig(c *gin.Context) {
	v, err := service.LoadForumConfig(model.DB.WithContext(c.Request.Context()))
	caps := gin.H{"immediate_publish": true, "likes": true, "comments": true, "image_uploads": true, "my_activity": true, "comment_replies": true, "notifications": true, "forum_profile": true, "receipt_lookup": true}
	if forumAvatarV1(c) {
		caps["forum_profile_avatar_v1"] = true
	}
	if forumMediaV2(c) {
		caps["forum_media_v2"] = true
		caps["video_uploads"] = true
	}
	forumRespond(c, gin.H{"revision": v.Revision, "enabled": v.Enabled, "capabilities": caps, "upload_rules": service.ForumUploadRules(forumMediaV2(c))}, err)
}
func PutForumConfig(c *gin.Context) {
	var in service.ForumConfig
	if !forumBind(c, &in) {
		return
	}
	v, err := service.SaveForumConfig(model.DB.WithContext(c.Request.Context()), forumActor(c), in)
	forumRespond(c, v, err)
}

type forumCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func forumPage(c *gin.Context) (int, forumCursor, error) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "30"))
	cursor := forumCursor{}
	if err != nil || limit < 1 || limit > 100 {
		return 0, cursor, &service.ForumError{Status: 400, Code: "FORUM_CURSOR_INVALID", Message: "Invalid forum page"}
	}
	if raw := c.Query("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 256 || e != nil || common.Unmarshal(b, &cursor) != nil || cursor.At < 1 || len(cursor.ID) > 40 || cursor.ID == "" {
			return 0, cursor, &service.ForumError{Status: 400, Code: "FORUM_CURSOR_INVALID", Message: "Invalid forum page"}
		}
	}
	return limit, cursor, nil
}
func forumNext(at int64, id string) string {
	b, _ := common.Marshal(forumCursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func forumFilter(c *gin.Context, q *gorm.DB, bodyColumn string) (*gorm.DB, error) {
	if !c.GetBool("forum_admin") {
		return q.Where("deleted_at = 0"), nil
	}
	switch c.DefaultQuery("review", "unreviewed") {
	case "unreviewed":
		q = q.Where("deleted_at = 0 AND reviewed_at = 0")
	case "reviewed":
		q = q.Where("deleted_at = 0 AND reviewed_at > 0")
	case "deleted":
		q = q.Where("deleted_at > 0")
	case "all":
	default:
		return nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"}
	}
	if text := strings.TrimSpace(c.Query("q")); text != "" {
		if utf8.RuneCountInString(text) > 100 {
			return nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"}
		}
		q = q.Where(bodyColumn+" LIKE ?", "%"+text+"%")
	}
	return q, nil
}
func ListForumPosts(c *gin.Context) {
	limit, cursor, err := forumPage(c)
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	q, err := forumFilter(c, db.Model(&model.ForumPost{}), "title")
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	if c.GetBool("forum_me") {
		q = q.Where("author_id = ?", forumActor(c).UserID)
	}
	if cursor.ID != "" {
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.At, cursor.At, cursor.ID)
	}
	var rows []model.ForumPost
	if err = q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		forumRespond(c, nil, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next := ""
	if more {
		last := rows[len(rows)-1]
		next = forumNext(last.CreatedAt, last.ID)
	}
	views, err := service.ProjectForumPosts(db, forumActor(c), rows, forumMediaV2(c), forumAvatarV1(c))
	forumRespond(c, gin.H{"items": views, "next_cursor": next, "has_more": more}, err)
}
func GetForumPost(c *gin.Context) {
	db := model.DB.WithContext(c.Request.Context())
	actor := forumActor(c)
	p, err := service.ForumPostAccess(db, actor, c.Param("post_id"))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	views, err := service.ProjectForumPosts(db, actor, []model.ForumPost{p}, forumMediaV2(c), forumAvatarV1(c))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	forumRespond(c, gin.H{"post": views[0]}, nil)
}
func CreateForumPost(c *gin.Context) {
	var in service.ForumCreate
	if !forumBind(c, &in) {
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	actor := forumActor(c)
	p, replayed, err := service.CreateForumPost(db, actor, in, forumMediaV2(c))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	views, err := service.ProjectForumPosts(db, actor, []model.ForumPost{p}, forumMediaV2(c), forumAvatarV1(c))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	out := gin.H{"post": views[0], "replayed": replayed}
	if !replayed {
		c.JSON(http.StatusCreated, gin.H{"success": true, "data": out})
		return
	}
	forumRespond(c, out, nil)
}
func PutForumLike(c *gin.Context) {
	var in struct {
		Liked *bool `json:"liked"`
	}
	if !forumBind(c, &in) {
		return
	}
	if in.Liked == nil {
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	actor := forumActor(c)
	if err := service.SetForumLike(db, actor, c.Param("post_id"), *in.Liked); err != nil {
		forumRespond(c, nil, err)
		return
	}
	GetForumPost(c)
}
func ListForumComments(c *gin.Context) {
	limit, cursor, err := forumPage(c)
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	actor := forumActor(c)
	q, err := forumFilter(c, db.Model(&model.ForumComment{}), "body")
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	postID := c.Param("post_id")
	if actor.Admin {
		postID = c.Query("post_id")
	}
	if postID != "" {
		if _, err = service.ForumPostAccess(db, actor, postID); err != nil {
			forumRespond(c, nil, err)
			return
		}
		q = q.Where("post_id = ?", postID)
	}
	if c.GetBool("forum_me") {
		q = q.Where("author_id = ?", actor.UserID).Where("post_id IN (?)", db.Model(&model.ForumPost{}).Select("id").Where("deleted_at = 0"))
	}
	if cursor.ID != "" {
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.At, cursor.At, cursor.ID)
	}
	order := "created_at DESC, id DESC"
	var rows []model.ForumComment
	if err = q.Order(order).Limit(limit + 1).Find(&rows).Error; err != nil {
		forumRespond(c, nil, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next := ""
	if more {
		last := rows[len(rows)-1]
		next = forumNext(last.CreatedAt, last.ID)
	}
	views, err := service.ProjectForumComments(db, actor, rows, forumMediaV2(c), forumAvatarV1(c))
	forumRespond(c, gin.H{"items": views, "next_cursor": next, "has_more": more}, err)
}
func CreateForumComment(c *gin.Context) {
	var in service.ForumCreate
	if !forumBind(c, &in) {
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	actor := forumActor(c)
	comment, replayed, err := service.CreateForumComment(db, actor, c.Param("post_id"), in, forumMediaV2(c))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	views, err := service.ProjectForumComments(db, actor, []model.ForumComment{comment}, forumMediaV2(c), forumAvatarV1(c))
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	out := gin.H{"comment": views[0], "replayed": replayed}
	if !replayed {
		c.JSON(http.StatusCreated, gin.H{"success": true, "data": out})
		return
	}
	forumRespond(c, out, nil)
}
func ModerateForumContent(c *gin.Context) {
	kind := "post"
	id := c.Param("post_id")
	if id == "" {
		kind = "comment"
		id = c.Param("comment_id")
	}
	action := "review"
	reason := ""
	if c.Request.Method == http.MethodDelete {
		action = "delete"
		if c.Request.ContentLength != 0 {
			var in struct {
				Reason string `json:"reason"`
			}
			if !forumBind(c, &in) {
				return
			}
			reason = in.Reason
		}
	}
	if action == "review" {
		var in struct {
			Reviewed bool `json:"reviewed"`
		}
		if !forumBind(c, &in) {
			return
		}
		if !in.Reviewed {
			forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
			return
		}
	}
	err := service.ModerateForumContent(model.DB.WithContext(c.Request.Context()), forumActor(c), kind, id, action, reason)
	forumRespond(c, gin.H{"id": id, "action": action}, err)
}
func GetForumAudit(c *gin.Context) {
	limit, cursor, err := forumPage(c)
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	q := model.DB.WithContext(c.Request.Context()).Model(&model.ForumAudit{})
	if target := c.Query("target_id"); target != "" {
		q = q.Where("target_id = ?", target)
	}
	if cursor.ID != "" {
		id, e := strconv.ParseUint(cursor.ID, 10, 64)
		if e != nil {
			forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_CURSOR_INVALID", Message: "Invalid forum page"})
			return
		}
		q = q.Where("id < ?", id)
	}
	var rows []model.ForumAudit
	err = q.Order("id DESC").Limit(limit + 1).Find(&rows).Error
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	next := ""
	if more {
		last := rows[len(rows)-1]
		next = forumNext(last.CreatedAt, strconv.FormatUint(last.ID, 10))
	}
	if rows == nil {
		rows = []model.ForumAudit{}
	}
	forumRespond(c, gin.H{"items": rows, "next_cursor": next, "has_more": more}, err)
}
