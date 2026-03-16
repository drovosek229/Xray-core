package splithttp

import (
	"context"
	"math"
	mathrand "math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/errors"
)

type XmuxConn interface {
	IsClosed() bool
}

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
	refillScheduled bool
	rng             *mathrand.Rand
}

func NewXmuxManager(xmuxConfig XmuxConfig, newConnFunc func() XmuxConn) *XmuxManager {
	manager := &XmuxManager{
		xmuxConfig:      xmuxConfig,
		concurrency:     xmuxConfig.GetNormalizedMaxConcurrency().rand(),
		connections:     xmuxConfig.GetNormalizedMaxConnections().rand(),
		warmConnections: xmuxConfig.GetNormalizedWarmConnections(),
		newConnFunc:     newConnFunc,
		xmuxClients:     make([]*XmuxClient, 0),
		rng:             mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
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
	defer m.access.Unlock()

	xmuxClient, usableCount := m.sweepAndPickAvailableClientLocked(ctx)

	if len(m.xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because xmuxClients is empty")
		xmuxClient = m.newXmuxClient()
		m.scheduleWarmRefillLocked(usableCount + 1)
		return xmuxClient
	}

	if m.connections > 0 && len(m.xmuxClients) < int(m.connections) {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConnections was not hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		m.scheduleWarmRefillLocked(usableCount + 1)
		return xmuxClient
	}

	if xmuxClient == nil {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConcurrency was hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient = m.newXmuxClient()
		m.scheduleWarmRefillLocked(usableCount + 1)
		return xmuxClient
	}

	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage -= 1
		if xmuxClient.leftUsage == 0 {
			usableCount -= 1
		}
	}
	m.scheduleWarmRefillLocked(usableCount)
	return xmuxClient
}

func (m *XmuxManager) sweepAndPickAvailableClientLocked(ctx context.Context) (*XmuxClient, int) {
	now := time.Now()
	kept := m.xmuxClients[:0]
	var selected *XmuxClient
	usableCount := 0
	eligible := 0

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

		if m.concurrency > 0 && xmuxClient.OpenUsage.Load() >= m.concurrency {
			continue
		}
		eligible++
		if m.rng.Intn(eligible) == 0 {
			selected = xmuxClient
		}
	}

	clear(m.xmuxClients[len(kept):])
	m.xmuxClients = kept
	return selected, usableCount
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
