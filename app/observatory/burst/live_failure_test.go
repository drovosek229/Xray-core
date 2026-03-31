package burst

import (
	"context"
	stderrors "errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drovosek229/Xray-core/app/observatory"
	"github.com/drovosek229/Xray-core/common/signal/done"
	feature_outbound "github.com/drovosek229/Xray-core/features/outbound"
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

func TestRuntimeFailureBackoffKeepsOverlayUntilHealthyObservationAndExpiry(t *testing.T) {
	observer := &Observer{
		hp: newTestHealthPing(),
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(80 * time.Millisecond),
			MaxBackoff:  int64(320 * time.Millisecond),
		},
	}
	observer.finished = done.New()
	_ = observer.finished.Close()
	observer.setStatusSnapshot([]*observatory.OutboundStatus{
		{
			Alive:       true,
			Delay:       20,
			OutboundTag: "node-a",
		},
	})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	observer.refreshSnapshot()

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || statuses[0].Alive {
		t.Fatalf("expected node-a to remain ejected before any healthy observation, got %+v", statuses)
	}

	observer.hp.PutResult("node-a", 25*time.Millisecond)
	observer.markLiveFailureHealthy("node-a", time.Now())
	observer.refreshSnapshot()

	response, err = observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses = response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || statuses[0].Alive {
		t.Fatalf("expected node-a to remain ejected until backoff expires, got %+v", statuses)
	}

	time.Sleep(100 * time.Millisecond)

	response, err = observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses = response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || !statuses[0].Alive {
		t.Fatalf("expected node-a to recover after healthy observation and backoff expiry, got %+v", statuses)
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
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(80 * time.Millisecond),
			MaxBackoff:  int64(320 * time.Millisecond),
		},
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
			!statuses[0].Alive &&
			statuses[0].LastErrorReason == "request failed" &&
			statuses[0].LastFailureTime != 0
	}, "expected successful reprobe to keep node-a ejected until backoff expires")

	time.Sleep(100 * time.Millisecond)

	response, err = observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses = response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || !statuses[0].Alive || statuses[0].LastErrorReason != "" || statuses[0].LastFailureTime == 0 {
		t.Fatalf("expected node-a to restore after backoff expiry, got %+v", statuses)
	}
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

func TestRuntimeFailureBackoffGrowsAndClamps(t *testing.T) {
	observer := &Observer{
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(20 * time.Millisecond),
			MaxBackoff:  int64(50 * time.Millisecond),
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed once")
	first := observer.failures["node-a"]
	assertBackoffNear(t, time.Until(first.backoffUntil), 20*time.Millisecond)
	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 1 {
		t.Fatalf("expected first failure streak to be 1, got %d", streak)
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed twice")
	second := observer.failures["node-a"]
	assertBackoffNear(t, time.Until(second.backoffUntil), 40*time.Millisecond)
	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 2 {
		t.Fatalf("expected second failure streak to be 2, got %d", streak)
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed thrice")
	third := observer.failures["node-a"]
	assertBackoffNear(t, time.Until(third.backoffUntil), 50*time.Millisecond)
	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 3 {
		t.Fatalf("expected third failure streak to be 3, got %d", streak)
	}
}

func TestRuntimeFailureHealthyProbeDoesNotRefreshFailureEpisode(t *testing.T) {
	observer := &Observer{
		hp: newTestHealthPing(),
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(80 * time.Millisecond),
			MaxBackoff:  int64(320 * time.Millisecond),
		},
	}
	observer.finished = done.New()
	_ = observer.finished.Close()
	observer.setStatusSnapshot([]*observatory.OutboundStatus{{
		Alive:       true,
		Delay:       20,
		OutboundTag: "node-a",
	}})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	failedTryTime := observer.failures["node-a"].lastTryTime

	observer.hp.PutResult("node-a", 25*time.Millisecond)
	observer.markLiveFailureHealthy("node-a", time.Now())
	observer.refreshSnapshot()

	if refreshedTryTime := observer.failures["node-a"].lastTryTime; refreshedTryTime != failedTryTime {
		t.Fatalf("expected healthy reprobe to keep failure episode timestamp %d, got %d", failedTryTime, refreshedTryTime)
	}
}

func TestRuntimeFailureBackoffDecaysAfterRecovery(t *testing.T) {
	observer := &Observer{
		hp: newTestHealthPing(),
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(40 * time.Millisecond),
			MaxBackoff:  int64(320 * time.Millisecond),
		},
	}
	observer.finished = done.New()
	_ = observer.finished.Close()
	observer.setStatusSnapshot([]*observatory.OutboundStatus{{
		Alive:       true,
		Delay:       20,
		OutboundTag: "node-a",
	}})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed once")
	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed twice")
	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 2 {
		t.Fatalf("expected pre-recovery streak 2, got %d", streak)
	}

	observer.hp.PutResult("node-a", 25*time.Millisecond)
	observer.markLiveFailureHealthy("node-a", time.Now())
	observer.statusLock.Lock()
	failure := observer.failures["node-a"]
	failure.backoffUntil = time.Now().Add(-time.Millisecond)
	observer.failures["node-a"] = failure
	observer.statusLock.Unlock()

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || !statuses[0].Alive {
		t.Fatalf("expected node-a to recover before decay test, got %+v", statuses)
	}

	time.Sleep(45 * time.Millisecond)
	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed after brief recovery")

	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 2 {
		t.Fatalf("expected recovered streak to decay to 2 before re-entry, got %d", streak)
	}
	assertBackoffNear(t, time.Until(observer.failures["node-a"].backoffUntil), 80*time.Millisecond)
}

func TestRuntimeFailureBackoffDoesNotClearWithoutHealthyObservation(t *testing.T) {
	observer := &Observer{
		hp: newTestHealthPing(),
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(40 * time.Millisecond),
			MaxBackoff:  int64(160 * time.Millisecond),
		},
	}
	observer.finished = done.New()
	_ = observer.finished.Close()
	observer.setStatusSnapshot([]*observatory.OutboundStatus{
		{
			Alive:       true,
			Delay:       20,
			OutboundTag: "node-a",
		},
	})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	observer.refreshSnapshot()

	observer.statusLock.Lock()
	failure := observer.failures["node-a"]
	failure.backoffUntil = time.Now().Add(-time.Millisecond)
	observer.failures["node-a"] = failure
	observer.statusLock.Unlock()

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || statuses[0].Alive {
		t.Fatalf("expected node-a to stay ejected without a healthy observation, got %+v", statuses)
	}
}

