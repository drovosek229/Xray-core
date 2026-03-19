import Foundation
import XCTest
@testable import XrayClientShared

final class DirectEgressSupportTests: XCTestCase {
    func testPlannerPrefersActiveWiFiInterface() {
        let snapshot = DirectEgressPathSnapshot(
            status: .satisfied,
            availableInterfaces: [
                DirectEgressInterface(name: "utun3", type: .other),
                DirectEgressInterface(
                    name: "en0",
                    type: .wifi,
                    addresses: [DirectEgressInterfaceAddress(host: "192.168.1.25", family: .ipv4)]
                ),
                DirectEgressInterface(
                    name: "pdp_ip0",
                    type: .cellular,
                    addresses: [DirectEgressInterfaceAddress(host: "10.0.0.20", family: .ipv4)]
                ),
            ],
            activeInterfaceTypes: [.wifi, .cellular]
        )

        XCTAssertEqual(
            DirectEgressPlanner.plan(for: snapshot),
            .bound(interfaceName: "en0", sourceAddress: "192.168.1.25", sourceAddressFamily: .ipv4)
        )
    }

    func testPlannerBlocksWhenNetworkPathIsUnsatisfied() {
        let snapshot = DirectEgressPathSnapshot(
            status: .unsatisfied,
            availableInterfaces: [
                DirectEgressInterface(
                    name: "en0",
                    type: .wifi,
                    addresses: [DirectEgressInterfaceAddress(host: "192.168.1.25", family: .ipv4)]
                ),
            ],
            activeInterfaceTypes: [.wifi]
        )

        XCTAssertEqual(
            DirectEgressPlanner.plan(for: snapshot),
            .blocked(reason: "No satisfied network path")
        )
    }

    func testPlannerBlocksWhenNoUsablePhysicalInterfaceExists() {
        let snapshot = DirectEgressPathSnapshot(
            status: .satisfied,
            availableInterfaces: [
                DirectEgressInterface(name: "utun2", type: .other),
                DirectEgressInterface(name: "lo0", type: .loopback),
            ],
            activeInterfaceTypes: [.other]
        )

        XCTAssertEqual(
            DirectEgressPlanner.plan(for: snapshot),
            .blocked(reason: "No usable physical interface")
        )
    }

    func testPlannerFallsBackToSystemDirectPathWhenInterfaceAddressIsUnavailable() {
        let snapshot = DirectEgressPathSnapshot(
            status: .satisfied,
            availableInterfaces: [
                DirectEgressInterface(name: "en0", type: .wifi, addresses: []),
            ],
            activeInterfaceTypes: [.wifi]
        )

        XCTAssertEqual(
            DirectEgressPlanner.plan(for: snapshot),
            .systemDefault(reason: "No usable interface address; using the system direct path")
        )
    }

    func testBoundPatchInjectsSockoptInterfaceIntoDirectOutbound() throws {
        let result = try DirectEgressRuntimeConfigPatcher.patch(
            runtimeConfigJSON: runtimeConfigJSON(
                rules: [
                    ["type": "field", "outboundTag": "direct", "ip": ["geoip:ru"]],
                    ["type": "field", "outboundTag": "proxy"],
                ]
            ),
            directEgressStatus: .bound(
                interfaceName: "en0",
                sourceAddress: "192.168.1.25",
                sourceAddressFamily: .ipv4
            )
        )

        XCTAssertTrue(result.hasDirectRules)
        XCTAssertEqual(
            result.directEgressStatus,
            .bound(interfaceName: "en0", sourceAddress: "192.168.1.25", sourceAddressFamily: .ipv4)
        )

        let object = try rootObject(from: result.configJSON)
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let directOutbound = try XCTUnwrap(outbounds.first(where: { ($0["tag"] as? String) == "direct" }))
        let settings = try XCTUnwrap(directOutbound["settings"] as? [String: Any])
        let streamSettings = try XCTUnwrap(directOutbound["streamSettings"] as? [String: Any])
        let sockopt = try XCTUnwrap(streamSettings["sockopt"] as? [String: Any])

        XCTAssertEqual(directOutbound["protocol"] as? String, "freedom")
        XCTAssertEqual(directOutbound["sendThrough"] as? String, "192.168.1.25")
        XCTAssertEqual(settings["domainStrategy"] as? String, "UseIPv4")
        XCTAssertEqual(sockopt["interface"] as? String, "en0")
    }

    func testSystemDefaultPatchLeavesDirectOutboundUntouched() throws {
        let originalJSON = runtimeConfigJSON(
            rules: [
                ["type": "field", "outboundTag": "direct", "ip": ["geoip:ru"]],
                ["type": "field", "outboundTag": "proxy"],
            ]
        )

        let result = try DirectEgressRuntimeConfigPatcher.patch(
            runtimeConfigJSON: originalJSON,
            directEgressStatus: .systemDefault(reason: "No usable interface address; using the system direct path")
        )

        XCTAssertTrue(result.hasDirectRules)
        XCTAssertEqual(
            result.directEgressStatus,
            .systemDefault(reason: "No usable interface address; using the system direct path")
        )
        XCTAssertEqual(result.configJSON, originalJSON)
    }

    func testBlockedPatchRewritesDirectOutboundToBlackhole() throws {
        let result = try DirectEgressRuntimeConfigPatcher.patch(
            runtimeConfigJSON: runtimeConfigJSON(
                rules: [
                    ["type": "field", "outboundTag": "direct", "ip": ["geoip:ru"]],
                    ["type": "field", "outboundTag": "proxy"],
                ]
            ),
            directEgressStatus: .blocked(reason: "No satisfied network path")
        )

        XCTAssertTrue(result.hasDirectRules)
        XCTAssertEqual(result.directEgressStatus, .blocked(reason: "No satisfied network path"))

        let object = try rootObject(from: result.configJSON)
        let outbounds = try XCTUnwrap(object["outbounds"] as? [[String: Any]])
        let directOutbound = try XCTUnwrap(outbounds.first(where: { ($0["tag"] as? String) == "direct" }))

        XCTAssertEqual(directOutbound["tag"] as? String, "direct")
        XCTAssertEqual(directOutbound["protocol"] as? String, "blackhole")
    }

    func testConfigsWithoutDirectRulesAreLeftUntouched() throws {
        let originalJSON = runtimeConfigJSON(
            rules: [
                ["type": "field", "outboundTag": "proxy"],
            ]
        )

        let result = try DirectEgressRuntimeConfigPatcher.patch(
            runtimeConfigJSON: originalJSON,
            directEgressStatus: .bound(
                interfaceName: "en0",
                sourceAddress: "192.168.1.25",
                sourceAddressFamily: .ipv4
            )
        )

        XCTAssertFalse(result.hasDirectRules)
        XCTAssertNil(result.directEgressStatus)
        XCTAssertEqual(result.configJSON, originalJSON)
    }

    private func runtimeConfigJSON(rules: [[String: Any]]) -> String {
        let object: [String: Any] = [
            "inbounds": [],
            "outbounds": [
                ["tag": "proxy", "protocol": "vless"],
                ["tag": "direct", "protocol": "freedom"],
                ["tag": "block", "protocol": "blackhole"],
            ],
            "routing": [
                "domainStrategy": "AsIs",
                "rules": rules,
            ],
            "dns": [
                "servers": ["198.18.0.1"],
            ],
        ]

        let data = try! JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
        return String(decoding: data, as: UTF8.self)
    }

    private func rootObject(from json: String) throws -> [String: Any] {
        let data = Data(json.utf8)
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    }
}
