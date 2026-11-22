package router

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This opt-in server is a NEW synthetic scope. Its normal production router,
// authentication, database ledger and artifacts run unchanged. The only vendor
// transport is a deny-by-default local simulator, so no paid call can escape.
func TestMediaGenerationV2LocalFixtureServer(t *testing.T) {
	root := os.Getenv("MEDIA_V2_FIXTURE_OUTPUT")
	if root == "" {
		t.Skip("new explicit isolated fixture only")
	}
	require.True(t, filepath.IsAbs(root))
	scope := os.Getenv("MEDIA_V2_FIXTURE_SCOPE")
	require.True(t, strings.HasPrefix(scope, "media-v2-interop-"))
	require.NoError(t, os.MkdirAll(root, 0700))
	fence, err := os.OpenFile(filepath.Join(root, "LAUNCH-CONSUMED"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err, "never replay this launch; use a new authorized scope")
	fence.Close()
	f := newH3RouterFixture(t, 1)
	t.Setenv("MEDIA_ASSET_STORAGE_DIR", filepath.Join(root, "private-assets"))
	require.NoError(t, model.MigrateMediaGenerationQuote(f.db))
	require.NoError(t, model.MigrateMediaGenerationAsset(f.db))
	require.NoError(t, model.MigrateMediaGenerationUpload(f.db))
	require.NoError(t, f.db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AuthFlow{}))
	common.SessionSecret = "synthetic-media-session-" + uuid.NewString()
	common.CryptoSecret = "synthetic-media-artifact-" + uuid.NewString()
	source, err := plugins.Source("ycapi")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	defer jsplugin.DefaultRegistry.Unregister("ycapi")
	names := []string{"ycapi-gpt-image-2.6-flare", "ycapi-seedance-2-5"}
	base := "https://ycapi.token6688.com"
	ch := model.Channel{Id: 981, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-v2-provider-" + uuid.NewString(), Name: "本地多模态联调渠道", Models: strings.Join(names, ","), Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &base}
	ch.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "ycapi"})
	ch.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, ch.Insert())
	model.InitChannelCache()
	for _, name := range names {
		contract, ok := ycapi.LookupMediaContract(name)
		require.True(t, ok)
		snapshot, e := model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		entry := snapshot.Entries[0]
		def := entry.Definition
		def.Kind = contract.Kind
		def.Enabled = true
		def.DisplayName = contract.Name
		def.Source = "local isolated simulator; not paid supplier qualification"
		def.Parameters = nil
		def.Routes = []model.ModelConfigurationRoute{{ChannelID: 981, Group: "default", UpstreamModel: name, Enabled: true}}
		def.MediaExecution = &model.ModelMediaExecutionConfiguration{SchemaVersion: 1, LaneID: contract.NativeID + "@fixture:only", Intents: ycapi.MediaContractIntents(contract), QualificationSHA256: strings.Repeat("a", 64), ContractSHA256: service.MediaGenerationDigest(contract), CheckedAt: time.Now().Format("2006-01-02")}
		quantity := "image_count"
		if contract.Kind == "video" {
			quantity = "seconds"
		}
		def.Pricing = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": fmt.Sprintf(`tier("synthetic",u("%s")*(0.2/%g))`, quantity, operation_setting.USDExchangeRate)}
		require.NoError(t, model.SaveModelConfigurationDraft(snapshot.Revision, entry.BaseVersion, def))
		snapshot, e = model.GetModelConfigurationSnapshot(name)
		require.NoError(t, e)
		require.NoError(t, model.PublishModelConfigurations(snapshot.Revision, []string{name}, ""))
	}
	model.RefreshPricing()
	fetch := system_setting.GetFetchSetting()
	fetch.EnableSSRFProtection = false // local simulator assets only; transport still denies every unknown target
	var pngBytes, gifBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 32, 24))))
	require.NoError(t, gif.Encode(&gifBytes, image.NewPaletted(image.Rect(0, 0, 32, 24), color.Palette{color.Black, color.White}), nil))
	videoPath := filepath.Join(root, "synthetic-output.mp4")
	require.NoError(t, exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x240:r=24:d=5", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-movflags", "+faststart", "-f", "mp4", "-y", videoPath).Run())
	var reference bytes.Buffer
	require.NoError(t, png.Encode(&reference, image.NewNRGBA(image.Rect(0, 0, 512, 512))))
	require.NoError(t, os.WriteFile(filepath.Join(root, "synthetic-reference.png"), reference.Bytes(), 0600))
	video, err := os.ReadFile(videoPath)
	require.NoError(t, err)
	files := map[string][]byte{"/main.png": pngBytes.Bytes(), "/motion.gif": gifBytes.Bytes(), "/layer.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="24"><rect width="32" height="24" fill="blue"/></svg>`), "/clip.mp4": video}
	mimes := map[string]string{"/main.png": "image/png", "/motion.gif": "image/gif", "/layer.svg": "image/svg+xml", "/clip.mp4": "video/mp4"}
	var generation, uploads, quotes atomic.Int64
	var mu sync.Mutex
	nativeTasks := map[string]map[string]any{}
	service.GetHttpClient().Transport = imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cdn.media-fixture" {
			data, ok := files[r.URL.Path]
			if !ok {
				return nil, fmt.Errorf("unknown fixture artifact")
			}
			if r.Header.Get("Authorization") != "" {
				return nil, fmt.Errorf("credential leak blocked")
			}
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", mimes[r.URL.Path])
			http.ServeContent(w, r, filepath.Base(r.URL.Path), time.Unix(1, 0), bytes.NewReader(data))
			return w.Result(), nil
		}
		if r.URL.Host != "ycapi.token6688.com" {
			return nil, fmt.Errorf("fixture blocks every external target")
		}
		result := map[string]any{}
		status := 200
		switch r.URL.Path {
		case "/v1/files":
			uploads.Add(1)
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				return nil, err
			}
			file, header, e := r.FormFile("file")
			if e != nil {
				return nil, e
			}
			data, e := io.ReadAll(file)
			file.Close()
			if e != nil {
				return nil, e
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			result = map[string]any{"id": "fixture_file_" + sum[:16], "object": "file", "url": "https://assets.token6688.com/sha256/" + sum + ".bin", "content_type": header.Header.Get("Content-Type"), "bytes": len(data)}
			status = 201
		case "/v1/pricing-estimate":
			quotes.Add(1)
			var request map[string]any
			if e := common.DecodeJson(r.Body, &request); e != nil {
				return nil, e
			}
			result = map[string]any{"effective_total_rmb": 0.02, "billing_mode": "fixed_amount", "param_adjustments": []any{}, "channels": []any{map[string]any{"lane": request["pinned_lane_id"], "is_delivery_head": true}}}
		case "/api/v1/model-runtime/dry-run":
			var request map[string]any
			if e := common.DecodeJson(r.Body, &request); e != nil {
				return nil, e
			}
			result = map[string]any{"object": "model_runtime.dry_run", "would_call_vendor": false, "pinned_lane": request["lane_id"], "modality": request["modality"], "wire": map[string]any{"method": "POST", "channel_id": request["lane_id"], "body": request["params"]}}
		case "/api/v1/model-runtime/invoke":
			var request map[string]any
			if e := common.DecodeJson(r.Body, &request); e != nil {
				return nil, e
			}
			generation.Add(1)
			id := "native_" + request["client_request_id"].(string)
			mu.Lock()
			if _, duplicate := nativeTasks[id]; duplicate {
				mu.Unlock()
				return nil, fmt.Errorf("duplicate generation blocked")
			}
			nativeTasks[id] = request
			mu.Unlock()
			contract, ok := ycapi.LookupMediaContract(request["model_id"].(string))
			if !ok {
				return nil, fmt.Errorf("unknown model")
			}
			result = map[string]any{"task_id": id, "status": "pending", "is_final": false, "model": map[string]any{"public_model": contract.UpstreamPublicID}}
		default:
			if !strings.HasPrefix(r.URL.Path, "/v1/tasks/") {
				return nil, fmt.Errorf("unexpected fixture path")
			}
			id := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
			mu.Lock()
			request, ok := nativeTasks[id]
			mu.Unlock()
			if !ok {
				return nil, fmt.Errorf("original task not found")
			}
			contract, _ := ycapi.LookupMediaContract(request["model_id"].(string))
			outputs := []any{}
			paths := []string{"/main.png", "/motion.gif", "/layer.svg"}
			if contract.Kind == "video" {
				paths = []string{"/clip.mp4"}
			}
			for _, path := range paths {
				outputs = append(outputs, map[string]any{"url": "https://cdn.media-fixture" + path, "type": contract.Kind, "mime_type": mimes[path]})
			}
			result = map[string]any{"task_id": id, "status": "completed", "is_final": true, "model": map[string]any{"public_model": contract.UpstreamPublicID}, "data": outputs}
		}
		data, e := common.Marshal(result)
		if e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})
	relayKey := strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 792).Update("key", relayKey).Error)
	phone := "+15550007920"
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 792).Updates(map[string]any{"phone_e164": phone, "display_name": "本地多模态联调用户"}).Error)
	bundle, err := service.CreateLoginSession(792, "synthetic-media-v2-interop", "127.0.0.1", "isolated-local-qa")
	require.NoError(t, err)
	// Fixture-only TTL extension, same normal SID/claims and normal production
	// parser. No production secret, authentication bypass or login rule change.
	claims := jwt.MapClaims{}
	_, _, err = new(jwt.Parser).ParseUnverified(bundle.AccessToken, claims)
	require.NoError(t, err)
	expires := time.Now().Add(3 * time.Hour).Unix()
	claims["exp"] = expires
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	mac.Write([]byte("new-api/auth/access/v1"))
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(mac.Sum(nil))
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(access)
	require.NoError(t, err)
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	baseURL := "http://" + listener.Addr().String()
	system_setting.TaskPublicAddress = baseURL
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: f.engine, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				service.RunTaskPollingOnce(ctx, nil)
			}
		}
	}()
	stored := map[string]any{"token": "synthetic-local-" + uuid.NewString(), "backendBaseUrl": baseURL, "upstream": map[string]any{"token": access, "expiresAt": time.Unix(expires, 0).UTC().Format(time.RFC3339Nano)}, "relay": map[string]any{"key": "sk-" + relayKey, "id": 792}, "expiresAt": time.Unix(expires, 0).UTC().Format(time.RFC3339Nano), "account": map[string]any{"id": "792", "phone": phone}}
	manifest := map[string]any{"schema_version": 1, "fixture_id": scope, "source_commit": os.Getenv("MEDIA_V2_FIXTURE_SOURCE_COMMIT"), "source_tree_sha256": os.Getenv("MEDIA_V2_FIXTURE_SOURCE_TREE_SHA256"), "base_url": baseURL, "pid": os.Getpid(), "synthetic_only": true, "outbound_egress_blocked": true, "expires_at": expires, "users": map[string]any{"owner": map[string]any{"id": 792, "phone_e164": phone, "role": common.RoleCommonUser, "group": "default", "quota": 100000000}}, "actors": map[string]any{"owner": map[string]any{"storedSession": stored}}, "auth_bundles": map[string]any{"owner": map[string]any{"access_token": access, "access_expires_at": expires}}, "managed_relay_key": "sk-" + relayKey, "models": names, "database_path": common.SQLitePath, "storage_path": os.Getenv("MEDIA_ASSET_STORAGE_DIR"), "quota_per_unit": common.QuotaPerUnit, "usd_exchange_rate": operation_setting.USDExchangeRate, "initial_generations": generation.Load(), "initial_uploads": uploads.Load(), "initial_quotes": quotes.Load()}
	data, err := common.Marshal(manifest)
	require.NoError(t, err)
	private := filepath.Join(root, "FIXTURE.private.json")
	require.NoError(t, os.WriteFile(private, data, 0600))
	require.NoError(t, os.Chmod(private, 0600))
	ready := map[string]any{"schema_version": 1, "fixture_id": scope, "mode": "media-v2-isolated-interop", "source_commit": os.Getenv("MEDIA_V2_FIXTURE_SOURCE_COMMIT"), "source_tree_sha256": os.Getenv("MEDIA_V2_FIXTURE_SOURCE_TREE_SHA256"), "base_url": baseURL, "pid": os.Getpid(), "private_manifest": private, "synthetic_only": true, "outbound_egress_blocked": true, "paid": 0, "status": "ACTIVE", "contract": "media-generation-v2/revision3", "expires_at": expires, "initial_generations": 0, "initial_uploads": 0, "initial_quotes": 0}
	data, err = common.Marshal(ready)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "API-FIXTURE-READY.json"), data, 0644))
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	if err != http.ErrServerClosed {
		require.NoError(t, err)
	}
}
