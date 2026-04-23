package worker

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestWriteThenRead(t *testing.T) {
	b := NewOutputBuffer()
	b.Write([]byte("hello"))
	b.Close()

	got, err := io.ReadAll(b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestBinarySafe(t *testing.T) {
	b := NewOutputBuffer()

	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}
	b.Write(payload)
	b.Close()

	got, _ := io.ReadAll(b.Reader())
	if !bytes.Equal(got, payload) {
		t.Errorf("bytes altered in round trip")
	}
}

func TestLateJoiner(t *testing.T) {
	// A reader created after everything is written + closed
	// should still see all the bytes from the start.
	b := NewOutputBuffer()
	b.Write([]byte("first "))
	b.Write([]byte("second"))
	b.Close()

	got, _ := io.ReadAll(b.Reader())
	if string(got) != "first second" {
		t.Errorf("got %q", got)
	}
}

func TestConcurrentReaders(t *testing.T) {
	b := NewOutputBuffer()
	const n = 10

	var wg sync.WaitGroup
	results := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data, err := io.ReadAll(b.Reader())
			if err != nil {
				t.Errorf("reader %d: %v", i, err)
				return
			}
			results[i] = string(data)
		}(i)
	}

	b.Write([]byte("hello "))
	time.Sleep(5 * time.Millisecond)
	b.Write([]byte("world"))
	b.Close()
	wg.Wait()

	for i, got := range results {
		if got != "hello world" {
			t.Errorf("reader %d: got %q", i, got)
		}
	}
}

func TestReaderWakesOnWrite(t *testing.T) {
	b := NewOutputBuffer()

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := b.Reader().Read(buf)
		got <- string(buf[:n])
	}()

	time.Sleep(20 * time.Millisecond) // let reader block
	b.Write([]byte("wakeup"))

	select {
	case s := <-got:
		if s != "wakeup" {
			t.Errorf("got %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not wake up")
	}
}

func TestReaderWakesOnClose(t *testing.T) {
	b := NewOutputBuffer()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := b.Reader().Read(buf)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	b.Close()

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Errorf("want EOF, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not wake")
	}
}

func TestWriteAfterClose(t *testing.T) {
	b := NewOutputBuffer()
	b.Close()

	if _, err := b.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v", err)
	}
}

func TestReaderCloseUnblocksRead(t *testing.T) {
	b := NewOutputBuffer()
	r := b.Reader()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := r.Read(buf)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	r.Close()

	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not return after reader close")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	b := NewOutputBuffer()
	b.Close()
	if err := b.Close(); err != nil {
		t.Error(err)
	}
	r := b.Reader()
	r.Close()
	if err := r.Close(); err != nil {
		t.Error(err)
	}
}

func TestStreamingLotsOfBytes(t *testing.T) {
	b := NewOutputBuffer()

	const total = 1000
	go func() {
		for i := 0; i < total; i++ {
			b.Write([]byte{byte(i % 256)})
		}
		b.Close()
	}()

	got, err := io.ReadAll(b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("got %d bytes, want %d", len(got), total)
	}
	for i, v := range got {
		if v != byte(i%256) {
			t.Fatalf("byte %d: got %d", i, v)
		}
	}
}