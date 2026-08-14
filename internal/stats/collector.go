package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/store"
)

// Keys and expiries from DockerApi.py.
const (
	HostStatsKey        = "host_stats"
	ContainerStatsSufix = "_stats"

	HostStatsTTL      = 10 * time.Second
	ContainerStatsTTL = 60 * time.Second

	// MaxSamples is the length of the per-container ring buffer.
	MaxSamples = 3

	// DefaultRefreshInterval matches wait=5 from DockerApi.py:518.
	DefaultRefreshInterval = 5 * time.Second
)

// ErrTimeout reports that no measurement arrived within the deadline.
//
// In main.py:75 and main.py:187 the request waited here in an endless loop that
// never ended without Redis.
var ErrTimeout = errors.New("timeout waiting for stats")

// Collector gathers measurements and puts them in the cache.
//
// Several concurrent requests for the same values trigger only one collection; the
// others wait for its result. The original used a flag and a list, without a lock.
type Collector struct {
	docker  dockerclient.API
	store   store.Store
	host    HostProvider
	log     *slog.Logger
	timeout time.Duration

	// refreshInterval is the pause between the two measurements of one
	// collection.
	refreshInterval time.Duration

	mu       sync.Mutex
	inflight map[string]*refresh
}

// refresh holds the state of a running collection.
type refresh struct {
	// done is closed as soon as the first measurement is complete.
	done chan struct{}
	// err holds that measurement's error, so the waiting request can report the
	// cause rather than only an expired deadline.
	err error
}

// Options describes a Collector.
type Options struct {
	Docker  dockerclient.API
	Store   store.Store
	Host    HostProvider
	Log     *slog.Logger
	Timeout time.Duration
	// RefreshInterval is optional; 0 means DefaultRefreshInterval.
	RefreshInterval time.Duration
}

// New builds a Collector.
func New(opts Options) *Collector {
	if opts.Host == nil {
		opts.Host = SystemHost{}
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.RefreshInterval == 0 {
		opts.RefreshInterval = DefaultRefreshInterval
	}

	return &Collector{
		docker:          opts.Docker,
		store:           opts.Store,
		host:            opts.Host,
		log:             opts.Log.With("component", "stats"),
		timeout:         opts.Timeout,
		refreshInterval: opts.RefreshInterval,
		inflight:        map[string]*refresh{},
	}
}

// HostStats returns the host's figures, collecting them first if necessary.
func (c *Collector) HostStats(ctx context.Context) (json.RawMessage, error) {
	return c.cachedOrRefresh(ctx, HostStatsKey, c.refreshHost)
}

// ContainerStats returns the ring buffer of a container's most recent samples.
func (c *Collector) ContainerStats(ctx context.Context, containerID string) (json.RawMessage, error) {
	key := containerID + ContainerStatsSufix

	return c.cachedOrRefresh(ctx, key, func(ctx context.Context) error {
		return c.refreshContainer(ctx, containerID)
	})
}

// cachedOrRefresh returns the cached value or waits for a collection.
func (c *Collector) cachedOrRefresh(
	ctx context.Context,
	key string,
	collect func(context.Context) error,
) (json.RawMessage, error) {
	if raw, ok, err := c.store.Get(ctx, key); err == nil && ok {
		return raw, nil
	}

	r := c.startRefresh(key, collect)

	timeout := c.timeout
	if timeout <= 0 {
		timeout = time.Minute
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-r.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrTimeout
	}

	raw, ok, err := c.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if ok {
		return raw, nil
	}

	// No value present: the collection's cause is more informative than an
	// expired deadline.
	if r.err != nil {
		return nil, r.err
	}

	return nil, ErrTimeout
}

// startRefresh starts a collection or returns the channel of one already running.
//
// The channel is closed as soon as the first measurement has been written — the
// waiting request does not have to sit through the second one.
func (c *Collector) startRefresh(key string, collect func(context.Context) error) *refresh {
	c.mu.Lock()
	defer c.mu.Unlock()

	if running, ok := c.inflight[key]; ok {
		return running
	}

	r := &refresh{done: make(chan struct{})}
	c.inflight[key] = r

	go func() {
		// The collection outlives the request that triggered it.
		ctx, cancel := context.WithTimeout(context.Background(), c.refreshInterval+time.Minute)
		defer cancel()

		if err := collect(ctx); err != nil {
			c.log.Error("collecting stats failed", "key", key, "err", err)
			r.err = err
		}

		close(r.done)

		// As in the original a second measurement follows, so the ring buffer
		// grows between two requests.
		select {
		case <-time.After(c.refreshInterval):
			if err := collect(ctx); err != nil {
				c.log.Error("collecting stats failed", "key", key, "err", err)
			}
		case <-ctx.Done():
		}

		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
	}()

	return r
}

// refreshHost collects the host's figures and stores them.
func (c *Collector) refreshHost(ctx context.Context) error {
	stats, err := c.host.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collecting the host statistics: %w", err)
	}

	raw, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("encoding the host statistics: %w", err)
	}

	return c.store.Set(ctx, HostStatsKey, raw, HostStatsTTL)
}

// refreshContainer appends a measurement to the container's ring buffer.
func (c *Collector) refreshContainer(ctx context.Context, containerID string) error {
	sample, err := c.docker.Stats(ctx, containerID)
	if err != nil {
		return fmt.Errorf("collecting the container statistics: %w", err)
	}

	key := containerID + ContainerStatsSufix

	samples := c.loadSamples(ctx, key)
	samples = append(samples, sample)
	if len(samples) > MaxSamples {
		samples = samples[len(samples)-MaxSamples:]
	}

	raw, err := json.Marshal(samples)
	if err != nil {
		return fmt.Errorf("encoding the container statistics: %w", err)
	}

	return c.store.Set(ctx, key, raw, ContainerStatsTTL)
}

// loadSamples reads the existing ring buffer. Unreadable content is discarded, so
// one damaged entry does not block a container permanently.
func (c *Collector) loadSamples(ctx context.Context, key string) []json.RawMessage {
	raw, ok, err := c.store.Get(ctx, key)
	if err != nil || !ok {
		return nil
	}

	var samples []json.RawMessage
	if err := json.Unmarshal(raw, &samples); err != nil {
		c.log.Warn("discarding unreadable stats buffer", "key", key, "err", err)
		return nil
	}

	return samples
}
