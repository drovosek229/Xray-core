import CryptoKit
import Foundation
import XrayAppCore

enum TunnelRoutePolicy: Hashable, Sendable {
    case disabled
    case include([String])
    case exclude([String])
}

extension TunnelRoutePolicy: Codable {
    private enum CodingKeys: String, CodingKey {
        case kind
        case values
    }

    private enum Kind: String, Codable {
        case disabled
        case include
        case exclude
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let kind = try container.decode(Kind.self, forKey: .kind)
        let values = try container.decodeIfPresent([String].self, forKey: .values) ?? []
        switch kind {
        case .disabled:
            self = .disabled
        case .include:
            self = .include(values)
        case .exclude:
            self = .exclude(values)
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .disabled:
            try container.encode(Kind.disabled, forKey: .kind)
        case let .include(values):
            try container.encode(Kind.include, forKey: .kind)
            try container.encode(values, forKey: .values)
        case let .exclude(values):
            try container.encode(Kind.exclude, forKey: .kind)
            try container.encode(values, forKey: .values)
        }
    }
}

struct TunnelProviderConfigurationEnvelope: Codable, Hashable, Sendable {
    var sessionID: UUID
    var activeTunnelTarget: ProfileReference
    var targetName: String
    var runtimeConfigJSON: String
    var createdAt: Date
    var configHash: String
    var routePolicy: TunnelRoutePolicy

    init(
        sessionID: UUID = UUID(),
        activeTunnelTarget: ProfileReference,
        targetName: String,
        runtimeConfigJSON: String,
        createdAt: Date = Date(),
        routePolicy: TunnelRoutePolicy = .disabled
    ) {
        self.sessionID = sessionID
        self.activeTunnelTarget = activeTunnelTarget
        self.targetName = targetName
        self.runtimeConfigJSON = runtimeConfigJSON
        self.createdAt = createdAt
        self.configHash = Self.hash(for: runtimeConfigJSON)
        self.routePolicy = routePolicy
    }

    var hasValidHash: Bool {
        configHash == Self.hash(for: runtimeConfigJSON)
    }

    static func hash(for configJSON: String) -> String {
        SHA256.hash(data: Data(configJSON.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}
