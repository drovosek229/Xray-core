import CryptoKit
import Foundation

private func normalizeUplinkHTTPMethod(_ value: String?) -> String {
    let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines).uppercased() ?? ""
    return normalized.isEmpty ? ManualUplinkHTTPMethod.post.rawValue : normalized
}

private func normalizeVLESSEncryption(_ value: String?) -> String {
    let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    if normalized.isEmpty {
        return "none"
    }
    return normalized.lowercased() == "none" ? "none" : normalized
}

private func normalizeFlow(
    _ value: String?,
    securityKind: ProfileSecurityKind
) -> String? {
    let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    guard !normalized.isEmpty else {
        return nil
    }
    guard securityKind == .reality else {
        return nil
    }
    return normalized
}

private func emptyToNil(_ value: String?) -> String? {
    guard let value else {
        return nil
    }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
}

private func decodeLegacyRealitySettings(
    securityKind: ProfileSecurityKind,
    serverName: String?,
    fingerprint: String?,
    publicKey: String?,
    shortId: String?,
    spiderX: String?
) -> RealitySecuritySettings? {
    guard securityKind == .reality else {
        return nil
    }
    return RealitySecuritySettings(
        serverName: serverName ?? "",
        fingerprint: fingerprint ?? ClientFingerprintPreset.chrome.rawValue,
        publicKey: publicKey ?? "",
        shortId: shortId,
        spiderX: spiderX
    )
}

private func decodeLegacyTLSSettings(
    securityKind: ProfileSecurityKind,
    serverName: String?,
    fingerprint: String?
) -> TLSSecuritySettings? {
    guard securityKind == .tls else {
        return nil
    }
    return TLSSecuritySettings(
        serverName: serverName ?? "",
        fingerprint: fingerprint
    )
}

public enum BehaviorProfile: String, Codable, CaseIterable, Sendable {
    case legacy
    case balanced

    public var displayName: String {
        switch self {
        case .legacy:
            return "Legacy"
        case .balanced:
            return "Balanced"
        }
    }
}

public enum XHTTPMode: String, Codable, CaseIterable, Sendable {
    case auto
    case packetUp = "packet-up"
    case streamUp = "stream-up"
    case streamOne = "stream-one"

    public var displayName: String {
        switch self {
        case .auto:
            return "Auto"
        case .packetUp:
            return "Packet Upload"
        case .streamUp:
            return "Stream Upload"
        case .streamOne:
            return "Single Stream"
        }
    }
}

public enum ProfileSecurityKind: String, Codable, CaseIterable, Sendable {
    case reality
    case tls

    public var displayName: String {
        switch self {
        case .reality:
            return "REALITY"
        case .tls:
            return "TLS"
        }
    }
}

public enum ProfileClassification: String, Codable, CaseIterable, Sendable {
    case recommendedFast = "recommended-fast"
    case standard
    case stealthCompatibility = "stealth-compatibility"

    public var displayName: String {
        switch self {
        case .recommendedFast:
            return "Fast"
        case .standard:
            return "Standard"
        case .stealthCompatibility:
            return "Stealth"
        }
    }
}

public enum VLESSFlow: String, Codable, CaseIterable, Sendable {
    case none
    case xtlsRprxVision = "xtls-rprx-vision"
    case xtlsRprxVisionUDP443 = "xtls-rprx-vision-udp443"

    public var displayName: String {
        switch self {
        case .none:
            return "None"
        case .xtlsRprxVision:
            return "XTLS Vision"
        case .xtlsRprxVisionUDP443:
            return "XTLS Vision UDP443"
        }
    }

    public var runtimeValue: String? {
        switch self {
        case .none:
            return nil
        case .xtlsRprxVision, .xtlsRprxVisionUDP443:
            return rawValue
        }
    }

    public static func fromRuntimeValue(_ value: String?) -> VLESSFlow {
        guard let value, !value.isEmpty else {
            return .none
        }
        return VLESSFlow(rawValue: value) ?? .none
    }
}

public enum ClientFingerprintPreset: String, Codable, CaseIterable, Sendable {
    case chrome
    case safari
    case firefox
    case edge
    case ios

    public var displayName: String {
        switch self {
        case .chrome:
            return "Chrome"
        case .safari:
            return "Safari"
        case .firefox:
            return "Firefox"
        case .edge:
            return "Edge"
        case .ios:
            return "iOS"
        }
    }

    public static func fromRawValue(_ value: String) -> ClientFingerprintPreset {
        ClientFingerprintPreset(rawValue: value) ?? .chrome
    }
}

