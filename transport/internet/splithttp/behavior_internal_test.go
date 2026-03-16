package splithttp

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestBalancedRequestBehaviorStable(t *testing.T) {
	config := &Config{
		BehaviorProfile: BehaviorProfileBalanced,
	}

	behavior := config.NewRequestBehavior("2")
	if behavior == nil {
		t.Fatal("expected balanced request behavior")
	}

	header1 := config.GetRequestHeaderForBehavior(behavior)
	header2 := config.GetRequestHeaderForBehavior(behavior)
	if header1.Get("Accept") == "" {
		t.Fatal("expected balanced behavior to set Accept header")
	}
	if header1.Get("Accept") != header2.Get("Accept") {
		t.Fatalf("expected stable Accept header, got %q and %q", header1.Get("Accept"), header2.Get("Accept"))
	}

	padding1 := config.GetRequestPaddingConfig("https://example.com/test", behavior)
	padding2 := config.GetRequestPaddingConfig("https://example.com/test", behavior)
	if padding1.Placement.Placement != padding2.Placement.Placement {
		t.Fatalf("expected stable padding placement, got %q and %q", padding1.Placement.Placement, padding2.Placement.Placement)
	}
}

func TestBalancedResponseContentType(t *testing.T) {
	config := &Config{
		BehaviorProfile: BehaviorProfileBalanced,
	}

	request, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/plain, */*;q=0.5")
	if got := config.GetResponseContentType(request); got != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected balanced content type: %q", got)
	}

	config.NoSSEHeader = true
	request.Header.Set("Accept", "text/event-stream")
	if got := config.GetResponseContentType(request); got != "" {
		t.Fatalf("expected SSE content type to be suppressed, got %q", got)
	}
}

func TestSessionOpenTimeoutReapsUnconnectedSession(t *testing.T) {
	handler := &requestHandler{
		ln:        &Listener{config: &Config{SessionOpenTimeoutSecs: 1}},
		sessionMu: &sync.Mutex{},
	}

	session := handler.upsertSession("open-timeout")
	if _, ok := handler.sessions.Load("open-timeout"); !ok {
		t.Fatal("expected session to exist immediately after creation")
	}

	time.Sleep(1200 * time.Millisecond)

	if _, ok := handler.sessions.Load("open-timeout"); ok {
		t.Fatal("expected session to be reaped after open timeout")
	}
	if err := session.uploadQueue.Push(Packet{Payload: []byte("x"), Seq: 0}); err == nil {
		t.Fatal("expected closed upload queue after open-timeout reap")
	}
}

func TestSessionIdleTimeoutReapsConnectedSession(t *testing.T) {
	handler := &requestHandler{
		ln:        &Listener{config: &Config{SessionOpenTimeoutSecs: 1, SessionIdleTimeoutSecs: 1}},
		sessionMu: &sync.Mutex{},
	}

	session := handler.upsertSession("idle-timeout")
	session.isFullyConnected.Close()
	session.touch()

	time.Sleep(1200 * time.Millisecond)

	if _, ok := handler.sessions.Load("idle-timeout"); ok {
		t.Fatal("expected session to be reaped after idle timeout")
	}
}

func TestSplitConnDeadlinesCloseSides(t *testing.T) {
	reader, writer := io.Pipe()
	conn := &splitConn{
		reader: reader,
		writer: writer,
	}
	defer conn.Close()

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := conn.Read(buf)
		readErr <- err
	}()

	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("expected read deadline to close the reader")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for read deadline")
	}

	if err := conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Fatal("expected write deadline to close the writer")
	}
}

func TestBrowserDialerClientIsClosedDoesNotPanic(t *testing.T) {
	client := &BrowserDialerClient{transportConfig: &Config{}}
	if !client.IsClosed() {
		t.Fatal("expected browser dialer client to report closed when browser dialer is unavailable")
	}
}

func TestH1UploadConnectionReuse(t *testing.T) {
	var dialCount int

	client := &DefaultDialerClient{
		transportConfig: &Config{
			UplinkHTTPMethod: "POST",
		},
		httpVersion: "1.1",
		dialUploadConn: func(context.Context) (net.Conn, error) {
			dialCount++
			clientConn, serverConn := net.Pipe()
			go serveH1UploadResponses(t, serverConn, 2)
			return clientConn, nil
		},
	}

	for i := 0; i < 2; i++ {
		err := client.PostPacket(
			context.Background(),
			"http://example.com/upload",
			"session",
			strconv.Itoa(i),
			bytes.NewReader([]byte("payload")),
			int64(len("payload")),
			nil,
		)
		if err != nil {
			t.Fatalf("unexpected H1 upload error on attempt %d: %v", i, err)
		}
	}

	if dialCount != 1 {
		t.Fatalf("expected one H1 upload connection, got %d", dialCount)
	}
}

func serveH1UploadResponses(t *testing.T, conn net.Conn, requests int) {
	t.Helper()
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for i := 0; i < requests; i++ {
		req, err := http.ReadRequest(reader)
		if err != nil {
			t.Errorf("failed to read request %d: %v", i, err)
			return
		}
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			t.Errorf("failed to drain request body %d: %v", i, err)
			req.Body.Close()
			return
		}
		req.Body.Close()
		if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"); err != nil {
			t.Errorf("failed to write response %d: %v", i, err)
			return
		}
	}
}
