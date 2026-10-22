package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

func TestExecuteTaskSubmissionFreezesDeclaredClientSource(t *testing.T) {
	for _, scenario := range []struct {
		name, expected string
		values         []string
	}{
		{"legacy", "", nil},
		{"desktop", "desktop", []string{"desktop"}},
		{"different case", "", []string{"Desktop"}},
		{"whitespace", "", []string{" desktop "}},
		{"combined", "", []string{"desktop,desktop"}},
		{"repeated", "", []string{"desktop", "desktop"}},
		{"unknown", "", []string{"private-client-value"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database := setupTaskSubmissionDatabase(t, true, &events)
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
			c := taskSubmissionTestContext()
			for _, value := range scenario.values {
				c.Request.Header.Add("X-Yunshu-Client", value)
			}
			c.Request.Header.Set("X-Preserve-Header", "unchanged")
			outcome, taskErr := executeTaskSubmissionWith(c, taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events}), func(ctx *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				assert.Empty(t, ctx.Request.Header.Values("X-Yunshu-Client"), "diagnostic declarations never reach upstream submission")
				assert.Equal(t, "unchanged", ctx.GetHeader("X-Preserve-Header"))
				return &relay.TaskSubmitResult{UpstreamTaskID: "private-upstream-id", Platform: "hailuo"}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			require.NotNil(t, outcome.Task.PrivateData.Execution)
			assert.Equal(t, scenario.expected, outcome.Task.PrivateData.Execution.ClientSource)
			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			require.NotNil(t, stored.PrivateData.Execution)
			assert.Equal(t, scenario.expected, stored.PrivateData.Execution.ClientSource)
			publicJSON, err := common.Marshal(stored)
			require.NoError(t, err)
			assert.NotContains(t, string(publicJSON), "client_source")
			query := taskSubmissionTestContext()
			query.Request.Method = http.MethodGet
			query.Request.Header.Set("X-Yunshu-Client", "desktop")
			service.CaptureTaskClientSource(query)
			assert.Empty(t, service.TaskExecutionSnapshotFromContext(query).ClientSource)
			assert.Empty(t, query.Request.Header.Get("X-Yunshu-Client"))
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, scenario.expected, stored.PrivateData.Execution.ClientSource, "later queries do not rewrite stored provenance")
		})
	}
}

