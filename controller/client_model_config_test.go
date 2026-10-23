package controller

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type clientModelPresentationTransport func(*http.Request) (*http.Response, error)

func (transport clientModelPresentationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestClientModelPresentationJSONContract(t *testing.T) {
	valid := `{"schema_version":1,"models":{"chat":{"description":"Plain <b>text</b>\nhttps://example.invalid","badge":"New"},"missing":{},"cleared":{"description":"","badge":""}}}`
	config, err := model.ParseClientModelPresentation([]byte(valid))
	require.NoError(t, err)
	assert.Nil(t, config.Models["missing"].Description)
	assert.Nil(t, config.Models["missing"].Badge)
	require.NotNil(t, config.Models["cleared"].Description)
	require.NotNil(t, config.Models["cleared"].Badge)
	assert.Empty(t, *config.Models["cleared"].Description)
	assert.Empty(t, *config.Models["cleared"].Badge)
	assert.Equal(t, "Plain <b>text</b>\nhttps://example.invalid", *config.Models["chat"].Description)
	encoded, err := common.Marshal(config)
	require.NoError(t, err)
	assert.JSONEq(t, valid, string(encoded))
	_, err = model.ParseClientModelPresentation([]byte(`{"schema_version":1,"models":{"chat":{"description":"` + strings.Repeat("😀", 250) + `","badge":"` + strings.Repeat("😀", 16) + `"}}}`))
	require.NoError(t, err)
	_, err = model.ParseClientModelPresentation([]byte(`{"schema_version":1,"models":{}}`))
	require.NoError(t, err)
	for name, body := range map[string]string{
		"null root":                  `null`,
		"missing fields":             `{}`,
		"unknown root":               `{"schema_version":1,"models":{},"extra":true}`,
		"wrong case":                 `{"Schema_Version":1,"models":{}}`,
		"duplicate version":          `{"schema_version":1,"schema_version":1,"models":{}}`,
		"escaped duplicate version":  `{"schema_version":1,"\u0073chema_version":1,"models":{}}`,
		"duplicate models":           `{"schema_version":1,"models":{},"models":{}}`,
		"duplicate model ID":         `{"schema_version":1,"models":{"chat":{},"chat":{}}}`,
		"escaped duplicate model ID": `{"schema_version":1,"models":{"chat":{},"\u0063hat":{}}}`,
		"duplicate description":      `{"schema_version":1,"models":{"chat":{"description":"a","description":"b"}}}`,
		"escaped duplicate badge":    `{"schema_version":1,"models":{"chat":{"badge":"a","\u0062adge":"b"}}}`,
		"unknown entry field":        `{"schema_version":1,"models":{"chat":{"tags":[]}}}`,
		"wrong entry case":           `{"schema_version":1,"models":{"chat":{"Badge":"a"}}}`,
		"schema zero":                `{"schema_version":0,"models":{}}`,
		"schema fractional":          `{"schema_version":1.0,"models":{}}`,
		"schema string":              `{"schema_version":"1","models":{}}`,
		"schema null":                `{"schema_version":null,"models":{}}`,
		"models null":                `{"schema_version":1,"models":null}`,
		"models array":               `{"schema_version":1,"models":[]}`,
		"entry null":                 `{"schema_version":1,"models":{"chat":null}}`,
		"description null":           `{"schema_version":1,"models":{"chat":{"description":null}}}`,
		"badge null":                 `{"schema_version":1,"models":{"chat":{"badge":null}}}`,
		"description object":         `{"schema_version":1,"models":{"chat":{"description":{}}}}`,
		"badge number":               `{"schema_version":1,"models":{"chat":{"badge":1}}}`,
		"description overlong":       `{"schema_version":1,"models":{"chat":{"description":"` + strings.Repeat("😀", 251) + `"}}}`,
		"badge overlong":             `{"schema_version":1,"models":{"chat":{"badge":"` + strings.Repeat("😀", 17) + `"}}}`,
		"badge newline":              `{"schema_version":1,"models":{"chat":{"badge":"a\nb"}}}`,
		"badge unicode newline":      `{"schema_version":1,"models":{"chat":{"badge":"a\u2028b"}}}`,
		"empty ID":                   `{"schema_version":1,"models":{"":{}}}`,
		"untrimmed ID":               `{"schema_version":1,"models":{" chat":{}}}`,
		"ID control":                 `{"schema_version":1,"models":{"chat\u0000":{}}}`,
		"ID overlong":                `{"schema_version":1,"models":{"` + strings.Repeat("a", 256) + `":{}}}`,
		"invalid UTF8":               "{\"schema_version\":1,\"models\":{\"chat\":{\"badge\":\"\xff\"}}}",
		"trailing value":             valid + `{}`,
		"overlong body":              strings.Repeat(" ", model.MaxClientModelPresentationBodyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := model.ParseClientModelPresentation([]byte(body))
			assert.ErrorIs(t, err, model.ErrInvalidClientModelPresentation)
		})
	}
	models := map[string]model.ClientModelPresentationEntry{}
	for i := range 257 {
		models[fmt.Sprintf("model-%d", i)] = model.ClientModelPresentationEntry{}
	}
	encoded, err = common.Marshal(model.ClientModelPresentation{SchemaVersion: 1, Models: models})
	require.NoError(t, err)
	_, err = model.ParseClientModelPresentation(encoded)
	assert.ErrorIs(t, err, model.ErrInvalidClientModelPresentation)
}

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

func TestClientModelPresentationDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Option{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.PasskeyCredential{})
	previousTransport, previousClientTransport := http.DefaultTransport, http.DefaultClient.Transport
	providerCalls := 0
	blockedTransport := clientModelPresentationTransport(func(*http.Request) (*http.Response, error) {
		providerCalls++
		return nil, fmt.Errorf("client model presentation must not make outbound requests")
	})
	http.DefaultTransport, http.DefaultClient.Transport = blockedTransport, blockedTransport
	previousDB, previousType := model.DB, common.MainDatabaseType()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{"SystemName": "preserved"}
	common.OptionMapRWMutex.Unlock()
	model.DB = db
	common.SetMainDatabaseType(dialect)
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient.Transport = previousTransport, previousClientTransport
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, db.Create(&model.Option{Key: "SystemName", Value: "preserved"}).Error)
	fallback := model.ClientModelPresentation{SchemaVersion: 1, Models: map[string]model.ClientModelPresentationEntry{}}
	config, err := model.ParseClientModelPresentation([]byte(`{"schema_version":1,"models":{"unknown-exact-ID":{"description":"Plain <script>text</script>\nhttps://example.invalid","badge":"New"},"missing":{},"cleared":{"description":"","badge":""}}}`))
	require.NoError(t, err)
	oldConfig := model.ClientModelConfig{SchemaVersion: 1, ModelOrder: []string{}}
	require.NoError(t, model.SaveClientModelConfig(oldConfig))
	type wireFixture struct {
		Case               string            `json:"case"`
		Method             string            `json:"method"`
		Path               string            `json:"path"`
		Status             int               `json:"status"`
		Body               common.RawMessage `json:"body"`
		BodyKind           string            `json:"body_kind"`
		FullResponseSHA256 string            `json:"full_response_sha256"`
		CacheControl       string            `json:"cache_control"`
	}
	fixtures := []wireFixture{}
	request := func(name, method, path, body, contentType string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(method, path, strings.NewReader(body))
		context.Request.Header.Set("Content-Type", contentType)
		handler(context)
		fixture := wireFixture{Case: name, Method: method, Path: path, Status: recorder.Code, Body: append(common.RawMessage(nil), recorder.Body.Bytes()...), BodyKind: "full_handler_response", FullResponseSHA256: fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), CacheControl: recorder.Header().Get("Cache-Control")}
		if path == "/api/status" {
			var public struct {
				Success bool                         `json:"success"`
				Data    map[string]common.RawMessage `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &public))
			fixture.Body, err = common.Marshal(map[string]any{"success": public.Success, "data": map[string]common.RawMessage{"client_model_config": public.Data["client_model_config"], "client_model_presentation": public.Data["client_model_presentation"]}})
			require.NoError(t, err)
			fixture.BodyKind = "only_client_model_status_fields"
		}
		fixtures = append(fixtures, fixture)
		return recorder
	}
	t.Run("missing resource returns fallback with no read side effects", func(t *testing.T) {
		assert.Equal(t, fallback, model.CurrentClientModelPresentation())
		var before, after []model.Option
		require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&before).Error)
		const callback = "client-presentation-read-only"
		writes := 0
		denyWrite := func(tx *gorm.DB) { writes++; tx.AddError(fmt.Errorf("GET must be read only")) }
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, denyWrite))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, denyWrite))
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callback, denyWrite))
		for _, path := range []string{"/api/status", "/api/client-model-config/presentation"} {
			handler := GetClientModelPresentation
			if path == "/api/status" {
				handler = GetStatus
			}
			response := request("fallback GET", http.MethodGet, path, "", "", handler)
			assert.Equal(t, http.StatusOK, response.Code)
		}
		require.NoError(t, db.Callback().Create().Remove(callback))
		require.NoError(t, db.Callback().Update().Remove(callback))
		require.NoError(t, db.Callback().Delete().Remove(callback))
		assert.Zero(t, writes)
		assert.Zero(t, providerCalls)
		require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&after).Error)
		assert.Equal(t, before, after)
	})
	t.Run("tri state round trips and independent preference writes", func(t *testing.T) {
		encoded, err := common.Marshal(config)
		require.NoError(t, err)
		response := request("mixed overrides PUT", http.MethodPut, "/api/client-model-config/presentation", string(encoded), "application/json", SaveClientModelPresentation)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, config, model.CurrentClientModelPresentation())
		var stored model.Option
		require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelPresentation"}).Error)
		parsed, err := model.ParseClientModelPresentation([]byte(stored.Value))
		require.NoError(t, err)
		assert.Equal(t, config, parsed)
		copy := model.CurrentClientModelPresentation()
		*copy.Models["unknown-exact-ID"].Description = "changed"
		*copy.Models["cleared"].Badge = "changed"
		delete(copy.Models, "missing")
		assert.Equal(t, config, model.CurrentClientModelPresentation())
		require.NoError(t, model.SaveClientModelConfig(oldConfig))
		assert.Equal(t, config, model.CurrentClientModelPresentation())
		assert.Equal(t, oldConfig, model.CurrentClientModelConfig())
		var abilities []model.Ability
		require.NoError(t, db.Find(&abilities).Error)
		assert.Empty(t, abilities, "presentation cannot grant access to unknown IDs")
		response = request("mixed overrides GET", http.MethodGet, "/api/client-model-config/presentation", "", "", GetClientModelPresentation)
		assert.JSONEq(t, `{"success":true,"message":"","data":`+string(encoded)+`}`, response.Body.String())
		response = request("mixed public status", http.MethodGet, "/api/status", "", "", GetStatus)
		var public struct {
			Data map[string]common.RawMessage `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &public))
		assert.JSONEq(t, string(encoded), string(public.Data["client_model_presentation"]))
		var threeFields map[string]any
		require.NoError(t, common.Unmarshal(public.Data["client_model_config"], &threeFields))
		assert.Len(t, threeFields, 3, "old strict clients still receive exactly three fields")
		assert.NotContains(t, response.Body.String(), "ClientModelPresentation")
		assert.NotContains(t, response.Body.String(), "not-a-provider-key")
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	})
	t.Run("maximum Unicode map persists beyond MySQL TEXT size", func(t *testing.T) {
		description, badge := strings.Repeat("汉", 500), strings.Repeat("新", 32)
		large := model.ClientModelPresentation{SchemaVersion: 1, Models: map[string]model.ClientModelPresentationEntry{}}
		for i := range 256 {
			large.Models[fmt.Sprintf("unicode-model-%03d", i)] = model.ClientModelPresentationEntry{Description: &description, Badge: &badge}
		}
		require.NoError(t, model.SaveClientModelPresentation(large))
		var stored model.Option
		require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelPresentation"}).Error)
		assert.Greater(t, len(stored.Value), 65535)
		parsed, err := model.ParseClientModelPresentation([]byte(stored.Value))
		require.NoError(t, err)
		assert.Equal(t, large, parsed)
		assert.Equal(t, large, model.CurrentClientModelPresentation())
		require.NoError(t, model.SaveClientModelPresentation(config))
	})
	t.Run("generic writers reject canonical and collation aliases atomically", func(t *testing.T) {
		keys := []string{"ClientModelPresentation", "clientmodelpresentation", "ClientModelPresentation "}
		if dialect == common.DatabaseTypeMySQL {
			keys = append(keys, "CliéntModelPresentation")
		}
		for _, key := range keys {
			t.Run(key, func(t *testing.T) {
				var before, after []model.Option
				require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&before).Error)
				assert.Error(t, model.UpdateOption(key, `{}`))
				assert.Error(t, model.UpdateOptionsBulk(map[string]string{key: `{}`, "SystemName": "overwritten"}))
				_, err := model.UpdatePasskeyDomainOptions(map[string]string{key: `{}`, "ServerAddress": "https://example.invalid"}, false, "")
				assert.Error(t, err)
				require.NoError(t, db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&after).Error)
				assert.Equal(t, before, after)
				assert.Equal(t, config, model.CurrentClientModelPresentation())
				common.OptionMapRWMutex.RLock()
				assert.Equal(t, "preserved", common.OptionMap["SystemName"])
				common.OptionMapRWMutex.RUnlock()
			})
		}
		if dialect == common.DatabaseTypeMySQL {
			t.Run("alias inserted before canonical", func(t *testing.T) {
				require.NoError(t, db.Delete(&model.Option{}, &model.Option{Key: "ClientModelPresentation"}).Error)
				for _, writer := range []func(string) error{
					func(key string) error { return model.UpdateOption(key, `{}`) },
					func(key string) error {
						return model.UpdateOptionsBulk(map[string]string{key: `{}`, "SystemName": "overwritten"})
					},
					func(key string) error {
						_, err := model.UpdatePasskeyDomainOptions(map[string]string{key: `{}`}, false, "")
						return err
					},
				} {
					assert.Error(t, writer("CliéntModelPresentation"))
					var count int64
					require.NoError(t, db.Model(&model.Option{}).Where(&model.Option{Key: "ClientModelPresentation"}).Count(&count).Error)
					assert.Zero(t, count, "tentative alias-first insert must roll back")
				}
				require.NoError(t, db.Create(&model.Option{Key: "CliéntModelPresentation", Value: "legacy alias preserved"}).Error)
				assert.ErrorIs(t, model.SaveClientModelPresentation(config), model.ErrClientModelPresentationSave)
				var stored model.Option
				require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelPresentation"}).Error)
				assert.Equal(t, "legacy alias preserved", stored.Value)
				require.NoError(t, db.Delete(&stored).Error)
				require.NoError(t, model.SaveClientModelPresentation(config))
			})
		}
	})
	t.Run("bounded safe errors retain committed database and memory", func(t *testing.T) {
		for _, tc := range []struct {
			name, body, contentType string
			status                  int
		}{
			{"malformed", `{}`, "application/json", http.StatusBadRequest},
			{"wrong content type", `{"schema_version":1,"models":{}}`, "text/plain", http.StatusBadRequest},
			{"too large", strings.Repeat(" ", model.MaxClientModelPresentationBodyBytes+1), "application/json", http.StatusRequestEntityTooLarge},
		} {
			response := request(tc.name, http.MethodPut, "/api/client-model-config/presentation", tc.body, tc.contentType, SaveClientModelPresentation)
			assert.Equal(t, tc.status, response.Code)
			assert.Equal(t, config, model.CurrentClientModelPresentation())
		}
		const callback = "client-presentation-test-reject-save"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Option" {
				tx.AddError(fmt.Errorf("synthetic private database detail"))
			}
		}))
		response := request("save failure", http.MethodPut, "/api/client-model-config/presentation", `{"schema_version":1,"models":{}}`, "application/json", SaveClientModelPresentation)
		require.NoError(t, db.Callback().Create().Remove(callback))
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), "private")
		assert.Equal(t, config, model.CurrentClientModelPresentation())
		var stored model.Option
		require.NoError(t, db.First(&stored, &model.Option{Key: "ClientModelPresentation"}).Error)
		parsed, err := model.ParseClientModelPresentation([]byte(stored.Value))
		require.NoError(t, err)
		assert.Equal(t, config, parsed)
	})
	t.Run("whole replacement explicitly clears every override", func(t *testing.T) {
		response := request("clear all overrides", http.MethodPut, "/api/client-model-config/presentation", `{"schema_version":1,"models":{}}`, "application/json", SaveClientModelPresentation)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, fallback, model.CurrentClientModelPresentation())
		assert.Equal(t, oldConfig, model.CurrentClientModelConfig())
		request("cleared public status", http.MethodGet, "/api/status", "", "", GetStatus)
	})
	t.Run("malformed stored value never exposes raw option", func(t *testing.T) {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["ClientModelPresentation"] = `{"schema_version":1,"models":{"private":{"badge":null,"key":"never expose"}}}`
		common.OptionMapRWMutex.Unlock()
		assert.Equal(t, fallback, model.CurrentClientModelPresentation())
		response := request("invalid stored fallback", http.MethodGet, "/api/client-model-config/presentation", "", "", GetClientModelPresentation)
		assert.NotContains(t, response.Body.String(), "never expose")
	})
	if output := os.Getenv("CLIENT_MODEL_PRESENTATION_FIXTURE_OUTPUT"); output != "" {
		require.True(t, filepath.IsAbs(output), "fixture output must be an absolute path")
		data, err := common.Marshal(map[string]any{"source": "actual local httptest handlers; synthetic isolated database, no provider or production account", "dialect": dialect, "fixtures": fixtures})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, data, 0600))
	}
	assert.Zero(t, providerCalls, "neither reads nor presentation writes may contact a provider or fetch text URLs")
}
