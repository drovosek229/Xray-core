package session

import (
	"context"
	"sync"

	"github.com/xtls/xray-core/common/ctx"
)

const balancerRetryStateKey ctx.SessionKey = 13

type BalancerSelectionKind string

const (
	BalancerSelectionKindRoute       BalancerSelectionKind = "route_balancer"
	BalancerSelectionKindDialerProxy BalancerSelectionKind = "dialer_proxy_balancer"
)

type BalancerRetrySnapshot struct {
	Kind                 BalancerSelectionKind
	BalancerTag          string
	RetryOwnerTag        string
	SelectedOutboundTag  string
	ExcludedOutboundTags []string
	Retried              bool
}

type balancerRetryState struct {
	access    sync.Mutex
	snapshot  BalancerRetrySnapshot
	exclusions map[string]struct{}
}

func ContextWithBalancerRetryState(ctx context.Context) context.Context {
	if balancerRetryStateFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, balancerRetryStateKey, &balancerRetryState{})
}

func balancerRetryStateFromContext(ctx context.Context) *balancerRetryState {
	if state, ok := ctx.Value(balancerRetryStateKey).(*balancerRetryState); ok {
		return state
	}
	return nil
}

func SetBalancerSelection(ctx context.Context, kind BalancerSelectionKind, balancerTag, retryOwnerTag, selectedOutboundTag string) context.Context {
	ctx = ContextWithBalancerRetryState(ctx)
	UpdateBalancerSelection(ctx, kind, balancerTag, retryOwnerTag, selectedOutboundTag)
	return ctx
}

func UpdateBalancerSelection(ctx context.Context, kind BalancerSelectionKind, balancerTag, retryOwnerTag, selectedOutboundTag string) bool {
	state := balancerRetryStateFromContext(ctx)
	if state == nil {
		return false
	}

	state.access.Lock()
	defer state.access.Unlock()

	if state.snapshot.Kind != kind || state.snapshot.BalancerTag != balancerTag {
		state.snapshot = BalancerRetrySnapshot{
			Kind:        kind,
			BalancerTag: balancerTag,
		}
		state.exclusions = nil
	}

	state.snapshot.RetryOwnerTag = retryOwnerTag
	state.snapshot.SelectedOutboundTag = selectedOutboundTag
	state.snapshot.ExcludedOutboundTags = state.snapshot.ExcludedOutboundTags[:0]
	for tag := range state.exclusions {
		state.snapshot.ExcludedOutboundTags = append(state.snapshot.ExcludedOutboundTags, tag)
	}

	return true
}

func AddBalancerExclusion(ctx context.Context, outboundTag string) bool {
	if outboundTag == "" {
		return false
	}
	state := balancerRetryStateFromContext(ctx)
	if state == nil {
		return false
	}

	state.access.Lock()
	defer state.access.Unlock()
	if state.exclusions == nil {
		state.exclusions = make(map[string]struct{})
	}
	state.exclusions[outboundTag] = struct{}{}
	state.snapshot.ExcludedOutboundTags = state.snapshot.ExcludedOutboundTags[:0]
	for tag := range state.exclusions {
		state.snapshot.ExcludedOutboundTags = append(state.snapshot.ExcludedOutboundTags, tag)
	}
	return true
}

func MarkBalancerRetried(ctx context.Context) bool {
	state := balancerRetryStateFromContext(ctx)
	if state == nil {
		return false
	}

	state.access.Lock()
	defer state.access.Unlock()
	state.snapshot.Retried = true
	return true
}

func GetBalancerRetrySnapshot(ctx context.Context) (BalancerRetrySnapshot, bool) {
	state := balancerRetryStateFromContext(ctx)
	if state == nil {
		return BalancerRetrySnapshot{}, false
	}

	state.access.Lock()
	defer state.access.Unlock()

	snapshot := state.snapshot
	if len(state.exclusions) > 0 {
		snapshot.ExcludedOutboundTags = snapshot.ExcludedOutboundTags[:0]
		for tag := range state.exclusions {
			snapshot.ExcludedOutboundTags = append(snapshot.ExcludedOutboundTags, tag)
		}
	}
	return snapshot, true
}
