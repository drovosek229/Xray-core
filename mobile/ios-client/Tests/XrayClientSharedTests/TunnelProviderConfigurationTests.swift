import Foundation
import XCTest
@testable import XrayClientShared

final class TunnelProviderConfigurationTests: XCTestCase {
    func testEnvelopeHashRoundTrips() throws {
        let envelope = TunnelProviderConfigurationEnvelope(
            sessionID: UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!,
            activeTunnelTarget: .manual(UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!),
            targetName: "direct-put",
            runtimeConfigJSON: #"{"inbounds":[],"outbounds":[]}"#,
            routePolicy: .disabled
        )

        let data = try JSONEncoder().encode(envelope)
        let decoded = try JSONDecoder().decode(TunnelProviderConfigurationEnvelope.self, from: data)

        XCTAssertEqual(decoded, envelope)
        XCTAssertTrue(decoded.hasValidHash)
    }

    func testRoutePolicyCodableRoundTrips() throws {
        let include = TunnelRoutePolicy.include(["1.1.1.0/24", "2606:4700::/32"])
        let exclude = TunnelRoutePolicy.exclude(["10.0.0.0/8"])

        XCTAssertEqual(try roundTrip(include), include)
        XCTAssertEqual(try roundTrip(exclude), exclude)
        XCTAssertEqual(try roundTrip(.disabled), .disabled)
    }

    private func roundTrip(_ value: TunnelRoutePolicy) throws -> TunnelRoutePolicy {
        let data = try JSONEncoder().encode(value)
        return try JSONDecoder().decode(TunnelRoutePolicy.self, from: data)
    }
}
