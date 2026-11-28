package ycapi

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaContractsPreserveNativeControlsWithoutGrantingExecution(t *testing.T) {
	contracts, err := MediaContracts()
	require.NoError(t, err)
	require.Len(t, contracts, 75)
	imageCount, videoCount := 0, 0
	for _, contract := range contracts {
		if contract.Kind == "image" {
			imageCount++
		} else if contract.Kind == "video" {
			videoCount++
		}
	}
	assert.Equal(t, 31, imageCount)
	assert.Equal(t, 44, videoCount)
	contract, ok := LookupMediaContract("gt-image-2.6-flare")
	require.True(t, ok)
	assert.Equal(t, "ycapi-gpt-image-2.6-flare", contract.ID)
	contract.Parameters["images"].Native["max_items"] = float64(999)
	fresh, ok := LookupMediaContract(contract.ID)
	require.True(t, ok)
	assert.Equal(t, float64(16), fresh.Parameters["images"].Native["max_items"])
	schema, ok := MediaContractSchema("ycapi-gemini-3.1-flash-lite-image")
	require.True(t, ok)
	properties := schema["properties"].(map[string]any)
	aspect := properties["aspect_ratio"].(map[string]any)
	assert.NotContains(t, aspect["enum"], "21:9", "an enum entry marked unavailable must not become a menu choice")
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
	assert.Equal(t, "aliyunsr", url["x-ycapi-contract"].(map[string]any)["direct_upload"])
	video := MediaAssetMetadata{Kind: "video", Bytes: 2048, Width: 1280, Height: 720, DurationSeconds: 2, FPS: 24, MIMEType: "video/mp4", Format: "mp4"}
	params, err := ValidateMediaContractParams("aliyun-video-superres", map[string]any{"url": "asset:source"}, map[string]MediaAssetMetadata{"asset:source": video})
	require.NoError(t, err)
	wire, err := BuildMediaRuntimeRequest("aliyun-video-superres", params, "aliyun-video-superres@synthetic:only", "task-synthetic-video-superres", MediaIntentTransform)
	require.NoError(t, err)
	assert.Equal(t, "asset:source", wire.Params["url"])
	assert.NotContains(t, wire.Params, "direct_upload", "public upload metadata must not become a caller-selected transport bypass")
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
	encoded, err := common.Marshal(request)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, common.Unmarshal(encoded, &wire))
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
	require.ErrorContains(t, err, "source video", "public extend availability still requires the declared video input")
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

