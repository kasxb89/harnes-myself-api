package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoUsageDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("VIDEO_USAGE_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("owned local video usage database fixture not configured")
			}
			db, _ := newAuditTestDatabase(t, kind, dsn)
			logDB, _ := newAuditTestDatabase(t, kind, dsn)
			typ := common.DatabaseTypeSQLite
			versionSQL := "SELECT sqlite_version()"
			if kind == "mysql" {
				typ = common.DatabaseTypeMySQL
				versionSQL = "SELECT VERSION()"
			} else if kind == "postgres" {
				typ = common.DatabaseTypePostgreSQL
				versionSQL = "SELECT VERSION()"
			}
			useLogExportDatabases(t, db, logDB, typ, typ)
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("actual engine version: %s", version)
			require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key, status integer, name varchar(255) default '', base_url varchar(255) default '')").Error)
			require.NoError(t, db.Exec("INSERT INTO channels (id,status) VALUES (1,1),(2,2)").Error)
			alice := model.User{Username: "video-alice", DisplayName: "Alice", AffCode: "video-alice"}
			bob := model.User{Username: "video-bob", DisplayName: "Bob", AffCode: "video-bob"}
			require.NoError(t, db.Create(&alice).Error)
			require.NoError(t, db.Create(&bob).Error)
			require.NoError(t, db.Create(&[]model.Ability{{Model: "MiniMax-H3", Group: "default", ChannelId: 1, Enabled: true}, {Model: "Configured-video", Group: "default", ChannelId: 1, Enabled: true}, {Model: "Disabled-video", Group: "default", ChannelId: 2, Enabled: true}, {Model: "Image-only", Group: "default", ChannelId: 1, Enabled: true}}).Error)
			require.NoError(t, db.Exec("UPDATE channels SET name = ?, base_url = ? WHERE id = 1", "Metaso H3", "https://metaso.cn/api/minimax?secret=private-query").Error)
			require.NoError(t, db.Exec("INSERT INTO channels (id,status,name,base_url) VALUES (3,1,?,?)", "Blackrx video", "https://hi.blackrxai.com/private-path").Error)
			now := time.Now().Unix()
			filter := model.VideoUsageFilter{Start: now - 120, End: now - 20, ClientSource: "all", TaskScope: "history", Page: 1, PageSize: 100}
			catalog := model.MediaOperationsCatalog{Models: map[string]string{"MiniMax-H3": "video", "Configured-video": "video", "Disabled-video": "video", "Other-video": "video", "Image-only": "image"}, Platforms: map[string]string{"hailuo": "video", "toapis-image": "image"}}
			rows := []model.Task{
				{Platform: "hailuo", Status: model.TaskStatusSuccess, UserId: alice.Id, SubmitTime: now - 150, StartTime: now - 140, FinishTime: now - 100}, // Cross-window interval A.
				{Platform: "hailuo", Status: model.TaskStatusSuccess, UserId: alice.Id, SubmitTime: now - 100, StartTime: now - 95, FinishTime: now - 50},   // B touches A's end.
				{Platform: "hailuo", Status: model.TaskStatusSuccess, UserId: bob.Id, SubmitTime: now - 50, StartTime: now - 50, FinishTime: now - 20},      // C touches B's end.
				{Platform: "hailuo", Status: model.TaskStatusSuccess, UserId: bob.Id, SubmitTime: now - 20, StartTime: now - 20, FinishTime: now - 10},      // End-exclusive.
				{Platform: "hailuo", Status: model.TaskStatusUnknown, UserId: alice.Id, SubmitTime: now - 90},
				{Platform: "hailuo", Status: model.TaskStatusSuccess, UserId: alice.Id, SubmitTime: now - 60, StartTime: now - 60, FinishTime: now - 60}, // Valid zero sample.
				{Platform: "hailuo", Status: model.TaskStatusFailure, UserId: alice.Id, SubmitTime: now - 70, StartTime: now - 65},                       // Missing finish.
				{Platform: "hailuo", Status: model.TaskStatusFailure, UserId: bob.Id, SubmitTime: now - 80, StartTime: now - 79, FinishTime: now + 10},   // Future finish.
				{Platform: "hailuo", Status: model.TaskStatusFailure, UserId: bob.Id, SubmitTime: now - 75, StartTime: now - 74, FinishTime: now - 76},   // Reversed.
				{Platform: "hailuo", Status: model.TaskStatusNotStart, UserId: alice.Id, SubmitTime: now - 1000, Properties: model.Properties{OriginModelName: "Other-video"}},
				{Platform: "hailuo", Status: model.TaskStatusInProgress, UserId: 99999, SubmitTime: now - 1000, Properties: model.Properties{UpstreamModelName: "private-upstream-model"}},
				{Platform: "toapis-image", Status: model.TaskStatusInProgress, UserId: alice.Id, SubmitTime: now - 90, Properties: model.Properties{OriginModelName: "Image-only"}},
				{Platform: "suno", Action: "generate", Status: model.TaskStatusInProgress, UserId: alice.Id, SubmitTime: now - 90, Properties: model.Properties{OriginModelName: "audio-only"}},
				{Platform: "hailuo", Status: model.TaskStatusInProgress, UserId: alice.Id, SubmitTime: now - 1000, Properties: model.Properties{OriginModelName: "Null-channel-video"}},
			}
			for index := range rows {
				row := &rows[index]
				if index < 9 {
					row.ChannelId = 1
				} else if index == 9 {
					row.ChannelId = 3
				} else if index == 10 {
					row.ChannelId = 99
				}
				if index < 9 {
					row.Properties = model.Properties{OriginModelName: "MiniMax-H3", Input: "private-prompt"}
				}
				row.TaskID = model.GenerateTaskID()
				row.PrivateData.Key = "secret-channel-key"
			}
			rows[1].PrivateData.Execution = &model.TaskExecutionSnapshot{ClientSource: "desktop"}
			rows[2].PrivateData.Execution = &model.TaskExecutionSnapshot{ClientSource: "web"}
			rows[1].Data = common.RawMessage(`{"task":{"usage":{"output_seconds":6}},"url":"https://private-output.example"}`)
			rows[2].Data = common.RawMessage(`{"task":{"usage":{"output_seconds":"8"}}}`) // String is not a reported number.
			rows[4].TaskID = "upstream-legacy-private-id"
			require.NoError(t, db.Create(&rows).Error)
			require.NoError(t, db.Model(&model.Task{}).Where("id = ?", rows[len(rows)-1].ID).Update("channel_id", nil).Error)
			var before []model.Task
			require.NoError(t, db.Order("id").Find(&before).Error)
			result, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.False(t, result.ObservationComplete)
			assert.Equal(t, "not_collected", result.ClientSessionDuration.Status)
			assert.Nil(t, result.ClientSessionDuration.Seconds)
			assert.Equal(t, "unknown", result.UpstreamCapacity.Status)
			assert.Nil(t, result.UpstreamCapacity.ConfiguredLimit)
			assert.Equal(t, 1, result.Summary.Current.NotStart)
			assert.Equal(t, 2, result.Summary.Current.InProgress)
			assert.Equal(t, 1, result.Summary.Current.Unknown)
			assert.Equal(t, 3, result.Summary.Current.Inflight)
			assert.Equal(t, 4, result.Summary.Current.TotalRecords)
			assert.Equal(t, 7, result.Summary.History.TaskCount)
			assert.Equal(t, 3, result.Summary.History.Success)
			assert.Equal(t, 3, result.Summary.History.Failure)
			assert.Equal(t, 1, result.Summary.History.Unresolved)
			assert.Equal(t, 2, result.Summary.History.Users)
			acceptedSum, trendPeak := 0, 0
			for _, point := range result.Trends.Points {
				acceptedSum += point.AcceptedTasks
				assert.Equal(t, point.EndTimestamp-point.StartTimestamp, point.Seconds)
				assert.InDelta(t, float64(point.AcceptedTasks)/float64(point.Seconds), point.AcceptedTaskRate, .0000001)
				if point.RecordedPeak != nil {
					trendPeak = max(trendPeak, *point.RecordedPeak)
				}
			}
			assert.Equal(t, result.Summary.History.TaskCount, acceptedSum)
			require.NotNil(t, result.Summary.Peak.Value)
			assert.Equal(t, *result.Summary.Peak.Value, trendPeak)
			assert.LessOrEqual(t, len(result.Trends.Points), 120)
			assert.Equal(t, "current_channel_host_not_historical_identity", result.ProviderClassification)
			channelMap := map[int]model.VideoUsageChannelBreakdown{}
			for _, item := range result.ChannelBreakdown {
				channelMap[item.Channel.ID] = item
			}
			assert.Equal(t, "metaso", channelMap[1].Channel.Provider)
			assert.Equal(t, "blackrx", channelMap[3].Channel.Provider)
			assert.Equal(t, "deleted", channelMap[99].Channel.Status)
			assert.Equal(t, "unknown", channelMap[99].Channel.Provider)
			turnaround := result.Summary.History.Turnaround
			require.NotNil(t, turnaround.MeanSeconds)
			assert.InDelta(t, 80.0/3, *turnaround.MeanSeconds, .00001)
			assert.Equal(t, 3, turnaround.ValidSamples)
			assert.Equal(t, 1, turnaround.MissingSamples)
			assert.Equal(t, 2, turnaround.InvalidSamples)
			assert.Equal(t, 1, turnaround.ZeroSamples)
			assert.EqualValues(t, 30, *turnaround.P50Seconds)
			assert.EqualValues(t, 50, *turnaround.P95Seconds)
			assert.Equal(t, 7, result.SourceCoverage.Total)
			assert.Equal(t, 1, result.SourceCoverage.Desktop)
			assert.Equal(t, 6, result.SourceCoverage.Unknown)
			byModel := map[string]model.VideoUsageModel{}
			for _, item := range result.Models {
				byModel[item.Model] = item
			}
			require.NotNil(t, byModel["MiniMax-H3"].Peak.Value)
			assert.Equal(t, 1, *byModel["MiniMax-H3"].Peak.Value, "end events precede starts, and the old A interval remains counted")
			assert.Equal(t, 3, byModel["MiniMax-H3"].Peak.IntervalSamples)
			assert.Equal(t, 5, byModel["MiniMax-H3"].Peak.ExcludedIntervals)
			assert.Equal(t, 1, byModel["MiniMax-H3"].Peak.UnknownState)
			assert.Equal(t, 1, byModel["MiniMax-H3"].Peak.MissingFinish)
			assert.Equal(t, 1, byModel["MiniMax-H3"].Peak.ZeroLength)
			assert.Equal(t, 2, byModel["MiniMax-H3"].Peak.InvalidTimestamps)
			assert.Contains(t, byModel, "Configured-video")
			assert.NotContains(t, byModel, "Disabled-video")
			assert.NotContains(t, byModel, "Image-only")
			assert.NotContains(t, byModel, "audio-only")
			assert.Contains(t, byModel, model.MediaOperationsUnknownModel)
			assert.Equal(t, 7, result.Tasks.Total)
			byID := map[int64]model.VideoUsageTask{}
			for _, item := range result.Tasks.Items {
				byID[item.ID] = item
			}
			assert.Nil(t, byID[rows[4].ID].TaskID)
			assert.Nil(t, byID[rows[4].ID].RecordedRunningSeconds)
			require.NotNil(t, byID[rows[1].ID].ReportedVideoSeconds)
			assert.EqualValues(t, 6, *byID[rows[1].ID].ReportedVideoSeconds)
			assert.Equal(t, "h3_usage", byID[rows[1].ID].ReportedVideoDurationSource)
			assert.Nil(t, byID[rows[2].ID].ReportedVideoSeconds)
			encoded, err := common.Marshal(result)
			require.NoError(t, err)
			for _, secret := range []string{"private-prompt", "secret-channel-key", "https://private-output.example", "private-upstream-model", "upstream-legacy-private-id", "billing_context", "request_path", "metaso.cn", "private-query", "private-path"} {
				assert.NotContains(t, string(encoded), secret)
			}
			filter.Provider = "metaso"
			metaso, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 7, metaso.Summary.History.TaskCount)
			assert.Equal(t, 1, metaso.Summary.Current.TotalRecords)
			assert.Equal(t, result.Ingress, metaso.Ingress, "entry QPS ignores task provider filters")
			filter.Provider = "all"
			blackrxID := 3
			filter.ChannelID = &blackrxID
			blackrx, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Zero(t, blackrx.Summary.History.TaskCount)
			assert.Equal(t, 1, blackrx.Summary.Current.Inflight)
			assert.Zero(t, blackrx.SourceCoverage.Total)
			assert.Equal(t, result.Ingress, blackrx.Ingress)
			zeroChannel := 0
			filter.ChannelID = &zeroChannel
			unknownChannel, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 1, unknownChannel.Summary.Current.Inflight, "SQL NULL channel ids belong to the same unknown-channel filter as projected zero")
			filter.ChannelID = nil
			filter.ClientSource = "desktop"
			desktop, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 1, desktop.Summary.History.TaskCount)
			assert.Equal(t, result.SourceCoverage, desktop.SourceCoverage, "coverage is measured before applying the source filter")
			filter.ClientSource = "all"
			filter.TaskScope = "current"
			current, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 4, current.Tasks.Total)
			for _, item := range current.Tasks.Items {
				if item.ID == rows[10].ID {
					require.NotNil(t, item.User)
					assert.Equal(t, 99999, item.User.ID)
					assert.Empty(t, item.User.Username)
				}
			}
			filter.TaskScope = "history"
			filter.PageSize = 2
			filter.Page = 2
			paged, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 7, paged.Tasks.Total)
			require.Len(t, paged.Tasks.Items, 2)
			assert.Equal(t, result.Tasks.Items[2:4], paged.Tasks.Items)
			filter.Model = "minimax-h3"
			empty, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Zero(t, empty.Summary.History.TaskCount)
			assert.Zero(t, empty.Tasks.Total)
			filter.Model = "MiniMax-H3"
			filter.PageSize = 100
			filter.Page = 1
			exact, err := model.GetVideoUsage(context.Background(), filter, catalog)
			require.NoError(t, err)
			assert.Equal(t, 7, exact.Summary.History.TaskCount)
			// A source claim is frozen metadata; legacy rows remain unknown, and all
			// task fields plus the independent log store remain untouched by reads.
			var after []model.Task
			require.NoError(t, db.Order("id").Find(&after).Error)
			assert.Equal(t, before, after)
			var logCount int64
			require.NoError(t, logDB.Model(&model.Log{}).Count(&logCount).Error)
			assert.Zero(t, logCount)
			require.NoError(t, db.Migrator().DropTable(&model.Task{}))
			_, err = model.GetVideoUsage(context.Background(), filter, catalog)
			require.ErrorIs(t, err, model.ErrVideoUsageQueryFailed)
		})
	}
}

