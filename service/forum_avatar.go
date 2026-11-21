package service

import (
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

const forumProfileAvatarPurpose = "profile-avatar"

type ForumAvatarView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}
type ForumProfileAvatarInput struct {
	ClientActionID     string  `json:"client_action_id"`
	Revision           int64   `json:"revision"`
	AvatarAttachmentID *string `json:"avatar_attachment_id"`
}

func CreateForumAvatarUpload(db *gorm.DB, a ForumActor, in ForumUploadCreate) (ForumUploadResult, error) {
	// Purpose is server-selected and enters the initial normal metadata hash.
	in.TargetKind = forumProfileAvatarPurpose
	in.PostID = ""
	if in.MimeType == "video/mp4" {
		return ForumUploadResult{}, forumError(415, "UPLOAD_UNSUPPORTED", "Unsupported forum image type")
	}
	return createForumUploadForPurpose(db, a, in, true)
}
func ForumAvatarUploadAccess(db *gorm.DB, a ForumActor, id string) error {
	var row model.ForumAttachment
	if db.Where("id = ? AND owner_id = ? AND target_kind = ?", id, a.UserID, forumProfileAvatarPurpose).First(&row).Error != nil {
		return ForumMissing()
	}
	return nil
}
func forumProfileAvatarAccess(db *gorm.DB, a ForumActor, v model.ForumAttachment) (model.ForumAttachment, error) {
	if !v.Bound {
		if v.OwnerID == a.UserID && v.ExpiresAt > time.Now().Unix() {
			return v, nil
		}
		return v, ForumMissing()
	}
	cfg, err := LoadForumConfig(db)
	if err != nil {
		return v, err
	}
	if !cfg.Enabled {
		return v, ForumMissing()
	}
	var profile model.ForumProfile
	q := db.Where("user_id = ? AND avatar_attachment_id = ?", v.OwnerID, v.ID).Where("user_id IN (?)", db.Model(&model.User{}).Select("id").Where("status = ?", common.UserStatusEnabled))
	if q.First(&profile).Error != nil || v.Kind != "image" || v.Status != "ready" || v.PostID != "" || v.CommentID != "" {
		return v, ForumMissing()
	}
	return v, nil
}
func forumProjectionAuthors(db *gorm.DB, a ForumActor, ids []int, options []bool) (map[int]ForumAuthor, error) {
	if len(options) > 1 && options[1] {
		return forumAuthors(db, ids, a)
	}
	return forumAuthors(db, ids)
}
func attachForumAvatars(db *gorm.DB, a ForumActor, names map[int]ForumAuthor) error {
	ids := make([]int, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	var profiles []model.ForumProfile
	if err := db.Where("user_id IN ? AND avatar_attachment_id <> ?", ids, "").Find(&profiles).Error; err != nil {
		return err
	}
	for _, p := range profiles {
		var asset model.ForumAttachment
		if err := db.Where("id = ? AND target_kind = ? AND kind = ? AND status = ?", p.AvatarAttachmentID, forumProfileAvatarPurpose, "image", "ready").First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		link, err := SignForumAttachmentLink(db, a, asset.ID, "inline")
		if err != nil {
			var problem *ForumError
			if errors.As(err, &problem) && problem.Status == 404 {
				continue
			}
			return err
		}
		v := names[p.UserID]
		v.Avatar = &ForumAvatarView{asset.ID, "image", asset.MimeType, asset.SizeBytes, asset.SHA256, link.URL, link.ExpiresAt}
		names[p.UserID] = v
	}
	return nil
}
func SaveForumProfileAvatar(db *gorm.DB, a ForumActor, in ForumProfileAvatarInput) (ForumProfileView, bool, error) {
	var out ForumProfileView
	key, err := uuid.Parse(in.ClientActionID)
	if err != nil || key.String() != in.ClientActionID || in.Revision < 0 || in.AvatarAttachmentID == nil {
		return out, false, forumInvalid()
	}
	hash := forumPayloadHash(in)
	receipt := func(q *gorm.DB) (bool, error) {
		var row model.ForumProfileAvatarReceipt
		err := q.First(&row, "user_id = ? AND client_action_id = ?", a.UserID, in.ClientActionID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if row.PayloadHash != hash {
			return false, forumConflict()
		}
		return true, nil
	}
	if found, err := receipt(db); found || err != nil {
		if err != nil {
			return out, false, err
		}
		out, e := GetForumProfile(db, a, true)
		return out, true, e
	}
	replayed := false
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := forumAccepting(tx); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ForumProfile{UserID: a.UserID, DisplayName: fmt.Sprintf("论坛用户%d", a.UserID)}).Error; err != nil {
			return err
		}
		q := tx
		if tx.Dialector.Name() != "sqlite" {
			q = tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var row model.ForumProfile
		if err := q.First(&row, "user_id = ?", a.UserID).Error; err != nil {
			return err
		}
		if found, e := receipt(tx); found || e != nil {
			replayed = found
			return e
		}
		if row.Revision != in.Revision {
			return &ForumError{409, "FORUM_PROFILE_CONFLICT", "Forum profile changed; reload and try again"}
		}
		if *in.AvatarAttachmentID != "" {
			var asset model.ForumAttachment
			if err := tx.First(&asset, "id = ?", *in.AvatarAttachmentID).Error; err != nil || asset.OwnerID != a.UserID || asset.TargetKind != forumProfileAvatarPurpose || asset.Status != "ready" || asset.Kind != "image" || asset.PostID != "" || asset.CommentID != "" {
				return forumError(400, "ATTACHMENT_INVALID", "Forum avatar must be a ready image owned by this account")
			}
			if !asset.Bound {
				res := tx.Model(&model.ForumAttachment{}).Where("id = ? AND bound = ? AND target_kind = ?", asset.ID, false, forumProfileAvatarPurpose).Update("bound", true)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return forumConflict()
				}
			}
		}
		res := tx.Model(&model.ForumProfile{}).Where("user_id = ? AND revision = ?", a.UserID, in.Revision).Updates(map[string]any{"avatar_attachment_id": *in.AvatarAttachmentID, "revision": in.Revision + 1})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return forumConflict()
		}
		return tx.Create(&model.ForumProfileAvatarReceipt{UserID: a.UserID, ClientActionID: in.ClientActionID, PayloadHash: hash}).Error
	})
	if err != nil {
		if found, e := receipt(db); found && e == nil {
			out, e = GetForumProfile(db, a, true)
			return out, true, e
		}
		return out, false, err
	}
	out, err = GetForumProfile(db, a, true)
	return out, replayed, err
}
