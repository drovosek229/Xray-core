package splithttp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/xtls/xray-core/transport/internet/stat"
)

func benchmarkFillPacketRequest(b *testing.B, config *Config, payload []byte, sessionID, seq string) {
	baseURL := &url.URL{Scheme: "https", Host: "example.com", Path: "/x/"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqURL := *baseURL
		request := &http.Request{
			Method:        http.MethodPost,
			URL:           &reqURL,
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(payload)),
			ContentLength: int64(len(payload)),
		}
		if err := config.FillPacketRequest(request, sessionID, seq, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkServeStreamOneRequest(b *testing.B, clientConfig, serverConfig *Config, payload []byte, method string) {
	baseURL := &url.URL{Scheme: "https", Host: "example.com", Path: serverConfig.GetNormalizedPath()}
	handler := &requestHandler{
		config:    serverConfig,
		host:      serverConfig.Host,
		path:      serverConfig.GetNormalizedPath(),
		sessionMu: &sync.Mutex{},
		ln: &Listener{
			config: serverConfig,
			addConn: func(conn stat.Connection) {
				_ = conn.Close()
			},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqURL := *baseURL
		request := &http.Request{
			Method:        method,
			URL:           &reqURL,
			Host:          reqURL.Host,
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(payload)),
			ContentLength: int64(len(payload)),
			RemoteAddr:    "127.0.0.1:12345",
		}
		clientConfig.FillStreamRequest(request, "", "", nil)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			b.Fatalf("unexpected status %d", recorder.Code)
		}
	}
}

func BenchmarkXHTTPRequestShapingFillPacketRequestHeaderPayload(b *testing.B) {
	config := &Config{
		Headers: map[string]string{
			"X-Test": "1",
		},
		UplinkDataPlacement: PlacementHeader,
		UplinkDataKey:       "X-Data",
		UplinkChunkSize:     &RangeConfig{From: 1024, To: 1024},
		SessionPlacement:    PlacementQuery,
		SessionKey:          "sid",
		SeqPlacement:        PlacementHeader,
		SeqKey:              "X-Seq",
		XPaddingBytes:       &RangeConfig{From: 96, To: 96},
		XPaddingObfsMode:    true,
		XPaddingPlacement:   PlacementQuery,
		XPaddingKey:         "x_pad",
		XPaddingMethod:      string(PaddingMethodRepeatX),
	}

	benchmarkFillPacketRequest(b, config, bytes.Repeat([]byte("A"), 4096), "session-header", "42")
}

func BenchmarkXHTTPRequestShapingFillPacketRequestCookiePayload(b *testing.B) {
	config := &Config{
		Headers: map[string]string{
			"X-Test": "1",
		},
		UplinkDataPlacement: PlacementCookie,
		UplinkDataKey:       "x_data",
		UplinkChunkSize:     &RangeConfig{From: 1024, To: 1024},
		SessionPlacement:    PlacementCookie,
		SessionKey:          "sid",
		SeqPlacement:        PlacementQuery,
		SeqKey:              "seq",
		XPaddingBytes:       &RangeConfig{From: 96, To: 96},
		XPaddingObfsMode:    true,
		XPaddingPlacement:   PlacementCookie,
		XPaddingKey:         "x_pad",
		XPaddingMethod:      string(PaddingMethodRepeatX),
	}

	benchmarkFillPacketRequest(b, config, bytes.Repeat([]byte("B"), 4096), "session-cookie", "43")
}

func BenchmarkXHTTPRequestShapingFillPacketRequestBodyPayload(b *testing.B) {
	config := &Config{
		Headers: map[string]string{
			"X-Test": "1",
		},
		UplinkDataPlacement: PlacementBody,
		SessionPlacement:    PlacementPath,
		SeqPlacement:        PlacementPath,
		XPaddingBytes:       &RangeConfig{From: 96, To: 96},
		XPaddingMethod:      string(PaddingMethodRepeatX),
	}

	benchmarkFillPacketRequest(b, config, bytes.Repeat([]byte("C"), 2048), "session-body", "44")
}

func BenchmarkXHTTPRequestShapingServeStreamOneBalancedClientBalancedServer(b *testing.B) {
	clientConfig := &Config{
		Path:             "/x/",
		BehaviorProfile:  BehaviorProfileBalanced,
		UplinkHTTPMethod: http.MethodPut,
		XPaddingBytes:    &RangeConfig{From: 96, To: 96},
	}
	serverConfig := &Config{
		Path:            "/x/",
		BehaviorProfile: BehaviorProfileBalanced,
		XPaddingBytes:   &RangeConfig{From: 96, To: 96},
	}

	benchmarkServeStreamOneRequest(b, clientConfig, serverConfig, bytes.Repeat([]byte("D"), 2048), http.MethodPut)
}

func BenchmarkXHTTPRequestShapingServeStreamOneBalancedClientLegacyServerCompat(b *testing.B) {
	clientConfig := &Config{
		Path:             "/x/",
		BehaviorProfile:  BehaviorProfileBalanced,
		UplinkHTTPMethod: http.MethodPut,
		XPaddingBytes:    &RangeConfig{From: 96, To: 96},
	}
	serverConfig := &Config{
		Path: "/x/",
		// leave behavior profile at legacy default to cover the real-world compatibility case
	}

	benchmarkServeStreamOneRequest(b, clientConfig, serverConfig, bytes.Repeat([]byte("E"), 2048), http.MethodPut)
}
