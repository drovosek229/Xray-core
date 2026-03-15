import Foundation
import XCTest
@testable import XrayAppCore

final class RuntimeConfigBuilderTests: XCTestCase {
    func testManualRealityProfileBuildsLoopbackSocksInboundAndBalancedXHTTP() throws {
        let profile = ManualProfile(
            name: "Primary",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            serverName: "cdn.example.com",
            fingerprint: "chrome",
            publicKey: "public-key",
            shortId: "abcd1234",
            spiderX: "/reality",
            xhttpHost: "cdn.example.com",
            xhttpPath: "/assets",
            xhttpMode: .auto,
            behaviorProfile: .balanced
        )

        let json = try RuntimeConfigBuilder.build(
            for: profile,
            context: RuntimeConfigContext(logFilePath: "/tmp/xray.log")
        )

        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let inbounds = try XCTUnwrap(object["inbounds"] as? [[String: Any]])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let routing = try XCTUnwrap(object["routing"] as? [String: Any])
        let dns = try XCTUnwrap(object["dns"] as? [String: Any])
        let firstOutbound = try XCTUnwrap(outbounds.first)
        let streamSettings = try XCTUnwrap(firstOutbound["streamSettings"] as? [String: Any])
        let xhttpSettings = try XCTUnwrap(streamSettings["xhttpSettings"] as? [String: Any])
        let rules = try XCTUnwrap(routing["rules"] as? [[String: Any]])

        XCTAssertEqual(inbounds.first?["protocol"] as? String, "socks")
        XCTAssertEqual(inbounds.first?["listen"] as? String, "127.0.0.1")
        XCTAssertEqual(inbounds.first?["port"] as? Int, 10_808)
        XCTAssertEqual((inbounds.first?["settings"] as? [String: Any])?["auth"] as? String, "noauth")
        XCTAssertEqual((inbounds.first?["settings"] as? [String: Any])?["udp"] as? Bool, true)
        XCTAssertEqual((inbounds.first?["sniffing"] as? [String: Any])?["enabled"] as? Bool, true)
        XCTAssertEqual(firstOutbound["protocol"] as? String, "vless")
        XCTAssertEqual(streamSettings["security"] as? String, "reality")
        XCTAssertNotNil(streamSettings["realitySettings"] as? [String: Any])
        XCTAssertEqual(xhttpSettings["behaviorProfile"] as? String, "balanced")
        XCTAssertEqual(xhttpSettings["uplinkHTTPMethod"] as? String, "POST")
        let xmux = try XCTUnwrap(xhttpSettings["xmux"] as? [String: Any])
        XCTAssertEqual(xmux["warmConnections"] as? Int, 1)
        XCTAssertEqual(xmux["hKeepAlivePeriod"] as? Int, 30)
        let settings = try XCTUnwrap(firstOutbound["settings"] as? [String: Any])
        let vnext = try XCTUnwrap(settings["vnext"] as? [[String: Any]])
        let users = try XCTUnwrap(vnext.first?["users"] as? [[String: Any]])
        XCTAssertEqual(users.first?["id"] as? String, "11111111-1111-1111-1111-111111111111")
        XCTAssertEqual(users.first?["encryption"] as? String, "none")
        XCTAssertEqual(outbounds[1]["tag"] as? String, "dns-out")
        XCTAssertEqual(outbounds[1]["protocol"] as? String, "dns")
        XCTAssertEqual(rules.first?["inboundTag"] as? [String], ["socks-in"])
        XCTAssertEqual(rules.first?["outboundTag"] as? String, "dns-out")
        XCTAssertEqual(rules.first?["port"] as? String, "53")
        XCTAssertEqual(rules.last?["outboundTag"] as? String, "proxy")
        XCTAssertEqual(dns["servers"] as? [String], ["https+local://1.1.1.1/dns-query", "https+local://1.0.0.1/dns-query"])
        XCTAssertEqual(dns["enableParallelQuery"] as? Bool, true)
    }

