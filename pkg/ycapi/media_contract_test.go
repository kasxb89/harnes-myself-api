package ycapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaContractsPreserveNativeControlsWithoutGrantingExecution(t *testing.T) {
	contracts, err := MediaContracts()
	require.NoError(t, err)
	require.Len(t, contracts, 73)
	imageCount, videoCount := 0, 0
	for _, contract := range contracts {
		if contract.Kind == "image" {
			imageCount++
		} else if contract.Kind == "video" {
			videoCount++
		}
	}
	assert.Equal(t, 31, imageCount)
	assert.Equal(t, 42, videoCount)
	contract, ok := LookupMediaContract("gt-image-2.6-flare")
	require.True(t, ok)
	assert.Equal(t, "ycapi-gpt-image-2.6-flare", contract.ID)
	contract.Parameters["images"].Native["max_items"] = float64(999)
	fresh, ok := LookupMediaContract(contract.ID)
	require.True(t, ok)
	assert.Equal(t, float64(16), fresh.Parameters["images"].Native["max_items"])
	schema, ok := MediaContractSchema("ycapi-gemini-nano-banana-2.1-image")
	require.True(t, ok)
	properties := schema["properties"].(map[string]any)
	aspect := properties["aspect_ratio"].(map[string]any)
	assert.NotContains(t, aspect["enum"], "8:1", "an enum entry marked unavailable must not become a menu choice")
	for _, private := range []string{"lane_id", "api_key", "host", "base_url"} {
		assert.NotContains(t, properties, private)
	}
}

func TestMediaRuntimeAssetFieldsUseExplicitNativeShapes(t *testing.T) {
	for _, example := range []struct{ id, kind string }{
		{"aliyun-image-superres", "image"}, {"aliyun-video-superres", "video"}, {"video-desubtitle", "video"},
	} {
		contract, ok := LookupMediaContract(example.id)
		require.True(t, ok)
		fields, err := AssetFields(contract)
		require.NoError(t, err)
		require.Len(t, fields, 1)
		assert.Equal(t, "url", fields[0].Name)
		assert.Equal(t, example.kind, fields[0].MediaKind)
		assert.Equal(t, "single", fields[0].Shape)
		assert.Equal(t, 1, fields[0].Minimum)
		contract.NativeID = "future-url-processing-card"
		_, err = AssetFields(contract)
		require.Error(t, err, "an unknown url role must not inherit a media kind guess")
	}
	contract, ok := LookupMediaContract("gpt-image-2-c")
	require.True(t, ok)
	fields, err := AssetFields(contract)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "multiple", fields[0].Shape, "images remains an array even with max_items=1")
	schema, ok := MediaContractSchema("aliyun-video-superres")
	require.True(t, ok)
	url := schema["properties"].(map[string]any)["url"].(map[string]any)
	assert.Equal(t, "array", url["type"], "the internal asset representation is always an array")
	assert.Equal(t, "single", url["x-media-native-shape"])
	assert.Len(t, schema["x-media-assets"], 1)
}

