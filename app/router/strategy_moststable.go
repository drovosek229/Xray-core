package router

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
)

const (
	mostStableDefaultTolerance            = 0.2
	mostStableDefaultMinSamples           = 4
	mostStableDefaultHoldDown             = 30 * time.Second
	mostStableDefaultRecoveryObservations = 2
	mostStableSwitchThreshold             = 0.90
	mostStableFailurePenalty              = time.Second
)

type MostStableStrategy struct {
	settings    *StrategyMostStableConfig
	costs       *WeightManager
	fallbackTag string

	observer extension.Observatory
	ctx      context.Context

	stateLock          sync.Mutex
	lastSelected       string
	histories          map[string]*mostStableHistory
	missingFallbackLog sync.Once
}

type mostStableHistory struct {
	lastTryTime     int64
	lastFailureTime int64
	holdDownUntil   time.Time
	recoveryPasses  int32
	needsRecovery   bool
}

type mostStableCandidate struct {
	Tag string

	observed        bool
	alive           bool
	hasHealthPing   bool
	sampleCount     int
	delay           time.Duration
	average         time.Duration
	deviation       time.Duration
	failRatio       float64
	score           float64
	lastTryTime     int64
	lastFailureTime int64

	rejectedByMaxRTT    bool
	rejectedByTolerance bool
	inHoldDown          bool
	recoveryPending     bool
	eligible            bool
}

func NewMostStableStrategy(settings *StrategyMostStableConfig, fallbackTag string) *MostStableStrategy {
	settings = normalizeMostStableConfig(settings)
	return &MostStableStrategy{
		settings:    settings,
		fallbackTag: fallbackTag,
		costs: NewWeightManager(
			settings.Costs, 1,
			func(value, weight float64) float64 {
				return value * weight
			},
		),
		histories: make(map[string]*mostStableHistory),
	}
}

func normalizeMostStableConfig(settings *StrategyMostStableConfig) *StrategyMostStableConfig {
	if settings == nil {
		settings = &StrategyMostStableConfig{}
	}
	normalized := *settings
	if normalized.GetMaxRTT() < 0 {
		normalized.MaxRTT = 0
	}
	if normalized.GetTolerance() <= 0 {
		normalized.Tolerance = mostStableDefaultTolerance
	}
	if normalized.GetTolerance() > 1 {
		normalized.Tolerance = 1
	}
	if normalized.GetMinSamples() <= 0 {
		normalized.MinSamples = mostStableDefaultMinSamples
	}
	if normalized.GetHoldDown() <= 0 {
		normalized.HoldDown = int64(mostStableDefaultHoldDown)
	}
	if normalized.GetRecoveryObservations() <= 0 {
		normalized.RecoveryObservations = mostStableDefaultRecoveryObservations
	}
	return &normalized
}

func (s *MostStableStrategy) InjectContext(ctx context.Context) {
	s.ctx = ctx
	common.Must(core.RequireFeatures(s.ctx, func(observatory extension.Observatory) error {
		s.observer = observatory
		return nil
	}))
	if s.fallbackTag == "" {
		s.missingFallbackLog.Do(func() {
			errors.LogWarning(s.ctx, "moststable balancer configured without fallbackTag; exhausted candidates will fail requests")
		})
	}
}

func (s *MostStableStrategy) GetPrincipleTarget(candidates []string) []string {
	result, err := s.getObservation()
	if err != nil {
		return nil
	}
	rawStates := s.collectCandidates(candidates, result)

	s.stateLock.Lock()
	histories := cloneMostStableHistories(s.histories)
	current := s.lastSelected
	s.stateLock.Unlock()

	selected, ordered := s.evaluate(rawStates, current, histories, false)
	return orderedWithSelected(selected, ordered)
}

func (s *MostStableStrategy) PickOutbound(candidates []string) string {
	result, err := s.getObservation()
	if err != nil {
		return ""
	}
	rawStates := s.collectCandidates(candidates, result)

	s.stateLock.Lock()
	defer s.stateLock.Unlock()
	selected, _ := s.evaluate(rawStates, s.lastSelected, s.histories, true)
	s.lastSelected = selected
	return selected
}

