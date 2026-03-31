package tagged

import (
	"context"

	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/features/routing"
)

type DialFunc func(ctx context.Context, dispatcher routing.Dispatcher, dest net.Destination, tag string) (net.Conn, error)

var Dialer DialFunc
