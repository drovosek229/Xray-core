package router

import (
	"context"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
)

type LeastPingStrategy struct {
	ctx         context.Context
	observatory extension.Observatory
}

func (l *LeastPingStrategy) GetPrincipleTarget(strings []string) []string {
	return []string{l.PickOutbound(strings)}
}

func (l *LeastPingStrategy) InjectContext(ctx context.Context) {
	l.ctx = ctx
	common.Must(core.RequireFeatures(l.ctx, func(observatory extension.Observatory) error {
		l.observatory = observatory
		return nil
	}))
}

func (l *LeastPingStrategy) PickOutbound(strings []string) string {
	if len(strings) == 0 {
		return ""
	}
	if l.observatory == nil {
		errors.LogError(l.ctx, "observer is nil")
		return strings[0]
	}
	observeReport, err := l.observatory.GetObservation(l.ctx)
	if err != nil {
		errors.LogInfoInner(l.ctx, err, "cannot get observer report")
		return strings[0]
	}
	outboundsList := outboundList(strings)
	if result, ok := observeReport.(*observatory.ObservationResult); ok {
		status := result.Status
		leastPing := int64(99999999)
		selectedOutboundName := ""
		explicitlyDead := make(map[string]bool, len(status))
		for _, v := range status {
			if !outboundsList.contains(v.OutboundTag) {
				continue
			}
			if v.Alive && v.Delay < leastPing {
				selectedOutboundName = v.OutboundTag
				leastPing = v.Delay
				continue
			}
			if !v.Alive {
				explicitlyDead[v.OutboundTag] = true
			}
		}
		if selectedOutboundName != "" {
			return selectedOutboundName
		}
		for _, candidate := range strings {
			if !explicitlyDead[candidate] {
				return candidate
			}
		}
		return selectedOutboundName
	}

	// No way to understand observeReport
	return strings[0]
}

type outboundList []string

func (o outboundList) contains(name string) bool {
	for _, v := range o {
		if v == name {
			return true
		}
	}
	return false
}
