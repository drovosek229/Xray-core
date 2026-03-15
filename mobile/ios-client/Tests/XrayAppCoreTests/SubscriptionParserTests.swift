import Foundation
import XCTest
@testable import XrayAppCore

final class SubscriptionParserTests: XCTestCase {
    func testParserExtractsSupportedVLESSRealityAndTLSXHTTPEndpoints() throws {
        let sourceID = UUID()
        let payload = """
        {
          "outbounds": [
            {
              "tag": "reality-route",
              "protocol": "vless",
              "settings": {
                "vnext": [
                  {
                    "address": "edge.example.com",
                    "port": 443,
                    "users": [
                      {
                        "id": "11111111-1111-1111-1111-111111111111",
                        "flow": "xtls-rprx-vision-udp443",
                        "encryption": "mlkem768x25519plus.native.1rtt.keymaterial"
                      }
                    ]
                  }
                ]
              },
              "streamSettings": {
                "network": "xhttp",
                "security": "reality",
                "realitySettings": {
                  "serverName": "cdn.example.com",
                  "fingerprint": "chrome",
                  "publicKey": "public-key",
                  "shortID": "abcd1234",
                  "spiderX": "/reality"
                },
                "xhttpSettings": {
                  "host": ["cdn.example.com"],
                  "path": "/assets",
                  "mode": "auto",
                  "behaviorProfile": "balanced",
                  "uplinkHTTPMethod": "PATCH",
                  "xmux": {
                    "warmConnections": 2,
                    "hKeepAlivePeriod": 45,
                    "maxConnections": "2-4"
                  }
                }
              }
            },
            {
              "tag": "tls-route",
              "protocol": "vless",
              "settings": {
                "vnext": [
                  {
                    "address": "tls.example.com",
                    "port": 443,
                    "users": [
                      {
                        "id": "22222222-2222-2222-2222-222222222222"
                      }
                    ]
                  }
                ]
              },
              "streamSettings": {
                "network": "xhttp",
                "security": "tls",
                "tlsSettings": {
                  "serverName": "cdn.tls.example.com",
                  "fingerprint": "chrome",
                  "alpn": ["h2", "h3"],
                  "allowInsecure": true,
                  "verifyPeerCertByName": "cdn.tls.example.com"
                },
                "xhttpSettings": {
                  "path": "/proxy",
                  "mode": "packet-up",
                  "behaviorProfile": "balanced",
                  "uplinkHTTPMethod": "DELETE"
                }
              }
            },
            {
              "tag": "unsupported",
              "protocol": "trojan"
            }
          ]
        }
        """

        let endpoints = try SubscriptionParser.parse(sourceID: sourceID, data: Data(payload.utf8))

        XCTAssertEqual(endpoints.count, 2)

        let realityEndpoint = try XCTUnwrap(endpoints.first { $0.displayName == "reality-route" })
        XCTAssertEqual(realityEndpoint.sourceID, sourceID)
        XCTAssertEqual(realityEndpoint.securityKind, .reality)
        XCTAssertEqual(realityEndpoint.behaviorProfile, .balanced)
        XCTAssertEqual(realityEndpoint.xhttpHost, "cdn.example.com")
        XCTAssertEqual(realityEndpoint.normalizedUplinkHTTPMethod, "PATCH")
        XCTAssertEqual(realityEndpoint.flow, "xtls-rprx-vision-udp443")
        XCTAssertEqual(realityEndpoint.normalizedEncryption, "mlkem768x25519plus.native.1rtt.keymaterial")
        XCTAssertEqual(realityEndpoint.xhttpAdvancedSettings?.xmux?.warmConnections, 2)
        XCTAssertEqual(realityEndpoint.xhttpAdvancedSettings?.xmux?.hKeepAlivePeriod, 45)
        XCTAssertEqual(realityEndpoint.xhttpAdvancedSettings?.xmux?.maxConnections, "2-4")

        let tlsEndpoint = try XCTUnwrap(endpoints.first { $0.displayName == "tls-route" })
        XCTAssertEqual(tlsEndpoint.securityKind, .tls)
        XCTAssertEqual(tlsEndpoint.serverName, "cdn.tls.example.com")
        XCTAssertEqual(tlsEndpoint.normalizedUplinkHTTPMethod, "DELETE")
        XCTAssertEqual(tlsEndpoint.tlsSettings?.alpn, ["h2", "h3"])
        XCTAssertEqual(tlsEndpoint.normalizedEncryption, "none")
        XCTAssertEqual(tlsEndpoint.metadata["tls_allow_insecure"], "true")
        XCTAssertNil(tlsEndpoint.xhttpAdvancedSettings?.xmux)
    }

