package splithttp

import (
	"context"
	"testing"
)

type benchmarkXmuxConn struct{}

func (benchmarkXmuxConn) IsClosed() bool { return false }

func BenchmarkXmuxManagerGetXmuxClientWarmPool(b *testing.B) {
	ctx := context.Background()
	manager := NewXmuxManager(XmuxConfig{
		MaxConnections:  &RangeConfig{From: 8, To: 8},
		WarmConnections: 8,
	}, func() XmuxConn {
		return benchmarkXmuxConn{}
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetXmuxClient(ctx)
	}
}

func BenchmarkXmuxManagerGetXmuxClientWithConcurrencyFilter(b *testing.B) {
	ctx := context.Background()
	manager := NewXmuxManager(XmuxConfig{
		MaxConnections:  &RangeConfig{From: 16, To: 16},
		MaxConcurrency:  &RangeConfig{From: 2, To: 2},
		WarmConnections: 16,
	}, func() XmuxConn {
		return benchmarkXmuxConn{}
	})

	for i, client := range manager.xmuxClients {
		if i%2 == 0 {
			client.OpenUsage.Store(2)
			continue
		}
		client.OpenUsage.Store(1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetXmuxClient(ctx)
	}
}
