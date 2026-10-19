package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
						require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}))
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
					require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}))
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
						require.NoError(t, db.AutoMigrate(&model.ClientActivitySession{}))
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
	require.NoError(t, model.DB.AutoMigrate(&model.ClientActivitySession{}))
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