func TestMediaContractFreshReplicaMaterialAndConditionalBoundaries(t *testing.T) {
	assets := map[string]MediaAssetMetadata{}
	images, videos, audios := []string{}, []string{}, []string{}
	for i := range 15 {
		ref := fmt.Sprintf("asset:image-%d", i)
		images = append(images, ref)
		assets[ref] = MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 576, MIMEType: "image/png", Format: "png"}
	}
	for i := range 6 {
		video, audio := fmt.Sprintf("asset:video-%d", i), fmt.Sprintf("asset:audio-%d", i)
		videos, audios = append(videos, video), append(audios, audio)
		assets[video] = MediaAssetMetadata{Kind: "video", Bytes: 2048, Width: 1280, Height: 720, DurationSeconds: 2, FPS: 24, MIMEType: "video/mp4", Format: "mp4"}
		assets[audio] = MediaAssetMetadata{Kind: "audio", Bytes: 1024, DurationSeconds: 2, MIMEType: "audio/mpeg", Format: "mp3"}
	}
	for _, tc := range []struct {
		name, model string
		params      map[string]any
		intent      MediaRuntimeIntent
		failure     string
		expected    map[string]any
	}{
		{"seedream fixed declared 2k default", "doubao-seedream-5-5", map[string]any{"prompt": "A blue cup"}, MediaIntentText, "", map[string]any{"resolution": "2k"}},
		{"seedream cannot invent 4k", "doubao-seedream-5-5", map[string]any{"prompt": "A blue cup", "resolution": "4k"}, MediaIntentText, "resolution", nil},
		{"flash lite unavailable ratio stays unavailable", "gemini-3.1-flash-lite-image", map[string]any{"prompt": "A wide cup", "aspect_ratio": "21:9"}, MediaIntentText, "aspect_ratio", nil},
		{"flash lite new prompt bound admits 50000", "gemini-3.1-flash-lite-image", map[string]any{"prompt": strings.Repeat("杯", 50000)}, MediaIntentText, "", nil},
		{"flash lite prompt bound rejects 50001", "gemini-3.1-flash-lite-image", map[string]any{"prompt": strings.Repeat("杯", 50001)}, MediaIntentText, "prompt exceeds", nil},
		{"nano banana newly declared wide ratio", "gemini-nano-banana-2.1-image", map[string]any{"prompt": "A wide cup", "aspect_ratio": "8:1"}, MediaIntentText, "", map[string]any{"aspect_ratio": "8:1"}},
		{"MJ return seed is explicit text boolean", "mj_imagine", map[string]any{"prompt": "A cup", "mode": "text-to-image", "return_seed": true}, MediaIntentText, "", map[string]any{"return_seed": true}},
		{"MJ return seed unavailable in reference mode", "mj_imagine", map[string]any{"prompt": "A cup", "mode": "multi-reference", "images": images[:2], "return_seed": true}, MediaIntentReference, "return_seed", nil},
		{"Grok extend has a real video role", "grok-imagine-video", map[string]any{"prompt": "Continue the source shot", "mode": "extend", "duration": "10", "videos": videos[:1]}, MediaIntentTransform, "", map[string]any{"mode": "extend"}},
		{"Grok extend cannot select long text durations", "grok-imagine-video", map[string]any{"prompt": "Continue", "mode": "extend", "duration": "12", "videos": videos[:1]}, MediaIntentTransform, "duration", nil},
		{"Grok extend source video cannot exceed one", "grok-imagine-video", map[string]any{"prompt": "Continue", "mode": "extend", "videos": videos[:2]}, MediaIntentTransform, "too many source", nil},
		{"Grok extend must not receive text-only controls", "grok-imagine-video", map[string]any{"prompt": "Continue", "mode": "extend", "resolution": "720p", "videos": videos[:1]}, MediaIntentTransform, "resolution", nil},
		{"Grok 1.5 keeps all 14 ordered inputs", "grok-imagine-1.5", map[string]any{"prompt": "Move across the references", "mode": "first-frame", "images": images[:14]}, MediaIntentFrames, "", nil},
		{"Grok 1.5 rejects input 15 without trimming", "grok-imagine-1.5", map[string]any{"prompt": "Move", "mode": "first-frame", "images": images}, MediaIntentFrames, "too many source", nil},
		{"Grok special keeps all 14 ordered inputs", "grok-imagine-1-5-7tu-special", map[string]any{"prompt": "Move", "mode": "reference", "images": images[:14], "aspect_ratio": "4:3"}, MediaIntentReference, "", nil},
		{"Grok special rejects input 15", "grok-imagine-1-5-7tu-special", map[string]any{"prompt": "Move", "mode": "reference", "images": images}, MediaIntentReference, "too many source", nil},
		{"H3 long text and new 1080p", "minimax-h3", map[string]any{"prompt": "A long shot", "mode": "text-to-video", "duration": "30", "resolution": "1080p"}, MediaIntentText, "", map[string]any{"duration": "30", "resolution": "1080p"}},
		{"H3 long reference and new 480p", "minimax-h3", map[string]any{"prompt": "A long shot", "mode": "reference", "duration": "16", "resolution": "480p", "images": images[:1]}, MediaIntentReference, "", map[string]any{"duration": "16", "resolution": "480p"}},
		{"H3 long first frame forbidden", "minimax-h3", map[string]any{"prompt": "A long shot", "mode": "first-frame", "duration": "16", "images": images[:1]}, MediaIntentFrames, "duration", nil},
		{"H3 long first last forbidden", "minimax-h3", map[string]any{"prompt": "A long shot", "mode": "first-last", "duration": "30", "images": images[:2]}, MediaIntentFrames, "duration", nil},
		{"H3 original short first last remains legal", "minimax-h3", map[string]any{"prompt": "A shot", "mode": "first-last", "duration": "15", "images": images[:2]}, MediaIntentFrames, "", nil},
		{"H3 audio only needs a visual reference", "minimax-h3", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "Reference audio requires", nil},
		{"H3 audio with image remains reference", "minimax-h3", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1], "images": images[:1]}, MediaIntentReference, "", nil},
		{"H3 audio with video remains reference", "minimax-h3", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1], "videos": videos[:1]}, MediaIntentReference, "", nil},
		{"Hailuo empty option condition never means all modes", "minimax-hailuo-2-3", map[string]any{"prompt": "A shot", "mode": "text-to-video", "resolution": "1080p"}, MediaIntentText, "resolution", nil},
		{"Hailuo remaining 768p is legal", "minimax-hailuo-2-3", map[string]any{"prompt": "A shot", "mode": "text-to-video", "resolution": "768p"}, MediaIntentText, "", nil},
		{"Omni 1.1 actual new default", "omni-flash-1-1", map[string]any{"prompt": "A shot", "mode": "text-to-video"}, MediaIntentText, "", map[string]any{"resolution": "1080p"}},
		{"Omni one reference video", "omni-flash-1-2", map[string]any{"prompt": "Follow the motion", "mode": "reference", "videos": videos[:1]}, MediaIntentReference, "", nil},
		{"Omni two reference videos are retained", "omni-flash-1-2", map[string]any{"prompt": "Follow the motion", "mode": "reference", "videos": videos[:2]}, MediaIntentReference, "", nil},
		{"Seedance promo reference 4k remains legal", "seedance-2-0-promo", map[string]any{"prompt": "A shot", "mode": "reference", "resolution": "4k", "images": images[:1]}, MediaIntentReference, "", nil},
		{"Seedance promo first last cannot use 4k", "seedance-2-0-promo", map[string]any{"prompt": "A shot", "mode": "first-last", "resolution": "4k", "images": images[:2]}, MediaIntentFrames, "resolution", nil},
		{"Seedance special first last cannot use 480p", "seedance-2-0-special", map[string]any{"prompt": "A shot", "mode": "first-last", "resolution": "480p", "images": images[:2]}, MediaIntentFrames, "resolution", nil},
		{"Seedance special first last 720p", "seedance-2-0-special", map[string]any{"prompt": "A shot", "mode": "first-last", "resolution": "720p", "images": images[:2]}, MediaIntentFrames, "", nil},
		{"Seedance 2.5 draft first last now declared", "seedance-2-5", map[string]any{"prompt": "A shot", "mode": "first-last", "draft": true, "images": images[:2]}, MediaIntentFrames, "", map[string]any{"draft": true}},
		{"Seedance 2.5 flash keeps new duration and five audio", "seedance-2-5-promo", map[string]any{"prompt": "A shot", "mode": "reference", "quality": "flash", "duration": "7", "images": images[:1], "audios": audios[:5]}, MediaIntentReference, "", map[string]any{"duration": "7"}},
		{"Seedance 2.5 flash audio six is rejected", "seedance-2-5-promo", map[string]any{"prompt": "A shot", "mode": "reference", "quality": "flash", "images": images[:1], "audios": audios}, MediaIntentReference, "too many source", nil},
		{"Seedance 2.5 special 480p reference", "seedance-2-5-special", map[string]any{"prompt": "A shot", "mode": "reference", "resolution": "480p", "images": images[:1]}, MediaIntentReference, "", nil},
		{"Seedance 2.5 special 480p first frame forbidden", "seedance-2-5-special", map[string]any{"prompt": "A shot", "mode": "first-frame", "resolution": "480p", "images": images[:1]}, MediaIntentFrames, "resolution", nil},
		{"Wan new audio and square aspect", "wan-3-0-special", map[string]any{"prompt": "A shot", "mode": "reference", "duration": "25", "aspect_ratio": "1:1", "images": images[:1], "videos": videos[:1], "audios": audios[:5]}, MediaIntentReference, "", nil},
		{"Wan regular audio only needs a visual reference", "wan-3-0", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "Reference audio requires", nil},
		{"Wan regular audio with image remains reference", "wan-3-0", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1], "images": images[:1]}, MediaIntentReference, "", nil},
		{"Wan regular audio with video remains reference", "wan-3-0", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1], "videos": videos[:1]}, MediaIntentReference, "", nil},
		{"Wan special explicitly permits audio only", "wan-3-0-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "", nil},
		{"Wan audio cap is five", "wan-3-0-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios}, MediaIntentReference, "too many source", nil},
		{"Wan audio forbidden for first frame", "wan-3-0-special", map[string]any{"prompt": "A shot", "mode": "first-frame", "images": images[:1], "audios": audios[:1]}, MediaIntentFrames, "too many source", nil},
		{"Magi explicit text", "magi-2", map[string]any{"prompt": "A shot", "mode": "text-to-video", "duration": "30"}, MediaIntentText, "", nil},
		{"Magi first frame keeps source", "magi-2", map[string]any{"prompt": "A shot", "mode": "first-frame", "images": images[:1]}, MediaIntentFrames, "", nil},
		{"Magi first frame cannot trim image two", "magi-2", map[string]any{"prompt": "A shot", "mode": "first-frame", "images": images[:2]}, MediaIntentFrames, "too many source", nil},
		{"Magi ordered first last", "magi-2", map[string]any{"prompt": "A shot", "mode": "first-last", "images": images[:2]}, MediaIntentFrames, "", nil},
		{"Magi first last needs both images", "magi-2", map[string]any{"prompt": "A shot", "mode": "first-last", "images": images[:1]}, MediaIntentFrames, "source image count", nil},
		{"Magi combined references thirteen", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:10], "videos": videos[:1], "audios": audios[:2]}, MediaIntentReference, "", nil},
		{"Magi combined references fourteen rejected", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:10], "videos": videos[:1], "audios": audios[:3]}, MediaIntentReference, "combined source asset count", nil},
		{"Magi reference videos count is one", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "videos": videos[:2]}, MediaIntentReference, "too many source", nil},
		{"Magi reference audio is an input not audio output", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "", nil},
		{"Magi special single image requires explicit first frame", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:1]}, MediaIntentReference, "single source image", nil},
		{"Magi special removed video role cannot switch intent", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:1], "videos": videos[:1]}, MediaIntentReference, "field is not declared", nil},
		{"Magi special explicit first frame", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "first-frame", "images": images[:1]}, MediaIntentFrames, "", nil},
		{"Magi special two images remain reference", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:2]}, MediaIntentReference, "", nil},
		{"Magi special removed video is rejected", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "videos": videos[:1]}, MediaIntentReference, "field is not declared", nil},
		{"Magi special removed audio is rejected", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "field is not declared", nil},
		{"Magi special three reference images", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:3]}, MediaIntentReference, "", nil},
		{"Magi special fourth image rejected without truncation", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:4]}, MediaIntentReference, "too many source", nil},
		{"Magi special multiple removed audios rejected", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:2]}, MediaIntentReference, "field is not declared", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := common.Marshal(tc.params)
			require.NoError(t, err)
			normalized, err := ValidateMediaContractParams(tc.model, tc.params, assets)
			if tc.failure != "" {
				require.ErrorContains(t, err, tc.failure)
			} else {
				require.NoError(t, err)
				wire, err := BuildMediaRuntimeRequest(tc.model, normalized, tc.model+"@synthetic:only", "task-synthetic-replica-original", tc.intent)
				require.NoError(t, err)
				for name, value := range tc.expected {
					assert.Equal(t, value, wire.Params[name])
				}
				if mode, ok := tc.params["mode"]; ok {
					assert.Equal(t, mode, wire.Params["mode"], "selected native mode must not silently change")
				}
				for _, role := range []string{"images", "videos", "audios"} {
					if refs, ok := tc.params[role]; ok {
						assert.Equal(t, refs, wire.Params[role], "all %s references and order must reach the native request", role)
					}
				}
				assert.NotContains(t, wire.Params, "intent")
			}
			after, err := common.Marshal(tc.params)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "validation must not trim references or rewrite the caller's original request")
		})
	}
}

