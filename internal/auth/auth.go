package auth

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"	
	"google.golang.org/grpc/peer"
)

type Role int

const (
	RoleUnknown Role = iota
	RoleAdmin
	RoleViewer
)

func (r Role) String() string {
	switch r {
	case RoleAdmin:
		return "admin"
	case RoleViewer:
		return "viewer"
	default:
		return "unknown"
	}
}

type IdentityMap map[string]Role

// allowedRoles is the per-RPC allowlist. Method names are full grpc paths like 
// "//jobworker.JobWorker/Start"
var allowedRoles = map[string][]Role{
	"/jobworker.JobWorker/Start" : {RoleAdmin},
	"/jobworker.JobWorker/Stop" : {RoleAdmin},
	"/jobworker.JobWorker/Status" : {RoleAdmin, RoleViewer},
	"/jobworker.JobWorker/Output" : {RoleAdmin, RoleViewer},
}

type ctxKey struct{}

var identityKey = ctxKey{}

// IdentityFromContext returns the CN of the calling client.
func IdentityFromContext(ctx context.Context) string {
	cn, _ := ctx.Value(identityKey).(string)
	return cn
}	

// UnaryInterceptor authenticates and authorizes unary RPCs
func UnaryInterceptor(idMap IdentityMap) grpc.UnaryServerInterceptor {
	return func(ctx context.Context,req interface{},info *grpc.UnaryServerInfo,handler grpc.UnaryHandler,)(interface{}, error) {
		newCtx, err := authorize(ctx, info.FullMethod, idMap)
		if err != nil {
			return nil, err
		}
		return handler(newCtx, req)
	}
}

// StreamInterceptor authenticates and authorizes streaming RPCs 
func StreamInterceptor(idMap IdentityMap) grpc.StreamServerInterceptor {
	return func(srv interface{},ss grpc.ServerStream,info *grpc.StreamServerInfo,handler grpc.StreamHandler,) error {
		newCtx, err := authorize(ss.Context(), info.FullMethod, idMap)
		if err != nil {
			return err
		}
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: newCtx})
	}
}

// authorize extract the CN from the peer's verified cert, looks up 
// the role and checks the allowedlist. If all checks pass,returns a new context with the CN.
func authorize(ctx context.Context, method string, idMap IdentityMap) (context.Context, error) {
	cn, err := cnFromContext(ctx)
	if err != nil {
		return nil, err
	}

	role, ok := idMap[cn]
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "unknown identity: %s", cn)
	}

	if !roleAllowed(role, method){
		return nil, status.Errorf(codes.PermissionDenied, "identity %s with role %s not allowed to call %s", cn, role, method)
	}
	return context.WithValue(ctx, identityKey, cn), nil

}

// cnFromContext returns the CN from the client's verified cert or an error if not found
func cnFromContext(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer found")
	}
	tlsInfo,ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no TLS information found")
		
	}
	chains := tlsInfo.State.VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "", status.Error(codes.Unauthenticated, "no verified client certs found")
	}
	return chains[0][0].Subject.CommonName, nil
}

func roleAllowed(role Role, method string) bool {
	for _,r := range allowedRoles[method] {
		if r == role {
			return true
		}
	}
	return false
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }