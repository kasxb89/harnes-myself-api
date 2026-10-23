package router

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// accessTokenExemptRoutes are /api routes that never reach dashboard
// authentication, so a personal access token has no rule to satisfy there.
var accessTokenExemptRoutes = []string{
	// Anonymous pages and setup.
	"GET /api/setup",
	"POST /api/setup",
	"GET /api/status",
	"GET /api/client-announcements",
	"GET /api/creation/content",
	"GET /api/creation/assets/:id",
	"HEAD /api/creation/assets/:id",
	"GET /api/uptime/status",
	"GET /api/notice",
	"GET /api/user-agreement",
	"GET /api/privacy-policy",
	"GET /api/about",
	"GET /api/home_page_content",
	"GET /api/ratio_config",
	"GET /api/user/groups",

	// Anonymous account entry points.
	"GET /api/verification",
	"GET /api/reset_password",
	"POST /api/user/reset",
	"POST /api/user/register",
	"GET /api/user/login/encryption-key",
	"POST /api/user/login",
	"POST /api/user/phone/challenge",
	"POST /api/user/phone/login",
	"POST /api/user/login/2fa",
	"POST /api/user/login/verify",
	"POST /api/user/login/passkey/begin",
	"POST /api/user/login/passkey/finish",
	"POST /api/user/passkey/login/begin",
	"POST /api/user/passkey/login/finish",
	"GET /api/oauth/wechat",
	"GET /api/oauth/telegram/login",
	"GET /api/oauth/telegram/bind/:flow_token",

	// Browser session cookie endpoints.
	"POST /api/user/auth/refresh",
	"POST /api/user/auth/logout",

	// Payment callbacks.
	"POST /api/user/wechat/notify",
	"POST /api/stripe/webhook",
	"POST /api/creem/webhook",
	"POST /api/waffo/webhook",
	"POST /api/waffo-pancake/webhook/:env",
	"GET /api/user/epay/notify",
	"POST /api/user/epay/notify",
	"GET /api/subscription/epay/notify",
	"POST /api/subscription/epay/notify",
	"GET /api/subscription/epay/return",
	"POST /api/subscription/epay/return",

	// API key authentication (TokenAuthReadOnly).
	"GET /api/usage/token/",
	"GET /api/log/token",
}

// accessTokenHelperRoutes are declared through handlePermissionRoute outside
// channelPermissionRoutes.
var accessTokenHelperRoutes = map[string]authz.Permission{
	"GET /api/task_plugin_options": authz.TaskPluginBind,
	"GET /api/audit":               authz.AuditRead,
}

var authRefreshTestSequence atomic.Uint32

func newAccessTokenScopeTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// A permission route that is also in the static table panics here.
	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetDashboardRouter(engine)
		SetRelayRouter(engine)
		SetTaskPluginProtocolRouter(engine)
		SetVideoRouter(engine)
		SetTaskRouter(engine)
	})
	return engine
}

func TestAuthRefreshHasIndependentBoundedRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			previousRedis, previousRDB := common.RedisEnabled, common.RDB
			previousGlobal := common.GlobalApiRateLimitEnable
			previousCritical, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
			previousSecure, previousTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
			t.Cleanup(func() {
				common.RedisEnabled, common.RDB = previousRedis, previousRDB
				common.GlobalApiRateLimitEnable = previousGlobal
				common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousCritical, previousNum, previousDuration
				common.SessionCookieSecure, common.SessionCookieTrustedURLs = previousSecure, previousTrusted
			})
			common.RedisEnabled, common.RDB = false, nil
			var redisServer *miniredis.Miniredis
			if backend == "redis" {
				redisServer = miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
				t.Cleanup(func() { _ = client.Close() })
				common.RedisEnabled, common.RDB = true, client
			}
			common.GlobalApiRateLimitEnable = false
			common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 2, 1200
			common.SessionCookieSecure, common.SessionCookieTrustedURLs = true, nil
			engine := gin.New()
			require.NoError(t, engine.SetTrustedProxies(nil))
			SetApiRouter(engine)
			engine.GET("/security-budget", middleware.CriticalRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := func(method, path, remoteAddr, origin string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, nil)
				r.RemoteAddr = remoteAddr
				r.Header.Set("Origin", origin)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, r)
				return w
			}
			sequence := authRefreshTestSequence.Add(1)
			remoteAddr := fmt.Sprintf("[2001:db8:%x:%x::1]:1234", sequence>>16, sequence&0xffff)
			otherRemoteAddr := fmt.Sprintf("[2001:db8:%x:%x::2]:1234", sequence>>16, sequence&0xffff)
			for range 2 {
				require.Equal(t, http.StatusNoContent, request(http.MethodGet, "/security-budget", remoteAddr, "http://example.com").Code)
			}
			// Password and SMS routes retain their shared credential-attempt limit.
			for _, path := range []string{"/api/user/login", "/api/user/phone/challenge", "/api/user/phone/login"} {
				require.Equal(t, http.StatusTooManyRequests, request(http.MethodPost, path, remoteAddr, "http://example.com").Code)
			}
			// Missing credentials still reach normal authentication, never a
			// successful session, even when the credential-attempt bucket is full.
			for range 120 {
				response := request(http.MethodPost, "/api/user/auth/refresh", remoteAddr, "http://example.com")
				require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			}
			response := request(http.MethodPost, "/api/user/auth/refresh", remoteAddr, "http://example.com")
			require.Equal(t, http.StatusTooManyRequests, response.Code)
			assert.Equal(t, "60", response.Header().Get("Retry-After"))
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
			assert.Empty(t, response.Header().Values("Set-Cookie"), "a transient limit must not clear refresh credentials")
			assert.JSONEq(t, `{"success":false,"code":"AUTH_REFRESH_RATE_LIMITED","message":"Too many requests","message_key":"Too many requests"}`, response.Body.String())
			assert.Equal(t, http.StatusForbidden, request(http.MethodPost, "/api/user/auth/refresh", remoteAddr, "https://untrusted.example").Code)
			assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/api/user/auth/refresh", otherRemoteAddr, "http://example.com").Code)
			assert.Equal(t, http.StatusNoContent, request(http.MethodGet, "/security-budget", otherRemoteAddr, "http://example.com").Code)

			common.CriticalRateLimitEnable = false
			bounded := gin.New()
			bounded.POST("/refresh", middleware.AuthRefreshRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			boundedRequest := httptest.NewRequest(http.MethodPost, "/refresh", nil)
			boundedRequest.RemoteAddr = remoteAddr
			boundedResponse := httptest.NewRecorder()
			bounded.ServeHTTP(boundedResponse, boundedRequest)
			assert.Equal(t, http.StatusTooManyRequests, boundedResponse.Code, "refresh retry protection stays enabled independently")
			if redisServer != nil {
				redisServer.FastForward(time.Minute)
				assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/api/user/auth/refresh", remoteAddr, "http://example.com").Code)
				require.NoError(t, common.RDB.Close())
				assert.Equal(t, http.StatusInternalServerError, request(http.MethodPost, "/api/user/auth/refresh", remoteAddr, "http://example.com").Code)
			}
		})
	}
}

func TestAccessTokenRouteRulesCoverEveryDashboardRoute(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	exempt := map[string]bool{}
	for _, key := range accessTokenExemptRoutes {
		exempt[key] = true
	}
	registered := map[string]bool{}
	panel := map[string]bool{}
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") && !strings.HasPrefix(route.Path, "/pg/") {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if !exempt[key] {
			panel[key] = true
		}
	}
	for _, key := range accessTokenExemptRoutes {
		assert.True(t, registered[key], "exempt route %s is no longer registered", key)
	}

	declared := map[string]int{}
	for _, key := range middleware.AccessTokenRouteRuleKeys() {
		declared[key]++
	}
	for key := range panel {
		assert.Equal(t, 1, declared[key], "panel route %s must be declared exactly once", key)
	}
	for key := range declared {
		assert.True(t, panel[key], "declared route %s is not a registered panel route", key)
	}

	permissionRoutes := map[string]authz.Permission{}
	for _, route := range channelPermissionRoutes {
		permissionRoutes[route.method+" "+joinPaths("/api/channel", route.path)] = route.permission
	}
	for key, permission := range accessTokenHelperRoutes {
		permissionRoutes[key] = permission
	}
	for key, permission := range permissionRoutes {
		rule, ok := middleware.AccessTokenRouteRule(key)
		require.True(t, ok, key)
		assert.Equal(t, "scope", rule.Kind(), key)
		assert.Equal(t, service.AccessTokenScopeOf(permission), rule.Scope(), key)
	}
}

