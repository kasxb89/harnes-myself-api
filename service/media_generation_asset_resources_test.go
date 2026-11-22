package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMediaGenerationConfiguredCatalogKeepsNativeContracts(t *testing.T) {
	imageContract, ok := ycapi.LookupMediaContract("ycapi-doubao-seedream-5-5")
	require.True(t, ok)
	videoContract, ok := ycapi.LookupMediaContract("ycapi-seedance-2-5")
	require.True(t, ok)
	t.Run("nil controls preserve the original contract and image intents exclude frames", func(t *testing.T) {
		native, ok := ycapi.MediaContractSchema(imageContract.ID)
		require.True(t, ok)
		actual, err := mediaGenerationConfiguredSchema(imageContract, nil)
		require.NoError(t, err)
		assert.Equal(t, native, actual)
		assert.NotContains(t, ycapi.MediaContractIntents(imageContract), "frames")
	})
	t.Run("a fixed nonzero count is visible and cannot exceed the native range", func(t *testing.T) {
		var controls map[string]any
		require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer","const":1}}}`, &controls))
		actual, err := mediaGenerationConfiguredSchema(imageContract, controls)
		require.NoError(t, err)
		field := actual["properties"].(map[string]any)["count"].(map[string]any)
		assert.Equal(t, float64(1), field["const"])
		assert.Equal(t, []any{float64(1)}, field["enum"])
		controls["properties"].(map[string]any)["count"].(map[string]any)["const"] = float64(51)
		_, err = mediaGenerationConfiguredSchema(imageContract, controls)
		assert.Error(t, err, "configuration cannot broaden the native 1..50 count")
	})
	t.Run("disabled references disappear from all catalog declarations", func(t *testing.T) {
		for _, raw := range []string{`{"properties":{"reference_images":false}}`, `{"properties":{"reference_images":{"type":"array","items":{"type":"string"},"maxItems":0}}}`} {
			var controls map[string]any
			require.NoError(t, common.UnmarshalJsonStr(raw, &controls))
			actual, err := mediaGenerationConfiguredSchema(imageContract, controls)
			require.NoError(t, err)
			assert.NotContains(t, actual["properties"].(map[string]any), "images")
			assert.Empty(t, actual["x-media-assets"])
			fields, err := mediaGenerationCatalogAssetFields(imageContract, actual)
			require.NoError(t, err)
			assert.Empty(t, fields)
			assert.Empty(t, mediaGenerationCatalogIntents("image", []string{"reference"}, fields), "an empty-intent record must not poison the strict catalog")
		}
	})
	t.Run("reference lower and upper bounds agree in the schema and asset fields", func(t *testing.T) {
		var controls map[string]any
		require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"reference_images":{"type":"array","items":{"type":"string"},"minItems":2,"maxItems":4}},"required":["reference_images"]}`, &controls))
		actual, err := mediaGenerationConfiguredSchema(imageContract, controls)
		require.NoError(t, err)
		assert.Contains(t, actual["required"], "images")
		assert.NotContains(t, actual["required"], "reference_images")
		fields, err := mediaGenerationCatalogAssetFields(imageContract, actual)
		require.NoError(t, err)
		require.Len(t, fields, 1)
		assert.Equal(t, 2, fields[0]["min_items"])
		assert.Equal(t, 4, fields[0]["max_items"])
		assert.Equal(t, []any{map[string]any{"media_kind": "image", "mime_types": []any{}, "min": float64(2), "max": float64(4), "name": "images", "ordered": true, "shape": "multiple"}}, actual["x-media-assets"])
		assert.Len(t, mediaGenerationCatalogIntents("image", []string{"text", "reference", "frames"}, fields), 2)
	})
	t.Run("specification aliases retain native string enum types", func(t *testing.T) {
		var controls map[string]any
		require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"seconds":{"type":"integer","enum":[5,10]},"resolution":{"type":"string","enum":["480p","720p"]}},"oneOf":[{"properties":{"seconds":{"type":"integer","const":5},"resolution":{"type":"string","const":"480p"}}},{"properties":{"seconds":{"type":"integer","const":10},"resolution":{"type":"string","const":"720p"}}}]}`, &controls))
		actual, err := mediaGenerationConfiguredSchema(videoContract, controls)
		require.NoError(t, err)
		branches, ok := actual["oneOf"].([]any)
		require.True(t, ok)
		require.Len(t, branches, 2)
		first := branches[0].(map[string]any)["properties"].(map[string]any)
		assert.NotContains(t, first, "seconds")
		duration := first["duration"].(map[string]any)
		assert.Equal(t, "string", duration["type"])
		assert.Equal(t, "5", duration["const"])
		assert.Equal(t, []any{"5"}, duration["enum"])
		assert.NoError(t, validateMediaGenerationControls(videoContract, controls, map[string]any{"duration": "5", "resolution": "480p"}))
		assert.Error(t, validateMediaGenerationControls(videoContract, controls, map[string]any{"duration": "5", "resolution": "720p"}))
	})
	t.Run("display suffixes describe only public qualified capabilities", func(t *testing.T) {
		for _, tc := range []struct {
			model, name, expected string
			intents, materials    []string
		}{
			{"doubao-seedream-5-5", "Seedream", "Seedream（只文生图）", []string{"text"}, []string{"image"}},
			{"doubao-seedream-5-5", "Seedream", "Seedream（图生图都可）", []string{"text", "reference"}, []string{"image"}},
			{"doubao-seedream-5-5", "Seedream（图生图都可）", "Seedream（图生图都可）", []string{"text", "reference"}, []string{"image"}},
			{"aliyun-image-superres", "图片超分", "图片超分（图片编辑）", []string{"transform"}, []string{"image"}},
			{"doubao-seedream-5-5", "", "图片模型（只文生图）", []string{"text"}, nil},
			{"seedance-2-5", "Seedance", "Seedance（只文生视频）", []string{"text"}, []string{"image", "video", "audio"}},
			{"seedance-2-5", "Seedance", "Seedance（文生／图生视频）", []string{"text", "frames"}, []string{"image"}},
			{"seedance-2-5", "Seedance", "Seedance（图生视频）", []string{"frames"}, []string{"image"}},
			{"seedance-video-edit", "视频编辑", "视频编辑（视频编辑／复刻）", []string{"transform"}, []string{"video"}},
			{"kling-v3-motion-control", "Kling", "Kling（动作复刻）", []string{"transform"}, []string{"image", "video"}},
			{"kling-v3-motion-control", "Kling", "Kling（只文生视频）", []string{"text"}, []string{"image", "video"}},
			{"kling-avatar", "Kling", "Kling（数字人视频）", []string{"transform"}, []string{"image", "audio"}},
			{"kling-lip-sync", "Kling", "Kling（口型同步）", []string{"transform"}, []string{"video", "audio"}},
		} {
			contract, ok := ycapi.LookupMediaContract(tc.model)
			require.True(t, ok)
			var fields, intents []map[string]any
			for _, kind := range tc.materials {
				fields = append(fields, map[string]any{"media_kind": kind})
			}
			for _, intent := range tc.intents {
				intents = append(intents, map[string]any{"id": intent})
			}
			assert.Equal(t, tc.expected, mediaGenerationDisplayName(tc.name, contract, fields, intents), tc.model)
		}
	})
}

