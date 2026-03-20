package extension

import (
	"context"

	"github.com/xtls/xray-core/features"
	"google.golang.org/protobuf/proto"
)

type Observatory interface {
	features.Feature

	GetObservation(ctx context.Context) (proto.Message, error)
}

type ObservatoryFeedback interface {
	features.Feature

	RecordOutboundFailure(ctx context.Context, outboundTag, reason string)
}

func ObservatoryType() interface{} {
	return (*Observatory)(nil)
}

func ObservatoryFeedbackType() interface{} {
	return (*ObservatoryFeedback)(nil)
}
