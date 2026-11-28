package plugins_test

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newYCAPIMediaV2Plugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("ycapi")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "ycapi"})
	require.NoError(t, err)
	return plugin
}

func TestYCAPILegacyAcceptedOutputsKeepFrozenFactsAndOriginalAlias(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	for _, kind := range []string{"image", "video"} {
		modelID := "ycapi-flux-kontext"
		if kind == "video" {
			modelID = "ycapi-grok-imagine-video"
		}
		for _, shape := range []string{"valid", "mixed invalid", "more than 64"} {
			t.Run(kind+"/"+shape, func(t *testing.T) {
				firstURL := "https://cdn.invalid/original." + map[string]string{"image": "png", "video": "mp4"}[kind]
				outputs := []any{firstURL}
				switch shape {
				case "mixed invalid":
					outputs = []any{"not-an-https-url", firstURL, "also-invalid"}
				case "more than 64":
					for index := 1; index < 65; index++ {
						outputs = append(outputs, fmt.Sprintf("https://cdn.invalid/extra-%d", index))
					}
				}
				facts := map[string]any{"spec": "frozen-historical-offer-no-longer-listed"}
				if kind == "image" {
					facts["image_count"] = 1
				} else {
					facts["seconds"] = 15
				}
				// This is the persisted 1.0.3 state shape. There is deliberately
				// no requestBody to normalize, nor a made-up query producerVersion.
				state := map[string]any{"facts": facts, "kind": kind, "snapshot": map[string]any{
					"id": "original-native-task", "kind": kind, "status": "pending", "is_final": false, "outputs": []any{},
				}}
				queryCtx := map[string]any{"model": modelID, "upstreamModel": modelID,
					"taskId": "original-native-task", "publicTaskId": "task_original",
					"state": state, "baseUrl": "https://ycapi.invalid", "apiKey": "synthetic-key"}
				query := codebloomHook(t, plugin, "buildQueryRequest", queryCtx)
				assert.Equal(t, "GET", query["method"])
				path := "/api/v1/model-runtime/tasks/original-native-task"
				if kind == "video" {
					path = "/v1/tasks/original-native-task"
				}
				assert.Equal(t, "https://ycapi.invalid"+path, query["url"])
				final := codebloomHook(t, plugin, "parseTaskResult", queryCtx, map[string]any{
					"task_id": "original-native-task", "status": "completed", "is_final": true, "outputs": outputs,
				})
				assert.Equal(t, "SUCCESS", final["status"])
				finalState := codebloomObject(t, final["state"])
				assert.Equal(t, codebloomObject(t, facts), codebloomObject(t, finalState["facts"]))
				assert.NotContains(t, finalState, "legacy_outputs_policy_version", "polling must not reclassify an old accepted task")
				assert.Equal(t, []any{firstURL}, codebloomObject(t, finalState["snapshot"])["outputs"])
				usage := codebloomHook(t, plugin, "extractUsageOnComplete", queryCtx, final, map[string]any{})
				assert.Empty(t, usage, "the original frozen image/video billing facts remain authoritative")
				value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{
					"status": "SUCCESS", "producerVersion": "1.0.3", "state": finalState,
				})
				require.NoError(t, err)
				artifacts := value.([]any)
				require.Len(t, artifacts, 1)
				assert.Equal(t, kind, codebloomObject(t, artifacts[0])["key"])
				content := codebloomHook(t, plugin, "buildContentRequest", map[string]any{
					"state": finalState, "producerVersion": "1.0.3", "artifactKey": kind,
					"clientRequest": map[string]any{"method": "HEAD"},
				})
				assert.Equal(t, firstURL, content["url"])
				assert.Equal(t, "HEAD", content["method"])
				assert.Equal(t, true, content["credentialless"])
			})
		}
	}
}