func TestVideoUsageStrictFiltersAndEmptyQuality(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key,status integer, name varchar(255) default '', base_url varchar(255) default '')").Error)
	router := gin.New()
	router.GET("/api/video-usage", GetVideoUsage)
	for _, query := range []string{"?model=%&start_timestamp=1&end_timestamp=2", "?unknown=x", "?model=x&model=y", "?user_id=0", "?user_id=-1", "?user_id=no", "?user_id=999999999999999999999", "?client_source=web", "?channel_id=-1", "?channel_id=x", "?channel_id=1&channel_id=2", "?provider=private-host", "?task_scope=live", "?p=0", "?p=1000001", "?page_size=101", "?page_size=0", "?model=%00", "?model=", "?start_timestamp=0", "?start_timestamp=2&end_timestamp=2", "?start_timestamp=3&end_timestamp=2", "?end_timestamp=9999999999", "?start_timestamp=1&end_timestamp=9999999999"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage"+query, nil))
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var envelope struct {
		Data model.VideoUsageResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	assert.EqualValues(t, 86400, envelope.Data.EndTimestamp-envelope.Data.StartTimestamp)
	assert.NotNil(t, envelope.Data.Summary.Peak.Value)
	assert.Zero(t, *envelope.Data.Summary.Peak.Value)
	assert.Nil(t, envelope.Data.Summary.History.Turnaround.MeanSeconds)
	assert.Empty(t, envelope.Data.Tasks.Items)
	now := time.Now().Unix()
	row := model.Task{Platform: "hailuo", Action: "text_to_video", Status: model.TaskStatusSuccess, SubmitTime: now - 10, FinishTime: 0}
	require.NoError(t, db.Create(&row).Error)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	assert.Nil(t, envelope.Data.Summary.Peak.Value, "excluded unknown interval is not a known zero")
	assert.Equal(t, 1, envelope.Data.Summary.Peak.MissingFinish)
	rows := make([]model.Task, 10001)
	for index := range rows {
		rows[index] = model.Task{Platform: "hailuo", Action: "text_to_video", Status: model.TaskStatusUnknown, SubmitTime: now}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "VIDEO_USAGE_QUERY_TOO_BROAD")
	require.NoError(t, db.Migrator().DropTable(&model.Task{}))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage", nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "VIDEO_USAGE_QUERY_FAILED")
	assert.NotContains(t, response.Body.String(), "no such table")
}

func TestVideoUsageControllerUsesSharedExplicitVideoCatalog(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key,status integer, name varchar(255) default '', base_url varchar(255) default '')").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id,status) VALUES(1,1)").Error)
	previous := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = previous })
	source, err := os.ReadFile("../plugins/tasks/hailuo/plugin.js")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.RegisterFactory(string(source), jsplugin.Options{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Ability{Model: "MiniMax-H3", Group: "default", ChannelId: 1, Enabled: true}).Error)
	router := gin.New()
	router.GET("/api/video-usage", GetVideoUsage)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/video-usage", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"models":["MiniMax-H3"]`)
}

