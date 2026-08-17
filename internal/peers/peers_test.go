package peers

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// mailcowStack is a listing shaped like the one a mailcow deployment produces:
// compose labels, the leading slash on the names, one network.
func mailcowStack() []dockerclient.Container {
	return []dockerclient.Container{
		{
			ID:     "aaa",
			Names:  []string{"/mailcowdockerized-watchdog-mailcow-1"},
			State:  "running",
			Labels: map[string]string{labelService: "watchdog-mailcow"},
			Endpoints: []dockerclient.Endpoint{
				{Network: "mailcowdockerized_mailcow-network", IPs: []string{"172.22.1.12", "fd4d:6169:6c63:6f77::12"}},
			},
		},
		{
			ID:     "bbb",
			Names:  []string{"/mailcowdockerized-dockerapi-mailcow-1"},
			State:  "running",
			Labels: map[string]string{labelService: "dockerapi-mailcow"},
			Endpoints: []dockerclient.Endpoint{
				{Network: "mailcowdockerized_mailcow-network", IPs: []string{"172.22.1.250"}},
			},
		},
	}
}

// testResolver builds a resolver with a pinned clock and no DNS fallback, so a
// test only sees what the container listing answered.
func testResolver(t *testing.T, fake *dockertest.Fake, now *time.Time) *Resolver {
	t.Helper()

	return New(Options{
		Docker: fake,
		Now:    func() time.Time { return *now },
		LookupAddr: func(_ context.Context, ip string) ([]string, error) {
			return nil, errors.New("no DNS in this test, asked for " + ip)
		},
		Log: slog.New(slog.DiscardHandler),
	})
}

func TestLookupNamesTheContainerBehindTheAddress(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}

	peer := testResolver(t, fake, &now).Lookup(context.Background(), "172.22.1.12:58798")

	if peer.Container != "mailcowdockerized-watchdog-mailcow-1" {
		t.Errorf("container = %q, want the watchdog without the leading slash", peer.Container)
	}
	if peer.Service != "watchdog-mailcow" {
		t.Errorf("service = %q, want watchdog-mailcow", peer.Service)
	}
	if peer.Network != "mailcowdockerized_mailcow-network" {
		t.Errorf("network = %q, want the mailcow network", peer.Network)
	}
	if peer.IP != "172.22.1.12" || peer.Port != "58798" {
		t.Errorf("address = %q:%q, want 172.22.1.12:58798", peer.IP, peer.Port)
	}
	if peer.Hostname != "" {
		t.Errorf("hostname = %q, want none — the listing answered, so DNS is not asked", peer.Hostname)
	}
	if !peer.Known() {
		t.Error("Known() = false for a resolved container")
	}
}

func TestLookupResolvesAnIPv6Address(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}

	peer := testResolver(t, fake, &now).Lookup(context.Background(), "[fd4d:6169:6c63:6f77::12]:58798")

	if peer.Container != "mailcowdockerized-watchdog-mailcow-1" {
		t.Errorf("container = %q, want the watchdog", peer.Container)
	}
	if peer.Port != "58798" {
		t.Errorf("port = %q, want 58798", peer.Port)
	}
}

// One Docker call per TTL, however many probes arrive in it — the point of the
// cache, given a peer that connects every few seconds.
func TestLookupCachesTheListing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}
	r := testResolver(t, fake, &now)

	r.Lookup(context.Background(), "172.22.1.12:1")
	r.Lookup(context.Background(), "172.22.1.12:2")
	// A second address of the same listing is answered from the cache as well.
	r.Lookup(context.Background(), "172.22.1.250:3")

	if got := len(fake.ListAllCalls); got != 1 {
		t.Errorf("ListAll calls = %d, want 1", got)
	}
}

func TestLookupKeepsThePortOutOfTheCache(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}
	r := testResolver(t, fake, &now)

	if got := r.Lookup(context.Background(), "172.22.1.12:58798").Port; got != "58798" {
		t.Fatalf("first port = %q, want 58798", got)
	}
	if got := r.Lookup(context.Background(), "172.22.1.12:58804").Port; got != "58804" {
		t.Errorf("second port = %q, want 58804 — the cached entry must not carry the first one", got)
	}
}

func TestLookupRefreshesAfterTheTTL(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}
	r := testResolver(t, fake, &now)

	r.Lookup(context.Background(), "172.22.1.12:1")

	// A recycled address must end up with its new owner rather than the one the
	// cache remembers.
	fake.Containers = []dockerclient.Container{{
		ID:     "ccc",
		Names:  []string{"/mailcowdockerized-netfilter-mailcow-1"},
		Labels: map[string]string{labelService: "netfilter-mailcow"},
		Endpoints: []dockerclient.Endpoint{
			{Network: "mailcowdockerized_mailcow-network", IPs: []string{"172.22.1.12"}},
		},
	}}

	now = now.Add(DefaultTTL - time.Second)
	if got := r.Lookup(context.Background(), "172.22.1.12:1").Service; got != "watchdog-mailcow" {
		t.Errorf("service before the TTL = %q, want the cached watchdog-mailcow", got)
	}

	now = now.Add(2 * time.Second)
	if got := r.Lookup(context.Background(), "172.22.1.12:1").Service; got != "netfilter-mailcow" {
		t.Errorf("service after the TTL = %q, want netfilter-mailcow", got)
	}
}

