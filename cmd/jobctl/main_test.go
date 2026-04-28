package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"github.com/anjalitiwari/job-worker/internal/auth"
	"github.com/anjalitiwari/job-worker/internal/certgen"
	"github.com/anjalitiwari/job-worker/internal/server"
	"github.com/anjalitiwari/job-worker/internal/tlsconfig"
	pb "github.com/anjalitiwari/job-worker/proto"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jobctl-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binPath = filepath.Join(dir, "jobctl")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build: " + err.Error())
	}

	os.Exit(m.Run())
}

func startServer(t *testing.T) (addr, certDir string) {
	t.Helper()
	certDir = t.TempDir()
	if err := certgen.Generate(certDir, "alice", "bob"); err != nil {
		t.Fatal(err)
	}

	tlsCfg, err := tlsconfig.Server(
		filepath.Join(certDir, "server.crt"),
		filepath.Join(certDir, "server.key"),
		filepath.Join(certDir, "ca.crt"),
	)
	if err != nil {
		t.Fatal(err)
	}

	idMap := auth.IdentityMap{"alice": auth.RoleAdmin, "bob": auth.RoleViewer}

	gs := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.UnaryInterceptor(auth.UnaryInterceptor(idMap)),
		grpc.StreamInterceptor(auth.StreamInterceptor(idMap)),
	)
	pb.RegisterJobWorkerServer(gs, server.New())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(ln) }()
	t.Cleanup(func() { gs.Stop() })

	return ln.Addr().String(), certDir
}

func runCLI(t *testing.T, addr, dir, user string, args ...string) (string, error) {
	t.Helper()

	cliArgs := []string{
		args[0],
		"--server", addr,
		"--cert", filepath.Join(dir, user+".crt"),
		"--key", filepath.Join(dir, user+".key"),
		"--ca", filepath.Join(dir, "ca.crt"),
	}
	cliArgs = append(cliArgs, args[1:]...)
	cmd := exec.Command(binPath, cliArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	return out.String(), cmd.Run()
}

// waitFor polls Status until the output contains the given substring.
func waitFor(t *testing.T, addr, dir, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := runCLI(t, addr, dir, "alice", "status", id)
		if strings.Contains(out, want) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %q", want)
	return ""
}

func TestStart(t *testing.T) {
	addr, dir := startServer(t)
	out, err := runCLI(t, addr, dir, "alice", "start", "echo", "hi")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if id := strings.TrimSpace(out); id == "" {
		t.Error("no id printed")
	}
}

func TestStatus(t *testing.T) {
	addr, dir := startServer(t)
	out, _ := runCLI(t, addr, dir, "alice", "start", "echo", "hi")
	id := strings.TrimSpace(out)

	got := waitFor(t, addr, dir, id, "EXITED")
	if !strings.Contains(got, "exit_code=0") {
		t.Errorf("got %q", got)
	}
}

func TestStop(t *testing.T) {
	addr, dir := startServer(t)
	out, _ := runCLI(t, addr, dir, "alice", "start", "sleep", "60")
	id := strings.TrimSpace(out)

	if _, err := runCLI(t, addr, dir, "alice", "stop", id); err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, addr, dir, id, "EXITED")
	if !strings.Contains(got, "exit_code=-1") {
		t.Errorf("got %q", got)
	}
}

func TestOutput(t *testing.T) {
	addr, dir := startServer(t)

	out, _ := runCLI(t, addr, dir, "alice", "start", "sh", "-c", "echo line1; echo line2")
	id := strings.TrimSpace(out)

	got, err := runCLI(t, addr, dir, "alice", "output", id)
	if err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") {
		t.Errorf("got %q", got)
	}
}

func TestViewerCantStart(t *testing.T) {
	addr, dir := startServer(t)

	out, err := runCLI(t, addr, dir, "bob", "start", "echo", "hi")
	if err == nil {
		t.Fatalf("expected error, got: %s", out)
	}
	if !strings.Contains(out, "PermissionDenied") {
		t.Errorf("got %q", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	addr, dir := startServer(t)
	out, err := runCLI(t, addr, dir, "alice", "huh")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(out, "usage:") {
		t.Errorf("got %q", out)
	}
}

func TestStopWithoutID(t *testing.T) {
	addr, dir := startServer(t)
	out, err := runCLI(t, addr, dir, "alice", "stop")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(out, "usage: jobctl stop") {
		t.Errorf("got %q", out)
	}
}
