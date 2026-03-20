package session

import (
	"context"
	"reflect"
	"testing"
)

func TestGetBalancerRetrySnapshotReturnsImmutableExcludedTags(t *testing.T) {
	ctx := ContextWithBalancerRetryState(context.Background())
	ctx = SetBalancerSelection(ctx, BalancerSelectionKindRoute, "balancer", "proxy-a", "proxy-a")
	if !AddBalancerExclusion(ctx, "proxy-b") {
		t.Fatal("failed to add proxy-b exclusion")
	}
	if !AddBalancerExclusion(ctx, "proxy-a") {
		t.Fatal("failed to add proxy-a exclusion")
	}

	snapshot, ok := GetBalancerRetrySnapshot(ctx)
	if !ok {
		t.Fatal("expected retry snapshot")
	}
	if !reflect.DeepEqual(snapshot.ExcludedOutboundTags, []string{"proxy-a", "proxy-b"}) {
		t.Fatalf("unexpected excluded tags: %v", snapshot.ExcludedOutboundTags)
	}

	snapshot.ExcludedOutboundTags[0] = "mutated"

	updatedSnapshot, ok := GetBalancerRetrySnapshot(ctx)
	if !ok {
		t.Fatal("expected updated retry snapshot")
	}
	if !reflect.DeepEqual(updatedSnapshot.ExcludedOutboundTags, []string{"proxy-a", "proxy-b"}) {
		t.Fatalf("expected immutable excluded tags, got %v", updatedSnapshot.ExcludedOutboundTags)
	}
}
