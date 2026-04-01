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

func TestConfiguredXmuxMergesBalancedDefaultsForPartialOverrides(t *testing.T) {
	xmuxConfig := getConfiguredXmux(&Config{
		BehaviorProfile: BehaviorProfileBalanced,
		Xmux: &XmuxConfig{
			WarmConnections: 2,
		},
	}, "2")

	if xmuxConfig.WarmConnections != 2 {
		t.Fatalf("expected warmConnections to be preserved, got %d", xmuxConfig.WarmConnections)
	}
	if xmuxConfig.GetNormalizedMaxConcurrency().From != 1 || xmuxConfig.GetNormalizedMaxConcurrency().To != 2 {
		t.Fatalf("expected balanced maxConcurrency defaults to remain, got %+v", xmuxConfig.GetNormalizedMaxConcurrency())
	}
	if xmuxConfig.GetNormalizedHMaxRequestTimes().From != 400 || xmuxConfig.GetNormalizedHMaxRequestTimes().To != 800 {
		t.Fatalf("expected balanced request defaults to remain, got %+v", xmuxConfig.GetNormalizedHMaxRequestTimes())
	}
	if xmuxConfig.GetNormalizedHMaxReusableSecs().From != 1200 || xmuxConfig.GetNormalizedHMaxReusableSecs().To != 2400 {
		t.Fatalf("expected balanced reusable defaults to remain, got %+v", xmuxConfig.GetNormalizedHMaxReusableSecs())
	}
}

func TestConfiguredXmuxMergesLegacyDefaultsForPartialOverrides(t *testing.T) {
	xmuxConfig := getConfiguredXmux(&Config{
		Xmux: &XmuxConfig{
			WarmConnections: 1,
		},
	}, "2")

	if xmuxConfig.WarmConnections != 1 {
		t.Fatalf("expected warmConnections to be preserved, got %d", xmuxConfig.WarmConnections)
	}
	if xmuxConfig.GetNormalizedMaxConcurrency().From != 1 || xmuxConfig.GetNormalizedMaxConcurrency().To != 1 {
		t.Fatalf("expected legacy maxConcurrency default to remain, got %+v", xmuxConfig.GetNormalizedMaxConcurrency())
	}
	if xmuxConfig.GetNormalizedHMaxRequestTimes().From != 600 || xmuxConfig.GetNormalizedHMaxRequestTimes().To != 900 {
		t.Fatalf("expected legacy request defaults to remain, got %+v", xmuxConfig.GetNormalizedHMaxRequestTimes())
	}
	if xmuxConfig.GetNormalizedHMaxReusableSecs().From != 1800 || xmuxConfig.GetNormalizedHMaxReusableSecs().To != 3000 {
		t.Fatalf("expected legacy reusable defaults to remain, got %+v", xmuxConfig.GetNormalizedHMaxReusableSecs())
	}
}

func TestConfiguredXmuxMaxConnectionsOverrideDoesNotRetainDefaultMaxConcurrency(t *testing.T) {
	xmuxConfig := getConfiguredXmux(&Config{
		BehaviorProfile: BehaviorProfileBalanced,
		Xmux: &XmuxConfig{
			MaxConnections: &RangeConfig{From: 4, To: 4},
		},
	}, "2")

	if xmuxConfig.MaxConcurrency != nil {
		t.Fatalf("expected explicit maxConnections to suppress default maxConcurrency, got %+v", xmuxConfig.GetNormalizedMaxConcurrency())
	}
	if xmuxConfig.GetNormalizedMaxConnections().From != 4 || xmuxConfig.GetNormalizedMaxConnections().To != 4 {
		t.Fatalf("expected maxConnections override to be preserved, got %+v", xmuxConfig.GetNormalizedMaxConnections())
	}
	if xmuxConfig.GetNormalizedHMaxRequestTimes().From != 400 || xmuxConfig.GetNormalizedHMaxRequestTimes().To != 800 {
		t.Fatalf("expected balanced request defaults to remain, got %+v", xmuxConfig.GetNormalizedHMaxRequestTimes())
	}
}
