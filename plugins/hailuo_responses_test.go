package plugins_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHailuoResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "hailuo",
		model:     "MiniMax-Hailuo-2.3",
		requestBody: map[string]any{
			"model": "MiniMax-Hailuo-2.3",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "ocean at sunset"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 10,
			"size":    "1920x1080",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":    "MiniMax-Hailuo-2.3",
			"prompt":   "ocean at sunset",
			"images":   []any{"https://cdn.example/frame.png"},
			"duration": float64(10),
			"size":     "1920x1080",
			"metadata": map[string]any{"first_frame_image": "https://cdn.example/frame.png"},
		},
		wantUsageKeys:  []string{"input_images", "input_video_seconds", "resolution", "seconds"},
		wantVendorName: "hailuo",
	})
}

// Every model selects only the dimensions it can actually bill, while
// submission still reports the union so prices saved against the previous
// schema keep evaluating.
func TestHailuoUsageProfilesPerModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	families := []struct {
		models      []string
		fields      []string
		resolutions []string
	}{
		{
			models:      []string{"MiniMax-H3"},
			fields:      []string{"input_images", "input_video_seconds", "resolution", "seconds"},
			resolutions: []string{"768P", "2K"},
		},
		{
			models:      []string{"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"768P", "1080P"},
		},
		{
			models:      []string{"MiniMax-Hailuo-02"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"512P", "768P", "1080P"},
		},
		{
			models: []string{"T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01"},
			fields: []string{"seconds"},
		},
	}
	profiled := make([]string, 0, len(plugin.Meta.Models))
	for _, family := range families {
		for _, name := range family.models {
			profiled = append(profiled, name)
			t.Run(name, func(t *testing.T) {
				schema, examples := plugin.Meta.UsageForModel(name)
				assert.Equal(t, family.fields, slices.Sorted(maps.Keys(schema)))
				assert.Equal(t, family.resolutions, schema["resolution"].Enum)
				assert.NotEmpty(t, examples)
			})
		}
	}
	assert.ElementsMatch(t, plugin.Meta.Models, profiled, "every declared model must select a usage profile")

	t.Run("submission keeps facts outside the selected profile billable", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example", UpstreamModelName: "MiniMax-Hailuo-2.3"},
			OriginModelName: "MiniMax-Hailuo-2.3",
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
		}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "duration": 10, "resolution": "768P"})
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

		facts, err := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}, facts)
	})
}

// MiniMax serves v1 videos only through the signed download_url of
// /v1/files/retrieve; that link is signed for GET, so HEAD clients get GET too.
func TestHailuoArtifactContentProxy(t *testing.T) {
	service.InitHttpClient()
	const signedURL = "https://bucket.oss-cn-wulanchabu.aliyuncs.com/video.mp4?Expires=1&OSSAccessKeyId=key&Signature=sig"
	retrieveBodies := map[string]string{
		"file/with space": `{"file":{"file_id":1,"download_url":"` + signedURL + `"},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"login":           `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`,
		"missing":         `{"file":{"file_id":3},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"html":            `<html>gateway error</html>`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/files/retrieve", r.URL.Path)
		assert.Equal(t, "Bearer test-ak", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(retrieveBodies[r.URL.Query().Get("file_id")]))
	}))
	defer upstream.Close()
	adaptor := taskplugin.New(loadHailuoPlugin(t))
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: upstream.URL},
	})

	for _, tc := range []struct {
		fileID  string
		wantErr string
	}{
		{fileID: "file/with space"},
		{fileID: "login", wantErr: "MiniMax file retrieve failed: login fail"},
		{fileID: "missing", wantErr: "artifact_not_found"},
		{fileID: "html", wantErr: "MiniMax file retrieve returned an unexpected response: HTTP 200"},
	} {
		t.Run(tc.fileID, func(t *testing.T) {
			data, err := common.Marshal(map[string]any{"file_id": tc.fileID})
			require.NoError(t, err)
			task := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Data: data}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, &channel.TaskContentRequest{URL: signedURL, Method: http.MethodGet, Credentialless: true}, descriptor)
		})
	}
}

func loadHailuoPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	return plugin
}

