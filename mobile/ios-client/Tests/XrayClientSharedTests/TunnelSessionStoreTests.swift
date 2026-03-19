import Foundation
import XCTest
@testable import XrayAppCore
@testable import XrayClientShared

final class TunnelSessionStoreTests: XCTestCase {
    func testRuntimeStateRoundTrips() throws {
        let harness = TunnelSessionHarness()
        let state = TunnelRuntimeState(
            sessionID: UUID(uuidString: "22222222-2222-2222-2222-222222222222")!,
            activeTunnelTarget: .subscriptionEndpoint(UUID(uuidString: "33333333-3333-3333-3333-333333333333")!),
            targetName: "Imported",
            phase: .starting,
            runtimeStage: .startup,
            lastError: "provider failed",
            directEgressStatus: .bound(
                interfaceName: "en0",
                sourceAddress: "192.168.1.25",
                sourceAddressFamily: .ipv4
            )
        )

        try harness.store.saveRuntimeState(state)

        XCTAssertEqual(try harness.store.loadRuntimeState(), state)

        harness.store.clearRuntimeState()

        XCTAssertNil(try harness.store.loadRuntimeState())
    }

    func testRuntimeStateUpdatePersistsRecoveryMetadata() throws {
        let harness = TunnelSessionHarness()
        let sessionID = UUID(uuidString: "44444444-4444-4444-4444-444444444444")!
        try harness.store.saveRuntimeState(
            TunnelRuntimeState(
                sessionID: sessionID,
                activeTunnelTarget: .manual(UUID(uuidString: "55555555-5555-5555-5555-555555555555")!),
                targetName: "Primary",
                phase: .recovering,
                runtimeStage: .recovery,
                lastError: "Recovering local runtime.",
                lastKnownSystemStatus: .connected,
                recoveryAttempt: 1,
                lastRecoveryTrigger: .tun2SocksExited,
                lastHealthyAt: Date(timeIntervalSince1970: 100),
                directEgressStatus: .blocked(reason: "No usable physical interface")
            )
        )

        try harness.store.updateRuntimeState { state in
            guard state.sessionID == sessionID else {
                return
            }
            state.phase = .connected
            state.runtimeStage = .steadyState
            state.lastError = nil
            state.recoveryAttempt = 0
            state.lastRecoveryTrigger = .healthCheckFailed
            state.lastHealthyAt = Date(timeIntervalSince1970: 200)
            state.directEgressStatus = .bound(
                interfaceName: "pdp_ip0",
                sourceAddress: "10.0.0.20",
                sourceAddressFamily: .ipv4
            )
        }

        let updated = try XCTUnwrap(harness.store.loadRuntimeState())
        XCTAssertEqual(updated.phase, .connected)
        XCTAssertEqual(updated.runtimeStage, .steadyState)
        XCTAssertEqual(updated.recoveryAttempt, 0)
        XCTAssertEqual(updated.lastRecoveryTrigger, .healthCheckFailed)
        XCTAssertEqual(updated.lastHealthyAt, Date(timeIntervalSince1970: 200))
        XCTAssertEqual(
            updated.directEgressStatus,
            .bound(interfaceName: "pdp_ip0", sourceAddress: "10.0.0.20", sourceAddressFamily: .ipv4)
        )
    }
}

private struct TunnelSessionHarness {
    let store: TunnelSessionStore

    init() {
        let appGroupStore = makeTestAppGroupStore()
        store = TunnelSessionStore(appGroupStore: appGroupStore)
    }
}