func TestMediaGenerationVersionIsSeparateFromContentAndConfiguredDefaults(t *testing.T) {
	request := MediaGenerationRequest{ClientOperationID: "operation-content-version-separation", Model: "ycapi-gpt-image-2.6-flare", ModelVersion: "previous", Intent: ycapi.MediaIntentReference, Parameters: map[string]any{"mode": "image-edit", "prompt": "Preserve every reference"}, Assets: map[string][]string{"images": {"ma2_first", "ma2_second"}}}
	current := request
	current.ModelVersion = "current"
	assert.Equal(t, MediaGenerationRequestDigest(request), MediaGenerationRequestDigest(current))
	assert.Equal(t, "previous", request.ModelVersion, "hashing never rewrites the stored original request")
	current.Assets = map[string][]string{"images": {"ma2_second", "ma2_first"}}
	assert.NotEqual(t, MediaGenerationRequestDigest(request), MediaGenerationRequestDigest(current), "reference order remains part of identity")
	current = request
	current.Intent = ycapi.MediaIntentText
	assert.NotEqual(t, MediaGenerationRequestDigest(request), MediaGenerationRequestDigest(current))
	var controls map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer","minimum":1,"maximum":2,"default":2}}}`, &controls))
	parameters := map[string]any{"mode": "text-to-image", "prompt": "A blue square"}
	result, _, err := prepareMediaNativeParameters(t.Context(), nil, MediaNativeRoute{}, request.Model, parameters, controls, nil, ycapi.MediaIntentText)
	require.NoError(t, err)
	assert.EqualValues(t, 2, result["count"])
	assert.NotContains(t, parameters, "count", "published defaults do not rewrite the caller's original body")
}

