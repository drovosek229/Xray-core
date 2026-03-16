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

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/signal/done"
)

// interface to abstract between use of browser dialer, vs net/http
type DialerClient interface {
	IsClosed() bool

	// ctx, url, sessionId, body, uploadOnly
	OpenStream(context.Context, string, string, io.Reader, bool, *RequestBehavior) (io.ReadCloser, net.Addr, net.Addr, error)

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

func (c *DefaultDialerClient) IsClosed() bool {
	return c.closed.Load()
}

func (c *DefaultDialerClient) OpenStream(ctx context.Context, url string, sessionId string, body io.Reader, uploadOnly bool, behavior *RequestBehavior) (wrc io.ReadCloser, remoteAddr, localAddr net.Addr, err error) {
	// this is done when the TCP/UDP connection to the server was established,
	// and we can unblock the Dial function and print correct net addresses in
	// logs
	gotConn := done.New()
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

	wrc = &WaitReadCloser{Wait: make(chan struct{})}
	go func() {
		resp, err := c.client.Do(req)
		if err != nil {
			if !uploadOnly { // stream-down is enough
				c.closed.Store(true)
				errors.LogInfoInner(ctx, err, "failed to "+method+" "+url)
			}
			gotConn.Close()
			wrc.Close()
			return
		}
		if resp.StatusCode != 200 && !uploadOnly {
			errors.LogInfo(ctx, "unexpected status ", resp.StatusCode)
		}
		if resp.StatusCode != 200 || uploadOnly { // stream-up
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close() // if it is called immediately, the upload will be interrupted also
			wrc.Close()
			return
		}
		wrc.(*WaitReadCloser).Set(resp.Body)
	}()

	<-gotConn.Wait()
	return
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
		requestBuff := new(bytes.Buffer)
		common.Must(req.Write(requestBuff))

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
			if _, err := h1UploadConn.Write(requestBuff.Bytes()); err != nil {
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
	Wait chan struct{}
	io.ReadCloser
}

func (w *WaitReadCloser) Set(rc io.ReadCloser) {
	w.ReadCloser = rc
	defer func() {
		if recover() != nil {
			rc.Close()
		}
	}()
	close(w.Wait)
}

func (w *WaitReadCloser) Read(b []byte) (int, error) {
	if w.ReadCloser == nil {
		if <-w.Wait; w.ReadCloser == nil {
			return 0, io.ErrClosedPipe
		}
	}
	return w.ReadCloser.Read(b)
}

func (w *WaitReadCloser) Close() error {
	if w.ReadCloser != nil {
		return w.ReadCloser.Close()
	}
	defer func() {
		if recover() != nil && w.ReadCloser != nil {
			w.ReadCloser.Close()
		}
	}()
	close(w.Wait)
	return nil
}
