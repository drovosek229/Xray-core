import Foundation

public struct RuntimeConfigContext: Sendable {
    public var tunnelName: String
    public var tunnelMTU: Int
    public var dnsServers: [String]
    public var localSocksListenAddress: String
    public var localSocksListenPort: Int
    public var logFilePath: String?

    public init(
        tunnelName: String = "utun",
        tunnelMTU: Int = 1280,
        dnsServers: [String] = [
            "https+local://1.1.1.1/dns-query",
            "https+local://1.0.0.1/dns-query",
        ],
        localSocksListenAddress: String = "127.0.0.1",
        localSocksListenPort: Int = 10_808,
        logFilePath: String? = nil
    ) {
        self.tunnelName = tunnelName
        self.tunnelMTU = tunnelMTU
        self.dnsServers = dnsServers
        self.localSocksListenAddress = localSocksListenAddress
        self.localSocksListenPort = localSocksListenPort
        self.logFilePath = logFilePath
    }
}

public enum RuntimeConfigBuilder {
    public static func build(
        for profile: ManualProfile,
        context: RuntimeConfigContext = RuntimeConfigContext()
    ) throws -> String {
        try buildJSON(
            name: profile.name,
            address: profile.address,
            port: profile.port,
            uuid: profile.uuid,
            flow: profile.flow,
            encryption: profile.normalizedEncryption,
            securityKind: profile.securityKind,
            realitySettings: profile.realitySettings,
            tlsSettings: profile.tlsSettings,
            xhttpHost: profile.xhttpHost,
            xhttpPath: profile.xhttpPath,
            xhttpMode: profile.xhttpMode,
            behaviorProfile: profile.behaviorProfile,
            uplinkHTTPMethod: profile.normalizedUplinkHTTPMethod,
            xhttpAdvancedSettings: profile.xhttpAdvancedSettings,
            context: context
        )
    }

    public static func build(
        for endpoint: SubscriptionEndpoint,
        context: RuntimeConfigContext = RuntimeConfigContext()
    ) throws -> String {
        try buildJSON(
            name: endpoint.displayName,
            address: endpoint.address,
            port: endpoint.port,
            uuid: endpoint.uuid,
            flow: endpoint.flow,
            encryption: endpoint.normalizedEncryption,
            securityKind: endpoint.securityKind,
            realitySettings: endpoint.realitySettings,
            tlsSettings: endpoint.tlsSettings,
            xhttpHost: endpoint.xhttpHost,
            xhttpPath: endpoint.xhttpPath,
            xhttpMode: endpoint.xhttpMode,
            behaviorProfile: endpoint.behaviorProfile,
            uplinkHTTPMethod: endpoint.normalizedUplinkHTTPMethod,
            xhttpAdvancedSettings: endpoint.xhttpAdvancedSettings,
            context: context
        )
    }

