import Foundation

final class LogStore {
    private let appGroupStore: AppGroupStore

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
    }

    var xrayLogFilePath: String {
        appGroupStore.fileURL(named: AppConfiguration.xrayLogFileName).path
    }

    func append(_ message: String) {
        let line = "[\(ISO8601DateFormatter().string(from: Date()))] \(message)\n"
        let fileURL = appGroupStore.fileURL(named: AppConfiguration.eventsLogFileName)
        if let data = line.data(using: .utf8) {
            if FileManager.default.fileExists(atPath: fileURL.path),
               let handle = try? FileHandle(forWritingTo: fileURL)
            {
                defer { try? handle.close() }
                _ = try? handle.seekToEnd()
                try? handle.write(contentsOf: data)
            } else {
                try? data.write(to: fileURL, options: .atomic)
            }
        }
    }

    func readLines(limit: Int = 200) -> [String] {
        var lines: [String] = []
        for fileName in [AppConfiguration.eventsLogFileName, AppConfiguration.xrayLogFileName] {
            let fileURL = appGroupStore.fileURL(named: fileName)
            if let contents = try? String(contentsOf: fileURL, encoding: .utf8) {
                lines.append(contents)
            }
        }
        return lines
            .flatMap { $0.split(separator: "\n").map(String.init) }
            .suffix(limit)
            .map { $0 }
    }

    func clear() {
        for fileName in [AppConfiguration.eventsLogFileName, AppConfiguration.xrayLogFileName] {
            let fileURL = appGroupStore.fileURL(named: fileName)
            try? FileManager.default.removeItem(at: fileURL)
        }
    }
}
