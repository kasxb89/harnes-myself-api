package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ForumActor struct {
	UserID int
	Admin  bool
}
type ForumError struct {
	Status        int
	Code, Message string
}

func (e *ForumError) Error() string { return e.Message }
func forumInvalid() error           { return &ForumError{400, "FORUM_INVALID_INPUT", "Invalid forum input"} }
func ForumMissing() error {
	return &ForumError{404, "FORUM_NOT_FOUND", "This forum content is no longer available"}
}

type ForumConfig struct {
	Revision int64 `json:"revision"`
	Enabled  bool  `json:"enabled"`
}

func LoadForumConfig(db *gorm.DB) (ForumConfig, error) {
	var row model.ForumConfiguration
	err := db.First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ForumConfig{}, nil
	}
	return ForumConfig{row.Revision, row.Enabled}, err
}
func SaveForumConfig(db *gorm.DB, actor ForumActor, in ForumConfig) (ForumConfig, error) {
	if in.Revision < 0 {
		return ForumConfig{}, forumInvalid()
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var res *gorm.DB
		if in.Revision == 0 {
			res = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ForumConfiguration{ID: 1, Revision: 1, Enabled: in.Enabled})
		} else {
			res = tx.Model(&model.ForumConfiguration{}).Where("id = ? AND revision = ?", 1, in.Revision).Updates(map[string]any{"enabled": in.Enabled, "revision": in.Revision + 1})
		}
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return &ForumError{409, "FORUM_REVISION_CONFLICT", "Forum settings changed. Reload and try again"}
		}
		return tx.Create(&model.ForumAudit{TargetType: "config", TargetID: "1", ActorID: actor.UserID, Action: "config_updated", CreatedAt: time.Now().Unix()}).Error
	})
	return ForumConfig{in.Revision + 1, in.Enabled}, err
}
func forumAccepting(db *gorm.DB) error {
	cfg, err := LoadForumConfig(db)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return &ForumError{403, "FORUM_CLOSED", "The forum is not accepting new content"}
	}
	return nil
}
func ForumPostAccess(db *gorm.DB, actor ForumActor, id string) (model.ForumPost, error) {
	var p model.ForumPost
	q := db.Where("id = ?", id)
	if !actor.Admin {
		q = q.Where("deleted_at = 0")
	}
	err := q.First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return p, ForumMissing()
	}
	return p, err
}

type ForumCreate struct {
	ClientRequestID string `json:"client_request_id"`
	Title           string `json:"title"`
	Body            string `json:"body"`
}

