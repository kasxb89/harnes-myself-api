package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Every outbound provider request is rewritten to this test's loopback server;
// an unrecognised host fails closed. The real plugin and host dispatcher run.
type h3FixtureTransport struct {
	base *url.URL
	next http.RoundTripper
}

func (r h3FixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Hostname() != "metaso.cn" {
		return nil, fmt.Errorf("H3 fixture blocked an external target")
	}
	validSubmit := request.Method == http.MethodPost && request.URL.Path == "/api/minimax/v2/video_generation"
	validQuery := request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/minimax/v2/query/video_generation/")
	if !validSubmit && !validQuery {
		return nil, fmt.Errorf("H3 fixture blocked an unexpected provider path")
	}
	local := request.Clone(request.Context())
	copyURL := *request.URL
	copyURL.Scheme, copyURL.Host = r.base.Scheme, r.base.Host
	local.URL = &copyURL
	local.Host = r.base.Host
	return r.next.RoundTrip(local)
}

type h3RouterFixture struct {
	logDB             *gorm.DB
	db                *gorm.DB
	engine            *gin.Engine
	mu                sync.Mutex
	posts, gets       int
	queryMode         string
	postMode          string
	providerID        string
	queryByKey        map[string]string
	bodies            []map[string]any
	historyWriteError error
}

func newH3RouterFixture(t *testing.T, accountCount int) *h3RouterFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath := common.SQLitePath
	previousMaster, previousRedis := common.IsMasterNode, common.RedisEnabled
	previousMemory, previousBatch := common.MemoryCacheEnabled, common.BatchUpdateEnabled
	previousConsume := common.LogConsumeEnabled
	previousRetry := common.RetryTimes
	previousLimit, previousTaskTimeout := constant.TaskQueryLimit, constant.TaskTimeoutMinutes
	previousPollFailures := constant.TaskPollMaxFailures
	constant.TaskPollMaxFailures = 2
	constant.TaskQueryLimit, constant.TaskTimeoutMinutes = 100, 0
	common.IsMasterNode, common.RedisEnabled = false, false
	common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false
	common.LogConsumeEnabled = true
	common.RetryTimes = 4
	common.SQLitePath = t.TempDir() + "/h3.db"
	dsn := os.Getenv("TEST_H3_SQL_DSN")
	if dsn == "" {
		dsn = "local"
	} else {
		require.True(t, strings.Contains(dsn, "test_h3_") || strings.Contains(dsn, "/h3_admission"), "only an explicitly disposable database is accepted")
	}
	logDSN := os.Getenv("TEST_H3_LOG_SQL_DSN")
	if dsn == "local" {
		logDSN = "local"
	} else {
		require.NotEmpty(t, logDSN, "external fixture requires a separate disposable log database")
	}
	t.Setenv("SQL_MAX_OPEN_CONNS", "4")
	t.Setenv("SQL_MAX_IDLE_CONNS", "2")
	t.Setenv("LOG_SQL_DSN", logDSN)
	t.Setenv("SQL_DSN", dsn)
	require.NoError(t, model.InitDB())
	db := model.DB
	mainPath := common.SQLitePath
	common.SQLitePath = t.TempDir() + "/h3-log.db"
	require.NoError(t, model.InitLogDB())
	common.SQLitePath = mainPath
	logDB := model.LOG_DB
	require.NotSame(t, db, logDB)
	require.NoError(t, logDB.Migrator().DropTable(&model.Log{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	t.Cleanup(func() {
		if logSQL, err := logDB.DB(); err == nil {
			_ = logSQL.Close()
		}
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMain, previousLogType)
		common.SQLitePath = previousPath
		common.IsMasterNode, common.RedisEnabled = previousMaster, previousRedis
		common.MemoryCacheEnabled, common.BatchUpdateEnabled = previousMemory, previousBatch
		common.LogConsumeEnabled, common.RetryTimes = previousConsume, previousRetry
		constant.TaskQueryLimit, constant.TaskTimeoutMinutes = previousLimit, previousTaskTimeout
		constant.TaskPollMaxFailures = previousPollFailures
	})
	models := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Option{}, &model.Model{}, &model.Vendor{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.SubscriptionPreConsumeRecord{}, &model.H3Account{}, &model.H3Admission{}, &model.MediaPool{}, &model.MediaAdmission{}}
	models = append(models, model.WalletFundingModels()...)
	// This database is dedicated to this fixture, never a product database.
	require.NoError(t, db.Migrator().DropTable(models...))
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, model.MigrateH3Admission(db))
	require.NoError(t, model.MigrateH3Admission(db))
	var version string
	query := "SELECT version()"
	if common.MainDatabaseType() == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("H3 actual router database %s %s", common.MainDatabaseType(), version)
	source, err := os.ReadFile("../plugins/tasks/hailuo/plugin.js")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("hailuo") })
	savedPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"MiniMax-H3":0.01}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices)) })
	for _, user := range []model.User{
		{Id: 791, Username: "h3-router-admin", AffCode: "h3adm", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Quota: 100000000, Group: "default", AuthVersion: 1},
		{Id: 792, Username: "h3-router-member", AffCode: "h3mem", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100000000, Group: "default", AuthVersion: 1},
	} {
		require.NoError(t, db.Create(&user).Error)
	}
	for _, token := range []model.Token{
		{Id: 791, UserId: 791, Key: "h3routeradmin", Name: "H3 fixture admin", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100000000},
		{Id: 792, UserId: 792, Key: "h3routermember", Name: "H3 fixture member", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100000000},
	} {
		require.NoError(t, db.Create(&token).Error)
	}
	for i := range accountCount {
		channel := &model.Channel{Id: 790 + i, Type: constant.ChannelTypeMiniMax, Key: fmt.Sprintf("synthetic-h3-account-%d", i), Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("H3 fixture %d", i), Models: "MiniMax-H3", Group: "default"}
		baseURL := "https://metaso.cn/api/minimax"
		channel.BaseURL = &baseURL
		channel.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MiniMax-H3", ChannelId: channel.Id, Enabled: true}).Error)
		account := fmt.Sprintf("router-account-%d", i)
		require.NoError(t, model.ProvisionH3Account(context.Background(), account, channel.Id))
		require.NoError(t, model.ReconcileH3Account(context.Background(), account))
		require.NoError(t, model.SetH3AccountEnabled(context.Background(), account, true))
	}
	model.InitChannelCache()
	fixture := &h3RouterFixture{db: db, logDB: logDB, queryMode: "queued"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fixture.posts++
			var body map[string]any
			data, _ := io.ReadAll(r.Body)
			if err := common.Unmarshal(data, &body); err != nil {
				http.Error(w, "invalid fixture body", 400)
				return
			}
			fixture.bodies = append(fixture.bodies, body)
			if strings.HasPrefix(fixture.postMode, "auth-") {
				status := http.StatusUnauthorized
				if strings.Contains(fixture.postMode, "403") {
					status = http.StatusForbidden
				}
				code := status
				if strings.Contains(fixture.postMode, "mismatch") {
					code = http.StatusForbidden
				}
				rejection := map[string]any{"error": map[string]any{"http_code": code, "code": "fixture_auth_failed", "message": "synthetic provider credential detail must not leak"}}
				if strings.Contains(fixture.postMode, "receipt") {
					rejection["task_id"] = "contradictory-provider-receipt"
				}
				w.WriteHeader(status)
				encoded, _ := common.Marshal(rejection)
				_, _ = w.Write(encoded)
				return
			}
			if fixture.postMode == "timeout" {
				time.Sleep(100 * time.Millisecond)
			}
			if fixture.postMode == "local_failure_malformed" {
				// Reproduce a historical local FAILURE after the send fence,
				// without establishing any provider outcome. Responses must
				// observe it as UNKNOWN while preserving its own wire format.
				update := fixture.db.Model(&model.Task{}).Where("user_id = ? AND status = ?", 791, model.TaskStatusUnknown).Updates(map[string]any{"status": model.TaskStatusFailure, "progress": "100%", "finish_time": time.Now().Unix(), "fail_reason": "private observer failure", "data": common.RawMessage(`{"task":{"id":"unrelated-private-provider","status":"failed"}}`)})
				if update.Error != nil || update.RowsAffected != 1 {
					fixture.historyWriteError = fmt.Errorf("historical fixture update: rows=%d error=%v", update.RowsAffected, update.Error)
					http.Error(w, "loopback history fixture unavailable", http.StatusInternalServerError)
					return
				}
			}
			if fixture.postMode == "malformed" || fixture.postMode == "local_failure_malformed" {
				_, _ = io.WriteString(w, `{"task_id":`)
				return
			}
			id := fixture.providerID
			if id == "" {
				id = fmt.Sprintf("provider-%d", fixture.posts)
			}
			_, _ = fmt.Fprintf(w, `{"task_id":%q}`, id)
			return
		}
		fixture.gets++
		if fixture.queryMode == "newapi_wrapper" {
			_, _ = io.WriteString(w, `{"code":"success","data":{"task_id":"other","status":"FAILURE"}}`)
			return
		}
		if fixture.queryMode == "malformed" {
			_, _ = io.WriteString(w, `{"task":`)
			return
		}
		for mode, status := range map[string]int{"http401": 401, "http404": 404, "http503": 503} {
			if fixture.queryMode == mode {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"fixture_unavailable","message":"free transient observation failure"}}`)
				return
			}
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if fixture.queryMode == "api_error" {
			_, _ = io.WriteString(w, `{"error":{"code":"InvalidSubscription","message":"synthetic query rejection"}}`)
			return
		}
		status := fixture.queryMode
		if override := fixture.queryByKey[r.Header.Get("Authorization")]; override != "" {
			status = override
		}
		_, _ = fmt.Fprintf(w, `{"task":{"id":%q,"status":%q}}`, id, status)
	}))
	t.Cleanup(server.Close)
	service.InitHttpClient()
	client := service.GetHttpClient()
	previousTransport, previousTimeout := client.Transport, client.Timeout
	loopbackURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client.Transport = h3FixtureTransport{base: loopbackURL, next: http.DefaultTransport}
	client.Timeout = 2 * time.Second
	t.Cleanup(func() { client.Transport, client.Timeout = previousTransport, previousTimeout })
	previousAdaptor := service.GetTaskAdaptorFunc
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(platform) }
	t.Cleanup(func() { service.GetTaskAdaptorFunc = previousAdaptor })
	fixture.engine = gin.New()
	// Use the complete production registration, including plugin NoRoute.
	t.Setenv("FRONTEND_BASE_URL", "http://frontend.invalid")
	require.NotPanics(t, func() { SetRouter(fixture.engine, WebAssets{}) })
	return fixture
}

func (f *h3RouterFixture) request(t *testing.T, method, path, body, credential string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer sk-"+credential)
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, r)
	return w
}
func (f *h3RouterFixture) task(t *testing.T, id string) *model.Task {
	t.Helper()
	var task model.Task
	require.NoError(t, f.db.Where("task_id = ?", id).First(&task).Error)
	return &task
}
func (f *h3RouterFixture) balance(t *testing.T) (int, int) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, f.db.First(&user, 791).Error)
	require.NoError(t, f.db.First(&token, 791).Error)
	return user.Quota, token.RemainQuota
}
func (f *h3RouterFixture) occupied(t *testing.T) int {
	t.Helper()
	var accounts []model.H3Account
	require.NoError(t, f.db.Find(&accounts).Error)
	total := 0
	for _, a := range accounts {
		assert.LessOrEqual(t, a.Occupied, 4)
		total += a.Occupied
	}
	return total
}

func TestH3AdmissionFullRouterThreeEntrypointsAndCapacity(t *testing.T) {
	f := newH3RouterFixture(t, 4)
	routes := []string{"/v1/video/generations", "/v1/tasks/hailuo", "/v1/videos"}
	for i := range 16 {
		path := routes[i%len(routes)]
		w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"free loopback fixture","seconds":5}`, "h3routeradmin")
		require.True(t, w.Code == http.StatusOK || w.Code == http.StatusAccepted, "%s: %d %s", path, w.Code, w.Body.String())
		id := w.Header().Get("X-New-Api-Task-Id")
		require.NotEmpty(t, id, w.Body.String())
		if i == 0 {
			for _, prefix := range []string{"/v1/video/generations/", "/v1/tasks/", "/v1/videos/"} {
				denied := f.request(t, http.MethodGet, prefix+id, "", "h3routermember")
				assert.NotEqual(t, http.StatusOK, denied.Code)
				assert.NotContains(t, denied.Body.String(), id, "another user's task must not be disclosed")
			}
		}
		task := f.task(t, id)
		require.NotEmpty(t, task.PrivateData.UpstreamTaskID)
		require.Greater(t, task.Quota, 0, "real wallet reservation must be nonzero")
		for _, getPath := range []string{"/v1/video/generations/", "/v1/tasks/", "/v1/videos/"} {
			get := f.request(t, http.MethodGet, getPath+id, "", "h3routeradmin")
			require.Equal(t, http.StatusOK, get.Code, get.Body.String())
			assert.Contains(t, get.Body.String(), id)
		}
	}
	assert.Equal(t, 16, f.posts)
	assert.Equal(t, 16, f.occupied(t))
	wallet, token := f.balance(t)
	for _, path := range routes {
		w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"must not send","seconds":5}`, "h3routeradmin")
		require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), service.H3CapacityCode)
		var rejection map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &rejection))
		assert.Equal(t, "not_submitted", rejection["submission_state"])
		assert.Equal(t, false, rejection["retryable"])
		assert.Equal(t, true, rejection["resubmittable"])
		assert.Contains(t, rejection, "data")
		assert.Nil(t, rejection["data"])
		assert.NotContains(t, rejection, "id")
		assert.NotContains(t, rejection, "task_id")
		assert.Empty(t, w.Header().Get("X-New-Api-Task-Id"))
	}
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, wallet, walletAfter, "capacity rejection is before debit")
	assert.Equal(t, token, tokenAfter)
	assert.Equal(t, 16, f.posts, "capacity rejection sends no provider POST")
	assert.Equal(t, 16, f.occupied(t))
	// The public pricing endpoint advertises a configured ceiling even when
	// all slots are occupied; it must never misrepresent this as availability.
	pricingRequest := httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	pricingResponse := httptest.NewRecorder()
	f.engine.ServeHTTP(pricingResponse, pricingRequest)
	require.Equal(t, http.StatusOK, pricingResponse.Code, pricingResponse.Body.String())
	var publicPricing struct {
		Success bool `json:"success"`
		Data    []struct {
			Model      string         `json:"model_name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(pricingResponse.Body.Bytes(), &publicPricing))
	require.True(t, publicPricing.Success)
	require.Len(t, publicPricing.Data, 1)
	assert.Equal(t, "MiniMax-H3", publicPricing.Data[0].Model)
	hint, ok := publicPricing.Data[0].Parameters["x-client-concurrency"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(16), hint["max_parallelism"])
	assert.Equal(t, false, hint["reservation"])
	assert.Equal(t, "shared_account_pool", hint["scope"])
	// Administrative channel suffix pins are enforced; a regular user cannot
	// acquire this privilege by copying the credential syntax.
	denied := f.request(t, http.MethodPost, routes[0], `{"model":"MiniMax-H3","prompt":"denied"}`, "h3routermember-790")
	assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	pinned := f.request(t, http.MethodPost, routes[0], `{"model":"MiniMax-H3","prompt":"pinned-full"}`, "h3routeradmin-790")
	assert.Equal(t, http.StatusTooManyRequests, pinned.Code, pinned.Body.String())
	assert.Equal(t, 16, f.posts)
}

