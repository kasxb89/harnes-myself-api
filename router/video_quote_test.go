package router

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var videoQuoteFixtureID atomic.Int64

type videoQuoteTransport struct {
	attempts atomic.Int64
	next     http.RoundTripper
}

func (transport *videoQuoteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.attempts.Add(1)
	ip := net.ParseIP(request.URL.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("video quote fixture blocks non-loopback requests")
	}
	return transport.next.RoundTrip(request)
}

type videoQuoteFixture struct {
	db        *gorm.DB
	engine    *gin.Engine
	userID    int
	transport *videoQuoteTransport
}

func newVideoQuoteFixture(t *testing.T) *videoQuoteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath := common.SQLitePath
	previousMaster, previousRedis, previousMemory := common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled
	common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "video-quote.db")
	if dsn := os.Getenv("SQL_DSN"); dsn != "" && dsn != "local" {
		require.True(t, strings.Contains(dsn, "video_quote") || strings.Contains(dsn, "test_h3_"), "only a disposable quote database may be reset")
	}
	require.NoError(t, model.InitDB())
	database := model.DB
	model.LOG_DB = database
	common.SetDatabaseTypes(common.MainDatabaseType(), common.MainDatabaseType())
	models := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.H3Account{}, &model.H3Admission{}, &model.Option{}, &model.Log{}}
	require.NoError(t, database.Migrator().DropTable(models...))
	require.NoError(t, database.AutoMigrate(models...))
	var version string
	query := "SELECT version()"
	if common.MainDatabaseType() == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, database.Raw(query).Scan(&version).Error)
	t.Logf("Video quote actual database: %s %s", common.MainDatabaseType(), version)
	t.Cleanup(func() {
		sqlDB, err := database.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.SQLitePath = previousPath
		common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = previousMaster, previousRedis, previousMemory
	})
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	previousGroups, previousGroupGroups := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	previousQuota, previousRate := common.QuotaPerUnit, operation_setting.USDExchangeRate
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.5}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`))
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 500000, 7
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(previousGroupGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
		common.QuotaPerUnit, operation_setting.USDExchangeRate = previousQuota, previousRate
	})
	fixture := &videoQuoteFixture{db: database, engine: gin.New(), userID: 9200 + int(videoQuoteFixtureID.Add(1))}
	require.NoError(t, fixture.engine.SetTrustedProxies(nil))
	for _, sourcePath := range []string{"../plugins/tasks/hailuo/plugin.js", "../deploy/instance/blackrxai-video-plugin.js"} {
		source, err := os.ReadFile(sourcePath)
		require.NoError(t, err)
		plugin, err := jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	}
	require.NoError(t, database.Create(&model.User{Id: fixture.userID, Username: fmt.Sprintf("video-quote-%d", fixture.userID), AffCode: "quote-main", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: 0}).Error)
	require.NoError(t, database.Create(&model.Token{Id: fixture.userID, UserId: fixture.userID, Key: "quoteonly", Name: "synthetic quote token", Status: common.TokenStatusExhausted, ExpiredTime: -1, RemainQuota: 0}).Error)
	for _, channel := range []*model.Channel{
		{Id: 870, Type: constant.ChannelTypeMiniMax, Key: "synthetic-unused-h3-credential", Name: "Synthetic H3 quote", Status: common.ChannelStatusEnabled, Models: "MiniMax-H3", Group: "default,vip"},
		{Id: 871, Type: constant.ChannelTypeTaskPlugin, Key: "synthetic-unused-blackrx-credential", Name: "Synthetic Blackrx quote", Status: common.ChannelStatusEnabled, Group: "default,vip"},
	} {
		if channel.Id == 871 {
			plugin, ok := jsplugin.DefaultRegistry.Get("blackrxai-video")
			require.True(t, ok)
			channel.Models = strings.Join(plugin.Meta.Models, ",")
			channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "blackrxai-video"})
		}
		require.NoError(t, database.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	fixture.setExpressions(t, map[string]string{"hailuo::MiniMax-H3": `u("resolution") == "2K" ? tier("2k", u("seconds") * 0.08) : tier("768p", u("seconds") * 0.04)`})
	model.InitChannelCache()
	service.InitHttpClient()
	fixture.transport = &videoQuoteTransport{next: http.DefaultTransport}
	previousDefault := http.DefaultTransport
	http.DefaultTransport = fixture.transport
	clients := []*http.Client{service.GetHttpClient(), service.GetSSRFProtectedHTTPClient()}
	for _, client := range clients {
		previousTransport := client.Transport
		client.Transport = fixture.transport
		t.Cleanup(func() { client.Transport = previousTransport })
	}
	t.Cleanup(func() {
		http.DefaultTransport = previousDefault
		assert.Zero(t, fixture.transport.attempts.Load(), "quotes must never fetch a reference, contact a provider or poll")
	})
	SetVideoRouter(fixture.engine)
	return fixture
}

func (fixture *videoQuoteFixture) setExpressions(t *testing.T, expressions map[string]string) {
	t.Helper()
	encoded, err := common.Marshal(expressions)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(encoded), "billing_setting.billing_mode": `{}`, "billing_setting.billing_expr": `{}`}))
}

func (fixture *videoQuoteFixture) request(t *testing.T, body, authorization, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/video/quote", strings.NewReader(body))
	request.RemoteAddr = fmt.Sprintf("127.0.%d.%d:12345", fixture.userID/250%250, fixture.userID%250+1)
	request.Header.Set("Content-Type", contentType)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	fixture.engine.ServeHTTP(recorder, request)
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
	assert.NotContains(t, recorder.Body.String(), "synthetic-unused-")
	assert.NotContains(t, recorder.Body.String(), "quoteonly")
	if output := os.Getenv("VIDEO_QUOTE_FIXTURE_OUTPUT"); output != "" {
		require.True(t, filepath.IsAbs(output))
		require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o700))
		var requestBody, responseBody any
		if len(body) > 4096 || common.Unmarshal([]byte(body), &requestBody) != nil {
			requestBody = map[string]any{"malformed_or_oversized_bytes": len(body)}
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &responseBody))
		var collection struct {
			Fixture string           `json:"fixture"`
			Cases   []map[string]any `json:"cases"`
		}
		if prior, err := os.ReadFile(output); err == nil {
			require.NoError(t, common.Unmarshal(prior, &collection))
		} else {
			require.True(t, os.IsNotExist(err))
		}
		collection.Fixture = "local synthetic configured prices; not production user quotes; no credentials recorded"
		collection.Cases = append(collection.Cases, map[string]any{"test": t.Name(), "database": common.MainDatabaseType(), "method": "POST", "path": "/v1/video/quote", "request": requestBody, "http_status": recorder.Code, "response": responseBody, "outbound_requests": fixture.transport.attempts.Load()})
		encoded, err := common.Marshal(collection)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
	return recorder
}

func (fixture *videoQuoteFixture) snapshot(t *testing.T) string {
	t.Helper()
	rows := make(map[string][]map[string]any)
	for _, table := range []string{"users", "tokens", "channels", "abilities", "tasks", "h3_accounts", "h3_admissions", "options", "logs"} {
		var values []map[string]any
		columns := []clause.OrderByColumn{{Column: clause.Column{Name: "id"}}}
		if table == "abilities" {
			columns = []clause.OrderByColumn{{Column: clause.Column{Name: "channel_id"}}, {Column: clause.Column{Name: "model"}}, {Column: clause.Column{Name: "group"}}}
		} else if table == "options" {
			columns = []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}
		}
		require.NoError(t, fixture.db.Table(table).Clauses(clause.OrderBy{Columns: columns}).Find(&values).Error)
		rows[table] = values
	}
	encoded, err := common.Marshal(rows)
	require.NoError(t, err)
	return string(encoded)
}

func (fixture *videoQuoteFixture) successfulQuote(t *testing.T, body string) types.VideoQuoteResponse {
	t.Helper()
	before := fixture.snapshot(t)
	response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var quote types.VideoQuoteResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	assert.Equal(t, before, fixture.snapshot(t))
	return quote
}

func TestVideoQuoteHTTPRejectsMalformedAndUnboundedWorkloads(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	valid := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5}}]}`
	for _, test := range []struct {
		name, body, contentType, code string
		status                        int
	}{
		{"non JSON", valid, "text/plain", "invalid_request", 415},
		{"oversized", `{"padding":"` + strings.Repeat("x", 64<<10) + `"}`, "application/json", "invalid_request", 413},
		{"malformed", `{"model":`, "application/json", "invalid_request", 400},
		{"array body", `[]`, "application/json", "invalid_request", 400},
		{"null body", `null`, "application/json", "invalid_request", 400},
		{"unknown envelope", strings.TrimSuffix(valid, "}") + `,"submit":true}`, "application/json", "invalid_request", 400},
		{"unknown segment", strings.Replace(valid, `"id":"one"`, `"id":"one","execute":true`, 1), "application/json", "invalid_request", 400},
		{"wrong segments type", `{"model":"MiniMax-H3","works_count":1,"segments":{}}`, "application/json", "invalid_request", 400},
		{"fractional works", strings.Replace(valid, `"works_count":1`, `"works_count":1.5`, 1), "application/json", "invalid_request", 400},
		{"huge works", strings.Replace(valid, `"works_count":1`, `"works_count":18446744073709551615`, 1), "application/json", "invalid_request", 400},
		{"zero works", strings.Replace(valid, `"works_count":1`, `"works_count":0`, 1), "application/json", "invalid_quote_parameters", 400},
		{"negative works", strings.Replace(valid, `"works_count":1`, `"works_count":-1`, 1), "application/json", "invalid_quote_parameters", 400},
		{"over works cap", strings.Replace(valid, `"works_count":1`, `"works_count":129`, 1), "application/json", "invalid_quote_parameters", 400},
		{"work times segments cap", `{"model":"MiniMax-H3","works_count":65,"segments":[{"id":"a","parameters":{}},{"id":"b","parameters":{}}]}`, "application/json", "invalid_quote_parameters", 400},
		{"empty segments", `{"model":"MiniMax-H3","works_count":1,"segments":[]}`, "application/json", "invalid_quote_parameters", 400},
		{"duplicate segment ID", `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"a","parameters":{}},{"id":"a","parameters":{}}]}`, "application/json", "invalid_quote_parameters", 400},
		{"null segment", `{"model":"MiniMax-H3","works_count":1,"segments":[null]}`, "application/json", "invalid_quote_parameters", 400},
		{"null parameters", strings.Replace(valid, `{"seconds":5}`, `null`, 1), "application/json", "invalid_quote_parameters", 400},
		{"array parameters", strings.Replace(valid, `{"seconds":5}`, `[]`, 1), "application/json", "invalid_request", 400},
		{"negative duration", strings.Replace(valid, `"seconds":5`, `"seconds":-1`, 1), "application/json", "invalid_quote_parameters", 400},
		{"unbounded duration", strings.Replace(valid, `"seconds":5`, `"seconds":3601`, 1), "application/json", "invalid_quote_parameters", 400},
		{"multiple videos per segment", strings.Replace(valid, `"seconds":5`, `"seconds":5,"n":2`, 1), "application/json", "invalid_quote_parameters", 400},
		{"forged internal references", strings.Replace(valid, `"seconds":5`, `"seconds":5,"_blackrxReferences":{}`, 1), "application/json", "invalid_quote_parameters", 400},
		{"local file path", strings.Replace(valid, `"seconds":5`, `"seconds":5,"images":["/tmp/private.png"]`, 1), "application/json", "invalid_quote_parameters", 400},
		{"base64 material", strings.Replace(valid, `"seconds":5`, `"seconds":5,"images":["data:image/png;base64,AA=="]`, 1), "application/json", "invalid_quote_parameters", 400},
		{"file reference object", strings.Replace(valid, `"seconds":5`, `"seconds":5,"images":[{"__fileRef":"request_file:images"}]`, 1), "application/json", "invalid_quote_parameters", 400},
		{"unknown count key", strings.Replace(valid, `"id":"one"`, `"id":"one","reference_counts":{"images":1,"paths":1}`, 1), "application/json", "invalid_request", 400},
		{"fractional reference count", strings.Replace(valid, `"id":"one"`, `"id":"one","reference_counts":{"images":1.5}`, 1), "application/json", "invalid_request", 400},
		{"overflow reference count", strings.Replace(valid, `"id":"one"`, `"id":"one","reference_counts":{"images":18446744073709551615}`, 1), "application/json", "invalid_request", 400},
		{"negative reference count", strings.Replace(valid, `"id":"one"`, `"id":"one","reference_counts":{"images":-1}`, 1), "application/json", "invalid_quote_parameters", 400},
		{"unbounded reference count", strings.Replace(valid, `"id":"one"`, `"id":"one","reference_counts":{"images":129}`, 1), "application/json", "invalid_quote_parameters", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.snapshot(t)
			response := fixture.request(t, test.body, "Bearer sk-quoteonly", test.contentType)
			assert.Equal(t, test.status, response.Code, response.Body.String())
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &failure))
			assert.Equal(t, test.code, failure.Error.Code)
			assert.Equal(t, before, fixture.snapshot(t))
		})
	}
}

