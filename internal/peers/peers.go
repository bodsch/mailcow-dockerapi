// Package peers names the container behind a remote address.
//
// Every client of this service talks to it over the mailcow network, so a log
// line carrying nothing but 172.22.1.12:58798 tells an operator nothing they can
// act on — least of all which of the two dozen containers of a mailcow stack it
// was. Each address in a Docker network belongs to a container, and
// GET /containers/json already says which; this package caches that mapping and
// turns an address into the container's name, its compose service and the network
// it came in on.
//
// The lookup has a second source: the resolver behind /etc/resolv.conf, which
// inside a Docker network is the daemon's embedded DNS at 127.0.0.11. It answers
// PTR records for container addresses, so it covers the cases the container
// listing does not — an address from another daemon, or one the listing missed
// because the container had already gone.
package peers

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient"
)

// labelService is the label compose puts the service name into. mailcow's
// services are named watchdog-mailcow, netfilter-mailcow and so on, which is the
// name an operator recognises from the compose file.
const labelService = "com.docker.compose.service"

const (
	// DefaultTTL is how long a resolved address is reused. It is short enough for
	// a recycled address to be attributed to its new container within a minute,
	// and long enough that a peer probing every few seconds costs one Docker call
	// per TTL rather than one per probe.
	DefaultTTL = 30 * time.Second

	// DefaultTimeout bounds a single lookup. It runs while a connection is being
	// logged, so it must not be able to hold that up for long.
	DefaultTimeout = 2 * time.Second
)

// Lister is the part of the Docker API this package uses.
type Lister interface {
	ListAll(ctx context.Context, all bool) ([]dockerclient.Container, error)
}

// Options configures the Resolver.
type Options struct {
	// Docker is the container listing. A nil Docker leaves DNS as the only
	// source.
	Docker Lister
	// TTL is how long an entry is reused. Zero means DefaultTTL.
	TTL time.Duration
	// Timeout bounds one lookup. Zero means DefaultTimeout.
	Timeout time.Duration
	// LookupAddr resolves an address to its names. Nil means
	// net.DefaultResolver.LookupAddr.
	LookupAddr func(ctx context.Context, ip string) ([]string, error)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	Log *slog.Logger
}

// Peer describes who is at the other end of a connection. Everything but the
// address is best effort — an unattributable address still yields IP and Port.
type Peer struct {
	IP   string
	Port string
	// Container is the container's name without the leading slash Docker adds.
	Container string
	// Service is the compose service the container belongs to, empty for a
	// container compose did not start.
	Service string
	// Network is the Docker network the address is on.
	Network string
	// Hostname is the reverse DNS name, set only when the container listing did
	// not know the address.
	Hostname string
}

// Known reports whether anything beyond the bare address could be resolved.
func (p Peer) Known() bool { return p.Container != "" || p.Hostname != "" }

// Attrs returns the peer as log attributes, leaving out what is empty. The keys
// are prefixed peer_ so they do not collide with a service's own fields.
func (p Peer) Attrs() []any {
	attrs := make([]any, 0, 12)

	for _, kv := range [][2]string{
		{"peer_ip", p.IP},
		{"peer_port", p.Port},
		{"peer_container", p.Container},
		{"peer_service", p.Service},
		{"peer_network", p.Network},
		{"peer_hostname", p.Hostname},
	} {
		if kv[1] != "" {
			attrs = append(attrs, kv[0], kv[1])
		}
	}

	return attrs
}

// Resolver answers who an address belongs to, from a cache backed by the Docker
// container listing. It is safe for concurrent use.
type Resolver struct {
	docker     Lister
	ttl        time.Duration
	timeout    time.Duration
	lookupAddr func(ctx context.Context, ip string) ([]string, error)
	now        func() time.Time
	log        *slog.Logger

	mu      sync.Mutex
	entries map[string]entry
}

// entry is one cached address. A miss is cached too: an address nothing knows
// about is usually probed again seconds later, and re-listing every container for
// each of those probes is what the cache exists to avoid.
type entry struct {
	peer Peer
	at   time.Time
}

