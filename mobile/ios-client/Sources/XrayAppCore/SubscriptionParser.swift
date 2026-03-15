import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

public struct SubscriptionFetchResponse: Sendable {
    public var endpoints: [SubscriptionEndpoint]
    public var etag: String?
    public var lastModified: String?

    public init(endpoints: [SubscriptionEndpoint], etag: String?, lastModified: String?) {
        self.endpoints = endpoints
        self.etag = etag
        self.lastModified = lastModified
    }
}

public final class SubscriptionClient: @unchecked Sendable {
    private let session: URLSession

    public init(session: URLSession = .shared) {
        self.session = session
    }

    public func makeRequest(
        for source: SubscriptionSource,
        fingerprint: SubscriptionClientFingerprint
    ) -> URLRequest {
        var request = URLRequest(url: source.subscriptionURL)
        request.httpMethod = "GET"
        request.timeoutInterval = 30
        for (header, value) in fingerprint.headers() {
            request.setValue(value, forHTTPHeaderField: header)
        }
        if let etag = source.etag, !etag.isEmpty {
            request.setValue(etag, forHTTPHeaderField: "If-None-Match")
        }
        if let lastModified = source.lastModified, !lastModified.isEmpty {
            request.setValue(lastModified, forHTTPHeaderField: "If-Modified-Since")
        }
        return request
    }

    public func fetch(
        source: SubscriptionSource,
        fingerprint: SubscriptionClientFingerprint
    ) async throws -> SubscriptionFetchResponse {
        let request = makeRequest(for: source, fingerprint: fingerprint)
        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw XrayAppCoreError.unsupportedSubscriptionPayload
        }
        if httpResponse.statusCode == 304 {
            throw XrayAppCoreError.notModified
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            throw XrayAppCoreError.invalidResponseStatus(httpResponse.statusCode)
        }

        let endpoints = try SubscriptionParser.parse(sourceID: source.id, data: data)
        return SubscriptionFetchResponse(
            endpoints: endpoints,
            etag: httpResponse.value(forHTTPHeaderField: "ETag"),
            lastModified: httpResponse.value(forHTTPHeaderField: "Last-Modified")
        )
    }
}

public enum SubscriptionParser {
    public static func parse(sourceID: UUID, data: Data) throws -> [SubscriptionEndpoint] {
        var recognizedPayload = false

        if let result = parseJSONPayload(sourceID: sourceID, data: data) {
            recognizedPayload = true
            if !result.isEmpty {
                return result
            }
        }

        for text in decodedTextCandidates(from: data) {
            let result = parseTextPayload(sourceID: sourceID, text: text)
            if result.recognized {
                recognizedPayload = true
            }
            if !result.endpoints.isEmpty {
                return result.endpoints
            }
        }

        if recognizedPayload {
            throw XrayAppCoreError.noSupportedEndpoints
        }
        throw XrayAppCoreError.unsupportedSubscriptionPayload
    }

    private static func parseJSONPayload(sourceID: UUID, data: Data) -> [SubscriptionEndpoint]? {
        let decoder = JSONDecoder()

        if let config = try? decoder.decode(ImportedConfig.self, from: data) {
            return parseConfig(sourceID: sourceID, config: config)
        }

        if let configs = try? decoder.decode([ImportedConfig].self, from: data) {
            return configs.flatMap { parseConfig(sourceID: sourceID, config: $0) }
        }

        if let envelope = try? decoder.decode(ImportedLinksEnvelope.self, from: data) {
            return parseLinkLines(sourceID: sourceID, links: envelope.links)
        }

        if let flexibleLinks = parseFlexibleLinkArrays(from: data) {
            return parseLinkLines(sourceID: sourceID, links: flexibleLinks)
        }

        return nil
    }

