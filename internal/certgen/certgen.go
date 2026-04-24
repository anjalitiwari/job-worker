package certgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const validFor = 10 * 365 * 24 * time.Hour // 10 years

func GenerateCerts(dir string, clientNames []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	caCert, caKey, err := NewCA()
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	if err := WritePair(dir, "ca", caCert, caKey); err != nil {
		return err
	}

	for _, name := range clientNames {
		cert, key, err := NewClientCert(caCert, caKey, name)
		if err != nil {
			return fmt.Errorf("generate client cert for %s: %w", name, err)
		}
		if err := WritePair(dir, name, cert, key); err != nil {
			return fmt.Errorf("write client cert for %s: %w", name, err)
		}
	}

	return nil
}

func NewCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)		

	if err != nil {
		return nil, nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "job-worker CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// NewServerCert returns a server cert signed by caCert/caKey, with the
// given DNS names and IP addresses in the SAN.
func NewServerCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, dnsNames []string, ips []net.IP) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	return newLeaf(caCert, caKey, &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "server"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	})
}

// NewClientCert returns a client cert with cn as the Common Name,
// signed by caCert/caKey. The CN is the identity the auth interceptor
// will extract.
func NewClientCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	return newLeaf(caCert, caKey, &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
}

// WritePair writes cert and key to {outDir}/{name}.crt and {name}.key
func WritePair(dir, name string, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	certPath := filepath.Join(outDir, name+".crt")
	if err := writePEM(certPath, "CERTIFICATE", cert.Raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", certPath, err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(outDir, name+".key")
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyBytes, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", keyPath, err)
	}
	return nil
}

func newLeaf(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, tmpl *x509.Certificate) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if caCert == nil || caKey == nil {
		return nil, nil, errors.New("certgen: ca cert and key are required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func serial() *big.Int {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		// rand.Int only fails if rand.Reader fails, which is an
		// environment-level problem — callers can't recover.
		panic(fmt.Sprintf("certgen: rand.Int: %v", err))
	}
	return n
}
