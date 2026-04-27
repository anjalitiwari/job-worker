package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	pb "github.com/anjalitiwari/job-worker/proto"
)

func ctxWithCN(cn string) context.Context {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	info := credentials.TLSInfo{
		State: tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{cert}},
		},
	}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: info})
}

func runUnary(t *testing.T, method, cn string, idMap IdentityMap) error {
	t.Helper()
	in := UnaryInterceptor(idMap)
	_, err := in(
		ctxWithCN(cn),
		nil,
		&grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, req any) (any, error) { return nil, nil },
	)
	return err
}

func TestAdminCanCallEverything(t *testing.T) {
	idMap := IdentityMap{"alice": RoleAdmin}
	for _, m := range []string{
		pb.JobWorker_Start_FullMethodName,
		pb.JobWorker_Stop_FullMethodName,
		pb.JobWorker_Status_FullMethodName,
		pb.JobWorker_Output_FullMethodName,
	} {
		if err := runUnary(t, m, "alice", idMap); err != nil {
			t.Errorf("admin %s: %v", m, err)
		}
	}
}

func TestViewerLimitedToReadOnly(t *testing.T) {
	idMap := IdentityMap{"bob": RoleViewer}

	// Allowed.
	for _, m := range []string{
		pb.JobWorker_Status_FullMethodName,
		pb.JobWorker_Output_FullMethodName,
	} {
		if err := runUnary(t, m, "bob", idMap); err != nil {
			t.Errorf("viewer %s: %v", m, err)
		}
	}

	// Denied.
	for _, m := range []string{
		pb.JobWorker_Start_FullMethodName,
		pb.JobWorker_Stop_FullMethodName,
	} {
		err := runUnary(t, m, "bob", idMap)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("viewer %s: got %v, want PermissionDenied", m, err)
		}
	}
}

func TestUnknownCN(t *testing.T) {
	err := runUnary(t, pb.JobWorker_Status_FullMethodName, "stranger", IdentityMap{"alice": RoleAdmin})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("got %v, want Unauthenticated", err)
	}
}

func TestNoPeer(t *testing.T) {
	in := UnaryInterceptor(IdentityMap{"alice": RoleAdmin})
	_, err := in(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: pb.JobWorker_Status_FullMethodName},
		func(ctx context.Context, req any) (any, error) { return nil, nil },
	)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("got %v", err)
	}
}

func TestUnknownMethod(t *testing.T) {
	// Not a real RPC — testing that unknown methods are denied.
	err := runUnary(t, "/jobworker.JobWorker/MysteryRPC", "alice", IdentityMap{"alice": RoleAdmin})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("got %v", err)
	}
}

func TestIdentityFlowsThrough(t *testing.T) {
	in := UnaryInterceptor(IdentityMap{"alice": RoleAdmin})

	var seen string
	_, err := in(
		ctxWithCN("alice"),
		nil,
		&grpc.UnaryServerInfo{FullMethod: pb.JobWorker_Status_FullMethodName},
		func(ctx context.Context, req any) (any, error) {
			seen = IdentityFromContext(ctx)
			return nil, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if seen != "alice" {
		t.Errorf("got %q", seen)
	}
}

func TestStreamPath(t *testing.T) {
	in := StreamInterceptor(IdentityMap{"bob": RoleViewer})

	called := false
	err := in(
		nil,
		&fakeStream{ctx: ctxWithCN("bob")},
		&grpc.StreamServerInfo{FullMethod: pb.JobWorker_Output_FullMethodName},
		func(srv any, ss grpc.ServerStream) error {
			called = true
			if cn := IdentityFromContext(ss.Context()); cn != "bob" {
				return errors.New("missing identity")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("handler never ran")
	}
}

func TestStreamRejected(t *testing.T) {
	in := StreamInterceptor(IdentityMap{"bob": RoleViewer})
	err := in(
		nil,
		&fakeStream{ctx: ctxWithCN("bob")},
		&grpc.StreamServerInfo{FullMethod: pb.JobWorker_Start_FullMethodName},
		func(srv any, ss grpc.ServerStream) error { return nil },
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("got %v", err)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeStream) Context() context.Context { return f.ctx }