func mediaAssetResourceFixture(t *testing.T) (*gorm.DB, MediaGenerationActor, []byte, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MEDIA_ASSET_STORAGE_DIR", root)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "assets.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, model.MigrateMediaGenerationAsset(db))
	var content bytes.Buffer
	require.NoError(t, png.Encode(&content, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	return db, MediaGenerationActor{UserID: 7, TokenID: 11, OwnerDigest: strings.Repeat("a", 64)}, content.Bytes(), root
}

type gatedMediaAssetReader struct {
	started chan struct{}
	release <-chan struct{}
	reader  io.Reader
	once    sync.Once
}

func (r *gatedMediaAssetReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.reader.Read(p)
}

type countedMediaAssetReader struct{ reads atomic.Int32 }

func (r *countedMediaAssetReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
}

func TestMediaGenerationAssetProcessingWaitsBeforeReadingAndHonorsContext(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	results := make(chan error, 2)
	for range 2 {
		reader := &gatedMediaAssetReader{started: make(chan struct{}), release: release, reader: bytes.NewReader(content)}
		go func() {
			_, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), reader)
			results <- err
		}()
		select {
		case <-reader.started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("material reader did not acquire a bounded processing slot")
		}
	}
	queued := &countedMediaAssetReader{}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := StoreMediaGenerationAsset(ctx, db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), queued)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, queued.reads.Load(), "queued uploads cannot first allocate/read the whole material")
	incoming, err := filepath.Glob(filepath.Join(root, ".incoming-*"))
	require.NoError(t, err)
	assert.Len(t, incoming, 2, "a canceled waiter creates no material file")
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	canceled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	_, err = StoreMediaGenerationAsset(canceled, db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), queued)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, queued.reads.Load())
}

func TestMediaGenerationAssetAmbiguousInsertKeepsCommittedBytes(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	lostAck := errors.New("synthetic database acknowledgement lost after commit")
	require.NoError(t, db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("asset_lost_commit_ack", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "MediaGenerationAsset" {
			tx.AddError(lostAck)
		}
	}))
	_, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
	require.ErrorIs(t, err, lostAck)
	require.NoError(t, db.Callback().Create().Remove("asset_lost_commit_ack"))
	var committed model.MediaGenerationAsset
	require.NoError(t, db.First(&committed).Error)
	actual, err := os.ReadFile(filepath.Join(root, committed.ObjectKey))
	require.NoError(t, err, "an uncertain response cannot delete bytes referenced by a committed receipt")
	assert.Equal(t, content, actual)
	clientID := uuid.NewString()
	first, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", clientID, bytes.NewReader(content))
	require.NoError(t, err)
	second, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", clientID, bytes.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	files, err := filepath.Glob(filepath.Join(root, "asset-*"))
	require.NoError(t, err)
	assert.Len(t, files, 2, "a confirmed duplicate discards only its own extra file")
}

func TestMediaGenerationAssetCancellationClosesBlockedReader(t *testing.T) {
	db, actor, content, _ := mediaAssetResourceFixture(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := StoreMediaGenerationAsset(ctx, db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), reader)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, len(mediaAssetProcessingSlots), "cancellation closes a real blocked ReadCloser and releases its slot")
	_, err = StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
	require.NoError(t, err)
}