func TestVideoQuoteHTTPRevisionAndAccountBinding(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5,"resolution":"768P","ratio":"16:9"}}]}`
	first := fixture.successfulQuote(t, body)
	second := fixture.successfulQuote(t, body)
	assert.Equal(t, first.PricingRevision, second.PricingRevision)
	assert.Equal(t, first.AccountBinding, second.AccountBinding)
	for _, changed := range []string{
		strings.Replace(body, `"seconds":5`, `"seconds":6`, 1),
		strings.Replace(body, `"ratio":"16:9"`, `"ratio":"9:16"`, 1),
		strings.Replace(body, `"works_count":1`, `"works_count":2`, 1),
	} {
		quote := fixture.successfulQuote(t, changed)
		assert.NotEqual(t, first.PricingRevision, quote.PricingRevision)
		assert.Equal(t, first.AccountBinding, quote.AccountBinding)
	}
	fixture.setExpressions(t, map[string]string{"hailuo::MiniMax-H3": `tier("changed", u("seconds") * 0.05)`})
	changedPrice := fixture.successfulQuote(t, body)
	assert.NotEqual(t, first.PricingRevision, changedPrice.PricingRevision)
	assert.NotEqual(t, first.TotalQuota, changedPrice.TotalQuota)
	require.NoError(t, fixture.db.Create(&model.User{Id: fixture.userID + 1000, Username: "other-video-quote-user", AffCode: "quote-other", Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}).Error)
	require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Update("user_id", fixture.userID+1000).Error)
	other := fixture.successfulQuote(t, body)
	assert.NotEqual(t, changedPrice.AccountBinding, other.AccountBinding)
	assert.NotEqual(t, changedPrice.PricingRevision, other.PricingRevision)
	require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Update("group", "vip").Error)
	vip := fixture.successfulQuote(t, body)
	assert.Equal(t, other.TotalQuota/2, vip.TotalQuota)
	assert.NotEqual(t, other.PricingRevision, vip.PricingRevision)
	assert.NotEqual(t, other.AccountBinding, vip.AccountBinding)
}

func TestVideoQuoteHTTPRejectsAmbiguousRoutesAndUnknownUsage(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5}}]}`
	for _, test := range []struct {
		name, changed string
	}{
		{"unmeasured H3 reference video", strings.Replace(body, `"seconds":5`, `"seconds":5,"metadata":{"reference_video":["https://no-fetch.invalid/reference.mp4"]}`, 1)},
		{"unmeasured H3 video count", strings.Replace(body, `"id":"one"`, `"id":"one","reference_counts":{"videos":1}`, 1)},
		{"unmeasured H3 audio count", strings.Replace(body, `"id":"one"`, `"id":"one","reference_counts":{"audios":1}`, 1)},
		{"unsupported model", strings.Replace(body, `"MiniMax-H3"`, `"not-a-video-model"`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.snapshot(t)
			response := fixture.request(t, test.changed, "Bearer sk-quoteonly", "application/json")
			assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
			assert.Equal(t, before, fixture.snapshot(t))
		})
	}
	// Equivalent eligible channels agree; quote creation neither allocates one
	// nor advances credentials. Different auto-group prices cannot agree.
	channel := &model.Channel{Id: 872, Type: constant.ChannelTypeMiniMax, Key: "synthetic-unused-second-credential", Name: "Same-price route", Models: "MiniMax-H3", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, fixture.db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(fixture.db))
	fixture.successfulQuote(t, body)
	require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Updates(map[string]any{"group": "auto", "auto_groups": `["default","vip"]`}).Error)
	before := fixture.snapshot(t)
	response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
	assert.Equal(t, before, fixture.snapshot(t))
	// A downstream override can change billable quantities and is not priced
	// from the pre-override request by guessing.
	require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Update("group", "").Error)
	require.NoError(t, fixture.db.Model(&model.Channel{}).Where("id = ?", 872).Update("param_override", `{"seconds":15}`).Error)
	before = fixture.snapshot(t)
	response = fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Equal(t, before, fixture.snapshot(t))
}