func callHailuoHook(t *testing.T, plugin *jsplugin.LoadedPlugin, hook string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.Call(t.Context(), hook, args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	return decoded
}

func hailuoH3SubmitContext(requestBody map[string]any) map[string]any {
	return map[string]any{
		"requestBody":   requestBody,
		"model":         "MiniMax-H3",
		"upstreamModel": "MiniMax-H3",
		"baseUrl":       "https://api.minimax.example",
		"apiKey":        "test-ak",
	}
}

// MiniMax-H3 submits to /v2/video_generation with a multimodal content array
// instead of the flat /v1 frame fields.
func TestHailuoH3BuildSubmitRequest(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		request    map[string]any
		wantBody   string
		wantAction string
	}{
		{
			name:       "text to video defaults duration ratio and resolution",
			request:    map[string]any{"prompt": "a boy playing basketball"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"a boy playing basketball"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:       "2K resolution from size",
			request:    map[string]any{"prompt": "p", "duration": 15, "size": "2K"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"2K","duration":15,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:    "first and last frame from metadata",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"first_frame_image": "first.png", "last_frame_image": "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name:    "frames from the images array",
			request: map[string]any{"prompt": "p", "images": []any{"first.png", "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "reference video and audio",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{
				"reference_video": "ref.mp4",
				"reference_audio": []any{"a.mp3", "b.mp3"},
			}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"video_url","role":"reference_video","video_url":{"url":"ref.mp4"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"a.mp3"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"b.mp3"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough prepends the prompt when no text item exists",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "img.png"}},
			}}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"reference_image","image_url":{"url":"img.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough keeps an existing text item",
			request: map[string]any{"prompt": "ignored", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "kept"},
			}}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"kept"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name: "explicit ratio callback and watermark",
			request: map[string]any{"prompt": "p", "duration": 4, "metadata": map[string]any{
				"ratio":          "9:16",
				"callback_url":   "https://example.com/cb",
				"aigc_watermark": true,
			}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"768P","duration":4,"ratio":"9:16","callback_url":"https://example.com/cb","aigc_watermark":true}`,
			wantAction: "text_to_video",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(testCase.request))
			assert.Equal(t, "https://api.minimax.example/v2/video_generation", descriptor["url"])
			assert.Equal(t, "POST", descriptor["method"])
			assert.Equal(t, testCase.wantAction, descriptor["action"])
			body, err := common.Marshal(descriptor["body"])
			require.NoError(t, err)
			assert.JSONEq(t, testCase.wantBody, string(body))
		})
	}
}

// Every MiniMax-H3 request bound is rejected before the upstream call, so an
// out-of-range duration can never reach quota calculation as a billing fact.
func TestHailuoH3RejectsOutOfContractRequests(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	tenReferenceImages := make([]any, 0, 10)
	for range 10 {
		tenReferenceImages = append(tenReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "u"},
		})
	}
	testCases := []struct {
		name    string
		request map[string]any
		wantErr string
	}{
		{"duration below the minimum", map[string]any{"prompt": "p", "duration": 3}, "duration must be an integer between 4 and 15"},
		{"duration above the maximum", map[string]any{"prompt": "p", "duration": 16}, "duration must be an integer between 4 and 15"},
		{"fractional duration", map[string]any{"prompt": "p", "duration": 5.5}, "duration must be an integer between 4 and 15"},
		{"unsupported resolution", map[string]any{"prompt": "p", "size": "1080P"}, "resolution must be 768P or 2K"},
		{"unknown ratio", map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "16:10"}}, "ratio must be one of"},
		{"adaptive ratio without a visual input", map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "adaptive"}}, "ratio adaptive requires an image or video input"},
		{"too many frame images", map[string]any{"prompt": "p", "images": []any{"a.png", "b.png", "c.png"}}, "at most 2 frame images"},
		{"media without text", map[string]any{"images": []any{"a.png"}}, "requires a non-empty text item"},
		{"content is not an array", map[string]any{"prompt": "p", "metadata": map[string]any{"content": "nope"}}, "metadata.content must be an array"},
		{
			name: "multiple first frame roles",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u2"}},
			}}},
			wantErr: "at most one first_frame image",
		},
		{
			name: "passthrough mixes frame and reference media",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "frame"}},
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "reference"}},
			}}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "assembled content mixes frame and reference media",
			request: map[string]any{"prompt": "p", "images": []any{"frame"}, "metadata": map[string]any{
				"reference_video": "reference.mp4",
			}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "too many reference videos",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u2"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u3"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u4"}},
			}}},
			wantErr: "at most 3 reference videos",
		},
		{
			name:    "too many reference images",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": tenReferenceImages}},
			wantErr: "at most 9 reference images",
		},
		{
			name:    "too many reference audios",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"reference_audio": []any{"a.mp3", "b.mp3", "c.mp3", "d.mp3"}}},
			wantErr: "at most 3 reference audios",
		},
		{"empty input", map[string]any{"prompt": "  "}, "requires a prompt or a media input"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoH3SubmitContext(testCase.request))
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

// The /v1 models keep the flat request shape and endpoint.
func TestHailuoLegacySubmitRequestUnchanged(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	ctx := hailuoH3SubmitContext(map[string]any{"prompt": "p", "duration": 10, "size": "768P"})
	ctx["model"] = "MiniMax-Hailuo-2.3"
	ctx["upstreamModel"] = "MiniMax-Hailuo-2.3"
	descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, "https://api.minimax.example/v1/video_generation", descriptor["url"])
	body, err := common.Marshal(descriptor["body"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"MiniMax-Hailuo-2.3","prompt":"p","duration":10,"resolution":"768P"}`, string(body))
}

func TestHailuoQueryRequestEndpointByModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		ctx  map[string]any
		want string
	}{
		{
			name: "H3 uses the v2 path parameter",
			ctx:  map[string]any{"taskId": "task/1", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/task%2F1",
		},
		{
			name: "an unmapped task falls back to the origin model",
			ctx:  map[string]any{"taskId": "t1", "model": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
		{
			name: "legacy models keep the v1 query parameter",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "MiniMax-Hailuo-2.3"},
			want: "https://api.minimax.example/v1/query/video_generation?task_id=t1",
		},
		{
			name: "a channel-mapped alias resolves through the upstream model",
			ctx:  map[string]any{"taskId": "t1", "model": "h3", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.ctx["baseUrl"] = "https://api.minimax.example"
			testCase.ctx["apiKey"] = "test-ak"
			descriptor := callHailuoHook(t, plugin, "buildQueryRequest", testCase.ctx)
			assert.Equal(t, testCase.want, descriptor["url"])
			assert.Equal(t, "GET", descriptor["method"])
		})
	}
}

func TestHailuoParseTaskResult(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{"H3 queued", `{"task":{"id":"1","status":"queued"}}`, "QUEUED", "", ""},
		{"H3 running", `{"task":{"id":"1","status":"running"}}`, "IN_PROGRESS", "", ""},
		{"H3 succeeded", `{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`, "SUCCESS", "https://cdn.example/h3.mp4", ""},
		{"H3 failed", `{"task":{"id":"1","status":"failed","error":{"code":"1026","message":"sensitive content"}}}`, "FAILURE", "", "sensitive content"},
		{"H3 cancelled", `{"task":{"id":"1","status":"cancelled"}}`, "FAILURE", "", "task cancelled"},
		{"legacy success", `{"task_id":"1","status":"Success","file_id":"f1","base_resp":{"status_code":0}}`, "SUCCESS", "", ""},
		{"legacy processing", `{"task_id":"1","status":"Processing","base_resp":{"status_code":0}}`, "IN_PROGRESS", "", ""},
		{"H3 prototype constructor", `{"task":{"id":"1","status":"constructor"}}`, "UNKNOWN", "", "unrecognized status: constructor"},
		{"H3 prototype object", `{"task":{"id":"1","status":"__proto__"}}`, "UNKNOWN", "", "unrecognized status: __proto__"},
		{"legacy prototype function", `{"task_id":"1","status":"toString"}`, "UNKNOWN", "", "unrecognized status: toString"},
		{"H3 unrecognized", `{"task":{"id":"1","status":"weird"}}`, "UNKNOWN", "", "unrecognized status: weird"},
		{"legacy unrecognized", `{"task_id":"1","status":"Weird","base_resp":{"status_code":0}}`, "UNKNOWN", "", "unrecognized status: Weird"},
		{"legacy base_resp failure", `{"task_id":"1","status":"Success","base_resp":{"status_code":1001,"status_msg":"upstream down"}}`, "FAILURE", "", "upstream down"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			result := callHailuoHook(t, plugin, "parseTaskResult", map[string]any{}, body)
			assert.Equal(t, testCase.wantStatus, result["status"])
			assert.Equal(t, testCase.wantURL, common.Interface2String(result["url"]))
			assert.Equal(t, testCase.wantReason, common.Interface2String(result["reason"]))
		})
	}
	t.Run("H3 query errors never assert a task terminal state", func(t *testing.T) {
		for _, code := range []int{400, 401, 402, 403, 404, 429, 500} {
			_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{"model": "MiniMax-H3", "taskId": "1"}, map[string]any{"type": "error", "error": map[string]any{"message": "synthetic query failed", "http_code": code}})
			require.ErrorContains(t, err, "synthetic query failed")
		}
	})
	t.Run("H3 terminal observations require the pinned task object", func(t *testing.T) {
		ctx := map[string]any{"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "taskId": "expected"}
		for _, body := range []map[string]any{
			{"task": map[string]any{"id": "different", "status": "succeeded"}},
			{"task": map[string]any{"status": "failed"}},
			{"task_id": "expected", "status": "Success", "base_resp": map[string]any{"status_code": 0}},
			{"base_resp": map[string]any{"status_code": 1001, "status_msg": "query rejected"}},
		} {
			result := callHailuoHook(t, plugin, "parseTaskResult", ctx, body)
			assert.Equal(t, "UNKNOWN", result["status"])
		}
		for _, status := range []string{"succeeded", "failed", "cancelled"} {
			result := callHailuoHook(t, plugin, "parseTaskResult", ctx, map[string]any{"task": map[string]any{"id": "expected", "status": status}})
			assert.EqualValues(t, 0, result["code"])
			assert.Contains(t, []string{"SUCCESS", "FAILURE"}, result["status"])
		}
	})
	t.Run("H3 retryable query error", func(t *testing.T) {
		var body any
		require.NoError(t, common.UnmarshalJsonStr(
			`{"type":"error","error":{"type":"rate_limit_error","message":"retry later","http_code":"429"}}`, &body))
		_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
		require.ErrorContains(t, err, "retry later")
	})
}

func TestHailuoExtractUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	nineReferenceImages := make([]any, 0, 9)
	for range 9 {
		nineReferenceImages = append(nineReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "image"},
		})
	}
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		want    map[string]any
	}{
		{"H3 defaults", "MiniMax-H3", map[string]any{"prompt": "p"}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 2K", "MiniMax-H3", map[string]any{"prompt": "p", "duration": 12, "size": "2K"}, map[string]any{
			"seconds": float64(12), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 reference images", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{"content": nineReferenceImages}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(9), "input_video_seconds": float64(0),
		}},
		{"H3 reference video reserves total duration limit", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{
			"reference_video": []any{"one.mp4", "two.mp4", "three.mp4"},
		}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(15),
		}},
		{"legacy model", "MiniMax-Hailuo-2.3", map[string]any{"prompt": "p", "duration": 10}, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := hailuoH3SubmitContext(testCase.request)
			ctx["model"] = testCase.model
			ctx["upstreamModel"] = testCase.model
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsage", ctx))
		})
	}
}

func TestHailuoH3CompletionUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "actual usage replaces submission estimates",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
			want: map[string]any{"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5)},
		},
		{
			name: "zero actual usage is retained for settlement",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":4,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"seconds": float64(4), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "zero output cannot erase the submission reservation",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":0,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "missing usage leaves submission estimates untouched",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P"}}`,
			want: map[string]any{"resolution": "768P"},
		},
		{
			name: "booleans and arrays cannot erase reservation facts",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":[5],"input_seconds":false,"input_image_count":[]}}}`,
			want: map[string]any{"resolution": "2K"},
		},
		{
			name: "true and blank strings are not measured usage",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":" ","input_seconds":true,"input_image_count":" "}}}`,
			want: map[string]any{"resolution": "2K"},
		},
		{
			name: "legacy numeric strings retain valid zeros",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":"5","input_seconds":"0","input_image_count":"0"}}}`,
			want: map[string]any{"resolution": "768P", "seconds": float64(5), "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "out of contract usage cannot become a billing multiplier",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":16,"input_seconds":16,"input_image_count":10}}}`,
			want: map[string]any{"resolution": "2K"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsageOnComplete", nil, nil, body))
		})
	}

	t.Run("polling adaptor carries actual facts into task settlement", func(t *testing.T) {
		adaptor := taskplugin.New(plugin)
		result, err := adaptor.ParseTaskResult(&model.Task{}, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, []byte(
			`{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
		))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5),
		}, result.UsageFacts)
	})
}

