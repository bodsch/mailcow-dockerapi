package peers

import (
	"context"
	"log"
	"log/slog"
	"strings"
)

// handshakePrefix is how net/http reports a connection that never became a TLS
// session — the only thing it reports about it, and the reason this file parses
// text: the handshake fails before there is a request, a handler or a ConnState
// callback carrying the error, so http.Server.ErrorLog is the single hook there is.
const handshakePrefix = "http: TLS handshake error from "

// HTTPErrorLog returns a logger for http.Server.ErrorLog that names the container
// behind the addresses in the server's error lines.
//
// Without it those lines take the default route: log.Default(), which
// slog.SetDefault points at the slog handler, writes each of them as one info
// line whose whole message is the text, address included. What an operator then
// has is "http: TLS handshake error from 172.22.1.12:58798: EOF" — an address that
// belongs to a different container after every restart, in a log they cannot
// filter by peer because the peer is not a field.
//
// The logger it returns writes the same information as fields, with the container
// name alongside. logger may be nil, in which case the resolver's own logger
// applies.
func (r *Resolver) HTTPErrorLog(logger *slog.Logger) *log.Logger {
	if logger == nil {
		logger = r.log
	}

	// No prefix and no flags: the timestamp and the source are the handler's job.
	return log.New(&errorWriter{resolver: r, log: logger.With("component", "http")}, "", 0)
}

// errorWriter turns one line from net/http into one structured record.
type errorWriter struct {
	resolver *Resolver
	log      *slog.Logger
}

// Write never reports an error: net/http's logger has nowhere to put one, and a
// short write would only make it repeat the line.
func (w *errorWriter) Write(p []byte) (int, error) {
	w.report(strings.TrimSpace(string(p)))
	return len(p), nil
}

// report classifies a line and logs it.
func (w *errorWriter) report(line string) {
	if line == "" {
		return
	}

	addr, reason, ok := cutHandshake(line)
	if !ok {
		// Everything else the server reports — a panic in a handler, a duplicate
		// WriteHeader — keeps the text it had. There is no address in it to
		// resolve, and inventing structure for lines whose shape may change with
		// the standard library would be worse than passing them through.
		w.log.Warn(line)
		return
	}

	// The context is the writer's own: net/http hands the line over without one,
	// and the lookup carries its own deadline.
	peer := w.resolver.Lookup(context.Background(), addr)

	attrs := []any{"err", reason}
	if peer.IP == "" {
		// The address was not of the ip:port shape the lookup expects. Log it
		// verbatim rather than dropping the only identification there is.
		attrs = append(attrs, "peer_addr", addr)
	}
	attrs = append(attrs, peer.Attrs()...)

	if isProbe(reason) {
		// A handshake that ends before the client sent anything is a port check:
		// a container healthcheck or a watchdog asking whether :443 is open, not
		// a client that failed to reach the service. Those repeat every few
		// seconds for as long as the stack runs, so they are debug — the reason
		// they used to fill the log at info level.
		w.log.Debug("a peer connected and closed without starting a TLS handshake", attrs...)
		return
	}

	w.log.Warn("the TLS handshake failed", attrs...)
}

// cutHandshake splits "http: TLS handshake error from 172.22.1.12:58798: EOF"
// into the address and the reason.
func cutHandshake(line string) (addr, reason string, ok bool) {
	_, rest, found := strings.Cut(line, handshakePrefix)
	if !found {
		return "", "", false
	}

	// The separator is ": " with the space: it cannot be confused with the colons
	// of the port or of an IPv6 address, which net/http writes in brackets.
	addr, reason, found = strings.Cut(rest, ": ")
	if !found {
		return rest, "", true
	}

	return addr, reason, true
}

// isProbe reports whether the handshake failed because the peer closed the
// connection instead of speaking TLS.
//
// The reasons are matched as text because that is how they arrive; net/http has
// already turned the error into a string by the time it reaches the logger.
func isProbe(reason string) bool {
	switch {
	case reason == "EOF",
		strings.HasSuffix(reason, "connection reset by peer"),
		strings.HasSuffix(reason, "broken pipe"):
		return true
	default:
		return false
	}
}