func (f *h3RouterFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts, f.gets
}

func TestH3AdmissionUnknownReceiptNeverRepostsOrRefunds(t *testing.T) {
	for _, mode := range []string{"malformed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := newH3RouterFixture(t, 1)
			f.postMode = mode
			service.GetHttpClient().Timeout = 25 * time.Millisecond
			beforeWallet, beforeToken := f.balance(t)
			w := f.request(t, http.MethodPost, "/v1/video/generations", `{"model":"MiniMax-H3","prompt":"uncertain free fixture","seconds":5}`, "h3routeradmin")
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			id := w.Header().Get("X-New-Api-Task-Id")
			require.NotEmpty(t, id)
			task := f.task(t, id)
			assert.EqualValues(t, model.TaskStatusUnknown, task.Status)
			assert.Empty(t, task.PrivateData.UpstreamTaskID)
			assert.NotEmpty(t, task.PrivateData.H3AdmissionID)
			assert.Greater(t, task.Quota, 0)
			wallet, token := f.balance(t)
			assert.Less(t, wallet, beforeWallet)
			assert.Less(t, token, beforeToken)
			for range 3 {
				for _, prefix := range []string{"/v1/video/generations/", "/v1/tasks/", "/v1/videos/"} {
					get := f.request(t, http.MethodGet, prefix+id, "", "h3routeradmin")
					require.Equal(t, http.StatusOK, get.Code, get.Body.String())
					assert.Contains(t, get.Body.String(), id)
				}
				service.RunTaskPollingOnce(context.Background(), nil)
			}
			posts, gets := f.counts()
			assert.Equal(t, 1, posts)
			assert.Zero(t, gets, "no provider identity means no guessed provider query")
			assert.Equal(t, 1, f.occupied(t))
			walletAfter, tokenAfter := f.balance(t)
			assert.Equal(t, wallet, walletAfter)
			assert.Equal(t, token, tokenAfter)
			assert.EqualValues(t, model.TaskStatusUnknown, f.task(t, id).Status)
		})
	}
}