func TestYCAPINewLegacyOutputsPolicyRetainsAllAndRejectsMalformedSets(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	submitCtx := map[string]any{"model": "ycapi-flux-kontext", "upstreamModel": "ycapi-flux-kontext", "publicTaskId": "task_new_legacy",
		"requestBody": map[string]any{"model": "ycapi-flux-kontext", "prompt": "A synthetic scene", "quality": "max", "size": "1:1", "n": 1}}
	parsed := codebloomHook(t, plugin, "parseSubmitResponse", submitCtx, map[string]any{"body": map[string]any{
		"task_id": "new-legacy-native", "status": "pending",
	}})
	state := codebloomObject(t, parsed["state"])
	assert.EqualValues(t, 2, state["legacy_outputs_policy_version"])
	queryCtx := map[string]any{"model": "ycapi-flux-kontext", "upstreamModel": "ycapi-flux-kontext", "taskId": "new-legacy-native", "state": state}
	urls := []any{"https://cdn.invalid/first.png", "https://cdn.invalid/second.png"}
	final := codebloomHook(t, plugin, "parseTaskResult", queryCtx, map[string]any{
		"task_id": "new-legacy-native", "status": "completed", "is_final": true, "outputs": urls,
	})
	finalState := codebloomObject(t, final["state"])
	assert.EqualValues(t, 2, finalState["legacy_outputs_policy_version"])
	assert.Equal(t, urls, codebloomObject(t, finalState["snapshot"])["outputs"])
	value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "producerVersion": "1.0.4", "state": finalState})
	require.NoError(t, err)
	artifacts := value.([]any)
	require.Len(t, artifacts, 2)
	assert.Equal(t, "image-1", codebloomObject(t, artifacts[0])["key"])
	assert.Equal(t, "image-2", codebloomObject(t, artifacts[1])["key"])
	content := codebloomHook(t, plugin, "buildContentRequest", map[string]any{
		"state": finalState, "artifactKey": "image-2", "clientRequest": map[string]any{"method": "HEAD"},
	})
	assert.Equal(t, urls[1], content["url"])
	oversized := make([]any, 65)
	for index := range oversized {
		oversized[index] = fmt.Sprintf("https://cdn.invalid/output-%d.png", index)
	}
	for _, test := range []struct {
		name    string
		outputs []any
	}{
		{"mixed invalid", []any{urls[0], "not-an-https-url"}},
		{"more than 64", oversized},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"task_id": "new-legacy-native", "status": "completed", "is_final": true, "outputs": test.outputs}
			_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", submitCtx, map[string]any{"body": body})
			require.Error(t, err, "new submission parsing must retain strict output validation")
			_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", queryCtx, body)
			require.Error(t, err, "new accepted tasks must not fall back to old output parsing")
		})
	}
	invalidState := map[string]any{"legacy_outputs_policy_version": 3, "kind": "image"}
	_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{
		"model": "ycapi-flux-kontext", "upstreamModel": "ycapi-flux-kontext", "taskId": "new-legacy-native", "state": invalidState,
	}, map[string]any{"task_id": "new-legacy-native", "status": "completed", "is_final": true, "outputs": urls})
	require.Error(t, err, "an unknown retained output policy must fail closed")
}

func TestYCAPICachedVideoRendererUsesReal502TaskViewWithoutProducerFields(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	firstURL := "https://cdn.invalid/original.mp4"
	oversized := []any{firstURL}
	for index := 1; index < 65; index++ {
		oversized = append(oversized, fmt.Sprintf("https://cdn.invalid/extra-%d.mp4", index))
	}
	for _, outputs := range [][]any{{firstURL}, {"not-an-https-url", firstURL}, oversized} {
		// ConvertToOpenAIVideo on 502 provides this renderer context and a
		// public TaskView with raw data, not plugin state or producerVersion.
		value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "render"},
			map[string]any{"protocol": "openai_video", "operation": "retrieve"},
			map[string]any{"task_id": "task_original", "status": "SUCCESS", "data": map[string]any{"outputs": outputs}})
		require.NoError(t, err)
		assert.Equal(t, firstURL, codebloomObject(t, value)["url"])
	}
}

