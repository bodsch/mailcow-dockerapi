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

// SystemHost reads real system values; what is checked is their plausibility and,
// above all, the format the mailcow UI builds on.
//
// The clock is pinned because SystemTime's rendering is an exact expectation. That
// makes every value derived from both the clock and the system unusable here, which
// is why Uptime has a test of its own.
func TestSystemHostCollect(t *testing.T) {
	fixed := time.Date(2026, 8, 14, 9, 5, 3, 0, time.UTC)
	h := SystemHost{Now: func() time.Time { return fixed }}

	got, err := h.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.CPU.Cores < 1 {
		t.Errorf("Cores = %d, want at least 1", got.CPU.Cores)
	}
	if got.CPU.Usage < 0 || got.CPU.Usage > 100*float64(got.CPU.Cores) {
		t.Errorf("Usage = %v, implausible", got.CPU.Usage)
	}
	if got.Memory.Total == 0 {
		t.Error("Memory.Total = 0")
	}
	if got.Memory.Usage < 0 || got.Memory.Usage > 100 {
		t.Errorf("Memory.Usage = %v, want 0..100", got.Memory.Usage)
	}

	// Uptime is deliberately not checked here — see TestSystemHostUptime. It is
	// now minus the boot time, and "now" is pinned in this test.

	if got.SystemTime != "14.08.2026 09:05:03" {
		t.Errorf("SystemTime = %q, want %q", got.SystemTime, "14.08.2026 09:05:03")
	}

	// psutil.swap_memory() is a named tuple with six fields.
	if len(got.Memory.Swap) != 6 {
		t.Errorf("Swap has %d fields, want 6: %v", len(got.Memory.Swap), got.Memory.Swap)
	}
}

// Uptime is time.time() - psutil.boot_time(), so it can only be judged against the
// clock the boot time came from.
//
// This check used to sit in TestSystemHostCollect, against its pinned date. That
// held only until a machine booted later than the pinned day: from then on the
// difference was negative and the test failed by calendar rather than by code —
// everywhere, CI included, since a runner always boots today.
func TestSystemHostUptime(t *testing.T) {
	got, err := SystemHost{}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.Uptime <= 0 {
		t.Errorf("Uptime = %v, want a positive value", got.Uptime)
	}

	// A decade of uptime means the boot time was misread rather than that the host
	// is old, which is the failure this bound is here to catch.
	if decade := (10 * 365 * 24 * time.Hour).Seconds(); got.Uptime > decade {
		t.Errorf("Uptime = %v, implausible — more than %v seconds", got.Uptime, decade)
	}
}

// platform.machine() in Python returns the same thing as uname -m. That is the most
// reliable oracle: on Linux/arm64 the value is aarch64, on Darwin/arm64 it is arm64 —
// a fixed expectation would be wrong on one of the two platforms.
func TestSystemHostArchitectureMatchesUname(t *testing.T) {
	uname, err := exec.LookPath("uname")
	if err != nil {
		t.Skip("uname is not available")
	}

	out, err := exec.Command(uname, "-m").Output()
	if err != nil {
		t.Skipf("uname -m failed: %v", err)
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

// On Linux — where the service runs — the answer must never be GOARCH.
func TestSystemHostDoesNotReportGOARCHOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("only meaningful on Linux (here: %s)", runtime.GOOS)
	}

	got, err := SystemHost{}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got.Architecture == runtime.GOARCH {
		t.Errorf("Architecture = %q equals GOARCH — the kernel spelling is expected",
			got.Architecture)
	}
}

// The encoded form has to match json.dumps: swap as an array, the fields in the
// order of the original.
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

	// Field order as in the dict from DockerApi.py:521.
	order := regexp.MustCompile(
		`^\{"cpu":\{"cores":\d+,"usage":[-\d.e+]+\},` +
			`"memory":\{"total":\d+,"usage":[-\d.e+]+,"swap":\[[^\]]*\]\},` +
			`"uptime":[-\d.e+]+,"system_time":"[^"]+","architecture":"[^"]+"\}$`,
	)

	if !order.Match(raw) {
		t.Errorf("the encoding differs:\n%s", raw)
	}
}

func TestSwapTupleOrderAndLength(t *testing.T) {
	// total, used, free, percent, sin, sout
	got := swapTuple(&swapFixture)

	want := []any{uint64(2048), uint64(1024), uint64(1024), 50.0, uint64(7), uint64(9)}
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d", len(got), len(want))
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

// swapFixture supplies fixed swap values for the ordering check.
var swapFixture = mem.SwapMemoryStat{
	Total:       2048,
	Used:        1024,
	Free:        1024,
	UsedPercent: 50.0,
	Sin:         7,
	Sout:        9,
}
