import Foundation
import XrayAppCore

enum ResolvedProfile {
    case manual(ManualProfile)
    case subscriptionEndpoint(SubscriptionEndpoint)
}

final class ProfileRepository {
    private let appGroupStore: AppGroupStore
    private let keychainStore: KeychainStore

    init(
        appGroupStore: AppGroupStore = AppGroupStore(),
        keychainStore: KeychainStore = KeychainStore()
    ) {
        self.appGroupStore = appGroupStore
        self.keychainStore = keychainStore
    }

    func loadManualProfiles() throws -> [ManualProfile] {
        let ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.manualProfileIDsKey) ?? []
        return try ids.compactMap { try keychainStore.codable(ManualProfile.self, forKey: manualKey(for: $0)) }
    }

    func saveManualProfile(_ profile: ManualProfile) throws {
        var ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.manualProfileIDsKey) ?? []
        if !ids.contains(profile.id) {
            ids.append(profile.id)
        }
        try appGroupStore.save(ids, forKey: AppConfiguration.manualProfileIDsKey)
        try keychainStore.setCodable(profile, forKey: manualKey(for: profile.id))
    }

    func deleteManualProfile(_ profileID: UUID) throws {
        var ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.manualProfileIDsKey) ?? []
        ids.removeAll { $0 == profileID }
        try appGroupStore.save(ids, forKey: AppConfiguration.manualProfileIDsKey)
        try keychainStore.removeValue(forKey: manualKey(for: profileID))
        if try activeTunnelTarget() == .manual(profileID) {
            try clearActiveTunnelTarget()
        }
    }

    func loadSubscriptionSources() throws -> [SubscriptionSource] {
        let ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.subscriptionSourceIDsKey) ?? []
        return try ids.compactMap { try keychainStore.codable(SubscriptionSource.self, forKey: sourceKey(for: $0)) }
    }

    func saveSubscriptionSource(_ source: SubscriptionSource) throws {
        var ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.subscriptionSourceIDsKey) ?? []
        if !ids.contains(source.id) {
            ids.append(source.id)
        }
        try appGroupStore.save(ids, forKey: AppConfiguration.subscriptionSourceIDsKey)
        try keychainStore.setCodable(source, forKey: sourceKey(for: source.id))
    }

    func deleteSubscriptionSource(_ sourceID: UUID) throws {
        var sourceIDs = try appGroupStore.load([UUID].self, forKey: AppConfiguration.subscriptionSourceIDsKey) ?? []
        sourceIDs.removeAll { $0 == sourceID }
        try appGroupStore.save(sourceIDs, forKey: AppConfiguration.subscriptionSourceIDsKey)
        try keychainStore.removeValue(forKey: sourceKey(for: sourceID))

        let endpoints = try loadSubscriptionEndpoints().filter { $0.sourceID == sourceID }
        for endpoint in endpoints {
            try deleteSubscriptionEndpoint(endpoint.id)
        }
    }

    func loadSubscriptionEndpoints() throws -> [SubscriptionEndpoint] {
        let ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.subscriptionEndpointIDsKey) ?? []
        return try ids.compactMap { try keychainStore.codable(SubscriptionEndpoint.self, forKey: endpointKey(for: $0)) }
    }

    func replaceSubscriptionEndpoints(sourceID: UUID, with endpoints: [SubscriptionEndpoint]) throws {
        let existing = try loadSubscriptionEndpoints()
        let remaining = existing.filter { $0.sourceID != sourceID }
        for endpoint in existing where endpoint.sourceID == sourceID {
            try keychainStore.removeValue(forKey: endpointKey(for: endpoint.id))
        }

        var ids = remaining.map(\.id)
        for endpoint in endpoints {
            ids.append(endpoint.id)
            try keychainStore.setCodable(endpoint, forKey: endpointKey(for: endpoint.id))
        }

        try appGroupStore.save(ids, forKey: AppConfiguration.subscriptionEndpointIDsKey)
    }

    func deleteSubscriptionEndpoint(_ endpointID: UUID) throws {
        var ids = try appGroupStore.load([UUID].self, forKey: AppConfiguration.subscriptionEndpointIDsKey) ?? []
        ids.removeAll { $0 == endpointID }
        try appGroupStore.save(ids, forKey: AppConfiguration.subscriptionEndpointIDsKey)
        try keychainStore.removeValue(forKey: endpointKey(for: endpointID))
        if try activeTunnelTarget() == .subscriptionEndpoint(endpointID) {
            try clearActiveTunnelTarget()
        }
    }

    func activeTunnelTarget() throws -> ProfileReference? {
        if let activeTarget = try appGroupStore.load(
            ProfileReference.self,
            forKey: AppConfiguration.activeTunnelTargetKey
        ) {
            return activeTarget
        }

        guard let legacyTarget = try appGroupStore.load(
            ProfileReference.self,
            forKey: AppConfiguration.legacySelectedProfileKey
        ) else {
            return nil
        }

        try appGroupStore.save(legacyTarget, forKey: AppConfiguration.activeTunnelTargetKey)
        appGroupStore.removeValue(forKey: AppConfiguration.legacySelectedProfileKey)
        return legacyTarget
    }

    func setActiveTunnelTarget(_ reference: ProfileReference?) throws {
        guard let reference else {
            try clearActiveTunnelTarget()
            return
        }
        try appGroupStore.save(reference, forKey: AppConfiguration.activeTunnelTargetKey)
        appGroupStore.removeValue(forKey: AppConfiguration.legacySelectedProfileKey)
    }

    func clearActiveTunnelTarget() throws {
        appGroupStore.removeValue(forKey: AppConfiguration.activeTunnelTargetKey)
        appGroupStore.removeValue(forKey: AppConfiguration.legacySelectedProfileKey)
    }

    private func manualKey(for id: UUID) -> String {
        "manual-profile-\(id.uuidString.lowercased())"
    }

    private func sourceKey(for id: UUID) -> String {
        "subscription-source-\(id.uuidString.lowercased())"
    }

    private func endpointKey(for id: UUID) -> String {
        "subscription-endpoint-\(id.uuidString.lowercased())"
    }
}