func TestAccessTokenCatalogCoversEveryPermission(t *testing.T) {
	catalog := service.AccessTokenCatalog(1, common.RoleRootUser)
	scopes := map[string]bool{}
	resources := map[string]int{}
	for _, group := range catalog {
		for _, resource := range group.Resources {
			resources[resource.Resource]++
			for _, action := range resource.Actions {
				scopes[resource.Resource+":"+action.Action] = true
			}
		}
	}
	for _, permission := range authz.AllPermissions() {
		assert.True(t, scopes[service.AccessTokenScopeOf(permission)], "root catalog lacks %s", service.AccessTokenScopeOf(permission))
	}
	for resource, count := range resources {
		assert.Equal(t, 1, count, "resource %s appears in more than one catalog entry", resource)
	}
}

func TestAccessTokenVerificationScopesAreAllListed(t *testing.T) {
	files, err := filepath.Glob("../service/*.go")
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	var scopes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if !strings.HasPrefix(name.Name, "VerificationScope") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					require.True(t, ok, name.Name)
					scope, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					scopes = append(scopes, scope)
				}
			}
		}
	}
	require.NotEmpty(t, scopes)
	for _, scope := range scopes {
		_, listed := service.AccessTokenVerificationScope(scope)
		assert.True(t, listed, "verification scope %s has no access token rule", scope)
	}
}

// Creation operations deliberately offer no PAT grant: operators use the dashboard session.
func TestCreationContentRoutesHaveExplicitPublicAndSessionBoundaries(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, key := range []string{"GET /api/creation/content", "GET /api/creation/assets/:id", "HEAD /api/creation/assets/:id"} {
		require.True(t, registered[key], key)
		_, declared := middleware.AccessTokenRouteRule(key)
		assert.False(t, declared, key)
		assert.Contains(t, accessTokenExemptRoutes, key)
	}
	for _, key := range []string{"GET /api/creation/admin/content", "PUT /api/creation/admin/content", "POST /api/creation/admin/assets"} {
		require.True(t, registered[key], key)
		rule, declared := middleware.AccessTokenRouteRule(key)
		require.True(t, declared, key)
		assert.Equal(t, "session", rule.Kind(), key)
	}
}

