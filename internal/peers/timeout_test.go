package peers

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// blockingLister accepts a listing request and never answers, which is what the
// Docker daemon looks like while it is itself wedged.
type blockingLister struct{ release chan struct{} }

func (l *blockingLister) ListAll(ctx context.Context, _ bool) ([]dockerclient.Container, error) {
	select {
	case <-l.release:
		return nil, errors.New("released")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestLookupGivesUpOnAWedgedDocker is the promise on DefaultTimeout: the lookup
// "runs while a connection is being logged, so it must not be able to hold that
// up for long".
//
// Attribute is called from errorlog.Write, which net/http calls from the
// goroutine serving the connection. A lookup that blocks blocks that goroutine,
// and TLS handshake failures are the normal case here — every port scan and every
// watchdog probe against the wrong port produces one. A wedged Docker daemon
// would pile up serving goroutines on a log line.
//
// The existing suite covers a Docker that *fails*, which returns at once. This is
// the one that does not.
func TestLookupGivesUpOnAWedgedDocker(t *testing.T) {
	lister := &blockingLister{release: make(chan struct{})}
	t.Cleanup(func() { close(lister.release) })

	r := New(Options{
		Docker:  lister,
		Timeout: 200 * time.Millisecond,
		LookupAddr: func(ctx context.Context, _ string) ([]string, error) {
			return nil, errors.New("no PTR record")
		},
		Log: quietLogger(),
	})

	done := make(chan Peer, 1)
	start := time.Now()
	go func() { done <- r.Lookup(context.Background(), "172.22.1.5:47000") }()

	select {
	case peer := <-done:
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("the lookup took %v, want it bounded by the configured timeout", took)
		}
		// Still useful: the address it could not attribute comes back as itself,
		// which is what the log line needs.
		if peer.IP != "172.22.1.5" {
			t.Errorf("IP = %q, want the address to survive a failed attribution", peer.IP)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup ignored its timeout against a Docker daemon that never answers; " +
			"in production every failed TLS handshake would block its serving goroutine")
	}
}

// The same for DNS, which is the second source and the more likely one to hang:
// unbound-mailcow is a container the watchdog restarts, and inside that window
// the resolver accepts queries and answers none.
func TestLookupGivesUpOnAWedgedResolver(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	r := New(Options{
		Timeout: 200 * time.Millisecond,
		LookupAddr: func(ctx context.Context, _ string) ([]string, error) {
			select {
			case <-release:
				return nil, errors.New("released")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		Log: quietLogger(),
	})

	done := make(chan Peer, 1)
	start := time.Now()
	go func() { done <- r.Lookup(context.Background(), "172.22.1.5") }()

	select {
	case <-done:
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("the lookup took %v, want it bounded by the configured timeout", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup ignored its timeout against a resolver that never answers")
	}
}

// TestErrorLogWriteReturnsWhileLookupsHang closes the loop: the timeout above is
// only worth anything if Write, the function net/http actually calls, is bounded
// by it too.
func TestErrorLogWriteReturnsWhileLookupsHang(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	r := New(Options{
		Timeout: 200 * time.Millisecond,
		LookupAddr: func(ctx context.Context, _ string) ([]string, error) {
			select {
			case <-release:
				return nil, errors.New("released")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		Log: quietLogger(),
	})

	logger := r.HTTPErrorLog(quietLogger())

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Print, not Write: this is exactly how net/http emits the line.
		logger.Print("http: TLS handshake error from 172.22.1.5:47000: EOF")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked on a peer lookup; net/http calls it from the connection's " +
			"own goroutine, so the server would leak one per failed handshake")
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(discard{}, nil)) }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
