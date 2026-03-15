import Foundation
@preconcurrency import NetworkExtension

@MainActor
final class TunnelManager {
    var onStatusChange: ((NEVPNStatus, Date?) -> Void)?

    private let preferencesClient: NetworkExtensionTunnelPreferencesClient
    private lazy var provisioner = TunnelProvisioningController(
        client: preferencesClient,
        desiredConfiguration: DesiredTunnelConfiguration(
            providerBundleIdentifier: AppConfiguration.packetTunnelBundleIdentifier,
            vpnDisplayName: AppConfiguration.vpnDisplayName,
            appGroupIdentifier: AppConfiguration.appGroupIdentifier,
            configurationVersion: AppConfiguration.tunnelConfigurationVersion,
            includeAllNetworks: true,
            excludeLocalNetworks: true,
            excludeCellularServices: true,
            excludeAPNs: true,
            excludeDeviceCommunication: true,
            disconnectOnSleep: false
        )
    )
    private var statusObserver: NSObjectProtocol?
    private var observedRecordIdentifier: String?

    init() {
        self.preferencesClient = NetworkExtensionTunnelPreferencesClient()
    }

    deinit {
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
        }
    }

    func loadOrCreateManager() async throws -> TunnelManagerSnapshot {
        try await reconcileForForeground()
    }

    func reconcileForForeground() async throws -> TunnelManagerSnapshot {
        try await reconcile(
            policy: .ensurePresent,
            forceReprovision: false,
            publishResult: true
        ).snapshot
    }

    func connect(
        providerConfiguration: TunnelProviderConfigurationEnvelope,
        forceReprovision: Bool = false
    ) async throws -> TunnelManagerSnapshot {
        var result = try await reconcile(
            policy: .ensurePresent,
            forceReprovision: forceReprovision,
            publishResult: false
        )

        guard var record = result.record else {
            throw NSError(
                domain: "internet",
                code: 500,
                userInfo: [NSLocalizedDescriptionKey: "No usable tunnel configuration is available."]
            )
        }

        let runtimeConfigurationData = try JSONEncoder().encode(providerConfiguration)
        if record.runtimeConfigurationData != runtimeConfigurationData {
            record.runtimeConfigurationData = runtimeConfigurationData
            record = try await preferencesClient.saveRecord(record)
            result.record = record
        }

        observeStatus(for: record)
        try await preferencesClient.start(record: record)
        publishStatus(systemStatus: .connecting, connectedDate: nil)
        return result.snapshot
    }

    func disconnect() async throws -> TunnelManagerSnapshot {
        let result = try await reconcile(
            policy: .existingOnly,
            forceReprovision: false,
            publishResult: false
        )

        guard let record = result.record else {
            publishStatus(systemStatus: .disconnected, connectedDate: nil)
            return result.snapshot
        }

        observeStatus(for: record)
        if result.snapshot.systemStatus.isDisconnectedLike {
            publishObservedStatus(for: record, fallback: result.snapshot)
            return result.snapshot
        }

        try await preferencesClient.stop(record: record)
        publishObservedStatus(for: record, fallback: result.snapshot)
        return result.snapshot
    }

    func isRecoverableConfigurationError(_ error: Error) -> Bool {
        guard let vpnErrorCode = normalizedVPNErrorCode(from: error) else {
            return false
        }

        switch vpnErrorCode {
        case .configurationInvalid,
             .configurationDisabled,
             .configurationStale,
             .configurationReadWriteFailed,
             .configurationUnknown:
            return true
        default:
            return false
        }
    }

    private func reconcile(
        policy: TunnelReconciliationPolicy,
        forceReprovision: Bool,
        publishResult: Bool
    ) async throws -> (record: TunnelManagerRecord?, snapshot: TunnelManagerSnapshot) {
        let result = try await provisioner.reconcile(
            policy: policy,
            forceReprovision: forceReprovision
        )

        if let record = result.record {
            observeStatus(for: record)
        } else {
            clearStatusObservation()
        }

        if publishResult {
            if let record = result.record {
                publishObservedStatus(for: record, fallback: result.snapshot)
            } else {
                publishStatus(
                    systemStatus: result.snapshot.systemStatus,
                    connectedDate: result.snapshot.connectedDate
                )
            }
        }

        return result
    }

    private func observeStatus(for record: TunnelManagerRecord) {
        guard let manager = preferencesClient.cachedManager(for: record.identifier) else {
            return
        }

        clearStatusObservation()
        observedRecordIdentifier = record.identifier
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: manager.connection,
            queue: .main
        ) { [weak self, weak manager] _ in
            guard let self else {
                return
            }
            Task { @MainActor in
                guard let manager else {
                    self.publishStatus(systemStatus: .disconnected, connectedDate: nil)
                    return
                }
                self.onStatusChange?(manager.connection.status, manager.connection.connectedDate)
            }
        }
    }

    private func clearStatusObservation() {
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
            self.statusObserver = nil
        }
        observedRecordIdentifier = nil
    }

    private func publishObservedStatus(
        for record: TunnelManagerRecord,
        fallback snapshot: TunnelManagerSnapshot
    ) {
        guard let manager = preferencesClient.cachedManager(for: record.identifier) else {
            publishStatus(
                systemStatus: snapshot.systemStatus,
                connectedDate: snapshot.connectedDate
            )
            return
        }

        onStatusChange?(manager.connection.status, manager.connection.connectedDate)
    }

    private func publishStatus(systemStatus: TunnelSystemStatus, connectedDate: Date?) {
        onStatusChange?(systemStatus.networkExtensionStatus, connectedDate)
    }

    private func normalizedVPNErrorCode(from error: Error) -> NEVPNError.Code? {
        if let vpnError = error as? NEVPNError {
            return vpnError.code
        }

        let nsError = error as NSError
        guard nsError.domain == NEVPNErrorDomain else {
            return nil
        }
        return NEVPNError.Code(rawValue: nsError.code)
    }
}

