package controller

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientActivityDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("ACTIVITY_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("local activity database DSN is not configured")
			}
			for _, mode := range []string{"fresh", "upgrade"} {
				t.Run(mode, func(t *testing.T) {
					db, _ := newAuditTestDatabase(t, kind, dsn)
					logDB, _ := newAuditTestDatabase(t, kind, dsn)
					typ := common.DatabaseTypeSQLite
					if kind == "mysql" {
						typ = common.DatabaseTypeMySQL
					} else if kind == "postgres" {
						typ = common.DatabaseTypePostgreSQL
					}
					useLogExportDatabases(t, db, logDB, typ, typ)
					var databaseVersion string
					versionQuery := "SELECT VERSION()"
					if kind == "sqlite" {
						versionQuery = "SELECT sqlite_version()"
					}
					require.NoError(t, db.Raw(versionQuery).Scan(&databaseVersion).Error)
					t.Logf("database version: %s", databaseVersion)
					// User and Log are unchanged released models. An upgrade starts
					// with existing account/log data and no presence table.
					require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
					require.NoError(t, logDB.AutoMigrate(&model.Log{}))
					if mode == "fresh" {
						require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}))
					}
					alice := model.User{Username: "activity-alice", AffCode: "activity-alice", Quota: 123456}
					bob := model.User{Username: "activity-bob", AffCode: "activity-bob", Quota: 654321}
					require.NoError(t, db.Create(&alice).Error)
					require.NoError(t, db.Create(&bob).Error)
					const now = int64(1800000000)
					logs := []model.Log{
						{UserId: alice.Id, ModelName: "deepseek-flash", CreatedAt: now + 20, Type: model.LogTypeConsume, Quota: 100},
						{UserId: alice.Id, ModelName: "MiniMax-H3", CreatedAt: now + 30, Type: model.LogTypeError, Content: "sensitive upstream detail"},
						{UserId: bob.Id, ModelName: "other-user-model", CreatedAt: now + 20, Type: model.LogTypeConsume},
						{UserId: alice.Id, ModelName: "outside-session", CreatedAt: now - 10, Type: model.LogTypeConsume},
					}
					require.NoError(t, logDB.Create(&logs).Error)
					require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}))
					beat := model.ClientActivityHeartbeat{SessionID: "c6fd6dce-e751-41a2-b3a9-868738e72061", Event: "start", Platform: "win32", AppVersion: "0.2.1-alpha.2", Foreground: true}
					session, err := model.RecordClientActivity(alice.Id, beat, now)
					require.NoError(t, err)
					require.Positive(t, session.ID)
					beat.Event = "heartbeat"
					session, err = model.RecordClientActivity(alice.Id, beat, now+30)
					require.NoError(t, err)
					assert.EqualValues(t, 30, session.OnlineSeconds)
					assert.EqualValues(t, 30, session.ForegroundSeconds)
					beat.Foreground = false
					session, err = model.RecordClientActivity(alice.Id, beat, now+40)
					require.NoError(t, err)
					assert.EqualValues(t, 40, session.ForegroundSeconds)
					session, err = model.RecordClientActivity(alice.Id, beat, now+70)
					require.NoError(t, err)
					assert.EqualValues(t, 70, session.OnlineSeconds)
					assert.EqualValues(t, 40, session.ForegroundSeconds)
					// A retry at the same server instant is idempotent; no client
					// duration or clock is accepted. A sleep gap is never charged.
					session, err = model.RecordClientActivity(alice.Id, beat, now+70)
					require.NoError(t, err)
					session, err = model.RecordClientActivity(alice.Id, beat, now+500)
					require.NoError(t, err)
					assert.EqualValues(t, 70, session.OnlineSeconds)
					view, usage, err := model.GetClientActivitySession(session.ID, now+500)
					require.NoError(t, err)
					assert.Equal(t, "background", view.Status)
					assert.EqualValues(t, 1, usage.Calls)
					assert.EqualValues(t, 1, usage.Errors)
					require.Len(t, usage.Models, 2)
					assert.Equal(t, "activity-alice", view.Username)
					view, _, err = model.GetClientActivitySession(session.ID, now+590)
					require.NoError(t, err)
					assert.Equal(t, "offline", view.Status)
					beat.Event = "stop"
					session, err = model.RecordClientActivity(alice.Id, beat, now+510)
					require.NoError(t, err)
					assert.EqualValues(t, 80, session.OnlineSeconds)
					assert.EqualValues(t, now+510, session.EndedAt)
					beat.Event, beat.Foreground = "heartbeat", true
					replayed, err := model.RecordClientActivity(alice.Id, beat, now+520)
					require.NoError(t, err)
					assert.Equal(t, session, replayed, "a delayed heartbeat must not reopen an ended process")
					other, err := model.RecordClientActivity(bob.Id, beat, now+520)
					require.NoError(t, err)
					assert.NotEqual(t, session.ID, other.ID, "a matching UUID cannot mutate another user's session")
					filter := model.ClientActivityFilter{Start: now, End: now + 600, Status: "all"}
					summary, err := model.GetClientActivitySummary(filter, now+520)
					require.NoError(t, err)
					assert.EqualValues(t, 1, summary.OnlineUsers)
					assert.EqualValues(t, 1, summary.ForegroundUsers)
					assert.EqualValues(t, 2, summary.StartedSessions)
					assert.EqualValues(t, 2, summary.ActiveUsers)
					assert.EqualValues(t, 80, summary.OnlineSeconds)
					assert.EqualValues(t, 2, summary.ModelCalls, "heartbeats cannot create model usage")
					assert.EqualValues(t, 1, summary.ModelErrors)
					filter.Username, filter.Status = alice.Username, "offline"
					items, total, err := model.ListClientActivitySessions(filter, 0, 10, now+520)
					require.NoError(t, err)
					assert.EqualValues(t, 1, total)
					require.Len(t, items, 1)
					assert.Equal(t, session.ID, items[0].ID)
					filter.Status = "foreground"
					items, total, err = model.ListClientActivitySessions(filter, 0, 10, now+520)
					require.NoError(t, err)
					assert.Zero(t, total)
					assert.Empty(t, items)
					// Repeat the actual new-model startup migration twice. Existing
					// accounts, model logs, unique indexes and session data survive.
					for range 2 {
						require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}))
					}
					assert.True(t, db.Migrator().HasIndex(&model.ClientActivitySession{}, "idx_client_activity_user_session"))
					duplicate := *session
					duplicate.ID = 0
					assert.Error(t, db.Create(&duplicate).Error)
					var savedUser model.User
					require.NoError(t, db.First(&savedUser, alice.Id).Error)
					assert.Equal(t, 123456, savedUser.Quota)
					var logCount int64
					require.NoError(t, logDB.Model(&model.Log{}).Count(&logCount).Error)
					assert.EqualValues(t, len(logs), logCount)
				})
			}
		})
	}
}

func TestClientActivityAuthenticationAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, legacyToken := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}))
	owner.Role = common.RoleCommonUser
	require.NoError(t, model.DB.Model(owner).Update("role", owner.Role).Error)
	_, jwt := createAccessTokenTestSession(t, owner.Id, "client-activity-login")
	router := gin.New()
	router.POST("/api/client-activity/heartbeat", middleware.UserAuth(), ClientActivityHeartbeat)
	router.GET("/api/client-activity/summary", middleware.AdminAuth(), GetClientActivitySummary)
	router.GET("/api/client-activity/sessions", middleware.AdminAuth(), GetClientActivitySessions)
	router.GET("/api/client-activity/sessions/:id", middleware.AdminAuth(), GetClientActivitySession)
	body := `{"session_id":"c6fd6dce-e751-41a2-b3a9-868738e72061","event":"heartbeat","foreground":true,"platform":"win32","app_version":"0.2.1","user_id":999,"online_seconds":999999,"timestamp":1}`
	response := accessTokenRequest(router, http.MethodPost, "/api/client-activity/heartbeat", jwt, "", body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var saved model.ClientActivitySession
	require.NoError(t, model.DB.First(&saved).Error)
	assert.Equal(t, owner.Id, saved.UserID)
	assert.Zero(t, saved.OnlineSeconds)
	assert.InDelta(t, time.Now().Unix(), saved.StartedAt, 2)
	for _, path := range []string{"/summary", "/sessions", "/sessions/1"} {
		response = accessTokenRequest(router, http.MethodGet, "/api/client-activity"+path, jwt, "", "")
		assert.Equal(t, http.StatusForbidden, response.Code, path)
	}
	response = accessTokenRequest(router, http.MethodPost, "/api/client-activity/heartbeat", legacyToken, "", body)
	assert.Equal(t, http.StatusForbidden, response.Code, "a valid legacy personal access token is not a desktop login")
	request := httptest.NewRequest(http.MethodPost, "/api/client-activity/heartbeat", strings.NewReader(body))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	for _, invalid := range []string{
		strings.Replace(body, "c6fd6dce-e751-41a2-b3a9-868738e72061", "device-fingerprint", 1),
		strings.Replace(body, `"heartbeat"`, `"unknown"`, 1),
		strings.Replace(body, `"win32"`, `"other"`, 1),
		strings.Replace(body, `"0.2.1"`, `"Authorization: Bearer private"`, 1),
		strings.Repeat("x", 2049),
	} {
		response = accessTokenRequest(router, http.MethodPost, "/api/client-activity/heartbeat", jwt, "", invalid)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.ClientActivitySession{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	admin := model.User{Username: "activity-administrator", AffCode: "activity-administrator", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&admin).Error)
	_, adminJWT := createAccessTokenTestSession(t, admin.Id, "client-activity-admin-login")
	for _, path := range []string{"/summary", "/sessions", "/sessions/1"} {
		response = accessTokenRequest(router, http.MethodGet, "/api/client-activity"+path, adminJWT, "", "")
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "refresh-")
	}
	for _, path := range []string{
		"/sessions?page_size=-1", "/sessions?p=-1", "/sessions?status=invalid",
		"/sessions?start_timestamp=1&end_timestamp=9999999999", "/summary?start_timestamp=invalid", "/sessions/invalid",
	} {
		response = accessTokenRequest(router, http.MethodGet, "/api/client-activity"+path, adminJWT, "", "")
		assert.Equal(t, http.StatusBadRequest, response.Code, path)
	}
	response = accessTokenRequest(router, http.MethodGet, "/api/client-activity/sessions/99999", adminJWT, "", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
	revoked, err := model.RevokeUserSession(owner.Id, "client-activity-login", "logout")
	require.NoError(t, err)
	require.True(t, revoked)
	response = accessTokenRequest(router, http.MethodPost, "/api/client-activity/heartbeat", jwt, "", body)
	assert.Equal(t, http.StatusUnauthorized, response.Code, "a logged-out JWT cannot keep a client online")
	var afterLogout model.ClientActivitySession
	require.NoError(t, model.DB.First(&afterLogout, saved.ID).Error)
	assert.Equal(t, saved.LastSeenAt, afterLogout.LastSeenAt)
}

// Integrate into controller/client_activity_test.go; imports: bytes, strings,
// github.com/QuantumNous/new-api/common and /model; testify require/assert.
func TestClientWorkloadSnapshotWire(t *testing.T) {
	const valid = `{"schema_version":1,"runtime_session_id":"c6fd6dce-e751-41a2-b3a9-868738e72061","sequence":1,"complete":true,"collections":{"chat_activity":"collected","project_run":"not_collected","storyboard_task":"not_collected","local_operation":"not_collected","shots":"not_collected"},"activities":[{"activity_id":"chat-1","kind":"chat_activity","work_state":"active","phase":"busy","chat_session_id":"chat-root"}],"shots":[]}`
	parsed, err := model.ParseClientWorkloadSnapshot([]byte(valid))
	require.NoError(t, err)
	assert.Equal(t, "chat-root", parsed.Activities[0].ChatSessionID)
	cases := []struct{ name, body string }{
		{"unknown root", strings.Replace(valid, `"sequence":1`, `"revision":1`, 1)},
		{"duplicate root", strings.Replace(valid, `"sequence":1`, `"sequence":1,"sequence":1`, 1)},
		{"escaped duplicate root", strings.Replace(valid, `"sequence":1`, `"sequence":1,"sequen\u0063e":1`, 1)},
		{"case folded root", strings.Replace(valid, `"sequence":1`, `"Sequence":1`, 1)},
		{"fractional sequence", strings.Replace(valid, `"sequence":1`, `"sequence":1.0`, 1)},
		{"unsafe sequence", strings.Replace(valid, `"sequence":1`, `"sequence":9007199254740992`, 1)},
		{"null root", strings.Replace(valid, `"shots":[]`, `"shots":null`, 1)},
		{"null row", strings.Replace(valid, `"shots":[]`, `"shots":[null]`, 1)},
		{"missing collection", strings.Replace(valid, `"project_run":"not_collected",`, ``, 1)},
		{"collection null", strings.Replace(valid, `"project_run":"not_collected"`, `"project_run":null`, 1)},
		{"collection escaped duplicate", strings.Replace(valid, `"shots":"not_collected"`, `"shots":"not_collected","shot\u0073":"not_collected"`, 1)},
		{"uncollected activity", strings.Replace(valid, `"chat_activity":"collected"`, `"chat_activity":"not_collected"`, 1)},
		{"activity escaped duplicate", strings.Replace(valid, `"phase":"busy"`, `"phase":"busy","ph\u0061se":"busy"`, 1)},
		{"unknown activity key", strings.Replace(valid, `"phase":"busy"`, `"phase":"busy","prompt":"no content"`, 1)},
		{"chat forbidden project", strings.Replace(valid, `"phase":"busy"`, `"phase":"busy","project_id":"project-1"`, 1)},
		{"active outcome", strings.Replace(valid, `"phase":"busy"`, `"phase":"busy","outcome":"succeeded"`, 1)},
		{"missing terminal outcome", strings.Replace(strings.Replace(valid, `"active"`, `"ended"`, 1), `"busy"`, `"ended"`, 1)},
		{"invalid opaque id", strings.Replace(valid, `"chat-root"`, `"../private"`, 1)},
		{"invalid optional empty", strings.Replace(valid, `"chat-root"`, `""`, 1)},
		{"null scalar", strings.Replace(valid, `"chat-root"`, `null`, 1)},
		{"uppercase runtime UUID", strings.Replace(valid, "c6fd6dce-e751-41a2-b3a9-868738e72061", "C6FD6DCE-E751-41A2-B3A9-868738E72061", 1)},
		{"oversize body", valid + strings.Repeat(" ", model.MaxClientWorkloadBodyBytes)},
		{"trailing JSON", valid + " {}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := model.ParseClientWorkloadSnapshot([]byte(tc.body))
			require.ErrorIs(t, err, model.ErrClientWorkloadInvalid)
		})
	}
	_, err = model.ParseClientWorkloadSnapshot(bytes.ReplaceAll([]byte(valid), []byte("chat-root"), []byte{0xff}))
	require.ErrorIs(t, err, model.ErrClientWorkloadInvalid)
}

func TestClientWorkloadSnapshotDigestSetOrdering(t *testing.T) {
	snapshot := model.ClientWorkloadSnapshot{
		SchemaVersion: 1, RuntimeSessionID: "c6fd6dce-e751-41a2-b3a9-868738e72061", Sequence: 1, Complete: true,
		Collections: model.ClientWorkloadCollections{ChatActivity: "not_collected", ProjectRun: "not_collected", StoryboardTask: "collected", LocalOperation: "not_collected", Shots: "collected"},
		Activities: []model.ClientWorkloadActivityInput{
			{ActivityID: "task-b", Kind: "storyboard_task", WorkState: "active", Phase: "submitting", AttemptID: "a4fc8581-0c91-4b9e-986e-7b37d547b82f", ShotIDs: []string{"shot-b", "shot-a"}},
			{ActivityID: "task-a", Kind: "storyboard_task", WorkState: "unknown", Phase: "unknown", AttemptID: "c6fd6dce-e751-41a2-b3a9-868738e72061"},
		},
		Shots: []model.ClientWorkloadShotInput{{ProjectID: "project-b", ShotID: "shot-b"}, {ProjectID: "project-a", ShotID: "shot-a"}},
	}
	before, err := common.Marshal(snapshot)
	require.NoError(t, err)
	first, err := snapshot.Digest()
	require.NoError(t, err)
	after, err := common.Marshal(snapshot)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "digest must not mutate caller arrays")
	snapshot.Activities[0], snapshot.Activities[1] = snapshot.Activities[1], snapshot.Activities[0]
	snapshot.Activities[1].ShotIDs[0], snapshot.Activities[1].ShotIDs[1] = snapshot.Activities[1].ShotIDs[1], snapshot.Activities[1].ShotIDs[0]
	snapshot.Shots[0], snapshot.Shots[1] = snapshot.Shots[1], snapshot.Shots[0]
	second, err := snapshot.Digest()
	require.NoError(t, err)
	assert.Equal(t, first, second)
	snapshot.Activities[0].WorkState = "ended"
	snapshot.Activities[0].Phase = "ended"
	snapshot.Activities[0].Outcome = "interrupted"
	changed, err := snapshot.Digest()
	require.NoError(t, err)
	assert.NotEqual(t, first, changed)
}

