import Foundation

struct TunnelManagerRecord: Hashable, Sendable {
    var identifier: String
    var localizedDescription: String?
    var isEnabled: Bool
    var isOnDemandEnabled: Bool
    var hasOnDemandRules: Bool
    var providerBundleIdentifier: String?
    var serverAddress: String?
    var appGroupIdentifier: String?
    var configurationVersion: Int?
    var includeAllNetworks: Bool?
    var excludeLocalNetworks: Bool?
    var excludeCellularServices: Bool?
    var excludeAPNs: Bool?
    var excludeDeviceCommunication: Bool?
    var disconnectOnSleep: Bool?
    var runtimeConfigurationData: Data?
    var systemStatus: TunnelSystemStatus
    var connectedDate: Date?

    init(
        identifier: String,
        localizedDescription: String? = nil,
        isEnabled: Bool = false,
        isOnDemandEnabled: Bool = false,
        hasOnDemandRules: Bool = false,
        providerBundleIdentifier: String? = nil,
        serverAddress: String? = nil,
        appGroupIdentifier: String? = nil,
        configurationVersion: Int? = nil,
        includeAllNetworks: Bool? = nil,
        excludeLocalNetworks: Bool? = nil,
        excludeCellularServices: Bool? = nil,
        excludeAPNs: Bool? = nil,
        excludeDeviceCommunication: Bool? = nil,
        disconnectOnSleep: Bool? = nil,
        runtimeConfigurationData: Data? = nil,
        systemStatus: TunnelSystemStatus = .invalid,
        connectedDate: Date? = nil
    ) {
        self.identifier = identifier
        self.localizedDescription = localizedDescription
        self.isEnabled = isEnabled
        self.isOnDemandEnabled = isOnDemandEnabled
        self.hasOnDemandRules = hasOnDemandRules
        self.providerBundleIdentifier = providerBundleIdentifier
        self.serverAddress = serverAddress
        self.appGroupIdentifier = appGroupIdentifier
        self.configurationVersion = configurationVersion
        self.includeAllNetworks = includeAllNetworks
        self.excludeLocalNetworks = excludeLocalNetworks
        self.excludeCellularServices = excludeCellularServices
        self.excludeAPNs = excludeAPNs
        self.excludeDeviceCommunication = excludeDeviceCommunication
        self.disconnectOnSleep = disconnectOnSleep
        self.runtimeConfigurationData = runtimeConfigurationData
        self.systemStatus = systemStatus
        self.connectedDate = connectedDate
    }
}

struct DesiredTunnelConfiguration: Hashable, Sendable {
    var providerBundleIdentifier: String
    var vpnDisplayName: String
    var appGroupIdentifier: String
    var configurationVersion: Int
    var includeAllNetworks: Bool
    var excludeLocalNetworks: Bool
    var excludeCellularServices: Bool
    var excludeAPNs: Bool
    var excludeDeviceCommunication: Bool
    var disconnectOnSleep: Bool

    func applying(to record: TunnelManagerRecord) -> TunnelManagerRecord {
        var updated = record
        updated.localizedDescription = vpnDisplayName
        updated.isEnabled = true
        updated.isOnDemandEnabled = false
        updated.hasOnDemandRules = false
        updated.providerBundleIdentifier = providerBundleIdentifier
        updated.serverAddress = vpnDisplayName
        updated.appGroupIdentifier = appGroupIdentifier
        updated.configurationVersion = configurationVersion
        updated.includeAllNetworks = includeAllNetworks
        updated.excludeLocalNetworks = excludeLocalNetworks
        updated.excludeCellularServices = excludeCellularServices
        updated.excludeAPNs = excludeAPNs
        updated.excludeDeviceCommunication = excludeDeviceCommunication
        updated.disconnectOnSleep = disconnectOnSleep
        return updated
    }

