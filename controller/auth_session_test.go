package controller

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestAuthLogoutRejectsRefreshCookieSessionMismatch(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.RedisEnabled = false
	common.SessionSecret = "auth-logout-mismatch-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
	})

	user := &model.User{
		Username: "logout-mismatch-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	sessionA, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-a")
	require.NoError(t, err)
	sessionB, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-b")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/logout", nil)
	c.Request.Header.Set("Authorization", "Bearer "+sessionA.AccessToken)
	c.Request.Header.Set("X-Auth-Session", sessionA.Session.SID)
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: sessionB.RefreshToken})

	AuthLogout(c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", response.Code)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusActive, stored.Status)
	}
}

func TestWriteAuthSessionErrorMapsSessionGrowthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "active session limit",
			err:            model.ErrUserSessionLimit,
			expectedStatus: http.StatusConflict,
			expectedCode:   "AUTH_SESSION_LIMIT",
		},
		{
			name:           "issuance limit",
			err:            model.ErrUserSessionIssuanceLimit,
			expectedStatus: http.StatusTooManyRequests,
			expectedCode:   "AUTH_SESSION_ISSUANCE_LIMIT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeAuthSessionError(c, test.err)

			assert.Equal(t, test.expectedStatus, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedCode, response.Code)
		})
	}
}

func TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.TwoFA{}, &model.PasskeyCredential{}))
	model.DB = db
	common.RedisEnabled = false
	common.UserSessionActiveLimit = 1
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})

	const previousLastLoginAt = int64(123)
	user := &model.User{
		Username: "rejected-login-audit-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{
		SID: "existing-active-session", UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	setupLogin(user, nil, c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, stored.LastLoginAt)
}

