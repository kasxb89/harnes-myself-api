package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaOperationsDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("MEDIA_OPERATIONS_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("owned local database fixture not configured")
			}
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB, _ := newAuditTestDatabase(t, kind, dsn)
			typ := common.DatabaseTypeSQLite
			if kind == "mysql" {
				typ = common.DatabaseTypeMySQL
			} else if kind == "postgres" {
				typ = common.DatabaseTypePostgreSQL
			}
			useLogExportDatabases(t, db, logDB, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, status integer)").Error)
			require.NoError(t, db.Exec("INSERT INTO channels (id,status) VALUES (1,1),(2,2)").Error)
			now := time.Now().Unix()
			filter := model.MediaOperationsFilter{Start: now - 3600, End: now, MediaKind: "all"}
			catalog := model.MediaOperationsCatalog{Models: map[string]string{"MiniMax-H3": "video", "Image-model": "image", "Configured-only": "image", "Disabled-channel": "image"}, Platforms: map[string]string{"hailuo": "video"}, SubmitPaths: map[string]string{"/v1/videos": "video"}}
			require.NoError(t, db.Create(&[]model.Ability{{Group: "default", Model: "MiniMax-H3", ChannelId: 1, Enabled: true}, {Group: "default", Model: "Configured-only", ChannelId: 1, Enabled: true}, {Group: "default", Model: "Disabled-channel", ChannelId: 2, Enabled: true}}).Error)
			tasks := []model.Task{
				{UserId: 1, Status: model.TaskStatusInProgress, SubmitTime: now - 90000, Properties: model.Properties{OriginModelName: "MiniMax-H3", Input: "secret-prompt"}, PrivateData: model.TaskPrivateData{Key: "secret-key", PollFailures: 3}},
				{UserId: 1, Status: model.TaskStatusUnknown, SubmitTime: now - 60, Properties: model.Properties{OriginModelName: "MiniMax-H3"}},
				{UserId: 1, Status: model.TaskStatusFailure, SubmitTime: now - 50, Properties: model.Properties{OriginModelName: "MiniMax-H3"}},
				{Platform: "hailuo", UserId: 2, Status: model.TaskStatusSuccess, SubmitTime: now - 80000, Properties: model.Properties{OriginModelName: "MiniMax-H3"}},
				{Platform: "hailuo", UserId: 2, Status: model.TaskStatusSuccess, SubmitTime: now - 70, Properties: model.Properties{OriginModelName: "Removed-old-model"}},
				{Platform: "hailuo", UserId: 2, Status: model.TaskStatusQueued, SubmitTime: now - 80000, Properties: model.Properties{UpstreamModelName: "Do-not-infer-original"}},
				{Platform: "suno", UserId: 3, Status: model.TaskStatusInProgress, SubmitTime: now - 20, Properties: model.Properties{OriginModelName: "audio-only"}},
				{Platform: "responses", UserId: 3, Status: model.TaskStatusUnknown, SubmitTime: now - 20, Action: "generate", Properties: model.Properties{OriginModelName: "ordinary-text"}},
			}
			require.NoError(t, db.Create(&tasks).Error)
			requestID := "same-request"
			policy1 := `{"request_path":"/v1/videos","error_code":"model_not_found","admin_info":{"request_policy":[{"attempt":1,"channel_id":9},{"attempt":1,"channel_id":9}]}}`
			policy2 := `{"request_path":"/v1/videos","error_stage":"task_terminal","error_code":"task_failed","provider_error_code":"resource_exhausted","status_code":502,"admin_info":{"request_policy":[{"attempt":1,"channel_id":9},{"attempt":2,"channel_id":10},{"attempt":2,"channel_id":10}]}}`
			logs := []model.Log{
				{UserId: 1, CreatedAt: now - 30, Type: model.LogTypeError, RequestId: requestID, ModelName: "MiniMax-H3", Other: policy1, Content: "sensitive error URL/secret; 当前没有可用的生成资源"},
				{UserId: 1, CreatedAt: now - 20, Type: model.LogTypeError, RequestId: requestID, ModelName: "MiniMax-H3", Other: policy2},
				{UserId: 1, CreatedAt: now - 10, Type: model.LogTypeConsume, RequestId: requestID, ModelName: "MiniMax-H3", Other: policy2},
				{UserId: 2, CreatedAt: now - 20, Type: model.LogTypeError, RequestId: requestID, ModelName: "MiniMax-H3", Other: `{"error_code":"invalid_request_error","status_code":502}`, Content: "模型不存在，被内容策略拒绝"},
				{UserId: 1, CreatedAt: now - 20, Type: model.LogTypeError, ModelName: "MiniMax-H3", Other: `{"error_code":"task_failed"}`},
				{UserId: 1, CreatedAt: now - 10, Type: model.LogTypeClientError, RequestId: "diagnostic-event-id", ModelName: "MiniMax-H3", Content: "TRANSPORT_TIMEOUT", Other: `{"source":"client","tool":"generate_video","stage":"query","request_id":"same-request"}`},
				{UserId: 1, CreatedAt: now - 10, Type: model.LogTypeClientError, RequestId: "diagnostic-other-id", ModelName: "MiniMax-H3", Content: "LOCAL_SAVE_FAILED", Other: `{"source":"client","tool":"generate_video","stage":"local-save"}`},
				{UserId: 3, CreatedAt: now - 10, Type: model.LogTypeError, RequestId: "text-only", ModelName: "ordinary-text", Other: strings.ReplaceAll(policy2, "/v1/videos", "/v1/chat/completions")},
			}
			require.NoError(t, logDB.Create(&logs).Error)
			// Client diagnostic records belong to aggregate operations views, never
			// legacy request lists, exports or token-based log reads.
			for _, legacyType := range []int{model.LogTypeUnknown, model.LogTypeClientError} {
				legacyFilter := model.LogFilter{Type: legacyType}
				wantAll, wantOwn := int64(6), int64(4)
				if legacyType == model.LogTypeClientError {
					wantAll, wantOwn = 0, 0
				}
				_, allCount, err := model.GetAllLogs(legacyFilter, 0, 100)
				require.NoError(t, err)
				assert.Equal(t, wantAll, allCount)
				_, ownCount, err := model.GetUserLogs(1, legacyFilter, 0, 100)
				require.NoError(t, err)
				assert.Equal(t, wantOwn, ownCount)
				var exportedAll, exportedOwn int64
				require.NoError(t, model.StreamAllLogs(context.Background(), legacyFilter, common.RoleRootUser, func(row *model.Log) error {
					assert.NotEqual(t, model.LogTypeClientError, row.Type)
					exportedAll++
					return nil
				}))
				require.NoError(t, model.StreamUserLogs(context.Background(), 1, legacyFilter, func(row *model.Log) error {
					assert.NotEqual(t, model.LogTypeClientError, row.Type)
					exportedOwn++
					return nil
				}))
				assert.Equal(t, wantAll, exportedAll)
				assert.Equal(t, wantOwn, exportedOwn)
			}
			tokenLogs, err := model.GetLogByTokenId(0)
			require.NoError(t, err)
			assert.Len(t, tokenLogs, 6)
			result, err := model.GetMediaOperations(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.False(t, result.ObservationComplete)
			assert.Equal(t, 1, result.Summary.InProgress)
			assert.Equal(t, 1, result.Summary.Unknown)
			assert.Equal(t, 1, result.Summary.Queued)
			assert.Equal(t, 3, result.HistorySummary.AcceptedTasks)
			assert.Equal(t, 4, result.HistorySummary.ErrorEvents)
			assert.Equal(t, 2, result.HistorySummary.ErrorRequests)
			assert.Equal(t, 1, result.HistorySummary.SubmitErrorRequests)
			assert.Equal(t, 1, result.HistorySummary.TerminalErrorRequests)
			assert.Equal(t, 1, result.HistorySummary.UnknownStageErrorRequests)
			assert.Equal(t, 2, result.HistorySummary.ObservedPolicyAttempts)
			assert.Equal(t, 2, result.HistorySummary.ClientReportEvents)
			assert.Equal(t, 1, result.HistorySummary.CorrelatedClientRequests)
			categoryMap := map[string]model.MediaOperationsCount{}
			for _, category := range result.HistorySummary.Categories {
				categoryMap[category.Category] = category
			}
			assert.Equal(t, 2, categoryMap["generation_capacity"].Events)
			assert.Equal(t, 1, categoryMap["content_policy"].Events)
			assert.Equal(t, 1, categoryMap["unknown"].Events, "generic task_failed and 502 are not capacity evidence")
			byModel := map[string]model.MediaOperationsModel{}
			for _, item := range result.Models {
				byModel[item.Model] = item
				assert.Equal(t, "unknown", item.Capacity.Status)
				assert.Nil(t, item.Capacity.ConfiguredLimit)
				assert.Nil(t, item.Capacity.AvailableSlots)
			}
			assert.Equal(t, 1, byModel["MiniMax-H3"].Tasks.Failure)
			assert.Zero(t, byModel["MiniMax-H3"].Tasks.Success)
			assert.Equal(t, 3, byModel["MiniMax-H3"].Poll.MaxConsecutiveFailures)
			assert.Equal(t, 1, byModel["MiniMax-H3"].Poll.TasksWithFailures)
			assert.Contains(t, byModel, "Configured-only")
			assert.Equal(t, "video", byModel["Removed-old-model"].MediaKind)
			assert.Contains(t, byModel, model.MediaOperationsUnknownModel)
			assert.NotContains(t, byModel, "ordinary-text")
			assert.NotContains(t, byModel, "Disabled-channel")
			encoded, err := common.Marshal(result)
			require.NoError(t, err)
			for _, secret := range []string{"secret-key", "secret-prompt", "sensitive error", "Do-not-infer-original", "same-request", "quota", "http://", "当前没有可用的生成资源", "被内容策略拒绝"} {
				assert.NotContains(t, string(encoded), secret)
			}
			filter.Model = "minimax-h3"
			empty, err := model.GetMediaOperations(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Empty(t, empty.Models, "exact identifier semantics match across case-folding SQL dialects")
			filter.Model = "MiniMax-H3"
			exact, err := model.GetMediaOperations(context.Background(), filter, catalog)
			require.NoError(t, err)
			require.Len(t, exact.Models, 1)
			assert.Equal(t, 4, exact.HistorySummary.ErrorEvents)
			filter.Model = ""
			filter.MediaKind = "image"
			images, err := model.GetMediaOperations(context.Background(), filter, catalog)
			require.NoError(t, err)
			require.Len(t, images.Models, 1)
			assert.Equal(t, "Configured-only", images.Models[0].Model)
			// The read never mutates either database, even though they are separate.
			var afterTasks []model.Task
			var afterLogs []model.Log
			require.NoError(t, db.Order("id").Find(&afterTasks).Error)
			require.NoError(t, logDB.Order("id").Find(&afterLogs).Error)
			assert.Equal(t, tasks, afterTasks)
			assert.Equal(t, logs, afterLogs)
			require.NoError(t, logDB.Migrator().DropTable(&model.Log{}))
			_, err = model.GetMediaOperations(context.Background(), filter, catalog)
			require.ErrorIs(t, err, model.ErrMediaOperationsQueryFailed)
		})
	}
}

