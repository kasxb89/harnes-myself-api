package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRateLimitMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())

	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() {
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	return redisServer, redisClient
}

func performRateLimitRequest(router http.Handler, path string, remoteAddr string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remoteAddr
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRedisIPRateLimiterThresholdTTLAndNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/limited", rateLimitFactory(2, 37, "TEST"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.10:12345"
	legacyKey := "rateLimit:TEST192.0.2.10"
	_, err := redisServer.Push(legacyKey, "legacy-list-entry")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", remoteAddr).Code)
	limitedResponse := performRateLimitRequest(router, "/limited", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, limitedResponse.Code)
	assert.Equal(t, "37", limitedResponse.Header().Get("Retry-After"))

	key := redisIPRateLimitKey("TEST", "192.0.2.10")
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "3", count)
	assert.Equal(t, 37*time.Second, redisServer.TTL(key))
	assert.True(t, redisServer.Exists(legacyKey), "the v2 counter must not touch an old list key")
}

func TestRedisUserRateLimiterUsesSharedFixedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	router.GET(
		"/limited",
		func(c *gin.Context) { c.Set("id", 42) },
		userRateLimitFactory(1, 23, "USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/limited", "192.0.2.20:12345").Code)
	assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/limited", "198.51.100.20:12345").Code)

	key := redisUserRateLimitKey("USER", 42)
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, 23*time.Second, redisServer.TTL(key))
}

