package service

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ForumProfileView struct {
	Avatar      *ForumAvatarView `json:"avatar,omitempty"`
	ID          int              `json:"id"`
	DisplayName string           `json:"display_name"`
	Revision    int64            `json:"revision"`
}

func GetForumProfile(db *gorm.DB, a ForumActor, avatarV1 ...bool) (ForumProfileView, error) {
	names, err := forumProjectionAuthors(db, a, []int{a.UserID}, []bool{false, len(avatarV1) > 0 && avatarV1[0]})
	if err != nil {
		return ForumProfileView{}, err
	}
	out := ForumProfileView{ID: a.UserID, DisplayName: names[a.UserID].DisplayName, Avatar: names[a.UserID].Avatar}
	var row model.ForumProfile
	err = db.First(&row, "user_id = ?", a.UserID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err == nil {
		out.Revision = row.Revision
	}
	return out, err
}
func SaveForumProfile(db *gorm.DB, a ForumActor, name string, revision int64) (ForumProfileView, error) {
	name = strings.TrimSpace(name)
	invalid := &ForumError{400, "FORUM_PROFILE_INVALID", "Choose a forum nickname without phone numbers or account names"}
	if name == "" || utf8.RuneCountInString(name) > 40 || revision < 0 {
		return ForumProfileView{}, invalid
	}
	// Accept names, not a contact/credential field. Fold separators before detecting phone-like runs.
	digits, masked := 0, false
	var compact strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			return ForumProfileView{}, invalid
		}
		if unicode.IsDigit(r) {
			digits++
			compact.WriteRune(r)
		} else if strings.ContainsRune("*•●", r) {
			masked = true
		} else if !unicode.IsSpace(r) && !strings.ContainsRune("+()-._", r) {
			compact.WriteByte('x')
		}
	}
	if masked && digits >= 4 {
		return ForumProfileView{}, invalid
	}
	run := 0
	for _, r := range compact.String() {
		if unicode.IsDigit(r) {
			run++
			if run >= 7 {
				return ForumProfileView{}, invalid
			}
		} else {
			run = 0
		}
	}
	var user model.User
	if err := db.Select("id,username").First(&user, a.UserID).Error; err != nil {
		return ForumProfileView{}, err
	}
	if strings.EqualFold(name, user.Username) {
		return ForumProfileView{}, invalid
	}
	row := model.ForumProfile{UserID: a.UserID, DisplayName: name, Revision: revision + 1}
	var res *gorm.DB
	if revision == 0 {
		res = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	} else {
		res = db.Model(&model.ForumProfile{}).Where("user_id = ? AND revision = ?", a.UserID, revision).Updates(map[string]any{"display_name": name, "revision": revision + 1})
	}
	if res.Error != nil {
		return ForumProfileView{}, res.Error
	}
	if res.RowsAffected == 0 {
		current, err := GetForumProfile(db, a)
		if err != nil {
			return current, err
		}
		if current.Revision == revision+1 && current.DisplayName == name {
			return current, nil
		}
		return current, &ForumError{409, "FORUM_REVISION_CONFLICT", "Forum settings changed. Reload and try again"}
	}
	return ForumProfileView{ID: a.UserID, DisplayName: name, Revision: revision + 1}, nil
}
func forumLikeEvent(post string, actor int) string { return fmt.Sprintf("like:%s:%d", post, actor) }
func notifyForumLike(tx *gorm.DB, p model.ForumPost, actor int) error {
	row := model.ForumNotification{RecipientID: p.AuthorID, EventKey: forumLikeEvent(p.ID, actor), Kind: "like", ActorID: actor, PostID: p.ID, CreatedAt: time.Now().Unix(), Active: true}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	return tx.Model(&model.ForumNotification{}).Where("recipient_id = ? AND event_key = ?", row.RecipientID, row.EventKey).Update("active", true).Error
}
func notifyForumComment(tx *gorm.DB, p model.ForumPost, c model.ForumComment) error {
	recipients := map[int]bool{p.AuthorID: true}
	if c.ReplyToCommentID != "" {
		var target model.ForumComment
		if err := tx.First(&target, "id = ?", c.ReplyToCommentID).Error; err != nil {
			return err
		}
		recipients[target.AuthorID] = true
	}
	delete(recipients, c.AuthorID)
	for recipient := range recipients {
		row := model.ForumNotification{RecipientID: recipient, EventKey: "reply:" + c.ID, Kind: "reply", ActorID: c.AuthorID, PostID: p.ID, CommentID: c.ID, CreatedAt: c.CreatedAt, Active: true}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// Notifications never revive hidden forum content. This same query powers count/list/read receipts.
func ForumNotificationQuery(db *gorm.DB, a ForumActor) *gorm.DB {
	return db.Model(&model.ForumNotification{}).Where("recipient_id = ? AND active = ?", a.UserID, true).
		Where("post_id IN (?)", db.Model(&model.ForumPost{}).Select("id").Where("deleted_at = 0")).
		Where("comment_id = '' OR comment_id IN (?)", db.Model(&model.ForumComment{}).Select("id").Where("deleted_at = 0"))
}
func ForumUnreadCount(db *gorm.DB, a ForumActor) (int64, error) {
	var n int64
	err := ForumNotificationQuery(db, a).Where("read_at = 0").Count(&n).Error
	return n, err
}

type ForumNotificationView struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	PostID    string      `json:"post_id"`
	CommentID string      `json:"comment_id"`
	Actor     ForumAuthor `json:"actor"`
	PostTitle string      `json:"post_title"`
	CreatedAt int64       `json:"created_at"`
	ReadAt    int64       `json:"read_at"`
}

func ProjectForumNotifications(db *gorm.DB, rows []model.ForumNotification, avatarViewer ...ForumActor) ([]ForumNotificationView, error) {
	out := make([]ForumNotificationView, 0, len(rows))
	ids := []int{}
	postIDs := []string{}
	for _, r := range rows {
		ids = append(ids, r.ActorID)
		postIDs = append(postIDs, r.PostID)
	}
	names, err := forumAuthors(db, ids, avatarViewer...)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if len(postIDs) > 0 {
		var posts []model.ForumPost
		if err = db.Select("id,title").Where("id IN ?", postIDs).Find(&posts).Error; err != nil {
			return nil, err
		}
		for _, p := range posts {
			titles[p.ID] = p.Title
		}
	}
	for _, r := range rows {
		author := names[r.ActorID]
		author.ID = r.ActorID
		out = append(out, ForumNotificationView{fmt.Sprint(r.ID), r.Kind, r.PostID, r.CommentID, author, titles[r.PostID], r.CreatedAt, r.ReadAt})
	}
	return out, nil
}
