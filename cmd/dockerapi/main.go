// Command dockerapi is the broker between the mailcow UI and the Docker daemon.
//
// It replaces the Python implementation in original/dockerapi while keeping the
// HTTP and PubSub interfaces unchanged.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embedded timezone data — the image ships without tzdata.
	_ "time/tzdata"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/api"
	"bodsch.me/mailcow-dockerapi/internal/config"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
	"bodsch.me/mailcow-dockerapi/internal/obs"
	"bodsch.me/mailcow-dockerapi/internal/pubsub"
	"bodsch.me/mailcow-dockerapi/internal/stats"
	"bodsch.me/mailcow-dockerapi/internal/store"
	"bodsch.me/mailcow-dockerapi/internal/tlsgen"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// version is set at build time from the Makefile.
var version = "dev"

const (
	// shutdownTimeout bounds the wait for in-flight requests when stopping.
	shutdownTimeout = 15 * time.Second

	// obsShutdownTimeout bounds the wait for the observability server to stop.
	obsShutdownTimeout = 10 * time.Second

	// redisPoll is how often startup retests Redis.
	redisPoll = 2 * time.Second
)

func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("dockerapi failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(config.EnvLookup)
	if err != nil {
		// The logger is not configured yet, so this goes out through the default
		// one rather than being swallowed.
		return err
	}

	log := logging.New(os.Stdout, logging.Options(cfg.Log))
	slog.SetDefault(log)
	log.Info("mailcow dockerapi starting",
		"version", version,
		"listen", cfg.Server.Listen,
		"docker", cfg.Docker.Host,
		"log_level", cfg.Log.Level)

	// Signals arrive here rather than in a goroutine so every stage of startup can
	// be interrupted cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m := metrics.New(registry, version)

	readiness := &obs.Readiness{}
	obsServer := obs.New(obs.Options{
		Listen:    cfg.Obs.Listen,
		Gatherer:  registry,
		Readiness: readiness,
		Log:       log,
	})

	obsDone := make(chan error, 1)
	go func() { obsDone <- obsServer.Run(ctx) }()

	deps, cleanup, err := connect(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer cleanup()

	srv, err := build(cfg, deps, m, log)
	if err != nil {
		return err
	}

	// The PubSub subscriber runs for as long as the service does.
	subscriber := pubsub.New(pubsub.Options{
		Client:  deps.store.Client(),
		Channel: cfg.Redis.Channel,
		Env:     deps.env,
		Metrics: m,
		Log:     log,
	})
	go func() {
		if err := subscriber.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("the pubsub subscriber stopped", "err", err)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Server.Listen)
		errCh <- srv.ListenAndServeTLS("", "")
	}()

	readiness.SetReady(true)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return waitForObs(obsDone)
}

// dependencies are the connections the service shares.
type dependencies struct {
	docker dockerclient.API
	store  *store.Redis
	env    actions.Env
}

// connect opens every external connection and waits for the ones the service
// cannot start without.
func connect(ctx context.Context, cfg *config.Config, log *slog.Logger) (*dependencies, func(), error) {
	var closers []func()
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	docker, err := dockerclient.New(cfg.Docker.Host)
	if err != nil {
		return nil, cleanup, fmt.Errorf("connecting to the docker daemon: %w", err)
	}
	closers = append(closers, func() { _ = docker.Close() })

	redis := store.New(store.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	closers = append(closers, func() { _ = redis.Close() })

	// Every stats request and every PubSub job needs Redis, so startup waits for
	// it rather than answering requests it cannot serve.
	if err := waitFor(ctx, log, "Redis", redis.Ping); err != nil {
		return nil, cleanup, err
	}

	log.Info("connected", "redis", cfg.Redis.Addr, "docker", cfg.Docker.Host)

	return &dependencies{
		docker: docker,
		store:  redis,
		env: actions.Env{
			Docker: docker,
			DBRoot: cfg.DB.Root,
			Log:    log,
		},
	}, cleanup, nil
}

// build assembles the HTTP server. It performs no I/O beyond reading or creating
// the certificate.
func build(cfg *config.Config, deps *dependencies, m *metrics.Metrics, log *slog.Logger) (*http.Server, error) {
	collector := stats.New(stats.Options{
		Docker:  deps.docker,
		Store:   deps.store,
		Log:     log,
		Timeout: cfg.Stats.Timeout,
	})

	// The certificate used to be created by the entrypoint with openssl; when it
	// is missing, one is generated at startup now.
	cert, err := tlsgen.Ensure(cfg.Server.CertFile, cfg.Server.KeyFile, tlsgen.Options{})
	if err != nil {
		return nil, fmt.Errorf("preparing the server certificate: %w", err)
	}

	handler := api.New(api.Options{
		Docker:  deps.docker,
		Stats:   collector,
		Env:     deps.env,
		Metrics: m,
		Log:     log,
	}).Handler()

	return &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}, nil
}

// waitFor polls until probe succeeds or the context is cancelled.
func waitFor(ctx context.Context, log *slog.Logger, what string, probe func(context.Context) error) error {
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, redisPoll)
		err := probe(attemptCtx)
		cancel()

		if err == nil {
			log.Info("dependency is available", "dependency", what, "attempts", attempt)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Only the first failure is worth an info line; after that it is noise
		// until it succeeds.
		if attempt == 1 {
			log.Info("waiting for dependency", "dependency", what, "err", err)
		} else {
			log.Debug("still waiting for dependency", "dependency", what, "attempt", attempt, "err", err)
		}

		select {
		case <-time.After(redisPoll):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitForObs collects the observability server's exit status, so a failed listener
// is reported rather than lost in a goroutine.
func waitForObs(done chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(obsShutdownTimeout):
		return errors.New("the observability server did not shut down")
	}
}
