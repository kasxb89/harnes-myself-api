package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/mediaprobe"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type MediaGenerationActor struct {
	UserID, TokenID int
	OwnerDigest     string
}

var mediaAssetProcessingSlots = make(chan struct{}, 2)

const DefaultMediaGenerationMaxUploadBytes int64 = 50 << 20

var ErrMediaGenerationUploadConfig = errors.New("media upload configuration unavailable")

// MediaGenerationMaxUploadBytes is the deployed single-file limit. Missing
// configuration retains 50 MiB; explicit values must be positive and at most
// the currently supported 1 GiB. Invalid configuration blocks new uploads.
func MediaGenerationMaxUploadBytes() (int64, error) {
	raw, present := os.LookupEnv("MEDIA_GENERATION_MAX_UPLOAD_BYTES")
	if !present {
		return DefaultMediaGenerationMaxUploadBytes, nil
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || limit > model.MaxMediaGenerationAssetBytes {
		return 0, ErrMediaGenerationUploadConfig
	}
	return limit, nil
}

// An upload has no role field. It must fit at least one native role; binding
// subsequently enforces the exact role's smaller byte and format limits.
func mediaGenerationAssetLimit(modelID, kind, mime string) (int64, error) {
	limit, err := MediaGenerationMaxUploadBytes()
	if err != nil {
		return 0, err
	}
	contract, ok := ycapi.LookupMediaContract(modelID)
	if !ok {
		return 0, errors.New("model_not_available")
	}
	fields, err := ycapi.AssetFields(contract)
	if err != nil {
		return 0, err
	}
	var maximum int64
	for _, field := range fields {
		if kind != "" && field.MediaKind != kind || mime != "" && len(field.MIMETypes) > 0 && !slices.Contains(field.MIMETypes, mime) {
			continue
		}
		if field.Maximum != nil && *field.Maximum == 0 {
			continue
		}
		allowed := limit
		if native := contract.Parameters[field.Name].MaxBytes; native != nil {
			allowed = min(allowed, *native)
		}
		maximum = max(maximum, allowed)
	}
	if maximum <= 0 {
		return 0, mediaprobe.ErrUnsupported
	}
	return maximum, nil
}

func beginMediaAssetProcessing(ctx context.Context) (context.Context, func(), error) {
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	select {
	case mediaAssetProcessingSlots <- struct{}{}:
		if err := bounded.Err(); err != nil {
			<-mediaAssetProcessingSlots
			cancel()
			return nil, nil, err
		}
		return bounded, func() { <-mediaAssetProcessingSlots; cancel() }, nil
	case <-bounded.Done():
		err := bounded.Err()
		cancel()
		return nil, nil, err
	}
}

type mediaAssetContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r mediaAssetContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	return n, err
}

func MediaGenerationActorFromContext(c *gin.Context) (MediaGenerationActor, error) {
	a := MediaGenerationActor{UserID: c.GetInt("id"), TokenID: c.GetInt("token_id"), OwnerDigest: common.GenerateHMAC("media-generation-owner:" + c.GetString("token_key"))}
	if a.UserID <= 0 || a.TokenID <= 0 || c.GetString("token_key") == "" {
		return a, errors.New("media authentication unavailable")
	}
	return a, nil
}

func mediaGenerationAssetPath(key string) (string, error) {
	root := os.Getenv("MEDIA_ASSET_STORAGE_DIR")
	if root == "" {
		if root = os.Getenv("DEMAND_STORAGE_DIR"); root != "" {
			root = filepath.Join(filepath.Dir(root), "media-private")
		} else if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			db, _, _ := strings.Cut(common.SQLitePath, "?")
			db = strings.TrimPrefix(db, "file:")
			if db != "" && db != ":memory:" {
				root = filepath.Join(filepath.Dir(db), "media-private")
			}
		}
	}
	if root == "" || key != "" && filepath.Base(key) != key {
		return "", errors.New("media storage unavailable")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return filepath.Join(root, key), nil
}

