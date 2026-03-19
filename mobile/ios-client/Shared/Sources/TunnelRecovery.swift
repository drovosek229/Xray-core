import Foundation

#if canImport(Network)
import Network
#endif

#if canImport(Darwin)
import Darwin
#endif

enum TunnelRuntimeStage: String, Codable, Hashable, Sendable {
    case startup
    case steadyState
    case recovery
}

enum TunnelLocalFailureReason: String, Codable, Hashable, Sendable {
    case xrayStartFailed
    case xrayRuntimeStopped
    case socksNotReady
    case tun2SocksExited
    case healthCheckFailed
    case recoveryBudgetExceeded

    var displayName: String {
        switch self {
        case .xrayStartFailed:
            return "Xray start failed"
        case .xrayRuntimeStopped:
            return "Xray runtime stopped"
        case .socksNotReady:
            return "Local SOCKS listener not ready"
        case .tun2SocksExited:
            return "tun2socks exited"
        case .healthCheckFailed:
            return "Local runtime health check failed"
        case .recoveryBudgetExceeded:
            return "Recovery budget exhausted"
        }
    }
}

struct TunnelRecoveryPolicy: Hashable, Sendable {
    var socksReadinessTimeout: TimeInterval
    var socksReadinessRetryInterval: TimeInterval
    var healthCheckInterval: TimeInterval
    var recoveryBackoffs: [TimeInterval]

    static let `default` = TunnelRecoveryPolicy(
        socksReadinessTimeout: 3,
        socksReadinessRetryInterval: 0.1,
        healthCheckInterval: 15,
        recoveryBackoffs: [0.25, 1, 3]
    )

    var maxRecoveryAttempts: Int {
        recoveryBackoffs.count
    }

    func backoff(forAttempt attempt: Int) -> TimeInterval? {
        guard attempt > 0, attempt <= recoveryBackoffs.count else {
            return nil
        }
        return recoveryBackoffs[attempt - 1]
    }
}

enum TunnelDirectEgressMode: String, Codable, Hashable, Sendable {
    case bound
    case systemDefault
    case blocked
}

enum DirectEgressAddressFamily: String, Codable, Hashable, Sendable {
    case ipv4
    case ipv6
}

struct TunnelDirectEgressStatus: Codable, Hashable, Sendable {
    var mode: TunnelDirectEgressMode
    var interfaceName: String?
    var sourceAddress: String?
    var sourceAddressFamily: DirectEgressAddressFamily?
    var reason: String?

    static func bound(
        interfaceName: String,
        sourceAddress: String,
        sourceAddressFamily: DirectEgressAddressFamily
    ) -> TunnelDirectEgressStatus {
        TunnelDirectEgressStatus(
            mode: .bound,
            interfaceName: interfaceName,
            sourceAddress: sourceAddress,
            sourceAddressFamily: sourceAddressFamily,
            reason: nil
        )
    }

    static func systemDefault(reason: String) -> TunnelDirectEgressStatus {
        TunnelDirectEgressStatus(
            mode: .systemDefault,
            interfaceName: nil,
            sourceAddress: nil,
            sourceAddressFamily: nil,
            reason: reason
        )
    }

    static func blocked(reason: String) -> TunnelDirectEgressStatus {
        TunnelDirectEgressStatus(
            mode: .blocked,
            interfaceName: nil,
            sourceAddress: nil,
            sourceAddressFamily: nil,
            reason: reason
        )
    }

    var summaryText: String {
        switch mode {
        case .bound:
            if let interfaceName, !interfaceName.isEmpty {
                return "Bound to \(interfaceName)"
            }
            return "Bound"
        case .systemDefault:
            return "Using system direct path"
        case .blocked:
            return "Direct traffic blocked"
        }
    }

    var detailText: String? {
        switch mode {
        case .bound:
            if let sourceAddress, !sourceAddress.isEmpty {
                return sourceAddress
            }
            return interfaceName
        case .systemDefault:
            return reason
        case .blocked:
            return reason
        }
    }

