package congestion

import (
	"github.com/apernet/quic-go"
	"github.com/drovosek229/Xray-core/transport/internet/hysteria/congestion/bbr"
	"github.com/drovosek229/Xray-core/transport/internet/hysteria/congestion/brutal"
)

func UseBBR(conn *quic.Conn) {
	conn.SetCongestionControl(bbr.NewBbrSender(
		bbr.DefaultClock{},
		bbr.GetInitialPacketSize(conn.RemoteAddr()),
	))
}

func UseBrutal(conn *quic.Conn, tx uint64) {
	conn.SetCongestionControl(brutal.NewBrutalSender(tx))
}
