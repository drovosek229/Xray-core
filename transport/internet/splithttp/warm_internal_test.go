package splithttp

import "testing"

func TestConfiguredXmuxIgnoresWarmConnectionsForHTTP11(t *testing.T) {
	xmuxConfig := getConfiguredXmux(&Config{
		Xmux: &XmuxConfig{
			WarmConnections: 2,
		},
	}, "1.1")

	if xmuxConfig.WarmConnections != 0 {
		t.Fatalf("expected h1 xmux warmConnections to be disabled, got %d", xmuxConfig.WarmConnections)
	}
}

func TestConfiguredXmuxDefaultsKeepAliveWhenWarmConnectionsEnabled(t *testing.T) {
	xmuxConfig := getConfiguredXmux(&Config{
		Xmux: &XmuxConfig{
			WarmConnections: 1,
		},
	}, "2")

	if xmuxConfig.WarmConnections != 1 {
		t.Fatalf("expected warmConnections to round-trip, got %d", xmuxConfig.WarmConnections)
	}
	if xmuxConfig.HKeepAlivePeriod != 30 {
		t.Fatalf("expected keepalive default of 30 seconds, got %d", xmuxConfig.HKeepAlivePeriod)
	}
}
