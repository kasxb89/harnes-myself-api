package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var codebloomImages = map[string]string{
	"gpt-image-2.5-sunburst": "images_generations", "gpt-image-2.5-flare": "images_generations", "gpt-image-2k": "images_generations",
	"grok-imagine-image-2.0": "chat_completions", "grok-imagine-image": "chat_completions", "gemini-3.1-flash-image-preview": "chat_completions", "nano-banana-2": "chat_completions", "gemini-nano-banana-2.1": "chat_completions", "nano-banana-pro": "chat_completions", "gemini-3-pro-image-preview": "chat_completions",
}

func newCodeBloomImagePlugin(t *testing.T) (*jsplugin.Registry, *jsplugin.LoadedPlugin) {
	t.Helper()
	source, err := builtinplugins.Source("codebloom-image")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "codebloom-image"})
	require.NoError(t, err)
	return registry, plugin
}
func codebloomImageContext(raw string, body map[string]any) map[string]any {
	return map[string]any{"model": "ys-" + raw, "upstreamModel": raw, "baseUrl": "https://codebloom.example", "apiKey": "synthetic-provider-secret", "publicTaskId": "task_gateway_image", "requestBody": body}
}
func codebloomObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := common.Marshal(v)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(raw, &result))
	return result
}
func codebloomHook(t *testing.T, p *jsplugin.LoadedPlugin, name string, args ...any) map[string]any {
	t.Helper()
	v, err := p.Engine.Call(t.Context(), name, args...)
	require.NoError(t, err)
	return codebloomObject(t, v)
}

func TestCodeBloomImageRequestTransports(t *testing.T) {
	registry, plugin := newCodeBloomImagePlugin(t)
	require.Len(t, plugin.Meta.Models, 10)
	require.Len(t, plugin.Meta.Protocols, 1)
	require.NotNil(t, plugin.Meta.Protocols[0].RetainResult)
	assert.True(t, *plugin.Meta.Protocols[0].RetainResult)
	for raw, transport := range codebloomImages {
		t.Run(raw, func(t *testing.T) {
			public := "ys-" + raw
			binding, found := registry.Generation().LookupEndpoint("POST", "/v1/images/generations", public)
			require.True(t, found)
			assert.Equal(t, "codebloom-image", binding.Plugin.Meta.Key)
			assert.Equal(t, "1.0.0", binding.Plugin.Meta.Version)
			assert.Equal(t, 1, binding.Plugin.Meta.APIVersion)
			_, found = registry.Generation().LookupEndpoint("POST", "/v1/images/generations", "ycapi-"+raw)
			assert.False(t, found)
			_, found = registry.Generation().LookupEndpoint("POST", "/v1/images/generations", "codebloom-"+raw)
			assert.False(t, found, "retired public IDs cannot claim the new route")
			ctx := codebloomImageContext(raw, map[string]any{"model": public, "prompt": "a cat", "n": 1})
			out := codebloomHook(t, plugin, "buildSubmitRequest", ctx)
			assert.Equal(t, "POST", out["method"])
			assert.Equal(t, "text_to_image", out["action"])
			headers := out["headers"].(map[string]any)
			assert.Equal(t, "Bearer synthetic-provider-secret", headers["Authorization"])
			body := out["body"].(map[string]any)
			assert.Equal(t, raw, body["model"])
			if transport == "images_generations" {
				assert.Equal(t, "https://codebloom.example/v1/images/generations", out["url"])
				assert.Equal(t, map[string]any{"model": raw, "prompt": "a cat", "size": "1024x1024", "n": float64(1)}, body)
			} else {
				assert.Equal(t, "https://codebloom.example/v1/chat/completions", out["url"])
				assert.Equal(t, map[string]any{"model": raw, "stream": false, "messages": []any{map[string]any{"role": "user", "content": "a cat"}}}, body)
				assert.NotContains(t, body, "size")
				assert.NotContains(t, body, "n")
			}
			assert.Equal(t, map[string]any{"image_count": float64(1)}, codebloomHook(t, plugin, "extractUsage", ctx))
		})
	}
}