    func testRuntimeBuildUsesCustomLoopbackSocksEndpointWhenProvided() throws {
        let profile = ManualProfile(
            name: "Custom Loopback",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            xhttpHost: "",
            xhttpPath: ""
        )

        let json = try RuntimeConfigBuilder.build(
            for: profile,
            context: RuntimeConfigContext(
                dnsServers: ["https+local://9.9.9.9/dns-query"],
                localSocksListenAddress: "::1",
                localSocksListenPort: 12_345
            )
        )
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let inbounds = try XCTUnwrap(object["inbounds"] as? [[String: Any]])
        let dns = try XCTUnwrap(object["dns"] as? [String: Any])

        XCTAssertEqual(inbounds.first?["listen"] as? String, "::1")
        XCTAssertEqual(inbounds.first?["port"] as? Int, 12_345)
        XCTAssertEqual(dns["servers"] as? [String], ["https+local://9.9.9.9/dns-query"])
    }

    func testManualTLSProfileBuildsTLSSettingsAndMethod() throws {
        let profile = ManualProfile(
            name: "TLS Route",
            address: "edge.example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            flow: VLESSFlow.xtlsRprxVision.rawValue,
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(
                serverName: "cdn.example.com",
                fingerprint: "chrome",
                alpn: ["h2", "h3"],
                pinnedPeerCertSha256: "abcd",
                verifyPeerCertByName: "cdn.example.com"
            ),
            encryption: "mlkem768x25519plus.native.1rtt.testkey",
            xhttpHost: "",
            xhttpPath: "",
            xhttpMode: .packetUp,
            behaviorProfile: .balanced,
            uplinkHTTPMethod: "PUT"
        )

        let json = try RuntimeConfigBuilder.build(for: profile)
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let firstOutbound: [String: Any] = try XCTUnwrap(outbounds.first)
        let streamSettings = try XCTUnwrap(firstOutbound["streamSettings"] as? [String: Any])
        let tlsSettings = try XCTUnwrap(streamSettings["tlsSettings"] as? [String: Any])
        let xhttpSettings = try XCTUnwrap(streamSettings["xhttpSettings"] as? [String: Any])
        let settings = try XCTUnwrap(firstOutbound["settings"] as? [String: Any])
        let vnext = try XCTUnwrap(settings["vnext"] as? [[String: Any]])
        let users = try XCTUnwrap(vnext.first?["users"] as? [[String: Any]])

        XCTAssertEqual(streamSettings["security"] as? String, "tls")
        XCTAssertNil(streamSettings["realitySettings"])
        XCTAssertEqual(tlsSettings["serverName"] as? String, "cdn.example.com")
        XCTAssertEqual(tlsSettings["fingerprint"] as? String, "chrome")
        XCTAssertEqual(tlsSettings["alpn"] as? [String], ["h2", "h3"])
        XCTAssertEqual(xhttpSettings["host"] as? String, "cdn.example.com")
        XCTAssertEqual(xhttpSettings["path"] as? String, "/")
        XCTAssertEqual(xhttpSettings["uplinkHTTPMethod"] as? String, "PUT")
        XCTAssertEqual(users.first?["encryption"] as? String, "mlkem768x25519plus.native.1rtt.testkey")
        XCTAssertNil(users.first?["flow"])
    }

    func testInvalidProfileIsRejected() {
        let profile = ManualProfile(
            name: "",
            address: "",
            port: 0,
            uuid: "",
            serverName: "",
            fingerprint: "",
            publicKey: "",
            xhttpHost: "",
            xhttpPath: ""
        )

        XCTAssertThrowsError(try RuntimeConfigBuilder.build(for: profile))
    }

    func testManualProfileDefaultsXHTTPHostAndPath() throws {
        let profile = ManualProfile(
            name: "Fallback Host",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            serverName: "cdn.example.com",
            fingerprint: "chrome",
            publicKey: "public-key",
            xhttpHost: "",
            xhttpPath: ""
        )

        let json = try RuntimeConfigBuilder.build(for: profile)
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let xhttpSettings = try XCTUnwrap(
            ((outbounds.first?["streamSettings"] as? [String: Any])?["xhttpSettings"] as? [String: Any])
        )

        XCTAssertEqual(xhttpSettings["host"] as? String, "cdn.example.com")
        XCTAssertEqual(xhttpSettings["path"] as? String, "/")
    }

