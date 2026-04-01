package splithttp

import (
	"context"
	"crypto/rand"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drovosek229/Xray-core/common/errors"
)

type XmuxConn interface {
	Close() error
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
	lastAccess      time.Time
}

func NewXmuxManager(xmuxConfig XmuxConfig, newConnFunc func() XmuxConn) *XmuxManager {
	manager := &XmuxManager{
		xmuxConfig:      xmuxConfig,
		concurrency:     xmuxConfig.GetNormalizedMaxConcurrency().rand(),
		connections:     xmuxConfig.GetNormalizedMaxConnections().rand(),
		warmConnections: xmuxConfig.GetNormalizedWarmConnections(),
		newConnFunc:     newConnFunc,
		xmuxClients:     make([]*XmuxClient, 0),
		lastAccess:      time.Now(),
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

	return m.getXmuxClientLocked(ctx, false)
}

func (m *XmuxManager) ReserveXmuxClient(ctx context.Context) *XmuxClient {
	m.access.Lock()
	defer m.access.Unlock()

	return m.getXmuxClientLocked(ctx, true)
}

func (m *XmuxManager) getXmuxClientLocked(ctx context.Context, reserve bool) *XmuxClient {
	reserveClient := func(xmuxClient *XmuxClient) *XmuxClient {
		if reserve && xmuxClient != nil {
			xmuxClient.OpenUsage.Add(1)
		}
		return xmuxClient
	}

	now := time.Now()
	m.lastAccess = now
	m.removeUnusableClientsLocked(ctx, now)
	m.scheduleWarmRefillLocked()

	if len(m.xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because xmuxClients is empty")
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return reserveClient(xmuxClient)
	}

	if m.connections > 0 && m.reusableClientCountLocked(now) < int(m.connections) {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConnections was not hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return reserveClient(xmuxClient)
	}

	xmuxClients := make([]*XmuxClient, 0)
	if m.concurrency > 0 {
		for _, xmuxClient := range m.xmuxClients {
			if m.isReusableClientLocked(xmuxClient, now) && xmuxClient.OpenUsage.Load() < m.concurrency {
				xmuxClients = append(xmuxClients, xmuxClient)
			}
		}
	} else {
		for _, xmuxClient := range m.xmuxClients {
			if m.isReusableClientLocked(xmuxClient, now) {
				xmuxClients = append(xmuxClients, xmuxClient)
			}
		}
	}

	if len(xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConcurrency was hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return reserveClient(xmuxClient)
	}

	i, _ := rand.Int(rand.Reader, big.NewInt(int64(len(xmuxClients))))
	xmuxClient := xmuxClients[i.Int64()]
	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage -= 1
	}
	m.scheduleWarmRefillLocked()
	return reserveClient(xmuxClient)
}

func (m *XmuxManager) removeUnusableClientsLocked(ctx context.Context, now time.Time) {
	for i := 0; i < len(m.xmuxClients); {
		xmuxClient := m.xmuxClients[i]
		if !m.shouldKeepClientLocked(xmuxClient, now) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			_ = xmuxClient.XmuxConn.Close()
			m.xmuxClients = append(m.xmuxClients[:i], m.xmuxClients[i+1:]...)
		} else {
			i++
		}
	}
}

func (m *XmuxManager) isReusableClientLocked(xmuxClient *XmuxClient, now time.Time) bool {
	return !xmuxClient.XmuxConn.IsClosed() &&
		xmuxClient.leftUsage != 0 &&
		xmuxClient.LeftRequests.Load() > 0 &&
		(xmuxClient.UnreusableAt == (time.Time{}) || !now.After(xmuxClient.UnreusableAt))
}

func (m *XmuxManager) shouldKeepClientLocked(xmuxClient *XmuxClient, now time.Time) bool {
	return xmuxClient.OpenUsage.Load() > 0 || m.isReusableClientLocked(xmuxClient, now)
}

func (m *XmuxManager) reusableClientCountLocked(now time.Time) int {
	count := 0
	for _, xmuxClient := range m.xmuxClients {
		if m.isReusableClientLocked(xmuxClient, now) {
			count++
		}
	}
	return count
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
		if m.isReusableClientLocked(xmuxClient, now) {
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

func (m *XmuxManager) scheduleWarmRefillLocked() {
	target := m.desiredWarmConnectionsLocked()
	if target == 0 || m.refillScheduled || m.warmUsableCountLocked() >= target {
		return
	}

	m.refillScheduled = true
	go func() {
		m.access.Lock()
		defer m.access.Unlock()
		defer func() {
			m.refillScheduled = false
		}()

		now := time.Now()
		m.lastAccess = now
		m.removeUnusableClientsLocked(context.Background(), now)
		m.fillWarmClientsLocked(context.Background())
	}()
}

func (m *XmuxManager) ShouldEvict(now time.Time, idleFor time.Duration) bool {
	m.access.Lock()
	defer m.access.Unlock()

	if now.Sub(m.lastAccess) < idleFor {
		return false
	}
	for _, xmuxClient := range m.xmuxClients {
		if xmuxClient.OpenUsage.Load() > 0 {
			return false
		}
	}
	return true
}

func (m *XmuxManager) Close() {
	m.access.Lock()
	defer m.access.Unlock()

	for _, xmuxClient := range m.xmuxClients {
		_ = xmuxClient.XmuxConn.Close()
	}
	m.xmuxClients = nil
}
