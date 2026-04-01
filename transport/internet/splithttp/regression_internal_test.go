package splithttp

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	xnet "github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/transport/internet"
)

type fakeDialerClient struct{}

func (f *fakeDialerClient) IsClosed() bool {
	return false
}

func (f *fakeDialerClient) OpenStream(context.Context, string, string, io.Reader, bool, *RequestBehavior) (StartedReadCloser, xnet.Addr, xnet.Addr, error) {
	return &readyReadCloser{ReadCloser: io.NopCloser(strings.NewReader(""))}, nil, nil, nil
}

func (f *fakeDialerClient) PostPacket(context.Context, string, string, string, io.Reader, int64, *RequestBehavior) error {
	return nil
}

type startedReadCloserStub struct {
	err error
}

func (s *startedReadCloserStub) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (s *startedReadCloserStub) Close() error {
	return nil
}

func (s *startedReadCloserStub) WaitStart() error {
	return s.err
}

func (s *startedReadCloserStub) CloseWithError(err error) error {
	return nil
}

func destinationFromHTTPURL(t *testing.T, rawURL string) xnet.Destination {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse URL %q: %v", rawURL, err)
	}

	host, portStr, err := stdnet.SplitHostPort(parsedURL.Host)
	if err != nil {
		t.Fatalf("failed to split host/port from %q: %v", parsedURL.Host, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("failed to parse port %q: %v", portStr, err)
	}

	if ip := stdnet.ParseIP(host); ip != nil {
		return xnet.TCPDestination(xnet.IPAddress(ip), xnet.Port(port))
	}

	return xnet.TCPDestination(xnet.DomainAddress(host), xnet.Port(port))
}

func newTestStreamConfig(config *Config) *internet.MemoryStreamConfig {
	return &internet.MemoryStreamConfig{
		ProtocolName:     "splithttp",
		ProtocolSettings: config,
	}
}

func assertHTTPStatusError(t *testing.T, err error, statusCode int) {
	t.Helper()

	var statusErr *HTTPStatusError
	if !stderrors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError(%d), got %T: %v", statusCode, err, err)
	}
	if statusErr.StatusCode != statusCode {
		t.Fatalf("expected status %d, got %d", statusCode, statusErr.StatusCode)
	}
}

func waitForReadError(t *testing.T, reader io.Reader) error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := reader.Read(buf)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for read error")
		return nil
	}
}

func waitForWriteError(t *testing.T, writer io.Writer) error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("x"))
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write error")
		return nil
	}
}

func newRequestHandlerForTest(config *Config) *requestHandler {
	handler := &requestHandler{
		config:    config,
		host:      config.Host,
		path:      config.GetNormalizedPath(),
		ln:        &Listener{config: config},
		sessionMu: &sync.Mutex{},
	}
	return handler
}

func TestDialReturnsStatusErrorForStreamDownStartupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := Dial(context.Background(), destinationFromHTTPURL(t, server.URL), newTestStreamConfig(&Config{Path: "/"}))
	if err == nil {
		t.Fatal("expected Dial to fail on stream-down startup status")
	}
	assertHTTPStatusError(t, err, http.StatusServiceUnavailable)
}

func TestDialReturnsTransportErrorForStreamDownStartupFailure(t *testing.T) {
	listener, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	host, portStr, err := stdnet.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Dial(
		context.Background(),
		xnet.TCPDestination(xnet.IPAddress(stdnet.ParseIP(host)), xnet.Port(port)),
		newTestStreamConfig(&Config{Path: "/"}),
	)
	if err == nil {
		t.Fatal("expected Dial to fail on stream-down transport error")
	}
}

func TestRelayAsyncStartFailureClosesBothSidesWithOriginalError(t *testing.T) {
	expectedErr := &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: http.StatusText(http.StatusServiceUnavailable)}
	reader := &WaitReadCloser{Wait: make(chan struct{})}
	readPipeReader, readPipeWriter := io.Pipe()
	defer readPipeWriter.Close()
	reader.Set(readPipeReader)
	pipeReader, pipeWriter := io.Pipe()
	defer pipeReader.Close()

	relayAsyncStartFailure(&startedReadCloserStub{err: expectedErr}, reader, pipeWriter, pipeReader)

	readErr := waitForReadError(t, reader)
	assertHTTPStatusError(t, readErr, http.StatusServiceUnavailable)

	if err := waitForWriteError(t, pipeWriter); err == nil {
		t.Fatal("expected writer to close with async startup error")
	} else {
		assertHTTPStatusError(t, err, http.StatusServiceUnavailable)
	}
}

func TestPacketUploadReservationRolloverReleasesExpiredClientUsage(t *testing.T) {
	currentClient := &XmuxClient{
		XmuxConn:     &fakeDialerClient{},
		UnreusableAt: time.Now().Add(-time.Second),
	}
	currentClient.LeftRequests.Store(5)
	currentClient.OpenUsage.Store(1)

	nextClient := &XmuxClient{XmuxConn: &fakeDialerClient{}}
	nextClient.LeftRequests.Store(5)
	nextClient.OpenUsage.Store(1)

	holder := newReservationHolder(&httpClientReservation{
		client:     &fakeDialerClient{},
		xmuxClient: currentClient,
	})

	if reservation := holder.Current(); !reservation.NeedsRefresh(time.Now()) {
		t.Fatal("expected expired reservation to require refresh")
	}

	oldReservation := holder.Swap(&httpClientReservation{
		client:     &fakeDialerClient{},
		xmuxClient: nextClient,
	})
	oldReservation.Release()

	if currentClient.OpenUsage.Load() != 0 {
		t.Fatalf("expected expired client usage to be released, got %d", currentClient.OpenUsage.Load())
	}

	holder.Current().ConsumeRequest()
	if nextClient.LeftRequests.Load() != 4 {
		t.Fatalf("expected replacement client to consume exactly one request, got %d", nextClient.LeftRequests.Load())
	}

	holder.Release()
	if nextClient.OpenUsage.Load() != 0 {
		t.Fatalf("expected replacement client usage to be released on close, got %d", nextClient.OpenUsage.Load())
	}
}