// The /v2 result is a public CDN URL, so its artifact is proxied without
// channel credentials instead of through the /v1 file download endpoint.
func TestHailuoH3ArtifactContentProxy(t *testing.T) {
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example"},
	})
	task := &model.Task{
		TaskID: "task-public",
		Status: model.TaskStatusSuccess,
		Data:   []byte(`{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`),
	}

	artifacts, err := adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

	descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	require.NotNil(t, descriptor)
	assert.Equal(t, "https://cdn.example/h3.mp4", descriptor.URL)
	assert.True(t, descriptor.Credentialless)

	pending := &model.Task{TaskID: "task-public", Status: model.TaskStatusInProgress, Data: task.Data}
	pendingArtifacts, err := adaptor.ListArtifacts(pending)
	require.NoError(t, err)
	assert.Empty(t, pendingArtifacts)
}

// The /v2 create response carries only task_id, without the /v1 base_resp envelope.
func TestHailuoParseSubmitResponse(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	t.Run("H3 create has no envelope", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"task_id": "h3-1"}})
		assert.Equal(t, "h3-1", parsed["taskId"])
	})
	t.Run("H3 rejection reports the envelope message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 2013, "status_msg": "invalid params"}}})
		require.ErrorContains(t, err, "invalid params")
	})
	t.Run("H3 rejection reports the v2 error message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"type": "error", "error": map[string]any{
				"type": "bad_request_error", "message": "content requires text", "http_code": "400",
			}}})
		require.ErrorContains(t, err, "content requires text")
	})
	t.Run("legacy create", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"task_id": "v1-1", "base_resp": map[string]any{"status_code": 0}}})
		assert.Equal(t, "v1-1", parsed["taskId"])
	})
	t.Run("legacy response without an envelope is rejected", func(t *testing.T) {
		ctx := map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"}
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx,
			map[string]any{"body": map[string]any{"task_id": "v1-1"}})
		require.ErrorContains(t, err, "hailuo submit failed")
	})
	t.Run("legacy error", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 1026, "status_msg": "sensitive"}}})
		require.ErrorContains(t, err, "sensitive")
	})
}

// Without an H3 exemption the shared /v1 combo table would reject every H3
// duration on the OpenAI Video path before the request is ever built.
func TestHailuoH3PassesOpenAIVideoDecode(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"},
		map[string]any{
			"body":          map[string]any{"kind": "json", "value": map[string]any{"model": "MiniMax-H3", "prompt": "p", "seconds": 12, "size": "2K"}},
			"model":         "MiniMax-H3",
			"upstreamModel": "MiniMax-H3",
		})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var intent map[string]any
	require.NoError(t, common.Unmarshal(encoded, &intent))
	assert.Equal(t, "submit", intent["kind"])
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12), requestBody["duration"])
}