public enum ManualUplinkHTTPMethod: String, Codable, CaseIterable, Sendable {
    case post = "POST"
    case put = "PUT"
    case patch = "PATCH"
    case delete = "DELETE"
    case get = "GET"

    public var displayName: String { rawValue }
}

public struct RealitySecuritySettings: Codable, Hashable, Sendable {
    public var serverName: String
    public var fingerprint: String
    public var publicKey: String
    public var shortId: String?
    public var spiderX: String?

    public init(
        serverName: String,
        fingerprint: String = ClientFingerprintPreset.chrome.rawValue,
        publicKey: String,
        shortId: String? = nil,
        spiderX: String? = nil
    ) {
        self.serverName = serverName
        self.fingerprint = fingerprint
        self.publicKey = publicKey
        self.shortId = shortId
        self.spiderX = spiderX
    }
}

public struct TLSSecuritySettings: Codable, Hashable, Sendable {
    public var serverName: String
    public var fingerprint: String?
    public var alpn: [String]
    public var pinnedPeerCertSha256: String?
    public var verifyPeerCertByName: String?
    public var allowInsecure: Bool?

    public init(
        serverName: String,
        fingerprint: String? = ClientFingerprintPreset.chrome.rawValue,
        alpn: [String] = [],
        pinnedPeerCertSha256: String? = nil,
        verifyPeerCertByName: String? = nil,
        allowInsecure: Bool? = nil
    ) {
        self.serverName = serverName
        self.fingerprint = fingerprint
        self.alpn = alpn
        self.pinnedPeerCertSha256 = pinnedPeerCertSha256
        self.verifyPeerCertByName = verifyPeerCertByName
        self.allowInsecure = allowInsecure
    }
}

public struct XHTTPAdvancedSettings: Codable, Hashable, Sendable {
    public var sessionPlacement: String?
    public var sessionKey: String?
    public var seqPlacement: String?
    public var seqKey: String?
    public var xPaddingBytes: String?
    public var xPaddingMethod: String?
    public var xPaddingPlacement: String?
    public var xPaddingKey: String?
    public var xPaddingObfsMode: Bool?
    public var noGRPCHeader: Bool?
    public var noSSEHeader: Bool?
    public var scMaxEachPostBytes: String?

    public init(
        sessionPlacement: String? = nil,
        sessionKey: String? = nil,
        seqPlacement: String? = nil,
        seqKey: String? = nil,
        xPaddingBytes: String? = nil,
        xPaddingMethod: String? = nil,
        xPaddingPlacement: String? = nil,
        xPaddingKey: String? = nil,
        xPaddingObfsMode: Bool? = nil,
        noGRPCHeader: Bool? = nil,
        noSSEHeader: Bool? = nil,
        scMaxEachPostBytes: String? = nil
    ) {
        self.sessionPlacement = sessionPlacement
        self.sessionKey = sessionKey
        self.seqPlacement = seqPlacement
        self.seqKey = seqKey
        self.xPaddingBytes = xPaddingBytes
        self.xPaddingMethod = xPaddingMethod
        self.xPaddingPlacement = xPaddingPlacement
        self.xPaddingKey = xPaddingKey
        self.xPaddingObfsMode = xPaddingObfsMode
        self.noGRPCHeader = noGRPCHeader
        self.noSSEHeader = noSSEHeader
        self.scMaxEachPostBytes = scMaxEachPostBytes
    }

    public var isEmpty: Bool {
        sessionPlacement == nil &&
            sessionKey == nil &&
            seqPlacement == nil &&
            seqKey == nil &&
            xPaddingBytes == nil &&
            xPaddingMethod == nil &&
            xPaddingPlacement == nil &&
            xPaddingKey == nil &&
            xPaddingObfsMode == nil &&
            noGRPCHeader == nil &&
            noSSEHeader == nil &&
            scMaxEachPostBytes == nil
    }

    public var normalized: XHTTPAdvancedSettings? {
        let value = XHTTPAdvancedSettings(
            sessionPlacement: emptyToNil(sessionPlacement),
            sessionKey: emptyToNil(sessionKey),
            seqPlacement: emptyToNil(seqPlacement),
            seqKey: emptyToNil(seqKey),
            xPaddingBytes: emptyToNil(xPaddingBytes),
            xPaddingMethod: emptyToNil(xPaddingMethod),
            xPaddingPlacement: emptyToNil(xPaddingPlacement),
            xPaddingKey: emptyToNil(xPaddingKey),
            xPaddingObfsMode: xPaddingObfsMode,
            noGRPCHeader: noGRPCHeader,
            noSSEHeader: noSSEHeader,
            scMaxEachPostBytes: emptyToNil(scMaxEachPostBytes)
        )
        return value.isEmpty ? nil : value
    }
}

