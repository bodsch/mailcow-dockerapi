package stats

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
)

// memStore is an in-memory cache.
type memStore struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
	ttls   map[string]time.Duration

	getErr error
	setErr error
	sets   int
}

func newMemStore() *memStore {
	return &memStore{
		values: map[string]json.RawMessage{},
		ttls:   map[string]time.Duration{},
	}
}

func (m *memStore) Get(_ context.Context, key string) (json.RawMessage, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, false, m.getErr
	}

	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memStore) Set(_ context.Context, key string, value json.RawMessage, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setErr != nil {
		return m.setErr
	}

	m.values[key] = value
	m.ttls[key] = ttl
	m.sets++

	return nil
}

func (m *memStore) Close() error { return nil }

func (m *memStore) ttl(key string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ttls[key]
}

// fixedHost returns canned figures.
type fixedHost struct {
	stats HostStats
	err   error
	calls int
	mu    sync.Mutex
}

func (f *fixedHost) Collect(context.Context) (HostStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	return f.stats, f.err
}

func (f *fixedHost) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

func testLogger() *slog.Logger {
	return logging.New(io.Discard, logging.Options{Level: "error", Format: "text"})
}

func newCollector(t *testing.T, opts Options) (*Collector, *memStore) {
	t.Helper()

	if opts.Store == nil {
		opts.Store = newMemStore()
	}
	if opts.Log == nil {
		opts.Log = testLogger()
	}
	if opts.Timeout == 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.RefreshInterval == 0 {
		// Keep it short so the second measurement does not hold up the test.
		opts.RefreshInterval = 10 * time.Millisecond
	}

	return New(opts), opts.Store.(*memStore)
}

// The response format has to match the dict from DockerApi.py — swap as an array
// and the kernel architecture in particular.
func TestHostStatsEncoding(t *testing.T) {
	host := &fixedHost{stats: HostStats{
		CPU: CPU{Cores: 8, Usage: 12.5},
		Memory: Memory{
			Total: 16777216,
			Usage: 42.5,
			Swap:  []any{uint64(2048), uint64(1024), uint64(1024), 50.0, uint64(0), uint64(0)},
		},
		Uptime:       123456.75,
		SystemTime:   "14.08.2026 12:00:00",
		Architecture: "aarch64",
	}}

	c, store := newCollector(t, Options{Host: host})

	raw, err := c.HostStats(context.Background())
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}

	want := `{"cpu":{"cores":8,"usage":12.5},` +
		`"memory":{"total":16777216,"usage":42.5,"swap":[2048,1024,1024,50,0,0]},` +
		`"uptime":123456.75,"system_time":"14.08.2026 12:00:00","architecture":"aarch64"}`

	if string(raw) != want {
		t.Errorf("response =\n%s\nwant\n%s", raw, want)
	}

	if ttl := store.ttl(HostStatsKey); ttl != HostStatsTTL {
		t.Errorf("TTL = %v, want %v", ttl, HostStatsTTL)
	}
}

// When a value is cached, nothing is collected.
func TestHostStatsUsesCache(t *testing.T) {
	host := &fixedHost{}
	c, store := newCollector(t, Options{Host: host})

	store.values[HostStatsKey] = json.RawMessage(`{"cached":true}`)

	raw, err := c.HostStats(context.Background())
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}

	if string(raw) != `{"cached":true}` {
		t.Errorf("response = %s", raw)
	}
	if host.callCount() != 0 {
		t.Errorf("collections = %d, want 0", host.callCount())
	}
}

