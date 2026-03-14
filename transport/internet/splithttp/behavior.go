package splithttp

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/crypto"
)

const (
	BehaviorProfileLegacy   = "legacy"
	BehaviorProfileBalanced = "balanced"
)

type requestPersona struct {
	accept             string
	uploadContentType  string
	directQueryPadding bool
	extraHeaders       map[string]string
}

type RequestBehavior struct {
	mu                 sync.Mutex
	header             http.Header
	uploadContentType  string
	directQueryPadding bool
	httpVersion        string
	observedRTT        time.Duration
	observedChunkLen   int32
}

func (c *Config) GetNormalizedBehaviorProfile() string {
	if c.BehaviorProfile == BehaviorProfileBalanced {
		return BehaviorProfileBalanced
	}
	return BehaviorProfileLegacy
}

func (c *Config) IsBalancedBehaviorProfile() bool {
	return c.GetNormalizedBehaviorProfile() == BehaviorProfileBalanced
}

func (c *Config) GetNormalizedSessionOpenTimeout() time.Duration {
	if c.SessionOpenTimeoutSecs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.SessionOpenTimeoutSecs) * time.Second
}

func (c *Config) GetNormalizedSessionIdleTimeout() time.Duration {
	if c.SessionIdleTimeoutSecs <= 0 {
		return 0
	}
	return time.Duration(c.SessionIdleTimeoutSecs) * time.Second
}

func (c *Config) NewRequestBehavior(httpVersion string) *RequestBehavior {
	if !c.IsBalancedBehaviorProfile() {
		return nil
	}

	personas := []requestPersona{
		{
			accept:            "*/*",
			uploadContentType: "application/octet-stream",
			extraHeaders: map[string]string{
				"Accept-Language": "en-US,en;q=0.9",
			},
		},
		{
			accept:            "text/event-stream",
			uploadContentType: "application/octet-stream",
			extraHeaders: map[string]string{
				"Cache-Control": "no-cache",
			},
		},
		{
			accept:             "text/plain, */*;q=0.5",
			uploadContentType:  "text/plain; charset=UTF-8",
			directQueryPadding: true,
			extraHeaders: map[string]string{
				"Accept-Language": "en-US,en;q=0.7",
			},
		},
	}

	persona := personas[int(crypto.RandBetween(0, int64(len(personas))))]
	header := c.GetRequestHeader()
	if header.Get("Accept") == "" && persona.accept != "" {
		header.Set("Accept", persona.accept)
	}
	for key, value := range persona.extraHeaders {
		if header.Get(key) == "" {
			header.Set(key, value)
		}
	}

	return &RequestBehavior{
		header:             header,
		uploadContentType:  persona.uploadContentType,
		directQueryPadding: persona.directQueryPadding,
		httpVersion:        httpVersion,
	}
}

func cloneHeader(src http.Header) http.Header {
	if src == nil {
		return http.Header{}
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
	return dst
}

func (b *RequestBehavior) Header() http.Header {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return cloneHeader(b.header)
}

func (b *RequestBehavior) UploadContentType() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.uploadContentType
}

func (b *RequestBehavior) UseDirectQueryPadding() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.directQueryPadding
}

func (b *RequestBehavior) RecordUpload(chunkLen int32, rtt time.Duration) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if chunkLen > 0 {
		if b.observedChunkLen == 0 {
			b.observedChunkLen = chunkLen
		} else {
			b.observedChunkLen = (b.observedChunkLen*3 + chunkLen) / 4
		}
	}

	if rtt > 0 {
		if b.observedRTT == 0 {
			b.observedRTT = rtt
		} else {
			b.observedRTT = (b.observedRTT*3 + rtt) / 4
		}
	}
}

