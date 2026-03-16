package conf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/common/platform/filesystem"
	"google.golang.org/protobuf/proto"
)

const remoteGeoAssetRefreshInterval = 24 * time.Hour

type RemoteGeoAssetConfig struct {
	URL string `json:"url"`
}

type GeoAssetsConfig struct {
	GeoIP   *RemoteGeoAssetConfig `json:"geoip"`
	GeoSite *RemoteGeoAssetConfig `json:"geosite"`
}

type geoAssetPathProvider interface {
	AssetPath(file string) string
}

type geoAssetResolver struct {
	paths map[string]string
}

func (r *geoAssetResolver) AssetPath(file string) string {
	if r != nil {
		if path, ok := r.paths[file]; ok && path != "" {
			return path
		}
	}
	return platform.GetAssetLocation(file)
}

type geoAssetKind string

const (
	geoAssetKindGeoIP   geoAssetKind = "geoip"
	geoAssetKindGeoSite geoAssetKind = "geosite"
)

func (k geoAssetKind) fileName() string {
	return string(k) + ".dat"
}

func (k geoAssetKind) displayName() string {
	return string(k) + ".dat"
}

type geoAssetMetadata struct {
	SourceURL             string    `json:"sourceURL"`
	LocalPath             string    `json:"localPath"`
	LastSuccessfulRefresh time.Time `json:"lastSuccessfulRefresh"`
	LastError             string    `json:"lastError,omitempty"`
}

func (c *Config) prepareGeoAssetResolver() (*geoAssetResolver, error) {
	return prepareGeoAssetResolver(c.GeoAssets)
}

func prepareGeoAssetResolver(config *GeoAssetsConfig) (*geoAssetResolver, error) {
	if config == nil || (config.GeoIP == nil && config.GeoSite == nil) {
		return nil, nil
	}

	resolver := &geoAssetResolver{paths: make(map[string]string, 2)}
	if config.GeoIP != nil {
		path, err := prepareRemoteGeoAsset(geoAssetKindGeoIP, config.GeoIP)
		if err != nil {
			return nil, err
		}
		resolver.paths[geoAssetKindGeoIP.fileName()] = path
	}
	if config.GeoSite != nil {
		path, err := prepareRemoteGeoAsset(geoAssetKindGeoSite, config.GeoSite)
		if err != nil {
			return nil, err
		}
		resolver.paths[geoAssetKindGeoSite.fileName()] = path
	}
	return resolver, nil
}

func resolveGeoAssetPath(file string, resolver geoAssetPathProvider) string {
	if resolver != nil && (file == geoAssetKindGeoIP.fileName() || file == geoAssetKindGeoSite.fileName()) {
		return resolver.AssetPath(file)
	}
	return platform.GetAssetLocation(file)
}

func prepareRemoteGeoAsset(kind geoAssetKind, config *RemoteGeoAssetConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("remote %s configuration is missing", kind.displayName())
	}

	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return "", fmt.Errorf("remote %s URL is empty", kind.displayName())
	}

	parsedURL, err := neturl.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid remote %s URL: %w", kind.displayName(), err)
	}
	if parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return "", fmt.Errorf("remote %s URL must use https", kind.displayName())
	}

	cacheDir, err := geoAssetCacheDir()
	if err != nil {
		return "", err
	}

	cacheKey := geoAssetCacheKey(rawURL)
	dataPath := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.dat", kind, cacheKey))
	metadataPath := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.json", kind, cacheKey))

	metadata, _ := readGeoAssetMetadata(metadataPath)
	if metadata == nil {
		metadata = &geoAssetMetadata{}
	}
	metadata.SourceURL = rawURL
	metadata.LocalPath = dataPath

	if geoAssetCacheIsFresh(kind, metadata, dataPath, rawURL) {
		return dataPath, nil
	}

	refreshErr := downloadRemoteGeoAsset(kind, rawURL, dataPath)
	if refreshErr == nil {
		metadata.LastSuccessfulRefresh = time.Now().UTC()
		metadata.LastError = ""
		_ = writeGeoAssetMetadata(metadataPath, metadata)
		return dataPath, nil
	}

	metadata.LastError = refreshErr.Error()
	_ = writeGeoAssetMetadata(metadataPath, metadata)
	if geoAssetCacheIsUsable(kind, metadata, dataPath, rawURL) {
		return dataPath, nil
	}

	return "", refreshErr
}

func geoAssetCacheDir() (string, error) {
	baseDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(baseDir) == "" {
		return ensureGeoAssetCacheDir(filepath.Join(os.TempDir(), "xray-geo-assets"))
	}

	return ensureGeoAssetCacheDir(filepath.Join(baseDir, "xray", "geo-assets"))
}

func ensureGeoAssetCacheDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("failed to create geo asset cache dir: %w", err)
	}
	return path, nil
}

func geoAssetCacheKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func geoAssetCacheIsFresh(kind geoAssetKind, metadata *geoAssetMetadata, dataPath, sourceURL string) bool {
	if metadata == nil || metadata.SourceURL != sourceURL || metadata.LocalPath != dataPath {
		return false
	}
	if metadata.LastSuccessfulRefresh.IsZero() || time.Since(metadata.LastSuccessfulRefresh) >= remoteGeoAssetRefreshInterval {
		return false
	}
	return validateGeoAssetFile(kind, dataPath) == nil
}

func geoAssetCacheIsUsable(kind geoAssetKind, metadata *geoAssetMetadata, dataPath, sourceURL string) bool {
	if metadata == nil || metadata.SourceURL != sourceURL || metadata.LocalPath != dataPath {
		return false
	}
	return validateGeoAssetFile(kind, dataPath) == nil
}

func readGeoAssetMetadata(path string) (*geoAssetMetadata, error) {
	data, err := filesystem.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var metadata geoAssetMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func writeGeoAssetMetadata(path string, metadata *geoAssetMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "geo-asset-meta-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, path)
}

func downloadRemoteGeoAsset(kind geoAssetKind, sourceURL, dataPath string) error {
	request, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for remote %s: %w", kind.displayName(), err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("failed to download remote %s: %w", kind.displayName(), err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download remote %s: unexpected status %d", kind.displayName(), response.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(dataPath), fmt.Sprintf("%s-*.tmp", kind))
	if err != nil {
		return fmt.Errorf("failed to create temp file for remote %s: %w", kind.displayName(), err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, response.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write remote %s: %w", kind.displayName(), err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to finalize remote %s: %w", kind.displayName(), err)
	}

	if err := validateGeoAssetFile(kind, tmpPath); err != nil {
		return err
	}

	if err := os.Remove(dataPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpPath, dataPath); err != nil {
		return fmt.Errorf("failed to replace cached %s: %w", kind.displayName(), err)
	}
	return nil
}

func validateGeoAssetFile(kind geoAssetKind, path string) error {
	data, err := filesystem.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", kind.displayName(), err)
	}
	if len(data) == 0 {
		return fmt.Errorf("remote %s payload is empty", kind.displayName())
	}

	switch kind {
	case geoAssetKindGeoIP:
		var list router.GeoIPList
		if err := proto.Unmarshal(data, &list); err != nil {
			return fmt.Errorf("remote %s payload is invalid: %w", kind.displayName(), err)
		}
	case geoAssetKindGeoSite:
		var list router.GeoSiteList
		if err := proto.Unmarshal(data, &list); err != nil {
			return fmt.Errorf("remote %s payload is invalid: %w", kind.displayName(), err)
		}
	default:
		return fmt.Errorf("unsupported remote geo asset kind: %s", kind)
	}

	return nil
}