// Official Metaso examples normalize onto the existing H3 contract, including
// the existing billing facts; client-declared media durations are not facts.
func TestHailuoH3OfficialCompatibility(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	decode := func(body map[string]any) map[string]any {
		t.Helper()
		value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
			"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": body,
		})
		require.NoError(t, err)
		return alibabaObject(t, value)
	}
	for _, spec := range []struct{ size, resolution, ratio string }{
		{"720x1280", "768P", "9:16"}, {"1280x720", "768P", "16:9"},
		{"1024x1792", "2K", "9:16"}, {"1792x1024", "2K", "16:9"},
	} {
		t.Run(spec.size, func(t *testing.T) {
			intent := decode(map[string]any{"kind": "json", "value": map[string]any{"prompt": "p", "size": spec.size}})
			body := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(intent["requestBody"].(map[string]any)))["body"].(map[string]any)
			assert.Equal(t, spec.resolution, body["resolution"])
			assert.Equal(t, spec.ratio, body["ratio"])
			assert.Equal(t, float64(5), body["duration"])
		})
	}
	for _, reference := range []struct {
		name  string
		input any
		url   string
	}{
		{"string", "image.png", "image.png"}, {"url object", map[string]any{"url": "image.png"}, "image.png"},
		{"image_url object", map[string]any{"image_url": "image.png"}, "image.png"},
		{"nested image_url", map[string]any{"image_url": map[string]any{"url": "image.png"}}, "image.png"},
	} {
		t.Run(reference.name, func(t *testing.T) {
			for _, withVideo := range []bool{false, true} {
				req := map[string]any{"prompt": "p", "input_reference": reference.input}
				if withVideo {
					req["reference_video"] = "reference.mp4"
				}
				intent := decode(map[string]any{"kind": "json", "value": req})
				ctx := hailuoH3SubmitContext(intent["requestBody"].(map[string]any))
				body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
				content := body["content"].([]any)
				image := content[len(content)-1].(map[string]any)
				role := "first_frame"
				if withVideo {
					role = "reference_image"
				}
				assert.Equal(t, role, image["role"])
				assert.Equal(t, reference.url, image["image_url"].(map[string]any)["url"])
				facts := callHailuoHook(t, plugin, "extractUsage", ctx)
				assert.Equal(t, float64(1), facts["input_images"])
				seconds := float64(0)
				if withVideo {
					seconds = 15
				}
				assert.Equal(t, seconds, facts["input_video_seconds"])
			}
		})
	}
	t.Run("official aliases keep inline references and explicit fields override pixel defaults", func(t *testing.T) {
		content := []any{
			map[string]any{"type": "text", "text": "p"},
			map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "data:video/mp4;base64,AA=="}},
			map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "data:video/mp4;base64,AQ=="}},
			map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "data:video/mp4;base64,Ag=="}},
		}
		req := map[string]any{"prompt": "ignored", "size": "720x1280", "duration": 12, "metadata": map[string]any{
			"metaso_content": content, "metaso_resolution": "2K", "metaso_ratio": "4:3", "input_video_seconds": 0,
		}}
		ctx := hailuoH3SubmitContext(decode(map[string]any{"kind": "json", "value": req})["requestBody"].(map[string]any))
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		assert.Equal(t, content, body["content"])
		assert.Equal(t, "2K", body["resolution"])
		assert.Equal(t, "4:3", body["ratio"])
		assert.Equal(t, float64(15), callHailuoHook(t, plugin, "extractUsage", ctx)["input_video_seconds"])
		missing := callHailuoHook(t, plugin, "extractUsageOnComplete", nil, nil, map[string]any{"task": map[string]any{"status": "succeeded", "resolution": "2K"}})
		assert.NotContains(t, missing, "input_video_seconds", "missing actual usage must leave the bounded reservation untouched")
	})
	t.Run("equivalent aliases compare objects structurally and do not duplicate input", func(t *testing.T) {
		intent := decode(map[string]any{"kind": "multipart", "fields": map[string][]string{
			"prompt": {"p"}, "content": {`[{"type":"video_url","role":"reference_video","video_url":{"url":"v.mp4"}}]`},
			"metadata": {`{"metaso_content":[{"video_url":{"url":"v.mp4"},"role":"reference_video","type":"video_url"}],"resolution":"2K","metaso_resolution":"2k"}`},
		}})
		ctx := hailuoH3SubmitContext(intent["requestBody"].(map[string]any))
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		assert.Len(t, body["content"], 2)
		assert.Equal(t, float64(15), callHailuoHook(t, plugin, "extractUsage", ctx)["input_video_seconds"])
	})
	t.Run("nested Metaso metadata shares output and billing normalization", func(t *testing.T) {
		req := map[string]any{"prompt": "p", "seconds": 8, "metadata": map[string]any{"metaso": map[string]any{
			"content": []any{map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "nested.mp4"}}},
			"ratio":   "3:4", "resolution": "2K",
		}}}
		ctx := hailuoH3SubmitContext(decode(map[string]any{"kind": "json", "value": req})["requestBody"].(map[string]any))
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		assert.Equal(t, "3:4", body["ratio"])
		assert.Equal(t, "2K", body["resolution"])
		assert.Equal(t, "nested.mp4", body["content"].([]any)[1].(map[string]any)["video_url"].(map[string]any)["url"])
		assert.Equal(t, map[string]any{"seconds": float64(8), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(15)}, callHailuoHook(t, plugin, "extractUsage", ctx))
	})

	t.Run("JSON video and audio synonyms retain list order", func(t *testing.T) {
		intent := decode(map[string]any{"kind": "json", "value": map[string]any{"prompt": "p", "metadata": map[string]any{
			"video_reference": []any{map[string]any{"url": "one.mp4"}, "two.mp4"}, "audio_reference": "voice.mp3",
		}}})
		ctx := hailuoH3SubmitContext(intent["requestBody"].(map[string]any))
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		content := body["content"].([]any)
		assert.Equal(t, "one.mp4", content[1].(map[string]any)["video_url"].(map[string]any)["url"])
		assert.Equal(t, "two.mp4", content[2].(map[string]any)["video_url"].(map[string]any)["url"])
		assert.Equal(t, "voice.mp3", content[3].(map[string]any)["audio_url"].(map[string]any)["url"])
	})
	t.Run("repeated URLs preserve explicit input items and distinct frame roles", func(t *testing.T) {
		for _, req := range []map[string]any{
			{"prompt": "p", "images": []any{"loop.png", "loop.png"}},
			{"prompt": "p", "reference_video": []any{"same.mp4", "same.mp4"}},
		} {
			ctx := hailuoH3SubmitContext(decode(map[string]any{"kind": "json", "value": req})["requestBody"].(map[string]any))
			body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
			assert.Len(t, body["content"], 3, "explicit item counts must not silently change")
		}
	})

	t.Run("Responses shares the official aliases and object normalization", func(t *testing.T) {
		value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
			"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{"kind": "json", "value": map[string]any{
				"model": "MiniMax-H3", "input": "p", "input_reference": map[string]any{"image_url": map[string]any{"url": "image.png"}},
				"metadata": map[string]any{"reference_video": "v.mp4", "metaso_ratio": "9:16"},
			}},
		})
		require.NoError(t, err)
		body := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(alibabaObject(t, value)["requestBody"].(map[string]any)))["body"].(map[string]any)
		assert.Len(t, body["content"], 3)
		assert.Equal(t, "9:16", body["ratio"])
	})
}

