package splithttp

import (
	"crypto/rand"
	"math"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http2/hpack"
)

type PaddingMethod string

const (
	PaddingMethodRepeatX  PaddingMethod = "repeat-x"
	PaddingMethodTokenish PaddingMethod = "tokenish"
)

const charsetBase62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Huffman encoding gives ~20% size reduction for base62 sequences
const avgHuffmanBytesPerCharBase62 = 0.8

const validationTolerance = 2

type XPaddingPlacement struct {
	Placement string
	Key       string
	Header    string
	RawURL    string
}

type XPaddingConfig struct {
	Length    int
	Placement XPaddingPlacement
	Method    PaddingMethod
}

func randStringFromCharset(n int, charset string) (string, bool) {
	if n <= 0 || len(charset) == 0 {
		return "", false
	}

	m := len(charset)
	limit := byte(256 - (256 % m))

	result := make([]byte, n)
	i := 0

	buf := make([]byte, 256)
	for i < n {
		if _, err := rand.Read(buf); err != nil {
			return "", false
		}
		for _, rb := range buf {
			if rb >= limit {
				continue
			}
			result[i] = charset[int(rb)%m]
			i++
			if i == n {
				break
			}
		}
	}

	return string(result), true
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func GenerateTokenishPaddingBase62(targetHuffmanBytes int) string {
	n := int(math.Ceil(float64(targetHuffmanBytes) / avgHuffmanBytesPerCharBase62))
	if n < 1 {
		n = 1
	}

	randBase62Str, ok := randStringFromCharset(n, charsetBase62)
	if !ok {
		return ""
	}

	const maxIter = 150
	adjustChar := byte('X')

	// Adjust until close enough
	for iter := 0; iter < maxIter; iter++ {
		currentLength := int(hpack.HuffmanEncodeLength(randBase62Str))
		diff := currentLength - targetHuffmanBytes

		if absInt(diff) <= validationTolerance {
			return randBase62Str
		}

		if diff < 0 {
			// Too small -> append padding char(s)
			randBase62Str += string(adjustChar)

			// Avoid a long run of identical chars
			if adjustChar == 'X' {
				adjustChar = 'Z'
			} else {
				adjustChar = 'X'
			}
		} else {
			// Too big -> remove from the end
			if len(randBase62Str) <= 1 {
				return randBase62Str
			}
			randBase62Str = randBase62Str[:len(randBase62Str)-1]
		}
	}

	return randBase62Str
}

func GeneratePadding(method PaddingMethod, length int) string {
	if length <= 0 {
		return ""
	}

	// https://www.rfc-editor.org/rfc/rfc7541.html#appendix-B
	// h2's HPACK Header Compression feature employs a huffman encoding using a static table.
	// 'X' and 'Z' are assigned an 8 bit code, so HPACK compression won't change actual padding length on the wire.
	// https://www.rfc-editor.org/rfc/rfc9204.html#section-4.1.2-2
	// h3's similar QPACK feature uses the same huffman table.

	switch method {
	case PaddingMethodRepeatX:
		return strings.Repeat("X", length)
	case PaddingMethodTokenish:
		paddingValue := GenerateTokenishPaddingBase62(length)
		if paddingValue == "" {
			return strings.Repeat("X", length)
		}
		return paddingValue
	default:
		return strings.Repeat("X", length)
	}
}

func isCookieValueSafe(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c <= 0x20 || c >= 0x7f:
			return false
		case c == '"' || c == ',' || c == ';' || c == '\\':
			return false
		}
	}
	return true
}

func appendCookieHeader(header http.Header, name, value string) bool {
	if header == nil || !isCookieToken(name) || !isCookieValueSafe(value) {
		return false
	}

	cookie := name + "=" + value
	if existing := header["Cookie"]; len(existing) > 0 {
		cookie = strings.Join(existing, "; ") + "; " + cookie
	}
	header.Set("Cookie", cookie)
	return true
}

func ApplyPaddingToCookie(req *http.Request, name, value string) {
	if req == nil || name == "" || value == "" {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if appendCookieHeader(req.Header, name, value) {
		return
	}
	req.AddCookie(&http.Cookie{
		Name:  name,
		Value: value,
		Path:  "/",
	})
}

func ApplyPaddingToResponseCookie(writer http.ResponseWriter, name, value string) {
	if name == "" || value == "" {
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:  name,
		Value: value,
		Path:  "/",
	})
}

