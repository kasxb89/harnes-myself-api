package controller

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/load"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
	"gorm.io/gorm"
)

const serverLoadRefreshInterval = 10 * time.Second

// ServerLoadSnapshot describes only resources visible to this service. A
// container's system counters are not a promise of complete host visibility.
// Unknown counters stay null instead of looking like healthy zero values.
type ServerLoadSnapshot struct {
	SchemaVersion          int                  `json:"schema_version"`
	SampledAt              int64                `json:"sampled_at"`
	RefreshIntervalSeconds int                  `json:"refresh_interval_seconds"`
	Scope                  string               `json:"scope"`
	HostVisibility         string               `json:"host_visibility"`
	RuntimeOS              string               `json:"runtime_os"`
	CPU                    ServerCPULoad        `json:"cpu"`
	Memory                 ServerMemoryLoad     `json:"memory"`
	Disk                   ServerDiskLoad       `json:"disk"`
	Container              ServerContainerLoad  `json:"container"`
	Process                ServerProcessLoad    `json:"process"`
	Service                ServerServiceLoad    `json:"service"`
	Database               ServerDatabaseLoad   `json:"database"`
	Thresholds             ServerLoadThresholds `json:"thresholds"`
}

type ServerCPULoad struct {
	UsagePercent *float64 `json:"usage_percent"`
	LogicalCores int      `json:"logical_cores"`
	Load1        *float64 `json:"load_1"`
	Load5        *float64 `json:"load_5"`
	Load15       *float64 `json:"load_15"`
}

type ServerMemoryLoad struct {
	TotalBytes     *uint64  `json:"total_bytes"`
	AvailableBytes *uint64  `json:"available_bytes"`
	UsedPercent    *float64 `json:"used_percent"`
}

type ServerDiskLoad struct {
	Scope          string   `json:"scope"`
	TotalBytes     *uint64  `json:"total_bytes"`
	AvailableBytes *uint64  `json:"available_bytes"`
	UsedBytes      *uint64  `json:"used_bytes"`
	UsedPercent    *float64 `json:"used_percent"`
}

type ServerContainerLoad struct {
	Detected           bool     `json:"detected"`
	CgroupVersion      *string  `json:"cgroup_version"`
	MemoryCurrentBytes *uint64  `json:"memory_current_bytes"`
	MemoryLimitBytes   *uint64  `json:"memory_limit_bytes"`
	MemoryLimitStatus  string   `json:"memory_limit_status"`
	CPULimitCores      *float64 `json:"cpu_limit_cores"`
	CPULimitStatus     string   `json:"cpu_limit_status"`
}

type ServerProcessLoad struct {
	RSSBytes         *uint64 `json:"rss_bytes"`
	Goroutines       int     `json:"goroutines"`
	GoHeapAllocBytes uint64  `json:"go_heap_alloc_bytes"`
	GoHeapSysBytes   uint64  `json:"go_heap_sys_bytes"`
	GoSysBytes       uint64  `json:"go_sys_bytes"`
	GCCycles         uint32  `json:"gc_cycles"`
}

