package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Replays paid supplier evidence through the real authenticated multipart/JSON
// routes, task ledger, settlement, canonical artifacts and signature checks.
// Every outbound request is intercepted; this test cannot generate or charge.
func TestObservedImageEditsAuthenticatedRouterDatabase(t *testing.T) {
	path := os.Getenv("TEST_IMAGE_EDIT_OBSERVED_MANIFEST")
	if path == "" {
		t.Skip("explicit private native receipts required")
	}
	raw, err := os.ReadFile(path)
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
		ImageSHA      string         `json:"image_sha256"`
	}
	require.NoError(t, common.Unmarshal(raw, &entries))
	require.NotEmpty(t, entries)
	var reports []map[string]any
	for _, entry := range entries {
		t.Run(entry.Label, func(t *testing.T) {
			f := newYcapiRouterFixture(t)
			oldAddress, oldSecret := system_setting.TaskPublicAddress, common.CryptoSecret
			system_setting.TaskPublicAddress = "https://gateway.example/harness"
			common.CryptoSecret = "synthetic-image-edit-artifact-key"
			t.Cleanup(func() { system_setting.TaskPublicAddress = oldAddress; common.CryptoSecret = oldSecret })
			if entry.Provider == "codebloom-image" {
				source, err := plugins.Source(entry.Provider)
				require.NoError(t, err)
				_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
				require.NoError(t, err)
				t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("codebloom-image") })
				base := "https://codebloom.fixture"
				ch := model.Channel{Id: 1001, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-qq-image", Name: "QQ image edit fixture", Models: entry.Model, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
				ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: entry.Provider})
				require.NoError(t, ch.Insert())
				model.InitChannelCache()
				m, ok := codebloom.Lookup(entry.Model)
				require.True(t, ok)
				require.True(t, m.ActivationReady)
				require.NotNil(t, m.RetailCNY)
				snapshot, err := model.GetModelPricingSnapshot([]string{entry.Model})
				require.NoError(t, err)
				expr := fmt.Sprintf(`tier("image", u("image_count") * (%s / %s))`, decimal.NewFromFloat(*m.RetailCNY).String(), decimal.NewFromFloat(operation_setting.USDExchangeRate).String())
				require.NoError(t, model.UpdateModelPricing([]model.ModelPricingChange{{ModelName: entry.Model, ExpectedVersion: snapshot.Entries[0].Version, Pricing: model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expr}}}))
				model.RefreshPricing()
			}
			submit, err := os.ReadFile(entry.SubmitPath)
			require.NoError(t, err)
			image, err := os.ReadFile(entry.ImagePath)
			require.NoError(t, err)
			reference, err := os.ReadFile(entry.ReferencePath)
			require.NoError(t, err)
			var poll []byte
			if entry.PollPath != "" {
				poll, err = os.ReadFile(entry.PollPath)
				require.NoError(t, err)
			}
			posts, queries, downloads := 0, 0, 0
			transport := imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
				data := image
				mime := "image/png"
				if r.Method == http.MethodPost {
					posts++
					require.Contains(t, []string{"ycapi.fixture", "codebloom.fixture"}, r.URL.Host)
					require.NotEmpty(t, r.Header.Get("Authorization"))
					mime = "application/json"
					data = submit
					if entry.Provider == "ycapi" {
						require.Equal(t, "/api/v1/model-runtime/invoke", r.URL.Path)
						var body map[string]any
						require.NoError(t, common.DecodeJson(r.Body, &body))
						require.Equal(t, "edit", body["operation"])
						require.Equal(t, entry.Lane, body["lane_id"])
						params := body["params"].(map[string]any)
						require.Len(t, params["images"], 1)
						require.Equal(t, params["images"], body["attachments"])
					} else if strings.HasPrefix(entry.Upstream, "gpt-") {
						require.Equal(t, "/v1/images/edits", r.URL.Path)
						require.NoError(t, r.ParseMultipartForm(8<<20))
						file, _, err := r.FormFile("image")
						require.NoError(t, err)
						defer file.Close()
						b, err := io.ReadAll(file)
						require.NoError(t, err)
						require.Equal(t, reference, b)
						require.Equal(t, "1", r.FormValue("n"))
					} else {
						require.Equal(t, "/v1/chat/completions", r.URL.Path)
						var body map[string]any
						require.NoError(t, common.DecodeJson(r.Body, &body))
						message := body["messages"].([]any)[0].(map[string]any)
						require.Len(t, message["content"], 2)
					}
				} else if r.URL.Host == "ycapi.fixture" {
					queries++
					data = poll
					mime = "application/json"
				} else {
					downloads++
					require.Empty(t, r.Header.Get("Authorization"))
					require.Empty(t, r.Header.Get("Cookie"))
					mime = http.DetectContentType(image)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{mime}, "Content-Length": []string{fmt.Sprint(len(data))}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: r}, nil
			})
			client, protected := service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()
			previous, previousProtected := client.Transport, protected.Transport
			client.Transport = transport
			protected.Transport = transport
			t.Cleanup(func() { client.Transport = previous; protected.Transport = previousProtected })
			var body bytes.Buffer
			request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
			if entry.Provider == "ycapi" {
				encoded, err := common.Marshal(entry.Request)
				require.NoError(t, err)
				request.Body = io.NopCloser(bytes.NewReader(encoded))
				request.ContentLength = int64(len(encoded))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Prefer", "respond-async")
			} else {
				writer := multipart.NewWriter(&body)
				for key, value := range entry.Request {
					require.NoError(t, writer.WriteField(key, fmt.Sprint(value)))
				}
				part, err := writer.CreateFormFile("image[]", "reference.png")
				require.NoError(t, err)
				_, err = part.Write(reference)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				// Set an explicit source MIME; browser FormData sends image/png too.
				content := bytes.ReplaceAll(body.Bytes(), []byte("Content-Type: application/octet-stream"), []byte("Content-Type: image/png"))
				request.Body = io.NopCloser(bytes.NewReader(content))
				request.ContentLength = int64(len(content))
				request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			request.Header.Set("Authorization", "Bearer sk-h3routermember")
			response := httptest.NewRecorder()
			f.engine.ServeHTTP(response, request)
			if entry.Provider == "ycapi" {
				require.Equal(t, 202, response.Code, response.Body.String())
				require.Zero(t, queries)
				require.Equal(t, "respond-async", response.Header().Get("Preference-Applied"))
				service.RunTaskPollingOnce(context.Background(), nil)
			} else {
				require.Equal(t, 200, response.Code, response.Body.String())
			}
			id := response.Header().Get("X-New-Api-Task-Id")
			require.NotEmpty(t, id)
			require.Equal(t, 1, posts)
			task := f.task(t, id)
			require.EqualValues(t, model.TaskStatusSuccess, task.Status)
			require.Greater(t, task.Quota, 0)
			manifest := f.request(t, http.MethodGet, "/v1/tasks/"+id+"/artifacts", "", "h3routermember")
			require.Equal(t, 200, manifest.Code, manifest.Body.String())
			var listing struct {
				Artifacts []struct {
					Key string `json:"key"`
					URL string `json:"content_url"`
				} `json:"artifacts"`
			}
			require.NoError(t, common.Unmarshal(manifest.Body.Bytes(), &listing))
			require.Len(t, listing.Artifacts, 1)
			artifact := listing.Artifacts[0]
			parsed, err := url.Parse(artifact.URL)
			require.NoError(t, err)
			contentPath := strings.TrimPrefix(parsed.EscapedPath(), "/harness") + "?" + parsed.RawQuery
			for range 2 {
				read := httptest.NewRequest(http.MethodGet, contentPath, nil)
				saved := httptest.NewRecorder()
				f.engine.ServeHTTP(saved, read)
				require.Equal(t, 200, saved.Code, saved.Body.String())
				require.Equal(t, entry.ImageSHA, fmt.Sprintf("%x", sha256.Sum256(saved.Body.Bytes())))
			}
			status := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routermember")
			require.Equal(t, 200, status.Code)
			require.NotContains(t, status.Body.String(), "synthetic-qq-image")
			foreign := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
			require.Equal(t, 404, foreign.Code)
			var charges int64
			require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ? AND model_name = ?", model.LogTypeConsume, entry.Model).Count(&charges).Error)
			require.EqualValues(t, 1, charges)
			require.Equal(t, 1, posts)
			require.Equal(t, 2, downloads)
			var parameters map[string]any
			if entry.Provider == "ycapi" {
				parameters, _ = model.YcapiRuntimeCatalog(jsplugin.DefaultRegistry.Generation(), entry.Model)
			} else {
				parameters = model.CodeBloomRuntimeParameters(jsplugin.DefaultRegistry.Generation(), entry.Model)
			}
			reports = append(reports, map[string]any{"label": entry.Label, "model_name": entry.Model, "kind": "image", "parameters": parameters, "request": entry.Request, "image_path": entry.ImagePath, "image_sha256": entry.ImageSHA, "reference_path": entry.ReferencePath, "one_submission": true, "one_charge": true, "original_artifact_read_twice": true, "owner_isolation": true})
		})
	}
	if out := os.Getenv("TEST_IMAGE_EDIT_HTTP_REPORT"); out != "" {
		b, err := common.Marshal(reports)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(out, b, 0o600))
	}
}