    var logDescription: String {
        switch mode {
        case .bound:
            return "bound(\(interfaceName ?? "unknown"),\(sourceAddress ?? "unknown"))"
        case .systemDefault:
            return "systemDefault(\(reason ?? "unknown"))"
        case .blocked:
            return "blocked(\(reason ?? "unknown"))"
        }
    }
}

enum DirectEgressPathStatus: String, Hashable, Sendable {
    case satisfied
    case requiresConnection
    case unsatisfied
    case unknown
}

enum DirectEgressInterfaceType: String, Hashable, Sendable {
    case wifi
    case cellular
    case wiredEthernet
    case other
    case loopback
    case unknown

    static let preferredActiveTypes: [DirectEgressInterfaceType] = [
        .wifi,
        .cellular,
        .wiredEthernet,
        .other,
    ]
}

struct DirectEgressInterface: Hashable, Sendable {
    var name: String
    var type: DirectEgressInterfaceType
    var addresses: [DirectEgressInterfaceAddress] = []

    var isUsablePhysicalInterface: Bool {
        let normalizedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return !normalizedName.isEmpty && !normalizedName.hasPrefix("utun") && type != .loopback
    }

    var preferredAddress: DirectEgressInterfaceAddress? {
        if let ipv4 = addresses.first(where: { $0.family == .ipv4 && $0.isRoutable }) {
            return ipv4
        }
        if let ipv6 = addresses.first(where: { $0.family == .ipv6 && $0.isRoutable }) {
            return ipv6
        }
        if let firstRoutable = addresses.first(where: \.isRoutable) {
            return firstRoutable
        }
        return addresses.first
    }
}

struct DirectEgressInterfaceAddress: Codable, Hashable, Sendable {
    var host: String
    var family: DirectEgressAddressFamily

    var isRoutable: Bool {
        switch family {
        case .ipv4:
            return host != "0.0.0.0" && host != "127.0.0.1" && !host.hasPrefix("169.254.")
        case .ipv6:
            let normalized = host.lowercased()
            return normalized != "::1" && !normalized.hasPrefix("fe80:")
        }
    }
}

struct DirectEgressPathSnapshot: Hashable, Sendable {
    var status: DirectEgressPathStatus
    var availableInterfaces: [DirectEgressInterface]
    var activeInterfaceTypes: [DirectEgressInterfaceType]

    var logDescription: String {
        let interfaceDescription = availableInterfaces
            .map { "\($0.name):\($0.type.rawValue)" }
            .joined(separator: ",")
        return "\(status.rawValue) [\(interfaceDescription)]"
    }
}

enum DirectEgressPlanner {
    static func plan(for snapshot: DirectEgressPathSnapshot?) -> TunnelDirectEgressStatus {
        guard let snapshot, snapshot.status == .satisfied else {
            return .blocked(reason: "No satisfied network path")
        }

        for interfaceType in DirectEgressInterfaceType.preferredActiveTypes
        where snapshot.activeInterfaceTypes.contains(interfaceType) {
            if let interface = snapshot.availableInterfaces.first(where: {
                $0.type == interfaceType && $0.isUsablePhysicalInterface
            }), let address = interface.preferredAddress {
                return .bound(
                    interfaceName: interface.name,
                    sourceAddress: address.host,
                    sourceAddressFamily: address.family
                )
            }
        }

        if let fallbackInterface = snapshot.availableInterfaces.first(where: \.isUsablePhysicalInterface),
           let address = fallbackInterface.preferredAddress {
            return .bound(
                interfaceName: fallbackInterface.name,
                sourceAddress: address.host,
                sourceAddressFamily: address.family
            )
        }

        if snapshot.availableInterfaces.contains(where: \.isUsablePhysicalInterface) {
            return .systemDefault(reason: "No usable interface address; using the system direct path")
        }

        return .blocked(reason: "No usable physical interface")
    }
}

struct DirectEgressPatchedRuntimeConfig: Hashable, Sendable {
    var configJSON: String
    var hasDirectRules: Bool
    var directEgressStatus: TunnelDirectEgressStatus?
}

