package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
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

type codeBloomObservedImageFixture struct {
	ModelID             string            `json:"model_id"`
	UpstreamID          string            `json:"upstream_id"`
	ResponsePath        string            `json:"response_path"`
	ResponseSHA256      string            `json:"response_sha256"`
	RequestPath         string            `json:"request_path"`
	ImagePath           string            `json:"image_path"`
	ImageSHA256         string            `json:"image_sha256"`
	PrimaryURLSHA256    string            `json:"primary_url_sha256"`
	Width               int               `json:"width"`
	Height              int               `json:"height"`
	ArtifactHTTPFiles   map[string]string `json:"artifact_http_files_sha256"`
	EvidenceFiles       map[string]string `json:"evidence_files_sha256"`
	ResponseFormat      string            `json:"-"`
	responseBody        []byte
	imageBytes          []byte
	artifactBodies      map[string][]byte
	primaryURL          string
	prompt              string
	originalRequestBody map[string]any
}

// Private opt-in fixtures retain the original supplier response and artifact
// bytes outside Git. Every outbound media request is intercepted in memory.
// Neither a successful fixture nor a known price can activate a candidate.
func TestCodeBloomObservedImageRetainedHTTPFixturesDatabase(t *testing.T) {
	manifestPath := os.Getenv("TEST_CODEBLOOM_ORIGINAL_IMAGE_FIXTURES")
	if manifestPath == "" {
		t.Skip("original private supplier evidence requires an explicit fixture manifest")
	}
	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.Equal(t, os.Getenv("TEST_CODEBLOOM_ORIGINAL_IMAGE_FIXTURES_SHA256"), fmt.Sprintf("%x", sha256.Sum256(raw)))
	var fixtures []codeBloomObservedImageFixture
	require.NoError(t, common.Unmarshal(raw, &fixtures))
	require.Len(t, fixtures, 2)
	for _, fixture := range fixtures {
		t.Run(fixture.UpstreamID, func(t *testing.T) {
			expected := map[string]struct {
				response, image string
				width, height   int
			}{
				"ys-grok-imagine-image":             {"7dcc0c70c9e82f9853d7d73e26566b66fdb87d852867f896686749877c93c278", "cad75605e881863a7b6cf6e97bfa6f7c2b90e4484b3e05c69559392865053af2", 768, 1152},
				"ys-gemini-3.1-flash-image-preview": {"4c8eed26cd6715e268a3684bfa3ecd45300ff00ab4d32b54bd14bc6ca0da07e0", "837210f2ae3aedc365d10b346c2918dcde8f5cd8a10e07528956e41e92fbc0fd", 1408, 768},
			}
			proof, ok := expected[fixture.ModelID]
			require.True(t, ok)
			require.Equal(t, "ys-"+fixture.UpstreamID, fixture.ModelID)
			require.Equal(t, proof.response, fixture.ResponseSHA256)
			require.Equal(t, proof.image, fixture.ImageSHA256)
			require.Equal(t, proof.width, fixture.Width)
			require.Equal(t, proof.height, fixture.Height)
			for path, digest := range fixture.EvidenceFiles {
				contents, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				require.Equal(t, digest, fmt.Sprintf("%x", sha256.Sum256(contents)))
			}
			fixture.responseBody, err = os.ReadFile(fixture.ResponsePath)
			require.NoError(t, err)
			require.Equal(t, fixture.ResponseSHA256, fmt.Sprintf("%x", sha256.Sum256(fixture.responseBody)))
			fixture.imageBytes, err = os.ReadFile(fixture.ImagePath)
			require.NoError(t, err)
			require.Equal(t, fixture.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(fixture.imageBytes)))
			requestRaw, readErr := os.ReadFile(fixture.RequestPath)
			require.NoError(t, readErr)
			var original struct {
				JSON map[string]any `json:"json"`
			}
			require.NoError(t, common.Unmarshal(requestRaw, &original))
			fixture.originalRequestBody = original.JSON
			messages := original.JSON["messages"].([]any)
			fixture.prompt = messages[0].(map[string]any)["content"].(string)
			fixture.artifactBodies = map[string][]byte{}
			for path, digest := range fixture.ArtifactHTTPFiles {
				contents, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				require.Equal(t, digest, fmt.Sprintf("%x", sha256.Sum256(contents)))
				var observed struct {
					Method string `json:"method"`
					URL    string `json:"url"`
					Status int    `json:"http_status"`
					Body   string `json:"body_b64"`
				}
				require.NoError(t, common.Unmarshal(contents, &observed))
				require.Equal(t, http.MethodGet, observed.Method)
				require.Equal(t, http.StatusOK, observed.Status)
				body, decodeErr := base64.StdEncoding.DecodeString(observed.Body)
				require.NoError(t, decodeErr)
				require.Equal(t, fixture.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(body)))
				fixture.artifactBodies[observed.URL] = body
				decoded, _, decodeErr := image.Decode(bytes.NewReader(body))
				require.NoError(t, decodeErr)
				require.Equal(t, image.Rect(0, 0, fixture.Width, fixture.Height), decoded.Bounds())
				if fmt.Sprintf("%x", sha256.Sum256([]byte(observed.URL))) == fixture.PrimaryURLSHA256 {
					fixture.primaryURL = observed.URL
				}
			}
			require.NotEmpty(t, fixture.primaryURL, "the exact original primary reference must be present")
			if fixture.ModelID == "ys-gemini-3.1-flash-image-preview" {
				require.Len(t, fixture.artifactBodies, 2, "both original mirror references must be covered")
			} else {
				require.Len(t, fixture.artifactBodies, 1)
			}
			variants := []map[string]any{}
			for _, format := range []string{"url", "b64_json"} {
				fixture.ResponseFormat = format
				var report map[string]any
				require.True(t, t.Run(format, func(t *testing.T) {
					report = codeBloomRetainedImageHTTPFixture(t, fixture.ModelID, fixture.UpstreamID, false, fixture)
				}))
				variants = append(variants, report)
			}
			report := map[string]any{"status": "LOCAL_ORIGINAL_RESPONSE_RETAINED_HANDLER_BOTH_FORMATS_VERIFIED", "model_id": fixture.ModelID, "upstream_id": fixture.UpstreamID, "variants": variants, "original_response_sha256": fixture.ResponseSHA256, "original_image_sha256": fixture.ImageSHA256, "evidence_files_sha256": fixture.EvidenceFiles, "supplier_requests": 0, "paid_generations": 0, "production_access": 0}
			encoded, marshalErr := common.Marshal(report)
			require.NoError(t, marshalErr)
			for rawURL := range fixture.artifactBodies {
				require.False(t, strings.Contains(string(encoded), rawURL), "supplier URLs must remain private")
			}
			dir := os.Getenv("TEST_CODEBLOOM_ORIGINAL_IMAGE_REPORT_DIR")
			require.NotEmpty(t, dir)
			require.NoError(t, os.WriteFile(filepath.Join(dir, fixture.UpstreamID+".json"), encoded, 0o600))
		})
	}
}

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

