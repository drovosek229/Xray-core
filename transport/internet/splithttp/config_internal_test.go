package splithttp

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestApplyXPaddingToHeaderPreservesExistingQuery(t *testing.T) {
	config := &Config{}
	headers := make(http.Header)

	config.ApplyXPaddingToHeader(headers, XPaddingConfig{
		Length: 12,
		Method: PaddingMethodRepeatX,
		Placement: XPaddingPlacement{
			Placement: PlacementQueryInHeader,
			Key:       "x_padding",
			Header:    "Referer",
			RawURL:    "https://example.com/xhttp?ed=2048&foo=bar",
		},
	})

	referer := headers.Get("Referer")
	if referer == "" {
		t.Fatal("expected Referer header to be set")
	}

	refererURL, err := url.Parse(referer)
	if err != nil {
		t.Fatalf("failed to parse Referer header: %v", err)
	}
	if got := refererURL.Query().Get("ed"); got != "2048" {
		t.Fatalf("expected original query parameter to be preserved, got %q", got)
	}
	if got := refererURL.Query().Get("foo"); got != "bar" {
		t.Fatalf("expected original foo parameter to be preserved, got %q", got)
	}
	if got := refererURL.Query().Get("x_padding"); got != "XXXXXXXXXXXX" {
		t.Fatalf("expected x_padding to be added, got %q", got)
	}
}

func TestNormalizedXPaddingDefaultsForObfsMode(t *testing.T) {
	config := &Config{XPaddingObfsMode: true}
	paddingConfig := config.GetRequestPaddingConfig("https://example.com/test?ed=2048", nil)

	if paddingConfig.Placement.Placement != PlacementQueryInHeader {
		t.Fatalf("expected default obfs placement %q, got %q", PlacementQueryInHeader, paddingConfig.Placement.Placement)
	}
	if paddingConfig.Placement.Key != "x_padding" {
		t.Fatalf("expected default obfs key x_padding, got %q", paddingConfig.Placement.Key)
	}
	if paddingConfig.Placement.Header != "Referer" {
		t.Fatalf("expected default obfs header Referer, got %q", paddingConfig.Placement.Header)
	}
	if paddingConfig.Method != PaddingMethodRepeatX {
		t.Fatalf("expected default obfs method %q, got %q", PaddingMethodRepeatX, paddingConfig.Method)
	}
}

func TestNormalizedUplinkDataKeyDefaults(t *testing.T) {
	headerConfig := &Config{UplinkDataPlacement: PlacementHeader}
	headerPayload := headerConfig.GetRequestHeaderWithPayload([]byte("payload"))
	if got := headerPayload.Get("X-Data-0"); got == "" {
		t.Fatal("expected default header payload key X-Data-0 to be populated")
	}

	cookieConfig := &Config{UplinkDataPlacement: PlacementCookie}
	cookies := cookieConfig.GetRequestCookiesWithPayload([]byte("payload"))
	if len(cookies) == 0 {
		t.Fatal("expected default cookie payload key to be populated")
	}
	if got := cookies[0].Name; got != "x_data_0" {
		t.Fatalf("expected default cookie payload key x_data_0, got %q", got)
	}
}

func TestNormalizedUplinkHTTPMethodUppercases(t *testing.T) {
	config := &Config{UplinkHTTPMethod: "  delete  "}
	if got := config.GetNormalizedUplinkHTTPMethod(); got != http.MethodDelete {
		t.Fatalf("expected normalized method %q, got %q", http.MethodDelete, got)
	}
}

func TestExtractXPaddingFromRequestUsesNormalizedObfsDefaults(t *testing.T) {
	config := &Config{XPaddingObfsMode: true}
	request, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Referer", "https://example.com/test?x_padding=XXXXXXXX")

	paddingValue, placement := config.ExtractXPaddingFromRequest(request, true)
	if paddingValue != "XXXXXXXX" {
		t.Fatalf("expected normalized default padding to be extracted, got %q", paddingValue)
	}
	if placement == "" {
		t.Fatal("expected padding placement description")
	}
}

func TestGetRequestHeaderWithPayloadLeavesBaseHeadersWhenPayloadKeyUnavailable(t *testing.T) {
	config := &Config{Headers: map[string]string{"X-Test": "ok"}}
	headers := config.GetRequestHeaderWithPayload(nil)
	if got := headers.Get("X-Test"); got != "ok" {
		t.Fatalf("expected base header to be preserved, got %q", got)
	}
	if got := headers.Get("User-Agent"); got == "" {
		t.Fatal("expected default User-Agent to be preserved")
	}

	cookies := config.GetRequestCookiesWithPayload(nil)
	if len(cookies) != 0 {
		t.Fatal("expected no cookies when payload placement does not use keyed transport")
	}
}

func TestApplyMetaToRequestPreservesAndReplacesQueryValues(t *testing.T) {
	config := &Config{
		SessionPlacement: PlacementQuery,
		SessionKey:       "sid",
		SeqPlacement:     PlacementQuery,
		SeqKey:           "seq",
	}

	request, err := http.NewRequest(http.MethodPost, "https://example.com/test?foo=bar&sid=old", nil)
	if err != nil {
		t.Fatal(err)
	}

	config.ApplyMetaToRequest(request, "session-new", "42")

	query := request.URL.Query()
	if got := query.Get("foo"); got != "bar" {
		t.Fatalf("expected existing query value to be preserved, got %q", got)
	}
	if got := query.Get("sid"); got != "session-new" {
		t.Fatalf("expected sid query value to be replaced, got %q", got)
	}
	if got := query.Get("seq"); got != "42" {
		t.Fatalf("expected seq query value to be appended, got %q", got)
	}
}

func TestApplyMetaToRequestCookiePathKeepsExistingCookiesVisible(t *testing.T) {
	config := &Config{
		SessionPlacement: PlacementCookie,
		SessionKey:       "sid",
		SeqPlacement:     PlacementCookie,
		SeqKey:           "seq",
	}

	request, err := http.NewRequest(http.MethodPost, "https://example.com/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "pref=1")

	config.ApplyMetaToRequest(request, "session-cookie", "43")

	cookieHeader := request.Header.Get("Cookie")
	for _, want := range []string{"pref=1", "sid=session-cookie", "seq=43"} {
		if !strings.Contains(cookieHeader, want) {
			t.Fatalf("expected cookie header %q to contain %q", cookieHeader, want)
		}
	}
	for name, want := range map[string]string{"pref": "1", "sid": "session-cookie", "seq": "43"} {
		cookie, err := request.Cookie(name)
		if err != nil {
			t.Fatalf("expected cookie %q to be readable: %v", name, err)
		}
		if cookie.Value != want {
			t.Fatalf("expected cookie %q=%q, got %q", name, want, cookie.Value)
		}
	}
}
