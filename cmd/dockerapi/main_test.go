package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/actions"
	"bodsch.me/mailcow-dockerapi/internal/config"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
	"bodsch.me/mailcow-dockerapi/internal/logging"
	"bodsch.me/mailcow-dockerapi/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// config.Log and logging.Options are converted into one another, which only
// compiles while the two structs agree. This test pins the behaviour that
// conversion is supposed to carry: the compatibility format stays the default and
// the structured formats are reachable.
func TestLoggerIsBuiltFromTheConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Log
		wants string
	}{
		{"the drop-in default", config.Log{Level: "info", Format: "python"}, "INFO:     hello"},
		{"json", config.Log{Level: "info", Format: "json"}, `"msg":"hello"`},
		{"text", config.Log{Level: "info", Format: "text"}, "msg=hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logging.New(&buf, logging.Options(tc.cfg)).Info("hello")

			if !strings.Contains(buf.String(), tc.wants) {
				t.Errorf("output %q does not contain %q", buf.String(), tc.wants)
			}
		})
	}

	// The level has to travel too.
	var buf bytes.Buffer
	log := logging.New(&buf, logging.Options(config.Log{Level: "error", Format: "python"}))
	if log.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("a warning should be filtered out at level error")
	}
}

// Startup waits for Redis rather than answering requests it cannot serve.
func TestWaitForRetriesUntilAvailable(t *testing.T) {
	attempts := 0
	probe := func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("connection refused")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := waitFor(ctx, discardLog(), "Redis", probe); err != nil {
		t.Fatalf("waitFor: %v", err)
	}
	if attempts != 3 {
		t.Errorf("probed %d times, want 3", attempts)
	}
}

func TestWaitForStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	probe := func(context.Context) error { return errors.New("connection refused") }
	if err := waitFor(ctx, discardLog(), "Redis", probe); !errors.Is(err, context.Canceled) {
		t.Errorf("waitFor = %v, want context.Canceled", err)
	}
}

// A failed listener has to reach run's caller rather than being lost in the
// goroutine that serves it.
func TestWaitForObsReportsTheServerError(t *testing.T) {
	failed := errors.New("serving metrics on :9394: address in use")

	done := make(chan error, 1)
	done <- failed

	if err := waitForObs(done); !errors.Is(err, failed) {
		t.Errorf("waitForObs = %v, want %v", err, failed)
	}
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// A failed TLS handshake never reaches a handler, so http.Server.ErrorLog is the
// only place it surfaces. The wiring that resolves the peer in those lines lives
// in build, which is where a refactoring loses it silently.
func TestBuildInstallsThePeerResolvingErrorLog(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Server: config.Server{
			Listen:   ":0",
			CertFile: filepath.Join(dir, "cert.pem"),
			KeyFile:  filepath.Join(dir, "key.pem"),
		},
		Stats: config.Stats{Timeout: time.Second},
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fake := dockertest.WithContainers("aaa", "mailcowdockerized-watchdog-mailcow-1")
	fake.Containers[0].Endpoints = []dockerclient.Endpoint{
		{Network: "mailcowdockerized_mailcow-network", IPs: []string{"172.22.1.12"}},
	}

	deps := &dependencies{docker: fake, env: actions.Env{Docker: fake, Log: log}}

	srv, err := build(cfg, deps, metrics.New(prometheus.NewRegistry(), metrics.Build{Version: "test"}), log)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if srv.ErrorLog == nil {
		t.Fatal("ErrorLog is nil — the server's error lines would go out as plain text again")
	}

	srv.ErrorLog.Print("http: TLS handshake error from 172.22.1.12:58798: EOF")

	if got := buf.String(); !strings.Contains(got, `"peer_container":"mailcowdockerized-watchdog-mailcow-1"`) {
		t.Errorf("the log line %q does not name the container behind the address", got)
	}
}
