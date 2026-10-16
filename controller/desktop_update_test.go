package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesktopUpdateFeedURLValidation(t *testing.T) {
	for _, value := range []string{"", "https://updates.example.com/mac-arm64/", "http://127.0.0.1:8900/mac-arm64", "http://localhost/feed/", "http://[::1]:8900/feed/"} {
		assert.NoError(t, system_setting.ValidateDesktopUpdateFeedURL(value), value)
	}
	for _, value := range []string{
		"http://updates.example.com/", "https://user:password@updates.example.com/",
		"https://updates.example.com/?token=secret", "https://updates.example.com/#fragment",
		"https://updates.example.com/?", "https://updates.example.com/#",
		"https://updates.example.com/\\path", "https://updates.example.com/%5Cpath",
		"https://updates.example.com/%0apath", " https://updates.example.com/",
		"https://updates.example.com/\n", "https://updates.example.com:99999/",
		"http://localhost.evil.test/", "file:///tmp/feed", "/relative/feed",
		"https://updates.example.com/installer.dmg", "https://updates.example.com/nightly.yml",
		"https://updates.example.com/nested%2Ffeed/", "http://127.0.0.2/feed/",
		"https://updates.example.com/manifest.json/", "https://updates.example.com/installer.dmg/",
	} {
		assert.Error(t, system_setting.ValidateDesktopUpdateFeedURL(value), value)
	}
	// Both option entry points must reject before reaching persistence.
	require.Error(t, model.UpdateOption("desktop_update.enabled", "invalid"))
	require.Error(t, model.UpdateOptionsBulk(map[string]string{
		"desktop_update.mac_arm64_url": "https://secret@example.com/",
	}))
	// A feed can be cleared independently of the switch, allowing either update order.
	require.NoError(t, system_setting.ValidateDesktopUpdateOption("desktop_update.mac_arm64_url", ""))
	require.NoError(t, system_setting.ValidateDesktopUpdateOption("desktop_update.enabled", "true"))
}

func TestGetStatusExposesDesktopUpdatePlatformFeedsWithoutCredentials(t *testing.T) {
	original := make(map[string]string)
	for key, value := range config.GlobalConfig.ExportAllConfigs() {
		if strings.HasPrefix(key, system_setting.DesktopUpdatePrefix) {
			original[key] = value
		}
	}
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(original)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"desktop_update.enabled":       "true",
		"desktop_update.mac_arm64_url": "https://updates.example.com/mac-arm64/",
		"desktop_update.mac_x64_url":   "",
		// Even invalid legacy database content cannot leak URL credentials.
		"desktop_update.win_x64_url": "https://secret@example.com/win-x64/",
	}))
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	GetStatus(context)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DesktopUpdate system_setting.DesktopUpdateStatus `json:"desktop_update"`
		} `json:"data"`
	}
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, system_setting.DesktopUpdateStatus{
		Enabled: true, Channel: "nightly", Feeds: map[string]string{
			"mac-arm64": "https://updates.example.com/mac-arm64/", "mac-x64": "", "win-x64": "",
		},
	}, payload.Data.DesktopUpdate)
	assert.NotContains(t, response.Body.String(), "secret@example.com")
}
