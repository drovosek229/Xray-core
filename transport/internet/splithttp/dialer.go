package splithttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	gotls "crypto/tls"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"hash"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptrace"
	"net/url"
	reflect "reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/drovosek229/Xray-core/common"
	"github.com/drovosek229/Xray-core/common/buf"
	"github.com/drovosek229/Xray-core/common/errors"
	"github.com/drovosek229/Xray-core/common/net"
	"github.com/drovosek229/Xray-core/common/signal/done"
	"github.com/drovosek229/Xray-core/common/uuid"
	"github.com/drovosek229/Xray-core/transport/internet"
	"github.com/drovosek229/Xray-core/transport/internet/browser_dialer"
	"github.com/drovosek229/Xray-core/transport/internet/hysteria/congestion"
	"github.com/drovosek229/Xray-core/transport/internet/reality"
	"github.com/drovosek229/Xray-core/transport/internet/stat"
	"github.com/drovosek229/Xray-core/transport/internet/tls"
	"github.com/drovosek229/Xray-core/transport/pipe"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

type dialerConf struct {
	Destination string
	SettingsKey string
}

var (
	globalDialerMap    map[dialerConf]*XmuxManager
	globalDialerAccess sync.Mutex
)

const dialerManagerIdleTimeout = 2 * net.ConnIdleTimeout

type httpClientReservation struct {
	client      DialerClient
	xmuxClient  *XmuxClient
	releaseOnce sync.Once
}

func reserveHTTPClient(ctx context.Context, dest net.Destination, streamSettings *internet.MemoryStreamConfig) *httpClientReservation {
	client, xmuxClient := getHTTPClient(ctx, dest, streamSettings)
	return &httpClientReservation{
		client:     client,
		xmuxClient: xmuxClient,
	}
}

func (r *httpClientReservation) Client() DialerClient {
	if r == nil {
		return nil
	}
	return r.client
}

func (r *httpClientReservation) Release() {
	if r == nil {
		return
	}
	r.releaseOnce.Do(func() {
		if r.xmuxClient != nil {
			r.xmuxClient.OpenUsage.Add(-1)
		}
	})
}

func (r *httpClientReservation) ConsumeRequest() {
	if r != nil && r.xmuxClient != nil {
		r.xmuxClient.LeftRequests.Add(-1)
	}
}

func (r *httpClientReservation) NeedsRefresh(now time.Time) bool {
	if r == nil || r.xmuxClient == nil {
		return false
	}
	if r.xmuxClient.LeftRequests.Load() <= 0 {
		return true
	}
	return !r.xmuxClient.UnreusableAt.IsZero() && now.After(r.xmuxClient.UnreusableAt)
}

type reservationHolder struct {
	mu          sync.Mutex
	reservation *httpClientReservation
}

func newReservationHolder(reservation *httpClientReservation) *reservationHolder {
	return &reservationHolder{reservation: reservation}
}

func (h *reservationHolder) Current() *httpClientReservation {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reservation
}

func (h *reservationHolder) Swap(reservation *httpClientReservation) *httpClientReservation {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.reservation
	h.reservation = reservation
	return old
}

func (h *reservationHolder) Release() {
	if h == nil {
		return
	}
	if reservation := h.Swap(nil); reservation != nil {
		reservation.Release()
	}
}

type closeWithErrorReader interface {
	io.ReadCloser
	CloseWithError(error) error
}

type closeWithErrorWriter interface {
	io.WriteCloser
	CloseWithError(error) error
}

func closeReadWithError(reader io.ReadCloser, err error) {
	if reader == nil {
		return
	}
	if errReader, ok := reader.(closeWithErrorReader); ok {
		_ = errReader.CloseWithError(err)
		return
	}
	_ = reader.Close()
}

func closeWriteWithError(writer io.WriteCloser, err error) {
	if writer == nil {
		return
	}
	if errWriter, ok := writer.(closeWithErrorWriter); ok {
		_ = errWriter.CloseWithError(err)
		return
	}
	_ = writer.Close()
}