    private static func decodedTextCandidates(from data: Data) -> [String] {
        var candidates: [String] = []
        var seen = Set<String>()

        func add(_ value: String?) {
            guard let value else {
                return
            }
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty, !seen.contains(trimmed) else {
                return
            }
            seen.insert(trimmed)
            candidates.append(trimmed)
        }

        add(String(data: data, encoding: .utf8))

        let rawText = String(decoding: data, as: UTF8.self)
        let compact = rawText.components(separatedBy: .whitespacesAndNewlines).joined()
        add(decodedBase64Text(compact))
        add(decodedBase64Text(compact.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")))

        return candidates
    }

    private static func decodedBase64Text(_ value: String) -> String? {
        guard !value.isEmpty else {
            return nil
        }
        let paddedLength = ((value.count + 3) / 4) * 4
        let padded = value.padding(toLength: paddedLength, withPad: "=", startingAt: 0)
        guard let data = Data(base64Encoded: padded, options: [.ignoreUnknownCharacters]) else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    private static func parseTextPayload(sourceID: UUID, text: String) -> (recognized: Bool, endpoints: [SubscriptionEndpoint]) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return (false, [])
        }

        if let jsonResult = parseJSONPayload(sourceID: sourceID, data: Data(trimmed.utf8)) {
            return (true, jsonResult)
        }

        let linkLines = trimmed
            .components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        let recognized = linkLines.contains { $0.lowercased().hasPrefix("vless://") }
        guard recognized else {
            return (false, [])
        }

        return (true, parseLinkLines(sourceID: sourceID, links: linkLines))
    }

    private static func parseLinkLines(sourceID: UUID, links: [String]) -> [SubscriptionEndpoint] {
        links.compactMap { parseVLESSLink(sourceID: sourceID, link: $0) }
    }

    private static func parseVLESSLink(sourceID: UUID, link: String) -> SubscriptionEndpoint? {
        guard let components = URLComponents(string: link), components.scheme?.lowercased() == "vless" else {
            return nil
        }
        let queryItems = Dictionary(grouping: components.queryItems ?? [], by: \.name)
        let transport = queryValue("type", in: queryItems)?.lowercased()

        let extra = queryValue("extra", in: queryItems).flatMap(parseLinkExtra)
        let extraTransport = extra?.network?.lowercased()
        guard transport == "xhttp" || extraTransport == "xhttp" else {
            return nil
        }

        guard
            let uuid = components.user?.trimmingCharacters(in: .whitespacesAndNewlines),
            !uuid.isEmpty,
            let address = components.host?.trimmingCharacters(in: .whitespacesAndNewlines),
            !address.isEmpty,
            let port = components.port
        else {
            return nil
        }

        let securityValue = (queryValue("security", in: queryItems) ?? extra?.security)?.lowercased() ?? ""
        guard let securityKind = ProfileSecurityKind(rawValue: securityValue) else {
            return nil
        }

        let displayName = components.fragment?.removingPercentEncoding?.trimmingCharacters(in: .whitespacesAndNewlines)
            ?? extra?.displayName
            ?? "\(address):\(port)"
        let flow = emptyToNil(queryValue("flow", in: queryItems) ?? extra?.flow)
        let encryption = queryValue("encryption", in: queryItems) ?? extra?.encryption ?? "none"
        let serverName = emptyToNil(
            queryValue("sni", in: queryItems)
                ?? queryValue("serverName", in: queryItems)
                ?? extra?.serverName
        )
        let host = emptyToNil(queryValue("host", in: queryItems) ?? extra?.host)
        let path = queryValue("path", in: queryItems) ?? extra?.path ?? "/"
        let mode = XHTTPMode(rawValue: queryValue("mode", in: queryItems) ?? extra?.mode ?? "") ?? .auto
        let behaviorProfile = BehaviorProfile(rawValue: queryValue("behaviorProfile", in: queryItems) ?? extra?.behaviorProfile ?? "") ?? .balanced
        let uplinkHTTPMethod = queryValue("uplinkHTTPMethod", in: queryItems) ?? extra?.uplinkHTTPMethod ?? ManualUplinkHTTPMethod.post.rawValue
        let advancedXHTTPSettings = makeAdvancedXHTTPSettings(queryItems: queryItems, extra: extra)

        switch securityKind {
        case .reality:
            let fingerprint = emptyToNil(queryValue("fp", in: queryItems) ?? extra?.fingerprint) ?? ClientFingerprintPreset.chrome.rawValue
            guard
                let publicKey = emptyToNil(queryValue("pbk", in: queryItems) ?? queryValue("publicKey", in: queryItems) ?? extra?.publicKey),
                let serverName
            else {
                return nil
            }

            let endpoint = SubscriptionEndpoint(
                id: SubscriptionEndpoint.stableID(
                    sourceID: sourceID,
                    displayName: displayName,
                    address: address,
                    port: port,
                    uuid: uuid,
                    flow: flow,
                    securityKind: .reality,
                    serverName: serverName,
                    fingerprint: fingerprint,
                    publicKey: publicKey,
                    shortId: emptyToNil(queryValue("sid", in: queryItems) ?? queryValue("shortId", in: queryItems) ?? extra?.shortId),
                    spiderX: emptyToNil(queryValue("spx", in: queryItems) ?? queryValue("spiderX", in: queryItems) ?? extra?.spiderX),
                    alpn: [],
                    encryption: encryption,
                    xhttpHost: host ?? serverName,
                    xhttpPath: path,
                    xhttpMode: mode,
                    behaviorProfile: behaviorProfile,
                    uplinkHTTPMethod: uplinkHTTPMethod,
                    xhttpAdvancedSettings: advancedXHTTPSettings
                ),
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
                    shortId: emptyToNil(queryValue("sid", in: queryItems) ?? queryValue("shortId", in: queryItems) ?? extra?.shortId),
                    spiderX: emptyToNil(queryValue("spx", in: queryItems) ?? queryValue("spiderX", in: queryItems) ?? extra?.spiderX)
                ),
                encryption: encryption,
                xhttpHost: host ?? serverName,
                xhttpPath: path,
                xhttpMode: mode,
                behaviorProfile: behaviorProfile,
                uplinkHTTPMethod: uplinkHTTPMethod,
                xhttpAdvancedSettings: advancedXHTTPSettings,
                tags: [],
                metadata: [
                    "network": "xhttp",
                    "security": "reality",
                ]
            )
            return (try? RuntimeConfigBuilder.build(for: endpoint)) != nil ? endpoint : nil
        case .tls:
            let alpn = parseALPN(queryValue("alpn", in: queryItems)) ?? extra?.alpn ?? []
            guard let serverName else {
                return nil
            }

            var metadata = [
                "network": "xhttp",
                "security": "tls",
            ]
            let allowInsecure = parseBool(queryValue("allowInsecure", in: queryItems)) ?? extra?.allowInsecure
            if allowInsecure == true {
                metadata["tls_allow_insecure"] = "true"
            }

            let endpoint = SubscriptionEndpoint(
                id: SubscriptionEndpoint.stableID(
                    sourceID: sourceID,
                    displayName: displayName,
                    address: address,
                    port: port,
                    uuid: uuid,
                    flow: flow,
                    securityKind: .tls,
                    serverName: serverName,
                    fingerprint: emptyToNil(queryValue("fp", in: queryItems) ?? extra?.fingerprint) ?? "",
                    publicKey: "",
                    shortId: nil,
                    spiderX: nil,
                    alpn: alpn,
                    encryption: encryption,
                    xhttpHost: host ?? serverName,
                    xhttpPath: path,
                    xhttpMode: mode,
                    behaviorProfile: behaviorProfile,
                    uplinkHTTPMethod: uplinkHTTPMethod,
                    xhttpAdvancedSettings: advancedXHTTPSettings
                ),
                sourceID: sourceID,
                displayName: displayName,
                address: address,
                port: port,
                uuid: uuid,
                flow: flow,
                securityKind: .tls,
                tlsSettings: TLSSecuritySettings(
                    serverName: serverName,
                    fingerprint: emptyToNil(queryValue("fp", in: queryItems) ?? extra?.fingerprint),
                    alpn: alpn,
                    pinnedPeerCertSha256: emptyToNil(queryValue("pinnedPeerCertSha256", in: queryItems) ?? extra?.pinnedPeerCertSha256),
                    verifyPeerCertByName: emptyToNil(queryValue("verifyPeerCertByName", in: queryItems) ?? extra?.verifyPeerCertByName),
                    allowInsecure: allowInsecure
                ),
                encryption: encryption,
                xhttpHost: host ?? serverName,
                xhttpPath: path,
                xhttpMode: mode,
                behaviorProfile: behaviorProfile,
                uplinkHTTPMethod: uplinkHTTPMethod,
                xhttpAdvancedSettings: advancedXHTTPSettings,
                tags: [],
                metadata: metadata
            )
            return (try? RuntimeConfigBuilder.build(for: endpoint)) != nil ? endpoint : nil
        }
    }

