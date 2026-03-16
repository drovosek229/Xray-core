package splithttp

import (
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/crypto"
	"github.com/xtls/xray-core/common/utils"
	"github.com/xtls/xray-core/transport/internet"
)

func (c *Config) GetNormalizedPath() string {
	pathAndQuery := strings.SplitN(c.Path, "?", 2)
	path := pathAndQuery[0]

	if path == "" || path[0] != '/' {
		path = "/" + path
	}

	if path[len(path)-1] != '/' {
		path = path + "/"
	}

	return path
}

func (c *Config) GetNormalizedQuery() string {
	pathAndQuery := strings.SplitN(c.Path, "?", 2)
	query := ""

	if len(pathAndQuery) > 1 {
		query = pathAndQuery[1]
	}

	/*
		if query != "" {
			query += "&"
		}
		query += "x_version=" + core.Version()
	*/

	return query
}

func (c *Config) GetRequestHeader() http.Header {
	header := http.Header{}
	for k, v := range c.Headers {
		header.Add(k, v)
	}
	if header.Get("User-Agent") == "" {
		header.Set("User-Agent", utils.ChromeUA)
	}
	return header
}

func (c *Config) GetRequestHeaderWithPayload(payload []byte) http.Header {
	header := c.GetRequestHeader()

	key := c.GetNormalizedUplinkDataKey()
	if key == "" {
		return header
	}
	encodedData := base64.RawURLEncoding.EncodeToString(payload)

	for i := 0; len(encodedData) > 0; i++ {
		chunkSize := min(int(c.GetNormalizedUplinkChunkSize().rand()), len(encodedData))
		chunk := encodedData[:chunkSize]
		encodedData = encodedData[chunkSize:]
		headerKey := key + "-" + strconv.Itoa(i)
		header.Set(headerKey, chunk)
	}

	return header
}

func (c *Config) GetRequestCookiesWithPayload(payload []byte) []*http.Cookie {
	cookies := []*http.Cookie{}

	key := c.GetNormalizedUplinkDataKey()
	if key == "" {
		return cookies
	}
	encodedData := base64.RawURLEncoding.EncodeToString(payload)

	for i := 0; len(encodedData) > 0; i++ {
		chunkSize := min(int(c.GetNormalizedUplinkChunkSize().rand()), len(encodedData))
		chunk := encodedData[:chunkSize]
		encodedData = encodedData[chunkSize:]
		cookieName := key + "_" + strconv.Itoa(i)
		cookies = append(cookies, &http.Cookie{Name: cookieName, Value: chunk})
	}

	return cookies
}

