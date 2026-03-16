package splithttp

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/errors"
)

type XmuxConn interface {
	IsClosed() bool
}

const xmuxHealthSweepInterval = 250 * time.Millisecond

type XmuxClient struct {
	XmuxConn     XmuxConn
	OpenUsage    atomic.Int32
	leftUsage    int32
	LeftRequests atomic.Int32
	UnreusableAt time.Time
}

type xmuxClientSnapshot struct {
	xmuxClients []*XmuxClient
	clientCount uint32
	indexMask   uint32
	powerOfTwo  bool
}

type XmuxManager struct {
	access              sync.Mutex
	xmuxConfig          XmuxConfig
	concurrency         int32
	connections         int32
	warmConnections     int32
	usableCount         int
	newConnFunc         func() XmuxConn
	xmuxClients         []*XmuxClient
	fastXmuxSnapshot    atomic.Pointer[xmuxClientSnapshot]
	fastNextClientIndex atomic.Uint32
	nextClientIndex     int
	sweepDue            bool
	sweepDueFlag        atomic.Bool
	sweepScheduled      bool
	refillScheduled     bool
}

func NewXmuxManager(xmuxConfig XmuxConfig, newConnFunc func() XmuxConn) *XmuxManager {
	concurrency := xmuxConfig.GetNormalizedMaxConcurrency().rand()
	connections := xmuxConfig.GetNormalizedMaxConnections().rand()
	warmConnections := xmuxConfig.GetNormalizedWarmConnections()
	if connections > 0 && warmConnections > connections {
		warmConnections = connections
	}

	manager := &XmuxManager{
		xmuxConfig:      xmuxConfig,
		concurrency:     concurrency,
		connections:     connections,
		warmConnections: warmConnections,
		newConnFunc:     newConnFunc,
		xmuxClients:     make([]*XmuxClient, 0),
	}
	manager.access.Lock()
	manager.fillWarmClientsLocked(context.Background())
	manager.publishXmuxClientsLocked()
	if len(manager.xmuxClients) > 0 {
		manager.scheduleSweepCheckLocked()
	}
	manager.access.Unlock()
	return manager
}

func (m *XmuxManager) publishXmuxClientsLocked() {
	if len(m.xmuxClients) == 0 {
		m.fastXmuxSnapshot.Store(nil)
		return
	}

	snapshot := &xmuxClientSnapshot{xmuxClients: append([]*XmuxClient(nil), m.xmuxClients...)}
	snapshot.clientCount = uint32(len(snapshot.xmuxClients))
	if snapshot.clientCount&(snapshot.clientCount-1) == 0 {
		snapshot.powerOfTwo = true
		snapshot.indexMask = snapshot.clientCount - 1
	}
	m.fastXmuxSnapshot.Store(snapshot)
}

func (m *XmuxManager) newXmuxClient() *XmuxClient {
	xmuxClient := &XmuxClient{
		XmuxConn:  m.newConnFunc(),
		leftUsage: -1,
	}
	if x := m.xmuxConfig.GetNormalizedCMaxReuseTimes().rand(); x > 0 {
		xmuxClient.leftUsage = x - 1
	}
	xmuxClient.LeftRequests.Store(math.MaxInt32)
	if x := m.xmuxConfig.GetNormalizedHMaxRequestTimes().rand(); x > 0 {
		xmuxClient.LeftRequests.Store(x)
	}
	if x := m.xmuxConfig.GetNormalizedHMaxReusableSecs().rand(); x > 0 {
		xmuxClient.UnreusableAt = time.Now().Add(time.Duration(x) * time.Second)
	}
	m.xmuxClients = append(m.xmuxClients, xmuxClient)
	m.usableCount += 1
	m.publishXmuxClientsLocked()
	return xmuxClient
}

