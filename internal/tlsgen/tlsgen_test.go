package tlsgen

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testOpts hält den Test schnell; die Länge ist für die geprüften
// Eigenschaften unerheblich.
var testOpts = Options{KeyBits: 2048}

func parseCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("kein CERTIFICATE-Block: %q", certPEM)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert
}

// Prüft die Eigenschaften, die docker-entrypoint.sh per openssl gesetzt hat.
func TestGenerateMatchesEntrypoint(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	certPEM, keyPEM, err := Generate(Options{KeyBits: 2048, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cert := parseCert(t, certPEM)

	if cert.Subject.CommonName != CommonName {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, CommonName)
	}
	if len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != Organization {
		t.Errorf("O = %v, want [%s]", cert.Subject.Organization, Organization)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != DNSName {
		t.Errorf("SAN = %v, want [%s]", cert.DNSNames, DNSName)
	}
	if cert.SignatureAlgorithm != x509.SHA256WithRSA {
		t.Errorf("SignatureAlgorithm = %v, want SHA256WithRSA", cert.SignatureAlgorithm)
	}

	// -days 3650
	wantNotAfter := now.Add(Validity)
	if !cert.NotAfter.Equal(wantNotAfter) {
		t.Errorf("NotAfter = %v, want %v", cert.NotAfter, wantNotAfter)
	}
	if cert.NotBefore.After(now) {
		t.Errorf("NotBefore = %v liegt nach now = %v", cert.NotBefore, now)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("kein PRIVATE KEY-Block")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		t.Errorf("ParsePKCS8PrivateKey: %v", err)
	}
}

func TestGenerateUsesFreshSerial(t *testing.T) {
	a, _, err := Generate(testOpts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, _, err := Generate(testOpts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if parseCert(t, a).SerialNumber.Cmp(parseCert(t, b).SerialNumber) == 0 {
		t.Error("zwei Zertifikate teilen dieselbe Seriennummer")
	}
}

func TestEnsureCreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	cert, err := Ensure(certFile, keyFile, testOpts)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("leeres Zertifikat")
	}

	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("Schluessel nicht geschrieben: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("Schluesselrechte = %o, want 600", perm)
	}
	if _, err := os.Stat(certFile); err != nil {
		t.Fatalf("Zertifikat nicht geschrieben: %v", err)
	}
}

func TestEnsureReusesExistingPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	first, err := Ensure(certFile, keyFile, testOpts)
	if err != nil {
		t.Fatalf("Ensure (1): %v", err)
	}
	second, err := Ensure(certFile, keyFile, testOpts)
	if err != nil {
		t.Fatalf("Ensure (2): %v", err)
	}

	if string(first.Certificate[0]) != string(second.Certificate[0]) {
		t.Error("Ensure hat ein vorhandenes Paar nicht wiederverwendet")
	}
}

func TestEnsureReplacesCorruptPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certFile, []byte("kaputt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}

	cert, err := Ensure(certFile, keyFile, testOpts)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("leeres Zertifikat")
	}
}

// Ohne beschreibbare Pfade muss der Dienst trotzdem starten können.
func TestEnsureWorksWithoutWritablePaths(t *testing.T) {
	cert, err := Ensure("/nicht/existent/cert.pem", "/nicht/existent/key.pem", testOpts)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("leeres Zertifikat")
	}
}
