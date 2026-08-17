package peers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"bodsch.me/mailcow-dockerapi/internal/dockerclient/dockertest"
)

// record is one decoded log line.
type record struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	Err       string `json:"err"`
	Component string `json:"component"`
	IP        string `json:"peer_ip"`
	Port      string `json:"peer_port"`
	Addr      string `json:"peer_addr"`
	Container string `json:"peer_container"`
	Service   string `json:"peer_service"`
	Network   string `json:"peer_network"`
	Hostname  string `json:"peer_hostname"`
}

// errorLog wires a resolver over the mailcow-shaped listing to a JSON logger, the
// way main does, and hands back the buffer the lines land in.
func errorLog(t *testing.T) (*bytes.Buffer, *log.Logger) {
	t.Helper()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	now := time.Unix(1700000000, 0)
	r := testResolver(t, &dockertest.Fake{Containers: mailcowStack()}, &now)

	return buf, r.HTTPErrorLog(logger)
}

// decode reads the single line the buffer holds.
func decode(t *testing.T, buf *bytes.Buffer) record {
	t.Helper()

	var rec record
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("cannot decode %q: %v", buf.String(), err)
	}
	return rec
}

// The line this whole package exists for: net/http's own text, as fields, with
// the container named.
func TestTheHandshakeErrorNamesThePeer(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("http: TLS handshake error from 172.22.1.12:58798: EOF")

	rec := decode(t, buf)
	if rec.Container != "mailcowdockerized-watchdog-mailcow-1" {
		t.Errorf("peer_container = %q, want the watchdog", rec.Container)
	}
	if rec.Service != "watchdog-mailcow" {
		t.Errorf("peer_service = %q, want watchdog-mailcow", rec.Service)
	}
	if rec.Network != "mailcowdockerized_mailcow-network" {
		t.Errorf("peer_network = %q, want the mailcow network", rec.Network)
	}
	if rec.IP != "172.22.1.12" || rec.Port != "58798" {
		t.Errorf("peer address = %q:%q, want 172.22.1.12:58798", rec.IP, rec.Port)
	}
	if rec.Err != "EOF" {
		t.Errorf("err = %q, want EOF", rec.Err)
	}
	if rec.Component != "http" {
		t.Errorf("component = %q, want http", rec.Component)
	}
}

// A peer that connects and hangs up is a port check, not a failed client, and
// repeats for as long as the stack runs — hence debug rather than warn.
func TestAProbeIsDebug(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		level string
	}{
		{
			name:  "EOF",
			line:  "http: TLS handshake error from 172.22.1.12:58798: EOF",
			level: "DEBUG",
		},
		{
			name:  "a reset connection",
			line:  "http: TLS handshake error from 172.22.1.12:58798: read tcp 172.22.1.250:443->172.22.1.12:58798: read: connection reset by peer",
			level: "DEBUG",
		},
		{
			name:  "a client that did speak TLS",
			line:  "http: TLS handshake error from 172.22.1.12:58798: tls: client offered only unsupported versions: [301]",
			level: "WARN",
		},
		{
			name:  "a timeout",
			line:  "http: TLS handshake error from 172.22.1.12:58798: read tcp 172.22.1.250:443->172.22.1.12:58798: i/o timeout",
			level: "WARN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, errLog := errorLog(t)

			errLog.Print(tt.line)

			rec := decode(t, buf)
			if rec.Level != tt.level {
				t.Errorf("level = %q, want %q for %q", rec.Level, tt.level, tt.line)
			}
			if rec.Container != "mailcowdockerized-watchdog-mailcow-1" {
				t.Errorf("peer_container = %q, want the peer resolved either way", rec.Container)
			}
		})
	}
}

// Whatever else the standard library reports keeps its text: there is no address
// in it to resolve, and its shape is not this package's to depend on.
func TestOtherLinesArePassedThrough(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("http: superfluous response.WriteHeader call from ...")

	rec := decode(t, buf)
	if rec.Level != "WARN" {
		t.Errorf("level = %q, want WARN", rec.Level)
	}
	if rec.Msg != "http: superfluous response.WriteHeader call from ..." {
		t.Errorf("msg = %q, want the line unchanged", rec.Msg)
	}
}

