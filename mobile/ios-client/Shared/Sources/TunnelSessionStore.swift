import CryptoKit
import Foundation
import XrayAppCore

enum TunnelRuntimePhase: String, Codable, Hashable, Sendable {
    case idle
    case preparing
    case starting
    case connected
    case stopping
    case failed

    var displayName: String {
        switch self {
        case .idle:
            return "Disconnected"
        case .preparing:
            return "Preparing"
        case .starting:
            return "Starting"
        case .connected:
            return "Connected"
        case .stopping:
            return "Stopping"
        case .failed:
            return "Failed"
        }
    }
}

enum TunnelSystemStatus: Int, Codable, Hashable, Sendable {
    case invalid = 0
    case disconnected = 1
    case connecting = 2
    case connected = 3
    case reasserting = 4
    case disconnecting = 5

    var isDisconnectedLike: Bool {
        self == .invalid || self == .disconnected
    }
}

enum TunnelStopOrigin: String, Codable, Hashable, Sendable {
    case app
    case system
    case provider
    case launchFailure
}

enum TunnelStopReason: Int, Codable, Hashable, Sendable {
    case unknown = -1
    case none = 0
    case userInitiated = 1
    case providerFailed = 2
    case noNetworkAvailable = 3
    case unrecoverableNetworkChange = 4
    case providerDisabled = 5
    case authenticationCanceled = 6
    case configurationFailed = 7
    case idleTimeout = 8
    case configurationDisabled = 9
    case configurationRemoved = 10
    case superceded = 11
    case userLogout = 12
    case userSwitch = 13
    case connectionFailed = 14
    case sleep = 15
    case appUpdate = 16
    case internalError = 17

    var fallbackErrorDescription: String? {
        switch self {
        case .providerFailed:
            return "The tunnel provider failed."
        case .authenticationCanceled:
            return "Tunnel authentication was canceled."
        case .configurationFailed:
            return "The tunnel provider could not be configured."
        case .connectionFailed:
            return "The tunnel connection failed."
        case .internalError:
            return "An internal Network Extension error occurred."
        default:
            return nil
        }
    }

    func classify(previousState: TunnelRuntimeState?) -> (phase: TunnelRuntimePhase, origin: TunnelStopOrigin) {
        switch self {
        case .userInitiated, .none:
            if previousState?.phase == .stopping || previousState?.stopOrigin == .app {
                return (.idle, .app)
            }
            return (.idle, .system)
        case .providerDisabled, .configurationDisabled, .configurationRemoved, .superceded,
             .sleep, .appUpdate, .userLogout, .userSwitch, .idleTimeout,
             .noNetworkAvailable, .unrecoverableNetworkChange:
            return (.idle, .system)
        case .providerFailed, .authenticationCanceled, .configurationFailed, .connectionFailed, .internalError:
            return (.failed, .provider)
        case .unknown:
            if previousState?.phase == .stopping {
                return (.idle, .app)
            }
            return (.failed, .provider)
        }
    }
}

struct TunnelLaunchPayload: Codable, Hashable, Sendable {
    var sessionID: UUID
    var activeTunnelTarget: ProfileReference
    var configJSON: String
    var createdAt: Date
    var targetName: String
    var configHash: String

    init(
        sessionID: UUID = UUID(),
        activeTunnelTarget: ProfileReference,
        configJSON: String,
        createdAt: Date = Date(),
        targetName: String
    ) {
        self.sessionID = sessionID
        self.activeTunnelTarget = activeTunnelTarget
        self.configJSON = configJSON
        self.createdAt = createdAt
        self.targetName = targetName
        self.configHash = Self.hash(for: configJSON)
    }

    var isExpired: Bool {
        Date().timeIntervalSince(createdAt) > AppConfiguration.tunnelLaunchPayloadMaxAge
    }

    var hasValidHash: Bool {
        configHash == Self.hash(for: configJSON)
    }

