import Foundation
import XCTest
@testable import XrayAppCore
@testable import XrayClientShared

final class TunnelSessionStoreTests: XCTestCase {
    func testLaunchPayloadRoundTripsForMatchingSession() throws {
        let harness = TunnelSessionHarness()
        let payload = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!,
            activeTunnelTarget: .manual(UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!),
            configJSON: #"{"outbounds":[]}"#,
            targetName: "Primary"
        )

        try harness.store.saveLaunchPayload(payload)

        XCTAssertEqual(try harness.store.loadLaunchPayload(expectedSessionID: payload.sessionID), payload)
    }

    func testLaunchPayloadRejectsSessionMismatch() throws {
        let harness = TunnelSessionHarness()
        let payload = TunnelLaunchPayload(
            activeTunnelTarget: .manual(UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!),
            configJSON: #"{"outbounds":[]}"#,
            targetName: "Primary"
        )

        try harness.store.saveLaunchPayload(payload)

        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: UUID(uuidString: "cccccccc-cccc-cccc-cccc-cccccccccccc")!)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .launchPayloadSessionMismatch)
        }
    }

    func testLaunchPayloadRejectsStalePayloadAndClearsIt() throws {
        let harness = TunnelSessionHarness()
        let payload = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "dddddddd-dddd-dddd-dddd-dddddddddddd")!,
            activeTunnelTarget: .manual(UUID(uuidString: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")!),
            configJSON: #"{"outbounds":[]}"#,
            createdAt: Date(timeIntervalSinceNow: -(AppConfiguration.tunnelLaunchPayloadMaxAge + 5)),
            targetName: "Primary"
        )

        try harness.store.saveLaunchPayload(payload)

        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: payload.sessionID)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .staleLaunchPayload)
        }
        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: payload.sessionID)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .missingLaunchPayload)
        }
    }

    func testLaunchPayloadRejectsCorruptedPayloadAndClearsIt() throws {
        let harness = TunnelSessionHarness()
        var payload = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "ffffffff-ffff-ffff-ffff-ffffffffffff")!,
            activeTunnelTarget: .manual(UUID(uuidString: "11111111-1111-1111-1111-111111111111")!),
            configJSON: #"{"outbounds":[]}"#,
            targetName: "Primary"
        )
        payload.configJSON = #"{"outbounds":[{"protocol":"vless"}]}"#

        try harness.store.saveLaunchPayload(payload)

        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: payload.sessionID)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .corruptedLaunchPayload)
        }
        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: payload.sessionID)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .missingLaunchPayload)
        }
    }

    func testClearingOneLaunchPayloadDoesNotRemoveAnotherSession() throws {
        let harness = TunnelSessionHarness()
        let first = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "aaaaaaaa-1111-1111-1111-aaaaaaaaaaaa")!,
            activeTunnelTarget: .manual(UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!),
            configJSON: #"{"outbounds":[{"tag":"first"}]}"#,
            targetName: "First"
        )
        let second = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "cccccccc-2222-2222-2222-cccccccccccc")!,
            activeTunnelTarget: .manual(UUID(uuidString: "dddddddd-dddd-dddd-dddd-dddddddddddd")!),
            configJSON: #"{"outbounds":[{"tag":"second"}]}"#,
            targetName: "Second"
        )

        try harness.store.saveLaunchPayload(first)
        try harness.store.saveLaunchPayload(second)
        harness.store.clearLaunchPayload(sessionID: first.sessionID)

        XCTAssertEqual(try harness.store.loadLaunchPayload(expectedSessionID: second.sessionID), second)
        XCTAssertThrowsError(
            try harness.store.loadLaunchPayload(expectedSessionID: first.sessionID)
        ) { error in
            XCTAssertEqual(error as? TunnelSessionStoreError, .launchPayloadSessionMismatch)
        }
    }

    func testLoadMostRecentLaunchPayloadReturnsNewestValidPayload() throws {
        let harness = TunnelSessionHarness()
        let older = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "aaaaaaaa-3333-3333-3333-aaaaaaaaaaaa")!,
            activeTunnelTarget: .manual(UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!),
            configJSON: #"{"outbounds":[{"tag":"older"}]}"#,
            createdAt: Date(timeIntervalSinceNow: -5),
            targetName: "Older"
        )
        let newer = TunnelLaunchPayload(
            sessionID: UUID(uuidString: "cccccccc-4444-4444-4444-cccccccccccc")!,
            activeTunnelTarget: .manual(UUID(uuidString: "dddddddd-dddd-dddd-dddd-dddddddddddd")!),
            configJSON: #"{"outbounds":[{"tag":"newer"}]}"#,
            targetName: "Newer"
        )

        try harness.store.saveLaunchPayload(older)
        try harness.store.saveLaunchPayload(newer)

        XCTAssertEqual(try harness.store.loadMostRecentLaunchPayload(), newer)
    }

    func testRuntimeStateRoundTrips() throws {
        let harness = TunnelSessionHarness()
        let state = TunnelRuntimeState(
            sessionID: UUID(uuidString: "22222222-2222-2222-2222-222222222222")!,
            activeTunnelTarget: .subscriptionEndpoint(UUID(uuidString: "33333333-3333-3333-3333-333333333333")!),
            targetName: "Imported",
            phase: .starting,
            lastError: "provider failed"
        )

        try harness.store.saveRuntimeState(state)

        XCTAssertEqual(try harness.store.loadRuntimeState(), state)

        harness.store.clearRuntimeState()

        XCTAssertNil(try harness.store.loadRuntimeState())
    }
}

private struct TunnelSessionHarness {
    let store: TunnelSessionStore

    init() {
        let appGroupStore = AppGroupStore(appGroupIdentifier: "tests.internet.\(UUID().uuidString.lowercased())")
        store = TunnelSessionStore(appGroupStore: appGroupStore)
    }
}