func TestYCAPICachedUnmarkedMultipleArtifactsKeepTheirIndexedKeys(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) {
			urls := []any{"https://cdn.invalid/one", "https://cdn.invalid/two"}
			state := map[string]any{"kind": kind, "snapshot": map[string]any{"kind": kind, "outputs": urls}}
			value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "state": state, "producerVersion": ""})
			require.NoError(t, err)
			artifacts := value.([]any)
			require.Len(t, artifacts, 2, "cached multi-output QA history must not be truncated during projection")
			assert.Equal(t, kind+"-1", codebloomObject(t, artifacts[0])["key"])
			assert.Equal(t, kind+"-2", codebloomObject(t, artifacts[1])["key"])
			content := codebloomHook(t, plugin, "buildContentRequest", map[string]any{
				"state": state, "artifactKey": kind + "-2", "clientRequest": map[string]any{"method": "HEAD"},
			})
			assert.Equal(t, urls[1], content["url"])
		})
	}
}

func TestYCAPIMediaV2OutputGuardsCannotFallBackToLegacyCompatibility(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	parsed := codebloomHook(t, plugin, "parseSubmitResponse", ycapiV2FixtureContext(), map[string]any{"body": map[string]any{
		"task_id": "native-v2-task", "status": "pending",
	}})
	queryCtx := map[string]any{"model": "ycapi-gpt-image-2.6-flare", "taskId": "native-v2-task", "state": parsed["state"]}
	oversized := make([]any, 65)
	for index := range oversized {
		oversized[index] = map[string]any{"url": fmt.Sprintf("https://cdn.invalid/%d.png", index), "type": "image"}
	}
	for _, outputs := range [][]any{
		{map[string]any{"url": "https://cdn.invalid/valid.png", "type": "image"}, map[string]any{"url": "not-an-https-url", "type": "image"}},
		oversized,
	} {
		_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", queryCtx, map[string]any{
			"task_id": "native-v2-task", "status": "completed", "is_final": true, "data": outputs,
		})
		require.Error(t, err)
	}
}

func TestYCAPIMediaV2BatchKeepsOriginalRolesKeysAndRetainedPolicy(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	batch := map[string]any{"schema_version": 1, "requested_output_count": 2, "expected_main_output_count": 2,
		"meter": "output_count", "output_duration_seconds": 5, "total_output_seconds": 10,
		"admission_unit": "native_operation", "admission_weight": 1,
		"partial_policy": "retain_reservation_until_complete", "policy_revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	state := map[string]any{"mediaGenerationV2": true, "gatewayModel": "ycapi-kling-v4", "nativeModel": "kling-v4", "kind": "video",
		"batch": batch, "snapshot": map[string]any{"id": "native-batch-task"}}
	final := codebloomHook(t, plugin, "parseTaskResult", map[string]any{"model": "ycapi-kling-v4", "taskId": "native-batch-task", "state": state}, map[string]any{
		"task_id": "native-batch-task", "status": "completed", "is_final": true,
		"data": []any{
			map[string]any{"url": "https://cdn.invalid/one.mp4", "type": "video", "output_id": "vendor-one", "output_index": 0},
			map[string]any{"url": "https://cdn.invalid/two.mp4", "type": "video", "output_id": "vendor-two", "output_index": 1},
		},
	})
	assert.Equal(t, "SUCCESS", final["status"])
	finalState := codebloomObject(t, final["state"])
	assert.Equal(t, codebloomObject(t, batch), codebloomObject(t, finalState["batch"]))
	snapshot := codebloomObject(t, finalState["snapshot"])
	assert.Equal(t, "complete", codebloomObject(t, snapshot["batch"])["completeness"])
	retained := snapshot["artifacts"].([]any)
	require.Len(t, retained, 2)
	for index, expected := range []string{"video-1", "video-2"} {
		artifact := codebloomObject(t, retained[index])
		assert.Equal(t, expected, artifact["key"])
		assert.Equal(t, "main", artifact["role"])
		assert.Equal(t, fmt.Sprintf("main-%d", index+1), artifact["output_id"])
	}
	value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "state": finalState})
	require.NoError(t, err)
	artifacts := value.([]any)
	require.Len(t, artifacts, 2)
	assert.Equal(t, "video-1", codebloomObject(t, artifacts[0])["key"])
	assert.Equal(t, "video-2", codebloomObject(t, artifacts[1])["key"])
	content := codebloomHook(t, plugin, "buildContentRequest", map[string]any{
		"state": finalState, "artifactKey": "video-2", "clientRequest": map[string]any{"method": "HEAD"},
	})
	assert.Equal(t, "https://cdn.invalid/two.mp4", content["url"])
}

