package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MediaNativeRoute struct {
	ChannelID                                         int
	BaseURL, Credential, CredentialDigest, ConfigHash string
	Settings                                          kitdto.ChannelSettings
}

func mediaNativeHTTPClient(route MediaNativeRoute) (*http.Client, error) {
	u, err := url.Parse(route.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host != "ycapi.token6688.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" && u.Path != "/" || route.Credential == "" || route.Settings.TLSInsecureSkipVerify || common.TLSInsecureSkipVerify {
		return nil, errors.New("invalid native media route")
	}
	client, err := GetHttpClientWithProxySettings(route.Settings.Proxy, route.Settings)
	if err != nil || client == nil {
		return nil, errors.New("native media client unavailable")
	}
	clone := *client
	clone.Jar = nil
	clone.Timeout = 30 * time.Second
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone, nil
}
func mediaNativeJSON(ctx context.Context, route MediaNativeRoute, path string, body any) (map[string]any, error) {
	if path != "/v1/pricing-estimate" && path != "/api/v1/model-runtime/dry-run" {
		return nil, errors.New("not a free media qualification path")
	}
	client, err := mediaNativeHTTPClient(route)
	if err != nil {
		return nil, err
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(route.BaseURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+route.Credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("native media quote unavailable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || resp.StatusCode/100 != 2 {
		return nil, errors.New("native media quote rejected")
	}
	var out map[string]any
	if common.Unmarshal(data, &out) != nil || out == nil {
		return nil, errors.New("invalid native media quote")
	}
	return out, nil
}

func resolveMediaNativeAsset(ctx context.Context, db *gorm.DB, route MediaNativeRoute, a model.MediaGenerationAsset) (string, error) {
	digest := common.GenerateHMAC("media-v2-upload:" + a.ID + ":" + route.CredentialDigest + ":" + route.ConfigHash)
	var old model.MediaGenerationUpload
	if err := db.WithContext(ctx).Where("id = ?", digest).Take(&old).Error; err == nil {
		if old.State == "ready" && validMediaNativeAssetURL(old.URL) {
			return old.URL, nil
		}
		return "", errors.New("asset_not_ready")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	record := model.MediaGenerationUpload{ID: digest, AssetID: a.ID, CredentialDigest: route.CredentialDigest, State: "sending", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
	inserted := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if inserted.Error != nil {
		return "", inserted.Error
	}
	if inserted.RowsAffected != 1 {
		return "", errors.New("asset_not_ready")
	}
	// Durable sending precedes the only upload POST; unknown outcomes are never
	// blindly resubmitted by a new quote or another concurrent caller.
	state := "unknown"
	defer func() {
		if state != "ready" {
			db.Model(&model.MediaGenerationUpload{}).Where("id = ? AND state = ?", digest, "sending").Updates(map[string]any{"state": state, "updated_at": time.Now().Unix()})
		}
	}()
	path, err := mediaGenerationAssetPath(a.ObjectKey)
	if err != nil {
		return "", err
	}
	file, err := openMediaNativeAsset(ctx, path, a)
	if err != nil {
		return "", err
	}
	defer file.Close()
	// Measure only the multipart framing; source bytes remain on the private
	// file and are copied through a pipe under the original upload fence.
	var framing bytes.Buffer
	framingWriter := multipart.NewWriter(&framing)
	_, err = framingWriter.CreatePart(mediaNativeAssetHeader(a.MIMEType))
	if err == nil {
		err = framingWriter.WriteField("purpose", "generation")
	}
	if err == nil {
		err = framingWriter.Close()
	}
	if err != nil {
		return "", err
	}
	client, err := mediaNativeHTTPClient(route)
	if err != nil {
		return "", err
	}
	reader, pipe := io.Pipe()
	writer := multipart.NewWriter(pipe)
	if err = writer.SetBoundary(framingWriter.Boundary()); err != nil {
		_ = reader.Close()
		_ = pipe.Close()
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		err := writeMediaNativeAsset(ctx, writer, file, a)
		_ = pipe.CloseWithError(err)
		done <- err
	}()
	joined := false
	stopClosing := context.AfterFunc(ctx, func() {
		_ = reader.CloseWithError(ctx.Err())
		_ = pipe.CloseWithError(ctx.Err())
	})
	defer func() {
		stopClosing()
		_ = reader.Close()
		_ = pipe.Close()
		if !joined {
			<-done
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(route.BaseURL, "/")+"/v1/files", reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+route.Credential)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = a.Bytes + int64(framing.Len())
	resp, err := client.Do(req)
	_ = reader.Close()
	producerErr := <-done
	joined = true
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil || producerErr != nil {
		return "", errors.New("asset upload outcome unknown")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || resp.StatusCode != http.StatusCreated {
		return "", errors.New("asset upload unavailable")
	}
	var native struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		URL    string `json:"url"`
		MIME   string `json:"content_type"`
		Bytes  int64  `json:"bytes"`
	}
	if common.Unmarshal(raw, &native) != nil || native.Object != "file" || native.ID == "" || len(native.ID) > 256 || native.MIME != a.MIMEType || native.Bytes != a.Bytes || (!validMediaNativeAssetURL(native.URL) || !strings.HasPrefix(strings.TrimPrefix(native.URL, "https://assets.token6688.com/sha256/"), a.SHA256)) {
		return "", errors.New("invalid asset upload receipt")
	}
	updated := db.WithContext(ctx).Model(&model.MediaGenerationUpload{}).Where("id = ? AND state = ?", digest, "sending").Updates(map[string]any{"state": "ready", "url": native.URL, "provider_file_id": native.ID, "updated_at": time.Now().Unix()})
	if updated.Error != nil || updated.RowsAffected != 1 {
		return "", errors.New("asset upload receipt unavailable")
	}
	state = "ready"
	return native.URL, nil
}
func openMediaNativeAsset(ctx context.Context, path string, a model.MediaGenerationAsset) (*os.File, error) {
	limit, err := mediaGenerationAssetLimit(a.Model, a.Kind, a.MIMEType)
	if err != nil {
		return nil, err
	}
	if a.Bytes <= 0 || a.Bytes > limit {
		return nil, errors.New("asset_not_ready")
	}
	info, err := verifyMediaGenerationAssetFile(ctx, path, a)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
		_ = f.Close()
		return nil, errors.New("asset_not_ready")
	}
	return f, nil
}

func mediaNativeAssetHeader(mime string) textproto.MIMEHeader {
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="reference`+mediaNativeExtension(mime)+`"`)
	header.Set("Content-Type", mime)
	return header
}

func writeMediaNativeAsset(ctx context.Context, writer *multipart.Writer, file io.Reader, asset model.MediaGenerationAsset) error {
	part, err := writer.CreatePart(mediaNativeAssetHeader(asset.MIMEType))
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(part, hash), io.LimitReader(mediaAssetContextReader{ctx, file}, asset.Bytes+1))
	if err != nil {
		return err
	}
	if size != asset.Bytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("asset_not_ready")
	}
	if err = writer.WriteField("purpose", "generation"); err != nil {
		return err
	}
	return writer.Close()
}
func mediaNativeExtension(m string) string {
	return map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif", "video/mp4": ".mp4", "video/webm": ".webm", "audio/mpeg": ".mp3", "audio/wav": ".wav", "audio/mp4": ".m4a"}[m]
}
func validMediaNativeAssetURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Scheme == "https" && u.Host == "assets.token6688.com" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" && strings.HasPrefix(u.Path, "/sha256/") && !strings.Contains(u.Path, "..") && !strings.ContainsAny(raw, "\\\r\n\t ")
}

func prepareMediaNativeParameters(ctx context.Context, db *gorm.DB, route MediaNativeRoute, modelID string, parameters, controls map[string]any, bindings map[string][]model.MediaGenerationAsset, intent ycapi.MediaRuntimeIntent) (map[string]any, map[string]ycapi.MediaAssetMetadata, error) {
	contract, ok := ycapi.LookupMediaContract(modelID)
	if !ok {
		return nil, nil, errors.New("model_not_available")
	}
	fields, err := ycapi.AssetFields(contract)
	if err != nil {
		return nil, nil, err
	}
	normalized := map[string]any{}
	for key, value := range parameters {
		normalized[key] = value
	}
	configuredSchema, err := mediaGenerationConfiguredSchema(contract, controls)
	if err != nil {
		return nil, nil, err
	}
	assetFields := map[string]bool{}
	for _, field := range fields {
		assetFields[field.Name] = true
	}
	configuredFields, _ := controls["properties"].(map[string]any)
	for controlName, raw := range configuredFields {
		control, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, configuredDefault := control["default"]; !configuredDefault {
			continue
		}
		name := mediaGenerationControlField(contract, controlName)
		if _, explicit := normalized[name]; explicit || assetFields[name] {
			continue
		}
		if declaration, ok := configuredSchema["properties"].(map[string]any)[name].(map[string]any); ok {
			if value, present := declaration["default"]; present {
				normalized[name] = value
			}
		}
	}
	for _, field := range fields {
		if _, exists := parameters[field.Name]; exists {
			return nil, nil, fmt.Errorf("asset parameters must use verified bindings: %s", field.Name)
		}
	}
	metadata := map[string]ycapi.MediaAssetMetadata{}
	// Validate every logical choice and source measurement locally before any
	// provider upload. Placeholder URLs are never sent over a network.
	for field, assets := range bindings {
		refs := []string{}
		for _, asset := range assets {
			ref := "https://media.invalid/" + asset.ID
			refs = append(refs, ref)
			metadata[ref] = ycapi.MediaAssetMetadata{Kind: asset.Kind, Bytes: asset.Bytes, Width: asset.Width, Height: asset.Height, DurationSeconds: asset.DurationSeconds, FPS: asset.FPS, MIMEType: asset.MIMEType, Format: asset.Format}
		}
		normalized[field] = refs
	}
	validated, err := ycapi.ValidateMediaContractParams(modelID, normalized, metadata)
	if err != nil {
		return nil, nil, err
	}
	if err = validateMediaGenerationControls(contract, controls, validated); err != nil {
		return nil, nil, err
	}
	if _, err = ycapi.BuildMediaRuntimeRequest(modelID, validated, "local-validation-only", "local-validation-only", intent); err != nil {
		return nil, nil, err
	}
	actualMetadata := map[string]ycapi.MediaAssetMetadata{}
	for field, assets := range bindings {
		refs := []string{}
		for _, asset := range assets {
			ref, err := resolveMediaNativeAsset(ctx, db, route, asset)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, ref)
			actualMetadata[ref] = metadata["https://media.invalid/"+asset.ID]
		}
		validated[field] = refs
	}
	return validated, actualMetadata, nil
}

// VerifyMediaNativeDryRun checks the supplier's free compiled request for the
// exact pinned route. Reference preprocessing is explicitly NOT qualified by
// dry-run; actual paid qualification remains a separate bounded step.
func VerifyMediaNativeDryRun(ctx context.Context, route MediaNativeRoute, native ycapi.MediaRuntimeRequest, metadata map[string]ycapi.MediaAssetMetadata) (string, error) {
	result, err := mediaNativeJSON(ctx, route, "/api/v1/model-runtime/dry-run", native)
	if err != nil {
		return "", err
	}
	called, known := result["would_call_vendor"].(bool)
	if !known || called || result["object"] != "model_runtime.dry_run" || result["pinned_lane"] != native.LaneID || result["modality"] != native.Modality {
		return "", errors.New("native dry-run unavailable")
	}
	wire, ok := result["wire"].(map[string]any)
	if !ok || wire["method"] != "POST" || wire["channel_id"] != native.LaneID {
		return "", errors.New("native route changed")
	}
	var body any
	if text, ok := wire["body"].(string); ok {
		if common.UnmarshalJsonStr(text, &body) != nil {
			return "", errors.New("native request invalid")
		}
	} else {
		body = wire["body"]
	}
	// Every occurrence must remain in the actual mapped body, not merely in the
	// input envelope. This catches adapters that silently slice/drop references.
	encoded, err := common.Marshal(body)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	fields, ok := ycapi.LookupMediaContract("ycapi-" + native.ModelID)
	if !ok {
		return "", errors.New("native contract unavailable")
	}
	assets, err := ycapi.AssetFields(fields)
	if err != nil {
		return "", err
	}
	for _, field := range assets {
		switch value := native.Params[field.Name].(type) {
		case string:
			counts[value]++
		case []string:
			for _, ref := range value {
				counts[ref]++
			}
		case []any:
			for _, ref := range value {
				if text, ok := ref.(string); ok {
					counts[text]++
				}
			}
		}
	}
	for ref, count := range counts {
		if _, verified := metadata[ref]; !verified || strings.Count(string(encoded), ref) < count {
			return "", errors.New("native reference mapping changed")
		}
	}
	// Only a digest is retained. The private dry-run may include the supplier's
	// own vendor credential header and must never enter public task/quote DTOs.
	return MediaGenerationDigest([]any{wire["method"], wire["channel_id"], body}), nil
}