func (s *MostStableStrategy) getObservation() (*observatory.ObservationResult, error) {
	if s.observer == nil {
		errors.LogError(s.ctx, "observer is nil")
		return nil, errors.New("observer is nil")
	}
	observeReport, err := s.observer.GetObservation(s.ctx)
	if err != nil {
		errors.LogInfoInner(s.ctx, err, "cannot get observation")
		return nil, err
	}
	result, ok := observeReport.(*observatory.ObservationResult)
	if !ok {
		return nil, errors.New("unexpected observation result type")
	}
	return result, nil
}

func (s *MostStableStrategy) collectCandidates(candidates []string, result *observatory.ObservationResult) map[string]*mostStableCandidate {
	outbounds := outboundList(candidates)
	states := make(map[string]*mostStableCandidate, len(candidates))
	maxRTT := time.Duration(s.settings.GetMaxRTT())
	tolerance := float64(s.settings.GetTolerance())

	for _, status := range result.Status {
		if !outbounds.contains(status.OutboundTag) {
			continue
		}

		candidate := &mostStableCandidate{
			Tag:             status.OutboundTag,
			observed:        true,
			alive:           status.Alive,
			delay:           time.Duration(status.Delay) * time.Millisecond,
			lastTryTime:     status.GetLastTryTime(),
			lastFailureTime: status.GetLastFailureTime(),
		}

		if status.GetHealthPing() != nil {
			candidate.hasHealthPing = true
			candidate.sampleCount = int(status.GetHealthPing().GetAll())
			candidate.average = time.Duration(status.GetHealthPing().GetAverage())
			candidate.deviation = time.Duration(status.GetHealthPing().GetDeviation())
			if status.GetHealthPing().GetAll() > 0 {
				candidate.failRatio = float64(status.GetHealthPing().GetFail()) / float64(status.GetHealthPing().GetAll())
			}
		} else {
			candidate.sampleCount = 1
			candidate.average = candidate.delay
			candidate.deviation = candidate.delay / 2
		}

		if maxRTT > 0 && candidate.delay >= maxRTT {
			candidate.rejectedByMaxRTT = true
		}
		if tolerance > 0 && candidate.failRatio > tolerance {
			candidate.rejectedByTolerance = true
		}

		candidate.score = s.calculateScore(candidate)
		states[candidate.Tag] = candidate
	}
	return states
}

func (s *MostStableStrategy) calculateScore(candidate *mostStableCandidate) float64 {
	failPenalty := time.Duration(candidate.failRatio * float64(mostStableFailurePenalty))
	base := candidate.average + candidate.deviation + failPenalty
	return s.costs.Apply(candidate.Tag, float64(base))
}

func (s *MostStableStrategy) evaluate(rawStates map[string]*mostStableCandidate, current string, histories map[string]*mostStableHistory, mutate bool) (string, []string) {
	now := time.Now()
	recoveryObservations := s.settings.GetRecoveryObservations()
	holdDown := time.Duration(s.settings.GetHoldDown())

	for tag, candidate := range rawStates {
		history := histories[tag]
		if history == nil {
			history = &mostStableHistory{}
			histories[tag] = history
		}
		history.update(candidate, holdDown, recoveryObservations, now)
		candidate.inHoldDown = now.Before(history.holdDownUntil)
		candidate.recoveryPending = history.needsRecovery
		candidate.eligible = candidate.observed &&
			candidate.alive &&
			!candidate.rejectedByMaxRTT &&
			!candidate.rejectedByTolerance &&
			!candidate.inHoldDown &&
			!candidate.recoveryPending
	}

	eligible := collectMostStableEligible(rawStates)
	sortMostStableCandidates(eligible)
	if len(eligible) == 0 {
		return "", nil
	}

	currentCandidate := rawStates[current]
	if currentCandidate != nil && currentCandidate.eligible {
		bestCandidate := bestMostStableChallenger(eligible, current, int(s.settings.GetMinSamples()))
		if bestCandidate == nil {
			return current, tagsFromMostStableCandidates(eligible)
		}
		if bestCandidate.Tag == current {
			return current, tagsFromMostStableCandidates(eligible)
		}
		if currentCandidate.score > 0 && bestCandidate.score <= currentCandidate.score*mostStableSwitchThreshold {
			return bestCandidate.Tag, tagsFromMostStableCandidates(eligible)
		}
		return current, tagsFromMostStableCandidates(eligible)
	}

	return eligible[0].Tag, tagsFromMostStableCandidates(eligible)
}

