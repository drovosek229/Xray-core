package outbound

import (
	"context"
	goerrors "errors"
	"io"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/transport"
)

const requestReplayLimitBytes int64 = 128 * 1024

type balancerFailureClassification int

const (
	balancerFailureClassificationNone balancerFailureClassification = iota
	balancerFailureClassificationZeroByteReplay
	balancerFailureClassificationRequestConsumed
	balancerFailureClassificationLegacyConsumedReplay
)

type requestReplayRecorderKey struct{}

type requestReplayRecorder struct {
	enabled  bool
	blocked  bool
	size     int64
	recorded [][]byte
}

func contextWithRequestReplayRecorder(ctx context.Context, recorder *requestReplayRecorder) context.Context {
	return context.WithValue(ctx, requestReplayRecorderKey{}, recorder)
}

func requestReplayRecorderFromContext(ctx context.Context) *requestReplayRecorder {
	if recorder, ok := ctx.Value(requestReplayRecorderKey{}).(*requestReplayRecorder); ok {
		return recorder
	}
	return nil
}

func newRequestReplayRecorder() *requestReplayRecorder {
	return &requestReplayRecorder{}
}

func (r *requestReplayRecorder) Enable() {
	if r == nil || r.blocked {
		return
	}
	r.enabled = true
}

func (r *requestReplayRecorder) Record(mb buf.MultiBuffer) {
	if r == nil || !r.enabled || r.blocked || mb.IsEmpty() {
		return
	}

	recordedSize := int64(mb.Len())
	if r.size+recordedSize > requestReplayLimitBytes {
		r.enabled = false
		r.blocked = true
		r.size = 0
		r.recorded = nil
		return
	}

	recorded := make([]byte, recordedSize)
	mb.Copy(recorded)
	r.recorded = append(r.recorded, recorded)
	r.size += recordedSize
}

func (r *requestReplayRecorder) CanReplay() bool {
	return r != nil && r.enabled && !r.blocked
}

func (r *requestReplayRecorder) ReplayReader(reader buf.Reader, timeoutReader buf.TimeoutReader) buf.Reader {
	if !r.CanReplay() {
		return nil
	}

	recorded := make([][]byte, len(r.recorded))
	copy(recorded, r.recorded)
	return &replayingReader{
		reader:        reader,
		timeoutReader: timeoutReader,
		recorded:      recorded,
	}
}

type countingReader struct {
	reader        buf.Reader
	timeoutReader buf.TimeoutReader
	bytesRead     int64
	replay        *requestReplayRecorder
}

func newCountingReader(reader buf.Reader, replay *requestReplayRecorder) *countingReader {
	counted := &countingReader{reader: reader, replay: replay}
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

func (r *countingReader) CanReplay() bool {
	return r.replay != nil && r.replay.CanReplay()
}

func (r *countingReader) ReplayReader() buf.Reader {
	if r.replay == nil {
		return nil
	}
	return r.replay.ReplayReader(r.reader, r.timeoutReader)
}

func (r *countingReader) record(mb buf.MultiBuffer) {
	if mb.IsEmpty() {
		return
	}

	r.bytesRead += int64(mb.Len())
	if r.replay != nil {
		r.replay.Record(mb)
	}
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

func (c balancerFailureClassification) shouldTreatBenignErrorAsFailure() bool {
	return c != balancerFailureClassificationNone
}

func (c balancerFailureClassification) allowsReplay() bool {
	return c == balancerFailureClassificationZeroByteReplay || c == balancerFailureClassificationLegacyConsumedReplay
}

func (h *Handler) retryReplayPolicy() proxyman.RetryReplayPolicy {
	if h.senderSettings == nil {
		return proxyman.RetryReplayPolicy_ZERO_BYTE_ONLY
	}
	return h.senderSettings.GetRetryReplayPolicy()
}

func (h *Handler) classifyBalancerFailure(ctx context.Context, err error, requestBytesRead, responseBytesWritten int64) balancerFailureClassification {
	if ctx.Err() != nil || responseBytesWritten != 0 {
		return balancerFailureClassificationNone
	}

	snapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	if !ok || snapshot.SelectedOutboundTag == "" {
		return balancerFailureClassificationNone
	}

	if requestBytesRead == 0 {
		return balancerFailureClassificationZeroByteReplay
	}

	if h.retryReplayPolicy() == proxyman.RetryReplayPolicy_LEGACY_CONSUMED_BENIGN &&
		(goerrors.Is(err, io.EOF) || goerrors.Is(err, context.Canceled)) {
		return balancerFailureClassificationLegacyConsumedReplay
	}

	return balancerFailureClassificationRequestConsumed
}

func (h *Handler) handleBalancerFailure(ctx context.Context, writer buf.Writer, requestReader *countingReader, err error, classification balancerFailureClassification) bool {
	snapshot, ok := session.GetBalancerRetrySnapshot(ctx)
	if !ok || snapshot.SelectedOutboundTag == "" {
		return false
	}
	if classification == balancerFailureClassificationNone {
		return false
	}

	if snapshot.RetryOwnerTag == h.tag {
		if feedback := h.observatoryFeedbackFromContext(ctx); feedback != nil {
			feedback.RecordOutboundFailure(ctx, snapshot.SelectedOutboundTag, err.Error())
		}
	}

	if snapshot.RetryOwnerTag == h.tag && classification == balancerFailureClassificationRequestConsumed {
		errors.LogInfo(ctx, "replay denied for outbound [", snapshot.SelectedOutboundTag, "]: request was already consumed")
	}

	if snapshot.Retried || snapshot.RetryOwnerTag != h.tag || requestReader == nil || !requestReader.CanReplay() || !classification.allowsReplay() {
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

	replayReader := requestReader.ReplayReader()
	if replayReader == nil {
		return false
	}

	switch classification {
	case balancerFailureClassificationZeroByteReplay:
		errors.LogInfo(ctx, "replay allowed for outbound [", snapshot.SelectedOutboundTag, "]: zero-byte failure")
	case balancerFailureClassificationLegacyConsumedReplay:
		errors.LogInfo(ctx, "replay allowed for outbound [", snapshot.SelectedOutboundTag, "]: legacy consumed benign policy configured")
	default:
		return false
	}

	switch updatedSnapshot.Kind {
	case session.BalancerSelectionKindRoute:
		return h.retryRouteBalancer(ctx, writer, replayReader, updatedSnapshot)
	case session.BalancerSelectionKindDialerProxy:
		errors.LogInfo(ctx, "retrying dialer proxy balancer [", updatedSnapshot.BalancerTag, "] after outbound [", updatedSnapshot.SelectedOutboundTag, "] failed")
		h.Dispatch(ctx, &transport.Link{
			Reader: replayReader,
			Writer: writer,
		})
		return true
	default:
		return false
	}
}

func (h *Handler) retryRouteBalancer(ctx context.Context, writer buf.Writer, replayReader buf.Reader, snapshot session.BalancerRetrySnapshot) bool {
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
		Reader: replayReader,
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
	if recorder := requestReplayRecorderFromContext(ctx); recorder != nil {
		recorder.Enable()
	}
	errors.LogInfo(ctx, "resolved chained proxy balancer ", tag, " to outbound ", resolved)
	return resolved, handler, nil
}
