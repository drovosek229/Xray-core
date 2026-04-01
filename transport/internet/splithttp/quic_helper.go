package splithttp

import (
	"context"
	"math/rand"
	reflect "reflect"

	"github.com/drovosek229/Xray-core/common/errors"
	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/transport/internet"
	"github.com/drovosek229/Xray-core/transport/internet/hysteria/udphop"
)

type packetConnCloser interface {
	Close() error
}

type http3PacketConnSetup struct {
	packetConn net.PacketConn
	udpAddr    *net.UDPAddr
	closers    []packetConnCloser
}

func (s *http3PacketConnSetup) Close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		_ = s.closers[i].Close()
	}
}

type dialSystemFunc func(context.Context, net.Destination, *internet.SocketConfig) (net.Conn, error)

func prepareHTTP3PacketConn(
	ctx context.Context,
	dest net.Destination,
	streamSettings *internet.MemoryStreamConfig,
	quicParams *internet.QuicParams,
	dialSystem dialSystemFunc,
) (*http3PacketConnSetup, error) {
	if quicParams == nil {
		quicParams = &internet.QuicParams{}
	}
	if quicParams.UdpHop == nil {
		quicParams.UdpHop = &internet.UdpHop{}
	}

	setup := &http3PacketConnSetup{}
	addCloser := func(closer packetConnCloser) {
		if closer != nil {
			setup.closers = append(setup.closers, closer)
		}
	}

	udphopDialer := func(addr *net.UDPAddr) (net.PacketConn, error) {
		conn, err := dialSystem(ctx, net.UDPDestination(net.IPAddress(addr.IP), net.Port(addr.Port)), streamSettings.SocketSettings)
		if err != nil {
			errors.LogDebug(context.Background(), "skip hop: failed to dial to dest")
			return nil, errors.New()
		}

		switch c := conn.(type) {
		case *internet.PacketConnWrapper:
			return c.PacketConn, nil
		case *net.UDPConn:
			return c, nil
		default:
			errors.LogDebug(context.Background(), "skip hop: udphop requires being at the outermost level ", reflect.TypeOf(c))
			conn.Close()
			return nil, errors.New()
		}
	}

	var index int
	if len(quicParams.UdpHop.Ports) > 0 {
		index = rand.Intn(len(quicParams.UdpHop.Ports))
		dest.Port = net.Port(quicParams.UdpHop.Ports[index])
	}

	conn, err := dialSystem(ctx, dest, streamSettings.SocketSettings)
	if err != nil {
		return nil, err
	}
	addCloser(conn)

	var udpConn net.PacketConn
	switch c := conn.(type) {
	case *internet.PacketConnWrapper:
		udpConn = c.PacketConn
		setup.udpAddr, err = net.ResolveUDPAddr("udp", c.Dest.String())
	case *net.UDPConn:
		udpConn = c
		setup.udpAddr, err = net.ResolveUDPAddr("udp", c.RemoteAddr().String())
	default:
		udpConn = &internet.FakePacketConn{Conn: c}
		setup.udpAddr, err = net.ResolveUDPAddr("udp", c.RemoteAddr().String())
		if err == nil && len(quicParams.UdpHop.Ports) > 0 {
			setup.Close()
			return nil, errors.New("udphop requires being at the outermost level ", reflect.TypeOf(c))
		}
	}
	if err != nil {
		setup.Close()
		return nil, err
	}

	if len(quicParams.UdpHop.Ports) > 0 {
		addr := &udphop.UDPHopAddr{
			IP:    setup.udpAddr.IP,
			Ports: quicParams.UdpHop.Ports,
		}
		udpConn, err = udphop.NewUDPHopPacketConn(addr, index, quicParams.UdpHop.IntervalMin, quicParams.UdpHop.IntervalMax, udphopDialer, udpConn)
		if err != nil {
			setup.Close()
			return nil, errors.New("udphop err").Base(err)
		}
		addCloser(udpConn)
	}

	if streamSettings.UdpmaskManager != nil {
		udpConn, err = streamSettings.UdpmaskManager.WrapPacketConnClient(udpConn)
		if err != nil {
			setup.Close()
			return nil, errors.New("mask err").Base(err)
		}
		addCloser(udpConn)
	}

	setup.packetConn = udpConn
	return setup, nil
}
