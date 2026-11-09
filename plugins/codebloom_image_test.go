package plugins_test

import (
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
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
	"gpt-image-2.5":          "images_generations",
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
	require.Len(t, plugin.Meta.Models, 11)
	require.Len(t, plugin.Meta.Protocols, 1)
	require.NotNil(t, plugin.Meta.Protocols[0].RetainResult)
	assert.True(t, *plugin.Meta.Protocols[0].RetainResult)
	for raw, transport := range codebloomImages {
		t.Run(raw, func(t *testing.T) {
			public := "ys-" + raw
			binding, found := registry.Generation().LookupEndpoint("POST", "/v1/images/generations", public)
			require.True(t, found)
			assert.Equal(t, "codebloom-image", binding.Plugin.Meta.Key)
			assert.Equal(t, "1.0.1", binding.Plugin.Meta.Version)
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
				assert.Equal(t, map[string]any{"model": raw, "prompt": "a cat", "size": "1024x1024", "n": float64(1), "response_format": "url"}, body)
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

// Explicit private fixtures replay real supplier receipts and image bytes.
// The synthetic qualification injection exists only in this test; production
// still requires the separately committed qualification and enabled abilities.
func TestObservedImageEditOriginalReceiptAndArtifactContracts(t *testing.T) {
	manifest := os.Getenv("TEST_IMAGE_EDIT_OBSERVED_MANIFEST")
	if manifest == "" {
		t.Skip("explicit private original image-edit evidence required")
	}
	raw, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var entries []struct {
		Label         string         `json:"label"`
		Provider      string         `json:"provider"`
		Model         string         `json:"model"`
		Upstream      string         `json:"upstream"`
		Offer         string         `json:"offer"`
		Lane          string         `json:"lane"`
		Request       map[string]any `json:"request"`
		SubmitPath    string         `json:"submit_path"`
		PollPath      string         `json:"poll_path"`
		ImagePath     string         `json:"image_path"`
		ReferencePath string         `json:"reference_path"`
		ImageSHA256   string         `json:"image_sha256"`
		Width         int            `json:"width"`
		Height        int            `json:"height"`
	}
	require.NoError(t, common.Unmarshal(raw, &entries))
	require.NotEmpty(t, entries)
	var reports []map[string]any
	for _, entry := range entries {
		t.Run(entry.Label, func(t *testing.T) {
			source, err := builtinplugins.Source(entry.Provider)
			require.NoError(t, err)
			var injection string
			if entry.Provider == "ycapi" {
				injection = fmt.Sprintf("globalThis.ycapiApprovedCatalog.find(m => m.id === %q).image_edit = {max_images:1,max_bytes:5242880,offers:[%q]};", entry.Model, entry.Offer)
			} else {
				injection = fmt.Sprintf("globalThis.codebloomImageEditModels = [%q];", entry.Model)
			}
			lines := strings.SplitN(source, "\n", 2)
			require.Len(t, lines, 2)
			source = lines[0] + "\n" + injection + "\n" + lines[1]
			p, err := jsplugin.CompilePlugin(source, jsplugin.Options{})
			require.NoError(t, err)
			request := maps.Clone(entry.Request)
			var body map[string]any
			if entry.Provider == "ycapi" {
				body = map[string]any{"kind": "json", "value": request}
			} else {
				fields := map[string][]string{}
				for key, value := range request {
					fields[key] = []string{fmt.Sprint(value)}
				}
				reference, err := os.ReadFile(entry.ReferencePath)
				require.NoError(t, err)
				body = map[string]any{"kind": "multipart", "fields": fields, "files": []any{map[string]any{
					"field": "image[]", "ref": "request_file:image[]", "filename": "reference.png", "mimeType": "image/png", "size": len(reference)}}}
			}
			decoded, err := p.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": entry.Model, "operation": "edit", "body": body})
			require.NoError(t, err)
			intent := codebloomObject(t, decoded)
			ctx := map[string]any{"model": entry.Model, "upstreamModel": entry.Model, "baseUrl": "https://supplier.invalid", "apiKey": "synthetic-secret", "publicTaskId": "task_observed_image_edit", "requestBody": intent["requestBody"]}
			if entry.Provider == "codebloom-image" {
				ctx["upstreamModel"] = entry.Upstream
			}
			wire := codebloomHook(t, p, "buildSubmitRequest", ctx)
			facts := codebloomHook(t, p, "extractUsage", ctx)
			assert.EqualValues(t, 1, facts["image_count"])
			if entry.Provider == "ycapi" {
				envelope := codebloomObject(t, wire["body"])
				assert.Equal(t, "edit", envelope["operation"])
				assert.Equal(t, entry.Lane, envelope["lane_id"])
				assert.Equal(t, entry.Upstream, envelope["model_id"])
				params := codebloomObject(t, envelope["params"])
				assert.Equal(t, params["images"], envelope["attachments"])
				assert.Equal(t, "image-edit", params["mode"])
				assert.Equal(t, entry.Offer, facts["spec"])
			} else if strings.HasPrefix(entry.Upstream, "gpt-") {
				assert.Equal(t, "https://supplier.invalid/v1/images/edits", wire["url"])
				assert.Equal(t, "multipart", wire["bodyType"])
				parts := wire["parts"].([]any)
				assert.Equal(t, "request_file:image[]", codebloomObject(t, parts[len(parts)-1])["fileRef"])
			} else {
				assert.Equal(t, "https://supplier.invalid/v1/chat/completions", wire["url"])
				messages := codebloomObject(t, wire["body"])["messages"].([]any)
				content := codebloomObject(t, messages[0])["content"].([]any)
				require.Len(t, content, 2)
				assert.Equal(t, "image_url", codebloomObject(t, content[1])["type"])
			}
			submit, err := os.ReadFile(entry.SubmitPath)
			require.NoError(t, err)
			var response any
			require.NoError(t, common.Unmarshal(submit, &response))
			parsed := codebloomHook(t, p, "parseSubmitResponse", ctx, map[string]any{"statusCode": 200, "body": response})
			state := parsed["state"]
			if entry.Provider == "ycapi" {
				poll, err := os.ReadFile(entry.PollPath)
				require.NoError(t, err)
				var original any
				require.NoError(t, common.Unmarshal(poll, &original))
				final := codebloomHook(t, p, "parseTaskResult", map[string]any{"model": entry.Model, "upstreamModel": entry.Model, "taskId": parsed["taskId"], "state": state}, original)
				assert.Equal(t, "SUCCESS", final["status"])
				state = final["state"]
			} else {
				assert.Equal(t, "SUCCESS", codebloomObject(t, parsed["immediate"])["status"])
			}
			task := map[string]any{"taskId": "task_observed_image_edit", "status": "SUCCESS", "state": state, "data": parsed["taskData"]}
			value, err := p.Engine.Call(t.Context(), "listArtifacts", task)
			require.NoError(t, err)
			artifacts := value.([]any)
			require.Len(t, artifacts, 1)
			key := codebloomObject(t, artifacts[0])["key"].(string)
			content := codebloomHook(t, p, "buildContentRequest", map[string]any{"state": state, "data": parsed["taskData"], "artifactKey": key, "clientRequest": map[string]any{"method": "GET"}})
			assert.Equal(t, true, content["credentialless"])
			assert.NotContains(t, content, "headers")
			gateway := "https://gateway.example/harness/v1/tasks/task_observed_image_edit/artifacts/" + key + "/content?access=synthetic-only"
			rendered, err := p.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "render"}, map[string]any{"artifacts": map[string]any{key: map[string]any{"url": gateway}}}, task)
			require.NoError(t, err)
			data := codebloomObject(t, rendered)["data"].([]any)
			assert.Equal(t, gateway, codebloomObject(t, data[0])["url"])
			bytes, err := os.ReadFile(entry.ImagePath)
			require.NoError(t, err)
			assert.Equal(t, entry.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(bytes)))
			decodedImage, _, err := image.Decode(strings.NewReader(string(bytes)))
			require.NoError(t, err)
			assert.Equal(t, entry.Width, decodedImage.Bounds().Dx())
			assert.Equal(t, entry.Height, decodedImage.Bounds().Dy())
			reports = append(reports, map[string]any{"label": entry.Label, "model": entry.Model, "offer": entry.Offer, "image_sha256": entry.ImageSHA256, "verified": true, "image_count": 1, "credentialless_artifact": true, "original_task_result": true})
		})
	}
	if output := os.Getenv("TEST_IMAGE_EDIT_OBSERVED_REPORT"); output != "" {
		encoded, err := common.Marshal(reports)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
}
