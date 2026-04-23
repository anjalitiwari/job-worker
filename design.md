# Job Worker Service - Design Document

## Overview 

A prototype job worker service that lets authenticated clients start, stop, query, and stream output from arbitrary Linux processes over gRPC. The system is composed of three components: a reusable Go worker library, a gRPC API server secured with mTLS, and a CLI client (`jobctl`)

---

## Scope

**In scope:**

- **Components:** a reusable Go worker library, a gRPC API server, and a CLI (`jobctl`).

- **Job control:** start a local executable, stop a running job, query status, and stream output for a job id.

- **Streaming semantics:** output is available from byte 0, supports multiple concurrent consumers, is binary-safe, and uses an efficient wait/notify path(no polling/busy-wait).

- **Transport security:** **mTLS** (TLS 1.3 minimum), verified client certs, and **no additional auth protocols** layered on top of mTLS.

- **Authorization:** a simple gRPC interceptor, CN → role mapping, explicit RPC allowlists.

- **Developer experience:** make certs for local PKI, tests cover auth and streaming including failure scenarios, all run with `go test -race` to catch data races.


**Out of scope:**

- **Persistence** — no database, WAL, or on-disk retention; restart loses all state.
- **Scheduling and queuing** — no delayed starts, priorities, cron, or worker pools.
- **Multi-node / HA** — no clustering, replication, leader election, or failover; single server process on a single host.

---

## User Stories

- **As an admin**, I want to start a job on a remote machine and stream its output in real time without SSH access.
- **As a viewer**, I want to check the status and stream the output of a running job, but not be able to start or stop jobs.

---

## Architecture

Three components, two binaries:

```
                 ┌────────────────────┐
                 │    jobctl (CLI)    │
                 └─────────┬──────────┘
                           │
                      gRPC + mTLS
                           │
                 ┌─────────▼──────────┐
                 │    gRPC Server     │
                 │   (interceptors)   │
                 └─────────┬──────────┘
                           │
                 ┌─────────▼──────────┐
                 │   Worker Library   │
                 │                    │
                 │   Job              │
                 │   └── OutputBuffer │
                 └─────────┬──────────┘
                           │
                     fork/exec
                           │
                 ┌─────────▼──────────┐
                 │  Linux processes   │
                 └────────────────────┘
```

The CLI and server are separate binaries communicating only via gRPC over mTLS. The gRPC layer and worker library are intentionally separated inside the server so the library is independently testable without a network.

---

## Worker library

### Library interface

```go
//The library exposes a single Job primitive.
type Job struct { /* ... */ }

func NewJob(command string, args []string) (*Job, error)

func (j *Job) Stop() error
func (j *Job) Status() JobStatus
func (j *Job) Output() io.ReadCloser  //starts at byte 0

type JobStatus struct {
    State    JobState
    ExitCode int
    PID      int
}

type JobState int

const (
    JobStateUnspecified JobState = 0
    JobStateRunning     JobState = 1
    JobStateExited      JobState = 2
)
```

The library exposes a single Job primitive — it can run a process, stream its output, and stop it.It doesn't track jobs, assign IDs, or manage anything across multiple jobs. Those are service-level concerns: the gRPC server keeps a map[string]*Job and picks UUIDs, but that's the server's call. Output returns an io.ReadCloser, and each call gets a fresh reader starting from byte 0 so multiple clients can stream the same job without stepping on each other.


### Job lifecycle

Each `Job` has a state (Running → Exited), and an OutputBuffer. The server assigns a UUID on creation and stores the Job reference in map[string]*Job, protected by a `sync.RWMutex` for safe concurrent access.


```
          exec() succeeds         ┌─────────┐
         ┌───────────────────────►│ RUNNING │
         │                        └────┬────┘
         │                             │
         │                ┌────────────┴────────────┐
         │                │                         │
         │           natural exit                SIGKILL
         │                │                         │
         │                ▼                         ▼
         │           ┌────────┐                ┌────────┐
         │           │ EXITED │                │ EXITED │
         │           └────────┘                └────────┘
         │
         │  exec() fails → returns error, no Job is created
         └─────────────────────────────────────────────────
```
State transitions are protected by a mutex within each Job. Pre-exec failures (binary not found, permission denied) are returned as errors from NewJob — no Job is created. The state machine is intentionally minimal: any termination lands in Exited, with the exit code carrying whether it was a natural exit or a signal.

### Stop and Exit Semantics

Stopping a job sends `SIGKILL` directly — there is no `SIGTERM` grace period. A production system would send `SIGTERM` first, wait, then escalate to `SIGKILL`; this keeps things simple.


> **Note:** Child process orphaning is a known limitation. If a job spawns child processes, those children are not tracked or killed when the parent is stopped. This will be noted as a TODO in the code.

Exit code interpretation:

- Natural exit — actual exit code from the process
- Killed via SIGKILL — exit code is `-1`
- Exec failure - returned as an error from NewJob; no Job is created

### Output streaming

Each job maintains an `OutputBuffer` - It captures combined stdout and stderr into a single, append-only byte buffer. This gives terminal-like output where stdout and stderr are interleaved in arrival order.

Key properties:

- The buffer stores raw bytes, not lines or strings, so it's binary-safe.
- **Read from byte 0** — every reader starts from the beginning, regardless of when it connects. Multiple clients can stream the same job simultaneously, each with its own offset into the buffer.
- **No polling** — readers block on `sync.Cond` when caught up. The writer signals on every append, waking all waiting readers immediately.
- **Post-exit drain** — after a job exits, readers can still connect and read the full output. The final read returns the remaining bytes plus an EOF.

## gRPC API

```protobuf
syntax = "proto3";
package jobworker;
option go_package = "github.com/anjalitiwari/job-worker/proto";

service JobWorker {
  rpc Start  (StartRequest)  returns (StartResponse);
  rpc Stop   (StopRequest)   returns (StopResponse);
  rpc Status (StatusRequest) returns (StatusResponse);
  rpc Output (OutputRequest) returns (stream OutputChunk);
}

message StartRequest  { string command = 1; repeated string args = 2; }
message StartResponse { string job_id = 1; }
message StopRequest   { string job_id = 1; }
message StopResponse  {}
message StatusRequest { string job_id = 1; }
message OutputRequest { string job_id = 1; }
message OutputChunk   { bytes data = 1; }

enum JobState {
  JOB_STATE_UNSPECIFIED = 0;
  JOB_STATE_RUNNING     = 1;
  JOB_STATE_EXITED      = 2;
}

message StatusResponse {
  string   job_id    = 1;
  JobState state     = 2;
  int32    exit_code = 3;
  int32    pid       = 4;
}
```

The API is intentionally minimal. `Output` is a server-streaming RPC — the client opens one call and receives chunks until the job exits and the buffer drains.

### Error handling

All RPCs return standard gRPC status codes. Errors include a human-readable message.

- Job ID not found, or caller lacks permission to access it → `NotFound`
- Invalid request (empty command) → `InvalidArgument`
- Stop on an already-exited job → `FailedPrecondition`
- Caller is not authenticated (no verified cert, unknown identity) → `Unauthenticated`
- Unexpected internal failure → `Internal`

> **Note:** To avoid leaking information about which jobs exist, authorization failures on per-job RPCs return `NotFound` rather than `PermissionDenied`. `Unauthenticated` is still returned for completely unauthenticated callers.


## Security

### Transport: mTLS

All connections require mutual TLS. Both the server and every client present certificates signed by the same CA.

- **TLS 1.3 exclusively** — `tls.Config` sets both `MinVersion` and `MaxVersion` to `tls.VersionTLS13`.
- **Cipher suites** — determined by the Go runtime for TLS 1.3; no manual override needed or possible.
- **Certificates** — P-256 ECDSA, generated via `make certs` using Go's `crypto/x509` package. No dependency on OpenSSL.

The make certs target generates a CA, a server certificate, and two client certificates (one for alice, one for bob) for local development and testing. The identity-to-role mapping (alice → admin, bob → viewer) is configured at server startup.


### Authorization: identity and role separation

Authentication and authorization are handled as two separate steps. The gRPC interceptor first extracts the Common Name (CN) from the verified client certificate — this identifies *who* the client is. A separate identity-to-role mapping, loaded at startup, decides *what* they're allowed to do:

| Identity      | Role   | Allowed RPCs                |
|---------------|--------|-----------------------------|
| Alice         | admin  | Start, Stop, Status, Output |
| Bob           | viewer | Status, Output              |

Keeping identity and authorization separate means changing a user's role is a config update rather than issuing a new certificate, and adding new roles doesn't require inventing new CNs.The mapping is an explicit allowlist — unknown users get no access by default.

## CLI: `jobctl`

The CLI is a thin gRPC client that accepts a server address and client certificate paths, then exposes the four operations as subcommands:

```
jobctl start <command> [args...]
jobctl stop <job-id>
jobctl status <job-id>
jobctl output <job-id>
```

Certificates and server address are passed via flags (`--cert`, `--key`, `--ca`, `--server`), with `./certs/` as the default directory for local dev.

`output` streams chunks to stdout until the job exits and the buffer drains, then exits cleanly. The CLI handles `SIGINT` / `SIGTERM` by cancelling the gRPC context.

## Testing Strategy

Tests run with `go test -race` to catch data races in the buffer, fan-out, and job lifecycle code.

**Worker library tests:**

- Job lifecycle: start → running → natural exit, start → running → killed, exec failure → error returned from NewJob
- Output buffer: write/read correctness, concurrent writers and readers, reader catches up from byte 0


**Auth tests:**

- Admin can call all four RPCs
- Viewer gets PermissionDenied on Start and Stop (not per-job RPCs), NotFound on Status/Output for jobs they don't own 
- Unknown CN gets `Unauthenticated` on all RPCs

**Streaming integration tests:**

- Late joiner reads full output from byte 0
- Multiple concurrent streams on the same job
- Client disconnect mid-stream does not leak goroutines or affect other readers
- Post-exit drain: connect after job exits, receive full output + EOF

## Trade-offs and Known Limitations

- SIGKILL without grace period — no SIGTERM escalation; keeps stop logic simple.
- Child process orphaning — children are not tracked or killed when a parent is stopped.
- Unbounded output buffer — memory grows without limit on chatty processes.
- No persistence — all state is lost on server restart.
- Fixed role set — no revocation, no per-job ACLs.
- Server shutdown — if the server process exits, running child processes are orphaned. Graceful shutdown with job cleanup can be a future improvement.
