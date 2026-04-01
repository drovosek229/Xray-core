package splithttp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/drovosek229/Xray-core/transport/internet/splithttp"
)

type fakeRoundTripper struct{}

func (f *fakeRoundTripper) Close() error {
	return nil
}

func (f *fakeRoundTripper) IsClosed() bool {
	return false
}

type closableFakeRoundTripper struct {
	closed atomic.Bool
}

func (f *closableFakeRoundTripper) IsClosed() bool {
	return f.closed.Load()
}

func (f *closableFakeRoundTripper) Close() error {
	f.closed.Store(true)
	return nil
}

func TestMaxConnections(t *testing.T) {
	xmuxConfig := XmuxConfig{
		MaxConnections: &RangeConfig{From: 4, To: 4},
	}

	xmuxManager := NewXmuxManager(xmuxConfig, func() XmuxConn {
		return &fakeRoundTripper{}
	})

	xmuxClients := make(map[interface{}]struct{})
	for i := 0; i < 8; i++ {
		xmuxClients[xmuxManager.GetXmuxClient(context.Background())] = struct{}{}
	}

	if len(xmuxClients) != 4 {
		t.Error("did not get 4 distinct clients, got ", len(xmuxClients))
	}
}

func TestCMaxReuseTimes(t *testing.T) {
	xmuxConfig := XmuxConfig{
		CMaxReuseTimes: &RangeConfig{From: 2, To: 2},
	}

	xmuxManager := NewXmuxManager(xmuxConfig, func() XmuxConn {
		return &fakeRoundTripper{}
	})

	xmuxClients := make(map[interface{}]struct{})
	for i := 0; i < 64; i++ {
		xmuxClients[xmuxManager.GetXmuxClient(context.Background())] = struct{}{}
	}

	if len(xmuxClients) != 32 {
		t.Error("did not get 32 distinct clients, got ", len(xmuxClients))
	}
}

func TestMaxConcurrency(t *testing.T) {
	xmuxConfig := XmuxConfig{
		MaxConcurrency: &RangeConfig{From: 2, To: 2},
	}

	xmuxManager := NewXmuxManager(xmuxConfig, func() XmuxConn {
		return &fakeRoundTripper{}
	})

	xmuxClients := make(map[interface{}]struct{})
	for i := 0; i < 64; i++ {
		xmuxClient := xmuxManager.GetXmuxClient(context.Background())
		xmuxClient.OpenUsage.Add(1)
		xmuxClients[xmuxClient] = struct{}{}
	}

	if len(xmuxClients) != 32 {
		t.Error("did not get 32 distinct clients, got ", len(xmuxClients))
	}
}

func TestDefault(t *testing.T) {
	xmuxConfig := XmuxConfig{}

	xmuxManager := NewXmuxManager(xmuxConfig, func() XmuxConn {
		return &fakeRoundTripper{}
	})

	xmuxClients := make(map[interface{}]struct{})
	for i := 0; i < 64; i++ {
		xmuxClient := xmuxManager.GetXmuxClient(context.Background())
		xmuxClient.OpenUsage.Add(1)
		xmuxClients[xmuxClient] = struct{}{}
	}

	if len(xmuxClients) != 1 {
		t.Error("did not get 1 distinct clients, got ", len(xmuxClients))
	}
}

func TestWarmConnectionsCreatesInitialReusableClient(t *testing.T) {
	var created atomic.Int32
	xmuxManager := NewXmuxManager(XmuxConfig{
		WarmConnections: 1,
	}, func() XmuxConn {
		created.Add(1)
		return &closableFakeRoundTripper{}
	})

	xmuxClient := xmuxManager.GetXmuxClient(context.Background())
	if xmuxClient == nil {
		t.Fatal("expected warm xmux client")
	}
	if created.Load() != 1 {
		t.Fatalf("expected exactly one warm client to be created, got %d", created.Load())
	}
}

func TestWarmConnectionsRefillsAfterClosedClient(t *testing.T) {
	var created atomic.Int32
	var createdClients []*closableFakeRoundTripper
	xmuxManager := NewXmuxManager(XmuxConfig{
		MaxConnections:  &RangeConfig{From: 2, To: 2},
		WarmConnections: 2,
	}, func() XmuxConn {
		client := &closableFakeRoundTripper{}
		created.Add(1)
		createdClients = append(createdClients, client)
		return client
	})

	if created.Load() != 2 {
		t.Fatalf("expected two warm clients to be created up front, got %d", created.Load())
	}

	createdClients[0].Close()
	xmuxManager.GetXmuxClient(context.Background())

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if created.Load() >= 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected warm pool refill after closing a client, got %d created clients", created.Load())
}

func TestWarmConnectionsRespectMaxConnectionsCap(t *testing.T) {
	var created atomic.Int32
	NewXmuxManager(XmuxConfig{
		MaxConnections:  &RangeConfig{From: 2, To: 2},
		WarmConnections: 4,
	}, func() XmuxConn {
		created.Add(1)
		return &closableFakeRoundTripper{}
	})

	if created.Load() != 2 {
		t.Fatalf("expected warm pool to honor maxConnections cap, got %d created clients", created.Load())
	}
}

func TestNonReusableClientStaysTrackedUntilReleased(t *testing.T) {
	var created atomic.Int32
	var clients []*closableFakeRoundTripper

	xmuxManager := NewXmuxManager(XmuxConfig{
		MaxConnections: &RangeConfig{From: 1, To: 1},
		CMaxReuseTimes: &RangeConfig{From: 1, To: 1},
	}, func() XmuxConn {
		client := &closableFakeRoundTripper{}
		created.Add(1)
		clients = append(clients, client)
		return client
	})

	inUse := xmuxManager.ReserveXmuxClient(context.Background())
	if created.Load() != 1 {
		t.Fatalf("expected exactly one client after first reservation, got %d", created.Load())
	}

	replacement := xmuxManager.GetXmuxClient(context.Background())
	if replacement == inUse {
		t.Fatal("expected a replacement client after the first one became non-reusable")
	}
	if created.Load() != 2 {
		t.Fatalf("expected a replacement client to be created, got %d", created.Load())
	}
	if clients[0].IsClosed() {
		t.Fatal("expected in-use non-reusable client to stay open")
	}

	inUse.OpenUsage.Add(-1)
	xmuxManager.GetXmuxClient(context.Background())

	if !clients[0].IsClosed() {
		t.Fatal("expected released non-reusable client to be closed on cleanup")
	}
}
