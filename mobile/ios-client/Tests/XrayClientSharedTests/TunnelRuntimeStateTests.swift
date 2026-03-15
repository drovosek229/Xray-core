import Foundation
import XCTest
@testable import XrayClientShared

final class TunnelRuntimeStateTests: XCTestCase {
    func testUserInitiatedStopAfterAppDisconnectIsClassifiedAsAppIdleStop() {
        let previousState = TunnelRuntimeState(
            phase: .stopping,
            stopOrigin: .app
        )

        let classification = TunnelStopReason.userInitiated.classify(previousState: previousState)

        XCTAssertEqual(classification.phase, .idle)
        XCTAssertEqual(classification.origin, .app)
    }

    func testConfigurationRemovedStopIsClassifiedAsCleanSystemStop() {
        let classification = TunnelStopReason.configurationRemoved.classify(previousState: nil)

        XCTAssertEqual(classification.phase, .idle)
        XCTAssertEqual(classification.origin, .system)
    }

    func testConnectionFailedStopIsClassifiedAsProviderFailure() {
        let classification = TunnelStopReason.connectionFailed.classify(previousState: nil)

        XCTAssertEqual(classification.phase, .failed)
        XCTAssertEqual(classification.origin, .provider)
        XCTAssertEqual(TunnelStopReason.connectionFailed.fallbackErrorDescription, "The tunnel connection failed.")
    }

    func testRuntimeStateReportsCleanExternalStop() {
        let state = TunnelRuntimeState(
            phase: .idle,
            stopReason: .configurationDisabled,
            stopOrigin: .system,
            lastKnownSystemStatus: .disconnected
        )

        XCTAssertTrue(state.isCleanStop)
        XCTAssertTrue(state.isExternalStop)
    }

    func testRecoveringPhaseHasRecoveringDisplayName() {
        XCTAssertEqual(TunnelRuntimePhase.recovering.displayName, "Recovering")
    }
}
