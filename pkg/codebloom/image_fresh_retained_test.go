package codebloom_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/codebloom"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is an offline replay of the six original native responses, never a
// supplier probe. Private artifacts are supplied explicitly by their owner.
func TestCodeBloomFreshImagesRetainSixOriginalResponses(t *testing.T) {
	scope := os.Getenv("TEST_CODEBLOOM_FRESH_IMAGE_QUALIFICATION_SCOPE")
	if scope == "" {
		t.Skip("explicit private six-operation evidence scope required")
	}
	read := func(path string) []byte {
		content, err := os.ReadFile(filepath.Join(scope, path))
		require.NoError(t, err)
		return content
	}
	digest := func(value []byte) string {
		hash := sha256.Sum256(value)
		return hex.EncodeToString(hash[:])
	}
	var final struct {
		Status    string `json:"status"`
		PaidPosts int    `json:"paid_posts"`
		CostCNY   string `json:"cost_cny"`
		Results   []struct {
			ID               string `json:"id"`
			Model            string `json:"model"`
			Mode             string `json:"mode"`
			Path             string `json:"path"`
			ContentType      string `json:"content_type"`
			BodySHA256       string `json:"body_sha256"`
			ResponseSHA256   string `json:"response_sha256"`
			GatewayRequestID string `json:"gateway_request_id"`
			RequestedN       int    `json:"requested_n"`
			RequestedSize    string `json:"requested_size"`
			HTTP             int    `json:"http"`
			CostCNY          string `json:"cost_cny"`
			Artifact         struct {
				SHA256 string `json:"sha256"`
				Bytes  int    `json:"bytes"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"artifact"`
		} `json:"results"`
	}
	require.NoError(t, common.Unmarshal(read("FINAL-RESULT-R04.json"), &final))
	require.Equal(t, "ALL_SIX_NATIVE_REQUESTS_QUALIFIED", final.Status)
	require.Equal(t, 6, final.PaidPosts)
	require.Equal(t, "0.450", final.CostCNY)
	require.Len(t, final.Results, 6)
	source, err := builtinplugins.Source("codebloom-image")
	require.NoError(t, err)
	if os.Getenv("TEST_CODEBLOOM_FRESH_IMAGE_BOOTSTRAP") == "1" {
		// Bootstrap establishes the retained proof before its digest is placed
		// into the qualification table. A second normal-source run is required.
		ids := codebloom.ImageEditModels()
		for _, candidate := range codebloom.ImageProtocolCandidates() {
			ids = append(ids, candidate.ID)
		}
		raw, err := common.Marshal(ids)
		require.NoError(t, err)
		lines := strings.SplitN(source, "\n", 2)
		require.Len(t, lines, 2)
		source = lines[0] + "\nglobalThis.codebloomImageEditModels = " + string(raw) + ";\n" + lines[1]
	}
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{})
	require.NoError(t, err)
	seenRID, seenOperation := map[string]bool{}, map[string]bool{}
	models := map[string]map[string]bool{}
	var reports []map[string]any
	totalQuota := 0
	for _, entry := range final.Results {
		t.Run(entry.Model+"/"+entry.Mode, func(t *testing.T) {
			require.False(t, seenOperation[entry.ID])
			require.False(t, seenRID[entry.GatewayRequestID])
			seenOperation[entry.ID], seenRID[entry.GatewayRequestID] = true, true
			if models[entry.Model] == nil {
				models[entry.Model] = map[string]bool{}
			}
			models[entry.Model][entry.Mode] = true
			require.Equal(t, 1, entry.RequestedN)
			require.Equal(t, "1024x1024", entry.RequestedSize)
			require.Equal(t, 200, entry.HTTP)
			require.Equal(t, "0.075", entry.CostCNY)
			body, response := read(entry.ID+"/request.body"), read(entry.ID+"/response.body")
			require.Equal(t, entry.BodySHA256, digest(body))
			require.Equal(t, entry.ResponseSHA256, digest(response))
			var headers map[string]string
			require.NoError(t, common.Unmarshal(read(entry.ID+"/response-headers.private.json"), &headers))
			require.Equal(t, entry.GatewayRequestID, headers["X-Oneapi-Request-Id"])
			var meter []struct {
				RequestID string `json:"request_id"`
				ModelName string `json:"model_name"`
				Type      int    `json:"type"`
				Quota     int    `json:"quota"`
				Other     string `json:"other"`
			}
			require.NoError(t, common.Unmarshal(read(entry.ID+"/meter-0.private.json"), &meter))
			matches := 0
			for _, row := range meter {
				if row.RequestID != entry.GatewayRequestID {
					continue
				}
				matches++
				require.Equal(t, entry.Model, row.ModelName)
				require.Equal(t, 2, row.Type)
				require.Equal(t, 37500, row.Quota)
				var usage map[string]any
				require.NoError(t, common.UnmarshalJsonStr(row.Other, &usage))
				assert.Equal(t, float64(1), usage["image_count"])
				assert.Equal(t, .75, usage["group_ratio"])
				assert.Equal(t, entry.Path, usage["request_path"])
				totalQuota += row.Quota
			}
			require.Equal(t, 1, matches)
			public := "ys-" + entry.Model
			request := map[string]any{}
			var protocolBody map[string]any
			inputSHA := ""
			if entry.Mode == "generation" {
				require.NoError(t, common.Unmarshal(body, &request))
				request["model"] = public
				protocolBody = map[string]any{"kind": "json", "value": request}
			} else {
				_, parameters, err := mime.ParseMediaType(entry.ContentType)
				require.NoError(t, err)
				reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
				fields, files := map[string][]string{}, []any{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					require.NoError(t, err)
					value, err := io.ReadAll(part)
					require.NoError(t, err)
					if part.FileName() == "" {
						fields[part.FormName()] = append(fields[part.FormName()], string(value))
						request[part.FormName()] = string(value)
					} else {
						require.Equal(t, "image", part.FormName())
						require.LessOrEqual(t, len(value), 5<<20)
						inputSHA = digest(value)
						require.Equal(t, digest(read("owned-materials/owned-red-blue-square-1024.png")), inputSHA)
						decoded, format, err := image.Decode(bytes.NewReader(value))
						require.NoError(t, err)
						require.Equal(t, "png", format)
						require.Equal(t, image.Rect(0, 0, 1024, 1024), decoded.Bounds())
						files = append(files, map[string]any{"field": "image", "ref": "request_file:image", "filename": part.FileName(), "mimeType": "image/png", "size": len(value)})
					}
				}
				require.Len(t, files, 1)
				fields["model"] = []string{public}
				protocolBody = map[string]any{"kind": "multipart", "fields": fields, "files": files}
			}
			decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": public, "operation": map[bool]string{true: "edit", false: "generate"}[entry.Mode == "edit"], "body": protocolBody})
			require.NoError(t, err)
			intent := imageCandidateObject(t, decoded)
			taskID := "task_" + entry.ID
			ctx := map[string]any{"model": public, "upstreamModel": entry.Model, "publicTaskId": taskID, "baseUrl": "https://supplier.invalid", "apiKey": "synthetic-replay-secret", "requestBody": intent["requestBody"]}
			value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			wire := imageCandidateObject(t, value)
			assert.Equal(t, "https://supplier.invalid"+entry.Path, wire["url"])
			if entry.Mode == "generation" {
				request["model"] = entry.Model
				assert.Equal(t, request, wire["body"])
			} else {
				parts := wire["parts"].([]any)
				require.Len(t, parts, 6)
				for _, value := range parts[:5] {
					part := imageCandidateObject(t, value)
					assert.Equal(t, request[part["name"].(string)], part["value"])
				}
				assert.Equal(t, map[string]any{"name": "image", "fileRef": "request_file:image"}, parts[5])
			}
			var native map[string]any
			require.NoError(t, common.Unmarshal(response, &native))
			require.Len(t, native["data"], 1)
			value, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"statusCode": entry.HTTP, "body": native})
			require.NoError(t, err)
			parsed := imageCandidateObject(t, value)
			assert.Equal(t, taskID, parsed["taskId"])
			assert.Equal(t, "SUCCESS", imageCandidateObject(t, parsed["immediate"])["status"])
			// Released T2I clients ask for b64_json; released I2I clients omit
			// the format. Both must build the same single URL-native envelope,
			// price facts and retained receipt from this original response.
			var compatibleBody map[string]any
			if entry.Mode == "generation" {
				var legacy map[string]any
				require.NoError(t, common.Unmarshal(body, &legacy))
				legacy["model"], legacy["response_format"] = public, "b64_json"
				compatibleBody = map[string]any{"kind": "json", "value": legacy}
			} else {
				legacyFields := map[string][]string{}
				for name, values := range protocolBody["fields"].(map[string][]string) {
					if name != "response_format" {
						legacyFields[name] = append([]string(nil), values...)
					}
				}
				compatibleBody = map[string]any{"kind": "multipart", "fields": legacyFields, "files": protocolBody["files"]}
			}
			decoded, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "decodeRequest"}, map[string]any{"model": public, "operation": map[bool]string{true: "edit", false: "generate"}[entry.Mode == "edit"], "body": compatibleBody})
			require.NoError(t, err)
			compatibleContext := map[string]any{}
			for name, value := range ctx {
				compatibleContext[name] = value
			}
			compatibleContext["requestBody"] = imageCandidateObject(t, decoded)["requestBody"]
			value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", compatibleContext)
			require.NoError(t, err)
			assert.Equal(t, wire, imageCandidateObject(t, value), "caller format preference does not add another request or reach native payload")
			value, err = plugin.Engine.Call(t.Context(), "extractUsage", compatibleContext)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"image_count": float64(1)}, imageCandidateObject(t, value))
			value, err = plugin.Engine.Call(t.Context(), "parseSubmitResponse", compatibleContext, map[string]any{"statusCode": entry.HTTP, "body": native})
			require.NoError(t, err)
			compatibleReceipt, err := common.Marshal(value)
			require.NoError(t, err)
			originalReceipt, err := common.Marshal(parsed)
			require.NoError(t, err)
			assert.Equal(t, digest(originalReceipt), digest(compatibleReceipt), "same original receipt and artifact URL, never a second generation")
			imageBytes := read(entry.ID + "/output.image")
			require.Equal(t, entry.Artifact.SHA256, digest(imageBytes))
			require.Equal(t, entry.Artifact.Bytes, len(imageBytes))
			output, format, err := image.Decode(bytes.NewReader(imageBytes))
			require.NoError(t, err)
			require.Equal(t, "png", format)
			require.Equal(t, image.Rect(0, 0, 1024, 1024), output.Bounds())
			value, err = plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "taskId": taskID, "state": parsed["state"], "data": parsed["taskData"]})
			require.NoError(t, err)
			artifacts := value.([]any)
			require.Len(t, artifacts, 1)
			assert.Equal(t, "image-1", imageCandidateObject(t, artifacts[0])["key"])
			value, err = plugin.Engine.Call(t.Context(), "buildContentRequest", map[string]any{"state": parsed["state"], "data": parsed["taskData"], "artifactKey": "image-1", "clientRequest": map[string]any{"method": "GET"}})
			require.NoError(t, err)
			content := imageCandidateObject(t, value)
			assert.Equal(t, true, content["credentialless"])
			assert.NotContains(t, content, "headers")
			assert.Equal(t, native["data"].([]any)[0].(map[string]any)["url"], content["url"])
			reports = append(reports, map[string]any{"operation_id": entry.ID, "public_task_id": taskID, "model": public, "mode": entry.Mode, "request_sha256": entry.BodySHA256, "response_sha256": entry.ResponseSHA256, "input_image_sha256": inputSHA, "image_sha256": entry.Artifact.SHA256, "gateway_request_id": entry.GatewayRequestID, "meter_evidence_sha256": digest(read(entry.ID + "/meter-0.private.json")), "quota": 37500, "account_cost_cny": "0.075", "format": format, "width": 1024, "height": 1024, "native_wire_matches": true, "retained_status": "SUCCESS", "artifact_key": "image-1", "credentialless_content": true})
		})
	}
	require.Len(t, models, 3)
	for _, modes := range models {
		require.True(t, modes["generation"] && modes["edit"])
	}
	require.Equal(t, 225000, totalQuota)
	require.Len(t, reports, 6)
	if path := os.Getenv("TEST_CODEBLOOM_FRESH_IMAGE_REPLAY_OUTPUT"); path != "" {
		raw, err := common.Marshal(map[string]any{"schema": 1, "type": "OFFLINE_ORIGINAL_RESPONSE_RETAINED_PROOF", "scope_id": filepath.Base(scope), "network_requests": 0, "paid_requests": 0, "original_native_requests": 6, "total_account_cost_cny": "0.450", "results": reports})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0600))
	}
}