func TestHailuoH3OfficialRejectsAmbiguousOrUnboundedInputs(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, tc := range []struct {
		name string
		req  map[string]any
		want string
	}{
		{"content aliases conflict", map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}}, "metadata": map[string]any{"metaso_content": []any{map[string]any{"type": "text", "text": "b"}}}}, "conflicting content/metaso_content"},
		{"resolution aliases conflict", map[string]any{"resolution": "2K", "metadata": map[string]any{"metaso_resolution": "768P"}}, "conflicting resolution/metaso_resolution"},
		{"ratio aliases conflict", map[string]any{"ratio": "9:16", "metadata": map[string]any{"metaso_ratio": "16:9"}}, "conflicting ratio/metaso_ratio"},
		{"duration aliases conflict", map[string]any{"seconds": 5, "duration": 10}, "conflicting seconds/duration"},
		{"frame aliases conflict", map[string]any{"images": []any{"one.png"}, "metadata": map[string]any{"first_frame_image": "two.png"}}, "conflicting frame image"},
		{"URL and file ID conflict", map[string]any{"input_reference": map[string]any{"url": "one.png", "file_id": "123"}}, "private Metaso file ids require an owned gateway asset"},
		{"malformed object", map[string]any{"input_reference": map[string]any{"path": "local.png"}}, "requires a URL or file reference"},
		{"malformed file ID", map[string]any{"input_reference": map[string]any{"file_id": "file-123"}}, "private Metaso file ids require an owned gateway asset"},
		{"video aliases conflict", map[string]any{"reference_video": "one.mp4", "video_reference": "two.mp4"}, "conflicting reference_video/video_reference"},
		{"video count", map[string]any{"reference_video": []any{"1.mp4", "2.mp4", "3.mp4", "4.mp4"}}, "at most 3 reference videos"},
		{"audio count", map[string]any{"audio_reference": []any{"1.mp3", "2.mp3", "3.mp3", "4.mp3"}}, "at most 3 reference audios"},
		{"declared duration cannot extend output", map[string]any{"seconds": 30}, "between 4 and 15"},
		{"unknown content type", map[string]any{"content": []any{map[string]any{"type": "file", "url": "x"}}}, "unsupported content type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := map[string]any{"prompt": "p"}
			maps.Copy(req, tc.req)
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{"kind": "json", "value": req},
			})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestHailuoH3OfficialMultipartUsesActualHostFileReferences(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, field := range []string{"input_reference", "reference_image", "reference_video", "video_reference", "reference_audio", "audio_reference"} {
		t.Run(field, func(t *testing.T) {
			typeName, mimeType, role := "image_url", "image/png", "reference_image"
			if field == "input_reference" {
				role = "first_frame"
			}
			if field == "reference_video" || field == "video_reference" {
				typeName, mimeType, role = "video_url", "video/mp4", "reference_video"
			}
			if field == "reference_audio" || field == "audio_reference" {
				typeName, mimeType, role = "audio_url", "audio/mpeg", "reference_audio"
			}
			var input bytes.Buffer
			writer := multipart.NewWriter(&input)
			require.NoError(t, writer.WriteField("prompt", "p"))
			count := 2
			if field == "input_reference" {
				count = 1
			}
			fileValues := make([]any, 0, count)
			payloads := []string{"first-media-bytes", "second-media-bytes"}
			for index := range count {
				header := make(textproto.MIMEHeader)
				header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%d.bin"`, field, index))
				header.Set("Content-Type", mimeType)
				part, err := writer.CreatePart(header)
				require.NoError(t, err)
				_, err = io.WriteString(part, payloads[index])
				require.NoError(t, err)
				ref := "request_file:" + field
				if index > 0 {
					ref += fmt.Sprintf("#%d", index)
				}
				fileValues = append(fileValues, map[string]any{"ref": ref, "field": field, "filename": fmt.Sprintf("%d.bin", index), "mimeType": mimeType, "size": len(payloads[index])})
			}
			require.NoError(t, writer.Close())
			value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{"kind": "multipart", "fields": map[string][]string{"prompt": {"p"}}, "files": fileValues},
			})
			require.NoError(t, err)
			intent := alibabaObject(t, value)
			info := &relaycommon.RelayInfo{
				OriginModelName: "MiniMax-H3", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public", Action: intent["action"].(string)},
				ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "fake", ChannelBaseUrl: "https://metaso.example/api/minimax", UpstreamModelName: "MiniMax-H3"},
			}
			adaptor := taskplugin.New(plugin)
			adaptor.Init(info)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(input.Bytes()))
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			c.Set("task_request", intent["requestBody"])
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			videoSeconds := float64(0)
			images := float64(0)
			if typeName == "video_url" {
				videoSeconds = 15
			}
			if typeName == "image_url" {
				images = float64(count)
			}
			assert.Equal(t, videoSeconds, facts["input_video_seconds"])
			assert.Equal(t, images, facts["input_images"])
			reader, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			encoded, err := io.ReadAll(reader)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, common.Unmarshal(encoded, &body))
			content := body["content"].([]any)
			require.Len(t, content, 1+count)
			for index := range count {
				item := content[index+1].(map[string]any)
				assert.Equal(t, role, item["role"])
				assert.Equal(t, "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString([]byte(payloads[index])), item[typeName].(map[string]any)["url"], "same-field files preserve #1 identity, MIME, bytes and order")
			}
			assert.NotContains(t, string(encoded), "__fileRef")
		})
	}
}