private func stableUUID(for signature: String) -> UUID {
    let digest = SHA256.hash(data: Data(signature.utf8))
    let bytes = Array(digest)
    var uuidBytes = Array(bytes.prefix(16))
    uuidBytes[6] = (uuidBytes[6] & 0x0F) | 0x50
    uuidBytes[8] = (uuidBytes[8] & 0x3F) | 0x80
    let uuid = uuid_t(
        uuidBytes[0], uuidBytes[1], uuidBytes[2], uuidBytes[3],
        uuidBytes[4], uuidBytes[5], uuidBytes[6], uuidBytes[7],
        uuidBytes[8], uuidBytes[9], uuidBytes[10], uuidBytes[11],
        uuidBytes[12], uuidBytes[13], uuidBytes[14], uuidBytes[15]
    )
    return UUID(uuid: uuid)
}

private func advancedXHTTPSignature(_ settings: XHTTPAdvancedSettings?) -> String {
    guard let settings = settings?.normalized else {
        return ""
    }
    var parts: [String] = []
    parts.append(settings.sessionPlacement ?? "")
    parts.append(settings.sessionKey ?? "")
    parts.append(settings.seqPlacement ?? "")
    parts.append(settings.seqKey ?? "")
    parts.append(settings.xPaddingBytes ?? "")
    parts.append(settings.xPaddingMethod ?? "")
    parts.append(settings.xPaddingPlacement ?? "")
    parts.append(settings.xPaddingKey ?? "")
    parts.append(settings.xPaddingObfsMode.map(String.init) ?? "")
    parts.append(settings.noGRPCHeader.map(String.init) ?? "")
    parts.append(settings.noSSEHeader.map(String.init) ?? "")
    parts.append(settings.scMaxEachPostBytes ?? "")
    return parts.joined(separator: "|")
}

public enum ProfileReference: Hashable, Sendable {
    case manual(UUID)
    case subscriptionEndpoint(UUID)
}

extension ProfileReference: Codable {
    private enum CodingKeys: String, CodingKey {
        case kind
        case id
    }

    private enum Kind: String, Codable {
        case manual
        case subscriptionEndpoint
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let kind = try container.decode(Kind.self, forKey: .kind)
        let id = try container.decode(UUID.self, forKey: .id)
        switch kind {
        case .manual:
            self = .manual(id)
        case .subscriptionEndpoint:
            self = .subscriptionEndpoint(id)
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case let .manual(id):
            try container.encode(Kind.manual, forKey: .kind)
            try container.encode(id, forKey: .id)
        case let .subscriptionEndpoint(id):
            try container.encode(Kind.subscriptionEndpoint, forKey: .kind)
            try container.encode(id, forKey: .id)
        }
    }
}

public struct ManualProfile: Identifiable, Hashable, Sendable {
    public var id: UUID
    public var name: String
    public var address: String
    public var port: Int
    public var uuid: String
    public var flow: String?
    public var securityKind: ProfileSecurityKind
    public var realitySettings: RealitySecuritySettings?
    public var tlsSettings: TLSSecuritySettings?
    public var encryption: String
    public var xhttpHost: String
    public var xhttpPath: String
    public var xhttpMode: XHTTPMode
    public var behaviorProfile: BehaviorProfile
    public var uplinkHTTPMethod: String
    public var xhttpAdvancedSettings: XHTTPAdvancedSettings?

    public init(
        id: UUID = UUID(),
        name: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String? = nil,
        securityKind: ProfileSecurityKind,
        realitySettings: RealitySecuritySettings? = nil,
        tlsSettings: TLSSecuritySettings? = nil,
        encryption: String = "none",
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode = .auto,
        behaviorProfile: BehaviorProfile = .balanced,
        uplinkHTTPMethod: String = ManualUplinkHTTPMethod.post.rawValue,
        xhttpAdvancedSettings: XHTTPAdvancedSettings? = nil
    ) {
        self.id = id
        self.name = name
        self.address = address
        self.port = port
        self.uuid = uuid
        self.flow = normalizeFlow(flow, securityKind: securityKind)
        self.securityKind = securityKind
        self.realitySettings = realitySettings
        self.tlsSettings = tlsSettings
        self.encryption = normalizeVLESSEncryption(encryption)
        self.xhttpHost = xhttpHost
        self.xhttpPath = xhttpPath
        self.xhttpMode = xhttpMode
        self.behaviorProfile = behaviorProfile
        self.uplinkHTTPMethod = normalizeUplinkHTTPMethod(uplinkHTTPMethod)
        self.xhttpAdvancedSettings = xhttpAdvancedSettings?.normalized
    }