func (m *XmuxManager) GetXmuxClient(ctx context.Context) *XmuxClient { // when locking
	if xmuxClient := m.tryGetXmuxClientFast(); xmuxClient != nil {
		return xmuxClient
	}

	m.access.Lock()

	xmuxClient := m.pickXmuxClientLocked(ctx)

	if len(m.xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because xmuxClients is empty")
		xmuxClient = m.newXmuxClient()
		m.scheduleSweepCheckLocked()
		m.scheduleWarmRefillLocked()
		m.access.Unlock()
		return xmuxClient
	}

	if m.connections > 0 && len(m.xmuxClients) < int(m.connections) {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConnections was not hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		m.scheduleSweepCheckLocked()
		m.scheduleWarmRefillLocked()
		m.access.Unlock()
		return xmuxClient
	}

	if xmuxClient == nil {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConcurrency was hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		m.scheduleSweepCheckLocked()
		m.scheduleWarmRefillLocked()
		m.access.Unlock()
		return xmuxClient
	}

	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage -= 1
		if xmuxClient.leftUsage == 0 && m.usableCount > 0 {
			m.usableCount -= 1
			m.scheduleWarmRefillLocked()
		}
	}
	m.access.Unlock()
	return xmuxClient
}

func (m *XmuxManager) tryGetXmuxClientFast() *XmuxClient {
	if m.sweepDueFlag.Load() {
		return nil
	}
	if m.xmuxConfig.HMaxReusableSecs != nil && m.xmuxConfig.HMaxReusableSecs.To > 0 {
		return nil
	}
	if m.xmuxConfig.HMaxRequestTimes != nil && m.xmuxConfig.HMaxRequestTimes.To > 0 {
		return nil
	}
	if m.xmuxConfig.CMaxReuseTimes != nil && m.xmuxConfig.CMaxReuseTimes.To > 0 {
		return nil
	}

	snapshot := m.fastXmuxSnapshot.Load()
	if snapshot == nil {
		return nil
	}
	xmuxClients := snapshot.xmuxClients
	clientCount := int(snapshot.clientCount)
	if m.connections > 0 && clientCount < int(m.connections) {
		return nil
	}

	nextIndex := m.fastNextClientIndex.Add(1) - 1
	if snapshot.powerOfTwo {
		start := int(nextIndex & snapshot.indexMask)
		if m.concurrency <= 0 {
			xmuxClient := xmuxClients[start]
			if xmuxClient.XmuxConn.IsClosed() {
				return nil
			}
			return xmuxClient
		}
		for scanned := uint32(0); scanned < snapshot.clientCount; scanned++ {
			xmuxClient := xmuxClients[int((nextIndex+scanned)&snapshot.indexMask)]
			if xmuxClient.OpenUsage.Load() >= m.concurrency {
				continue
			}
			if xmuxClient.XmuxConn.IsClosed() {
				return nil
			}
			return xmuxClient
		}
		return nil
	}

	start := int(nextIndex % snapshot.clientCount)
	if m.concurrency <= 0 {
		for scanned := 0; scanned < clientCount; scanned++ {
			index := start + scanned
			if index >= clientCount {
				index -= clientCount
			}
			xmuxClient := xmuxClients[index]
			if xmuxClient.XmuxConn.IsClosed() {
				return nil
			}
			return xmuxClient
		}
		return nil
	}

	for scanned := 0; scanned < clientCount; scanned++ {
		index := start + scanned
		if index >= clientCount {
			index -= clientCount
		}
		xmuxClient := xmuxClients[index]
		if xmuxClient.OpenUsage.Load() >= m.concurrency {
			continue
		}
		if xmuxClient.XmuxConn.IsClosed() {
			return nil
		}
		return xmuxClient
	}
	return nil
}

