package router

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newVideoQueueFixture(t *testing.T, accounts int) *h3RouterFixture {
	t.Helper()
	previousDispatcher := service.CurrentVideoQueueDispatcher()
	previousUpdateTask := constant.UpdateTask
	constant.UpdateTask = true
	t.Cleanup(func() { constant.UpdateTask = previousUpdateTask })
	f := newH3RouterFixture(t, accounts)
	t.Cleanup(func() { service.SetVideoQueueDispatcher(previousDispatcher) })
	// The existing fixture creates the released schema and existing accounts
	// first. Queue migration is then applied twice, just as on upgrade/restart.
	require.NoError(t, f.db.Migrator().DropTable(&model.VideoQueueItem{}, &model.VideoQueueGuard{}))
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(&model.VideoQueueItem{}, &model.VideoQueueGuard{}))
	}
	var users int64
	require.NoError(t, f.db.Model(&model.User{}).Count(&users).Error)
	require.EqualValues(t, 2, users, "existing user data survives queue upgrade")
	return f
}

func enqueueQueueFixture(t *testing.T, f *h3RouterFixture, index int, request string) *model.VideoQueueItem {
	t.Helper()
	id := fmt.Sprintf("vq_00000000-0000-4000-8000-%012d", index)
	body := fmt.Sprintf(`{"schema_version":1,"queue_id":%q,"operation_id":"fixture-operation-%d","request":%s}`, id, index, request)
	w := f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routeradmin")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	item, err := model.GetVideoQueueItem(context.Background(), 791, id)
	require.NoError(t, err)
	return item
}

func TestVideoQueueDefaultOneIdempotencyOwnershipAndCancel(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	beforeWallet, beforeToken := f.balance(t)
	item := enqueueQueueFixture(t, f, 1, `{"model":"MiniMax-H3","prompt":"来个10s视频","seconds":"10"}`)
	assert.Equal(t, 1, item.Count)
	assert.Equal(t, 10, item.DurationSeconds)
	assert.Equal(t, model.VideoQueueQueued, item.State)
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
	body := `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000000001","operation_id":"fixture-operation-1","count":1,"request":{"seconds":"10","prompt":"来个10s视频","model":"MiniMax-H3"}}`
	for range 2 {
		w := f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routeradmin")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), item.TaskID)
	}
	conflict := f.request(t, http.MethodPost, "/v1/video/queue", strings.Replace(body, "10s视频", "different", 1), "h3routeradmin")
	assert.Equal(t, http.StatusConflict, conflict.Code)
	foreign := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routermember")
	assert.Equal(t, http.StatusNotFound, foreign.Code)
	assert.NotContains(t, foreign.Body.String(), item.TaskID)
	for range 2 {
		canceled := f.request(t, http.MethodPost, "/v1/video/queue/"+item.QueueID+"/cancel", "", "h3routeradmin")
		require.Equal(t, 200, canceled.Code, canceled.Body.String())
		assert.Contains(t, canceled.Body.String(), `"state":"canceled"`)
	}
	summary, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, summary.Dispatched)
	assert.Zero(t, f.posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
}