    public init(
        id: UUID = UUID(),
        name: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String? = nil,
        serverName: String,
        fingerprint: String = ClientFingerprintPreset.chrome.rawValue,
        publicKey: String,
        shortId: String? = nil,
        spiderX: String? = nil,
        encryption: String = "none",
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode = .auto,
        behaviorProfile: BehaviorProfile = .balanced,
        uplinkHTTPMethod: String = ManualUplinkHTTPMethod.post.rawValue,
        xhttpAdvancedSettings: XHTTPAdvancedSettings? = nil
    ) {
        self.init(
            id: id,
            name: name,
            address: address,
            port: port,
            uuid: uuid,
            flow: flow,
            securityKind: .reality,
            realitySettings: RealitySecuritySettings(
                serverName: serverName,
                fingerprint: fingerprint,
                publicKey: publicKey,
                shortId: shortId,
                spiderX: spiderX
            ),
            encryption: encryption,
            xhttpHost: xhttpHost,
            xhttpPath: xhttpPath,
            xhttpMode: xhttpMode,
            behaviorProfile: behaviorProfile,
            uplinkHTTPMethod: uplinkHTTPMethod,
            xhttpAdvancedSettings: xhttpAdvancedSettings
        )
    }

    public var serverName: String {
        switch securityKind {
        case .reality:
            return realitySettings?.serverName ?? ""
        case .tls:
            return tlsSettings?.serverName ?? ""
        }
    }

    public var fingerprint: String {
        switch securityKind {
        case .reality:
            return realitySettings?.fingerprint ?? ""
        case .tls:
            return tlsSettings?.fingerprint ?? ""
        }
    }

    public var publicKey: String {
        realitySettings?.publicKey ?? ""
    }

    public var shortId: String? {
        realitySettings?.shortId
    }

    public var spiderX: String? {
        realitySettings?.spiderX
    }

    public var normalizedUplinkHTTPMethod: String {
        normalizeUplinkHTTPMethod(uplinkHTTPMethod)
    }

    public var normalizedEncryption: String {
        normalizeVLESSEncryption(encryption)
    }

    public var classification: ProfileClassification {
        classifyProfile(
            securityKind: securityKind,
            xhttpMode: xhttpMode,
            uplinkHTTPMethod: normalizedUplinkHTTPMethod,
            encryption: normalizedEncryption
        )
    }
}

extension ManualProfile: Codable {
    private enum CodingKeys: String, CodingKey {
        case id
        case name
        case address
        case port
        case uuid
        case flow
        case securityKind
        case realitySettings
        case tlsSettings
        case encryption
        case serverName
        case fingerprint
        case publicKey
        case shortId
        case spiderX
        case xhttpHost
        case xhttpPath
        case xhttpMode
        case behaviorProfile
        case uplinkHTTPMethod
        case xhttpAdvancedSettings
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let securityKind = try container.decodeIfPresent(ProfileSecurityKind.self, forKey: .securityKind) ?? .reality
        let legacyServerName = try container.decodeIfPresent(String.self, forKey: .serverName)
        let legacyFingerprint = try container.decodeIfPresent(String.self, forKey: .fingerprint)
        let legacyPublicKey = try container.decodeIfPresent(String.self, forKey: .publicKey)
        let legacyShortId = try container.decodeIfPresent(String.self, forKey: .shortId)
        let legacySpiderX = try container.decodeIfPresent(String.self, forKey: .spiderX)

