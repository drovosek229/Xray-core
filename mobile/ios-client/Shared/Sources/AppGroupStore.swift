import Foundation

final class AppGroupStore {
    private let defaults: UserDefaults
    private let containerURL: URL

    init(appGroupIdentifier: String = AppConfiguration.appGroupIdentifier) {
        defaults = UserDefaults(suiteName: appGroupIdentifier) ?? .standard
        let fallbackContainerName = String(
            appGroupIdentifier.unicodeScalars.map { scalar in
                CharacterSet.alphanumerics.contains(scalar) ? Character(scalar) : "-"
            }
        )
        containerURL = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroupIdentifier)
            ?? FileManager.default.temporaryDirectory
            .appendingPathComponent("xray-ios-client-\(fallbackContainerName)", isDirectory: true)
        try? FileManager.default.createDirectory(at: containerURL, withIntermediateDirectories: true)
    }

    func save<T: Encodable>(_ value: T, forKey key: String) throws {
        let data = try JSONEncoder().encode(value)
        defaults.set(data, forKey: key)
        defaults.synchronize()
    }

    func load<T: Decodable>(_ type: T.Type, forKey key: String) throws -> T? {
        guard let data = defaults.data(forKey: key) else {
            return nil
        }
        return try JSONDecoder().decode(type, from: data)
    }

    func removeValue(forKey key: String) {
        defaults.removeObject(forKey: key)
        defaults.synchronize()
    }

    func fileURL(named fileName: String) -> URL {
        containerURL.appendingPathComponent(fileName)
    }
}