func TestVideoQueueRealProtocolDispatchThirtyMinutePendingAndResume(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 2, `{"model":"MiniMax-H3","prompt":"free long running fixture","seconds":10}`)
	summary, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, summary.Dispatched)
	require.Equal(t, 1, f.posts)
	task := f.task(t, item.TaskID)
	require.NotEmpty(t, task.PrivateData.UpstreamTaskID)
	assert.Equal(t, 791, task.PrivateData.TokenId)
	assert.NotEmpty(t, task.PrivateData.Key)
	assert.EqualValues(t, 10, f.bodies[0]["duration"])
	var persisted model.VideoQueueItem
	require.NoError(t, f.db.First(&persisted, item.ID).Error)
	assert.True(t, persisted.SendFenced)
	assert.Equal(t, model.VideoQueueRunning, persisted.State)
	denied := f.request(t, http.MethodPost, "/v1/video/queue/"+item.QueueID+"/cancel", "", "h3routeradmin")
	assert.Equal(t, http.StatusConflict, denied.Code)
	// The provider still says queued after 31 minutes. Observation neither
	// times out the task nor creates another paid request.
	constant.TaskTimeoutMinutes = 1440
	require.NoError(t, f.db.Model(&model.Task{}).Where("id = ?", task.ID).Update("submit_time", time.Now().Add(-31*time.Minute).Unix()).Error)
	service.RunTaskPollingOnce(context.Background(), nil)
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	w := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"state":"running"`)
	assert.Equal(t, 1, f.posts)
	assert.Equal(t, 1, f.occupied(t))
	// Re-registering the routing layer simulates a process reconnect with no
	// in-memory queue state. The durable item still resolves the same task.
	SetVideoQueueRouter(gin.New())
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, f.posts)
}

func TestVideoQueueCapacityUsesOriginalSlotsWithoutChargingWaiters(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	for index := 10; index < 15; index++ {
		enqueueQueueFixture(t, f, index, `{"model":"MiniMax-H3","prompt":"free bounded fixture","seconds":5}`)
	}
	summary, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 4, summary.Dispatched)
	assert.Equal(t, 4, f.posts)
	assert.Equal(t, 4, f.occupied(t))
	beforeWallet, beforeToken := f.balance(t)
	summary, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.WaitingCapacity)
	assert.Equal(t, 4, f.posts)
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
	var waiting model.VideoQueueItem
	require.NoError(t, f.db.Where("queue_id = ?", "vq_00000000-0000-4000-8000-000000000014").First(&waiting).Error)
	assert.Equal(t, model.VideoQueueQueued, waiting.State)
	assert.False(t, waiting.SendFenced)
	var tasks int64
	require.NoError(t, f.db.Model(&model.Task{}).Where("task_id = ?", waiting.TaskID).Count(&tasks).Error)
	assert.Zero(t, tasks)
	// Only actual provider terminal evidence releases the occupied slots.
	f.queryMode = "failed"
	service.RunTaskPollingOnce(context.Background(), nil)
	assert.Zero(t, f.occupied(t))
	require.NoError(t, f.db.Model(&model.VideoQueueItem{}).Where("id = ?", waiting.ID).Update("next_attempt_at", 0).Error)
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, f.posts)
	assert.NotEmpty(t, f.task(t, waiting.TaskID).PrivateData.UpstreamTaskID)
}

func TestVideoQueueUnknownAndInterruptedDispatchNeverRepeatPOST(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	f.postMode = "malformed"
	item := enqueueQueueFixture(t, f, 20, `{"model":"MiniMax-H3","prompt":"free unknown fixture","seconds":5}`)
	_, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, f.posts)
	for range 2 {
		_, err = service.RunVideoQueueOnce(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, f.posts)
	assert.Equal(t, 1, f.occupied(t))
	w := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
	assert.Contains(t, w.Body.String(), `"submission_state":"unknown"`)
	second := enqueueQueueFixture(t, f, 21, `{"model":"MiniMax-H3","prompt":"free crash fixture","seconds":5}`)
	claimed, err := model.ClaimVideoQueueItem(context.Background())
	require.NoError(t, err)
	require.Equal(t, second.QueueID, claimed.QueueID)
	SetVideoQueueRouter(gin.New())
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, f.posts, "restart after dispatch claim never causes a new provider POST")
	assert.Equal(t, http.StatusConflict, f.request(t, http.MethodPost, "/v1/video/queue/"+second.QueueID+"/cancel", "", "h3routeradmin").Code)
	// A different crash boundary, before Bind/Acquire, is independently proven
	// unsent. Revoking that attempt atomically makes a late old worker fail.
	require.NoError(t, f.db.Model(&model.VideoQueueItem{}).Where("id = ?", claimed.ID).Update("dispatch_at", time.Now().Unix()-121).Error)
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	lateTask := &model.Task{TaskID: claimed.TaskID, UserId: claimed.UserID, Properties: model.Properties{OriginModelName: claimed.Model}}
	assert.ErrorIs(t, model.BindVideoQueueTask(context.Background(), claimed.QueueID, claimed.AttemptID, lateTask), model.ErrVideoQueueState)
	var recovered model.VideoQueueItem
	require.NoError(t, f.db.First(&recovered, claimed.ID).Error)
	assert.Equal(t, model.VideoQueueQueued, recovered.State)
	assert.Empty(t, recovered.AttemptID)
	assert.Equal(t, 1, f.posts)
}

func TestVideoQueueFreshIdentityRevocationAtDispatch(t *testing.T) {
	for _, branch := range []string{"token_disabled", "token_expired", "token_quota", "token_deleted", "user_disabled", "user_deleted", "token_owner", "token_group", "token_ip", "model_limit"} {
		t.Run(branch, func(t *testing.T) {
			f := newVideoQueueFixture(t, 1)
			item := enqueueQueueFixture(t, f, 30, `{"model":"MiniMax-H3","prompt":"must not send revoked request","seconds":5}`)
			switch branch {
			case "token_disabled":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("status", common.TokenStatusDisabled).Error)
			case "token_expired":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("expired_time", time.Now().Unix()-1).Error)
			case "token_quota":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("remain_quota", 0).Error)
			case "token_deleted":
				require.NoError(t, f.db.Delete(&model.Token{}, 791).Error)
			case "user_disabled":
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("status", common.UserStatusDisabled).Error)
			case "user_deleted":
				require.NoError(t, f.db.Delete(&model.User{}, 791).Error)
			case "token_owner":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("user_id", 792).Error)
			case "token_group":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("group", "not-allowed-group").Error)
			case "token_ip":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("allow_ips", "10.0.0.1").Error)
			case "model_limit":
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "other-model"}).Error)
			}
			_, err := service.RunVideoQueueOnce(context.Background())
			require.NoError(t, err)
			assert.Zero(t, f.posts)
			assert.Zero(t, f.occupied(t))
			var persisted model.VideoQueueItem
			require.NoError(t, f.db.First(&persisted, item.ID).Error)
			assert.Equal(t, model.VideoQueueFailed, persisted.State)
			assert.Empty(t, persisted.RequestBody)
		})
	}
}

func TestVideoQueueBoundsFairnessAndTerminalCAS(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	for index, request := range []string{`{"model":"MiniMax-H3","seconds":0}`, `{"model":"MiniMax-H3","seconds":3601}`, `{"model":"MiniMax-H3","seconds":10,"n":10}`, `{"model":"MiniMax-H3","metadata":{"count":2}}`, `{"model":"MiniMax-H3","image":"data:image/png;base64,xx"}`, `{"model":"MiniMax-H3","authorization":"do-not-save"}`, `{"model":"MiniMax-H3","input_reference":"ma_unowned_local_reference"}`, `{"model":"MiniMax-H3","image_url":"C:\\private\\fixture.png"}`, `{"model":"MiniMax-H3","__yunshu_files":[{"path":"/tmp/fixture"}]}`, `{"model":"MiniMax-H3","metadata":{"image_url":{"b64_json":"xx"}}}`} {
		id := fmt.Sprintf("vq_00000000-0000-4000-8000-%012d", 100+index)
		body := fmt.Sprintf(`{"schema_version":1,"queue_id":%q,"operation_id":"fixture-operation-%d","request":%s}`, id, 100+index, request)
		assert.Equal(t, http.StatusBadRequest, f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routeradmin").Code)
	}
	first := enqueueQueueFixture(t, f, 200, `{"model":"MiniMax-H3","seconds":5}`)
	second := enqueueQueueFixture(t, f, 201, `{"model":"MiniMax-H3","seconds":5}`)
	other := &model.VideoQueueItem{QueueID: "vq_00000000-0000-4000-8000-000000000202", UserID: 792, TokenID: 792, TokenFingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte("h3routermember"))), OperationID: "fixture-operation-202", BodySHA256: first.BodySHA256, RequestBody: first.RequestBody, ClientIP: first.ClientIP, Model: first.Model, Count: 1, DurationSeconds: 5, TaskID: model.GenerateTaskID()}
	_, _, err := model.EnqueueVideo(context.Background(), other)
	require.NoError(t, err)
	claim1, err := model.ClaimVideoQueueItem(context.Background())
	require.NoError(t, err)
	claim2, err := model.ClaimVideoQueueItem(context.Background())
	require.NoError(t, err)
	claim3, err := model.ClaimVideoQueueItem(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first.QueueID, claim1.QueueID)
	assert.Equal(t, other.QueueID, claim2.QueueID)
	assert.Equal(t, second.QueueID, claim3.QueueID)
	stale := *claim1
	require.NoError(t, model.UpdateVideoQueueState(context.Background(), claim1, model.VideoQueueSucceeded, "accepted", ""))
	require.NoError(t, model.UpdateVideoQueueState(context.Background(), &stale, model.VideoQueueRunning, "accepted", ""))
	assert.Equal(t, model.VideoQueueSucceeded, stale.State, "stale GET cannot regress a terminal job")
	assert.ErrorIs(t, model.UpdateVideoQueueState(context.Background(), &stale, model.VideoQueueFailed, "accepted", ""), model.ErrVideoQueueState)
	_, err = model.ClaimVideoQueueItem(context.Background())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Zero(t, f.posts)
}

func TestVideoQueueMigrationTwiceAndDatabaseReopenPreserveIdentity(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 300, `{"model":"MiniMax-H3","prompt":"migration retained request","seconds":10}`)
	for range 2 {
		require.NoError(t, f.db.AutoMigrate(&model.VideoQueueItem{}, &model.VideoQueueGuard{}))
	}
	previous := model.DB
	require.NoError(t, model.InitDB())
	reopened := model.DB
	t.Cleanup(func() {
		if connection, err := reopened.DB(); err == nil {
			_ = connection.Close()
		}
		model.DB = previous
	})
	restored, err := model.GetVideoQueueItem(context.Background(), item.UserID, item.QueueID)
	require.NoError(t, err)
	assert.Equal(t, *item, *restored)
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, f.posts, "reopened queued operation dispatches once")
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, f.posts)
}

func TestVideoQueueUserBoundRejectsBeforeCharge(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	for index := 0; index < model.VideoQueueUserLimit; index++ {
		enqueueQueueFixture(t, f, 400+index, `{"model":"MiniMax-H3","seconds":5}`)
	}
	w := f.request(t, http.MethodPost, "/v1/video/queue", `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000000999","operation_id":"fixture-operation-999","request":{"model":"MiniMax-H3","seconds":5}}`, "h3routeradmin")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "video_queue_full")
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
}