func TestVideoUsageReportedDurationBoundsAndMissingTimes(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key,status integer, name varchar(255) default '', base_url varchar(255) default '')").Error)
	now := time.Now().Unix()
	rows := []model.Task{}
	for _, entry := range []struct {
		platform string
		status   model.TaskStatus
		value    string
	}{{"hailuo", model.TaskStatusSuccess, "4"}, {"hailuo", model.TaskStatusSuccess, "15"}, {"hailuo", model.TaskStatusSuccess, "3.9"}, {"hailuo", model.TaskStatusSuccess, "15.1"}, {"hailuo", model.TaskStatusSuccess, `"7"`}, {"hailuo", model.TaskStatusFailure, "7"}, {"other-video", model.TaskStatusSuccess, "7"}} {
		row := model.Task{Platform: constant.TaskPlatform(entry.platform), Action: "text_to_video", Status: entry.status, SubmitTime: now - 10, StartTime: now - 10, FinishTime: now - 10, Properties: model.Properties{OriginModelName: "MiniMax-H3"}, Data: common.RawMessage(fmt.Sprintf(`{"task":{"usage":{"output_seconds":%s}}}`, entry.value))}
		rows = append(rows, row)
	}
	rows = append(rows, model.Task{Action: "text_to_video", Status: model.TaskStatusFailure, SubmitTime: now - 10, StartTime: -1, FinishTime: now - 5}, model.Task{Action: "text_to_video", Status: model.TaskStatusFailure, SubmitTime: now - 10, FinishTime: now - 5})
	rows = append(rows,
		model.Task{Platform: "hailuo", Action: "text_to_video", Status: model.TaskStatusSuccess, SubmitTime: now - 10, StartTime: now - 10, FinishTime: now - 10, Properties: model.Properties{OriginModelName: "h3-friendly-alias", UpstreamModelName: "MiniMax-H3"}, Data: common.RawMessage(`{"task":{"usage":{"output_seconds":7}}}`)},
		model.Task{Platform: "hailuo", Action: "text_to_video", Status: model.TaskStatusSuccess, SubmitTime: now - 10, StartTime: now - 10, FinishTime: now - 10, Properties: model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-Hailuo-2.3"}, Data: common.RawMessage(`{"task":{"usage":{"output_seconds":6}}}`)},
		model.Task{Action: "text_to_video", Status: model.TaskStatusSuccess, SubmitTime: now - 15, StartTime: now - 5, FinishTime: now - 10})
	require.NoError(t, db.Create(&rows).Error)
	filter := model.VideoUsageFilter{Start: now - 100, End: now, ClientSource: "all", TaskScope: "history", Page: 1, PageSize: 100}
	result, err := model.GetVideoUsage(context.Background(), filter, model.MediaOperationsCatalog{})
	require.NoError(t, err)
	reported := 0
	for _, item := range result.Tasks.Items {
		if item.ReportedVideoSeconds != nil {
			reported++
			assert.Contains(t, []float64{4, 15, 7}, *item.ReportedVideoSeconds)
		}
	}
	assert.Equal(t, 3, reported)
	assert.Equal(t, 2, result.Summary.History.RecordedRun.InvalidSamples)
	assert.Equal(t, 1, result.Summary.History.RecordedRun.MissingSamples)
	for _, item := range result.Tasks.Items {
		if item.ID == rows[len(rows)-1].ID {
			assert.Equal(t, "invalid", item.TimingQuality)
			assert.Nil(t, item.TurnaroundSeconds)
			assert.Nil(t, item.RecordedRunningSeconds)
			assert.Nil(t, item.WaitingSeconds)
		}
	}
}