    func matches(_ record: TunnelManagerRecord) -> Bool {
        record.localizedDescription == vpnDisplayName &&
            record.isEnabled &&
            record.isOnDemandEnabled == false &&
            record.hasOnDemandRules == false &&
            record.providerBundleIdentifier == providerBundleIdentifier &&
            record.serverAddress == vpnDisplayName &&
            record.appGroupIdentifier == appGroupIdentifier &&
            record.configurationVersion == configurationVersion &&
            record.includeAllNetworks == includeAllNetworks &&
            record.excludeLocalNetworks == excludeLocalNetworks &&
            record.excludeCellularServices == excludeCellularServices &&
            record.excludeAPNs == excludeAPNs &&
            record.excludeDeviceCommunication == excludeDeviceCommunication &&
            record.disconnectOnSleep == disconnectOnSleep
    }
}

struct TunnelManagerSnapshot: Hashable, Sendable {
    var systemStatus: TunnelSystemStatus
    var connectedDate: Date?
    var hadExistingManager: Bool
    var reprovisioned: Bool
    var isHealthy: Bool
    var managerAvailable: Bool
    var reconcileDurationMs: Int?
}

enum TunnelReconciliationPolicy: Sendable {
    case ensurePresent
    case existingOnly
}

protocol TunnelPreferencesClient: AnyObject {
    func loadAllRecords() async throws -> [TunnelManagerRecord]
    func makeRecord() -> TunnelManagerRecord
    func saveRecord(_ record: TunnelManagerRecord) async throws -> TunnelManagerRecord
    func removeRecord(_ record: TunnelManagerRecord) async throws
    func start(record: TunnelManagerRecord) async throws
    func stop(record: TunnelManagerRecord) async throws
}

final class TunnelProvisioningController {
    private let client: TunnelPreferencesClient
    private let desiredConfiguration: DesiredTunnelConfiguration

    init(
        client: TunnelPreferencesClient,
        desiredConfiguration: DesiredTunnelConfiguration
    ) {
        self.client = client
        self.desiredConfiguration = desiredConfiguration
    }

    func reconcile(
        policy: TunnelReconciliationPolicy,
        forceReprovision: Bool = false
    ) async throws -> (record: TunnelManagerRecord?, snapshot: TunnelManagerSnapshot) {
        let reconcileStartedAt = DispatchTime.now()
        let allRecords = try await client.loadAllRecords()
        var matchingRecords = allRecords.filter {
            $0.providerBundleIdentifier == desiredConfiguration.providerBundleIdentifier
        }
        let hadExistingManager = !matchingRecords.isEmpty
        var reprovisioned = false

        if matchingRecords.count > 1 || forceReprovision {
            for record in matchingRecords {
                try await client.removeRecord(record)
            }
            matchingRecords = []
            reprovisioned = reprovisioned || hadExistingManager
        }

        guard var record = matchingRecords.first else {
            guard policy == .ensurePresent else {
                return (
                    nil,
                    TunnelManagerSnapshot(
                        systemStatus: .invalid,
                    connectedDate: nil,
                    hadExistingManager: hadExistingManager,
                    reprovisioned: reprovisioned,
                    isHealthy: false,
                    managerAvailable: false,
                    reconcileDurationMs: elapsedMilliseconds(since: reconcileStartedAt)
                )
            )
        }

        let created = desiredConfiguration.applying(to: client.makeRecord())
            let saved = try await client.saveRecord(created)
            return (
                saved,
                TunnelManagerSnapshot(
                    systemStatus: saved.systemStatus,
                    connectedDate: saved.connectedDate,
                    hadExistingManager: hadExistingManager,
                    reprovisioned: true,
                    isHealthy: true,
                    managerAvailable: true,
                    reconcileDurationMs: elapsedMilliseconds(since: reconcileStartedAt)
                )
            )
        }

        let wasHealthy = desiredConfiguration.matches(record)
        if !wasHealthy {
            record = desiredConfiguration.applying(to: record)
            record = try await client.saveRecord(record)
            reprovisioned = true
        }

        return (
            record,
            TunnelManagerSnapshot(
                systemStatus: record.systemStatus,
                connectedDate: record.connectedDate,
                hadExistingManager: hadExistingManager,
                reprovisioned: reprovisioned,
                isHealthy: true,
                managerAvailable: true,
                reconcileDurationMs: elapsedMilliseconds(since: reconcileStartedAt)
            )
        )
    }
}

private func elapsedMilliseconds(since start: DispatchTime) -> Int {
    let elapsedNs = DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds
    return Int(elapsedNs / 1_000_000)
}