func TestYcapiReferenceImageUploadDoesNotGenerateOrCharge(t *testing.T) {
	f := newYcapiRouterFixture(t)
	base := "https://ycapi.token6688.com"
	require.NoError(t, f.db.Model(&model.Channel{}).Where("id = ?", 999).Update("base_url", base).Error)
	model.InitChannelCache()
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	source := pngBytes.Bytes()
	posts := 0
	transport := imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
		posts++
		require.Equal(t, "https://ycapi.token6688.com/v1/files", r.URL.String())
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "Bearer synthetic-ycapi-only", r.Header.Get("Authorization"))
		require.NoError(t, r.ParseMultipartForm(8<<20))
		file, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		bytes, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, source, bytes)
		body, err := common.Marshal(map[string]any{"id": "file-synthetic", "object": "file", "content_type": "image/png", "bytes": len(source), "url": "https://assets.token6688.com/sha256/ab/" + strings.Repeat("c", 62) + ".png"})
		require.NoError(t, err)
		return &http.Response{StatusCode: 201, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	client := service.GetHttpClient()
	previous := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = previous })
	var before, after model.User
	require.NoError(t, f.db.First(&before, 792).Error)
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	require.NoError(t, writer.WriteField("model", "ycapi-gemini-nano-banana-2.1-image"))
	file, err := writer.CreateFormFile("file", "reference.png")
	require.NoError(t, err)
	_, err = file.Write(source)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/images", bytes.NewReader(input.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer sk-h3routermember")
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, req)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"success":true`)
	require.NotContains(t, response.Body.String(), "synthetic-ycapi-only")
	require.Equal(t, 1, posts)
	require.NoError(t, f.db.First(&after, 792).Error)
	require.Equal(t, before.Quota, after.Quota)
	var tasks, charges int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&charges).Error)
	require.Zero(t, tasks)
	require.Zero(t, charges)
}