func TestVideoQueueWarmCachePermissionsAndExhaustedRecovery(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 700, `{"model":"MiniMax-H3","seconds":5}`)
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient; _ = client.Close() })
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("auto_groups", `["stale-fixture-group"]`).Error)
	_, err := model.GetTokenByKey("h3routeradmin", true)
	require.NoError(t, err)
	for _, current := range []string{"", "[]"} {
		require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("auto_groups", current).Error)
		engine := gin.New()
		require.NoError(t, engine.SetTrustedProxies(nil))
		engine.GET("/identity", middleware.TokenAuth(), func(c *gin.Context) {
			stale, exists := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
			require.True(t, exists)
			assert.Equal(t, []string{"stale-fixture-group"}, stale)
			c.Next()
		}, middleware.FreshVideoQueueIdentity(), func(c *gin.Context) {
			assert.Equal(t, service.GetUserAutoGroup("default"), service.GetRequestAutoGroups(c, "default"))
			value, _ := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
			assert.NotEqual(t, []string{"stale-fixture-group"}, value)
			c.Status(204)
		})
		request := httptest.NewRequest(http.MethodGet, "/identity", nil)
		request.Header.Set("Authorization", "Bearer h3routeradmin")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, 204, response.Code, response.Body.String())
	}
	common.RedisEnabled = false
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Updates(map[string]any{"remain_quota": 0, "expired_time": time.Now().Unix() - 1, "status": common.TokenStatusExhausted}).Error)
	get := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
	require.Equal(t, 200, get.Code, get.Body.String())
	cancel := f.request(t, http.MethodPost, "/v1/video/queue/"+item.QueueID+"/cancel", "", "h3routeradmin")
	require.Equal(t, 200, cancel.Code, cancel.Body.String())
	assert.Contains(t, cancel.Body.String(), `"state":"canceled"`)
	assert.Zero(t, f.posts)
	common.RedisEnabled = true
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Updates(map[string]any{"key": "rotatedqueuefixture", "remain_quota": 1000000, "expired_time": -1, "status": common.TokenStatusEnabled}).Error)
	for _, methodPath := range [][2]string{{http.MethodGet, "/v1/video/queue/" + item.QueueID}, {http.MethodPost, "/v1/video/queue/" + item.QueueID + "/cancel"}} {
		old := f.request(t, methodPath[0], methodPath[1], "", "h3routeradmin")
		assert.Equal(t, http.StatusForbidden, old.Code, "a warm old token hash cannot authorize a rotated key")
		current := f.request(t, methodPath[0], methodPath[1], "", "rotatedqueuefixture")
		require.Equal(t, http.StatusOK, current.Code, current.Body.String())
	}
}