func TestVideoQuoteHTTPReferenceCountsUseNativeLimitsWithoutMaterialIO(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	fixture.setExpressions(t, map[string]string{
		"hailuo::MiniMax-H3":       `tier("h3", u("seconds") * 0.04 + u("input_images") * 0.01)`,
		"blackrxai-video::model_5": `tier("blackrx", u("seconds") * 0.075714285714285714)`,
	})
	first := fixture.successfulQuote(t, `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5},"reference_counts":{"images":2}}]}`)
	require.Len(t, first.Segments, 1)
	assert.Equal(t, 110000, first.TotalQuota)
	assert.Equal(t, 2.0, first.Segments[0].Usage["input_images"])
	assert.Equal(t, 2, first.Segments[0].ReferenceCounts.Images)
	more := fixture.successfulQuote(t, `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5},"reference_counts":{"images":9}}]}`)
	assert.Equal(t, 145000, more.TotalQuota)
	assert.NotEqual(t, first.PricingRevision, more.PricingRevision)
	blackrx := fixture.successfulQuote(t, `{"model":"model_5","works_count":1,"segments":[{"id":"one","parameters":{"seconds":15,"size":"1280x720"},"reference_counts":{"images":30,"videos":10,"audios":10}}]}`)
	assert.Equal(t, types.VideoQuoteReferenceCounts{Images: 30, Videos: 10, Audios: 10}, blackrx.Segments[0].ReferenceCounts)
	// Public URLs are counted by the real decoder without downloading content.
	fixture.successfulQuote(t, `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5,"reference_image":["https://no-fetch.invalid/image.png"]}}]}`)
	for _, body := range []string{
		`{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5},"reference_counts":{"images":10}}]}`,
		`{"model":"model_5","works_count":1,"segments":[{"id":"one","parameters":{"seconds":15},"reference_counts":{"images":31}}]}`,
		`{"model":"model_5","works_count":1,"segments":[{"id":"one","parameters":{"seconds":15,"image_urls":["https://no-fetch.invalid/a.png"]},"reference_counts":{"images":30}}]}`,
	} {
		before := fixture.snapshot(t)
		response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
		assert.Equal(t, before, fixture.snapshot(t))
	}
}

