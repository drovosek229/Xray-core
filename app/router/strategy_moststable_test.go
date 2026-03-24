package router

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/extension"
	"google.golang.org/protobuf/proto"
)

func TestMostStableColdStartPrefersLowestStableScore(t *testing.T) {
	strategy := newTestMostStableStrategy(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 50, 10, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", true, 60, 10, 0, 10, 0, 50*time.Millisecond, 15*time.Millisecond),
		),
	})

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected cold start to choose node-a, got %q", got)
	}
}

func TestMostStableKeepsHealthyWinnerForMarginalImprovement(t *testing.T) {
	strategy := newTestMostStableStrategy(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 92, 10, 0, 10, 0, 82*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", true, 100, 10, 0, 10, 0, 90*time.Millisecond, 10*time.Millisecond),
		),
	})
	strategy.lastSelected = "node-b"

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected node-b to remain selected for marginal improvement, got %q", got)
	}
}

func TestMostStableSwitchesWhenChallengerBeatsThreshold(t *testing.T) {
	strategy := newTestMostStableStrategy(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 85, 10, 0, 10, 0, 75*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", true, 100, 10, 0, 10, 0, 90*time.Millisecond, 10*time.Millisecond),
		),
	})
	strategy.lastSelected = "node-b"

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected node-a to replace node-b, got %q", got)
	}
}

func TestMostStableRuntimeFailureStartsHoldDownUntilRecoveryCycle(t *testing.T) {
	failedAt := time.Now().UnixMilli()
	strategy := newTestMostStableStrategy(&sequenceObservatory{
		results: []proto.Message{
			mostStableResult(
				mostStableStatus("node-a", true, 50, 10, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 10, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", false, 99999999, 11, failedAt, 0, 0, 0, 0),
				mostStableStatus("node-b", true, 70, 11, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 12, failedAt, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 12, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 13, failedAt, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 13, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
		},
	})

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected initial winner node-a, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected runtime failure to switch to node-b, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected hold-down to keep node-a out after reprobe, got %q", got)
	}

	time.Sleep(100 * time.Millisecond)
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected node-a to return after hold-down and recovery, got %q", got)
	}
}

func TestMostStableMaxRTTBreachRequiresRecoveryObservations(t *testing.T) {
	strategy := newTestMostStableStrategyWithConfig(&sequenceObservatory{
		results: []proto.Message{
			mostStableResult(
				mostStableStatus("node-a", true, 50, 10, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 10, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 150, 11, 0, 10, 0, 140*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 11, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 12, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 12, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 13, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 13, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
		},
	}, &StrategyMostStableConfig{
		MaxRTT:               int64(100 * time.Millisecond),
		Tolerance:            0.2,
		MinSamples:           1,
		HoldDown:             int64(60 * time.Millisecond),
		RecoveryObservations: 2,
	})

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected initial winner node-a, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected RTT breach to switch to node-b, got %q", got)
	}

	time.Sleep(70 * time.Millisecond)
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected first recovery cycle to keep node-a out, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected second recovery cycle to restore node-a, got %q", got)
	}
}

func TestMostStableToleranceBreachRequiresRecoveryObservations(t *testing.T) {
	strategy := newTestMostStableStrategyWithConfig(&sequenceObservatory{
		results: []proto.Message{
			mostStableResult(
				mostStableStatus("node-a", true, 50, 10, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 10, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 11, 0, 10, 5, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 11, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 12, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 12, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
			mostStableResult(
				mostStableStatus("node-a", true, 50, 13, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
				mostStableStatus("node-b", true, 70, 13, 0, 10, 0, 60*time.Millisecond, 10*time.Millisecond),
			),
		},
	}, &StrategyMostStableConfig{
		Tolerance:            0.2,
		MinSamples:           1,
		HoldDown:             int64(60 * time.Millisecond),
		RecoveryObservations: 2,
	})

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected initial winner node-a, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected tolerance breach to switch to node-b, got %q", got)
	}

	time.Sleep(70 * time.Millisecond)
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected first recovery cycle to keep node-a out, got %q", got)
	}
	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected second recovery cycle to restore node-a, got %q", got)
	}
}

func TestMostStableMinSamplesBlocksUndersampledChallenger(t *testing.T) {
	strategy := newTestMostStableStrategyWithConfig(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 30, 10, 0, 1, 0, 20*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", true, 50, 10, 0, 10, 0, 40*time.Millisecond, 10*time.Millisecond),
		),
	}, &StrategyMostStableConfig{
		Tolerance:            0.2,
		MinSamples:           2,
		HoldDown:             int64(20 * time.Millisecond),
		RecoveryObservations: 1,
	})
	strategy.lastSelected = "node-b"

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-b" {
		t.Fatalf("expected minSamples to keep node-b, got %q", got)
	}
}

