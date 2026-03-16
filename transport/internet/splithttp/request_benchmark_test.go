package splithttp

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"
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
