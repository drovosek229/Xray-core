import Foundation
@testable import XrayClientShared

func makeTestAppGroupStore() -> AppGroupStore {
    let suiteName = "tests.internet.\(UUID().uuidString.lowercased())"
    let defaults = UserDefaults(suiteName: suiteName) ?? .standard
    defaults.removePersistentDomain(forName: suiteName)

    let containerURL = FileManager.default.temporaryDirectory
        .appendingPathComponent("xray-ios-client-tests-\(UUID().uuidString.lowercased())", isDirectory: true)

    return AppGroupStore(defaults: defaults, containerURL: containerURL)
}