enum DirectEgressRuntimeConfigPatcher {
    static func patch(
        runtimeConfigJSON: String,
        directEgressStatus: TunnelDirectEgressStatus
    ) throws -> DirectEgressPatchedRuntimeConfig {
        let data = Data(runtimeConfigJSON.utf8)
        guard var rootObject = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw NSError(
                domain: "internet",
                code: 500,
                userInfo: [NSLocalizedDescriptionKey: "Tunnel runtime config is not a JSON object."]
            )
        }

        let hasDirectRules = hasRulesTargetingDirect(in: rootObject)
        guard hasDirectRules else {
            return DirectEgressPatchedRuntimeConfig(
                configJSON: runtimeConfigJSON,
                hasDirectRules: false,
                directEgressStatus: nil
            )
        }

        guard var outbounds = rootObject["outbounds"] as? [[String: Any]],
              let directOutboundIndex = outbounds.firstIndex(where: { ($0["tag"] as? String) == "direct" })
        else {
            throw NSError(
                domain: "internet",
                code: 500,
                userInfo: [NSLocalizedDescriptionKey: "Tunnel runtime config is missing the direct outbound."]
            )
        }

        switch directEgressStatus.mode {
        case .bound:
            var directOutbound = outbounds[directOutboundIndex]
            directOutbound["tag"] = "direct"
            directOutbound["protocol"] = "freedom"
            directOutbound["sendThrough"] = directEgressStatus.sourceAddress

            var settings = directOutbound["settings"] as? [String: Any] ?? [:]
            switch directEgressStatus.sourceAddressFamily {
            case .ipv4:
                settings["domainStrategy"] = "UseIPv4"
            case .ipv6:
                settings["domainStrategy"] = "UseIPv6"
            case nil:
                break
            }
            if !settings.isEmpty {
                directOutbound["settings"] = settings
            }

            var streamSettings = directOutbound["streamSettings"] as? [String: Any] ?? [:]
            var sockopt = streamSettings["sockopt"] as? [String: Any] ?? [:]
            sockopt["interface"] = directEgressStatus.interfaceName
            streamSettings["sockopt"] = sockopt
            directOutbound["streamSettings"] = streamSettings
            outbounds[directOutboundIndex] = directOutbound
        case .systemDefault:
            return DirectEgressPatchedRuntimeConfig(
                configJSON: runtimeConfigJSON,
                hasDirectRules: true,
                directEgressStatus: directEgressStatus
            )
        case .blocked:
            outbounds[directOutboundIndex] = [
                "tag": "direct",
                "protocol": "blackhole",
            ]
        }

        rootObject["outbounds"] = outbounds

        let patchedData = try JSONSerialization.data(
            withJSONObject: rootObject,
            options: [.prettyPrinted, .sortedKeys]
        )
        guard let patchedJSON = String(data: patchedData, encoding: .utf8) else {
            throw NSError(
                domain: "internet",
                code: 500,
                userInfo: [NSLocalizedDescriptionKey: "Failed to encode the patched tunnel runtime config."]
            )
        }

        return DirectEgressPatchedRuntimeConfig(
            configJSON: patchedJSON,
            hasDirectRules: true,
            directEgressStatus: directEgressStatus
        )
    }

    static func hasDirectRules(in runtimeConfigJSON: String) -> Bool {
        guard let data = runtimeConfigJSON.data(using: .utf8),
              let jsonObject = try? JSONSerialization.jsonObject(with: data),
              let rootObject = jsonObject as? [String: Any]
        else {
            return false
        }

        return hasRulesTargetingDirect(in: rootObject)
    }

    private static func hasRulesTargetingDirect(in rootObject: [String: Any]) -> Bool {
        guard let routing = rootObject["routing"] as? [String: Any],
              let rules = routing["rules"] as? [[String: Any]]
        else {
            return false
        }

        return rules.contains { ($0["outboundTag"] as? String) == "direct" }
    }
}

