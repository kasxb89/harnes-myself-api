package router

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
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
		{http.MethodGet, "/api/media-operations"},
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
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"schema_version":1,"default_chat_model":"","model_order":[]}`))
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
	t.Run("media operations rejects scoped and relay credentials", func(t *testing.T) {
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
				request := httptest.NewRequest(http.MethodGet, "/api/media-operations", nil)
				request.Header.Set("Authorization", "Bearer "+identity.token)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)
				assert.Equal(t, identity.status, recorder.Code, recorder.Body.String())
				assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
			})
		}
	})
	t.Run("cross origin write is denied", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPut, "/api/client-model-config", strings.NewReader(`{"schema_version":1,"default_chat_model":"","model_order":[]}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://untrusted.example")
		request.Header.Set("Authorization", "Bearer "+adminSession.AccessToken)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusForbidden, recorder.Code)
	})
	t.Run("revoked administrator session cannot read configuration", func(t *testing.T) {
		_, err := model.RevokeUserSession(7411, adminSession.Session.SID, "test revocation")
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodGet, "/api/client-model-config", nil)
		request.Header.Set("Authorization", "Bearer "+adminSession.AccessToken)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	})
}
