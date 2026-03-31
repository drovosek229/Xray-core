package router

import (
	"context"
	"errors"
	"testing"

	"github.com/xtls/xray-core/app/observatory"
)

func TestLeastPingPrefersLowestObservedAlive(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			result: &observatory.ObservationResult{
				Status: []*observatory.OutboundStatus{
					{OutboundTag: "node-a", Alive: true, Delay: 50},
					{OutboundTag: "node-b", Alive: true, Delay: 20},
					{OutboundTag: "node-c", Alive: false, Delay: 99999999},
				},
			},
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b", "node-c"}); got != "node-b" {
		t.Fatalf("expected leastping to choose lowest alive delay, got %q", got)
	}
}

func TestLeastPingBootstrapsToFirstCandidateWithoutObservations(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			result: &observatory.ObservationResult{},
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected cold-start bootstrap to choose first candidate, got %q", got)
	}
}

func TestLeastPingBootstrapsPastExplicitlyDeadCandidates(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			result: &observatory.ObservationResult{
				Status: []*observatory.OutboundStatus{
					{OutboundTag: "node-a", Alive: false, Delay: 99999999},
				},
			},
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b", "node-c"}); got != "node-b" {
		t.Fatalf("expected bootstrap to skip explicitly dead node-a, got %q", got)
	}
}

func TestLeastPingReturnsEmptyWhenAllCandidatesExplicitlyDead(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			result: &observatory.ObservationResult{
				Status: []*observatory.OutboundStatus{
					{OutboundTag: "node-a", Alive: false, Delay: 99999999},
					{OutboundTag: "node-b", Alive: false, Delay: 99999999},
				},
			},
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "" {
		t.Fatalf("expected no candidate when all are explicitly dead, got %q", got)
	}
}

func TestLeastPingFallsBackToFirstCandidateOnObservationError(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			err: errors.New("observatory unavailable"),
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected error fallback to choose first candidate, got %q", got)
	}
}

func TestLeastPingFallsBackToFirstCandidateOnUnexpectedObservationType(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			result: &observatory.HealthPingMeasurementResult{},
		},
	}

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected unexpected-type fallback to choose first candidate, got %q", got)
	}
}

func TestLeastPingReturnsEmptyForNoCandidatesOnObservationError(t *testing.T) {
	strategy := &LeastPingStrategy{
		ctx: context.Background(),
		observatory: &staticObservatory{
			err: errors.New("observatory unavailable"),
		},
	}

	if got := strategy.PickOutbound(nil); got != "" {
		t.Fatalf("expected empty result without candidates, got %q", got)
	}
}
