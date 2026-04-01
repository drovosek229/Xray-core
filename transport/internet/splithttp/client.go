package splithttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drovosek229/Xray-core/common/errors"
	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/common/signal/done"
)

// interface to abstract between use of browser dialer, vs net/http
type DialerClient interface {
	IsClosed() bool

	// ctx, url, sessionId, body, uploadOnly
	OpenStream(context.Context, string, string, io.Reader, bool, *RequestBehavior) (StartedReadCloser, net.Addr, net.Addr, error)

	// ctx, url, sessionId, seqStr, body, contentLength
	PostPacket(context.Context, string, string, string, io.Reader, int64, *RequestBehavior) error
}

// implements splithttp.DialerClient in terms of direct network connections
type DefaultDialerClient struct {
	transportConfig *Config
	client          *http.Client
	closed          atomic.Bool
	httpVersion     string
	h1UploadMu      sync.Mutex
	h1UploadConn    *H1Conn
	dialUploadConn  func(ctxInner context.Context) (net.Conn, error)
}

type StartedReadCloser interface {
	io.ReadCloser
	WaitStart() error
	CloseWithError(error) error
}

type readyReadCloser struct {
	io.ReadCloser
}

func (r *readyReadCloser) WaitStart() error {
	return nil
}

func (r *readyReadCloser) CloseWithError(err error) error {
	return r.Close()
}

func (c *DefaultDialerClient) IsClosed() bool {
	return c.closed.Load()
}

type streamOpenResult struct {
	reader io.ReadCloser
	err    error
}

func (c *DefaultDialerClient) OpenStream(ctx context.Context, url string, sessionId string, body io.Reader, uploadOnly bool, behavior *RequestBehavior) (StartedReadCloser, net.Addr, net.Addr, error) {
	// this is done when the TCP/UDP connection to the server was established,
	// and we can unblock the Dial function and print correct net addresses in
	// logs
	gotConn := done.New()
	var remoteAddr net.Addr
	var localAddr net.Addr
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			remoteAddr = connInfo.Conn.RemoteAddr()
			localAddr = connInfo.Conn.LocalAddr()
			gotConn.Close()
		},
	})

	method := "GET" // stream-down
	if body != nil {
		method = c.transportConfig.GetNormalizedUplinkHTTPMethod() // stream-up/one
	}
	req, _ := http.NewRequestWithContext(context.WithoutCancel(ctx), method, url, body)
	c.transportConfig.FillStreamRequest(req, sessionId, "", behavior)

	resultCh := make(chan streamOpenResult, 1)
	go func() {
		resp, err := c.client.Do(req)
		if err != nil {
			c.closed.Store(true)
			errors.LogInfoInner(ctx, err, "failed to "+method+" "+url)
			gotConn.Close()
			resultCh <- streamOpenResult{err: err}
			return
		}
		if resp.StatusCode != 200 {
			err = &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			resultCh <- streamOpenResult{err: err}
			return
		}
		if uploadOnly {
			resultCh <- streamOpenResult{}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close() // if it is called immediately, the upload will be interrupted also
			return
		}
		resultCh <- streamOpenResult{reader: resp.Body}
	}()

	wrc := &WaitReadCloser{Wait: make(chan struct{})}

	if body == nil && !uploadOnly {
		result := <-resultCh
		if result.err != nil {
			wrc.SetError(result.err)
			return nil, remoteAddr, localAddr, result.err
		}
		wrc.Set(result.reader)
		return wrc, remoteAddr, localAddr, nil
	}

	go func() {
		result := <-resultCh
		if result.err != nil {
			wrc.SetError(result.err)
			return
		}
		wrc.Set(result.reader)
	}()

	<-gotConn.Wait()
	return wrc, remoteAddr, localAddr, nil
}

func (c *DefaultDialerClient) PostPacket(ctx context.Context, url string, sessionId string, seqStr string, body io.Reader, contentLength int64, behavior *RequestBehavior) error {
	method := c.transportConfig.GetNormalizedUplinkHTTPMethod()
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), method, url, body)
	if err != nil {
		return err
	}
	req.ContentLength = contentLength
	if err := c.transportConfig.FillPacketRequest(req, sessionId, seqStr, behavior); err != nil {
		return err
	}

	if c.httpVersion != "1.1" {
		startedAt := time.Now()
		resp, err := c.client.Do(req)
		if err != nil {
			c.closed.Store(true)
			return err
		}

		io.Copy(io.Discard, resp.Body)
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
		}
		behavior.RecordUpload(int32(contentLength), time.Since(startedAt))
	} else {
		var bufferedRequest []byte
		if req.Body != nil && req.GetBody == nil {
			bufferedRequest, err = buildHTTPRequestBytes(req)
			if err != nil {
				c.closed.Store(true)
				return err
			}
		}

		c.h1UploadMu.Lock()
		defer c.h1UploadMu.Unlock()

		for attempt := 0; attempt < 2; attempt++ {
			h1UploadConn, err := c.getOrCreateH1UploadConn(ctx)
			if err != nil {
				return err
			}

			if err := c.drainH1Responses(req); err != nil {
				c.closeH1UploadConn()
				if attempt == 0 {
					continue
				}
				c.closed.Store(true)
				return err
			}

			startedAt := time.Now()
			if bufferedRequest != nil {
				if _, err := h1UploadConn.ReqBufWriter.Write(bufferedRequest); err != nil {
					c.closeH1UploadConn()
					if attempt == 0 {
						continue
					}
					c.closed.Store(true)
					return err
				}
			} else {
				if attempt > 0 && req.GetBody != nil {
					req.Body, err = req.GetBody()
					if err != nil {
						c.closed.Store(true)
						return err
					}
				}
				if err := req.Write(h1UploadConn.ReqBufWriter); err != nil {
					c.closeH1UploadConn()
					if attempt == 0 {
						continue
					}
					c.closed.Store(true)
					return err
				}
			}
			if err := h1UploadConn.ReqBufWriter.Flush(); err != nil {
				c.closeH1UploadConn()
				if attempt == 0 {
					continue
				}
				c.closed.Store(true)
				return err
			}
			h1UploadConn.UnreadedResponsesCount++

			resp, err := http.ReadResponse(h1UploadConn.RespBufReader, req)
			if err != nil {
				c.closeH1UploadConn()
				if attempt == 0 {
					continue
				}
				c.closed.Store(true)
				return fmt.Errorf("error while reading response: %s", err.Error())
			}
			h1UploadConn.UnreadedResponsesCount--

			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.Close {
				c.closeH1UploadConn()
			}
			if resp.StatusCode != 200 {
				return &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
			}
			behavior.RecordUpload(int32(contentLength), time.Since(startedAt))
			return nil
		}
	}

	return nil
}