private final class NetworkExtensionTunnelPreferencesClient: TunnelPreferencesClient {
    private var managersByIdentifier: [String: NETunnelProviderManager] = [:]

    func loadAllRecords() async throws -> [TunnelManagerRecord] {
        let managers = try await Self.loadManagers()
        managersByIdentifier.removeAll(keepingCapacity: true)

        return managers.enumerated().map { index, manager in
            let identifier = Self.identifier(for: manager, fallbackIndex: index)
            managersByIdentifier[identifier] = manager
            return Self.record(identifier: identifier, from: manager)
        }
    }

    func makeRecord() -> TunnelManagerRecord {
        TunnelManagerRecord(identifier: UUID().uuidString.lowercased())
    }

    func saveRecord(_ record: TunnelManagerRecord) async throws -> TunnelManagerRecord {
        let manager = managersByIdentifier[record.identifier] ?? NETunnelProviderManager()
        apply(record, to: manager)
        try await Self.save(manager)
        try await Self.load(manager)
        managersByIdentifier[record.identifier] = manager
        return Self.record(identifier: record.identifier, from: manager)
    }

    func removeRecord(_ record: TunnelManagerRecord) async throws {
        guard let manager = managersByIdentifier.removeValue(forKey: record.identifier) else {
            return
        }
        try await Self.remove(manager)
    }

    func start(record: TunnelManagerRecord) async throws {
        let manager = try manager(for: record.identifier)
        try manager.connection.startVPNTunnel()
    }

    func stop(record: TunnelManagerRecord) async throws {
        let manager = try manager(for: record.identifier)
        manager.connection.stopVPNTunnel()
    }

    func cachedManager(for identifier: String) -> NETunnelProviderManager? {
        managersByIdentifier[identifier]
    }

    private func manager(for identifier: String) throws -> NETunnelProviderManager {
        guard let manager = managersByIdentifier[identifier] else {
            throw NSError(
                domain: "internet",
                code: 500,
                userInfo: [NSLocalizedDescriptionKey: "No tunnel manager is available for the requested record."]
            )
        }
        return manager
    }

