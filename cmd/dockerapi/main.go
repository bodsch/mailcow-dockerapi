// Command dockerapi ist der Vermittler zwischen der mailcow-Oberfläche und
// dem Docker-Daemon.
//
// Es ersetzt die Python-Fassung aus original/dockerapi bei gleichbleibender
// HTTP- und PubSub-Schnittstelle.
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

	// Eingebettete Zeitzonendaten – das Abbild kommt ohne tzdata aus.
	_ "time/tzdata"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/api"
	"bodsch.me/mailcow-dockerapi/internal/config"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"bodsch.me/mailcow-dockerapi/internal/pubsub"
	"bodsch.me/mailcow-dockerapi/internal/stats"
	"bodsch.me/mailcow-dockerapi/internal/store"
	"bodsch.me/mailcow-dockerapi/internal/tlsgen"
)

// shutdownTimeout begrenzt das Warten auf laufende Anfragen beim Beenden.
const shutdownTimeout = 15 * time.Second

// version wird beim Bauen über -ldflags gesetzt.
var version = "dev"

func main() {
	logger := logging.New(os.Stdout, slog.LevelInfo)

	if err := run(logger); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	logger.Info("Init APP", "version", version)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Beendet den Dienst geordnet bei SIGINT und SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	docker, err := dockerclient.New(cfg.DockerHost)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer docker.Close()

	redisStore := store.NewRedis(store.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisStore.Close()

	logger.Info("connected", "redis", cfg.RedisAddr, "docker", cfg.DockerHost)

	env := actions.Env{Docker: docker, DBRoot: cfg.DBRoot, Logger: logger}

	collector := stats.New(stats.Config{
		Docker:  docker,
		Store:   redisStore,
		Logger:  logger,
		Timeout: cfg.StatsTimeout,
	})

	// Der PubSub-Empfänger läuft, solange der Dienst läuft.
	subscriber := pubsub.New(redisStore.Client(), cfg.RedisChannel, env, logger)
	go func() {
		if err := subscriber.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("pubsub stopped", "error", err)
		}
	}()

	// Das Zertifikat entstand früher im Entrypoint per openssl; fehlt es,
	// wird jetzt beim Start eines erzeugt.
	cert, err := tlsgen.Ensure(cfg.CertFile, cfg.KeyFile, tlsgen.Options{})
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: api.New(docker, collector, env, logger).Handler(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr)
		errCh <- srv.ListenAndServeTLS("", "")
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}
