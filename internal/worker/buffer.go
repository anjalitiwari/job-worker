// Package worker runs, observes, and stops Linux processes.
package worker

import (
	"io"
	"sync"
)
// OutputBuffer is an append-only byte buffer with one writer and many
// concurrent readers. Each reader starts at byte 0 and blocks when it
// catches up; readers wake on the next Write or Close.
type OutputBuffer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	closed bool
}

func NewOutputBuffer() *OutputBuffer {
	b := &OutputBuffer{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write appends p to the buffer and wakes blocked readers.
// Returns io.ErrClosedPipe if Close has been called.
func (b *OutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, io.ErrClosedPipe
	}
	b.data = append(b.data, p...)
	b.cond.Broadcast()
	return len(p), nil
}

// Close marks the buffer as finished. Readers drain remaining bytes
// then receive io.EOF. Idempotent.
func (b *OutputBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if b.closed {
		return nil
	}
	b.closed = true
	b.cond.Broadcast()
	return nil
}

// Reader returns a new reader positioned at byte 0. Each call gets an
// independent cursor.
func (b *OutputBuffer) Reader() io.ReadCloser {
	return &bufferReader{buf: b}
}

type bufferReader struct {
	buf    *OutputBuffer
	offset int
	closed bool // guarded by buf.mu
}

func (r *bufferReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	r.buf.mu.Lock()
	defer r.buf.mu.Unlock()

	// Wait for new bytes, buffer close, or reader close.
	for r.offset >= len(r.buf.data) && !r.buf.closed && !r.closed {
		r.buf.cond.Wait()
	}
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if r.offset >= len(r.buf.data) {
		return 0, io.EOF
	}

	n := copy(p, r.buf.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *bufferReader) Close() error {
	r.buf.mu.Lock()
	defer r.buf.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true
	r.buf.cond.Broadcast()
	return nil
}
