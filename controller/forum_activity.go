package controller

import (
	"errors"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func GetForumProfile(c *gin.Context) {
	v, err := service.GetForumProfile(model.DB.WithContext(c.Request.Context()), forumActor(c))
	forumRespond(c, gin.H{"profile": v}, err)
}
func PutForumProfile(c *gin.Context) {
	var in struct {
		DisplayName string `json:"display_name"`
		Revision    *int64 `json:"revision"`
	}
	if !forumBind(c, &in) {
		return
	}
	if in.Revision == nil {
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
		return
	}
	v, err := service.SaveForumProfile(model.DB.WithContext(c.Request.Context()), forumActor(c), in.DisplayName, *in.Revision)
	forumRespond(c, gin.H{"profile": v}, err)
}
func GetForumComment(c *gin.Context) {
	db := model.DB.WithContext(c.Request.Context())
	a := forumActor(c)
	if _, err := service.ForumPostAccess(db, a, c.Param("post_id")); err != nil {
		forumRespond(c, nil, err)
		return
	}
	var row model.ForumComment
	q := db.Where("id = ? AND post_id = ?", c.Param("comment_id"), c.Param("post_id"))
	if !a.Admin {
		q = q.Where("deleted_at = 0")
	}
	if err := q.First(&row).Error; err != nil {
		forumRespond(c, nil, service.ForumMissing())
		return
	}
	views, err := service.ProjectForumComments(db, a, []model.ForumComment{row})
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	forumRespond(c, gin.H{"comment": views[0]}, nil)
}
func GetForumReceipt(c *gin.Context) {
	db := model.DB.WithContext(c.Request.Context())
	a := forumActor(c)
	key := c.Query("client_request_id")
	switch c.Query("kind") {
	case "post":
		var row model.ForumPost
		err := db.Where("author_id = ? AND client_request_id = ? AND deleted_at = 0", a.UserID, key).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = service.ForumMissing()
		}
		if err != nil {
			forumRespond(c, nil, err)
			return
		}
		views, err := service.ProjectForumPosts(db, a, []model.ForumPost{row})
		if err != nil {
			forumRespond(c, nil, err)
			return
		}
		forumRespond(c, gin.H{"post": views[0], "replayed": true}, nil)
	case "comment":
		var row model.ForumComment
		err := db.Where("author_id = ? AND client_request_id = ? AND deleted_at = 0", a.UserID, key).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = service.ForumMissing()
		}
		if err != nil {
			forumRespond(c, nil, err)
			return
		}
		if _, err = service.ForumPostAccess(db, a, row.PostID); err != nil {
			forumRespond(c, nil, err)
			return
		}
		views, err := service.ProjectForumComments(db, a, []model.ForumComment{row})
		if err != nil {
			forumRespond(c, nil, err)
			return
		}
		forumRespond(c, gin.H{"comment": views[0], "replayed": true}, nil)
	default:
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
	}
}
func GetForumUnread(c *gin.Context) {
	n, err := service.ForumUnreadCount(model.DB.WithContext(c.Request.Context()), forumActor(c))
	forumRespond(c, gin.H{"unread_count": n}, err)
}
func ListForumNotifications(c *gin.Context) {
	limit, cursor, err := forumPage(c)
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	db := model.DB.WithContext(c.Request.Context())
	a := forumActor(c)
	q := service.ForumNotificationQuery(db, a)
	if cursor.ID != "" {
		id, e := strconv.ParseUint(cursor.ID, 10, 64)
		if e != nil {
			forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_CURSOR_INVALID", Message: "Invalid forum page"})
			return
		}
		q = q.Where("id < ?", id)
	}
	var rows []model.ForumNotification
	if err = q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
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
		next = forumNext(last.CreatedAt, strconv.FormatUint(last.ID, 10))
	}
	views, err := service.ProjectForumNotifications(db, rows)
	if err != nil {
		forumRespond(c, nil, err)
		return
	}
	n, err := service.ForumUnreadCount(db, a)
	forumRespond(c, gin.H{"items": views, "has_more": more, "next_cursor": next, "unread_count": n}, err)
}
func ReadForumNotifications(c *gin.Context) {
	var in struct {
		IDs []string `json:"notification_ids"`
	}
	if !forumBind(c, &in) {
		return
	}
	if len(in.IDs) < 1 || len(in.IDs) > 100 {
		forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
		return
	}
	ids := []uint64{}
	for _, id := range in.IDs {
		v, err := strconv.ParseUint(id, 10, 64)
		if err != nil || v == 0 {
			forumRespond(c, nil, &service.ForumError{Status: 400, Code: "FORUM_INVALID_INPUT", Message: "Invalid forum input"})
			return
		}
		ids = append(ids, v)
	}
	db := model.DB.WithContext(c.Request.Context())
	a := forumActor(c)
	if err := db.Model(&model.ForumNotification{}).Where("recipient_id = ? AND id IN ? AND read_at = 0", a.UserID, ids).Update("read_at", time.Now().Unix()).Error; err != nil {
		forumRespond(c, nil, err)
		return
	}
	GetForumUnread(c)
}