func (m *XmuxManager) pickXmuxClientLocked(ctx context.Context) *XmuxClient {
	if m.sweepDue {
		now := time.Now()
		xmuxClient := m.sweepAndPickAvailableClientLocked(ctx, now)
		m.sweepDue = false
		m.sweepDueFlag.Store(false)
		m.scheduleSweepCheckLocked()
		return xmuxClient
	}

	if m.xmuxConfig.HMaxReusableSecs != nil && m.xmuxConfig.HMaxReusableSecs.To > 0 {
		return m.pickAvailableClientWithDeadlineLocked(ctx, time.Now())
	}
	if m.xmuxConfig.HMaxRequestTimes == nil || m.xmuxConfig.HMaxRequestTimes.To <= 0 {
		if m.xmuxConfig.CMaxReuseTimes == nil || m.xmuxConfig.CMaxReuseTimes.To <= 0 {
			if m.concurrency <= 0 {
				return m.pickAvailableClientClosedOnlyLocked(ctx)
			}
			return m.pickAvailableClientWithoutDeadlineNoReuseLocked(ctx)
		}
		if m.concurrency <= 0 {
			return m.pickAvailableClientWithoutDeadlineOrConcurrencyLocked(ctx)
		}
		return m.pickAvailableClientWithoutDeadlineLocked(ctx)
	}
	return m.pickAvailableClientWithDeadlineLocked(ctx, time.Time{})
}

func (m *XmuxManager) sweepAndPickAvailableClientLocked(ctx context.Context, now time.Time) *XmuxClient {
	kept := m.xmuxClients[:0]
	cursor := m.nextClientIndex
	var selected *XmuxClient
	selectedIndex := -1
	var wrapped *XmuxClient
	wrappedIndex := -1
	usableCount := 0

	for _, xmuxClient := range m.xmuxClients {
		if !m.isUsableClientLocked(xmuxClient, now) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			continue
		}

		keptIndex := len(kept)
		kept = append(kept, xmuxClient)
		usableCount++

		if m.concurrency > 0 && xmuxClient.OpenUsage.Load() >= m.concurrency {
			continue
		}
		if keptIndex >= cursor {
			if selected == nil {
				selected = xmuxClient
				selectedIndex = keptIndex
			}
		} else if wrapped == nil {
			wrapped = xmuxClient
			wrappedIndex = keptIndex
		}
	}

	removedCount := len(m.xmuxClients) - len(kept)
	clear(m.xmuxClients[len(kept):])
	m.xmuxClients = kept
	m.usableCount = usableCount
	m.publishXmuxClientsLocked()
	if removedCount > 0 {
		m.scheduleWarmRefillLocked()
	}

	if selected == nil {
		selected = wrapped
		selectedIndex = wrappedIndex
	}

	if len(kept) == 0 {
		m.nextClientIndex = 0
	} else if selectedIndex >= 0 {
		m.nextClientIndex = selectedIndex + 1
		if m.nextClientIndex >= len(kept) {
			m.nextClientIndex = 0
		}
	} else if m.nextClientIndex >= len(kept) {
		m.nextClientIndex = 0
	}

	return selected
}

func (m *XmuxManager) removeXmuxClientLocked(index int) {
	last := len(m.xmuxClients) - 1
	copy(m.xmuxClients[index:], m.xmuxClients[index+1:])
	m.xmuxClients[last] = nil
	m.xmuxClients = m.xmuxClients[:last]
	if m.usableCount > 0 {
		m.usableCount -= 1
		m.scheduleWarmRefillLocked()
	}
}

