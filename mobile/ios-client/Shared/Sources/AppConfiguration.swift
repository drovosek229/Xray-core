import Foundation

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
    static let pendingTunnelLaunchPayloadKey = "pending_tunnel_launch_payload"
    static let tunnelRuntimeStateKey = "tunnel_runtime_state"
    static let latestBenchmarkResultKey = "latest_benchmark_result"
    static let pendingTunnelLaunchPayloadFileName = "pending_tunnel_launch_payload.json"
    static let tunnelRuntimeStateFileName = "tunnel_runtime_state.json"
    static let tunnelProviderConfigurationAppGroupKey = "AppGroupIdentifier"
    static let tunnelProviderConfigurationVersionKey = "ConfigurationVersion"
    static let tunnelProviderConfigurationEnvelopeKey = "RuntimeEnvelope"
    static let tunnelConfigurationVersion = 2
    static let staleRefreshInterval: TimeInterval = 60 * 60
    static let latencyRefreshInterval: TimeInterval = 15 * 60
    static let latencyProbeTimeout: TimeInterval = 4
    static let latencyProbeMaxConcurrent = 4
    static let tunnelLaunchPayloadMaxAge: TimeInterval = 60
    static let defaultDNSServers = ["198.18.0.1"]
    static let localSocksListenAddress = "127.0.0.1"
    static let localSocksListenPort = 10_808
    static let runtimeDoHServers = [
        "https+local://1.1.1.1/dns-query",
        "https+local://1.0.0.1/dns-query",
    ]
    static let defaultTunnelMTU = 1280
    static let benchmarkProbeURLString = "https://www.cloudflare.com/cdn-cgi/trace"
    static let benchmarkRequestTimeout: TimeInterval = 15
    static let xrayLogFileName = "xray.log"
    static let eventsLogFileName = "client-events.log"
}
