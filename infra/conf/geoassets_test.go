package conf

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	approver "github.com/xtls/xray-core/app/router"
	xraynet "github.com/xtls/xray-core/common/net"
	"google.golang.org/protobuf/proto"
)

func TestConfigBuildSupportsRemoteGeoAssets(t *testing.T) {
	setupGeoAssetTestEnv(t)

	geoIPPayload := mustMarshalProto(t, &approver.GeoIPList{
		Entry: []*approver.GeoIP{
			{
				CountryCode: "US",
				Cidr: []*approver.CIDR{
					{
						Ip:     []byte{7, 7, 7, 0},
						Prefix: 24,
					},
				},
			},
		},
	})
	geoSitePayload := mustMarshalProto(t, &approver.GeoSiteList{
		Entry: []*approver.GeoSite{
			{
				CountryCode: "US",
				Domain: []*approver.Domain{
					{
						Type:  approver.Domain_Domain,
						Value: "example.com",
					},
				},
			},
		},
	})
	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geoip.dat":   {status: http.StatusOK, body: geoIPPayload},
		"/geosite.dat": {status: http.StatusOK, body: geoSitePayload},
	})

	config := &Config{
		GeoAssets: &GeoAssetsConfig{
			GeoIP:   &RemoteGeoAssetConfig{URL: server.URL + "/geoip.dat"},
			GeoSite: &RemoteGeoAssetConfig{URL: server.URL + "/geosite.dat"},
		},
		RouterConfig: &RouterConfig{
			RuleList: []json.RawMessage{
				json.RawMessage(`{"ip":["geoip:us"],"outboundTag":"direct"}`),
			},
		},
		DNSConfig: &DNSConfig{
			Servers: []*NameServerConfig{
				{
					Address: &Address{Address: xraynet.ParseAddress("8.8.8.8")},
					Port:    53,
					Domains: []string{"geosite:us"},
				},
			},
		},
	}

	if _, err := config.Build(); err != nil {
		t.Fatalf("Config.Build() failed with remote geo assets: %v", err)
	}
}

func TestConfigOverrideReplacesGeoAssets(t *testing.T) {
	config := &Config{
		GeoAssets: &GeoAssetsConfig{
			GeoIP: &RemoteGeoAssetConfig{URL: "https://example.com/geoip.dat"},
		},
	}

	config.Override(&Config{
		GeoAssets: &GeoAssetsConfig{
			GeoSite: &RemoteGeoAssetConfig{URL: "https://example.com/geosite.dat"},
		},
	}, "override")

	if config.GeoAssets == nil || config.GeoAssets.GeoSite == nil {
		t.Fatal("GeoAssets override did not replace the config")
	}
	if config.GeoAssets.GeoIP != nil {
		t.Fatal("GeoAssets override should replace the previous object as a whole")
	}
}

func TestPrepareRemoteGeoAssetRefreshesStaleCache(t *testing.T) {
	setupGeoAssetTestEnv(t)

	sourceURL := "https://example.test/geoip.dat"
	stalePayload := mustMarshalProto(t, geoIPListForOctet(1))
	freshPayload := mustMarshalProto(t, geoIPListForOctet(9))
	dataPath, metadataPath := writeGeoAssetCache(t, geoAssetKindGeoIP, sourceURL, stalePayload, time.Now().Add(-25*time.Hour))
	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geoip.dat": {status: http.StatusOK, body: freshPayload},
	})
	sourceURL = server.URL + "/geoip.dat"
	dataPath, metadataPath = rewriteGeoAssetCachePaths(t, geoAssetKindGeoIP, sourceURL, stalePayload, dataPath, metadataPath, time.Now().Add(-25*time.Hour))

	resolver, err := prepareGeoAssetResolver(&GeoAssetsConfig{
		GeoIP: &RemoteGeoAssetConfig{URL: sourceURL},
	})
	if err != nil {
		t.Fatalf("prepareGeoAssetResolver() failed: %v", err)
	}

	geoipList, err := ToCidrListWithAssetResolver(StringList{"geoip:us"}, resolver)
	if err != nil {
		t.Fatalf("ToCidrListWithAssetResolver() failed: %v", err)
	}

	if got := geoipList[0].Cidr[0].Ip[0]; got != 9 {
		t.Fatalf("expected refreshed cache to be used, got first octet %d", got)
	}
	gotPayload, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("failed to read refreshed cache: %v", err)
	}
	if !bytes.Equal(gotPayload, freshPayload) {
		t.Fatal("stale cache was not replaced by the refreshed payload")
	}
	metadata, err := readGeoAssetMetadata(metadataPath)
	if err != nil {
		t.Fatalf("failed to read geo asset metadata: %v", err)
	}
	if metadata.LastSuccessfulRefresh.IsZero() {
		t.Fatal("expected metadata to record a successful refresh")
	}
}