    private static func buildJSON(
        name: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String?,
        encryption: String,
        securityKind: ProfileSecurityKind,
        realitySettings: RealitySecuritySettings?,
        tlsSettings: TLSSecuritySettings?,
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode,
        behaviorProfile: BehaviorProfile,
        uplinkHTTPMethod: String,
        xhttpAdvancedSettings: XHTTPAdvancedSettings?,
        context: RuntimeConfigContext
    ) throws -> String {
        let normalizedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedAddress = address.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedUUID = uuid.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedMethod = normalizeHTTPMethod(uplinkHTTPMethod)
        let normalizedFlow = try normalizeFlow(flow)
        let normalizedEncryption = try normalizeEncryption(encryption)

        guard !normalizedName.isEmpty else {
            throw XrayAppCoreError.invalidProfile("Profile name is required.")
        }
        guard !normalizedAddress.isEmpty else {
            throw XrayAppCoreError.invalidProfile("Server address is required.")
        }
        guard (1...65535).contains(port) else {
            throw XrayAppCoreError.invalidProfile("Port must be between 1 and 65535.")
        }
        guard !normalizedUUID.isEmpty else {
            throw XrayAppCoreError.invalidProfile("UUID is required.")
        }
        if normalizedMethod == ManualUplinkHTTPMethod.get.rawValue && xhttpMode != .packetUp {
            throw XrayAppCoreError.invalidProfile("GET uplinkHTTPMethod requires Packet Upload mode.")
        }

        let normalizedSecurity = try normalizeSecurity(
            kind: securityKind,
            realitySettings: realitySettings,
            tlsSettings: tlsSettings
        )
        if normalizedSecurity.kind == .tls, normalizedFlow != nil {
            throw XrayAppCoreError.invalidProfile("TLS profiles cannot use a VLESS flow.")
        }
        let normalizedXHTTPHost = xhttpHost.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? normalizedSecurity.serverName
            : xhttpHost.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedXHTTPPath = xhttpPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? "/"
            : xhttpPath.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedAdvancedXHTTPSettings = xhttpAdvancedSettings?.normalized
        let normalizedXmuxSettings = effectiveXmuxSettings(from: normalizedAdvancedXHTTPSettings)

        let root = RuntimeConfig(
            log: context.logFilePath.map { RuntimeLog(logLevel: "warning", error: $0) },
            inbounds: [
                RuntimeInbound(
                    tag: "socks-in",
                    listen: context.localSocksListenAddress,
                    port: context.localSocksListenPort,
                    protocolName: "socks",
                    settings: RuntimeSocksInboundSettings(
                        auth: "noauth",
                        udp: true
                    ),
                    sniffing: RuntimeSniffing(
                        enabled: true,
                        destOverride: ["http", "tls", "quic"]
                    )
                )
            ],
            outbounds: [
                RuntimeOutbound(
                    tag: "proxy",
                    protocolName: "vless",
                    settings: RuntimeOutboundSettings(
                        vnext: [
                            RuntimeVNext(
                                address: normalizedAddress,
                                port: port,
                                users: [
                                    RuntimeUser(
                                        id: normalizedUUID,
                                        encryption: normalizedEncryption,
                                        flow: normalizedFlow
                                    )
                                ]
                            )
                        ]
                    ),
                    streamSettings: RuntimeStreamSettings(
                        network: "xhttp",
                        security: normalizedSecurity.kind.rawValue,
                        realitySettings: normalizedSecurity.realitySettings.map {
                            RuntimeRealitySettings(
                                show: false,
                                serverName: $0.serverName,
                                fingerprint: $0.fingerprint,
                                publicKey: $0.publicKey,
                                shortID: $0.shortId,
                                spiderX: $0.spiderX
                            )
                        },
                        tlsSettings: normalizedSecurity.tlsSettings.map {
                            RuntimeTLSSettings(
                                serverName: $0.serverName,
                                alpn: $0.alpn.isEmpty ? nil : $0.alpn,
                                fingerprint: emptyToNil($0.fingerprint),
                                pinnedPeerCertSha256: emptyToNil($0.pinnedPeerCertSha256),
                                verifyPeerCertByName: emptyToNil($0.verifyPeerCertByName),
                                allowInsecure: $0.allowInsecure
                            )
                        },
                        xhttpSettings: RuntimeXHTTPSettings(
                            host: normalizedXHTTPHost,
                            path: normalizedXHTTPPath,
                            mode: xhttpMode.rawValue,
                            behaviorProfile: behaviorProfile.rawValue,
                            uplinkHTTPMethod: normalizedMethod,
                            sessionPlacement: normalizedAdvancedXHTTPSettings?.sessionPlacement,
                            sessionKey: normalizedAdvancedXHTTPSettings?.sessionKey,
                            seqPlacement: normalizedAdvancedXHTTPSettings?.seqPlacement,
                            seqKey: normalizedAdvancedXHTTPSettings?.seqKey,
                            xPaddingBytes: normalizedAdvancedXHTTPSettings?.xPaddingBytes,
                            xPaddingMethod: normalizedAdvancedXHTTPSettings?.xPaddingMethod,
                            xPaddingPlacement: normalizedAdvancedXHTTPSettings?.xPaddingPlacement,
                            xPaddingKey: normalizedAdvancedXHTTPSettings?.xPaddingKey,
                            xPaddingObfsMode: normalizedAdvancedXHTTPSettings?.xPaddingObfsMode,
                            noGRPCHeader: normalizedAdvancedXHTTPSettings?.noGRPCHeader,
                            noSSEHeader: normalizedAdvancedXHTTPSettings?.noSSEHeader,
                            scMaxEachPostBytes: normalizedAdvancedXHTTPSettings?.scMaxEachPostBytes,
                            xmux: normalizedXmuxSettings.map(RuntimeXmuxSettings.init)
                        )
                    )
                ),
                RuntimeOutbound(tag: "dns-out", protocolName: "dns"),
                RuntimeOutbound(tag: "direct", protocolName: "freedom"),
                RuntimeOutbound(tag: "block", protocolName: "blackhole"),
            ],
            routing: RuntimeRouting(
                domainStrategy: "AsIs",
                rules: [
                    RuntimeRule(
                        type: "field",
                        inboundTag: ["socks-in"],
                        network: "udp",
                        port: "53",
                        outboundTag: "dns-out"
                    ),
                    RuntimeRule(
                        type: "field",
                        inboundTag: ["socks-in"],
                        outboundTag: "proxy"
                    )
                ]
            ),
            dns: RuntimeDNS(
                servers: context.dnsServers,
                queryStrategy: "UseIP",
                disableCache: false,
                enableParallelQuery: true
            )
        )

        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        let data = try encoder.encode(root)
        guard let json = String(data: data, encoding: .utf8) else {
            throw XrayAppCoreError.invalidProfile("Failed to encode runtime config.")
        }
        return json
    }