func forumPayloadHash(v any) string {
	b, _ := common.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func forumRequestValid(key, body string, max int) bool {
	return key != "" && utf8.RuneCountInString(key) <= 80 && strings.TrimSpace(body) != "" && utf8.RuneCountInString(body) <= max
}
func CreateForumPost(db *gorm.DB, actor ForumActor, in ForumCreate) (model.ForumPost, bool, error) {
	if !forumRequestValid(in.ClientRequestID, in.Body, 20000) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 {
		return model.ForumPost{}, false, forumInvalid()
	}
	hash := forumPayloadHash(in)
	var prior model.ForumPost
	err := db.Where("author_id = ? AND client_request_id = ?", actor.UserID, in.ClientRequestID).First(&prior).Error
	if err == nil {
		if prior.PayloadHash != hash {
			return prior, false, &ForumError{409, "FORUM_REQUEST_CONFLICT", "The publication key belongs to different content"}
		}
		if prior.DeletedAt != 0 {
			return prior, false, ForumMissing()
		}
		return prior, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return prior, false, err
	}
	if err = forumAccepting(db); err != nil {
		return prior, false, err
	}
	p := model.ForumPost{ID: uuid.NewString(), AuthorID: actor.UserID, ClientRequestID: in.ClientRequestID, PayloadHash: hash, Title: strings.TrimSpace(in.Title), Body: strings.TrimSpace(in.Body), CreatedAt: time.Now().Unix()}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&p)
	if result.Error != nil {
		return p, false, result.Error
	}
	if result.RowsAffected == 1 {
		return p, false, nil
	}
	err = db.Where("author_id = ? AND client_request_id = ?", actor.UserID, in.ClientRequestID).First(&prior).Error
	if err != nil {
		return prior, false, err
	}
	if prior.PayloadHash != hash {
		return prior, false, &ForumError{409, "FORUM_REQUEST_CONFLICT", "The publication key belongs to different content"}
	}
	if prior.DeletedAt != 0 {
		return prior, false, ForumMissing()
	}
	return prior, true, nil
}
func CreateForumComment(db *gorm.DB, actor ForumActor, postID string, in ForumCreate) (model.ForumComment, bool, error) {
	if !forumRequestValid(in.ClientRequestID, in.Body, 4000) {
		return model.ForumComment{}, false, forumInvalid()
	}
	var out model.ForumComment
	replayed := false
	hash := forumPayloadHash(struct {
		PostID string
		Input  ForumCreate
	}{postID, in})
	err := db.Transaction(func(tx *gorm.DB) error {
		p, err := model.LockForumPost(tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) || p.DeletedAt != 0 {
			return ForumMissing()
		}
		if err != nil {
			return err
		}
		err = tx.Where("author_id = ? AND client_request_id = ?", actor.UserID, in.ClientRequestID).First(&out).Error
		if err == nil {
			if out.PayloadHash != hash {
				return &ForumError{409, "FORUM_REQUEST_CONFLICT", "The publication key belongs to different content"}
			}
			if out.DeletedAt != 0 {
				return ForumMissing()
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err = forumAccepting(tx); err != nil {
			return err
		}
		out = model.ForumComment{ID: uuid.NewString(), PostID: postID, AuthorID: actor.UserID, ClientRequestID: in.ClientRequestID, PayloadHash: hash, Body: strings.TrimSpace(in.Body), CreatedAt: time.Now().Unix()}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&out)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			return nil
		}
		if err = tx.Where("author_id = ? AND client_request_id = ?", actor.UserID, in.ClientRequestID).First(&out).Error; err != nil {
			return err
		}
		if out.PayloadHash != hash {
			return &ForumError{409, "FORUM_REQUEST_CONFLICT", "The publication key belongs to different content"}
		}
		if out.DeletedAt != 0 {
			return ForumMissing()
		}
		replayed = true
		return nil
	})
	return out, replayed, err
}
func SetForumLike(db *gorm.DB, actor ForumActor, postID string, liked bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		p, err := model.LockForumPost(tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) || p.DeletedAt != 0 {
			return ForumMissing()
		}
		if err != nil {
			return err
		}
		if err = forumAccepting(tx); err != nil {
			return err
		}
		if liked {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ForumLike{PostID: postID, UserID: actor.UserID, CreatedAt: time.Now().Unix()}).Error
		}
		return tx.Where("post_id = ? AND user_id = ?", postID, actor.UserID).Delete(&model.ForumLike{}).Error
	})
}

type ForumAuthor struct {
	ID          int    `json:"id"`
	DisplayName string `json:"display_name"`
}
type ForumPostView struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Body         string      `json:"body"`
	Author       ForumAuthor `json:"author"`
	CreatedAt    int64       `json:"created_at"`
	LikeCount    int64       `json:"like_count"`
	CommentCount int64       `json:"comment_count"`
	Liked        bool        `json:"liked"`
	ReviewedAt   int64       `json:"reviewed_at,omitempty"`
	DeletedAt    int64       `json:"deleted_at,omitempty"`
	DeleteReason string      `json:"delete_reason,omitempty"`
}
type ForumCommentView struct {
	ID           string      `json:"id"`
	PostID       string      `json:"post_id"`
	PostTitle    string      `json:"post_title,omitempty"`
	Body         string      `json:"body"`
	Author       ForumAuthor `json:"author"`
	CreatedAt    int64       `json:"created_at"`
	ReviewedAt   int64       `json:"reviewed_at,omitempty"`
	DeletedAt    int64       `json:"deleted_at,omitempty"`
	DeleteReason string      `json:"delete_reason,omitempty"`
}

