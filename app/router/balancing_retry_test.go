package router

import (
	"context"
	"testing"

	feature_outbound "github.com/xtls/xray-core/features/outbound"
)

type testHandlerSelectorManager struct {
	selected []string
}

func (*testHandlerSelectorManager) Start() error { return nil }

func (*testHandlerSelectorManager) Close() error { return nil }

func (*testHandlerSelectorManager) Type() interface{} { return feature_outbound.ManagerType() }

func (*testHandlerSelectorManager) GetHandler(string) feature_outbound.Handler { return nil }

func (*testHandlerSelectorManager) GetDefaultHandler() feature_outbound.Handler { return nil }

func (*testHandlerSelectorManager) AddHandler(context.Context, feature_outbound.Handler) error { return nil }

func (*testHandlerSelectorManager) RemoveHandler(context.Context, string) error { return nil }

func (*testHandlerSelectorManager) ListHandlers(context.Context) []feature_outbound.Handler { return nil }

func (m *testHandlerSelectorManager) Select([]string) []string {
	return append([]string(nil), m.selected...)
}

func TestBalancerPickOutboundExcludingSkipsFailedCandidate(t *testing.T) {
	balancer := &Balancer{
		selectors: []string{"proxy"},
		strategy:  &RoundRobinStrategy{},
		ohm: &testHandlerSelectorManager{
			selected: []string{"proxy-a", "proxy-b"},
		},
	}

	got, err := balancer.PickOutboundExcluding([]string{"proxy-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "proxy-b" {
		t.Fatalf("expected exclusion-aware pick to choose proxy-b, got %q", got)
	}
}

func TestBalancerPickOutboundExcludingFallsBackWhenCandidatesExhausted(t *testing.T) {
	balancer := &Balancer{
		selectors:   []string{"proxy"},
		strategy:    &RoundRobinStrategy{},
		fallbackTag: "fallback",
		ohm: &testHandlerSelectorManager{
			selected: []string{"proxy-a", "proxy-b"},
		},
	}

	got, err := balancer.PickOutboundExcluding([]string{"proxy-a", "proxy-b"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("expected fallback tag, got %q", got)
	}
}
