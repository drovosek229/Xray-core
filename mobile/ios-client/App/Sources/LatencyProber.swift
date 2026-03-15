import Foundation
import Network
import XrayAppCore

struct ProfileLatencyTarget: Sendable {
    let id: UUID
    let profile: ResolvedProfile
    let localSocksPort: Int
}

enum LatencyProber {
    static func targets(from profiles: [ResolvedProfile]) -> [ProfileLatencyTarget] {
        profiles.enumerated().compactMap { index, profile in
            guard isProfileProbeable(profile) else {
                return nil
            }
            return ProfileLatencyTarget(
                id: profileID(for: profile),
                profile: profile,
                localSocksPort: AppConfiguration.latencyProbeLocalSocksPortBase + index
            )
        }
    }

    static func probe(
        targets: [ProfileLatencyTarget],
        timeout: TimeInterval = AppConfiguration.latencyProbeTimeout,
        maxConcurrent: Int = AppConfiguration.latencyProbeMaxConcurrent
    ) async -> [UUID: ProfileLatencyRecord] {
        guard !targets.isEmpty else {
            return [:]
        }

        var results: [UUID: ProfileLatencyRecord] = [:]
        let chunkSize = max(1, maxConcurrent)

        for chunkStart in stride(from: 0, to: targets.count, by: chunkSize) {
            let chunk = Array(targets[chunkStart..<min(chunkStart + chunkSize, targets.count)])
            let chunkResults = await withTaskGroup(of: (UUID, ProfileLatencyRecord).self) { group in
                for target in chunk {
                    group.addTask {
                        (target.id, await probe(target: target, timeout: timeout))
                    }
                }

                var collected: [(UUID, ProfileLatencyRecord)] = []
                for await result in group {
                    collected.append(result)
                }
                return collected
            }

            for (id, record) in chunkResults {
                results[id] = record
            }
        }

        return results
    }

    private static func probe(
        target: ProfileLatencyTarget,
        timeout: TimeInterval
    ) async -> ProfileLatencyRecord {
        do {
            guard let probeURL = URL(string: AppConfiguration.latencyProbeURLString) else {
                throw LatencyProbeError.invalidProbeURL
            }

            let configJSON = try buildRuntimeConfig(
                for: target.profile,
                localSocksPort: target.localSocksPort
            )

            let bridge = XrayEngineBridge()
            try bridge.validate(configJSON: configJSON)
            try bridge.start(configJSON: configJSON)
            defer { bridge.stop() }

            try await waitUntilSOCKSReady(
                host: AppConfiguration.localSocksListenAddress,
                port: target.localSocksPort,
                timeout: min(timeout, 1.5)
            )

            let sample = try await BenchmarkRunner.measure(
                url: probeURL,
                timeout: timeout,
                socksProxy: HTTPSOCKSProxyConfiguration(
                    host: AppConfiguration.localSocksListenAddress,
                    port: target.localSocksPort
                )
            )

            return ProfileLatencyRecord(
                latencyMs: sample.totalMs,
                state: .available,
                measuredAt: Date(),
                detail: probeURL.absoluteString
            )
        } catch {
            return ProfileLatencyRecord(
                latencyMs: nil,
                state: .failed,
                measuredAt: Date(),
                detail: error.localizedDescription
            )
        }
    }

    private static func buildRuntimeConfig(
        for profile: ResolvedProfile,
        localSocksPort: Int
    ) throws -> String {
        let context = RuntimeConfigContext(
            dnsServers: AppConfiguration.runtimeDoHServers,
            localSocksListenAddress: AppConfiguration.localSocksListenAddress,
            localSocksListenPort: localSocksPort
        )

        switch profile {
        case let .manual(manual):
            return try RuntimeConfigBuilder.build(for: manual, context: context)
        case let .subscriptionEndpoint(endpoint):
            return try RuntimeConfigBuilder.build(for: endpoint, context: context)
        }
    }

    private static func profileID(for profile: ResolvedProfile) -> UUID {
        switch profile {
        case let .manual(manual):
            return manual.id
        case let .subscriptionEndpoint(endpoint):
            return endpoint.id
        }
    }

    private static func isProfileProbeable(_ profile: ResolvedProfile) -> Bool {
        switch profile {
        case let .manual(manual):
            return !manual.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                && (1...65535).contains(manual.port)
        case let .subscriptionEndpoint(endpoint):
            return !endpoint.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                && (1...65535).contains(endpoint.port)
        }
    }

    private static func waitUntilSOCKSReady(
        host: String,
        port: Int,
        timeout: TimeInterval
    ) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            do {
                _ = try await measureTCPReady(
                    host: host,
                    port: port,
                    timeout: min(0.25, max(0.05, deadline.timeIntervalSinceNow))
                )
                return
            } catch {
                try? await Task.sleep(nanoseconds: 50_000_000)
            }
        }
        throw LatencyProbeError.timeout
    }

    private static func measureTCPReady(host: String, port: Int, timeout: TimeInterval) async throws -> Int {
        guard let endpointPort = NWEndpoint.Port(rawValue: UInt16(port)) else {
            throw LatencyProbeError.invalidPort
        }

        return try await withCheckedThrowingContinuation { continuation in
            let connection = NWConnection(host: NWEndpoint.Host(host), port: endpointPort, using: .tcp)
            let queue = DispatchQueue(label: "internet.probe.ready.\(UUID().uuidString)")
            let start = DispatchTime.now()
            let finishState = FinishState()

            @Sendable func finish(_ result: Result<Int, Error>) {
                guard !finishState.isFinished else {
                    return
                }
                finishState.isFinished = true
                connection.stateUpdateHandler = nil
                connection.cancel()
                continuation.resume(with: result)
            }

            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    let elapsedNs = DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds
                    finish(.success(max(1, Int(elapsedNs / 1_000_000))))
                case let .failed(error):
                    finish(.failure(error))
                case .cancelled:
                    finish(.failure(LatencyProbeError.cancelled))
                default:
                    break
                }
            }

            connection.start(queue: queue)
            queue.asyncAfter(deadline: .now() + timeout) {
                finish(.failure(LatencyProbeError.timeout))
            }
        }
    }
}

private final class FinishState: @unchecked Sendable {
    var isFinished = false
}

private enum LatencyProbeError: LocalizedError {
    case invalidPort
    case invalidProbeURL
    case timeout
    case cancelled

    var errorDescription: String? {
        switch self {
        case .invalidPort:
            return "Invalid probe port."
        case .invalidProbeURL:
            return "Invalid Cloudflare probe URL."
        case .timeout:
            return "Timed out while probing Cloudflare."
        case .cancelled:
            return "Cancelled."
        }
    }
}