    func testParserParsesSampleFixtureFromRawLinksAndPreservesAdvancedXHTTPSettings() throws {
        let sourceID = UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!
        let payload = try fixture(named: "live_subscription_links")

        let endpoints = try SubscriptionParser.parse(sourceID: sourceID, data: payload)

        XCTAssertEqual(endpoints.map(\.displayName), ["compat-a", "compat-b", "fast-put"])

        let compatibilityProfile = try XCTUnwrap(endpoints.first { $0.displayName == "compat-a" })
        XCTAssertEqual(compatibilityProfile.securityKind, .tls)
        XCTAssertEqual(compatibilityProfile.xhttpMode, .packetUp)
        XCTAssertEqual(compatibilityProfile.normalizedUplinkHTTPMethod, "DELETE")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.sessionPlacement, "query")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.sessionKey, "sid")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.seqPlacement, "query")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.seqKey, "rid")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.xPaddingMethod, "tokenish")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.xPaddingPlacement, "query")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.xPaddingBytes, "1-8")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.xPaddingKey, "t")
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.xPaddingObfsMode, true)
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.noGRPCHeader, true)
        XCTAssertEqual(compatibilityProfile.xhttpAdvancedSettings?.scMaxEachPostBytes, "16384")

        let fastProfile = try XCTUnwrap(endpoints.first { $0.displayName == "fast-put" })
        XCTAssertEqual(fastProfile.xhttpMode, .streamUp)
        XCTAssertEqual(fastProfile.normalizedUplinkHTTPMethod, "PUT")
        XCTAssertEqual(fastProfile.classification, .recommendedFast)
        XCTAssertEqual(compatibilityProfile.classification, .stealthCompatibility)
    }

    func testParserParsesBase64AndURLSafeBase64SubscriptionBodies() throws {
        let payload = try fixture(named: "live_subscription_links")
        let base64 = Data(payload).base64EncodedData()
        let urlSafe = Data(String(decoding: base64, as: UTF8.self)
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .utf8)

        let base64Endpoints = try SubscriptionParser.parse(sourceID: UUID(), data: base64)
        let urlSafeEndpoints = try SubscriptionParser.parse(sourceID: UUID(), data: urlSafe)

        XCTAssertEqual(base64Endpoints.map(\.displayName), ["compat-a", "compat-b", "fast-put"])
        XCTAssertEqual(urlSafeEndpoints.map(\.displayName), ["compat-a", "compat-b", "fast-put"])
    }

    func testParserProducesStableIDsAcrossEquivalentImports() throws {
        let sourceID = UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!
        let payload = try fixture(named: "live_subscription_links")

        let first = try SubscriptionParser.parse(sourceID: sourceID, data: payload)
        let second = try SubscriptionParser.parse(sourceID: sourceID, data: payload)

        XCTAssertEqual(first.map(\.id), second.map(\.id))
    }

    func testParserRejectsUnsupportedPayload() {
        let payload = """
        {
          "outbounds": [
            {
              "protocol": "trojan"
            }
          ]
        }
        """

        XCTAssertThrowsError(
            try SubscriptionParser.parse(sourceID: UUID(), data: Data(payload.utf8))
        ) { error in
            XCTAssertEqual(error as? XrayAppCoreError, .noSupportedEndpoints)
        }
    }

    private func fixture(named name: String) throws -> Data {
        guard let url = Bundle.module.url(forResource: name, withExtension: "txt") else {
            XCTFail("Missing fixture \(name)")
            return Data()
        }
        return try Data(contentsOf: url)
    }
}