type startupContext struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func newStartupContext(parent context.Context, connCtx context.Context) *startupContext {
	ctx, cancel := context.WithCancel(connCtx)
	startup := &startupContext{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if parent != nil {
		go func() {
			select {
			case <-parent.Done():
				startup.Cancel()
			case <-startup.done:
			}
		}()
	}
	return startup
}

func (s *startupContext) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *startupContext) Detach() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.done)
	})
}

func (s *startupContext) Cancel() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.cancel()
		close(s.done)
	})
}

func (s *startupContext) Complete(err error) error {
	if err != nil {
		s.Cancel()
		return err
	}
	s.Detach()
	return nil
}

func relayAsyncStartFailure(started StartedReadCloser, reader io.ReadCloser, writer io.WriteCloser, uploadBody io.ReadCloser, startup *startupContext) {
	if started == nil {
		startup.Detach()
		return
	}

	go func() {
		if err := started.WaitStart(); err != nil {
			startup.Complete(err)
			closeReadWithError(reader, err)
			if uploadBody != nil {
				closeReadWithError(uploadBody, err)
				return
			}
			closeWriteWithError(writer, err)
			return
		}
		startup.Detach()
	}()
}

func dialerConfigKey(dest net.Destination, streamSettings *internet.MemoryStreamConfig) dialerConf {
	return dialerConf{
		Destination: dest.String(),
		SettingsKey: buildDialerSettingsKey(streamSettings),
	}
}

func buildDialerSettingsKey(streamSettings *internet.MemoryStreamConfig) string {
	hasher := sha256.New()
	if streamSettings == nil {
		writeDialerSegment(hasher, "nil", "true")
		return hex.EncodeToString(hasher.Sum(nil))
	}

	writeDialerSegment(hasher, "protocol_name", streamSettings.ProtocolName)
	writeDialerSegment(hasher, "security_type", streamSettings.SecurityType)
	writeProtoDialerSegment(hasher, "protocol_settings", streamSettings.ProtocolSettings)
	writeProtoDialerSegment(hasher, "security_settings", streamSettings.SecuritySettings)
	writeProtoDialerSegment(hasher, "socket_settings", streamSettings.SocketSettings)
	writeProtoDialerSegment(hasher, "quic_params", streamSettings.QuicParams)
	writeDialerSegment(hasher, "tcpmask_manager", reflectValueSignature(reflect.ValueOf(streamSettings.TcpmaskManager)))
	writeDialerSegment(hasher, "udpmask_manager", reflectValueSignature(reflect.ValueOf(streamSettings.UdpmaskManager)))
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeDialerSegment(hasher hash.Hash, label string, value string) {
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(value))
	_, _ = hasher.Write([]byte{0xff})
}

func writeProtoDialerSegment(hasher hash.Hash, label string, value any) {
	if value == nil {
		writeDialerSegment(hasher, label, "<nil>")
		return
	}
	if message, ok := value.(proto.Message); ok {
		bytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err == nil {
			writeDialerSegment(hasher, label, hex.EncodeToString(bytes))
			return
		}
	}
	writeDialerSegment(hasher, label, reflectValueSignature(reflect.ValueOf(value)))
}

func reflectValueSignature(value reflect.Value) string {
	hasher := sha256.New()
	hashReflectValue(hasher, value)
	return hex.EncodeToString(hasher.Sum(nil))
}

