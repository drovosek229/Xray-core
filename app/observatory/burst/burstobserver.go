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
const maxDuration = time.Duration(1<<63 - 1)

type Observer struct {
	config *Config
	ctx    context.Context

	statusLock              sync.RWMutex
	activeOutbounds         map[string]struct{}
	status                  []*observatory.OutboundStatus
	failures                map[string]liveFailure
	runtimeFailureHistories map[string]runtimeFailureHistory
	lastFailureTimes        map[string]int64
	hp                      *HealthPing

	reprobeLock    sync.Mutex
	pendingReprobe map[string]struct{}
	reprobeDelay   time.Duration
	reprobeFn      func(string) (time.Duration, error)

	finished *done.Instance

	ohm            outbound.Manager
	runtimeFailure *RuntimeFailureConfig
}

type liveFailure struct {
	lastErrorReason     string
	lastTryTime         int64
	lastFailureTime     int64
	failedAt            time.Time
	backoffUntil        time.Time
	healthySinceFailure bool
}

type runtimeFailureHistory struct {
	failureStreak int32
	recoveredAt   time.Time
}

func (o *Observer) GetObservation(ctx context.Context) (proto.Message, error) {
	o.releaseRecoveredFailures()

	o.statusLock.RLock()
	status := cloneObservationStatuses(o.status)
	failures := cloneLiveFailures(o.failures)
	o.statusLock.RUnlock()

	return &observatory.ObservationResult{Status: applyLiveFailures(status, failures)}, nil
}

func (o *Observer) Check(tag []string) {
	o.hp.Check(tag)
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
		o.hp.StartScheduler(o.selectOutbounds, o.refreshSnapshotForTags)
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

func (o *Observer) refreshSnapshotForTags(tags []string) {
	o.setStatusSnapshot(o.createResult(), tags)
}

func (o *Observer) setStatusSnapshot(status []*observatory.OutboundStatus, activeTags ...[]string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()

	if len(activeTags) != 0 && activeTags[0] != nil {
		activeSet := make(map[string]struct{}, len(activeTags[0]))
		for _, tag := range activeTags[0] {
			activeSet[tag] = struct{}{}
		}
		o.activeOutbounds = activeSet
		o.pruneInactiveStateLocked(activeSet)
	}

	if len(o.failures) != 0 {
		if !o.runtimeFailureEnabled() {
			for _, snapshot := range status {
				if snapshot != nil && snapshot.Alive {
					delete(o.failures, snapshot.OutboundTag)
				}
			}
		} else {
			for tag, observedAt := range o.latestHealthyObservationTimesLocked() {
				failure, found := o.failures[tag]
				if !found || observedAt.Before(failure.failedAt) {
					continue
				}
				failure.healthySinceFailure = true
				o.failures[tag] = failure
			}
			o.releaseRecoveredFailuresLocked(time.Now())
		}
	}
	o.status = status
}

func (o *Observer) RecordOutboundFailure(ctx context.Context, outboundTag, reason string) {
	if outboundTag == "" {
		return
	}
	if !o.shouldTrackOutbound(outboundTag) {
		o.dropOutboundState(outboundTag)
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
	failedAt := time.Now()
	failedAtMillis := failedAt.UnixMilli()
	streak := int32(1)
	if o.runtimeFailureEnabled() {
		if o.runtimeFailureHistories == nil {
			o.runtimeFailureHistories = make(map[string]runtimeFailureHistory)
		}
		streak = o.nextRuntimeFailureStreakLocked(outboundTag, failedAt)
		o.runtimeFailureHistories[outboundTag] = runtimeFailureHistory{
			failureStreak: streak,
		}
	}
	o.lastFailureTimes[outboundTag] = failedAtMillis
	failure := liveFailure{
		lastErrorReason: reason,
		lastTryTime:     failedAt.Unix(),
		lastFailureTime: failedAtMillis,
		failedAt:        failedAt,
	}
	if o.runtimeFailureEnabled() {
		failure.backoffUntil = failedAt.Add(o.computeRuntimeFailureBackoff(streak))
	}
	o.failures[outboundTag] = failure
}

func (o *Observer) clearLiveFailure(outboundTag string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	if len(o.failures) == 0 {
		return
	}
	delete(o.failures, outboundTag)
	o.markRuntimeFailureRecoveredLocked(outboundTag, time.Now())
}

func (o *Observer) noteLiveFailureProbeFailure(outboundTag, reason string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()

	failure, found := o.failures[outboundTag]
	if !found {
		return
	}
	failure.lastErrorReason = reason
	o.failures[outboundTag] = failure
}

func (o *Observer) markLiveFailureHealthy(outboundTag string, observedAt time.Time) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()

	failure, found := o.failures[outboundTag]
	if !found || observedAt.Before(failure.failedAt) {
		return
	}
	failure.healthySinceFailure = true
	o.failures[outboundTag] = failure
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
	if !o.shouldTrackOutbound(outboundTag) {
		o.dropOutboundState(outboundTag)
		return
	}

	delay, err := o.probeFailureOutbound(outboundTag)
	if err != nil {
		if !o.shouldTrackOutbound(outboundTag) {
			o.dropOutboundState(outboundTag)
			return
		}
		o.hp.PutResult(outboundTag, rttFailed)
		o.noteLiveFailureProbeFailure(outboundTag, "burst reprobe failed: "+err.Error())
		o.refreshSnapshot()
		return
	}

	observedAt := time.Now()
	if !o.shouldTrackOutbound(outboundTag) {
		o.dropOutboundState(outboundTag)
		return
	}
	o.hp.PutResult(outboundTag, delay)
	o.markLiveFailureHealthy(outboundTag, observedAt)
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

func cloneActiveOutbounds(src map[string]struct{}) map[string]struct{} {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]struct{}, len(src))
	for tag := range src {
		cloned[tag] = struct{}{}
	}
	return cloned
}