func TestH3AdmissionQueryErrorThenTerminalRefundExactlyOnce(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	beforeWallet, beforeToken := f.balance(t)
	w := f.request(t, http.MethodPost, "/v1/videos", `{"model":"MiniMax-H3","prompt":"free query recovery fixture","seconds":5}`, "h3routeradmin")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	id := w.Header().Get("X-New-Api-Task-Id")
	task := f.task(t, id)
	require.NotEmpty(t, task.PrivateData.UpstreamTaskID)
	chargedWallet, chargedToken := f.balance(t)
	for _, mode := range []string{"api_error", "http401", "http404", "http503", "malformed", "newapi_wrapper"} {
		f.mu.Lock()
		f.queryMode = mode
		f.mu.Unlock()
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	assert.Equal(t, 1, f.occupied(t), "query API rejection is not a confirmed task failure")
	unknown := f.task(t, id)
	assert.EqualValues(t, model.TaskStatusUnknown, unknown.Status)
	assert.Equal(t, task.PrivateData.UpstreamTaskID, unknown.PrivateData.UpstreamTaskID)
	wallet, token := f.balance(t)
	assert.Equal(t, chargedWallet, wallet)
	assert.Equal(t, chargedToken, token)
	f.mu.Lock()
	f.queryMode = "failed"
	f.mu.Unlock()
	service.RunTaskPollingOnce(context.Background(), nil)
	terminal := f.task(t, id)
	require.EqualValues(t, model.TaskStatusFailure, terminal.Status)
	assert.Zero(t, terminal.Quota)
	assert.Zero(t, f.occupied(t))
	wallet, token = f.balance(t)
	assert.Equal(t, beforeWallet, wallet)
	assert.Equal(t, beforeToken, token)
	for _, prefix := range []string{"/v1/tasks/", "/v1/video/generations/", "/v1/videos/"} {
		get := f.request(t, http.MethodGet, prefix+id, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		assert.Contains(t, strings.ToLower(get.Body.String()), "failed")
		assert.Contains(t, get.Body.String(), `"provider_terminal_confirmed":true`)
		assert.NotContains(t, get.Body.String(), "refund_confirmed")
		assert.NotContains(t, get.Body.String(), "resubmittable")
		if prefix == "/v1/tasks/" {
			captureH3ReceiptFixture(t, "canonical_get_confirmed_provider_failure", get)
		}
	}
	for range 3 {
		service.RunTaskPollingOnce(context.Background(), nil)
	}
	wallet, token = f.balance(t)
	assert.Equal(t, beforeWallet, wallet, "terminal replay must not refund twice")
	assert.Equal(t, beforeToken, token)
	assert.Zero(t, f.occupied(t))
	posts, _ := f.counts()
	assert.Equal(t, 1, posts)
	var refunds int64
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ?", model.LogTypeRefund).Count(&refunds).Error)
	assert.EqualValues(t, 1, refunds)
}

func TestH3AdmissionSameProviderIDDoesNotCrossChannels(t *testing.T) {
	f := newH3RouterFixture(t, 2)
	f.providerID = "provider-shared-id"
	ids := []string{}
	for i := range 2 {
		w := f.request(t, http.MethodPost, "/v1/video/generations", `{"model":"MiniMax-H3","prompt":"two independent free accounts","seconds":5}`, fmt.Sprintf("h3routeradmin-%d", 790+i))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		ids = append(ids, w.Header().Get("X-New-Api-Task-Id"))
	}
	require.NotEqual(t, ids[0], ids[1])
	f.mu.Lock()
	f.queryByKey = map[string]string{"Bearer synthetic-h3-account-0": "failed", "Bearer synthetic-h3-account-1": "succeeded"}
	f.mu.Unlock()
	service.RunTaskPollingOnce(context.Background(), nil)
	assert.EqualValues(t, model.TaskStatusFailure, f.task(t, ids[0]).Status)
	assert.EqualValues(t, model.TaskStatusSuccess, f.task(t, ids[1]).Status)
	assert.Zero(t, f.occupied(t))
	posts, gets := f.counts()
	assert.Equal(t, 2, posts)
	assert.Equal(t, 2, gets)
}

func TestH3AdmissionTwoIndependentConnectionsDoNotOverbook(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	second, err := gorm.Open(f.db.Dialector, &gorm.Config{})
	require.NoError(t, err)
	firstSQL, err := f.db.DB()
	require.NoError(t, err)
	secondSQL, err := second.DB()
	require.NoError(t, err)
	require.NotSame(t, firstSQL, secondSQL, "independent SQL connection pools must contend on one database")
	t.Cleanup(func() { _ = secondSQL.Close() })
	firstSQL.SetMaxOpenConns(1)
	secondSQL.SetMaxOpenConns(1)
	stores := []model.H3AdmissionStore{{DB: f.db}, {DB: second}}
	newTask := func(id string) *model.Task {
		return &model.Task{TaskID: id, Platform: "hailuo", UserId: 791, Group: "default", ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	}
	for i := range 3 {
		require.NoError(t, stores[0].Acquire(context.Background(), newTask(fmt.Sprintf("task_reserved_%d", i)), "synthetic-h3-account-0"))
	}
	assert.Equal(t, 3, f.occupied(t))
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for i := range 2 {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			results <- stores[i].Acquire(context.Background(), newTask(fmt.Sprintf("task_last_slot_%d", i)), "synthetic-h3-account-0")
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	acquired, rejected := 0, 0
	for result := range results {
		if result == nil {
			acquired++
		} else {
			require.ErrorIs(t, result, model.ErrH3Capacity)
			rejected++
		}
	}
	assert.Equal(t, 1, acquired, "the two connections contend for the single final slot")
	assert.Equal(t, 1, rejected)
	require.ErrorIs(t, stores[1].Acquire(context.Background(), newTask("task_fifth_rejected"), "synthetic-h3-account-0"), model.ErrH3Capacity)
	assert.Equal(t, 4, f.occupied(t))
	var admissions, tasks int64
	require.NoError(t, f.db.Model(&model.H3Admission{}).Count(&admissions).Error)
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	assert.EqualValues(t, 4, admissions)
	assert.EqualValues(t, 4, tasks)
	wallet, token := f.balance(t)
	assert.Equal(t, 100000000, wallet)
	assert.Equal(t, 100000000, token)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func TestH3AdmissionUpgradeReconcilesUncertainHistoryIdempotently(t *testing.T) {
	f := newH3RouterFixture(t, 0)
	// The base release had ordinary task rows and no H3 ledger tables. Preserve
	// that JSON representation while migrating the additive ledger twice.
	require.NoError(t, f.db.Migrator().DropTable(&model.H3Admission{}, &model.H3Account{}))
	baseURL := "https://metaso.cn/api/minimax"
	channel := &model.Channel{Id: 790, Type: constant.ChannelTypeMiniMax, Key: "synthetic-h3-account-0", BaseURL: &baseURL, Status: common.ChannelStatusEnabled, Name: "H3 pre-ledger", Models: "MiniMax-H3", Group: "default"}
	channel.SetOtherSettings(kitdto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	require.NoError(t, f.db.Create(channel).Error)
	require.NoError(t, f.db.Create(&model.Ability{Group: "default", Model: "MiniMax-H3", ChannelId: 790, Enabled: true}).Error)
	now := time.Now().Unix()
	for _, test := range []struct {
		id       string
		status   model.TaskStatus
		finish   int64
		reason   string
		upstream string
	}{
		{"task_legacy_unknown", model.TaskStatusUnknown, 0, "", ""},
		{"task_legacy_timeout", model.TaskStatusFailure, now, "Task timed out", "provider-legacy-timeout"},
		{"legacy-provider-v1", model.TaskStatusInProgress, 0, "", ""},
		{"task_legacy_sql_null", model.TaskStatusUnknown, now, "", "provider-legacy-null"},
		{"task_legacy_success", model.TaskStatusSuccess, now, "", "provider-success"},
		{"task_legacy_failed", model.TaskStatusFailure, now, "provider explicitly failed", "provider-failed"},
	} {
		task := &model.Task{TaskID: test.id, Platform: "hailuo", UserId: 791, ChannelId: 790, Group: "default", Status: test.status, Progress: "100%", FinishTime: test.finish, FailReason: test.reason, Quota: 123, PrivateData: model.TaskPrivateData{Key: channel.Key, UpstreamTaskID: test.upstream}, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
		if test.id == "task_legacy_failed" {
			task.SetData(map[string]any{"task": map[string]any{"id": test.upstream, "status": "failed"}})
		}
		require.NoError(t, f.db.Create(task).Error)
	}
	// GORM's zero-value string is not SQL NULL; exercise the actual legacy row.
	result := f.db.Exec("UPDATE tasks SET status = NULL WHERE task_id = ?", "task_legacy_sql_null")
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
	var nullCount int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("task_id = ? AND status IS NULL", "task_legacy_sql_null").Count(&nullCount).Error)
	require.EqualValues(t, 1, nullCount)
	beforeWallet, beforeToken := f.balance(t)
	for range 2 {
		require.NoError(t, model.MigrateH3Admission(f.db))
	}
	require.NoError(t, model.ProvisionH3Account(context.Background(), "legacy-account", 790))
	for range 2 {
		require.NoError(t, model.ReconcileH3Account(context.Background(), "legacy-account"))
		assert.Equal(t, 4, f.occupied(t), "SQL NULL, unknown, timed-out and historical running rows each occupy one slot")
		var count int64
		require.NoError(t, f.db.Model(&model.H3Admission{}).Count(&count).Error)
		assert.EqualValues(t, 4, count, "repeated reconciliation must not multiply reservations")
	}
	for _, id := range []string{"task_legacy_unknown", "task_legacy_timeout", "legacy-provider-v1", "task_legacy_sql_null"} {
		task := f.task(t, id)
		assert.NotEmpty(t, task.PrivateData.H3AdmissionID)
		assert.Equal(t, 123, task.Quota, "migration never rewrites old billing")
		assert.NotEqual(t, "100%", task.Progress, "uncertain records must remain eligible for recovery")
		assert.Zero(t, task.FinishTime)
	}
	assert.EqualValues(t, model.TaskStatusUnknown, f.task(t, "task_legacy_sql_null").Status)
	assert.Equal(t, "provider-legacy-null", f.task(t, "task_legacy_sql_null").PrivateData.UpstreamTaskID)
	assert.EqualValues(t, model.TaskStatusInProgress, f.task(t, "legacy-provider-v1").Status)
	unfinished := make(map[string]bool)
	for _, task := range model.GetAllUnFinishSyncTasks(100) {
		unfinished[task.TaskID] = true
	}
	for _, id := range []string{"task_legacy_unknown", "task_legacy_timeout", "legacy-provider-v1", "task_legacy_sql_null"} {
		assert.True(t, unfinished[id], "legacy progress=100 must not hide %s from the real recovery selector", id)
	}
	assert.Empty(t, f.task(t, "task_legacy_success").PrivateData.H3AdmissionID)
	assert.Empty(t, f.task(t, "task_legacy_failed").PrivateData.H3AdmissionID)
	wallet, token := f.balance(t)
	assert.Equal(t, beforeWallet, wallet)
	assert.Equal(t, beforeToken, token)
	require.NoError(t, model.SetH3AccountEnabled(context.Background(), "legacy-account", true))
	model.InitChannelCache()
	get := f.request(t, http.MethodGet, "/v1/video/generations/task_legacy_sql_null", "", "h3routeradmin")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	assert.Contains(t, get.Body.String(), "task_legacy_sql_null")
	service.RunTaskPollingOnce(context.Background(), nil)
	assert.Equal(t, 4, f.occupied(t), "queued provider observations do not release legacy reservations")
	for _, path := range []string{"/v1/video/generations", "/v1/tasks/hailuo"} {
		w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"all legacy slots remain occupied"}`, "h3routeradmin")
		assert.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	}
	wallet, token = f.balance(t)
	assert.Equal(t, beforeWallet, wallet)
	assert.Equal(t, beforeToken, token)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets, "historical keys establish occupancy, but a current route cannot establish original execution provenance")
}

func TestH3AdmissionCancelledAfterReceiptRecoversSamePublicID(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	callback := "h3:cancel-after-durable-provider-receipt"
	require.NoError(t, f.db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "tasks" || tx.Error != nil || cancelled {
			return
		}
		values, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		private, ok := values["private_data"].(model.TaskPrivateData)
		if ok && private.UpstreamTaskID != "" {
			cancelled = true
			cancel()
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Update().Remove(callback) })
	r := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(`{"model":"MiniMax-H3","prompt":"cancel only after free provider receipt","seconds":5}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer sk-h3routeradmin")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, r)
	require.True(t, cancelled, "fixture must cancel at the durable receipt boundary")
	require.True(t, w.Code == http.StatusOK || w.Code == http.StatusAccepted, w.Body.String())
	id := w.Header().Get("X-New-Api-Task-Id")
	require.NotEmpty(t, id)
	task := f.task(t, id)
	require.NotEmpty(t, task.PrivateData.UpstreamTaskID)
	wallet, token := f.balance(t)
	for _, prefix := range []string{"/v1/video/generations/", "/v1/tasks/", "/v1/videos/"} {
		get := f.request(t, http.MethodGet, prefix+id, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		assert.Contains(t, get.Body.String(), id)
	}
	f.mu.Lock()
	f.queryMode = "queued"
	f.mu.Unlock()
	service.RunTaskPollingOnce(context.Background(), nil)
	assert.Equal(t, task.PrivateData.UpstreamTaskID, f.task(t, id).PrivateData.UpstreamTaskID)
	assert.Equal(t, 1, f.occupied(t))
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, wallet, walletAfter)
	assert.Equal(t, token, tokenAfter)
	posts, gets := f.counts()
	assert.Equal(t, 1, posts)
	assert.Greater(t, gets, 0)
}

func TestH3AdmissionOldAndNewNativeParametersReachProvider(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	for _, test := range []struct {
		path, body        string
		seconds           float64
		resolution, ratio string
	}{
		{"/v1/video/generations", `{"model":"MiniMax-H3","prompt":"legacy fixture","duration":5,"size":"1280x720"}`, 5, "768P", "16:9"},
		{"/v1/video/generations", `{"model":"MiniMax-H3","prompt":"metadata fixture","seconds":8,"metadata":{"metaso_resolution":"2K","metaso_ratio":"9:16"}}`, 8, "2K", "9:16"},
		{"/v1/videos", `{"model":"MiniMax-H3","prompt":"new fixture","seconds":9,"resolution":"2K","ratio":"4:3"}`, 9, "2K", "4:3"},
	} {
		w := f.request(t, http.MethodPost, test.path, test.body, "h3routeradmin")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		f.mu.Lock()
		body := f.bodies[len(f.bodies)-1]
		f.mu.Unlock()
		assert.Equal(t, test.seconds, body["duration"])
		assert.Equal(t, test.resolution, body["resolution"])
		assert.Equal(t, test.ratio, body["ratio"])
	}
	posts, _ := f.counts()
	assert.Equal(t, 3, posts)
}

func TestH3AdmissionUnsentFailuresReleaseWithoutDebit(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	wallet, token := f.balance(t)
	invalid := f.request(t, http.MethodPost, "/v1/tasks/hailuo", `{"model":"MiniMax-H3","prompt":"invalid duration must not send","seconds":99}`, "h3routeradmin")
	assert.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	if id := invalid.Header().Get("X-New-Api-Task-Id"); id != "" {
		assertH3NotSubmittedReceipt(t, invalid.Body.Bytes(), id, "not_charged", false)
	}
	assert.Zero(t, f.occupied(t))
	walletAfter, tokenAfter := f.balance(t)
	assert.Equal(t, wallet, walletAfter)
	assert.Equal(t, token, tokenAfter)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 0).Error)
	insufficient := f.request(t, http.MethodPost, "/v1/video/generations", `{"model":"MiniMax-H3","prompt":"insufficient wallet must not send","seconds":5}`, "h3routeradmin")
	assert.Equal(t, http.StatusForbidden, insufficient.Code, insufficient.Body.String())
	assertH3NotSubmittedReceipt(t, insufficient.Body.Bytes(), insufficient.Header().Get("X-New-Api-Task-Id"), "not_charged", false)
	assert.Zero(t, f.occupied(t))
	walletAfter, tokenAfter = f.balance(t)
	assert.Zero(t, walletAfter)
	assert.Equal(t, token, tokenAfter)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

// This receipt is the durable boundary that permits a separately requested
// replacement attempt. It never instructs an automatic HTTP retry.
func assertH3NotSubmittedReceipt(t *testing.T, data []byte, id, billing string, nested bool) {
	t.Helper()
	require.NotEmpty(t, id)
	var body map[string]any
	require.NoError(t, common.Unmarshal(data, &body))
	if nested {
		var ok bool
		body, ok = body["submission_receipt"].(map[string]any)
		require.True(t, ok, string(data))
	}
	assert.Equal(t, id, body["id"])
	assert.Equal(t, id, body["task_id"])
	assert.Equal(t, service.H3NotSentCode, body["submission_code"])
	assert.Equal(t, "not_submitted", body["submission_state"])
	assert.Equal(t, billing, body["billing_state"])
	assert.Equal(t, false, body["upstream_submitted"])
	assert.Equal(t, false, body["retryable"])
	assert.Equal(t, true, body["resubmittable"])
	if billing == "refunded" {
		assert.Equal(t, true, body["refund_confirmed"])
	} else {
		assert.NotContains(t, body, "refund_confirmed")
	}
	recovery, ok := body["recovery"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, http.MethodGet, recovery["method"])
	assert.Equal(t, "/v1/tasks/"+id, recovery["path"])
}

func captureH3ReceiptFixture(t *testing.T, name string, response *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	encoded, err := common.Marshal(map[string]any{"name": name, "http_status": response.Code, "headers": map[string]string{"Content-Type": response.Header().Get("Content-Type"), "Cache-Control": response.Header().Get("Cache-Control"), "X-New-Api-Task-Id": response.Header().Get("X-New-Api-Task-Id")}, "body": body})
	require.NoError(t, err)
	t.Log("H3_RECEIPT_FIXTURE " + string(encoded))
}

func TestH3AdmissionUnsentReceiptsPreserveBusinessErrorsAcrossEntrypoints(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/tasks/hailuo", "/v1/videos", "/v1/responses"} {
		for _, source := range []string{"wallet", "token"} {
			t.Run(path+"/"+source, func(t *testing.T) {
				f := newH3RouterFixture(t, 1)
				if source == "wallet" {
					require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 0).Error)
				} else {
					require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("remain_quota", 1).Error)
				}
				wallet, token := f.balance(t)
				w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","input":"free failure receipt","prompt":"free failure receipt","seconds":5,"stream":true}`, "h3routeradmin")
				require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
				id := w.Header().Get("X-New-Api-Task-Id")
				assertH3NotSubmittedReceipt(t, w.Body.Bytes(), id, "not_charged", false)
				assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
				code := "insufficient_user_quota"
				if source == "token" {
					code = "pre_consume_token_quota_failed"
				}
				persisted := f.task(t, id)
				assert.Equal(t, code, persisted.PrivateData.SubmissionErrorCode)
				reason := "Insufficient account balance or subscription quota to submit this task"
				if source == "token" {
					reason = "Insufficient API token quota to submit this task"
				}
				assert.Equal(t, reason, persisted.FailReason)
				var body map[string]any
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, code, body["code"], "receipt must not erase the actionable original business error")
				if path == "/v1/video/generations" && source == "wallet" {
					captureH3ReceiptFixture(t, "post_wallet_insufficient_not_charged", w)
				}
				if path == "/v1/responses" {
					errorBody, ok := body["error"].(map[string]any)
					require.True(t, ok)
					assert.Equal(t, code, errorBody["code"])
					assert.NotContains(t, w.Header().Get("Content-Type"), "text/event-stream")
				}
				for _, prefix := range []string{"/v1/tasks/", "/v1/video/generations/", "/v1/videos/"} {
					get := f.request(t, http.MethodGet, prefix+id, "", "h3routeradmin")
					require.Equal(t, http.StatusOK, get.Code, get.Body.String())
					assertH3NotSubmittedReceipt(t, get.Body.Bytes(), id, "not_charged", true)
					assert.Equal(t, "private, no-store", get.Header().Get("Cache-Control"))
					if path == "/v1/video/generations" && source == "wallet" && prefix == "/v1/tasks/" {
						captureH3ReceiptFixture(t, "canonical_get_not_charged", get)
					}
					denied := f.request(t, http.MethodGet, prefix+id, "", "h3routermember")
					assert.NotEqual(t, http.StatusOK, denied.Code)
					assert.NotContains(t, denied.Body.String(), id)
				}
				assert.Zero(t, f.occupied(t))
				walletAfter, tokenAfter := f.balance(t)
				assert.Equal(t, wallet, walletAfter)
				assert.Equal(t, token, tokenAfter)
				posts, gets := f.counts()
				assert.Zero(t, posts)
				assert.Zero(t, gets)
			})
		}
	}
}

