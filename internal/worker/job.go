package worker

import (
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
)

type JobState int
const (
	JobStateUnspecified JobState = iota
	JobStateRunning
	JobStateExited
)

func (s JobState) String() string {
	switch s {
	case JobStateRunning:
		return "RUNNING"
	case JobStateExited:
		return "EXITED"
	default:
		return "UNSPECIFIED"
	}
}

type JobStatus struct {
	State    JobState
	ExitCode int // valid only when State == JobStateExited
	PID      int
}

// Job wraps a running process and its captured output.
type Job struct {
	cmd *exec.Cmd
	buf *OutputBuffer
	mu       sync.Mutex
	state    JobState
	exitCode int
	done     chan struct{}
}

// NewJob starts command+args and returns a running Job. If the process
// can't be started, the error is returned and no Job is created
func NewJob(command string, args []string) (*Job, error) {
	if command == "" {
		return nil, errors.New("command is required")
	}

	j := &Job{
		cmd:  exec.Command(command, args...),
		buf:  NewOutputBuffer(),
		done: make(chan struct{}),
	}
	// stdout and stderr both go to the same buffer.
	j.cmd.Stdout = j.buf
	j.cmd.Stderr = j.buf

	if err := j.cmd.Start(); err != nil {
		return nil, err
	}
	j.state = JobStateRunning
	go j.wait()
	return j, nil
}

func (j *Job) wait() {
	err := j.cmd.Wait()
	j.mu.Lock()

	switch {
	case err == nil:
		j.state = JobStateExited
		j.exitCode = 0
	case errors.As(err, new(*exec.ExitError)):
		j.state = JobStateExited
		j.exitCode = j.cmd.ProcessState.ExitCode() // -1 if signaled
	default:
		j.state = JobStateExited
		j.exitCode = -1
	}
	j.mu.Unlock()

	// Close buffer first so readers see EOF, then done so anyone
	// waiting wakes up with final state already visible.
    j.buf.Close()
    close(j.done)
}


// Stop sends SIGKILL. No grace period — see design doc.
//
// TODO: this kills the direct child only. If the command spawned its
// own children, they're orphaned. A future pass should use a process
// group (Setpgid) and signal the whole group.
func (j *Job) Stop() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.state != JobStateRunning {
		return errors.New("job is not running")
	}
	return j.cmd.Process.Signal(syscall.SIGKILL)
}

func (j *Job) Status() JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()

	pid := 0
	if j.cmd.Process != nil {
		pid = j.cmd.Process.Pid
	}
	return JobStatus{State: j.state, ExitCode: j.exitCode, PID: pid}
}

// Output returns a fresh reader over merged stdout+stderr, starting at
// byte 0. Safe to call any number of times
func (j *Job) Output() io.ReadCloser {
	return j.buf.Reader()
}

