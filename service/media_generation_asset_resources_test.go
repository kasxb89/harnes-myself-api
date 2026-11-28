package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
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
	t.Run("video batch declaration does not grant unmetered batch execution", func(t *testing.T) {
		native, ok := ycapi.MediaContractSchema(videoContract.ID)
		require.True(t, ok)
		assert.Equal(t, float64(50), native["properties"].(map[string]any)["count"].(map[string]any)["maximum"])
		qualified, err := mediaGenerationConfiguredSchema(videoContract, nil)
		require.NoError(t, err)
		count := qualified["properties"].(map[string]any)["count"].(map[string]any)
		assert.Equal(t, float64(1), count["maximum"])
		assert.Equal(t, float64(50), count["x-ycapi-contract"].(map[string]any)["max"], "supplier declaration remains lossless")
		var controls map[string]any
		require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer","const":2}}}`, &controls))
		_, err = mediaGenerationConfiguredSchema(videoContract, controls)
		assert.Error(t, err, "administrator controls cannot bypass missing batch metering")
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
	result, _, err := prepareMediaNativeParameters(t.Context(), nil, MediaNativeRoute{}, request.Model, parameters, model.ModelConfiguration{Parameters: controls}, nil, ycapi.MediaIntentText, "", false)
	require.NoError(t, err)
	assert.EqualValues(t, 2, result["count"])
	assert.NotContains(t, parameters, "count", "published defaults do not rewrite the caller's original body")
	t.Run("effective batch defaults are checked before resolving any supplier asset", func(t *testing.T) {
		contract, ok := ycapi.LookupMediaContract("ycapi-seedance-2-5")
		require.True(t, ok)
		expression := `tier("batch",u("output_count")*0.01)`
		definition := model.ModelConfiguration{ModelName: contract.ID, Kind: "video", Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expression}}
		definition.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: "synthetic:batch-defaults", Intents: []string{"text", "frames"}, QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: MediaGenerationDigest(contract), CheckedAt: "2026-11-27"}
		definition.MediaExecution.BatchPolicy = &model.ModelMediaBatchPolicy{SchemaVersion: 1, Policy: "qualified_native_batch", MaxCount: 3, Meter: "output_count", Modes: []string{"first-frame"}, AdmissionUnit: "native_operation", DurationBasis: "requested_uniform", OutputContract: "ordered_unique_video_payloads_v1", ExpressionSHA256: billingexpr.ExprHashString(expression), LaneID: definition.MediaExecution.LaneID, ContractSHA256: definition.MediaExecution.ContractSHA256, QualificationSHA256: definition.MediaExecution.QualificationSHA256, EvidenceSHA256: strings.Repeat("b", 64), CheckedAt: "2026-11-27"}
		require.NoError(t, common.UnmarshalJsonStr(`{"properties":{"count":{"type":"integer","minimum":1,"maximum":3,"default":2},"mode":{"type":"string","default":"first-frame"}}}`, &definition.Parameters))
		bindings := map[string][]model.MediaGenerationAsset{"images": {{ID: "ma2_pre_admission_only", Kind: "image", Bytes: 1000, Width: 512, Height: 512, MIMEType: "image/png", Format: "png"}}}
		original := map[string]any{"prompt": "Preserve this reference"}
		require.NoError(t, validateMediaGenerationBatchRequest(definition, contract, original, expression), "raw omitted fields cannot decide the effective defaults")
		// A nil DB/route intentionally makes any premature asset resolution fail:
		// these typed rejection errors must precede all supplier preparation I/O.
		_, _, err := prepareMediaNativeParameters(t.Context(), nil, MediaNativeRoute{}, contract.ID, original, definition, bindings, ycapi.MediaIntentFrames, expression, false)
		require.ErrorIs(t, err, ErrMediaBatchClientUnsupported)
		assert.NotContains(t, original, "count")
		assert.NotContains(t, original, "mode")
		definition.MediaExecution.BatchPolicy.Modes = []string{"text-to-video"}
		require.NoError(t, model.ValidateModelMediaExecutionConfiguration(definition))
		_, _, err = prepareMediaNativeParameters(t.Context(), nil, MediaNativeRoute{}, contract.ID, original, definition, bindings, ycapi.MediaIntentFrames, expression, true)
		require.EqualError(t, err, "invalid_parameters", "unreviewed effective mode must fail before any supplier asset lookup")
	})
}