func TestVideoUsageHTTPSourceAndDurationContract(t *testing.T) {
	owner, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}, &model.Token{}, &model.Channel{}, &model.Ability{}))
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	err := model.InitLogDB()
	common.IsMasterNode = previousMaster
	require.NoError(t, err)
	previousMemory, previousConsume := common.MemoryCacheEnabled, common.LogConsumeEnabled
	common.MemoryCacheEnabled, common.LogConsumeEnabled = false, false
	t.Cleanup(func() { common.MemoryCacheEnabled, common.LogConsumeEnabled = previousMemory, previousConsume })
	_, adminJWT := createAccessTokenTestSession(t, owner.Id, "video-usage-http-contract")
	relayKey := strings.Repeat("v", 48)
	require.NoError(t, model.DB.Create(&model.Token{UserId: owner.Id, Key: relayKey, Status: common.TokenStatusEnabled, Name: "local-video-contract", UnlimitedQuota: true, ExpiredTime: -1}).Error)
	engine := gin.New()
	engine.Use(middleware.RequestId())
	engine.POST("/v1/videos", middleware.TokenAuth(), func(c *gin.Context) {
		events := []string{}
		info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
		info.UserId, info.OriginModelName, info.Action = c.GetInt("id"), "MiniMax-H3", constant.TaskActionTextToVideo
		info.PublicTaskID = model.GenerateTaskID()
		outcome, taskErr := executeTaskSubmissionWith(c, info, func(ctx *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			assert.Empty(t, ctx.GetHeader("X-Yunshu-Client"))
			return &relay.TaskSubmitResult{UpstreamTaskID: "private-local-upstream", Platform: "hailuo"}, nil
		})
		if !assert.Nil(t, taskErr) {
			respondTaskSubmissionError(c, taskErr)
			return
		}
		presentTaskSubmission(c, outcome)
	})
	engine.GET("/api/video-usage", middleware.DisableCache(), middleware.AdminAuth(), GetVideoUsage)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	for _, source := range []string{"desktop", ""} {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/videos", strings.NewReader(`{"model":"MiniMax-H3","prompt":"synthetic local contract"}`))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer sk-"+relayKey)
		request.Header.Set("Content-Type", "application/json")
		if source != "" {
			request.Header.Set("X-Yunshu-Client", source)
		}
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, string(body))
		assert.NotContains(t, string(body), "client_source")
	}
	var tasks []model.Task
	require.NoError(t, model.DB.Order("id").Find(&tasks).Error)
	require.Len(t, tasks, 2)
	assert.Equal(t, "desktop", tasks[0].PrivateData.Execution.ClientSource)
	assert.Empty(t, tasks[1].PrivateData.Execution.ClientSource)
	now := time.Now().Unix()
	for i, task := range tasks {
		submit, start := now-100, now-90
		if i == 1 {
			submit, start = now-60, 0
		}
		require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"submit_time": submit, "start_time": start, "finish_time": now - 20, "status": model.TaskStatusSuccess}).Error)
	}
	for _, sourceFilter := range []string{"all", "desktop"} {
		path := fmt.Sprintf("/api/video-usage?start_timestamp=%d&end_timestamp=%d&client_source=%s", now-3600, now, sourceFilter)
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+adminJWT)
		request.Header.Set("X-Yunshu-Client", "desktop")
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, string(body))
		var envelope struct {
			Success bool                     `json:"success"`
			Data    model.VideoUsageResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(body, &envelope))
		require.True(t, envelope.Success)
		assert.Equal(t, 2, envelope.Data.SourceCoverage.Total)
		assert.Equal(t, 1, envelope.Data.SourceCoverage.Desktop)
		assert.Equal(t, 1, envelope.Data.SourceCoverage.Unknown)
		wantTasks, wantP50 := 2, float64(40)
		if sourceFilter == "desktop" {
			wantTasks, wantP50 = 1, 80
		}
		assert.Equal(t, wantTasks, envelope.Data.Summary.History.TaskCount)
		require.NotNil(t, envelope.Data.Summary.History.Turnaround.P50Seconds)
		assert.Equal(t, wantP50, *envelope.Data.Summary.History.Turnaround.P50Seconds)
		assert.NotContains(t, string(body), "private-local-upstream")
	}
	var unchanged model.Task
	require.NoError(t, model.DB.First(&unchanged, tasks[1].ID).Error)
	assert.Empty(t, unchanged.PrivateData.Execution.ClientSource, "GET declarations must not relabel legacy tasks")
	var after model.User
	require.NoError(t, model.DB.First(&after, owner.Id).Error)
	assert.Equal(t, owner.Quota, after.Quota)
	assert.Equal(t, owner.UsedQuota, after.UsedQuota)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{}, &model.Log{})
			previousDB, previousLogDB, previousErrorLog := model.DB, model.LOG_DB, constant.ErrorLogEnabled
			model.DB, model.LOG_DB, constant.ErrorLogEnabled = database, database, true
			t.Cleanup(func() { model.DB, model.LOG_DB, constant.ErrorLogEnabled = previousDB, previousLogDB, previousErrorLog })
			previousRedis := common.RedisEnabled
			common.RedisEnabled = false
			t.Cleanup(func() { common.RedisEnabled = previousRedis })
			require.NoError(t, database.Create(&model.User{Id: 1, Username: "diagnostic-user", Status: common.UserStatusEnabled}).Error)
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)
			if tc.immediate != nil && tc.immediate.Status == model.TaskStatusFailure {
				upstream = []byte(`{"status":"failed","error":{"code":"content_policy_violation","message":"rejected"}}`)
			}

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			if tc.immediate != nil && tc.immediate.Status == model.TaskStatusFailure {
				var logs []model.Log
				require.NoError(t, database.Where("type = ?", model.LogTypeError).Find(&logs).Error)
				require.Len(t, logs, 1)
				other, err := common.StrToMap(logs[0].Other)
				require.NoError(t, err)
				assert.Equal(t, "gateway_task_mapping", other["status_code_origin"])
				assert.Equal(t, "content_policy_violation", other["provider_error_code"])
				assert.NotContains(t, other, "upstream_http_status", "submit result does not carry the actual response status")
				assert.Equal(t, float64(http.StatusBadGateway), other["status_code"])
			}
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
	previousRetry := common.RetryTimes
	common.RetryTimes = 2
	t.Cleanup(func() { common.RetryTimes = previousRetry })
	attempts := 0
	outcome, taskErr := executeTaskSubmissionWith(c, taskSubmissionRelayInfo(nil), func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		attempts++
		return nil, &dto.TaskError{Code: "submit_stream_failed", StatusCode: http.StatusBadGateway, LocalError: true, NoRetry: true}
	})
	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, 1, attempts, "an accepted provider operation must not be submitted again")
}