func TestPrepareRemoteGeoAssetUsesLastKnownGoodCacheOnStaleRefreshFailure(t *testing.T) {
	setupGeoAssetTestEnv(t)

	sourceURL := "https://example.test/geoip.dat"
	cachedPayload := mustMarshalProto(t, geoIPListForOctet(3))
	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geoip.dat": {status: http.StatusInternalServerError, body: []byte("boom")},
	})
	sourceURL = server.URL + "/geoip.dat"
	dataPath, metadataPath := writeGeoAssetCache(t, geoAssetKindGeoIP, sourceURL, cachedPayload, time.Now().Add(-25*time.Hour))

	resolver, err := prepareGeoAssetResolver(&GeoAssetsConfig{
		GeoIP: &RemoteGeoAssetConfig{URL: sourceURL},
	})
	if err != nil {
		t.Fatalf("prepareGeoAssetResolver() should fall back to the cached asset: %v", err)
	}

	geoipList, err := ToCidrListWithAssetResolver(StringList{"geoip:us"}, resolver)
	if err != nil {
		t.Fatalf("ToCidrListWithAssetResolver() failed: %v", err)
	}
	if got := geoipList[0].Cidr[0].Ip[0]; got != 3 {
		t.Fatalf("expected cached payload to be used, got first octet %d", got)
	}
	gotPayload, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("failed to read cached payload: %v", err)
	}
	if !bytes.Equal(gotPayload, cachedPayload) {
		t.Fatal("cached payload was unexpectedly replaced after a failed refresh")
	}
	metadata, err := readGeoAssetMetadata(metadataPath)
	if err != nil {
		t.Fatalf("failed to read geo asset metadata: %v", err)
	}
	if !strings.Contains(metadata.LastError, "unexpected status") {
		t.Fatalf("expected the refresh error to be recorded, got %q", metadata.LastError)
	}
}

func TestPrepareRemoteGeoAssetFailsWithoutUsableCache(t *testing.T) {
	setupGeoAssetTestEnv(t)

	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geoip.dat": {status: http.StatusInternalServerError, body: []byte("boom")},
	})

	_, err := prepareGeoAssetResolver(&GeoAssetsConfig{
		GeoIP: &RemoteGeoAssetConfig{URL: server.URL + "/geoip.dat"},
	})
	if err == nil {
		t.Fatal("expected prepareGeoAssetResolver() to fail without a usable cache")
	}
}

func TestPrepareRemoteGeoAssetRejectsInvalidRefreshWithoutReplacingCache(t *testing.T) {
	setupGeoAssetTestEnv(t)

	sourceURL := "https://example.test/geoip.dat"
	cachedPayload := mustMarshalProto(t, geoIPListForOctet(4))
	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geoip.dat": {status: http.StatusOK, body: []byte("not-a-protobuf")},
	})
	sourceURL = server.URL + "/geoip.dat"
	dataPath, _ := writeGeoAssetCache(t, geoAssetKindGeoIP, sourceURL, cachedPayload, time.Now().Add(-25*time.Hour))

	resolver, err := prepareGeoAssetResolver(&GeoAssetsConfig{
		GeoIP: &RemoteGeoAssetConfig{URL: sourceURL},
	})
	if err != nil {
		t.Fatalf("prepareGeoAssetResolver() should have fallen back to the cached asset: %v", err)
	}

	geoipList, err := ToCidrListWithAssetResolver(StringList{"geoip:us"}, resolver)
	if err != nil {
		t.Fatalf("ToCidrListWithAssetResolver() failed: %v", err)
	}
	if got := geoipList[0].Cidr[0].Ip[0]; got != 4 {
		t.Fatalf("expected cached payload to remain active, got first octet %d", got)
	}
	gotPayload, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("failed to read cached payload: %v", err)
	}
	if !bytes.Equal(gotPayload, cachedPayload) {
		t.Fatal("cached payload was replaced by an invalid download")
	}
}

