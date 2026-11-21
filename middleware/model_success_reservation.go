package middleware

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

type modelSuccessState int

const (
	modelSuccessUnknown modelSuccessState = iota
	modelSuccessFailed
	modelSuccessCompleted
)

// The old list remains untouched and conservatively counts until its original
// TTL elapses. A fresh namespace prevents LIST/ZSET type conflicts at upgrade.
// Each ID is idempotent even if a Redis response is lost and the client retries.
var modelSuccessReserveScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local duration = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
if redis.call('ZSCORE', KEYS[1], ARGV[1]) then return 1 end
local legacy = 0
local kind = redis.call('TYPE', KEYS[3]).ok
if kind == 'list' then
 legacy = redis.call('LLEN', KEYS[3])
 if legacy > 0 and redis.call('PTTL', KEYS[3]) < 0 then redis.call('PEXPIRE', KEYS[3], duration) end
elseif kind ~= 'none' then return redis.error_reply('invalid legacy model limit type') end
if redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2]) + legacy >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], now + duration, ARGV[1])
redis.call('PEXPIRE', KEYS[1], duration)
return 1`)

var modelSuccessFinishScript = redis.NewScript(`
if ARGV[2] == 'release' then return redis.call('ZREM', KEYS[1], ARGV[1]) end
if not redis.call('ZSCORE', KEYS[1], ARGV[1]) and not redis.call('ZSCORE', KEYS[2], ARGV[1]) then return 0 end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZADD', KEYS[2], now + tonumber(ARGV[3]), ARGV[1])
redis.call('PEXPIRE', KEYS[2], ARGV[3])
return 1`)

var modelSuccessRenewScript = redis.NewScript(`
if not redis.call('ZSCORE', KEYS[1], ARGV[1]) then return 0 end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1`)

type modelSuccessReservation struct {
	client   *redis.Client
	keys     []string
	id       string
	duration time.Duration
	stop     context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

func reserveModelSuccess(ctx context.Context, client *redis.Client, user string, maxCount int, durationSeconds int64) (*modelSuccessReservation, bool, error) {
	if maxCount <= 0 {
		return nil, true, nil
	}
	if client == nil || durationSeconds <= 0 || durationSeconds > int64((time.Duration(1<<63-1))/time.Second) {
		return nil, false, errors.New("invalid Redis model limit configuration")
	}
	duration := time.Duration(durationSeconds) * time.Second
	r := &modelSuccessReservation{client: client, keys: []string{"rateLimit:model-success:v2:pending:" + user, "rateLimit:model-success:v2:completed:" + user, "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + user}, id: common.GetRandomString(24), duration: duration}
	allowed, err := modelSuccessReserveScript.Run(ctx, client, r.keys, r.id, maxCount, duration.Milliseconds()).Int()
	if err != nil || allowed != 1 {
		return nil, false, err
	}
	active, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	r.done = make(chan struct{})
	// Hold a slow live response's admission beyond a single counting window.
	// On process loss or uncertain completion the bounded lease expires; it is
	// never a durable billing authorization and never triggers a relay retry.
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(min(duration/3, 10*time.Second))
		defer ticker.Stop()
		for {
			select {
			case <-active.Done():
				return
			case <-ticker.C:
				attempt, stop := context.WithTimeout(active, 2*time.Second)
				renewed, err := modelSuccessRenewScript.Run(attempt, r.client, r.keys[:1], r.id, r.duration.Milliseconds()).Int()
				stop()
				if err != nil || renewed != 1 {
					return
				}
			}
		}
	}()
	return r, true, nil
}

// A failed finalization leaves the original lease in place until TTL. It must
// not rewrite an already-sent response, clear authentication, or replay work.
func (r *modelSuccessReservation) finish(outcome modelSuccessState) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.stop()
		<-r.done
		if outcome == modelSuccessUnknown {
			return
		}
		operation := "complete"
		if outcome == modelSuccessFailed {
			operation = "release"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := modelSuccessFinishScript.Run(ctx, r.client, r.keys[:2], r.id, operation, r.duration.Milliseconds()).Result(); err != nil {
			common.SysLog(common.LogText("model request limit finalization failed; keeping bounded reservation"))
		}
	})
}

func modelSuccessOutcome(c *gin.Context) modelSuccessState {
	if submission := service.MediaSubmissionFromContext(c); submission != nil && submission.Unknown {
		return modelSuccessUnknown
	}
	if submission := service.H3SubmissionFromContext(c); submission != nil && submission.Unknown {
		return modelSuccessUnknown
	}
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	if status != nil {
		snapshot := status.OutcomeSnapshot()
		if snapshot.Response == relaycommon.ResponseOutcomeFailed {
			return modelSuccessFailed
		}
		if snapshot.Response == relaycommon.ResponseOutcomeUnknown && (snapshot.ExpectsTerminal || snapshot.HasErrors || !status.IsNormalEnd() && snapshot.EndReason != relaycommon.StreamEndReasonNone) {
			return modelSuccessUnknown
		}
	}
	if c.Writer.Status() >= 400 {
		return modelSuccessFailed
	}
	return modelSuccessCompleted
}