func TestMediaRuntimeReferenceModeCannotBecomeTextIntent(t *testing.T) {
	for _, example := range []struct{ model, mode string }{
		{"gpt-image-2.6-flare", "multi-reference"},
		{"magi-2", "reference"},
		{"wan-3-0-special", "reference"},
	} {
		t.Run(example.model, func(t *testing.T) {
			original := map[string]any{"prompt": "Keep the original reference intent", "mode": example.mode}
			normalized, err := ValidateMediaContractParams(example.model, original, nil)
			require.NoError(t, err, "the builder must enforce the local intent/material contract")
			_, err = BuildMediaRuntimeRequest(example.model, normalized, example.model+"@synthetic:only", "task-synthetic-reference-intent", MediaIntentText)
			require.ErrorContains(t, err, "reference intent")
			_, err = BuildMediaRuntimeRequest(example.model, normalized, example.model+"@synthetic:only", "task-synthetic-reference-intent", MediaIntentReference)
			require.ErrorContains(t, err, "text fallback is forbidden")
			assert.Equal(t, example.mode, original["mode"])
		})
	}
	t.Run("flux ordered frames and independent quality modes retain their meanings", func(t *testing.T) {
		image := MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 576, MIMEType: "image/png"}
		frames, err := ValidateMediaContractParams("flux-3-video", map[string]any{"prompt": "Move", "mode": "reference", "images": []string{"asset:first", "asset:last"}}, map[string]MediaAssetMetadata{"asset:first": image, "asset:last": image})
		require.NoError(t, err)
		wire, err := BuildMediaRuntimeRequest("flux-3-video", frames, "flux-3-video@synthetic:only", "task-synthetic-flux-frames", MediaIntentFrames)
		require.NoError(t, err)
		assert.Equal(t, []string{"asset:first", "asset:last"}, wire.Params["images"])
		for _, mode := range []string{"std", "pro"} {
			params, err := ValidateMediaContractParams("kling-v3-omni-cankao", map[string]any{"prompt": "A shot", "mode": mode}, nil)
			require.NoError(t, err)
			wire, err := BuildMediaRuntimeRequest("kling-v3-omni-cankao", params, "kling-v3-omni-cankao@synthetic:only", "task-synthetic-quality-mode", MediaIntentText)
			require.NoError(t, err)
			assert.Equal(t, mode, wire.Params["mode"])
		}
	})
}

func TestMediaMaterialConditionsPublishedPolicyValidation(t *testing.T) {
	contract, ok := LookupMediaContract("magi-2-special")
	require.True(t, ok)
	condition := MediaContractMaterialConditions(contract)
	require.NotNil(t, condition)
	before, err := common.Marshal(contract)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		condition *MediaMaterialConditions
		params    map[string]any
		counts    map[string]int
		failure   string
	}{
		{"published exact-one rejection", condition, map[string]any{"mode": "reference"}, map[string]int{"images": 1}, "single source image"},
		{"absent image count is zero not invented min two", condition, map[string]any{"mode": "reference"}, map[string]int{"videos": 1}, ""},
		{"different explicit operation does not activate rule", condition, map[string]any{"mode": "first-frame"}, map[string]int{"images": 1}, ""},
		{"unsupported policy version", &MediaMaterialConditions{SchemaVersion: 2, Rules: condition.Rules}, map[string]any{}, nil, "version"},
		{"both alternative constraints are unavailable", &MediaMaterialConditions{SchemaVersion: 1, Rules: []MediaMaterialRule{{ID: "bad", AssetField: "images", DisallowedCounts: []int{1}, RequiresAnyAssets: []string{"videos"}, Message: "invalid"}}}, nil, nil, "unavailable"},
		{"negative count predicate is unavailable", &MediaMaterialConditions{SchemaVersion: 1, Rules: []MediaMaterialRule{{ID: "bad", AssetField: "images", DisallowedCounts: []int{-1}, Message: "invalid"}}}, nil, nil, "unavailable"},
		{"when keys all match before a rule applies", &MediaMaterialConditions{SchemaVersion: 1, Rules: []MediaMaterialRule{{ID: "and", When: map[string][]any{"mode": {"reference"}, "quality": {"high"}}, AssetField: "images", DisallowedCounts: []int{1}, Message: "blocked"}}}, map[string]any{"mode": "reference", "quality": "standard"}, map[string]int{"images": 1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, e := common.Marshal(tc.params)
			require.NoError(t, e)
			err := validateMediaMaterialConditions(tc.condition, tc.params, tc.counts)
			if tc.failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.failure)
			}
			after, e := common.Marshal(tc.params)
			require.NoError(t, e)
			assert.Equal(t, raw, after)
		})
	}
	condition.Rules[0].DisallowedCounts[0] = 99
	assert.Equal(t, []int{1}, MediaContractMaterialConditions(contract).Rules[0].DisallowedCounts, "callers cannot mutate later catalog or validation rules")
	after, err := common.Marshal(contract)
	require.NoError(t, err)
	assert.Equal(t, before, after, "host conditions stay outside the native contract/hash")
	for _, name := range []string{"minimax-h3", "wan-3-0"} {
		c, ok := LookupMediaContract(name)
		require.True(t, ok)
		rule := MediaContractMaterialConditions(c)
		require.ErrorContains(t, validateMediaMaterialConditions(rule, map[string]any{"mode": "reference"}, map[string]int{"audios": 1}), "source image or video")
		require.NoError(t, validateMediaMaterialConditions(rule, map[string]any{"mode": "reference"}, map[string]int{"images": 1}), "no audio leaves dependency inactive")
	}
	unaffected, ok := LookupMediaContract("wan-3-0-special")
	require.True(t, ok)
	assert.Nil(t, MediaContractMaterialConditions(unaffected))
}