func TestH3AdmissionHistoricalReceiptRequiresExactDurableIdentityAndFinance(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	task := &model.Task{TaskID: "task_h3_historical_unsent", Platform: "hailuo", UserId: 791, Group: "default", ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	require.NoError(t, model.AcquireH3Admission(context.Background(), task, "synthetic-h3-account-0"))
	require.NoError(t, model.ReleaseH3BeforeSend(context.Background(), task))
	baseline := f.task(t, task.TaskID)
	var ledger model.H3Admission
	require.NoError(t, f.db.Where("id = ?", task.TaskID).First(&ledger).Error)
	for _, prefix := range []string{"/v1/tasks/", "/v1/video/generations/", "/v1/videos/"} {
		get := f.request(t, http.MethodGet, prefix+task.TaskID, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		assertH3NotSubmittedReceipt(t, get.Body.Bytes(), task.TaskID, "not_charged", true)
	}
	for _, tc := range []struct {
		name                        string
		taskValues, admissionValues map[string]any
	}{
		{name: "send_fence", admissionValues: map[string]any{"sending_at": 1}},
		{name: "not_released", admissionValues: map[string]any{"state": model.H3Unknown}},
		{name: "refund_pending", admissionValues: map[string]any{"billing_state": "refund_pending"}},
		{name: "billing_uncertain", admissionValues: map[string]any{"billing_state": "settlement_uncertain"}},
		{name: "quota_nonzero", taskValues: map[string]any{"quota": 1}},
		{name: "task_unknown", taskValues: map[string]any{"status": model.TaskStatusUnknown}},
		{name: "channel_mismatch", admissionValues: map[string]any{"channel_id": 999}},
		{name: "task_db_identity_mismatch", admissionValues: map[string]any{"task_db_id": baseline.ID + 100000}},
		{name: "provider_receipt", taskValues: map[string]any{"private_data": model.TaskPrivateData{H3AdmissionID: task.TaskID, UpstreamTaskID: "private-provider-id"}}},
		{name: "admission_identity_mismatch", taskValues: map[string]any{"private_data": model.TaskPrivateData{H3AdmissionID: "task_other_admission"}}},
		{name: "legacy_subscription_minimum_unproven", taskValues: map[string]any{"private_data": model.TaskPrivateData{H3AdmissionID: task.TaskID, BillingSource: "subscription", SubscriptionId: 791}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.taskValues != nil {
				require.NoError(t, f.db.Model(&model.Task{}).Where("id = ?", baseline.ID).Updates(tc.taskValues).Error)
			}
			if tc.admissionValues != nil {
				require.NoError(t, f.db.Model(&model.H3Admission{}).Where("id = ?", ledger.ID).Updates(tc.admissionValues).Error)
			}
			get := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routeradmin")
			require.Equal(t, http.StatusOK, get.Code, get.Body.String())
			assert.NotContains(t, get.Body.String(), "submission_receipt")
			assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
			assert.Contains(t, get.Body.String(), `"finished_at":0`)
			assert.NotContains(t, get.Body.String(), "private-provider-id")
			if tc.name == "send_fence" || tc.name == "refund_pending" {
				captureH3ReceiptFixture(t, "canonical_get_unproven_"+tc.name, get)
			}
			require.NoError(t, f.db.Model(&model.Task{}).Where("id = ?", baseline.ID).Updates(map[string]any{"quota": baseline.Quota, "status": baseline.Status, "private_data": baseline.PrivateData}).Error)
			require.NoError(t, f.db.Model(&model.H3Admission{}).Where("id = ?", ledger.ID).Updates(map[string]any{"state": ledger.State, "billing_state": ledger.BillingState, "sending_at": ledger.SendingAt, "task_db_id": ledger.TaskDBID, "channel_id": ledger.ChannelID}).Error)
		})
	}
	require.NoError(t, f.db.Delete(&model.H3Admission{}, "id = ?", ledger.ID).Error)
	legacy := f.request(t, http.MethodGet, "/v1/tasks/"+task.TaskID, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())
	assert.NotContains(t, legacy.Body.String(), "submission_receipt", "historical FAILURE alone is not evidence of no submission")
	assert.Contains(t, legacy.Body.String(), `"status":"UNKNOWN"`)
	captureH3ReceiptFixture(t, "canonical_get_legacy_no_admission", legacy)
	responseID := "resp_" + strings.TrimPrefix(task.TaskID, "task_")
	responseGet := f.request(t, http.MethodGet, "/v1/responses/"+responseID, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, responseGet.Code, responseGet.Body.String())
	assert.Contains(t, responseGet.Body.String(), `"object":"response"`)
	assert.Contains(t, responseGet.Body.String(), responseID)
	assert.NotContains(t, responseGet.Body.String(), `"status":"failed"`)
	assert.NotContains(t, responseGet.Body.String(), `"status":"completed"`)
	captureH3ReceiptFixture(t, "responses_get_legacy_failure_remains_pending", responseGet)
	missing := f.request(t, http.MethodGet, "/v1/tasks/task_does_not_exist", "", "h3routeradmin")
	assert.Equal(t, http.StatusNotFound, missing.Code)
	assert.NotContains(t, missing.Body.String(), "submission_receipt")
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func TestH3AdmissionProviderTerminalProjectionDoesNotCertifyFinancialUncertainty(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	task := &model.Task{TaskID: "task_h3_provider_terminal_projection", Platform: "hailuo", UserId: 791, Group: "default", ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	require.NoError(t, model.AcquireH3Admission(context.Background(), task, "synthetic-h3-account-0"))
	require.NoError(t, model.ReleaseH3BeforeSend(context.Background(), task))
	finishTime := f.task(t, task.TaskID).FinishTime
	for index, tc := range []struct {
		name, providerID, data, billing string
		quota                           int
		confirmed                       bool
	}{
		{name: "exact_string", providerID: "42", data: `{"task":{"id":"42","status":"failed"}}`, billing: "reserved", confirmed: true},
		{name: "safe_numeric_without_wire_evidence", providerID: "42", data: `{"task":{"id":42,"status":"failed"}}`, billing: "reserved"},
		{name: "safe_numeric_exponent_without_wire_evidence", providerID: "42", data: `{"task":{"id":4.2e1,"status":"failed"}}`, billing: "reserved"},
		{name: "large_string", providerID: "2113445600016039936", data: `{"task":{"id":"2113445600016039936","status":"cancelled"}}`, billing: "reserved", confirmed: true},
		{name: "wrong_identity", providerID: "42", data: `{"task":{"id":"43","status":"failed"}}`, billing: "reserved"},
		{name: "unsafe_numeric", providerID: "9007199254740993", data: `{"task":{"id":9007199254740993,"status":"failed"}}`, billing: "reserved"},
		{name: "fractional_numeric", providerID: "42", data: `{"task":{"id":42.01,"status":"failed"}}`, billing: "reserved"},
		{name: "rounded_fractional_numeric", providerID: "42", data: `{"task":{"id":42.000000000000000001,"status":"failed"}}`, billing: "reserved"},
		{name: "query_error_with_task", providerID: "42", data: `{"error":{"message":"failed to query"},"task":{"id":"42","status":"failed"}}`, billing: "reserved"},
		{name: "refund_pending", providerID: "42", data: `{"task":{"id":"42","status":"failed"}}`, billing: "refund_pending"},
		{name: "settlement_uncertain", providerID: "42", data: `{"task":{"id":"42","status":"failed"}}`, billing: "settlement_uncertain"},
		{name: "quota_pending", providerID: "42", data: `{"task":{"id":"42","status":"failed"}}`, billing: "reserved", quota: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			private := model.TaskPrivateData{H3AdmissionID: task.TaskID, UpstreamTaskID: tc.providerID, ResultURL: "https://fixture.invalid/failed-private-result"}
			// MySQL reports changed rows; JSON normalization can make two
			// adjacent inputs physically identical. A distinct fixture finish
			// timestamp keeps the successful one-row update verifiable.
			update := f.db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"private_data": private, "data": common.RawMessage(tc.data), "quota": tc.quota, "finish_time": finishTime + int64(index) + 1})
			require.NoError(t, update.Error)
			require.EqualValues(t, 1, update.RowsAffected)
			require.NoError(t, f.db.Model(&model.H3Admission{}).Where("id = ?", task.TaskID).Updates(map[string]any{"sending_at": 1, "billing_state": tc.billing}).Error)
			beforeGet := f.task(t, task.TaskID)
			for _, prefix := range []string{"/v1/tasks/", "/v1/video/generations/", "/v1/videos/"} {
				get := f.request(t, http.MethodGet, prefix+task.TaskID, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				assert.NotContains(t, get.Body.String(), "submission_receipt")
				assert.NotContains(t, get.Body.String(), "refund_confirmed")
				assert.NotContains(t, get.Body.String(), "resubmittable")
				if tc.confirmed {
					assert.Contains(t, get.Body.String(), `"provider_terminal_confirmed":true`)
				} else {
					assert.Contains(t, strings.ToLower(get.Body.String()), "unknown")
					assert.NotContains(t, get.Body.String(), "provider_terminal_confirmed")
					assert.NotContains(t, strings.ToLower(get.Body.String()), `"failed"`)
					assert.NotContains(t, get.Body.String(), "FAILURE")
					assert.NotContains(t, get.Body.String(), "failed-private-result")
				}
			}
			persisted := f.task(t, task.TaskID)
			assert.EqualValues(t, model.TaskStatusFailure, persisted.Status, "the conservative projection must not rewrite historical task data")
			assert.Equal(t, string(beforeGet.Data), string(persisted.Data))
		})
	}
}

func TestH3AdmissionResponsesObserversDoNotExposeUnprovenLocalFailure(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, failure := range []string{"untrusted_terminal", "proof_unavailable", "primary_load_unavailable"} {
			t.Run(fmt.Sprintf("stream_%t/%s", stream, failure), func(t *testing.T) {
				f := newH3RouterFixture(t, 1)
				f.postMode = "local_failure_malformed"
				const callback = "h3:observer-history-proof-unavailable"
				injected := false
				if failure != "untrusted_terminal" {
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
						posts, _ := f.counts()
						if posts == 0 {
							return
						}
						if (failure == "proof_unavailable" && tx.Statement.Table == "h3_receipt_task") || (failure == "primary_load_unavailable" && tx.Statement.Table == "tasks" && len(tx.Statement.Joins) == 0) {
							injected = true
							tx.AddError(fmt.Errorf("private observer database unavailable"))
						}
					}))
					t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
				}
				body := fmt.Sprintf(`{"model":"MiniMax-H3","input":"free bounded observer failure fixture","seconds":5,"stream":%t}`, stream)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer sk-h3routeradmin")
				response := httptest.NewRecorder()
				var writer http.ResponseWriter = response
				if stream && failure == "untrusted_terminal" {
					writer = &h3CancelAfterCreatedRecorder{ResponseRecorder: response, cancel: cancel}
				}
				f.engine.ServeHTTP(writer, request)
				f.mu.Lock()
				historyWriteError := f.historyWriteError
				f.mu.Unlock()
				require.NoError(t, historyWriteError, "the response observer fixture must first persist a genuine historical FAILURE")
				id := response.Header().Get("X-New-Api-Task-Id")
				require.NotEmpty(t, id)
				assert.NotContains(t, response.Body.String(), `"code":"task_failed"`)
				assert.NotContains(t, response.Body.String(), `"status":"failed"`)
				assert.NotContains(t, response.Body.String(), "response.failed")
				assert.NotContains(t, response.Body.String(), "private observer failure")
				assert.NotContains(t, response.Body.String(), "unrelated-private-provider")
				assert.NotContains(t, response.Body.String(), `"object":"video"`)
				if stream {
					assert.Contains(t, response.Body.String(), "response.created")
					assert.Contains(t, response.Body.String(), "resp_"+strings.TrimPrefix(id, "task_"))
				}
				if failure != "untrusted_terminal" {
					require.True(t, injected)
					if stream {
						assert.Contains(t, response.Body.String(), "response.incomplete")
					} else {
						assert.Contains(t, response.Body.String(), `"status":"queued"`)
						assert.Contains(t, response.Body.String(), `"object":"response"`)
						assert.Contains(t, response.Body.String(), "retrieval_path")
						captureH3ReceiptFixture(t, "responses_"+failure+"_remains_recoverable", response)
					}
					require.NoError(t, f.db.Callback().Query().Remove(callback))
				}
				get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				assert.Contains(t, get.Body.String(), `"status":"UNKNOWN"`)
				assert.EqualValues(t, model.TaskStatusFailure, f.task(t, id).Status, "the read-only observer must preserve the historical database row")
				posts, gets := f.counts()
				assert.Equal(t, 1, posts)
				assert.Zero(t, gets)
			})
		}
	}
}