        self.id = try container.decodeIfPresent(UUID.self, forKey: .id) ?? UUID()
        self.name = try container.decode(String.self, forKey: .name)
        self.address = try container.decode(String.self, forKey: .address)
        self.port = try container.decode(Int.self, forKey: .port)
        self.uuid = try container.decode(String.self, forKey: .uuid)
        self.flow = normalizeFlow(
            try container.decodeIfPresent(String.self, forKey: .flow),
            securityKind: securityKind
        )
        self.securityKind = securityKind
        self.realitySettings = try container.decodeIfPresent(RealitySecuritySettings.self, forKey: .realitySettings)
            ?? decodeLegacyRealitySettings(
                securityKind: securityKind,
                serverName: legacyServerName,
                fingerprint: legacyFingerprint,
                publicKey: legacyPublicKey,
                shortId: legacyShortId,
                spiderX: legacySpiderX
            )
        self.tlsSettings = try container.decodeIfPresent(TLSSecuritySettings.self, forKey: .tlsSettings)
            ?? decodeLegacyTLSSettings(
                securityKind: securityKind,
                serverName: legacyServerName,
                fingerprint: legacyFingerprint
            )
        self.encryption = normalizeVLESSEncryption(
            try container.decodeIfPresent(String.self, forKey: .encryption)
        )
        self.xhttpHost = try container.decodeIfPresent(String.self, forKey: .xhttpHost) ?? ""
        self.xhttpPath = try container.decodeIfPresent(String.self, forKey: .xhttpPath) ?? ""
        self.xhttpMode = try container.decodeIfPresent(XHTTPMode.self, forKey: .xhttpMode) ?? .auto
        self.behaviorProfile = try container.decodeIfPresent(BehaviorProfile.self, forKey: .behaviorProfile) ?? .balanced
        self.uplinkHTTPMethod = normalizeUplinkHTTPMethod(
            try container.decodeIfPresent(String.self, forKey: .uplinkHTTPMethod)
        )
        self.xhttpAdvancedSettings = try container.decodeIfPresent(XHTTPAdvancedSettings.self, forKey: .xhttpAdvancedSettings)?.normalized
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(name, forKey: .name)
        try container.encode(address, forKey: .address)
        try container.encode(port, forKey: .port)
        try container.encode(uuid, forKey: .uuid)
        try container.encodeIfPresent(flow, forKey: .flow)
        try container.encode(securityKind, forKey: .securityKind)
        try container.encodeIfPresent(realitySettings, forKey: .realitySettings)
        try container.encodeIfPresent(tlsSettings, forKey: .tlsSettings)
        try container.encode(normalizedEncryption, forKey: .encryption)
        try container.encode(xhttpHost, forKey: .xhttpHost)
        try container.encode(xhttpPath, forKey: .xhttpPath)
        try container.encode(xhttpMode, forKey: .xhttpMode)
        try container.encode(behaviorProfile, forKey: .behaviorProfile)
        try container.encode(normalizedUplinkHTTPMethod, forKey: .uplinkHTTPMethod)
        try container.encodeIfPresent(xhttpAdvancedSettings?.normalized, forKey: .xhttpAdvancedSettings)
    }
}

public struct SubscriptionSource: Identifiable, Hashable, Sendable {
    public var id: UUID
    public var name: String
    public var subscriptionURL: URL
    public var lastSyncAt: Date?
    public var etag: String?
    public var lastModified: String?
    public var hwid: String

    public init(
        id: UUID = UUID(),
        name: String,
        subscriptionURL: URL,
        lastSyncAt: Date? = nil,
        etag: String? = nil,
        lastModified: String? = nil,
        hwid: String
    ) {
        self.id = id
        self.name = name
        self.subscriptionURL = subscriptionURL
        self.lastSyncAt = lastSyncAt
        self.etag = etag
        self.lastModified = lastModified
        self.hwid = hwid
    }
}

extension SubscriptionSource: Codable {
    private enum CodingKeys: String, CodingKey {
        case id
        case name
        case subscriptionURL
        case feedURL
        case lastSyncAt
        case etag
        case lastModified
        case hwid
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)

        self.id = try container.decodeIfPresent(UUID.self, forKey: .id) ?? UUID()
        self.name = try container.decode(String.self, forKey: .name)
        self.subscriptionURL =
            try container.decodeIfPresent(URL.self, forKey: .subscriptionURL)
            ?? container.decode(URL.self, forKey: .feedURL)
        self.lastSyncAt = try container.decodeIfPresent(Date.self, forKey: .lastSyncAt)
        self.etag = try container.decodeIfPresent(String.self, forKey: .etag)
        self.lastModified = try container.decodeIfPresent(String.self, forKey: .lastModified)
        self.hwid = try container.decode(String.self, forKey: .hwid)
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(name, forKey: .name)
        try container.encode(subscriptionURL, forKey: .subscriptionURL)
        try container.encodeIfPresent(lastSyncAt, forKey: .lastSyncAt)
        try container.encodeIfPresent(etag, forKey: .etag)
        try container.encodeIfPresent(lastModified, forKey: .lastModified)
        try container.encode(hwid, forKey: .hwid)
    }
}