func TestRecordOutboundFailureIgnoresInactiveOutbound(t *testing.T) {
	manager := &burstTestHandlerSelectorManager{}
	manager.SetSelected([]string{"node-b"})

	observer := &Observer{
		config: &Config{SubjectSelector: []string{"node"}},
		hp:     newTestHealthPing(),
		ohm:    manager,
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(40 * time.Millisecond),
			MaxBackoff:  int64(160 * time.Millisecond),
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")

	if len(observer.failures) != 0 {
		t.Fatalf("expected inactive outbound failures to be ignored, got %+v", observer.failures)
	}
	if len(observer.lastFailureTimes) != 0 {
		t.Fatalf("expected no failure times for inactive outbounds, got %+v", observer.lastFailureTimes)
	}
	if len(observer.runtimeFailureHistories) != 0 {
		t.Fatalf("expected no runtime history for inactive outbounds, got %+v", observer.runtimeFailureHistories)
	}
}

func TestRefreshSnapshotPrunesRemovedOutboundState(t *testing.T) {
	observer := &Observer{
		hp: newTestHealthPing(),
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(40 * time.Millisecond),
			MaxBackoff:  int64(160 * time.Millisecond),
		},
		failures: map[string]liveFailure{
			"node-a": {lastErrorReason: "request failed", lastFailureTime: 123},
		},
		lastFailureTimes: map[string]int64{
			"node-a": 123,
			"node-b": 456,
		},
		runtimeFailureHistories: map[string]runtimeFailureHistory{
			"node-a": {failureStreak: 2},
		},
	}
	observer.hp.PutResult("node-a", 20*time.Millisecond)
	observer.hp.PutResult("node-b", 30*time.Millisecond)
	observer.hp.Cleanup([]string{"node-b"})

	observer.refreshSnapshotForTags([]string{"node-b"})

	response, err := observer.GetObservation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.(*observatory.ObservationResult).Status
	if len(statuses) != 1 || statuses[0].OutboundTag != "node-b" {
		t.Fatalf("expected only active outbound node-b after pruning, got %+v", statuses)
	}
	if _, found := observer.failures["node-a"]; found {
		t.Fatal("expected removed outbound live failure to be pruned")
	}
	if _, found := observer.lastFailureTimes["node-a"]; found {
		t.Fatal("expected removed outbound failure time to be pruned")
	}
	if _, found := observer.runtimeFailureHistories["node-a"]; found {
		t.Fatal("expected removed outbound runtime history to be pruned")
	}
}

