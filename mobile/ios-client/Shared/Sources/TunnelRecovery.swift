import Foundation

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