// Concurrent requests trigger exactly one collection.
func TestHostStatsCollectsOnceUnderConcurrency(t *testing.T) {
	host := &fixedHost{stats: HostStats{Architecture: "x86_64"}}
	c, _ := newCollector(t, Options{Host: host, RefreshInterval: time.Hour})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.HostStats(context.Background()); err != nil {
				t.Errorf("HostStats: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := host.callCount(); got != 1 {
		t.Errorf("collections = %d, want 1", got)
	}
}

// When collecting fails, the request reports the cause — rather than waiting
// forever on a Redis key, as the original did.
func TestHostStatsReportsCollectError(t *testing.T) {
	collectErr := errors.New("psutil is broken")
	host := &fixedHost{err: collectErr}
	c, _ := newCollector(t, Options{Host: host, Timeout: 100 * time.Millisecond})

	start := time.Now()
	_, err := c.HostStats(context.Background())

	if !errors.Is(err, collectErr) {
		t.Fatalf("err = %v, want %v", err, collectErr)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v, too long", elapsed)
	}
}

// blockingHost holds the collection until it is released.
type blockingHost struct {
	release chan struct{}
}

func (b *blockingHost) Collect(ctx context.Context) (HostStats, error) {
	select {
	case <-b.release:
		return HostStats{}, nil
	case <-ctx.Done():
		return HostStats{}, ctx.Err()
	}
}

// When the caller gives up while a collection is still running, the request ends
// immediately.
func TestHostStatsTimesOutWhileCollecting(t *testing.T) {
	host := &blockingHost{release: make(chan struct{})}
	defer close(host.release)

	c, _ := newCollector(t, Options{Host: host, Timeout: 50 * time.Millisecond})

	start := time.Now()
	if _, err := c.HostStats(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v, too long", elapsed)
	}
}

func TestHostStatsRespectsContextCancellation(t *testing.T) {
	host := &blockingHost{release: make(chan struct{})}
	defer close(host.release)

	c, _ := newCollector(t, Options{Host: host, Timeout: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if _, err := c.HostStats(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestContainerStatsBuildsRingBuffer(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")

	var counter int
	var mu sync.Mutex
	fake.StatsFn = func(string) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()

		counter++
		return json.RawMessage(`{"n":` + itoa(counter) + `}`), nil
	}

	c, store := newCollector(t, Options{Docker: fake, RefreshInterval: time.Hour})

	raw, err := c.ContainerStats(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}

	if string(raw) != `[{"n":1}]` {
		t.Errorf("response = %s, want [{\"n\":1}]", raw)
	}

	if ttl := store.ttl("abc123" + ContainerStatsSufix); ttl != ContainerStatsTTL {
		t.Errorf("TTL = %v, want %v", ttl, ContainerStatsTTL)
	}
}

// The ring buffer holds at most three measurements; the oldest falls out.
func TestContainerStatsKeepsAtMostThreeSamples(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	c, store := newCollector(t, Options{Docker: fake, RefreshInterval: time.Hour})

	key := "abc123" + ContainerStatsSufix
	store.values[key] = json.RawMessage(`[{"n":1},{"n":2},{"n":3}]`)

	fake.StatsFn = func(string) (json.RawMessage, error) {
		return json.RawMessage(`{"n":4}`), nil
	}

	if err := c.refreshContainer(context.Background(), "abc123"); err != nil {
		t.Fatalf("refreshContainer: %v", err)
	}

	var samples []json.RawMessage
	if err := json.Unmarshal(store.values[key], &samples); err != nil {
		t.Fatalf("reading the ring buffer: %v", err)
	}

	if len(samples) != MaxSamples {
		t.Fatalf("samples = %d, want %d", len(samples), MaxSamples)
	}
	if string(samples[0]) != `{"n":2}` {
		t.Errorf("oldest entry = %s, want {\"n\":2}", samples[0])
	}
	if string(samples[2]) != `{"n":4}` {
		t.Errorf("newest entry = %s, want {\"n\":4}", samples[2])
	}
}

// A damaged entry must not block a container permanently.
func TestContainerStatsRecoversFromCorruptBuffer(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	fake.StatsFn = func(string) (json.RawMessage, error) {
		return json.RawMessage(`{"n":1}`), nil
	}

	c, store := newCollector(t, Options{Docker: fake, RefreshInterval: time.Hour})
	store.values["abc123"+ContainerStatsSufix] = json.RawMessage(`not json`)

	if err := c.refreshContainer(context.Background(), "abc123"); err != nil {
		t.Fatalf("refreshContainer: %v", err)
	}

	if got := string(store.values["abc123"+ContainerStatsSufix]); got != `[{"n":1}]` {
		t.Errorf("ring buffer = %s", got)
	}
}

// After the interval a second measurement follows, so the buffer grows.
func TestRefreshTakesSecondSample(t *testing.T) {
	fake := dockertest.WithContainers("abc123", "postfix-mailcow")

	var mu sync.Mutex
	var counter int
	fake.StatsFn = func(string) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()

		counter++
		return json.RawMessage(`{"n":` + itoa(counter) + `}`), nil
	}

	c, store := newCollector(t, Options{Docker: fake, RefreshInterval: 20 * time.Millisecond})

	if _, err := c.ContainerStats(context.Background(), "abc123"); err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}

	// Wait for the second measurement.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, _, _ := store.Get(context.Background(), "abc123"+ContainerStatsSufix)
		if strings.Count(string(raw), `"n"`) >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Error("the second measurement never happened")
}

func TestContainerStatsPropagatesDockerError(t *testing.T) {
	dockerErr := errors.New("the container is gone")

	fake := dockertest.WithContainers("abc123", "postfix-mailcow")
	fake.StatsFn = func(string) (json.RawMessage, error) {
		return nil, dockerErr
	}

	c, _ := newCollector(t, Options{
		Docker:          fake,
		Timeout:         100 * time.Millisecond,
		RefreshInterval: time.Hour,
	})

	if _, err := c.ContainerStats(context.Background(), "abc123"); !errors.Is(err, dockerErr) {
		t.Errorf("err = %v, want %v", err, dockerErr)
	}
}

func TestSwapTupleOrder(t *testing.T) {
	got := swapTuple(nil)
	if len(got) != 0 {
		t.Errorf("swapTuple(nil) = %v, want an empty array", got)
	}
}

func TestTimeLayoutMatchesPython(t *testing.T) {
	// strftime("%d.%m.%Y %H:%M:%S")
	ts := time.Date(2026, 8, 14, 9, 5, 3, 0, time.UTC)

	if got := ts.Format(TimeLayout); got != "14.08.2026 09:05:03" {
		t.Errorf("format = %q, want %q", got, "14.08.2026 09:05:03")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}
