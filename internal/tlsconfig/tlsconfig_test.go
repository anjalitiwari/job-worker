package tlsconfig


import (
	"crypto/tls"
	"path/filepath"
	"testing"
	"time"

	"github.com/anjalitiwari/job-worker/internal/certgen"
)


func setupCerts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := certgen.Generate(dir, "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHandshake(t *testing.T) {
	d := setupCerts(t)
	s := mustServer(t, d)
	c := mustClient(t, d, "alice")

	if err := handshake(t, s, c); err != nil {
		t.Errorf("handshake: %v", err)
	}
}

func TestRejectsTLS12(t *testing.T) {
	d := setupCerts(t)
	s := mustServer(t, d)
	c := mustClient(t, d, "alice")
	c.MinVersion = tls.VersionTLS12
	c.MaxVersion = tls.VersionTLS12

	if err := handshake(t, s, c); err == nil {
		t.Fatal("expected failure with TLS 1.2 client")
	}
}

func TestRejectsMissingClientCert(t *testing.T) {
	d := setupCerts(t)
	s := mustServer(t, d)

	pool, err := loadCAPool(filepath.Join(d, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	c := &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: "localhost",
	}

	if err := handshake(t, s, c); err == nil {
		t.Fatal("expected failure with no client cert")
	}
}

func TestRejectsUnknownServer(t *testing.T) {
	d := setupCerts(t)
	// Server using a cert from a different CA.
	other := t.TempDir()
	if err := certgen.Generate(other); err != nil {
		t.Fatal(err)
	}
	wrong, err := tls.LoadX509KeyPair(filepath.Join(other, "server.crt"), filepath.Join(other, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	s := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{wrong},
	}

	c := mustClient(t, d, "alice")
	if err := handshake(t, s, c); err == nil {
		t.Fatal("expected failure — server cert not signed by our CA")
	}
}

func TestMissingFiles(t *testing.T) {
	if _, err := Server("x.crt", "x.key", "x.ca"); err == nil {
		t.Error("server: expected error")
	}
	if _, err := Client("x.crt", "x.key", "x.ca", "localhost"); err == nil {
		t.Error("client: expected error")
	}
}

func mustServer(t *testing.T, d string) *tls.Config {
	t.Helper()
	cfg, err := Server(filepath.Join(d, "server.crt"), filepath.Join(d, "server.key"), filepath.Join(d, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func mustClient(t *testing.T, d, name string) *tls.Config {
	t.Helper()
	cfg, err := Client(filepath.Join(d, name+".crt"), filepath.Join(d, name+".key"), filepath.Join(d, "ca.crt"), "localhost")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// handshake runs a one-shot TLS server/client against each other and
// returns any error from either side.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) error {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srvErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			srvErr <- err
			return
		}
		defer conn.Close()
		srvErr <- conn.(*tls.Conn).Handshake()
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Handshake(); err != nil {
		return err
	}

	select {
	case err := <-srvErr:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
		return nil
	}
}


