package middleware

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestModelRedisRateLimitReservesConcurrentAdmissionAndRetainsUnknown(t *testing.T) {
	require.NoError(t, i18n.Init())
	server, _ := useRateLimitMiniRedis(t)
	userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	router := gin.New()
	router.GET("/:outcome", func(c *gin.Context) { c.Set("id", userID) }, redisRateLimitHandler(60, 0, 1), func(c *gin.Context) {
		status := relaycommon.NewStreamStatus()
		switch c.Param("outcome") {
		case "pending":
			close(entered)
			<-release
			status.MarkFailed("server_error", "", 0)
		case "unknown":
			status.RequireTerminal()
			status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		default:
			status.MarkCompleted()
		}
		common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
		c.Status(http.StatusOK)
	})
	go func() { defer close(finished); performRateLimitRequest(router, "/pending", "127.0.0.1:1000") }()
	<-entered
	blocked := performRateLimitRequest(router, "/completed", "127.0.0.1:1000")
	close(release)
	<-finished
	require.Equal(t, http.StatusTooManyRequests, blocked.Code, "in-flight reservation consumes the only admission")
	require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/unknown", "127.0.0.1:1000").Code)
	require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code, "missing protocol terminal must not release the slot")
	server.FastForward(61 * time.Second)
	require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
}

func TestModelRedisReservationIdempotencyFinalizationFailureAndLease(t *testing.T) {
	server, client := useRateLimitMiniRedis(t)
	ctx := context.Background()
	r, allowed, err := reserveModelSuccess(ctx, client, "synthetic-finalization", 1, 60)
	require.NoError(t, err)
	require.True(t, allowed)
	again, err := modelSuccessReserveScript.Run(ctx, client, r.keys, r.id, 1, r.duration.Milliseconds()).Int()
	require.NoError(t, err)
	require.Equal(t, 1, again)
	require.Equal(t, int64(1), client.ZCard(ctx, r.keys[0]).Val(), "lost Redis reply retries reserve the same ID once")
	require.NoError(t, client.Close())
	r.finish(modelSuccessCompleted)
	require.True(t, server.Exists(r.keys[0]), "failed finalization cannot drop an unresolved accepted request")
	require.Greater(t, server.TTL(r.keys[0]), time.Duration(0))
	server.FastForward(61 * time.Second)
	require.False(t, server.Exists(r.keys[0]))
}

func TestModelRedisUnknownTaskFailureDoesNotFreeAdmission(t *testing.T) {
	useRateLimitMiniRedis(t)
	require.NoError(t, i18n.Init())
	router := gin.New()
	userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
	router.GET("/", func(c *gin.Context) { c.Set("id", userID) }, redisRateLimitHandler(60, 0, 1), func(c *gin.Context) {
		c.Set("media_durable_submission", &service.MediaSubmission{Unknown: true, MayHaveSent: true})
		c.Status(http.StatusServiceUnavailable)
	})
	require.Equal(t, http.StatusServiceUnavailable, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
	require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
}

func TestPreparedRedisModelAdmissionQualification(t *testing.T) {
	addr := os.Getenv("REDIS_PREPARATION_ADDR")
	if addr == "" {
		t.Skip("opt-in, owned loopback Redis only")
	}
	host, _, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", host)
	require.NotEmpty(t, os.Getenv("REDIS_PREPARATION_PASSWORD"))
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PREPARATION_PASSWORD"), DB: 14, MaxRetries: -1})
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err())
	totalKey := "synthetic-total-" + common.GetRandomString(12)
	total := limiter.New(ctx, client)
	first, err := total.Allow(ctx, totalKey, limiter.WithCapacity(60), limiter.WithRate(1), limiter.WithRequested(60))
	require.NoError(t, err)
	require.True(t, first)
	require.NoError(t, client.ScriptFlush(ctx).Err())
	second, err := total.Allow(ctx, totalKey, limiter.WithCapacity(60), limiter.WithRate(1), limiter.WithRequested(60))
	require.NoError(t, err)
	require.False(t, second, "total budget survives Lua reload")
	t.Cleanup(func() { client.Del(context.Background(), totalKey) })
	user := "qualification-" + common.GetRandomString(12)
	var wg sync.WaitGroup
	var mu sync.Mutex
	reservations := []*modelSuccessReservation{}
	errs := []error{}
	for range 30 {
		wg.Go(func() {
			r, allowed, err := reserveModelSuccess(ctx, client, user, 3, 60)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if allowed {
				reservations = append(reservations, r)
			}
		})
	}
	wg.Wait()
	require.Empty(t, errs)
	require.Len(t, reservations, 3, "Lua must atomically bound parallel admissions")
	reservations[0].finish(modelSuccessFailed)
	reservations[1].finish(modelSuccessCompleted)
	reservations[2].finish(modelSuccessUnknown)
	next, allowed, err := reserveModelSuccess(ctx, client, user, 3, 60)
	require.NoError(t, err)
	require.True(t, allowed)
	_, allowed, err = reserveModelSuccess(ctx, client, user, 3, 60)
	require.NoError(t, err)
	require.False(t, allowed)
	next.finish(modelSuccessFailed)
	require.NoError(t, client.ScriptFlush(ctx).Err())
	afterFlush, allowed, err := reserveModelSuccess(ctx, client, user, 3, 60)
	require.NoError(t, err)
	require.True(t, allowed, "Lua cache loss reloads code without replaying a relay")
	afterFlush.finish(modelSuccessFailed)
	// A live response holds its slot beyond one window; completion joins the heartbeat.
	slow, allowed, err := reserveModelSuccess(ctx, client, user+"-slow", 1, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	defer slow.finish(modelSuccessFailed)
	time.Sleep(1200 * time.Millisecond)
	_, allowed, err = reserveModelSuccess(ctx, client, user+"-slow", 1, 1)
	require.NoError(t, err)
	require.False(t, allowed)
	t.Logf("owned Redis %s: 30 contenders/3 admissions, release/completed/unknown isolation, SCRIPT FLUSH recovery, slow-request lease passed", addr)
	require.NoError(t, client.Del(ctx, reservations[0].keys...).Err())
	require.NoError(t, client.Del(ctx, fmt.Sprintf("rateLimit:model-success:v2:completed:%s", user)).Err())
}

func TestModelRedisRejectedBeforeRelayFreesOnlyOwnReservation(t *testing.T) {
	server, client := useRateLimitMiniRedis(t)
	require.NoError(t, i18n.Init())
	userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
	// Force a definite total-limit infrastructure rejection before the relay.
	require.NoError(t, client.Set(context.Background(), fmt.Sprintf("rateLimit:%d", userID), "wrong-total-type", 0).Err())
	calls := 0
	router := gin.New()
	router.GET("/", func(c *gin.Context) { c.Set("id", userID) }, redisRateLimitHandler(60, 1, 1), func(c *gin.Context) { calls++; c.Status(http.StatusOK) })
	require.Equal(t, http.StatusInternalServerError, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
	require.Zero(t, calls)
	require.False(t, server.Exists(fmt.Sprintf("rateLimit:model-success:v2:pending:%d", userID)))
	require.NoError(t, client.Del(context.Background(), fmt.Sprintf("rateLimit:%d", userID)).Err())
	require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
	require.Equal(t, 1, calls)
}