func TestVideoQuoteHTTPNativeModelBindingFailsClosed(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5}}]}`
	fixture.successfulQuote(t, body)
	source, err := os.ReadFile("../plugins/tasks/hailuo/plugin.js")
	require.NoError(t, err)
	restricted := strings.Replace(string(source), `}, "openai_video"],`, `}, {name: "openai_video", models: ["MiniMax-Hailuo-2.3"]}],`, 1)
	require.NotEqual(t, string(source), restricted)
	_, err = jsplugin.DefaultRegistry.Register(restricted, jsplugin.Options{})
	require.NoError(t, err)
	before := fixture.snapshot(t)
	response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
	assert.Equal(t, before, fixture.snapshot(t))
	_, err = jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	fixture.setExpressions(t, map[string]string{"blackrxai-video::lec-mj-seedance-2-5-cd-720p": `tier("base", u("seconds") * 0.04)`})
	body = `{"model":"lec-mj-seedance-2-5-cd-720p","works_count":1,"segments":[{"id":"one","parameters":{"seconds":30,"size":"1280x720"}}]}`
	fixture.successfulQuote(t, body)
	require.NoError(t, fixture.db.Model(&model.Channel{}).Where("id = ?", 871).Update("model_mapping", `{"lec-mj-seedance-2-5-cd-720p":"bt-seedance-2.0"}`).Error)
	before = fixture.snapshot(t)
	response = fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
	assert.Equal(t, before, fixture.snapshot(t), "a 30-second-only alias must not quote against a different 15-second native contract")
}

func TestVideoQuoteHTTPCachedChannelsAndMalformedSettingsStayUnchanged(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	var channel model.Channel
	require.NoError(t, fixture.db.First(&channel, 870).Error)
	channel.Key = "synthetic-unused-key-one\nsynthetic-unused-key-two"
	channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyPollingIndex: 1, MultiKeyMode: constant.MultiKeyModePolling}
	require.NoError(t, fixture.db.Save(&channel).Error)
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	cached, err := model.CacheGetChannel(870)
	require.NoError(t, err)
	require.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex)
	beforeCached, err := common.Marshal(cached)
	require.NoError(t, err)
	beforeKeys := append([]string(nil), cached.Keys...)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5}}]}`
	fixture.successfulQuote(t, body)
	afterCached, err := common.Marshal(cached)
	require.NoError(t, err)
	assert.Equal(t, string(beforeCached), string(afterCached))
	assert.Equal(t, beforeKeys, cached.Keys)
	assert.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex, "a quote must not select or rotate a credential")
	for _, useCache := range []bool{true, false} {
		common.MemoryCacheEnabled = useCache
		require.NoError(t, fixture.db.Model(&model.Channel{}).Where("id = ?", 870).Update("setting", `{invalid`).Error)
		model.InitChannelCache()
		before := fixture.snapshot(t)
		var cachedBefore string
		if useCache {
			cached, err = model.CacheGetChannel(870)
			require.NoError(t, err)
			encoded, err := common.Marshal(cached)
			require.NoError(t, err)
			cachedBefore = string(encoded)
		}
		response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
		assert.Equal(t, before, fixture.snapshot(t), "a quote must not repair or save malformed channel settings")
		if useCache {
			encoded, err := common.Marshal(cached)
			require.NoError(t, err)
			assert.Equal(t, cachedBefore, string(encoded))
		}
	}
}