func TestCleanupExpiredMediaGenerationAssetsKeepsReceiptsAndBoundsWork(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	var originals []model.MediaGenerationAsset
	for i := range 3 {
		asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
		require.NoError(t, err)
		asset.ExpiresAt = time.Now().Unix() - int64(10-i)
		require.NoError(t, db.Model(asset).Update("expires_at", asset.ExpiresAt).Error)
		originals = append(originals, *asset)
	}
	future, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
	require.NoError(t, err)
	for _, limit := range []int{0, 101} {
		n, err := CleanupExpiredMediaGenerationAssets(t.Context(), db, limit)
		require.Error(t, err)
		assert.Zero(t, n)
	}
	n, err := CleanupExpiredMediaGenerationAssets(t.Context(), db, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var expired int64
	require.NoError(t, db.Model(&model.MediaGenerationAsset{}).Where("state = ?", "expired").Count(&expired).Error)
	assert.Equal(t, int64(1), expired)
	n, err = CleanupExpiredMediaGenerationAssets(t.Context(), db, 100)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	for _, original := range originals {
		var actual model.MediaGenerationAsset
		require.NoError(t, db.First(&actual, "id = ?", original.ID).Error)
		original.State = "expired"
		assert.Equal(t, original, actual, "all receipt metadata and SHA survive cleanup")
		_, err = os.Lstat(filepath.Join(root, original.ObjectKey))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err = os.Stat(filepath.Join(root, future.ObjectKey))
	require.NoError(t, err)
	var unchanged model.MediaGenerationAsset
	require.NoError(t, db.First(&unchanged, "id = ?", future.ID).Error)
	assert.Equal(t, *future, unchanged)
}

func TestCleanupExpiredMediaGenerationAssetsRejectsUnownedOrChangedFiles(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	for _, kind := range []string{"manual", "symlink", "changed", "not_ready"} {
		asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
		require.NoError(t, err)
		path := filepath.Join(root, asset.ObjectKey)
		switch kind {
		case "manual":
			asset.ObjectKey = "manual-protected.png"
			require.NoError(t, os.Rename(path, filepath.Join(root, asset.ObjectKey)))
		case "symlink":
			outside := filepath.Join(t.TempDir(), "protected.png")
			require.NoError(t, os.WriteFile(outside, content, 0600))
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Symlink(outside, path))
		case "changed":
			changed := bytes.Clone(content)
			changed[len(changed)-1] ^= 1
			require.NoError(t, os.WriteFile(path, changed, 0600))
		case "not_ready":
			asset.State = "uploading"
		}
		asset.ExpiresAt = time.Now().Unix() - 1
		require.NoError(t, db.Model(asset).Updates(map[string]any{"object_key": asset.ObjectKey, "expires_at": asset.ExpiresAt, "state": asset.State}).Error)
	}
	n, err := CleanupExpiredMediaGenerationAssets(t.Context(), db, 100)
	require.Error(t, err)
	assert.Zero(t, n)
	var assets []model.MediaGenerationAsset
	require.NoError(t, db.Find(&assets).Error)
	for _, asset := range assets {
		_, err = os.Lstat(filepath.Join(root, asset.ObjectKey))
		require.NoError(t, err, "unsafe or changed files remain untouched")
		assert.NotEqual(t, "expired", asset.State)
	}
}

func TestCleanupExpiredMediaGenerationAssetsRetriesFailedStateAcknowledgement(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, db.Model(asset).Update("expires_at", time.Now().Unix()-1).Error)
	lostAck := errors.New("synthetic expired-state update failure")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("asset_cleanup_state_failure", func(tx *gorm.DB) { tx.AddError(lostAck) }))
	n, err := CleanupExpiredMediaGenerationAssets(t.Context(), db, 100)
	require.ErrorIs(t, err, lostAck)
	assert.Zero(t, n)
	_, err = os.Stat(filepath.Join(root, asset.ObjectKey))
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, db.Callback().Update().Remove("asset_cleanup_state_failure"))
	n, err = CleanupExpiredMediaGenerationAssets(t.Context(), db, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "an already absent owned file allows retrying only the expired-state update")
	var actual model.MediaGenerationAsset
	require.NoError(t, db.First(&actual, "id = ?", asset.ID).Error)
	assert.Equal(t, "expired", actual.State)
	assert.Equal(t, asset.SHA256, actual.SHA256)
}