    private static func queryValue(_ key: String, in items: [String: [URLQueryItem]]) -> String? {
        items[key]?.first?.value?.removingPercentEncoding ?? items[key]?.first?.value
    }

    private static func parseALPN(_ value: String?) -> [String]? {
        let items = value?
            .split(separator: ",")
            .map { String($0).trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        guard let items, !items.isEmpty else {
            return nil
        }
        return items
    }

    private static func parseBool(_ value: String?) -> Bool? {
        guard let value else {
            return nil
        }
        switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "1", "true", "yes":
            return true
        case "0", "false", "no":
            return false
        default:
            return nil
        }
    }

    private static func parseLinkExtra(_ value: String) -> ImportedLinkExtra? {
        guard let data = value.data(using: .utf8) else {
            return nil
        }
        return try? JSONDecoder().decode(ImportedLinkExtra.self, from: data)
    }

    private static func makeAdvancedXHTTPSettings(
        queryItems: [String: [URLQueryItem]],
        extra: ImportedLinkExtra?
    ) -> XHTTPAdvancedSettings? {
        XHTTPAdvancedSettings(
            sessionPlacement: queryValue("sessionPlacement", in: queryItems) ?? extra?.sessionPlacement,
            sessionKey: queryValue("sessionKey", in: queryItems) ?? extra?.sessionKey,
            seqPlacement: queryValue("seqPlacement", in: queryItems) ?? extra?.seqPlacement,
            seqKey: queryValue("seqKey", in: queryItems) ?? extra?.seqKey,
            xPaddingBytes: queryValue("xPaddingBytes", in: queryItems) ?? extra?.xPaddingBytes?.value,
            xPaddingMethod: queryValue("xPaddingMethod", in: queryItems) ?? extra?.xPaddingMethod,
            xPaddingPlacement: queryValue("xPaddingPlacement", in: queryItems) ?? extra?.xPaddingPlacement,
            xPaddingKey: queryValue("xPaddingKey", in: queryItems) ?? extra?.xPaddingKey,
            xPaddingObfsMode: parseBool(queryValue("xPaddingObfsMode", in: queryItems)) ?? extra?.xPaddingObfsMode,
            noGRPCHeader: parseBool(queryValue("noGRPCHeader", in: queryItems)) ?? extra?.noGRPCHeader,
            noSSEHeader: parseBool(queryValue("noSSEHeader", in: queryItems)) ?? extra?.noSSEHeader,
            scMaxEachPostBytes: queryValue("scMaxEachPostBytes", in: queryItems) ?? extra?.scMaxEachPostBytes?.value
        ).normalized
    }