// Exact synthetic request produced by the client serializer; no credentials or customer content.
const clientWorkloadInteropBody = `{"schema_version":1,"runtime_session_id":"01234567-89ab-4cde-8fab-0123456789ab","sequence":1,"complete":true,"collections":{"chat_activity":"collected","project_run":"collected","storyboard_task":"collected","local_operation":"not_collected","shots":"not_collected"},"activities":[{"activity_id":"bfb35610-9ed7-4f3f-9d36-1c9bce3cfcf0","kind":"chat_activity","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","work_state":"active","phase":"busy"},{"activity_id":"b97ef4c8-35ce-4099-b7d4-b66c794a32c9","kind":"project_run","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5"},{"activity_id":"05ae8583-779f-403a-afca-d255263320ce","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000001-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_1ceb4611dc41b06ca948fc5f89cc0a1ab89d83e06361e4fc08893bd04d275499"]},{"activity_id":"6c4c33b8-6a12-49c7-928d-ef7350d8e96e","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000002-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_d7c288a0a2d9a95834fba03ecac022055764e52ac6b062019358e42f9aa8ee54"]},{"activity_id":"d2ea1bee-c53e-43e3-b93c-3db9996b9cb3","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000003-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_a97819054f1f9e04ca3910abc1f1e99e4404c42108c95c633e687ba02a6c888a"]},{"activity_id":"2208e8d8-ac4b-4c20-bf20-ff764b9cf384","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000004-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_73bc6365892df5565552264adb1f200430f731d1f0788c898ff425a764feb8c0"]},{"activity_id":"3ad0dc73-5d71-4591-94b2-529a20aed88a","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000005-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_59967f3e5cf198e4ef300ff1e410ca513188c61c80c6d9d03b32e165627a8602"]},{"activity_id":"0c88330d-d8bc-478c-9f51-81fa5b2dabc9","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000006-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_ef948cd44a5a9591954153cf8d1840910addc80b151b6dc807a1aeeae78a89c6"]},{"activity_id":"554d1864-6857-4354-977c-e575e5d4ee8e","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000007-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_f1af49a74cfb5689fad852a5b59611a7ef7a2b2af1bd2887735bc6bf8673dc0e"]},{"activity_id":"2d09b068-ec1d-460c-a5d3-344ea936ca99","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000008-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_0bdee722a623d3a4775daca7b48ae9b383e025fec30299feb1c06bd88c49cf93"]}],"shots":[]}`