func forumAuthors(db *gorm.DB, ids []int) (map[int]ForumAuthor, error) {
	out := map[int]ForumAuthor{}
	if len(ids) == 0 {
		return out, nil
	}
	var users []struct {
		ID          int
		DisplayName string
	}
	err := db.Model(&model.User{}).Select("id, display_name").Where("id IN ?", ids).Find(&users).Error
	for _, u := range users {
		out[u.ID] = ForumAuthor{u.ID, u.DisplayName}
	}
	return out, err
}
func ProjectForumPosts(db *gorm.DB, actor ForumActor, posts []model.ForumPost) ([]ForumPostView, error) {
	views := make([]ForumPostView, 0, len(posts))
	if len(posts) == 0 {
		return views, nil
	}
	ids := make([]string, 0, len(posts))
	authors := make([]int, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
		authors = append(authors, p.AuthorID)
	}
	names, err := forumAuthors(db, authors)
	if err != nil {
		return nil, err
	}
	type countRow struct {
		PostID string
		Total  int64
	}
	var likes, comments []countRow
	var liked []model.ForumLike
	if err = db.Model(&model.ForumLike{}).Select("post_id, COUNT(*) AS total").Where("post_id IN ?", ids).Group("post_id").Find(&likes).Error; err != nil {
		return nil, err
	}
	if err = db.Model(&model.ForumComment{}).Select("post_id, COUNT(*) AS total").Where("post_id IN ? AND deleted_at = 0", ids).Group("post_id").Find(&comments).Error; err != nil {
		return nil, err
	}
	if err = db.Where("post_id IN ? AND user_id = ?", ids, actor.UserID).Find(&liked).Error; err != nil {
		return nil, err
	}
	likeCounts, commentCounts, owned := map[string]int64{}, map[string]int64{}, map[string]bool{}
	for _, x := range likes {
		likeCounts[x.PostID] = x.Total
	}
	for _, x := range comments {
		commentCounts[x.PostID] = x.Total
	}
	for _, x := range liked {
		owned[x.PostID] = true
	}
	for _, p := range posts {
		v := ForumPostView{ID: p.ID, Title: p.Title, Body: p.Body, Author: ForumAuthor{ID: p.AuthorID, DisplayName: names[p.AuthorID].DisplayName}, CreatedAt: p.CreatedAt, LikeCount: likeCounts[p.ID], CommentCount: commentCounts[p.ID], Liked: owned[p.ID]}
		if actor.Admin {
			v.ReviewedAt = p.ReviewedAt
			v.DeletedAt = p.DeletedAt
			v.DeleteReason = p.DeleteReason
		}
		views = append(views, v)
	}
	return views, nil
}
func ProjectForumComments(db *gorm.DB, actor ForumActor, comments []model.ForumComment) ([]ForumCommentView, error) {
	views := make([]ForumCommentView, 0, len(comments))
	if len(comments) == 0 {
		return views, nil
	}
	ids := make([]int, 0, len(comments))
	postIDs := make([]string, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.AuthorID)
		postIDs = append(postIDs, c.PostID)
	}
	authors, err := forumAuthors(db, ids)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if actor.Admin {
		var posts []model.ForumPost
		if err = db.Select("id,title").Where("id IN ?", postIDs).Find(&posts).Error; err != nil {
			return nil, err
		}
		for _, p := range posts {
			titles[p.ID] = p.Title
		}
	}
	for _, c := range comments {
		v := ForumCommentView{ID: c.ID, PostID: c.PostID, Body: c.Body, Author: ForumAuthor{ID: c.AuthorID, DisplayName: authors[c.AuthorID].DisplayName}, CreatedAt: c.CreatedAt}
		if actor.Admin {
			v.PostTitle = titles[c.PostID]
			v.ReviewedAt = c.ReviewedAt
			v.DeletedAt = c.DeletedAt
			v.DeleteReason = c.DeleteReason
		}
		views = append(views, v)
	}
	return views, nil
}

// Review is post-publication bookkeeping. Removal never needs the author's approval.
func ModerateForumContent(db *gorm.DB, actor ForumActor, kind, id, action, reason string) error {
	if !actor.Admin {
		return &ForumError{403, "FORUM_FORBIDDEN", "Forum moderation permission is required"}
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 1000 {
		return forumInvalid()
	}
	if (kind != "post" && kind != "comment") || (action != "review" && action != "delete") {
		return forumInvalid()
	}
	return db.Transaction(func(tx *gorm.DB) error {
		postID := id
		var comment model.ForumComment
		if kind == "comment" {
			err := tx.Where("id = ?", id).First(&comment).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ForumMissing()
			}
			if err != nil {
				return err
			}
			postID = comment.PostID
		}
		p, err := model.LockForumPost(tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ForumMissing()
		}
		if err != nil {
			return err
		}
		deletedAt, reviewedAt := p.DeletedAt, p.ReviewedAt
		var target any = &model.ForumPost{}
		if kind == "comment" {
			if err = tx.Where("id = ?", id).First(&comment).Error; err != nil {
				return err
			}
			deletedAt, reviewedAt = comment.DeletedAt, comment.ReviewedAt
			target = &model.ForumComment{}
		}
		now := time.Now().Unix()
		updates := map[string]any{}
		if action == "delete" {
			if deletedAt != 0 {
				return nil
			}
			updates = map[string]any{"deleted_at": now, "deleted_by": actor.UserID, "delete_reason": reason}
		}
		if action == "review" {
			if deletedAt != 0 {
				return ForumMissing()
			}
			if reviewedAt != 0 {
				return nil
			}
			updates = map[string]any{"reviewed_at": now, "reviewed_by": actor.UserID}
		}
		if err = tx.Model(target).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Create(&model.ForumAudit{TargetType: kind, TargetID: id, ActorID: actor.UserID, Action: action, Reason: reason, CreatedAt: now}).Error
	})
}
