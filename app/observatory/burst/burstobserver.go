package burst

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/signal/done"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"google.golang.org/protobuf/proto"
)

const runtimeFailureReprobeDelay = 250 * time.Millisecond

type Observer struct {
	config *Config
	ctx    context.Context

	statusLock       sync.RWMutex
	status           []*observatory.OutboundStatus
	failures         map[string]liveFailure
	lastFailureTimes map[string]int64
	hp               *HealthPing

	reprobeLock    sync.Mutex
	pendingReprobe map[string]struct{}
	reprobeDelay   time.Duration
	reprobeFn      func(string) (time.Duration, error)

	finished *done.Instance

	ohm outbound.Manager
}

type liveFailure struct {
	lastErrorReason string
	lastTryTime     int64
	lastFailureTime int64
}

func (o *Observer) GetObservation(ctx context.Context) (proto.Message, error) {
	o.statusLock.RLock()
	status := cloneObservationStatuses(o.status)
	failures := cloneLiveFailures(o.failures)
	o.statusLock.RUnlock()

	return &observatory.ObservationResult{Status: applyLiveFailures(status, failures)}, nil
}

func (o *Observer) createResult() []*observatory.OutboundStatus {
	var result []*observatory.OutboundStatus
	o.statusLock.RLock()
	lastFailureTimes := cloneFailureTimes(o.lastFailureTimes)
	o.statusLock.RUnlock()
	o.hp.access.Lock()
	defer o.hp.access.Unlock()
	tags := make([]string, 0, len(o.hp.Results))
	for name := range o.hp.Results {
		tags = append(tags, name)
	}
	sort.Strings(tags)
	for _, name := range tags {
		value := o.hp.Results[name]
		stats := value.GetWithCache()
		lastTryTime, lastSeenTime := value.LatestTimes()
		status := observatory.OutboundStatus{
			Alive:           stats.All != stats.Fail,
			Delay:           stats.Average.Milliseconds(),
			LastErrorReason: "",
			OutboundTag:     name,
			LastSeenTime:    unixOrZero(lastSeenTime),
			LastTryTime:     unixOrZero(lastTryTime),
			LastFailureTime: lastFailureTimes[name],
			HealthPing: &observatory.HealthPingMeasurementResult{
				All:       int64(stats.All),
				Fail:      int64(stats.Fail),
				Deviation: int64(stats.Deviation),
				Average:   int64(stats.Average),
				Max:       int64(stats.Max),
				Min:       int64(stats.Min),
			},
		}
		result = append(result, &status)
	}
	return result
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func (o *Observer) Type() interface{} {
	return extension.ObservatoryType()
}

func (o *Observer) Start() error {
	if o.config != nil && len(o.config.SubjectSelector) != 0 {
		o.finished = done.New()
		o.hp.StartScheduler(o.selectOutbounds, o.refreshSnapshot)
	}
	return nil
}

func (o *Observer) Close() error {
	if o.finished != nil {
		o.hp.StopScheduler()
		return o.finished.Close()
	}
	return nil
}

func (o *Observer) selectOutbounds() ([]string, error) {
	hs, ok := o.ohm.(outbound.HandlerSelector)
	if !ok {
		return nil, errors.New("outbound.Manager is not a HandlerSelector")
	}
	return hs.Select(o.config.SubjectSelector), nil
}

func (o *Observer) refreshSnapshot() {
	o.setStatusSnapshot(o.createResult())
}

func (o *Observer) setStatusSnapshot(status []*observatory.OutboundStatus) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	for _, snapshot := range status {
		if snapshot != nil && snapshot.Alive {
			delete(o.failures, snapshot.OutboundTag)
		}
	}
	o.status = status
}

func (o *Observer) RecordOutboundFailure(ctx context.Context, outboundTag, reason string) {
	if outboundTag == "" {
		return
	}
	if reason == "" {
		reason = "runtime request failed"
	}

	o.setLiveFailure(outboundTag, reason)
	o.scheduleFailureReprobe(outboundTag)
}

func (o *Observer) setLiveFailure(outboundTag, reason string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	if o.failures == nil {
		o.failures = make(map[string]liveFailure)
	}
	if o.lastFailureTimes == nil {
		o.lastFailureTimes = make(map[string]int64)
	}
	failedAt := time.Now().UnixMilli()
	o.lastFailureTimes[outboundTag] = failedAt
	o.failures[outboundTag] = liveFailure{
		lastErrorReason: reason,
		lastTryTime:     time.Now().Unix(),
		lastFailureTime: failedAt,
	}
}

func (o *Observer) clearLiveFailure(outboundTag string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	if len(o.failures) == 0 {
		return
	}
	delete(o.failures, outboundTag)
}