func codeBloomRetainedImageHTTPFixture(t *testing.T, modelID, upstreamID string, imagesEndpoint bool, observed ...codeBloomObservedImageFixture) map[string]any {
	var original *codeBloomObservedImageFixture
	if len(observed) != 0 {
		require.Len(t, observed, 1)
		original = &observed[0]
	}
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
	savedImage := "https://127.0.0.1/local-synthetic-image.png"
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
	var engine *gin.Engine
	presentationDownloads := 0
	if original != nil {
		deps.downloadImage = func(rawURL string) (string, string, error) {
			presentationDownloads++
			parsed, parseErr := url.Parse(rawURL)
			require.NoError(t, parseErr)
			path := strings.TrimPrefix(parsed.EscapedPath(), "/harness") + "?" + parsed.RawQuery
			request := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code, "retained b64_json must use the authenticated content handler")
			require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())))
			return recorder.Header().Get("Content-Type"), base64.StdEncoding.EncodeToString(recorder.Body.Bytes()), nil
		}
	}
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
			if original != nil {
				body, buildErr := adaptor.BuildRequestBody(c, info)
				require.NoError(t, buildErr)
				var built map[string]any
				require.NoError(t, common.DecodeJson(body, &built))
				require.Equal(t, original.originalRequestBody, built, "the declared route must build the exact original request semantics")
				builtURL, buildErr := adaptor.BuildRequestURL(info)
				require.NoError(t, buildErr)
				require.Equal(t, "https://supplier.invalid/v1/chat/completions", builtURL)
				facts, factsErr := adaptor.ExtractUsageFactsValidated(c, info)
				require.NoError(t, factsErr)
				require.EqualValues(t, 1, facts["image_count"], "mirror URLs cannot create a second billed image request")
			}
			response := `{"data":[{"url":"` + savedImage + `"}]}`
			if !imagesEndpoint {
				response = `{"model":"` + upstreamID + `","choices":[{"finish_reason":"stop","message":{"content":"![image](` + savedImage + `)","images":[{"image_url":{"url":"` + savedImage + `"}}]}}]}`
			}
			if original != nil {
				response = string(original.responseBody)
			}
			result, taskErr := adaptor.ParseResponse(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, info)
			if taskErr != nil {
				return nil, taskErr
			}
			if original != nil {
				var normalized struct {
					OutputURL string `json:"output_url"`
				}
				require.NoError(t, common.Unmarshal(result.TaskData, &normalized))
				require.True(t, normalized.OutputURL == original.primaryURL, "the parser must choose the original explicit image, not a bare mirror")
				savedImage = normalized.OutputURL
			}
			return &relay.TaskSubmitResult{Platform: "codebloom-image", UpstreamTaskID: result.UpstreamTaskID, TaskData: result.TaskData, PluginState: result.PluginState, Immediate: result.Immediate}, nil
		})
	}
	engine = gin.New()
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
	if original == nil {
		authenticated.GET("/v1/tasks/:key/artifacts/:artifact_key/content", TaskArtifactContent)
	} else {
		engine.GET("/v1/tasks/:key/artifacts/:artifact_key/content", middleware.TokenOrTaskArtifactAccessAuth("key", "artifact_key"), TaskArtifactContent)
	}
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
	if original != nil {
		imageBytes = original.imageBytes
		decoded, _, decodeErr := image.Decode(bytes.NewReader(imageBytes))
		require.NoError(t, decodeErr)
		require.Equal(t, image.Rect(0, 0, original.Width, original.Height), decoded.Bounds())
	}
	cdnRequests := 0
	protected := service.GetSSRFProtectedHTTPClient()
	oldTransport := protected.Transport
	t.Cleanup(func() { protected.Transport = oldTransport })
	protected.Transport = clientModelPresentationTransport(func(request *http.Request) (*http.Response, error) {
		cdnRequests++
		body := imageBytes
		if original == nil {
			require.Equal(t, savedImage, request.URL.String())
		} else {
			var present bool
			body, present = original.artifactBodies[request.URL.String()]
			require.True(t, present, "unexpected HTTP destination; the fixture never permits a supplier POST or other network request")
			require.Equal(t, http.MethodGet, request.Method)
		}
		assert.Empty(t, request.Header.Get("Authorization"))
		assert.Empty(t, request.Header.Get("X-Api-Key"))
		assert.Empty(t, request.Header.Get("Cookie"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{http.DetectContentType(body)}, "Content-Length": []string{fmt.Sprint(len(body))}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
	})
	baselineRequests := 0
	if original != nil {
		for rawURL := range original.artifactBodies {
			if rawURL == original.primaryURL {
				continue
			}
			response, fetchErr := protected.Get(rawURL)
			require.NoError(t, fetchErr)
			body, readErr := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, readErr)
			require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(body)))
			decoded, _, decodeErr := image.Decode(bytes.NewReader(body))
			require.NoError(t, decodeErr)
			require.Equal(t, image.Rect(0, 0, original.Width, original.Height), decoded.Bounds())
		}
		baselineRequests = cdnRequests
	}
	sanitizeURL := func(value string) string {
		if original != nil {
			for rawURL := range original.artifactBodies {
				value = strings.ReplaceAll(value, rawURL, "REDACTED_SUPPLIER_IMAGE_URL")
			}
		}
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
		if identity != "" {
			request.Header.Set("Authorization", "Bearer sk-synthetichttp"+identity)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if original == nil {
			require.Equal(t, want, recorder.Code, recorder.Body.String())
		} else {
			require.Equal(t, want, recorder.Code, "source-bound fixture HTTP status differs")
		}
		requestHeaders := map[string]any{}
		if identity != "" {
			requestHeaders["Authorization"] = "Bearer <" + strings.ToUpper(identity) + "_RELAY_KEY>"
		}
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
	createBody := `{"model":"` + modelID + `","prompt":"a synthetic fixture image","n":1` + size + `,"response_format":"url"}`
	if original != nil {
		encoded, encodeErr := common.Marshal(map[string]any{"model": modelID, "prompt": original.prompt, "n": 1, "response_format": original.ResponseFormat})
		require.NoError(t, encodeErr)
		createBody = string(encoded)
	}
	created := call("synchronous_images_success", http.MethodPost, "/v1/images/generations", createBody, "owner", http.StatusOK)
	assert.Equal(t, taskID, created.Header().Get("X-New-Api-Task-Id"))
	assert.Contains(t, created.Body.String(), "/v1/tasks/"+taskID+"/artifacts/image-1/content")
	assert.NotContains(t, created.Body.String(), "127.0.0.1")
	var saved model.Task
	require.NoError(t, db.Where("task_id = ?", taskID).First(&saved).Error)
	assert.True(t, saved.ResultRetrievable())
	if original == nil {
		assert.JSONEq(t, `{"output_url":"`+savedImage+`"}`, string(saved.Data))
	} else {
		var compact map[string]any
		require.NoError(t, common.Unmarshal(saved.Data, &compact))
		require.Len(t, compact, 1, "raw supplier response and mirror must not enter the retained task")
		require.True(t, compact["output_url"] == original.primaryURL)
		var presented struct {
			Data []struct {
				URL string `json:"url"`
				B64 string `json:"b64_json"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(created.Body.Bytes(), &presented))
		require.Len(t, presented.Data, 1)
		for rawURL := range original.artifactBodies {
			require.False(t, strings.Contains(created.Body.String(), rawURL), "presentation must hide provider URLs")
		}
		if original.ResponseFormat == "b64_json" {
			body, decodeErr := base64.StdEncoding.DecodeString(presented.Data[0].B64)
			require.NoError(t, decodeErr)
			require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(body)))
		} else {
			require.Empty(t, presented.Data[0].B64)
		}
	}
	taskPath := "/v1/tasks/" + taskID
	ownerTask := call("owner_task_status", http.MethodGet, taskPath, "", "owner", http.StatusOK)
	assert.NotContains(t, ownerTask.Body.String(), "result")
	assert.NotContains(t, ownerTask.Body.String(), "127.0.0.1")
	call("owner_artifact_manifest", http.MethodGet, taskPath+"/artifacts", "", "owner", http.StatusOK)
	content := call("owner_artifact_content", http.MethodGet, taskPath+"/artifacts/image-1/content", "", "owner", http.StatusOK)
	if original == nil {
		assert.Equal(t, imageBytes, content.Body.Bytes())
	}
	decodedContent, _, err := image.Decode(bytes.NewReader(content.Body.Bytes()))
	require.NoError(t, err)
	if original == nil {
		require.Equal(t, image.Rect(0, 0, 1, 1), decodedContent.Bounds())
		require.Equal(t, pixel, color.NRGBAModel.Convert(decodedContent.At(0, 0)))
	} else {
		require.Equal(t, image.Rect(0, 0, original.Width, original.Height), decodedContent.Bounds())
		require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(content.Body.Bytes())))
	}
	assert.Equal(t, baselineRequests+presentationDownloads+1, cdnRequests)
	for _, surface := range []struct{ name, path string }{
		{"other_owner_task_not_found", taskPath},
		{"other_owner_artifacts_not_found", taskPath + "/artifacts"},
		{"other_owner_artifact_content_not_found", taskPath + "/artifacts/image-1/content"},
	} {
		call(surface.name, http.MethodGet, surface.path, "", "other", http.StatusNotFound)
	}
	assert.Equal(t, baselineRequests+presentationDownloads+1, cdnRequests, "another owner's ordinary API key must not fetch content")
	capabilityDownloads := 0
	if original != nil {
		contentURL, issueErr := service.BuildTaskArtifactContentURL(taskID, "image-1")
		require.NoError(t, issueErr)
		parsed, parseErr := url.Parse(contentURL)
		require.NoError(t, parseErr)
		localPath := strings.TrimPrefix(parsed.EscapedPath(), "/harness")
		query := parsed.Query()
		access := query.Get(service.TaskArtifactAccessQueryParameter)
		require.True(t, service.VerifyTaskArtifactAccess(access, taskID, "image-1"))
		capability := call("signed_capability_content", http.MethodGet, localPath+"?"+query.Encode(), "", "", http.StatusOK)
		require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(capability.Body.Bytes())))
		capabilityDownloads++
		call("anonymous_content_unauthorized", http.MethodGet, localPath, "", "", http.StatusUnauthorized)
		call("capability_wrong_task_not_found", http.MethodGet, strings.Replace(localPath, taskID, taskID+"_wrong", 1)+"?"+query.Encode(), "", "", http.StatusNotFound)
		call("capability_wrong_artifact_not_found", http.MethodGet, strings.Replace(localPath, "image-1", "image-other", 1)+"?"+query.Encode(), "", "", http.StatusNotFound)
		query.Set(service.TaskArtifactAccessQueryParameter, strings.Repeat("A", len(access)))
		call("tampered_capability_not_found", http.MethodGet, localPath+"?"+query.Encode(), "", "", http.StatusNotFound)
		assert.Equal(t, baselineRequests+presentationDownloads+capabilityDownloads+1, cdnRequests, "denied requests must not download content")
	}
	// A sale namespace change must not gate recovery of an already saved task.
	retired := saved
	retired.ID, retired.TaskID = 0, taskID+"_retired"
	retired.Properties.OriginModelName = "codebloom-gpt-image-2k"
	if original != nil {
		retired.Properties.OriginModelName = "codebloom-" + upstreamID
	}
	require.NoError(t, db.Create(&retired).Error)
	retiredPath := "/v1/tasks/" + retired.TaskID
	retiredStatus := call("retired_model_owner_task_status", http.MethodGet, retiredPath, "", "owner", http.StatusOK)
	assert.Contains(t, retiredStatus.Body.String(), retired.Properties.OriginModelName)
	call("retired_model_owner_artifact_manifest", http.MethodGet, retiredPath+"/artifacts", "", "owner", http.StatusOK)
	retiredContent := call("retired_model_owner_artifact_content", http.MethodGet, retiredPath+"/artifacts/image-1/content", "", "owner", http.StatusOK)
	if original == nil {
		assert.Equal(t, imageBytes, retiredContent.Body.Bytes())
	} else {
		require.Equal(t, original.ImageSHA256, fmt.Sprintf("%x", sha256.Sum256(retiredContent.Body.Bytes())))
	}
	assert.Equal(t, baselineRequests+presentationDownloads+capabilityDownloads+2, cdnRequests)
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
	if original != nil {
		artifact["model_id"], artifact["upstream_id"], artifact["response_format"] = modelID, upstreamID, original.ResponseFormat
		artifact["original_response_sha256"] = original.ResponseSHA256
		artifact["image_fixture"] = map[string]any{"decoder": "Go image.Decode", "http_content_full_decode": "PASS", "width": original.Width, "height": original.Height, "bytes": len(imageBytes), "sha256": original.ImageSHA256, "original_references_byte_identical": len(original.artifactBodies), "default_output_only": true}
		artifact["scope"].(map[string]any)["supplier_completed_result"] = "Exact frozen original supplier response and actual original artifact bytes injected only at the test submit dependency; actual plugin builder/parser, durable task, TokenAuth, signed capability, owner isolation and artifact content handlers executed"
		artifact["scope"].(map[string]any)["upstream_post_requests"] = 0
		artifact["redactions"] = append(artifact["redactions"].([]string), "original supplier image URLs omitted")
	}
	encoded, err := common.Marshal(artifact)
	require.NoError(t, err)
	for _, secret := range []string{"synthetic-provider-secret", "synthetichttpowner", "synthetichttpother", "synthetic-artifact-fixture-signing-key"} {
		assert.NotContains(t, string(encoded), secret)
	}
	require.False(t, t.Failed(), "unsafe samples cannot be exported")
	if output := os.Getenv("TEST_RETAINED_IMAGE_HTTP_FIXTURES"); output != "" && original == nil {
		if !imagesEndpoint {
			output += ".grok.json"
		}
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
	return artifact
}
