package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ForumUploadCreate struct {
	ClientUploadID string `json:"client_upload_id"`
	TargetKind     string `json:"target_kind"`
	PostID         string `json:"post_id"`
	Name           string `json:"name"`
	MimeType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
}
type ForumUploadResult struct {
	Attachment model.ForumAttachment `json:"-"`
	Offset     int64                 `json:"offset"`
	ChunkBytes int64                 `json:"chunk_bytes"`
	ExpiresAt  int64                 `json:"expires_at"`
}

func forumStoragePath(key string) (string, error) {
	root := os.Getenv("FORUM_STORAGE_DIR")
	if root == "" {
		base, err := demandStoragePath("")
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "forum-images")
	}
	if key != "" && filepath.Base(key) != key {
		return "", forumError(503, "STORAGE_UNAVAILABLE", "Forum storage is unavailable")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return filepath.Join(root, key), nil
}
func ForumAttachmentAccess(db *gorm.DB, a ForumActor, id string) (model.ForumAttachment, error) {
	var v model.ForumAttachment
	if err := db.First(&v, "id = ?", id).Error; err != nil {
		return v, ForumMissing()
	}
	if !v.Bound {
		if v.OwnerID == a.UserID && v.ExpiresAt > time.Now().Unix() {
			return v, nil
		}
		return v, ForumMissing()
	}
	if _, err := ForumPostAccess(db, a, v.PostID); err != nil {
		return v, err
	}
	if v.TargetKind == "comment" {
		var c model.ForumComment
		q := db.Where("id = ? AND post_id = ?", v.CommentID, v.PostID)
		if !a.Admin {
			q = q.Where("deleted_at = 0")
		}
		if q.First(&c).Error != nil {
			return v, ForumMissing()
		}
	}
	return v, nil
}
func forumUploadWritable(db *gorm.DB, a ForumActor, v model.ForumAttachment) error {
	if v.Bound {
		return &ForumError{409, "FORUM_ATTACHMENT_INVALID", "Forum image is already published"}
	}
	if err := forumAccepting(db); err != nil {
		return err
	}
	if v.TargetKind == "comment" {
		_, err := ForumPostAccess(db, ForumActor{UserID: a.UserID}, v.PostID)
		return err
	}
	return nil
}
func CreateForumUpload(db *gorm.DB, a ForumActor, in ForumUploadCreate) (ForumUploadResult, error) {
	var out ForumUploadResult
	r := ForumUploadRules()
	var err error
	if (in.ClientUploadID == "" || utf8.RuneCountInString(in.ClientUploadID) > 80) || in.Name == "" || utf8.RuneCountInString(in.Name) > 255 || strings.ContainsAny(in.Name, "\r\n\x00/\\") || in.SizeBytes < 1 {
		return out, forumError(400, "INVALID_INPUT", "Invalid forum image")
	}
	hash := forumPayloadHash(in)
	var v model.ForumAttachment
	err = db.Where("owner_id = ? AND client_upload_id = ?", a.UserID, in.ClientUploadID).First(&v).Error
	if err == nil {
		if v.MetadataHash != hash {
			return out, forumError(409, "IDEMPOTENCY_CONFLICT", "The upload key was already used for a different file")
		}
		return ForumUploadResult{v, v.UploadedBytes, r.ChunkBytes, v.ExpiresAt}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if !slices.Contains(r.AllowedMimeTypes, in.MimeType) {
		return out, forumError(415, "UPLOAD_UNSUPPORTED", "Unsupported forum image type")
	}
	kind := "image"
	if in.SizeBytes > r.MaxImageBytes {
		return out, forumError(413, "UPLOAD_TOO_LARGE", "Forum image exceeds the upload limit")
	}
	if in.TargetKind != "post" && in.TargetKind != "comment" || in.TargetKind == "post" && in.PostID != "" || in.TargetKind == "comment" && in.PostID == "" {
		return out, forumInvalid()
	}
	if err = forumUploadWritable(db, a, model.ForumAttachment{TargetKind: in.TargetKind, PostID: in.PostID}); err != nil {
		return out, err
	}

	if _, err = forumStoragePath(""); err != nil {
		return out, err
	}
	v = model.ForumAttachment{ID: uuid.NewString(), OwnerID: a.UserID, ClientUploadID: in.ClientUploadID, MetadataHash: hash, TargetKind: in.TargetKind, PostID: in.PostID, Name: in.Name, Kind: kind, MimeType: in.MimeType, SizeBytes: in.SizeBytes, Status: "uploading", ExpiresAt: time.Now().Unix() + r.UploadTTLSeconds}
	if err = db.Create(&v).Error; err != nil {
		var existing model.ForumAttachment
		if db.Where("owner_id = ? AND client_upload_id = ?", a.UserID, in.ClientUploadID).First(&existing).Error == nil && existing.MetadataHash == hash {
			return ForumUploadResult{existing, existing.UploadedBytes, r.ChunkBytes, existing.ExpiresAt}, nil
		}
		return out, err
	}
	return ForumUploadResult{v, 0, r.ChunkBytes, v.ExpiresAt}, nil
}
func GetForumUpload(db *gorm.DB, a ForumActor, id string) (ForumUploadResult, error) {
	v, err := ForumAttachmentAccess(db, a, id)
	if err != nil {
		return ForumUploadResult{}, err
	}
	if v.OwnerID != a.UserID {
		return ForumUploadResult{}, forumError(404, "NOT_FOUND", "Forum image not found")
	}
	return ForumUploadResult{v, v.UploadedBytes, ForumUploadRules().ChunkBytes, v.ExpiresAt}, nil
}

// Each chunk is an immutable private file. A failed/concurrent database commit
// removes only that attempt's file; it cannot append twice or corrupt another writer.
func PutForumUploadChunk(db *gorm.DB, a ForumActor, id string, offset int64, body io.Reader) (ForumUploadResult, error) {
	out, err := GetForumUpload(db, a, id)
	if err != nil {
		return out, err
	}
	v := out.Attachment
	if err = forumUploadWritable(db, a, v); err != nil {
		return out, err
	}
	if v.Status != "uploading" || v.ExpiresAt <= time.Now().Unix() || offset < 0 || offset != v.UploadedBytes {
		return out, forumError(409, "UPLOAD_OFFSET_CONFLICT", "Upload progress changed; read its current offset")
	}
	key := uuid.NewString() + ".part"
	path, err := forumStoragePath(key)
	if err != nil {
		return out, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return out, forumError(503, "STORAGE_UNAVAILABLE", "Forum image storage is unavailable")
	}
	keep := false
	defer func() {
		_ = f.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	limit := min(out.ChunkBytes, v.SizeBytes-offset)
	n, err := io.Copy(f, io.LimitReader(body, limit+1))
	if err != nil {
		return out, forumError(400, "INVALID_INPUT", "Forum image transfer did not complete")
	}
	if n < 1 || n > limit {
		return out, forumError(413, "UPLOAD_TOO_LARGE", "Forum image chunk exceeds the upload limit")
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	if err = f.Close(); err != nil {
		return out, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.ForumAttachment{}).Where("id = ? AND owner_id = ? AND status = ? AND uploaded_bytes = ? AND expires_at > ?", id, a.UserID, "uploading", offset, time.Now().Unix()).UpdateColumn("uploaded_bytes", offset+n)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return forumError(409, "UPLOAD_OFFSET_CONFLICT", "Upload progress changed; read its current offset")
		}
		return tx.Create(&model.ForumUploadPart{ID: uuid.NewString(), UploadID: id, Offset: offset, Size: n, ObjectKey: key}).Error
	})
	if err != nil {
		return out, err
	}
	keep = true
	return GetForumUpload(db, a, id)
}
func CompleteForumUpload(db *gorm.DB, a ForumActor, id, expectedSHA string) (ForumUploadResult, error) {
	out, err := GetForumUpload(db, a, id)
	if err != nil {
		return out, err
	}
	v := out.Attachment
	if expectedSHA == "" {
		return out, forumInvalid()
	}
	if expectedSHA != "" {
		decoded, e := hex.DecodeString(expectedSHA)
		if e != nil || len(decoded) != 32 || expectedSHA != strings.ToLower(expectedSHA) {
			return out, forumError(400, "INVALID_INPUT", "Invalid attachment checksum")
		}
	}
	if v.Status == "ready" {
		if expectedSHA != "" && expectedSHA != v.SHA256 {
			return out, forumError(409, "IDEMPOTENCY_CONFLICT", "Attachment checksum does not match")
		}
		return out, nil
	}
	if err = forumUploadWritable(db, a, v); err != nil {
		return out, err
	}
	if v.UploadedBytes != v.SizeBytes || v.ExpiresAt <= time.Now().Unix() {
		return out, forumError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment upload is not complete")
	}
	nonce := uuid.NewString()
	now := time.Now().Unix()
	res := db.Model(&model.ForumAttachment{}).Where("id = ? AND owner_id = ? AND (status = ? OR (status = ? AND assembly_until < ?))", id, a.UserID, "uploading", "assembling", now).Updates(map[string]any{"status": "assembling", "assembly_token": nonce, "assembly_until": now + 1800})
	if res.Error != nil {
		return out, res.Error
	}
	if res.RowsAffected != 1 {
		return out, forumError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment verification is already in progress")
	}
	success := false
	defer func() {
		if !success {
			db.Model(&model.ForumAttachment{}).Where("id = ? AND assembly_token = ?", id, nonce).Updates(map[string]any{"status": "uploading", "assembly_token": "", "assembly_until": 0})
		}
	}()
	var parts []model.ForumUploadPart
	if err = db.Where("upload_id = ?", id).Order(clause.OrderByColumn{Column: clause.Column{Name: "offset"}}).Find(&parts).Error; err != nil {
		return out, err
	}
	key := uuid.NewString() + ".media"
	path, err := forumStoragePath(key)
	if err != nil {
		return out, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return out, err
	}
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	var total int64
	for _, part := range parts {
		if part.Offset != total {
			return out, forumError(400, "INVALID_INPUT", "Attachment upload has missing chunks")
		}
		partPath, e := forumStoragePath(part.ObjectKey)
		if e != nil {
			return out, e
		}
		src, e := os.Open(partPath)
		if e != nil {
			return out, forumError(503, "STORAGE_UNAVAILABLE", "Forum image storage is unavailable")
		}
		n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(src, part.Size+1))
		_ = src.Close()
		if e != nil || n != part.Size {
			return out, forumError(400, "INVALID_INPUT", "Attachment chunk verification failed")
		}
		total += n
	}
	if total != v.SizeBytes {
		return out, forumError(400, "INVALID_INPUT", "Attachment size verification failed")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA != "" && actual != expectedSHA {
		return out, forumError(400, "INVALID_INPUT", "Attachment checksum verification failed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	detected := http.DetectContentType(head[:n])
	if detected != v.MimeType {
		return out, forumError(415, "UPLOAD_UNSUPPORTED", "Attachment content does not match its declared format")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return out, forumError(415, "UPLOAD_UNSUPPORTED", "Invalid forum image dimensions or encoding")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	if _, _, err = image.Decode(f); err != nil {
		return out, forumError(415, "UPLOAD_UNSUPPORTED", "Invalid forum image encoding")
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	if err = f.Close(); err != nil {
		return out, err
	}
	res = db.Model(&model.ForumAttachment{}).Where("id = ? AND assembly_token = ? AND status = ?", id, nonce, "assembling").Updates(map[string]any{"status": "ready", "sha256": actual, "object_key": key, "assembly_token": "", "assembly_until": 0})
	if res.Error != nil {
		return out, res.Error
	}
	if res.RowsAffected != 1 {
		return out, forumError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment verification progress changed")
	}
	success = true
	// Retained immutable parts allow recovery after process failure. Successful
	// uploads no longer need their parts; remove only these exact owned objects.
	for _, part := range parts {
		if path, e := forumStoragePath(part.ObjectKey); e == nil {
			_ = os.Remove(path)
		}
	}
	_ = db.Where("upload_id = ?", id).Delete(&model.ForumUploadPart{}).Error
	return GetForumUpload(db, a, id)
}

type forumAttachmentCapability struct {
	ID          string `json:"id"`
	UserID      int    `json:"user_id"`
	AuthVersion int64  `json:"uv"`
	Admin       bool   `json:"admin"`
	Expires     int64  `json:"exp"`
	Disposition string `json:"disposition"`
}
type ForumAttachmentLink struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

func SignForumAttachmentLink(db *gorm.DB, a ForumActor, id, disposition string) (ForumAttachmentLink, error) {
	var out ForumAttachmentLink
	v, err := ForumAttachmentAccess(db, a, id)
	if err != nil {
		return out, err
	}
	if v.Status != "ready" || !slices.Contains([]string{"inline", "attachment"}, disposition) || common.CryptoSecret == "" {
		return out, forumError(400, "INVALID_INPUT", "Attachment is not available")
	}
	var u model.User
	if err = db.Select("id", "auth_version").First(&u, a.UserID).Error; err != nil {
		return out, err
	}
	expires := time.Now().Unix() + 60
	raw, _ := common.Marshal(forumAttachmentCapability{id, a.UserID, u.AuthVersion, a.Admin, expires, disposition})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("forum-attachment-v1\x00" + payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	base := strings.TrimRight(system_setting.TaskPublicAddress, "/")
	if base == "" {
		base = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	if err = ValidateTaskArtifactBaseURL(base); err != nil {
		return out, forumError(503, "STORAGE_UNAVAILABLE", "Forum image address is not configured")
	}
	return ForumAttachmentLink{base + "/api/product/forum/attachments/" + url.PathEscape(id) + "/content?token=" + url.QueryEscape(token), expires}, nil
}
func OpenForumAttachment(db *gorm.DB, id, token string) (model.ForumAttachment, *os.File, string, error) {
	var empty model.ForumAttachment
	fail := forumError(404, "NOT_FOUND", "Forum image not found")
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 2048 || common.CryptoSecret == "" {
		return empty, nil, "", fail
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return empty, nil, "", fail
	}
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("forum-attachment-v1\x00" + parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return empty, nil, "", fail
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return empty, nil, "", fail
	}
	var capability forumAttachmentCapability
	if common.Unmarshal(raw, &capability) != nil || capability.ID != id || capability.Expires <= time.Now().Unix() || capability.Expires > time.Now().Unix()+60 || !slices.Contains([]string{"inline", "attachment"}, capability.Disposition) {
		return empty, nil, "", fail
	}
	var u model.User
	if db.Select("id", "status", "role", "auth_version").First(&u, capability.UserID).Error != nil || u.Status != common.UserStatusEnabled || u.AuthVersion != capability.AuthVersion || capability.Admin && !authz.Can(u.Id, u.Role, authz.ForumRead) {
		return empty, nil, "", fail
	}
	v, err := ForumAttachmentAccess(db, ForumActor{capability.UserID, capability.Admin}, id)
	if err != nil || v.Status != "ready" {
		return empty, nil, "", fail
	}
	path, err := forumStoragePath(v.ObjectKey)
	if err != nil {
		return empty, nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return empty, nil, "", fail
	}
	return v, f, mime.FormatMediaType(capability.Disposition, map[string]string{"filename": ProjectForumAttachment(v).Name}), nil
}

type ForumUploadRuleSet struct {
	MaxAttachments   int      `json:"max_attachments"`
	MaxImageBytes    int64    `json:"max_image_bytes"`
	ChunkBytes       int64    `json:"chunk_bytes"`
	UploadTTLSeconds int64    `json:"upload_ttl_seconds"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
}

func ForumUploadRules() ForumUploadRuleSet {
	return ForumUploadRuleSet{8, 20 << 20, 4 << 20, 86400, []string{"image/png", "image/jpeg", "image/gif", "image/webp"}}
}
func forumError(status int, code, message string) error {
	return &ForumError{status, "FORUM_" + code, message}
}

type ForumAttachmentView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	PreviewPath  string `json:"preview_path"`
	DownloadPath string `json:"download_path"`
}

func ProjectForumAttachment(v model.ForumAttachment) ForumAttachmentView {
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[v.MimeType]
	path := "/attachments/" + v.ID + "/link"
	return ForumAttachmentView{v.ID, "forum-image." + ext, "image", v.MimeType, v.SizeBytes, v.SHA256, v.Status, path, path}
}
func forumBoundAttachments(db *gorm.DB, kind, post, comment string) ([]ForumAttachmentView, error) {
	out := []ForumAttachmentView{}
	var rows []model.ForumAttachment
	if err := db.Where("bound = ? AND target_kind = ? AND post_id = ? AND comment_id = ?", true, kind, post, comment).Order("position, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, v := range rows {
		out = append(out, ProjectForumAttachment(v))
	}
	return out, nil
}
func bindForumAttachments(tx *gorm.DB, a ForumActor, ids []string, kind, post, comment string) error {
	if len(ids) > ForumUploadRules().MaxAttachments {
		return forumError(400, "ATTACHMENT_INVALID", "Too many forum images")
	}
	seen := map[string]bool{}
	for position, id := range ids {
		if id == "" || seen[id] {
			return forumError(400, "ATTACHMENT_INVALID", "Invalid forum image")
		}
		seen[id] = true
		q := tx.Model(&model.ForumAttachment{}).Where("id = ? AND owner_id = ? AND status = ? AND bound = ? AND expires_at > ? AND target_kind = ?", id, a.UserID, "ready", false, time.Now().Unix(), kind)
		expectedPost := ""
		if kind == "comment" {
			expectedPost = post
		}
		q = q.Where("post_id = ?", expectedPost)
		res := q.Updates(map[string]any{"post_id": post, "comment_id": comment, "bound": true, "position": position})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return forumError(400, "ATTACHMENT_INVALID", "Forum image is not ready or belongs to another publication")
		}
	}
	return nil
}
