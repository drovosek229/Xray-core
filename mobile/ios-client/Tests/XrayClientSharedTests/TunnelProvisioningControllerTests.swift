import Foundation
import XCTest
@testable import XrayClientShared

final class TunnelProvisioningControllerTests: XCTestCase {
    func testNoManagerCreatesAndSavesOne() async throws {
        let client = FakeTunnelPreferencesClient()
        let controller = makeController(client: client)

        let result = try await controller.reconcile(policy: .ensurePresent)

        XCTAssertNotNil(result.record)
        XCTAssertEqual(client.savedRecords.count, 1)
        XCTAssertFalse(result.snapshot.hadExistingManager)
        XCTAssertTrue(result.snapshot.reprovisioned)
        XCTAssertTrue(result.snapshot.isHealthy)
        XCTAssertTrue(result.snapshot.managerAvailable)
    }

    func testStaleManagerIsRewrittenAndReloaded() async throws {
        let client = FakeTunnelPreferencesClient()
        client.records = [
            TunnelManagerRecord(
                identifier: "existing",
                localizedDescription: "old",
                isEnabled: false,
                providerBundleIdentifier: "com.example.internet.PacketTunnel",
                serverAddress: "old",
                appGroupIdentifier: "group.old",
                configurationVersion: 0,
                includeAllNetworks: false,
                excludeLocalNetworks: false,
                excludeCellularServices: false,
                excludeAPNs: false,
                excludeDeviceCommunication: false,
                disconnectOnSleep: true,
                systemStatus: .disconnected
            )
        ]
        let controller = makeController(client: client)

        let result = try await controller.reconcile(policy: .ensurePresent)
        let saved = try XCTUnwrap(client.savedRecords.last)

        XCTAssertEqual(saved.localizedDescription, "internet")
        XCTAssertEqual(saved.configurationVersion, 1)
        XCTAssertEqual(saved.appGroupIdentifier, "group.com.example.internet")
        XCTAssertTrue(saved.isEnabled)
        XCTAssertTrue(result.snapshot.hadExistingManager)
        XCTAssertTrue(result.snapshot.reprovisioned)
        XCTAssertTrue(result.snapshot.isHealthy)
    }

    func testMultipleMatchingManagersAreRemovedAndRecreated() async throws {
        let client = FakeTunnelPreferencesClient()
        client.records = [
            TunnelManagerRecord(
                identifier: "first",
                providerBundleIdentifier: "com.example.internet.PacketTunnel",
                systemStatus: .disconnected
            ),
            TunnelManagerRecord(
                identifier: "second",
                providerBundleIdentifier: "com.example.internet.PacketTunnel",
                systemStatus: .invalid
            ),
        ]
        let controller = makeController(client: client)

        let result = try await controller.reconcile(policy: .ensurePresent)

        XCTAssertEqual(client.removedRecordIDs.sorted(), ["first", "second"])
        XCTAssertEqual(client.savedRecords.count, 1)
        XCTAssertNotNil(result.record)
        XCTAssertTrue(result.snapshot.hadExistingManager)
        XCTAssertTrue(result.snapshot.reprovisioned)
        XCTAssertTrue(result.snapshot.managerAvailable)
    }

    func testForegroundReconciliationReloadsSystemTruthEachTime() async throws {
        let client = FakeTunnelPreferencesClient()
        client.loadQueue = [
            [
                TunnelManagerRecord(
                    identifier: "existing",
                    localizedDescription: "internet",
                    isEnabled: true,
                    providerBundleIdentifier: "com.example.internet.PacketTunnel",
                    serverAddress: "internet",
                    appGroupIdentifier: "group.com.example.internet",
                    configurationVersion: 1,
                    includeAllNetworks: true,
                    excludeLocalNetworks: true,
                    excludeCellularServices: true,
                    excludeAPNs: true,
                    excludeDeviceCommunication: true,
                    disconnectOnSleep: false,
                    systemStatus: .disconnected
                ),
            ],
            [
                TunnelManagerRecord(
                    identifier: "existing",
                    localizedDescription: "internet",
                    isEnabled: true,
                    providerBundleIdentifier: "com.example.internet.PacketTunnel",
                    serverAddress: "internet",
                    appGroupIdentifier: "group.com.example.internet",
                    configurationVersion: 1,
                    includeAllNetworks: true,
                    excludeLocalNetworks: true,
                    excludeCellularServices: true,
                    excludeAPNs: true,
                    excludeDeviceCommunication: true,
                    disconnectOnSleep: false,
                    systemStatus: .connected,
                    connectedDate: Date(timeIntervalSince1970: 100)
                ),
            ],
        ]
        let controller = makeController(client: client)

        let first = try await controller.reconcile(policy: .ensurePresent)
        let second = try await controller.reconcile(policy: .ensurePresent)

        XCTAssertEqual(first.snapshot.systemStatus, .disconnected)
        XCTAssertEqual(second.snapshot.systemStatus, .connected)
        XCTAssertEqual(second.snapshot.connectedDate, Date(timeIntervalSince1970: 100))
        XCTAssertEqual(client.loadCallCount, 2)
    }
}

private func makeController(
    client: FakeTunnelPreferencesClient
) -> TunnelProvisioningController {
    TunnelProvisioningController(
        client: client,
        desiredConfiguration: DesiredTunnelConfiguration(
            providerBundleIdentifier: "com.example.internet.PacketTunnel",
            vpnDisplayName: "internet",
            appGroupIdentifier: "group.com.example.internet",
            configurationVersion: 1,
            includeAllNetworks: true,
            excludeLocalNetworks: true,
            excludeCellularServices: true,
            excludeAPNs: true,
            excludeDeviceCommunication: true,
            disconnectOnSleep: false
        )
    )
}

private final class FakeTunnelPreferencesClient: TunnelPreferencesClient {
    var records: [TunnelManagerRecord] = []
    var loadQueue: [[TunnelManagerRecord]] = []
    var savedRecords: [TunnelManagerRecord] = []
    var removedRecordIDs: [String] = []
    var loadCallCount = 0

    func loadAllRecords() async throws -> [TunnelManagerRecord] {
        loadCallCount += 1
        if !loadQueue.isEmpty {
            let next = loadQueue.removeFirst()
            records = next
            return next
        }
        return records
    }

    func makeRecord() -> TunnelManagerRecord {
        TunnelManagerRecord(identifier: "created-\(savedRecords.count)")
    }

    func saveRecord(_ record: TunnelManagerRecord) async throws -> TunnelManagerRecord {
        savedRecords.append(record)
        if let index = records.firstIndex(where: { $0.identifier == record.identifier }) {
            records[index] = record
        } else {
            records.append(record)
        }
        return record
    }

    func removeRecord(_ record: TunnelManagerRecord) async throws {
        removedRecordIDs.append(record.identifier)
        records.removeAll { $0.identifier == record.identifier }
    }

    func start(record: TunnelManagerRecord) async throws {}

    func stop(record: TunnelManagerRecord) async throws {}
}