    func testManualProfileDefaultsToNoFlowAndChromeFingerprint() throws {
        let profile = ManualProfile(
            name: "Defaults",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            serverName: "cdn.example.com",
            publicKey: "public-key",
            xhttpHost: "",
            xhttpPath: ""
        )

        XCTAssertNil(profile.flow)
        XCTAssertEqual(profile.fingerprint, ClientFingerprintPreset.chrome.rawValue)
        XCTAssertEqual(profile.normalizedEncryption, "none")
        XCTAssertEqual(profile.normalizedUplinkHTTPMethod, "POST")

        let json = try RuntimeConfigBuilder.build(for: profile)
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let firstOutbound = try XCTUnwrap(outbounds.first)
        let settings = try XCTUnwrap(firstOutbound["settings"] as? [String: Any])
        let vnext = try XCTUnwrap(settings["vnext"] as? [[String: Any]])
        let users = try XCTUnwrap(vnext.first?["users"] as? [[String: Any]])
        let realitySettings = try XCTUnwrap(
            ((firstOutbound["streamSettings"] as? [String: Any])?["realitySettings"] as? [String: Any])
        )

        XCTAssertNil(users.first?["flow"])
        XCTAssertEqual(users.first?["encryption"] as? String, "none")
        XCTAssertEqual(realitySettings["fingerprint"] as? String, "chrome")
    }

    func testManualRealityProfileSupportsVisionUDP443Flow() throws {
        let profile = ManualProfile(
            name: "UDP Vision",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            flow: VLESSFlow.xtlsRprxVisionUDP443.rawValue,
            serverName: "cdn.example.com",
            publicKey: "public-key",
            xhttpHost: "",
            xhttpPath: ""
        )

        let json = try RuntimeConfigBuilder.build(for: profile)
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let users = try XCTUnwrap(
            (((outbounds.first?["settings"] as? [String: Any])?["vnext"] as? [[String: Any]])?.first?["users"] as? [[String: Any]])
        )

        XCTAssertEqual(users.first?["flow"] as? String, "xtls-rprx-vision-udp443")
    }

    func testGetMethodRequiresPacketUpMode() {
        let profile = ManualProfile(
            name: "GET Route",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            xhttpHost: "",
            xhttpPath: "",
            xhttpMode: .auto,
            behaviorProfile: .balanced,
            uplinkHTTPMethod: "GET"
        )

        XCTAssertThrowsError(try RuntimeConfigBuilder.build(for: profile)) { error in
            XCTAssertEqual(
                error as? XrayAppCoreError,
                .invalidProfile("GET uplinkHTTPMethod requires Packet Upload mode.")
            )
        }
    }

    func testTLSProfilesCannotUseVLESSFlow() {
        var profile = ManualProfile(
            name: "TLS Flow",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            xhttpHost: "",
            xhttpPath: ""
        )

        profile.flow = VLESSFlow.xtlsRprxVision.rawValue

        XCTAssertThrowsError(try RuntimeConfigBuilder.build(for: profile)) { error in
            XCTAssertEqual(
                error as? XrayAppCoreError,
                .invalidProfile("TLS profiles cannot use a VLESS flow.")
            )
        }
    }

    func testProfileClassificationPrefersFastTLSStreamUpProfiles() {
        let fast = ManualProfile(
            name: "Fast",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            encryption: "none",
            xhttpHost: "",
            xhttpPath: "",
            xhttpMode: .streamUp,
            behaviorProfile: .balanced,
            uplinkHTTPMethod: "PUT"
        )
        let stealth = ManualProfile(
            name: "Stealth",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            encryption: "mlkem768x25519plus.native.1rtt.testkey",
            xhttpHost: "",
            xhttpPath: "",
            xhttpMode: .packetUp,
            behaviorProfile: .balanced,
            uplinkHTTPMethod: "DELETE"
        )

        XCTAssertEqual(fast.classification, .recommendedFast)
        XCTAssertEqual(stealth.classification, .stealthCompatibility)
    }