    static func hash(for configJSON: String) -> String {
        SHA256.hash(data: Data(configJSON.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

struct TunnelRuntimeState: Codable, Hashable, Sendable {
    var sessionID: UUID?
    var activeTunnelTarget: ProfileReference?
    var targetName: String?
    var phase: TunnelRuntimePhase
    var createdAt: Date
    var startedAt: Date?
    var updatedAt: Date
    var lastError: String?
    var configHash: String?
    var performance: TunnelPerformanceTimings?
    var stopReason: TunnelStopReason?
    var stopOrigin: TunnelStopOrigin?
    var lastKnownSystemStatus: TunnelSystemStatus?

    init(
        sessionID: UUID? = nil,
        activeTunnelTarget: ProfileReference? = nil,
        targetName: String? = nil,
        phase: TunnelRuntimePhase,
        createdAt: Date = Date(),
        startedAt: Date? = nil,
        updatedAt: Date = Date(),
        lastError: String? = nil,
        configHash: String? = nil,
        performance: TunnelPerformanceTimings? = nil,
        stopReason: TunnelStopReason? = nil,
        stopOrigin: TunnelStopOrigin? = nil,
        lastKnownSystemStatus: TunnelSystemStatus? = nil
    ) {
        self.sessionID = sessionID
        self.activeTunnelTarget = activeTunnelTarget
        self.targetName = targetName
        self.phase = phase
        self.createdAt = createdAt
        self.startedAt = startedAt
        self.updatedAt = updatedAt
        self.lastError = lastError
        self.configHash = configHash
        self.performance = performance
        self.stopReason = stopReason
        self.stopOrigin = stopOrigin
        self.lastKnownSystemStatus = lastKnownSystemStatus
    }

    var isCleanStop: Bool {
        phase == .idle && stopReason != nil
    }

    var isExternalStop: Bool {
        isCleanStop && stopOrigin == .system
    }
}

enum TunnelSessionStoreError: LocalizedError, Equatable {
    case missingLaunchPayload
    case staleLaunchPayload
    case launchPayloadSessionMismatch
    case corruptedLaunchPayload

    var errorDescription: String? {
        switch self {
        case .missingLaunchPayload:
            return "Missing tunnel launch payload."
        case .staleLaunchPayload:
            return "Tunnel launch payload expired."
        case .launchPayloadSessionMismatch:
            return "Tunnel launch payload session mismatch."
        case .corruptedLaunchPayload:
            return "Tunnel launch payload integrity check failed."
        }
    }
}

final class TunnelSessionStore {
    private let appGroupStore: AppGroupStore
    private let launchPayloadsFileURL: URL
    private let runtimeStateFileURL: URL

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
        launchPayloadsFileURL = appGroupStore.fileURL(named: AppConfiguration.pendingTunnelLaunchPayloadFileName)
        runtimeStateFileURL = appGroupStore.fileURL(named: AppConfiguration.tunnelRuntimeStateFileName)
    }

    func saveLaunchPayload(_ payload: TunnelLaunchPayload) throws {
        var payloads = loadLaunchPayloads()
        payloads[payload.sessionID.uuidString.lowercased()] = payload
        saveLaunchPayloads(payloads)
    }

    func loadLaunchPayload(expectedSessionID: UUID) throws -> TunnelLaunchPayload {
        var payloads = loadLaunchPayloads()
        let key = expectedSessionID.uuidString.lowercased()
        guard let payload = payloads[key] else {
            throw payloads.isEmpty ? TunnelSessionStoreError.missingLaunchPayload : TunnelSessionStoreError.launchPayloadSessionMismatch
        }
        guard !payload.isExpired else {
            payloads.removeValue(forKey: key)
            saveLaunchPayloads(payloads)
            throw TunnelSessionStoreError.staleLaunchPayload
        }
        guard payload.hasValidHash else {
            payloads.removeValue(forKey: key)
            saveLaunchPayloads(payloads)
            throw TunnelSessionStoreError.corruptedLaunchPayload
        }
        return payload
    }

    func loadMostRecentLaunchPayload() throws -> TunnelLaunchPayload? {
        var payloads = loadLaunchPayloads()
        guard !payloads.isEmpty else {
            return nil
        }

        var latestPayload: TunnelLaunchPayload?
        var needsSave = false
        for (key, payload) in payloads.sorted(by: { $0.value.createdAt < $1.value.createdAt }) {
            if payload.isExpired || !payload.hasValidHash {
                payloads.removeValue(forKey: key)
                needsSave = true
                continue
            }
            latestPayload = payload
        }

        if needsSave {
            saveLaunchPayloads(payloads)
        }

        return latestPayload
    }

    func clearLaunchPayload(sessionID: UUID? = nil) {
        guard let sessionID else {
            removeFile(at: launchPayloadsFileURL)
            appGroupStore.removeValue(forKey: AppConfiguration.pendingTunnelLaunchPayloadKey)
            return
        }

        var payloads = loadLaunchPayloads()
        payloads.removeValue(forKey: sessionID.uuidString.lowercased())
        saveLaunchPayloads(payloads)
    }

    func saveRuntimeState(_ state: TunnelRuntimeState) throws {
        try write(state, to: runtimeStateFileURL)
        appGroupStore.removeValue(forKey: AppConfiguration.tunnelRuntimeStateKey)
    }

    func updateRuntimeState(_ transform: (inout TunnelRuntimeState) -> Void) throws {
        guard var state = try loadRuntimeState() else {
            return
        }
        transform(&state)
        state.updatedAt = Date()
        try saveRuntimeState(state)
    }

    func loadRuntimeState() throws -> TunnelRuntimeState? {
        if let state: TunnelRuntimeState = try read(TunnelRuntimeState.self, from: runtimeStateFileURL) {
            return state
        }
        if let state = try appGroupStore.load(TunnelRuntimeState.self, forKey: AppConfiguration.tunnelRuntimeStateKey) {
            try? write(state, to: runtimeStateFileURL)
            appGroupStore.removeValue(forKey: AppConfiguration.tunnelRuntimeStateKey)
            return state
        }
        return nil
    }

    func clearRuntimeState() {
        removeFile(at: runtimeStateFileURL)
        appGroupStore.removeValue(forKey: AppConfiguration.tunnelRuntimeStateKey)
    }

    private func loadLaunchPayloads() -> [String: TunnelLaunchPayload] {
        if let payloads: [String: TunnelLaunchPayload] = try? read(
            [String: TunnelLaunchPayload].self,
            from: launchPayloadsFileURL
        ) {
            return payloads
        }

        do {
            if let payloads = try appGroupStore.load(
                [String: TunnelLaunchPayload].self,
                forKey: AppConfiguration.pendingTunnelLaunchPayloadKey
            ) {
                try? write(payloads, to: launchPayloadsFileURL)
                appGroupStore.removeValue(forKey: AppConfiguration.pendingTunnelLaunchPayloadKey)
                return payloads
            }
        } catch {
        }

        do {
            if let legacyPayload = try appGroupStore.load(
                TunnelLaunchPayload.self,
                forKey: AppConfiguration.pendingTunnelLaunchPayloadKey
            ) {
                let payloads = [legacyPayload.sessionID.uuidString.lowercased(): legacyPayload]
                saveLaunchPayloads(payloads)
                return payloads
            }
        } catch {
        }

        return [:]
    }

    private func saveLaunchPayloads(_ payloads: [String: TunnelLaunchPayload]) {
        if payloads.isEmpty {
            removeFile(at: launchPayloadsFileURL)
            appGroupStore.removeValue(forKey: AppConfiguration.pendingTunnelLaunchPayloadKey)
            return
        }
        try? write(payloads, to: launchPayloadsFileURL)
        appGroupStore.removeValue(forKey: AppConfiguration.pendingTunnelLaunchPayloadKey)
    }

    private func read<T: Decodable>(_ type: T.Type, from fileURL: URL) throws -> T? {
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            return nil
        }
        let data = try Data(contentsOf: fileURL)
        return try JSONDecoder().decode(type, from: data)
    }

    private func write<T: Encodable>(_ value: T, to fileURL: URL) throws {
        let data = try JSONEncoder().encode(value)
        try data.write(to: fileURL, options: .atomic)
    }

    private func removeFile(at fileURL: URL) {
        try? FileManager.default.removeItem(at: fileURL)
    }
}

struct TunnelPerformanceTimings: Codable, Hashable, Sendable {
    var configBuildMs: Int?
    var managerReconcileMs: Int?
    var setTunnelNetworkSettingsMs: Int?
    var configValidateMs: Int?
    var xrayEngineStartMs: Int?
    var tun2SocksStartMs: Int?
    var firstDNSAnswerMs: Int?
    var firstOutboundConnectMs: Int?
    var firstByteMs: Int?

    init(
        configBuildMs: Int? = nil,
        managerReconcileMs: Int? = nil,
        setTunnelNetworkSettingsMs: Int? = nil,
        configValidateMs: Int? = nil,
        xrayEngineStartMs: Int? = nil,
        tun2SocksStartMs: Int? = nil,
        firstDNSAnswerMs: Int? = nil,
        firstOutboundConnectMs: Int? = nil,
        firstByteMs: Int? = nil
    ) {
        self.configBuildMs = configBuildMs
        self.managerReconcileMs = managerReconcileMs
        self.setTunnelNetworkSettingsMs = setTunnelNetworkSettingsMs
        self.configValidateMs = configValidateMs
        self.xrayEngineStartMs = xrayEngineStartMs
        self.tun2SocksStartMs = tun2SocksStartMs
        self.firstDNSAnswerMs = firstDNSAnswerMs
        self.firstOutboundConnectMs = firstOutboundConnectMs
        self.firstByteMs = firstByteMs
    }

    mutating func merge(from other: TunnelPerformanceTimings) {
        if let otherValue = other.configBuildMs {
            configBuildMs = otherValue
        }
        if let otherValue = other.managerReconcileMs {
            managerReconcileMs = otherValue
        }
        if let otherValue = other.setTunnelNetworkSettingsMs {
            setTunnelNetworkSettingsMs = otherValue
        }
        if let otherValue = other.configValidateMs {
            configValidateMs = otherValue
        }
        if let otherValue = other.xrayEngineStartMs {
            xrayEngineStartMs = otherValue
        }
        if let otherValue = other.tun2SocksStartMs {
            tun2SocksStartMs = otherValue
        }
        if let otherValue = other.firstDNSAnswerMs {
            firstDNSAnswerMs = otherValue
        }
        if let otherValue = other.firstOutboundConnectMs {
            firstOutboundConnectMs = otherValue
        }
        if let otherValue = other.firstByteMs {
            firstByteMs = otherValue
        }
    }
}