public struct SubscriptionEndpoint: Identifiable, Hashable, Sendable {
    public var id: UUID
    public var sourceID: UUID
    public var displayName: String
    public var address: String
    public var port: Int
    public var uuid: String
    public var flow: String?
    public var securityKind: ProfileSecurityKind
    public var realitySettings: RealitySecuritySettings?
    public var tlsSettings: TLSSecuritySettings?
    public var encryption: String
    public var xhttpHost: String
    public var xhttpPath: String
    public var xhttpMode: XHTTPMode
    public var behaviorProfile: BehaviorProfile
    public var uplinkHTTPMethod: String
    public var xhttpAdvancedSettings: XHTTPAdvancedSettings?
    public var tags: [String]
    public var metadata: [String: String]

    public init(
        id: UUID = UUID(),
        sourceID: UUID,
        displayName: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String? = nil,
        securityKind: ProfileSecurityKind,
        realitySettings: RealitySecuritySettings? = nil,
        tlsSettings: TLSSecuritySettings? = nil,
        encryption: String = "none",
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode = .auto,
        behaviorProfile: BehaviorProfile = .balanced,
        uplinkHTTPMethod: String = ManualUplinkHTTPMethod.post.rawValue,
        xhttpAdvancedSettings: XHTTPAdvancedSettings? = nil,
        tags: [String] = [],
        metadata: [String: String] = [:]
    ) {
        self.id = id
        self.sourceID = sourceID
        self.displayName = displayName
        self.address = address
        self.port = port
        self.uuid = uuid
        self.flow = normalizeFlow(flow, securityKind: securityKind)
        self.securityKind = securityKind
        self.realitySettings = realitySettings
        self.tlsSettings = tlsSettings
        self.encryption = normalizeVLESSEncryption(encryption)
        self.xhttpHost = xhttpHost
        self.xhttpPath = xhttpPath
        self.xhttpMode = xhttpMode
        self.behaviorProfile = behaviorProfile
        self.uplinkHTTPMethod = normalizeUplinkHTTPMethod(uplinkHTTPMethod)
        self.xhttpAdvancedSettings = xhttpAdvancedSettings?.normalized
        self.tags = tags
        self.metadata = metadata
    }

    public init(
        id: UUID = UUID(),
        sourceID: UUID,
        displayName: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String? = nil,
        serverName: String,
        fingerprint: String,
        publicKey: String,
        shortId: String? = nil,
        spiderX: String? = nil,
        encryption: String = "none",
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode = .auto,
        behaviorProfile: BehaviorProfile = .balanced,
        uplinkHTTPMethod: String = ManualUplinkHTTPMethod.post.rawValue,
        xhttpAdvancedSettings: XHTTPAdvancedSettings? = nil,
        tags: [String] = [],
        metadata: [String: String] = [:]
    ) {
        self.init(
            id: id,
            sourceID: sourceID,
            displayName: displayName,
            address: address,
            port: port,
            uuid: uuid,
            flow: flow,
            securityKind: .reality,
            realitySettings: RealitySecuritySettings(
                serverName: serverName,
                fingerprint: fingerprint,
                publicKey: publicKey,
                shortId: shortId,
                spiderX: spiderX
            ),
            encryption: encryption,
            xhttpHost: xhttpHost,
            xhttpPath: xhttpPath,
            xhttpMode: xhttpMode,
            behaviorProfile: behaviorProfile,
            uplinkHTTPMethod: uplinkHTTPMethod,
            xhttpAdvancedSettings: xhttpAdvancedSettings,
            tags: tags,
            metadata: metadata
        )
    }

    public var serverName: String {
        switch securityKind {
        case .reality:
            return realitySettings?.serverName ?? ""
        case .tls:
            return tlsSettings?.serverName ?? ""
        }
    }

    public var fingerprint: String {
        switch securityKind {
        case .reality:
            return realitySettings?.fingerprint ?? ""
        case .tls:
            return tlsSettings?.fingerprint ?? ""
        }
    }

    public var publicKey: String {
        realitySettings?.publicKey ?? ""
    }

    public var shortId: String? {
        realitySettings?.shortId
    }

    public var spiderX: String? {
        realitySettings?.spiderX
    }

    public var normalizedUplinkHTTPMethod: String {
        normalizeUplinkHTTPMethod(uplinkHTTPMethod)
    }

    public var normalizedEncryption: String {
        normalizeVLESSEncryption(encryption)
    }