// A miss is cached too: an unattributable address is usually probed again
// seconds later, and re-listing every container each time is what the cache is
// there to prevent.
func TestLookupCachesAMiss(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}
	r := testResolver(t, fake, &now)

	first := r.Lookup(context.Background(), "10.0.0.1:1")
	r.Lookup(context.Background(), "10.0.0.1:2")

	if first.Known() {
		t.Errorf("Known() = true for %+v, want false", first)
	}
	if first.IP != "10.0.0.1" {
		t.Errorf("ip = %q, want the address to survive an unsuccessful lookup", first.IP)
	}
	if got := len(fake.ListAllCalls); got != 1 {
		t.Errorf("ListAll calls = %d, want 1 — the miss is cached as well", got)
	}
}

// The listing only knows the daemon's own containers. Everything else — a peer
// from another daemon, a container that had already gone — is left to DNS.
func TestLookupFallsBackToDNS(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}

	r := New(Options{
		Docker: fake,
		Now:    func() time.Time { return now },
		LookupAddr: func(_ context.Context, ip string) ([]string, error) {
			if ip != "172.22.1.99" {
				t.Errorf("LookupAddr(%q), want the unresolved address", ip)
			}
			return []string{"mailcowdockerized-sogo-mailcow-1.mailcowdockerized_mailcow-network."}, nil
		},
		Log: slog.New(slog.DiscardHandler),
	})

	peer := r.Lookup(context.Background(), "172.22.1.99:4711")

	if peer.Hostname != "mailcowdockerized-sogo-mailcow-1.mailcowdockerized_mailcow-network" {
		t.Errorf("hostname = %q, want the PTR name without its trailing dot", peer.Hostname)
	}
	if peer.Container != "" {
		t.Errorf("container = %q, want none — the listing did not know the address", peer.Container)
	}
	if !peer.Known() {
		t.Error("Known() = false although DNS answered")
	}
}

// A broken Docker socket must not cost the log line its address.
func TestLookupSurvivesADockerFailure(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{ListErr: errors.New("cannot connect to the docker daemon")}

	r := New(Options{
		Docker: fake,
		Now:    func() time.Time { return now },
		LookupAddr: func(_ context.Context, _ string) ([]string, error) {
			return []string{"watchdog."}, nil
		},
		Log: slog.New(slog.DiscardHandler),
	})

	peer := r.Lookup(context.Background(), "172.22.1.12:58798")

	if peer.IP != "172.22.1.12" || peer.Port != "58798" {
		t.Errorf("address = %q:%q, want it to survive the failure", peer.IP, peer.Port)
	}
	if peer.Hostname != "watchdog" {
		t.Errorf("hostname = %q, want the DNS answer", peer.Hostname)
	}
}

func TestLookupWithoutDocker(t *testing.T) {
	r := New(Options{
		LookupAddr: func(_ context.Context, _ string) ([]string, error) { return []string{"redis-mailcow."}, nil },
		Log:        slog.New(slog.DiscardHandler),
	})

	if got := r.Lookup(context.Background(), "172.22.1.249:6379").Hostname; got != "redis-mailcow" {
		t.Errorf("hostname = %q, want DNS to answer on its own", got)
	}
}

func TestLookupOfANonAddress(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: mailcowStack()}
	r := testResolver(t, fake, &now)

	for _, addr := range []string{"", "   ", "not-an-address", "example.org:443"} {
		peer := r.Lookup(context.Background(), addr)
		if peer != (Peer{}) {
			t.Errorf("Lookup(%q) = %+v, want the empty peer", addr, peer)
		}
	}

	if got := len(fake.ListAllCalls); got != 0 {
		t.Errorf("ListAll calls = %d, want 0 — there is nothing to look up", got)
	}
}

// A container without a network — one in the host's namespace, say — has no
// address of its own and must not make the mapping fall over.
func TestLookupIgnoresContainersWithoutEndpoints(t *testing.T) {
	now := time.Unix(1700000000, 0)
	fake := &dockertest.Fake{Containers: []dockerclient.Container{
		{ID: "aaa", Names: nil, State: "running"},
		{ID: "bbb", Names: []string{"/net-host"}, Endpoints: []dockerclient.Endpoint{{Network: "host"}}},
	}}

	if peer := testResolver(t, fake, &now).Lookup(context.Background(), "172.22.1.12:1"); peer.Known() {
		t.Errorf("Lookup = %+v, want an unresolved peer", peer)
	}
}

func TestAttrsLeaveOutWhatIsEmpty(t *testing.T) {
	peer := Peer{IP: "172.22.1.12", Port: "58798", Container: "watchdog-1", Service: "watchdog-mailcow"}

	want := []any{
		"peer_ip", "172.22.1.12",
		"peer_port", "58798",
		"peer_container", "watchdog-1",
		"peer_service", "watchdog-mailcow",
	}

	got := peer.Attrs()
	if len(got) != len(want) {
		t.Fatalf("Attrs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Attrs()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNewAppliesTheDefaults(t *testing.T) {
	r := New(Options{})

	if r.ttl != DefaultTTL {
		t.Errorf("ttl = %v, want %v", r.ttl, DefaultTTL)
	}
	if r.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", r.timeout, DefaultTimeout)
	}
	if r.lookupAddr == nil || r.now == nil || r.log == nil {
		t.Error("New left a dependency nil")
	}
}