    private func apply(_ record: TunnelManagerRecord, to manager: NETunnelProviderManager) {
        let proto = (manager.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = record.providerBundleIdentifier
        proto.serverAddress = record.serverAddress

        var providerConfiguration: [String: Any] = [:]
        if let appGroupIdentifier = record.appGroupIdentifier {
            providerConfiguration[AppConfiguration.tunnelProviderConfigurationAppGroupKey] = appGroupIdentifier
        }
        if let configurationVersion = record.configurationVersion {
            providerConfiguration[AppConfiguration.tunnelProviderConfigurationVersionKey] = configurationVersion
        }
        if let runtimeConfigurationData = record.runtimeConfigurationData {
            providerConfiguration[AppConfiguration.tunnelProviderConfigurationEnvelopeKey] = runtimeConfigurationData
        }
        proto.providerConfiguration = providerConfiguration

        proto.includeAllNetworks = record.includeAllNetworks ?? false
        proto.excludeLocalNetworks = record.excludeLocalNetworks ?? false
        proto.excludeCellularServices = record.excludeCellularServices ?? false
        proto.excludeAPNs = record.excludeAPNs ?? false
        proto.excludeDeviceCommunication = record.excludeDeviceCommunication ?? false
        proto.disconnectOnSleep = record.disconnectOnSleep ?? false

        manager.localizedDescription = record.localizedDescription
        manager.protocolConfiguration = proto
        manager.isEnabled = record.isEnabled
        manager.onDemandRules = record.hasOnDemandRules ? [NEOnDemandRuleConnect()] : []
        manager.isOnDemandEnabled = record.isOnDemandEnabled
    }

    private static func identifier(
        for manager: NETunnelProviderManager,
        fallbackIndex: Int
    ) -> String {
        let proto = manager.protocolConfiguration as? NETunnelProviderProtocol
        let bundleIdentifier = proto?.providerBundleIdentifier ?? "unknown"
        let serverAddress = proto?.serverAddress ?? manager.localizedDescription ?? ""
        return "\(bundleIdentifier)|\(serverAddress)|\(fallbackIndex)"
    }

    private static func record(
        identifier: String,
        from manager: NETunnelProviderManager
    ) -> TunnelManagerRecord {
        let proto = manager.protocolConfiguration as? NETunnelProviderProtocol
        let providerConfiguration = proto?.providerConfiguration ?? [:]

        return TunnelManagerRecord(
            identifier: identifier,
            localizedDescription: manager.localizedDescription,
            isEnabled: manager.isEnabled,
            isOnDemandEnabled: manager.isOnDemandEnabled,
            hasOnDemandRules: !(manager.onDemandRules?.isEmpty ?? true),
            providerBundleIdentifier: proto?.providerBundleIdentifier,
            serverAddress: proto?.serverAddress,
            appGroupIdentifier: providerConfiguration[AppConfiguration.tunnelProviderConfigurationAppGroupKey] as? String,
            configurationVersion: configurationVersion(from: providerConfiguration),
            includeAllNetworks: proto?.includeAllNetworks,
            excludeLocalNetworks: proto?.excludeLocalNetworks,
            excludeCellularServices: proto?.excludeCellularServices,
            excludeAPNs: proto?.excludeAPNs,
            excludeDeviceCommunication: proto?.excludeDeviceCommunication,
            disconnectOnSleep: proto?.disconnectOnSleep,
            runtimeConfigurationData: providerConfiguration[AppConfiguration.tunnelProviderConfigurationEnvelopeKey] as? Data,
            systemStatus: manager.connection.status.tunnelSystemStatus,
            connectedDate: manager.connection.connectedDate
        )
    }

    private static func configurationVersion(from providerConfiguration: [String: Any]) -> Int? {
        if let value = providerConfiguration[AppConfiguration.tunnelProviderConfigurationVersionKey] as? Int {
            return value
        }
        if let value = providerConfiguration[AppConfiguration.tunnelProviderConfigurationVersionKey] as? NSNumber {
            return value.intValue
        }
        if let value = providerConfiguration[AppConfiguration.tunnelProviderConfigurationVersionKey] as? String {
            return Int(value)
        }
        return nil
    }

    private static func loadManagers() async throws -> [NETunnelProviderManager] {
        try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: managers ?? [])
                }
            }
        }
    }

    private static func save(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private static func load(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.loadFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private static func remove(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.removeFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }
}

private extension NEVPNStatus {
    var tunnelSystemStatus: TunnelSystemStatus {
        TunnelSystemStatus(rawValue: rawValue) ?? .invalid
    }
}

private extension TunnelSystemStatus {
    var networkExtensionStatus: NEVPNStatus {
        NEVPNStatus(rawValue: rawValue) ?? .invalid
    }
}