func TestH3AdmissionConcurrentTerminalCASReleasesAndRefundsOneWinner(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	beforeWallet, beforeToken := f.balance(t)
	w := f.request(t, http.MethodPost, "/v1/videos", `{"model":"MiniMax-H3","prompt":"free terminal compare-and-swap fixture","seconds":5}`, "h3routeradmin")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	id := w.Header().Get("X-New-Api-Task-Id")
	first, second := f.task(t, id), f.task(t, id)
	from := first.Status
	start := make(chan struct{})
	type result struct {
		won bool
		err error
	}
	results := make(chan result, 2)
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for _, task := range []*model.Task{first, second} {
		go func(task *model.Task) {
			defer done.Done()
			task.Status = model.TaskStatusFailure
			task.Progress = "100%"
			task.FinishTime = time.Now().Unix()
			task.FailReason = "trusted synthetic terminal"
			ready.Done()
			<-start
			won, err := model.UpdateH3TaskWithStatus(context.Background(), task, from, true)
			if won && err == nil {
				service.RefundTaskQuota(context.Background(), task, "trusted synthetic terminal")
			}
			results <- result{won: won, err: err}
		}(task)
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	winners := 0
	for r := range results {
		require.NoError(t, r.err)
		if r.won {
			winners++
		}
	}
	assert.Equal(t, 1, winners)
	assert.Zero(t, f.occupied(t))
	wallet, token := f.balance(t)
	assert.Equal(t, beforeWallet, wallet)
	assert.Equal(t, beforeToken, token)
	var refunds int64
	require.NoError(t, f.logDB.Model(&model.Log{}).Where("type = ?", model.LogTypeRefund).Count(&refunds).Error)
	assert.EqualValues(t, 1, refunds)
	assert.Zero(t, f.task(t, id).Quota)
}

func TestH3AdmissionDefinitiveAuthRejectionFailsOnceAndRefunds(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/tasks/hailuo", "/v1/videos"} {
		for _, status := range []int{401, 403} {
			t.Run(fmt.Sprintf("%s-%d", path, status), func(t *testing.T) {
				f := newH3RouterFixture(t, 1)
				f.postMode = fmt.Sprintf("auth-%d", status)
				beforeWallet, beforeToken := f.balance(t)
				w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"free definitive authentication refusal","seconds":5}`, "h3routeradmin")
				require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), "h3_upstream_auth_rejected")
				var rejection map[string]any
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &rejection))
				assert.Equal(t, "failed", rejection["submission_state"])
				assert.Equal(t, true, rejection["refund_confirmed"])
				assert.Equal(t, false, rejection["retryable"])
				assert.Equal(t, false, rejection["resubmittable"])
				assert.NotContains(t, w.Body.String(), "synthetic provider credential detail")
				id := w.Header().Get("X-New-Api-Task-Id")
				require.NotEmpty(t, id)
				assert.Equal(t, id, rejection["id"])
				assert.Equal(t, id, rejection["task_id"])
				assert.EqualValues(t, model.TaskStatusFailure, f.task(t, id).Status)
				assert.Zero(t, f.task(t, id).Quota)
				assert.Zero(t, f.occupied(t))
				wallet, token := f.balance(t)
				assert.Equal(t, beforeWallet, wallet)
				assert.Equal(t, beforeToken, token)
				for range 2 {
					service.RunTaskPollingOnce(context.Background(), nil)
				}
				posts, gets := f.counts()
				assert.Equal(t, 1, posts, "definitive rejection never auto-retries another account")
				assert.Zero(t, gets)
				wallet, token = f.balance(t)
				assert.Equal(t, beforeWallet, wallet)
				assert.Equal(t, beforeToken, token)
			})
		}
	}
}

func TestH3AdmissionContradictoryAuthResponseRemainsUnknown(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/tasks/hailuo", "/v1/videos"} {
		for _, mode := range []string{"auth-401-mismatch", "auth-401-receipt", "auth-403-receipt"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				f := newH3RouterFixture(t, 1)
				f.postMode = mode
				beforeWallet, beforeToken := f.balance(t)
				w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"free contradictory receipt","seconds":5}`, "h3routeradmin")
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				id := w.Header().Get("X-New-Api-Task-Id")
				require.NotEmpty(t, id)
				assertH3UnknownEnvelope(t, w, id)
				assert.EqualValues(t, model.TaskStatusUnknown, f.task(t, id).Status)
				assert.Equal(t, 1, f.occupied(t))
				wallet, token := f.balance(t)
				assert.Less(t, wallet, beforeWallet)
				assert.Less(t, token, beforeToken)
				get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				service.RunTaskPollingOnce(context.Background(), nil)
				posts, _ := f.counts()
				assert.Equal(t, 1, posts)
				walletAfter, tokenAfter := f.balance(t)
				assert.Equal(t, wallet, walletAfter)
				assert.Equal(t, token, tokenAfter)
			})
		}
	}
}

