# job-worker

A prototype job worker service that starts, stops, queries, and streams output from Linux processes over gRPC with mTLS.

See [design.md](./design.md) for the full design.

## Components

- `internal/worker` — worker library (runs a process, captures output, stops it)
- `cmd/server` — gRPC server wrapping the library with mTLS
- `cmd/jobctl` — CLI client

## Build & test

```bash
make build   # compile server and jobctl into ./bin
make test    # run all tests with -race
make proto   # regenerate protobuf code
```

## Status

In active development.