func ycapiV2FixtureContext() map[string]any {
	return map[string]any{"model": "ycapi-gpt-image-2.6-flare", "upstreamModel": "gpt-image-2.6-flare", "publicTaskId": "task_gateway_v2",
		"baseUrl": "https://ycapi.invalid", "apiKey": "synthetic-vendor-secret", "requestBody": map[string]any{"prompt": "untrusted body is not the native envelope"},
		"mediaGenerationV2": map[string]any{"kind": "image", "retailCny": "0.2",
			"nativeRequest": map[string]any{"model_id": "gpt-image-2.6-flare", "modality": "image", "operation": "generate", "params": map[string]any{"prompt": "native validated prompt", "count": 2, "output_format": "png"}, "lane_id": "fixture:lane", "client_request_id": "task_gateway_v2"},
			"usageFacts":    map[string]any{"image_count": 2, "input_video_seconds": 0, "input_audio_seconds": 0, "input_image_count": 0, "generate_audio": false, "draft": false, "intent": "text", "spec": "media-v2", "resolution": "1K", "quality": "low", "mode": "text-to-image", "aspect_ratio": "1:1"}}}
}

func TestYCAPIMediaV2PrivateEnvelopeAndUsageBinding(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	assert.Equal(t, "1.0.4", plugin.Meta.Version)
	assert.Len(t, plugin.Meta.Models, 78, "75 media contracts union the unchanged three legacy audio cards")
	usageSchema, _ := plugin.Meta.UsageForModel("ycapi-gpt-image-2.6-flare")
	assert.Equal(t, "boolean", usageSchema["generate_audio"].Type)
	assert.Equal(t, "boolean", usageSchema["draft"].Type)
	ctx := ycapiV2FixtureContext()
	wire := codebloomHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, "https://ycapi.invalid/api/v1/model-runtime/invoke", wire["url"])
	assert.Equal(t, codebloomObject(t, ctx["mediaGenerationV2"])["nativeRequest"], wire["body"])
	facts := codebloomHook(t, plugin, "extractUsage", ctx)
	assert.Equal(t, codebloomObject(t, codebloomObject(t, ctx["mediaGenerationV2"])["usageFacts"]), facts)
	structContext := ycapiV2FixtureContext()
	structContext["mediaGenerationV2"].(map[string]any)["nativeRequest"] = ycapi.MediaRuntimeRequest{
		ModelID: "gpt-image-2.6-flare", Modality: "image", Operation: "generate", Params: map[string]any{"prompt": "native validated prompt", "count": 2, "output_format": "png"},
		LaneID: "fixture:lane", ClientRequestID: "task_gateway_v2",
	}
	structWire := codebloomHook(t, plugin, "buildSubmitRequest", structContext)
	assert.Equal(t, wire["body"], structWire["body"], "the Host's typed envelope must honor its JSON field names")
	private := ctx["mediaGenerationV2"].(map[string]any)
	request := private["nativeRequest"].(map[string]any)
	request["client_request_id"] = "different-task"
	_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "task identity mismatch")
	ctx = ycapiV2FixtureContext()
	delete(ctx["mediaGenerationV2"].(map[string]any)["usageFacts"].(map[string]any), "input_image_count")
	_, err = plugin.Engine.Call(t.Context(), "extractUsage", ctx)
	require.ErrorContains(t, err, "usage facts")
}