func TestHailuoH3OfficialMultipartValidation(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	file := func(field, ref, mime string, size int) map[string]any {
		return map[string]any{"field": field, "ref": ref, "filename": "media.bin", "mimeType": mime, "size": size}
	}
	video := file("reference_video", "request_file:reference_video", "video/mp4", 10)
	for _, tc := range []struct {
		name  string
		files []any
		want  string
	}{
		{"unsupported file", []any{file("unrecognized", "request_file:unrecognized", "video/mp4", 10)}, "unexpected file field"},
		{"wrong MIME", []any{file("reference_video", "request_file:reference_video", "audio/mpeg", 10)}, "MIME type does not match"},
		{"duplicate opaque file reference", []any{video, video}, "duplicate uploaded file references"},
		{"input_reference original limit", []any{file("input_reference", "request_file:input_reference", "image/png", 20971521)}, "exceeds its media size limit"},
		{"official image limit", []any{file("reference_image", "request_file:reference_image", "image/png", 31457281)}, "exceeds its media size limit"},
		{"official video limit", []any{file("reference_video", "request_file:reference_video", "video/mp4", 52428801)}, "exceeds its media size limit"},
		{"official audio limit", []any{file("reference_audio", "request_file:reference_audio", "audio/mpeg", 15728641)}, "exceeds its media size limit"},
		{"too many video files", []any{video,
			file("reference_video", "request_file:reference_video#1", "video/mp4", 10),
			file("reference_video", "request_file:reference_video#2", "video/mp4", 10),
			file("video_reference", "request_file:video_reference", "video/mp4", 10)}, "at most 3 reference videos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{"kind": "multipart", "fields": map[string][]string{"prompt": {"p"}}, "files": tc.files},
			})
			require.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("image video and audio file mixture retains all references and usage", func(t *testing.T) {
		files := []any{
			file("input_reference", "request_file:input_reference", "image/jpeg", 10), video,
			file("audio_reference", "request_file:audio_reference", "audio/wav", 10),
		}
		ctx := hailuoH3SubmitContext(map[string]any{"prompt": "p"})
		ctx["files"] = files
		ctx["mediaMetadata"] = map[string]any{"videos": []any{map[string]any{"durationSeconds": 0}}}
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		content := body["content"].([]any)
		require.Len(t, content, 4)
		for index, typeName := range []string{"image_url", "video_url", "audio_url"} {
			item := content[index+1].(map[string]any)
			assert.Equal(t, files[index].(map[string]any)["ref"], item[typeName].(map[string]any)["url"].(map[string]any)["__fileRef"])
		}
		assert.Equal(t, "reference_image", content[1].(map[string]any)["role"])
		assert.Equal(t, map[string]any{"seconds": float64(5), "resolution": "768P", "input_images": float64(1), "input_video_seconds": float64(15)}, callHailuoHook(t, plugin, "extractUsage", ctx))
	})
}