// Run this same HTTP -> controller -> service -> database contract against
// each configured engine. External fixtures are loopback-only and each case
// receives its own database through the existing audit fixture.
func TestRefreshAuthFailureClassificationDatabaseMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			dsn := os.Getenv("TEST_" + strings.ToUpper(kind) + "_DSN")
			if kind != "sqlite" && dsn == "" {
				t.Skip("local authentication database DSN is not configured")
			}
			db, _ := newAuditTestDatabase(t, kind, dsn)
			db = db.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
			typ := map[string]common.DatabaseType{"sqlite": common.DatabaseTypeSQLite, "mysql": common.DatabaseTypeMySQL, "postgres": common.DatabaseTypePostgreSQL}[kind]
			useLogExportDatabases(t, db, db, typ, typ)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
			versionSQL := "SELECT version()"
			if kind == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
			previousSecret, previousSecure := common.SessionSecret, common.SessionCookieSecure
			previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
			common.SessionSecret, common.SessionCookieSecure = "refresh-auth-isolated-test-secret", true
			var diagnostics bytes.Buffer
			gin.DefaultWriter, gin.DefaultErrorWriter = &diagnostics, &diagnostics
			t.Cleanup(func() {
				common.SessionSecret, common.SessionCookieSecure = previousSecret, previousSecure
				gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
			})
			router := gin.New()
			router.Use(middleware.RequestId())
			router.POST("/api/user/auth/refresh", RefreshAuth)
			sequence := 0
			newSession := func(t *testing.T) (*model.User, *service.AuthBundle) {
				t.Helper()
				sequence++
				user := &model.User{Username: fmt.Sprintf("refresh-%d", sequence), AffCode: fmt.Sprintf("refresh-%d", sequence), Password: "unused-test-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
				require.NoError(t, db.Create(user).Error)
				bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "isolated-test-agent")
				require.NoError(t, err)
				return user, bundle
			}
			refresh := func(raw, sid string) *httptest.ResponseRecorder {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
				request.Header.Set("X-Auth-Session", sid)
				if raw != "" {
					request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: raw})
				}
				router.ServeHTTP(recorder, request)
				return recorder
			}

			t.Run("rotation_and_previous_token_grace", func(t *testing.T) {
				_, bundle := newSession(t)
				first := refresh(bundle.RefreshToken, bundle.Session.SID)
				require.Equal(t, http.StatusOK, first.Code)
				var rotated string
				for _, cookie := range first.Result().Cookies() {
					if cookie.Name == service.RefreshCookieName {
						rotated = cookie.Value
						assert.True(t, cookie.HttpOnly)
						assert.True(t, cookie.Secure)
						assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
					}
				}
				require.NotEmpty(t, rotated)
				assert.NotEqual(t, bundle.RefreshToken, rotated)
				grace := refresh(bundle.RefreshToken, bundle.Session.SID)
				require.Equal(t, http.StatusOK, grace.Code)
				for _, cookie := range grace.Result().Cookies() {
					if cookie.Name == service.RefreshCookieName {
						assert.Equal(t, rotated, cookie.Value)
					}
				}
				assert.Equal(t, "no-store", first.Header().Get("Cache-Control"))
			})
			t.Run("database_connection_recovers_without_losing_cookie", func(t *testing.T) {
				_, bundle := newSession(t)
				unavailable, err := gorm.Open(db.Dialector, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
				require.NoError(t, err)
				connection, err := unavailable.DB()
				require.NoError(t, err)
				require.NoError(t, connection.Close())
				model.DB = unavailable
				response := refresh(bundle.RefreshToken, bundle.Session.SID)
				model.DB = db
				assert.Equal(t, http.StatusInternalServerError, response.Code)
				assert.JSONEq(t, `{"success":false,"code":"AUTH_INTERNAL_ERROR","message":"Internal Server Error"}`, response.Body.String())
				assert.Empty(t, response.Header().Values("Set-Cookie"))
				assert.Equal(t, http.StatusOK, refresh(bundle.RefreshToken, bundle.Session.SID).Code)
			})
			t.Run("concurrent_previous_token_recovers_same_rotation", func(t *testing.T) {
				_, bundle := newSession(t)
				responses := make(chan *httptest.ResponseRecorder, 2)
				for range 2 {
					go func() { responses <- refresh(bundle.RefreshToken, bundle.Session.SID) }()
				}
				var winner string
				for range 2 {
					response := <-responses
					require.Equal(t, http.StatusOK, response.Code)
					for _, cookie := range response.Result().Cookies() {
						if cookie.Name == service.RefreshCookieName {
							if winner == "" {
								winner = cookie.Value
							} else {
								assert.Equal(t, winner, cookie.Value)
							}
						}
					}
				}
				require.NotEmpty(t, winner)
			})

			for _, test := range []struct {
				name, table string
				query       int
			}{
				{"session_storage_failure", "user_sessions", 1},
				{"user_cache_storage_failure", "users", 1},
				{"authoritative_user_storage_failure", "users", 2},
				{"rotation_storage_failure", "user_sessions", 2},
			} {
				t.Run(test.name, func(t *testing.T) {
					_, bundle := newSession(t)
					before, err := model.GetUserSessionBySID(bundle.Session.SID)
					require.NoError(t, err)
					// This error intentionally contains a credential-shaped marker;
					// neither response nor diagnostic logs may reproduce it.
					failure := errors.New("storage failure private-marker " + bundle.RefreshToken)
					seen := 0
					callback := "test:refresh_storage_failure"
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == test.table {
							seen++
							if seen == test.query {
								tx.AddError(failure)
							}
						}
					}))
					diagnostics.Reset()
					failed := refresh(bundle.RefreshToken, bundle.Session.SID)
					require.NoError(t, db.Callback().Query().Remove(callback))
					assert.Equal(t, http.StatusInternalServerError, failed.Code)
					assert.JSONEq(t, `{"success":false,"code":"AUTH_INTERNAL_ERROR","message":"Internal Server Error"}`, failed.Body.String())
					assert.Empty(t, failed.Header().Values("Set-Cookie"))
					assert.Equal(t, "no-store", failed.Header().Get("Cache-Control"))
					assert.Contains(t, diagnostics.String(), "code=AUTH_INTERNAL_ERROR reason=internal_error")
					assert.Contains(t, diagnostics.String(), failed.Header().Get(common.RequestIdKey))
					for _, secret := range []string{bundle.RefreshToken, bundle.AccessToken, bundle.Session.SID, "private-marker"} {
						assert.NotContains(t, diagnostics.String(), secret)
						assert.NotContains(t, failed.Body.String(), secret)
					}
					after, err := model.GetUserSessionBySID(bundle.Session.SID)
					require.NoError(t, err)
					assert.Equal(t, before, after, "a failed read cannot rotate or revoke the session")
					recovered := refresh(bundle.RefreshToken, bundle.Session.SID)
					assert.Equal(t, http.StatusOK, recovered.Code, "the original cookie must still refresh after storage recovers")
				})
			}

			for _, name := range []string{"missing", "malformed", "not_found", "unknown_secret", "revoked", "expired", "deleted_user", "deleted_after_cache", "disabled_user", "replay", "session_mismatch", "revoked_during_rotation", "deleted_during_rotation"} {
				t.Run(name, func(t *testing.T) {
					user, bundle := newSession(t)
					raw, sid, code := bundle.RefreshToken, bundle.Session.SID, "AUTH_SESSION_REVOKED"
					status := http.StatusUnauthorized
					switch name {
					case "missing":
						raw, code = "", "AUTH_UNAUTHORIZED"
					case "malformed":
						raw, code = "malformed-private-marker", "AUTH_UNAUTHORIZED"
					case "not_found":
						require.NoError(t, db.Delete(&model.UserSession{}, "sid = ?", sid).Error)
						code = "AUTH_UNAUTHORIZED"
					case "unknown_secret":
						raw, code = sid+".unknown-private-marker", "AUTH_UNAUTHORIZED"
					case "revoked":
						_, err := model.RevokeUserSession(user.Id, sid, "test_logout")
						require.NoError(t, err)
					case "expired":
						require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", sid).Update("expires_at", time.Now().Unix()-1).Error)
					case "deleted_user":
						require.NoError(t, db.Delete(user).Error)
					case "disabled_user":
						require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
					case "replay":
						require.Equal(t, http.StatusOK, refresh(raw, sid).Code)
						require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", sid).Update("previous_valid_until", time.Now().Unix()-1).Error)
					case "session_mismatch":
						sid, status, code = "different-session", http.StatusConflict, "AUTH_SESSION_MISMATCH"
					case "deleted_after_cache", "revoked_during_rotation", "deleted_during_rotation":
						seen := 0
						callback := "test:refresh_security_mutation"
						table := "user_sessions"
						if name == "deleted_after_cache" {
							table = "users"
						}
						require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table != table {
								return
							}
							seen++
							if seen != 2 {
								return
							}
							switch name {
							case "deleted_after_cache":
								require.NoError(t, db.Delete(user).Error)
							case "deleted_during_rotation":
								require.NoError(t, db.Delete(&model.UserSession{}, "sid = ?", sid).Error)
							default:
								require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", sid).Update("status", model.UserSessionStatusRevoked).Error)
							}
						}))
						t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
					}
					diagnostics.Reset()
					response := refresh(raw, sid)
					assert.Equal(t, status, response.Code)
					assert.JSONEq(t, fmt.Sprintf(`{"success":false,"code":%q,"message":%q}`, code, http.StatusText(status)), response.Body.String())
					assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
					if status == http.StatusUnauthorized {
						require.Len(t, response.Result().Cookies(), 2)
						for _, cookie := range response.Result().Cookies() {
							assert.Less(t, cookie.MaxAge, 0)
						}
					} else {
						assert.Empty(t, response.Header().Values("Set-Cookie"))
					}
					for _, secret := range []string{bundle.RefreshToken, bundle.AccessToken, bundle.Session.SID, "private-marker"} {
						assert.NotContains(t, diagnostics.String(), secret)
					}
					if name == "unknown_secret" || name == "session_mismatch" {
						stored, err := model.GetUserSessionBySID(bundle.Session.SID)
						require.NoError(t, err)
						assert.Equal(t, model.UserSessionStatusActive, stored.Status)
					}
				})
			}
		})
	}
}