func TestMediaGenerationQuoteProjectionKeepsRemovedAssetRolesPrivate(t *testing.T) {
	request := MediaGenerationRequest{
		ClientOperationID: "synthetic-original-magi-special-operation",
		Model:             "ycapi-magi-2-special",
		ModelVersion:      "original-frozen-model-version",
		Intent:            ycapi.MediaIntentReference,
		Parameters:        map[string]any{"mode": "reference", "prompt": "Preserve the original three material roles", "duration": "5"},
		Assets: map[string][]string{
			"images": {"ma2_original_image"},
			"videos": {"ma2_original_video"},
			"audios": {"ma2_original_audio"},
		},
	}
	parameters := map[string]any{
		"mode": "reference", "prompt": "Preserve the original three material roles", "duration": "5",
		"images": []string{"https://synthetic-private.invalid/original-image"},
		"videos": []string{"https://synthetic-private.invalid/original-video"},
		"audios": []string{"https://synthetic-private.invalid/original-audio"},
	}
	frozen := MediaGenerationFrozen{Request: request, Parameters: parameters, Facts: map[string]any{"seconds": 5}, CostCNY: "0.01", DryRunSHA256: strings.Repeat("a", 64), RetailPolicySHA256: strings.Repeat("b", 64)}
	rawFrozen, err := common.Marshal(frozen)
	require.NoError(t, err)
	rawParameters, err := common.Marshal(parameters)
	require.NoError(t, err)
	original := model.MediaGenerationQuote{
		QuoteID: "mgq_original_magi_special", ClientOpID: request.ClientOperationID,
		Model: request.Model, Kind: "video", ModelConfigVersion: request.ModelVersion,
		RequestSHA256: MediaGenerationRequestDigest(request), MaterialSHA256: strings.Repeat("c", 64),
		UsageFactsJSON: model.LongText(rawFrozen), NativeParamsJSON: model.LongText(rawParameters),
		CostCNY: "0.01", RetailCNY: "0.02", ReserveQuota: 1370, ExpiresAt: 1,
	}
	for _, taskID := range []string{"", "original-accepted-task"} {
		t.Run("task="+taskID, func(t *testing.T) {
			quote := original
			quote.TaskID = taskID
			before := quote
			projection, err := MediaGenerationQuoteProjection(&quote)
			require.NoError(t, err)
			assert.Equal(t, request.ClientOperationID, projection.ClientOperationID)
			assert.Equal(t, before.RequestSHA256, projection.RequestSHA256)
			assert.Equal(t, request.ModelVersion, projection.ModelVersion)
			assert.Equal(t, request.Assets, projection.Assets, "retain the original opaque IDs, including roles removed from the current contract")
			assert.Equal(t, request.Parameters, projection.Parameters, "native upload URLs never enter the public parameters")
			assert.Equal(t, before.RetailCNY, projection.RetailCNY)
			if taskID == "" {
				assert.Equal(t, "quoted", projection.OperationState)
			} else {
				assert.Equal(t, "accepted", projection.OperationState)
			}
			assert.Equal(t, before, quote, "projection cannot rewrite the frozen body, UUID, material binding or financial evidence")
		})
	}
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

type mediaAssetPaddingReader struct{}

func (mediaAssetPaddingReader) Read(p []byte) (int, error) {
	if len(p) > 1<<20 {
		return 0, errors.New("synthetic source rejects a whole-file allocation")
	}
	clear(p)
	return len(p), nil
}

func TestMediaGenerationUploadCapAndStreamingOriginalAsset(t *testing.T) {
	db, actor, content, root := mediaAssetResourceFixture(t)
	const modelID = "ycapi-minimax-h3-image"
	const size = (50 << 20) + 1
	newSource := func() io.Reader {
		return io.MultiReader(bytes.NewReader(content), io.LimitReader(mediaAssetPaddingReader{}, int64(size-len(content))))
	}
	t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", "")
	require.NoError(t, os.Unsetenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES"))
	cap, err := MediaGenerationMaxUploadBytes()
	require.NoError(t, err)
	assert.Equal(t, DefaultMediaGenerationMaxUploadBytes, cap)
	clientID := uuid.NewString()
	_, err = StoreMediaGenerationAsset(t.Context(), db, actor, modelID, clientID, newSource())
	require.Error(t, err, "a larger native model limit cannot raise the default global cap")
	incoming, err := filepath.Glob(filepath.Join(root, ".incoming-*"))
	require.NoError(t, err)
	assert.Empty(t, incoming)

	t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", strconv.Itoa(64<<20))
	asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, modelID, clientID, newSource())
	require.NoError(t, err)
	assert.EqualValues(t, size, asset.Bytes)
	hash := sha256.New()
	_, err = io.Copy(hash, newSource())
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(hash.Sum(nil)), asset.SHA256)
	bindings, err := ReadMediaGenerationAssets(t.Context(), db, actor, modelID, map[string][]string{"images": {asset.ID}})
	require.NoError(t, err)
	assert.Equal(t, asset.ID, bindings["images"][0].ID)
	original, err := FindMediaGenerationAsset(t.Context(), db, actor, modelID, clientID)
	require.NoError(t, err)
	assert.Equal(t, asset.ID, original.ID, "the original UUID remains the upload recovery key")
	file, err := openMediaNativeAsset(t.Context(), filepath.Join(root, asset.ObjectKey), *asset)
	require.NoError(t, err)
	defer file.Close()
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		err := writeMediaNativeAsset(t.Context(), multipartWriter, file, *asset)
		_ = writer.CloseWithError(err)
		done <- err
		close(done)
	}()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close(); <-done })
	multipartReader := multipart.NewReader(reader, multipartWriter.Boundary())
	part, err := multipartReader.NextPart()
	require.NoError(t, err)
	assert.Equal(t, "file", part.FormName())
	hash.Reset()
	count, err := io.Copy(hash, part)
	require.NoError(t, err)
	assert.Equal(t, asset.Bytes, count)
	assert.Equal(t, asset.SHA256, hex.EncodeToString(hash.Sum(nil)))
	part, err = multipartReader.NextPart()
	require.NoError(t, err)
	assert.Equal(t, "purpose", part.FormName())
	purpose, err := io.ReadAll(part)
	require.NoError(t, err)
	assert.Equal(t, "generation", string(purpose))
	_, err = multipartReader.NextPart()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, <-done)

	t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", strconv.Itoa(50<<20))
	_, err = ReadMediaGenerationAssets(t.Context(), db, actor, modelID, map[string][]string{"images": {asset.ID}})
	require.Error(t, err, "new quote inputs honor the current global cap")
	_, err = openMediaNativeAsset(t.Context(), filepath.Join(root, asset.ObjectKey), *asset)
	require.Error(t, err)
	original, err = FindMediaGenerationAsset(t.Context(), db, actor, modelID, clientID)
	require.NoError(t, err, "lowering the upload cap does not erase an original upload receipt")
	assert.Equal(t, asset.ID, original.ID)
}