func (o *Observer) scheduleFailureReprobe(outboundTag string) {
	if !o.canRunFailureReprobe() {
		return
	}
	if o.finished != nil && o.finished.Done() {
		return
	}

	o.reprobeLock.Lock()
	if o.pendingReprobe == nil {
		o.pendingReprobe = make(map[string]struct{})
	}
	if _, found := o.pendingReprobe[outboundTag]; found {
		o.reprobeLock.Unlock()
		return
	}
	o.pendingReprobe[outboundTag] = struct{}{}
	o.reprobeLock.Unlock()

	go o.runFailureReprobe(outboundTag)
}

func (o *Observer) canRunFailureReprobe() bool {
	return o.hp != nil && o.hp.Settings != nil
}

func (o *Observer) runFailureReprobe(outboundTag string) {
	defer o.finishFailureReprobe(outboundTag)

	if !o.waitForFailureReprobeDelay() {
		return
	}

	delay, err := o.probeFailureOutbound(outboundTag)
	if err != nil {
		o.hp.PutResult(outboundTag, rttFailed)
		o.setLiveFailure(outboundTag, "burst reprobe failed: "+err.Error())
		o.refreshSnapshot()
		return
	}

	o.hp.PutResult(outboundTag, delay)
	o.clearLiveFailure(outboundTag)
	o.refreshSnapshot()
}

func (o *Observer) finishFailureReprobe(outboundTag string) {
	o.reprobeLock.Lock()
	defer o.reprobeLock.Unlock()
	if len(o.pendingReprobe) == 0 {
		return
	}
	delete(o.pendingReprobe, outboundTag)
}

func (o *Observer) waitForFailureReprobeDelay() bool {
	delay := o.reprobeDelay
	if delay <= 0 {
		delay = runtimeFailureReprobeDelay
	}
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-o.doneWait():
		return false
	}
}

func (o *Observer) doneWait() <-chan struct{} {
	if o.finished == nil {
		return nil
	}
	return o.finished.Wait()
}

func (o *Observer) probeFailureOutbound(outboundTag string) (time.Duration, error) {
	if o.reprobeFn != nil {
		return o.reprobeFn(outboundTag)
	}
	if o.hp == nil {
		return 0, errors.New("health ping is not initialized")
	}
	return o.hp.MeasureDelay(outboundTag)
}

func cloneObservationStatuses(statuses []*observatory.OutboundStatus) []*observatory.OutboundStatus {
	clones := make([]*observatory.OutboundStatus, 0, len(statuses))
	for _, status := range statuses {
		if status == nil {
			continue
		}
		cloned := *status
		if status.HealthPing != nil {
			healthPing := *status.HealthPing
			cloned.HealthPing = &healthPing
		}
		clones = append(clones, &cloned)
	}
	return clones
}

func cloneLiveFailures(failures map[string]liveFailure) map[string]liveFailure {
	if len(failures) == 0 {
		return nil
	}
	clones := make(map[string]liveFailure, len(failures))
	for tag, failure := range failures {
		clones[tag] = failure
	}
	return clones
}

func cloneFailureTimes(failureTimes map[string]int64) map[string]int64 {
	if len(failureTimes) == 0 {
		return nil
	}
	clones := make(map[string]int64, len(failureTimes))
	for tag, failedAt := range failureTimes {
		clones[tag] = failedAt
	}
	return clones
}

func applyLiveFailures(statuses []*observatory.OutboundStatus, failures map[string]liveFailure) []*observatory.OutboundStatus {
	if len(failures) == 0 {
		return statuses
	}

	indexByTag := make(map[string]int, len(statuses))
	for idx, status := range statuses {
		if status == nil {
			continue
		}
		indexByTag[status.OutboundTag] = idx
	}

	for tag, failure := range failures {
		if idx, found := indexByTag[tag]; found {
			statuses[idx].Alive = false
			statuses[idx].Delay = rttFailed.Milliseconds()
			statuses[idx].LastErrorReason = failure.lastErrorReason
			statuses[idx].LastTryTime = failure.lastTryTime
			statuses[idx].LastFailureTime = failure.lastFailureTime
			continue
		}

		statuses = append(statuses, &observatory.OutboundStatus{
			Alive:           false,
			Delay:           rttFailed.Milliseconds(),
			LastErrorReason: failure.lastErrorReason,
			OutboundTag:     tag,
			LastTryTime:     failure.lastTryTime,
			LastFailureTime: failure.lastFailureTime,
		})
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].OutboundTag < statuses[j].OutboundTag
	})
	return statuses
}

func New(ctx context.Context, config *Config) (*Observer, error) {
	observer := &Observer{
		config: config,
		ctx:    ctx,
	}
	if err := core.RequireFeatures(ctx, func(om outbound.Manager, rd routing.Dispatcher) {
		observer.ohm = om
		observer.hp = NewHealthPing(ctx, rd, config.PingConfig)
	}); err != nil {
		return nil, errors.New("Cannot get depended features").Base(err)
	}
	return observer, nil
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return New(ctx, config.(*Config))
	}))
}