// A valid reference video may exceed ReadForm's 32 MiB memory threshold. Each
// hook must reparse BodyStorage after earlier temporary forms are removed.
func TestHailuoH3ReferenceVideoSurvivesMultipartDiskSpill(t *testing.T) {
	previousLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 0 // ReadForm fallback 32 MiB; inline fallback 64 MiB.
	t.Cleanup(func() { constant.MaxFileDownloadMB = previousLimit })
	payload := bytes.Repeat([]byte{0x5a}, 33<<20)
	wantSHA := sha256.Sum256(payload)
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	require.NoError(t, writer.WriteField("prompt", "p"))
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="reference_video"; filename="reference.mp4"`)
	header.Set("Content-Type", "video/mp4")
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(input.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	form, err := common.ParseMultipartFormReusable(c)
	require.NoError(t, err)
	t.Cleanup(func() { _ = form.RemoveAll() })
	require.Len(t, form.File["reference_video"], 1)
	opened, err := form.File["reference_video"][0].Open()
	require.NoError(t, err)
	_, onDisk := opened.(*os.File)
	require.NoError(t, opened.Close())
	require.True(t, onDisk, "the reference must actually cross ReadForm's disk-spill threshold")
	require.NoError(t, form.RemoveAll())
	plugin := loadHailuoPlugin(t)
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{
			"kind": "multipart", "fields": map[string][]string{"prompt": {"p"}}, "files": []any{map[string]any{
				"field": "reference_video", "ref": "request_file:reference_video", "filename": "reference.mp4", "mimeType": "video/mp4", "size": len(payload),
			}},
		},
	})
	require.NoError(t, err)
	intent := alibabaObject(t, value)
	info := &relaycommon.RelayInfo{
		OriginModelName: "MiniMax-H3", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public", Action: intent["action"].(string)},
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "fake", ChannelBaseUrl: "https://metaso.example/api/minimax", UpstreamModelName: "MiniMax-H3"},
	}
	adaptor := taskplugin.New(plugin)
	adaptor.Init(info)
	c.Set("task_request", intent["requestBody"])
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	require.NoError(t, err)
	assert.Equal(t, float64(15), facts["input_video_seconds"])
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(encoded, &body))
	content := body["content"].([]any)
	require.Len(t, content, 2)
	url := content[1].(map[string]any)["video_url"].(map[string]any)["url"].(string)
	base64Content, ok := strings.CutPrefix(url, "data:video/mp4;base64,")
	require.True(t, ok)
	actual, err := base64.StdEncoding.DecodeString(base64Content)
	require.NoError(t, err)
	assert.Len(t, actual, len(payload))
	assert.Equal(t, wantSHA, sha256.Sum256(actual))
}

// Shared upstream account credentials do not prove that a private Metaso file
// id belongs to this gateway user. URL/data-URL and real uploads remain usable.
func TestHailuoH3RejectsUnownedPrivateMetasoFiles(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	const want = "private Metaso file ids require an owned gateway asset; use URL, data URL or upload"
	textOnly := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(map[string]any{"prompt": "explain file_id and mm_file://123 as text"}))
	assert.Equal(t, "explain file_id and mm_file://123 as text", textOnly["body"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"])
	for _, typeName := range []string{"image_url", "video_url", "audio_url"} {
		for _, item := range []map[string]any{
			{"type": typeName, "file_id": "12345", typeName: map[string]any{"url": "https://cdn.example/media"}},
			{"type": typeName, typeName: map[string]any{"url": "https://cdn.example/media", "file_id": "12345"}},
		} {
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoH3SubmitContext(map[string]any{
				"prompt": "p", "metadata": map[string]any{"content": []any{item}},
			}))
			require.ErrorContains(t, err, want)
		}
	}

	for _, reference := range []struct {
		name  string
		value any
	}{
		{"numeric ID", map[string]any{"file_id": "12345"}},
		{"null ID", map[string]any{"file_id": nil}},
		{"ID alongside ordinary URL", map[string]any{"file_id": "12345", "url": "https://cdn.example/image.png"}},
		{"native URI", "mm_file://12345"},
		{"mixed-case URI", " Mm_FiLe://12345 "},
		{"uppercase scheme", "MM_FILE:12345"},
		{"nested URL URI", map[string]any{"url": "mM_fIlE://12345"}},
		{"nested image_url ID", map[string]any{"image_url": map[string]any{"file_id": "12345"}}},
		{"nested image_url URI", map[string]any{"image_url": map[string]any{"url": "MM_FILE://12345"}}},
	} {
		t.Run(reference.name, func(t *testing.T) {
			for _, typeName := range []string{"image_url", "video_url", "audio_url"} {
				requests := []map[string]any{
					{"prompt": "p", "input_reference": reference.value},
					{"prompt": "p", "metadata": map[string]any{"content": []any{map[string]any{"type": typeName, typeName: reference.value}}}},
					{"prompt": "p", "metadata": map[string]any{"metaso_content": []any{map[string]any{"type": typeName, typeName: map[string]any{"url": reference.value}}}}},
				}
				for _, req := range requests {
					_, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
						"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "body": map[string]any{"kind": "json", "value": req},
					})
					require.ErrorContains(t, decodeErr, want)
					ctx := hailuoH3SubmitContext(req)
					_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
					require.ErrorContains(t, err, want)
					_, err = plugin.Engine.Call(t.Context(), "extractUsage", ctx)
					require.ErrorContains(t, err, want)
				}
			}
		})
	}
}

func TestHailuoH3ResponsesReferencePlacement(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, mediaType := range []string{"input_video", "input_audio", "video_url", "audio_url"} {
		t.Run(mediaType, func(t *testing.T) {
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
				"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3",
				"body": map[string]any{"kind": "json", "value": map[string]any{"model": "MiniMax-H3", "input": []any{map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "input_text", "text": "synthetic"},
					map[string]any{"type": mediaType, "url": "https://cdn.example/synthetic"},
				}}}}},
			})
			require.ErrorContains(t, err, "metadata.content")
		})
	}
	t.Run("canonical metadata preserves a video reference", func(t *testing.T) {
		intent, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
			"model": "MiniMax-H3", "upstreamModel": "MiniMax-H3",
			"body": map[string]any{"kind": "json", "value": map[string]any{"model": "MiniMax-H3", "input": "synthetic", "metadata": map[string]any{"metaso_content": []any{
				map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://cdn.example/synthetic.mp4"}},
			}}}},
		})
		require.NoError(t, err)
		request := alibabaObject(t, intent)["requestBody"].(map[string]any)
		ctx := hailuoH3SubmitContext(request)
		facts := callHailuoHook(t, plugin, "extractUsage", ctx)
		assert.EqualValues(t, 15, facts["input_video_seconds"])
		body := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)["body"].(map[string]any)
		require.Len(t, body["content"], 2)
		assert.Equal(t, "video_url", body["content"].([]any)[1].(map[string]any)["type"])
	})
}
