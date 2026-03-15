import Foundation

enum ProfileLatencyState: String, Codable {
    case idle
    case available
    case failed
}

struct ProfileLatencyRecord: Codable, Hashable {
    var latencyMs: Int?
    var state: ProfileLatencyState
    var measuredAt: Date?
    var detail: String?

    static let idle = ProfileLatencyRecord(latencyMs: nil, state: .idle, measuredAt: nil, detail: nil)
}

final class ProfileLatencyStore {
    private let appGroupStore: AppGroupStore

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
    }

    func loadRecords() throws -> [String: ProfileLatencyRecord] {
        try appGroupStore.load([String: ProfileLatencyRecord].self, forKey: AppConfiguration.latencyCacheKey) ?? [:]
    }

    func saveRecords(_ records: [String: ProfileLatencyRecord]) throws {
        try appGroupStore.save(records, forKey: AppConfiguration.latencyCacheKey)
    }
}
