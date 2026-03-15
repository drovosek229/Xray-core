import Foundation

struct HTTPBenchmarkSample: Codable, Hashable, Sendable {
    var dnsLookupMs: Int?
    var outboundConnectMs: Int?
    var tlsHandshakeMs: Int?
    var firstByteMs: Int?
    var totalMs: Int
    var statusCode: Int?
}

struct TunnelBenchmarkResult: Codable, Hashable, Sendable {
    var generatedAt: Date
    var targetName: String
    var profileShape: String
    var probeURL: String
    var cold: HTTPBenchmarkSample
    var warm: HTTPBenchmarkSample
    var sessionTimings: TunnelPerformanceTimings?
}

final class BenchmarkStore {
    private let appGroupStore: AppGroupStore

    init(appGroupStore: AppGroupStore = AppGroupStore()) {
        self.appGroupStore = appGroupStore
    }

    func loadLatestResult() throws -> TunnelBenchmarkResult? {
        try appGroupStore.load(TunnelBenchmarkResult.self, forKey: AppConfiguration.latestBenchmarkResultKey)
    }

    func saveLatestResult(_ result: TunnelBenchmarkResult) throws {
        try appGroupStore.save(result, forKey: AppConfiguration.latestBenchmarkResultKey)
    }

    func clearLatestResult() {
        appGroupStore.removeValue(forKey: AppConfiguration.latestBenchmarkResultKey)
    }
}
