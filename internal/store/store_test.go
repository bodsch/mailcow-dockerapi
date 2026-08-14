package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newTestStore(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()

	srv := miniredis.RunT(t)
	s := New(Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = s.Close() })

	return s, srv
}

func TestSetAndGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	value := json.RawMessage(`{"cpu":{"cores":8}}`)
	if err := s.Set(ctx, "host_stats", value, 10*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok, err := s.Get(ctx, "host_stats")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("the key was not found")
	}
	if string(got) != string(value) {
		t.Errorf("value = %s, want %s", got, value)
	}
}

// A missing key is not an error.
func TestGetMissingKey(t *testing.T) {
	s, _ := newTestStore(t)

	got, ok, err := s.Get(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (value: %s)", got)
	}
}

// The expiry has to arrive — without it, stale measurements would be served
// forever.
func TestSetAppliesTTL(t *testing.T) {
	s, srv := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "host_stats", json.RawMessage(`{}`), 10*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if ttl := srv.TTL("host_stats"); ttl != 10*time.Second {
		t.Errorf("TTL = %v, want 10s", ttl)
	}

	srv.FastForward(11 * time.Second)

	if _, ok, _ := s.Get(ctx, "host_stats"); ok {
		t.Error("the key outlived its expiry")
	}
}

func TestSetOverwrites(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "k", json.RawMessage(`[1]`), time.Minute)
	_ = s.Set(ctx, "k", json.RawMessage(`[1,2]`), time.Minute)

	got, _, _ := s.Get(ctx, "k")
	if string(got) != `[1,2]` {
		t.Errorf("value = %s, want [1,2]", got)
	}
}

// A connection error has to name the operation, so a log line says what failed.
func TestErrorsNameTheOperation(t *testing.T) {
	s, srv := newTestStore(t)
	srv.Close()

	_, _, err := s.Get(context.Background(), "k")
	if err == nil {
		t.Fatal("expected an error on a closed connection")
	}
	if !strings.Contains(err.Error(), "GET k") {
		t.Errorf("Get error %q does not name the operation", err)
	}

	if err := s.Set(context.Background(), "k", json.RawMessage(`{}`), time.Minute); err == nil {
		t.Error("expected an error on a closed connection")
	} else if !strings.Contains(err.Error(), "SET k") {
		t.Errorf("Set error %q does not name the operation", err)
	}
}

func TestPing(t *testing.T) {
	s, srv := newTestStore(t)

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	srv.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping should fail once the server is gone")
	}
}

func TestClientIsExposedForPubSub(t *testing.T) {
	s, _ := newTestStore(t)

	if s.Client() == nil {
		t.Error("Client() returned nil")
	}
}
