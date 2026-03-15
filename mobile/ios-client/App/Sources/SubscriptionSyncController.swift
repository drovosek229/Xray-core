import Foundation
import XrayAppCore

final class SubscriptionSyncController {
    private let repository: ProfileRepository
    private let logStore: LogStore
    private let client: SubscriptionClient

    init(
        repository: ProfileRepository,
        logStore: LogStore,
        client: SubscriptionClient = SubscriptionClient()
    ) {
        self.repository = repository
        self.logStore = logStore
        self.client = client
    }

    func importSource(name: String, urlString: String) async throws {
        guard let url = URL(string: urlString) else {
            throw XrayAppCoreError.invalidSubscriptionURL
        }

        let source = SubscriptionSource(
            name: name.isEmpty ? "Subscription Link" : name,
            subscriptionURL: url,
            hwid: UUID().uuidString
        )
        try repository.saveSubscriptionSource(source)
        logStore.append("Imported subscription source \(source.name)")
        try await refresh(sourceID: source.id)
    }

    func refreshIfStale(sourceID: UUID, maxAge: TimeInterval = AppConfiguration.staleRefreshInterval) async throws {
        guard let source = try repository.loadSubscriptionSources().first(where: { $0.id == sourceID }) else {
            return
        }
        if let lastSyncAt = source.lastSyncAt, Date().timeIntervalSince(lastSyncAt) < maxAge {
            return
        }
        try await refresh(sourceID: sourceID)
    }

    func refresh(sourceID: UUID) async throws {
        guard var source = try repository.loadSubscriptionSources().first(where: { $0.id == sourceID }) else {
            return
        }

        let fingerprint = DeviceFingerprintProvider.make(hwid: source.hwid)
        do {
            let response = try await client.fetch(source: source, fingerprint: fingerprint)
            source.lastSyncAt = Date()
            source.etag = response.etag
            source.lastModified = response.lastModified

            try repository.saveSubscriptionSource(source)
            try repository.replaceSubscriptionEndpoints(sourceID: source.id, with: response.endpoints)
            logStore.append("Refreshed \(source.name) with \(response.endpoints.count) endpoints")
            if response.endpoints.contains(where: { $0.metadata["tls_allow_insecure"] == "true" }) {
                logStore.append("Imported TLS profiles using allowInsecure. This is compatibility-only and may stop working after 2026-06-01.")
            }
        } catch XrayAppCoreError.notModified {
            source.lastSyncAt = Date()
            try repository.saveSubscriptionSource(source)
            logStore.append("Subscription \(source.name) returned 304 Not Modified")
        }
    }
}