func TestH3AdmissionSweepPreservesReservedAndSendingFences(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	constant.TaskTimeoutMinutes = 1
	for _, state := range []string{model.H3Reserved, model.H3Sending} {
		task := &model.Task{TaskID: "task_h3_sweep_" + state, Platform: "hailuo", UserId: 791, Group: "default", ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
		require.NoError(t, model.AcquireH3Admission(context.Background(), task, "synthetic-h3-account-0"))
		if state == model.H3Sending {
			require.NoError(t, model.SaveH3BillingReservation(context.Background(), task))
			require.NoError(t, model.MarkH3Sending(context.Background(), task.PrivateData.H3AdmissionID))
		}
	}
	service.RunTaskPollingOnce(context.Background(), nil)
	for _, state := range []string{model.H3Reserved, model.H3Sending} {
		var admission model.H3Admission
		require.NoError(t, f.db.Where("id = ?", "task_h3_sweep_"+state).First(&admission).Error)
		assert.Equal(t, state, admission.State, "SubmitTime=0 is pre-created and must not break a pending send fence")
		assert.Zero(t, f.task(t, "task_h3_sweep_"+state).SubmitTime)
	}
	// A provider receipt can be durable before the sending transaction is
	// finalised. Even an old timestamp must not let the sweep bypass that fence.
	sending := f.task(t, "task_h3_sweep_"+model.H3Sending)
	sending.PrivateData.UpstreamTaskID = "provider-receipt-still-sending"
	sending.SubmitTime = time.Now().Unix() - 120
	require.NoError(t, model.SaveH3Receipt(context.Background(), sending))
	service.RunTaskPollingOnce(context.Background(), nil)
	var fenced model.H3Admission
	require.NoError(t, f.db.Where("id = ?", sending.PrivateData.H3AdmissionID).First(&fenced).Error)
	assert.Equal(t, model.H3Sending, fenced.State)
	assert.Equal(t, sending.PrivateData.UpstreamTaskID, f.task(t, sending.TaskID).PrivateData.UpstreamTaskID)
	assert.Equal(t, 2, f.occupied(t))
	wallet, token := f.balance(t)
	assert.Equal(t, 100000000, wallet)
	assert.Equal(t, 100000000, token)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func assertH3UnknownEnvelope(t *testing.T, w *httptest.ResponseRecorder, id string) {
	t.Helper()
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, id, body["id"])
	assert.Equal(t, id, body["task_id"])
	assert.Equal(t, "unknown", body["submission_state"])
	assert.Equal(t, false, body["retryable"])
	assert.Equal(t, false, body["resubmittable"])
	assert.NotEqual(t, true, body["refund_confirmed"])
	errorBody, ok := body["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, service.H3UnknownCode, errorBody["code"])
}

func TestH3AdmissionUncertainReleaseOrRefundNeverClaimsFailed(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/tasks/hailuo", "/v1/videos"} {
		for _, mode := range []string{"slot_release", "wallet_refund", "refund_confirmation"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				f := newH3RouterFixture(t, 1)
				f.postMode = "auth-401"
				injected := false
				callback := "h3:uncertain-auth-compensation-" + mode
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					values, ok := tx.Statement.Dest.(map[string]any)
					if !ok {
						return
					}
					posts, _ := f.counts()
					if posts == 0 {
						return
					}
					fail := false
					switch mode {
					case "slot_release":
						_, hasOccupied := values["occupied"]
						fail = tx.Statement.Table == "h3_accounts" && hasOccupied
					case "wallet_refund":
						_, hasQuota := values["quota"]
						fail = tx.Statement.Table == "users" && hasQuota
					case "refund_confirmation":
						fail = tx.Statement.Table == "h3_admissions" && values["billing_state"] == "refunded"
					}
					if fail {
						injected = true
						tx.AddError(fmt.Errorf("synthetic compensation database write unavailable"))
					}
				}))
				t.Cleanup(func() { _ = f.db.Callback().Update().Remove(callback) })
				w := f.request(t, http.MethodPost, path, `{"model":"MiniMax-H3","prompt":"free uncertain compensation","seconds":5}`, "h3routeradmin")
				require.True(t, injected, "fixture must reach the intended DB write")
				id := w.Header().Get("X-New-Api-Task-Id")
				require.NotEmpty(t, id)
				assertH3UnknownEnvelope(t, w, id)
				assert.NotContains(t, w.Body.String(), "synthetic")
				assert.EqualValues(t, model.TaskStatusUnknown, f.task(t, id).Status)
				assert.Greater(t, f.task(t, id).Quota, 0, "uncertain compensation must not claim a zero settled balance")
				if mode == "slot_release" {
					assert.Equal(t, 1, f.occupied(t))
				} else {
					assert.Zero(t, f.occupied(t))
				}
				wallet, token := f.balance(t)
				get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				assert.Contains(t, get.Body.String(), "UNKNOWN")
				for range 2 {
					service.RunTaskPollingOnce(context.Background(), nil)
				}
				walletAfter, tokenAfter := f.balance(t)
				assert.Equal(t, wallet, walletAfter)
				assert.Equal(t, token, tokenAfter)
				posts, gets := f.counts()
				assert.Equal(t, 1, posts)
				assert.Zero(t, gets)
			})
		}
	}
}