func TestMediaOperationsControllerStrictFiltersAndFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, status integer)").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id,status) VALUES (1,1),(2,2)").Error)
	router := gin.New()
	router.GET("/api/media-operations", GetMediaOperations)
	for _, query := range []string{"?model=%", "?model=%&start_timestamp=1000000000&end_timestamp=1000000001", "?start_timestamp=0", "?start_timestamp=-1", "?start_timestamp=abc", "?end_timestamp=9999999999", "?start_timestamp=1&end_timestamp=9999999999", "?start_timestamp=3&end_timestamp=2", "?model=x&model=y", "?model=", "?model=%00", "?model=%20x", "?media_kind=VIDEO", "?media_kind=text", "?unknown=x", "?model=x;media_kind=image"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations"+query, nil))
		assert.Equal(t, http.StatusBadRequest, recorder.Code, query)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations", nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	// Bounded scan is observable as an explicit error, never a plausible zero.
	now := time.Now().Unix()
	rows := make([]model.Task, 10001)
	for index := range rows {
		rows[index] = model.Task{Action: "text_to_video", Status: model.TaskStatusUnknown, SubmitTime: now}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "MEDIA_OPERATIONS_QUERY_TOO_BROAD")
	require.NoError(t, db.Migrator().DropTable(&model.Task{}))
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations", nil))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "MEDIA_OPERATIONS_QUERY_FAILED")
	assert.NotContains(t, recorder.Body.String(), "no such table")
}

func TestMediaOperationsActualToapisNativeCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, status integer)").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id,status) VALUES (1,1),(2,2)").Error)
	source, err := os.ReadFile("../deploy/instance/toapis-image-plugin.js")
	require.NoError(t, err)
	oldRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = oldRegistry })
	_, err = jsplugin.DefaultRegistry.RegisterFactory(string(source), jsplugin.Options{})
	require.NoError(t, err)
	// Factory presence is classification evidence, not proof a model is enabled.
	now := time.Now().Unix()
	task := model.Task{Platform: "toapis-image", Action: "generate", Status: model.TaskStatusInProgress, SubmitTime: now - 80000, Properties: model.Properties{OriginModelName: "yunshu-internal-gpt-image-2"}}
	require.NoError(t, db.Create(&task).Error)
	require.NoError(t, logDB.Create(&model.Log{UserId: 1, Type: model.LogTypeError, ModelName: "yunshu-internal-gpt-image-2", CreatedAt: now, RequestId: "image-request", Other: `{"request_path":"/toapis/v1/images/generations","error_code":"task_failed"}`}).Error)
	require.NoError(t, logDB.Create(&model.Log{UserId: 1, Type: model.LogTypeError, ModelName: "gpt-image-2", CreatedAt: now, RequestId: "old-image-request", Other: `{"request_path":"/v1/images/generations","error_code":"task_failed"}`}).Error)
	router := gin.New()
	router.GET("/api/media-operations", GetMediaOperations)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations?media_kind=image", nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body struct {
		Data model.MediaOperationsResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Data.Models, 2)
	assert.Equal(t, "gpt-image-2", body.Data.Models[0].Model)
	assert.Equal(t, "image", body.Data.Models[0].MediaKind, "explicit historical image path classifies media before the image filter")
	assert.Equal(t, "image", body.Data.Models[1].MediaKind)
	assert.Equal(t, "yunshu-internal-gpt-image-2", body.Data.Models[1].Model)
	assert.Equal(t, 1, body.Data.Summary.InProgress)
	assert.Equal(t, 2, body.Data.HistorySummary.ErrorEvents)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "yunshu-internal-gpt-image-2", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, logDB.Create(&model.Log{UserId: 1, Type: model.LogTypeError, ModelName: "yunshu-internal-gpt-image-2", CreatedAt: now, RequestId: "conflicting-kind", Other: `{"request_path":"/v1/videos","error_code":"task_failed"}`}).Error)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations?media_kind=image", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Data.Models, 1)
	assert.Equal(t, "gpt-image-2", body.Data.Models[0].Model, "a preloaded configured image row cannot survive conflicting media evidence")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Data.Models, 2)
	assert.Equal(t, "unknown", body.Data.Models[1].MediaKind)
	require.NoError(t, db.Where("model = ?", "yunshu-internal-gpt-image-2").Delete(&model.Ability{}).Error)
	require.NoError(t, db.Delete(&task).Error)
	require.NoError(t, logDB.Where("type = ?", model.LogTypeError).Delete(&model.Log{}).Error)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/media-operations", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Empty(t, body.Data.Models, "unconfigured factory models must not become configured observations")
}