func TestMediaGenerationUploadCapRejectsInvalidConfigAndNativeMIMELimits(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "1073741825", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", value)
			_, err := MediaGenerationMaxUploadBytes()
			require.ErrorIs(t, err, ErrMediaGenerationUploadConfig)
		})
	}
	t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", strconv.Itoa(1<<30))
	limit, err := mediaGenerationAssetLimit("ycapi-seedance-2-5", "video", "video/mp4")
	require.NoError(t, err)
	assert.EqualValues(t, 200<<20, limit)
	limit, err = mediaGenerationAssetLimit("ycapi-seedance-2-5", "image", "image/png")
	require.NoError(t, err)
	assert.EqualValues(t, 5<<20, limit)
	_, err = mediaGenerationAssetLimit("ycapi-seedance-2-5", "audio", "audio/mp4")
	require.Error(t, err, "a native MP3/WAV role does not accept M4A")
	db, actor, content, root := mediaAssetResourceFixture(t)
	_, err = StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-seedance-2-5", uuid.NewString(),
		io.MultiReader(bytes.NewReader(content), io.LimitReader(mediaAssetPaddingReader{}, 5<<20)))
	require.Error(t, err, "the real measured image MIME uses the smaller image cap")
	files, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, files)
	assert.Empty(t, mediaAssetProcessingSlots)
}

func TestMediaGenerationNativeAssetWriterJoinsAfterInterruptedBody(t *testing.T) {
	content := bytes.Repeat([]byte("synthetic-original-media"), 4096)
	sum := sha256.Sum256(content)
	asset := model.MediaGenerationAsset{Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), MIMEType: "video/mp4"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, pipe := io.Pipe()
	writer := multipart.NewWriter(pipe)
	done := make(chan error, 1)
	go func() {
		err := writeMediaNativeAsset(ctx, writer, bytes.NewReader(content), asset)
		_ = pipe.CloseWithError(err)
		done <- err
		close(done)
	}()
	t.Cleanup(func() { _ = reader.Close(); _ = pipe.Close(); <-done })
	_, err := multipart.NewReader(reader, writer.Boundary()).NextPart()
	require.NoError(t, err, "the producer has started the actual file part")
	cancel()
	_ = reader.CloseWithError(ctx.Err())
	_ = pipe.CloseWithError(ctx.Err())
	require.Error(t, <-done)
}

type mediaAssetUploadTransport func(*http.Request) (*http.Response, error)