func TestCodeBloomImageRejectsUnverifiedParameters(t *testing.T) {
	_, p := newCodeBloomImagePlugin(t)
	for _, raw := range []string{"gpt-image-2.5-flare", "nano-banana-2"} {
		for _, field := range []string{"quality", "resolution", "output_format", "moderation", "format", "aspect_ratio", "ratio", "seed", "reference_images", "image", "mask", "metadata", "background", "messages", "extra_body"} {
			t.Run(raw+"/"+field, func(t *testing.T) {
				body := map[string]any{"prompt": "cat", field: "unverified"}
				_, err := p.Engine.Call(t.Context(), "buildSubmitRequest", codebloomImageContext(raw, body))
				require.Error(t, err)
			})
		}
	}
	for _, body := range []map[string]any{{"prompt": " "}, {"prompt": "cat", "n": 2}, {"prompt": "cat", "n": "1"}, {"prompt": "cat", "count": 2}, {"prompt": "cat", "stream": true}, {"prompt": "cat", "response_format": "png"}, {"prompt": strings.Repeat("x", 16001)}} {
		_, err := p.Engine.Call(t.Context(), "buildSubmitRequest", codebloomImageContext("gpt-image-2.5-flare", body))
		require.Error(t, err)
	}
	for _, tc := range []struct{ raw, size string }{{"gpt-image-2k", "2048x2048"}, {"nano-banana-2", "1024x1024"}, {"nano-banana-2", "auto"}} {
		_, err := p.Engine.Call(t.Context(), "buildSubmitRequest", codebloomImageContext(tc.raw, map[string]any{"prompt": "cat", "size": tc.size}))
		require.Error(t, err)
	}
	_, err := p.Engine.Call(t.Context(), "buildSubmitRequest", codebloomImageContext("unknown", map[string]any{"prompt": "cat"}))
	require.Error(t, err)
	ctx := codebloomImageContext("gpt-image-2k", map[string]any{"prompt": "cat"})
	ctx["upstreamModel"] = "gpt-image-2.5-flare"
	_, err = p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.Error(t, err)
	_, err = p.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": "ys-gpt-image-2k", "operation": "edit", "body": map[string]any{"kind": "json", "value": map[string]any{"prompt": "cat"}}})
	require.Error(t, err)
}