func TestVideoQuoteHTTPNativeParametersAreValidated(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	fixture.setExpressions(t, map[string]string{
		"hailuo::MiniMax-H3":                           `tier("h3", u("seconds") * 0.04)`,
		"blackrxai-video::lec-mj-seedance-2-5-cd-720p": `tier("br", u("seconds") * 0.04)`,
	})
	for _, test := range []struct {
		model, parameters string
	}{
		{"MiniMax-H3", `{"seconds":0}`},
		{"MiniMax-H3", `{"seconds":3}`},
		{"MiniMax-H3", `{"seconds":16}`},
		{"MiniMax-H3", `{"seconds":5.5}`},
		{"MiniMax-H3", `{"seconds":5,"duration":6}`},
		{"MiniMax-H3", `{"seconds":5,"resolution":"4K"}`},
		{"MiniMax-H3", `{"seconds":5,"ratio":"1:0"}`},
		{"MiniMax-H3", `{"seconds":5,"resolution":"2K","metadata":{"metaso_resolution":"768P"}}`},
		{"lec-mj-seedance-2-5-cd-720p", `{"seconds":15}`},
		{"lec-mj-seedance-2-5-cd-720p", `{"seconds":30,"size":"4096x4096"}`},
		{"lec-mj-seedance-2-5-cd-720p", `{"seconds":30,"size":"1280x720","ratio":"9:16"}`},
	} {
		body := fmt.Sprintf(`{"model":%q,"works_count":1,"segments":[{"id":"one","parameters":%s}]}`, test.model, test.parameters)
		before := fixture.snapshot(t)
		response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
		assert.Equal(t, before, fixture.snapshot(t))
	}
}

