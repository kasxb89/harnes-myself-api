package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"mime"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const demandServiceGalleryScope = "service-gallery"

type DemandServiceGalleryEntry struct {
	AttachmentID       string `json:"attachment_id"`
	PosterAttachmentID string `json:"poster_attachment_id,omitempty"`
}
type DemandServiceGalleryInput struct {
	Revision          int64                       `json:"revision"`
	ClientActionID    string                      `json:"client_action_id"`
	CoverAttachmentID string                      `json:"cover_attachment_id"`
	Media             []DemandServiceGalleryEntry `json:"media"`
}
type DemandServiceMediaView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	PosterURL string `json:"poster_url,omitempty"`
}
type DemandServiceGalleryView struct {
	ProfileID         string                   `json:"profile_id"`
	Revision          int64                    `json:"revision"`
	CoverAttachmentID string                   `json:"cover_attachment_id"`
	CoverURL          string                   `json:"cover_url"`
	Media             []DemandServiceMediaView `json:"media"`
}

func demandServiceMediaMissing() error {
	return demandError(404, "ATTACHMENT_NOT_FOUND", "Demand attachment not found")
}
func serviceGalleryEntries(v model.DemandServiceGallery) ([]DemandServiceGalleryEntry, error) {
	entries := []DemandServiceGalleryEntry{}
	if v.MediaJSON != "" {
		if err := common.Unmarshal([]byte(v.MediaJSON), &entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}
func galleryAssetIDs(v model.DemandServiceGallery) ([]string, error) {
	entries, err := serviceGalleryEntries(v)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	if v.CoverAttachmentID != "" {
		ids = append(ids, v.CoverAttachmentID)
	}
	for _, e := range entries {
		ids = append(ids, e.AttachmentID)
		if e.PosterAttachmentID != "" {
			ids = append(ids, e.PosterAttachmentID)
		}
	}
	return ids, nil
}
func isServiceGalleryAsset(v model.DemandAttachment) bool {
	allowed := map[string]string{"image/png": "image", "image/jpeg": "image", "image/gif": "image", "image/webp": "image", "video/mp4": "video", "video/webm": "video", "video/quicktime": "video"}
	return v.AvatarReserved && v.AvatarScope == demandServiceGalleryScope && v.Status == "ready" && v.DemandID == "" && v.MessageID == "" && (v.Kind == "image" || v.Kind == "video") && allowed[v.MimeType] == v.Kind
}
func demandServiceProfileAccess(db *gorm.DB, a DemandActor, id string) (model.DemandServiceProfile, error) {
	var p model.DemandServiceProfile
	q := db.Where("id = ?", id)
	if !a.Admin {
		q = q.Where("published = ? AND enabled = ?", true, true)
	}
	if q.First(&p).Error != nil {
		return p, demandServiceMediaMissing()
	}
	return p, nil
}

func CreateDemandServiceMediaUpload(db *gorm.DB, a DemandActor, in DemandUploadCreate) (DemandUploadResult, error) {
	if !a.Admin {
		return DemandUploadResult{}, demandServiceMediaMissing()
	}
	return createDemandUploadForScope(db, a, in, demandServiceGalleryScope)
}
func GetDemandServiceMediaUpload(db *gorm.DB, a DemandActor, id string) (DemandUploadResult, error) {
	v, err := GetDemandUpload(db, a, id)
	if err != nil {
		return v, err
	}
	if !a.Admin || v.Attachment.OwnerID != a.UserID || !v.Attachment.AvatarReserved || v.Attachment.AvatarScope != demandServiceGalleryScope {
		return DemandUploadResult{}, demandServiceMediaMissing()
	}
	return v, nil
}

func SaveDemandServiceGallery(db *gorm.DB, a DemandActor, profileID string, in DemandServiceGalleryInput) (DemandServiceGalleryView, bool, error) {
	invalid := demandError(400, "UPLOAD_INVALID", "Invalid service gallery")
	if !a.Admin || profileID == "" || len(profileID) > 80 || in.Revision < 0 || len(in.Media) > 30 {
		return DemandServiceGalleryView{}, false, invalid
	}
	if _, err := uuid.Parse(in.ClientActionID); err != nil {
		return DemandServiceGalleryView{}, false, invalid
	}
	if (len(in.Media) == 0) != (in.CoverAttachmentID == "") {
		return DemandServiceGalleryView{}, false, invalid
	}
	hash := demandHash(in)
	replayed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var profile model.DemandServiceProfile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&profile, "id = ?", profileID).Error; err != nil {
			return demandServiceMediaMissing()
		}
		var receipt model.DemandServiceGalleryReceipt
		e := tx.Where("actor_id = ? AND client_action_id = ?", a.UserID, in.ClientActionID).First(&receipt).Error
		if e == nil {
			if receipt.ProfileID != profileID || receipt.PayloadHash != hash {
				return demandError(409, "IDEMPOTENCY_CONFLICT", "The request key was already used with different content")
			}
			replayed = true
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.DemandServiceGallery{ProfileID: profileID, MediaJSON: "[]"}).Error; err != nil {
			return err
		}
		var old model.DemandServiceGallery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, "profile_id = ?", profileID).Error; err != nil {
			return err
		}
		if old.Revision != in.Revision {
			return demandError(409, "REVISION_CONFLICT", "Profile changed; reload before saving")
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, m := range in.Media {
			if m.AttachmentID == "" || seen[m.AttachmentID] {
				return invalid
			}
			seen[m.AttachmentID] = true
			ids = append(ids, m.AttachmentID)
			if m.PosterAttachmentID != "" {
				ids = append(ids, m.PosterAttachmentID)
			}
		}
		if len(in.Media) > 0 {
			if !slices.Contains(ids, in.CoverAttachmentID) {
				return invalid
			}
			var assets []model.DemandAttachment
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Find(&assets).Error; err != nil {
				return err
			}
			byID := map[string]model.DemandAttachment{}
			for _, asset := range assets {
				if !isServiceGalleryAsset(asset) || asset.OwnerID != a.UserID {
					return invalid
				}
				byID[asset.ID] = asset
			}
			if byID[in.CoverAttachmentID].Kind != "image" {
				return invalid
			}
			for _, m := range in.Media {
				if byID[m.AttachmentID].ID == "" {
					return invalid
				}
				if m.PosterAttachmentID != "" && byID[m.PosterAttachmentID].Kind != "image" {
					return invalid
				}
			}
		}
		raw, err := common.Marshal(in.Media)
		if err != nil {
			return err
		}
		result := tx.Model(&model.DemandServiceGallery{}).Where("profile_id = ? AND revision = ?", profileID, in.Revision).Updates(map[string]any{"revision": in.Revision + 1, "cover_attachment_id": in.CoverAttachmentID, "media_json": string(raw)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return demandError(409, "REVISION_CONFLICT", "Profile changed; reload before saving")
		}
		return tx.Create(&model.DemandServiceGalleryReceipt{ActorID: a.UserID, ClientActionID: in.ClientActionID, ProfileID: profileID, PayloadHash: hash}).Error
	})
	if err != nil {
		var receipt model.DemandServiceGalleryReceipt
		if db.Where("actor_id = ? AND client_action_id = ?", a.UserID, in.ClientActionID).First(&receipt).Error == nil {
			if receipt.ProfileID != profileID || receipt.PayloadHash != hash {
				return DemandServiceGalleryView{}, false, demandError(409, "IDEMPOTENCY_CONFLICT", "The request key was already used with different content")
			}
			replayed, err = true, nil
		}
	}
	if err != nil {
		return DemandServiceGalleryView{}, false, err
	}
	v, err := GetDemandServiceGallery(db, a, profileID)
	return v, replayed, err
}

