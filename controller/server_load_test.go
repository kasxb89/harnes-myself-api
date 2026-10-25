package controller

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestServerLoadCacheAndCPUContract(t *testing.T) {
	for _, sample := range []struct {
		name              string
		previous, current *serverCPUCounter
		want              *float64
	}{
		{name: "first sample stays unknown", current: &serverCPUCounter{total: 100, busy: 30}},
		{name: "nonblocking delta", previous: &serverCPUCounter{total: 100, busy: 30}, current: &serverCPUCounter{total: 120, busy: 40}, want: new(float64(50))},
		{name: "counter reset", previous: &serverCPUCounter{total: 100, busy: 30}, current: &serverCPUCounter{total: 20, busy: 10}},
		{name: "no elapsed CPU time", previous: &serverCPUCounter{total: 100, busy: 30}, current: &serverCPUCounter{total: 100, busy: 30}},
		{name: "invalid busy count", previous: &serverCPUCounter{total: 100, busy: 30}, current: &serverCPUCounter{total: 110, busy: 60}},
		{name: "invalid nonfinite counter", previous: &serverCPUCounter{total: 100, busy: 30}, current: &serverCPUCounter{total: math.NaN(), busy: 60}},
	} {
		t.Run(sample.name, func(t *testing.T) { assert.Equal(t, sample.want, serverCPUPercent(sample.previous, sample.current)) })
	}
	var calls int
	sampler := serverLoadSampler{collect: func(now time.Time, previous *serverCPUCounter) (ServerLoadSnapshot, *serverCPUCounter) {
		calls++
		return ServerLoadSnapshot{SampledAt: now.Unix(), CPU: ServerCPULoad{UsagePercent: serverCPUPercent(previous, &serverCPUCounter{total: float64(calls * 10), busy: float64(calls * 5)})}}, &serverCPUCounter{total: float64(calls * 10), busy: float64(calls * 5)}
	}}
	now := time.Unix(1000, 0)
	first := sampler.snapshot(now)
	assert.Nil(t, first.CPU.UsagePercent)
	assert.Equal(t, first, sampler.snapshot(now.Add(9*time.Second)))
	assert.Equal(t, 1, calls, "page polling cannot trigger a new native probe inside the shared cache interval")
	next := sampler.snapshot(now.Add(10 * time.Second))
	require.NotNil(t, next.CPU.UsagePercent)
	assert.Equal(t, float64(50), *next.CPU.UsagePercent)
	assert.EqualValues(t, 1010, next.SampledAt)
	assert.Equal(t, 2, calls)
	assert.Nil(t, sampler.snapshot(now.Add(50*time.Second)).CPU.UsagePercent, "an inactive dashboard cannot present a stale long-term CPU average as current load")
}

