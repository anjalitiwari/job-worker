package worker

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestEmptyCommand(t *testing.T) {
	if _, err := NewJob("", nil); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestBinaryNotFound(t *testing.T) {
	if _, err := NewJob("/nope/does/not/exist", nil); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestNaturalExit(t *testing.T) {
	j, err := NewJob("echo", []string{"hello world"})
	if err != nil {
		t.Fatal(err)
	}
	<-j.Done()

	st := j.Status()
	if st.State != JobStateExited || st.ExitCode != 0 {
		t.Errorf("got %+v", st)
	}

	out, _ := io.ReadAll(j.Output())
	if !strings.Contains(string(out), "hello world") {
		t.Errorf("output %q", out)
	}
}

func TestNonZeroExit(t *testing.T) {
	j, _ := NewJob("sh", []string{"-c", "exit 42"})
	<-j.Done()
	if code := j.Status().ExitCode; code != 42 {
		t.Errorf("exit code %d, want 42", code)
	}
}

func TestStopKills(t *testing.T) {
	j, _ := NewJob("sleep", []string{"60"})

	if err := j.Stop(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-j.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not die after SIGKILL")
	}

	if code := j.Status().ExitCode; code != -1 {
		t.Errorf("exit code %d, want -1", code)
	}
}

func TestStopAfterExit(t *testing.T) {
	j, _ := NewJob("echo", []string{"bye"})
	<-j.Done()
	if err := j.Stop(); err == nil {
		t.Error("expected error stopping an exited job")
	}
}

func TestStderrMerged(t *testing.T) {
	j, _ := NewJob("sh", []string{"-c", "echo out; echo err 1>&2"})
	<-j.Done()

	s, _ := io.ReadAll(j.Output())
	if !strings.Contains(string(s), "out") || !strings.Contains(string(s), "err") {
		t.Errorf("output %q", s)
	}
}

func TestConcurrentOutputReaders(t *testing.T) {
	j, _ := NewJob("sh", []string{"-c", "echo first; sleep 0.05; echo second"})

	got := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			b, _ := io.ReadAll(j.Output())
			got <- string(b)
		}()
	}
	<-j.Done()

	for i := 0; i < 2; i++ {
		select {
		case s := <-got:
			if !strings.Contains(s, "first") || !strings.Contains(s, "second") {
				t.Errorf("reader got %q", s)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reader stuck")
		}
	}
}

func TestStatusPID(t *testing.T) {
	j, _ := NewJob("sleep", []string{"5"})
	defer j.Stop()
	if pid := j.Status().PID; pid <= 0 {
		t.Errorf("pid %d", pid)
	}
}

func TestStateTransitions(t *testing.T) {
	j, _ := NewJob("sleep", []string{"0.1"})

	if j.Status().State != JobStateRunning {
		t.Errorf("initial state %v", j.Status().State)
	}
	<-j.Done()
	if j.Status().State != JobStateExited {
		t.Errorf("final state %v", j.Status().State)
	}
}