func collectMostStableEligible(states map[string]*mostStableCandidate) []*mostStableCandidate {
	eligible := make([]*mostStableCandidate, 0, len(states))
	for _, candidate := range states {
		if candidate.eligible {
			eligible = append(eligible, candidate)
		}
	}
	return eligible
}

func bestMostStableChallenger(eligible []*mostStableCandidate, current string, minSamples int) *mostStableCandidate {
	for _, candidate := range eligible {
		if candidate.Tag == current {
			continue
		}
		if minSamples > 0 && candidate.hasHealthPing && candidate.sampleCount < minSamples {
			continue
		}
		return candidate
	}
	return nil
}

func sortMostStableCandidates(candidates []*mostStableCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.score != right.score {
			return left.score < right.score
		}
		if left.sampleCount != right.sampleCount {
			return left.sampleCount > right.sampleCount
		}
		return left.Tag < right.Tag
	})
}

func tagsFromMostStableCandidates(candidates []*mostStableCandidate) []string {
	tags := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		tags = append(tags, candidate.Tag)
	}
	return tags
}

func orderedWithSelected(selected string, ordered []string) []string {
	if selected == "" || len(ordered) == 0 {
		return ordered
	}
	if ordered[0] == selected {
		return ordered
	}
	result := []string{selected}
	for _, tag := range ordered {
		if tag == selected {
			continue
		}
		result = append(result, tag)
	}
	return result
}

func cloneMostStableHistories(src map[string]*mostStableHistory) map[string]*mostStableHistory {
	if len(src) == 0 {
		return make(map[string]*mostStableHistory)
	}
	cloned := make(map[string]*mostStableHistory, len(src))
	for tag, history := range src {
		if history == nil {
			cloned[tag] = &mostStableHistory{}
			continue
		}
		copy := *history
		cloned[tag] = &copy
	}
	return cloned
}

func (h *mostStableHistory) update(candidate *mostStableCandidate, holdDown time.Duration, recoveryObservations int32, now time.Time) {
	if candidate == nil || !candidate.observed {
		return
	}
	if candidate.lastFailureTime > 0 && candidate.lastFailureTime > h.lastFailureTime {
		h.lastFailureTime = candidate.lastFailureTime
		h.startHoldDown(time.UnixMilli(candidate.lastFailureTime), holdDown)
	}
	if candidate.lastTryTime <= 0 || candidate.lastTryTime == h.lastTryTime {
		return
	}

	h.lastTryTime = candidate.lastTryTime
	if candidate.failedObservation() {
		h.startHoldDown(time.Unix(candidate.lastTryTime, 0), holdDown)
		return
	}
	if !h.needsRecovery || now.Before(h.holdDownUntil) {
		return
	}
	h.recoveryPasses++
	if h.recoveryPasses >= recoveryObservations {
		h.needsRecovery = false
	}
}

func (h *mostStableHistory) startHoldDown(start time.Time, holdDown time.Duration) {
	if start.IsZero() {
		start = time.Now()
	}
	until := start.Add(holdDown)
	if until.After(h.holdDownUntil) {
		h.holdDownUntil = until
	}
	h.recoveryPasses = 0
	h.needsRecovery = true
}

func (c *mostStableCandidate) failedObservation() bool {
	if c == nil {
		return false
	}
	return !c.alive || c.rejectedByMaxRTT || c.rejectedByTolerance
}
