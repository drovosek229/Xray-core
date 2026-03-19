import Foundation
import XCTest
@testable import XrayAppCore
@testable import XrayClientShared

final class SimpleRoutingSettingsTests: XCTestCase {
    func testSimpleRoutingSettingsRoundTripThroughPreferencesStore() throws {
        let appGroupStore = makeTestAppGroupStore()
        let preferencesStore = ClientPreferencesStore(appGroupStore: appGroupStore)
        let settings = SimpleRoutingSettings(
            isEnabled: true,
            rules: [
                SimpleRoutingRule(
                    id: UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!,
                    kind: .geoSite(selectors: ["category-ads-all"]),
                    target: .block
                ),
                SimpleRoutingRule(
                    id: UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!,
                    kind: .geoIP(selectors: ["ru"]),
                    target: .direct
                ),
                SimpleRoutingRule(
                    id: UUID(uuidString: "cccccccc-cccc-cccc-cccc-cccccccccccc")!,
                    kind: .network(.tcpUDP),
                    target: .proxy
                ),
            ]
        )
        let expected = SimpleRoutingSettings(
            isEnabled: true,
            rules: [
                SimpleRoutingRule(
                    id: UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!,
                    kind: .geoSite(selectors: ["category-ads-all"]),
                    target: .block
                ),
                SimpleRoutingRule(
                    id: UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!,
                    kind: .geoIP(selectors: ["ru"]),
                    target: .direct
                ),
            ]
        )

        try preferencesStore.saveSimpleRoutingSettings(settings)

        XCTAssertEqual(try preferencesStore.loadSimpleRoutingSettings(), expected)
    }

    func testRussiaPresetMatchesExpectedURLsAndRuleOrder() {
        XCTAssertEqual(
            AppConfiguration.russiaPresetRemoteGeoAssetSettings.geoIPURLString,
            "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geoip.dat"
        )
        XCTAssertEqual(
            AppConfiguration.russiaPresetRemoteGeoAssetSettings.geoSiteURLString,
            "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/geosite.dat"
        )

        let settings = AppConfiguration.russiaPresetSimpleRoutingSettings
        XCTAssertTrue(settings.isEnabled)
        XCTAssertEqual(settings.rules.count, 3)

        XCTAssertEqual(
            settings.rules[0],
            SimpleRoutingRule(
                id: UUID(uuidString: "2f9f79e0-feca-4b35-9193-6da9a51d1fb9")!,
                kind: .geoSite(selectors: ["category-ads-all"]),
                target: .block
            )
        )
        XCTAssertEqual(
            settings.rules[1],
            SimpleRoutingRule(
                id: UUID(uuidString: "99dd8b1c-20e4-43bb-bb51-4738062d87bf")!,
                kind: .geoIP(selectors: ["cloudflare", "google"]),
                target: .proxy
            )
        )
        XCTAssertEqual(
            settings.rules[2],
            SimpleRoutingRule(
                id: UUID(uuidString: "6c515c38-70bc-4745-a68d-fe374db1a92f")!,
                kind: .geoIP(selectors: ["ru"]),
                target: .direct
            )
        )
    }
}