type demandServiceMediaCapability struct {
	ProfileID    string `json:"p"`
	AttachmentID string `json:"a"`
	UserID       int    `json:"u"`
	AuthVersion  int64  `json:"uv"`
	Admin        bool   `json:"admin"`
	Expires      int64  `json:"exp"`
	Disposition  string `json:"d"`
}

func serviceMediaURL(p, asset, base string, u model.User, a DemandActor, disposition string) (string, int64, error) {
	if common.CryptoSecret == "" {
		return "", 0, demandError(503, "STORAGE_UNAVAILABLE", "Demand attachment storage is unavailable")
	}
	if err := ValidateTaskArtifactBaseURL(base); err != nil {
		return "", 0, err
	}
	expires := time.Now().Unix() + 60
	raw, _ := common.Marshal(demandServiceMediaCapability{p, asset, u.Id, u.AuthVersion, a.Admin, expires, disposition})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("demand-service-gallery-v1\x00" + payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base + "/api/product/demand-service-media/" + url.PathEscape(p) + "/" + url.PathEscape(asset) + "/content?token=" + url.QueryEscape(token), expires, nil
}
func serviceMediaBase() string {
	base := strings.TrimRight(system_setting.TaskPublicAddress, "/")
	if base == "" {
		base = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	return base
}

// One profile read, one gallery read and one asset read cover the whole page.
// Generated URLs never turn another profile's/private upload into public media.
func GetDemandServiceGalleryBatch(db *gorm.DB, a DemandActor, profiles []model.DemandServiceProfile) (map[string]DemandServiceGalleryView, error) {
	out := map[string]DemandServiceGalleryView{}
	if len(profiles) == 0 {
		return out, nil
	}
	var u model.User
	if db.Select("id", "status", "role", "auth_version").First(&u, a.UserID).Error != nil || u.Status != common.UserStatusEnabled {
		return nil, demandServiceMediaMissing()
	}
	profileIDs := make([]string, 0, len(profiles))
	allowed := map[string]bool{}
	for _, p := range profiles {
		if a.Admin || p.Enabled && p.Published {
			profileIDs = append(profileIDs, p.ID)
			allowed[p.ID] = true
			out[p.ID] = DemandServiceGalleryView{ProfileID: p.ID, Media: []DemandServiceMediaView{}}
		}
	}
	var galleries []model.DemandServiceGallery
	if err := db.Where("profile_id IN ?", profileIDs).Find(&galleries).Error; err != nil {
		return nil, err
	}
	ids := []string{}
	for _, g := range galleries {
		all, err := galleryAssetIDs(g)
		if err != nil {
			return nil, err
		}
		ids = append(ids, all...)
	}
	assets := map[string]model.DemandAttachment{}
	if len(ids) > 0 {
		var rows []model.DemandAttachment
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, v := range rows {
			if isServiceGalleryAsset(v) {
				assets[v.ID] = v
			}
		}
	}
	base := serviceMediaBase()
	for _, g := range galleries {
		if !allowed[g.ProfileID] {
			continue
		}
		view := DemandServiceGalleryView{ProfileID: g.ProfileID, Revision: g.Revision, CoverAttachmentID: g.CoverAttachmentID, Media: []DemandServiceMediaView{}}
		entries, err := serviceGalleryEntries(g)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			v, ok := assets[e.AttachmentID]
			if !ok {
				return nil, demandServiceMediaMissing()
			}
			link, _, err := serviceMediaURL(g.ProfileID, v.ID, base, u, a, "inline")
			if err != nil {
				return nil, err
			}
			media := DemandServiceMediaView{ID: v.ID, Kind: v.Kind, MimeType: v.MimeType, SizeBytes: v.SizeBytes, SHA256: v.SHA256, URL: link}
			if e.PosterAttachmentID != "" {
				poster, ok := assets[e.PosterAttachmentID]
				if !ok || poster.Kind != "image" {
					return nil, demandServiceMediaMissing()
				}
				media.PosterURL, _, err = serviceMediaURL(g.ProfileID, poster.ID, base, u, a, "inline")
				if err != nil {
					return nil, err
				}
			}
			view.Media = append(view.Media, media)
		}
		if g.CoverAttachmentID != "" {
			cover, ok := assets[g.CoverAttachmentID]
			if !ok || cover.Kind != "image" {
				return nil, demandServiceMediaMissing()
			}
			view.CoverURL, _, err = serviceMediaURL(g.ProfileID, cover.ID, base, u, a, "inline")
			if err != nil {
				return nil, err
			}
		}
		out[g.ProfileID] = view
	}
	return out, nil
}
func GetDemandServiceGallery(db *gorm.DB, a DemandActor, id string) (DemandServiceGalleryView, error) {
	p, err := demandServiceProfileAccess(db, a, id)
	if err != nil {
		return DemandServiceGalleryView{}, err
	}
	all, err := GetDemandServiceGalleryBatch(db, a, []model.DemandServiceProfile{p})
	return all[id], err
}
func serviceGalleryAssetAccess(db *gorm.DB, a DemandActor, profileID, assetID string) (model.DemandAttachment, error) {
	var empty model.DemandAttachment
	if _, err := demandServiceProfileAccess(db, a, profileID); err != nil {
		return empty, err
	}
	var g model.DemandServiceGallery
	if db.First(&g, "profile_id = ?", profileID).Error != nil {
		return empty, demandServiceMediaMissing()
	}
	ids, err := galleryAssetIDs(g)
	if err != nil || !slices.Contains(ids, assetID) {
		return empty, demandServiceMediaMissing()
	}
	var v model.DemandAttachment
	if db.First(&v, "id = ?", assetID).Error != nil || !isServiceGalleryAsset(v) {
		return empty, demandServiceMediaMissing()
	}
	return v, nil
}
func SignDemandServiceMediaLink(db *gorm.DB, a DemandActor, profileID, assetID, disposition string) (DemandAttachmentLink, error) {
	if !slices.Contains([]string{"inline", "attachment"}, disposition) {
		return DemandAttachmentLink{}, demandError(400, "UPLOAD_INVALID", "Invalid attachment disposition")
	}
	if _, err := serviceGalleryAssetAccess(db, a, profileID, assetID); err != nil {
		return DemandAttachmentLink{}, err
	}
	var u model.User
	if db.Select("id", "status", "role", "auth_version").First(&u, a.UserID).Error != nil || u.Status != common.UserStatusEnabled {
		return DemandAttachmentLink{}, demandServiceMediaMissing()
	}
	address, expires, err := serviceMediaURL(profileID, assetID, serviceMediaBase(), u, a, disposition)
	return DemandAttachmentLink{URL: address, ExpiresAt: expires}, err
}
func OpenDemandServiceMedia(db *gorm.DB, profileID, assetID, token string) (model.DemandAttachment, *os.File, string, error) {
	var empty model.DemandAttachment
	fail := demandServiceMediaMissing()
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 2048 || common.CryptoSecret == "" {
		return empty, nil, "", fail
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return empty, nil, "", fail
	}
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("demand-service-gallery-v1\x00" + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return empty, nil, "", fail
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return empty, nil, "", fail
	}
	var c demandServiceMediaCapability
	if common.Unmarshal(raw, &c) != nil || c.ProfileID != profileID || c.AttachmentID != assetID || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Unix()+60 || !slices.Contains([]string{"inline", "attachment"}, c.Disposition) {
		return empty, nil, "", fail
	}
	var u model.User
	if db.Select("id", "status", "role", "auth_version").First(&u, c.UserID).Error != nil || u.Status != common.UserStatusEnabled || u.AuthVersion != c.AuthVersion || c.Admin && !authz.Can(u.Id, u.Role, authz.DemandsRead) {
		return empty, nil, "", fail
	}
	v, err := serviceGalleryAssetAccess(db, DemandActor{UserID: c.UserID, Admin: c.Admin}, profileID, assetID)
	if err != nil {
		return empty, nil, "", fail
	}
	path, err := demandStoragePath(v.ObjectKey)
	if err != nil {
		return empty, nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return empty, nil, "", fail
	}
	extension := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov"}[v.MimeType]
	disposition := mime.FormatMediaType(c.Disposition, map[string]string{"filename": "service-media." + extension})
	return v, f, disposition, nil
}