func (m *XmuxManager) pickAvailableClientClosedOnlyLocked(ctx context.Context) *XmuxClient {
	xmuxClients := m.xmuxClients
	clientCount := len(xmuxClients)
	if clientCount == 0 {
		m.nextClientIndex = 0
		m.usableCount = 0
		return nil
	}

	start := m.nextClientIndex
	if start >= clientCount {
		start = 0
	}

	for scanned := 0; scanned < clientCount && clientCount > 0; {
		if start >= clientCount {
			start = 0
		}
		xmuxClient := xmuxClients[start]
		scanned += 1
		if xmuxClient.XmuxConn.IsClosed() {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			xmuxClients = m.xmuxClients
			clientCount = len(xmuxClients)
			scanned -= 1
			if clientCount == 0 {
				m.nextClientIndex = 0
				m.usableCount = 0
				return nil
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= clientCount {
			m.nextClientIndex = 0
		}
		return xmuxClient
	}

	if m.nextClientIndex >= clientCount {
		m.nextClientIndex = 0
	}
	if m.usableCount > clientCount {
		m.usableCount = clientCount
	}
	return nil
}

func (m *XmuxManager) pickAvailableClientWithoutDeadlineOrConcurrencyLocked(ctx context.Context) *XmuxClient {
	clientCount := len(m.xmuxClients)
	if clientCount == 0 {
		m.nextClientIndex = 0
		m.usableCount = 0
		return nil
	}

	start := m.nextClientIndex
	if start >= clientCount {
		start = 0
	}

	for scanned := 0; scanned < clientCount && clientCount > 0; {
		if start >= clientCount {
			start = 0
		}
		xmuxClient := m.xmuxClients[start]
		scanned += 1
		if xmuxClient.XmuxConn.IsClosed() || xmuxClient.leftUsage == 0 {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			clientCount -= 1
			scanned -= 1
			if clientCount == 0 {
				m.nextClientIndex = 0
				m.usableCount = 0
				return nil
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= clientCount {
			m.nextClientIndex = 0
		}
		return xmuxClient
	}

	if m.nextClientIndex >= clientCount {
		m.nextClientIndex = 0
	}
	if m.usableCount > clientCount {
		m.usableCount = clientCount
	}
	return nil
}

func (m *XmuxManager) pickAvailableClientWithoutDeadlineNoReuseLocked(ctx context.Context) *XmuxClient {
	xmuxClients := m.xmuxClients
	clientCount := len(xmuxClients)
	if clientCount == 0 {
		m.nextClientIndex = 0
		m.usableCount = 0
		return nil
	}

	start := m.nextClientIndex
	if start >= clientCount {
		start = 0
	}

	for scanned := 0; scanned < clientCount && clientCount > 0; {
		if start >= clientCount {
			start = 0
		}
		xmuxClient := xmuxClients[start]
		openUsage := xmuxClient.OpenUsage.Load()
		scanned += 1
		if m.concurrency > 0 && openUsage >= m.concurrency {
			start += 1
			continue
		}
		if xmuxClient.XmuxConn.IsClosed() {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", openUsage,
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			xmuxClients = m.xmuxClients
			clientCount = len(xmuxClients)
			scanned -= 1
			if clientCount == 0 {
				m.nextClientIndex = 0
				m.usableCount = 0
				return nil
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= clientCount {
			m.nextClientIndex = 0
		}
		return xmuxClient
	}

	if m.nextClientIndex >= clientCount {
		m.nextClientIndex = 0
	}
	if m.usableCount > clientCount {
		m.usableCount = clientCount
	}
	return nil
}

func (m *XmuxManager) pickAvailableClientWithoutDeadlineLocked(ctx context.Context) *XmuxClient {
	clientCount := len(m.xmuxClients)
	if clientCount == 0 {
		m.nextClientIndex = 0
		m.usableCount = 0
		return nil
	}

	start := m.nextClientIndex
	if start >= clientCount {
		start = 0
	}

	for scanned := 0; scanned < clientCount && clientCount > 0; {
		if start >= clientCount {
			start = 0
		}
		xmuxClient := m.xmuxClients[start]
		openUsage := xmuxClient.OpenUsage.Load()
		scanned += 1
		if m.concurrency > 0 && openUsage >= m.concurrency {
			start += 1
			continue
		}
		if xmuxClient.XmuxConn.IsClosed() || xmuxClient.leftUsage == 0 {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", openUsage,
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			clientCount -= 1
			scanned -= 1
			if clientCount == 0 {
				m.nextClientIndex = 0
				m.usableCount = 0
				return nil
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= clientCount {
			m.nextClientIndex = 0
		}
		return xmuxClient
	}

	if m.nextClientIndex >= clientCount {
		m.nextClientIndex = 0
	}
	if m.usableCount > clientCount {
		m.usableCount = clientCount
	}
	return nil
}

func (m *XmuxManager) pickAvailableClientWithDeadlineLocked(ctx context.Context, now time.Time) *XmuxClient {
	clientCount := len(m.xmuxClients)
	if clientCount == 0 {
		m.nextClientIndex = 0
		m.usableCount = 0
		return nil
	}

	start := m.nextClientIndex
	if start >= clientCount {
		start = 0
	}

	for scanned := 0; scanned < clientCount && clientCount > 0; {
		if start >= clientCount {
			start = 0
		}
		xmuxClient := m.xmuxClients[start]
		openUsage := xmuxClient.OpenUsage.Load()
		scanned += 1
		if m.concurrency > 0 && openUsage >= m.concurrency {
			start += 1
			continue
		}
		if !m.isUsableClientLocked(xmuxClient, now) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", openUsage,
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			clientCount -= 1
			scanned -= 1
			if clientCount == 0 {
				m.nextClientIndex = 0
				m.usableCount = 0
				return nil
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= clientCount {
			m.nextClientIndex = 0
		}
		return xmuxClient
	}

	if m.nextClientIndex >= clientCount {
		m.nextClientIndex = 0
	}
	if m.usableCount > clientCount {
		m.usableCount = clientCount
	}
	return nil
}

func (m *XmuxManager) removeUnusableClientsLocked(ctx context.Context) {
	now := time.Now()
	kept := m.xmuxClients[:0]
	usableCount := 0

	for _, xmuxClient := range m.xmuxClients {
		if !m.isUsableClientLocked(xmuxClient, now) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			continue
		}
		kept = append(kept, xmuxClient)
		usableCount++
	}

	clear(m.xmuxClients[len(kept):])
	m.xmuxClients = kept
	m.usableCount = usableCount
	m.publishXmuxClientsLocked()
}

func (m *XmuxManager) isUsableClientLocked(xmuxClient *XmuxClient, now time.Time) bool {
	if xmuxClient.XmuxConn.IsClosed() || xmuxClient.leftUsage == 0 {
		return false
	}
	if m.xmuxConfig.HMaxRequestTimes != nil && m.xmuxConfig.HMaxRequestTimes.To > 0 && xmuxClient.LeftRequests.Load() <= 0 {
		return false
	}
	if m.xmuxConfig.HMaxReusableSecs != nil && m.xmuxConfig.HMaxReusableSecs.To > 0 && xmuxClient.UnreusableAt != (time.Time{}) && now.After(xmuxClient.UnreusableAt) {
		return false
	}
	return true
}

func (m *XmuxManager) warmUsableCountLocked() int {
	return m.usableCount
}

func (m *XmuxManager) fillWarmClientsLocked(ctx context.Context) {
	if m.warmConnections <= 0 {
		return
	}

	target := int(m.warmConnections)
	for m.warmUsableCountLocked() < target {
		if m.connections > 0 && len(m.xmuxClients) >= int(m.connections) {
			return
		}
		errors.LogDebug(ctx, "XMUX: creating warm xmuxClient, warm target = ", target, ", xmuxClients = ", len(m.xmuxClients))
		m.newXmuxClient()
	}
}

func (m *XmuxManager) scheduleSweepCheckLocked() {
	if m.sweepDue || m.sweepScheduled {
		return
	}

	m.sweepScheduled = true
	time.AfterFunc(xmuxHealthSweepInterval, func() {
		m.access.Lock()
		m.sweepScheduled = false
		m.sweepDue = true
		m.sweepDueFlag.Store(true)
		m.access.Unlock()
	})
}

func (m *XmuxManager) scheduleWarmRefillLocked() {
	if m.warmConnections <= 0 || m.refillScheduled || m.usableCount >= int(m.warmConnections) {
		return
	}

	m.refillScheduled = true
	go func() {
		m.access.Lock()
		defer m.access.Unlock()
		defer func() {
			m.refillScheduled = false
		}()

		m.removeUnusableClientsLocked(context.Background())
		m.fillWarmClientsLocked(context.Background())
	}()
}
