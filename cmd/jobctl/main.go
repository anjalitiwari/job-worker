// jobctl is the command-line client for the job-worker gRPC server
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/anjalitiwari/job-worker/internal/tlsconfig"
	pb "github.com/anjalitiwari/job-worker/proto"
)

type config struct {
	server, cert, key, ca string
}

type command func(client *jobctlClient, args []string) error

var commands = map[string]command{
	"start":  cmdStart,
	"stop":   cmdStop,
	"status": cmdStatus,
	"output": cmdOutput,
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("command is required")
	}

	name,rest := args[0], args[1:]

	if name == "help" || name == "--help" || name == "-h" {
		usage()
		return nil
	}

	cmd,ok := commands[name]
	if !ok {
		usage()
		return fmt.Errorf("unknown command: %s", name)
	}

	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg := config{}
	fs.StringVar(&cfg.server, "server", "127.0.0.1:50051,", "gRPC server address")
	fs.StringVar(&cfg.cert, "cert", "certs/client.crt", "client certificate")
	fs.StringVar(&cfg.key, "key", "certs/client.key", "client key")
	fs.StringVar(&cfg.ca, "ca", "certs/ca.crt", "trusted CA")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	client, err := newClient(cfg)
	if err != nil {
		return fmt.Errorf("client: %w", err)
	}

	defer client.Close()

	return cmd(client, fs.Args())
}

// jobctlClient owns the gRPC connection and exposes one method per RPC.
type jobctlClient struct {
	rpc  pb.JobWorkerClient
	conn *grpc.ClientConn
}

func newClient(cfg config) (*jobctlClient, error) {
	tlsCfg, err := tlsconfig.Client(cfg.cert, cfg.key, cfg.ca, "localhost")
	if err != nil {
		return nil, fmt.Errorf("tls: %w", err)
	}

	conn, err := grpc.NewClient(cfg.server, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}

	return &jobctlClient{rpc:  pb.NewJobWorkerClient(conn),conn: conn}, nil
}

func (c *jobctlClient) Close() error {
	return c.conn.Close()
}

func (c *jobctlClient) Start(ctx context.Context, command string, args []string) (string, error) {
	resp, err := c.rpc.Start(ctx, &pb.StartRequest{Command: command, Args: args})
	if err != nil {
		return "", err
	}
	return resp.JobId, nil
}

func (c *jobctlClient) Status(ctx context.Context, id string) (*pb.StatusResponse, error) {
	return c.rpc.Status(ctx, &pb.StatusRequest{JobId: id})
}


func (j *jobctlClient) StreamOutput(ctx context.Context, id string, w io.Writer) error {
	stream, err := j.rpc.Output(ctx, &pb.OutputRequest{JobId: id})
	if err != nil {
		return err
	}
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
	}
}

func cmdStart(client *jobctlClient, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jobctl start <command> [args...]")
	}
	id, err := client.Start(context.Background(), args[0], args[1:])
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func cmdStop(client *jobctlClient, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jobctl stop <job-id>")
	}
	return client.Stop(context.Background(), args[0])
}

func cmdStatus(client *jobctlClient, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jobctl status <job-id>")
	}
	resp, err := client.Status(context.Background(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("state=%s exit_code=%d pid=%d\n", resp.State, resp.ExitCode, resp.Pid)
	return nil
}

func cmdOutput(client *jobctlClient, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jobctl output <job-id>")
	}
	// Cancel the stream on SIGINT/SIGTERM so the server can clean up
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	return client.StreamOutput(ctx, args[0], os.Stdout)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  	jobctl start <command> [args...]
  	jobctl stop <job-id>
  	jobctl status <job-id>
  	jobctl output <job-id>

	flags:
 	 --server   server address (default 127.0.0.1:50051)
  	--cert     client certificate (default certs/alice.crt)
  	--key      client key (default certs/alice.key)
  	--ca       trusted CA (default certs/ca.crt)`)
}