#if canImport(Network)
extension DirectEgressPathSnapshot {
    init(path: Network.NWPath) {
        self.init(
            status: DirectEgressPathStatus(networkPathStatus: path.status),
            availableInterfaces: path.availableInterfaces.map {
                DirectEgressInterface(
                    name: $0.name,
                    type: DirectEgressInterfaceType(networkInterfaceType: $0.type),
                    addresses: DirectEgressInterfaceAddressResolver.resolveAddresses(for: $0.name)
                )
            },
            activeInterfaceTypes: DirectEgressInterfaceType.preferredActiveTypes.filter { interfaceType in
                guard let nwInterfaceType = interfaceType.networkInterfaceType else {
                    return false
                }
                return path.usesInterfaceType(nwInterfaceType)
            }
        )
    }
}

private extension DirectEgressPathStatus {
    init(networkPathStatus status: Network.NWPath.Status) {
        switch status {
        case .satisfied:
            self = .satisfied
        case .requiresConnection:
            self = .requiresConnection
        case .unsatisfied:
            self = .unsatisfied
        @unknown default:
            self = .unknown
        }
    }
}

private extension DirectEgressInterfaceType {
    init(networkInterfaceType type: Network.NWInterface.InterfaceType) {
        switch type {
        case .wifi:
            self = .wifi
        case .cellular:
            self = .cellular
        case .wiredEthernet:
            self = .wiredEthernet
        case .loopback:
            self = .loopback
        case .other:
            self = .other
        @unknown default:
            self = .unknown
        }
    }

    var networkInterfaceType: Network.NWInterface.InterfaceType? {
        switch self {
        case .wifi:
            return .wifi
        case .cellular:
            return .cellular
        case .wiredEthernet:
            return .wiredEthernet
        case .other:
            return .other
        case .loopback, .unknown:
            return nil
        }
    }
}
#endif

enum DirectEgressInterfaceAddressResolver {
    static func resolveAddresses(for interfaceName: String) -> [DirectEgressInterfaceAddress] {
        #if canImport(Darwin)
        var result: [DirectEgressInterfaceAddress] = []
        var interfaceAddresses: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&interfaceAddresses) == 0, let firstAddress = interfaceAddresses else {
            return []
        }
        defer { freeifaddrs(interfaceAddresses) }

        var current = firstAddress
        while true {
            let interface = current.pointee
            let name = String(cString: interface.ifa_name)
            if name == interfaceName,
               let addressPointer = interface.ifa_addr,
               let address = makeAddress(from: addressPointer) {
                result.append(address)
            }

            guard let next = interface.ifa_next else {
                break
            }
            current = next
        }

        return orderedUnique(result)
        #else
        return []
        #endif
    }

    private static func orderedUnique(_ addresses: [DirectEgressInterfaceAddress]) -> [DirectEgressInterfaceAddress] {
        var seen = Set<DirectEgressInterfaceAddress>()
        var unique: [DirectEgressInterfaceAddress] = []
        unique.reserveCapacity(addresses.count)

        for address in addresses where seen.insert(address).inserted {
            unique.append(address)
        }

        return unique
    }

    #if canImport(Darwin)
    private static func makeAddress(from socketAddress: UnsafeMutablePointer<sockaddr>) -> DirectEgressInterfaceAddress? {
        switch Int32(socketAddress.pointee.sa_family) {
        case AF_INET:
            return hostString(from: socketAddress, family: .ipv4)
        case AF_INET6:
            return hostString(from: socketAddress, family: .ipv6)
        default:
            return nil
        }
    }

    private static func hostString(
        from socketAddress: UnsafeMutablePointer<sockaddr>,
        family: DirectEgressAddressFamily
    ) -> DirectEgressInterfaceAddress? {
        var hostBuffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
        let length: socklen_t
        switch family {
        case .ipv4:
            length = socklen_t(MemoryLayout<sockaddr_in>.size)
        case .ipv6:
            length = socklen_t(MemoryLayout<sockaddr_in6>.size)
        }

        let status = getnameinfo(
            socketAddress,
            length,
            &hostBuffer,
            socklen_t(hostBuffer.count),
            nil,
            0,
            NI_NUMERICHOST
        )
        guard status == 0 else {
            return nil
        }

        let host = String(cString: hostBuffer)
        guard !host.isEmpty else {
            return nil
        }
        return DirectEgressInterfaceAddress(host: host, family: family)
    }
    #endif
}