func TestRedisEmailVerificationRateLimiterPreservesResponseAndTTL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer, _ := useRateLimitMiniRedis(t)

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/verify", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	remoteAddr := "192.0.2.30:12345"
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/verify", remoteAddr).Code)
	response := performRateLimitRequest(router, "/verify", remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "30", response.Header().Get("Retry-After"))
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	assert.JSONEq(t, `{"success":false,"code":"API_RATE_LIMITED","message":"Please wait 30 seconds before requesting another verification code.","message_key":"Please wait {{seconds}} seconds before requesting another verification code.","message_params":{"seconds":30}}`, response.Body.String())

	key := redisIPRateLimitKey(EmailVerificationRateLimitMark, "192.0.2.30")
	assert.True(t, redisServer.Exists(key))
	assert.Equal(t, time.Duration(EmailVerificationDuration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowIsAtomicUnderConcurrency(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const (
		requestCount = 20
		maximumCount = 7
		duration     = int64(41)
	)
	key := redisIPRateLimitKey("CONCURRENT", "192.0.2.40")

	var allowedCount atomic.Int64
	errorsFound := make(chan error, requestCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for range requestCount {
		go func() {
			defer waitGroup.Done()
			allowed, _, _, err := redisFixedWindowTake(context.Background(), key, maximumCount, duration)
			if err != nil {
				errorsFound <- err
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}

	assert.Equal(t, int64(maximumCount), allowedCount.Load())
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "20", count)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))
}

func TestRedisFixedWindowResetsAtBoundary(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(10)
	key := redisIPRateLimitKey("BOUNDARY", "192.0.2.50")

	for range 2 {
		allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
	allowed, _, _, err := redisFixedWindowTake(context.Background(), key, 2, duration)
	require.NoError(t, err)
	assert.False(t, allowed)

	// This reset is intentional fixed-window behavior. A client can consume one
	// full allowance immediately before and another immediately after a boundary.
	redisServer.FastForward(time.Duration(duration) * time.Second)
	for range 2 {
		allowed, _, _, err = redisFixedWindowTake(context.Background(), key, 2, duration)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
}

func TestRedisFixedWindowRepairsCounterWithoutTTL(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	const duration = int64(29)
	key := redisIPRateLimitKey("MISSING-TTL", "192.0.2.51")
	redisServer.Set(key, "5")

	allowed, count, ttl, err := redisFixedWindowTake(context.Background(), key, 3, duration)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, int64(6), count)
	assert.Equal(t, duration, ttl)
	assert.Equal(t, time.Duration(duration)*time.Second, redisServer.TTL(key))

	redisServer.FastForward(time.Duration(duration) * time.Second)
	assert.False(t, redisServer.Exists(key), "a recovered counter must not remain permanently rate-limited")
}

func TestRedisFailurePolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, redisClient := useRateLimitMiniRedis(t)
	require.NoError(t, redisClient.Close())

	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/ip", rateLimitFactory(1, 30, "FAIL-IP"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET(
		"/user",
		func(c *gin.Context) { c.Set("id", 7) },
		userRateLimitFactory(1, 30, "FAIL-USER"),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET("/email", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	ipResponse := performRateLimitRequest(router, "/ip", "192.0.2.60:12345")
	assert.Equal(t, http.StatusInternalServerError, ipResponse.Code)
	assert.Empty(t, ipResponse.Body.String())
	userResponse := performRateLimitRequest(router, "/user", "192.0.2.61:12345")
	assert.Equal(t, http.StatusInternalServerError, userResponse.Code)
	assert.Empty(t, userResponse.Body.String())
	assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/email", "192.0.2.62:12345").Code)
}

func TestGlobalWebRateLimitCountsFrontendFilesInStaticBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousWebEnable, previousWebNum, previousWebDuration := common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration
	previousStaticEnable, previousStaticNum, previousStaticDuration := common.GlobalStaticRateLimitEnable, common.GlobalStaticRateLimitNum, common.GlobalStaticRateLimitDuration
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration = previousWebEnable, previousWebNum, previousWebDuration
		common.GlobalStaticRateLimitEnable, common.GlobalStaticRateLimitNum, common.GlobalStaticRateLimitDuration = previousStaticEnable, previousStaticNum, previousStaticDuration
	})
	common.GlobalWebRateLimitEnable, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration = true, 2, 41
	common.GlobalStaticRateLimitNum, common.GlobalStaticRateLimitDuration = 2, 43

	frontendDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(frontendDir, "static", "js"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(frontendDir, "static", "js", "app.js"), []byte("app"), 0o644))
	frontendFS := static.LocalFile(frontendDir, false)

	const clientIP = "192.0.2.70"
	const remoteAddr = clientIP + ":12345"

	t.Run("static limiter disabled by default", func(t *testing.T) {
		redisServer, _ := useRateLimitMiniRedis(t)
		common.GlobalStaticRateLimitEnable = false
		router := gin.New()
		require.NoError(t, router.SetTrustedProxies(nil))
		router.NoRoute(GlobalWebRateLimit(frontendFS), func(c *gin.Context) { c.Status(http.StatusNoContent) })

		for range 5 {
			assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/static/js/app.js", remoteAddr).Code)
		}
		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/channels", remoteAddr).Code)
		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/static/js/missing.js", remoteAddr).Code)
		pageResponse := performRateLimitRequest(router, "/channels", remoteAddr)
		assert.Equal(t, http.StatusTooManyRequests, pageResponse.Code)
		assert.Equal(t, "41", pageResponse.Header().Get("Retry-After"))

		webCount, err := redisServer.Get(redisIPRateLimitKey("GW", clientIP))
		require.NoError(t, err)
		assert.Equal(t, "3", webCount)
		assert.False(t, redisServer.Exists(redisIPRateLimitKey("GS", clientIP)))
	})

	t.Run("static limiter enabled uses its own bucket", func(t *testing.T) {
		redisServer, _ := useRateLimitMiniRedis(t)
		common.GlobalStaticRateLimitEnable = true
		router := gin.New()
		require.NoError(t, router.SetTrustedProxies(nil))
		router.NoRoute(GlobalWebRateLimit(frontendFS), func(c *gin.Context) { c.Status(http.StatusNoContent) })

		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/static/js/app.js", remoteAddr).Code)
		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/static/js/app.js", remoteAddr).Code)
		staticResponse := performRateLimitRequest(router, "/static/js/app.js", remoteAddr)
		assert.Equal(t, http.StatusTooManyRequests, staticResponse.Code)
		assert.Equal(t, "43", staticResponse.Header().Get("Retry-After"))
		assert.Equal(t, http.StatusNoContent, performRateLimitRequest(router, "/channels", remoteAddr).Code)

		staticCount, err := redisServer.Get(redisIPRateLimitKey("GS", clientIP))
		require.NoError(t, err)
		assert.Equal(t, "3", staticCount)
		webCount, err := redisServer.Get(redisIPRateLimitKey("GW", clientIP))
		require.NoError(t, err)
		assert.Equal(t, "1", webCount)
	})
}

var managementRateLimitTestSequence atomic.Int64

