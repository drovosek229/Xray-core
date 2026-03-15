import Foundation
import XCTest
@testable import XrayAppCore
@testable import XrayClientShared

final class ProfileRepositoryTests: XCTestCase {
    func testLegacySelectedProfileMigratesToActiveTunnelTarget() throws {
        let harness = RepositoryHarness()
        let reference = ProfileReference.manual(UUID(uuidString: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")!)

        try harness.appGroupStore.save(reference, forKey: AppConfiguration.legacySelectedProfileKey)

        let migrated = try harness.repository.activeTunnelTarget()

        XCTAssertEqual(migrated, reference)
        XCTAssertEqual(
            try harness.appGroupStore.load(ProfileReference.self, forKey: AppConfiguration.activeTunnelTargetKey),
            reference
        )
        XCTAssertNil(
            try harness.appGroupStore.load(ProfileReference.self, forKey: AppConfiguration.legacySelectedProfileKey)
        )
    }

    func testDeletingActiveManualProfileClearsActiveTunnelTarget() throws {
        let harness = RepositoryHarness()
        let profile = ManualProfile(
            id: UUID(uuidString: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")!,
            name: "Manual",
            address: "example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            serverName: "cdn.example.com",
            publicKey: "public-key",
            xhttpHost: "",
            xhttpPath: "/"
        )

        try harness.repository.saveManualProfile(profile)
        try harness.repository.setActiveTunnelTarget(.manual(profile.id))

        try harness.repository.deleteManualProfile(profile.id)

        XCTAssertNil(try harness.repository.activeTunnelTarget())
    }

    func testDeletingActiveSubscriptionEndpointClearsActiveTunnelTarget() throws {
        let harness = RepositoryHarness()
        let sourceID = UUID(uuidString: "cccccccc-cccc-cccc-cccc-cccccccccccc")!
        let endpoint = SubscriptionEndpoint(
            id: UUID(uuidString: "dddddddd-dddd-dddd-dddd-dddddddddddd")!,
            sourceID: sourceID,
            displayName: "Imported",
            address: "edge.example.com",
            port: 443,
            uuid: "11111111-1111-1111-1111-111111111111",
            securityKind: .tls,
            tlsSettings: TLSSecuritySettings(serverName: "cdn.example.com"),
            xhttpHost: "",
            xhttpPath: "/"
        )

        try harness.repository.replaceSubscriptionEndpoints(sourceID: sourceID, with: [endpoint])
        try harness.repository.setActiveTunnelTarget(.subscriptionEndpoint(endpoint.id))

        try harness.repository.deleteSubscriptionEndpoint(endpoint.id)

        XCTAssertNil(try harness.repository.activeTunnelTarget())
    }

    func testActiveTunnelTargetPersistsAcrossRepositoryInstances() throws {
        let appGroupStore = AppGroupStore(appGroupIdentifier: "tests.internet.\(UUID().uuidString.lowercased())")
        let service = "tests.internet.service.\(UUID().uuidString.lowercased())"
        let repository = ProfileRepository(
            appGroupStore: appGroupStore,
            keychainStore: KeychainStore(service: service, accessGroup: nil)
        )
        let secondRepository = ProfileRepository(
            appGroupStore: appGroupStore,
            keychainStore: KeychainStore(service: service, accessGroup: nil)
        )
        let reference = ProfileReference.manual(UUID(uuidString: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")!)

        try repository.setActiveTunnelTarget(reference)

        XCTAssertEqual(try secondRepository.activeTunnelTarget(), reference)
    }
}

private struct RepositoryHarness {
    let appGroupStore: AppGroupStore
    let repository: ProfileRepository

    init() {
        let appGroupIdentifier = "tests.internet.\(UUID().uuidString.lowercased())"
        let service = "tests.internet.service.\(UUID().uuidString.lowercased())"
        appGroupStore = AppGroupStore(appGroupIdentifier: appGroupIdentifier)
        repository = ProfileRepository(
            appGroupStore: appGroupStore,
            keychainStore: KeychainStore(service: service, accessGroup: nil)
        )
    }
}