    private static func parseConfig(sourceID: UUID, config: ImportedConfig) -> [SubscriptionEndpoint] {
        config.outbounds.compactMap { outbound -> SubscriptionEndpoint? in
            guard outbound.protocolName == "vless" else {
                return nil
            }
            guard outbound.streamSettings?.network == "xhttp" else {
                return nil
            }
            guard
                let vnext = outbound.settings?.vnext.first,
                let user = vnext.users.first,
                !vnext.address.isEmpty,
                !user.id.isEmpty
            else {
                return nil
            }

            let securityKind = ProfileSecurityKind(rawValue: outbound.streamSettings?.security ?? "")
            let xhttpSettings = outbound.streamSettings?.xhttpSettings
            let mode = XHTTPMode(rawValue: xhttpSettings?.mode ?? "") ?? .auto
            let behaviorProfile = BehaviorProfile(rawValue: xhttpSettings?.behaviorProfile ?? "") ?? .balanced
            let method = normalizeHTTPMethod(xhttpSettings?.uplinkHTTPMethod)
            let displayName = outbound.tag ?? user.email ?? "\(vnext.address):\(vnext.port)"
            let advancedXHTTPSettings = xhttpSettings?.advancedSettings

            let candidate: SubscriptionEndpoint?
            switch securityKind {
            case .reality:
                guard
                    let realitySettings = outbound.streamSettings?.realitySettings,
                    !realitySettings.serverName.isEmpty,
                    !realitySettings.fingerprint.isEmpty,
                    !realitySettings.publicKey.isEmpty
                else {
                    return nil
                }

                let host = xhttpSettings?.host?.first ?? realitySettings.serverName
                candidate = SubscriptionEndpoint(
                    id: SubscriptionEndpoint.stableID(
                        sourceID: sourceID,
                        displayName: displayName,
                        address: vnext.address,
                        port: vnext.port,
                        uuid: user.id,
                        flow: user.flow,
                        securityKind: .reality,
                        serverName: realitySettings.serverName,
                        fingerprint: realitySettings.fingerprint,
                        publicKey: realitySettings.publicKey,
                        shortId: realitySettings.shortID,
                        spiderX: realitySettings.spiderX,
                        alpn: [],
                        encryption: user.encryption ?? "none",
                        xhttpHost: host,
                        xhttpPath: xhttpSettings?.path ?? "/",
                        xhttpMode: mode,
                        behaviorProfile: behaviorProfile,
                        uplinkHTTPMethod: method,
                        xhttpAdvancedSettings: advancedXHTTPSettings
                    ),
                    sourceID: sourceID,
                    displayName: displayName,
                    address: vnext.address,
                    port: vnext.port,
                    uuid: user.id,
                    flow: user.flow,
                    securityKind: .reality,
                    realitySettings: RealitySecuritySettings(
                        serverName: realitySettings.serverName,
                        fingerprint: realitySettings.fingerprint,
                        publicKey: realitySettings.publicKey,
                        shortId: realitySettings.shortID,
                        spiderX: realitySettings.spiderX
                    ),
                    encryption: user.encryption ?? "none",
                    xhttpHost: host,
                    xhttpPath: xhttpSettings?.path ?? "/",
                    xhttpMode: mode,
                    behaviorProfile: behaviorProfile,
                    uplinkHTTPMethod: method,
                    xhttpAdvancedSettings: advancedXHTTPSettings,
                    tags: outbound.tag.map { [$0] } ?? [],
                    metadata: [
                        "network": outbound.streamSettings?.network ?? "",
                        "security": outbound.streamSettings?.security ?? "",
                    ]
                )
            case .tls:
                guard
                    let tlsSettings = outbound.streamSettings?.tlsSettings,
                    !tlsSettings.serverName.isEmpty
                else {
                    return nil
                }

                let host = xhttpSettings?.host?.first ?? tlsSettings.serverName
                var metadata = [
                    "network": outbound.streamSettings?.network ?? "",
                    "security": outbound.streamSettings?.security ?? "",
                ]
                if tlsSettings.allowInsecure == true {
                    metadata["tls_allow_insecure"] = "true"
                }

                candidate = SubscriptionEndpoint(
                    id: SubscriptionEndpoint.stableID(
                        sourceID: sourceID,
                        displayName: displayName,
                        address: vnext.address,
                        port: vnext.port,
                        uuid: user.id,
                        flow: user.flow,
                        securityKind: .tls,
                        serverName: tlsSettings.serverName,
                        fingerprint: tlsSettings.fingerprint ?? "",
                        publicKey: "",
                        shortId: nil,
                        spiderX: nil,
                        alpn: tlsSettings.alpn?.values ?? [],
                        encryption: user.encryption ?? "none",
                        xhttpHost: host,
                        xhttpPath: xhttpSettings?.path ?? "/",
                        xhttpMode: mode,
                        behaviorProfile: behaviorProfile,
                        uplinkHTTPMethod: method,
                        xhttpAdvancedSettings: advancedXHTTPSettings
                    ),
                    sourceID: sourceID,
                    displayName: displayName,
                    address: vnext.address,
                    port: vnext.port,
                    uuid: user.id,
                    flow: user.flow,
                    securityKind: .tls,
                    tlsSettings: TLSSecuritySettings(
                        serverName: tlsSettings.serverName,
                        fingerprint: emptyToNil(tlsSettings.fingerprint),
                        alpn: tlsSettings.alpn?.values ?? [],
                        pinnedPeerCertSha256: emptyToNil(tlsSettings.pinnedPeerCertSha256),
                        verifyPeerCertByName: emptyToNil(tlsSettings.verifyPeerCertByName),
                        allowInsecure: tlsSettings.allowInsecure
                    ),
                    encryption: user.encryption ?? "none",
                    xhttpHost: host,
                    xhttpPath: xhttpSettings?.path ?? "/",
                    xhttpMode: mode,
                    behaviorProfile: behaviorProfile,
                    uplinkHTTPMethod: method,
                    xhttpAdvancedSettings: advancedXHTTPSettings,
                    tags: outbound.tag.map { [$0] } ?? [],
                    metadata: metadata
                )
            case nil:
                return nil
            }

            guard let candidate, (try? RuntimeConfigBuilder.build(for: candidate)) != nil else {
                return nil
            }
            return candidate
        }
    }