func managementRateLimitSession(t *testing.T, name string) (*model.User, service.AuthIdentity, string) {
	t.Helper()
	id := int(managementRateLimitTestSequence.Add(1)) + 1_000_000
	user := &model.User{Id: id, Username: name, Password: "placeholder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: name}
	require.NoError(t, model.DB.Create(user).Error)
	now := time.Now().Unix()
	session := &model.UserSession{SID: fmt.Sprintf("management-session-%d", id), UserID: id, Version: 1,
		UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "placeholder", ExpiresAt: now + 3600,
		CreatedAt: now, LastActiveAt: now, LoginMethod: "password"}
	require.NoError(t, model.CreateUserSession(session))
	identity := service.AuthIdentity{UserID: id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	token, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	return user, identity, token
}

func managementRateLimitRequest(router http.Handler, method, path, remoteAddr, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestManagementReadRateLimitsIsolateHistoryAndIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			setupDashboardAuthMiddlewareTest(t)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			}
			previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
			t.Cleanup(func() {
				common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousEnable, previousNum, previousDuration
			})
			common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 2, 180
			user, _, token := managementRateLimitSession(t, "management-primary")
			_, _, otherToken := managementRateLimitSession(t, "management-secondary")
			alternatePAT, _ := createMiddlewareScopedToken(t, user.Id, 0, "profile:read", "usage:read")
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(AccessTokenAudit(), GlobalAPIRateLimit())
			okHandler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
			for _, path := range []string{"/api/user/self", "/api/log/self", "/api/log/self/stat", "/api/data/self", "/api/data/flow/self"} {
				router.GET(path, UserAuth(), okHandler)
			}
			router.POST("/api/log/self/batch", UserAuth(), okHandler)
			router.POST("/api/client-activity/heartbeat", UserAuth(), ClientActivityRateLimit(), okHandler)
			router.POST("/api/client-activity/workload-snapshot", UserAuth(), ClientWorkloadRateLimit(), okHandler)
			router.GET("/api/other", okHandler)
			remoteAddr := fmt.Sprintf("[2001:db8:1:%x::1]:1234", user.Id-1_000_000)
			otherIP := fmt.Sprintf("[2001:db8:2:%x::1]:1234", user.Id-1_000_000)
			for range 2 {
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/other", remoteAddr, "").Code)
			}
			require.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/other", remoteAddr, "").Code)
			for range 360 {
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/log/self", remoteAddr, token).Code)
			}
			historyLimited := managementRateLimitRequest(router, http.MethodGet, "/api/log/self", remoteAddr, token)
			require.Equal(t, http.StatusTooManyRequests, historyLimited.Code)
			assert.JSONEq(t, `{"success":false,"code":"API_RATE_LIMITED","message":"Too many requests","message_key":"Too many requests"}`, historyLimited.Body.String())
			assert.Equal(t, "180", historyLimited.Header().Get("Retry-After"))
			assert.Equal(t, "no-store", historyLimited.Header().Get("Cache-Control"))
			assert.Contains(t, historyLimited.Header().Values("Vary"), "Authorization")
			assert.Empty(t, historyLimited.Header().Values("Set-Cookie"))
			assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/log/self", otherIP, token).Code)
			assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodPost, "/api/log/self/batch", otherIP, alternatePAT).Code)
			assert.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/log/self", remoteAddr, otherToken).Code)
			for range 120 {
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/user/self", remoteAddr, token).Code)
			}
			assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/user/self", otherIP, alternatePAT).Code)
			assert.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/user/self", remoteAddr, otherToken).Code)
			for range 30 {
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/log/self/stat", remoteAddr, token).Code)
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/data/self", remoteAddr, token).Code)
			}
			assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/log/self/stat", otherIP, token).Code)
			assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/data/flow/self", otherIP, token).Code)
			assert.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/data/self", remoteAddr, otherToken).Code)
			for _, path := range []string{"/api/client-activity/heartbeat", "/api/client-activity/workload-snapshot"} {
				for range 120 {
					require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodPost, path, remoteAddr, token).Code)
				}
				assert.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodPost, path, otherIP, token).Code)
				assert.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodPost, path, remoteAddr, otherToken).Code)
			}
		})
	}
}

