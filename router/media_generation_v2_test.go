package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	"github.com/QuantumNous/new-api/plugins"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestMediaGenerationV2ActualRouterQuoteReferenceAndReceiptRecovery(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) { testMediaGenerationV2RouterReferenceReceipt(t, kind, "") })
	}
}
func TestMediaGenerationV2RejectedOriginalOperationsNeverChargeOrMultiplyTasks(t *testing.T) {
	for _, condition := range []string{"capacity", "balance"} {
		t.Run(condition, func(t *testing.T) { testMediaGenerationV2RouterReferenceReceipt(t, "image", condition) })
	}
}
func testMediaGenerationV2RouterReferenceReceipt(t *testing.T, kind, condition string) {
	f := newH3RouterFixture(t, 1)
	common.OptionMapRWMutex.Lock()
	previousPublished, hadPublished := common.OptionMap[model.ModelConfigurationKey]
	delete(common.OptionMap, model.ModelConfigurationKey)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadPublished {
			common.OptionMap[model.ModelConfigurationKey] = previousPublished
		} else {
			delete(common.OptionMap, model.ModelConfigurationKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.MigrateMediaGenerationQuote(f.db))
	require.NoError(t, model.MigrateMediaGenerationAsset(f.db))
	require.NoError(t, model.MigrateMediaGenerationUpload(f.db))
	t.Setenv("MEDIA_ASSET_STORAGE_DIR", t.TempDir())
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("ycapi") })
	name := "ycapi-gpt-image-2.6-flare"
	if kind == "video" {
		name = "ycapi-seedance-2-5"
	}
	contract, ok := ycapi.LookupMediaContract(name)
	require.True(t, ok)
	base := "https://ycapi.token6688.com"
	ch := model.Channel{Id: 981, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-v2-provider-key", Name: "V2 isolated local channel", Models: name, Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	ch.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, ch.Insert())
	model.InitChannelCache()
	snapshot, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 1)
	entry := snapshot.Entries[0]
	definition := entry.Definition
	definition.Kind = kind
	definition.Enabled = true
	definition.DisplayName = "GPT Image 2.6（图生图都可）"
	definition.Source = "isolated synthetic v2 qualification only"
	definition.Parameters = nil
	definition.Routes = []model.ModelConfigurationRoute{{ChannelID: ch.Id, Group: "default", UpstreamModel: name, Enabled: true}}
	definition.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: contract.NativeID + "@fixture:only", Intents: ycapi.MediaContractIntents(contract), QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: service.MediaGenerationDigest(contract), CheckedAt: "2026-11-23"}
	quantity := "image_count"
	if kind == "video" {
		quantity = "seconds"
	}
	definition.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": fmt.Sprintf(`tier("synthetic", u("%s") * (0.2 / %g))`, quantity, operation_setting.USDExchangeRate)}
	require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, entry.BaseVersion, definition))
	snapshot, err = model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{name}, ""))
	legacySave, err := model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	legacyDefinition := legacySave.Entries[0].Definition
	legacyDefinition.MediaExecution = nil
	require.NoError(t, model.SaveModelConfigurationDraft(legacySave.Revision, legacySave.Entries[0].BaseVersion, legacyDefinition))
	legacySave, err = model.GetModelConfigurationSnapshot(name)
	require.NoError(t, err)
	require.Equal(t, definition.MediaExecution, legacySave.Entries[0].Draft.Definition.MediaExecution, "older dashboard omission cannot delist native execution")
	model.RefreshPricing()
	var posts, uploads, quotes atomic.Int64
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	oldAddress := system_setting.TaskPublicAddress
	system_setting.TaskPublicAddress = "https://gateway.media-fixture/harness"
	t.Cleanup(func() { system_setting.TaskPublicAddress = oldAddress })
	fetch := system_setting.GetFetchSetting()
	oldProtection := fetch.EnableSSRFProtection
	fetch.EnableSSRFProtection = false
	t.Cleanup(func() { fetch.EnableSSRFProtection = oldProtection })
	var outputPNG bytes.Buffer
	require.NoError(t, png.Encode(&outputPNG, image.NewNRGBA(image.Rect(0, 0, 32, 24))))
	var outputGIF bytes.Buffer
	require.NoError(t, gif.Encode(&outputGIF, image.NewPaletted(image.Rect(0, 0, 32, 24), color.Palette{color.Black, color.White}), nil))
	mediaBytes := map[string][]byte{"/main.png": outputPNG.Bytes(), "/motion.gif": outputGIF.Bytes(), "/layer.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="24"><rect width="32" height="24" fill="blue"/></svg>`)}
	mimeTypes := map[string]string{"/main.png": "image/png", "/motion.gif": "image/gif", "/layer.svg": "image/svg+xml"}
	if kind == "video" {
		path := filepath.Join(t.TempDir(), "result.mp4")
		require.NoError(t, exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=48x32:r=24:d=0.5", "-c:v", "mpeg4", "-f", "mp4", "-y", path).Run())
		data, e := os.ReadFile(path)
		require.NoError(t, e)
		mediaBytes["/clip.mp4"] = data
		mimeTypes["/clip.mp4"] = "video/mp4"
	}
	var referenceSHA string
	var envelopes []map[string]any
	client.Transport = imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cdn.media-fixture" {
			require.Empty(t, r.Header.Get("Authorization"), "provider credentials must not reach artifact storage")
			data, ok := mediaBytes[r.URL.Path]
			require.True(t, ok)
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", mimeTypes[r.URL.Path])
			http.ServeContent(rec, r, filepath.Base(r.URL.Path), time.Unix(1, 0), bytes.NewReader(data))
			return rec.Result(), nil
		}
		require.Equal(t, "ycapi.token6688.com", r.URL.Host, "all outgoing requests must be synthetic and intercepted")
		body := ""
		status := 200
		switch r.URL.Path {
		case "/v1/files":
			uploads.Add(1)
			require.NoError(t, r.ParseMultipartForm(2<<20))
			file, _, e := r.FormFile("file")
			require.NoError(t, e)
			actual, e := io.ReadAll(file)
			require.NoError(t, e)
			file.Close()
			referenceSHA = fmt.Sprintf("%x", sha256.Sum256(actual))
			body = fmt.Sprintf(`{"id":"file_fixture","object":"file","url":"https://assets.token6688.com/sha256/%s.png","content_type":"image/png","bytes":%d}`, referenceSHA, len(actual))
			status = 201
		case "/v1/pricing-estimate":
			quotes.Add(1)
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			require.Equal(t, definition.MediaExecution.LaneID, request["pinned_lane_id"])
			params := request["params"].(map[string]any)
			require.Len(t, params["images"], 1)
			body = fmt.Sprintf(`{"effective_total_rmb":0.02,"billing_mode":"fixed_amount","param_adjustments":[],"channels":[{"lane":%q,"is_delivery_head":true}]}`, definition.MediaExecution.LaneID)
		case "/api/v1/model-runtime/dry-run":
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			nativeParams := request["params"]
			bodyBytes, _ := common.Marshal(map[string]any{"object": "model_runtime.dry_run", "would_call_vendor": false, "pinned_lane": definition.MediaExecution.LaneID, "modality": kind, "wire": map[string]any{"channel_id": definition.MediaExecution.LaneID, "method": "POST", "body": nativeParams}})
			body = string(bodyBytes)
		case "/api/v1/model-runtime/invoke":
			posts.Add(1)
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			envelopes = append(envelopes, request)
			if kind == "image" {
				require.Equal(t, "edit", request["operation"])
			} else {
				require.NotContains(t, request, "operation")
				require.Equal(t, "video", request["modality"])
			}
			require.Equal(t, definition.MediaExecution.LaneID, request["lane_id"])
			params := request["params"].(map[string]any)
			require.Len(t, params["images"], 1)
			require.Contains(t, fmt.Sprint(params["images"]), referenceSHA)
			body = fmt.Sprintf(`{"task_id":"v2_original_provider_task","status":"pending","is_final":false,"model":{"public_model":%q}}`, contract.UpstreamPublicID)
		case "/v1/tasks/v2_original_provider_task":
			outputs := []map[string]any{}
			if kind == "image" {
				for _, path := range []string{"/main.png", "/motion.gif", "/layer.svg"} {
					outputs = append(outputs, map[string]any{"url": "https://cdn.media-fixture" + path, "type": "image", "mime_type": mimeTypes[path]})
				}
			} else {
				outputs = append(outputs, map[string]any{"url": "https://cdn.media-fixture/clip.mp4", "type": "video", "mime_type": "video/mp4"})
			}
			raw, _ := common.Marshal(map[string]any{"task_id": "v2_original_provider_task", "status": "completed", "is_final": true, "model": map[string]any{"public_model": contract.UpstreamPublicID}, "data": outputs})
			body = string(raw)
		default:
			return nil, fmt.Errorf("fixture denies unexpected outbound %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	catalog := f.request(t, "GET", "/v1/media-v2/models", "", "h3routermember")
	require.Equal(t, 200, catalog.Code, catalog.Body.String())
	var result map[string]any
	require.NoError(t, common.Unmarshal(catalog.Body.Bytes(), &result))
	models := result["data"].(map[string]any)["models"].([]any)
	require.Len(t, models, 1)
	version := models[0].(map[string]any)["model_version"]
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 512, 512))))
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	require.NoError(t, writer.WriteField("model", name))
	require.NoError(t, writer.WriteField("client_asset_id", "8deaa249-bc33-4fd4-bc68-42603e962d01"))
	file, err := writer.CreateFormFile("file", "reference.png")
	require.NoError(t, err)
	_, err = file.Write(picture.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	r := httptest.NewRequest("POST", "/v1/media-v2/assets", &upload)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer sk-h3routermember")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
	asset := result["data"].(map[string]any)
	require.NotContains(t, asset, "object_key")
	require.NotContains(t, asset, "owner_digest")
	require.NotContains(t, asset, "url")
	request := map[string]any{"client_operation_id": "646b0609-72ba-4682-bc61-7c02a160b14c", "model": name, "model_version": version, "intent": "reference", "parameters": map[string]any{"mode": "image-edit", "prompt": "保留参考图形状，换成蓝色背景"}, "assets": map[string]any{"images": []any{asset["asset_id"]}}}
	if kind == "video" {
		request["intent"] = "frames"
		request["parameters"] = map[string]any{"mode": "first-frame", "duration": "5", "prompt": "保留参考画面，镜头缓慢向前移动"}
	}
	encoded, _ := common.Marshal(request)
	quote := f.request(t, "POST", "/v1/media-v2/quotes", string(encoded), "h3routermember")
	require.Equal(t, 200, quote.Code, quote.Body.String())
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &result))
	q := result["data"].(map[string]any)
	if kind == "image" {
		require.Equal(t, "0.2000054", q["retail_cny"])
	} else {
		require.Equal(t, "0.9999978", q["retail_cny"], "the quote includes exact existing quota rounding")
	}
	require.NotContains(t, q, "cost_cny")
	require.NotContains(t, q, "lane_id")
	require.NotContains(t, q, "credits")
	recovery := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routermember")
	require.Equal(t, 200, recovery.Code, recovery.Body.String())
	require.Contains(t, recovery.Body.String(), q["quote_id"])
	require.Contains(t, recovery.Body.String(), "retail_cny")
	if condition == "" {
		// A new configuration authorizes a new free quote, not a second user
		// operation. Keep the original body/material and rotate only the version.
		oldQuoteID, oldRequestSHA := q["quote_id"], q["request_sha256"]
		latest, e := model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		changed := latest.Entries[0].Definition
		changed.Parameters = map[string]any{"properties": map[string]any{"count": map[string]any{"type": "integer", "maximum": float64(1)}}}
		require.NoError(t, model.SaveModelConfigurationDraft(latest.Revision, latest.Entries[0].BaseVersion, changed))
		latest, e = model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		require.NoError(t, model.PublishModelConfigurations(latest.Revision, []string{name}, ""))
		records, e := model.CurrentPublishedModelConfigurations()
		require.NoError(t, e)
		request["model_version"] = model.ModelExecutionConfigurationVersion(name, records[name])
		require.NotEqual(t, version, request["model_version"])
		refreshedBody, e := common.Marshal(request)
		require.NoError(t, e)
		refreshed := f.request(t, "POST", "/v1/media-v2/quotes", string(refreshedBody), "h3routermember")
		require.Equal(t, 200, refreshed.Code, refreshed.Body.String())
		require.NoError(t, common.Unmarshal(refreshed.Body.Bytes(), &result))
		q = result["data"].(map[string]any)
		require.NotEqual(t, oldQuoteID, q["quote_id"])
		require.Equal(t, oldRequestSHA, q["request_sha256"])
		require.Zero(t, posts.Load(), "explicit quote refresh never generates")
	}
	submit := map[string]any{"quote_id": q["quote_id"], "client_operation_id": request["client_operation_id"], "request_sha256": q["request_sha256"]}
	wire, _ := common.Marshal(submit)
	first := f.request(t, "POST", "/v1/media-v2/tasks", string(wire), "h3routermember")
	require.Equal(t, 200, first.Code, first.Body.String())
	require.NoError(t, common.Unmarshal(first.Body.Bytes(), &result))
	task := result["data"].(map[string]any)["task"].(map[string]any)
	require.NotEmpty(t, task["id"])
	require.Equal(t, task["id"], envelopes[0]["client_request_id"])
	expectedRows := int64(1)
	if condition != "" {
		if condition == "capacity" {
			policy, e := model.GetMediaConcurrencyPolicy(t.Context())
			require.NoError(t, e)
			defaults := policy.ProviderDefaults["ycapi"]
			defaults.Image = 1
			policy.ProviderDefaults["ycapi"] = defaults
			_, e = model.SaveMediaConcurrencyPolicy(t.Context(), policy.Revision, policy)
			require.NoError(t, e)
		} else {
			require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 792).Update("quota", 0).Error)
		}
		rejectedRequest := map[string]any{}
		for k, v := range request {
			rejectedRequest[k] = v
		}
		rejectedRequest["client_operation_id"] = "b80e9581-79df-49b7-9faa-40c3d93415f6"
		payload, _ := common.Marshal(rejectedRequest)
		quoteReply := f.request(t, "POST", "/v1/media-v2/quotes", string(payload), "h3routermember")
		require.Equal(t, 200, quoteReply.Code, quoteReply.Body.String())
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(quoteReply.Body.Bytes(), &decoded))
		failedQuote := decoded["data"].(map[string]any)
		submitPayload, _ := common.Marshal(map[string]any{"quote_id": failedQuote["quote_id"], "client_operation_id": rejectedRequest["client_operation_id"], "request_sha256": failedQuote["request_sha256"]})
		refused := f.request(t, "POST", "/v1/media-v2/tasks", string(submitPayload), "h3routermember")
		require.Equal(t, 200, refused.Code, refused.Body.String())
		require.NoError(t, common.Unmarshal(refused.Body.Bytes(), &decoded))
		refusedTask := decoded["data"].(map[string]any)["task"].(map[string]any)
		require.Equal(t, "failed", refusedTask["status"], refused.Body.String())
		require.Equal(t, "not_submitted", refusedTask["submission_state"])
		require.Equal(t, false, refusedTask["charged"])
		expectedCode := "media_capacity_full"
		if condition == "balance" {
			expectedCode = "insufficient_quota"
		}
		require.Equal(t, expectedCode, refusedTask["failure"].(map[string]any)["code"])
		refusedAgain := f.request(t, "POST", "/v1/media-v2/tasks", string(submitPayload), "h3routermember")
		require.Equal(t, 200, refusedAgain.Code, refusedAgain.Body.String())
		require.Contains(t, refusedAgain.Body.String(), refusedTask["id"])
		require.Contains(t, refusedAgain.Body.String(), `"replayed":true`)
		failedRow := f.task(t, refusedTask["id"].(string))
		require.Zero(t, failedRow.Quota)
		require.Empty(t, failedRow.PrivateData.UpstreamTaskID)
		require.EqualValues(t, 1, posts.Load(), "rejected operations must not reach provider")
		var consumeCount int64
		require.NoError(t, f.logDB.Model(&model.Log{}).Where("user_id = ? AND type = ?", 792, model.LogTypeConsume).Count(&consumeCount).Error)
		require.EqualValues(t, 1, consumeCount, "only the first successful synthetic request is charged")
		expectedRows = 2
	}
	acceptedOperation := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routermember")
	require.Equal(t, 200, acceptedOperation.Code, acceptedOperation.Body.String())
	var acceptedEnvelope map[string]any
	require.NoError(t, common.Unmarshal(acceptedOperation.Body.Bytes(), &acceptedEnvelope))
	acceptedData := acceptedEnvelope["data"].(map[string]any)
	require.Equal(t, acceptedData["task"], acceptedData["quote"].(map[string]any)["task"], "accepted nested quote must carry the identical original task")
	// Receipt-first survives expired quote, disabled model/route and local files.
	require.NoError(t, f.db.Model(&model.MediaGenerationQuote{}).Where("client_op_id = ?", request["client_operation_id"]).Update("expires_at", 1).Error)
	again := f.request(t, "POST", "/v1/media-v2/tasks", string(wire), "h3routermember")
	require.Equal(t, 200, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), `"replayed":true`)
	require.Contains(t, again.Body.String(), task["id"])
	denied := f.request(t, "GET", "/v1/media-v2/operations/646b0609-72ba-4682-bc61-7c02a160b14c", "", "h3routeradmin")
	require.Equal(t, 404, denied.Code, denied.Body.String())
	service.RunTaskPollingOnce(context.Background(), nil)
	statusReply := f.request(t, "GET", "/v1/media-v2/tasks/"+task["id"].(string), "", "h3routermember")
	require.Equal(t, 200, statusReply.Code, statusReply.Body.String())
	require.NoError(t, common.Unmarshal(statusReply.Body.Bytes(), &result))
	completed := result["data"].(map[string]any)["task"].(map[string]any)
	require.Equal(t, "succeeded", completed["status"], statusReply.Body.String())
	artifacts := completed["artifacts"].([]any)
	expectedCount := 3
	if kind == "video" {
		expectedCount = 1
	}
	require.Len(t, artifacts, expectedCount)
	for _, raw := range artifacts {
		artifact := raw.(map[string]any)
		key := artifact["key"].(string)
		endpoint := "/v1/tasks/" + task["id"].(string) + "/artifacts/" + key + "/content"
		download := f.request(t, "GET", endpoint, "", "h3routermember")
		require.Equal(t, 200, download.Code, download.Body.String())
		require.NotEmpty(t, download.Body.Bytes())
		head := f.request(t, "HEAD", endpoint, "", "h3routermember")
		require.Equal(t, 200, head.Code, head.Body.String())
		require.Zero(t, head.Body.Len())
		rr := httptest.NewRequest("GET", endpoint, nil)
		rr.Header.Set("Authorization", "Bearer sk-h3routermember")
		rr.Header.Set("Range", "bytes=0-15")
		rw := httptest.NewRecorder()
		f.engine.ServeHTTP(rw, rr)
		require.Equal(t, 206, rw.Code, rw.Body.String())
		require.Len(t, rw.Body.Bytes(), 16)
		require.Equal(t, download.Body.Bytes()[:16], rw.Body.Bytes())
	}
	require.EqualValues(t, 1, posts.Load())
	require.EqualValues(t, 1, uploads.Load())
	expectedQuotes := int64(1)
	if condition == "" {
		expectedQuotes++
	}
	if condition != "" {
		expectedQuotes = 2
	}
	require.EqualValues(t, expectedQuotes, quotes.Load())
	var count int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("user_id = ? AND platform = ?", 792, "ycapi").Count(&count).Error)
	require.EqualValues(t, expectedRows, count)
	var admissions int64
	require.NoError(t, f.db.Model(&model.MediaAdmission{}).Where("channel_id = ?", ch.Id).Count(&admissions).Error)
	require.EqualValues(t, expectedRows, admissions)
}