    private static func normalizeHTTPMethod(_ value: String) -> String {
        let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        return normalized.isEmpty ? ManualUplinkHTTPMethod.post.rawValue : normalized
    }

    private static func effectiveXmuxSettings(from advancedSettings: XHTTPAdvancedSettings?) -> XHTTPXmuxSettings? {
        if let explicitXmux = advancedSettings?.xmux?.normalized {
            return explicitXmux
        }

        return XHTTPXmuxSettings(
            hKeepAlivePeriod: 30,
            warmConnections: 1
        )
    }

    private static func normalizeFlow(_ value: String?) throws -> String? {
        let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !normalized.isEmpty else {
            return nil
        }

        switch normalized {
        case VLESSFlow.xtlsRprxVision.rawValue, VLESSFlow.xtlsRprxVisionUDP443.rawValue:
            return normalized
        default:
            throw XrayAppCoreError.invalidProfile("Unsupported VLESS flow: \(normalized)")
        }
    }

    private static func normalizeEncryption(_ value: String) throws -> String {
        let normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if normalized.isEmpty || normalized.lowercased() == "none" {
            return "none"
        }

        let parts = normalized.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count >= 4, parts[0] == "mlkem768x25519plus" else {
            throw XrayAppCoreError.invalidProfile("Unsupported VLESS encryption: \(normalized)")
        }
        guard parts[1] == "native" || parts[1] == "xorpub" || parts[1] == "random" else {
            throw XrayAppCoreError.invalidProfile("Unsupported VLESS encryption mode: \(parts[1])")
        }
        guard parts[2] == "1rtt" || parts[2] == "0rtt" else {
            throw XrayAppCoreError.invalidProfile("Unsupported VLESS encryption RTT mode: \(parts[2])")
        }
        return normalized
    }

    private static func normalizeSecurity(
        kind: ProfileSecurityKind,
        realitySettings: RealitySecuritySettings?,
        tlsSettings: TLSSecuritySettings?
    ) throws -> NormalizedSecurity {
        switch kind {
        case .reality:
            guard let realitySettings else {
                throw XrayAppCoreError.invalidProfile("REALITY settings are required.")
            }
            let serverName = realitySettings.serverName.trimmingCharacters(in: .whitespacesAndNewlines)
            let fingerprint = realitySettings.fingerprint.trimmingCharacters(in: .whitespacesAndNewlines)
            let publicKey = realitySettings.publicKey.trimmingCharacters(in: .whitespacesAndNewlines)

            guard !serverName.isEmpty else {
                throw XrayAppCoreError.invalidProfile("REALITY serverName is required.")
            }
            guard !fingerprint.isEmpty else {
                throw XrayAppCoreError.invalidProfile("REALITY fingerprint is required.")
            }
            guard !publicKey.isEmpty else {
                throw XrayAppCoreError.invalidProfile("REALITY publicKey is required.")
            }

            return NormalizedSecurity(
                kind: .reality,
                serverName: serverName,
                realitySettings: RealitySecuritySettings(
                    serverName: serverName,
                    fingerprint: fingerprint,
                    publicKey: publicKey,
                    shortId: emptyToNil(realitySettings.shortId),
                    spiderX: emptyToNil(realitySettings.spiderX)
                ),
                tlsSettings: nil
            )
        case .tls:
            guard let tlsSettings else {
                throw XrayAppCoreError.invalidProfile("TLS settings are required.")
            }
            let serverName = tlsSettings.serverName.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !serverName.isEmpty else {
                throw XrayAppCoreError.invalidProfile("TLS serverName is required.")
            }

            return NormalizedSecurity(
                kind: .tls,
                serverName: serverName,
                realitySettings: nil,
                tlsSettings: TLSSecuritySettings(
                    serverName: serverName,
                    fingerprint: emptyToNil(tlsSettings.fingerprint),
                    alpn: tlsSettings.alpn.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty },
                    pinnedPeerCertSha256: emptyToNil(tlsSettings.pinnedPeerCertSha256),
                    verifyPeerCertByName: emptyToNil(tlsSettings.verifyPeerCertByName),
                    allowInsecure: tlsSettings.allowInsecure
                )
            )
        }
    }
}

private struct NormalizedSecurity {
    let kind: ProfileSecurityKind
    let serverName: String
    let realitySettings: RealitySecuritySettings?
    let tlsSettings: TLSSecuritySettings?
}

private struct RuntimeConfig: Encodable {
    let log: RuntimeLog?
    let inbounds: [RuntimeInbound]
    let outbounds: [RuntimeOutbound]
    let routing: RuntimeRouting
    let dns: RuntimeDNS
}

