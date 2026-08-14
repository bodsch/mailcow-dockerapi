// Package stats sammelt Kennzahlen des Wirtssystems und der Container und
// legt sie im gemeinsamen Zwischenspeicher ab.
package stats

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// TimeLayout entspricht dem Format "%d.%m.%Y %H:%M:%S" aus DockerApi.py:532.
const TimeLayout = "02.01.2006 15:04:05"

// HostStats ist die Antwort von GET /host/stats.
//
// Die Feldnamen und ihre Reihenfolge entsprechen dem dict im Original.
type HostStats struct {
	CPU          CPU     `json:"cpu"`
	Memory       Memory  `json:"memory"`
	Uptime       float64 `json:"uptime"`
	SystemTime   string  `json:"system_time"`
	Architecture string  `json:"architecture"`
}

// CPU hält Kernzahl und momentane Auslastung.
type CPU struct {
	Cores int     `json:"cores"`
	Usage float64 `json:"usage"`
}

// Memory hält die Arbeitsspeicherwerte.
//
// Swap ist bewusst ein Feld variabler Länge: psutil.swap_memory() liefert ein
// benanntes Tupel, das json.dumps als Array kodiert. Ein Objekt an dieser
// Stelle würde die Auswertung im mailcow-Frontend brechen.
type Memory struct {
	Total uint64  `json:"total"`
	Usage float64 `json:"usage"`
	Swap  []any   `json:"swap"`
}

// HostProvider liefert die Rohwerte des Wirtssystems.
//
// Die Abstraktion erlaubt es, das Antwortformat ohne Zugriff auf echte
// Systemwerte zu prüfen.
type HostProvider interface {
	Collect(ctx context.Context) (HostStats, error)
}

// SystemHost liest die Werte über gopsutil aus.
type SystemHost struct {
	// Now erzeugt den Zeitstempel; nil bedeutet time.Now.
	Now func() time.Time
}

func (s SystemHost) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Collect stellt die Kennzahlen zusammen.
func (s SystemHost) Collect(ctx context.Context) (HostStats, error) {
	now := s.now()

	// psutil.cpu_count() zählt logische Kerne.
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return HostStats{}, err
	}

	// psutil.cpu_percent() ohne Intervall misst gegen den vorigen Aufruf und
	// liefert beim ersten Mal 0.0; gopsutil verhält sich mit interval=0 gleich.
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

	// platform.machine() meldet die Kernel-Architektur (x86_64, aarch64) –
	// nicht die Go-Zielarchitektur (amd64, arm64).
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

// swapTuple bringt die Auslagerungswerte in die Reihenfolge des benannten
// Tupels von psutil: total, used, free, percent, sin, sout.
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
