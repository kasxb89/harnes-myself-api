package limiter

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRedisLimiterUsesSuppliedClientAndReloadsScripts(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"first-owned-client", "second-owned-client"} {
		t.Run(name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer client.Close()
			limiter := New(ctx, client)
			allowed, err := limiter.Allow(ctx, "synthetic-total", WithCapacity(60), WithRate(1), WithRequested(60))
			require.NoError(t, err)
			require.True(t, allowed)
			require.NoError(t, client.ScriptFlush(ctx).Err())
			allowed, err = limiter.Allow(ctx, "synthetic-total", WithCapacity(60), WithRate(1), WithRequested(60))
			require.NoError(t, err)
			require.False(t, allowed, "script reload must retain the original budget")
		})
	}
}