func TestMediaRuntimeBuilderPreservesIntentFramesAndNativeEnvelope(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png"}
	assets := map[string]MediaAssetMetadata{"asset:first": image, "asset:last": image}
	text, err := ValidateMediaContractParams("gpt-image-2-c", map[string]any{"prompt": "A calm landscape"}, nil)
	require.NoError(t, err)
	request, err := BuildMediaRuntimeRequest("gpt-image-2-c", text, "gpt-image-2-c@fixture:lane", "task-fixture-text", MediaIntentText)
	require.NoError(t, err)
	assert.Equal(t, "gpt-image-2-c", request.ModelID)
	assert.Equal(t, "image", request.Modality)
	assert.Equal(t, "generate", request.Operation)
	assert.Equal(t, "task-fixture-text", request.ClientRequestID)
	frames, err := ValidateMediaContractParams("viduq3", map[string]any{"prompt": "Move gently", "mode": "first-last", "images": []string{"asset:first", "asset:last"}}, assets)
	require.NoError(t, err)
	request, err = BuildMediaRuntimeRequest("ycapi-viduq3", frames, "viduq3@fixture:lane", "task-fixture-frames", MediaIntentFrames)
	require.NoError(t, err)
	assert.Equal(t, []string{"asset:first", "asset:last"}, request.Params["images"])
	assert.Equal(t, "first-last", request.Params["mode"])
	assert.NotContains(t, request.Params, "frame_images", "a native frame field must not be invented")
	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.NotContains(t, wire, "operation", "video runtime requests omit image operations")
	assert.NotContains(t, wire, "intent", "wire intent is a local request guard")
	assert.Equal(t, []string{"asset:first", "asset:last"}, frames["images"], "builder must not mutate normalized inputs")
	_, err = BuildMediaRuntimeRequest("viduq3", frames, "viduq3@fixture:lane", "task-fixture-frames", MediaIntentReference)
	require.ErrorContains(t, err, "frame intent")
}

func TestMediaRuntimeBuilderUsesScalarURLAndRejectsTextFallback(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 512, MIMEType: "image/png"}
	assets := map[string]MediaAssetMetadata{"asset:source": image}
	processing, err := ValidateMediaContractParams("aliyun-image-superres", map[string]any{"url": "asset:source"}, assets)
	require.NoError(t, err)
	assert.Equal(t, []string{"asset:source"}, processing["url"])
	request, err := BuildMediaRuntimeRequest("aliyun-image-superres", processing, "aliyun-image-superres@fixture:lane", "task-fixture-transform", MediaIntentTransform)
	require.NoError(t, err)
	assert.Equal(t, "asset:source", request.Params["url"])
	assert.Equal(t, []string{"asset:source"}, processing["url"])
	_, err = BuildMediaRuntimeRequest("aliyun-image-superres", processing, "aliyun-image-superres@fixture:lane", "task-fixture-transform", MediaIntentText)
	require.ErrorContains(t, err, "text intent cannot include source assets")
	text, err := ValidateMediaContractParams("gpt-image-2-c", map[string]any{"prompt": "A landscape"}, nil)
	require.NoError(t, err)
	_, err = BuildMediaRuntimeRequest("gpt-image-2-c", text, "gpt-image-2-c@fixture:lane", "task-fixture-reference", MediaIntentReference)
	require.ErrorContains(t, err, "text fallback is forbidden")
	_, err = BuildMediaRuntimeRequest("gpt-image-2-c", text, "gpt-image-2-c@fixture:lane", "task-fixture-reference", MediaRuntimeIntent("auto"))
	require.ErrorContains(t, err, "intent")
	edit, err := ValidateMediaContractParams("gpt-image-2-c", map[string]any{"prompt": "Change the background", "mode": "image-edit", "images": []string{"asset:source"}}, assets)
	require.NoError(t, err)
	request, err = BuildMediaRuntimeRequest("gpt-image-2-c", edit, "gpt-image-2-c@fixture:lane", "task-fixture-reference", MediaIntentReference)
	require.NoError(t, err)
	assert.Equal(t, "edit", request.Operation)
	assert.Equal(t, []string{"asset:source"}, request.Params["images"])
}

func TestMediaRuntimeCharacterAndStyleReferencesCannotHideInTextIntent(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png"}
	params, err := ValidateMediaContractParams("mj_imagine", map[string]any{"prompt": "A landscape", "sref": "asset:style"}, map[string]MediaAssetMetadata{"asset:style": image})
	require.NoError(t, err)
	_, err = BuildMediaRuntimeRequest("mj_imagine", params, "mj_imagine@fixture:lane", "task-fixture-style", MediaIntentText)
	require.ErrorContains(t, err, "text intent cannot include source assets")
	request, err := BuildMediaRuntimeRequest("mj_imagine", params, "mj_imagine@fixture:lane", "task-fixture-style", MediaIntentReference)
	require.NoError(t, err)
	assert.Equal(t, "asset:style", request.Params["sref"])
}