func TestVideoQuoteHTTPBoundsEligibleRoutes(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	// One H3 route already exists. These 63 deterministic routes reach the
	// actual resource limit; this is a boundary fixture, not a stress loop.
	channels := make([]model.Channel, 0, 63)
	abilities := make([]model.Ability, 0, 66)
	for index := range 63 {
		channelID := 1000 + index
		channels = append(channels, model.Channel{Id: channelID, Type: constant.ChannelTypeMiniMax, Key: fmt.Sprintf("synthetic-unused-bounded-%d", index), Name: fmt.Sprintf("Bounded quote route %d", index), Models: "MiniMax-H3", Group: "default", Status: common.ChannelStatusEnabled})
		abilities = append(abilities, model.Ability{Group: "default", Model: "MiniMax-H3", ChannelId: channelID, Enabled: true})
	}
	// Each irrelevant row differs at a distinct predicate applied before the
	// limit. Their presence must not turn 64 usable routes into a false 65.
	abilities = append(abilities,
		model.Ability{Group: "unavailable", Model: "MiniMax-H3", ChannelId: 870, Enabled: true},
		model.Ability{Group: "default", Model: "unrelated-video", ChannelId: 870, Enabled: true},
		model.Ability{Group: "default", Model: "MiniMax-H3", ChannelId: 871, Enabled: false},
	)
	require.NoError(t, fixture.db.Create(&channels).Error)
	require.NoError(t, fixture.db.Create(&abilities).Error)
	var relevant int64
	require.NoError(t, fixture.db.Model(&model.Ability{}).Where(&model.Ability{Group: "default", Model: "MiniMax-H3", Enabled: true}).Count(&relevant).Error)
	require.EqualValues(t, 64, relevant)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"one","parameters":{"seconds":5}}]}`
	fixture.successfulQuote(t, body)
	channel := &model.Channel{Id: 1063, Type: constant.ChannelTypeMiniMax, Key: "synthetic-unused-sixty-fifth", Name: "Over quote route limit", Models: "MiniMax-H3", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, fixture.db.Create(channel).Error)
	require.NoError(t, fixture.db.Create(&model.Ability{Group: "default", Model: "MiniMax-H3", ChannelId: channel.Id, Enabled: true}).Error)
	require.NoError(t, fixture.db.Model(&model.Ability{}).Where(&model.Ability{Group: "default", Model: "MiniMax-H3", Enabled: true}).Count(&relevant).Error)
	require.EqualValues(t, 65, relevant)
	before := fixture.snapshot(t)
	response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"code":"quote_unavailable"`)
	assert.Equal(t, before, fixture.snapshot(t), "a bounded scan must not silently truncate eligible routes or start work")
}

func TestVideoQuoteHTTPClientRequestedExamples(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	fixture.setExpressions(t, map[string]string{
		"hailuo::MiniMax-H3":               `tier("h3", u("seconds") * 0.04)`,
		"blackrxai-video::bt-seedance-2.0": `tier("base", 0.2142857142857142857 + u("seconds") * 0.0142857142857142857)`,
		"blackrxai-video::video-sd25-480p": `tier("base", u("seconds") * 0.1)`,
	})
	var examples []map[string]any
	for _, test := range []struct {
		name, model, parameters string
		seconds, status         int
	}{
		{"grok audio", "grok-imagine-video", `{"seconds":4,"audio":true}`, 4, 400},
		{"grok silent", "grok-imagine-video", `{"seconds":4,"audio":false}`, 4, 400},
		{"BT concrete size", "bt-seedance-2.0", `{"seconds":10,"size":"1792x1024"}`, 10, 200},
		{"SD25 concrete size", "video-sd25-480p", `{"seconds":15,"size":"1280x720","gen_type":"text_to_video","n":1}`, 15, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, candidate := range []struct {
				model, parameters string
				status            int
			}{{test.model, test.parameters, test.status}, {"MiniMax-H3", fmt.Sprintf(`{"seconds":%d,"metadata":{"resolution":"768P"}}`, test.seconds), 200}} {
				body := fmt.Sprintf(`{"model":%q,"works_count":1,"segments":[{"id":"clip-1","parameters":%s,"reference_counts":{"images":0,"videos":0,"audios":0}}]}`, candidate.model, candidate.parameters)
				before := fixture.snapshot(t)
				response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
				assert.Equal(t, candidate.status, response.Code, response.Body.String())
				assert.Equal(t, before, fixture.snapshot(t))
				var request, result any
				require.NoError(t, common.Unmarshal([]byte(body), &request))
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				examples = append(examples, map[string]any{"case": test.name, "method": "POST", "path": "/v1/video/quote", "request": request, "http_status": response.Code, "response": result})
			}
		})
	}
	if output := os.Getenv("VIDEO_QUOTE_FIXTURE_OUTPUT"); output != "" {
		output = strings.TrimSuffix(output, filepath.Ext(output)) + "-requested-examples.json"
		require.True(t, filepath.IsAbs(output))
		require.NoError(t, os.MkdirAll(filepath.Dir(output), 0o700))
		encoded, err := common.Marshal(map[string]any{"fixture": "synthetic configured prices, not production user quotes; Grok is not configured by this fixture", "outbound_requests": fixture.transport.attempts.Load(), "examples": examples})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, encoded, 0o600))
	}
}

