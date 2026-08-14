package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newTestStore(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()

	srv := miniredis.RunT(t)
	s := NewRedis(Options{Addr: srv.Addr()})
	t.Cleanup(func() { s.Close() })

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
		t.Fatal("Schluessel nicht gefunden")
	}
	if string(got) != string(value) {
		t.Errorf("Wert = %s, want %s", got, value)
	}
}

// Ein fehlender Schlüssel ist kein Fehler.
func TestGetMissingKey(t *testing.T) {
	s, _ := newTestStore(t)

	got, ok, err := s.Get(context.Background(), "gibtsnicht")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (Wert: %s)", got)
	}
}

// Die Verfallszeit muss ankommen – ohne sie würden veraltete Messwerte
// dauerhaft ausgeliefert.
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
		t.Error("der Schluessel hat die Verfallszeit ueberdauert")
	}
}

func TestSetOverwrites(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	s.Set(ctx, "k", json.RawMessage(`[1]`), time.Minute)
	s.Set(ctx, "k", json.RawMessage(`[1,2]`), time.Minute)

	got, _, _ := s.Get(ctx, "k")
	if string(got) != `[1,2]` {
		t.Errorf("Wert = %s, want [1,2]", got)
	}
}

func TestGetReportsConnectionError(t *testing.T) {
	s, srv := newTestStore(t)
	srv.Close()

	if _, _, err := s.Get(context.Background(), "k"); err == nil {
		t.Error("erwarte einen Fehler bei geschlossener Verbindung")
	}
}

func TestClientIsExposedForPubSub(t *testing.T) {
	s, _ := newTestStore(t)

	if s.Client() == nil {
		t.Error("Client() liefert nil")
	}
}