func isCookieToken(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case '0' <= c && c <= '9':
		case 'a' <= c && c <= 'z':
		case 'A' <= c && c <= 'Z':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' || c == '*' || c == '+' || c == '-' || c == '.' || c == '^' || c == '_' || c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

func (c *Config) WriteResponseHeader(writer http.ResponseWriter, requestMethod string, requestHeader http.Header) {
	// CORS headers for the browser dialer
	if origin := requestHeader.Get("Origin"); origin == "" {
		writer.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		// Chrome says: The value of the 'Access-Control-Allow-Origin' header in the response must not be the wildcard '*' when the request's credentials mode is 'include'.
		writer.Header().Set("Access-Control-Allow-Origin", origin)
	}

	if c.GetNormalizedSessionPlacement() == PlacementCookie ||
		c.GetNormalizedSeqPlacement() == PlacementCookie ||
		c.GetNormalizedXPaddingPlacement() == PlacementCookie ||
		c.GetNormalizedUplinkDataPlacement() == PlacementCookie {
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if requestMethod == "OPTIONS" {
		requestedMethod := requestHeader.Get("Access-Control-Request-Method")
		if requestedMethod != "" {
			writer.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		} else {
			writer.Header().Set("Access-Control-Allow-Methods", "*")
		}

		requestedHeaders := requestHeader.Get("Access-Control-Request-Headers")
		if requestedHeaders == "" {
			writer.Header().Set("Access-Control-Allow-Headers", "*")
		} else {
			writer.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		}
	}
}

func (c *Config) GetNormalizedUplinkHTTPMethod() string {
	if method := strings.TrimSpace(c.UplinkHTTPMethod); method != "" {
		return strings.ToUpper(method)
	}

	return "POST"
}

func (c *Config) GetNormalizedScMaxEachPostBytes() RangeConfig {
	if c.ScMaxEachPostBytes == nil || c.ScMaxEachPostBytes.To == 0 {
		if c.IsBalancedBehaviorProfile() {
			return RangeConfig{
				From: 128 * 1024,
				To:   512 * 1024,
			}
		}
		return RangeConfig{
			From: 1000000,
			To:   1000000,
		}
	}

	return *c.ScMaxEachPostBytes
}

func (c *Config) GetNormalizedScMinPostsIntervalMs() RangeConfig {
	if c.ScMinPostsIntervalMs == nil || c.ScMinPostsIntervalMs.To == 0 {
		if c.IsBalancedBehaviorProfile() {
			return RangeConfig{
				From: 20,
				To:   90,
			}
		}
		return RangeConfig{
			From: 30,
			To:   30,
		}
	}

	return *c.ScMinPostsIntervalMs
}

func (c *Config) GetNormalizedScMaxBufferedPosts() int {
	if c.ScMaxBufferedPosts == 0 {
		return 30
	}

	return int(c.ScMaxBufferedPosts)
}

func (c *Config) GetNormalizedScStreamUpServerSecs() RangeConfig {
	if c.ScStreamUpServerSecs == nil || c.ScStreamUpServerSecs.To == 0 {
		return RangeConfig{
			From: 20,
			To:   80,
		}
	}

	return *c.ScStreamUpServerSecs
}

func (c *Config) GetNormalizedUplinkChunkSize() RangeConfig {
	if c.UplinkChunkSize == nil || c.UplinkChunkSize.To == 0 {
		switch c.UplinkDataPlacement {
		case PlacementCookie:
			return RangeConfig{
				From: 2 * 1024, // 2 KiB
				To:   3 * 1024, // 3 KiB
			}
		case PlacementHeader:
			return RangeConfig{
				From: 3 * 1000, // 3 KB
				To:   4 * 1000, // 4 KB
			}
		default:
			return c.GetNormalizedScMaxEachPostBytes()
		}
	} else if c.UplinkChunkSize.From < 64 {
		return RangeConfig{
			From: 64,
			To:   max(64, c.UplinkChunkSize.To),
		}
	}

	return *c.UplinkChunkSize
}

func (c *Config) GetNormalizedServerMaxHeaderBytes() int {
	if c.ServerMaxHeaderBytes <= 0 {
		return 8192
	} else {
		return int(c.ServerMaxHeaderBytes)
	}
}

func (c *Config) GetNormalizedSessionPlacement() string {
	if c.SessionPlacement == "" {
		return PlacementPath
	}
	return c.SessionPlacement
}

func (c *Config) GetNormalizedSeqPlacement() string {
	if c.SeqPlacement == "" {
		return PlacementPath
	}
	return c.SeqPlacement
}

func (c *Config) GetNormalizedUplinkDataPlacement() string {
	if c.UplinkDataPlacement == "" {
		return PlacementBody
	}
	return c.UplinkDataPlacement
}

func (c *Config) GetNormalizedUplinkDataKey() string {
	if c.UplinkDataKey != "" {
		return c.UplinkDataKey
	}
	switch c.GetNormalizedUplinkDataPlacement() {
	case PlacementHeader:
		return "X-Data"
	case PlacementCookie, PlacementQuery:
		return "x_data"
	default:
		return ""
	}
}

func (c *Config) GetNormalizedXPaddingPlacement() string {
	if c.XPaddingPlacement != "" {
		return c.XPaddingPlacement
	}
	return PlacementQueryInHeader
}

func (c *Config) GetNormalizedXPaddingKey() string {
	if c.XPaddingKey != "" {
		return c.XPaddingKey
	}
	return "x_padding"
}

func (c *Config) GetNormalizedXPaddingHeader() string {
	if c.XPaddingHeader != "" {
		return c.XPaddingHeader
	}
	switch c.GetNormalizedXPaddingPlacement() {
	case PlacementHeader:
		return "X-Padding"
	case PlacementQueryInHeader:
		return "Referer"
	default:
		return ""
	}
}

func (c *Config) GetNormalizedXPaddingMethod() string {
	switch PaddingMethod(c.XPaddingMethod) {
	case PaddingMethodTokenish:
		return string(PaddingMethodTokenish)
	case PaddingMethodRepeatX:
		return string(PaddingMethodRepeatX)
	default:
		return string(PaddingMethodRepeatX)
	}
}

func (c *Config) GetNormalizedSessionKey() string {
	if c.SessionKey != "" {
		return c.SessionKey
	}
	switch c.GetNormalizedSessionPlacement() {
	case PlacementHeader:
		return "X-Session"
	case PlacementCookie, PlacementQuery:
		return "x_session"
	default:
		return ""
	}
}

func (c *Config) GetNormalizedSeqKey() string {
	if c.SeqKey != "" {
		return c.SeqKey
	}
	switch c.GetNormalizedSeqPlacement() {
	case PlacementHeader:
		return "X-Seq"
	case PlacementCookie, PlacementQuery:
		return "x_seq"
	default:
		return ""
	}
}

func (c *Config) ApplyMetaToRequest(req *http.Request, sessionId string, seqStr string) {
	sessionPlacement := c.GetNormalizedSessionPlacement()
	seqPlacement := c.GetNormalizedSeqPlacement()
	sessionKey := c.GetNormalizedSessionKey()
	seqKey := c.GetNormalizedSeqKey()

	if sessionId != "" {
		switch sessionPlacement {
		case PlacementPath:
			req.URL.Path = appendToPath(req.URL.Path, sessionId)
		case PlacementQuery:
			q := req.URL.Query()
			q.Set(sessionKey, sessionId)
			req.URL.RawQuery = q.Encode()
		case PlacementHeader:
			req.Header.Set(sessionKey, sessionId)
		case PlacementCookie:
			req.AddCookie(&http.Cookie{Name: sessionKey, Value: sessionId})
		}
	}

	if seqStr != "" {
		switch seqPlacement {
		case PlacementPath:
			req.URL.Path = appendToPath(req.URL.Path, seqStr)
		case PlacementQuery:
			q := req.URL.Query()
			q.Set(seqKey, seqStr)
			req.URL.RawQuery = q.Encode()
		case PlacementHeader:
			req.Header.Set(seqKey, seqStr)
		case PlacementCookie:
			req.AddCookie(&http.Cookie{Name: seqKey, Value: seqStr})
		}
	}
}

func (c *Config) FillStreamRequest(request *http.Request, sessionId string, seqStr string, behavior *RequestBehavior) {
	request.Header = c.GetRequestHeaderForBehavior(behavior)
	c.ApplyXPaddingToRequest(request, c.GetRequestPaddingConfig(request.URL.String(), behavior))
	c.ApplyMetaToRequest(request, sessionId, "")

	if request.Body != nil && request.Header.Get("Content-Type") == "" {
		if !c.IsBalancedBehaviorProfile() {
			if !c.NoGRPCHeader {
				request.Header.Set("Content-Type", "application/grpc")
			}
		} else if contentType := behavior.UploadContentType(); contentType != "" {
			if contentType != "application/grpc" || !c.NoGRPCHeader {
				request.Header.Set("Content-Type", contentType)
			}
		}
	}
}

func (c *Config) FillPacketRequest(request *http.Request, sessionId string, seqStr string, behavior *RequestBehavior) error {
	dataPlacement := c.GetNormalizedUplinkDataPlacement()

	if dataPlacement == PlacementBody || dataPlacement == PlacementAuto {
		request.Header = c.GetRequestHeaderForBehavior(behavior)
	} else {
		var data []byte
		var err error
		if request.Body != nil {
			data, err = io.ReadAll(request.Body)
			if err != nil {
				return err
			}
		}
		request.Body = nil
		request.ContentLength = 0
		switch dataPlacement {
		case PlacementHeader:
			request.Header = c.GetRequestHeaderForBehavior(behavior)
			if key := c.GetNormalizedUplinkDataKey(); key != "" {
				encodedData := base64.RawURLEncoding.EncodeToString(data)
				for i := 0; len(encodedData) > 0; i++ {
					chunkSize := min(int(c.GetNormalizedUplinkChunkSize().rand()), len(encodedData))
					chunk := encodedData[:chunkSize]
					encodedData = encodedData[chunkSize:]
					request.Header.Set(key+"-"+strconv.Itoa(i), chunk)
				}
			}
		case PlacementCookie:
			request.Header = c.GetRequestHeaderForBehavior(behavior)
			if key := c.GetNormalizedUplinkDataKey(); key != "" {
				encodedData := base64.RawURLEncoding.EncodeToString(data)
				if isCookieToken(key) {
					var builder strings.Builder
					if existingCookie := request.Header.Get("Cookie"); existingCookie != "" {
						builder.WriteString(existingCookie)
					}
					for i := 0; len(encodedData) > 0; i++ {
						chunkSize := min(int(c.GetNormalizedUplinkChunkSize().rand()), len(encodedData))
						chunk := encodedData[:chunkSize]
						encodedData = encodedData[chunkSize:]
						if builder.Len() > 0 {
							builder.WriteString("; ")
						}
						builder.WriteString(key)
						builder.WriteByte('_')
						builder.WriteString(strconv.Itoa(i))
						builder.WriteByte('=')
						builder.WriteString(chunk)
					}
					request.Header.Set("Cookie", builder.String())
				} else {
					for i := 0; len(encodedData) > 0; i++ {
						chunkSize := min(int(c.GetNormalizedUplinkChunkSize().rand()), len(encodedData))
						chunk := encodedData[:chunkSize]
						encodedData = encodedData[chunkSize:]
						request.AddCookie(&http.Cookie{Name: key + "_" + strconv.Itoa(i), Value: chunk})
					}
				}
			}
		}
	}

	if request.Body != nil && request.Header.Get("Content-Type") == "" && c.IsBalancedBehaviorProfile() {
		if contentType := behavior.UploadContentType(); contentType != "" {
			if contentType != "application/grpc" || !c.NoGRPCHeader {
				request.Header.Set("Content-Type", contentType)
			}
		}
	}

	c.ApplyXPaddingToRequest(request, c.GetRequestPaddingConfig(request.URL.String(), behavior))
	c.ApplyMetaToRequest(request, sessionId, seqStr)

	return nil
}

func (c *Config) ExtractMetaFromRequest(req *http.Request, path string) (sessionId string, seqStr string) {
	sessionPlacement := c.GetNormalizedSessionPlacement()
	seqPlacement := c.GetNormalizedSeqPlacement()
	sessionKey := c.GetNormalizedSessionKey()
	seqKey := c.GetNormalizedSeqKey()

	var subpath []string
	pathPart := 0
	if sessionPlacement == PlacementPath || seqPlacement == PlacementPath {
		subpath = strings.Split(req.URL.Path[len(path):], "/")
	}

	switch sessionPlacement {
	case PlacementPath:
		if len(subpath) > pathPart {
			sessionId = subpath[pathPart]
			pathPart += 1
		}
	case PlacementQuery:
		sessionId = req.URL.Query().Get(sessionKey)
	case PlacementHeader:
		sessionId = req.Header.Get(sessionKey)
	case PlacementCookie:
		if cookie, e := req.Cookie(sessionKey); e == nil {
			sessionId = cookie.Value
		}
	}

	switch seqPlacement {
	case PlacementPath:
		if len(subpath) > pathPart {
			seqStr = subpath[pathPart]
			pathPart += 1
		}
	case PlacementQuery:
		seqStr = req.URL.Query().Get(seqKey)
	case PlacementHeader:
		seqStr = req.Header.Get(seqKey)
	case PlacementCookie:
		if cookie, e := req.Cookie(seqKey); e == nil {
			seqStr = cookie.Value
		}
	}

	return sessionId, seqStr
}

func (m *XmuxConfig) GetNormalizedMaxConcurrency() RangeConfig {
	if m.MaxConcurrency == nil {
		return RangeConfig{
			From: 0,
			To:   0,
		}
	}

	return *m.MaxConcurrency
}

func (m *XmuxConfig) GetNormalizedMaxConnections() RangeConfig {
	if m.MaxConnections == nil {
		return RangeConfig{
			From: 0,
			To:   0,
		}
	}

	return *m.MaxConnections
}

func (m *XmuxConfig) GetNormalizedCMaxReuseTimes() RangeConfig {
	if m.CMaxReuseTimes == nil {
		return RangeConfig{
			From: 0,
			To:   0,
		}
	}

	return *m.CMaxReuseTimes
}

func (m *XmuxConfig) GetNormalizedHMaxRequestTimes() RangeConfig {
	if m.HMaxRequestTimes == nil {
		return RangeConfig{
			From: 0,
			To:   0,
		}
	}

	return *m.HMaxRequestTimes
}

func (m *XmuxConfig) GetNormalizedHMaxReusableSecs() RangeConfig {
	if m.HMaxReusableSecs == nil {
		return RangeConfig{
			From: 0,
			To:   0,
		}
	}

	return *m.HMaxReusableSecs
}

func (m *XmuxConfig) GetNormalizedWarmConnections() int32 {
	if m == nil || m.WarmConnections <= 0 {
		return 0
	}

	return m.WarmConnections
}

func init() {
	common.Must(internet.RegisterProtocolConfigCreator(protocolName, func() interface{} {
		return new(Config)
	}))
}

func (c RangeConfig) rand() int32 {
	return int32(crypto.RandBetween(int64(c.From), int64(c.To)))
}

func appendToPath(path, value string) string {
	if strings.HasSuffix(path, "/") {
		return path + value
	}
	return path + "/" + value
}
