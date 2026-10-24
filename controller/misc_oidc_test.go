package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStatusAdvertisesVideoQueueOnlyWithRunningDispatcher(t *testing.T) {
	oldUpdateTask := constant.UpdateTask
	oldDispatcher := service.CurrentVideoQueueDispatcher()
	t.Cleanup(func() {
		constant.UpdateTask = oldUpdateTask
		service.SetVideoQueueDispatcher(oldDispatcher)
	})
	for _, test := range []struct {
		name                   string
		update, ready, enabled bool
	}{
		{"disabled scheduler", false, true, false},
		{"dispatcher not initialized", true, false, false},
		{"ready", true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			constant.UpdateTask = test.update
			service.SetVideoQueueDispatcher(nil)
			if test.ready {
				service.SetVideoQueueDispatcher(func(context.Context, *service.VideoQueueDispatch) (int, error) { return http.StatusOK, nil })
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
			GetStatus(ctx)
			// The response includes other scalar fields, so decode only the
			// capability object without changing the legacy status envelope.
			var wire map[string]any
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &wire))
			data, ok := wire["data"].(map[string]any)
			require.True(t, ok)
			capability, ok := data["video_queue"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, float64(1), capability["schema_version"])
			assert.Equal(t, test.enabled, capability["enabled"])
			assert.Equal(t, "/v1/video/queue", capability["create_path"])
			assert.Equal(t, true, capability["client_generated_id"])
			assert.Equal(t, false, capability["queued_charge_reserved"])
			assert.Equal(t, "GET", capability["lost_ack_recovery_method"])
		})
	}
}

func TestGetStatusReturnsEffectiveOIDCDisplayName(t *testing.T) {
	settings := system_setting.GetOIDCSettings()
	originalDisplayName := settings.DisplayName
	originalOptionMap := common.OptionMap
	t.Cleanup(func() {
		settings.DisplayName = originalDisplayName
		common.OptionMap = originalOptionMap
	})
	common.OptionMap = map[string]string{}

	tests := []struct {
		name        string
		displayName string
		want        string
	}{
		{
			name:        "custom name is trimmed",
			displayName: "  Acme SSO  ",
			want:        "Acme SSO",
		},
		{
			name:        "whitespace-only name falls back",
			displayName: "   ",
			want:        "OIDC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings.DisplayName = tt.displayName
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

			GetStatus(context)

			var payload struct {
				Success bool           `json:"success"`
				Data    map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assert.Equal(t, tt.want, payload.Data["oidc_display_name"])
		})
	}
}
