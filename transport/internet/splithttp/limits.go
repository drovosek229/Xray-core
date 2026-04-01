package splithttp

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/drovosek229/Xray-core/common/errors"
)

const (
	estimatedSessionIDLength = 36
	estimatedSeqLength       = 20
)

func cloneRangeConfig(input *RangeConfig) *RangeConfig {
	if input == nil {
		return nil
	}

	cloned := *input
	return &cloned
}

func fixedRangeConfig(value int32) *RangeConfig {
	return &RangeConfig{
		From: value,
		To:   value,
	}
}

func isZeroRangeConfig(config *RangeConfig) bool {
	return config == nil || (config.From == 0 && config.To == 0)
}

func mergeXmuxRange(defaultValue, override *RangeConfig) *RangeConfig {
	if !isZeroRangeConfig(override) {
		return cloneRangeConfig(override)
	}
	if isZeroRangeConfig(defaultValue) {
		return nil
	}
	return cloneRangeConfig(defaultValue)
}

func (c *Config) defaultXmuxConfig(httpVersion string) XmuxConfig {
	if !c.IsBalancedBehaviorProfile() {
		return XmuxConfig{
			MaxConcurrency:   &RangeConfig{From: 1, To: 1},
			HMaxRequestTimes: &RangeConfig{From: 600, To: 900},
			HMaxReusableSecs: &RangeConfig{From: 1800, To: 3000},
		}
	}

	switch httpVersion {
	case "1.1":
		return XmuxConfig{
			MaxConcurrency:   &RangeConfig{From: 1, To: 1},
			HMaxRequestTimes: &RangeConfig{From: 32, To: 96},
			HMaxReusableSecs: &RangeConfig{From: 45, To: 180},
		}
	default:
		return XmuxConfig{
			MaxConcurrency:   &RangeConfig{From: 1, To: 2},
			HMaxRequestTimes: &RangeConfig{From: 400, To: 800},
			HMaxReusableSecs: &RangeConfig{From: 1200, To: 2400},
			HKeepAlivePeriod: 30,
		}
	}
}

func (c *Config) GetConfiguredXmux(httpVersion string) XmuxConfig {
	merged := c.defaultXmuxConfig(httpVersion)
	if c.Xmux != nil {
		overrideHasMaxConcurrency := !isZeroRangeConfig(c.Xmux.MaxConcurrency)
		overrideHasMaxConnections := !isZeroRangeConfig(c.Xmux.MaxConnections)

		if overrideHasMaxConnections && !overrideHasMaxConcurrency {
			merged.MaxConcurrency = nil
		}

		merged.MaxConcurrency = mergeXmuxRange(merged.MaxConcurrency, c.Xmux.MaxConcurrency)
		merged.MaxConnections = mergeXmuxRange(merged.MaxConnections, c.Xmux.MaxConnections)
		merged.CMaxReuseTimes = mergeXmuxRange(merged.CMaxReuseTimes, c.Xmux.CMaxReuseTimes)
		merged.HMaxRequestTimes = mergeXmuxRange(merged.HMaxRequestTimes, c.Xmux.HMaxRequestTimes)
		merged.HMaxReusableSecs = mergeXmuxRange(merged.HMaxReusableSecs, c.Xmux.HMaxReusableSecs)
		if c.Xmux.HKeepAlivePeriod > 0 {
			merged.HKeepAlivePeriod = c.Xmux.HKeepAlivePeriod
		}
		if c.Xmux.WarmConnections > 0 {
			merged.WarmConnections = c.Xmux.WarmConnections
		}
	}

	if httpVersion == "1.1" {
		merged.WarmConnections = 0
	}
	if merged.WarmConnections > 0 && merged.HKeepAlivePeriod == 0 {
		merged.HKeepAlivePeriod = 30
	}

	return merged
}

func getConfiguredXmux(config *Config, httpVersion string) XmuxConfig {
	return config.GetConfiguredXmux(httpVersion)
}