func TestClientModelConfigRequiresAnActiveAdministratorSession(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}, &model.Option{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Log{}, &model.Task{}, &model.Token{}))
	previousDB, previousLog := model.DB, model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis, previousMemory, previousSecret := common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret
	previousSecure := common.SessionCookieSecure
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SessionSecret = "isolated-client-model-config-auth-test"
	common.SessionCookieSecure = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret = previousRedis, previousMemory, previousSecret
		common.SessionCookieSecure = previousSecure
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	engine := newAccessTokenScopeTestEngine(t)
	var adminSession *service.AuthBundle
	var userSession *service.AuthBundle
	legacy := "client-model-config-legacy-test"
	for _, user := range []model.User{
		{Id: 7411, Username: "cm-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "cm-admin"},
		{Id: 7412, Username: "cm-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "cm-user"},
		{Id: 7413, Username: "cm-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "cm-root", AccessToken: &legacy},
	} {
		require.NoError(t, db.Create(&user).Error)
		if user.Role == common.RoleRootUser {
			continue
		}
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "client-model-test")
		require.NoError(t, err)
		if user.Role == common.RoleAdminUser {
			adminSession = bundle
		} else {
			userSession = bundle
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/client-model-config"},
		{http.MethodPut, "/api/client-model-config"},
		{http.MethodGet, "/api/client-model-config/models"},
		{http.MethodGet, "/api/client-model-config/presentation"},
		{http.MethodPut, "/api/client-model-config/presentation"},
		{http.MethodGet, "/api/media-operations"},
		{http.MethodGet, "/api/video-usage"},
	} {
		rule, ok := middleware.AccessTokenRouteRule(route.method + " " + route.path)
		require.True(t, ok)
		assert.Equal(t, "session", rule.Kind())
		for _, identity := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", http.StatusUnauthorized},
			{"ordinary user", userSession.AccessToken, http.StatusForbidden},
			{"legacy root token", legacy, http.StatusForbidden},
			{"administrator", adminSession.AccessToken, http.StatusOK},
		} {
			t.Run(route.method+route.path+identity.name, func(t *testing.T) {
				body := `{"schema_version":1,"default_chat_model":"","model_order":[]}`
				if strings.HasSuffix(route.path, "/presentation") {
					body = `{"schema_version":1,"models":{}}`
				}
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", "http://example.com")
				if identity.token != "" {
					request.Header.Set("Authorization", "Bearer "+identity.token)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				assert.Equal(t, identity.status, recorder.Code, recorder.Body.String())
				assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
			})
		}
	}
	t.Run("administrator pages reject scoped and relay credentials", func(t *testing.T) {
		pat := model.AccessTokenPrefix + strings.Repeat("m", 43)
		credential := model.UserAccessToken{UserId: 7411, Name: "media-admin-fixture", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
		credential.SetScopes([]string{"channel:read"})
		require.NoError(t, db.Create(&credential).Error)
		relayKey := strings.Repeat("r", 48)
		require.NoError(t, db.Create(&model.Token{UserId: 7411, Key: relayKey, Status: common.TokenStatusEnabled, Name: "media-relay-fixture", UnlimitedQuota: true, ExpiredTime: -1}).Error)
		for _, identity := range []struct {
			name, token string
			status      int
		}{
			{"scoped administrator token", pat, http.StatusForbidden},
			{"relay token", "sk-" + relayKey, http.StatusUnauthorized},
		} {
			t.Run(identity.name, func(t *testing.T) {
				for _, route := range []struct{ method, path string }{
					{http.MethodGet, "/api/media-operations"},
					{http.MethodGet, "/api/video-usage"},
					{http.MethodGet, "/api/client-model-config/presentation"},
					{http.MethodPut, "/api/client-model-config/presentation"},
				} {
					request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"schema_version":1,"models":{}}`))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Origin", "http://example.com")
					request.Header.Set("Authorization", "Bearer "+identity.token)
					recorder := httptest.NewRecorder()
					engine.ServeHTTP(recorder, request)
					assert.Equal(t, identity.status, recorder.Code, recorder.Body.String())
					assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
				}
			})
		}
	})
	t.Run("cross origin write is denied", func(t *testing.T) {
		for _, path := range []string{"/api/client-model-config", "/api/client-model-config/presentation"} {
			request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"schema_version":1,"models":{}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://untrusted.example")
			request.Header.Set("Authorization", "Bearer "+adminSession.AccessToken)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
		}
	})
	t.Run("expired administrator session cannot read presentation", func(t *testing.T) {
		require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", adminSession.Session.SID).Update("expires_at", time.Now().Unix()-1).Error)
		request := httptest.NewRequest(http.MethodGet, "/api/client-model-config/presentation", nil)
		request.Header.Set("Authorization", "Bearer "+adminSession.AccessToken)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", adminSession.Session.SID).Update("expires_at", adminSession.Session.ExpiresAt).Error)
	})
	t.Run("revoked administrator session cannot read configuration", func(t *testing.T) {
		_, err := model.RevokeUserSession(7411, adminSession.Session.SID, "test revocation")
		require.NoError(t, err)
		for _, path := range []string{"/api/client-model-config", "/api/client-model-config/presentation"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer "+adminSession.AccessToken)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		}
	})
}

func TestFinancialRoutesPreserveCredentialAndRoleBoundaries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}, &model.Option{}, &model.TopUp{}, &model.UpstreamTopup{}, &model.Log{}, &model.Token{}))
	previousDB, previousLog, previousType := model.DB, model.LOG_DB, common.MainDatabaseType()
	previousRedis, previousMemory, previousSecret := common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret
	previousGlobal, previousCritical := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	previousPhone, previousWechat := common.PhoneLoginEnabled, common.WechatPayEnabled
	previousTransport, previousClientTransport := http.DefaultTransport, http.DefaultClient.Transport
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SessionSecret = "isolated-financial-route-auth-test"
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false
	common.PhoneLoginEnabled, common.WechatPayEnabled = true, false
	var outbound atomic.Int32
	blocked := imageUploadRoundTripper(func(*http.Request) (*http.Response, error) {
		outbound.Add(1)
		return nil, fmt.Errorf("external requests are disabled by the financial route test")
	})
	http.DefaultTransport, http.DefaultClient.Transport = blocked, blocked
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret = previousRedis, previousMemory, previousSecret
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = previousGlobal, previousCritical
		common.PhoneLoginEnabled, common.WechatPayEnabled = previousPhone, previousWechat
		http.DefaultTransport, http.DefaultClient.Transport = previousTransport, previousClientTransport
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		assert.Zero(t, outbound.Load(), "no SMS, payment, or reconciliation request may leave this test")
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy := "financial-route-legacy-root"
	sessions := map[int]*service.AuthBundle{}
	for _, user := range []model.User{
		{Id: 7521, Username: "finance-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "finance-user"},
		{Id: 7522, Username: "finance-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "finance-admin"},
		{Id: 7523, Username: "finance-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "finance-root", AccessToken: &legacy},
	} {
		require.NoError(t, db.Create(&user).Error)
		sessions[user.Role], err = service.CreateLoginSession(user.Id, "password", "127.0.0.1", "finance-route-test")
		require.NoError(t, err)
	}
	expired, err := service.CreateLoginSession(7523, "password", "127.0.0.1", "expired-finance-test")
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", expired.Session.SID).Update("expires_at", time.Now().Unix()-1).Error)
	revoked, err := service.CreateLoginSession(7523, "password", "127.0.0.1", "revoked-finance-test")
	require.NoError(t, err)
	_, err = model.RevokeUserSession(7523, revoked.Session.SID, "isolated regression")
	require.NoError(t, err)
	pats := map[string]string{}
	for i, grant := range []struct {
		name   string
		userID int
		scopes []string
	}{
		{"no scope", 7521, nil},
		{"wallet read", 7521, []string{"wallet:read"}},
		{"wallet write", 7521, []string{"wallet:write"}},
		{"root PAT", 7523, []string{"billing:read", "billing:write", "model:read", "model:write", "option:read", "option:write", "wallet:read", "wallet:write"}},
	} {
		raw := model.AccessTokenPrefix + strings.Repeat(string(rune('a'+i)), 43)
		pat := model.UserAccessToken{UserId: grant.userID, Name: grant.name, TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw)}
		pat.SetScopes(grant.scopes)
		require.NoError(t, db.Create(&pat).Error)
		pats[grant.name] = raw
	}
	relay := strings.Repeat("x", 48)
	require.NoError(t, db.Create(&model.Token{UserId: 7523, Key: relay, Status: common.TokenStatusEnabled, Name: "finance-relay", UnlimitedQuota: true, ExpiredTime: -1}).Error)
	actual := newAccessTokenScopeTestEngine(t)
	gates := gin.New()
	routes := []struct {
		method, path string
		minRole      int
	}{
		{http.MethodGet, "/api/balance-ledger/", common.RoleAdminUser},
		{http.MethodGet, "/api/model-profit/", common.RoleAdminUser},
		{http.MethodGet, "/api/model-profit/cost-rules", common.RoleAdminUser},
		{http.MethodPut, "/api/model-profit/cost-rules", common.RoleRootUser},
		{http.MethodGet, "/api/upstream-topup/", common.RoleAdminUser},
		{http.MethodPost, "/api/upstream-topup/", common.RoleAdminUser},
		{http.MethodPut, "/api/upstream-topup/:id", common.RoleAdminUser},
		{http.MethodDelete, "/api/upstream-topup/:id", common.RoleAdminUser},
		{http.MethodPost, "/api/wechat/reconcile", common.RoleRootUser},
	}
	for _, route := range routes {
		rule, declared := middleware.AccessTokenRouteRule(route.method + " " + route.path)
		require.True(t, declared)
		require.Equal(t, "session", rule.Kind())
		auth := middleware.AdminAuth()
		if route.minRole == common.RoleRootUser {
			auth = middleware.RootAuth()
		}
		// Exercise a successful gate without running any financial write.
		gates.Handle(route.method, route.path, auth, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	request := func(engine *gin.Engine, method, path, token, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://untrusted.example")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, route := range routes {
		for _, actor := range []struct {
			name, token  string
			role, status int
		}{
			{"anonymous", "", 0, http.StatusUnauthorized},
			{"ordinary", sessions[common.RoleCommonUser].AccessToken, common.RoleCommonUser, http.StatusForbidden},
			{"administrator", sessions[common.RoleAdminUser].AccessToken, common.RoleAdminUser, http.StatusNoContent},
			{"root", sessions[common.RoleRootUser].AccessToken, common.RoleRootUser, http.StatusNoContent},
			{"legacy root", legacy, 0, http.StatusForbidden},
			{"scoped root", pats["root PAT"], 0, http.StatusForbidden},
			{"relay", "sk-" + relay, 0, http.StatusUnauthorized},
			{"expired", expired.AccessToken, 0, http.StatusUnauthorized},
			{"revoked", revoked.AccessToken, 0, http.StatusUnauthorized},
		} {
			t.Run(route.method+route.path+"/"+actor.name, func(t *testing.T) {
				status := actor.status
				if actor.role > 0 && actor.role < route.minRole {
					status = http.StatusForbidden
				}
				path := strings.ReplaceAll(route.path, ":id", "invalid")
				w := request(gates, route.method, path, actor.token, `{}`, "")
				assert.Equal(t, status, w.Code, w.Body.String())
				if status != http.StatusNoContent {
					w = request(actual, route.method, path, actor.token, `{}`, "")
					assert.Equal(t, status, w.Code, w.Body.String())
					if actor.name == "legacy root" || actor.name == "scoped root" {
						assert.Contains(t, w.Body.String(), `"code":"AUTH_SESSION_REQUIRED"`)
					}
				}
			})
		}
		t.Run(route.method+route.path+"/cookie alone cannot authorize", func(t *testing.T) {
			w := request(actual, route.method, strings.ReplaceAll(route.path, ":id", "invalid"), "", `{}`, sessions[common.RoleRootUser].RefreshToken)
			assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		})
	}
	for _, entry := range []struct{ path, scope string }{{"/api/user/wechat/order", "wallet:read"}, {"/api/user/wechat/native", "wallet:write"}} {
		method := http.MethodGet
		if entry.scope == "wallet:write" {
			method = http.MethodPost
		}
		rule, declared := middleware.AccessTokenRouteRule(method + " " + entry.path)
		require.True(t, declared)
		require.Equal(t, "scope", rule.Kind())
		require.Equal(t, entry.scope, rule.Scope())
		for _, actor := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", http.StatusUnauthorized}, {"relay", "sk-" + relay, http.StatusUnauthorized},
			{"ordinary session", sessions[common.RoleCommonUser].AccessToken, http.StatusOK},
			{"legacy", legacy, http.StatusOK}, {"no scope", pats["no scope"], http.StatusForbidden},
			{"wallet read", pats["wallet read"], http.StatusForbidden}, {"wallet write", pats["wallet write"], http.StatusForbidden},
			{"expired", expired.AccessToken, http.StatusUnauthorized}, {"revoked", revoked.AccessToken, http.StatusUnauthorized},
		} {
			t.Run(entry.path+"/"+actor.name, func(t *testing.T) {
				status := actor.status
				if actor.name == "wallet read" && entry.scope == "wallet:read" || actor.name == "wallet write" && entry.scope == "wallet:write" {
					status = http.StatusOK
				}
				w := request(actual, method, entry.path, actor.token, `{}`, "")
				assert.Equal(t, status, w.Code, w.Body.String())
				if status == http.StatusOK {
					assert.Contains(t, w.Body.String(), `"success":false`)
				}
			})
		}
	}
	t.Run("real controllers reject invalid financial requests without changes", func(t *testing.T) {
		for _, entry := range []struct {
			method, path string
			status       int
		}{
			{http.MethodGet, "/api/balance-ledger/?category=invalid", http.StatusBadRequest},
			{http.MethodGet, "/api/model-profit/", http.StatusBadRequest},
			{http.MethodGet, "/api/model-profit/cost-rules", http.StatusOK},
			{http.MethodPut, "/api/model-profit/cost-rules", http.StatusBadRequest},
			{http.MethodGet, "/api/upstream-topup/", http.StatusOK},
			{http.MethodPost, "/api/upstream-topup/", http.StatusOK},
			{http.MethodPut, "/api/upstream-topup/invalid", http.StatusOK},
			{http.MethodDelete, "/api/upstream-topup/invalid", http.StatusOK},
			{http.MethodPost, "/api/wechat/reconcile", http.StatusOK},
		} {
			token := sessions[common.RoleAdminUser].AccessToken
			if entry.method == http.MethodPut && entry.path == "/api/model-profit/cost-rules" || entry.path == "/api/wechat/reconcile" {
				token = sessions[common.RoleRootUser].AccessToken
			}
			w := request(actual, entry.method, entry.path, token, `{}`, "")
			assert.Equal(t, entry.status, w.Code, w.Body.String())
			if entry.method != http.MethodGet {
				assert.Contains(t, w.Body.String(), `"success":false`)
			}
		}
		for _, table := range []any{&model.TopUp{}, &model.UpstreamTopup{}} {
			var count int64
			require.NoError(t, db.Model(table).Count(&count).Error)
			assert.Zero(t, count)
		}
	})
	t.Run("wallet scope cannot read another account order", func(t *testing.T) {
		order := model.TopUp{UserId: 7521, TradeNo: "synthetic-existing-order", Amount: 500, Money: 1, Status: common.TopUpStatusPending, PaymentMethod: model.PaymentMethodWechat, PaymentProvider: model.PaymentProviderWechat}
		require.NoError(t, db.Create(&order).Error)
		for _, actor := range []struct {
			name, token string
			owns        bool
		}{{"owner session", sessions[common.RoleCommonUser].AccessToken, true}, {"owner PAT", pats["wallet read"], true}, {"other root PAT", pats["root PAT"], false}, {"other root session", sessions[common.RoleRootUser].AccessToken, false}} {
			w := request(actual, http.MethodGet, "/api/user/wechat/order?out_trade_no="+order.TradeNo, actor.token, "", "")
			require.Equal(t, http.StatusOK, w.Code, actor.name+" "+w.Body.String())
			assert.Contains(t, w.Body.String(), fmt.Sprintf(`"success":%t`, actor.owns), actor.name)
			if !actor.owns {
				assert.NotContains(t, w.Body.String(), order.TradeNo)
			}
		}
		var after model.TopUp
		require.NoError(t, db.First(&after, order.Id).Error)
		assert.Equal(t, order, after)
	})
	t.Run("anonymous routes retain controller validation", func(t *testing.T) {
		for _, path := range []string{"/api/user/phone/challenge", "/api/user/phone/login"} {
			_, declared := middleware.AccessTokenRouteRule(http.MethodPost + " " + path)
			require.False(t, declared)
			require.Contains(t, accessTokenExemptRoutes, http.MethodPost+" "+path)
			w := request(actual, http.MethodPost, path, "", `{}`, "")
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"PHONE_INVALID_PARAMETERS"`)
		}
		w := request(actual, http.MethodPost, "/api/user/wechat/notify", "", `{}`, "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"code":"FAIL","message":"wechat pay not enabled"}`, w.Body.String())
	})
	t.Run("public callback still verifies raw body signature serial and freshness", func(t *testing.T) {
		previous := []string{common.WechatAppId, common.WechatMchId, common.WechatApiV3Key, common.WechatMerchantSerial, common.WechatMerchantKeyPath, common.WechatPlatformKeyId, common.WechatPlatformKeyPath, common.WechatNotifyUrl}
		defer func() {
			common.WechatPayEnabled = false
			common.WechatAppId, common.WechatMchId, common.WechatApiV3Key, common.WechatMerchantSerial = previous[0], previous[1], previous[2], previous[3]
			common.WechatMerchantKeyPath, common.WechatPlatformKeyId, common.WechatPlatformKeyPath, common.WechatNotifyUrl = previous[4], previous[5], previous[6], previous[7]
		}()
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		require.NoError(t, err)
		publicPath := filepath.Join(t.TempDir(), "synthetic-platform-public.pem")
		require.NoError(t, os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0o600))
		common.WechatPayEnabled = true
		common.WechatAppId, common.WechatMchId, common.WechatApiV3Key, common.WechatMerchantSerial = "synthetic-app", "synthetic-merchant", strings.Repeat("s", 32), "synthetic-merchant-serial"
		common.WechatMerchantKeyPath, common.WechatPlatformKeyId, common.WechatPlatformKeyPath, common.WechatNotifyUrl = "unused-synthetic-private-key", "synthetic-platform-serial", publicPath, "https://callback.invalid"
		const body = `{"event_type":"TRANSACTION.CLOSED"}`
		for _, entry := range []struct {
			name, timestamp, serial, signedBody string
			status                              int
		}{
			{"valid non-settlement event", strconv.FormatInt(time.Now().Unix(), 10), "synthetic-platform-serial", body, http.StatusOK},
			{"tampered body", strconv.FormatInt(time.Now().Unix(), 10), "synthetic-platform-serial", body + " ", http.StatusUnauthorized},
			{"wrong serial", strconv.FormatInt(time.Now().Unix(), 10), "wrong-platform-serial", body, http.StatusUnauthorized},
			{"stale notification", strconv.FormatInt(time.Now().Add(-24*time.Hour).Unix(), 10), "synthetic-platform-serial", body, http.StatusUnauthorized},
			{"missing signature", strconv.FormatInt(time.Now().Unix(), 10), "synthetic-platform-serial", "", http.StatusUnauthorized},
		} {
			t.Run(entry.name, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodPost, "/api/user/wechat/notify", strings.NewReader(body))
				r.Header.Set("Wechatpay-Timestamp", entry.timestamp)
				r.Header.Set("Wechatpay-Nonce", "synthetic-notification-nonce")
				r.Header.Set("Wechatpay-Serial", entry.serial)
				if entry.signedBody != "" {
					digest := sha256.Sum256([]byte(strings.Join([]string{entry.timestamp, "synthetic-notification-nonce", entry.signedBody, ""}, "\n")))
					signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
					require.NoError(t, err)
					r.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
				}
				w := httptest.NewRecorder()
				actual.ServeHTTP(w, r)
				assert.Equal(t, entry.status, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), map[bool]string{true: `"code":"SUCCESS"`, false: `"code":"FAIL"`}[entry.status == http.StatusOK])
			})
		}
	})
	t.Run("disabled account cannot reuse a live session", func(t *testing.T) {
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", 7523).Update("status", common.UserStatusDisabled).Error)
		for _, route := range routes {
			w := request(actual, route.method, strings.ReplaceAll(route.path, ":id", "invalid"), sessions[common.RoleRootUser].AccessToken, `{}`, "")
			assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		}
	})

}