func TestRemovedOutboundPendingReprobeDoesNotRecreateState(t *testing.T) {
	manager := &burstTestHandlerSelectorManager{}
	manager.SetSelected([]string{"node-a"})

	var calls atomic.Int32
	observer := &Observer{
		config:       &Config{SubjectSelector: []string{"node"}},
		hp:           newTestHealthPing(),
		ohm:          manager,
		reprobeDelay: 20 * time.Millisecond,
		reprobeFn: func(string) (time.Duration, error) {
			calls.Add(1)
			return 25 * time.Millisecond, nil
		},
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(40 * time.Millisecond),
			MaxBackoff:  int64(160 * time.Millisecond),
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed")
	manager.SetSelected([]string{"node-b"})

	waitForCondition(t, time.Second, func() bool {
		return !observer.hasPendingReprobe("node-a")
	}, "expected pending reprobe to finish after removal")

	if calls.Load() != 0 {
		t.Fatalf("expected removed outbound to skip reprobe, got %d reprobes", calls.Load())
	}
	if _, found := observer.failures["node-a"]; found {
		t.Fatal("expected removed outbound live failure to be cleared")
	}
	if _, found := observer.lastFailureTimes["node-a"]; found {
		t.Fatal("expected removed outbound failure time to be cleared")
	}
	if _, found := observer.runtimeFailureHistories["node-a"]; found {
		t.Fatal("expected removed outbound runtime history to be cleared")
	}
	if _, found := observer.hp.Results["node-a"]; found {
		t.Fatal("expected removed outbound reprobe to avoid recreating health results")
	}
}

func TestReintroducedOutboundStartsFreshRuntimeFailureStreak(t *testing.T) {
	manager := &burstTestHandlerSelectorManager{}
	manager.SetSelected([]string{"node-a"})

	observer := &Observer{
		config: &Config{SubjectSelector: []string{"node"}},
		hp:     newTestHealthPing(),
		ohm:    manager,
		runtimeFailure: &RuntimeFailureConfig{
			BaseBackoff: int64(20 * time.Millisecond),
			MaxBackoff:  int64(160 * time.Millisecond),
		},
	}

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed once")
	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed twice")
	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 2 {
		t.Fatalf("expected initial streak 2, got %d", streak)
	}

	manager.SetSelected([]string{"node-b"})
	observer.refreshSnapshotForTags([]string{"node-b"})
	manager.SetSelected([]string{"node-a"})
	observer.refreshSnapshotForTags([]string{"node-a"})

	observer.RecordOutboundFailure(context.Background(), "node-a", "request failed after reintroduction")

	if streak := observer.runtimeFailureHistories["node-a"].failureStreak; streak != 1 {
		t.Fatalf("expected reintroduced outbound to restart at streak 1, got %d", streak)
	}
	assertBackoffNear(t, time.Until(observer.failures["node-a"].backoffUntil), 20*time.Millisecond)
}

type burstTestHandlerSelectorManager struct {
	mu       sync.RWMutex
	selected []string
}

func (*burstTestHandlerSelectorManager) Start() error { return nil }

func (*burstTestHandlerSelectorManager) Close() error { return nil }

func (*burstTestHandlerSelectorManager) Type() interface{} { return feature_outbound.ManagerType() }

func (*burstTestHandlerSelectorManager) GetHandler(string) feature_outbound.Handler { return nil }

func (*burstTestHandlerSelectorManager) GetDefaultHandler() feature_outbound.Handler { return nil }

func (*burstTestHandlerSelectorManager) AddHandler(context.Context, feature_outbound.Handler) error {
	return nil
}

func (*burstTestHandlerSelectorManager) RemoveHandler(context.Context, string) error { return nil }

func (*burstTestHandlerSelectorManager) ListHandlers(context.Context) []feature_outbound.Handler {
	return nil
}

func (m *burstTestHandlerSelectorManager) Select([]string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.selected...)
}

func (m *burstTestHandlerSelectorManager) SetSelected(tags []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selected = append([]string(nil), tags...)
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

func assertBackoffNear(t *testing.T, got, want time.Duration) {
	t.Helper()

	if got < want-15*time.Millisecond || got > want+15*time.Millisecond {
		t.Fatalf("expected backoff near %s, got %s", want, got)
	}
}