func TestCodeBloomImageResponseNormalization(t *testing.T) {
	_, p := newCodeBloomImagePlugin(t)
	for raw, transport := range codebloomImages {
		t.Run(raw, func(t *testing.T) {
			url := "https://cdn.example/image"
			var response map[string]any
			if transport == "images_generations" {
				response = map[string]any{"data": []any{map[string]any{"url": url}}, "task_id": "private-vendor-id", "usage": map[string]any{"output_tokens": 8192}}
			} else {
				response = map[string]any{"model": raw, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "![image](" + url + ")\nhttps://mirror.example/same-image", "images": []any{url}}}}, "id": "private-vendor-id"}
			}
			result := codebloomHook(t, p, "parseSubmitResponse", codebloomImageContext(raw, map[string]any{"prompt": "cat"}), map[string]any{"statusCode": 200, "body": response})
			assert.Equal(t, "task_gateway_image", result["taskId"])
			assert.Equal(t, map[string]any{"output_url": url}, result["taskData"])
			immediate := result["immediate"].(map[string]any)
			assert.Equal(t, "SUCCESS", immediate["status"])
			assert.Equal(t, map[string]any{"image_count": float64(1)}, immediate["usageFacts"])
			encoded, err := common.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-vendor-id")
			assert.NotContains(t, string(encoded), "output_tokens")
			assert.NotContains(t, string(encoded), "mirror.example")
			assert.NotContains(t, string(encoded), "synthetic-provider-secret")
		})
	}
	for _, message := range []map[string]any{
		{"content": "https://cdn.example/bare-image"},
		{"content": "plain text", "images": []any{"https://cdn.example/structured-image"}},
		{"content": "![cat](https://cdn.example/markdown-image)"},
	} {
		result := codebloomHook(t, p, "parseSubmitResponse", codebloomImageContext("nano-banana-2", map[string]any{"prompt": "cat"}), map[string]any{"statusCode": 200, "body": map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": message}}}})
		require.NotEmpty(t, result["taskData"])
	}
}

func TestCodeBloomImageResponseRejectsFalseSuccess(t *testing.T) {
	_, p := newCodeBloomImagePlugin(t)
	for _, body := range []map[string]any{
		{}, {"error": map[string]any{"message": "Bearer synthetic-provider-secret https://private.example"}},
		{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "Read https://docs.example for instructions"}}}},
		{"choices": []any{map[string]any{"finish_reason": "length", "message": map[string]any{"content": "![x](https://cdn.example/image)"}}}},
		{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "[read more](https://cdn.example/image)"}}}},
		{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "![x](https://key@cdn.example/image)"}}}},
		{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "![x](http://cdn.example/image)"}}}},
	} {
		_, err := p.Engine.Call(t.Context(), "parseSubmitResponse", codebloomImageContext("nano-banana-2", map[string]any{"prompt": "cat"}), map[string]any{"statusCode": 200, "body": body})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "synthetic-provider-secret")
		assert.NotContains(t, err.Error(), "private.example")
	}
	for _, status := range []int{301, 401, 429, 500} {
		_, err := p.Engine.Call(t.Context(), "parseSubmitResponse", codebloomImageContext("gpt-image-2k", map[string]any{"prompt": "cat"}), map[string]any{"statusCode": status, "body": map[string]any{"data": []any{map[string]any{"url": "https://cdn.example/image"}}, "error": "synthetic-provider-secret"}})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "synthetic-provider-secret")
	}
}

func TestCodeBloomImageArtifactsAreCredentiallessAndDoNotPoll(t *testing.T) {
	_, p := newCodeBloomImagePlugin(t)
	task := map[string]any{"status": "SUCCESS", "data": map[string]any{"output_url": "https://cdn.example/image"}, "apiKey": "synthetic-provider-secret"}
	artifacts, err := p.Engine.Call(t.Context(), "listArtifacts", task)
	require.NoError(t, err)
	assert.Len(t, artifacts, 1)
	ctx := map[string]any{"data": task["data"], "artifactKey": "image-1", "apiKey": "synthetic-provider-secret", "authHeader": "Bearer synthetic-provider-secret", "clientRequest": map[string]any{"method": "GET"}}
	descriptor := codebloomHook(t, p, "buildContentRequest", ctx)
	assert.Equal(t, map[string]any{"url": "https://cdn.example/image", "method": "GET", "credentialless": true}, descriptor)
	encoded, err := common.Marshal(descriptor)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "synthetic-provider-secret")
	assert.NotContains(t, descriptor, "headers")
	_, err = p.Engine.Call(t.Context(), "buildQueryRequest", ctx)
	require.Error(t, err)
	assert.Equal(t, "UNKNOWN", codebloomHook(t, p, "parseTaskResult", ctx, map[string]any{"status": "completed"})["status"])
	task["status"] = "IN_PROGRESS"
	artifacts, err = p.Engine.Call(t.Context(), "listArtifacts", task)
	require.NoError(t, err)
	assert.Empty(t, artifacts)
}