func TestVideoQueuePerKeyCapacityAndNoTaskOnFull(t *testing.T) {
	f := newVideoQueueFixture(t, 4)
	for index := 0; index < 17; index++ {
		enqueueQueueFixture(t, f, 800+index, `{"model":"MiniMax-H3","seconds":5,"prompt":"free four-account fixture"}`)
	}
	for range 4 {
		_, err := service.RunVideoQueueOnce(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 16, f.posts)
	assert.Equal(t, 16, f.occupied(t), "four independent credentials retain four atomic slots each")
	summary, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.WaitingCapacity)
	assert.Equal(t, 16, f.posts)
	var taskCount int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&taskCount).Error)
	assert.EqualValues(t, 16, taskCount)
}

func TestVideoQueueHourlyCreationBudgetAndDisabledFeature(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	var records []model.VideoQueueItem
	for index := range model.VideoQueueHourlyCreateLimit {
		records = append(records, model.VideoQueueItem{QueueID: fmt.Sprintf("vq_00000000-0000-4000-8000-%012d", 10000+index), UserID: 791, TokenID: 791, OperationID: fmt.Sprintf("fixture-hourly-%06d", index), TaskID: model.GenerateTaskID(), Model: "MiniMax-H3", Count: 1, State: model.VideoQueueCanceled, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	}
	require.NoError(t, f.db.Create(&records).Error)
	body := `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000001000","operation_id":"fixture-operation-1000","request":{"model":"MiniMax-H3","seconds":5}}`
	w := f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routeradmin")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "video_queue_create_rate_exhausted")
	// The budget is per account. One user's canceled requests cannot exhaust
	// the admission budget for an unrelated user.
	w = f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routermember")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	constant.UpdateTask = false
	w = f.request(t, http.MethodPost, "/v1/video/queue", strings.ReplaceAll(body, "001000", "001001"), "h3routermember")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "video_queue_feature_disabled")
	assert.NotContains(t, w.Body.String(), `"accepted":true`)
	w = f.request(t, http.MethodGet, "/v1/video/queue/vq_00000000-0000-4000-8000-000000001000", "", "h3routermember")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = f.request(t, http.MethodPost, "/v1/video/queue/vq_00000000-0000-4000-8000-000000001000/cancel", "", "h3routermember")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Zero(t, f.posts)
}

func TestVideoQueueClientSourceSurvivesRestartWithoutForwardingHeaders(t *testing.T) {
	for _, declaration := range []string{"desktop", "absent", "duplicate"} {
		t.Run(declaration, func(t *testing.T) {
			f := newVideoQueueFixture(t, 1)
			request := httptest.NewRequest(http.MethodPost, "/v1/video/queue", strings.NewReader(`{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000009000","operation_id":"fixture-operation-9000","request":{"model":"MiniMax-H3","seconds":5,"prompt":"free source provenance fixture"}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer h3routeradmin")
			if declaration != "absent" {
				request.Header.Add("X-Yunshu-Client", "desktop")
			}
			if declaration == "duplicate" {
				request.Header.Add("x-yunshu-client", "desktop")
			}
			response := httptest.NewRecorder()
			f.engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
			item, err := model.GetVideoQueueItem(context.Background(), 791, "vq_00000000-0000-4000-8000-000000009000")
			require.NoError(t, err)
			expected := ""
			if declaration == "desktop" {
				expected = "desktop"
			}
			assert.Equal(t, expected, item.ClientSource)
			assert.Empty(t, request.Header.Values("X-Yunshu-Client"), "diagnostic declaration is consumed before hooks")
			SetVideoQueueRouter(gin.New())
			_, err = service.RunVideoQueueOnce(context.Background())
			require.NoError(t, err)
			task := f.task(t, item.TaskID)
			require.NotNil(t, task.PrivateData.Execution)
			assert.Equal(t, expected, task.PrivateData.Execution.ClientSource)
			assert.Equal(t, 1, f.posts)
		})
	}
}

func TestVideoQueueOpaqueOperationIDsAreCaseSensitiveAcrossDatabases(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	first := enqueueQueueFixture(t, f, 9100, `{"model":"MiniMax-H3","seconds":5}`)
	second := *first
	second.ID, second.QueueID, second.TaskID = 0, "vq_00000000-0000-4000-8000-000000009101", model.GenerateTaskID()
	second.OperationID = strings.ToUpper(first.OperationID)
	result, created, err := model.EnqueueVideo(context.Background(), &second)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, first.OperationHash, result.OperationHash)
	assert.Equal(t, second.OperationID, result.OperationID)
	assert.Zero(t, f.posts)
}

func TestVideoQueueConcurrentClaimAndSendFence(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 9500, `{"model":"MiniMax-H3","seconds":5}`)
	start := make(chan struct{})
	results := make(chan *model.VideoQueueItem, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			claimed, err := model.ClaimVideoQueueItem(context.Background())
			if err != nil {
				errors <- err
				return
			}
			results <- claimed
		})
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	var successful []*model.VideoQueueItem
	for result := range results {
		successful = append(successful, result)
	}
	for err := range errors {
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	require.Len(t, successful, 1)
	claimed := successful[0]
	assert.Equal(t, item.QueueID, claimed.QueueID)
	task := &model.Task{TaskID: claimed.TaskID, UserId: claimed.UserID, Properties: model.Properties{OriginModelName: claimed.Model}}
	require.NoError(t, model.BindVideoQueueTask(context.Background(), claimed.QueueID, claimed.AttemptID, task))
	require.NoError(t, model.BindVideoQueueTask(context.Background(), claimed.QueueID, claimed.AttemptID, task), "same-attempt bind is idempotent on MySQL changed-row semantics")
	task.ID = 123
	require.NoError(t, model.FenceVideoQueueSend(context.Background(), claimed.QueueID, claimed.AttemptID, task))
	assert.ErrorIs(t, model.FenceVideoQueueSend(context.Background(), claimed.QueueID, claimed.AttemptID, task), model.ErrVideoQueueState)
	assert.ErrorIs(t, model.CancelVideoQueueItem(context.Background(), claimed.UserID, claimed.QueueID), model.ErrVideoQueueState)
	assert.Zero(t, f.posts)
}

func TestVideoQueueClientSerializerHTTPInterop(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	body := []byte(`{"schema_version":1,"queue_id":"vq_924d7dc3-dd66-4e9e-a459-2c928102070c","operation_id":"924d7dc3-dd66-4e9e-a459-2c928102070c","count":1,"request":{"model":"MiniMax-H3","prompt":"synthetic free contract landscape","seconds":"10"}}`)
	if source := os.Getenv("TEST_VIDEO_QUEUE_CLIENT_REQUEST"); source != "" {
		var err error
		body, err = os.ReadFile(source)
		require.NoError(t, err)
		t.Logf("Actual client serializer body SHA256 %x", sha256.Sum256(body))
	}
	var request struct {
		QueueID string `json:"queue_id"`
	}
	require.NoError(t, common.Unmarshal(body, &request))
	require.NotEmpty(t, request.QueueID)
	beforeWallet, beforeToken := f.balance(t)
	ack := f.request(t, http.MethodPost, "/v1/video/queue", string(body), "h3routeradmin")
	require.Equal(t, http.StatusAccepted, ack.Code, ack.Body.String())
	assert.Contains(t, ack.Body.String(), `"accepted":true`)
	assert.Contains(t, ack.Body.String(), `"duration_seconds":10`)
	get := f.request(t, http.MethodGet, "/v1/video/queue/"+request.QueueID, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	assert.Contains(t, get.Body.String(), `"state":"queued"`)
	list := f.request(t, http.MethodGet, "/v1/video/queue?after=0&limit=1", "", "h3routeradmin")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var page struct {
		NextAfter int64 `json:"next_after"`
	}
	require.NoError(t, common.Unmarshal(list.Body.Bytes(), &page))
	require.Greater(t, page.NextAfter, int64(0))
	after := f.request(t, http.MethodGet, fmt.Sprintf("/v1/video/queue?after=%d&limit=1", page.NextAfter), "", "h3routeradmin")
	require.Equal(t, http.StatusOK, after.Code, after.Body.String())
	assert.Contains(t, after.Body.String(), `"has_more":false`)
	cancel := f.request(t, http.MethodPost, "/v1/video/queue/"+request.QueueID+"/cancel", "", "h3routeradmin")
	require.Equal(t, http.StatusOK, cancel.Code, cancel.Body.String())
	canceled := f.request(t, http.MethodGet, "/v1/video/queue/"+request.QueueID, "", "h3routeradmin")
	require.Equal(t, http.StatusOK, canceled.Code, canceled.Body.String())
	assert.Contains(t, canceled.Body.String(), `"state":"canceled"`)
	foreign := f.request(t, http.MethodGet, "/v1/video/queue/"+request.QueueID, "", "h3routermember")
	require.Equal(t, http.StatusNotFound, foreign.Code)
	assert.NotContains(t, foreign.Body.String(), request.QueueID)
	if directory := os.Getenv("TEST_VIDEO_QUEUE_FIXTURE_OUTPUT"); directory != "" {
		require.NoError(t, os.MkdirAll(directory, 0700))
		for name, response := range map[string]*httptest.ResponseRecorder{"ack": ack, "get": get, "cancel": cancel, "canceled-get": canceled, "list": list, "not-found": foreign} {
			path := filepath.Join(directory, "API-queue-"+name+".json")
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			require.NoError(t, err, "never overwrite prior HTTP evidence")
			_, err = file.Write(response.Body.Bytes())
			require.NoError(t, err)
			require.NoError(t, file.Close())
		}
	}
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
	var tasks int64
	require.NoError(t, f.db.Model(&model.Task{}).Count(&tasks).Error)
	assert.Zero(t, tasks)
}

func TestVideoQueueCredentialRotationRevokesQueuedAuthorization(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 9800, `{"model":"MiniMax-H3","seconds":5,"prompt":"must remain unsent after credential rotation"}`)
	toCancel := enqueueQueueFixture(t, f, 9801, `{"model":"MiniMax-H3","seconds":5,"prompt":"new credential may cancel original waiting item"}`)
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient; _ = client.Close() })
	_, err := model.GetTokenByKey("h3routeradmin", true)
	require.NoError(t, err)
	beforeWallet, beforeToken := f.balance(t)
	require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("key", "rotatedqueueauthorization").Error)
	old := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
	assert.Equal(t, http.StatusForbidden, old.Code)
	canceled := f.request(t, http.MethodPost, "/v1/video/queue/"+toCancel.QueueID+"/cancel", "", "rotatedqueueauthorization")
	require.Equal(t, http.StatusOK, canceled.Code, canceled.Body.String())
	_, err = service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	current := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "rotatedqueueauthorization")
	require.Equal(t, http.StatusOK, current.Code, current.Body.String())
	assert.Contains(t, current.Body.String(), `"state":"failed"`)
	assert.Contains(t, current.Body.String(), `"submission_state":"not_submitted"`)
	assert.Contains(t, current.Body.String(), "video_queue_authorization_revoked")
	assert.NotContains(t, current.Body.String(), item.TokenFingerprint)
	assert.NotContains(t, current.Body.String(), "rotatedqueueauthorization")
	afterWallet, afterToken := f.balance(t)
	assert.Equal(t, beforeWallet, afterWallet)
	assert.Equal(t, beforeToken, afterToken)
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
}

// Use the same offline usage expression for enqueue estimates and dispatch.
func videoQueueFailurePricing(t *testing.T) {
	t.Helper()
	previous := config.GlobalConfig.ExportAllConfigs()
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		billing_setting.PluginBillingExprOption: `{"hailuo::MiniMax-H3":"u(\"seconds\") * 0.01"}`,
		"billing_setting.billing_mode":          `{}`, "billing_setting.billing_expr": `{}`,
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(previous)) })
}

func TestVideoQueueInsufficientBalanceRejectedBeforeAcceptance(t *testing.T) {
	for _, source := range []string{"wallet", "token"} {
		t.Run(source, func(t *testing.T) {
			f := newVideoQueueFixture(t, 1)
			videoQueueFailurePricing(t)
			if source == "wallet" {
				require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 1).Error)
			} else {
				require.NoError(t, f.db.Model(&model.Token{}).Where("id = ?", 791).Update("remain_quota", 1).Error)
			}
			wallet, token := f.balance(t)
			w := f.request(t, http.MethodPost, "/v1/video/queue", `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000000701","operation_id":"failure-balance-operation","request":{"model":"MiniMax-H3","prompt":"balance fixture","seconds":5}}`, "h3routeradmin")
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			code := "insufficient_user_quota"
			if source == "token" {
				code = "pre_consume_token_quota_failed"
			}
			assert.Contains(t, w.Body.String(), code)
			assert.Contains(t, w.Body.String(), `"accepted":false`)
			assert.Contains(t, w.Body.String(), `"submission_state":"not_submitted"`)
			for _, table := range []any{&model.VideoQueueItem{}, &model.Task{}, &model.H3Admission{}} {
				var count int64
				require.NoError(t, f.db.Model(table).Count(&count).Error)
				assert.Zero(t, count)
			}
			currentWallet, currentToken := f.balance(t)
			assert.Equal(t, wallet, currentWallet)
			assert.Equal(t, token, currentToken)
			assert.Zero(t, f.posts)
			assert.Zero(t, f.occupied(t))
		})
	}
}

func TestVideoQueueAcceptedOperationStillResolvesAfterBalanceSpent(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	videoQueueFailurePricing(t)
	item := enqueueQueueFixture(t, f, 702, `{"model":"MiniMax-H3","prompt":"accepted operation fixture","seconds":5}`)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 0).Error)
	body := `{"schema_version":1,"queue_id":"vq_00000000-0000-4000-8000-000000000702","operation_id":"fixture-operation-702","request":{"model":"MiniMax-H3","prompt":"accepted operation fixture","seconds":5}}`
	w := f.request(t, http.MethodPost, "/v1/video/queue", body, "h3routeradmin")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), item.TaskID)
	conflict := f.request(t, http.MethodPost, "/v1/video/queue", strings.Replace(body, "accepted operation fixture", "changed operation", 1), "h3routeradmin")
	assert.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	assert.Zero(t, f.posts)
}

