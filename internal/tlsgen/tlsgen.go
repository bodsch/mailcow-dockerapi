// Package tlsgen erzeugt das selbstsignierte Serverzertifikat, das in der
// Python-Implementierung von docker-entrypoint.sh per openssl angelegt wurde:
//
//	openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
//	  -subj /CN=dockerapi/O=mailcow -addext subjectAltName=DNS:dockerapi
//
// Damit entfallen Entrypoint-Skript und openssl im Image.
package tlsgen

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

// Vorgaben aus docker-entrypoint.sh.
const (
	CommonName   = "dockerapi"
	Organization = "mailcow"
	DNSName      = "dockerapi"
	Validity     = 3650 * 24 * time.Hour
	KeyBits      = 4096
)

// Options steuert die Zertifikatserzeugung. Der Nullwert entspricht dem
// Verhalten des Entrypoint-Skripts.
type Options struct {
	// KeyBits ist die RSA-Schlüssellänge; 0 bedeutet KeyBits (4096).
	KeyBits int
	// Now erlaubt es Tests, die Gültigkeitsspanne zu kontrollieren.
	Now func() time.Time
}

func (o Options) bits() int {
	if o.KeyBits == 0 {
		return KeyBits
	}
	return o.KeyBits
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// Generate erzeugt ein frisches selbstsigniertes Zertifikat samt Schlüssel
// und gibt beide PEM-kodiert zurück.
func Generate(opts Options) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, opts.bits())
	if err != nil {
		return nil, nil, fmt.Errorf("generate rsa key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	now := opts.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   CommonName,
			Organization: []string{Organization},
		},
		DNSNames:              []string{DNSName},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustMarshalKey(key),
	})

	return certPEM, keyPEM, nil
}

// Ensure lädt das Zertifikatspaar von certFile/keyFile. Fehlt es oder ist es
// unlesbar, wird ein neues erzeugt und – sofern die Pfade beschreibbar sind –
// abgelegt. Ein fehlgeschlagener Schreibversuch ist kein Fehler: der Dienst
// läuft dann mit dem Zertifikat im Speicher weiter.
func Ensure(certFile, keyFile string, opts Options) (tls.Certificate, error) {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err == nil {
			return cert, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Beschädigtes Material wird verworfen und neu erzeugt.
			_ = err
		}
	}

	certPEM, keyPEM, err := Generate(opts)
	if err != nil {
		return tls.Certificate{}, err
	}

	if certFile != "" && keyFile != "" {
		writePair(certFile, certPEM, keyFile, keyPEM)
	}

	return tls.X509KeyPair(certPEM, keyPEM)
}

func writePair(certFile string, certPEM []byte, keyFile string, keyPEM []byte) {
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return
	}
	_ = os.WriteFile(certFile, certPEM, 0o644)
}

func mustMarshalKey(key *rsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		// Bei einem gültigen RSA-Schlüssel ist das unerreichbar.
		panic(err)
	}
	return der
}
