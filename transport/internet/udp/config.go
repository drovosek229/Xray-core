package udp

import (
	"github.com/drovosek229/Xray-core/common"
	"github.com/drovosek229/Xray-core/transport/internet"
)

func init() {
	common.Must(internet.RegisterProtocolConfigCreator(protocolName, func() interface{} {
		return new(Config)
	}))
}