func buildHTTPRequestBytes(req *http.Request) ([]byte, error) {
	requestBuff := bytes.NewBuffer(nil)
	if err := req.Write(requestBuff); err != nil {
		return nil, err
	}
	return requestBuff.Bytes(), nil
}

type HTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *HTTPStatusError) Error() string {
	return "bad status code: " + e.Status
}

func (c *DefaultDialerClient) getOrCreateH1UploadConn(ctx context.Context) (*H1Conn, error) {
	if c.h1UploadConn != nil {
		return c.h1UploadConn, nil
	}
	newConn, err := c.dialUploadConn(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	c.h1UploadConn = NewH1Conn(newConn)
	return c.h1UploadConn, nil
}

func (c *DefaultDialerClient) drainH1Responses(req *http.Request) error {
	for c.h1UploadConn != nil && c.h1UploadConn.UnreadedResponsesCount > 0 {
		resp, err := http.ReadResponse(c.h1UploadConn.RespBufReader, req)
		if err != nil {
			return fmt.Errorf("error while draining response: %s", err.Error())
		}
		c.h1UploadConn.UnreadedResponsesCount--
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.Close {
			c.closeH1UploadConn()
			return nil
		}
	}
	return nil
}

func (c *DefaultDialerClient) closeH1UploadConn() {
	if c.h1UploadConn == nil {
		return
	}
	c.h1UploadConn.Close()
	c.h1UploadConn = nil
}

type WaitReadCloser struct {
	Wait      chan struct{}
	readyOnce sync.Once
	mu        sync.Mutex
	reader    io.ReadCloser
	startErr  error
	readErr   error
	closed    bool
}

func (w *WaitReadCloser) Set(rc io.ReadCloser) {
	var toClose io.ReadCloser
	var signalReady bool

	w.mu.Lock()
	switch {
	case w.closed || w.reader != nil || w.startErr != nil:
		toClose = rc
	default:
		w.reader = rc
		signalReady = true
	}
	w.mu.Unlock()

	if signalReady {
		w.signalReady()
	}
	if toClose != nil {
		_ = toClose.Close()
	}
}

func (w *WaitReadCloser) SetError(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}

	var reader io.ReadCloser
	var signalReady bool

	w.mu.Lock()
	if !w.closed {
		w.closed = true
		reader = w.reader
		w.reader = nil
		if w.readErr == nil {
			w.readErr = err
		}
		if w.startErr == nil && w.Wait != nil {
			w.startErr = err
		}
		signalReady = true
	}
	w.mu.Unlock()

	if signalReady {
		w.signalReady()
	}
	if reader != nil {
		_ = reader.Close()
	}
}

func (w *WaitReadCloser) WaitStart() error {
	if w.Wait != nil {
		<-w.Wait
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startErr
}

func (w *WaitReadCloser) Read(b []byte) (int, error) {
	if w.Wait != nil {
		<-w.Wait
	}

	w.mu.Lock()
	reader := w.reader
	readErr := w.readErr
	w.mu.Unlock()

	if reader == nil {
		if readErr != nil {
			return 0, readErr
		}
		return 0, io.ErrClosedPipe
	}

	n, err := reader.Read(b)
	if err != nil && n == 0 {
		w.mu.Lock()
		readErr = w.readErr
		w.mu.Unlock()
		if readErr != nil {
			return 0, readErr
		}
	}
	return n, err
}

func (w *WaitReadCloser) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	reader := w.reader
	w.mu.Unlock()

	w.signalReady()
	if reader != nil {
		return reader.Close()
	}
	return nil
}

func (w *WaitReadCloser) CloseWithError(err error) error {
	if err == nil {
		return w.Close()
	}

	var reader io.ReadCloser

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	reader = w.reader
	w.reader = nil
	if w.readErr == nil {
		w.readErr = err
	}
	if w.startErr == nil && w.Wait != nil {
		w.startErr = err
	}
	w.mu.Unlock()

	w.signalReady()
	if reader != nil {
		return reader.Close()
	}
	return nil
}

func (w *WaitReadCloser) signalReady() {
	if w.Wait == nil {
		return
	}
	w.readyOnce.Do(func() {
		close(w.Wait)
	})
}
