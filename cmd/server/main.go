package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	
	"github.com/anjalitiwari/job-worker/internal/auth"
	"github.com/anjalitiwari/job-worker/internal/server"
	"github.com/anjalitiwari/job-worker/internal/tlsconfig"
	pb "github.com/anjalitiwari/job-worker/proto"
)

func main() {
	var (
		listen = flag.String("listen", "127.0.0.1:50051", "address to listen on")
		certFile = flag.String("cert", "certs/server.crt", "server cert file")
		keyFile = flag.String("key", "certs/server.key", "server key file")
		caFile = flag.String("ca", "certs/ca.crt", "CA cert file for client cert verification")
	)
	flag.Parse()

	tlsCfg, err := tlsconfig.Server(*certFile, *keyFile, *caFile)
	if err != nil {
		log.Fatalf("tls: %v", err)
	}

	idMap := auth.IdentityMap{"alice": auth.RoleAdmin, "bob": auth.RoleViewer}

	gs := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.UnaryInterceptor(auth.UnaryInterceptor(idMap)),
		grpc.StreamInterceptor(auth.StreamInterceptor(idMap)),
	)
	pb.RegisterJobWorkerServer(gs, server.New())

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutting down...")
		gs.GracefulStop()
	}()

	log.Printf("listening on %s", *listen)
	if err := gs.Serve(ln); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

