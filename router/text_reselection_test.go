package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercises the actual authenticated HTTP boundary, provider transport and
// disposable database. No supplier or production network is reachable.
func TestTextReselectionHTTPReceiptAndBillingBoundary(t *testing.T) {
	var fixtures []map[string]any
	for _, tc := range []struct {
		name, model, path, mode, credential string
		status, sends                       int
		proof                               bool
	}{
		{name: "missing channel", model: "ys-deepseek-v4.1-flash", status: 503, proof: true},
		{name: "legacy request unchanged", model: "ys-deepseek-v4.1-flash", mode: "legacy", status: 503},
		{name: "unknown opt in version", model: "ys-deepseek-v4.1-flash", mode: "unknown-version", status: 503},
		{name: "strict session", model: "ys-deepseek-v4.1-flash", mode: "strict", status: 503},
		{name: "unqualified model", model: "ys-gpt-6.1-terra", mode: "unqualified", status: 400, proof: true},
		{name: "token model forbidden", model: "gpt-4o", mode: "forbidden", status: 403},
		{name: "insufficient balance", model: "gpt-4o", mode: "balance", status: 403},
		{name: "pinned unqualified model", model: "ys-gpt-6.1-terra", mode: "unqualified", credential: "h3routeradmin-1600", status: 400},
		{name: "cancelled before dispatch", model: "ys-deepseek-v4.1-flash", mode: "cancelled", status: 503},
		{name: "responses not in contract", model: "ys-deepseek-v4.1-flash", path: "/v1/responses", status: 503},
		{name: "images not in contract", model: "missing-image", path: "/v1/images/generations", status: 503},
		{name: "invalid input", model: "gpt-4o", mode: "invalid", status: 400},
		{name: "provider spoofed receipt", model: "gpt-4o", mode: "provider-error", status: 503, sends: 1},
		{name: "provider timeout", model: "gpt-4o", mode: "timeout", sends: 1},
		{name: "partial output", model: "gpt-4o", mode: "partial", status: 200, sends: 1},
		{name: "completed once", model: "gpt-4o", mode: "success", status: 200, sends: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newH3RouterFixture(t, 0)
			oldTimeout := constant.StreamingTimeout
			constant.StreamingTimeout = 30
			t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
			// main installs RequestId before the production router registration.
			f.engine = gin.New()
			f.engine.Use(middleware.RequestId())
			if tc.mode == "strict" {
				f.engine.Use(func(c *gin.Context) { service.RequestPolicy(c).SessionMode = "strict"; c.Next() })
			}
			SetRouter(f.engine, WebAssets{})
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o":0.001,"gpt-4o-mini":0.0005}`))
			model.RefreshPricing()
			fallback := operation_setting.GetModelFallbackSetting()
			oldFallback := *fallback
			*fallback = operation_setting.ModelFallbackSetting{ModelFallbackMap: `{"gpt-4o":["gpt-4o-mini"]}`, ModelFallbackMaxCandidates: 3}
			t.Cleanup(func() { *fallback = oldFallback })
			if tc.mode != "" && tc.mode != "cancelled" && tc.mode != "legacy" && tc.mode != "strict" && tc.mode != "unknown-version" {
				base := "https://text-reselection.fixture"
				channel := &model.Channel{Id: 1600, Type: constant.ChannelTypeOpenAI, Name: "Local text fixture", Key: "synthetic-provider-key", Status: common.ChannelStatusEnabled, Models: tc.model, Group: "default", BaseURL: &base}
				if tc.model == "gpt-4o" {
					channel.Models += ",gpt-4o-mini"
				}
				require.NoError(t, channel.Insert())
				model.InitChannelCache()
			}
			if tc.mode == "forbidden" {
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 792).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "another-model"}).Error)
			}
			if tc.mode == "balance" {
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 792).Update("quota", 0).Error)
			}
			var beforeUser model.User
			var beforeToken model.Token
			require.NoError(t, f.db.First(&beforeUser, 792).Error)
			require.NoError(t, f.db.First(&beforeToken, 792).Error)
			posts := 0
			service.GetHttpClient().Transport = imageUploadRoundTripper(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "text-reselection.fixture", r.URL.Host, "all other egress is forbidden")
				require.Equal(t, http.MethodPost, r.Method)
				posts++
				if tc.mode == "timeout" {
					return nil, context.DeadlineExceeded
				}
				var body io.Reader
				header := http.Header{"Content-Type": []string{"text/event-stream"}}
				status := http.StatusOK
				if tc.mode == "provider-error" {
					id, _ := r.Context().Value(common.RequestIdKey).(string)
					spoof, err := common.Marshal(map[string]any{
						"error":            map[string]any{"type": "new_api_error", "code": "model_not_found", "message": "synthetic provider rejection"},
						"text_reselection": service.TextReselectionReceipt{SchemaVersion: 1, Model: tc.model, RequestID: id, SubmissionState: "not_submitted", Reason: "model_unavailable"},
					})
					require.NoError(t, err)
					status, body = http.StatusServiceUnavailable, strings.NewReader(string(spoof))
					header.Set("Content-Type", "application/json")
				} else {
					first := "data: {\"id\":\"local-text\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"local answer\"},\"finish_reason\":null}]}\n\n"
					body = strings.NewReader(first + "data: {\"id\":\"local-text\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":30,\"total_tokens\":80}}\n\ndata: [DONE]\n\n")
					if tc.mode == "partial" {
						body = io.MultiReader(strings.NewReader(first), iotest.ErrReader(io.ErrUnexpectedEOF))
					}
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(body), Request: r}, nil
			})
			path := tc.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			payload := map[string]any{"model": tc.model, "messages": []any{map[string]any{"role": "user", "content": "Synthetic local fixture"}}, "stream": true, "max_tokens": 128}
			if tc.mode == "invalid" {
				payload["max_tokens"] = -1
			}
			requestBody, err := common.Marshal(payload)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(requestBody)))
			req.Header.Set("Content-Type", "application/json")
			if tc.mode != "legacy" {
				req.Header.Set("X-Yunshu-Text-Reselection", "1")
			}
			if tc.mode == "unknown-version" {
				req.Header.Set("X-Yunshu-Text-Reselection", "2")
			}
			credential := tc.credential
			if credential == "" {
				credential = "h3routermember"
			}
			req.Header.Set("Authorization", "Bearer sk-"+credential)
			if tc.mode == "cancelled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			f.engine.ServeHTTP(w, req)
			if tc.status == 0 {
				require.GreaterOrEqual(t, w.Code, 400, w.Body.String())
			} else {
				require.Equal(t, tc.status, w.Code, w.Body.String())
			}
			var result map[string]any
			if w.Code != http.StatusOK {
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
			}
			if tc.proof {
				receipt, ok := result["text_reselection"].(map[string]any)
				require.True(t, ok, w.Body.String())
				assert.Equal(t, map[string]any{"schema_version": float64(1), "model": tc.model, "request_id": w.Header().Get(common.RequestIdKey), "submission_state": "not_submitted", "upstream_submitted": false, "billing_reserved": false, "reason": "model_unavailable"}, receipt)
				require.NotEmpty(t, w.Header().Get(common.RequestIdKey))
				assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			} else {
				assert.NotContains(t, result, "text_reselection", w.Body.String())
			}
			assert.Equal(t, tc.sends, posts)
			var afterUser model.User
			var afterToken model.Token
			require.NoError(t, f.db.First(&afterUser, 792).Error)
			require.NoError(t, f.db.First(&afterToken, 792).Error)
			var consumptions int64
			require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumptions).Error)
			if tc.sends == 0 {
				assert.Equal(t, beforeUser.Quota, afterUser.Quota)
				assert.Equal(t, beforeToken.RemainQuota, afterToken.RemainQuota)
				assert.Zero(t, consumptions)
			} else {
				assert.LessOrEqual(t, consumptions, int64(1), "one provider attempt must never settle twice")
			}
			if consumptions == 1 {
				var consumption model.Log
				require.NoError(t, f.logDB.Where("type = ?", model.LogTypeConsume).First(&consumption).Error)
				assert.Equal(t, consumption.Quota, beforeUser.Quota-afterUser.Quota)
				assert.Equal(t, consumption.Quota, beforeToken.RemainQuota-afterToken.RemainQuota)
			}
			if tc.mode == "success" {
				assert.EqualValues(t, 1, consumptions)
				assert.Less(t, afterUser.Quota, beforeUser.Quota)
				assert.Contains(t, w.Body.String(), "local answer")
			}
			if tc.mode == "partial" {
				assert.Contains(t, w.Body.String(), "local answer")
			}
			fixtures = append(fixtures, map[string]any{"name": tc.name, "request": payload, "path": path, "request_headers": map[string]string{"X-Yunshu-Text-Reselection": req.Header.Get("X-Yunshu-Text-Reselection")}, "status": w.Code, "headers": map[string]string{common.RequestIdKey: w.Header().Get(common.RequestIdKey), "Content-Type": w.Header().Get("Content-Type")}, "body": w.Body.String(), "proof_expected": tc.proof, "provider_posts": posts, "consumption_logs": consumptions})
		})
	}
	if path := os.Getenv("TEST_TEXT_RESELECTION_FIXTURES"); path != "" && !t.Failed() {
		data, err := common.Marshal(map[string]any{"schema_version": 1, "api_source_base": "39b82ac05", "scope": "Actual local authenticated Go HTTP responses with disposable databases and intercepted providers, not production or supplier calls", "cases": fixtures})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
		t.Log(fmt.Sprintf("client protocol fixture: %s", path))
	}
}
