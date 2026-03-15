import Foundation
import XrayAppCore

enum TunnelRuntimePhase: String, Codable, Hashable, Sendable {
    case idle
    case preparing
    case starting
    case recovering
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
        case .recovering:
            return "Recovering"
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

struct TunnelRuntimeState: Codable, Hashable, Sendable {
    var sessionID: UUID?
    var activeTunnelTarget: ProfileReference?
    var targetName: String?
    var phase: TunnelRuntimePhase
    var runtimeStage: TunnelRuntimeStage?
    var createdAt: Date
    var startedAt: Date?
    var updatedAt: Date
    var lastError: String?
    var configHash: String?
    var performance: TunnelPerformanceTimings?
    var stopReason: TunnelStopReason?
    var stopOrigin: TunnelStopOrigin?
    var lastKnownSystemStatus: TunnelSystemStatus?
    var recoveryAttempt: Int?
    var lastRecoveryTrigger: TunnelLocalFailureReason?
    var lastHealthyAt: Date?

    init(
        sessionID: UUID? = nil,
        activeTunnelTarget: ProfileReference? = nil,
        targetName: String? = nil,
        phase: TunnelRuntimePhase,
        runtimeStage: TunnelRuntimeStage? = nil,
        createdAt: Date = Date(),
        startedAt: Date? = nil,
        updatedAt: Date = Date(),
        lastError: String? = nil,
        configHash: String? = nil,
        performance: TunnelPerformanceTimings? = nil,
        stopReason: TunnelStopReason? = nil,
        stopOrigin: TunnelStopOrigin? = nil,
        lastKnownSystemStatus: TunnelSystemStatus? = nil,
        recoveryAttempt: Int? = nil,
        lastRecoveryTrigger: TunnelLocalFailureReason? = nil,
        lastHealthyAt: Date? = nil
    ) {
        self.sessionID = sessionID
        self.activeTunnelTarget = activeTunnelTarget
        self.targetName = targetName
        self.phase = phase
        self.runtimeStage = runtimeStage
        self.createdAt = createdAt
        self.startedAt = startedAt
        self.updatedAt = updatedAt
        self.lastError = lastError
        self.configHash = configHash
        self.performance = performance
        self.stopReason = stopReason
        self.stopOrigin = stopOrigin
        self.lastKnownSystemStatus = lastKnownSystemStatus
        self.recoveryAttempt = recoveryAttempt
        self.lastRecoveryTrigger = lastRecoveryTrigger
        self.lastHealthyAt = lastHealthyAt
    }

    var isCleanStop: Bool {
        phase == .idle && stopReason != nil
    }

    var isExternalStop: Bool {
        isCleanStop && stopOrigin == .system
    }
}

final class TunnelSessionStore {
    private let appGroupStore: AppGroupStore
    private let runtimeStateFileURL: URL

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
        runtimeStateFileURL = appGroupStore.fileURL(named: AppConfiguration.tunnelRuntimeStateFileName)
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