func TestMediaOperationsClientOnlyKindsAreOrderIndependent(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, status integer)").Error)
	now := time.Now().Unix()
	rows := []model.Log{{Type: model.LogTypeClientError, CreatedAt: now, ModelName: "Client-only-old-model", Content: "INVALID_ARGUMENT", Other: `{"source":"client","tool":"generate_image","stage":"preflight"}`}, {Type: model.LogTypeClientError, CreatedAt: now, ModelName: "Client-only-old-model", Content: "INVALID_ARGUMENT", Other: `{"source":"client","tool":"generate_video","stage":"preflight"}`}}
	require.NoError(t, logDB.Create(&rows).Error)
	filter := model.MediaOperationsFilter{Start: now - 60, End: now, MediaKind: "all"}
	catalog := model.MediaOperationsCatalog{}
	first, err := model.GetMediaOperations(context.Background(), filter, catalog)
	require.NoError(t, err)
	require.Len(t, first.Models, 1)
	assert.Equal(t, "unknown", first.Models[0].MediaKind)
	require.NoError(t, logDB.Where("type = ?", model.LogTypeClientError).Delete(&model.Log{}).Error)
	reversed := []model.Log{rows[1], rows[0]}
	for index := range reversed {
		reversed[index].Id = 0
	}
	require.NoError(t, logDB.Create(&reversed).Error)
	second, err := model.GetMediaOperations(context.Background(), filter, catalog)
	require.NoError(t, err)
	assert.Equal(t, first.Models, second.Models)
	assert.Equal(t, first.HistorySummary, second.HistorySummary)
}
