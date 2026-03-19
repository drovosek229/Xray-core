import Foundation
import XrayAppCore

enum AppConfiguration {
    static var appDisplayName: String {
        Bundle.main.object(forInfoDictionaryKey: "AppDisplayName") as? String
            ?? "internet"
    }

    static var vpnDisplayName: String {
        Bundle.main.object(forInfoDictionaryKey: "VPNDisplayName") as? String
            ?? appDisplayName
    }

    static var userAgentName: String {
        Bundle.main.object(forInfoDictionaryKey: "UserAgentName") as? String
            ?? appDisplayName.replacingOccurrences(of: " ", with: "")
    }

    static var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
            ?? "0.1"
    }

    static var appGroupIdentifier: String {
        Bundle.main.object(forInfoDictionaryKey: "AppGroupIdentifier") as? String
            ?? "group.com.example.internet"
    }

    static var keychainService: String {
        Bundle.main.object(forInfoDictionaryKey: "SharedKeychainService") as? String
            ?? "com.example.internet"
    }

    static var keychainAccessGroup: String? {
        Bundle.main.object(forInfoDictionaryKey: "SharedKeychainAccessGroup") as? String
    }

    static var packetTunnelBundleIdentifier: String {
        Bundle.main.object(forInfoDictionaryKey: "PacketTunnelBundleIdentifier") as? String
            ?? "com.example.internet.PacketTunnel"
    }

    static let legacySelectedProfileKey = "selected_profile_reference"
    static let activeTunnelTargetKey = "active_tunnel_target_reference"
    static let manualProfileIDsKey = "manual_profile_ids"
    static let subscriptionSourceIDsKey = "subscription_source_ids"
    static let subscriptionEndpointIDsKey = "subscription_endpoint_ids"
    static let homeSortModeKey = "home_sort_mode"
    static let collapsedSectionIDsKey = "collapsed_section_ids"
    static let latencyCacheKey = "profile_latency_cache"
    static let tunnelRuntimeStateKey = "tunnel_runtime_state"
    static let latestBenchmarkResultKey = "latest_benchmark_result"
    static let remoteGeoAssetSettingsKey = "remote_geo_asset_settings"
    static let remoteGeoAssetRefreshStateKey = "remote_geo_asset_refresh_state"
    static let simpleRoutingSettingsKey = "simple_routing_settings"
    static let tunnelRuntimeStateFileName = "tunnel_runtime_state.json"
    static let tunnelProviderConfigurationAppGroupKey = "AppGroupIdentifier"
    static let tunnelProviderConfigurationManagerIdentifierKey = "ManagerIdentifier"
    static let tunnelProviderConfigurationVersionKey = "ConfigurationVersion"
    static let tunnelProviderConfigurationEnvelopeKey = "RuntimeEnvelope"
    static let tunnelConfigurationVersion = 2
    static let staleRefreshInterval: TimeInterval = 60 * 60
    static let latencyRefreshInterval: TimeInterval = 15 * 60
    static let latencyProbeTimeout: TimeInterval = 4
    static let latencyProbeMaxConcurrent = 4
    static let latencyProbeURLString = "https://cp.cloudflare.com/generate_204"
    static let latencyProbeLocalSocksPortBase = 21_080
    static let defaultDNSServers = ["198.18.0.1"]
    static let localSocksListenAddress = "127.0.0.1"
    static let localSocksListenPort = 10_808
    static let runtimeDoHServers = [
        "https+local://1.1.1.1/dns-query",
        "https+local://1.0.0.1/dns-query",
    ]
    static let defaultTunnelMTU = 1280
    static let benchmarkProbeURLString = latencyProbeURLString
    static let benchmarkRequestTimeout: TimeInterval = 15
    static let xrayLogFileName = "xray.log"
    static let eventsLogFileName = "client-events.log"
    static let remoteGeoAssetRefreshInterval: TimeInterval = 24 * 60 * 60
    static let remoteGeoAssetRequestTimeout: TimeInterval = 15
    static let remoteGeoAssetResourceTimeout: TimeInterval = 90
    static let geoIPAssetFileName = "geoip.dat"
    static let geoSiteAssetFileName = "geosite.dat"
    static let russiaPresetGeoIPURLString =
        "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geoip.dat"
    static let russiaPresetGeoSiteURLString =
        "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat"
    static let russiaPresetRemoteGeoAssetSettings = RemoteGeoAssetSettings(
        geoIPURLString: russiaPresetGeoIPURLString,
        geoSiteURLString: russiaPresetGeoSiteURLString
    )
    static let russiaPresetSimpleRoutingSettings = SimpleRoutingSettings(
        isEnabled: true,
        rules: [
            SimpleRoutingRule(
                id: UUID(uuidString: "2f9f79e0-feca-4b35-9193-6da9a51d1fb9")!,
                kind: .geoSite(selectors: ["category-ads-all"]),
                target: .block
            ),
            SimpleRoutingRule(
                id: UUID(uuidString: "99dd8b1c-20e4-43bb-bb51-4738062d87bf")!,
                kind: .geoIP(selectors: ["cloudflare", "google"]),
                target: .proxy
            ),
            SimpleRoutingRule(
                id: UUID(uuidString: "6c515c38-70bc-4745-a68d-fe374db1a92f")!,
                kind: .geoIP(selectors: ["ru"]),
                target: .direct
            ),
        ]
    )
}