func TestMediaMJReviewedVersionAndTextConditionsBeforeQuoteAndRuntime(t *testing.T) {
	contract, ok := LookupMediaContract("mj_imagine")
	require.True(t, ok)
	nativeBefore, err := common.Marshal(contract)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		fields map[string]any
		field  string
	}{
		{"default version is not invented", map[string]any{"bot_type": "NIJI_JOURNEY"}, ""},
		{"explicit account default remains empty", map[string]any{"bot_type": "NIJI_JOURNEY", "version": ""}, ""},
		{"niji6 valid", map[string]any{"bot_type": "NIJI_JOURNEY", "version": "6"}, ""},
		{"niji7 quality has no effect but is not invalid", map[string]any{"bot_type": "NIJI_JOURNEY", "version": "7", "quality": "2"}, ""},
		{"MJ8.2 remains available", map[string]any{"bot_type": "MID_JOURNEY", "version": "8.2"}, ""},
		{"MJ5.1 remains available", map[string]any{"bot_type": "MID_JOURNEY", "version": "5.1"}, ""},
		{"unmodified advanced whitespace", map[string]any{"extra": " \t--sameseed  --fast\n--turbo\r--relax "}, ""},
		{"optional advanced empty", map[string]any{"extra": ""}, ""},
		{"natural negative prose", map[string]any{"negative_prompt": "black-and-white, watermarks, letters--numbers"}, ""},
		{"hyphens embedded in prose", map[string]any{"prompt": "A black-and-white bird with letters--numbers"}, ""},
		{"prompt independent alias", map[string]any{"prompt": "A bird --v 7"}, "prompt"},
		{"prompt repeated jobs alias", map[string]any{"prompt": "A bird --r 2"}, "prompt"},
		{"negative starts parameter", map[string]any{"negative_prompt": "--ar 16:9"}, "negative_prompt"},
		{"negative comma boundary", map[string]any{"negative_prompt": "文字,--video"}, "negative_prompt"},
		{"negative Unicode whitespace boundary", map[string]any{"negative_prompt": "文字\u3000--video"}, "negative_prompt"},
		{"repeat equals cannot bypass", map[string]any{"extra": "--repeat=2"}, "extra"},
		{"triple dash cannot bypass", map[string]any{"extra": "---repeat 2"}, "extra"},
		{"uppercase cannot bypass", map[string]any{"extra": "--REPEAT 2"}, "extra"},
		{"quoted flags not supported", map[string]any{"extra": "\"--repeat 2\""}, "extra"},
		{"single dash not supported", map[string]any{"extra": "-sameseed"}, "extra"},
		{"arguments not supported", map[string]any{"extra": "--sameseed 2"}, "extra"},
		{"unreviewed flags not supported", map[string]any{"extra": "--new-native-mode"}, "extra"},
		{"duplicate flags not supported", map[string]any{"extra": "--sameseed --sameseed"}, "extra"},
		{"Unicode advanced separator not supported", map[string]any{"extra": "--fast\u00a0--sameseed"}, "extra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"prompt": "A bird"}
			for k, v := range tc.fields {
				params[k] = v
			}
			before, err := common.Marshal(params)
			require.NoError(t, err)
			normalized, quoteErr := ValidateMediaContractParams(contract.ID, params, nil)
			// Raw cached, not-yet-accepted parameters must encounter the same
			// guard even when the fresh quote path was not called this time.
			wire, runtimeErr := BuildMediaRuntimeRequest(contract.ID, params, "mj@synthetic-only", "task-synthetic-mj-conditions", MediaIntentText)
			if tc.field == "" {
				require.NoError(t, quoteErr)
				require.NoError(t, runtimeErr)
				for field, original := range tc.fields {
					assert.Equal(t, original, normalized[field])
					assert.Equal(t, original, wire.Params[field])
				}
				if _, supplied := params["version"]; !supplied {
					assert.NotContains(t, normalized, "version")
					assert.NotContains(t, wire.Params, "version")
				}
			} else {
				require.ErrorContains(t, quoteErr, "parameter "+tc.field)
				require.ErrorContains(t, runtimeErr, "parameter "+tc.field)
			}
			after, err := common.Marshal(params)
			require.NoError(t, err)
			assert.Equal(t, before, after, "new rules never rewrite the original frozen parameters")
		})
	}
	for _, version := range []string{"8.2", "8.1", "6.1", "5.2", "5.1"} {
		params := map[string]any{"prompt": "A bird", "bot_type": "NIJI_JOURNEY", "version": version}
		_, err := ValidateMediaContractParams(contract.ID, params, nil)
		require.ErrorContains(t, err, "version", version)
		_, err = BuildMediaRuntimeRequest(contract.ID, params, "mj@synthetic-only", "task-synthetic-version", MediaIntentText)
		require.ErrorContains(t, err, "version", version)
	}
	policy := MediaContractParameterConditions(contract)
	for _, name := range policy.Rules[1].TextFlags.ForbiddenNames {
		_, err := ValidateMediaContractParams(contract.ID, map[string]any{"prompt": "A bird", "extra": "--" + name + " 2"}, nil)
		require.ErrorContains(t, err, "extra", name)
	}
	for alias := range policy.Rules[1].TextFlags.ReservedAliases {
		_, err := ValidateMediaContractParams(contract.ID, map[string]any{"prompt": "A bird", "extra": "--" + alias + " 1"}, nil)
		require.ErrorContains(t, err, "extra", alias)
	}
	nativeAfter, err := common.Marshal(contract)
	require.NoError(t, err)
	assert.Equal(t, nativeBefore, nativeAfter, "reviewed conditions never change native schema or qualification hash")
}