    public var classification: ProfileClassification {
        classifyProfile(
            securityKind: securityKind,
            xhttpMode: xhttpMode,
            uplinkHTTPMethod: normalizedUplinkHTTPMethod,
            encryption: normalizedEncryption
        )
    }

    public static func stableID(
        sourceID: UUID,
        displayName: String,
        address: String,
        port: Int,
        uuid: String,
        flow: String?,
        securityKind: ProfileSecurityKind,
        serverName: String,
        fingerprint: String,
        publicKey: String,
        shortId: String?,
        spiderX: String?,
        alpn: [String],
        encryption: String,
        xhttpHost: String,
        xhttpPath: String,
        xhttpMode: XHTTPMode,
        behaviorProfile: BehaviorProfile,
        uplinkHTTPMethod: String,
        xhttpAdvancedSettings: XHTTPAdvancedSettings?
    ) -> UUID {
        let signature = [
            sourceID.uuidString.lowercased(),
            displayName.trimmingCharacters(in: .whitespacesAndNewlines),
            address.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
            String(port),
            uuid.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
            flow?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "",
            securityKind.rawValue,
            serverName.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
            fingerprint.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
            publicKey.trimmingCharacters(in: .whitespacesAndNewlines),
            shortId?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "",
            spiderX?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "",
            alpn.joined(separator: ","),
            normalizeVLESSEncryption(encryption),
            xhttpHost.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
            xhttpPath.trimmingCharacters(in: .whitespacesAndNewlines),
            xhttpMode.rawValue,
            behaviorProfile.rawValue,
            normalizeUplinkHTTPMethod(uplinkHTTPMethod),
            advancedXHTTPSignature(xhttpAdvancedSettings),
        ].joined(separator: "\n")
        return stableUUID(for: signature)
    }
}

extension SubscriptionEndpoint: Codable {
    private enum CodingKeys: String, CodingKey {
        case id
        case sourceID
        case displayName
        case address
        case port
        case uuid
        case flow
        case securityKind
        case realitySettings
        case tlsSettings
        case encryption
        case serverName
        case fingerprint
        case publicKey
        case shortId
        case spiderX
        case xhttpHost
        case xhttpPath
        case xhttpMode
        case behaviorProfile
        case uplinkHTTPMethod
        case xhttpAdvancedSettings
        case tags
        case metadata
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let securityKind = try container.decodeIfPresent(ProfileSecurityKind.self, forKey: .securityKind) ?? .reality
        let legacyServerName = try container.decodeIfPresent(String.self, forKey: .serverName)
        let legacyFingerprint = try container.decodeIfPresent(String.self, forKey: .fingerprint)
        let legacyPublicKey = try container.decodeIfPresent(String.self, forKey: .publicKey)
        let legacyShortId = try container.decodeIfPresent(String.self, forKey: .shortId)
        let legacySpiderX = try container.decodeIfPresent(String.self, forKey: .spiderX)