func TestClientActivityLiveScopeAcrossMidnight(t *testing.T) {
	owner, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ClientActivitySession{}))
	const midnight = int64(1800057600)
	beat := model.ClientActivityHeartbeat{SessionID: "c6fd6dce-e751-41a2-b3a9-868738e72061", Event: "start", Platform: "win32", AppVersion: "1.0.0", Foreground: true}
	row, err := model.RecordClientActivity(owner.Id, beat, midnight-120)
	require.NoError(t, err)
	beat.Event = "heartbeat"
	_, err = model.RecordClientActivity(owner.Id, beat, midnight+30)
	require.NoError(t, err)
	old := model.ClientActivityFilter{Start: midnight, End: midnight + 60, Status: "online"}
	items, total, err := model.ListClientActivitySessions(old, 0, 10, midnight+60)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, items)
	old.Scope = "live"
	items, total, err = model.ListClientActivitySessions(old, 0, 10, midnight+60)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, row.ID, items[0].ID)
	assert.Equal(t, "live", items[0].ObservationDetail)
	old.Scope, old.TimeBasis = "history", "last_seen"
	_, total, err = model.ListClientActivitySessions(old, 0, 10, midnight+60)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	old.TimeBasis = "started"
	_, total, err = model.ListClientActivitySessions(old, 0, 10, midnight+60)
	require.NoError(t, err)
	assert.Zero(t, total)
	before := items[0].OnlineSeconds
	_, err = model.RecordClientActivity(owner.Id, beat, midnight-60)
	require.NoError(t, err)
	var saved model.ClientActivitySession
	require.NoError(t, model.DB.First(&saved, row.ID).Error)
	assert.Equal(t, midnight+30, saved.LastSeenAt)
	assert.Equal(t, before, saved.OnlineSeconds)
	// A server clock rollback must not make a future heartbeat live in cards
	// while the exact same runtime is absent from the live list.
	futureView, _, err := model.GetClientActivitySession(row.ID, midnight+10)
	require.NoError(t, err)
	assert.False(t, futureView.Online)
	assert.Equal(t, "expired", futureView.ObservationDetail)
	futureSummary, err := model.GetClientActivitySummary(model.ClientActivityFilter{Start: midnight - 3600, End: midnight + 10}, midnight+10)
	require.NoError(t, err)
	assert.Zero(t, futureSummary.OnlineUsers)
	assert.Zero(t, futureSummary.ForegroundUsers)
	assert.Zero(t, futureSummary.RuntimeSessions)
	for _, status := range []string{"online", "foreground", "background"} {
		_, total, err = model.ListClientActivitySessions(model.ClientActivityFilter{Start: midnight - 3600, End: midnight + 10, Status: status}, 0, 10, midnight+10)
		require.NoError(t, err)
		assert.Zero(t, total, status)
	}
	_, total, err = model.ListClientActivitySessions(model.ClientActivityFilter{Start: midnight - 3600, End: midnight + 10, Status: "offline"}, 0, 10, midnight+10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestClientWorkloadActualClientHTTPAndOrdering(t *testing.T) {
	network := denyClientWorkloadNetwork(t)
	owner, legacy := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}, &model.Task{}, &model.Token{}))
	_, jwt := createAccessTokenTestSession(t, owner.Id, "workload-interop-login")
	parsed, err := model.ParseClientWorkloadSnapshot([]byte(clientWorkloadFinalInteropBody))
	require.NoError(t, err)
	now := common.GetTimestamp()
	beat := model.ClientActivityHeartbeat{SessionID: parsed.RuntimeSessionID, Event: "start", Platform: "win32", AppVersion: "1024.1", Foreground: true}
	presence, err := model.RecordClientActivity(owner.Id, beat, now)
	require.NoError(t, err)
	router := gin.New()
	router.POST("/api/client-activity/workload-snapshot", middleware.UserAuth(), ClientActivityWorkloadSnapshot)
	router.GET("/api/client-activity/summary", middleware.AdminAuth(), middleware.RequirePermission(authz.UserActivityRead), GetClientActivitySummary)
	router.GET("/api/client-activity/sessions/:id", middleware.AdminAuth(), middleware.RequirePermission(authz.UserActivityRead), GetClientActivitySession)
	router.GET("/api/status", GetStatus)
	var fixtures []map[string]any
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		var safe map[string]any
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &safe))
		if path == "/api/status" {
			data, ok := safe["data"].(map[string]any)
			require.True(t, ok)
			safe = map[string]any{"success": safe["success"], "data": map[string]any{"client_activity_workload": data["client_activity_workload"]}}
		}
		fixtures = append(fixtures, map[string]any{"method": method, "path": path, "http_status": w.Code, "response": safe, "synthetic": true, "paid_requests": 0})
		return w
	}
	status := request("GET", "/api/status", "", "")
	require.Equal(t, http.StatusOK, status.Code)
	var publicStatus struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(status.Body.Bytes(), &publicStatus))
	assert.Equal(t, map[string]any{"schema_version": float64(1), "snapshot_path": "/api/client-activity/workload-snapshot", "heartbeat_interval_seconds": float64(30), "workload_timeout_seconds": float64(90)}, publicStatus.Data["client_activity_workload"])
	for _, token := range []string{"", legacy} {
		w := request("POST", "/api/client-activity/workload-snapshot", token, clientWorkloadFinalInteropBody)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, w.Code)
	}
	accepted := request("POST", "/api/client-activity/workload-snapshot", jwt, clientWorkloadFinalInteropBody)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	assert.Contains(t, accepted.Body.String(), `"disposition":"accepted"`)
	var initial model.ClientWorkloadRuntime
	require.NoError(t, model.DB.First(&initial).Error)
	duplicate := request("POST", "/api/client-activity/workload-snapshot", jwt, clientWorkloadFinalInteropBody)
	require.Equal(t, http.StatusOK, duplicate.Code, duplicate.Body.String())
	assert.Contains(t, duplicate.Body.String(), `"disposition":"duplicate"`)
	var after model.ClientWorkloadRuntime
	require.NoError(t, model.DB.First(&after).Error)
	assert.Equal(t, initial, after, "a duplicate never renews workload lease")
	conflict := strings.Replace(clientWorkloadFinalInteropBody, `"phase":"busy"`, `"phase":"unknown"`, 1)
	conflict = strings.Replace(conflict, `"work_state":"active"`, `"work_state":"unknown"`, 1)
	w := request("POST", "/api/client-activity/workload-snapshot", jwt, conflict)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "ACTIVITY_SEQUENCE_CONFLICT")
	w = request("GET", "/api/client-activity/summary", jwt, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var summary struct {
		Success bool                        `json:"success"`
		Data    model.ClientActivitySummary `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &summary))
	require.True(t, summary.Success)
	require.NotNil(t, summary.Data.Workload)
	assert.EqualValues(t, 1, summary.Data.Workload.Known.GeneratingChatSessions)
	assert.EqualValues(t, 1, summary.Data.Workload.Known.Projects)
	assert.EqualValues(t, 8, summary.Data.Workload.Known.TaskAttempts)
	assert.EqualValues(t, 4, summary.Data.Workload.Known.QueuedTasks)
	assert.EqualValues(t, 4, summary.Data.Workload.Known.GeneratingTasks)
	w = request("GET", fmt.Sprintf("/api/client-activity/sessions/%d", presence.ID), jwt, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "workload-interop-login")
	assert.NotContains(t, w.Body.String(), initial.PayloadDigest)
	var user model.User
	require.NoError(t, model.DB.First(&user, owner.Id).Error)
	assert.Equal(t, owner.Quota, user.Quota)
	var taskCount int64
	require.NoError(t, model.DB.Model(&model.Task{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount, "observation acceptance must not submit or create a task")
	require.NoError(t, model.DB.First(&presence, presence.ID).Error)
	assert.Equal(t, now, presence.LastSeenAt, "snapshot must not refresh presence")

	// New management read grants never gate ordinary own reporting.
	require.NoError(t, authz.SetUserPermissions(owner.Id, authz.PermissionsMap{"user_activity": {"read": false}}))
	assert.Equal(t, http.StatusForbidden, request("GET", "/api/client-activity/summary", jwt, "").Code)
	nextBody := strings.Replace(clientWorkloadFinalInteropBody, `"sequence":1`, `"sequence":2`, 1)
	require.Equal(t, http.StatusOK, request("POST", "/api/client-activity/workload-snapshot", jwt, nextBody).Code)
	pat := model.AccessTokenPrefix + strings.Repeat("w", 43)
	personal := model.UserAccessToken{UserId: owner.Id, Name: "workload-no-PAT", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
	require.NoError(t, personal.SetScopes([]string{"usage:read"}))
	require.NoError(t, model.DB.Create(&personal).Error)
	assert.Equal(t, http.StatusForbidden, request("POST", "/api/client-activity/workload-snapshot", pat, nextBody).Code)
	relayKey := strings.Repeat("r", 48)
	require.NoError(t, model.DB.Create(&model.Token{UserId: owner.Id, Key: relayKey, Name: "workload-no-relay", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}).Error)
	assert.Equal(t, http.StatusUnauthorized, request("POST", "/api/client-activity/workload-snapshot", "sk-"+relayKey, nextBody).Code)
	_, switchedJWT := createAccessTokenTestSession(t, owner.Id, "workload-switched-login")
	assert.Equal(t, http.StatusBadRequest, request("POST", "/api/client-activity/workload-snapshot", switchedJWT, nextBody).Code, "new login cannot adopt an existing reporter")
	member := model.User{Username: "workload-member", AffCode: "workload-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&member).Error)
	_, memberJWT := createAccessTokenTestSession(t, member.Id, "workload-member-login")
	_, err = model.RecordClientActivity(member.Id, beat, now)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, request("POST", "/api/client-activity/workload-snapshot", memberJWT, clientWorkloadFinalInteropBody).Code, "ordinary owner needs no admin permission")
	assert.Equal(t, http.StatusForbidden, request("GET", "/api/client-activity/summary", memberJWT, "").Code)
	// Capacity rejects only the snapshot, with no semantic row or lease update.
	var storedBudget model.ClientWorkloadBudget
	require.NoError(t, model.DB.Where(&model.ClientWorkloadBudget{Key: "global"}).Take(&storedBudget).Error)
	require.NoError(t, model.DB.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: "global"}).Update("reserved_bytes", model.MaxClientWorkloadGlobalBytes).Error)
	capacityInput, err := model.ParseClientWorkloadSnapshot([]byte(nextBody))
	require.NoError(t, err)
	capacityInput.Sequence = 3
	capacityInput.Activities = append(capacityInput.Activities, model.ClientWorkloadActivityInput{ActivityID: "capacity-http-id", Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "capacity-root"})
	capacityBody, err := common.Marshal(capacityInput)
	require.NoError(t, err)
	capacityResponse := request("POST", "/api/client-activity/workload-snapshot", jwt, string(capacityBody))
	require.Equal(t, http.StatusTooManyRequests, capacityResponse.Code, capacityResponse.Body.String())
	assert.Contains(t, capacityResponse.Body.String(), "ACTIVITY_SNAPSHOT_CAPACITY")
	require.NoError(t, model.DB.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: "global"}).Update("reserved_bytes", storedBudget.ReservedBytes).Error)
	revoked, err := model.RevokeUserSession(owner.Id, "workload-interop-login", "fixture logout")
	require.NoError(t, err)
	require.True(t, revoked)
	assert.Equal(t, http.StatusUnauthorized, request("POST", "/api/client-activity/workload-snapshot", jwt, nextBody).Code)
	if output := os.Getenv("CLIENT_WORKLOAD_HTTP_FIXTURE_OUTPUT"); output != "" {
		require.True(t, filepath.IsAbs(output))
		encoded, err := common.Marshal(map[string]any{"source_fixture_sha256": clientWorkloadFinalFixtureSHA, "source_body_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(clientWorkloadFinalInteropBody))), "fixtures": fixtures, "outbound_requests": network.calls.Load(), "paid_requests": 0, "synthetic": true})
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(output), 0700))
		require.NoError(t, os.WriteFile(output, encoded, 0600))
	}
}

func TestClientWorkloadPersistenceDatabaseMatrix(t *testing.T) {
	denyClientWorkloadNetwork(t)
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("ACTIVITY_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("local isolated activity database is not configured")
			}
			for _, mode := range []string{"fresh", "upgrade"} {
				t.Run(mode, func(t *testing.T) {
					db, _ := newAuditTestDatabase(t, kind, dsn)
					typ := common.DatabaseTypeSQLite
					if kind == "mysql" {
						typ = common.DatabaseTypeMySQL
					} else if kind == "postgres" {
						typ = common.DatabaseTypePostgreSQL
					}
					useLogExportDatabases(t, db, db, typ, typ)
					oldModels := []any{&model.User{}, &model.UserSession{}, &model.ClientActivitySession{}, &model.Task{}, &model.Log{}, &model.Option{}}
					require.NoError(t, db.AutoMigrate(oldModels...))
					var version string
					query := "SELECT version()"
					if kind == "sqlite" {
						query = "SELECT sqlite_version()"
					}
					require.NoError(t, db.Raw(query).Scan(&version).Error)
					t.Logf("workload %s %s: %s", kind, mode, version)
					now := common.GetTimestamp()
					owner := model.User{Username: "workload-owner", AffCode: "workload-owner", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 123456}
					foreign := model.User{Username: "workload-other", AffCode: "workload-other", Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 654321}
					require.NoError(t, db.Create(&owner).Error)
					require.NoError(t, db.Create(&foreign).Error)
					login := model.UserSession{SID: "workload-matrix-login", UserID: owner.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", CreatedAt: now - 100, LastActiveAt: now - 100, ExpiresAt: now + 3600}
					require.NoError(t, db.Create(&login).Error)
					task := model.Task{TaskID: "Owned-Case-Task", UserId: owner.Id, Quota: 77, CreatedAt: now - 100}
					require.NoError(t, db.Create(&task).Error)
					foreignTask := model.Task{TaskID: "Foreign-Task", UserId: foreign.Id, Quota: 88, CreatedAt: now - 100}
					require.NoError(t, db.Create(&foreignTask).Error)
					log := model.Log{UserId: owner.Id, Type: model.LogTypeConsume, Quota: 19, CreatedAt: now - 100}
					require.NoError(t, db.Create(&log).Error)
					option := model.Option{Key: "WorkloadFixtureUnchanged", Value: "unchanged"}
					require.NoError(t, db.Create(&option).Error)
					input, err := model.ParseClientWorkloadSnapshot([]byte(clientWorkloadInteropBody))
					require.NoError(t, err)
					beat := model.ClientActivityHeartbeat{SessionID: input.RuntimeSessionID, Event: "start", Platform: "win32", AppVersion: "1024.1", Foreground: true}
					presence, err := model.RecordClientActivity(owner.Id, beat, now-30)
					require.NoError(t, err)
					if mode == "upgrade" {
						beat.Event = "heartbeat"
						presence, err = model.RecordClientActivity(owner.Id, beat, now)
						require.NoError(t, err)
					}
					beforeOwner, beforeLogin, beforeTask, beforeLog, beforePresence, beforeOption := owner, login, task, log, *presence, option
					telemetry := []any{&model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}}
					require.False(t, db.Migrator().HasTable(&model.ClientWorkloadRuntime{}))
					for range 2 {
						require.NoError(t, db.AutoMigrate(telemetry...))
					}
					identity := service.AuthIdentity{UserID: owner.Id, SessionID: login.SID, UserAuthVersion: 1, SessionVersion: 1}
					ack, err := model.RecordClientWorkload(identity, input, now)
					require.NoError(t, err)
					assert.True(t, ack.Recorded)
					var first model.ClientWorkloadRuntime
					require.NoError(t, db.First(&first).Error)
					ack, err = model.RecordClientWorkload(identity, input, now+10)
					require.NoError(t, err)
					assert.Equal(t, "duplicate", ack.Disposition)
					var unchanged model.ClientWorkloadRuntime
					require.NoError(t, db.First(&unchanged).Error)
					assert.Equal(t, first, unchanged)
					// Commit a higher sequence after the reader has fetched its
					// runtime version. All telemetry rows must stay in one snapshot.
					if kind == "sqlite" {
						require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
					}
					for _, readKind := range []string{"summary", "detail"} {
						t.Run("consistent_"+readKind, func(t *testing.T) {
							reached, resume := make(chan struct{}), make(chan struct{})
							var paused atomic.Bool
							callback := "workload-read-consistency-" + readKind
							require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(query *gorm.DB) {
								if query.Statement.Table == "client_workload_runtimes" && paused.CompareAndSwap(false, true) {
									close(reached)
									<-resume
								}
							}))
							defer db.Callback().Query().Remove(callback)
							type readResult struct {
								summary *model.ClientWorkloadSummary
								detail  *model.ClientWorkloadDetail
								err     error
							}
							result := make(chan readResult, 1)
							expectedSequence := input.Sequence
							go func() {
								value := readResult{}
								if readKind == "summary" {
									value.summary, value.err = model.GetClientWorkloadSummary(now + 20)
								} else {
									value.detail, value.err = model.GetClientWorkloadDetail(owner.Id, input.RuntimeSessionID, now+20)
								}
								result <- value
							}()
							select {
							case <-reached:
							case <-time.After(5 * time.Second):
								close(resume)
								t.Fatal("reader did not reach the version boundary")
							}
							candidate := input
							candidate.Sequence++
							_, writeErr := model.RecordClientWorkload(identity, candidate, now+15)
							close(resume)
							require.NoError(t, writeErr)
							input = candidate
							select {
							case value := <-result:
								require.NoError(t, value.err)
								if readKind == "summary" {
									assert.EqualValues(t, 8, value.summary.Known.TaskAttempts)
									assert.EqualValues(t, 4, value.summary.Known.QueuedTasks)
									assert.EqualValues(t, 4, value.summary.Known.GeneratingTasks)
									assert.Equal(t, "complete", value.summary.Coverage.Collections["storyboard_task"].Coverage)
								} else {
									assert.Equal(t, expectedSequence, value.detail.AcceptedSequence)
									require.Len(t, value.detail.Activities, 10)
									for _, item := range value.detail.Activities {
										assert.Equal(t, "live", item.ObservationStatus)
									}
								}
							case <-time.After(5 * time.Second):
								t.Fatal("reader did not finish")
							}
						})
					}
					input.Sequence = 3
					ack, err = model.RecordClientWorkload(identity, input, now+20)
					require.NoError(t, err)
					assert.EqualValues(t, 3, ack.AcceptedSequence)
					input.Sequence = 2
					ack, err = model.RecordClientWorkload(identity, input, now+30)
					require.NoError(t, err)
					assert.Equal(t, "out_of_order", ack.Disposition)
					assert.EqualValues(t, 3, ack.AcceptedSequence)
					input.Sequence = 3
					input.Activities[0].WorkState, input.Activities[0].Phase = "unknown", "unknown"
					_, err = model.RecordClientWorkload(identity, input, now+31)
					require.ErrorIs(t, err, model.ErrClientWorkloadConflict)
					input.Sequence = 4
					input.Activities[0].WorkState, input.Activities[0].Phase = "active", "busy"
					// Default MySQL CI collation must never merge two exact opaque IDs.
					input.Activities = append(input.Activities, model.ClientWorkloadActivityInput{ActivityID: "Case-Distinct", Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "Case-Root"}, model.ClientWorkloadActivityInput{ActivityID: "case-distinct", Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "case-root"})
					_, err = model.RecordClientWorkload(identity, input, now+32)
					require.NoError(t, err)
					var rows int64
					require.NoError(t, db.Model(&model.ClientWorkloadRecord{}).Count(&rows).Error)
					assert.EqualValues(t, 12, rows)
					// An ended activity cannot reopen even from another reporting runtime.
					input.Sequence = 5
					input.Activities[0].WorkState, input.Activities[0].Phase, input.Activities[0].Outcome = "ended", "ended", "succeeded"
					_, err = model.RecordClientWorkload(identity, input, now+33)
					require.NoError(t, err)
					input.Sequence = 6
					input.Activities[0].WorkState, input.Activities[0].Phase, input.Activities[0].Outcome = "active", "busy", ""
					_, err = model.RecordClientWorkload(identity, input, now+34)
					require.ErrorIs(t, err, model.ErrClientWorkloadConflict)
					// A valid positive sequence alone cannot invent a parent/task association.
					input.Activities = input.Activities[1:]
					input.Activities[1].APITaskID = "owned-case-task"
					_, err = model.RecordClientWorkload(identity, input, now+35)
					require.ErrorIs(t, err, model.ErrClientWorkloadInvalid)
					input.Activities[1].APITaskID = "Foreign-Task"
					_, err = model.RecordClientWorkload(identity, input, now+36)
					require.ErrorIs(t, err, model.ErrClientWorkloadInvalid)
					input.Activities[1].APITaskID = "Owned-Case-Task"
					_, err = model.RecordClientWorkload(identity, input, now+37)
					require.NoError(t, err)
					input.Sequence = 7
					input.Activities[1].APITaskID = ""
					input.Activities[1].ParentActivityID = "missing-parent"
					_, err = model.RecordClientWorkload(identity, input, now+38)
					require.ErrorIs(t, err, model.ErrClientWorkloadInvalid)
					input.Activities[1].ParentActivityID = input.Activities[0].ActivityID
					_, err = model.RecordClientWorkload(identity, input, now+39)
					require.NoError(t, err)
					// A bounded valid association payload may exceed MySQL TEXT64KiB;
					// shared LongText storage must round-trip it without changing the wire.
					input.Sequence = 8
					largeBindings := make([]string, 600)
					for i := range largeBindings {
						largeBindings[i] = strings.Repeat("s", 115) + fmt.Sprintf("%04d", i)
					}
					input.Activities[1].ShotIDs = largeBindings
					encoded, err := common.Marshal(input)
					require.NoError(t, err)
					require.Less(t, len(encoded), model.MaxClientWorkloadBodyBytes)
					bindings, err := common.Marshal(largeBindings)
					require.NoError(t, err)
					require.Greater(t, len(bindings), 65535)
					_, err = model.RecordClientWorkload(identity, input, now+40)
					require.NoError(t, err)
					var observations []model.ClientWorkloadObservation
					require.NoError(t, db.Where("included_sequence = ?", 8).Find(&observations).Error)
					foundLarge := false
					for _, observation := range observations {
						if len(observation.ShotIDs) > 65535 {
							var restored []string
							require.NoError(t, common.UnmarshalJsonStr(string(observation.ShotIDs), &restored))
							assert.Equal(t, largeBindings, restored)
							foundLarge = true
						}
					}

					// Bound storage without writing the configured cap: place
					// each ledger at its threshold, then attempt one new ID.
					var beforeBudgets []model.ClientWorkloadBudget
					require.NoError(t, db.Find(&beforeBudgets).Error)
					var originalGlobal, originalUser model.ClientWorkloadBudget
					for _, budget := range beforeBudgets {
						if budget.Key == "global" {
							originalGlobal = budget
						} else {
							originalUser = budget
						}
					}
					require.NotEmpty(t, originalGlobal.Key)
					require.NotEmpty(t, originalUser.Key)
					for _, capCase := range []struct {
						name  string
						key   string
						limit int64
					}{{"global", originalGlobal.Key, model.MaxClientWorkloadGlobalBytes}, {"user", originalUser.Key, model.MaxClientWorkloadUserBytes}} {
						t.Run("capacity "+capCase.name, func(t *testing.T) {
							require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: capCase.key}).Update("reserved_bytes", capCase.limit).Error)
							var beforeRuntime model.ClientWorkloadRuntime
							require.NoError(t, db.First(&beforeRuntime).Error)
							var atCap []model.ClientWorkloadBudget
							require.NoError(t, db.Find(&atCap).Error)
							candidate := input
							candidate.Sequence = 9
							candidate.Activities = append(append([]model.ClientWorkloadActivityInput(nil), input.Activities...), model.ClientWorkloadActivityInput{ActivityID: "budget-rejected-id", Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "budget-root"})
							_, err = model.RecordClientWorkload(identity, candidate, now+40)
							require.ErrorIs(t, err, model.ErrClientWorkloadCapacity)
							var unchangedRuntime model.ClientWorkloadRuntime
							require.NoError(t, db.First(&unchangedRuntime).Error)
							assert.Equal(t, beforeRuntime, unchangedRuntime)
							var unchangedBudgets []model.ClientWorkloadBudget
							require.NoError(t, db.Find(&unchangedBudgets).Error)
							assert.ElementsMatch(t, atCap, unchangedBudgets)
							var identities int64
							require.NoError(t, db.Model(&model.ClientWorkloadRecord{}).Count(&identities).Error)
							assert.EqualValues(t, 12, identities)
							require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: originalGlobal.Key}).Update("reserved_bytes", originalGlobal.ReservedBytes).Error)
							require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: originalUser.Key}).Update("reserved_bytes", originalUser.ReservedBytes).Error)
						})
					}
					// A real higher revision with zero growth remains usable
					// while a ledger is full; current large bindings survive.
					require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: originalUser.Key}).Update("reserved_bytes", model.MaxClientWorkloadUserBytes).Error)
					input.Sequence = 9
					_, err = model.RecordClientWorkload(identity, input, now+40)
					require.NoError(t, err)
					require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: originalUser.Key}).Update("reserved_bytes", originalUser.ReservedBytes).Error)
					assert.True(t, foundLarge)
					// Omission is unreported, not a synthetic success or cancellation.
					input.Sequence = 10
					input.Activities = []model.ClientWorkloadActivityInput{}
					_, err = model.RecordClientWorkload(identity, input, now+40)
					require.NoError(t, err)
					var refunded model.ClientWorkloadBudget
					require.NoError(t, db.Where(&model.ClientWorkloadBudget{Key: originalUser.Key}).Take(&refunded).Error)
					assert.Greater(t, originalUser.ReservedBytes-refunded.ReservedBytes, int64(65535), "unreported large associations return reserved bytes")
					omitted, err := model.GetClientWorkloadSummary(now + 41)
					require.NoError(t, err)
					assert.Zero(t, omitted.Known.TaskAttempts)
					assert.EqualValues(t, 8, omitted.Uncertainty.UnknownTaskAttempts, "complete omission cannot silently end retained attempts")
					assert.Positive(t, omitted.Uncertainty.UnknownChatSessions)
					assert.Positive(t, omitted.Uncertainty.UnknownProjects)

					detail, err := model.GetClientWorkloadDetail(owner.Id, input.RuntimeSessionID, now+41)
					require.NoError(t, err)
					require.NotEmpty(t, detail.Activities)
					notReported := false
					for _, activity := range detail.Activities {
						notReported = notReported || activity.ObservationStatus == "not_reported"
					}
					assert.True(t, notReported)
					beforeRestart := model.DB.Dialector
					sqlDB, err := db.DB()
					require.NoError(t, err)
					require.NoError(t, sqlDB.Close())
					reopened, err := gorm.Open(beforeRestart, &gorm.Config{})
					require.NoError(t, err)
					db = reopened
					model.DB, model.LOG_DB = db, db
					t.Cleanup(func() {
						connection, e := reopened.DB()
						if e == nil {
							_ = connection.Close()
						}
					})
					for range 2 {
						require.NoError(t, db.AutoMigrate(telemetry...))
					}
					var afterOwner model.User
					require.NoError(t, db.First(&afterOwner, owner.Id).Error)
					assert.Equal(t, beforeOwner, afterOwner)
					var afterLogin model.UserSession
					require.NoError(t, db.First(&afterLogin, "sid = ?", login.SID).Error)
					assert.Equal(t, beforeLogin, afterLogin)
					var afterTask model.Task
					require.NoError(t, db.First(&afterTask, task.ID).Error)
					assert.Equal(t, beforeTask, afterTask)
					var afterLog model.Log
					require.NoError(t, db.First(&afterLog, log.Id).Error)
					assert.Equal(t, beforeLog, afterLog)
					var afterPresence model.ClientActivitySession
					require.NoError(t, db.First(&afterPresence, presence.ID).Error)
					assert.Equal(t, beforePresence, afterPresence)
					var afterOption model.Option
					require.NoError(t, db.Where(&model.Option{Key: option.Key}).Take(&afterOption).Error)
					assert.Equal(t, beforeOption, afterOption)
					// Reloaded canonical tombstones keep an original execution closed.
					input.Sequence = 11
					input.Activities = []model.ClientWorkloadActivityInput{{ActivityID: "bfb35610-9ed7-4f3f-9d36-1c9bce3cfcf0", Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80"}}
					_, err = model.RecordClientWorkload(identity, input, now+42)
					require.ErrorIs(t, err, model.ErrClientWorkloadConflict)
					// Presence and workload have independent leases; a missing
					// old client is unreported, never mislabeled expired.
					beat.Event = "heartbeat"
					_, err = model.RecordClientActivity(owner.Id, beat, now+100)
					require.NoError(t, err)
					missingBeat := beat
					missingBeat.SessionID = "d6fd6dce-e751-41a2-b3a9-868738e72061"
					_, err = model.RecordClientActivity(foreign.Id, missingBeat, now+100)
					require.NoError(t, err)
					expired, err := model.GetClientWorkloadSummary(now + 130)
					require.NoError(t, err)
					assert.EqualValues(t, 1, expired.Uncertainty.ExpiredRuntimeSessions)
					assert.EqualValues(t, 1, expired.Uncertainty.UnreportedRuntimeSessions)
					original, err := model.ParseClientWorkloadSnapshot([]byte(clientWorkloadInteropBody))
					require.NoError(t, err)
					original.Sequence = 11
					original.Collections = model.ClientWorkloadCollections{ChatActivity: "not_collected", ProjectRun: "not_collected", StoryboardTask: "collected", LocalOperation: "not_collected", Shots: "not_collected"}
					original.Activities = []model.ClientWorkloadActivityInput{original.Activities[2]}
					_, err = model.RecordClientWorkload(identity, original, now+101)
					require.NoError(t, err)
					another := original
					another.RuntimeSessionID = "c6fd6dce-e751-41a2-b3a9-868738e72061"
					another.Sequence = 1
					nextBeat := beat
					nextBeat.SessionID = another.RuntimeSessionID
					_, err = model.RecordClientActivity(owner.Id, nextBeat, now+100)
					require.NoError(t, err)
					another.Activities = append([]model.ClientWorkloadActivityInput(nil), original.Activities...)
					another.Activities[0].Phase = "queued"
					_, err = model.RecordClientWorkload(identity, another, now+101)
					require.NoError(t, err)
					conflict, err := model.GetClientWorkloadSummary(now + 102)
					require.NoError(t, err)
					assert.EqualValues(t, 1, conflict.Uncertainty.ConflictingTaskAttempts)
					assert.Zero(t, conflict.Known.TaskAttempts)
					assert.Zero(t, conflict.Known.GeneratingChatSessions)
					original.Sequence = 12
					original.Activities = []model.ClientWorkloadActivityInput{}
					_, err = model.RecordClientWorkload(identity, original, now+103)
					require.NoError(t, err)
					another.Sequence = 2
					another.Activities[0].WorkState, another.Activities[0].Phase = "unknown", "unknown"
					_, err = model.RecordClientWorkload(identity, another, now+104)
					require.NoError(t, err)
					unknown, err := model.GetClientWorkloadSummary(now + 105)
					require.NoError(t, err)
					assert.EqualValues(t, 8, unknown.Uncertainty.UnknownTaskAttempts)
					assert.Zero(t, unknown.Uncertainty.ConflictingTaskAttempts)
					assert.Zero(t, unknown.Known.TaskAttempts)
					another.Sequence = 3
					another.Collections.StoryboardTask, another.Collections.LocalOperation = "not_collected", "collected"
					another.Activities = []model.ClientWorkloadActivityInput{{ActivityID: "compose-only", Kind: "local_operation", WorkState: "active", Phase: "composing", ChatSessionID: "chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80"}}

					_, err = model.RecordClientWorkload(identity, another, now+106)
					require.NoError(t, err)
					composing, err := model.GetClientWorkloadSummary(now + 107)
					require.NoError(t, err)
					assert.EqualValues(t, 1, composing.Known.GeneratingChatSessions)
					assert.EqualValues(t, 1, composing.Known.LocalOperations)
					assert.Zero(t, composing.Known.GeneratingTasks)
					// Two publishers share the global reservation guard. With
					// room for exactly one compact observation, only one wins.
					var beforeRace model.ClientWorkloadBudget
					require.NoError(t, db.Where(&model.ClientWorkloadBudget{Key: "global"}).Take(&beforeRace).Error)
					const oneNewObservation = int64(4096 + 512 + 4)
					require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: "global"}).Update("reserved_bytes", model.MaxClientWorkloadGlobalBytes-oneNewObservation).Error)
					raceInputs := []model.ClientWorkloadSnapshot{original, another}
					raceInputs[0].Sequence = 13
					raceInputs[1].Sequence = 4
					for i := range raceInputs {
						raceInputs[i].Collections = model.ClientWorkloadCollections{ChatActivity: "collected", ProjectRun: "not_collected", StoryboardTask: "not_collected", LocalOperation: "not_collected", Shots: "not_collected"}
						raceInputs[i].Activities = []model.ClientWorkloadActivityInput{{ActivityID: fmt.Sprintf("budget-race-%d", i), Kind: "chat_activity", WorkState: "active", Phase: "busy", ChatSessionID: "budget-race-root"}}
					}
					start := make(chan struct{})
					results := make(chan error, 2)
					var wait sync.WaitGroup
					for _, candidate := range raceInputs {
						wait.Go(func() { <-start; _, err := model.RecordClientWorkload(identity, candidate, now+108); results <- err })
					}
					close(start)
					wait.Wait()
					close(results)
					acceptedCount, capacityCount := 0, 0
					for result := range results {
						if result == nil {
							acceptedCount++
						} else if errors.Is(result, model.ErrClientWorkloadCapacity) {
							capacityCount++
						} else {
							require.NoError(t, result)
						}
					}
					assert.Equal(t, 1, acceptedCount)
					assert.Equal(t, 1, capacityCount)
					var afterRace model.ClientWorkloadBudget
					require.NoError(t, db.Where(&model.ClientWorkloadBudget{Key: "global"}).Take(&afterRace).Error)
					assert.LessOrEqual(t, afterRace.ReservedBytes, model.MaxClientWorkloadGlobalBytes)
					require.NoError(t, db.Model(&model.ClientWorkloadBudget{}).Where(&model.ClientWorkloadBudget{Key: "global"}).Update("reserved_bytes", beforeRace.ReservedBytes+oneNewObservation).Error)

				})
			}
		})
	}
}

type clientWorkloadNetworkGuard struct{ calls atomic.Int64 }

func (guard *clientWorkloadNetworkGuard) RoundTrip(*http.Request) (*http.Response, error) {
	guard.calls.Add(1)
	return nil, fmt.Errorf("workload telemetry may not make outbound requests")
}
func denyClientWorkloadNetwork(t *testing.T) *clientWorkloadNetworkGuard {
	t.Helper()
	guard := &clientWorkloadNetworkGuard{}
	previousTransport, previousClientTransport := http.DefaultTransport, http.DefaultClient.Transport
	http.DefaultTransport, http.DefaultClient.Transport = guard, guard
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient.Transport = previousTransport, previousClientTransport
		assert.Zero(t, guard.calls.Load(), "no provider, SMS, media download or other network call")
	})
	return guard
}

func TestClientWorkloadLimiterIsIndependent(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			previousRedis, previousRDB := common.RedisEnabled, common.RDB
			common.RedisEnabled = false
			if backend == "redis" {
				server := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: server.Addr()})
				common.RedisEnabled, common.RDB = true, client
				t.Cleanup(func() { _ = client.Close() })
			}
			t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousRDB })
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("id", 19875423) })
			router.POST("/workload", middleware.ClientWorkloadRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			router.POST("/presence", middleware.ClientActivityRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := func(path string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
				return w
			}
			for range 120 {
				require.Equal(t, http.StatusNoContent, request("/workload").Code)
			}
			limited := request("/workload")
			assert.Equal(t, http.StatusTooManyRequests, limited.Code)
			assert.NotEmpty(t, limited.Header().Get("Retry-After"))
			assert.Equal(t, http.StatusNoContent, request("/presence").Code, "workload quota cannot exhaust ordinary presence reporting")
		})
	}
}

const clientWorkloadFinalFixtureSHA = "0dab80318c8afb5c56bcb3efae1d947396e745c761896f17d054dbcdbec075ee"
const clientWorkloadFinalInteropBody = `{"schema_version":1,"runtime_session_id":"01234567-89ab-4cde-8fab-0123456789ab","sequence":1,"complete":true,"collections":{"chat_activity":"collected","project_run":"collected","storyboard_task":"collected","local_operation":"not_collected","shots":"not_collected"},"activities":[{"activity_id":"85a4dd9c-119e-40db-870a-249cfa43e4ee","kind":"chat_activity","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","work_state":"active","phase":"busy"},{"activity_id":"549cf411-e9de-4392-82d7-ea72ef399591","kind":"project_run","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5"},{"activity_id":"594789b8-cb8e-4e9c-97a6-8dd3748659a8","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000001-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_1ceb4611dc41b06ca948fc5f89cc0a1ab89d83e06361e4fc08893bd04d275499"]},{"activity_id":"0981b96e-2670-47ef-84ba-cb3ea94d9ba1","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000002-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_d7c288a0a2d9a95834fba03ecac022055764e52ac6b062019358e42f9aa8ee54"]},{"activity_id":"b32b800c-b61f-466c-8022-ffdff168be01","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000003-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_a97819054f1f9e04ca3910abc1f1e99e4404c42108c95c633e687ba02a6c888a"]},{"activity_id":"1252a066-15b8-4f14-8e57-79458e62bb69","kind":"storyboard_task","work_state":"active","phase":"generating","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000004-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_73bc6365892df5565552264adb1f200430f731d1f0788c898ff425a764feb8c0"]},{"activity_id":"58848712-6495-44d1-86ef-0903193bfe81","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000005-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_59967f3e5cf198e4ef300ff1e410ca513188c61c80c6d9d03b32e165627a8602"]},{"activity_id":"31b4a9ea-a5ca-46a8-93ae-d3de3f2bfae3","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000006-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_ef948cd44a5a9591954153cf8d1840910addc80b151b6dc807a1aeeae78a89c6"]},{"activity_id":"e56239f1-5472-4354-a04a-6bd384ba153a","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000007-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_f1af49a74cfb5689fad852a5b59611a7ef7a2b2af1bd2887735bc6bf8673dc0e"]},{"activity_id":"34146a3a-7e03-4ee4-8a3b-8e16a0bb9e71","kind":"storyboard_task","work_state":"active","phase":"queued","chat_session_id":"chat_61abf90109ca78d0496fea98a6ac2c52dc828d0849bb65311c4cd39152d89b80","project_id":"project_82ed5607b521c87e84a12ef145daac3c34da305e1e5b5051176217a5ee7c76f5","attempt_id":"00000008-89ab-4cde-8fab-0123456789ab","shot_ids":["shot_0bdee722a623d3a4775daca7b48ae9b383e025fec30299feb1c06bd88c49cf93"]}],"shots":[]}`

// The HTTP response must compose its legacy presence fields and the additive
// workload from the same main-DB version, even when a process stops in between.
func TestClientActivityHandlerReadConsistencyDatabaseMatrix(t *testing.T) {
	denyClientWorkloadNetwork(t)
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("ACTIVITY_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("local isolated activity database is not configured")
			}
			db, _ := newAuditTestDatabase(t, kind, dsn)
			typ := common.DatabaseTypeSQLite
			if kind == "mysql" {
				typ = common.DatabaseTypeMySQL
			} else if kind == "postgres" {
				typ = common.DatabaseTypePostgreSQL
			}
			useLogExportDatabases(t, db, db, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Log{}, &model.ClientActivitySession{}, &model.ClientWorkloadBudget{}, &model.ClientWorkloadRuntime{}, &model.ClientWorkloadRecord{}, &model.ClientWorkloadObservation{}, &model.ClientWorkloadShot{}))
			if kind == "sqlite" {
				require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
			}
			now := common.GetTimestamp()
			owner := model.User{Username: "http-consistent-owner", AffCode: "http-consistent-owner", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
			require.NoError(t, db.Create(&owner).Error)
			login := model.UserSession{SID: "http-consistent-login", UserID: owner.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("d", 64), ExpiresAt: now + 3600}
			require.NoError(t, db.Create(&login).Error)
			identity := service.AuthIdentity{UserID: owner.Id, SessionID: login.SID, SessionVersion: 1, UserAuthVersion: 1}
			router := gin.New()
			router.GET("/summary", GetClientActivitySummary)
			router.GET("/sessions/:id", GetClientActivitySession)
			for i, readKind := range []string{"summary", "detail"} {
				t.Run(readKind, func(t *testing.T) {
					input, err := model.ParseClientWorkloadSnapshot([]byte(clientWorkloadFinalInteropBody))
					require.NoError(t, err)
					input.RuntimeSessionID = fmt.Sprintf("1234567%d-89ab-4cde-8fab-0123456789ab", i)
					// Stable activity identities may be observed from another runtime.
					beat := model.ClientActivityHeartbeat{SessionID: input.RuntimeSessionID, Event: "start", Platform: "win32", AppVersion: "1024.1", Foreground: true}
					presence, err := model.RecordClientActivity(owner.Id, beat, now)
					require.NoError(t, err)
					_, err = model.RecordClientWorkload(identity, input, now)
					require.NoError(t, err)
					reached, resume := make(chan struct{}), make(chan struct{})
					var paused atomic.Bool
					callback := "workload-http-composition-" + readKind
					require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(query *gorm.DB) {
						if query.Statement.Table == "client_activity_sessions" && paused.CompareAndSwap(false, true) {
							close(reached)
							<-resume
						}
					}))
					defer db.Callback().Query().Remove(callback)
					path := "/summary"
					if readKind == "detail" {
						path = fmt.Sprintf("/sessions/%d", presence.ID)
					}
					finished := make(chan *httptest.ResponseRecorder, 1)
					go func() {
						response := httptest.NewRecorder()
						router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
						finished <- response
					}()
					select {
					case <-reached:
					case <-time.After(5 * time.Second):
						close(resume)
						t.Fatal("HTTP reader did not reach presence boundary")
					}
					beat.Event = "stop"
					_, writeErr := model.RecordClientActivity(owner.Id, beat, now+1)
					close(resume)
					require.NoError(t, writeErr)
					select {
					case response := <-finished:
						require.Equal(t, http.StatusOK, response.Code, response.Body.String())
						if readKind == "summary" {
							var result struct {
								Data model.ClientActivitySummary `json:"data"`
							}
							require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
							require.NotNil(t, result.Data.Workload)
							assert.EqualValues(t, 1, result.Data.OnlineUsers)
							assert.EqualValues(t, 1, result.Data.RuntimeSessions)
							assert.Equal(t, result.Data.RuntimeSessions, result.Data.Workload.Coverage.OnlineRuntimeSessions)
							assert.EqualValues(t, 1, result.Data.Workload.Coverage.ReportingRuntimeSessions)
							assert.EqualValues(t, 8, result.Data.Workload.Known.TaskAttempts)
							assert.Equal(t, result.Data.ServerTime, result.Data.Workload.ServerTime)
						} else {
							var result struct {
								Data struct {
									Session  model.ClientActivityView   `json:"session"`
									Workload model.ClientWorkloadDetail `json:"workload"`
								} `json:"data"`
							}
							require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
							assert.Zero(t, result.Data.Session.EndedAt)
							assert.Equal(t, "live", result.Data.Session.ObservationDetail)
							assert.Equal(t, "live", result.Data.Workload.ObservationStatus)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("HTTP reader did not finish")
					}
					// A subsequent request sees the committed stop, proving this was a
					// real concurrent commit rather than a no-op in the test callback.
					subsequent := httptest.NewRecorder()
					router.ServeHTTP(subsequent, httptest.NewRequest("GET", path, nil))
					require.Equal(t, http.StatusOK, subsequent.Code, subsequent.Body.String())
					if readKind == "summary" {
						var result struct {
							Data model.ClientActivitySummary `json:"data"`
						}
						require.NoError(t, common.Unmarshal(subsequent.Body.Bytes(), &result))
						assert.Zero(t, result.Data.RuntimeSessions)
						assert.Zero(t, result.Data.Workload.Coverage.OnlineRuntimeSessions)
					} else {
						assert.Contains(t, subsequent.Body.String(), `"observation_status":"ended"`)
					}
				})
			}
		})
	}
}