private struct RuntimeLog: Encodable {
    let logLevel: String
    let error: String

    private enum CodingKeys: String, CodingKey {
        case logLevel = "loglevel"
        case error
    }
}

private struct RuntimeInbound: Encodable {
    let tag: String
    let listen: String
    let port: Int
    let protocolName: String
    let settings: RuntimeSocksInboundSettings
    let sniffing: RuntimeSniffing?

    private enum CodingKeys: String, CodingKey {
        case tag
        case listen
        case port
        case protocolName = "protocol"
        case settings
        case sniffing
    }
}

private struct RuntimeSocksInboundSettings: Encodable {
    let auth: String
    let udp: Bool
}

private struct RuntimeSniffing: Encodable {
    let enabled: Bool
    let destOverride: [String]
}

private struct RuntimeOutbound: Encodable {
    let tag: String
    let protocolName: String
    var settings: RuntimeOutboundSettings?
    var streamSettings: RuntimeStreamSettings?

    private enum CodingKeys: String, CodingKey {
        case tag
        case protocolName = "protocol"
        case settings
        case streamSettings
    }

    init(
        tag: String,
        protocolName: String,
        settings: RuntimeOutboundSettings? = nil,
        streamSettings: RuntimeStreamSettings? = nil
    ) {
        self.tag = tag
        self.protocolName = protocolName
        self.settings = settings
        self.streamSettings = streamSettings
    }
}

private struct RuntimeOutboundSettings: Encodable {
    let vnext: [RuntimeVNext]
}

private struct RuntimeVNext: Encodable {
    let address: String
    let port: Int
    let users: [RuntimeUser]
}

private struct RuntimeUser: Encodable {
    let id: String
    let encryption: String
    let flow: String?
}

private struct RuntimeStreamSettings: Encodable {
    let network: String
    let security: String
    let realitySettings: RuntimeRealitySettings?
    let tlsSettings: RuntimeTLSSettings?
    let xhttpSettings: RuntimeXHTTPSettings
}

private struct RuntimeRealitySettings: Encodable {
    let show: Bool
    let serverName: String
    let fingerprint: String
    let publicKey: String
    let shortID: String?
    let spiderX: String?
}

private struct RuntimeTLSSettings: Encodable {
    let serverName: String
    let alpn: [String]?
    let fingerprint: String?
    let pinnedPeerCertSha256: String?
    let verifyPeerCertByName: String?
    let allowInsecure: Bool?
}

private struct RuntimeXHTTPSettings: Encodable {
    let host: String
    let path: String
    let mode: String
    let behaviorProfile: String
    let uplinkHTTPMethod: String
    let sessionPlacement: String?
    let sessionKey: String?
    let seqPlacement: String?
    let seqKey: String?
    let xPaddingBytes: String?
    let xPaddingMethod: String?
    let xPaddingPlacement: String?
    let xPaddingKey: String?
    let xPaddingObfsMode: Bool?
    let noGRPCHeader: Bool?
    let noSSEHeader: Bool?
    let scMaxEachPostBytes: String?
    let xmux: RuntimeXmuxSettings?
}

private struct RuntimeXmuxSettings: Encodable {
    let maxConcurrency: String?
    let maxConnections: String?
    let cMaxReuseTimes: String?
    let hMaxRequestTimes: String?
    let hMaxReusableSecs: String?
    let hKeepAlivePeriod: Int?
    let warmConnections: Int?

    init(_ settings: XHTTPXmuxSettings) {
        let normalized = settings.normalized
        self.maxConcurrency = normalized?.maxConcurrency
        self.maxConnections = normalized?.maxConnections
        self.cMaxReuseTimes = normalized?.cMaxReuseTimes
        self.hMaxRequestTimes = normalized?.hMaxRequestTimes
        self.hMaxReusableSecs = normalized?.hMaxReusableSecs
        self.hKeepAlivePeriod = normalized?.hKeepAlivePeriod
        self.warmConnections = normalized?.warmConnections
    }
}

private struct RuntimeRouting: Encodable {
    let domainStrategy: String
    let rules: [RuntimeRule]
}

private struct RuntimeRule: Encodable {
    let type: String
    let inboundTag: [String]
    let network: String?
    let port: String?
    let outboundTag: String

    init(
        type: String,
        inboundTag: [String],
        network: String? = nil,
        port: String? = nil,
        outboundTag: String
    ) {
        self.type = type
        self.inboundTag = inboundTag
        self.network = network
        self.port = port
        self.outboundTag = outboundTag
    }
}

private struct RuntimeDNS: Encodable {
    let servers: [String]
    let queryStrategy: String
    let disableCache: Bool
    let enableParallelQuery: Bool
}

private func emptyToNil(_ value: String?) -> String? {
    guard let value else {
        return nil
    }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
}
