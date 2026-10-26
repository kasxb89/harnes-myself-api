package router

import (
	"bytes"
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
	"image"
	"image/png"
	"maps"
	"mime/multipart"
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
	if model.DB != nil {
		// Full HTTP fixtures must bootstrap authorization just as application startup does.
		require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
		previousMaster := common.IsMasterNode
		common.IsMasterNode = true
		err := authz.Init(model.DB)
		common.IsMasterNode = previousMaster
		require.NoError(t, err)
	}
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
			previousGlobalNum, previousGlobalDuration := common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
			previousCritical, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
			previousSecure, previousTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
			t.Cleanup(func() {
				common.RedisEnabled, common.RDB = previousRedis, previousRDB
				common.GlobalApiRateLimitEnable = previousGlobal
				common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousGlobalNum, previousGlobalDuration
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
			common.GlobalApiRateLimitEnable = true
			common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = 2, 1200
			common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 2, 1200
			common.SessionCookieSecure, common.SessionCookieTrustedURLs = true, nil
			engine := gin.New()
			require.NoError(t, engine.SetTrustedProxies(nil))
			SetApiRouter(engine)
			engine.GET("/general-budget", middleware.GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
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
			for range 2 {
				require.Equal(t, http.StatusNoContent, request(http.MethodGet, "/general-budget", remoteAddr, "http://example.com").Code)
			}
			require.Equal(t, http.StatusTooManyRequests, request(http.MethodGet, "/general-budget", remoteAddr, "http://example.com").Code)
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
	for _, resource := range authz.Catalog() {
		for _, action := range resource.Actions {
			permission := authz.Permission{Resource: resource.Resource, Action: action.Action}
			assert.Equal(t, !resource.SessionOnly, scopes[service.AccessTokenScopeOf(permission)], "PAT catalog boundary for %s", service.AccessTokenScopeOf(permission))
		}
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

// This fixture exercises real HTTP gates and option/authorization persistence
// against a fresh, explicitly named database. No provider request is permitted.
func TestAdminFeaturePermissionBoundaries(t *testing.T) {
	dsn := os.Getenv("TEST_ADMIN_PERMISSIONS_SQL_DSN")
	if dsn == "" {
		dsn = "local"
	} else {
		require.Contains(t, dsn, "test_admin_permissions_")
	}
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", "")
	t.Setenv("CREATION_CONTENT_DIR", t.TempDir())
	previousDB, previousLogDB, previousPath := model.DB, model.LOG_DB, common.SQLitePath
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis, previousMemory := common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled
	previousGlobal, previousCritical, previousSecure, previousSecret := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure, common.SessionSecret
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = false, false, false
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure = false, false, true
	common.SessionSecret, common.SQLitePath = "isolated-admin-permission-fixture", filepath.Join(t.TempDir(), "permissions.db")
	require.NoError(t, model.InitDB())
	db := model.DB
	model.LOG_DB = db
	common.SetDatabaseTypes(common.MainDatabaseType(), common.MainDatabaseType())
	require.False(t, db.Migrator().HasTable(&model.User{}), "refuse a shared or previously used database")
	models := []any{&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Token{}, &model.AuditLog{}, &model.Option{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.UpstreamTopup{}, &model.TopUp{}, &model.Log{}}
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.AutoMigrate(models...))
	var version string
	query := "SELECT version()"
	if common.MainDatabaseType() == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("Admin permissions actual database: %s %s", common.MainDatabaseType(), version)
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB, common.SQLitePath = previousDB, previousLogDB, previousPath
		common.SetDatabaseTypes(previousMain, previousLog)
		common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = previousMaster, previousRedis, previousMemory
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionCookieSecure, common.SessionSecret = previousGlobal, previousCritical, previousSecure, previousSecret
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	transport := &customerServiceNoNetwork{}
	previousTransport, previousClientTransport := http.DefaultTransport, http.DefaultClient.Transport
	http.DefaultTransport, http.DefaultClient.Transport = transport, transport
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient.Transport = previousTransport, previousClientTransport
		assert.Zero(t, transport.calls)
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	credentials, users := map[string]string{}, map[string]int{}
	for _, identity := range []struct {
		name string
		role int
	}{{"admin", common.RoleAdminUser}, {"root", common.RoleRootUser}, {"member", common.RoleCommonUser}} {
		user := model.User{Username: "permission-" + identity.name, AffCode: "perm-" + identity.name, Role: identity.role, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "isolated-admin-permission-fixture")
		require.NoError(t, err)
		credentials[identity.name], users[identity.name] = bundle.AccessToken, user.Id
	}

	legacy := "admin-permission-legacy-root"
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", users["root"]).Update("access_token", legacy).Error)
	pat := model.AccessTokenPrefix + strings.Repeat("z", 43)
	credential := model.UserAccessToken{UserId: users["root"], Name: "unusable session-only scope fixture", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
	// Even a directly stored malicious scope cannot bypass the route session gate.
	require.NoError(t, credential.SetScopes([]string{"customer_service:read", "customer_service:write", "announcements:read", "announcements:write", "option:write"}))
	require.NoError(t, db.Create(&credential).Error)
	engine := newAccessTokenScopeTestEngine(t)
	request := func(method, path, credential, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, resource := range authz.Catalog() {
		if !resource.SessionOnly {
			continue
		}
		require.NotEmpty(t, resource.GroupKey)
		require.NotEmpty(t, resource.DescriptionKey)
		for _, action := range resource.Actions {
			_, err := service.NormalizeAccessTokenScopes(users["root"], common.RoleRootUser, []string{resource.Resource + ":" + action.Action})
			assert.ErrorIs(t, err, service.ErrAccessTokenScopeInvalid, "session-only resources must never become grantable PAT scopes")
		}
		for _, definition := range service.AccessTokenScopeDictionary() {
			assert.NotEqual(t, resource.Resource, definition.Resource)
		}
	}
	routes := []struct {
		method, path string
		permission   authz.Permission
	}{
		{"GET", "/api/customer-service", authz.CustomerServiceRead}, {"PUT", "/api/customer-service", authz.CustomerServiceWrite}, {"POST", "/api/customer-service/assets", authz.CustomerServiceWrite},
		{"GET", "/api/announcements", authz.AnnouncementsRead}, {"PUT", "/api/announcements", authz.AnnouncementsWrite},
		{"GET", "/api/client-model-config", authz.ClientModelConfigRead}, {"GET", "/api/client-model-config/models", authz.ClientModelConfigRead}, {"GET", "/api/client-model-config/presentation", authz.ClientModelConfigRead},
		{"PUT", "/api/client-model-config", authz.ClientModelConfigWrite}, {"PUT", "/api/client-model-config/presentation", authz.ClientModelConfigWrite},
		{"GET", "/api/creation/admin/content", authz.CreationContentRead}, {"PUT", "/api/creation/admin/content", authz.CreationContentWrite}, {"POST", "/api/creation/admin/assets", authz.CreationContentWrite},
		{"GET", "/api/balance-ledger/", authz.BalanceLedgerRead}, {"GET", "/api/model-profit/", authz.ModelProfitsRead}, {"GET", "/api/model-profit/cost-rules", authz.ModelProfitsRead},
		{"GET", "/api/upstream-topup/", authz.UpstreamTopupsRead}, {"POST", "/api/upstream-topup/", authz.UpstreamTopupsWrite}, {"PUT", "/api/upstream-topup/1", authz.UpstreamTopupsWrite}, {"DELETE", "/api/upstream-topup/1", authz.UpstreamTopupsWrite},
		{"GET", "/api/client-activity/summary", authz.UserActivityRead}, {"GET", "/api/client-activity/sessions", authz.UserActivityRead}, {"GET", "/api/client-activity/sessions/1", authz.UserActivityRead},
		{"GET", "/api/media-operations", authz.MediaOperationsRead}, {"GET", "/api/media-concurrency", authz.MediaOperationsRead}, {"GET", "/api/video-usage", authz.VideoUsageRead},
		{"GET", "/api/operations/overview", authz.OperationsRead}, {"GET", "/api/data/", authz.OperationsRead}, {"GET", "/api/data/users", authz.OperationsRead}, {"GET", "/api/data/flow", authz.OperationsRead},
		{"GET", "/api/operations/server-load", authz.DiagnosticsRead}, {"GET", "/api/status/test", authz.DiagnosticsRead}, {"GET", "/api/log/", authz.DiagnosticsRead}, {"GET", "/api/log/stat", authz.DiagnosticsRead}, {"GET", "/api/log/search", authz.DiagnosticsRead}, {"GET", "/api/log/export", authz.DiagnosticsRead}, {"GET", "/api/log/channel_affinity_usage_cache", authz.DiagnosticsRead},
	}

	for _, entry := range []struct{ method, path string }{{"GET", "/api/announcements"}, {"PUT", "/api/announcements"}, {"GET", "/api/customer-service"}, {"PUT", "/api/customer-service"}, {"POST", "/api/customer-service/assets"}, {"GET", "/api/operations/server-load"}} {
		for _, token := range []string{legacy, pat} {
			w := request(entry.method, entry.path, token, `{}`, "http://example.com")
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "AUTH_SESSION_REQUIRED")
		}
	}
	for _, route := range routes {
		t.Run("revocation "+route.method+" "+route.path, func(t *testing.T) {
			require.NoError(t, authz.SetUserPermissions(users["admin"], authz.PermissionsMap{route.permission.Resource: {route.permission.Action: false}}))
			w := request(route.method, route.path, credentials["admin"], `{}`, "http://example.com")
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "insufficient privileges")
			require.Equal(t, http.StatusForbidden, request(route.method, route.path, credentials["member"], `{}`, "http://example.com").Code)
			require.NoError(t, authz.ClearUserPermissions(users["admin"]))
		})
	}
	t.Run("server load is an authenticated read-only diagnostic", func(t *testing.T) {
		path := "/api/operations/server-load"
		assert.Equal(t, http.StatusUnauthorized, request("GET", path, "", "", "http://example.com").Code)
		require.NoError(t, authz.ClearUserPermissions(users["admin"]))
		w := request("GET", path, credentials["admin"], "", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		assert.Contains(t, w.Body.String(), `"schema_version":1`)
		assert.Contains(t, w.Body.String(), `"refresh_interval_seconds":10`)
		assert.NotContains(t, w.Body.String(), "permission-fixture")
		assert.NotContains(t, w.Body.String(), "dsn")
	})
	t.Run("dedicated announcement grant preserves root system boundary", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, request("GET", "/api/announcements", credentials["admin"], "", "http://example.com").Code)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return authz.SetUserPermissionsInTx(tx, users["admin"], authz.PermissionsMap{"announcements": {"read": true, "write": true}})
		}))
		require.NoError(t, authz.ReloadPolicy())
		body := `{"console_setting.announcements":"[{\"id\":1,\"title\":\"中文公告\",\"content\":\"测试内容\",\"publishDate\":\"2026-10-23T01:00:00Z\",\"type\":\"default\"}]","console_setting.announcements_enabled":"false"}`
		w := request("PUT", "/api/announcements", credentials["admin"], body, "http://example.com")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `updatedAt`)
		w = request("GET", "/api/announcements", credentials["admin"], "", "http://example.com")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "中文公告")
		assert.Equal(t, http.StatusForbidden, request("GET", "/api/option/", credentials["admin"], "", "http://example.com").Code)
		assert.Equal(t, http.StatusForbidden, request("PUT", "/api/announcements", credentials["admin"], body, "https://untrusted.invalid").Code)
		before := w.Body.String()
		for _, invalid := range []string{`{}`, `{"console_setting.announcements":"[]","console_setting.announcements_enabled":"true","SMTPToken":"forbidden"}`, `{"console_setting.announcements":null,"console_setting.announcements_enabled":"true"}`, `{"console_setting.announcements":"[]","console_setting.announcements":"[]","console_setting.announcements_enabled":"true"}`, `{"console_setting.announcements":"[]","console_setting.announcements_enabled":true}`} {
			assert.Equal(t, http.StatusBadRequest, request("PUT", "/api/announcements", credentials["admin"], invalid, "http://example.com").Code)
			assert.Equal(t, before, request("GET", "/api/announcements", credentials["admin"], "", "http://example.com").Body.String())
		}
		require.NoError(t, authz.SetUserPermissions(users["admin"], authz.PermissionsMap{"announcements": {"read": false, "write": false}}))
		assert.Equal(t, http.StatusForbidden, request("GET", "/api/announcements", credentials["admin"], "", "http://example.com").Code)
	})
	t.Run("customer upload works without creation privileges", func(t *testing.T) {
		require.NoError(t, authz.SetUserPermissions(users["admin"], authz.PermissionsMap{"creation_content": {"read": false, "write": false}, "customer_service": {"read": true, "write": true}}))
		var pngData bytes.Buffer
		require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
		for _, sample := range []struct {
			data   []byte
			status int
		}{{pngData.Bytes(), http.StatusOK}, {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), http.StatusBadRequest}} {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			file, err := writer.CreateFormFile("file", "二维码.png")
			require.NoError(t, err)
			_, err = file.Write(sample.data)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			r := httptest.NewRequest("POST", "/api/customer-service/assets", &body)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			r.Header.Set("Authorization", "Bearer "+credentials["admin"])
			r.Header.Set("Origin", "http://example.com")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)
			require.Equal(t, sample.status, w.Code, w.Body.String())
		}
		assert.Equal(t, http.StatusForbidden, request("POST", "/api/creation/admin/assets", credentials["admin"], `{}`, "http://example.com").Code)
		for _, path := range []string{"/api/model-profit/cost-rules", "/api/media-concurrency"} {
			assert.Equal(t, http.StatusForbidden, request("PUT", path, credentials["admin"], `{}`, "http://example.com").Code)
		}
		assert.Equal(t, http.StatusForbidden, request("POST", "/api/media-concurrency/refresh", credentials["admin"], `{}`, "http://example.com").Code)
	})

	t.Run("operating data and upstream records have independent server projections", func(t *testing.T) {
		require.NoError(t, db.Create(&model.UpstreamTopup{Upstream: "fixture-provider", AmountCents: 12345, Currency: "CNY", PaidAt: time.Now().Unix(), Note: "UPSTREAM_RECORD_PRIVATE_NOTE"}).Error)
		require.NoError(t, db.Create(&model.TopUp{UserId: users["member"], Money: 25, TradeNo: "permission-fixture-credit", PaymentProvider: "fixture-provider", Status: common.TopUpStatusSuccess, CompleteTime: time.Now().Unix()}).Error)
		require.NoError(t, db.Create(&model.Log{UserId: users["member"], Type: model.LogTypeConsume, Quota: 500, CreatedAt: time.Now().Unix()}).Error)
		decode := func(w *httptest.ResponseRecorder) map[string]any {
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var envelope struct {
				Success bool           `json:"success"`
				Data    map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &envelope))
			require.True(t, envelope.Success, w.Body.String())
			return envelope.Data
		}
		require.NoError(t, authz.ClearUserPermissions(users["admin"]))
		legacyFull := decode(request("GET", "/api/upstream-topup/", credentials["admin"], "", "http://example.com"))
		assert.Contains(t, legacyFull, "items")
		assert.Contains(t, legacyFull, "topup_users")
		assert.Contains(t, legacyFull, "recent_topups")
		assert.Equal(t, float64(500), legacyFull["consumed_quota"])
		overview := decode(request("GET", "/api/operations/overview", credentials["admin"], "", "http://example.com"))
		delete(legacyFull, "items")
		assert.Equal(t, legacyFull, overview)
		require.NoError(t, authz.SetUserPermissions(users["admin"], authz.PermissionsMap{"operations": {"read": false}, "upstream_topups": {"read": true}}))
		records := decode(request("GET", "/api/upstream-topup/", credentials["admin"], "", "http://example.com"))
		require.Len(t, records, 2)
		assert.Contains(t, records, "items")
		assert.Contains(t, records, "spent_cents")
		assert.Equal(t, http.StatusForbidden, request("GET", "/api/operations/overview", credentials["admin"], "", "http://example.com").Code)
		require.NoError(t, authz.SetUserPermissions(users["admin"], authz.PermissionsMap{"operations": {"read": true}, "upstream_topups": {"read": false}}))
		w := request("GET", "/api/operations/overview", credentials["admin"], "", "http://example.com")
		onlyOverview := decode(w)
		assert.Equal(t, overview, onlyOverview)
		assert.NotContains(t, w.Body.String(), "UPSTREAM_RECORD_PRIVATE_NOTE")
		assert.Equal(t, http.StatusForbidden, request("GET", "/api/upstream-topup/", credentials["admin"], "", "http://example.com").Code)
		for _, token := range []string{pat, legacy} {
			w := request("GET", "/api/operations/overview", token, "", "http://example.com")
			assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "AUTH_SESSION_REQUIRED")
		}
		rootLegacy := decode(request("GET", "/api/upstream-topup/", credentials["root"], "", "http://example.com"))
		assert.Contains(t, rootLegacy, "items")
		delete(rootLegacy, "items")
		assert.Equal(t, overview, rootLegacy)
		var rows int64
		require.NoError(t, db.Model(&model.TopUp{}).Count(&rows).Error)
		assert.Equal(t, int64(1), rows)
		require.NoError(t, db.Model(&model.Log{}).Count(&rows).Error)
		assert.Equal(t, int64(1), rows)
	})
	for range 2 {
		require.NoError(t, db.AutoMigrate(models...))
		require.NoError(t, authz.ReloadPolicy())
		assert.False(t, authz.Can(users["admin"], common.RoleAdminUser, authz.AnnouncementsRead))
	}
}

