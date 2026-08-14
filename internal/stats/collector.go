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

// Schlüssel und Verfallszeiten aus DockerApi.py.
const (
	HostStatsKey        = "host_stats"
	ContainerStatsSufix = "_stats"

	HostStatsTTL      = 10 * time.Second
	ContainerStatsTTL = 60 * time.Second

	// MaxSamples ist die Länge des Ringpuffers je Container.
	MaxSamples = 3

	// DefaultRefreshInterval entspricht dem wait=5 aus DockerApi.py:518.
	DefaultRefreshInterval = 5 * time.Second
)

// ErrTimeout meldet, dass innerhalb der Frist keine Messwerte vorlagen.
//
// In main.py:75 und main.py:187 wartete die Anfrage an dieser Stelle in einer
// Endlosschleife, die ohne Redis nie endete.
var ErrTimeout = errors.New("timeout waiting for stats")

// Collector sammelt Messwerte und legt sie im Zwischenspeicher ab.
//
// Mehrere gleichzeitige Anfragen für dieselben Werte lösen nur einen
// Sammelvorgang aus; die übrigen warten auf dessen Ergebnis. Das Original
// nutzte dafür einen Merker und eine Liste ohne Sperre.
type Collector struct {
	docker  dockerclient.API
	store   store.Store
	host    HostProvider
	logger  *slog.Logger
	timeout time.Duration

	// refreshInterval ist die Pause zwischen den beiden Messungen eines
	// Sammelvorgangs.
	refreshInterval time.Duration

	mu       sync.Mutex
	inflight map[string]*refresh
}

// refresh hält den Zustand eines laufenden Sammelvorgangs.
type refresh struct {
	// done wird geschlossen, sobald die erste Messung abgeschlossen ist.
	done chan struct{}
	// err hält den Fehler dieser Messung, damit die wartende Anfrage die
	// Ursache melden kann statt nur eine abgelaufene Frist.
	err error
}

// Config beschreibt einen Collector.
type Config struct {
	Docker  dockerclient.API
	Store   store.Store
	Host    HostProvider
	Logger  *slog.Logger
	Timeout time.Duration
	// RefreshInterval ist optional; 0 bedeutet DefaultRefreshInterval.
	RefreshInterval time.Duration
}

// New baut einen Collector.
func New(cfg Config) *Collector {
	if cfg.Host == nil {
		cfg.Host = SystemHost{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = DefaultRefreshInterval
	}

	return &Collector{
		docker:          cfg.Docker,
		store:           cfg.Store,
		host:            cfg.Host,
		logger:          cfg.Logger,
		timeout:         cfg.Timeout,
		refreshInterval: cfg.RefreshInterval,
		inflight:        map[string]*refresh{},
	}
}

// HostStats liefert die Kennzahlen des Wirtssystems, notfalls nach einem
// frisch angestoßenen Sammelvorgang.
func (c *Collector) HostStats(ctx context.Context) (json.RawMessage, error) {
	return c.cachedOrRefresh(ctx, HostStatsKey, c.refreshHost)
}

// ContainerStats liefert den Ringpuffer der letzten Messungen eines Containers.
func (c *Collector) ContainerStats(ctx context.Context, containerID string) (json.RawMessage, error) {
	key := containerID + ContainerStatsSufix

	return c.cachedOrRefresh(ctx, key, func(ctx context.Context) error {
		return c.refreshContainer(ctx, containerID)
	})
}

// cachedOrRefresh gibt den zwischengespeicherten Wert zurück oder wartet auf
// einen Sammelvorgang.
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

	// Kein Wert vorhanden: die Ursache aus dem Sammelvorgang ist
	// aussagekräftiger als eine abgelaufene Frist.
	if r.err != nil {
		return nil, r.err
	}

	return nil, ErrTimeout
}

// startRefresh stößt einen Sammelvorgang an oder liefert den Kanal eines
// bereits laufenden.
//
// Der Kanal wird geschlossen, sobald die erste Messung geschrieben ist – die
// wartende Anfrage muss die zweite Messung nicht abwarten.
func (c *Collector) startRefresh(key string, collect func(context.Context) error) *refresh {
	c.mu.Lock()
	defer c.mu.Unlock()

	if running, ok := c.inflight[key]; ok {
		return running
	}

	r := &refresh{done: make(chan struct{})}
	c.inflight[key] = r

	go func() {
		// Der Sammelvorgang überdauert die auslösende Anfrage.
		ctx, cancel := context.WithTimeout(context.Background(), c.refreshInterval+time.Minute)
		defer cancel()

		if err := collect(ctx); err != nil {
			c.logger.Error("collecting stats failed", "key", key, "error", err)
			r.err = err
		}

		close(r.done)

		// Wie im Original folgt eine zweite Messung, damit der Ringpuffer
		// zwischen zwei Anfragen wächst.
		select {
		case <-time.After(c.refreshInterval):
			if err := collect(ctx); err != nil {
				c.logger.Error("collecting stats failed", "key", key, "error", err)
			}
		case <-ctx.Done():
		}

		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
	}()

	return r
}

// refreshHost sammelt die Kennzahlen des Wirtssystems und legt sie ab.
func (c *Collector) refreshHost(ctx context.Context) error {
	stats, err := c.host.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect host stats: %w", err)
	}

	raw, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("encode host stats: %w", err)
	}

	return c.store.Set(ctx, HostStatsKey, raw, HostStatsTTL)
}

// refreshContainer hängt eine Messung an den Ringpuffer des Containers an.
func (c *Collector) refreshContainer(ctx context.Context, containerID string) error {
	sample, err := c.docker.Stats(ctx, containerID)
	if err != nil {
		return fmt.Errorf("collect container stats: %w", err)
	}

	key := containerID + ContainerStatsSufix

	samples := c.loadSamples(ctx, key)
	samples = append(samples, sample)
	if len(samples) > MaxSamples {
		samples = samples[len(samples)-MaxSamples:]
	}

	raw, err := json.Marshal(samples)
	if err != nil {
		return fmt.Errorf("encode container stats: %w", err)
	}

	return c.store.Set(ctx, key, raw, ContainerStatsTTL)
}

// loadSamples liest den bestehenden Ringpuffer. Unlesbare Inhalte werden
// verworfen, damit ein beschädigter Eintrag den Container nicht dauerhaft
// blockiert.
func (c *Collector) loadSamples(ctx context.Context, key string) []json.RawMessage {
	raw, ok, err := c.store.Get(ctx, key)
	if err != nil || !ok {
		return nil
	}

	var samples []json.RawMessage
	if err := json.Unmarshal(raw, &samples); err != nil {
		c.logger.Warn("discarding unreadable stats buffer", "key", key, "error", err)
		return nil
	}

	return samples
}