func TestCodeBloomImageHostAdaptorUsesExactWireRequest(t *testing.T) {
	_, plugin := newCodeBloomImagePlugin(t)
	for raw, transport := range codebloomImages {
		t.Run(raw, func(t *testing.T) {
			id := "ys-" + raw
			info := &relaycommon.RelayInfo{OriginModelName: id, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 61, ChannelBaseUrl: "https://codebloom.example", UpstreamModelName: raw, ApiKey: "synthetic-provider-secret"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_host_image"}}
			adaptor := taskplugin.New(plugin)
			adaptor.Init(info)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			c.Set("task_request", map[string]any{"model": id, "prompt": "cat", "n": 1})
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			reader, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, common.Unmarshal(data, &body))
			assert.Equal(t, raw, body["model"])
			upstreamRequest, err := http.NewRequest("POST", "https://codebloom.example", nil)
			require.NoError(t, err)
			require.NoError(t, adaptor.BuildRequestHeader(c, upstreamRequest, info))
			assert.Equal(t, "Bearer synthetic-provider-secret", upstreamRequest.Header.Get("Authorization"))
			requestURL, err := adaptor.BuildRequestURL(info)
			require.NoError(t, err)
			if transport == "images_generations" {
				assert.True(t, strings.HasSuffix(requestURL, "/v1/images/generations"))
			} else {
				assert.True(t, strings.HasSuffix(requestURL, "/v1/chat/completions"))
				assert.NotContains(t, body, "n")
			}
			responseBody := `{"data":[{"url":"https://cdn.example/image"}]}`
			if transport != "images_generations" {
				responseBody = `{"choices":[{"finish_reason":"stop","message":{"content":"![cat](https://cdn.example/image)"}}]}`
			}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(responseBody))}
			parsed, taskErr := adaptor.ParseResponse(c, response, info)
			require.Nil(t, taskErr)
			require.NotNil(t, parsed)
			require.NotNil(t, parsed.Immediate)
			assert.Equal(t, "task_host_image", parsed.UpstreamTaskID)
			assert.Equal(t, "SUCCESS", string(parsed.Immediate.Status))
			assert.JSONEq(t, `{"output_url":"https://cdn.example/image"}`, string(parsed.TaskData))
		})
	}
}

func TestCodeBloomImageRendererUsesRetainedArtifact(t *testing.T) {
	_, plugin := newCodeBloomImagePlugin(t)
	task := map[string]any{"task_id": "task_gateway_image", "submit_time": 123, "status": "SUCCESS", "data": map[string]any{"output_url": "https://private-cdn.example/short-lived-image"}}
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "render"}, map[string]any{"artifacts": map[string]any{"image-1": map[string]any{"url": "/v1/tasks/task_gateway_image/artifacts/image-1/content"}}}, task)
	require.NoError(t, err)
	out := codebloomObject(t, value)
	encoded, err := common.Marshal(out)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "/v1/tasks/task_gateway_image/artifacts/image-1/content")
	assert.NotContains(t, string(encoded), "private-cdn")
}

func TestCodeBloomImageHostContentRequestIsCredentialless(t *testing.T) {
	_, plugin := newCodeBloomImagePlugin(t)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 61, ChannelBaseUrl: "https://codebloom.example", ApiKey: "synthetic-provider-secret"}})
	task := &model.Task{TaskID: "task_content", Status: model.TaskStatusSuccess}
	task.PrivateData.Key = "synthetic-private-saved-key"
	task.SetData(map[string]any{"output_url": "https://cdn.example/image"})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		content, err := adaptor.BuildContentRequest(t.Context(), task, "image-1", channel.TaskArtifactClientRequest{Method: method})
		require.NoError(t, err)
		require.NotNil(t, content)
		assert.True(t, content.Credentialless)
		assert.Equal(t, "https://cdn.example/image", content.URL)
		assert.Equal(t, method, content.Method)
		assert.Empty(t, content.Headers)
		assert.Empty(t, content.Body)
		encoded, err := common.Marshal(content)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "synthetic-provider-secret")
		assert.NotContains(t, string(encoded), "synthetic-private-saved-key")
	}
	_, err := adaptor.BuildContentRequest(t.Context(), task, "unknown", channel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.Error(t, err)
	_, err = adaptor.BuildContentRequest(t.Context(), task, "image-1", channel.TaskArtifactClientRequest{Method: http.MethodPost})
	require.Error(t, err)
}