func TestVideoUsagePeakQualityCannotBecomeKnownZero(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key,status integer, name varchar(255) default '', base_url varchar(255) default '')").Error)
	now := time.Now().Unix()
	filter := model.VideoUsageFilter{Start: now - 200, End: now - 100, TaskScope: "current", ClientSource: "all", Page: 1, PageSize: 100}
	for _, tc := range []struct {
		name                      string
		row                       model.Task
		invalid, missing, unknown int
		knownZero                 bool
	}{
		{name: "future_current", row: model.Task{Status: model.TaskStatusInProgress, SubmitTime: now + 60}, invalid: 1},
		{name: "inverted_before_window", row: model.Task{Status: model.TaskStatusFailure, SubmitTime: now - 1000, FinishTime: now - 1001}, invalid: 1},
		{name: "missing_finish_before_window", row: model.Task{Status: model.TaskStatusFailure, SubmitTime: now - 1000}, missing: 1},
		{name: "unknown_is_not_running", row: model.Task{Status: model.TaskStatusUnknown, SubmitTime: now - 150}, unknown: 1},
		{name: "valid_current_after_window", row: model.Task{Status: model.TaskStatusNotStart, SubmitTime: now - 50}, knownZero: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, db.Where("1 = 1").Delete(&model.Task{}).Error)
			row := tc.row
			row.Action = "text_to_video"
			require.NoError(t, db.Create(&row).Error)
			result, err := model.GetVideoUsage(context.Background(), filter, model.MediaOperationsCatalog{})
			require.NoError(t, err)
			assert.Equal(t, tc.invalid, result.Summary.Peak.InvalidTimestamps)
			assert.Equal(t, tc.missing, result.Summary.Peak.MissingFinish)
			assert.Equal(t, tc.unknown, result.Summary.Peak.UnknownState)
			if tc.knownZero {
				require.NotNil(t, result.Summary.Peak.Value)
				assert.Zero(t, *result.Summary.Peak.Value)
			} else {
				assert.Nil(t, result.Summary.Peak.Value)
			}
		})
	}
}