func TestMediaContractProcessingDoesNotRequireInventedPrompt(t *testing.T) {
	params := map[string]any{"url": "asset:source", "upscale_factor": 2}
	assets := map[string]MediaAssetMetadata{"asset:source": {Kind: "image", Bytes: 1024, Width: 1024, Height: 512, MIMEType: "image/png"}}
	normalized, err := ValidateMediaContractParams("aliyun-image-superres", params, assets)
	require.NoError(t, err)
	assert.NotContains(t, normalized, "prompt")
	assert.NotContains(t, params, "output_quality", "normalization must not mutate the caller's request")
	assert.Equal(t, float64(95), normalized["output_quality"])
	_, err = ValidateMediaContractParams("aliyun-image-superres", params, nil)
	require.ErrorContains(t, err, "verified source asset metadata")
	_, err = ValidateMediaContractParams("face-repaint", map[string]any{"images": []string{"asset:source"}}, assets)
	require.NoError(t, err, "face repaint explicitly permits an omitted prompt")
}

func TestMediaContractQualityModeIsNotRewrittenAsVideoOperation(t *testing.T) {
	params := map[string]any{"mode": "std", "prompt": "A calm shot"}
	normalized, err := ValidateMediaContractParams("kling-v3-omni-cankao", params, nil)
	require.NoError(t, err)
	assert.Equal(t, "std", normalized["mode"])
	_, err = ValidateMediaContractParams("kling-v3-omni-cankao", map[string]any{"mode": "text-to-video", "prompt": "A calm shot"}, nil)
	require.ErrorContains(t, err, "mode")
}

func TestMediaContractRejectsExcessOrMissingReferencesWithoutTruncation(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png"}
	refs := []string{"asset:first", "asset:second"}
	assets := map[string]MediaAssetMetadata{"asset:first": image, "asset:second": image}
	params := map[string]any{"mode": "image-edit", "prompt": "Change the background", "images": refs}
	_, err := ValidateMediaContractParams("gpt-image-2-c", params, assets)
	require.ErrorContains(t, err, "too many source assets")
	assert.Len(t, params["images"], 2)
	_, err = ValidateMediaContractParams("gpt-image-2-c", map[string]any{"mode": "image-edit", "prompt": "Change the background"}, nil)
	require.ErrorContains(t, err, "source image")
	_, err = ValidateMediaContractParams("viduq3", map[string]any{"mode": "first-last", "prompt": "Move slowly", "images": refs[:1]}, assets)
	require.ErrorContains(t, err, "source image count")
	_, err = ValidateMediaContractParams("viduq3", map[string]any{"mode": "first-last", "prompt": "Move slowly", "images": refs}, assets)
	require.NoError(t, err)
}

func TestMediaContractConditionalFormatsAndUnavailableChoices(t *testing.T) {
	params := map[string]any{"output_format": "gif", "prompt": "A bouncing ball"}
	normalized, err := ValidateMediaContractParams("gpt-image-2.6-flare", params, nil)
	require.NoError(t, err)
	assert.NotContains(t, normalized, "resolution")
	assert.NotContains(t, normalized, "quality")
	assert.Equal(t, float64(9), normalized["frame_count"])
	params["quality"] = "high"
	_, err = ValidateMediaContractParams("gpt-image-2.6-flare", params, nil)
	require.ErrorContains(t, err, "unavailable for the selected controls")
	_, err = ValidateMediaContractParams("seedance-2-5-special", map[string]any{"mode": "text-to-video", "prompt": "A calm shot", "resolution": "1080p"}, nil)
	require.ErrorContains(t, err, "resolution")
	_, err = ValidateMediaContractParams("grok-imagine-video", map[string]any{"mode": "extend", "duration": "10", "prompt": "Continue"}, nil)
	require.ErrorContains(t, err, "mode")
}

