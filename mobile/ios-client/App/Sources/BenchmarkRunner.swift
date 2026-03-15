import Foundation

struct HTTPSOCKSProxyConfiguration: Sendable {
    let host: String
    let port: Int

    var connectionProxyDictionary: [AnyHashable: Any] {
        [
            "SOCKSEnable": true,
            "SOCKSProxy": host,
            "SOCKSPort": port,
        ]
    }
}

enum BenchmarkRunner {
    static func run(
        url: URL,
        timeout: TimeInterval,
        targetName: String,
        profileShape: String,
        sessionTimings: TunnelPerformanceTimings?
    ) async throws -> TunnelBenchmarkResult {
        let runner = HTTPBenchmarkSession(timeout: timeout)
        let cold = try await runner.measure(url: url)
        let warm = try await runner.measure(url: url)
        runner.invalidate()

        return TunnelBenchmarkResult(
            generatedAt: Date(),
            targetName: targetName,
            profileShape: profileShape,
            probeURL: url.absoluteString,
            cold: cold,
            warm: warm,
            sessionTimings: sessionTimings
        )
    }

    static func measure(
        url: URL,
        timeout: TimeInterval,
        socksProxy: HTTPSOCKSProxyConfiguration?
    ) async throws -> HTTPBenchmarkSample {
        let runner = HTTPBenchmarkSession(timeout: timeout, socksProxy: socksProxy)
        defer { runner.invalidate() }
        return try await runner.measure(url: url)
    }
}

private final class HTTPBenchmarkSession: NSObject, URLSessionTaskDelegate, URLSessionDataDelegate {
    private struct PendingTask {
        let startedAt: Date
        let continuation: CheckedContinuation<HTTPBenchmarkSample, Error>
    }

    private var session: URLSession!
    private let lock = NSLock()
    private var pendingTasks: [Int: PendingTask] = [:]
    private var metricsByTaskID: [Int: URLSessionTaskMetrics] = [:]
    private var statusCodes: [Int: Int] = [:]

    init(timeout: TimeInterval, socksProxy: HTTPSOCKSProxyConfiguration? = nil) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeout
        configuration.timeoutIntervalForResource = timeout
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.waitsForConnectivity = false
        configuration.connectionProxyDictionary = socksProxy?.connectionProxyDictionary
        super.init()
        self.session = URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
    }

    func measure(url: URL) async throws -> HTTPBenchmarkSample {
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.timeoutInterval = session.configuration.timeoutIntervalForRequest

        return try await withCheckedThrowingContinuation { continuation in
            let task = session.dataTask(with: request)
            let pendingTask = PendingTask(startedAt: Date(), continuation: continuation)

            lock.lock()
            pendingTasks[task.taskIdentifier] = pendingTask
            lock.unlock()

            task.resume()
        }
    }

    func invalidate() {
        session.invalidateAndCancel()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didFinishCollecting metrics: URLSessionTaskMetrics) {
        lock.lock()
        metricsByTaskID[task.taskIdentifier] = metrics
        lock.unlock()
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
    ) {
        if let httpResponse = response as? HTTPURLResponse {
            lock.lock()
            statusCodes[dataTask.taskIdentifier] = httpResponse.statusCode
            lock.unlock()
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        lock.lock()
        let pendingTask = pendingTasks.removeValue(forKey: task.taskIdentifier)
        let metrics = metricsByTaskID.removeValue(forKey: task.taskIdentifier)
        let statusCode = statusCodes.removeValue(forKey: task.taskIdentifier)
        lock.unlock()

        guard let pendingTask else {
            return
        }

        if let error {
            pendingTask.continuation.resume(throwing: error)
            return
        }

        guard let metrics else {
            pendingTask.continuation.resume(throwing: BenchmarkRunnerError.missingMetrics)
            return
        }

        let transaction = metrics.transactionMetrics.last
        let sample = HTTPBenchmarkSample(
            dnsLookupMs: durationMs(from: transaction?.domainLookupStartDate, to: transaction?.domainLookupEndDate),
            outboundConnectMs: durationMs(from: transaction?.connectStartDate, to: transaction?.connectEndDate),
            tlsHandshakeMs: durationMs(from: transaction?.secureConnectionStartDate, to: transaction?.secureConnectionEndDate),
            firstByteMs: durationMs(from: pendingTask.startedAt, to: transaction?.responseStartDate),
            totalMs: max(1, durationMs(from: pendingTask.startedAt, to: Date()) ?? 0),
            statusCode: statusCode
        )
        pendingTask.continuation.resume(returning: sample)
    }
}

private enum BenchmarkRunnerError: LocalizedError {
    case missingMetrics

    var errorDescription: String? {
        switch self {
        case .missingMetrics:
            return "No benchmark metrics were collected."
        }
    }
}

private func durationMs(from start: Date?, to end: Date?) -> Int? {
    guard let start, let end else {
        return nil
    }
    return max(0, Int(end.timeIntervalSince(start) * 1000.0))
}
