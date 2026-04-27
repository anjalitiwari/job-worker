package certgen

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, "alice", "bob"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"ca", "server", "alice", "bob"} {
		for _, ext := range []string{".crt", ".key"} {
			if _, err := os.Stat(filepath.Join(dir, name+ext)); err != nil {
				t.Errorf("missing %s%s: %v", name, ext, err)
			}
		}
	}
}

func TestCertsChain(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, "alice"); err != nil {
		t.Fatal(err)
	}

	ca := readCert(t, filepath.Join(dir, "ca.crt"))
	server := readCert(t, filepath.Join(dir, "server.crt"))
	client := readCert(t, filepath.Join(dir, "alice.crt"))

	pool := x509.NewCertPool()
	pool.AddCert(ca)

	if _, err := server.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "localhost",
	}); err != nil {
		t.Errorf("server verify: %v", err)
	}

	if _, err := client.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("client verify: %v", err)
	}
}

func TestServerSANs(t *testing.T) {
	ca, caKey, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := NewServerCert(ca, caKey, []string{"foo.example"}, []net.IP{net.ParseIP("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DNSNames) != 1 || s.DNSNames[0] != "foo.example" {
		t.Errorf("DNSNames = %v", s.DNSNames)
	}
	if len(s.IPAddresses) != 1 || !s.IPAddresses[0].Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("IPAddresses = %v", s.IPAddresses)
	}
}

func TestClientCN(t *testing.T) {
	ca, caKey, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob", "someone-else"} {
		c, _, err := NewClientCert(ca, caKey, name)
		if err != nil {
			t.Fatal(err)
		}
		if c.Subject.CommonName != name {
			t.Errorf("CN = %q, want %q", c.Subject.CommonName, name)
		}
	}
}

func TestNilCA(t *testing.T) {
	if _, _, err := NewServerCert(nil, nil, nil, nil); err == nil {
		t.Error("server: expected error")
	}
	if _, _, err := NewClientCert(nil, nil, "x"); err == nil {
		t.Error("client: expected error")
	}
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatalf("no PEM in %s", path)
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}