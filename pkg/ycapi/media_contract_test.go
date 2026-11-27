package ycapi

import (
	"fmt"
	"strings"
	"testing"

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
		{"Omni second reference video is rejected", "omni-flash-1-2", map[string]any{"prompt": "Follow the motion", "mode": "reference", "videos": videos[:2]}, MediaIntentReference, "too many source", nil},
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
		{"Magi reference videos count is one despite generic max three", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "videos": videos[:2]}, MediaIntentReference, "too many source", nil},
		{"Magi reference audio is an input not audio output", "magi-2", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "", nil},
		{"Magi special single image requires explicit first frame", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:1]}, MediaIntentReference, "single source image", nil},
		{"Magi special single image plus video must not switch intent", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:1], "videos": videos[:1]}, MediaIntentReference, "single source image", nil},
		{"Magi special explicit first frame", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "first-frame", "images": images[:1]}, MediaIntentFrames, "", nil},
		{"Magi special two images remain reference", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:2]}, MediaIntentReference, "", nil},
		{"Magi special reference video with zero images", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "videos": videos[:1]}, MediaIntentReference, "", nil},
		{"Magi special reference audio with zero images", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:1]}, MediaIntentReference, "", nil},
		{"Magi special combined ten", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:8], "videos": videos[:1], "audios": audios[:1]}, MediaIntentReference, "", nil},
		{"Magi special combined eleven rejected", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "images": images[:9], "videos": videos[:1], "audios": audios[:1]}, MediaIntentReference, "combined source asset count", nil},
		{"Magi special second audio rejected", "magi-2-special", map[string]any{"prompt": "A shot", "mode": "reference", "audios": audios[:2]}, MediaIntentReference, "too many source", nil},
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
