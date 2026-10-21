package controller

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClientModelConfigJSONContract(t *testing.T) {
	valid := `{"schema_version":1,"default_chat_model":"chat","model_order":["media","chat"]}`
	config, err := model.ParseClientModelConfig([]byte(valid))
	require.NoError(t, err)
	assert.Equal(t, model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "chat", ModelOrder: []string{"media", "chat"}}, config)
	_, err = model.ParseClientModelConfig([]byte(`{"schema_version":1,"default_chat_model":"","model_order":[]}`))
	require.NoError(t, err)
	for name, body := range map[string]string{
		"null object":       `null`,
		"missing fields":    `{}`,
		"unknown field":     strings.TrimSuffix(valid, "}") + `,"extra":true}`,
		"duplicate field":   strings.TrimSuffix(valid, "}") + `,"schema_version":1}`,
		"escaped duplicate": strings.TrimSuffix(valid, "}") + `,"\u0073chema_version":1}`,
		"wrong field case":  strings.Replace(valid, "schema_version", "Schema_Version", 1),
		"trailing object":   valid + `{}`,
		"wrong version":     strings.Replace(valid, `:1,`, `:2,`, 1),
		"fraction version":  strings.Replace(valid, `:1,`, `:1.0,`, 1),
		"string version":    strings.Replace(valid, `:1,`, `:"1",`, 1),
		"null default":      `{"schema_version":1,"default_chat_model":null,"model_order":[]}`,
		"number default":    `{"schema_version":1,"default_chat_model":5,"model_order":[]}`,
		"null order":        `{"schema_version":1,"default_chat_model":"","model_order":null}`,
		"null order item":   `{"schema_version":1,"default_chat_model":"","model_order":[null]}`,
		"object order":      `{"schema_version":1,"default_chat_model":"","model_order":{}}`,
		"duplicate ID":      `{"schema_version":1,"default_chat_model":"","model_order":["chat","chat"]}`,
		"untrimmed ID":      strings.Replace(valid, `"chat"`, `" chat"`, 1),
		"control character": strings.Replace(valid, `"chat"`, `"chat\u0000"`, 1),
		"long ID":           strings.Replace(valid, `"chat"`, `"`+strings.Repeat("a", 256)+`"`, 1),
		"invalid UTF8":      strings.Replace(valid, `"chat"`, "\"\xff\"", 1),
		"too many entries":  `{"schema_version":1,"default_chat_model":"","model_order":[` + strings.TrimSuffix(strings.Repeat(`"chat",`, 257), ",") + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := model.ParseClientModelConfig([]byte(body))
			assert.ErrorIs(t, err, model.ErrInvalidClientModelConfig)
		})
	}
}

func TestClientModelConfigDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Option{}, &model.Channel{}, &model.Ability{}, &model.Model{})
	originalDB := model.DB
	originalMain := common.MainDatabaseType()
	common.OptionMapRWMutex.Lock()
	originalOptions := common.OptionMap
	common.OptionMap = map[string]string{"SystemName": "preserved"}
	common.OptionMapRWMutex.Unlock()
	model.DB = db
	common.SetMainDatabaseType(dialect)
	t.Cleanup(func() {
		model.DB = originalDB
		common.SetMainDatabaseType(originalMain)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptions
		common.OptionMapRWMutex.Unlock()
	})

	chat := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "synthetic chat", Key: "not-a-provider-key", Status: common.ChannelStatusEnabled}
	media := model.Channel{Type: constant.ChannelTypeTaskPlugin, Name: "synthetic media", Key: "not-a-provider-key", Status: common.ChannelStatusEnabled}
	disabled := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "disabled", Key: "not-a-provider-key", Status: common.ChannelStatusManuallyDisabled}
	for _, channel := range []*model.Channel{&chat, &media, &disabled} {
		require.NoError(t, db.Create(channel).Error)
	}
	for _, ability := range []model.Ability{
		{Group: "a", Model: "qa-chat", ChannelId: chat.Id, Enabled: true},
		{Group: "b", Model: "qa-chat", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "qa-second-chat", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "gpt-image-qa", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "opaque-task", ChannelId: media.Id, Enabled: true},
		{Group: "a", Model: "array-chat", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "array-media", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "compact-only", ChannelId: chat.Id, Enabled: true},
		{Group: "a", Model: "hidden-channel-model", ChannelId: disabled.Id, Enabled: true},
		{Group: "a", Model: "disabled-ability", ChannelId: chat.Id, Enabled: false},
	} {
		require.NoError(t, db.Create(&ability).Error)
	}
	for _, metadata := range []model.Model{
		{ModelName: "array-chat", Endpoints: `["openai"]`},
		{ModelName: "array-media", Endpoints: `["image-generation"]`},
		{ModelName: "compact-only", Endpoints: `["openai-response-compact"]`},
	} {
		require.NoError(t, db.Create(&metadata).Error)
	}

	t.Run("live catalog deduplicates and excludes disabled routes and media defaults", func(t *testing.T) {
		candidates, err := model.ListClientModelCandidates()
		require.NoError(t, err)
		assert.Equal(t, []model.ClientModelCandidate{
			{ID: "array-chat", DisplayName: "array-chat", ChatEligible: true},
			{ID: "array-media", DisplayName: "array-media"},
			{ID: "compact-only", DisplayName: "compact-only"},
			{ID: "gpt-image-qa", DisplayName: "gpt-image-qa"},
			{ID: "opaque-task", DisplayName: "opaque-task"},
			{ID: "qa-chat", DisplayName: "qa-chat", ChatEligible: true},
			{ID: "qa-second-chat", DisplayName: "qa-second-chat", ChatEligible: true},
		}, candidates)
	})

	config := model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "qa-second-chat", ModelOrder: []string{"gpt-image-qa", "qa-chat"}}
	t.Run("complete object persists and public status returns exact preference without catalog", func(t *testing.T) {
		require.NoError(t, model.SaveClientModelConfig(config))
		var stored model.Option
		require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelConfig"}).Error)
		parsed, err := model.ParseClientModelConfig([]byte(stored.Value))
		require.NoError(t, err)
		assert.Equal(t, config, parsed)
		assert.Equal(t, config, model.CurrentClientModelConfig())
		copy := model.CurrentClientModelConfig()
		copy.ModelOrder[0] = "changed-copy"
		assert.Equal(t, config, model.CurrentClientModelConfig())
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		GetStatus(context)
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Config model.ClientModelConfig `json:"client_model_config"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		assert.True(t, payload.Success)
		assert.Equal(t, config, payload.Data.Config)
		assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		assert.NotContains(t, recorder.Body.String(), "not-a-provider-key")
		assert.NotContains(t, recorder.Body.String(), "chat_eligible")
	})

	t.Run("invalid and unavailable choices retain entire previous object", func(t *testing.T) {
		for _, tc := range []struct {
			value model.ClientModelConfig
			want  error
		}{
			{model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "gpt-image-qa", ModelOrder: []string{}}, model.ErrClientDefaultMustChat},
			{model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "opaque-task", ModelOrder: []string{}}, model.ErrClientDefaultMustChat},
			{model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "compact-only", ModelOrder: []string{}}, model.ErrClientDefaultMustChat},
			{model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "not-available", ModelOrder: []string{}}, model.ErrUnavailableClientModel},
			{model.ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{"hidden-channel-model"}}, model.ErrUnavailableClientModel},
			{model.ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{"qa-chat", "qa-chat"}}, model.ErrInvalidClientModelConfig},
		} {
			assert.ErrorIs(t, model.SaveClientModelConfig(tc.value), tc.want)
			assert.Equal(t, config, model.CurrentClientModelConfig())
		}
	})

	t.Run("ordinary option writers cannot bypass validation", func(t *testing.T) {
		encoded, err := common.Marshal(config)
		require.NoError(t, err)
		assert.Error(t, model.UpdateOption("ClientModelConfig", string(encoded)))
		assert.Error(t, model.UpdateOptionsBulk(map[string]string{"ClientModelConfig": string(encoded), "SystemName": "overwritten"}))
		assert.Equal(t, config, model.CurrentClientModelConfig())
		common.OptionMapRWMutex.RLock()
		name := common.OptionMap["SystemName"]
		common.OptionMapRWMutex.RUnlock()
		assert.Equal(t, "preserved", name)
	})

	t.Run("channel retirement preserves saved preference but rejects a stale resave", func(t *testing.T) {
		require.NoError(t, db.Model(&chat).Update("status", common.ChannelStatusManuallyDisabled).Error)
		assert.Equal(t, config, model.CurrentClientModelConfig())
		assert.ErrorIs(t, model.SaveClientModelConfig(config), model.ErrUnavailableClientModel)
		require.NoError(t, db.Model(&chat).Update("status", common.ChannelStatusEnabled).Error)
	})

	t.Run("database failure does not publish new memory state", func(t *testing.T) {
		const callback = "client-config-test-reject-save"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Option" {
				tx.AddError(fmt.Errorf("synthetic private database detail"))
			}
		}))
		err := model.SaveClientModelConfig(model.ClientModelConfig{SchemaVersion: 1, DefaultChatModel: "qa-chat", ModelOrder: []string{}})
		require.NoError(t, db.Callback().Create().Remove(callback))
		assert.ErrorIs(t, err, model.ErrClientModelSave)
		assert.NotContains(t, err.Error(), "private")
		assert.Equal(t, config, model.CurrentClientModelConfig())
		var stored model.Option
		require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelConfig"}).Error)
		parsed, err := model.ParseClientModelConfig([]byte(stored.Value))
		require.NoError(t, err)
		assert.Equal(t, config, parsed)
	})

	t.Run("HTTP errors are bounded and safe and clearing is atomic", func(t *testing.T) {
		for _, tc := range []struct {
			body, contentType string
			status            int
		}{
			{`{}`, "application/json", http.StatusBadRequest},
			{`{"schema_version":1,"default_chat_model":"qa-chat","model_order":[]}`, "text/plain", http.StatusBadRequest},
			{strings.Repeat(" ", model.MaxClientModelConfigBodyBytes+1), "application/json", http.StatusRequestEntityTooLarge},
			{`{"schema_version":1,"default_chat_model":"gpt-image-qa","model_order":[]}`, "application/json", http.StatusBadRequest},
			{`{"schema_version":1,"default_chat_model":"","model_order":[]}`, "application/json", http.StatusOK},
		} {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/client-model-config", strings.NewReader(tc.body))
			context.Request.Header.Set("Content-Type", tc.contentType)
			SaveClientModelConfig(context)
			assert.Equal(t, tc.status, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "not-a-provider-key")
		}
		assert.Equal(t, model.ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{}}, model.CurrentClientModelConfig())
	})

	t.Run("invalid legacy stored data produces safe default", func(t *testing.T) {
		common.OptionMapRWMutex.Lock()
		before := maps.Clone(common.OptionMap)
		common.OptionMap["ClientModelConfig"] = `{"schema_version":1,"default_chat_model":"private","model_order":null}`
		common.OptionMapRWMutex.Unlock()
		assert.Equal(t, model.ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{}}, model.CurrentClientModelConfig())
		common.OptionMapRWMutex.Lock()
		common.OptionMap = before
		common.OptionMapRWMutex.Unlock()
	})
}
