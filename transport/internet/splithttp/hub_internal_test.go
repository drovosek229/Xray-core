package splithttp

import "testing"

func TestParseRequestRemoteAddrIPv4(t *testing.T) {
	addr, ok := parseRequestRemoteAddr("127.0.0.1:12345")
	if !ok {
		t.Fatal("expected IPv4 remote address to parse")
	}
	if got := addr.String(); got != "127.0.0.1:12345" {
		t.Fatalf("unexpected parsed IPv4 remote address: %q", got)
	}
}

func TestParseRequestRemoteAddrIPv6(t *testing.T) {
	addr, ok := parseRequestRemoteAddr("[2001:db8::1]:443")
	if !ok {
		t.Fatal("expected IPv6 remote address to parse")
	}
	if got := addr.String(); got != "[2001:db8::1]:443" {
		t.Fatalf("unexpected parsed IPv6 remote address: %q", got)
	}
}

func TestParseRequestRemoteAddrRejectsHostnames(t *testing.T) {
	if _, ok := parseRequestRemoteAddr("example.com:443"); ok {
		t.Fatal("expected hostname remote address to fall back to slower parsing path")
	}
}
