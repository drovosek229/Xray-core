import Foundation
import XCTest
@testable import XrayAppCore

final class ProfileCompatibilityTests: XCTestCase {
    func testLegacyManualProfileDecodesAsRealityWithDefaultMethod() throws {
        let legacy = """
        {
          "id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
          "name": "Legacy Manual",
          "address": "example.com",
          "port": 443,
          "uuid": "11111111-1111-1111-1111-111111111111",
          "flow": "xtls-rprx-vision",
          "serverName": "cdn.example.com",
          "fingerprint": "chrome",
          "publicKey": "public-key",
          "shortId": "abcd1234",
          "spiderX": "/reality",
          "xhttpHost": "cdn.example.com",
          "xhttpPath": "/assets",
          "xhttpMode": "auto",
          "behaviorProfile": "balanced"
        }
        """

        let decoded = try JSONDecoder().decode(ManualProfile.self, from: Data(legacy.utf8))

        XCTAssertEqual(decoded.securityKind, .reality)
        XCTAssertEqual(decoded.realitySettings?.serverName, "cdn.example.com")
        XCTAssertEqual(decoded.realitySettings?.publicKey, "public-key")
        XCTAssertEqual(decoded.normalizedEncryption, "none")
        XCTAssertEqual(decoded.normalizedUplinkHTTPMethod, "POST")
    }

    func testLegacySubscriptionEndpointDecodesAsRealityWithDefaultMethod() throws {
        let legacy = """
        {
          "id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
          "sourceID": "cccccccc-cccc-cccc-cccc-cccccccccccc",
          "displayName": "Legacy Imported",
          "address": "edge.example.com",
          "port": 443,
          "uuid": "11111111-1111-1111-1111-111111111111",
          "serverName": "cdn.example.com",
          "fingerprint": "chrome",
          "publicKey": "public-key",
          "shortId": "abcd1234",
          "spiderX": "/reality",
          "xhttpHost": "cdn.example.com",
          "xhttpPath": "/assets",
          "xhttpMode": "auto",
          "behaviorProfile": "balanced",
          "tags": ["legacy"],
          "metadata": {"security": "reality"}
        }
        """

        let decoded = try JSONDecoder().decode(SubscriptionEndpoint.self, from: Data(legacy.utf8))

        XCTAssertEqual(decoded.securityKind, .reality)
        XCTAssertEqual(decoded.realitySettings?.serverName, "cdn.example.com")
        XCTAssertEqual(decoded.normalizedEncryption, "none")
        XCTAssertEqual(decoded.normalizedUplinkHTTPMethod, "POST")
    }

    func testLegacySubscriptionSourceDecodesLegacyFeedURLKey() throws {
        let legacy = """
        {
          "id": "dddddddd-dddd-dddd-dddd-dddddddddddd",
          "name": "Legacy Source",
          "feedURL": "https://example.com/subscription",
          "hwid": "device-hwid"
        }
        """

        let decoded = try JSONDecoder().decode(SubscriptionSource.self, from: Data(legacy.utf8))

        XCTAssertEqual(decoded.name, "Legacy Source")
        XCTAssertEqual(decoded.subscriptionURL.absoluteString, "https://example.com/subscription")
        XCTAssertEqual(decoded.hwid, "device-hwid")
    }

    func testLegacyTLSManualProfileDropsInvalidFlow() throws {
        let legacy = """
        {
          "id": "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
          "name": "Legacy TLS",
          "address": "tls.example.com",
          "port": 443,
          "uuid": "11111111-1111-1111-1111-111111111111",
          "flow": "xtls-rprx-vision",
          "securityKind": "tls",
          "serverName": "cdn.example.com",
          "fingerprint": "chrome",
          "xhttpHost": "cdn.example.com",
          "xhttpPath": "/assets"
        }
        """

        let decoded = try JSONDecoder().decode(ManualProfile.self, from: Data(legacy.utf8))

        XCTAssertEqual(decoded.securityKind, .tls)
        XCTAssertNil(decoded.flow)
    }
}