    private static func normalizeHTTPMethod(_ value: String?) -> String {
        let normalized = value?.trimmingCharacters(in: .whitespacesAndNewlines).uppercased() ?? ""
        return normalized.isEmpty ? ManualUplinkHTTPMethod.post.rawValue : normalized
    }

    private static func parseFlexibleLinkArrays(from data: Data) -> [String]? {
        guard let object = try? JSONSerialization.jsonObject(with: data) else {
            return nil
        }

        if let dictionary = object as? [String: Any] {
            let links = dictionary.values.flatMap { extractLinks(from: $0) }
            return links.isEmpty ? nil : links
        }

        if let array = object as? [Any] {
            let links = array.flatMap(extractLinks(from:))
            return links.isEmpty ? nil : links
        }

        return nil
    }

    private static func extractLinks(from value: Any) -> [String] {
        if let string = value as? String, string.lowercased().hasPrefix("vless://") {
            return [string]
        }
        if let array = value as? [String] {
            return array.filter { $0.lowercased().hasPrefix("vless://") }
        }
        if let nested = value as? [Any] {
            return nested.flatMap(extractLinks(from:))
        }
        if let nested = value as? [String: Any] {
            return nested.values.flatMap(extractLinks(from:))
        }
        return []
    }
}

private struct ImportedLinksEnvelope: Decodable {
    let links: [String]
}

