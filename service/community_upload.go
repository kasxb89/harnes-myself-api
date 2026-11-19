package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

type CommunityUploadCreate struct {
	ClientUploadID string `json:"client_upload_id"`
	Scope          string `json:"scope"`
	OrderID        string `json:"order_id"`
	Name           string `json:"name"`
	MimeType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
}
type CommunityUploadResult struct {
	Attachment model.CommunityAttachment `json:"-"`
	Offset     int64                     `json:"offset"`
	ChunkBytes int64                     `json:"chunk_bytes"`
	ExpiresAt  int64                     `json:"expires_at"`
}

func communityStoragePath(key string) (string, error) {
	root := os.Getenv("COMMUNITY_STORAGE_DIR")
	if root == "" {
		base, err := demandStoragePath("")
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "community-orders")
	}
	if key != "" && filepath.Base(key) != key {
		return "", communityError(503, "STORAGE_UNAVAILABLE", "Community storage is unavailable")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return filepath.Join(root, key), nil
}
func CommunityAttachmentAccess(db *gorm.DB, a CommunityActor, id string) (model.CommunityAttachment, error) {
	var v model.CommunityAttachment
	if err := db.First(&v, "id = ?", id).Error; err != nil {
		return v, communityMissing()
	}
	if v.MessageID == "" {
		if v.OwnerID == a.UserID {
			return v, nil
		}
		return v, communityMissing()
	}
	if v.Scope == "listing" {
		_, err := CommunityListingAccess(db, a, v.OrderID)
		return v, err
	}
	if v.Scope != "conversation" {
		return v, communityMissing()
	}
	_, err := CommunityOrderAccess(db, a, v.OrderID)
	return v, err
}
func communityUploadWritable(db *gorm.DB, a CommunityActor, v model.CommunityAttachment) error {
	if v.OrderID == "" {
		return communityEnabled(db)
	}
	o, err := CommunityOrderAccess(db, a, v.OrderID)
	if err != nil {
		return err
	}
	if communityClosed(o.Status) {
		return communityError(409, "READ_ONLY", "Order is read-only")
	}
	if v.Scope == "listing" && (o.PublisherID != a.UserID || o.AcceptorID != 0) {
		return communityError(403, "FORBIDDEN", "Only the publisher can attach listing files before acceptance")
	}
	return nil
}
func CreateCommunityUpload(db *gorm.DB, a CommunityActor, in CommunityUploadCreate) (CommunityUploadResult, error) {
	var out CommunityUploadResult
	cfg, err := LoadDemandConfig(db)
	if err != nil {
		return out, err
	}
	r := cfg.Config.UploadRules
	if !communityKey(in.ClientUploadID) || in.Name == "" || utf8.RuneCountInString(in.Name) > 255 || strings.ContainsAny(in.Name, "\r\n\x00/\\") || in.SizeBytes < 1 {
		return out, communityError(400, "INVALID_INPUT", "Invalid demand attachment")
	}
	hash := demandHash(in)
	var v model.CommunityAttachment
	err = db.Where("owner_id = ? AND client_upload_id = ?", a.UserID, in.ClientUploadID).First(&v).Error
	if err == nil {
		if v.MetadataHash != hash {
			return out, communityError(409, "IDEMPOTENCY_CONFLICT", "The upload key was already used for a different file")
		}
		return CommunityUploadResult{v, v.UploadedBytes, r.ChunkBytes, v.ExpiresAt}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if !slices.Contains(r.AllowedMimeTypes, in.MimeType) {
		return out, communityError(415, "UPLOAD_UNSUPPORTED", "Unsupported demand attachment type")
	}
	kind := "image"
	maxBytes := r.MaxImageBytes
	if strings.HasPrefix(in.MimeType, "video/") {
		kind = "video"
		maxBytes = r.MaxVideoBytes
	}
	if in.SizeBytes > maxBytes {
		return out, communityError(413, "UPLOAD_TOO_LARGE", "Demand attachment exceeds the upload limit")
	}
	if in.Scope != "listing" && in.Scope != "conversation" {
		return out, communityError(400, "INVALID_INPUT", "An upload scope is required")
	}
	if err = communityUploadWritable(db, a, model.CommunityAttachment{OrderID: in.OrderID, Scope: in.Scope}); err != nil {
		return out, err
	}

	if _, err = communityStoragePath(""); err != nil {
		return out, err
	}
	v = model.CommunityAttachment{ID: uuid.NewString(), OwnerID: a.UserID, ClientUploadID: in.ClientUploadID, MetadataHash: hash, Scope: in.Scope, OrderID: in.OrderID, Name: in.Name, Kind: kind, MimeType: in.MimeType, SizeBytes: in.SizeBytes, Status: "uploading", ExpiresAt: time.Now().Unix() + r.UploadTTLSeconds}
	if err = db.Create(&v).Error; err != nil {
		var existing model.CommunityAttachment
		if db.Where("owner_id = ? AND client_upload_id = ?", a.UserID, in.ClientUploadID).First(&existing).Error == nil && existing.MetadataHash == hash {
			return CommunityUploadResult{existing, existing.UploadedBytes, r.ChunkBytes, existing.ExpiresAt}, nil
		}
		return out, err
	}
	return CommunityUploadResult{v, 0, r.ChunkBytes, v.ExpiresAt}, nil
}
func GetCommunityUpload(db *gorm.DB, a CommunityActor, id string) (CommunityUploadResult, error) {
	v, err := CommunityAttachmentAccess(db, a, id)
	if err != nil {
		return CommunityUploadResult{}, err
	}
	if v.OwnerID != a.UserID {
		return CommunityUploadResult{}, communityError(404, "NOT_FOUND", "Demand attachment not found")
	}
	cfg, err := LoadDemandConfig(db)
	return CommunityUploadResult{v, v.UploadedBytes, cfg.Config.UploadRules.ChunkBytes, v.ExpiresAt}, err
}

// Each chunk is an immutable private file. A failed/concurrent database commit
// removes only that attempt's file; it cannot append twice or corrupt another writer.
func PutCommunityUploadChunk(db *gorm.DB, a CommunityActor, id string, offset int64, body io.Reader) (CommunityUploadResult, error) {
	out, err := GetCommunityUpload(db, a, id)
	if err != nil {
		return out, err
	}
	v := out.Attachment
	if err = communityUploadWritable(db, a, v); err != nil {
		return out, err
	}
	if v.Status != "uploading" || v.ExpiresAt <= time.Now().Unix() || offset < 0 || offset != v.UploadedBytes {
		return out, communityError(409, "UPLOAD_OFFSET_CONFLICT", "Upload progress changed; read its current offset")
	}
	key := uuid.NewString() + ".part"
	path, err := communityStoragePath(key)
	if err != nil {
		return out, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return out, communityError(503, "STORAGE_UNAVAILABLE", "Demand attachment storage is unavailable")
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
		return out, communityError(400, "INVALID_INPUT", "Demand attachment transfer did not complete")
	}
	if n < 1 || n > limit {
		return out, communityError(413, "UPLOAD_TOO_LARGE", "Demand attachment chunk exceeds the upload limit")
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	if err = f.Close(); err != nil {
		return out, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.CommunityAttachment{}).Where("id = ? AND owner_id = ? AND status = ? AND uploaded_bytes = ? AND expires_at > ?", id, a.UserID, "uploading", offset, time.Now().Unix()).UpdateColumn("uploaded_bytes", offset+n)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return communityError(409, "UPLOAD_OFFSET_CONFLICT", "Upload progress changed; read its current offset")
		}
		return tx.Create(&model.CommunityUploadPart{ID: uuid.NewString(), UploadID: id, Offset: offset, Size: n, ObjectKey: key}).Error
	})
	if err != nil {
		return out, err
	}
	keep = true
	return GetCommunityUpload(db, a, id)
}
func CompleteCommunityUpload(db *gorm.DB, a CommunityActor, id, expectedSHA string) (CommunityUploadResult, error) {
	out, err := GetCommunityUpload(db, a, id)
	if err != nil {
		return out, err
	}
	v := out.Attachment
	if expectedSHA != "" {
		decoded, e := hex.DecodeString(expectedSHA)
		if e != nil || len(decoded) != 32 || expectedSHA != strings.ToLower(expectedSHA) {
			return out, communityError(400, "INVALID_INPUT", "Invalid attachment checksum")
		}
	}
	if v.Status == "ready" {
		if expectedSHA != "" && expectedSHA != v.SHA256 {
			return out, communityError(409, "IDEMPOTENCY_CONFLICT", "Attachment checksum does not match")
		}
		return out, nil
	}
	if err = communityUploadWritable(db, a, v); err != nil {
		return out, err
	}
	if v.UploadedBytes != v.SizeBytes || v.ExpiresAt <= time.Now().Unix() {
		return out, communityError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment upload is not complete")
	}
	nonce := uuid.NewString()
	now := time.Now().Unix()
	res := db.Model(&model.CommunityAttachment{}).Where("id = ? AND owner_id = ? AND (status = ? OR (status = ? AND assembly_until < ?))", id, a.UserID, "uploading", "assembling", now).Updates(map[string]any{"status": "assembling", "assembly_token": nonce, "assembly_until": now + 1800})
	if res.Error != nil {
		return out, res.Error
	}
	if res.RowsAffected != 1 {
		return out, communityError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment verification is already in progress")
	}
	success := false
	defer func() {
		if !success {
			db.Model(&model.CommunityAttachment{}).Where("id = ? AND assembly_token = ?", id, nonce).Updates(map[string]any{"status": "uploading", "assembly_token": "", "assembly_until": 0})
		}
	}()
	var parts []model.CommunityUploadPart
	if err = db.Where("upload_id = ?", id).Order(clause.OrderByColumn{Column: clause.Column{Name: "offset"}}).Find(&parts).Error; err != nil {
		return out, err
	}
	key := uuid.NewString() + ".media"
	path, err := communityStoragePath(key)
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
			return out, communityError(400, "INVALID_INPUT", "Attachment upload has missing chunks")
		}
		partPath, e := communityStoragePath(part.ObjectKey)
		if e != nil {
			return out, e
		}
		src, e := os.Open(partPath)
		if e != nil {
			return out, communityError(503, "STORAGE_UNAVAILABLE", "Demand attachment storage is unavailable")
		}
		n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(src, part.Size+1))
		_ = src.Close()
		if e != nil || n != part.Size {
			return out, communityError(400, "INVALID_INPUT", "Attachment chunk verification failed")
		}
		total += n
	}
	if total != v.SizeBytes {
		return out, communityError(400, "INVALID_INPUT", "Attachment size verification failed")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA != "" && actual != expectedSHA {
		return out, communityError(400, "INVALID_INPUT", "Attachment checksum verification failed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	detected := http.DetectContentType(head[:n])
	if detected != v.MimeType {
		return out, communityError(415, "UPLOAD_UNSUPPORTED", "Attachment content does not match its declared format")
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	if err = f.Close(); err != nil {
		return out, err
	}
	res = db.Model(&model.CommunityAttachment{}).Where("id = ? AND assembly_token = ? AND status = ?", id, nonce, "assembling").Updates(map[string]any{"status": "ready", "sha256": actual, "object_key": key, "assembly_token": "", "assembly_until": 0})
	if res.Error != nil {
		return out, res.Error
	}
	if res.RowsAffected != 1 {
		return out, communityError(409, "UPLOAD_OFFSET_CONFLICT", "Attachment verification progress changed")
	}
	success = true
	// Retained immutable parts allow recovery after process failure. Successful
	// uploads no longer need their parts; remove only these exact owned objects.
	for _, part := range parts {
		if path, e := communityStoragePath(part.ObjectKey); e == nil {
			_ = os.Remove(path)
		}
	}
	_ = db.Where("upload_id = ?", id).Delete(&model.CommunityUploadPart{}).Error
	return GetCommunityUpload(db, a, id)
}

type communityAttachmentCapability struct {
	ID          string `json:"id"`
	UserID      int    `json:"user_id"`
	AuthVersion int64  `json:"uv"`
	Admin       bool   `json:"admin"`
	Expires     int64  `json:"exp"`
	Disposition string `json:"disposition"`
}
type CommunityAttachmentLink struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

func SignCommunityAttachmentLink(db *gorm.DB, a CommunityActor, id, disposition string) (CommunityAttachmentLink, error) {
	var out CommunityAttachmentLink
	v, err := CommunityAttachmentAccess(db, a, id)
	if err != nil {
		return out, err
	}
	if v.Status != "ready" || !slices.Contains([]string{"inline", "attachment"}, disposition) || common.CryptoSecret == "" {
		return out, communityError(400, "INVALID_INPUT", "Attachment is not available")
	}
	var u model.User
	if err = db.Select("id", "auth_version").First(&u, a.UserID).Error; err != nil {
		return out, err
	}
	expires := time.Now().Unix() + 60
	raw, _ := common.Marshal(communityAttachmentCapability{id, a.UserID, u.AuthVersion, a.Admin, expires, disposition})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("community-attachment-v1\x00" + payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	base := strings.TrimRight(system_setting.TaskPublicAddress, "/")
	if base == "" {
		base = strings.TrimRight(system_setting.ServerAddress, "/")
	}
	if err = ValidateTaskArtifactBaseURL(base); err != nil {
		return out, communityError(503, "STORAGE_UNAVAILABLE", "Demand attachment address is not configured")
	}
	return CommunityAttachmentLink{base + "/api/product/community-demands/attachments/" + url.PathEscape(id) + "/content?token=" + url.QueryEscape(token), expires}, nil
}
func OpenCommunityAttachment(db *gorm.DB, id, token string) (model.CommunityAttachment, *os.File, string, error) {
	var empty model.CommunityAttachment
	fail := communityError(404, "NOT_FOUND", "Demand attachment not found")
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 2048 || common.CryptoSecret == "" {
		return empty, nil, "", fail
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return empty, nil, "", fail
	}
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write([]byte("community-attachment-v1\x00" + parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return empty, nil, "", fail
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return empty, nil, "", fail
	}
	var capability communityAttachmentCapability
	if common.Unmarshal(raw, &capability) != nil || capability.ID != id || capability.Expires <= time.Now().Unix() || capability.Expires > time.Now().Unix()+60 || !slices.Contains([]string{"inline", "attachment"}, capability.Disposition) {
		return empty, nil, "", fail
	}
	var u model.User
	if db.Select("id", "status", "role", "auth_version").First(&u, capability.UserID).Error != nil || u.Status != common.UserStatusEnabled || u.AuthVersion != capability.AuthVersion || capability.Admin && !authz.Can(u.Id, u.Role, authz.CommunityDemandsRead) {
		return empty, nil, "", fail
	}
	v, err := CommunityAttachmentAccess(db, CommunityActor{capability.UserID, capability.Admin}, id)
	if err != nil || v.Status != "ready" {
		return empty, nil, "", fail
	}
	path, err := communityStoragePath(v.ObjectKey)
	if err != nil {
		return empty, nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return empty, nil, "", fail
	}
	return v, f, mime.FormatMediaType(capability.Disposition, map[string]string{"filename": v.Name}), nil
}