func TestUploadErrorStatusMappingAndRetryMatrix(t *testing.T) {
	if got := uploadErrorStatus(errSessionGone); got != http.StatusGone {
		t.Fatalf("expected errSessionGone => 410, got %d", got)
	}
	if got := uploadErrorStatus(errUploadQueueClosed); got != http.StatusGone {
		t.Fatalf("expected errUploadQueueClosed => 410, got %d", got)
	}
	if got := uploadErrorStatus(errUploadReaderExists); got != http.StatusConflict {
		t.Fatalf("expected errUploadReaderExists => 409, got %d", got)
	}

	retriableStatuses := []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	for _, statusCode := range retriableStatuses {
		if !isRetriablePostError(&HTTPStatusError{StatusCode: statusCode, Status: http.StatusText(statusCode)}) {
			t.Fatalf("expected %d to be retriable", statusCode)
		}
	}

	nonRetriableStatuses := []int{http.StatusBadRequest, http.StatusConflict, http.StatusGone, http.StatusRequestEntityTooLarge, http.StatusInternalServerError}
	for _, statusCode := range nonRetriableStatuses {
		if isRetriablePostError(&HTTPStatusError{StatusCode: statusCode, Status: http.StatusText(statusCode)}) {
			t.Fatalf("expected %d to be non-retriable", statusCode)
		}
	}
}

func TestServeHTTPReturnsExpectedUploadStatuses(t *testing.T) {
	config := &Config{
		Path:               "/x",
		XPaddingBytes:      &RangeConfig{From: 1, To: 1},
		ScMaxEachPostBytes: &RangeConfig{From: 1, To: 1},
	}
	tests := []struct {
		name       string
		setup      func(*requestHandler)
		method     string
		target     string
		body       string
		statusCode int
	}{
		{
			name:       "invalid-seq",
			method:     http.MethodPost,
			target:     "http://example.com/x/badseq/not-a-number?x_padding=X",
			body:       "",
			statusCode: http.StatusBadRequest,
		},
		{
			name: "closed-session",
			setup: func(handler *requestHandler) {
				handler.closedSessions.Store("gone", time.Now())
			},
			method:     http.MethodPost,
			target:     "http://example.com/x/gone/0?x_padding=X",
			body:       "x",
			statusCode: http.StatusGone,
		},
		{
			name: "duplicate-stream-reader",
			setup: func(handler *requestHandler) {
				session, err := handler.upsertSession("dup")
				if err != nil {
					t.Fatalf("unexpected upsert error: %v", err)
				}
				if err := session.uploadQueue.Push(Packet{Reader: io.NopCloser(strings.NewReader(""))}); err != nil {
					t.Fatalf("unexpected queue push error: %v", err)
				}
			},
			method:     http.MethodPost,
			target:     "http://example.com/x/dup?x_padding=X",
			body:       "x",
			statusCode: http.StatusConflict,
		},
		{
			name:       "oversized-upload",
			method:     http.MethodPost,
			target:     "http://example.com/x/large/0?x_padding=X",
			body:       "ab",
			statusCode: http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newRequestHandlerForTest(config)
			if test.setup != nil {
				test.setup(handler)
			}

			request := httptest.NewRequest(test.method, test.target, bytes.NewBufferString(test.body))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.statusCode {
				t.Fatalf("expected status %d, got %d", test.statusCode, recorder.Code)
			}
		})
	}
}

func TestUploadQueueCloseUnblocksBlockedPushAndRead(t *testing.T) {
	queue := NewUploadQueue(1)
	if err := queue.Push(Packet{Payload: []byte("a"), Seq: 0}); err != nil {
		t.Fatalf("unexpected initial push error: %v", err)
	}

	pushErrCh := make(chan error, 1)
	go func() {
		pushErrCh <- queue.Push(Packet{Payload: []byte("b"), Seq: 1})
	}()

	readErrCh := make(chan error, 1)
	emptyQueue := NewUploadQueue(1)
	go func() {
		buf := make([]byte, 1)
		_, err := emptyQueue.Read(buf)
		readErrCh <- err
	}()

	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- queue.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("unexpected close error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue.Close")
	}

	if err := emptyQueue.Close(); err != nil {
		t.Fatalf("unexpected emptyQueue close error: %v", err)
	}

	select {
	case err := <-pushErrCh:
		if !stderrors.Is(err, errUploadQueueClosed) {
			t.Fatalf("expected blocked push to return errUploadQueueClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked push to exit")
	}

	select {
	case err := <-readErrCh:
		if err != io.EOF {
			t.Fatalf("expected blocked read to return EOF after close, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked read to exit")
	}
}