func TestAnEmptyLineIsDropped(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("\n")

	if buf.Len() != 0 {
		t.Errorf("logged %q, want nothing", buf.String())
	}
}

// An address that is not ip:port still has to reach the operator.
func TestAnUnparsableAddressIsLoggedVerbatim(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("http: TLS handshake error from /var/run/some.sock: EOF")

	rec := decode(t, buf)
	if rec.Addr != "/var/run/some.sock" {
		t.Errorf("peer_addr = %q, want the address as it arrived", rec.Addr)
	}
	if rec.IP != "" {
		t.Errorf("peer_ip = %q, want none", rec.IP)
	}
	if rec.Err != "EOF" {
		t.Errorf("err = %q, want EOF", rec.Err)
	}
}

// net/http writes an IPv6 peer in brackets; the colons of the address must not be
// mistaken for the separator before the reason.
func TestAnIPv6PeerIsSplitCorrectly(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("http: TLS handshake error from [fd4d:6169:6c63:6f77::12]:58798: EOF")

	rec := decode(t, buf)
	if rec.IP != "fd4d:6169:6c63:6f77::12" || rec.Port != "58798" {
		t.Errorf("peer address = %q:%q, want the IPv6 address and its port", rec.IP, rec.Port)
	}
	if rec.Err != "EOF" {
		t.Errorf("err = %q, want EOF", rec.Err)
	}
	if rec.Container != "mailcowdockerized-watchdog-mailcow-1" {
		t.Errorf("peer_container = %q, want the watchdog", rec.Container)
	}
}

// A handshake line without a reason is not one the standard library writes today,
// but the address is the part worth keeping if it ever does.
func TestAHandshakeLineWithoutAReason(t *testing.T) {
	buf, errLog := errorLog(t)

	errLog.Print("http: TLS handshake error from 172.22.1.12:58798")

	rec := decode(t, buf)
	if rec.IP != "172.22.1.12" {
		t.Errorf("peer_ip = %q, want the address", rec.IP)
	}
	if rec.Level != "WARN" {
		t.Errorf("level = %q, want WARN — an unknown reason is not a probe", rec.Level)
	}
}

// The resolver's own logger applies when none is passed in.
func TestHTTPErrorLogWithoutALogger(t *testing.T) {
	buf := &bytes.Buffer{}
	r := New(Options{
		Docker: &dockertest.Fake{Containers: mailcowStack()},
		Log:    slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	r.HTTPErrorLog(nil).Print("http: TLS handshake error from 172.22.1.12:58798: EOF")

	if rec := decode(t, buf); rec.Container != "mailcowdockerized-watchdog-mailcow-1" {
		t.Errorf("peer_container = %q, want the watchdog", rec.Container)
	}
}

// syncBuffer collects log lines written from the server's handshake goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// The parsing above assumes a line shape net/http produces but does not promise.
// This test drives a real server the way a port check does — connect, close, send
// nothing — and asserts that the line comes out as fields.
func TestARealFailedHandshakeIsLoggedWithItsPeer(t *testing.T) {
	out := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	now := time.Unix(1700000000, 0)
	r := testResolver(t, &dockertest.Fake{Containers: mailcowStack()}, &now)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: time.Second,
		ErrorLog:          r.HTTPErrorLog(logger),
	}
	t.Cleanup(func() { _ = srv.Close() })

	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{testCertificate(t)},
		MinVersion:   tls.VersionTLS12,
	}

	go func() { _ = srv.ServeTLS(ln, "", "") }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	// The line is written from the server's own goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "peer_ip") {
		if time.Now().After(deadline) {
			t.Fatalf("no peer-resolved line within the deadline, got %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	line := out.String()
	for _, want := range []string{
		`"level":"DEBUG"`,
		`"msg":"a peer connected and closed without starting a TLS handshake"`,
		`"err":"EOF"`,
		`"peer_ip":"127.0.0.1"`,
		`"peer_port":`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the line %q does not contain %s", line, want)
		}
	}
}

// testCertificate is a throwaway pair. Which one it is does not matter: the peer
// of the test closes the connection before a certificate would be sent.
func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("cannot generate a key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dockerapi"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cannot create the certificate: %v", err)
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