func TestVideoQueueDispatchFailureRetainsSpecificBalanceReason(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	videoQueueFailurePricing(t)
	item := enqueueQueueFixture(t, f, 703, `{"model":"MiniMax-H3","prompt":"balance changed during queue","seconds":5}`)
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Update("quota", 1).Error)
	wallet, token := f.balance(t)
	_, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	task := f.task(t, item.TaskID)
	assert.EqualValues(t, model.TaskStatusFailure, task.Status)
	assert.Equal(t, "Insufficient account balance or subscription quota to submit this task", task.FailReason)
	var admission model.H3Admission
	require.NoError(t, f.db.Where("task_db_id = ?", task.ID).First(&admission).Error)
	assert.Equal(t, "not_charged", admission.BillingState)
	assert.Zero(t, admission.SendingAt)
	for range 2 {
		w := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"failure_code":"insufficient_user_quota"`)
		assert.Contains(t, w.Body.String(), "Insufficient account balance or subscription quota to submit this task")
		assert.Contains(t, w.Body.String(), `"error_code":"h3_submission_not_sent"`, "the existing submission-receipt contract remains compatible")
	}
	currentWallet, currentToken := f.balance(t)
	assert.Equal(t, wallet, currentWallet)
	assert.Equal(t, token, currentToken)
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
}

func TestVideoQueuePreSendInvalidParametersHaveSpecificReason(t *testing.T) {
	f := newVideoQueueFixture(t, 1)
	item := enqueueQueueFixture(t, f, 704, `{"model":"MiniMax-H3","prompt":"invalid duration fixture","seconds":5,"resolution":"not-a-resolution"}`)
	_, err := service.RunVideoQueueOnce(context.Background())
	require.NoError(t, err)
	for range 2 {
		w := f.request(t, http.MethodGet, "/v1/video/queue/"+item.QueueID, "", "h3routeradmin")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"state":"failed"`)
		assert.Contains(t, w.Body.String(), `"failure_code":"invalid_request"`)
		assert.Contains(t, w.Body.String(), "Invalid task request parameters")
	}
	assert.Zero(t, f.posts)
	assert.Zero(t, f.occupied(t))
}

func TestVideoQueueSubscriptionFundingIsNotRejectedByWalletBalance(t *testing.T) {
	for _, preference := range []string{"subscription_only", "subscription_first", "wallet_first"} {
		t.Run(preference, func(t *testing.T) {
			f := newVideoQueueFixture(t, 1)
			videoQueueFailurePricing(t)
			require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", 791).Updates(map[string]any{"quota": 1, "setting": fmt.Sprintf(`{"billing_preference":%q}`, preference)}).Error)
			require.NoError(t, f.db.Create(&model.UserSubscription{UserId: 791, Status: "active", EndTime: time.Now().Unix() + 3600}).Error)
			enqueueQueueFixture(t, f, 705, `{"model":"MiniMax-H3","prompt":"subscription fixture","seconds":5}`)
			assert.Zero(t, f.posts)
			assert.Zero(t, f.occupied(t))
			wallet, _ := f.balance(t)
			assert.Equal(t, 1, wallet)
		})
	}
}