func TestMediaContractAppliesQualityAndCrossAssetBudgets(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 576, MIMEType: "image/png"}
	video := MediaAssetMetadata{Kind: "video", Bytes: 2048, Width: 1024, Height: 576, DurationSeconds: 8, FPS: 30, MIMEType: "video/mp4"}
	audio := MediaAssetMetadata{Kind: "audio", Bytes: 1024, DurationSeconds: 8, MIMEType: "audio/mpeg"}
	assets := map[string]MediaAssetMetadata{"asset:image": image, "asset:video": video, "asset:audio": audio}
	_, err := ValidateMediaContractParams("kling-v4", map[string]any{"mode": "reference", "quality": "flash", "prompt": "A shot", "images": []string{"asset:image"}, "audios": []string{"asset:audio"}}, assets)
	require.ErrorContains(t, err, "too many source assets")
	_, err = ValidateMediaContractParams("seedance-2-0-official", map[string]any{"mode": "reference", "prompt": "A shot", "images": []string{"asset:image"}, "videos": []string{"asset:video"}, "audios": []string{"asset:audio"}}, assets)
	require.ErrorContains(t, err, "combined reference duration")
	video.DurationSeconds = 14
	assets["asset:video"] = video
	_, err = ValidateMediaContractParams("wan-3-0", map[string]any{"mode": "reference", "prompt": "A shot", "duration": "20", "videos": []string{"asset:video"}}, assets)
	require.ErrorContains(t, err, "reference plus output")
	_, err = ValidateMediaContractParams("omni-flash-1-2", map[string]any{"mode": "reference", "prompt": "A shot", "images": []string{"asset:image"}, "videos": []string{"asset:video"}}, assets)
	require.Error(t, err, "reference inputs must be checked rather than silently selecting one")
}

func TestMediaContractMultiChoiceNoneAndTransportFieldsFailClosed(t *testing.T) {
	_, err := ValidateMediaContractParams("muse-video", map[string]any{"mode": "text-to-video", "prompt": "A shot", "skill_preset": []string{"none", "dolly_zoom_oner"}}, nil)
	require.ErrorContains(t, err, "exclusive choice")
	_, err = ValidateMediaContractParams("flux-3", map[string]any{"prompt": "A shot", "lane_id": "unreviewed"}, nil)
	require.ErrorContains(t, err, "lane_id")
	_, err = ValidateMediaContractParams("gpt-image-2-c", map[string]any{"mode": "image-edit", "prompt": "A shot", "images": []string{"https://private.invalid/sensitive"}}, nil)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "private.invalid"), "errors must not echo asset references")
}

func TestMediaContractOptionDependenciesAndOptionalPromptTypes(t *testing.T) {
	image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png"}
	audio := MediaAssetMetadata{Kind: "audio", Bytes: 1024, DurationSeconds: 8, MIMEType: "audio/mpeg"}
	assets := map[string]MediaAssetMetadata{"asset:image": image, "asset:audio": audio}
	for _, example := range []struct {
		id     string
		params map[string]any
	}{
		{"grok-imagine-1.5", map[string]any{"mode": "reference", "resolution": "1080p", "duration": "10", "prompt": "A shot"}},
		{"kling-v4", map[string]any{"mode": "first-frame", "quality": "standard", "resolution": "4k", "images": []string{"asset:image"}, "prompt": "A shot"}},
		{"minimax-hailuo-2-3", map[string]any{"mode": "text-to-video", "resolution": "1080p", "duration": "10", "prompt": "A shot"}},
	} {
		_, err := ValidateMediaContractParams(example.id, example.params, assets)
		require.Error(t, err, example.id+" must reject option-specific unavailable combinations")
	}
	params := map[string]any{"images": []string{"asset:image"}, "audios": []string{"asset:audio"}}
	_, err := ValidateMediaContractParams("kling-avatar", params, assets)
	require.NoError(t, err, "avatar permits an omitted prompt")
	params["prompt"] = 42
	_, err = ValidateMediaContractParams("kling-avatar", params, assets)
	require.ErrorContains(t, err, "prompt")
}
