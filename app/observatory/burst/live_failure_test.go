package burst

import (
	"context"
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
}
