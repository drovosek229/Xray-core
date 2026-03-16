package splithttp

import (
	"context"
	"crypto/rand"
	"math"
	"math/big"
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
	defer m.access.Unlock()

	m.removeUnusableClientsLocked(ctx)
	m.scheduleWarmRefillLocked()

	if len(m.xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because xmuxClients is empty")
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return xmuxClient
	}

	if m.connections > 0 && len(m.xmuxClients) < int(m.connections) {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConnections was not hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return xmuxClient
	}

	xmuxClients := make([]*XmuxClient, 0)
	if m.concurrency > 0 {
		for _, xmuxClient := range m.xmuxClients {
			if xmuxClient.OpenUsage.Load() < m.concurrency {
				xmuxClients = append(xmuxClients, xmuxClient)
			}
		}
	} else {
		xmuxClients = m.xmuxClients
	}

	if len(xmuxClients) == 0 {
		errors.LogDebug(ctx, "XMUX: creating xmuxClient because maxConcurrency was hit, xmuxClients = ", len(m.xmuxClients))
		xmuxClient := m.newXmuxClient()
		m.scheduleWarmRefillLocked()
		return xmuxClient
	}

	i, _ := rand.Int(rand.Reader, big.NewInt(int64(len(xmuxClients))))
	xmuxClient := xmuxClients[i.Int64()]
	if xmuxClient.leftUsage > 0 {
		xmuxClient.leftUsage -= 1
	}
	m.scheduleWarmRefillLocked()
	return xmuxClient
}

func (m *XmuxManager) removeUnusableClientsLocked(ctx context.Context) {
	for i := 0; i < len(m.xmuxClients); {
		xmuxClient := m.xmuxClients[i]
		if !m.isUsableClientLocked(xmuxClient, time.Now()) {
			errors.LogDebug(ctx, "XMUX: removing xmuxClient, IsClosed() = ", xmuxClient.XmuxConn.IsClosed(),
				", OpenUsage = ", xmuxClient.OpenUsage.Load(),
				", leftUsage = ", xmuxClient.leftUsage,
				", LeftRequests = ", xmuxClient.LeftRequests.Load(),
				", UnreusableAt = ", xmuxClient.UnreusableAt)
			m.xmuxClients = append(m.xmuxClients[:i], m.xmuxClients[i+1:]...)
		} else {
			i++
		}
	}
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

		m.removeUnusableClientsLocked(context.Background())
		m.fillWarmClientsLocked(context.Background())
	}()
}