func TestVideoIngressRealHTTPArrivalAndNativeDurableContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{}
	database := setupTaskSubmissionDatabase(t, true, &events)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Channel{}))
	require.NoError(t, database.Create(&model.User{Id: 1, Username: "ingress-http-fixture", AffCode: "ingress-http-fixture", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, database.Create(&model.Channel{Id: 1, Name: "Owned local fixture", Key: "synthetic-fixture-key", Status: common.ChannelStatusEnabled}).Error)
	previousConsume, previousError := common.LogConsumeEnabled, constant.ErrorLogEnabled
	common.LogConsumeEnabled, constant.ErrorLogEnabled = false, false
	t.Cleanup(func() { common.LogConsumeEnabled, constant.ErrorLogEnabled = previousConsume, previousError })
	previous := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = previous })
	source := `export const meta={apiVersion:1,key:"video-native-observer",name:"Video native observer",version:"1.0.0",author:{name:"Fixture"},models:["MiniMax-H3"],fetchMode:"per_task",routes:[{method:"POST",path:"/fixture/v1/videos",type:"submit",decode:"decode",render:"render"},{method:"GET",path:"/fixture/v1/videos/:task_id",type:"query",render:"render"}]};export const native={decode:function(ctx){return {kind:"submit",model:"MiniMax-H3",requestBody:ctx.body.value};},render:function(){return {};}};export function buildSubmitRequest(){return {};}export function parseSubmitResponse(){return {taskId:"fake"};}export function buildQueryRequest(){return {};}export function parseTaskResult(){return {status:"UNKNOWN"};}`
	_, err := jsplugin.DefaultRegistry.RegisterFactory(source, jsplugin.Options{})
	require.NoError(t, err)
	h3, err := os.ReadFile("../plugins/tasks/hailuo/plugin.js")
	require.NoError(t, err)
	_, err = jsplugin.DefaultRegistry.RegisterFactory(string(h3), jsplugin.Options{})
	require.NoError(t, err)
	recorder := model.NewVideoIngressRecorder(time.Now().Add(-time.Minute))
	outer, inner := gin.New(), gin.New()
	outer.Use(middleware.VideoIngressObserver(recorder))
	outer.Use(func(c *gin.Context) {
		if value := c.GetHeader("X-Fixture-Reject"); value != "" {
			code, parseErr := strconv.Atoi(value)
			if parseErr == nil {
				c.AbortWithStatus(code)
				return
			}
		}
		c.Next()
	})
	entered, release := make(chan struct{}), make(chan struct{})
	outer.POST("/v1/videos", func(c *gin.Context) {
		if c.GetHeader("X-Fixture-Pending") == "yes" {
			close(entered)
			<-release
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusAccepted)
	})
	outer.POST("/v1/tasks/:key", func(c *gin.Context) { c.Status(http.StatusBadRequest) })
	outer.GET("/v1/videos/:task_id", func(c *gin.Context) { c.Status(http.StatusOK) })
	outer.GET("/v1/videos/:task_id/content", func(c *gin.Context) { c.Status(http.StatusOK) })
	outer.HEAD("/v1/videos/:task_id/content", func(c *gin.Context) { c.Status(http.StatusOK) })
	outer.GET("/v1/tasks/:key", func(c *gin.Context) { c.Status(http.StatusOK) })
	inner.Use(middleware.VideoIngressObserver(recorder)) // Same context must prevent a second arrival.
	inner.POST("/fixture/v1/videos", func(c *gin.Context) {
		info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
		info.PublicTaskID = model.GenerateTaskID()
		outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			return &relay.TaskSubmitResult{UpstreamTaskID: "private-native-upstream", Platform: "hailuo"}, nil
		})
		if taskErr != nil || outcome == nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusAccepted)
	})
	inner.GET("/fixture/v1/videos/:task_id", func(c *gin.Context) { c.Status(http.StatusOK) })
	outer.NoRoute(func(c *gin.Context) { inner.ServeHTTP(c.Writer, c.Request); c.Abort() })
	server := httptest.NewServer(outer)
	var releaseOnce sync.Once
	releasePending := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { releasePending(); server.Close() })
	do := func(method, path string, headers map[string]string) int {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, strings.NewReader(`{}`))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return response.StatusCode
	}
	started := time.Now().Unix()
	for _, status := range []int{401, 403, 429, 413, 400, 500} {
		assert.Equal(t, status, do(http.MethodPost, "/v1/videos", map[string]string{"X-Fixture-Reject": strconv.Itoa(status)}))
	}
	assert.Equal(t, http.StatusAccepted, do(http.MethodPost, "/fixture/v1/videos", nil))
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPost, "/v1/tasks/hailuo", nil), "a known video-only generic create route counts despite rejection")
	assert.Equal(t, http.StatusAccepted, do(http.MethodPost, "/v1/videos", map[string]string{"X-Video-Ingress-Accepted": "true"}), "a header or HTTP 2xx cannot manufacture durable acceptance")
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/videos/task_public", nil))
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/fixture/v1/videos/task_public", nil))
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/videos/task_public/content", nil))
	assert.Equal(t, http.StatusOK, do(http.MethodHead, "/v1/videos/task_public/content", nil))
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/tasks/task_public", nil))
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/videos", strings.NewReader(`{}`))
	require.NoError(t, err)
	request.Header.Set("X-Fixture-Pending", "yes")
	result := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if err == nil {
			err = response.Body.Close()
		}
		result <- err
	}()
	<-entered
	end := time.Now().Unix() + 2
	points := []model.VideoUsageTrendPoint{{StartTimestamp: started, EndTimestamp: end, Seconds: end - started}}
	snapshot := recorder.Snapshot(points, end)
	require.Len(t, snapshot.Points, 1)
	point := snapshot.Points[0]
	require.NotNil(t, point.SubmitRequests)
	assert.EqualValues(t, 10, *point.SubmitRequests)
	assert.EqualValues(t, 2, *point.StatusRequests)
	assert.EqualValues(t, 1, *point.SubmitPending)
	assert.EqualValues(t, 1, *point.SubmitAccepted)
	assert.EqualValues(t, 8, *point.SubmitEndedWithoutAcceptance)
	assert.InDelta(t, 10.0/float64(point.Seconds), *point.SubmitQPS, .0000001)
	releasePending()
	require.NoError(t, <-result)
	after := recorder.Snapshot(points, end).Points[0]
	assert.Zero(t, *after.SubmitPending)
	assert.EqualValues(t, 9, *after.SubmitEndedWithoutAcceptance)
	var tasks int64
	require.NoError(t, database.Model(&model.Task{}).Count(&tasks).Error)
	assert.EqualValues(t, 1, tasks, "native inner and outer contexts observe one actual durable insert")
}