func isURLQueryComponentSafe(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case '0' <= c && c <= '9':
		case 'a' <= c && c <= 'z':
		case 'A' <= c && c <= 'Z':
		case c == '-' || c == '.' || c == '_' || c == '~':
		default:
			return false
		}
	}
	return true
}

func rawQueryHasKey(rawQuery, key string) bool {
	for rawQuery != "" {
		part := rawQuery
		if i := strings.IndexByte(rawQuery, '&'); i >= 0 {
			part = rawQuery[:i]
			rawQuery = rawQuery[i+1:]
		} else {
			rawQuery = ""
		}
		if j := strings.IndexByte(part, '='); j >= 0 {
			part = part[:j]
		}
		if part == key {
			return true
		}
	}
	return false
}

func setURLQueryParam(u *url.URL, key, value string) {
	if u == nil || key == "" || value == "" {
		return
	}
	if isURLQueryComponentSafe(key) && isURLQueryComponentSafe(value) && strings.IndexByte(u.RawQuery, ';') < 0 && !rawQueryHasKey(u.RawQuery, key) {
		if u.RawQuery == "" {
			u.RawQuery = key + "=" + value
		} else {
			u.RawQuery += "&" + key + "=" + value
		}
		return
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
}

func appendQueryParamToRawURL(rawURL, key, value string) (string, bool) {
	if rawURL == "" || !isURLQueryComponentSafe(key) || !isURLQueryComponentSafe(value) {
		return "", false
	}

	fragment := ""
	if fragmentStart := strings.IndexByte(rawURL, '#'); fragmentStart >= 0 {
		fragment = rawURL[fragmentStart:]
		rawURL = rawURL[:fragmentStart]
	}

	queryStart := strings.IndexByte(rawURL, '?')
	if queryStart < 0 {
		return rawURL + "?" + key + "=" + value + fragment, true
	}

	rawQuery := rawURL[queryStart+1:]
	if strings.IndexByte(rawQuery, ';') >= 0 || rawQueryHasKey(rawQuery, key) {
		return "", false
	}
	if rawQuery == "" {
		return rawURL + key + "=" + value + fragment, true
	}
	return rawURL + "&" + key + "=" + value + fragment, true
}

func extractQueryParamFromRawURL(rawURL, key string) (string, bool) {
	if !isURLQueryComponentSafe(key) {
		return "", false
	}
	queryStart := strings.IndexByte(rawURL, '?')
	if queryStart < 0 || queryStart+1 >= len(rawURL) {
		return "", false
	}
	query := rawURL[queryStart+1:]
	if fragmentStart := strings.IndexByte(query, '#'); fragmentStart >= 0 {
		query = query[:fragmentStart]
	}
	for query != "" {
		part := query
		if i := strings.IndexByte(query, '&'); i >= 0 {
			part = query[:i]
			query = query[i+1:]
		} else {
			query = ""
		}
		value := ""
		if i := strings.IndexByte(part, '='); i >= 0 {
			value = part[i+1:]
			part = part[:i]
		}
		if part != key {
			continue
		}
		if isURLQueryComponentSafe(value) {
			return value, true
		}
		return "", false
	}
	return "", false
}

func ApplyPaddingToQuery(u *url.URL, key, value string) {
	setURLQueryParam(u, key, value)
}

func (c *Config) GetNormalizedXPaddingBytes() RangeConfig {
	if c.XPaddingBytes == nil || c.XPaddingBytes.To == 0 {
		if c.IsBalancedBehaviorProfile() {
			return RangeConfig{
				From: 80,
				To:   640,
			}
		}
		return RangeConfig{
			From: 100,
			To:   1000,
		}
	}

	return *c.XPaddingBytes
}

func (c *Config) ApplyXPaddingToHeader(h http.Header, config XPaddingConfig) {
	if h == nil {
		return
	}

	paddingValue := GeneratePadding(config.Method, config.Length)

	switch p := config.Placement; p.Placement {
	case PlacementHeader:
		h.Set(p.Header, paddingValue)
	case PlacementQueryInHeader:
		if rawURL, ok := appendQueryParamToRawURL(p.RawURL, p.Key, paddingValue); ok {
			h.Set(p.Header, rawURL)
			return
		}
		u, err := url.Parse(p.RawURL)
		if err != nil || u == nil {
			return
		}
		setURLQueryParam(u, p.Key, paddingValue)
		h.Set(p.Header, u.String())
	}
}

func (c *Config) ApplyXPaddingToRequest(req *http.Request, config XPaddingConfig) {
	if req == nil {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}

	placement := config.Placement.Placement

	if placement == PlacementHeader || placement == PlacementQueryInHeader {
		c.ApplyXPaddingToHeader(req.Header, config)
		return
	}

	paddingValue := GeneratePadding(config.Method, config.Length)

	switch placement {
	case PlacementCookie:
		ApplyPaddingToCookie(req, config.Placement.Key, paddingValue)
	case PlacementQuery:
		ApplyPaddingToQuery(req.URL, config.Placement.Key, paddingValue)
	}
}

func (c *Config) ApplyXPaddingToResponse(writer http.ResponseWriter, config XPaddingConfig) {
	placement := config.Placement.Placement

	if placement == PlacementHeader || placement == PlacementQueryInHeader {
		c.ApplyXPaddingToHeader(writer.Header(), config)
		return
	}

	paddingValue := GeneratePadding(config.Method, config.Length)

	switch placement {
	case PlacementCookie:
		ApplyPaddingToResponseCookie(writer, config.Placement.Key, paddingValue)
	}
}

func (c *Config) ExtractXPaddingFromRequest(req *http.Request, obfsMode bool) (string, string) {
	if req == nil {
		return "", ""
	}

	if !obfsMode {
		referrer := req.Header.Get("Referer")

		if referrer != "" {
			if paddingValue, ok := extractQueryParamFromRawURL(referrer, "x_padding"); ok {
				paddingPlacement := PlacementQueryInHeader + "=Referer, key=x_padding"
				return paddingValue, paddingPlacement
			}
			if referrerURL, err := url.Parse(referrer); err == nil {
				paddingValue := referrerURL.Query().Get("x_padding")
				paddingPlacement := PlacementQueryInHeader + "=Referer, key=x_padding"
				return paddingValue, paddingPlacement
			}
		} else {
			paddingValue := req.URL.Query().Get("x_padding")
			return paddingValue, PlacementQuery + ", key=x_padding"
		}
	}

	key := c.GetNormalizedXPaddingKey()
	header := c.GetNormalizedXPaddingHeader()
	placement := c.GetNormalizedXPaddingPlacement()

	if cookie, err := req.Cookie(key); err == nil {
		if cookie != nil && cookie.Value != "" {
			paddingValue := cookie.Value
			paddingPlacement := PlacementCookie + ", key=" + key
			return paddingValue, paddingPlacement
		}
	}

	headerValue := req.Header.Get(header)

	if headerValue != "" {
		if placement == PlacementHeader {
			paddingPlacement := PlacementHeader + "=" + header
			return headerValue, paddingPlacement
		}

		if paddingValue, ok := extractQueryParamFromRawURL(headerValue, key); ok {
			paddingPlacement := PlacementQueryInHeader + "=" + header + ", key=" + key
			return paddingValue, paddingPlacement
		}
		if parsedURL, err := url.Parse(headerValue); err == nil {
			paddingPlacement := PlacementQueryInHeader + "=" + header + ", key=" + key

			return parsedURL.Query().Get(key), paddingPlacement
		}
	}

	queryValue := req.URL.Query().Get(key)

	if queryValue != "" {
		paddingPlacement := PlacementQuery + ", key=" + key
		return queryValue, paddingPlacement
	}

	return "", ""
}

func (c *Config) getImplicitDefaultPaddingValidationRange() (int32, int32, bool) {
	if c == nil || (c.XPaddingBytes != nil && c.XPaddingBytes.To != 0) {
		return 0, 0, false
	}
	return 80, 1000, true
}

func (c *Config) IsPaddingValid(paddingValue string, from, to int32, method PaddingMethod) bool {
	if paddingValue == "" {
		return false
	}
	if implicitFrom, implicitTo, ok := c.getImplicitDefaultPaddingValidationRange(); ok {
		from, to = implicitFrom, implicitTo
	} else if to <= 0 {
		r := c.GetNormalizedXPaddingBytes()
		from, to = r.From, r.To
	}

	switch method {
	case PaddingMethodRepeatX:
		n := int32(len(paddingValue))
		return n >= from && n <= to
	case PaddingMethodTokenish:
		const tolerance = int32(validationTolerance)

		n := int32(hpack.HuffmanEncodeLength(paddingValue))
		f := from - tolerance
		t := to + tolerance
		if f < 0 {
			f = 0
		}
		return n >= f && n <= t
	default:
		n := int32(len(paddingValue))
		return n >= from && n <= to
	}
}
