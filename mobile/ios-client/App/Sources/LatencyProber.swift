import Foundation
import Network
import Security
import XrayAppCore

struct ProfileLatencyTarget: Sendable {
    let id: UUID
    let host: String
    let port: Int
    let securityKind: ProfileSecurityKind
    let tlsSettings: TLSSecuritySettings?
}

enum LatencyProber {
    static func targets(from profiles: [ResolvedProfile]) -> [ProfileLatencyTarget] {
        profiles.compactMap { profile in
            switch profile {
            case let .manual(manual):
                return target(
                    id: manual.id,
                    address: manual.address,
                    port: manual.port,
                    securityKind: manual.securityKind,
                    tlsSettings: manual.tlsSettings
                )
            case let .subscriptionEndpoint(endpoint):
                return target(
                    id: endpoint.id,
                    address: endpoint.address,
                    port: endpoint.port,
                    securityKind: endpoint.securityKind,
                    tlsSettings: endpoint.tlsSettings
                )
            }
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

    private static func target(
        id: UUID,
        address: String,
        port: Int,
        securityKind: ProfileSecurityKind,
        tlsSettings: TLSSecuritySettings?
    ) -> ProfileLatencyTarget? {
        let normalizedAddress = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalizedAddress.isEmpty, (1...65535).contains(port) else {
            return nil
        }
        return ProfileLatencyTarget(
            id: id,
            host: normalizedAddress,
            port: port,
            securityKind: securityKind,
            tlsSettings: tlsSettings
        )
    }

    private static func probe(
        target: ProfileLatencyTarget,
        timeout: TimeInterval
    ) async -> ProfileLatencyRecord {
        do {
            let latencyMs: Int
            switch target.securityKind {
            case .reality:
                latencyMs = try await measureTCPReady(host: target.host, port: target.port, timeout: timeout)
            case .tls:
                latencyMs = try await measureTLSReady(target: target, timeout: timeout)
            }

            return ProfileLatencyRecord(
                latencyMs: latencyMs,
                state: .available,
                measuredAt: Date(),
                detail: nil
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

    private static func measureTCPReady(host: String, port: Int, timeout: TimeInterval) async throws -> Int {
        try await measureConnection(
            host: host,
            port: port,
            parameters: .tcp,
            timeout: timeout
        )
    }

    private static func measureTLSReady(target: ProfileLatencyTarget, timeout: TimeInterval) async throws -> Int {
        let tlsOptions = NWProtocolTLS.Options()
        let securityOptions = tlsOptions.securityProtocolOptions

        let peerName = target.tlsSettings?.verifyPeerCertByName
            ?? target.tlsSettings?.serverName
            ?? target.host
        if !peerName.isEmpty {
            sec_protocol_options_set_tls_server_name(securityOptions, peerName)
        }

        for protocolName in target.tlsSettings?.alpn ?? [] {
            sec_protocol_options_add_tls_application_protocol(securityOptions, protocolName)
        }

        if target.tlsSettings?.allowInsecure == true {
            sec_protocol_options_set_verify_block(securityOptions, { _, _, complete in
                complete(true)
            }, DispatchQueue.global(qos: .utility))
        }

        let parameters = NWParameters(tls: tlsOptions, tcp: NWProtocolTCP.Options())
        return try await measureConnection(
            host: target.host,
            port: target.port,
            parameters: parameters,
            timeout: timeout
        )
    }

    private static func measureConnection(
        host: String,
        port: Int,
        parameters: NWParameters,
        timeout: TimeInterval
    ) async throws -> Int {
        guard let endpointPort = NWEndpoint.Port(rawValue: UInt16(port)) else {
            throw LatencyProbeError.invalidPort
        }

        return try await withCheckedThrowingContinuation { continuation in
            let connection = NWConnection(host: NWEndpoint.Host(host), port: endpointPort, using: parameters)
            let queue = DispatchQueue(label: "internet.latency.\(UUID().uuidString)")
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
                    let latencyMs = Int(elapsedNs / 1_000_000)
                    finish(.success(max(1, latencyMs)))
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
    case timeout
    case cancelled

    var errorDescription: String? {
        switch self {
        case .invalidPort:
            return "Invalid port."
        case .timeout:
            return "Timed out."
        case .cancelled:
            return "Cancelled."
        }
    }
}
