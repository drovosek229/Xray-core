import Foundation

enum HomeSortMode: String, Codable, CaseIterable {
    case latency
    case name

    var displayName: String {
        switch self {
        case .latency:
            return "Latency"
        case .name:
            return "Name"
        }
    }
}

final class ClientPreferencesStore {
    private let appGroupStore: AppGroupStore

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
    }

    func loadHomeSortMode() throws -> HomeSortMode {
        try appGroupStore.load(HomeSortMode.self, forKey: AppConfiguration.homeSortModeKey) ?? .latency
    }

    func saveHomeSortMode(_ value: HomeSortMode) throws {
        try appGroupStore.save(value, forKey: AppConfiguration.homeSortModeKey)
    }

    func loadCollapsedSectionIDs() throws -> Set<String> {
        Set(try appGroupStore.load([String].self, forKey: AppConfiguration.collapsedSectionIDsKey) ?? [])
    }

    func saveCollapsedSectionIDs(_ values: Set<String>) throws {
        try appGroupStore.save(Array(values).sorted(), forKey: AppConfiguration.collapsedSectionIDsKey)
    }

}
