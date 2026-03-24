package burst

import (
	"context"
	stderrors "errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/observatory"
)

func TestLiveFailureOverlayMarksOutboundDeadImmediately(t *testing.T) {
	observer := &Observer{}
	observer.setStatusSnapshot([]*observatory.OutboundStatus{
		{
			Alive:        true,
			Delay:        20,
			OutboundTag:  "node-a",
			LastSeenTime: 12345,
		},
	})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Alive {
		t.Fatal("expected live failure overlay to mark node-a dead")
	}
	if statuses[0].LastSeenTime != 12345 {
		t.Fatalf("expected LastSeenTime to be preserved, got %d", statuses[0].LastSeenTime)
	}
	if statuses[0].Delay != rttFailed.Milliseconds() {
		t.Fatalf("expected sentinel delay %d, got %d", rttFailed.Milliseconds(), statuses[0].Delay)
	}
	if statuses[0].LastErrorReason != "request failed" {
		t.Fatalf("expected failure reason to be recorded, got %q", statuses[0].LastErrorReason)
	}
	if statuses[0].LastTryTime == 0 {
		t.Fatal("expected LastTryTime to be refreshed")
	}
	if statuses[0].LastFailureTime == 0 {
		t.Fatal("expected LastFailureTime to be recorded")
	}
}

func TestSuccessfulProbeClearsLiveFailureOverlay(t *testing.T) {
	result := NewHealthPingResult(1, time.Hour)
	result.Put(25 * time.Millisecond)

	observer := &Observer{
		hp: &HealthPing{
			Results: map[string]*HealthPingRTTS{
				"node-a": result,
			},
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	observer.refreshSnapshot()

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if !statuses[0].Alive {
		t.Fatal("expected successful probe to clear live failure overlay")
	}
	if statuses[0].LastErrorReason != "" {
		t.Fatalf("expected failure reason to be cleared, got %q", statuses[0].LastErrorReason)
	}
	if statuses[0].LastFailureTime == 0 {
		t.Fatal("expected LastFailureTime to persist after successful probe")
	}
}

func TestRuntimeFailureReprobeDebouncesDuplicates(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	observer := &Observer{
		hp:           newTestHealthPing(),
		reprobeDelay: 10 * time.Millisecond,
		reprobeFn: func(string) (time.Duration, error) {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return 25 * time.Millisecond, nil
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed again")

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected a reprobe to start")
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed while reprobe in-flight")

	select {
	case <-started:
		t.Fatal("expected duplicate failures to be coalesced into the pending reprobe")
	case <-time.After(40 * time.Millisecond):
	}

	close(release)
	waitForCondition(t, time.Second, func() bool {
		return calls.Load() == 1 && !observer.hasPendingReprobe("node-a")
	}, "expected pending reprobe to finish")

	if calls.Load() != 1 {
		t.Fatalf("expected exactly one reprobe, got %d", calls.Load())
	}
}

func TestSuccessfulReprobeClearsLiveFailureOverlayAsynchronously(t *testing.T) {
	observer := &Observer{
		hp:           newTestHealthPing(),
		reprobeDelay: 30 * time.Millisecond,
		reprobeFn: func(string) (time.Duration, error) {
			return 25 * time.Millisecond, nil
		},
	}
	observer.setStatusSnapshot([]*observatory.OutboundStatus{
		{
			Alive:       true,
			Delay:       20,
			OutboundTag: "node-a",
		},
	})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || statuses[0].Alive {
		t.Fatalf("expected immediate failure overlay before reprobe completes, got %+v", statuses)
	}

	waitForCondition(t, time.Second, func() bool {
		response, err := observer.GetObservation(context.Background())
		if err != nil {
			return false
		}
		statuses := response.(*observatory.ObservationResult).Status
		return len(statuses) == 1 &&
			statuses[0].Alive &&
			statuses[0].LastErrorReason == "" &&
			statuses[0].LastFailureTime != 0
	}, "expected successful reprobe to restore node-a")
}

func TestFailedReprobeKeepsLiveFailureOverlayAndRecordsFailureSample(t *testing.T) {
	observer := &Observer{
		hp:           newTestHealthPing(),
		reprobeDelay: 30 * time.Millisecond,
		reprobeFn: func(string) (time.Duration, error) {
			return 0, stderrors.New("probe failed")
		},
	}
	observer.setStatusSnapshot([]*observatory.OutboundStatus{
		{
			Alive:       true,
			Delay:       20,
			OutboundTag: "node-a",
		},
	})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")

	waitForCondition(t, time.Second, func() bool {
		response, err := observer.GetObservation(context.Background())
		if err != nil {
			return false
		}
		statuses := response.(*observatory.ObservationResult).Status
		return len(statuses) == 1 &&
			!statuses[0].Alive &&
			strings.Contains(statuses[0].LastErrorReason, "burst reprobe failed") &&
			observer.hp.Results["node-a"] != nil
	}, "expected failed reprobe to keep node-a ejected")

	stats := observer.hp.Results["node-a"].Get()
	if stats.All != 1 || stats.Fail != 1 {
		t.Fatalf("expected failed reprobe to add one failed sample, got all=%d fail=%d", stats.All, stats.Fail)
	}
}

func newTestHealthPing() *HealthPing {
	return &HealthPing{
		ctx: context.Background(),
		Settings: &HealthPingSettings{
			Destination:   "https://connectivitycheck.gstatic.com/generate_204",
			Interval:      time.Second,
			SamplingCount: 1,
			Timeout:       time.Second,
			HttpMethod:    "HEAD",
		},
		Results: make(map[string]*HealthPingRTTS),
	}
}

func (o *Observer) hasPendingReprobe(outboundTag string) bool {
	o.reprobeLock.Lock()
	defer o.reprobeLock.Unlock()
	_, found := o.pendingReprobe[outboundTag]
	return found
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal(message)
}