func TestBuildMPHCacheUsesRemoteGeosite(t *testing.T) {
	setupGeoAssetTestEnv(t)

	geoSitePayload := mustMarshalProto(t, &approver.GeoSiteList{
		Entry: []*approver.GeoSite{
			{
				CountryCode: "US",
				Domain: []*approver.Domain{
					{
						Type:  approver.Domain_Domain,
						Value: "example.com",
					},
				},
			},
		},
	})
	server := newRemoteGeoAssetServer(t, map[string]remoteGeoAssetResponse{
		"/geosite.dat": {status: http.StatusOK, body: geoSitePayload},
	})
	matcherPath := filepath.Join(t.TempDir(), "matcher.cache")
	config := &Config{
		GeoAssets: &GeoAssetsConfig{
			GeoSite: &RemoteGeoAssetConfig{URL: server.URL + "/geosite.dat"},
		},
		RouterConfig: &RouterConfig{
			RuleList: []json.RawMessage{
				json.RawMessage(`{"ruleTag":"rule-1","domain":["geosite:us"],"outboundTag":"direct"}`),
			},
		},
	}

	if err := config.BuildMPHCache(&matcherPath); err != nil {
		t.Fatalf("BuildMPHCache() failed: %v", err)
	}

	data, err := os.ReadFile(matcherPath)
	if err != nil {
		t.Fatalf("failed to read matcher cache: %v", err)
	}
	matcher, err := approver.LoadGeoSiteMatcher(bytes.NewReader(data), "rule-1")
	if err != nil {
		t.Fatalf("LoadGeoSiteMatcher() failed: %v", err)
	}
	if matcher.Match("example.com") == nil {
		t.Fatal("matcher cache did not include the remote geosite entry")
	}
}

type remoteGeoAssetResponse struct {
	status int
	body   []byte
}

func newRemoteGeoAssetServer(t *testing.T, responses map[string]remoteGeoAssetResponse) *httptest.Server {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(response.status)
		_, _ = w.Write(response.body)
	}))

	originalTransport := http.DefaultTransport
	transport, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		server.Close()
		t.Fatal("unexpected test server transport type")
	}
	http.DefaultTransport = transport.Clone()
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
		server.Close()
	})

	return server
}

func setupGeoAssetTestEnv(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(homeDir, "xdg-cache"))
}

func geoIPListForOctet(octet byte) *approver.GeoIPList {
	return &approver.GeoIPList{
		Entry: []*approver.GeoIP{
			{
				CountryCode: "US",
				Cidr: []*approver.CIDR{
					{
						Ip:     []byte{octet, octet, octet, 0},
						Prefix: 24,
					},
				},
			},
		},
	}
}

func writeGeoAssetCache(
	t *testing.T,
	kind geoAssetKind,
	sourceURL string,
	payload []byte,
	lastRefresh time.Time,
) (string, string) {
	t.Helper()

	cacheDir, err := geoAssetCacheDir()
	if err != nil {
		t.Fatalf("geoAssetCacheDir() failed: %v", err)
	}

	dataPath := filepath.Join(cacheDir, kind.fileNameWithKey(sourceURL))
	metadataPath := filepath.Join(cacheDir, kind.metadataFileNameWithKey(sourceURL))
	if err := os.WriteFile(dataPath, payload, 0o600); err != nil {
		t.Fatalf("failed to write geo asset cache: %v", err)
	}
	if err := writeGeoAssetMetadata(metadataPath, &geoAssetMetadata{
		SourceURL:             sourceURL,
		LocalPath:             dataPath,
		LastSuccessfulRefresh: lastRefresh,
	}); err != nil {
		t.Fatalf("failed to write geo asset metadata: %v", err)
	}

	return dataPath, metadataPath
}

func rewriteGeoAssetCachePaths(
	t *testing.T,
	kind geoAssetKind,
	sourceURL string,
	payload []byte,
	oldDataPath string,
	oldMetadataPath string,
	lastRefresh time.Time,
) (string, string) {
	t.Helper()

	if err := os.Remove(oldDataPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to remove old geo asset cache: %v", err)
	}
	if err := os.Remove(oldMetadataPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to remove old geo asset metadata: %v", err)
	}
	return writeGeoAssetCache(t, kind, sourceURL, payload, lastRefresh)
}

func (k geoAssetKind) fileNameWithKey(sourceURL string) string {
	return string(k) + "-" + geoAssetCacheKey(sourceURL) + ".dat"
}

func (k geoAssetKind) metadataFileNameWithKey(sourceURL string) string {
	return string(k) + "-" + geoAssetCacheKey(sourceURL) + ".json"
}

func mustMarshalProto(t *testing.T, message proto.Message) []byte {
	t.Helper()

	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("failed to marshal test protobuf: %v", err)
	}
	return data
}