func TestVideoIngressRetentionCoverageAndClassificationIdempotence(t *testing.T) {
	store := model.NewVideoIngressRecorder(time.Unix(1000, 500))
	pending := store.Begin("submit", time.Unix(1002, 0))
	accepted := store.Begin("submit", time.Unix(1003, 0))
	model.MarkVideoIngressAccepted(accepted.Context(context.Background()))
	accepted.Finish(false)
	accepted.Finish(true)
	ended := store.Begin("submit", time.Unix(1003, 0))
	ended.Finish(false)
	ended.Finish(false)
	store.Begin("status", time.Unix(1004, 0))
	points := []model.VideoUsageTrendPoint{{StartTimestamp: 1000, EndTimestamp: 1002, Seconds: 2}, {StartTimestamp: 1002, EndTimestamp: 1005, Seconds: 3}, {StartTimestamp: 1005, EndTimestamp: 1007, Seconds: 2}}
	snapshot := store.Snapshot(points, 1007)
	assert.Equal(t, "partial", snapshot.Points[0].Coverage)
	assert.Nil(t, snapshot.Points[0].SubmitRequests)
	assert.EqualValues(t, 1, snapshot.Points[0].CoveredSeconds)
	middle := snapshot.Points[1]
	require.NotNil(t, middle.SubmitRequests)
	assert.EqualValues(t, 3, *middle.SubmitRequests)
	assert.EqualValues(t, 1, *middle.SubmitPending)
	assert.EqualValues(t, 1, *middle.SubmitAccepted)
	assert.EqualValues(t, 1, *middle.SubmitEndedWithoutAcceptance)
	zero := snapshot.Points[2]
	assert.Equal(t, "complete", zero.Coverage)
	require.NotNil(t, zero.SubmitQPS)
	assert.Zero(t, *zero.SubmitQPS)
	// Advancing beyond retention overwrites only fixed ring slots; an expired
	// completion cannot contaminate the newer second occupying the same slot.
	store.Begin("submit", time.Unix(4603, 0)).Finish(false)
	pending.Finish(true)
	expired := store.Snapshot(points, 4610)
	for _, point := range expired.Points {
		assert.Nil(t, point.SubmitQPS)
		assert.Equal(t, "unavailable", point.Coverage)
	}
	restarted := model.NewVideoIngressRecorder(time.Unix(4605, 0)).Snapshot(points, 4607)
	for _, point := range restarted.Points {
		assert.Nil(t, point.SubmitRequests)
	}
}

