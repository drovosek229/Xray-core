package splithttp_test

import (
	"io"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	. "github.com/xtls/xray-core/transport/internet/splithttp"
)

func Test_regression_readzero(t *testing.T) {
	q := NewUploadQueue(10)
	q.Push(Packet{
		Payload: []byte("x"),
		Seq:     0,
	})
	buf := make([]byte, 20)
	n, err := q.Read(buf)
	common.Must(err)
	if n != 1 {
		t.Error("n=", n)
	}
}

func Test_reorders_out_of_order_packets(t *testing.T) {
	q := NewUploadQueue(10)
	common.Must(q.Push(Packet{
		Payload: []byte("b"),
		Seq:     1,
	}))
	common.Must(q.Push(Packet{
		Payload: []byte("a"),
		Seq:     0,
	}))

	buf := make([]byte, 1)

	n, err := q.Read(buf)
	common.Must(err)
	if n != 1 || string(buf[:n]) != "a" {
		t.Fatalf("first read = %q, %d", string(buf[:n]), n)
	}

	n, err = q.Read(buf)
	common.Must(err)
	if n != 1 || string(buf[:n]) != "b" {
		t.Fatalf("second read = %q, %d", string(buf[:n]), n)
	}

	common.Must(q.Close())
	if _, err = q.Read(buf); err != io.EOF {
		t.Fatalf("expected EOF after close, got %v", err)
	}
}

func TestUploadQueueDropsStaleDuplicatePackets(t *testing.T) {
	q := NewUploadQueue(10)

	common.Must(q.Push(Packet{
		Payload: []byte("a"),
		Seq:     0,
	}))

	buf := make([]byte, 1)
	n, err := q.Read(buf)
	common.Must(err)
	if n != 1 || string(buf[:n]) != "a" {
		t.Fatalf("first read = %q, %d", string(buf[:n]), n)
	}

	common.Must(q.Push(Packet{
		Payload: []byte("z"),
		Seq:     0,
	}))
	common.Must(q.Push(Packet{
		Payload: []byte("b"),
		Seq:     1,
	}))

	type result struct {
		n    int
		err  error
		data string
	}
	done := make(chan result, 1)
	go func() {
		nextBuf := make([]byte, 1)
		n, err := q.Read(nextBuf)
		done <- result{n: n, err: err, data: string(nextBuf[:max(0, n)])}
	}()

	select {
	case res := <-done:
		common.Must(res.err)
		if res.n != 1 || res.data != "b" {
			t.Fatalf("second read = %q, %d", res.data, res.n)
		}
	case <-time.After(time.Second):
		t.Fatal("upload queue stalled on stale duplicate packet")
	}
}
