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

type XmuxManager struct {
	access          sync.Mutex
	xmuxConfig      XmuxConfig
	concurrency     int32
	connections     int32
	warmConnections int32
	newConnFunc     func() XmuxConn
	xmuxClients     []*XmuxClient
	nextClientIndex int
	nextSweepAt     time.Time
	refillScheduled bool
}

func NewXmuxManager(xmuxConfig XmuxConfig, newConnFunc func() XmuxConn) *XmuxManager {
	manager := &XmuxManager{
		xmuxConfig:      xmuxConfig,
		concurrency:     xmuxConfig.GetNormalizedMaxConcurrency().rand(),
		connections:     xmuxConfig.GetNormalizedMaxConnections().rand(),
		warmConnections: xmuxConfig.GetNormalizedWarmConnections(),
		newConnFunc:     newConnFunc,
		xmuxClients:     make([]*XmuxClient, 0),
	}
	manager.access.Lock()
	manager.fillWarmClientsLocked(context.Background())
	manager.access.Unlock()
	return manager
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
	return xmuxClient
}

func (m *XmuxManager) GetXmuxClient(ctx context.Context) *XmuxClient { // when locking
	m.access.Lock()

	now := time.Now()
	xmuxClient, usableCount := m.pickXmuxClientLocked(ctx, now)

	if len(m.xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because xmuxClients is empty")
		xmuxClient = m.newXmuxClient()
		usableCount += 1
		m.nextSweepAt = now.Add(xmuxHealthSweepInterval)
		m.scheduleWarmRefillLocked(usableCount)
		m.access.Unlock()
		return xmuxClient
	}

	if m.connections > 0 && len(m.xmuxClients) < int(m.connections) {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConnections was not hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		usableCount += 1
		m.nextSweepAt = now.Add(xmuxHealthSweepInterval)
		m.scheduleWarmRefillLocked(usableCount)
		m.access.Unlock()
		return xmuxClient
	}

	if xmuxClient == nil {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConcurrency was hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		usableCount += 1
		m.nextSweepAt = now.Add(xmuxHealthSweepInterval)
		m.scheduleWarmRefillLocked(usableCount)
		m.access.Unlock()
		return xmuxClient
	}

	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage -= 1
		if xmuxClient.leftUsage == 0 {
			usableCount -= 1
		}
	}
	m.scheduleWarmRefillLocked(usableCount)
	m.access.Unlock()
	return xmuxClient
}

func (m *XmuxManager) pickXmuxClientLocked(ctx context.Context, now time.Time) (*XmuxClient, int) {
	if m.nextSweepAt.IsZero() || !now.Before(m.nextSweepAt) {
		xmuxClient, usableCount := m.sweepAndPickAvailableClientLocked(ctx, now)
		m.nextSweepAt = now.Add(xmuxHealthSweepInterval)
		return xmuxClient, usableCount
	}
	return m.pickAvailableClientLocked(ctx, now)
}

func (m *XmuxManager) sweepAndPickAvailableClientLocked(ctx context.Context, now time.Time) (*XmuxClient, int) {
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

	clear(m.xmuxClients[len(kept):])
	m.xmuxClients = kept

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

	return selected, usableCount
}

func (m *XmuxManager) removeXmuxClientLocked(index int) {
	last := len(m.xmuxClients) - 1
	copy(m.xmuxClients[index:], m.xmuxClients[index+1:])
	m.xmuxClients[last] = nil
	m.xmuxClients = m.xmuxClients[:last]
}

func (m *XmuxManager) pickAvailableClientLocked(ctx context.Context, now time.Time) (*XmuxClient, int) {
	usableCount := len(m.xmuxClients)
	if usableCount == 0 {
		m.nextClientIndex = 0
		return nil, 0
	}

	start := m.nextClientIndex
	if start >= len(m.xmuxClients) {
		start = 0
	}

	for scanned := 0; scanned < usableCount && len(m.xmuxClients) > 0; {
		if start >= len(m.xmuxClients) {
			start = 0
		}
		xmuxClient := m.xmuxClients[start]
		if !m.isUsableClientLocked(xmuxClient, now) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.removeXmuxClientLocked(start)
			usableCount -= 1
			if usableCount == 0 {
				m.nextClientIndex = 0
				return nil, 0
			}
			if start < m.nextClientIndex && m.nextClientIndex > 0 {
				m.nextClientIndex -= 1
			}
			continue
		}

		scanned += 1
		if m.concurrency > 0 && xmuxClient.OpenUsage.Load() >= m.concurrency {
			start += 1
			continue
		}

		m.nextClientIndex = start + 1
		if m.nextClientIndex >= len(m.xmuxClients) {
			m.nextClientIndex = 0
		}
		return xmuxClient, usableCount
	}

	if m.nextClientIndex >= len(m.xmuxClients) {
		m.nextClientIndex = 0
	}
	return nil, usableCount
}

func (m *XmuxManager) removeUnusableClientsLocked(ctx context.Context) {
	now := time.Now()
	kept := m.xmuxClients[:0]

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
	}

	clear(m.xmuxClients[len(kept):])
	m.xmuxClients = kept
}

func (m *XmuxManager) isUsableClientLocked(xmuxClient *XmuxClient, now time.Time) bool {
	return !xmuxClient.XmuxConn.IsClosed() &&
		xmuxClient.leftUsage != 0 &&
		xmuxClient.LeftRequests.Load() > 0 &&
		(xmuxClient.UnreusableAt == (time.Time{}) || !now.After(xmuxClient.UnreusableAt))
}

func (m *XmuxManager) desiredWarmConnectionsLocked() int {
	if m.warmConnections <= 0 {
		return 0
	}

	warmConnections := int(m.warmConnections)
	if m.connections > 0 && warmConnections > int(m.connections) {
		warmConnections = int(m.connections)
	}
	return warmConnections
}

func (m *XmuxManager) warmUsableCountLocked() int {
	now := time.Now()
	count := 0
	for _, xmuxClient := range m.xmuxClients {
		if m.isUsableClientLocked(xmuxClient, now) {
			count++
		}
	}
	return count
}

func (m *XmuxManager) fillWarmClientsLocked(ctx context.Context) {
	target := m.desiredWarmConnectionsLocked()
	if target == 0 {
		return
	}

	for m.warmUsableCountLocked() < target {
		if m.connections > 0 && len(m.xmuxClients) >= int(m.connections) {
			return
		}
		errors.LogDebug(ctx, "XMUX: creating warm xmuxClient, warm target = ", target, ", xmuxClients = ", len(m.xmuxClients))
		m.newXmuxClient()
	}
}

func (m *XmuxManager) scheduleWarmRefillLocked(usableCount int) {
	target := m.desiredWarmConnectionsLocked()
	if target == 0 || m.refillScheduled || usableCount >= target {
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
