package router

import (
	"bytes"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCapacityDirectoryAndPersistence(t *testing.T) {
	setupRelayRouterTestDB(t)
	common.OptionMapRWMutex.Lock()
	oldMarker := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if oldMarker == "" {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		} else {
			common.OptionMap[model.ModelCapacityOptionKey] = oldMarker
		}
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Option{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Channel{}, &model.Option{})) })
	previous := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = previous })
	previousMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemory })
	u := model.User{Username: "capacity-user", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&u).Error)
	require.NoError(t, model.DB.Create(&model.Token{UserId: u.Id, Key: "capacityfixturekey", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	for _, id := range []int{1, 2, 3} {
		require.NoError(t, model.DB.Create(&model.Channel{Id: id, Type: 1, Status: 1, Name: "synthetic", Group: "default", Models: "joint-small"}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "joint-small", ChannelId: id, Enabled: true}).Error)
	}
	makeRoute := func(id, window, output int) model.CapacityRoute {
		return model.CapacityRoute{Model: "joint-small", Group: "default", RouteModel: "joint-small", ChannelID: id, ContextWindow: &window, MaxOutputTokens: &output, BudgetField: "max_tokens", Source: "local synthetic upstream specification", VerifiedAt: "2026-10-31"}
	}
	cfg := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{makeRoute(1, 32768, 8192), makeRoute(2, 16384, 4096)}}
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	e, err := service.ModelCapacityForGroups("joint-small", []string{"default"})
	require.NoError(t, err)
	assert.Len(t, e.Routes, 2)
	assert.Equal(t, 4096, *e.MaxOutputTokens)
	assert.Equal(t, 16384, *e.ContextWindow)
	engine := gin.New()
	SetRelayRouter(engine)
	get := func() map[string]any {
		r := httptest.NewRecorder()
		q := httptest.NewRequest("GET", "/v1/models", nil)
		q.Header.Set("Authorization", "Bearer capacityfixturekey")
		engine.ServeHTTP(r, q)
		require.Equal(t, 200, r.Code, r.Body.String())
		var v map[string]any
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &v))
		assert.NotContains(t, r.Body.String(), "channel_id")
		assert.NotContains(t, r.Body.String(), "source")
		return v
	}
	v := get()
	entries := v["data"].([]any)
	require.Len(t, entries, 1)
	entry := entries[0].(map[string]any)
	assert.Equal(t, float64(4096), entry["max_output_tokens"])
	cfg.Routes[0] = makeRoute(1, 8000, 1000)
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	entries = get()["data"].([]any)
	assert.Equal(t, float64(1000), entries[0].(map[string]any)["max_output_tokens"])
	require.NoError(t, model.SaveModelCapacityConfig(model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{}}))
	read, err := model.ReadModelCapacityConfig()
	require.NoError(t, err)
	assert.Len(t, read.Routes, 2, "omission must not revoke old declarations")
	cfg.Routes[0] = makeRoute(1, 100, 200)
	assert.Error(t, model.SaveModelCapacityConfig(cfg))
	cfg.Routes[0] = makeRoute(1, 2147483647, 2147483647)
	assert.Error(t, model.SaveModelCapacityConfig(cfg))
	assert.Error(t, model.UpdateOption(model.ModelCapacityOptionKey, `{}`))
	// A missing declaration never bans an old model.
	c, err := service.ModelCapacityForGroups("legacy", []string{"default"})
	require.NoError(t, err)
	assert.False(t, c.Managed)

	// Repointing a channel invalidates its old proof while the other reviewed
	// route remains available; another group cannot borrow this route's proof.
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("model_mapping", `{"joint-small":"changed-native"}`).Error)
	changed, err := service.ModelCapacityForGroups("joint-small", []string{"default"})
	require.NoError(t, err)
	assert.Len(t, changed.Routes, 1)
	assert.Equal(t, 4096, *changed.MaxOutputTokens)
	other, err := service.ModelCapacityForGroups("joint-small", []string{"other"})
	require.NoError(t, err)
	assert.True(t, other.Managed)
	assert.Empty(t, other.Routes)
	// Ordinary credentials cannot mutate the root-only management endpoint.
	api := gin.New()
	SetApiRouter(api)
	body, _ := common.Marshal(cfg)
	q := httptest.NewRequest("PUT", "/api/option/model_capacity", bytes.NewReader(body))
	q.Header.Set("Authorization", "Bearer capacityfixturekey")
	r := httptest.NewRecorder()
	api.ServeHTTP(r, q)
	assert.NotEqual(t, http.StatusOK, r.Code)
}

