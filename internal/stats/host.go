// Package stats collects figures about the host and the containers and puts them
// in the shared cache.
package stats

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// TimeLayout matches the format "%d.%m.%Y %H:%M:%S" from DockerApi.py:532.
const TimeLayout = "02.01.2006 15:04:05"

// HostStats is the response of GET /host/stats.
//
// The field names and their order match the dict in the original.
type HostStats struct {
	CPU          CPU     `json:"cpu"`
	Memory       Memory  `json:"memory"`
	Uptime       float64 `json:"uptime"`
	SystemTime   string  `json:"system_time"`
	Architecture string  `json:"architecture"`
}

// CPU holds the core count and the current load.
type CPU struct {
	Cores int     `json:"cores"`
	Usage float64 `json:"usage"`
}

// Memory holds the memory figures.
//
// Swap is deliberately a variable-length field: psutil.swap_memory() returns a
// named tuple, which json.dumps encodes as an array. An object here would break
// the parsing in the mailcow frontend.
type Memory struct {
	Total uint64  `json:"total"`
	Usage float64 `json:"usage"`
	Swap  []any   `json:"swap"`
}

// HostProvider supplies the host's raw values.
//
// The abstraction is what makes the response format checkable without access to
// real system values.
type HostProvider interface {
	Collect(ctx context.Context) (HostStats, error)
}

// SystemHost reads the values through gopsutil.
type SystemHost struct {
	// Now produces the timestamp; nil means time.Now.
	Now func() time.Time
}

func (s SystemHost) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Collect assembles the figures.
func (s SystemHost) Collect(ctx context.Context) (HostStats, error) {
	now := s.now()

	// psutil.cpu_count() counts logical cores.
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return HostStats{}, err
	}

	// psutil.cpu_percent() without an interval measures against the previous call
	// and returns 0.0 the first time; gopsutil behaves the same with interval=0.
	usage, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return HostStats{}, err
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return HostStats{}, err
	}

	swap, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return HostStats{}, err
	}

	bootTime, err := host.BootTimeWithContext(ctx)
	if err != nil {
		return HostStats{}, err
	}

	// platform.machine() reports the kernel architecture (x86_64, aarch64), not
	// the Go target architecture (amd64, arm64).
	arch, err := host.KernelArch()
	if err != nil {
		return HostStats{}, err
	}

	return HostStats{
		CPU:    CPU{Cores: cores, Usage: firstOrZero(usage)},
		Memory: Memory{Total: vm.Total, Usage: vm.UsedPercent, Swap: swapTuple(swap)},
		// time.time() - psutil.boot_time()
		Uptime:       float64(now.UnixNano())/float64(time.Second) - float64(bootTime),
		SystemTime:   now.Format(TimeLayout),
		Architecture: arch,
	}, nil
}

// swapTuple puts the swap values in the order of psutil's named tuple: total, used,
// free, percent, sin, sout.
func swapTuple(s *mem.SwapMemoryStat) []any {
	if s == nil {
		return []any{}
	}

	return []any{s.Total, s.Used, s.Free, s.UsedPercent, s.Sin, s.Sout}
}

func firstOrZero(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}