func TestVideoUsageChannelUnionAndLateUnknownTrend(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	logDB, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, logDB, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Ability{}, &model.User{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Exec("CREATE TABLE channels (id integer primary key,status integer,name varchar(255),base_url varchar(255))").Error)
	require.NoError(t, db.Exec("INSERT INTO channels (id,status,name,base_url) VALUES (1,1,'One account','https://metaso.cn/api/minimax'),(2,1,'Unclassified host','https://metaso.cn.evil.example/private')").Error)
	now := time.Now().Unix()
	filter := model.VideoUsageFilter{Start: now - 240, End: now - 120, ClientSource: "all", TaskScope: "history", Page: 1, PageSize: 100}
	rows := []model.Task{{Platform: "hailuo", ChannelId: 1, UserId: 1, Status: model.TaskStatusSuccess, SubmitTime: now - 240, StartTime: now - 240, FinishTime: now - 220, Properties: model.Properties{OriginModelName: "Model-A"}}, {Platform: "hailuo", ChannelId: 1, UserId: 2, Status: model.TaskStatusSuccess, SubmitTime: now - 220, StartTime: now - 220, FinishTime: now - 200, Properties: model.Properties{OriginModelName: "Model-B"}}, {Platform: "hailuo", ChannelId: 1, UserId: 1, Status: model.TaskStatusUnknown, SubmitTime: now - 180, Properties: model.Properties{OriginModelName: "Model-B"}}, {Platform: "hailuo", ChannelId: 2, UserId: 1, Status: model.TaskStatusSuccess, SubmitTime: now - 200, StartTime: now - 200, FinishTime: now - 199, Properties: model.Properties{OriginModelName: "Model-C"}}}
	require.NoError(t, db.Create(&rows).Error)
	catalog := model.MediaOperationsCatalog{Platforms: map[string]string{"hailuo": "video"}}
	result, err := model.GetVideoUsage(context.Background(), filter, catalog)
	require.NoError(t, err)
	channels := map[int]model.VideoUsageChannelBreakdown{}
	for _, channel := range result.ChannelBreakdown {
		channels[channel.Channel.ID] = channel
	}
	require.NotNil(t, channels[1].Peak.Value)
	assert.Equal(t, 1, *channels[1].Peak.Value, "nonoverlapping per-model peaks are not summed")
	assert.Equal(t, "unknown", channels[2].Channel.Provider, "lookalike domains never become a known supplier")
	sum, maxPeak := 0, 0
	unknownLate := false
	for _, point := range result.Trends.Points {
		sum += point.AcceptedTasks
		if point.RecordedPeak != nil {
			maxPeak = max(maxPeak, *point.RecordedPeak)
		}
		if point.StartTimestamp >= now-180 {
			unknownLate = true
			assert.Nil(t, point.RecordedPeak)
			assert.Equal(t, 1, point.PeakExclusions.UnknownState)
		}
	}
	assert.True(t, unknownLate)
	assert.Equal(t, result.Summary.History.TaskCount, sum)
	require.NotNil(t, result.Summary.Peak.Value)
	assert.Equal(t, *result.Summary.Peak.Value, maxPeak)
	// A 121-second range has 2-second buckets except its one-second tail.
	// Tail rates use that actual second, while the exclusive end is not accepted.
	tailStart := now - 400
	tailFilter := filter
	tailFilter.Start, tailFilter.End = tailStart, tailStart+121
	tailRows := []model.Task{
		{Platform: "hailuo", ChannelId: 1, UserId: 1, Status: model.TaskStatusSuccess, SubmitTime: tailStart + 120, StartTime: tailStart + 120, FinishTime: tailStart + 121, Properties: model.Properties{OriginModelName: "Tail-video"}},
		{Platform: "hailuo", ChannelId: 1, UserId: 1, Status: model.TaskStatusSuccess, SubmitTime: tailStart + 121, StartTime: tailStart + 121, FinishTime: tailStart + 122, Properties: model.Properties{OriginModelName: "Tail-video"}},
	}
	require.NoError(t, db.Create(&tailRows).Error)
	tailResult, err := model.GetVideoUsage(context.Background(), tailFilter, catalog)
	require.NoError(t, err)
	assert.EqualValues(t, 2, tailResult.Trends.BucketSeconds)
	require.NotEmpty(t, tailResult.Trends.Points)
	tail := tailResult.Trends.Points[len(tailResult.Trends.Points)-1]
	assert.EqualValues(t, 1, tail.Seconds)
	assert.Equal(t, 1, tail.AcceptedTasks)
	assert.Equal(t, float64(1), tail.AcceptedTaskRate)
	assert.Equal(t, 1, tailResult.Summary.History.TaskCount)
}