func TestManagementReadRateLimitsKeepInvalidCredentialsUnderIPProtection(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			setupDashboardAuthMiddlewareTest(t)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			}
			previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
			t.Cleanup(func() {
				common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousEnable, previousNum, previousDuration
			})
			common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 2, 180
			user, identity, valid := managementRateLimitSession(t, "probe-valid")
			_, revokedIdentity, revoked := managementRateLimitSession(t, "probe-revoked")
			changed, err := model.RevokeUserSession(revokedIdentity.UserID, revokedIdentity.SessionID, "test")
			require.NoError(t, err)
			require.True(t, changed)
			disabledUser, _, disabled := managementRateLimitSession(t, "probe-disabled")
			require.NoError(t, model.DB.Model(disabledUser).Update("status", common.UserStatusDisabled).Error)
			wrongVersion := identity
			wrongVersion.UserAuthVersion++
			versionToken, _, err := service.IssueAccessToken(wrongVersion)
			require.NoError(t, err)
			wrongScope, _ := createMiddlewareScopedToken(t, user.Id, 0, "usage:read")
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(func(c *gin.Context) { c.Set("id", user.Id) }, AccessTokenAudit(), GlobalAPIRateLimit())
			router.GET("/api/user/self", UserAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			for i, test := range []struct {
				name, token string
				status      int
			}{
				{name: "absent credential", status: http.StatusUnauthorized},
				{name: "relay key", token: "sk-relay-key-is-not-dashboard-auth", status: http.StatusUnauthorized},
				{name: "forged JWT", token: tamperDashboardToken(valid), status: http.StatusUnauthorized},
				{name: "expired JWT", token: issueExpiredDashboardAccessToken(t, identity), status: http.StatusUnauthorized},
				{name: "revoked session", token: revoked, status: http.StatusUnauthorized},
				{name: "version changed", token: versionToken, status: http.StatusUnauthorized},
				{name: "disabled user", token: disabled, status: http.StatusUnauthorized},
				{name: "wrong PAT scope", token: wrongScope, status: http.StatusForbidden},
			} {
				t.Run(test.name, func(t *testing.T) {
					ip := fmt.Sprintf("[2001:db8:3:%x:%x::1]:1234", user.Id-1_000_000, i)
					for range 2 {
						response := managementRateLimitRequest(router, http.MethodGet, "/api/user/self?user_id=123", ip, test.token)
						require.Equal(t, test.status, response.Code, response.Body.String())
					}
					response := managementRateLimitRequest(router, http.MethodGet, "/api/user/self?user_id=124", ip, test.token)
					assert.Equal(t, http.StatusTooManyRequests, response.Code)
					assert.Equal(t, "180", response.Header().Get("Retry-After"))
					assert.Contains(t, response.Body.String(), "API_RATE_LIMITED")
					assert.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/user/self", ip, valid).Code, "failed probes cannot exhaust a valid account's separate budget")
				})
			}
		})
	}
}

func TestManagementStatusAndRefreshDoNotShareGeneralOrSecurityBudget(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
			t.Cleanup(func() {
				common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousEnable, previousNum, previousDuration
			})
			common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 1, 180
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			} else {
				previousRedis := common.RedisEnabled
				common.RedisEnabled = false
				t.Cleanup(func() { common.RedisEnabled = previousRedis })
			}
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(GlobalAPIRateLimit())
			ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
			router.GET("/api/other", ok)
			router.GET("/api/status", ok)
			router.POST("/api/user/auth/refresh", AuthRefreshRateLimit(), ok)
			ip := fmt.Sprintf("[2001:db8:4:%x::1]:1234", managementRateLimitTestSequence.Add(1))
			require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/other", ip, "").Code)
			require.Equal(t, http.StatusTooManyRequests, managementRateLimitRequest(router, http.MethodGet, "/api/other", ip, "").Code)
			for range 120 {
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodGet, "/api/status", ip, "invalid.authorization").Code)
				require.Equal(t, http.StatusNoContent, managementRateLimitRequest(router, http.MethodPost, "/api/user/auth/refresh", ip, "").Code)
			}
			statusLimited := managementRateLimitRequest(router, http.MethodGet, "/api/status", ip, "invalid.authorization")
			assert.Equal(t, http.StatusTooManyRequests, statusLimited.Code)
			assert.Contains(t, statusLimited.Body.String(), "API_RATE_LIMITED")
			refreshLimited := managementRateLimitRequest(router, http.MethodPost, "/api/user/auth/refresh", ip, "")
			assert.Equal(t, http.StatusTooManyRequests, refreshLimited.Code)
			assert.JSONEq(t, `{"success":false,"code":"AUTH_REFRESH_RATE_LIMITED","message":"Too many requests","message_key":"Too many requests"}`, refreshLimited.Body.String())
			assert.Equal(t, "60", refreshLimited.Header().Get("Retry-After"))
			assert.Equal(t, "no-store", refreshLimited.Header().Get("Cache-Control"))
			assert.Empty(t, refreshLimited.Header().Values("Set-Cookie"))
		})
	}
}

func TestRateLimitedRetryAfterNeverEncouragesImmediateRetry(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	writeRateLimited(c, 0)
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "1", response.Header().Get("Retry-After"))
	assert.Contains(t, response.Body.String(), "API_RATE_LIMITED")
}

func TestMemoryEmailVerificationRateLimitReturnsRetryHint(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	engine.GET("/verify", EmailVerificationRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for range 2 {
		require.Equal(t, http.StatusNoContent, performRateLimitRequest(engine, "/verify", "192.0.2.209:1234").Code)
	}
	response := performRateLimitRequest(engine, "/verify", "192.0.2.209:1234")
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "30", response.Header().Get("Retry-After"))
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	assert.JSONEq(t, `{"success":false,"code":"API_RATE_LIMITED","message":"Please wait before requesting another verification code.","message_key":"Please wait before requesting another verification code."}`, response.Body.String())
}
