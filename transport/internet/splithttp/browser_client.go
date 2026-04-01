package splithttp

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/drovosek229/Xray-core/common/errors"
	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/transport/internet/browser_dialer"
	"github.com/drovosek229/Xray-core/transport/internet/websocket"
)

// BrowserDialerClient implements splithttp.DialerClient in terms of browser dialer
type BrowserDialerClient struct {
	transportConfig *Config
	closed          atomic.Bool
}

func (c *BrowserDialerClient) Close() error {
	return nil
}

func (c *BrowserDialerClient) IsClosed() bool {
	return c.closed.Load() || !browser_dialer.HasBrowserDialer()
}

func (c *BrowserDialerClient) OpenStream(ctx context.Context, url string, sessionId string, body io.Reader, uploadOnly bool, behavior *RequestBehavior) (StartedReadCloser, net.Addr, net.Addr, error) {
	if body != nil {
		return nil, nil, nil, errors.New("bidirectional streaming for browser dialer not implemented yet")
	}

	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, nil, err
	}

	c.transportConfig.FillStreamRequest(request, sessionId, "", behavior)

	conn, err := browser_dialer.DialGet(request.URL.String(), request.Header, request.Cookies())
	dummyAddr := &net.IPAddr{}
	if err != nil {
		c.closed.Store(true)
		return nil, dummyAddr, dummyAddr, err
	}

	return &readyReadCloser{ReadCloser: websocket.NewConnection(conn, dummyAddr, nil, 0)}, conn.RemoteAddr(), conn.LocalAddr(), nil
}

func (c *BrowserDialerClient) PostPacket(ctx context.Context, url string, sessionId string, seqStr string, body io.Reader, contentLength int64, behavior *RequestBehavior) error {
	method := c.transportConfig.GetNormalizedUplinkHTTPMethod()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		c.closed.Store(true)
		return err
	}

	request.ContentLength = contentLength
	err = c.transportConfig.FillPacketRequest(request, sessionId, seqStr, behavior)
	if err != nil {
		c.closed.Store(true)
		return err
	}

	var bytes []byte
	if request.Body != nil {
		bytes, err = io.ReadAll(request.Body)
		if err != nil {
			return err
		}
	}

	err = browser_dialer.DialPacket(method, request.URL.String(), request.Header, request.Cookies(), bytes)
	if err != nil {
		c.closed.Store(true)
		return err
	}

	return nil
}
