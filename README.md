# job-worker

A prototype job worker service that starts, stops, queries, and streams output from Linux processes over gRPC with mTLS.

See [design.md](./design.md) for the full design.

## Quick start

```bash
make certs     # generate local PKI into ./certs
make build     # compile bin/server and bin/jobctl
./bin/server   # listens on 127.0.0.1:50051 by default
```

In another terminal:

```bash
./bin/jobctl start echo "hello world"
# prints a UUID

./bin/jobctl status <uuid>
# state=EXITED exit_code=0 pid=12345

./bin/jobctl output <uuid>
# hello world
```

## Components

- `internal/worker` — runs a process, captures merged stdout+stderr, stops it via SIGKILL
- `internal/server` — gRPC service wrapping the worker library, owns the job map
- `internal/auth` — mTLS-based identity extraction and role-based authorization
- `internal/tlsconfig` — TLS 1.3 config helpers for server and client
- `internal/certgen` — local PKI generator (CA + server + client certs)
- `cmd/server` — server binary
- `cmd/jobctl` — CLI client

## Identities

`make certs` generates two client identities for local dev:

| User  | Role   | Can call                    |
|-------|--------|-----------------------------|
| alice | admin  | Start, Stop, Status, Output |
| bob   | viewer | Status, Output              |

`jobctl` defaults to alice's certs. To use bob:

```bash
./bin/jobctl --cert certs/bob.crt --key certs/bob.key status <uuid>
```

## Make targets

```bash
make certs   # generate ./certs (CA, server, alice, bob)
make build   # compile bin/server and bin/jobctl
make test    # run all tests with -race
make proto   # regenerate protobuf code (requires protoc + plugins)
make clean   # remove bin/ and certs/
```

## Tests

```bash
go test -race ./...
```

The integration tests spin up a real gRPC server with mTLS on a random port.