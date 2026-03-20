package outbound

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/extension"
	feature_outbound "github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

type retryTestProxy struct {
	calls   int
	process func(context.Context, *transport.Link, internet.Dialer) error
}

func (p *retryTestProxy) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	p.calls++
	return p.process(ctx, link, dialer)
}

type retryTestOutboundManager struct {
	handlers map[string]feature_outbound.Handler
}

func (*retryTestOutboundManager) Start() error { return nil }

func (*retryTestOutboundManager) Close() error { return nil }

func (*retryTestOutboundManager) Type() interface{} { return feature_outbound.ManagerType() }

func (m *retryTestOutboundManager) GetHandler(tag string) feature_outbound.Handler {
	return m.handlers[tag]
}

func (*retryTestOutboundManager) GetDefaultHandler() feature_outbound.Handler { return nil }

func (*retryTestOutboundManager) AddHandler(context.Context, feature_outbound.Handler) error { return nil }

func (*retryTestOutboundManager) RemoveHandler(context.Context, string) error { return nil }

func (*retryTestOutboundManager) ListHandlers(context.Context) []feature_outbound.Handler { return nil }

type retryTestBalancerSelector struct {
	choices        map[string][]string
	excludingCalls [][]string
}

func (s *retryTestBalancerSelector) PickBalancerOutbound(tag string) (string, bool, error) {
	return s.PickBalancerOutboundExcluding(tag, nil)
}

func (s *retryTestBalancerSelector) PickBalancerOutboundExcluding(tag string, excluded []string) (string, bool, error) {
	call := append([]string(nil), excluded...)
	s.excludingCalls = append(s.excludingCalls, call)
	for _, candidate := range s.choices[tag] {
		skip := false
		for _, failed := range excluded {
			if failed == candidate {
				skip = true
				break
			}
		}
		if !skip {
			return candidate, true, nil
		}
	}
	return "", true, nil
}

type retryTestObservatoryFeedback struct {
	tags []string
}

func (*retryTestObservatoryFeedback) Start() error { return nil }

func (*retryTestObservatoryFeedback) Close() error { return nil }

func (*retryTestObservatoryFeedback) Type() interface{} { return extension.ObservatoryFeedbackType() }

func (f *retryTestObservatoryFeedback) RecordOutboundFailure(ctx context.Context, outboundTag, reason string) {
	f.tags = append(f.tags, outboundTag)
}

func newRetryTestContext() context.Context {
	ctx := context.Background()
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{
		Target: net.TCPDestination(net.DomainAddress("example.com"), 80),
	}})
	ctx = session.ContextWithContent(ctx, &session.Content{})
	return ctx
}

func TestHandlerRetriesRouteBalancerBeforeAnyPayloadFlows(t *testing.T) {
	manager := &retryTestOutboundManager{handlers: map[string]feature_outbound.Handler{}}
	selector := &retryTestBalancerSelector{
		choices: map[string][]string{
			"balancer": {"proxy-a", "proxy-b"},
		},
	}
	feedback := &retryTestObservatoryFeedback{}

	proxyA := &retryTestProxy{process: func(context.Context, *transport.Link, internet.Dialer) error {
		return io.ErrClosedPipe
	}}
	proxyB := &retryTestProxy{process: func(context.Context, *transport.Link, internet.Dialer) error {
		return nil
	}}

	handlerA := &Handler{
		tag:                 "proxy-a",
		proxy:               proxyA,
		outboundManager:     manager,
		balancerSelectorEx:  selector,
		observatoryFeedback: feedback,
	}
	handlerB := &Handler{
		tag:                 "proxy-b",
		proxy:               proxyB,
		outboundManager:     manager,
		balancerSelectorEx:  selector,
		observatoryFeedback: feedback,
	}
	manager.handlers["proxy-a"] = handlerA
	manager.handlers["proxy-b"] = handlerB

	ctx := newRetryTestContext()
	ctx = session.SetBalancerSelection(ctx, session.BalancerSelectionKindRoute, "balancer", "proxy-a", "proxy-a")

	link := &transport.Link{
		Reader: buf.NewReader(bytes.NewReader(nil)),
		Writer: buf.NewWriter(io.Discard),
	}

	handlerA.Dispatch(ctx, link)

	if proxyA.calls != 1 {
		t.Fatalf("expected proxy-a to run once, got %d", proxyA.calls)
	}
	if proxyB.calls != 1 {
		t.Fatalf("expected proxy-b retry to run once, got %d", proxyB.calls)
	}
	if len(feedback.tags) == 0 || feedback.tags[0] != "proxy-a" {
		t.Fatalf("expected proxy-a failure to be reported, got %v", feedback.tags)
	}
}

