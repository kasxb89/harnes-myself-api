package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Actual Go authentication, Images presentation, durable task insertion and
// artifact handlers are exercised. Only the supplier's completed result is
// injected: this test does not bypass readiness in any production entrypoint.
func TestCodeBloomRetainedImageHTTPFixturesDatabase(t *testing.T) {
	t.Run("images_generations", func(t *testing.T) {
		codeBloomRetainedImageHTTPFixture(t, "ys-gpt-image-2k", "gpt-image-2k", true)
	})
	t.Run("chat_completions", func(t *testing.T) {
		codeBloomRetainedImageHTTPFixture(t, "ys-grok-imagine-image-2.0", "grok-imagine-image-2.0", false)
	})
}

func codeBloomRetainedImageHTTPFixture(t *testing.T, modelID, upstreamID string, imagesEndpoint bool) {
	db := codeBloomFinancialTestDatabase(t)
	oldRegistry := pluginruntime.DefaultRegistry
	pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
	oldAddress, oldSecret := system_setting.TaskPublicAddress, common.CryptoSecret
	system_setting.TaskPublicAddress = "https://gateway.example/harness"
	common.CryptoSecret = "synthetic-artifact-fixture-signing-key"
	t.Cleanup(func() {
		pluginruntime.DefaultRegistry = oldRegistry
		system_setting.TaskPublicAddress, common.CryptoSecret = oldAddress, oldSecret
	})
	allowPrivateTaskMediaTest(t)
	source, err := builtinplugins.Source("codebloom-image")
	require.NoError(t, err)
	plugin, err := pluginruntime.DefaultRegistry.RegisterFactory(source, pluginruntime.Options{})
	require.NoError(t, err)
	const taskID = "task_codebloom_http_fixture"
	const savedImage = "https://127.0.0.1/local-synthetic-image.png"
	for _, user := range []model.User{
		{Id: 1, Username: "fixture-owner", AffCode: "fixture-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1000000},
		{Id: 2, Username: "fixture-other", AffCode: "fixture-other", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1000000},
	} {
		require.NoError(t, db.Create(&user).Error)
	}
	for _, token := range []model.Token{
		{UserId: 1, Key: "synthetichttpowner", Name: "Fixture owner", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000},
		{UserId: 2, Key: "synthetichttpother", Name: "Fixture other", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000000},
	} {
		require.NoError(t, db.Create(&token).Error)
	}
	channel := model.Channel{Id: 61, Type: constant.ChannelTypeTaskPlugin, Name: "Synthetic retained image", Status: common.ChannelStatusEnabled, Key: "synthetic-provider-secret", BaseURL: common.GetPointer("https://supplier.invalid"), Models: modelID, Group: "default"}
	channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "codebloom-image"})
	require.NoError(t, db.Create(&channel).Error)
	generation := pluginruntime.DefaultRegistry.Generation()
	pinned := pluginruntime.PinnedEndpoint{Generation: generation, Plugin: plugin, Protocol: pluginruntime.ProtocolOpenAIImage, Operation: pluginruntime.HostProtocolOperation{Name: "generate", Methods: []string{http.MethodPost}, Path: "/v1/images/generations", BodyKinds: []pluginruntime.BodyKind{pluginruntime.BodyJSON}, ModelField: "model"}, Model: modelID}
	require.True(t, pinned.RetainsImageResult())
	deps := pluginProtocolTestDeps()
	deps.submit = func(c *gin.Context, info *relaycommon.RelayInfo) (*taskSubmissionOutcome, *dto.TaskError) {
		info.InitChannelMeta(c)
		info.OriginModelName, info.UpstreamModelName = modelID, upstreamID
		info.PublicTaskID = taskID
		info.LockedChannel = &channel
		events := []string{}
		info.Billing = &taskSubmissionTestBilling{events: &events}
		return executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			adaptor := taskplugin.New(plugin)
			adaptor.Init(info)
			if err := adaptor.ValidateRequestAndSetAction(c, info); err != nil {
				return nil, err
			}
			response := `{"data":[{"url":"` + savedImage + `"}]}`
			if !imagesEndpoint {
				response = `{"model":"` + upstreamID + `","choices":[{"finish_reason":"stop","message":{"content":"![image](` + savedImage + `)","images":[{"image_url":{"url":"` + savedImage + `"}}]}}]}`
			}
			result, taskErr := adaptor.ParseResponse(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, info)
			if taskErr != nil {
				return nil, taskErr
			}
			return &relay.TaskSubmitResult{Platform: "codebloom-image", UpstreamTaskID: result.UpstreamTaskID, TaskData: result.TaskData, PluginState: result.PluginState, Immediate: result.Immediate}, nil
		})
	}
	engine := gin.New()
	authenticated := engine.Group("")
	authenticated.Use(middleware.TokenAuth())
	authenticated.POST("/v1/images/generations", func(c *gin.Context) {
		body := map[string]any{}
		require.NoError(t, common.DecodeJson(c.Request.Body, &body))
		c.Set("resolved_task_model", modelID)
		c.Set("task_action", "text_to_image")
		c.Set("task_request", body)
		c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: plugin})
		c.Set(pluginruntime.ContextKeyPinnedEndpoint, pinned)
		c.Set(pluginruntime.ContextKeyProtocolRequest, pluginruntime.ProtocolRequestContext{RouteRequestContext: pluginruntime.RouteRequestContext{Path: c.Request.URL.Path, Method: http.MethodPost, Params: map[string]string{}, Query: map[string][]string{}, Body: map[string]any{"kind": "json", "value": body}, RequestBody: body}, Protocol: pluginruntime.ProtocolOpenAIImage, Operation: "generate", Model: modelID})
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, modelID))
		serveTaskPluginImageProtocol(c, pinned, deps)
	})
	authenticated.GET("/v1/tasks/:key", GetTask)
	authenticated.GET("/v1/tasks/:key/artifacts", GetTaskArtifacts)
	authenticated.GET("/v1/tasks/:key/artifacts/:artifact_key/content", TaskArtifactContent)
	pixel := color.NRGBA{R: 54, G: 132, B: 196, A: 255}
	fixtureImage := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	fixtureImage.SetNRGBA(0, 0, pixel)
	var encodedImage bytes.Buffer
	require.NoError(t, png.Encode(&encodedImage, fixtureImage))
	imageBytes := encodedImage.Bytes()
	decodedImage, err := png.Decode(bytes.NewReader(imageBytes))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 1, 1), decodedImage.Bounds())
	require.Equal(t, pixel, color.NRGBAModel.Convert(decodedImage.At(0, 0)))
	cdnRequests := 0
	protected := service.GetSSRFProtectedHTTPClient()
	oldTransport := protected.Transport
	t.Cleanup(func() { protected.Transport = oldTransport })
	protected.Transport = clientModelPresentationTransport(func(request *http.Request) (*http.Response, error) {
		cdnRequests++
		require.Equal(t, savedImage, request.URL.String())
		assert.Empty(t, request.Header.Get("Authorization"))
		assert.Empty(t, request.Header.Get("X-Api-Key"))
		assert.Empty(t, request.Header.Get("Cookie"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}, "Content-Length": []string{fmt.Sprint(len(imageBytes))}}, Body: io.NopCloser(bytes.NewReader(imageBytes)), ContentLength: int64(len(imageBytes)), Request: request}, nil
	})
	sanitizeURL := func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil {
			return value
		}
		query := parsed.Query()
		if query.Has(service.TaskArtifactAccessQueryParameter) {
			query.Set(service.TaskArtifactAccessQueryParameter, "REDACTED_CAPABILITY")
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
		return value
	}
	var sanitizeValue func(any) any
	sanitizeValue = func(value any) any {
		switch value := value.(type) {
		case string:
			return sanitizeURL(value)
		case []any:
			for i := range value {
				value[i] = sanitizeValue(value[i])
			}
			return value
		case map[string]any:
			for k := range value {
				value[k] = sanitizeValue(value[k])
			}
			return value
		default:
			return value
		}
	}
	cases := []map[string]any{}
	call := func(name, method, path, body, identity string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer sk-synthetichttp"+identity)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, want, recorder.Code, recorder.Body.String())
		requestHeaders := map[string]any{"Authorization": "Bearer <" + strings.ToUpper(identity) + "_RELAY_KEY>"}
		if body != "" {
			requestHeaders["Content-Type"] = "application/json"
		}
		requestBody := any(nil)
		if body != "" {
			require.NoError(t, common.Unmarshal([]byte(body), &requestBody))
		}
		responseHeaders := map[string]any{}
		for k, values := range recorder.Header() {
			responseHeaders[k] = values
		}
		response := map[string]any{"status": recorder.Code, "headers": responseHeaders}
		if strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
			var value any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &value))
			response["body"] = sanitizeValue(value)
		} else {
			response["body_base64"] = base64.StdEncoding.EncodeToString(recorder.Body.Bytes())
			response["body_sha256"] = fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes()))
			response["body_bytes"] = recorder.Body.Len()
		}
		cases = append(cases, map[string]any{"name": name, "identity": identity, "request": map[string]any{"method": method, "path": sanitizeURL(path), "headers": requestHeaders, "body": requestBody}, "response": response})
		return recorder
	}
	size := ""
	if imagesEndpoint {
		size = `,"size":"1024x1024"`
	}
	created := call("synchronous_images_success", http.MethodPost, "/v1/images/generations", `{"model":"`+modelID+`","prompt":"a synthetic fixture image","n":1`+size+`,"response_format":"url"}`, "owner", http.StatusOK)
	assert.Equal(t, taskID, created.Header().Get("X-New-Api-Task-Id"))
	assert.Contains(t, created.Body.String(), "/v1/tasks/"+taskID+"/artifacts/image-1/content")
	assert.NotContains(t, created.Body.String(), "127.0.0.1")
	var saved model.Task
	require.NoError(t, db.Where("task_id = ?", taskID).First(&saved).Error)
	assert.True(t, saved.ResultRetrievable())
	assert.JSONEq(t, `{"output_url":"`+savedImage+`"}`, string(saved.Data))
	taskPath := "/v1/tasks/" + taskID
	ownerTask := call("owner_task_status", http.MethodGet, taskPath, "", "owner", http.StatusOK)
	assert.NotContains(t, ownerTask.Body.String(), "result")
	assert.NotContains(t, ownerTask.Body.String(), "127.0.0.1")
	call("owner_artifact_manifest", http.MethodGet, taskPath+"/artifacts", "", "owner", http.StatusOK)
	content := call("owner_artifact_content", http.MethodGet, taskPath+"/artifacts/image-1/content", "", "owner", http.StatusOK)
	assert.Equal(t, imageBytes, content.Body.Bytes())
	decodedContent, err := png.Decode(bytes.NewReader(content.Body.Bytes()))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 1, 1), decodedContent.Bounds())
	require.Equal(t, pixel, color.NRGBAModel.Convert(decodedContent.At(0, 0)))
	assert.Equal(t, 1, cdnRequests)
	for _, surface := range []struct{ name, path string }{
		{"other_owner_task_not_found", taskPath},
		{"other_owner_artifacts_not_found", taskPath + "/artifacts"},
		{"other_owner_artifact_content_not_found", taskPath + "/artifacts/image-1/content"},
	} {
		call(surface.name, http.MethodGet, surface.path, "", "other", http.StatusNotFound)
	}
	assert.Equal(t, 1, cdnRequests, "another owner's ordinary API key must not fetch content")
	// A sale namespace change must not gate recovery of an already saved task.
	retired := saved
	retired.ID, retired.TaskID = 0, taskID+"_retired"
	retired.Properties.OriginModelName = "codebloom-gpt-image-2k"
	require.NoError(t, db.Create(&retired).Error)
	retiredPath := "/v1/tasks/" + retired.TaskID
	retiredStatus := call("retired_model_owner_task_status", http.MethodGet, retiredPath, "", "owner", http.StatusOK)
	assert.Contains(t, retiredStatus.Body.String(), "codebloom-gpt-image-2k")
	call("retired_model_owner_artifact_manifest", http.MethodGet, retiredPath+"/artifacts", "", "owner", http.StatusOK)
	retiredContent := call("retired_model_owner_artifact_content", http.MethodGet, retiredPath+"/artifacts/image-1/content", "", "owner", http.StatusOK)
	assert.Equal(t, imageBytes, retiredContent.Body.Bytes())
	assert.Equal(t, 2, cdnRequests)
	require.False(t, t.Failed(), "failed observations must not publish verified samples")
	sources := map[string]string{}
	for _, path := range []string{"controller/codebloom_retained_http_test.go", "controller/codebloom_protocol_test.go", "controller/relay_task_plugin_test.go", "controller/plugin_protocol_test.go", "controller/plugin_protocol_image.go", "controller/relay.go", "controller/task.go", "controller/video_proxy.go", "middleware/auth.go", "pkg/jsplugin/routing.go", "pkg/jsplugin/registry.go", "plugins/tasks/codebloom-image/plugin.js", "service/task_artifact_access.go", "service/task_artifact_store.go", "deploy/instance/toapis-image-plugin.js"} {
		contents, err := os.ReadFile(filepath.Join("..", path))
		require.NoError(t, err)
		sources[path] = fmt.Sprintf("%x", sha256.Sum256(contents))
	}
	artifact := map[string]any{
		"status": "LOCAL_REAL_HANDLER_HTTP_FIXTURES_VERIFIED", "verification_date": "2026-11-02", "database": string(common.MainDatabaseType()), "cases": cases, "source_files_sha256": sources,
		"image_fixture":      map[string]any{"encoder": "Go image/png.Encode", "png_decode": "PASS", "http_content_png_decode": "PASS", "width": 1, "height": 1, "bytes": len(imageBytes), "sha256": fmt.Sprintf("%x", sha256.Sum256(imageBytes))},
		"retention_contract": map[string]any{"plugin_key": "codebloom-image", "protocol": "openai_image", "retainResult": true, "requires_explicit_protocol_opt_in": true, "retainResult_scope": "server plugin protocol claim only; not a general ClientParameters marker", "client_metadata_has_generic_retention_marker": false, "client_opt_in": map[string]any{"x-task-plugin": map[string]any{"key": "codebloom-image", "version": "1.0.0", "api_version": 1}, "requires_response_task_id_header": true, "do_not_generalize_task_id_header": true}, "task_id_header": "X-New-Api-Task-Id", "task_body_contains_image_result": false, "recovery": "GET task status, then GET artifact manifest/content", "retained_payload": "normalized result URL/artifact metadata", "artifact_bytes_locally_persisted": service.GetTaskArtifactStore().Enabled(), "client_should_save_image_locally": true},
		"scope":              map[string]any{"supplier_completed_result": "synthetic response injected at the existing test submit dependency; actual CodeBloom parser, host persistence, renderer, TokenAuth and GET handlers executed", "production_activation_gate_bypassed_only_in_test_dependency": true, "candidate_availability_is_separately_gated": true, "production_access": 0, "paid_generations": 0, "external_network_requests": 0, "artifact_content_transport": "intercepted in memory; no Authorization/X-Api-Key/Cookie forwarded", "ownership_scope": "404 samples use ordinary relay credentials without a valid artifact capability. Existing signed capability URLs retain their separate possession-based access semantics.", "legacy_toapis": "Existing Toapis async route and client local save/cache/recovery remain unchanged; do not apply CodeBloom opt-in handling to it."},
		"redactions":         []string{"Authorization replaced by role placeholders", "artifact access query value replaced by REDACTED_CAPABILITY"},
	}
	encoded, err := common.Marshal(artifact)
	require.NoError(t, err)
	for _, secret := range []string{"synthetic-provider-secret", "synthetichttpowner", "synthetichttpother", "synthetic-artifact-fixture-signing-key"} {
		assert.NotContains(t, string(encoded), secret)
	}
	require.False(t, t.Failed(), "unsafe samples cannot be exported")
	if output := os.Getenv("TEST_RETAINED_IMAGE_HTTP_FIXTURES"); output != "" {
		if !imagesEndpoint {
			output += ".grok.json"
		}
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
}