func (f mediaAssetUploadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestMediaGenerationNativeAssetStreamKeepsOriginalUploadFence(t *testing.T) {
	for _, outcome := range []string{"ready", "lost-ack", "early-response", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			db, actor, content, _ := mediaAssetResourceFixture(t)
			require.NoError(t, model.MigrateMediaGenerationUpload(db))
			asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			posts := 0
			originalClient := httpClient
			httpClient = &http.Client{Transport: mediaAssetUploadTransport(func(request *http.Request) (*http.Response, error) {
				posts++
				require.Equal(t, "https://ycapi.token6688.com/v1/files", request.URL.String())
				require.Equal(t, http.MethodPost, request.Method)
				response := &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
					`{"id":"synthetic-file","object":"file","url":"https://assets.token6688.com/sha256/%s.png","content_type":%q,"bytes":%d}`,
					asset.SHA256, asset.MIMEType, asset.Bytes)))}
				if outcome == "early-response" {
					return response, nil
				}
				if outcome == "cancelled" {
					_, values, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
					require.NoError(t, err)
					_, err = multipart.NewReader(request.Body, values["boundary"]).NextPart()
					require.NoError(t, err)
					cancel()
					return nil, ctx.Err()
				}
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				require.EqualValues(t, len(body), request.ContentLength)
				_, values, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
				require.NoError(t, err)
				parts := multipart.NewReader(bytes.NewReader(body), values["boundary"])
				part, err := parts.NextPart()
				require.NoError(t, err)
				require.Equal(t, "file", part.FormName())
				actual, err := io.ReadAll(part)
				require.NoError(t, err)
				require.Equal(t, content, actual)
				part, err = parts.NextPart()
				require.NoError(t, err)
				require.Equal(t, "purpose", part.FormName())
				actual, err = io.ReadAll(part)
				require.NoError(t, err)
				require.Equal(t, "generation", string(actual))
				_, err = parts.NextPart()
				require.ErrorIs(t, err, io.EOF)
				if outcome == "lost-ack" {
					_ = response.Body.Close()
					return nil, errors.New("synthetic acknowledgement lost")
				}
				return response, nil
			})}
			t.Cleanup(func() { httpClient = originalClient })
			route := MediaNativeRoute{BaseURL: "https://ycapi.token6688.com", Credential: "synthetic-only", CredentialDigest: "synthetic-digest", ConfigHash: "synthetic-config"}
			client, err := mediaNativeHTTPClient(route)
			require.NoError(t, err)
			_, fakeTransport := client.Transport.(mediaAssetUploadTransport)
			require.True(t, fakeTransport, "this test must not acquire a network transport")
			url, err := resolveMediaNativeAsset(ctx, db, route, *asset)
			var receipt model.MediaGenerationUpload
			require.NoError(t, db.Where("asset_id = ?", asset.ID).Take(&receipt).Error)
			if outcome == "ready" {
				require.NoError(t, err)
				require.Equal(t, "ready", receipt.State)
				require.NotEmpty(t, url)
			} else {
				require.Error(t, err)
				require.Equal(t, "unknown", receipt.State)
			}
			_, again := resolveMediaNativeAsset(t.Context(), db, route, *asset)
			if outcome == "ready" {
				require.NoError(t, again)
			} else {
				require.Error(t, again)
			}
			assert.Equal(t, 1, posts, "the original sending/unknown fence never sends a second upload")
			var count int64
			require.NoError(t, db.Model(&model.MediaGenerationUpload{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestCleanupExpiredMediaGenerationAssetsIgnoresNewUploadLimit(t *testing.T) {
	for _, limit := range []string{"1", "invalid"} {
		t.Run(limit, func(t *testing.T) {
			db, actor, content, root := mediaAssetResourceFixture(t)
			asset, err := StoreMediaGenerationAsset(t.Context(), db, actor, "ycapi-gpt-image-2.6-flare", uuid.NewString(), bytes.NewReader(content))
			require.NoError(t, err)
			require.NoError(t, db.Model(asset).Update("expires_at", 1).Error)
			t.Setenv("MEDIA_GENERATION_MAX_UPLOAD_BYTES", limit)
			count, err := CleanupExpiredMediaGenerationAssets(t.Context(), db, 1)
			require.NoError(t, err)
			assert.Equal(t, 1, count)
			_, err = os.Stat(filepath.Join(root, asset.ObjectKey))
			require.ErrorIs(t, err, os.ErrNotExist)
			var receipt model.MediaGenerationAsset
			require.NoError(t, db.First(&receipt, "id = ?", asset.ID).Error)
			assert.Equal(t, "expired", receipt.State)
			assert.Equal(t, asset.SHA256, receipt.SHA256)
		})
	}
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