func (c *Config) headerBudgetConfig() *Config {
	cloned := *c
	paddingRange := c.GetNormalizedXPaddingBytes()
	chunkRange := c.GetNormalizedUplinkChunkSize()

	cloned.XPaddingBytes = fixedRangeConfig(paddingRange.To)
	cloned.UplinkChunkSize = fixedRangeConfig(chunkRange.From)

	return &cloned
}

func (c *Config) requestURLForEstimation() url.URL {
	requestURL := url.URL{
		Scheme: "http",
		Host:   c.Host,
		Path:   c.GetNormalizedPath(),
	}
	if requestURL.Host == "" {
		requestURL.Host = "example.com"
	}
	requestURL.RawQuery = c.GetNormalizedQuery()

	return requestURL
}

func (c *Config) estimationBehaviors() []*RequestBehavior {
	if !c.IsBalancedBehaviorProfile() {
		return []*RequestBehavior{nil}
	}

	behaviors := make([]*RequestBehavior, 0, len(balancedRequestPersonas))
	for _, persona := range balancedRequestPersonas {
		header := c.GetRequestHeader()
		if header.Get("Accept") == "" && persona.accept != "" {
			header.Set("Accept", persona.accept)
		}
		for key, value := range persona.extraHeaders {
			if header.Get(key) == "" {
				header.Set(key, value)
			}
		}

		behaviors = append(behaviors, &RequestBehavior{
			header:             header,
			uploadContentType:  persona.uploadContentType,
			directQueryPadding: persona.directQueryPadding,
		})
	}

	return behaviors
}

func (c *Config) estimatePacketRequestHeaderBytes(payloadSize int, behavior *RequestBehavior) (int, error) {
	payload := bytes.Repeat([]byte{'A'}, payloadSize)
	requestURL := c.requestURLForEstimation()
	request, err := http.NewRequest(c.GetNormalizedUplinkHTTPMethod(), requestURL.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.ContentLength = int64(len(payload))

	if err := c.FillPacketRequest(
		request,
		strings.Repeat("s", estimatedSessionIDLength),
		strings.Repeat("9", estimatedSeqLength),
		behavior,
	); err != nil {
		return 0, err
	}

	requestBytes, err := buildHTTPRequestBytes(request)
	if err != nil {
		return 0, err
	}

	if headerEnd := bytes.Index(requestBytes, []byte("\r\n\r\n")); headerEnd >= 0 {
		return headerEnd + len("\r\n\r\n"), nil
	}

	return len(requestBytes), nil
}

func (c *Config) maxEstimatedPacketRequestHeaderBytes(payloadSize int) (int, error) {
	maxHeaderBytes := 0
	for _, behavior := range c.estimationBehaviors() {
		headerBytes, err := c.estimatePacketRequestHeaderBytes(payloadSize, behavior)
		if err != nil {
			return 0, err
		}
		if headerBytes > maxHeaderBytes {
			maxHeaderBytes = headerBytes
		}
	}

	return maxHeaderBytes, nil
}

func (c *Config) GetPacketUpHeaderBudgetCap() (int32, error) {
	switch c.GetNormalizedUplinkDataPlacement() {
	case PlacementHeader, PlacementCookie:
	default:
		return 0, nil
	}

	budgetConfig := c.headerBudgetConfig()
	headerBudget := budgetConfig.GetNormalizedServerMaxHeaderBytes()

	baseHeaderBytes, err := budgetConfig.maxEstimatedPacketRequestHeaderBytes(0)
	if err != nil {
		return 0, errors.New("failed to estimate packet-up header size").Base(err)
	}
	if baseHeaderBytes > headerBudget {
		return 0, errors.New("serverMaxHeaderBytes is too small for packet-up metadata")
	}

	low := int32(0)
	high := int32(max(1, headerBudget))

	for low < high {
		mid := low + (high-low+1)/2
		headerBytes, err := budgetConfig.maxEstimatedPacketRequestHeaderBytes(int(mid))
		if err != nil {
			return 0, errors.New("failed to estimate packet-up header size").Base(err)
		}
		if headerBytes <= headerBudget {
			low = mid
		} else {
			high = mid - 1
		}
	}

	return low, nil
}