func TestYCAPIMediaV2RejectsRequestBodyContextForgery(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	ctx := ycapiV2FixtureContext()
	fake := ctx["mediaGenerationV2"]
	delete(ctx, "mediaGenerationV2")
	ctx["requestBody"] = map[string]any{"mediaGenerationV2": fake, "nativeRequest": fake}
	_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.Error(t, err, "a new model cannot enter native runtime through public body fields")
	_, err = plugin.Engine.Call(t.Context(), "extractUsage", ctx)
	require.Error(t, err)
	ctx["model"], ctx["upstreamModel"] = "ycapi-flux-kontext", "ycapi-flux-kontext"
	_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "Unsupported generation parameter")
}

func TestYCAPIMediaV2VideoOmitsImageOperationAndFreezesReferenceQuantities(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	ctx := ycapiV2FixtureContext()
	ctx["model"], ctx["upstreamModel"] = "ycapi-kling-v4", "kling-v4"
	private := ctx["mediaGenerationV2"].(map[string]any)
	private["kind"] = "video"
	private["nativeRequest"] = ycapi.MediaRuntimeRequest{ModelID: "kling-v4", Modality: "video", LaneID: "fixture:lane", ClientRequestID: "task_gateway_v2",
		Params: map[string]any{"prompt": "A shot", "mode": "reference", "quality": "standard", "resolution": "720p", "duration": "5", "generate_audio": true, "images": []string{"asset:first"}, "videos": []string{"asset:motion"}}}
	facts := map[string]any{"seconds": 5, "input_video_seconds": 3.5, "input_audio_seconds": 0, "input_image_count": 1,
		"generate_audio": true, "draft": false,
		"intent": "reference", "spec": "media-v2", "resolution": "720p", "quality": "standard", "mode": "reference", "aspect_ratio": "16:9"}
	private["usageFacts"] = facts
	wire := codebloomHook(t, plugin, "buildSubmitRequest", ctx)
	assert.NotContains(t, codebloomObject(t, wire["body"]), "operation")
	expectedFacts := codebloomObject(t, facts)
	expectedFacts["output_count"], expectedFacts["total_output_seconds"] = float64(0), float64(0)
	assert.Equal(t, expectedFacts, codebloomHook(t, plugin, "extractUsage", ctx))
	assert.NotContains(t, facts, "output_count", "historical captured facts remain unchanged")
	assert.NotContains(t, facts, "total_output_seconds")
	request := codebloomObject(t, private["nativeRequest"])
	request["operation"] = "generate"
	private["nativeRequest"] = request
	_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "Invalid Host media operation")
}

func TestYCAPIMediaV2BooleanFactsStayTypedAndHostErrorsCannotFallBack(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	ctx := ycapiV2FixtureContext()
	ctx["model"], ctx["upstreamModel"] = "ycapi-seedance-2-5", "seedance-2-5"
	private := ctx["mediaGenerationV2"].(map[string]any)
	private["kind"] = "video"
	private["nativeRequest"] = ycapi.MediaRuntimeRequest{ModelID: "seedance-2-5", Modality: "video", LaneID: "fixture:lane", ClientRequestID: "task_gateway_v2",
		Params: map[string]any{"prompt": "A draft shot", "mode": "text-to-video", "resolution": "720p", "duration": "5", "draft": true}}
	facts := map[string]any{"seconds": 5, "input_video_seconds": 0, "input_audio_seconds": 0, "input_image_count": 0,
		"generate_audio": false, "draft": true, "intent": "text", "spec": "media-v2", "resolution": "720p", "quality": "", "mode": "text-to-video", "aspect_ratio": "16:9"}
	private["usageFacts"] = facts
	wire := codebloomHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, true, codebloomObject(t, codebloomObject(t, wire["body"])["params"])["draft"])
	expectedFacts := codebloomObject(t, facts)
	expectedFacts["output_count"], expectedFacts["total_output_seconds"] = float64(0), float64(0)
	assert.Equal(t, expectedFacts, codebloomHook(t, plugin, "extractUsage", ctx))
	assert.NotContains(t, facts, "output_count", "historical captured facts remain unchanged")
	assert.NotContains(t, facts, "total_output_seconds")
	for _, key := range []string{"generate_audio", "draft"} {
		original := facts[key]
		for _, invalid := range []any{"true", "false", 0, 1, nil} {
			facts[key] = invalid
			_, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
			require.ErrorContains(t, err, "usage facts")
		}
		delete(facts, key)
		_, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
		require.ErrorContains(t, err, "usage facts")
		facts[key] = original
	}
	ctx["mediaGenerationV2Error"] = true
	_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "Invalid Host media generation context")
}