func TestH3AdmissionCrashAfterRejectionCannotExposeFailedBeforeRefund(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	beforeWallet, beforeToken := f.balance(t)
	const amount = 777
	task := &model.Task{TaskID: "task_h3_crash_before_refund", Platform: "hailuo", UserId: 791, Group: "default", ChannelId: 790, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"}}
	require.NoError(t, model.AcquireH3Admission(context.Background(), task, "synthetic-h3-account-0"))
	// These are the same public wallet/token operations used by the live
	// billing session; no provider call or synthetic billing implementation.
	require.NoError(t, model.DecreaseUserQuota(791, amount, false))
	require.NoError(t, model.DecreaseTokenQuota(791, "h3routeradmin", amount))
	task.Quota = amount
	task.PrivateData.TokenId = 791
	task.PrivateData.BillingSource = "wallet"
	require.NoError(t, model.SaveH3BillingReservation(context.Background(), task))
	require.NoError(t, model.MarkH3Sending(context.Background(), task.PrivateData.H3AdmissionID))
	require.NoError(t, model.RejectH3Submission(context.Background(), task))
	// Simulate process termination at the durable boundary: no compensation
	// callback runs. A fresh load and every GET must remain visibly unknown.
	persisted := f.task(t, task.TaskID)
	assert.EqualValues(t, model.TaskStatusUnknown, persisted.Status)
	assert.Equal(t, amount, persisted.Quota)
	var admission model.H3Admission
	require.NoError(t, f.db.Where("id = ?", task.PrivateData.H3AdmissionID).First(&admission).Error)
	assert.Equal(t, model.H3Released, admission.State)
	assert.Equal(t, "refund_pending", admission.BillingState)
	assert.Zero(t, f.occupied(t))
	wallet, token := f.balance(t)
	assert.Equal(t, beforeWallet-amount, wallet)
	assert.Equal(t, beforeToken-amount, token)
	for _, prefix := range []string{"/v1/video/generations/", "/v1/tasks/", "/v1/videos/"} {
		get := f.request(t, http.MethodGet, prefix+task.TaskID, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		assert.Contains(t, get.Body.String(), task.TaskID)
		assert.NotContains(t, get.Body.String(), `"status":"failed"`)
		assert.NotContains(t, get.Body.String(), `"status":"FAILURE"`)
		assert.Contains(t, strings.ToLower(get.Body.String()), "unknown")
	}
	// Only after actual credits succeed may the confirmation transaction
	// atomically expose failed+quota0. Replaying that label never credits again.
	require.NoError(t, model.IncreaseUserQuota(791, amount, false))
	require.NoError(t, model.IncreaseTokenQuota(791, "h3routeradmin", amount))
	for range 2 {
		require.NoError(t, model.ConfirmH3Refund(context.Background(), persisted))
	}
	assert.EqualValues(t, model.TaskStatusFailure, f.task(t, task.TaskID).Status)
	assert.Zero(t, f.task(t, task.TaskID).Quota)
	wallet, token = f.balance(t)
	assert.Equal(t, beforeWallet, wallet)
	assert.Equal(t, beforeToken, token)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func TestH3AdmissionPreSendBuildFailurePreservesRefundTruth(t *testing.T) {
	for _, failRefund := range []bool{false, true} {
		t.Run(fmt.Sprintf("refund_fails_%t", failRefund), func(t *testing.T) {
			f := newH3RouterFixture(t, 1)
			beforeWallet, beforeToken := f.balance(t)
			injected := false
			debitObserved := false
			debitCallback := "h3:observe-real-pre-send-wallet-debit"
			require.NoError(t, f.db.Callback().Update().After("gorm:update").Register(debitCallback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || tx.Statement.Table != "users" || tx.Error != nil || tx.RowsAffected != 1 {
					return
				}
				debit, ok := values["quota"].(clause.Expr)
				if ok && strings.Contains(debit.SQL, "-") {
					debitObserved = true
				}
				if quota, ok := values["quota"].(int64); ok && quota < int64(beforeWallet) {
					debitObserved = true
				}
			}))
			t.Cleanup(func() { _ = f.db.Callback().Update().Remove(debitCallback) })
			if failRefund {
				callback := "h3:pre-send-refund-write-failure"
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					values, ok := tx.Statement.Dest.(map[string]any)
					if !ok || tx.Statement.Table != "users" || !debitObserved {
						return
					}
					// Funding first acquires SQLite's write lock with quota + 0,
					// both before debit and before refund. Fail the real absolute
					// restoration UPDATE, never that no-op locking statement.
					quota, assigned := values["quota"].(int64)
					if assigned && quota == int64(beforeWallet) {
						injected = true
						tx.AddError(fmt.Errorf("synthetic pre-send refund database unavailable"))
					}
				}))
				t.Cleanup(func() { _ = f.db.Callback().Update().Remove(callback) })
			}
			// The builtin accepts a structured file reference in validation;
			// the real inline body builder then detects its missing request file.
			body := `{"model":"MiniMax-H3","prompt":"free actual inline build failure","seconds":5,"input_reference":{"__fileRef":"request_file:input_reference","encoding":"dataUrl","mimeType":"image/png","maxBytes":1024}}`
			w := f.request(t, http.MethodPost, "/v1/videos", body, "h3routeradmin")
			require.True(t, debitObserved, "actual wallet pre-consumption must happen before the inline build failure")
			id := w.Header().Get("X-New-Api-Task-Id")
			require.NotEmpty(t, id, w.Body.String())
			posts, gets := f.counts()
			assert.Zero(t, posts)
			assert.Zero(t, gets)
			assert.Zero(t, f.occupied(t), "pre-send failure returns its unconsumed provider slot")
			if failRefund {
				require.True(t, injected, "the actual wallet compensation must reach its failed write")
				assertH3UnknownEnvelope(t, w, id)
				assert.EqualValues(t, model.TaskStatusUnknown, f.task(t, id).Status)
				wallet, _ := f.balance(t)
				assert.Less(t, wallet, beforeWallet)
				get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				assert.Contains(t, get.Body.String(), "UNKNOWN")
			} else {
				require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), "build_request_failed")
				assertH3NotSubmittedReceipt(t, w.Body.Bytes(), id, "refunded", false)
				captureH3ReceiptFixture(t, "post_build_failure_refunded", w)
				get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
				require.Equal(t, http.StatusOK, get.Code, get.Body.String())
				assertH3NotSubmittedReceipt(t, get.Body.Bytes(), id, "refunded", true)
				captureH3ReceiptFixture(t, "canonical_get_refunded", get)
				task := f.task(t, id)
				assert.EqualValues(t, model.TaskStatusFailure, task.Status)
				assert.Zero(t, task.Quota)
				assert.Equal(t, "Task submission could not be prepared", task.FailReason)
				assert.Equal(t, "build_request_failed", task.PrivateData.SubmissionErrorCode)
				assert.NotContains(t, strings.ToLower(task.FailReason), "authentication")
				wallet, token := f.balance(t)
				assert.Equal(t, beforeWallet, wallet)
				assert.Equal(t, beforeToken, token)
			}
		})
	}
}

