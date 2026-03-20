package outbound

import (
	"context"
	goerrors "errors"
	"io"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

type countingReader struct {
	reader        buf.Reader
	timeoutReader buf.TimeoutReader
	bytesRead     int64
	recorded      [][]byte
}

func newCountingReader(reader buf.Reader) *countingReader {
	counted := &countingReader{reader: reader}
	if timeoutReader, ok := reader.(buf.TimeoutReader); ok {
		counted.timeoutReader = timeoutReader
	}
	return counted
}

func (r *countingReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.reader.ReadMultiBuffer()
	r.record(mb)
	return mb, err
}

func (r *countingReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	if r.timeoutReader == nil {
		return r.ReadMultiBuffer()
	}
	mb, err := r.timeoutReader.ReadMultiBufferTimeout(timeout)
	r.record(mb)
	return mb, err
}

func (r *countingReader) Interrupt() {
	common.Interrupt(r.reader)
}

func (r *countingReader) Close() error {
	return common.Close(r.reader)
}

func (r *countingReader) BytesRead() int64 {
	return r.bytesRead
}

func (r *countingReader) ReplayReader() buf.Reader {
	recorded := make([][]byte, len(r.recorded))
	copy(recorded, r.recorded)
	return &replayingReader{
		reader:        r.reader,
		timeoutReader: r.timeoutReader,
		recorded:      recorded,
	}
}

func (r *countingReader) record(mb buf.MultiBuffer) {
	if mb.IsEmpty() {
		return
	}

	recorded := make([]byte, mb.Len())
	mb.Copy(recorded)
	r.bytesRead += int64(len(recorded))
	r.recorded = append(r.recorded, recorded)
}

type replayingReader struct {
	reader        buf.Reader
	timeoutReader buf.TimeoutReader
	recorded      [][]byte
	index         int
}

func (r *replayingReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if mb, ok := r.nextRecorded(); ok {
		return mb, nil
	}
	return r.reader.ReadMultiBuffer()
}

func (r *replayingReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	if mb, ok := r.nextRecorded(); ok {
		return mb, nil
	}
	if r.timeoutReader == nil {
		return r.reader.ReadMultiBuffer()
	}
	return r.timeoutReader.ReadMultiBufferTimeout(timeout)
}

func (r *replayingReader) Interrupt() {
	common.Interrupt(r.reader)
}

func (r *replayingReader) Close() error {
	return common.Close(r.reader)
}

func (r *replayingReader) nextRecorded() (buf.MultiBuffer, bool) {
	for r.index < len(r.recorded) {
		recorded := r.recorded[r.index]
		r.index++
		if len(recorded) == 0 {
			continue
		}
		return buf.MultiBuffer{buf.FromBytes(recorded)}, true
	}
	return nil, false
}

type countingWriter struct {
	writer       buf.Writer
	bytesWritten int64
}

func newCountingWriter(writer buf.Writer) *countingWriter {
	return &countingWriter{writer: writer}
}

func (w *countingWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.bytesWritten += int64(mb.Len())
	return w.writer.WriteMultiBuffer(mb)
}

func (w *countingWriter) Interrupt() {
	common.Interrupt(w.writer)
}

func (w *countingWriter) Close() error {
	return common.Close(w.writer)
}

func (w *countingWriter) BytesWritten() int64 {
	return w.bytesWritten
}

func (h *Handler) handleBalancerFailure(ctx context.Context, writer buf.Writer, requestReader *countingReader, err error, requestBytesRead, responseBytesWritten int64, allowConsumedRequest bool) bool {
	snapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	if !ok || snapshot.SelectedOutboundTag == "" {
		return false
	}

	if feedback := h.observatoryFeedbackFromContext(ctx); feedback != nil {
		feedback.RecordOutboundFailure(ctx, snapshot.SelectedOutboundTag, err.Error())
	}

	if snapshot.Retried || snapshot.RetryOwnerTag != h.tag || responseBytesWritten != 0 || (!allowConsumedRequest && requestBytesRead != 0) {
		return false
	}

	if !session.AddBalancerExclusion(ctx, snapshot.SelectedOutboundTag) {
		return false
	}
	session.MarkBalancerRetried(ctx)
	updatedSnapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	if !ok {
		return false
	}

	switch updatedSnapshot.Kind {
	case session.BalancerSelectionKindRoute:
		return h.retryRouteBalancer(ctx, writer, requestReader, updatedSnapshot)
	case session.BalancerSelectionKindDialerProxy:
		errors.LogInfo(ctx, "retrying dialer proxy balancer [", updatedSnapshot.BalancerTag, "] after outbound [", updatedSnapshot.SelectedOutboundTag, "] failed")
		h.Dispatch(ctx, &transport.Link{
			Reader: requestReader.ReplayReader(),
			Writer: writer,
		})
		return true
	default:
		return false
	}
}

func (h *Handler) shouldTreatBenignErrorAsFailure(ctx context.Context, err error, requestBytesRead, responseBytesWritten int64) bool {
	if responseBytesWritten != 0 {
		return false
	}
	if requestBytesRead != 0 && !goerrors.Is(err, io.EOF) && !goerrors.Is(err, context.Canceled) {
		return false
	}
	snapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	return ok && snapshot.SelectedOutboundTag != ""
}

func (h *Handler) shouldTreatCompletionWithoutResponseAsFailure(ctx context.Context, responseBytesWritten int64) bool {
	if responseBytesWritten != 0 {
		return false
	}
	snapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	return ok && snapshot.SelectedOutboundTag != ""
}

func (h *Handler) retryRouteBalancer(ctx context.Context, writer buf.Writer, requestReader *countingReader, snapshot session.BalancerRetrySnapshot) bool {
	selector := h.balancerSelectorExFromContext(ctx)
	if selector == nil {
		errors.LogInfo(ctx, "cannot retry route balancer [", snapshot.BalancerTag, "]: balancer exclusion support unavailable")
		return false
	}

	nextTag, found, err := selector.PickBalancerOutboundExcluding(snapshot.BalancerTag, snapshot.ExcludedOutboundTags)
	if err != nil {
		errors.LogInfoInner(ctx, err, "cannot retry route balancer ", snapshot.BalancerTag)
		return false
	}
	if !found || nextTag == "" {
		errors.LogInfo(ctx, "cannot retry route balancer [", snapshot.BalancerTag, "]: no replacement outbound available")
		return false
	}

	handler := h.outboundManager.GetHandler(nextTag)
	if handler == nil {
		errors.LogInfo(ctx, "cannot retry route balancer [", snapshot.BalancerTag, "]: outbound [", nextTag, "] not found")
		return false
	}

	session.UpdateBalancerSelection(ctx, session.BalancerSelectionKindRoute, snapshot.BalancerTag, nextTag, nextTag)
	if outbounds := session.OutboundsFromContext(ctx); len(outbounds) > 0 {
		outbounds[len(outbounds)-1].Tag = nextTag
	}

	errors.LogInfo(ctx, "retrying route balancer [", snapshot.BalancerTag, "] from outbound [", snapshot.SelectedOutboundTag, "] to [", nextTag, "]")
	handler.Dispatch(ctx, &transport.Link{
		Reader: requestReader.ReplayReader(),
		Writer: writer,
	})
	return true
}

func (h *Handler) resolveProxyTarget(ctx context.Context, tag string) (string, outbound.Handler, error) {
	if handler := h.outboundManager.GetHandler(tag); handler != nil {
		return tag, handler, nil
	}

	selector := h.balancerSelectorFromContext(ctx)
	if selector == nil {
		return "", nil, errors.New("failed to get outbound handler with tag: " + tag)
	}

	var (
		excluded []string
		found    bool
		err      error
		resolved string
	)

	if snapshot, ok := session.GetBalancerRetrySnapshot(ctx); ok && snapshot.Kind == session.BalancerSelectionKindDialerProxy && snapshot.BalancerTag == tag {
		excluded = snapshot.ExcludedOutboundTags
	}

	selectorEx := h.balancerSelectorExFromContext(ctx)
	if selectorEx != nil && len(excluded) > 0 {
		resolved, found, err = selectorEx.PickBalancerOutboundExcluding(tag, excluded)
	} else {
		resolved, found, err = selector.PickBalancerOutbound(tag)
	}
	if err != nil {
		return "", nil, errors.New("failed to get outbound from chained proxy balancer " + tag).Base(err)
	}
	if !found || resolved == "" {
		return "", nil, errors.New("failed to get outbound handler with tag: " + tag)
	}

	handler := h.outboundManager.GetHandler(resolved)
	if handler == nil {
		return "", nil, errors.New("failed to get outbound handler with tag: " + resolved)
	}

	session.UpdateBalancerSelection(ctx, session.BalancerSelectionKindDialerProxy, tag, h.tag, resolved)
	errors.LogInfo(ctx, "resolved chained proxy balancer ", tag, " to outbound ", resolved)
	return resolved, handler, nil
}
