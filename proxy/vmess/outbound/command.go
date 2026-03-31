package outbound

import (
	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/common/protocol"
)

// As a stub command consumer.
func (h *Handler) handleCommand(dest net.Destination, cmd protocol.ResponseCommand) {
	switch cmd.(type) {
	default:
	}
}