        self.id = try container.decodeIfPresent(UUID.self, forKey: .id) ?? UUID()
        self.sourceID = try container.decode(UUID.self, forKey: .sourceID)
        self.displayName = try container.decode(String.self, forKey: .displayName)
        self.address = try container.decode(String.self, forKey: .address)
        self.port = try container.decode(Int.self, forKey: .port)
        self.uuid = try container.decode(String.self, forKey: .uuid)
        self.flow = normalizeFlow(
            try container.decodeIfPresent(String.self, forKey: .flow),
            securityKind: securityKind
        )
        self.securityKind = securityKind
        self.realitySettings = try container.decodeIfPresent(RealitySecuritySettings.self, forKey: .realitySettings)
            ?? decodeLegacyRealitySettings(
                securityKind: securityKind,
                serverName: legacyServerName,
                fingerprint: legacyFingerprint,
                publicKey: legacyPublicKey,
                shortId: legacyShortId,
                spiderX: legacySpiderX
            )
        self.tlsSettings = try container.decodeIfPresent(TLSSecuritySettings.self, forKey: .tlsSettings)
            ?? decodeLegacyTLSSettings(
                securityKind: securityKind,
                serverName: legacyServerName,
                fingerprint: legacyFingerprint
            )
        self.encryption = normalizeVLESSEncryption(
            try container.decodeIfPresent(String.self, forKey: .encryption)
        )
        self.xhttpHost = try container.decodeIfPresent(String.self, forKey: .xhttpHost) ?? ""
        self.xhttpPath = try container.decodeIfPresent(String.self, forKey: .xhttpPath) ?? ""
        self.xhttpMode = try container.decodeIfPresent(XHTTPMode.self, forKey: .xhttpMode) ?? .auto
        self.behaviorProfile = try container.decodeIfPresent(BehaviorProfile.self, forKey: .behaviorProfile) ?? .balanced
        self.uplinkHTTPMethod = normalizeUplinkHTTPMethod(
            try container.decodeIfPresent(String.self, forKey: .uplinkHTTPMethod)
        )
        self.xhttpAdvancedSettings = try container.decodeIfPresent(XHTTPAdvancedSettings.self, forKey: .xhttpAdvancedSettings)?.normalized
        self.tags = try container.decodeIfPresent([String].self, forKey: .tags) ?? []
        self.metadata = try container.decodeIfPresent([String: String].self, forKey: .metadata) ?? [:]
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(sourceID, forKey: .sourceID)
        try container.encode(displayName, forKey: .displayName)
        try container.encode(address, forKey: .address)
        try container.encode(port, forKey: .port)
        try container.encode(uuid, forKey: .uuid)
        try container.encodeIfPresent(flow, forKey: .flow)
        try container.encode(securityKind, forKey: .securityKind)
        try container.encodeIfPresent(realitySettings, forKey: .realitySettings)
        try container.encodeIfPresent(tlsSettings, forKey: .tlsSettings)
        try container.encode(normalizedEncryption, forKey: .encryption)
        try container.encode(xhttpHost, forKey: .xhttpHost)
        try container.encode(xhttpPath, forKey: .xhttpPath)
        try container.encode(xhttpMode, forKey: .xhttpMode)
        try container.encode(behaviorProfile, forKey: .behaviorProfile)
        try container.encode(normalizedUplinkHTTPMethod, forKey: .uplinkHTTPMethod)
        try container.encodeIfPresent(xhttpAdvancedSettings?.normalized, forKey: .xhttpAdvancedSettings)
        try container.encode(tags, forKey: .tags)
        try container.encode(metadata, forKey: .metadata)
    }
}

public struct SubscriptionClientFingerprint: Hashable, Sendable {
    public var userAgent: String
    public var hwid: String
    public var deviceOS: String
    public var osVersion: String
    public var deviceModel: String

    public init(
        userAgent: String,
        hwid: String,
        deviceOS: String,
        osVersion: String,
        deviceModel: String
    ) {
        self.userAgent = userAgent
        self.hwid = hwid
        self.deviceOS = deviceOS
        self.osVersion = osVersion
        self.deviceModel = deviceModel
    }

    public func headers() -> [String: String] {
        [
            "User-Agent": userAgent,
            "x-hwid": hwid,
            "x-device-os": deviceOS,
            "x-ver-os": osVersion,
            "x-device-model": deviceModel,
        ]
    }
}

public enum XrayAppCoreError: LocalizedError, Equatable {
    case invalidProfile(String)
    case invalidSubscriptionURL
    case unsupportedSubscriptionPayload
    case noSupportedEndpoints
    case invalidResponseStatus(Int)
    case notModified

    public var errorDescription: String? {
        switch self {
        case let .invalidProfile(message):
            return message
        case .invalidSubscriptionURL:
            return "The subscription link is invalid."
        case .unsupportedSubscriptionPayload:
            return "The subscription payload does not look like a supported profile list or Xray config."
        case .noSupportedEndpoints:
            return "The subscription did not contain any VLESS + XHTTP endpoints over REALITY or TLS."
        case let .invalidResponseStatus(status):
            return "The subscription endpoint returned an unexpected HTTP status: \(status)."
        case .notModified:
            return "The subscription link has not changed."
        }
    }
}

private func classifyProfile(
    securityKind: ProfileSecurityKind,
    xhttpMode: XHTTPMode,
    uplinkHTTPMethod: String,
    encryption: String
) -> ProfileClassification {
    let normalizedMethod = normalizeUplinkHTTPMethod(uplinkHTTPMethod)
    let normalizedEncryption = normalizeVLESSEncryption(encryption)

    if securityKind == .tls &&
        xhttpMode == .streamUp &&
        normalizedMethod == ManualUplinkHTTPMethod.put.rawValue &&
        normalizedEncryption == "none" {
        return .recommendedFast
    }

    if xhttpMode == .packetUp &&
        normalizedMethod == ManualUplinkHTTPMethod.delete.rawValue &&
        normalizedEncryption != "none" {
        return .stealthCompatibility
    }

    return .standard
}