private struct FlexibleString: Decodable {
    let value: String

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let stringValue = try? container.decode(String.self) {
            value = stringValue
        } else if let intValue = try? container.decode(Int.self) {
            value = String(intValue)
        } else if let doubleValue = try? container.decode(Double.self) {
            value = String(Int(doubleValue))
        } else {
            throw DecodingError.typeMismatch(
                String.self,
                DecodingError.Context(codingPath: decoder.codingPath, debugDescription: "Expected string/int")
            )
        }
    }
}

private struct ImportedLinkExtra: Decodable {
    let displayName: String?
    let network: String?
    let host: String?
    let path: String?
    let security: String?
    let serverName: String?
    let fingerprint: String?
    let publicKey: String?
    let shortId: String?
    let spiderX: String?
    let flow: String?
    let encryption: String?
    let alpn: [String]?
    let allowInsecure: Bool?
    let verifyPeerCertByName: String?
    let pinnedPeerCertSha256: String?
    let mode: String?
    let behaviorProfile: String?
    let uplinkHTTPMethod: String?
    let sessionPlacement: String?
    let sessionKey: String?
    let seqPlacement: String?
    let seqKey: String?
    let xPaddingBytes: FlexibleString?
    let xPaddingMethod: String?
    let xPaddingPlacement: String?
    let xPaddingKey: String?
    let xPaddingObfsMode: Bool?
    let noGRPCHeader: Bool?
    let noSSEHeader: Bool?
    let scMaxEachPostBytes: FlexibleString?