func TestServerCgroupLimitsAndUnknown(t *testing.T) {
	for _, sample := range []struct {
		name                       string
		files                      map[string]string
		version, memory, cpu       string
		memoryLimit, memoryCurrent *uint64
		cpuLimit                   *float64
	}{
		{name: "unavailable is unknown", version: "", memory: "unknown", cpu: "unknown"},
		{name: "v2 ancestor applies even if leaf is unlimited", files: map[string]string{
			"/proc/self/cgroup":                            "0::/tenant/service\n",
			"/sys/fs/cgroup/tenant/service/memory.current": "123456",
			"/sys/fs/cgroup/tenant/service/memory.max":     "max",
			"/sys/fs/cgroup/tenant/service/cpu.max":        "max 100000",
			"/sys/fs/cgroup/tenant/memory.max":             "268435456",
			"/sys/fs/cgroup/tenant/cpu.max":                "150000 100000",
			"/sys/fs/cgroup/memory.max":                    "max",
			"/sys/fs/cgroup/cpu.max":                       "max 100000",
		}, version: "2", memory: "limited", cpu: "limited", memoryLimit: new(uint64(268435456)), memoryCurrent: new(uint64(123456)), cpuLimit: new(float64(1.5))},
		{name: "v2 visible unlimited is distinguished from missing", files: map[string]string{
			"/proc/self/cgroup": "0::/\n", "/sys/fs/cgroup/memory.max": "max", "/sys/fs/cgroup/cpu.max": "max 100000",
		}, version: "2", memory: "unlimited", cpu: "unlimited"},
		{name: "v1 account limits", files: map[string]string{
			"/proc/self/cgroup": "4:memory:/docker/test\n3:cpu,cpuacct:/docker/test\n",
			"/sys/fs/cgroup/memory/docker/test/memory.usage_in_bytes":  "1024",
			"/sys/fs/cgroup/memory/docker/test/memory.limit_in_bytes":  "268435456",
			"/sys/fs/cgroup/cpu,cpuacct/docker/test/cpu.cfs_quota_us":  "200000",
			"/sys/fs/cgroup/cpu,cpuacct/docker/test/cpu.cfs_period_us": "100000",
		}, version: "1", memory: "limited", cpu: "limited", memoryLimit: new(uint64(268435456)), memoryCurrent: new(uint64(1024)), cpuLimit: new(float64(2))},
		{name: "v1 ancestor limit applies to unlimited leaf", files: map[string]string{
			"/proc/self/cgroup": "4:memory:/docker/test\n3:cpu,cpuacct:/docker/test\n",
			"/sys/fs/cgroup/memory/docker/test/memory.limit_in_bytes": "9223372036854771712",
			"/sys/fs/cgroup/memory/docker/memory.limit_in_bytes":      "536870912",
			"/sys/fs/cgroup/cpu/docker/test/cpu.cfs_quota_us":         "-1",
			"/sys/fs/cgroup/cpu/docker/cpu.cfs_quota_us":              "100000",
			"/sys/fs/cgroup/cpu/docker/cpu.cfs_period_us":             "100000",
		}, version: "1", memory: "limited", cpu: "limited", memoryLimit: new(uint64(536870912)), cpuLimit: new(float64(1))},
		{name: "invalid values cannot become zero", files: map[string]string{
			"/proc/self/cgroup": "0::/\n", "/sys/fs/cgroup/memory.max": "-1", "/sys/fs/cgroup/cpu.max": "100 0", "/sys/fs/cgroup/memory.current": "invalid",
		}, version: "2", memory: "unknown", cpu: "unknown"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := readServerCgroup(func(path string) ([]byte, error) {
				assert.True(t, path == "/proc/self/cgroup" || strings.HasPrefix(path, "/sys/fs/cgroup/"), path)
				value, ok := sample.files[path]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(value), nil
			})
			if sample.version == "" {
				assert.Nil(t, result.CgroupVersion)
			} else {
				require.NotNil(t, result.CgroupVersion)
				assert.Equal(t, sample.version, *result.CgroupVersion)
			}
			assert.Equal(t, sample.memory, result.MemoryLimitStatus)
			assert.Equal(t, sample.cpu, result.CPULimitStatus)
			assert.Equal(t, sample.memoryLimit, result.MemoryLimitBytes)
			assert.Equal(t, sample.memoryCurrent, result.MemoryCurrentBytes)
			assert.Equal(t, sample.cpuLimit, result.CPULimitCores)
		})
	}
}

func TestServerLoadReadBoundsAndDatabaseHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded-native-counter")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 32<<10)), 0600))
	contents, err := readServerMetricFile(path)
	require.NoError(t, err)
	assert.Len(t, contents, 16<<10)
	_, err = readServerMetricFile(path + ".missing")
	assert.ErrorIs(t, err, os.ErrNotExist)
	unknown := readServerDatabasePool(context.Background(), nil)
	assert.Equal(t, "unknown", unknown.Status)
	assert.Nil(t, unknown.OpenConnections)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "PRIVATE_DSN.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	up := readServerDatabasePool(context.Background(), db)
	assert.Equal(t, "up", up.Status)
	require.NotNil(t, up.OpenConnections)
	assert.GreaterOrEqual(t, *up.OpenConnections, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, "unknown", readServerDatabasePool(ctx, db).Status, "an exhausted sampling budget is not proof that the database is down")
	require.NoError(t, pool.Close())
	down := readServerDatabasePool(context.Background(), db)
	assert.Equal(t, "down", down.Status)
	encoded, err := common.Marshal(down)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "PRIVATE_DSN")
	assert.NotContains(t, string(encoded), "closed")

	cgroup := readServerCgroup(func(string) ([]byte, error) { return nil, errors.New("PRIVATE_HOST_PATH") })
	encoded, err = common.Marshal(cgroup)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"memory_current_bytes":null`)
	assert.NotContains(t, string(encoded), "PRIVATE_HOST_PATH")
}
