// Package certgen generates a local PKI for the job-worker service.
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

const validFor = 10 * 365 * 24 * time.Hour

// Generate writes a CA, server cert, and one client cert per name
// into dir. Files are {name}.crt and {name}.key.
func Generate(dir string, clientNames ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	ca, caKey, err := NewCA()
	if err != nil {
		return fmt.Errorf("ca: %w", err)
	}
	if err := write(dir, "ca", ca, caKey); err != nil {
		return err
	}

	server, serverKey, err := NewServerCert(ca, caKey, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := write(dir, "server", server, serverKey); err != nil {
		return err
	}

	for _, name := range clientNames {
		c, k, err := NewClientCert(ca, caKey, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := write(dir, name, c, k); err != nil {
			return err
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
	return sign(tmpl, tmpl, key, key)
}

func NewServerCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, dnsNames []string, ips []net.IP) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if ca == nil || caKey == nil {
		return nil, nil, errors.New("certgen: ca required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "server"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	return sign(tmpl, ca, key, caKey)
}

func NewClientCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if ca == nil || caKey == nil {
		return nil, nil, errors.New("certgen: ca required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return sign(tmpl, ca, key, caKey)
}

// sign creates the cert from tmpl against parent, signed with parentKey,
// returning the parsed cert and the new leaf's key.
func sign(tmpl, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func write(dir, name string, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	if err := writePEM(filepath.Join(dir, name+".crt"), "CERTIFICATE", cert.Raw, 0o644); err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(filepath.Join(dir, name+".key"), "EC PRIVATE KEY", kb, 0o600)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