func TestMediaParameterConditionsPublishedSchemaFailsClosed(t *testing.T) {
	contract, ok := LookupMediaContract("mj_imagine")
	require.True(t, ok)
	policy := MediaContractParameterConditions(contract)
	require.Equal(t, 2, policy.SchemaVersion)
	for _, tc := range []struct {
		name   string
		mutate func(*MediaParameterConditions)
	}{
		{"unknown version", func(p *MediaParameterConditions) { p.SchemaVersion = 3 }},
		{"empty document", func(p *MediaParameterConditions) { p.Rules = nil }},
		{"duplicate identity", func(p *MediaParameterConditions) { p.Rules[1].ID = p.Rules[0].ID }},
		{"unknown inactive profile", func(p *MediaParameterConditions) {
			p.Rules[1].When = map[string][]any{"bot_type": {"NOT_ACTIVE"}}
			p.Rules[1].TextFlags.Profile = "future-v99"
		}},
		{"both constraint alternatives", func(p *MediaParameterConditions) {
			p.Rules[0].TextFlags = &MediaTextFlagPolicy{Profile: "text-no-native-parameter-token-v1"}
		}},
		{"null predicate", func(p *MediaParameterConditions) { p.Rules[0].When["bot_type"] = []any{nil} }},
		{"compound predicate", func(p *MediaParameterConditions) { p.Rules[0].When["bot_type"] = []any{map[string]any{"x": "y"}} }},
		{"malformed field name", func(p *MediaParameterConditions) { p.Rules[0].Parameter = "version;exec" }},
		{"message exceeds UTF8 byte bound", func(p *MediaParameterConditions) { p.Rules[0].Message = strings.Repeat("a", 1025) }},
		{"message contains invalid UTF8", func(p *MediaParameterConditions) { p.Rules[0].Message = string([]byte{0xff}) }},
		{"text profile with unrecognized options", func(p *MediaParameterConditions) { p.Rules[2].TextFlags.AllowedNames = []string{"x"} }},
		{"ambiguous allowed forbidden flag", func(p *MediaParameterConditions) {
			p.Rules[1].TextFlags.AllowedNames = append(p.Rules[1].TextFlags.AllowedNames, "repeat")
		}},
		{"unsafe alias", func(p *MediaParameterConditions) {
			p.Rules[1].TextFlags.ReservedAliases["quoted alias"] = "aspect_ratio"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := MediaContractParameterConditions(contract)
			tc.mutate(fresh)
			require.ErrorContains(t, validateMediaParameterConditions(fresh, map[string]any{"bot_type": "MID_JOURNEY", "prompt": "A bird"}), "conditions")
		})
	}
	policy.Rules[0].DisallowedValues[0] = "changed"
	policy.Rules[1].TextFlags.ReservedAliases["ar"] = "changed"
	fresh := MediaContractParameterConditions(contract)
	assert.Equal(t, "8.2", fresh.Rules[0].DisallowedValues[0])
	assert.Equal(t, "aspect_ratio", fresh.Rules[1].TextFlags.ReservedAliases["ar"])
	assert.Nil(t, MediaContractMaterialConditions(contract), "MJ parameter conditions are separate from material v1")
	for _, id := range []string{"magi-2-special", "minimax-h3", "wan-3-0", "gpt-image-2-c"} {
		c, ok := LookupMediaContract(id)
		require.True(t, ok)
		assert.Nil(t, MediaContractParameterConditions(c), "unaffected models do not gain parameter conditions")
	}
	for _, params := range []map[string]any{{}, {"version": ""}, {"version": "6.1"}, {"bot_type": "NIJI_JOURNEY"}} {
		require.NoError(t, ValidateMediaParameterConditions(contract, params), "missing predicate/target is not an invented default")
	}
	require.ErrorContains(t, ValidateMediaParameterConditions(contract, map[string]any{"bot_type": "NIJI_JOURNEY", "version": nil}), "null")
	booleanRule := &MediaParameterConditions{SchemaVersion: 2, Rules: []MediaParameterRule{{ID: "typed", When: map[string][]any{"flag": {true}}, Parameter: "number", DisallowedValues: []any{float64(2)}, Message: "blocked"}}}
	require.NoError(t, validateMediaParameterConditions(booleanRule, map[string]any{"flag": "true", "number": float64(2)}), "no string/boolean coercion")
	require.NoError(t, validateMediaParameterConditions(booleanRule, map[string]any{"flag": true, "number": "2"}), "no numeric/string coercion")
	require.ErrorContains(t, validateMediaParameterConditions(booleanRule, map[string]any{"flag": true, "number": float64(2)}), "blocked")
}

