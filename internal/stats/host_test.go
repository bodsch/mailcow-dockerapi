package stats

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

// SystemHost liest echte Systemwerte; geprüft wird die Plausibilität und vor
// allem das Format, auf das die mailcow-Oberfläche baut.
func TestSystemHostCollect(t *testing.T) {
	fixed := time.Date(2026, 8, 14, 9, 5, 3, 0, time.UTC)
	h := SystemHost{Now: func() time.Time { return fixed }}

	got, err := h.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.CPU.Cores < 1 {
		t.Errorf("Cores = %d, want mindestens 1", got.CPU.Cores)
	}
	if got.CPU.Usage < 0 || got.CPU.Usage > 100*float64(got.CPU.Cores) {
		t.Errorf("Usage = %v, unplausibel", got.CPU.Usage)
	}
	if got.Memory.Total == 0 {
		t.Error("Memory.Total = 0")
	}
	if got.Memory.Usage < 0 || got.Memory.Usage > 100 {
		t.Errorf("Memory.Usage = %v, want 0..100", got.Memory.Usage)
	}
	if got.Uptime <= 0 {
		t.Errorf("Uptime = %v, want positiv", got.Uptime)
	}

	if got.SystemTime != "14.08.2026 09:05:03" {
		t.Errorf("SystemTime = %q, want %q", got.SystemTime, "14.08.2026 09:05:03")
	}

	// psutil.swap_memory() ist ein benanntes Tupel mit sechs Feldern.
	if len(got.Memory.Swap) != 6 {
		t.Errorf("Swap hat %d Felder, want 6: %v", len(got.Memory.Swap), got.Memory.Swap)
	}
}

// platform.machine() in Python liefert dasselbe wie uname -m. Das ist das
// verlässlichste Orakel: auf Linux/arm64 lautet der Wert aarch64, auf
// Darwin/arm64 dagegen arm64 – eine feste Erwartung wäre auf einer der beiden
// Plattformen falsch.
func TestSystemHostArchitectureMatchesUname(t *testing.T) {
	uname, err := exec.LookPath("uname")
	if err != nil {
		t.Skip("uname nicht verfuegbar")
	}

	out, err := exec.Command(uname, "-m").Output()
	if err != nil {
		t.Skipf("uname -m fehlgeschlagen: %v", err)
	}
	want := strings.TrimSpace(string(out))

	got, err := SystemHost{}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.Architecture != want {
		t.Errorf("Architecture = %q, want %q (uname -m)", got.Architecture, want)
	}
}

// Auf Linux – dort läuft der Dienst – darf niemals GOARCH herauskommen.
func TestSystemHostDoesNotReportGOARCHOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("nur auf Linux aussagekraeftig (hier: %s)", runtime.GOOS)
	}

	got, err := SystemHost{}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.Architecture == runtime.GOARCH {
		t.Errorf("Architecture = %q entspricht GOARCH – erwartet wird die Kernel-Schreibweise",
			got.Architecture)
	}
}

// Die kodierte Form muss der von json.dumps entsprechen: swap als Array,
// die Felder in der Reihenfolge des Originals.
func TestSystemHostEncodesLikePython(t *testing.T) {
	h := SystemHost{Now: func() time.Time { return time.Date(2026, 8, 14, 9, 5, 3, 0, time.UTC) }}

	got, err := h.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Feldreihenfolge wie im dict aus DockerApi.py:521.
	order := regexp.MustCompile(
		`^\{"cpu":\{"cores":\d+,"usage":[-\d.e+]+\},` +
			`"memory":\{"total":\d+,"usage":[-\d.e+]+,"swap":\[[^\]]*\]\},` +
			`"uptime":[-\d.e+]+,"system_time":"[^"]+","architecture":"[^"]+"\}$`,
	)

	if !order.Match(raw) {
		t.Errorf("Kodierung weicht ab:\n%s", raw)
	}
}

func TestSwapTupleOrderAndLength(t *testing.T) {
	// total, used, free, percent, sin, sout
	got := swapTuple(&swapFixture)

	want := []any{uint64(2048), uint64(1024), uint64(1024), 50.0, uint64(7), uint64(9)}
	if len(got) != len(want) {
		t.Fatalf("Laenge = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFirstOrZero(t *testing.T) {
	if got := firstOrZero(nil); got != 0 {
		t.Errorf("firstOrZero(nil) = %v, want 0", got)
	}
	if got := firstOrZero([]float64{12.5, 99}); got != 12.5 {
		t.Errorf("firstOrZero = %v, want 12.5", got)
	}
}

// swapFixture liefert feste Auslagerungswerte für die Reihenfolgeprüfung.
var swapFixture = mem.SwapMemoryStat{
	Total:       2048,
	Used:        1024,
	Free:        1024,
	UsedPercent: 50.0,
	Sin:         7,
	Sout:        9,
}