func TestYCAPIMediaV2RetainsAllOrderedMixedArtifactsAndOriginalTask(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	ctx := ycapiV2FixtureContext()
	body := map[string]any{"task_id": "native-v2-task", "model": map[string]any{"public_model": "gt-image-2.6-flare"}, "status": "completed", "is_final": true,
		"output_url": "https://cdn.invalid/first.png", "billing": map[string]any{"secret": "not-public-billing"},
		"data": []any{
			map[string]any{"url": "https://cdn.invalid/first.png", "type": "image", "mime_type": "image/png"},
			map[string]any{"url": "https://cdn.invalid/layer.svg", "type": "image", "mime_type": "image/svg+xml"},
			map[string]any{"url": "https://cdn.invalid/clip.mp4", "type": "video", "mime_type": "video/mp4"},
			map[string]any{"url": "https://cdn.invalid/audio.mp3", "type": "audio", "mime_type": "audio/mpeg"},
		}}
	parsed := codebloomHook(t, plugin, "parseSubmitResponse", ctx, map[string]any{"body": body, "statusCode": 200})
	assert.Equal(t, "native-v2-task", parsed["taskId"])
	assert.NotContains(t, parsed, "immediate", "persist the original task and recovery state before declaring completion")
	queryCtx := map[string]any{"model": ctx["model"], "taskId": parsed["taskId"], "state": parsed["state"], "baseUrl": ctx["baseUrl"], "apiKey": ctx["apiKey"]}
	query := codebloomHook(t, plugin, "buildQueryRequest", queryCtx)
	assert.Equal(t, "https://ycapi.invalid/v1/tasks/native-v2-task", query["url"])
	final := codebloomHook(t, plugin, "parseTaskResult", queryCtx, body)
	assert.Equal(t, "SUCCESS", final["status"])
	value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "state": final["state"]})
	require.NoError(t, err)
	artifacts := value.([]any)
	require.Len(t, artifacts, 4)
	for index, expected := range []string{"image-1", "file-2", "video-3", "audio-4"} {
		assert.Equal(t, expected, codebloomObject(t, artifacts[index])["key"])
	}
	assert.Equal(t, "image/svg+xml", codebloomObject(t, artifacts[1])["mimeType"])
	content := codebloomHook(t, plugin, "buildContentRequest", map[string]any{"state": final["state"], "artifactKey": "file-2", "clientRequest": map[string]any{"method": "HEAD"}})
	assert.Equal(t, "https://cdn.invalid/layer.svg", content["url"])
	assert.Equal(t, true, content["credentialless"])
	assert.NotContains(t, content, "headers")
	rendered, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "render"}, map[string]any{"artifacts": map[string]any{
		"audio-4": map[string]any{"url": "/v1/tasks/task_gateway_v2/artifacts/audio-4/content"}, "video-3": map[string]any{"url": "/v1/tasks/task_gateway_v2/artifacts/video-3/content"},
		"file-2": map[string]any{"url": "/v1/tasks/task_gateway_v2/artifacts/file-2/content"}, "image-1": map[string]any{"url": "/v1/tasks/task_gateway_v2/artifacts/image-1/content"},
	}}, map[string]any{})
	require.NoError(t, err)
	renderedData := codebloomObject(t, rendered)["data"].([]any)
	require.Len(t, renderedData, 4)
	for index, expected := range []string{"image-1", "file-2", "video-3", "audio-4"} {
		assert.Equal(t, "/v1/tasks/task_gateway_v2/artifacts/"+expected+"/content", codebloomObject(t, renderedData[index])["url"])
	}
	encoded, err := common.Marshal(parsed)
	require.NoError(t, err)
	for _, private := range []string{"not-public-billing", "fixture:lane", "synthetic-vendor-secret"} {
		assert.NotContains(t, string(encoded), private)
	}
	queryCtx["taskId"] = "other-task"
	_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", queryCtx, body)
	require.ErrorContains(t, err, "identity mismatch")
}