func TestMostStableMinSamplesDoesNotBlockBootstrap(t *testing.T) {
	strategy := newTestMostStableStrategyWithConfig(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 30, 10, 0, 1, 0, 20*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", false, 99999999, 10, 0, 0, 0, 0, 0),
		),
	}, &StrategyMostStableConfig{
		Tolerance:            0.2,
		MinSamples:           2,
		HoldDown:             int64(20 * time.Millisecond),
		RecoveryObservations: 1,
	})
	strategy.lastSelected = "node-b"

	if got := strategy.PickOutbound([]string{"node-a", "node-b"}); got != "node-a" {
		t.Fatalf("expected bootstrap to choose undersampled node-a, got %q", got)
	}
}

func TestMostStableReturnsEmptyWhenAllCandidatesIneligible(t *testing.T) {
	strategy := newTestMostStableStrategyWithConfig(&staticObservatory{
		result: mostStableResult(
			mostStableStatus("node-a", true, 150, 10, 0, 10, 0, 140*time.Millisecond, 10*time.Millisecond),
			mostStableStatus("node-b", false, 99999999, 10, 0, 0, 0, 0, 0),
		),
	}, &StrategyMostStableConfig{
		MaxRTT:               int64(100 * time.Millisecond),
		Tolerance:            0.2,
		MinSamples:           1,
		HoldDown:             int64(20 * time.Millisecond),
		RecoveryObservations: 1,
	})

	balancer := &Balancer{
		selectors:   []string{"proxy"},
		strategy:    strategy,
		fallbackTag: "fallback",
		ohm: &testHandlerSelectorManager{
			selected: []string{"node-a", "node-b"},
		},
	}

	got, err := balancer.PickOutbound()
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("expected fallback when all candidates are ineligible, got %q", got)
	}
}

func newTestMostStableStrategy(observer extension.Observatory) *MostStableStrategy {
	return newTestMostStableStrategyWithConfig(observer, &StrategyMostStableConfig{
		Tolerance:            0.2,
		MinSamples:           1,
		HoldDown:             int64(20 * time.Millisecond),
		RecoveryObservations: 1,
	})
}

func newTestMostStableStrategyWithConfig(observer extension.Observatory, config *StrategyMostStableConfig) *MostStableStrategy {
	strategy := NewMostStableStrategy(config, "")
	strategy.ctx = context.Background()
	strategy.observer = observer
	return strategy
}

func mostStableResult(statuses ...*observatory.OutboundStatus) *observatory.ObservationResult {
	return &observatory.ObservationResult{Status: statuses}
}

func mostStableStatus(tag string, alive bool, delayMs int64, lastTryTime int64, lastFailureTime int64, all int64, fail int64, average time.Duration, deviation time.Duration) *observatory.OutboundStatus {
	status := &observatory.OutboundStatus{
		Alive:           alive,
		Delay:           delayMs,
		OutboundTag:     tag,
		LastTryTime:     lastTryTime,
		LastFailureTime: lastFailureTime,
	}
	if all > 0 {
		status.HealthPing = &observatory.HealthPingMeasurementResult{
			All:       all,
			Fail:      fail,
			Average:   int64(average),
			Deviation: int64(deviation),
		}
	}
	return status
}