func TestExecuteTaskSubmissionPreservesBillingRejectionsAndUpstreamRetries(t *testing.T) {
	require.NoError(t, i18n.Init())
	allowPrivateTaskMediaTest(t)
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	oldErrorLog, oldRetry, oldQuotaPerUnit := constant.ErrorLogEnabled, common.RetryTimes, common.QuotaPerUnit
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	// Initialize the host's dialect-specific column names without migrating or
	// opening another database. Token rejection diagnostics use those names.
	t.Setenv("LOG_SQL_DSN", "")
	wasMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = wasMaster
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	constant.ErrorLogEnabled, common.RetryTimes, common.QuotaPerUnit = true, 2, 500000
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		common.IsMasterNode = wasMaster
		model.LOG_DB = oldLogDB
		common.SetLogDatabaseType(oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		constant.ErrorLogEnabled, common.RetryTimes, common.QuotaPerUnit = oldErrorLog, oldRetry, oldQuotaPerUnit
	})
	withTieredBillingConfig(t,
		map[string]string{"rejection-model": "tiered_expr", "MiniMax-H3": "tiered_expr"},
		map[string]string{"rejection-model": `tier("request", u("units") * 0.01)`, "MiniMax-H3": `tier("request", u("seconds") * 0.01)`})
	const source = `
export const meta={apiVersion:1,key:"billing-rejection",name:"Billing rejection",version:"1.0.0",author:{name:"Test"},models:["rejection-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count",description:{en:"Task operation unit price"}}},routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native={
  decode:function(ctx){return {kind:"submit",model:ctx.body.value.model,requestBody:ctx.body.value};},
  created:function(ctx,task){return {id:task.task_id};},
  ERROR_HOOK
};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",body:ctx.requestBody};}
export function extractUsage(){return {units:4};}
export function parseSubmitResponse(ctx,response){return {taskId:"private-upstream-task",taskData:response.body,immediate:{status:"SUCCESS"}};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function buildQueryRequest(){return {};}
export function parseTaskResult(){return {status:"UNKNOWN"};}
`
	const errorHook = `error:function(ctx,error){return {code:error.code,message:error.message,status:error.httpStatus,retryable:error.retryable,keys:Object.keys(error).sort()};}`
	withoutCause := kittypes.InitOpenAIError(kittypes.ErrorCodeInsufficientUserQuota, http.StatusForbidden, kittypes.ErrOptionWithSkipRetry(), kittypes.ErrOptionWithNoRecordErrorLog())
	withoutCause.Err = nil
	converted := service.TaskErrorFromAPIError(withoutCause)
	assert.Equal(t, withoutCause.Error(), converted.Message)
	assert.Same(t, withoutCause, taskSubmissionAPIError(converted))
	assert.Nil(t, service.TaskErrorFromAPIError(nil))
	lookupFailure := kittypes.NewError(errors.New("wallet lookup failed"), kittypes.ErrorCodeQueryDataError, kittypes.ErrOptionWithSkipRetry())
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}, decideTaskRetry(taskSubmissionTestContext(), service.TaskErrorFromAPIError(lookupFailure), 2), "a local 500 must honor the billing retry opt-out before status rules")
	for index, tc := range []struct {
		name, presenter, upstreamBody string
		wallet, token, upstreamStatus int
		wantAttempts, wantRequests    int
		wantLogs                      int64
		wantCode, wantTaskCode        string
		legacyH3, accepted            bool
	}{
		{name: "insufficient wallet", wallet: 1, token: 100000, upstreamStatus: 503, wantAttempts: 1, wantCode: "insufficient_user_quota", wantTaskCode: "insufficient_user_quota"},
		{name: "insufficient wallet without native error hook", presenter: "missing", wallet: 1, token: 100000, upstreamStatus: 503, wantAttempts: 1, wantCode: "insufficient_user_quota", wantTaskCode: "insufficient_user_quota"},
		{name: "insufficient wallet with throwing native error hook", presenter: "throwing", wallet: 1, token: 100000, upstreamStatus: 503, wantAttempts: 1, wantCode: "insufficient_user_quota", wantTaskCode: "insufficient_user_quota"},
		{name: "insufficient token quota", wallet: 100000, token: 1, upstreamStatus: 503, wantAttempts: 1, wantCode: "permission_denied", wantTaskCode: "pre_consume_token_quota_failed"},
		{name: "upstream unavailable", wallet: 100000, token: 100000, upstreamStatus: 503, wantAttempts: 3, wantRequests: 3, wantLogs: 3, wantCode: "server_error", wantTaskCode: "fail_to_fetch_task"},
		{name: "upstream forbidden", wallet: 100000, token: 100000, upstreamStatus: 403, wantAttempts: 3, wantRequests: 3, wantLogs: 3, wantCode: "permission_denied", wantTaskCode: "fail_to_fetch_task"},
		{name: "forbidden with misleading wallet message", wallet: 100000, token: 100000, upstreamStatus: 403, upstreamBody: `{"message":"insufficient user quota, please top up"}`, wantAttempts: 3, wantRequests: 3, wantLogs: 3, wantCode: "permission_denied", wantTaskCode: "fail_to_fetch_task"},
		{name: "upstream spoofed wallet code and local flag", wallet: 100000, token: 100000, upstreamStatus: 403, upstreamBody: `{"code":"insufficient_user_quota","LocalError":true,"local_error":true,"message":"insufficient user quota"}`, wantAttempts: 3, wantRequests: 3, wantLogs: 3, wantCode: "permission_denied", wantTaskCode: "fail_to_fetch_task"},
		{name: "channel quota rejection", wallet: 100000, token: 100000, upstreamStatus: 403, upstreamBody: `{"error":{"code":"insufficient_quota","type":"insufficient_user_quota","message":"channel balance insufficient"}}`, wantAttempts: 3, wantRequests: 3, wantLogs: 3, wantCode: "permission_denied", wantTaskCode: "fail_to_fetch_task"},
		{name: "accepted operation with final token reserve failure", wallet: 100000, token: 30000, upstreamStatus: 202, upstreamBody: `{"usage":{"units":8}}`, wantAttempts: 1, wantRequests: 1, wantLogs: 1, wantCode: "insufficient_user_quota", wantTaskCode: "insufficient_user_quota", accepted: true},
		// The built-in Hailuo plugin has no native route. This legacy URL must
		// keep reaching RelayTask and its existing raw TaskError response.
		{name: "legacy H3 insufficient wallet", wallet: 1, token: 100000, upstreamStatus: 503, wantCode: "insufficient_user_quota", legacyH3: true},
		{name: "legacy H3 upstream spoofed wallet code", wallet: 100000, token: 100000, upstreamStatus: 403, upstreamBody: `{"code":"insufficient_user_quota","LocalError":true,"message":"provider balance insufficient"}`, wantRequests: 1, wantLogs: 1, wantCode: "fail_to_fetch_task", legacyH3: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.RetryTimes = 2
			if tc.legacyH3 {
				common.RetryTimes = 0
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.upstreamStatus)
				body := tc.upstreamBody
				if body == "" {
					body = `{"error":"provider permission denied"}`
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			user := model.User{Username: fmt.Sprintf("rejection_user_%d", index), AffCode: fmt.Sprintf("reject_aff_%d", index), Quota: tc.wallet}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("fixture-token-%d", index), Name: "fixture token", RemainQuota: tc.token}
			require.NoError(t, db.Create(&token).Error)
			channel := model.Channel{Name: "fixture provider", Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "private-fixture-channel-key", BaseURL: common.GetPointer(server.URL)}
			modelName, routePath := "rejection-model", "/vendor/jobs"
			if tc.legacyH3 {
				modelName, routePath = "MiniMax-H3", "/v1/video/generations"
				channel.Type = constant.ChannelTypeMiniMax
			} else {
				channel.SetSetting(kitdto.ChannelSettings{TaskPluginKey: "billing-rejection"})
			}
			require.NoError(t, db.Create(&channel).Error)
			router := gin.New()
			seed := func(c *gin.Context) {
				c.Set(common.RequestIdKey, "billing-rejection-request")
				common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
				common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
				common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUserQuota, tc.wallet)
				common.SetContextKey(c, constant.ContextKeyUserSetting, kitdto.UserSetting{BillingPreference: "wallet_only"})
				common.SetContextKey(c, constant.ContextKeyTokenId, token.Id)
				common.SetContextKey(c, constant.ContextKeyTokenKey, token.Key)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
				require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, modelName))
				c.Next()
			}
			var info *relaycommon.RelayInfo
			var requestContext *gin.Context
			var taskErr *dto.TaskError
			attempts := 0
			if tc.legacyH3 {
				plugin, ok := relay.ResolveTaskPluginForPlatform(pluginruntime.DefaultRegistry.Generation(), "35")
				require.True(t, ok)
				require.Equal(t, "hailuo", plugin.Meta.Key)
				require.Empty(t, plugin.Meta.Routes)
				router.POST(routePath, middleware.PinTaskPluginEndpoint(), middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()), middleware.PrepareTaskPluginEndpoint(), seed, func(c *gin.Context) {
					_, endpoint := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
					_, native := c.Get(pluginruntime.ContextKeyPinnedRoute)
					assert.False(t, endpoint)
					assert.False(t, native)
					RelayTaskPluginEndpoint(c, RelayTask)
				})
			} else {
				hook := errorHook
				switch tc.presenter {
				case "missing":
					hook = ""
				case "throwing":
					hook = `error:function(){throw new Error("private renderer failure");}`
				}
				plugin, err := pluginruntime.CompilePlugin(strings.Replace(source, "ERROR_HOOK", hook, 1), pluginruntime.Options{})
				require.NoError(t, err)
				router.POST(routePath, func(c *gin.Context) {
					c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
					c.Next()
				}, seed, middleware.PrepareTaskPluginRoute(), func(c *gin.Context) {
					requestContext = c
					info = taskSubmissionRelayInfo(nil)
					info.UserId, info.OriginModelName = user.Id, modelName
					info.UserGroup, info.TokenGroup = "default", "default"
					info.TokenId, info.TokenKey = token.Id, token.Key
					info.UserSetting.BillingPreference = "wallet_only"
					info.LockedChannel = &channel
					var outcome *taskSubmissionOutcome
					outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
						attempts++
						return relay.RelayTaskSubmit(c, info)
					})
					require.Nil(t, outcome)
					require.NotNil(t, taskErr)
					respondTaskSubmissionError(c, taskErr)
				})
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(fmt.Sprintf(`{"model":%q,"prompt":"a lighthouse","duration":5,"size":"768P","credential":"private-request-secret"}`, modelName)))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			wantStatus := http.StatusForbidden
			if tc.wantRequests > 0 && !tc.accepted {
				wantStatus = tc.upstreamStatus
			}
			assert.Equal(t, wantStatus, recorder.Code, recorder.Body.String())
			assert.EqualValues(t, tc.wantRequests, requests.Load())
			var response map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.wantCode, response["code"])
			for _, private := range []string{token.Key, channel.Key, "private-request-secret", "private renderer failure", "private-upstream-task"} {
				assert.NotContains(t, recorder.Body.String(), private)
			}
			assert.NotContains(t, response, "LocalError")
			assert.NotContains(t, response, "Error")
			assert.NotContains(t, response, "NoRetry")
			if !tc.legacyH3 {
				assert.Equal(t, tc.wantAttempts, attempts)
				assert.Equal(t, tc.wantTaskCode, taskErr.Code)
				if tc.presenter == "" {
					assert.Equal(t, tc.upstreamStatus >= 500 && tc.wantRequests > 0, response["retryable"])
					assert.Equal(t, []any{"code", "httpStatus", "message", "requestId", "retryable"}, response["keys"])
					assert.EqualValues(t, wantStatus, response["status"])
				} else {
					assert.Nil(t, response["data"])
					assert.NotContains(t, response, "retryable")
				}
				if tc.wantRequests == 0 {
					assert.True(t, taskErr.LocalError)
					apiErr := taskSubmissionAPIError(taskErr)
					assert.Equal(t, kittypes.ErrorCode(tc.wantTaskCode), apiErr.GetErrorCode())
					assert.True(t, kittypes.IsSkipRetryError(apiErr))
					assert.False(t, kittypes.IsRecordErrorLog(apiErr))
					assert.Nil(t, info.Billing, "a rejected pre-consume must not create a billing session")
					events := service.RequestPolicy(requestContext).Events()
					require.Len(t, events, 3)
					assert.Equal(t, "local_rejection", events[1].Decision.Reason)
					assert.Equal(t, "local", events[1].ErrorSource)
					assert.Equal(t, "non_retryable_error", events[2].Decision.Reason)
					assert.Equal(t, "unchanged", events[2].Health)
				} else if tc.accepted {
					assert.True(t, taskErr.LocalError)
					require.NotNil(t, info.Billing, "the provider was accepted after an actual pre-consume")
					assert.Equal(t, 20000, info.FinalPreConsumedQuota)
					assert.False(t, info.Billing.NeedsRefund(), "the failed final reserve must refund once")
				} else {
					assert.False(t, taskErr.LocalError, "a provider rejection remains a channel failure")
				}
			}
			require.Eventually(t, func() bool {
				var updated model.User
				var updatedToken model.Token
				return db.First(&updated, user.Id).Error == nil && updated.Quota == tc.wallet && updated.UsedQuota == 0 &&
					db.First(&updatedToken, token.Id).Error == nil && updatedToken.RemainQuota == tc.token && updatedToken.UsedQuota == 0
			}, time.Second, time.Millisecond, "failed submissions must leave wallet and token quotas unchanged")
			var logCount, taskCount int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&logCount).Error)
			require.NoError(t, db.Model(&model.Task{}).Where("user_id = ?", user.Id).Count(&taskCount).Error)
			assert.Equal(t, tc.wantLogs, logCount)
			assert.Zero(t, taskCount)
		})
	}
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}