// StoreMediaGenerationAsset performs no provider call or generation. The ready
// receipt is published only after the exact private file is synced and probed.
func StoreMediaGenerationAsset(ctx context.Context, db *gorm.DB, a MediaGenerationActor, modelID, clientID string, source io.Reader) (*model.MediaGenerationAsset, error) {
	limit, err := mediaGenerationAssetLimit(modelID, "", "")
	if err != nil {
		return nil, err
	}
	if _, err := model.MediaGenerationAssetOperationKey(a.UserID, a.TokenID, a.OwnerDigest, modelID, clientID); err != nil {
		return nil, err
	}
	if source == nil || db == nil {
		return nil, mediaprobe.ErrUnsupported
	}
	ctx, release, err := beginMediaAssetProcessing(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if closer, ok := source.(io.Closer); ok {
		stopClose := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stopClose()
	}
	root, err := mediaGenerationAssetPath("")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(root, ".incoming-*")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	defer os.Remove(path)
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(mediaAssetContextReader{ctx, source}, limit+1))
	if err != nil {
		return nil, err
	}
	if size == 0 || size > limit {
		return nil, mediaprobe.ErrUnsupported
	}
	if err = file.Sync(); err != nil {
		return nil, err
	}
	meta, err := mediaprobe.InspectFile(ctx, file, limit)
	if err != nil {
		return nil, err
	}
	limit, err = mediaGenerationAssetLimit(modelID, meta.Kind, meta.MIMEType)
	if err != nil || size > limit {
		return nil, mediaprobe.ErrUnsupported
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	key := "asset-" + uuid.NewString()
	target, err := mediaGenerationAssetPath(key)
	if err != nil {
		return nil, err
	}
	if err = os.Rename(path, target); err != nil {
		return nil, err
	}
	v := model.MediaGenerationAsset{ClientAssetID: clientID, UserID: a.UserID, TokenID: a.TokenID, OwnerDigest: a.OwnerDigest, Model: modelID, State: "ready", Kind: meta.Kind, MIMEType: meta.MIMEType, Bytes: size, SHA256: digest, Width: meta.Width, Height: meta.Height, DurationSeconds: meta.DurationSeconds, FPS: meta.FPS, Format: meta.Format, ObjectKey: key}
	actual, err := model.InsertMediaGenerationAsset(db.WithContext(ctx), v)
	// A lost database acknowledgement may follow a committed insert. Keep its
	// bytes unless a returned receipt proves this is a different duplicate file.
	if actual != nil && actual.ObjectKey != key {
		os.Remove(target)
	}
	return actual, err
}

func FindMediaGenerationAsset(ctx context.Context, db *gorm.DB, a MediaGenerationActor, modelID, clientID string) (*model.MediaGenerationAsset, error) {
	key, err := model.MediaGenerationAssetOperationKey(a.UserID, a.TokenID, a.OwnerDigest, modelID, clientID)
	if err != nil {
		return nil, err
	}
	var v model.MediaGenerationAsset
	err = db.WithContext(ctx).Where("operation_key = ?", key).Take(&v).Error
	if err != nil {
		return nil, err
	}
	if v.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("asset_expired")
	}
	return &v, nil
}