func hashReflectValue(hasher hash.Hash, value reflect.Value) {
	if !value.IsValid() {
		writeDialerSegment(hasher, "kind", "<invalid>")
		return
	}

	writeDialerSegment(hasher, "type", value.Type().String())

	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			writeDialerSegment(hasher, "value", "<nil>")
			return
		}
		hashReflectValue(hasher, value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			writeDialerSegment(hasher, "field", value.Type().Field(i).Name)
			hashReflectValue(hasher, value.Field(i))
		}
	case reflect.Slice, reflect.Array:
		writeDialerSegment(hasher, "len", strconv.Itoa(value.Len()))
		for i := 0; i < value.Len(); i++ {
			hashReflectValue(hasher, value.Index(i))
		}
	case reflect.Map:
		writeDialerSegment(hasher, "len", strconv.Itoa(value.Len()))
		for _, key := range value.MapKeys() {
			hashReflectValue(hasher, key)
			hashReflectValue(hasher, value.MapIndex(key))
		}
	case reflect.String:
		writeDialerSegment(hasher, "value", value.String())
	case reflect.Bool:
		writeDialerSegment(hasher, "value", strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		writeDialerSegment(hasher, "value", strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		writeDialerSegment(hasher, "value", strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		writeDialerSegment(hasher, "value", strconv.FormatFloat(value.Float(), 'g', -1, 64))
	case reflect.Complex64, reflect.Complex128:
		complexValue := value.Complex()
		writeDialerSegment(hasher, "real", strconv.FormatFloat(real(complexValue), 'g', -1, 64))
		writeDialerSegment(hasher, "imag", strconv.FormatFloat(imag(complexValue), 'g', -1, 64))
	default:
		writeDialerSegment(hasher, "value", fmt.Sprintf("%v", value))
	}
}

func cleanupGlobalDialersLocked(now time.Time) {
	for key, manager := range globalDialerMap {
		if manager.ShouldEvict(now, dialerManagerIdleTimeout) {
			delete(globalDialerMap, key)
			manager.Close()
		}
	}
}

func getHTTPClient(ctx context.Context, dest net.Destination, streamSettings *internet.MemoryStreamConfig) (DialerClient, *XmuxClient) {
	realityConfig := reality.ConfigFromStreamSettings(streamSettings)

	if browser_dialer.HasBrowserDialer() && realityConfig == nil {
		return &BrowserDialerClient{transportConfig: streamSettings.ProtocolSettings.(*Config)}, nil
	}

	globalDialerAccess.Lock()
	defer globalDialerAccess.Unlock()

	if globalDialerMap == nil {
		globalDialerMap = make(map[dialerConf]*XmuxManager)
	}

	now := time.Now()
	cleanupGlobalDialersLocked(now)

	key := dialerConfigKey(dest, streamSettings)

	xmuxManager, found := globalDialerMap[key]

	if !found {
		transportConfig := streamSettings.ProtocolSettings.(*Config)
		xmuxConfig := transportConfig.GetConfiguredXmux(decideHTTPVersion(tls.ConfigFromStreamSettings(streamSettings), realityConfig))

		xmuxManager = NewXmuxManager(xmuxConfig, func() XmuxConn {
			return createHTTPClient(dest, streamSettings)
		})
		globalDialerMap[key] = xmuxManager
	}

	xmuxClient := xmuxManager.ReserveXmuxClient(ctx)
	return xmuxClient.XmuxConn.(DialerClient), xmuxClient
}

func decideHTTPVersion(tlsConfig *tls.Config, realityConfig *reality.Config) string {
	if realityConfig != nil {
		return "2"
	}
	if tlsConfig == nil {
		return "1.1"
	}
	if len(tlsConfig.NextProtocol) != 1 {
		return "2"
	}
	if tlsConfig.NextProtocol[0] == "http/1.1" {
		return "1.1"
	}
	if tlsConfig.NextProtocol[0] == "h3" {
		return "3"
	}
	return "2"
}

func createHTTPClient(dest net.Destination, streamSettings *internet.MemoryStreamConfig) DialerClient {
	tlsConfig := tls.ConfigFromStreamSettings(streamSettings)
	realityConfig := reality.ConfigFromStreamSettings(streamSettings)

	httpVersion := decideHTTPVersion(tlsConfig, realityConfig)
	if httpVersion == "3" {
		dest.Network = net.Network_UDP // better to keep this line
	}

	var gotlsConfig *gotls.Config

	if tlsConfig != nil {
		gotlsConfig = tlsConfig.GetTLSConfig(tls.WithDestination(dest))
	}

	transportConfig := streamSettings.ProtocolSettings.(*Config)

	dialContext := func(ctxInner context.Context) (net.Conn, error) {
		conn, err := internet.DialSystem(ctxInner, dest, streamSettings.SocketSettings)
		if err != nil {
			return nil, err
		}

		if streamSettings.TcpmaskManager != nil {
			newConn, err := streamSettings.TcpmaskManager.WrapConnClient(conn)
			if err != nil {
				conn.Close()
				return nil, errors.New("mask err").Base(err)
			}
			conn = newConn
		}

		if realityConfig != nil {
			return reality.UClient(conn, realityConfig, ctxInner, dest)
		}

		if gotlsConfig != nil {
			if fingerprint := tls.GetFingerprint(tlsConfig.Fingerprint); fingerprint != nil {
				conn = tls.UClient(conn, gotlsConfig, fingerprint)
				if err := conn.(*tls.UConn).HandshakeContext(ctxInner); err != nil {
					return nil, err
				}
			} else {
				conn = tls.Client(conn, gotlsConfig)
			}
		}

		return conn, nil
	}

	var keepAlivePeriod time.Duration
	if streamSettings.ProtocolSettings.(*Config).Xmux != nil {
		keepAlivePeriod = time.Duration(streamSettings.ProtocolSettings.(*Config).Xmux.HKeepAlivePeriod) * time.Second
	}

	var transport http.RoundTripper

	if httpVersion == "3" {
		quicParams := streamSettings.QuicParams
		if quicParams == nil {
			quicParams = &internet.QuicParams{}
		}
		if quicParams.UdpHop == nil {
			quicParams.UdpHop = &internet.UdpHop{}
		}

		quicConfig := &quic.Config{
			InitialStreamReceiveWindow:     quicParams.InitStreamReceiveWindow,
			MaxStreamReceiveWindow:         quicParams.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow: quicParams.InitConnReceiveWindow,
			MaxConnectionReceiveWindow:     quicParams.MaxConnReceiveWindow,
			MaxIdleTimeout:                 time.Duration(quicParams.MaxIdleTimeout) * time.Second,
			KeepAlivePeriod:                time.Duration(quicParams.KeepAlivePeriod) * time.Second,
			MaxIncomingStreams:             quicParams.MaxIncomingStreams,
			DisablePathMTUDiscovery:        quicParams.DisablePathMtuDiscovery,
		}
		if quicParams.MaxIdleTimeout == 0 {
			quicConfig.MaxIdleTimeout = net.ConnIdleTimeout
		}
		if quicParams.KeepAlivePeriod == 0 {
			if keepAlivePeriod == 0 {
				quicConfig.KeepAlivePeriod = net.QuicgoH3KeepAlivePeriod
			}
		}
		if quicParams.MaxIncomingStreams == 0 {
			// these two are defaults of quic-go/http3. the default of quic-go (no
			// http3) is different, so it is hardcoded here for clarity.
			// https://github.com/quic-go/quic-go/blob/b8ea5c798155950fb5bbfdd06cad1939c9355878/http3/client.go#L36-L39
			quicConfig.MaxIncomingStreams = -1
		}

		transport = &http3.Transport{
			QUICConfig:      quicConfig,
			TLSClientConfig: gotlsConfig,
			Dial: func(ctx context.Context, addr string, tlsCfg *gotls.Config, cfg *quic.Config) (*quic.Conn, error) {
				setup, err := prepareHTTP3PacketConn(ctx, dest, streamSettings, quicParams, internet.DialSystem)
				if err != nil {
					return nil, err
				}

				quicConn, err := quic.DialEarly(ctx, setup.packetConn, setup.udpAddr, tlsCfg, cfg)
				if err != nil {
					setup.Close()
					return nil, err
				}

				switch quicParams.Congestion {
				case "force-brutal":
					errors.LogDebug(context.Background(), quicConn.RemoteAddr(), " ", "congestion brutal bytes per second ", quicParams.BrutalUp)
					congestion.UseBrutal(quicConn, quicParams.BrutalUp)
				case "reno":
					errors.LogDebug(context.Background(), quicConn.RemoteAddr(), " ", "congestion reno")
				default:
					errors.LogDebug(context.Background(), quicConn.RemoteAddr(), " ", "congestion bbr")
					congestion.UseBBR(quicConn)
				}

				return quicConn, nil
			},
		}
	} else if httpVersion == "2" {
		if keepAlivePeriod == 0 {
			keepAlivePeriod = net.ChromeH2KeepAlivePeriod
		}
		if keepAlivePeriod < 0 {
			keepAlivePeriod = 0
		}
		transport = &http2.Transport{
			DialTLSContext: func(ctxInner context.Context, network string, addr string, cfg *gotls.Config) (net.Conn, error) {
				return dialContext(ctxInner)
			},
			IdleConnTimeout: net.ConnIdleTimeout,
			ReadIdleTimeout: keepAlivePeriod,
		}
	} else {
		httpDialContext := func(ctxInner context.Context, network string, addr string) (net.Conn, error) {
			return dialContext(ctxInner)
		}

		transport = &http.Transport{
			DialTLSContext:  httpDialContext,
			DialContext:     httpDialContext,
			IdleConnTimeout: net.ConnIdleTimeout,
			// chunked transfer download with KeepAlives is buggy with
			// http.Client and our custom dial context.
			DisableKeepAlives: true,
		}
	}

	client := &DefaultDialerClient{
		transportConfig: transportConfig,
		client: &http.Client{
			Transport: transport,
		},
		httpVersion:    httpVersion,
		dialUploadConn: dialContext,
	}

	return client
}

func init() {
	common.Must(internet.RegisterTransportDialer(protocolName, Dial))
}

func Dial(ctx context.Context, dest net.Destination, streamSettings *internet.MemoryStreamConfig) (stat.Connection, error) {
	tlsConfig := tls.ConfigFromStreamSettings(streamSettings)
	realityConfig := reality.ConfigFromStreamSettings(streamSettings)

	httpVersion := decideHTTPVersion(tlsConfig, realityConfig)
	if httpVersion == "3" {
		dest.Network = net.Network_UDP
	}

	transportConfiguration := streamSettings.ProtocolSettings.(*Config)
	var requestURL url.URL

	if tlsConfig != nil || realityConfig != nil {
		requestURL.Scheme = "https"
	} else {
		requestURL.Scheme = "http"
	}
	requestURL.Host = transportConfiguration.Host
	if requestURL.Host == "" && tlsConfig != nil {
		requestURL.Host = tlsConfig.ServerName
	}
	if requestURL.Host == "" && realityConfig != nil {
		requestURL.Host = realityConfig.ServerName
	}
	if requestURL.Host == "" {
		requestURL.Host = dest.Address.String()
	}

	requestURL.Path = transportConfiguration.GetNormalizedPath()
	requestURL.RawQuery = transportConfiguration.GetNormalizedQuery()

	connCtx, connCancel := context.WithCancel(context.Background())
	uploadReservation := reserveHTTPClient(connCtx, dest, streamSettings)
	requestBehavior := transportConfiguration.NewRequestBehavior(httpVersion)

	mode := transportConfiguration.Mode
	if mode == "" || mode == "auto" {
		mode = "packet-up"
		if realityConfig != nil {
			mode = "stream-one"
			if transportConfiguration.DownloadSettings != nil {
				mode = "stream-up"
			}
		}
	}

	sessionId := ""
	if mode != "stream-one" {
		sessionIdUuid := uuid.New()
		sessionId = sessionIdUuid.String()
	}

	errors.LogInfo(ctx, fmt.Sprintf("XHTTP is dialing to %s, mode %s, HTTP version %s, host %s", dest, mode, httpVersion, requestURL.Host))

	requestURL2 := requestURL
	downloadReservation := uploadReservation
	requestBehavior2 := requestBehavior
	if transportConfiguration.DownloadSettings != nil {
		globalDialerAccess.Lock()
		if streamSettings.DownloadSettings == nil {
			streamSettings.DownloadSettings = common.Must2(internet.ToMemoryStreamConfig(transportConfiguration.DownloadSettings))
			if streamSettings.SocketSettings != nil && streamSettings.SocketSettings.Penetrate {
				streamSettings.DownloadSettings.SocketSettings = streamSettings.SocketSettings
			}
		}
		globalDialerAccess.Unlock()
		memory2 := streamSettings.DownloadSettings
		dest2 := *memory2.Destination // just panic
		tlsConfig2 := tls.ConfigFromStreamSettings(memory2)
		realityConfig2 := reality.ConfigFromStreamSettings(memory2)
		httpVersion2 := decideHTTPVersion(tlsConfig2, realityConfig2)
		if httpVersion2 == "3" {
			dest2.Network = net.Network_UDP
		}
		if tlsConfig2 != nil || realityConfig2 != nil {
			requestURL2.Scheme = "https"
		} else {
			requestURL2.Scheme = "http"
		}
		config2 := memory2.ProtocolSettings.(*Config)
		requestURL2.Host = config2.Host
		if requestURL2.Host == "" && tlsConfig2 != nil {
			requestURL2.Host = tlsConfig2.ServerName
		}
		if requestURL2.Host == "" && realityConfig2 != nil {
			requestURL2.Host = realityConfig2.ServerName
		}
		if requestURL2.Host == "" {
			requestURL2.Host = dest2.Address.String()
		}
		requestURL2.Path = config2.GetNormalizedPath()
		requestURL2.RawQuery = config2.GetNormalizedQuery()
		downloadReservation = reserveHTTPClient(connCtx, dest2, memory2)
		requestBehavior2 = config2.NewRequestBehavior(httpVersion2)
		errors.LogInfo(ctx, fmt.Sprintf("XHTTP is downloading from %s, mode %s, HTTP version %s, host %s", dest2, "stream-down", httpVersion2, requestURL2.Host))
	}

	var closed atomic.Int32

	reader, writer := io.Pipe()
	packetUploadReservations := newReservationHolder(uploadReservation)
	conn := splitConn{
		writer: writer,
		onClose: func() {
			if closed.Add(1) > 1 {
				return
			}
			connCancel()
			packetUploadReservations.Release()
			if downloadReservation != uploadReservation {
				downloadReservation.Release()
			}
		},
	}

	if mode == "stream-one" {
		requestURL.Path = transportConfiguration.GetNormalizedPath()
		uploadReservation.ConsumeRequest()
		startup := newStartupContext(ctx, connCtx)
		streamReader, remoteAddr, localAddr, err := uploadReservation.Client().OpenStream(startup.Context(), requestURL.String(), sessionId, reader, false, requestBehavior)
		if err != nil { // browser dialer only
			startup.Cancel()
			connCancel()
			uploadReservation.Release()
			return nil, err
		}
		conn.reader, conn.remoteAddr, conn.localAddr = streamReader, remoteAddr, localAddr
		relayAsyncStartFailure(streamReader, nil, conn.writer, reader, startup)
		return stat.Connection(&conn), nil
	} else { // stream-down
		downloadReservation.ConsumeRequest()
		startup := newStartupContext(ctx, connCtx)
		streamReader, remoteAddr, localAddr, err := downloadReservation.Client().OpenStream(startup.Context(), requestURL2.String(), sessionId, nil, false, requestBehavior2)
		if err != nil { // browser dialer only
			startup.Cancel()
			connCancel()
			if downloadReservation != uploadReservation {
				downloadReservation.Release()
			}
			packetUploadReservations.Release()
			return nil, err
		}
		if err := streamReader.WaitStart(); err != nil {
			startup.Complete(err)
			connCancel()
			if downloadReservation != uploadReservation {
				downloadReservation.Release()
			}
			packetUploadReservations.Release()
			return nil, err
		}
		startup.Detach()
		conn.reader, conn.remoteAddr, conn.localAddr = streamReader, remoteAddr, localAddr
	}
	if mode == "stream-up" {
		uploadReservation.ConsumeRequest()
		startup := newStartupContext(ctx, connCtx)
		uploadReader, _, _, err := uploadReservation.Client().OpenStream(startup.Context(), requestURL.String(), sessionId, reader, true, requestBehavior)
		if err != nil { // browser dialer only
			startup.Cancel()
			connCancel()
			if downloadReservation != uploadReservation {
				downloadReservation.Release()
			}
			packetUploadReservations.Release()
			return nil, err
		}
		relayAsyncStartFailure(uploadReader, conn.reader, conn.writer, reader, startup)
		return stat.Connection(&conn), nil
	}

	scMaxEachPostBytes := transportConfiguration.GetNormalizedScMaxEachPostBytes()
	scMinPostsIntervalMs := transportConfiguration.GetNormalizedScMinPostsIntervalMs()

	if scMaxEachPostBytes.From <= 0 {
		return nil, errors.New("`scMaxEachPostBytes` should be bigger than 0")
	}

	maxUploadSize := scMaxEachPostBytes.rand()
	if transportConfiguration.IsBalancedBehaviorProfile() {
		maxUploadSize = max(1, scMaxEachPostBytes.To)
	}
	// WithSizeLimit(0) will still allow single bytes to pass, and a lot of
	// code relies on this behavior. Subtract 1 so that together with
	// uploadWriter wrapper, exact size limits can be enforced
	// uploadPipeReader, uploadPipeWriter := pipe.New(pipe.WithSizeLimit(maxUploadSize - 1))
	uploadPipeReader, uploadPipeWriter := pipe.New(pipe.WithSizeLimit(max(0, maxUploadSize-buf.Size)))

	conn.writer = uploadWriter{
		uploadPipeWriter,
		maxUploadSize,
	}

	go func() {
		var seq int64
		var lastWrite time.Time

		for {
			// by offloading the uploads into a buffered pipe, multiple conn.Write
			// calls get automatically batched together into larger POST requests.
			// without batching, bandwidth is extremely limited.
			remainder, err := uploadPipeReader.ReadMultiBuffer()
			if err != nil {
				break
			}

			doSplit := atomic.Bool{}
			for doSplit.Store(true); doSplit.Load(); {
				var chunk buf.MultiBuffer
				chunkSize := maxUploadSize
				if transportConfiguration.IsBalancedBehaviorProfile() {
					chunkSize = requestBehavior.NextUploadSize(scMaxEachPostBytes, remainder.Len())
				}
				remainder, chunk = buf.SplitSize(remainder, chunkSize)
				if chunk.IsEmpty() {
					break
				}

				wroteRequest := done.New()

				requestCtx := httptrace.WithClientTrace(connCtx, &httptrace.ClientTrace{
					WroteRequest: func(httptrace.WroteRequestInfo) {
						wroteRequest.Close()
					},
				})

				seqStr := strconv.FormatInt(seq, 10)
				seq += 1

				if scMinPostsIntervalMs.From > 0 {
					sleepFor := time.Duration(scMinPostsIntervalMs.rand()) * time.Millisecond
					if transportConfiguration.IsBalancedBehaviorProfile() {
						sleepFor = requestBehavior.NextPostInterval(scMinPostsIntervalMs)
					}
					sleepFor -= time.Since(lastWrite)
					if sleepFor > 0 {
						timer := time.NewTimer(sleepFor)
						select {
						case <-connCtx.Done():
							timer.Stop()
							uploadPipeReader.Interrupt()
							doSplit.Store(false)
							return
						case <-timer.C:
						}
					}
				}

				lastWrite = time.Now()

				currentReservation := packetUploadReservations.Current()
				if currentReservation == nil || currentReservation.NeedsRefresh(lastWrite) {
					nextReservation := reserveHTTPClient(connCtx, dest, streamSettings)
					if oldReservation := packetUploadReservations.Swap(nextReservation); oldReservation != nil {
						oldReservation.Release()
					}
					currentReservation = nextReservation
				}
				currentReservation.ConsumeRequest()
				httpClient := currentReservation.Client()

				payloadBytes, err := buf.ReadAllToBytes(&buf.MultiBufferContainer{MultiBuffer: chunk})
				if err != nil {
					errors.LogInfoInner(requestCtx, err, "failed to buffer upload")
					uploadPipeReader.Interrupt()
					doSplit.Store(false)
					break
				}

				go func() {
					err := postPacketWithRetry(
						requestCtx,
						httpClient,
						transportConfiguration,
						requestURL.String(),
						sessionId,
						seqStr,
						payloadBytes,
						requestBehavior,
					)
					wroteRequest.Close()
					if err != nil {
						errors.LogInfoInner(requestCtx, err, "failed to send upload")
						uploadPipeReader.Interrupt()
						doSplit.Store(false)
					}
				}()

				if _, ok := httpClient.(*DefaultDialerClient); ok {
					<-wroteRequest.Wait()
				}
			}
		}
	}()

	return stat.Connection(&conn), nil
}

func isZeroXmuxConfig(config XmuxConfig) bool {
	return isZeroRangeConfig(config.MaxConcurrency) &&
		isZeroRangeConfig(config.MaxConnections) &&
		isZeroRangeConfig(config.CMaxReuseTimes) &&
		isZeroRangeConfig(config.HMaxRequestTimes) &&
		isZeroRangeConfig(config.HMaxReusableSecs) &&
		config.HKeepAlivePeriod == 0 &&
		config.WarmConnections == 0
}

func postPacketWithRetry(ctx context.Context, client DialerClient, config *Config, requestURL string, sessionID string, seqStr string, payload []byte, behavior *RequestBehavior) error {
	maxAttempts := 1
	if config.IsBalancedBehaviorProfile() {
		maxAttempts = 3
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = client.PostPacket(
			ctx,
			requestURL,
			sessionID,
			seqStr,
			bytes.NewReader(payload),
			int64(len(payload)),
			behavior,
		)
		if lastErr == nil {
			return nil
		}
		if !config.IsBalancedBehaviorProfile() || !isRetriablePostError(lastErr) || attempt == maxAttempts-1 {
			return lastErr
		}

		backoff := time.Duration(25*(1<<attempt))*time.Millisecond + time.Duration(cryptoRandJitterMillis(25))*time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func isRetriablePostError(err error) bool {
	var statusErr *HTTPStatusError
	if stderrors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	return true
}

func cryptoRandJitterMillis(maxMillis int64) int64 {
	if maxMillis <= 0 {
		return 0
	}
	return cryptoRandBetween(0, maxMillis+1)
}

func cryptoRandBetween(from, to int64) int64 {
	return int64(rand.Int63n(to-from)) + from
}

// A wrapper around pipe that ensures the size limit is exactly honored.
//
// The MultiBuffer pipe accepts any single WriteMultiBuffer call even if that
// single MultiBuffer exceeds the size limit, and then starts blocking on the
// next WriteMultiBuffer call. This means that ReadMultiBuffer can return more
// bytes than the size limit. We work around this by splitting a potentially
// too large write up into multiple.
type uploadWriter struct {
	*pipe.Writer
	maxLen int32
}

func (w uploadWriter) Write(b []byte) (int, error) {
	/*
		capacity := int(w.maxLen - w.Len())
		if capacity > 0 && capacity < len(b) {
			b = b[:capacity]
		}
	*/

	buffer := buf.MultiBufferContainer{}
	common.Must2(buffer.Write(b))

	var writed int
	for _, buff := range buffer.MultiBuffer {
		writeLen := int(buff.Len())
		// WriteMultiBuffer takes ownership of buff; don't read or mutate it after the handoff.
		err := w.WriteMultiBuffer(buf.MultiBuffer{buff})
		if err != nil {
			return writed, err
		}
		writed += writeLen
	}
	return writed, nil
}
