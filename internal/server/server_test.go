package server

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/anjalitiwari/job-worker/internal/auth"
	"github.com/anjalitiwari/job-worker/internal/certgen"
	"github.com/anjalitiwari/job-worker/internal/tlsconfig"
	pb "github.com/anjalitiwari/job-worker/proto"
)

func setup(t *testing.T) (addr, certDir string) {
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
	pb.RegisterJobWorkerServer(gs, New())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = gs.Serve(ln) }()
	t.Cleanup(func() { gs.Stop() })

	return ln.Addr().String(), certDir
}

func dial(t *testing.T, addr, certDir, user string) pb.JobWorkerClient {
	t.Helper()

	cfg, err := tlsconfig.Client(
		filepath.Join(certDir, user+".crt"),
		filepath.Join(certDir, user+".key"),
		filepath.Join(certDir, "ca.crt"),
		"localhost",
	)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewJobWorkerClient(conn)
}

// waitForExit polls Status until the job is exited or the deadline hits
func waitForExit(t *testing.T, c pb.JobWorkerClient, id string) *pb.StatusResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := c.Status(context.Background(), &pb.StatusRequest{JobId: id})
		if err != nil {
			t.Fatal(err)
		}
		if st.State == pb.JobState_JOB_STATE_EXITED {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not exit in time")
	return nil
}

func TestStartAndStatus(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")

	resp, err := c.Start(context.Background(), &pb.StartRequest{Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobId == "" {
		t.Fatal("no job_id")
	}

	st := waitForExit(t, c, resp.JobId)
	if st.ExitCode != 0 {
		t.Errorf("exit %d", st.ExitCode)
	}
}

func TestEmptyCommand(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")

	_, err := c.Start(context.Background(), &pb.StartRequest{Command: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got %v", err)
	}
}

func TestStop(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")

	resp, err := c.Start(context.Background(), &pb.StartRequest{Command: "sleep", Args: []string{"60"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Stop(context.Background(), &pb.StopRequest{JobId: resp.JobId}); err != nil {
		t.Fatal(err)
	}

	st := waitForExit(t, c, resp.JobId)
	if st.ExitCode != -1 {
		t.Errorf("exit %d, want -1", st.ExitCode)
	}
}

func TestStopAfterExit(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")

	resp, _ := c.Start(context.Background(), &pb.StartRequest{Command: "echo", Args: []string{"bye"}})
	waitForExit(t, c, resp.JobId)

	_, err := c.Stop(context.Background(), &pb.StopRequest{JobId: resp.JobId})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("got %v", err)
	}
}

func TestStatusUnknownJob(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")
	_, err := c.Status(context.Background(), &pb.StatusRequest{JobId: "bogus"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got %v", err)
	}
}

func TestStopUnknownJob(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")
	_, err := c.Stop(context.Background(), &pb.StopRequest{JobId: "bogus"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got %v", err)
	}
}

func TestOutputUnknownJob(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")
	stream, _ := c.Output(context.Background(), &pb.OutputRequest{JobId: "bogus"})
	_, err := stream.Recv()
	if status.Code(err) != codes.NotFound {
		t.Errorf("got %v", err)
	}
}

func TestOutput(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "alice")

	resp, err := c.Start(context.Background(), &pb.StartRequest{
		Command: "sh",
		Args:    []string{"-c", "echo line1; echo line2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := c.Output(context.Background(), &pb.OutputRequest{JobId: resp.JobId})
	if err != nil {
		t.Fatal(err)
	}

	var got []byte
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, chunk.Data...)
	}

	s := string(got)
	if !strings.Contains(s, "line1") || !strings.Contains(s, "line2") {
		t.Errorf("output %q", s)
	}
}

func TestViewerBlocked(t *testing.T) {
	addr, dir := setup(t)
	c := dial(t, addr, dir, "bob")

	_, err := c.Start(context.Background(), &pb.StartRequest{Command: "echo"})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("got %v", err)
	}
}

func TestViewerCanRead(t *testing.T) {
	addr, dir := setup(t)
	admin := dial(t, addr, dir, "alice")
	viewer := dial(t, addr, dir, "bob")

	resp, err := admin.Start(context.Background(), &pb.StartRequest{Command: "echo", Args: []string{"hi"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := viewer.Status(context.Background(), &pb.StatusRequest{JobId: resp.JobId}); err != nil {
		t.Errorf("viewer status: %v", err)
	}
}