func TestH3AdmissionUnsentProofDatabaseFailureRemainsUnknown(t *testing.T) {
	f := newH3RouterFixture(t, 1)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 0).Error)
	const callback = "h3:unavailable-unsent-durable-proof"
	injected := false
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "h3_receipt_task" && len(tx.Statement.Joins) > 0 {
			injected = true
			tx.AddError(fmt.Errorf("synthetic private ledger database unavailable"))
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	w := f.request(t, http.MethodPost, "/v1/video/generations", `{"model":"MiniMax-H3","prompt":"free proof failure","seconds":5}`, "h3routeradmin")
	require.True(t, injected)
	id := w.Header().Get("X-New-Api-Task-Id")
	assertH3UnknownEnvelope(t, w, id)
	captureH3ReceiptFixture(t, "post_unavailable_durable_proof_unknown", w)
	get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
	assert.Equal(t, http.StatusServiceUnavailable, get.Code, get.Body.String())
	assert.NotContains(t, get.Body.String(), "submission_receipt")
	assert.NotContains(t, get.Body.String(), "synthetic private")
	require.NoError(t, f.db.Callback().Query().Remove(callback))
	recovered := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, recovered.Code, recovered.Body.String())
	assertH3NotSubmittedReceipt(t, recovered.Body.Bytes(), id, "not_charged", true)
	posts, gets := f.counts()
	assert.Zero(t, posts)
	assert.Zero(t, gets)
}

func TestH3AdmissionZeroPriceSubscriptionRefundIsConfirmedBeforeReceipt(t *testing.T) {
	for _, failure := range []string{"none", "refund_confirmation", "subscription_record"} {
		t.Run(failure, func(t *testing.T) {
			f := newH3RouterFixture(t, 1)
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"MiniMax-H3":0}`))
			require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
			plan := &model.SubscriptionPlan{Id: 791, Title: "Free fixture subscription", TotalAmount: 1000, QuotaResetPeriod: "never"}
			require.NoError(t, f.db.Create(plan).Error)
			sub := &model.UserSubscription{Id: 791, UserId: 791, PlanId: plan.Id, AmountTotal: 1000, Status: "active", StartTime: time.Now().Unix(), EndTime: time.Now().Add(24 * time.Hour).Unix()}
			require.NoError(t, f.db.Create(sub).Error)
			injected := false
			if failure != "none" {
				const callback = "h3:zero-price-subscription-refund-confirmation"
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					values, ok := tx.Statement.Dest.(map[string]any)
					if failure == "refund_confirmation" && ok && tx.Statement.Table == "h3_admissions" && values["billing_state"] == "refunded" {
						injected = true
						tx.AddError(fmt.Errorf("synthetic private subscription refund confirmation unavailable"))
					}
					if record, ok := tx.Statement.Dest.(*model.SubscriptionPreConsumeRecord); failure == "subscription_record" && ok && tx.Statement.Table == "subscription_pre_consume_records" && record.Status == "refunded" {
						injected = true
						tx.AddError(fmt.Errorf("synthetic private subscription refund record write unavailable"))
					}
				}))
				t.Cleanup(func() { _ = f.db.Callback().Update().Remove(callback) })
			}
			body := `{"model":"MiniMax-H3","input":"free subscription minimum refund","prompt":"free subscription minimum refund","background":true,"seconds":5,"input_reference":{"__fileRef":"request_file:input_reference","encoding":"dataUrl","mimeType":"image/png","maxBytes":1024}}`
			w := f.request(t, http.MethodPost, "/v1/responses", body, "h3routeradmin")
			id := w.Header().Get("X-New-Api-Task-Id")
			require.NotEmpty(t, id, w.Body.String())
			var preConsume model.SubscriptionPreConsumeRecord
			require.NoError(t, f.db.Where("user_id = ?", 791).First(&preConsume).Error)
			assert.EqualValues(t, 1, preConsume.PreConsumed, "zero-price subscription still reserves its actual minimum")
			require.NoError(t, f.db.First(sub, sub.Id).Error)
			if failure == "subscription_record" {
				assert.Equal(t, "consumed", preConsume.Status)
				assert.EqualValues(t, 1, sub.AmountUsed, "a failed refund record write must roll back the subscription credit")
			} else {
				assert.Equal(t, "refunded", preConsume.Status)
				assert.Zero(t, sub.AmountUsed, "the subscription minimum must actually return")
			}
			get := f.request(t, http.MethodGet, "/v1/tasks/"+id, "", "h3routeradmin")
			require.Equal(t, http.StatusOK, get.Code, get.Body.String())
			if failure == "none" {
				require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
				assertH3NotSubmittedReceipt(t, w.Body.Bytes(), id, "refunded", false)
				assertH3NotSubmittedReceipt(t, get.Body.Bytes(), id, "refunded", true)
				assert.NotContains(t, w.Body.String(), "request_file", "Responses must preserve error-message sanitization")
				assert.Contains(t, w.Body.String(), "Task protocol request failed")
				captureH3ReceiptFixture(t, "responses_build_failure_refunded_sanitized", w)
			} else {
				require.True(t, injected)
				// Responses keeps its existing pending Response presentation for
				// uncertain durable outcomes; it must never claim safe resubmission.
				assert.NotContains(t, w.Body.String(), service.H3NotSentCode)
				var response map[string]any
				require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
				assert.Equal(t, "response", response["object"])
				assert.True(t, strings.HasPrefix(response["id"].(string), "resp_"))
				assert.NotContains(t, get.Body.String(), "submission_receipt")
				assert.Contains(t, get.Body.String(), "UNKNOWN")
				if failure == "subscription_record" {
					require.NoError(t, f.db.Callback().Update().Remove("h3:zero-price-subscription-refund-confirmation"))
					for range 2 {
						require.NoError(t, model.RefundSubscriptionPreConsume(preConsume.RequestId))
					}
					require.NoError(t, f.db.First(sub, sub.Id).Error)
					assert.Zero(t, sub.AmountUsed, "the original refund must succeed exactly once after the database recovers")
					require.NoError(t, f.db.First(&preConsume, preConsume.Id).Error)
					assert.Equal(t, "refunded", preConsume.Status)
					assert.NotContains(t, get.Body.String(), "submission_receipt", "funding repair alone cannot manufacture an admission confirmation")
				}
			}
			posts, gets := f.counts()
			assert.Zero(t, posts)
			assert.Zero(t, gets)
		})
	}
}

func TestH3AdmissionHistoricalIdentityConflictsLeaveTransactionUntouched(t *testing.T) {
	for _, conflict := range []string{"empty_key", "other_key", "orphan_private_id", "ledger_account", "ledger_channel", "ledger_id", "legacy_refilled_key", "legacy_uncertain_refilled_key"} {
		t.Run(conflict, func(t *testing.T) {
			f := newH3RouterFixture(t, 1)
			ctx := context.Background()
			require.NoError(t, model.SetH3AccountEnabled(ctx, "router-account-0", false))
			var channel model.Channel
			require.NoError(t, f.db.First(&channel, 790).Error)
			good := &model.Task{TaskID: "task_h3_good_original_key", Platform: "hailuo", UserId: 791, ChannelId: 790, Status: model.TaskStatusUnknown, Progress: "100%", Quota: 123,
				PrivateData: model.TaskPrivateData{Key: channel.Key, UpstreamTaskID: "provider-good"}, Properties: model.Properties{OriginModelName: "MiniMax-H3"}}
			require.NoError(t, f.db.Create(good).Error)
			bad := *good
			bad.ID, bad.TaskID = 0, "task_h3_bad_identity"
			switch conflict {
			case "empty_key":
				bad.PrivateData.Key = ""
			case "other_key":
				bad.PrivateData.Key = "synthetic-other-provider-account"
			case "orphan_private_id":
				bad.PrivateData.H3AdmissionID = "orphan-ledger"
			default:
				bad.PrivateData.H3AdmissionID = bad.TaskID
			}
			require.NoError(t, f.db.Create(&bad).Error)
			if strings.HasPrefix(conflict, "ledger_") || strings.Contains(conflict, "refilled") {
				ledger := model.H3Admission{ID: bad.TaskID, TaskDBID: bad.ID, AccountID: "router-account-0", ChannelID: 790, State: model.H3Unknown, BillingState: "reserved", SendingAt: 1}
				switch conflict {
				case "ledger_account":
					ledger.AccountID = "other-account"
				case "ledger_channel":
					ledger.ChannelID = 999
				case "ledger_id":
					ledger.ID = "unrelated-ledger-id"
				case "legacy_refilled_key":
					ledger.BillingState = "legacy"
				case "legacy_uncertain_refilled_key":
					ledger.BillingState = "legacy_uncertain"
				}
				require.NoError(t, f.db.Create(&ledger).Error)
			}
			var beforeTasks, afterTasks []model.Task
			var beforeLedgers, afterLedgers []model.H3Admission
			var beforeAccount, afterAccount model.H3Account
			require.NoError(t, f.db.Order("id").Find(&beforeTasks).Error)
			require.NoError(t, f.db.Order("id").Find(&beforeLedgers).Error)
			require.NoError(t, f.db.First(&beforeAccount).Error)
			wallet, token := f.balance(t)
			assert.ErrorIs(t, model.ReconcileH3Account(ctx, "router-account-0"), model.ErrH3AdmissionUnavailable)
			require.NoError(t, f.db.Order("id").Find(&afterTasks).Error)
			require.NoError(t, f.db.Order("id").Find(&afterLedgers).Error)
			require.NoError(t, f.db.First(&afterAccount).Error)
			assert.Equal(t, beforeTasks, afterTasks, "a later identity conflict rolls back earlier imports and normalization")
			assert.Equal(t, beforeLedgers, afterLedgers)
			assert.Equal(t, beforeAccount, afterAccount)
			afterWallet, afterToken := f.balance(t)
			assert.Equal(t, wallet, afterWallet)
			assert.Equal(t, token, afterToken)
			posts, gets := f.counts()
			assert.Zero(t, posts)
			assert.Zero(t, gets)
		})
	}
}
