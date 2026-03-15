import XCTest
@testable import XrayClientShared

final class TunnelRecoveryPolicyTests: XCTestCase {
    func testDefaultPolicyUsesThreeBoundedRecoveryAttempts() {
        let policy = TunnelRecoveryPolicy.default

        XCTAssertEqual(policy.maxRecoveryAttempts, 3)
        XCTAssertEqual(policy.backoff(forAttempt: 1), 0.25)
        XCTAssertEqual(policy.backoff(forAttempt: 2), 1)
        XCTAssertEqual(policy.backoff(forAttempt: 3), 3)
        XCTAssertNil(policy.backoff(forAttempt: 4))
    }
}