    private enum CodingKeys: String, CodingKey {
        case displayName = "ps"
        case network = "net"
        case host
        case path
        case security = "tls"
        case serverName = "sni"
        case fingerprint = "fp"
        case publicKey = "pbk"
        case shortId = "sid"
        case spiderX = "spx"
        case flow
        case encryption
        case alpn
        case allowInsecure
        case verifyPeerCertByName
        case pinnedPeerCertSha256
        case mode
        case behaviorProfile
        case uplinkHTTPMethod
        case sessionPlacement
        case sessionKey
        case seqPlacement
        case seqKey
        case xPaddingBytes
        case xPaddingMethod
        case xPaddingPlacement
        case xPaddingKey
        case xPaddingObfsMode
        case noGRPCHeader
        case noSSEHeader
        case scMaxEachPostBytes
    }
}

private func emptyToNil(_ value: String?) -> String? {
    guard let value else {
        return nil
    }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
}

private struct ImportedConfig: Decodable {
    let outbounds: [ImportedOutbound]

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        outbounds = try container.decodeIfPresent([ImportedOutbound].self, forKey: .outbounds) ?? []
    }

    private enum CodingKeys: String, CodingKey {
        case outbounds
    }
}

private struct ImportedOutbound: Decodable {
    let tag: String?
    let protocolName: String
    let settings: ImportedOutboundSettings?
    let streamSettings: ImportedStreamSettings?

    private enum CodingKeys: String, CodingKey {
        case tag
        case protocolName = "protocol"
        case settings
        case streamSettings
    }
}

private struct ImportedOutboundSettings: Decodable {
    let vnext: [ImportedVNext]
}

private struct ImportedVNext: Decodable {
    let address: String
    let port: Int
    let users: [ImportedUser]
}

private struct ImportedUser: Decodable {
    let id: String
    let flow: String?
    let encryption: String?
    let email: String?
}

private struct ImportedStreamSettings: Decodable {
    let network: String?
    let security: String?
    let realitySettings: ImportedRealitySettings?
    let tlsSettings: ImportedTLSSettings?
    let xhttpSettings: ImportedXHTTPSettings?
}

private struct ImportedRealitySettings: Decodable {
    let serverName: String
    let fingerprint: String
    let publicKey: String
    let shortID: String?
    let spiderX: String?
}

private struct ImportedTLSSettings: Decodable {
    let serverName: String
    let alpn: OneOrManyString?
    let fingerprint: String?
    let pinnedPeerCertSha256: String?
    let verifyPeerCertByName: String?
    let allowInsecure: Bool?
}

private struct ImportedXHTTPSettings: Decodable {
    let host: OneOrManyString?
    let path: String?
    let mode: String?
    let behaviorProfile: String?
    let uplinkHTTPMethod: String?
    let sessionPlacement: String?
    let sessionKey: String?
    let seqPlacement: String?
    let seqKey: String?
    let xPaddingBytes: FlexibleString?
    let xPaddingMethod: String?
    let xPaddingPlacement: String?
    let xPaddingKey: String?
    let xPaddingObfsMode: Bool?
    let noGRPCHeader: Bool?
    let noSSEHeader: Bool?
    let scMaxEachPostBytes: FlexibleString?

    var advancedSettings: XHTTPAdvancedSettings? {
        XHTTPAdvancedSettings(
            sessionPlacement: sessionPlacement,
            sessionKey: sessionKey,
            seqPlacement: seqPlacement,
            seqKey: seqKey,
            xPaddingBytes: xPaddingBytes?.value,
            xPaddingMethod: xPaddingMethod,
            xPaddingPlacement: xPaddingPlacement,
            xPaddingKey: xPaddingKey,
            xPaddingObfsMode: xPaddingObfsMode,
            noGRPCHeader: noGRPCHeader,
            noSSEHeader: noSSEHeader,
            scMaxEachPostBytes: scMaxEachPostBytes?.value
        ).normalized
    }
}

private struct OneOrManyString: Decodable {
    let values: [String]

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let one = try? container.decode(String.self) {
            values = [one]
        } else {
            values = try container.decode([String].self)
        }
    }

    var first: String? {
        values.first
    }
}