func ReadMediaGenerationAssets(ctx context.Context, db *gorm.DB, a MediaGenerationActor, modelID string, bindings map[string][]string) (map[string][]model.MediaGenerationAsset, error) {
	if len(bindings) > 0 {
		if _, err := MediaGenerationMaxUploadBytes(); err != nil {
			return nil, err
		}
	}
	ctx, release, err := beginMediaAssetProcessing(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	fields, ok := ycapi.LookupMediaContract(modelID)
	if !ok {
		return nil, errors.New("model_not_available")
	}
	declarations, err := ycapi.AssetFields(fields)
	if err != nil {
		return nil, err
	}
	byName := map[string]ycapi.MediaAssetField{}
	for _, f := range declarations {
		byName[f.Name] = f
	}
	result := map[string][]model.MediaGenerationAsset{}
	total := 0
	for field, ids := range bindings {
		declaration, ok := byName[field]
		if !ok {
			return nil, fmt.Errorf("invalid asset field %s", field)
		}
		if declaration.Maximum != nil && len(ids) > *declaration.Maximum {
			return nil, errors.New("reference_limit_exceeded")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			total++
			if total > 64 || seen[id] {
				return nil, errors.New("invalid asset bindings")
			}
			seen[id] = true
			var v model.MediaGenerationAsset
			if err := db.WithContext(ctx).Where("id = ? AND user_id = ? AND token_id = ? AND owner_digest = ? AND model = ?", id, a.UserID, a.TokenID, a.OwnerDigest, modelID).Take(&v).Error; err != nil {
				return nil, errors.New("asset_not_found")
			}
			if v.State != "ready" || v.ExpiresAt <= time.Now().Unix() || v.Kind != declaration.MediaKind {
				return nil, errors.New("asset_binding_mismatch")
			}
			if native := fields.Parameters[field].MaxBytes; native != nil && v.Bytes > *native ||
				len(declaration.MIMETypes) > 0 && !slices.Contains(declaration.MIMETypes, v.MIMEType) {
				return nil, errors.New("asset_binding_mismatch")
			}
			limit, err := mediaGenerationAssetLimit(modelID, v.Kind, v.MIMEType)
			if err != nil {
				return nil, err
			}
			if v.Bytes > limit {
				return nil, errors.New("asset_binding_mismatch")
			}
			// Verify the stored bytes again before they become priced vendor input.
			path, e := mediaGenerationAssetPath(v.ObjectKey)
			if e != nil {
				return nil, e
			}
			if _, e = verifyMediaGenerationAssetFile(ctx, path, v); e != nil {
				return nil, errors.New("asset_not_ready")
			}
			result[field] = append(result[field], v)
		}
	}
	return result, nil
}

func verifyMediaGenerationAssetFile(ctx context.Context, path string, asset model.MediaGenerationAsset) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || asset.Bytes <= 0 || asset.Bytes > model.MaxMediaGenerationAssetBytes || info.Size() != asset.Bytes {
		return nil, errors.New("media asset bytes are unconfirmed")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("media asset file changed")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(mediaAssetContextReader{ctx, file}, asset.Bytes+1))
	if err != nil {
		return nil, err
	}
	if n != asset.Bytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return nil, errors.New("media asset digest changed")
	}
	return info, nil
}

// CleanupExpiredMediaGenerationAssets unlinks only verified private files owned
// by expired ready receipts. Receipts, SHA and all task/financial rows remain.
// A failed unlink or state update is left for a later bounded cleanup pass.
func CleanupExpiredMediaGenerationAssets(ctx context.Context, db *gorm.DB, limit int) (int, error) {
	if db == nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid media asset cleanup limit or database")
	}
	ctx, release, err := beginMediaAssetProcessing(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	now := time.Now().Unix()
	var assets []model.MediaGenerationAsset
	if err = db.WithContext(ctx).Where("state = ? AND expires_at > 0 AND expires_at <= ?", "ready", now).Order("expires_at ASC, id ASC").Limit(limit).Find(&assets).Error; err != nil {
		return 0, err
	}
	cleaned := 0
	var failures []error
	for _, asset := range assets {
		if err = ctx.Err(); err != nil {
			return cleaned, errors.Join(append(failures, err)...)
		}
		key := strings.TrimPrefix(asset.ObjectKey, "asset-")
		id, parseErr := uuid.Parse(key)
		if parseErr != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || asset.ObjectKey != "asset-"+id.String() {
			failures = append(failures, errors.New("expired media asset has an unconfirmed object key"))
			continue
		}
		path, pathErr := mediaGenerationAssetPath(asset.ObjectKey)
		if pathErr != nil {
			failures = append(failures, pathErr)
			continue
		}
		info, fileErr := verifyMediaGenerationAssetFile(ctx, path, asset)
		if fileErr == nil {
			current, statErr := os.Lstat(path)
			if statErr != nil || !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
				fileErr = errors.New("expired media asset file changed")
			} else {
				fileErr = os.Remove(path)
			}
		}
		if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
			failures = append(failures, fileErr)
			continue
		}
		result := db.WithContext(ctx).Model(&model.MediaGenerationAsset{}).
			Where("id = ? AND object_key = ? AND state = ? AND expires_at = ?", asset.ID, asset.ObjectKey, "ready", asset.ExpiresAt).
			Update("state", "expired")
		if result.Error != nil {
			failures = append(failures, result.Error)
			continue
		}
		cleaned += int(result.RowsAffected)
	}
	return cleaned, errors.Join(failures...)
}
