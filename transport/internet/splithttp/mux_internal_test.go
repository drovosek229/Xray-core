package splithttp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type internalClosableXmuxConn struct {
	closed atomic.Bool
}

func (c *internalClosableXmuxConn) IsClosed() bool {
	return c.closed.Load()
}

func (c *internalClosableXmuxConn) xmuxClosedFlag() *atomic.Bool {
	return &c.closed
}

func TestXmuxScheduledSweepRemovesOffCursorClosedClient(t *testing.T) {
	var created atomic.Int32
	createdClients := make([]*internalClosableXmuxConn, 0, 3)

	xmuxManager := NewXmuxManager(XmuxConfig{
		MaxConnections:  &RangeConfig{From: 2, To: 2},
		WarmConnections: 2,
	}, func() XmuxConn {
		client := &internalClosableXmuxConn{}
		createdClients = append(createdClients, client)
		created.Add(1)
		return client
	})

	if created.Load() != 2 {
		t.Fatalf("expected two warm clients to be created up front, got %d", created.Load())
	}

	xmuxManager.access.Lock()
	xmuxManager.nextClientIndex = 1
	xmuxManager.access.Unlock()

	createdClients[0].closed.Store(true)

	if xmuxClient := xmuxManager.GetXmuxClient(context.Background()); xmuxClient == nil {
		t.Fatal("expected immediate fast-path selection to still return a client")
	}
	if created.Load() != 2 {
		t.Fatalf("expected off-cursor closed client to wait for scheduled sweep before refill, got %d created clients", created.Load())
	}

	time.Sleep(xmuxHealthSweepInterval + 50*time.Millisecond)

	if xmuxClient := xmuxManager.GetXmuxClient(context.Background()); xmuxClient == nil {
		t.Fatal("expected scheduled sweep to preserve an available client")
	}
	if created.Load() != 3 {
		t.Fatalf("expected scheduled sweep to remove the off-cursor closed client and create a replacement, got %d created clients", created.Load())
	}
}