func TestYCAPIMediaV2RejectsOversizedOutputSetsAndWaitsForFinality(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	ctx := ycapiV2FixtureContext()
	parsed := codebloomHook(t, plugin, "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"task_id": "native-v2-task", "status": "pending"}})
	query := map[string]any{"model": ctx["model"], "taskId": "native-v2-task", "state": parsed["state"]}
	review := codebloomHook(t, plugin, "parseTaskResult", query, map[string]any{"task_id": "native-v2-task", "status": "needs_review", "is_final": false})
	assert.Equal(t, "IN_PROGRESS", review["status"])
	var outputs []any
	for index := 0; index < 65; index++ {
		outputs = append(outputs, map[string]any{"url": fmt.Sprintf("https://cdn.invalid/%d.png", index), "type": "image"})
	}
	_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", query, map[string]any{"task_id": "native-v2-task", "status": "completed", "is_final": true, "data": outputs})
	require.ErrorContains(t, err, "Too many upstream media outputs")
	missing := codebloomHook(t, plugin, "parseTaskResult", query, map[string]any{"task_id": "native-v2-task", "status": "completed", "is_final": true})
	assert.Equal(t, "UNKNOWN", missing["status"])
}

func TestYCAPIMediaV2KeepsLegacyQueryArtifactKeyAndOriginalOffer(t *testing.T) {
	plugin := newYCAPIMediaV2Plugin(t)
	model, ok := ycapi.Lookup("ycapi-flux-kontext")
	require.True(t, ok)
	ctx := map[string]any{"model": model.ID, "upstreamModel": model.ID, "publicTaskId": "task-old", "baseUrl": "https://ycapi.invalid", "apiKey": "synthetic-secret",
		"requestBody": map[string]any{"prompt": "A cat", "size": "1:1", "quality": "pro"}}
	wire := codebloomHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, "https://ycapi.invalid/api/v1/model-runtime/invoke", wire["url"])
	legacyFacts := codebloomHook(t, plugin, "extractUsage", ctx)
	assert.Equal(t, false, legacyFacts["generate_audio"])
	assert.Equal(t, false, legacyFacts["draft"])
	parsed := codebloomHook(t, plugin, "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"task_id": "native-legacy", "status": "pending"}})
	queryCtx := map[string]any{"model": model.ID, "upstreamModel": model.ID, "taskId": "native-legacy", "state": parsed["state"], "baseUrl": "https://ycapi.invalid", "apiKey": "synthetic-secret"}
	query := codebloomHook(t, plugin, "buildQueryRequest", queryCtx)
	assert.Equal(t, "https://ycapi.invalid/api/v1/model-runtime/tasks/native-legacy", query["url"])
	final := codebloomHook(t, plugin, "parseTaskResult", queryCtx, map[string]any{"task_id": "native-legacy", "status": "completed", "is_final": true, "outputs": []any{"https://cdn.invalid/old.png"}})
	value, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "state": final["state"]})
	require.NoError(t, err)
	artifacts := value.([]any)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "image", codebloomObject(t, artifacts[0])["key"])
	v2 := ycapiV2FixtureContext()
	v2["model"] = model.ID
	private := v2["mediaGenerationV2"].(map[string]any)
	request := private["nativeRequest"].(map[string]any)
	request["model_id"] = model.UpstreamID
	facts := private["usageFacts"].(map[string]any)
	facts["spec"], facts["resolution"], facts["quality"], facts["aspect_ratio"] = model.Offers[0].ID, "", "pro", "1:1"
	result := codebloomHook(t, plugin, "extractUsage", v2)
	assert.Equal(t, model.Offers[0].ID, result["spec"], "Host-selected old offer identity must not be overwritten by the sentinel")
}