// New builds a Resolver. It performs no I/O; the first lookup fills the cache.
func New(opts Options) *Resolver {
	r := &Resolver{
		docker:     opts.Docker,
		ttl:        opts.TTL,
		timeout:    opts.Timeout,
		lookupAddr: opts.LookupAddr,
		now:        opts.Now,
		log:        opts.Log,
		entries:    make(map[string]entry),
	}

	if r.ttl <= 0 {
		r.ttl = DefaultTTL
	}
	if r.timeout <= 0 {
		r.timeout = DefaultTimeout
	}
	if r.lookupAddr == nil {
		r.lookupAddr = net.DefaultResolver.LookupAddr
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	r.log = r.log.With("component", "peers")

	return r
}

// Lookup resolves an address of the form ip:port, as net.Conn.RemoteAddr and the
// server's error lines spell it.
//
// It does not fail: an address it cannot attribute comes back with only IP and
// Port filled in, which is still what the caller had. Failures of the sources
// themselves are logged at debug level — a peer lookup exists to explain another
// log line and must not turn into one of its own.
func (r *Resolver) Lookup(ctx context.Context, addr string) Peer {
	ip, port := splitAddr(addr)
	if ip == "" {
		return Peer{}
	}

	peer, ok := r.cached(ip)
	if !ok {
		peer = r.resolve(ctx, ip)
		r.store(ip, peer)
	}

	// The port belongs to the connection, not to the peer, so it is filled in
	// after the cache rather than stored in it.
	peer.Port = port

	return peer
}

// cached returns the entry for ip while it is still fresh.
func (r *Resolver) cached(ip string) (Peer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[ip]
	if !ok || r.now().Sub(e.at) >= r.ttl {
		return Peer{}, false
	}

	return e.peer, true
}

// store caches one address.
func (r *Resolver) store(ip string, peer Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[ip] = entry{peer: peer, at: r.now()}
}

// resolve asks the container listing first and DNS second.
func (r *Resolver) resolve(ctx context.Context, ip string) Peer {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if r.docker != nil {
		if found, err := r.fromDocker(ctx); err != nil {
			r.log.Debug("cannot list the containers for the peer lookup", "peer_ip", ip, "err", err)
		} else {
			// The whole listing is cached, not just the address asked for: the
			// next peer is then answered without another Docker call.
			for addr, peer := range found {
				r.store(addr, peer)
			}
			if peer, ok := found[ip]; ok {
				return peer
			}
		}
	}

	return r.fromDNS(ctx, ip)
}

// fromDocker builds the address to container mapping from the container listing.
func (r *Resolver) fromDocker(ctx context.Context) (map[string]Peer, error) {
	// Stopped containers hold no addresses, so the listing stays narrow.
	list, err := r.docker.ListAll(ctx, false)
	if err != nil {
		return nil, err
	}

	out := make(map[string]Peer, len(list))
	for _, c := range list {
		name := containerName(c)
		for _, ep := range c.Endpoints {
			for _, ip := range ep.IPs {
				out[ip] = Peer{
					IP:        ip,
					Container: name,
					Service:   c.Labels[labelService],
					Network:   ep.Network,
				}
			}
		}
	}

	return out, nil
}

// fromDNS asks the resolver for the address's PTR record.
func (r *Resolver) fromDNS(ctx context.Context, ip string) Peer {
	names, err := r.lookupAddr(ctx, ip)
	if err != nil {
		r.log.Debug("cannot resolve the peer address", "peer_ip", ip, "err", err)
		return Peer{IP: ip}
	}
	if len(names) == 0 {
		return Peer{IP: ip}
	}

	// The embedded DNS answers a container's name; the trailing dot of the FQDN
	// is noise in a log line.
	return Peer{IP: ip, Hostname: strings.TrimSuffix(names[0], ".")}
}

// containerName is the first of a container's names without the leading slash the
// Docker API prefixes them with.
func containerName(c dockerclient.Container) string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// splitAddr separates an ip:port address. An address without a port — or with
// nothing that parses as one — is treated as a bare address rather than being
// dropped, because a caller's log line is worth writing either way.
func splitAddr(addr string) (ip, port string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", ""
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, ""
	}
	if net.ParseIP(host) == nil {
		return "", ""
	}

	return host, port
}