type ServerServiceLoad struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	StartedAt     int64  `json:"started_at"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type ServerDatabaseLoad struct {
	Main   ServerDatabasePool `json:"main"`
	Logs   ServerDatabasePool `json:"logs"`
	Shared bool               `json:"shared"`
}

type ServerDatabasePool struct {
	Status             string   `json:"status"`
	OpenConnections    *int     `json:"open_connections"`
	InUse              *int     `json:"in_use"`
	Idle               *int     `json:"idle"`
	MaxOpenConnections *int     `json:"max_open_connections"`
	WaitCount          *int64   `json:"wait_count"`
	WaitDurationMS     *float64 `json:"wait_duration_ms"`
}

type ServerLoadThresholds struct {
	GuardEnabled  bool `json:"guard_enabled"`
	CPUPercent    int  `json:"cpu_percent"`
	MemoryPercent int  `json:"memory_percent"`
	DiskPercent   int  `json:"disk_percent"`
}

type serverCPUCounter struct{ total, busy float64 }

type serverLoadSampler struct {
	mu       sync.Mutex
	at       time.Time
	latest   ServerLoadSnapshot
	previous *serverCPUCounter
	collect  func(time.Time, *serverCPUCounter) (ServerLoadSnapshot, *serverCPUCounter)
}

var currentServerLoad = serverLoadSampler{collect: collectServerLoad}

// GetServerLoad is a read-only administrator endpoint. The ten-second cache
// coalesces concurrent readers; CPU sampling uses two counters without sleeping.
func GetServerLoad(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, currentServerLoad.snapshot(time.Now()))
}

func (s *serverLoadSampler) snapshot(now time.Time) ServerLoadSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && now.Sub(s.at) < serverLoadRefreshInterval {
		return s.latest
	}
	previous := s.previous
	if s.at.IsZero() || now.Sub(s.at) > 3*serverLoadRefreshInterval {
		// After an idle dashboard, do not label a long historical average as
		// current CPU usage. The next normal polling interval establishes it.
		previous = nil
	}
	s.latest, s.previous = s.collect(now, previous)
	s.at = now
	return s.latest
}

func collectServerLoad(now time.Time, previous *serverCPUCounter) (ServerLoadSnapshot, *serverCPUCounter) {
	monitor := common.GetPerformanceMonitorConfig()
	result := ServerLoadSnapshot{
		SchemaVersion: 1, SampledAt: now.Unix(), RefreshIntervalSeconds: int(serverLoadRefreshInterval.Seconds()),
		Scope: "host", HostVisibility: "system", RuntimeOS: runtime.GOOS,
		CPU: ServerCPULoad{LogicalCores: runtime.NumCPU()}, Disk: ServerDiskLoad{Scope: "service_storage"},
		Container:  ServerContainerLoad{Detected: common.IsRunningInContainer(), MemoryLimitStatus: "unknown", CPULimitStatus: "unknown"},
		Service:    ServerServiceLoad{Status: "up", Version: common.Version, StartedAt: common.StartTime, UptimeSeconds: max(0, now.Unix()-common.StartTime)},
		Thresholds: ServerLoadThresholds{GuardEnabled: monitor.Enabled, CPUPercent: monitor.CPUThreshold, MemoryPercent: monitor.MemoryThreshold, DiskPercent: monitor.DiskThreshold},
	}
	if result.Container.Detected {
		result.Scope, result.HostVisibility = "container", "partial"
	}
	// Native counter reads and database probes share one bounded sampling
	// budget. No filesystem walks, process lists, commands or remote URL input.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var current *serverCPUCounter
	if values, err := cpu.TimesWithContext(ctx, false); err == nil && len(values) == 1 {
		value := values[0]
		total := value.User + value.System + value.Idle + value.Nice + value.Iowait + value.Irq + value.Softirq + value.Steal
		current = &serverCPUCounter{total: total, busy: total - value.Idle - value.Iowait}
		result.CPU.UsagePercent = serverCPUPercent(previous, current)
	}
	if values, err := load.AvgWithContext(ctx); err == nil {
		result.CPU.Load1, result.CPU.Load5, result.CPU.Load15 = finiteLoad(values.Load1), finiteLoad(values.Load5), finiteLoad(values.Load15)
	}
	if values, err := mem.VirtualMemoryWithContext(ctx); err == nil && values.Total > 0 && values.Available <= values.Total {
		percent := 100 * float64(values.Total-values.Available) / float64(values.Total)
		result.Memory = ServerMemoryLoad{TotalBytes: &values.Total, AvailableBytes: &values.Available, UsedPercent: &percent}
	}
	cachePath := common.GetDiskCachePath()
	if cachePath == "" {
		cachePath = os.TempDir()
	}
	if values, err := disk.UsageWithContext(ctx, cachePath); err == nil && values.Total > 0 {
		result.Disk = ServerDiskLoad{Scope: "service_storage", TotalBytes: &values.Total, AvailableBytes: &values.Free, UsedBytes: &values.Used, UsedPercent: finiteLoad(values.UsedPercent)}
	}
	if runtime.GOOS == "linux" {
		container := readServerCgroup(readServerMetricFile)
		container.Detected = result.Container.Detected
		result.Container = container
	}
	var goMemory runtime.MemStats
	runtime.ReadMemStats(&goMemory)
	result.Process = ServerProcessLoad{Goroutines: runtime.NumGoroutine(), GoHeapAllocBytes: goMemory.HeapAlloc, GoHeapSysBytes: goMemory.HeapSys, GoSysBytes: goMemory.Sys, GCCycles: goMemory.NumGC}
	if own, err := process.NewProcess(int32(os.Getpid())); err == nil {
		if info, err := own.MemoryInfoWithContext(ctx); err == nil {
			result.Process.RSSBytes = &info.RSS
		}
	}
	result.Database.Main = readServerDatabasePool(ctx, model.DB)
	result.Database.Shared = model.DB != nil && model.LOG_DB == model.DB
	if result.Database.Shared {
		result.Database.Logs = result.Database.Main
	} else {
		result.Database.Logs = readServerDatabasePool(ctx, model.LOG_DB)
	}
	return result, current
}

func serverCPUPercent(previous, current *serverCPUCounter) *float64 {
	if previous == nil || current == nil || current.total <= previous.total || current.busy < previous.busy {
		return nil
	}
	value := 100 * (current.busy - previous.busy) / (current.total - previous.total)
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return nil
	}
	return &value
}

func finiteLoad(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	return &value
}

func readServerDatabasePool(ctx context.Context, db *gorm.DB) ServerDatabasePool {
	result := ServerDatabasePool{Status: "unknown"}
	if db == nil {
		return result
	}
	pool, err := db.DB()
	if err != nil {
		return result
	}
	stats := pool.Stats()
	waitMS := float64(stats.WaitDuration) / float64(time.Millisecond)
	result = ServerDatabasePool{Status: "up", OpenConnections: &stats.OpenConnections, InUse: &stats.InUse, Idle: &stats.Idle, MaxOpenConnections: &stats.MaxOpenConnections, WaitCount: &stats.WaitCount, WaitDurationMS: &waitMS}
	if ctx.Err() != nil {
		// Another native probe may have exhausted the shared budget. The
		// database was not checked, so do not report an outage for it.
		result.Status = "unknown"
		return result
	}
	if err := pool.PingContext(ctx); err != nil {
		result.Status = "down"
	}
	// Do not return driver errors, which can include database addresses or DSNs.
	return result
}

func readServerMetricFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 16<<10))
}

func readServerCgroup(readFile func(string) ([]byte, error)) ServerContainerLoad {
	result := ServerContainerLoad{MemoryLimitStatus: "unknown", CPULimitStatus: "unknown"}
	contents, err := readFile("/proc/self/cgroup")
	if err != nil {
		return result
	}
	membership := map[string]string{}
	for line := range strings.SplitSeq(string(contents), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "/") {
			continue
		}
		for controller := range strings.SplitSeq(parts[1], ",") {
			membership[controller] = filepath.Clean(parts[2])
		}
	}
	readNumber := func(path string) *uint64 {
		value, err := readFile(path)
		if err != nil {
			return nil
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(string(value)), 10, 64)
		if err != nil {
			return nil
		}
		return &parsed
	}
	if relative, ok := membership[""]; ok {
		version := "2"
		result.CgroupVersion = &version
		root := "/sys/fs/cgroup"
		leaf := filepath.Join(root, strings.TrimPrefix(relative, "/"))
		result.MemoryCurrentBytes = readNumber(filepath.Join(leaf, "memory.current"))
		// An ancestor may impose the effective limit even when the leaf says
		// max. Bound the walk and never leave the cgroup filesystem root.
		for path, depth := leaf, 0; depth < 32; path, depth = filepath.Dir(path), depth+1 {
			if value, err := readFile(filepath.Join(path, "memory.max")); err == nil {
				if strings.TrimSpace(string(value)) == "max" {
					if result.MemoryLimitStatus == "unknown" {
						result.MemoryLimitStatus = "unlimited"
					}
				} else if number := readNumber(filepath.Join(path, "memory.max")); number != nil && *number > 0 {
					if result.MemoryLimitBytes == nil || *number < *result.MemoryLimitBytes {
						result.MemoryLimitBytes = number
					}
					result.MemoryLimitStatus = "limited"
				}
			}
			if value, err := readFile(filepath.Join(path, "cpu.max")); err == nil {
				parts := strings.Fields(string(value))
				if len(parts) == 2 && parts[0] == "max" {
					if result.CPULimitStatus == "unknown" {
						result.CPULimitStatus = "unlimited"
					}
				} else if len(parts) == 2 {
					quota, qerr := strconv.ParseUint(parts[0], 10, 64)
					period, perr := strconv.ParseUint(parts[1], 10, 64)
					if qerr == nil && perr == nil && quota > 0 && period > 0 {
						cores := float64(quota) / float64(period)
						if result.CPULimitCores == nil || cores < *result.CPULimitCores {
							result.CPULimitCores = &cores
						}
						result.CPULimitStatus = "limited"
					}
				}
			}
			if path == root {
				break
			}
		}
		return result
	}
	if relative, ok := membership["memory"]; ok {
		version := "1"
		result.CgroupVersion = &version
		root := "/sys/fs/cgroup/memory"
		leaf := filepath.Join(root, strings.TrimPrefix(relative, "/"))
		result.MemoryCurrentBytes = readNumber(filepath.Join(leaf, "memory.usage_in_bytes"))
		for path, depth := leaf, 0; depth < 32; path, depth = filepath.Dir(path), depth+1 {
			if number := readNumber(filepath.Join(path, "memory.limit_in_bytes")); number != nil && *number > 0 {
				// Linux v1 represents unlimited memory with an enormous sentinel.
				if *number >= 1<<60 {
					if result.MemoryLimitStatus == "unknown" {
						result.MemoryLimitStatus = "unlimited"
					}
				} else {
					if result.MemoryLimitBytes == nil || *number < *result.MemoryLimitBytes {
						result.MemoryLimitBytes = number
					}
					result.MemoryLimitStatus = "limited"
				}
			}
			if path == root {
				break
			}
		}
	}
	if relative, ok := membership["cpu"]; ok {
		version := "1"
		result.CgroupVersion = &version
		for _, controller := range []string{"cpu", "cpu,cpuacct"} {
			root := filepath.Join("/sys/fs/cgroup", controller)
			leaf := filepath.Join(root, strings.TrimPrefix(relative, "/"))
			for path, depth := leaf, 0; depth < 32; path, depth = filepath.Dir(path), depth+1 {
				if value, err := readFile(filepath.Join(path, "cpu.cfs_quota_us")); err == nil {
					quota, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
					if err == nil && quota == -1 {
						if result.CPULimitStatus == "unknown" {
							result.CPULimitStatus = "unlimited"
						}
					} else if period := readNumber(filepath.Join(path, "cpu.cfs_period_us")); err == nil && quota > 0 && period != nil && *period > 0 {
						cores := float64(quota) / float64(*period)
						if result.CPULimitCores == nil || cores < *result.CPULimitCores {
							result.CPULimitCores = &cores
						}
						result.CPULimitStatus = "limited"
					}
				}
				if path == root {
					break
				}
			}
		}
	}
	return result
}
