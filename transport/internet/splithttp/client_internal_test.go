package splithttp

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type trackedReadCloser struct {
	io.Reader
	mu         sync.Mutex
	closeCount int
	closed     chan struct{}
}

func newTrackedReadCloser(payload string) *trackedReadCloser {
	return &trackedReadCloser{
		Reader: strings.NewReader(payload),
		closed: make(chan struct{}),
	}
}

func (r *trackedReadCloser) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closeCount++
	if r.closeCount == 1 {
		close(r.closed)
	}
	return nil
}

func TestWaitReadCloserReadBlocksUntilSet(t *testing.T) {
	w := &WaitReadCloser{Wait: make(chan struct{})}

	type result struct {
		n    int
		err  error
		data string
	}
	done := make(chan result, 1)

	go func() {
		buf := make([]byte, 5)
		n, err := w.Read(buf)
		done <- result{n: n, err: err, data: string(buf[:max(0, n)])}
	}()

	select {
	case res := <-done:
		t.Fatalf("read returned before Set: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}

	w.Set(io.NopCloser(strings.NewReader("hello")))

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected read error: %v", res.err)
		}
		if res.n != 5 || res.data != "hello" {
			t.Fatalf("unexpected read result: n=%d data=%q", res.n, res.data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read after Set")
	}
}

func TestWaitReadCloserCloseBeforeSet(t *testing.T) {
	w := &WaitReadCloser{Wait: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := w.Read(buf)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := w.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	select {
	case err := <-done:
		if err != io.ErrClosedPipe {
			t.Fatalf("expected io.ErrClosedPipe, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read after Close")
	}
}

func TestWaitReadCloserSetAfterCloseClosesIncoming(t *testing.T) {
	w := &WaitReadCloser{Wait: make(chan struct{})}
	if err := w.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	reader := newTrackedReadCloser("hello")
	w.Set(reader)

	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("expected Set to close reader after early Close")
	}

	buf := make([]byte, 1)
	if _, err := w.Read(buf); err != io.ErrClosedPipe {
		t.Fatalf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestWaitReadCloserSecondSetClosesIncoming(t *testing.T) {
	w := &WaitReadCloser{Wait: make(chan struct{})}
	w.Set(io.NopCloser(strings.NewReader("first")))

	reader := newTrackedReadCloser("second")
	w.Set(reader)

	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("expected second Set to close incoming reader")
	}

	buf := make([]byte, 5)
	n, err := w.Read(buf)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if n != 5 || string(buf[:n]) != "first" {
		t.Fatalf("unexpected read result: n=%d data=%q", n, string(buf[:n]))
	}
}