    func testUnsupportedEncryptionIsRejected() {
        let profile = ManualProfile(
            name: "Bad Encryption",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            serverName: "cdn.example.com",
            publicKey: "public-key",
            encryption: "aes-256-gcm",
            xhttpHost: "",
            xhttpPath: ""
        )

        XCTAssertThrowsError(try RuntimeConfigBuilder.build(for: profile)) { error in
            XCTAssertEqual(
                error as? XrayAppCoreError,
                .invalidProfile("Unsupported VLESS encryption: aes-256-gcm")
            )
        }
    }

    func testImportedAdvancedXHTTPSettingsAreEmittedIntoRuntimeConfig() throws {
        let endpoint = SubscriptionEndpoint(
            sourceID: UUID(),
            displayName: "Imported",
            address: "compat-a.example.net",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "compat-a.example.net"),
            encryption: "mlkem768x25519plus.native.0rtt.fixture",
            xhttpHost: "",
            xhttpPath: "/RBIUmReH8AaaMr",
            xhttpMode: .packetUp,
            behaviorProfile: .balanced,
            uplinkHTTPMethod: "DELETE",
            xhttpAdvancedSettings: XHTTPAdvancedSettings(
                sessionPlacement: "query",
                sessionKey: "sid",
                seqPlacement: "query",
                seqKey: "rid",
                xPaddingBytes: "1-8",
                xPaddingMethod: "tokenish",
                xPaddingPlacement: "query",
                xPaddingKey: "t",
                xPaddingObfsMode: true,
                noGRPCHeader: true,
                noSSEHeader: false,
                scMaxEachPostBytes: "16384",
                xmux: XHTTPXmuxSettings(
                    maxConnections: "2-4",
                    hKeepAlivePeriod: 45,
                    warmConnections: 2
                )
            )
        )

        let json = try RuntimeConfigBuilder.build(for: endpoint)
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let firstOutbound = try XCTUnwrap(outbounds.first)
        let streamSettings = try XCTUnwrap(firstOutbound["streamSettings"] as? [String: Any])
        let xhttpSettings = try XCTUnwrap(streamSettings["xhttpSettings"] as? [String: Any])

        XCTAssertEqual(xhttpSettings["uplinkHTTPMethod"] as? String, "DELETE")
        XCTAssertEqual(xhttpSettings["sessionPlacement"] as? String, "query")
        XCTAssertEqual(xhttpSettings["sessionKey"] as? String, "sid")
        XCTAssertEqual(xhttpSettings["seqPlacement"] as? String, "query")
        XCTAssertEqual(xhttpSettings["seqKey"] as? String, "rid")
        XCTAssertEqual(xhttpSettings["xPaddingBytes"] as? String, "1-8")
        XCTAssertEqual(xhttpSettings["xPaddingMethod"] as? String, "tokenish")
        XCTAssertEqual(xhttpSettings["xPaddingPlacement"] as? String, "query")
        XCTAssertEqual(xhttpSettings["xPaddingKey"] as? String, "t")
        XCTAssertEqual(xhttpSettings["xPaddingObfsMode"] as? Bool, true)
        XCTAssertEqual(xhttpSettings["noGRPCHeader"] as? Bool, true)
        XCTAssertEqual(xhttpSettings["noSSEHeader"] as? Bool, false)
        XCTAssertEqual(xhttpSettings["scMaxEachPostBytes"] as? String, "16384")
        let xmux = try XCTUnwrap(xhttpSettings["xmux"] as? [String: Any])
        XCTAssertEqual(xmux["maxConnections"] as? String, "2-4")
        XCTAssertEqual(xmux["hKeepAlivePeriod"] as? Int, 45)
        XCTAssertEqual(xmux["warmConnections"] as? Int, 2)
    }
}