func TestVideoQuoteHTTPH3AmountsAndSegmentRounding(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	body := `{"model":"MiniMax-H3","works_count":3,"segments":[{"id":"wide","parameters":{"seconds":5,"resolution":"768P"}},{"id":"detail","parameters":{"seconds":"8","resolution":"2K"}}]}`
	before := fixture.snapshot(t)
	response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var quote types.VideoQuoteResponse
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	assert.Equal(t, "video.quote", quote.Object)
	assert.Equal(t, 1, quote.SchemaVersion)
	assert.Equal(t, "CNY", quote.Currency)
	assert.Equal(t, 500000.0, quote.QuotaPerUSD)
	assert.Equal(t, 7.0, quote.USDExchangeRate)
	require.Len(t, quote.Segments, 2)
	assert.Equal(t, 100000, quote.Segments[0].Quota)
	assert.Equal(t, 320000, quote.Segments[1].Quota)
	assert.Equal(t, "1.4", quote.Segments[0].EstimatedAmount)
	assert.Equal(t, "4.48", quote.Segments[1].EstimatedAmount)
	assert.Equal(t, 420000, quote.OneWorkQuota)
	assert.Equal(t, 1260000, quote.TotalQuota)
	assert.Equal(t, "5.88", quote.EstimatedOneWork)
	assert.Equal(t, "17.64", quote.EstimatedTotal)
	assert.True(t, quote.EstimateOnly)
	assert.False(t, quote.Charged)
	assert.False(t, quote.Submitted)
	assert.False(t, quote.Reserved)
	assert.NotEmpty(t, quote.QuoteID)
	assert.NotEmpty(t, quote.AccountBinding)
	assert.NotEmpty(t, quote.PricingRevision)
	assert.Greater(t, quote.ExpiresAt, quote.CreatedAt)
	assert.LessOrEqual(t, quote.ExpiresAt-quote.CreatedAt, int64(300))
	assert.Equal(t, before, fixture.snapshot(t))
	// Two individually rounded half-quota segments cost 2, not round(0.5+0.5).
	fixture.setExpressions(t, map[string]string{"hailuo::MiniMax-H3": `tier("tiny", u("seconds") * 0.0000002)`})
	response = fixture.request(t, `{"model":"MiniMax-H3","works_count":3,"segments":[{"id":"a","parameters":{"seconds":5}},{"id":"b","parameters":{"seconds":5}}]}`, "Bearer sk-quoteonly", "application/json")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	assert.Equal(t, 1, quote.Segments[0].Quota)
	assert.Equal(t, 1, quote.Segments[1].Quota)
	assert.Equal(t, 2, quote.OneWorkQuota)
	assert.Equal(t, 6, quote.TotalQuota)
	assert.Equal(t, "0.000028", quote.EstimatedOneWork)
	assert.Equal(t, "0.000084", quote.EstimatedTotal)
	assert.Equal(t, before, fixture.snapshot(t))
}