// The batch endpoint must enforce the same owner and PAT scope boundary as
// existing self-usage reads, including when called through the full API router.
func TestUsageReceiptBatchPreservesAuthenticationAndScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/receipt-router.db"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}, &model.Option{}, &model.Log{}, &model.Token{}))
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldSecret := common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret
	oldGlobal := common.GlobalApiRateLimitEnable
	oldGlobalNum, oldGlobalDuration := common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SessionSecret = "isolated-usage-receipt-router-test"
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = 360, 180
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret = oldRedis, oldMemory, oldSecret
		common.GlobalApiRateLimitEnable = oldGlobal
		common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = oldGlobalNum, oldGlobalDuration
		sqlDB, closeErr := db.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy := "receipt-batch-legacy-owner"
	owner := model.User{Id: 7631, Username: "receipt-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "receipt-owner", AccessToken: &legacy, Quota: 500}
	other := model.User{Id: 7632, Username: "receipt-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "receipt-other", Quota: 700}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	bundle, err := service.CreateLoginSession(owner.Id, "password", "127.0.0.1", "receipt-router-test")
	require.NoError(t, err)
	tokens := map[string]string{}
	for i, scope := range []string{"usage:read", "profile:read"} {
		raw := model.AccessTokenPrefix + strings.Repeat(string(rune('a'+i)), 43)
		pat := model.UserAccessToken{UserId: owner.Id, Name: scope, TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw)}
		pat.SetScopes([]string{scope})
		require.NoError(t, db.Create(&pat).Error)
		tokens[scope] = raw
	}
	relay := strings.Repeat("r", 48)
	require.NoError(t, db.Create(&model.Token{UserId: owner.Id, Key: relay, Status: common.TokenStatusEnabled, Name: "receipt-relay", UnlimitedQuota: true, ExpiredTime: -1}).Error)
	for _, entry := range []model.Log{
		{UserId: owner.Id, Type: model.LogTypeConsume, CreatedAt: time.Now().Unix(), RequestId: "shared-receipt", ModelName: "yunshu-flash", Quota: 11},
		{UserId: other.Id, Type: model.LogTypeConsume, CreatedAt: time.Now().Unix(), RequestId: "shared-receipt", ModelName: "private-other-model", Quota: 99},
		{UserId: other.Id, Type: model.LogTypeConsume, CreatedAt: time.Now().Unix(), RequestId: "other-only", ModelName: "private-other-model", Quota: 123},
	} {
		require.NoError(t, db.Create(&entry).Error)
	}
	engine := newAccessTokenScopeTestEngine(t)
	require.NoError(t, engine.SetTrustedProxies(nil))
	for _, identity := range []struct {
		name, token string
		status      int
	}{
		{"session", bundle.AccessToken, http.StatusOK},
		{"usage PAT", tokens["usage:read"], http.StatusOK},
		{"legacy compatibility", legacy, http.StatusOK},
		{"wrong PAT scope", tokens["profile:read"], http.StatusForbidden},
		{"anonymous", "", http.StatusUnauthorized},
		{"relay key", relay, http.StatusUnauthorized},
	} {
		t.Run(identity.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/log/self/batch?user_id=7632", strings.NewReader(`{"request_ids":["shared-receipt","other-only"]}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("New-Api-User", "7632")
			if identity.token != "" {
				request.Header.Set("Authorization", "Bearer "+identity.token)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, identity.status, response.Code, response.Body.String())
			if identity.status != http.StatusOK {
				return
			}
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
			var body struct {
				Success bool `json:"success"`
				Data    struct {
					Items []struct {
						Status string `json:"status"`
						Logs   []struct {
							Quota int `json:"quota"`
						} `json:"consume_logs"`
						Refund     string `json:"refund_status"`
						Settlement string `json:"settlement_status"`
					} `json:"items"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			require.True(t, body.Success)
			require.Len(t, body.Data.Items, 2)
			assert.Equal(t, "found", body.Data.Items[0].Status)
			require.Len(t, body.Data.Items[0].Logs, 1)
			assert.Equal(t, 11, body.Data.Items[0].Logs[0].Quota)
			assert.Equal(t, "not_found", body.Data.Items[1].Status)
			assert.Empty(t, body.Data.Items[1].Logs)
			assert.Equal(t, "unknown", body.Data.Items[0].Refund)
			assert.Equal(t, "unknown", body.Data.Items[0].Settlement)
			assert.NotContains(t, response.Body.String(), "private-other-model")
		})
	}
	var current model.User
	require.NoError(t, db.First(&current, owner.Id).Error)
	assert.Equal(t, owner.Quota, current.Quota, "read receipts never change wallet quota")
}