func TestMediaParameterConditionsWireBoundaryFixtures(t *testing.T) {
	contract, ok := LookupMediaContract("mj_imagine")
	require.True(t, ok)
	for _, tc := range []struct {
		name   string
		mutate func(*MediaParameterConditions)
		valid  bool
	}{
		{"factory", func(p *MediaParameterConditions) {}, true},
		{"message-1024-ASCII-bytes", func(p *MediaParameterConditions) { p.Rules[0].Message = strings.Repeat("a", 1024) }, true},
		{"message-1024-UTF8-bytes", func(p *MediaParameterConditions) { p.Rules[0].Message = strings.Repeat("字", 341) + "a" }, true},
		{"message-1025-bytes", func(p *MediaParameterConditions) { p.Rules[0].Message = strings.Repeat("字", 341) + "ab" }, false},
		{"message-empty", func(p *MediaParameterConditions) { p.Rules[0].Message = "" }, false},
		{"unknown-version", func(p *MediaParameterConditions) { p.SchemaVersion = 9 }, false},
		{"inactive-unknown-profile", func(p *MediaParameterConditions) {
			p.Rules[1].When = map[string][]any{"bot_type": {"NOT_ACTIVE"}}
			p.Rules[1].TextFlags.Profile = "future-v1"
		}, false},
		{"rule-ID-hyphen-valid", func(p *MediaParameterConditions) { p.Rules[0].ID = "rule-with_1" }, true},
		{"field-hyphen-invalid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = "not-a-field" }, false},
		{"name-leading-digit-invalid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = "1field" }, false},
		{"name-uppercase-invalid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = "Version" }, false},
		{"name-Unicode-invalid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = "版本" }, false},
		{"name-64-byte-valid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = strings.Repeat("a", 64) }, true},
		{"name-65-byte-invalid", func(p *MediaParameterConditions) { p.Rules[0].Parameter = strings.Repeat("a", 65) }, false},
		{"advanced-optional-options-absent", func(p *MediaParameterConditions) {
			p.Rules[1].TextFlags.ForbiddenNames = nil
			p.Rules[1].TextFlags.ReservedAliases = nil
		}, true},
		{"advanced-required-allowlist-missing", func(p *MediaParameterConditions) { p.Rules[1].TextFlags.AllowedNames = nil }, false},
		{"advanced-required-double-dash-false", func(p *MediaParameterConditions) { p.Rules[1].TextFlags.RequireDoubleDash = false }, false},
		{"text-profile-options-must-be-empty", func(p *MediaParameterConditions) { p.Rules[2].TextFlags.RequireDoubleDash = true }, false},
		{"bool-literal-valid", func(p *MediaParameterConditions) { p.Rules[0].DisallowedValues = []any{true} }, true},
		{"null-literal-invalid", func(p *MediaParameterConditions) { p.Rules[0].DisallowedValues = []any{nil} }, false},
		{"compound-literal-invalid", func(p *MediaParameterConditions) { p.Rules[0].DisallowedValues = []any{[]any{"x"}} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := MediaContractParameterConditions(contract)
			tc.mutate(policy)
			params := map[string]any{"bot_type": "MID_JOURNEY", "prompt": "A bird"}
			err := validateMediaParameterConditions(policy, params)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "conditions")
			}
			// A focused synthetic exporter consumes these Go-produced rows;
			// tests do not write files, run services, or issue model requests.
			row := map[string]any{"name": tc.name, "conditions": policy, "params": params, "valid": err == nil}
			if err != nil {
				row["error"] = err.Error()
			}
			data, marshalErr := common.Marshal(row)
			require.NoError(t, marshalErr)
			t.Logf("MJ_PARAMETER_POLICY_FIXTURE %s", data)
		})
	}
	for _, value := range []any{math.NaN(), math.Inf(1), math.Inf(-1), int(1)} {
		policy := MediaContractParameterConditions(contract)
		policy.Rules[0].DisallowedValues = []any{value}
		require.ErrorContains(t, validateMediaParameterConditions(policy, nil), "conditions", "non-JSON/nonfinite Go literals must not enter the policy")
	}
	whiteSpace := []rune{'\u0009', '\u000a', '\u000b', '\u000c', '\u000d', '\u0020', '\u0085', '\u00a0', '\u1680', '\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000'}
	logLexer := func(name, text string, valid bool) {
		policy := MediaTextFlagPolicy{Profile: "text-no-native-parameter-token-v1"}
		actual := mediaTextFlagsValid(policy, text)
		require.Equal(t, valid, actual, name)
		data, err := common.Marshal(map[string]any{"name": name, "text_flags": policy, "text": text, "valid": actual})
		require.NoError(t, err)
		t.Logf("MJ_PARAMETER_LEXER_FIXTURE %s", data)
	}
	for _, r := range whiteSpace {
		require.True(t, unicode.IsSpace(r), "reviewed fixed White_Space set includes U+%04X", r)
		text := "letters" + string(r) + "--r 2"
		logLexer(fmt.Sprintf("Unicode-White-Space-U+%04X", r), text, false)
	}
	for _, text := range []string{"letters\ufeff--r 2", "letters\u200b--r 2", "https://assets.invalid/path--token", "letters--numbers", "(quoted)--r 2"} {
		logLexer("literal-unreviewed-boundary-"+text, text, true)
	}
	logLexer("comma-before-native-parameter", "watermarks,--ar 16:9", false)
	require.False(t, unicode.IsSpace('\ufeff'))
	require.False(t, unicode.IsSpace('\u200b'))
}

func TestMediaRuntimeTransformOnlyContractWithNativeReferenceMode(t *testing.T) {
	contract, ok := LookupMediaContract("veo-4-omni-v2v")
	require.True(t, ok)
	assert.Equal(t, []string{"transform"}, MediaContractIntents(contract))
	nativeBefore, err := common.Marshal(contract)
	require.NoError(t, err)
	params := map[string]any{"aspect_ratio": "16:9", "count": float64(1), "mode": "reference", "prompt": "A small blue cube rests on a plain table in soft light.", "videos": []string{"asset:original-source-video"}}
	original, err := common.Marshal(params)
	require.NoError(t, err)
	video := MediaAssetMetadata{Kind: "video", Bytes: 1024, Width: 720, Height: 720, DurationSeconds: 2, FPS: 24, MIMEType: "video/mp4", Format: "mp4"}
	normalized, err := ValidateMediaContractParams(contract.ID, params, map[string]MediaAssetMetadata{"asset:original-source-video": video})
	require.NoError(t, err)
	wire, err := BuildMediaRuntimeRequest(contract.ID, normalized, "veo@synthetic-only", "task-synthetic-original-video", MediaIntentTransform)
	require.NoError(t, err)
	assert.Equal(t, "reference", wire.Params["mode"], "reviewed intent does not rewrite supplier-native parameters")
	assert.Equal(t, []string{"asset:original-source-video"}, wire.Params["videos"])
	assert.Equal(t, "task-synthetic-original-video", wire.ClientRequestID)
	for _, intent := range []MediaRuntimeIntent{MediaIntentText, MediaIntentReference, MediaIntentFrames} {
		_, err := BuildMediaRuntimeRequest(contract.ID, normalized, "veo@synthetic-only", "task-synthetic-original-video", intent)
		require.ErrorContains(t, err, "intent", string(intent))
	}
	withoutMaterial := map[string]any{"mode": "reference", "prompt": "Keep the original video intent"}
	_, err = BuildMediaRuntimeRequest(contract.ID, withoutMaterial, "veo@synthetic-only", "task-synthetic-no-source", MediaIntentTransform)
	require.Error(t, err, "transform-only declaration does not authorize missing source material")
	after, err := common.Marshal(params)
	require.NoError(t, err)
	assert.Equal(t, original, after)
	nativeAfter, err := common.Marshal(contract)
	require.NoError(t, err)
	assert.Equal(t, nativeBefore, nativeAfter)
	// A regular reference card still cannot claim a transform intent.
	ref, err := ValidateMediaContractParams("wan-3-0-special", map[string]any{"mode": "reference", "prompt": "A shot", "images": []string{"asset:reference"}}, map[string]MediaAssetMetadata{"asset:reference": {Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png"}})
	require.NoError(t, err)
	_, err = BuildMediaRuntimeRequest("wan-3-0-special", ref, "wan@synthetic-only", "task-synthetic-no-intent-expansion", MediaIntentTransform)
	require.ErrorContains(t, err, "reference intent")
}

func TestMediaRuntimeVideoBatchNeedsSeparateMeterQualification(t *testing.T) {
	contract, ok := LookupMediaContract("magi-2")
	require.True(t, ok)
	require.NotNil(t, contract.Parameters["count"].Maximum)
	assert.Equal(t, float64(50), *contract.Parameters["count"].Maximum, "the public native declaration remains lossless")
	params, err := ValidateMediaContractParams(contract.ID, map[string]any{"mode": "text-to-video", "prompt": "A shot", "count": float64(2)}, nil)
	require.NoError(t, err, "supplier-declared quantity support is distinct from execution billing qualification")
	_, err = BuildMediaRuntimeRequest(contract.ID, params, "magi-2@synthetic:only", "task-synthetic-batch-not-qualified", MediaIntentText)
	require.ErrorContains(t, err, "count")
	assert.Equal(t, float64(2), params["count"], "reject the batch instead of trimming it to one")
	for _, example := range []struct {
		model, mode string
		count       float64
	}{
		{"magi-2", "text-to-video", 1}, {"gpt-image-2.6-flare", "text-to-image", 2}, {"gpt-image-2.6-flare", "text-to-image", 50},
	} {
		params, err := ValidateMediaContractParams(example.model, map[string]any{"mode": example.mode, "prompt": "A cup", "count": example.count}, nil)
		require.NoError(t, err)
		wire, err := BuildMediaRuntimeRequest(example.model, params, example.model+"@synthetic:only", "task-synthetic-qualified-single-or-image", MediaIntentText)
		require.NoError(t, err)
		assert.Equal(t, example.count, wire.Params["count"])
	}
	require.NoError(t, ValidateMediaRuntimeQuantity(contract, map[string]any{}), "omission means the one-output default")
	for _, value := range []any{"1", true, nil, float64(0), float64(-1), float64(0.5), float64(2)} {
		require.ErrorContains(t, ValidateMediaRuntimeQuantity(contract, map[string]any{"count": value}), "count")
	}
}

func TestMediaContractFreshSixDeclarationDrift(t *testing.T) {
	assets := map[string]MediaAssetMetadata{}
	images, videos := []string{}, []string{}
	for i := range 4 {
		image, video := fmt.Sprintf("asset:drift-image-%d", i), fmt.Sprintf("asset:drift-video-%d", i)
		images, videos = append(images, image), append(videos, video)
		assets[image] = MediaAssetMetadata{Kind: "image", Bytes: 1024, Width: 1024, Height: 1024, MIMEType: "image/png", Format: "png"}
		assets[video] = MediaAssetMetadata{Kind: "video", Bytes: 2048, Width: 720, Height: 720, DurationSeconds: 2, FPS: 24, MIMEType: "video/mp4", Format: "mp4"}
	}
	assets["asset:drift-audio"] = MediaAssetMetadata{Kind: "audio", Bytes: 1024, DurationSeconds: 2, MIMEType: "audio/mpeg", Format: "mp3"}
	for _, id := range []string{"mj_imagine", "magi-2", "magi-2-special", "veo-4-omni", "omni-flash-1-2", "pixverse-c1"} {
		contract, ok := LookupMediaContract(id)
		require.True(t, ok)
		schema, ok := MediaContractSchema(id)
		require.True(t, ok)
		fields, err := AssetFields(contract)
		require.NoError(t, err)
		properties := schema["properties"].(map[string]any)
		switch id {
		case "mj_imagine":
			assert.Equal(t, 1980, contract.MaxPromptChars)
		case "magi-2":
			assert.Equal(t, 1, *contract.Parameters["videos"].MaxItems)
		case "magi-2-special":
			require.Len(t, fields, 1)
			assert.Equal(t, "images", fields[0].Name)
			assert.Equal(t, 3, *fields[0].Maximum)
			assert.NotContains(t, properties, "videos")
			assert.NotContains(t, properties, "audios")
			duration := properties["duration"].(map[string]any)
			assert.Equal(t, "12", duration["default"])
			assert.Len(t, duration["enum"], 9, "13-25 remain in the native evidence but unavailable choices are omitted")
			assert.Contains(t, contract.Parameters["duration"].Enum, "25")
			assert.NotContains(t, contract.Parameters["duration"].Enum, "26")
		case "veo-4-omni":
			duration := properties["duration"].(map[string]any)
			assert.Equal(t, "3", contract.Parameters["duration"].Default, "preserve the unavailable supplier default instead of inventing a replacement")
			assert.Equal(t, []any{"4", "6", "8", "10"}, duration["enum"])
			assert.NotContains(t, duration, "default", "an unavailable native default is not offered as an executable choice")
		case "omni-flash-1-2":
			assert.Equal(t, 3, *contract.Parameters["videos"].MaxItems)
			assert.Equal(t, 3, contract.Parameters["videos"].MaxItemsByMode["reference"])
			assert.Equal(t, 1, contract.Parameters["videos"].MaxItemsByMode["edit"])
			assert.Equal(t, 1, contract.Parameters["videos"].MaxItemsByMode["extend"])
		case "pixverse-c1":
			assert.Equal(t, []any{"3", "5", "6", "8", "9", "12", "15"}, properties["duration"].(map[string]any)["enum"])
			assert.Equal(t, "3", properties["duration"].(map[string]any)["default"])
		}
		data, err := common.Marshal(map[string]any{"model": contract.ID, "synthetic": true, "contract": contract, "schema": schema, "asset_fields": fields, "intents": MediaContractIntents(contract), "material_conditions": MediaContractMaterialConditions(contract), "parameter_conditions": MediaContractParameterConditions(contract), "execution_qualified": false})
		require.NoError(t, err)
		t.Logf("YC_SIX_CATALOG_FIXTURE %s", data)
	}
	type driftCase struct {
		name, model                  string
		params                       map[string]any
		intent                       MediaRuntimeIntent
		quoteFailure, runtimeFailure string
		expected                     map[string]any
	}
	cases := []driftCase{
		{"MJ newly permitted 1025 runes", "mj_imagine", map[string]any{"prompt": strings.Repeat("字", 1025)}, MediaIntentText, "", "", nil},
		{"MJ exact 1980 Unicode runes", "mj_imagine", map[string]any{"prompt": strings.Repeat("字", 1980)}, MediaIntentText, "", "", nil},
		{"MJ 1981 Unicode runes rejected", "mj_imagine", map[string]any{"prompt": strings.Repeat("字", 1981)}, MediaIntentText, "prompt", "prompt", nil},
		{"Magi one reference video retained", "magi-2", map[string]any{"prompt": "Follow this reference", "mode": "reference", "videos": videos[:1]}, MediaIntentReference, "", "", nil},
		{"Magi second reference video rejected", "magi-2", map[string]any{"prompt": "Follow these references", "mode": "reference", "videos": videos[:2]}, MediaIntentReference, "too many source", "too many source", nil},
		{"Magi special default is twelve", "magi-2-special", map[string]any{"prompt": "A calm shot", "mode": "text-to-video"}, MediaIntentText, "", "", map[string]any{"duration": "12"}},
		{"Magi special first frame retained", "magi-2-special", map[string]any{"prompt": "Animate this frame", "mode": "first-frame", "images": images[:1]}, MediaIntentFrames, "", "", nil},
		{"Magi special reference two images retained", "magi-2-special", map[string]any{"prompt": "Use both references", "mode": "reference", "images": images[:2]}, MediaIntentReference, "", "", nil},
		{"Magi special reference three images retained", "magi-2-special", map[string]any{"prompt": "Use all three references", "mode": "reference", "images": images[:3]}, MediaIntentReference, "", "", nil},
		{"Magi special fourth image rejected", "magi-2-special", map[string]any{"prompt": "Use all references", "mode": "reference", "images": images}, MediaIntentReference, "too many source", "too many source", nil},
		{"Magi special reference single image stays forbidden", "magi-2-special", map[string]any{"prompt": "Keep reference intent", "mode": "reference", "images": images[:1]}, MediaIntentReference, "single source image", "single source image", nil},
		{"Magi special empty reference cannot fall back to text", "magi-2-special", map[string]any{"prompt": "Keep reference intent", "mode": "reference"}, MediaIntentReference, "", "text fallback is forbidden", nil},
		{"Magi special removed video rejected", "magi-2-special", map[string]any{"prompt": "Keep original material", "mode": "reference", "videos": videos[:1]}, MediaIntentReference, "field is not declared", "field is not declared", nil},
		{"Magi special removed audio rejected", "magi-2-special", map[string]any{"prompt": "Keep original material", "mode": "reference", "audios": []string{"asset:drift-audio"}}, MediaIntentReference, "field is not declared", "field is not declared", nil},
		{"Magi special first frame cannot take two images", "magi-2-special", map[string]any{"prompt": "Animate", "mode": "first-frame", "images": images[:2]}, MediaIntentFrames, "too many source", "too many source", nil},
		{"Veo unavailable default rejects omission", "veo-4-omni", map[string]any{"prompt": "A calm shot", "mode": "text-to-video"}, MediaIntentText, "duration", "duration", nil},
		{"Veo explicit frame and duration preserve intent", "veo-4-omni", map[string]any{"prompt": "Animate this frame", "mode": "first-frame", "duration": "4", "images": images[:1]}, MediaIntentFrames, "", "", nil},
		{"Veo frame cannot be relabeled text", "veo-4-omni", map[string]any{"prompt": "Animate this frame", "mode": "first-frame", "duration": "4", "images": images[:1]}, MediaIntentText, "", "intent", nil},
		{"Omni three ordered reference videos retained", "omni-flash-1-2", map[string]any{"prompt": "Preserve each motion", "mode": "reference", "videos": videos[:3]}, MediaIntentReference, "", "", nil},
		{"Omni fourth reference video rejected", "omni-flash-1-2", map[string]any{"prompt": "Preserve each motion", "mode": "reference", "videos": videos}, MediaIntentReference, "too many source", "too many source", nil},
		{"Omni edit still limited to one source", "omni-flash-1-2", map[string]any{"prompt": "Change the background", "mode": "edit", "videos": videos[:2]}, MediaIntentTransform, "too many source", "too many source", nil},
		{"Omni extend still limited to one source", "omni-flash-1-2", map[string]any{"prompt": "Continue the motion", "mode": "extend", "videos": videos[:2]}, MediaIntentTransform, "too many source", "too many source", nil},
		{"Omni reference video cannot become text", "omni-flash-1-2", map[string]any{"prompt": "Preserve the motion", "mode": "reference", "videos": videos[:1]}, MediaIntentText, "", "intent", nil},
		{"Pixverse default three retains first frame", "pixverse-c1", map[string]any{"prompt": "Animate the frame", "mode": "first-frame", "images": images[:1]}, MediaIntentFrames, "", "", map[string]any{"duration": "3"}},
		{"Pixverse second frame rejected", "pixverse-c1", map[string]any{"prompt": "Animate the frame", "mode": "first-frame", "images": images[:2]}, MediaIntentFrames, "too many source", "too many source", nil},
	}
	for _, duration := range []string{"4", "12", "13", "25", "26"} {
		failure := ""
		if duration == "13" || duration == "25" || duration == "26" {
			failure = "duration"
		}
		cases = append(cases, driftCase{"Magi special duration " + duration, "magi-2-special", map[string]any{"prompt": "A calm shot", "mode": "text-to-video", "duration": duration}, MediaIntentText, failure, failure, nil})
	}
	for _, duration := range []string{"3", "4", "5", "6", "7", "8", "9", "10"} {
		failure := ""
		if duration == "3" || duration == "5" || duration == "7" || duration == "9" {
			failure = "duration"
		}
		cases = append(cases, driftCase{"Veo explicit duration " + duration, "veo-4-omni", map[string]any{"prompt": "A calm shot", "mode": "text-to-video", "duration": duration}, MediaIntentText, failure, failure, nil})
	}
	for _, duration := range []string{"3", "5", "6", "8", "9", "12", "15", "4", "7", "16"} {
		failure := ""
		if duration == "4" || duration == "7" || duration == "16" {
			failure = "duration"
		}
		cases = append(cases, driftCase{"Pixverse duration " + duration, "pixverse-c1", map[string]any{"prompt": "A calm shot", "mode": "text-to-video", "duration": duration}, MediaIntentText, failure, failure, nil})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, err := common.Marshal(tc.params)
			require.NoError(t, err)
			normalized, quoteErr := ValidateMediaContractParams(tc.model, tc.params, assets)
			wire, runtimeErr := BuildMediaRuntimeRequest(tc.model, tc.params, tc.model+"@synthetic-local-only", "task-drift6-original-uuid", tc.intent)
			if tc.quoteFailure == "" {
				require.NoError(t, quoteErr)
			} else {
				require.ErrorContains(t, quoteErr, tc.quoteFailure)
			}
			if tc.runtimeFailure == "" {
				require.NoError(t, runtimeErr)
				for name, expected := range tc.expected {
					assert.Equal(t, expected, normalized[name])
					assert.Equal(t, expected, wire.Params[name])
				}
				for _, name := range []string{"mode", "duration", "prompt", "images", "videos", "audios"} {
					if original, supplied := tc.params[name]; supplied {
						assert.Equal(t, original, wire.Params[name], "native request retains original %s and asset order", name)
					}
				}
			} else {
				require.ErrorContains(t, runtimeErr, tc.runtimeFailure)
			}
			after, err := common.Marshal(tc.params)
			require.NoError(t, err)
			assert.Equal(t, before, after, "validation never rewrites the original request or drops source references")
			row := map[string]any{"name": tc.name, "model": tc.model, "synthetic": true, "params": tc.params, "intent": tc.intent, "asset_metadata": assets, "normalized": normalized, "quote_valid": quoteErr == nil, "runtime_valid": runtimeErr == nil, "runtime_request": wire, "request_bytes_unchanged": string(before) == string(after), "expected_quote_failure": tc.quoteFailure, "expected_runtime_failure": tc.runtimeFailure}
			if quoteErr != nil {
				row["quote_error"] = quoteErr.Error()
			}
			if runtimeErr != nil {
				row["runtime_error"] = runtimeErr.Error()
			}
			data, err := common.Marshal(row)
			require.NoError(t, err)
			t.Logf("YC_SIX_VALIDATION_FIXTURE %s", data)
		})
	}
}