func TestModelCapacityWireBudgetBoundaries(t *testing.T) {
	require.NoError(t, i18n.Init())
	setupRelayRouterTestDB(t)
	common.OptionMapRWMutex.Lock()
	oldMarker := common.OptionMap[model.ModelCapacityOptionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if oldMarker == "" {
			delete(common.OptionMap, model.ModelCapacityOptionKey)
		} else {
			common.OptionMap[model.ModelCapacityOptionKey] = oldMarker
		}
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Option{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Channel{}, &model.Option{})) })
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Type: 1, Status: 1, Name: "local", Group: "default", Models: "joint"}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "joint", ChannelId: 1, Enabled: true}).Error)
	cfg := model.ModelCapacityConfig{Schema: 1, Routes: []model.CapacityRoute{{Model: "joint", Group: "default", RouteModel: "joint", ChannelID: 1, ContextWindow: common.GetPointer(32768), MaxOutputTokens: common.GetPointer(8192), BudgetField: "max_tokens", Source: "synthetic only", VerifiedAt: "2026-10-31"}}}
	require.NoError(t, model.SaveModelCapacityConfig(cfg))
	cases := []struct {
		name, body, path string
		expected         int
		bad              bool
	}{
		{"large legacy", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":384000}`, "/v1/chat/completions", 8192, false},
		{"smaller caller", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`, "/v1/chat/completions", 1024, false},
		{"completion spelling", `{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":384000}`, "/v1/chat/completions", 8192, false},
		{"missing budget", `{"model":"joint","messages":[{"role":"user","content":"hello"}]}`, "/v1/chat/completions", 8192, false},
		{"native responses", `{"model":"joint","input":"hello","max_output_tokens":384000}`, "/v1/responses", 8192, false},
		{"native gemini", `{"generationConfig":{"maxOutputTokens":384000},"contents":[]}`, "/v1beta/models/joint:generateContent", 8192, false},
		{"nested override", `{"model":"joint","max_tokens":384000,"extra_body":{"max_tokens":999999}}`, "/v1/chat/completions", 8192, false},
		{"zero", `{"model":"joint","max_tokens":0}`, "/v1/chat/completions", 0, true},
		{"negative", `{"model":"joint","max_tokens":-1}`, "/v1/chat/completions", 0, true},
		{"fraction", `{"model":"joint","max_tokens":1.5}`, "/v1/chat/completions", 0, true},
		{"wrong type", `{"model":"joint","max_tokens":"100"}`, "/v1/chat/completions", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			info := &relaycommon.RelayInfo{OriginModelName: "joint", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, UpstreamModelName: "gpt-4"}}
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Request = req
			err := service.EnforceModelCapacityOutbound(c, req, info)
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			wire, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, common.Unmarshal(wire, &got))
			field := "max_tokens"
			if tc.path == "/v1/responses" {
				field = "max_output_tokens"
			}
			if strings.Contains(tc.path, ":generateContent") {
				assert.Equal(t, float64(tc.expected), got["generationConfig"].(map[string]any)["maxOutputTokens"])
			} else {
				assert.Equal(t, float64(tc.expected), got[field])
			}
			if tc.name == "nested override" {
				assert.Equal(t, float64(8192), got["extra_body"].(map[string]any)["max_tokens"])
			}
			assert.Equal(t, int64(len(wire)), req.ContentLength)
		})
	}
	// Capacity is applied before param()-aware billing reads the incoming body.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"joint","messages":[{"role":"user","content":"hello"}],"max_tokens":384000}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	r := &kitdto.GeneralOpenAIRequest{Model: "joint", Messages: []kitdto.Message{{Role: "user", Content: "hello"}}, MaxTokens: common.GetPointer(uint(384000))}
	info := &relaycommon.RelayInfo{OriginModelName: "joint", Request: r}
	require.Nil(t, service.ClampModelCapacityRequest(c, info))
	require.NotNil(t, info.BillingRequestInput)
	assert.Contains(t, string(info.BillingRequestInput.Body), `"max_tokens":8192`)
}