func (b *RequestBehavior) NextPostInterval(bounds RangeConfig) time.Duration {
	minDelay := time.Duration(bounds.From) * time.Millisecond
	maxDelay := time.Duration(max(bounds.From, bounds.To)) * time.Millisecond
	if maxDelay < minDelay {
		maxDelay = minDelay
	}

	if b == nil {
		return minDelay
	}

	b.mu.Lock()
	observedRTT := b.observedRTT
	b.mu.Unlock()

	delay := minDelay
	if observedRTT > 0 {
		delay = observedRTT / 2
	} else if maxDelay > minDelay {
		delay = minDelay + (maxDelay-minDelay)/2
	}

	jitterWindow := max(int64(delay/3), int64(5*time.Millisecond))
	jitter := time.Duration(crypto.RandBetween(-jitterWindow, jitterWindow+1))
	delay += jitter

	if delay < minDelay {
		delay = minDelay
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func (b *RequestBehavior) NextUploadSize(bounds RangeConfig, pending int32) int32 {
	minSize := bounds.From
	if minSize < 1 {
		minSize = 1
	}
	maxSize := bounds.To
	if maxSize < minSize {
		maxSize = minSize
	}
	target := maxSize

	if b != nil {
		b.mu.Lock()
		lastChunkLen := b.observedChunkLen
		httpVersion := b.httpVersion
		b.mu.Unlock()

		switch httpVersion {
		case "1.1":
			target = 96 * 1024
		case "2":
			target = 192 * 1024
		case "3":
			target = 256 * 1024
		}

		if lastChunkLen > 0 {
			target = lastChunkLen + lastChunkLen/2
		}
	}

	jitterWindow := target / 4
	if jitterWindow < 16*1024 {
		jitterWindow = 16 * 1024
	}
	target += int32(crypto.RandBetween(-int64(jitterWindow), int64(jitterWindow)+1))

	if target < minSize {
		target = minSize
	}
	if target > maxSize {
		target = maxSize
	}
	if pending > 0 && target > pending {
		target = pending
	}
	if target < 1 {
		target = 1
	}
	return target
}

func (c *Config) GetRequestHeaderForBehavior(behavior *RequestBehavior) http.Header {
	if behavior == nil {
		return c.GetRequestHeader()
	}
	return behavior.Header()
}

func (c *Config) GetResponseContentType(request *http.Request) string {
	if !c.IsBalancedBehaviorProfile() {
		if c.NoSSEHeader {
			return ""
		}
		return "text/event-stream"
	}

	accept := strings.ToLower(request.Header.Get("Accept"))
	switch {
	case strings.Contains(accept, "text/event-stream"):
		if c.NoSSEHeader {
			return ""
		}
		return "text/event-stream"
	case strings.Contains(accept, "text/plain"):
		return "text/plain; charset=utf-8"
	case strings.Contains(accept, "application/octet-stream"), strings.Contains(accept, "*/*"):
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

func (c *Config) GetRequestPaddingConfig(requestURL string, behavior *RequestBehavior) XPaddingConfig {
	config := XPaddingConfig{Length: int(c.GetNormalizedXPaddingBytes().rand())}

	if c.XPaddingObfsMode {
		config.Placement = XPaddingPlacement{
			Placement: c.XPaddingPlacement,
			Key:       c.XPaddingKey,
			Header:    c.XPaddingHeader,
			RawURL:    requestURL,
		}
		config.Method = PaddingMethod(c.XPaddingMethod)
		return config
	}

	config.Method = PaddingMethod(c.XPaddingMethod)
	if !c.IsBalancedBehaviorProfile() {
		config.Placement = XPaddingPlacement{
			Placement: PlacementQueryInHeader,
			Key:       "x_padding",
			Header:    "Referer",
			RawURL:    requestURL,
		}
		return config
	}

	if behavior != nil && behavior.UseDirectQueryPadding() {
		config.Placement = XPaddingPlacement{
			Placement: PlacementQuery,
			Key:       "x_padding",
		}
		return config
	}

	config.Placement = XPaddingPlacement{
		Placement: PlacementQueryInHeader,
		Key:       "x_padding",
		Header:    "Referer",
		RawURL:    requestURL,
	}
	return config
}
