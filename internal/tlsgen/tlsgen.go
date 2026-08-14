// Package tlsgen creates the self-signed server certificate that
// docker-entrypoint.sh produced with openssl in the Python implementation:
//
//	openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
//	  -subj /CN=dockerapi/O=mailcow -addext subjectAltName=DNS:dockerapi
//
// That removes both the entrypoint script and openssl from the image.
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

// The values from docker-entrypoint.sh.
const (
	CommonName   = "dockerapi"
	Organization = "mailcow"
	DNSName      = "dockerapi"
	Validity     = 3650 * 24 * time.Hour
	KeyBits      = 4096
)

// Options controls certificate generation. The zero value matches the behaviour of
// the entrypoint script.
type Options struct {
	// KeyBits is the RSA key length; 0 means KeyBits (4096).
	KeyBits int
	// Now lets tests control the validity window.
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

// Generate creates a fresh self-signed certificate together with its key and
// returns both PEM-encoded.
func Generate(opts Options) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, opts.bits())
	if err != nil {
		return nil, nil, fmt.Errorf("generating the rsa key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generating the serial number: %w", err)
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
		return nil, nil, fmt.Errorf("creating the certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustMarshalKey(key),
	})

	return certPEM, keyPEM, nil
}

// Ensure loads the certificate pair from certFile/keyFile. When it is missing or
// unreadable, a new one is generated and — where the paths are writable — stored. A
// failed write is not an error: the service then carries on with the certificate in
// memory.
func Ensure(certFile, keyFile string, opts Options) (tls.Certificate, error) {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err == nil {
			return cert, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Damaged material is discarded and generated anew.
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
	// G306: the certificate is public material — openssl wrote it world-readable
	// in the entrypoint script too. Only the key above is restricted.
	_ = os.WriteFile(certFile, certPEM, 0o644) //nolint:gosec
}

func mustMarshalKey(key *rsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		// Unreachable for a valid RSA key.
		panic(err)
	}
	return der
}