func (o *Observer) runtimeFailureEnabled() bool {
	return o.runtimeFailure != nil && o.runtimeFailure.GetBaseBackoff() > 0
}

func (o *Observer) computeRuntimeFailureBackoff(streak int32) time.Duration {
	if !o.runtimeFailureEnabled() {
		return 0
	}

	backoff := time.Duration(o.runtimeFailure.GetBaseBackoff())
	maxBackoff := time.Duration(o.runtimeFailure.GetMaxBackoff())
	if streak <= 1 || backoff >= maxBackoff {
		return backoff
	}
	for i := int32(1); i < streak && backoff < maxBackoff; i++ {
		if backoff > maxBackoff/2 {
			backoff = maxBackoff
			break
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return backoff
}

func (o *Observer) latestHealthyObservationTimesLocked() map[string]time.Time {
	if len(o.failures) == 0 || o.hp == nil {
		return nil
	}

	o.hp.access.Lock()
	defer o.hp.access.Unlock()

	observed := make(map[string]time.Time, len(o.failures))
	for tag := range o.failures {
		result := o.hp.Results[tag]
		if result == nil {
			continue
		}
		_, lastSeen := result.LatestTimes()
		if !lastSeen.IsZero() {
			observed[tag] = lastSeen
		}
	}
	return observed
}

func (o *Observer) releaseRecoveredFailures() {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	o.releaseRecoveredFailuresLocked(time.Now())
}

func (o *Observer) releaseRecoveredFailuresLocked(now time.Time) {
	if len(o.failures) == 0 {
		return
	}

	for tag, failure := range o.failures {
		if o.canClearLiveFailure(failure, now) {
			delete(o.failures, tag)
			o.markRuntimeFailureRecoveredLocked(tag, now)
		}
	}
}

func (o *Observer) canClearLiveFailure(failure liveFailure, now time.Time) bool {
	if !failure.healthySinceFailure {
		return false
	}
	if !o.runtimeFailureEnabled() {
		return true
	}
	return failure.backoffUntil.IsZero() || !now.Before(failure.backoffUntil)
}

func (o *Observer) nextRuntimeFailureStreakLocked(outboundTag string, failedAt time.Time) int32 {
	history, found := o.runtimeFailureHistories[outboundTag]
	if !found || history.failureStreak <= 0 {
		return 1
	}

	if _, active := o.failures[outboundTag]; active || history.recoveredAt.IsZero() {
		return history.failureStreak + 1
	}

	baseBackoff := time.Duration(o.runtimeFailure.GetBaseBackoff())
	if baseBackoff <= 0 {
		return 1
	}

	decaySteps := int32(failedAt.Sub(history.recoveredAt) / baseBackoff)
	decayedStreak := history.failureStreak - decaySteps
	if decayedStreak < 0 {
		decayedStreak = 0
	}
	return decayedStreak + 1
}

func (o *Observer) markRuntimeFailureRecoveredLocked(outboundTag string, recoveredAt time.Time) {
	if !o.runtimeFailureEnabled() || len(o.runtimeFailureHistories) == 0 {
		return
	}

	history, found := o.runtimeFailureHistories[outboundTag]
	if !found || history.failureStreak <= 0 {
		return
	}
	if !history.recoveredAt.IsZero() && !recoveredAt.After(history.recoveredAt) {
		return
	}
	history.recoveredAt = recoveredAt
	o.runtimeFailureHistories[outboundTag] = history
}

func (o *Observer) pruneInactiveStateLocked(activeSet map[string]struct{}) {
	for tag := range o.failures {
		if _, ok := activeSet[tag]; !ok {
			delete(o.failures, tag)
		}
	}
	for tag := range o.lastFailureTimes {
		if _, ok := activeSet[tag]; !ok {
			delete(o.lastFailureTimes, tag)
		}
	}
	for tag := range o.runtimeFailureHistories {
		if _, ok := activeSet[tag]; !ok {
			delete(o.runtimeFailureHistories, tag)
		}
	}
}

func (o *Observer) dropOutboundState(outboundTag string) {
	o.statusLock.Lock()
	defer o.statusLock.Unlock()
	delete(o.failures, outboundTag)
	delete(o.lastFailureTimes, outboundTag)
	delete(o.runtimeFailureHistories, outboundTag)
	if o.hp != nil {
		o.hp.access.Lock()
		delete(o.hp.Results, outboundTag)
		o.hp.access.Unlock()
	}
}

func (o *Observer) shouldTrackOutbound(outboundTag string) bool {
	if outboundTag == "" {
		return false
	}
	if tags, err := o.selectOutbounds(); err == nil {
		for _, tag := range tags {
			if tag == outboundTag {
				return true
			}
		}
		return false
	}

	o.statusLock.RLock()
	activeSet := cloneActiveOutbounds(o.activeOutbounds)
	o.statusLock.RUnlock()
	if len(activeSet) == 0 {
		return true
	}
	_, ok := activeSet[outboundTag]
	return ok
}

func safeMultiplyDuration(value time.Duration, factor int) time.Duration {
	if value <= 0 || factor <= 0 {
		return 0
	}

	multiplier := time.Duration(factor)
	if value > maxDuration/multiplier {
		return maxDuration
	}
	return value * multiplier
}

func normalizeRuntimeFailureConfig(config *RuntimeFailureConfig) *RuntimeFailureConfig {
	if config == nil || config.GetBaseBackoff() <= 0 {
		return nil
	}

	normalized := *config
	baseBackoff := time.Duration(normalized.GetBaseBackoff())
	maxBackoff := time.Duration(normalized.GetMaxBackoff())
	if maxBackoff <= 0 {
		maxBackoff = safeMultiplyDuration(baseBackoff, 8)
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}

	normalized.BaseBackoff = int64(baseBackoff)
	normalized.MaxBackoff = int64(maxBackoff)
	return &normalized
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
	var runtimeFailure *RuntimeFailureConfig
	if config != nil {
		runtimeFailure = normalizeRuntimeFailureConfig(config.GetRuntimeFailure())
	}

	observer := &Observer{
		config:         config,
		ctx:            ctx,
		runtimeFailure: runtimeFailure,
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