func TestVideoQuoteHTTPBlackrxConfiguredTariffs(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	// The released configuration uses 10 credits/CNY and adds CNY0.10/s.
	// These test-configured expressions are not a production price snapshot.
	prices := []struct {
		model      string
		seconds    int
		costCNY    string
		perSecond  bool
		wantAmount string
	}{
		{"lec-mj-seedance-2-5-cd-720p", 30, "5.5", false, "8.5"},
		{"sd-25-900-2", 30, "6.5", false, "9.5"},
		{"k2-sd2.5-720p", 30, "8.6", false, "11.6"},
		{"lec-sz-sd2-full-480p", 30, "8.5", false, "11.5"},
		{"video-sd25-480p", 30, "0.6", true, "21"},
		{"model_5", 30, "0.43", true, "15.9"},
		{"sd2.5-720p-x2", 30, "0.52", true, "18.6"},
		{"sd2-th-2.0-720p", 15, "2.4", false, "3.9"},
		{"bt-seedance-2.0", 15, "1.5", false, "3"},
		{"lec-rs-seedance-2-0-fast", 15, "2.5", false, "4"},
		{"sd20-933-720p", 15, "3.2", false, "4.7"},
		{"sd2-hn2-480-pro", 15, "4.3", false, "5.8"},
		{"v3-sd2.0-933-720p", 15, "4.5", false, "6"},
		{"sd-20-9/3/3-1", 15, "5", false, "6.5"},
		{"sd-20-903", 15, "1.8", false, "3.3"},
	}
	expressions := make(map[string]string)
	for _, price := range prices {
		cost := decimal.RequireFromString(price.costCNY)
		body := fmt.Sprintf(`%s + u("seconds") * %s`, cost.Div(decimal.NewFromInt(7)), decimal.RequireFromString("0.1").Div(decimal.NewFromInt(7)))
		if price.perSecond {
			body = fmt.Sprintf(`u("seconds") * %s`, cost.Add(decimal.RequireFromString("0.1")).Div(decimal.NewFromInt(7)))
		}
		expressions["blackrxai-video::"+price.model] = `tier("base", ` + body + `)`
	}
	fixture.setExpressions(t, expressions)
	before := fixture.snapshot(t)
	for _, price := range prices {
		t.Run(price.model, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":%q,"works_count":2,"segments":[{"id":"single","parameters":{"seconds":%d,"size":"1280x720"}}]}`, price.model, price.seconds)
			response := fixture.request(t, body, "Bearer sk-quoteonly", "application/json")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var quote types.VideoQuoteResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
			want := decimal.RequireFromString(price.wantAmount)
			wantQuota := common.QuotaRound(want.Div(decimal.NewFromInt(7)).Mul(decimal.NewFromInt(500000)).InexactFloat64())
			assert.Equal(t, wantQuota, quote.OneWorkQuota)
			assert.Equal(t, wantQuota*2, quote.TotalQuota)
			amount, err := decimal.NewFromString(quote.EstimatedOneWork)
			require.NoError(t, err)
			assert.True(t, amount.Sub(want).Abs().LessThanOrEqual(decimal.RequireFromString("0.000007")), "quota rounding must be reflected in the CNY amount: %s", amount)
			assert.Equal(t, before, fixture.snapshot(t))
		})
	}
}

func TestVideoQuoteHTTPAuthenticationIsReadOnly(t *testing.T) {
	fixture := newVideoQuoteFixture(t)
	body := `{"model":"MiniMax-H3","works_count":1,"segments":[{"id":"shot1","parameters":{"seconds":5,"resolution":"768P"}}]}`
	for _, test := range []struct {
		name, credential string
		token, user      map[string]any
		status           int
	}{
		{"exhausted token and zero wallet allowed", "Bearer sk-quoteonly", nil, nil, 200},
		{"enabled zero token allowed", "Bearer sk-quoteonly", map[string]any{"status": common.TokenStatusEnabled}, nil, 200},
		{"missing bearer", "", nil, nil, 401},
		{"wrong bearer", "Bearer sk-unknown", nil, nil, 401},
		{"non bearer", "Basic quoteonly", nil, nil, 401},
		{"pinned key rejected", "Bearer sk-quoteonly-870", nil, nil, 401},
		{"expired time", "Bearer sk-quoteonly", map[string]any{"expired_time": time.Now().Unix() - 10}, nil, 401},
		{"disabled token", "Bearer sk-quoteonly", map[string]any{"status": common.TokenStatusDisabled}, nil, 401},
		{"expired status", "Bearer sk-quoteonly", map[string]any{"status": common.TokenStatusExpired}, nil, 401},
		{"disabled user", "Bearer sk-quoteonly", nil, map[string]any{"status": common.UserStatusDisabled}, 403},
		{"IP restricted", "Bearer sk-quoteonly", map[string]any{"allow_ips": "192.0.2.0/24"}, nil, 403},
		{"model restricted", "Bearer sk-quoteonly", map[string]any{"model_limits_enabled": true, "model_limits": "different-model"}, nil, 403},
		{"unusable token group", "Bearer sk-quoteonly", map[string]any{"group": "unavailable-secret-group"}, nil, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Updates(map[string]any{"status": common.TokenStatusExhausted, "expired_time": -1, "allow_ips": "", "model_limits_enabled": false, "model_limits": "", "group": ""}).Error)
			require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userID).Update("status", common.UserStatusEnabled).Error)
			if test.token != nil {
				require.NoError(t, fixture.db.Model(&model.Token{}).Where("id = ?", fixture.userID).Updates(test.token).Error)
			}
			if test.user != nil {
				require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userID).Updates(test.user).Error)
			}
			before := fixture.snapshot(t)
			response := fixture.request(t, body, test.credential, "application/json")
			assert.Equal(t, test.status, response.Code, response.Body.String())
			assert.Equal(t, before, fixture.snapshot(t), "authentication and quoting must not mutate token state, balances, tasks or admission")
		})
	}
}