func TestHandlerDoesNotRetryAfterRequestBytesAreConsumed(t *testing.T) {
	manager := &retryTestOutboundManager{handlers: map[string]feature_outbound.Handler{}}
	selector := &retryTestBalancerSelector{
		choices: map[string][]string{
			"balancer": {"proxy-a", "proxy-b"},
		},
	}

	proxyA := &retryTestProxy{process: func(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
		if _, err := link.Reader.ReadMultiBuffer(); err != nil {
			t.Fatalf("failed to consume request bytes: %v", err)
		}
		return io.ErrClosedPipe
	}}
	proxyB := &retryTestProxy{process: func(context.Context, *transport.Link, internet.Dialer) error {
		return nil
	}}

	handlerA := &Handler{
		tag:                "proxy-a",
		proxy:              proxyA,
		outboundManager:    manager,
		balancerSelectorEx: selector,
	}
	handlerB := &Handler{
		tag:                "proxy-b",
		proxy:              proxyB,
		outboundManager:    manager,
		balancerSelectorEx: selector,
	}
	manager.handlers["proxy-a"] = handlerA
	manager.handlers["proxy-b"] = handlerB

	ctx := newRetryTestContext()
	ctx = session.SetBalancerSelection(ctx, session.BalancerSelectionKindRoute, "balancer", "proxy-a", "proxy-a")

	link := &transport.Link{
		Reader: buf.NewReader(bytes.NewReader([]byte("payload"))),
		Writer: buf.NewWriter(io.Discard),
	}

	handlerA.Dispatch(ctx, link)

	if proxyB.calls != 0 {
		t.Fatalf("expected no retry after request bytes were consumed, got %d retries", proxyB.calls)
	}
}

func TestHandlerDoesNotRetryAfterResponseBytesAreWritten(t *testing.T) {
	manager := &retryTestOutboundManager{handlers: map[string]feature_outbound.Handler{}}
	selector := &retryTestBalancerSelector{
		choices: map[string][]string{
			"balancer": {"proxy-a", "proxy-b"},
		},
	}

	proxyA := &retryTestProxy{process: func(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
		if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes([]byte("x"))}); err != nil {
			return err
		}
		return io.ErrClosedPipe
	}}
	proxyB := &retryTestProxy{process: func(context.Context, *transport.Link, internet.Dialer) error {
		return nil
	}}

	handlerA := &Handler{
		tag:                "proxy-a",
		proxy:              proxyA,
		outboundManager:    manager,
		balancerSelectorEx: selector,
	}
	handlerB := &Handler{
		tag:                "proxy-b",
		proxy:              proxyB,
		outboundManager:    manager,
		balancerSelectorEx: selector,
	}
	manager.handlers["proxy-a"] = handlerA
	manager.handlers["proxy-b"] = handlerB

	ctx := newRetryTestContext()
	ctx = session.SetBalancerSelection(ctx, session.BalancerSelectionKindRoute, "balancer", "proxy-a", "proxy-a")

	link := &transport.Link{
		Reader: buf.NewReader(bytes.NewReader(nil)),
		Writer: buf.NewWriter(&bytes.Buffer{}),
	}

	handlerA.Dispatch(ctx, link)

	if proxyB.calls != 0 {
		t.Fatalf("expected no retry after response bytes were written, got %d retries", proxyB.calls)
	}
}

func TestHandlerRetriesDialerProxyBalancerByRerunningOuterOutbound(t *testing.T) {
	manager := &retryTestOutboundManager{handlers: map[string]feature_outbound.Handler{}}
	selector := &retryTestBalancerSelector{
		choices: map[string][]string{
			"proxy-balancer": {"proxy-a", "proxy-b"},
		},
	}
	feedback := &retryTestObservatoryFeedback{}

	innerA := &retryTestProxy{process: func(context.Context, *transport.Link, internet.Dialer) error {
		return io.ErrClosedPipe
	}}
	innerB := &retryTestProxy{process: func(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
		return link.Writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes([]byte("x"))})
	}}
	outer := &retryTestProxy{process: func(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
		conn, err := dialer.Dial(ctx, net.TCPDestination(net.DomainAddress("example.com"), 80))
		if err != nil {
			return err
		}
		defer conn.Close()

		var b [1]byte
		_, err = conn.Read(b[:])
		return err
	}}

	proxyAHandler := &Handler{
		tag:                 "proxy-a",
		proxy:               innerA,
		outboundManager:     manager,
		observatoryFeedback: feedback,
	}
	proxyBHandler := &Handler{
		tag:                 "proxy-b",
		proxy:               innerB,
		outboundManager:     manager,
		observatoryFeedback: feedback,
	}
	outerHandler := &Handler{
		tag:                 "outer",
		proxy:               outer,
		outboundManager:     manager,
		observatoryFeedback: feedback,
		balancerSelector:    selector,
		balancerSelectorEx:  selector,
		senderSettings: &proxyman.SenderConfig{
			ProxySettings: &internet.ProxyConfig{Tag: "proxy-balancer"},
		},
	}

	manager.handlers["proxy-a"] = proxyAHandler
	manager.handlers["proxy-b"] = proxyBHandler
	manager.handlers["outer"] = outerHandler

	ctx := newRetryTestContext()
	link := &transport.Link{
		Reader: buf.NewReader(bytes.NewReader(nil)),
		Writer: buf.NewWriter(io.Discard),
	}

	outerHandler.Dispatch(ctx, link)

	if outer.calls != 2 {
		t.Fatalf("expected outer outbound to run twice, got %d", outer.calls)
	}
	if innerA.calls != 1 {
		t.Fatalf("expected proxy-a to run once, got %d", innerA.calls)
	}
	if innerB.calls != 1 {
		t.Fatalf("expected proxy-b to run once, got %d", innerB.calls)
	}
	if len(selector.excludingCalls) < 2 {
		t.Fatalf("expected balancer to be queried twice, got %d calls", len(selector.excludingCalls))
	}
	if got := selector.excludingCalls[1]; len(got) != 1 || got[0] != "proxy-a" {
		t.Fatalf("expected second balancer call to exclude proxy-a, got %v", got)
	}
}
