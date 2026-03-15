import Foundation
import NetworkExtension

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logStore = LogStore()
    private let tunnelSessionStore = TunnelSessionStore()
    private lazy var bridge = XrayEngineBridge()
    private let tun2SocksBridge = Tun2SocksBridge()
    private let stateQueue = DispatchQueue(label: "internet.packet-tunnel.state")
    private var isStopping = false

    override func startTunnel(
        options _: [String: NSObject]?,
        completionHandler: @escaping (Error?) -> Void
    ) {
        stateQueue.sync {
            isStopping = false
        }

        do {
            let providerConfiguration = try loadProviderConfigurationEnvelope()
            writeRuntimeState(
                for: providerConfiguration,
                phase: .starting,
                lastKnownSystemStatus: .connecting
            )
            startTunnel(with: providerConfiguration, completionHandler: completionHandler)
        } catch {
            let runtimeState: TunnelRuntimeState
            if Self.isCleanExternalStartError(error) {
                runtimeState = TunnelRuntimeState(
                    phase: .idle,
                    stopReason: .none,
                    stopOrigin: .system,
                    lastKnownSystemStatus: .disconnected
                )
                logStore.append("Ignored tunnel start without persisted runtime configuration: \(error.localizedDescription)")
            } else {
                runtimeState = TunnelRuntimeState(
                    phase: .failed,
                    lastError: error.localizedDescription,
                    stopOrigin: .launchFailure,
                    lastKnownSystemStatus: .invalid
                )
                logStore.append("Tunnel start failed before network settings: \(error.localizedDescription)")
            }
            try? tunnelSessionStore.saveRuntimeState(runtimeState)
            completionHandler(error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        stateQueue.sync {
            isStopping = true
        }

        let previousState = try? tunnelSessionStore.loadRuntimeState()
        let stopReason = TunnelStopReason(rawValue: reason.rawValue) ?? .unknown
        let classification = stopReason.classify(previousState: previousState)

        tun2SocksBridge.stop()
        bridge.stop()

        try? tunnelSessionStore.saveRuntimeState(
            TunnelRuntimeState(
                sessionID: previousState?.sessionID,
                activeTunnelTarget: previousState?.activeTunnelTarget,
                targetName: previousState?.targetName,
                phase: classification.phase,
                createdAt: previousState?.createdAt ?? Date(),
                startedAt: previousState?.startedAt,
                lastError: classification.phase == .failed
                    ? previousState?.lastError ?? stopReason.fallbackErrorDescription
                    : nil,
                configHash: previousState?.configHash,
                performance: previousState?.performance,
                stopReason: stopReason,
                stopOrigin: classification.origin,
                lastKnownSystemStatus: .disconnected
            )
        )
        logStore.append("Tunnel stopped with reason \(stopReason.rawValue)")
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        let response = "Xray Engine \(bridge.version())".data(using: .utf8)
        completionHandler?(response)
    }

    private func startTunnel(
        with providerConfiguration: TunnelProviderConfigurationEnvelope,
        completionHandler: @escaping (Error?) -> Void
    ) {
        let settings = makeTunnelSettings(routePolicy: providerConfiguration.routePolicy)
        let settingsStartedAt = DispatchTime.now()
        setTunnelNetworkSettings(settings) { [weak self, providerConfiguration] error in
            guard let self else {
                completionHandler(nil)
                return
            }

            let settingsDurationMs = Self.elapsedMilliseconds(since: settingsStartedAt)
            if let error {
                self.writeRuntimeState(
                    for: providerConfiguration,
                    phase: .failed,
                    lastError: error.localizedDescription,
                    performance: TunnelPerformanceTimings(
                        setTunnelNetworkSettingsMs: settingsDurationMs
                    ),
                    stopOrigin: .launchFailure,
                    lastKnownSystemStatus: .invalid
                )
                self.logStore.append("Failed to apply tunnel settings in \(settingsDurationMs)ms: \(error.localizedDescription)")
                completionHandler(error)
                return
            }

            do {
                let validateStartedAt = DispatchTime.now()
                try self.bridge.validate(configJSON: providerConfiguration.runtimeConfigJSON)
                let validateDurationMs = Self.elapsedMilliseconds(since: validateStartedAt)

                let engineStartedAt = DispatchTime.now()
                try self.bridge.start(
                    configJSON: providerConfiguration.runtimeConfigJSON,
                    tunFD: -1,
                    assetDir: Bundle.main.resourceURL?.path ?? ""
                )
                let engineStartDurationMs = Self.elapsedMilliseconds(since: engineStartedAt)

                let tun2SocksStartedAt = DispatchTime.now()
                self.tun2SocksBridge.start(
                    configuration: Tun2SocksConfiguration(
                        socksAddress: AppConfiguration.localSocksListenAddress,
                        socksPort: AppConfiguration.localSocksListenPort,
                        mtu: AppConfiguration.defaultTunnelMTU
                    )
                ) { [weak self] code in
                    self?.handleTun2SocksExit(
                        code: code,
                        providerConfiguration: providerConfiguration
                    )
                }
                let tun2SocksStartDurationMs = Self.elapsedMilliseconds(since: tun2SocksStartedAt)

                self.writeRuntimeState(
                    for: providerConfiguration,
                    phase: .connected,
                    startedAt: Date(),
                    performance: TunnelPerformanceTimings(
                        setTunnelNetworkSettingsMs: settingsDurationMs,
                        configValidateMs: validateDurationMs,
                        xrayEngineStartMs: engineStartDurationMs,
                        tun2SocksStartMs: tun2SocksStartDurationMs
                    ),
                    lastKnownSystemStatus: .connected
                )
                self.logStore.append(
                    "Tunnel started with \(providerConfiguration.targetName) using engine version \(self.bridge.version()) "
                        + "[settings \(settingsDurationMs)ms, validate \(validateDurationMs)ms, engine \(engineStartDurationMs)ms, tun2socks \(tun2SocksStartDurationMs)ms]"
                )
                completionHandler(nil)
            } catch {
                self.tun2SocksBridge.stop()
                self.bridge.stop()
                self.writeRuntimeState(
                    for: providerConfiguration,
                    phase: .failed,
                    lastError: error.localizedDescription,
                    performance: TunnelPerformanceTimings(
                        setTunnelNetworkSettingsMs: settingsDurationMs
                    ),
                    stopOrigin: .launchFailure,
                    lastKnownSystemStatus: .invalid
                )
                self.logStore.append("Tunnel start failed: \(error.localizedDescription)")
                completionHandler(error)
            }
        }
    }

    private func handleTun2SocksExit(
        code: Int32,
        providerConfiguration: TunnelProviderConfigurationEnvelope
    ) {
        let isStopping = stateQueue.sync { self.isStopping }
        if isStopping {
            logStore.append("tun2socks exited during tunnel shutdown with code \(code)")
            return
        }

        let message = code == 0
            ? "tun2socks exited unexpectedly."
            : "tun2socks exited with code \(code)."
        writeRuntimeState(
            for: providerConfiguration,
            phase: .failed,
            lastError: message,
            stopOrigin: .provider,
            lastKnownSystemStatus: .disconnecting
        )
        logStore.append(message)
        cancelTunnelWithError(
            NSError(
                domain: "internet",
                code: 502,
                userInfo: [NSLocalizedDescriptionKey: message]
            )
        )
    }

    private func makeTunnelSettings(routePolicy: TunnelRoutePolicy) -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "198.18.0.1")

        let ipv4 = NEIPv4Settings(addresses: ["198.18.0.2"], subnetMasks: ["255.255.255.252"])
        let plannedIPv4Routes = plannedIPv4Routes(for: routePolicy)
        ipv4.includedRoutes = plannedIPv4Routes.included
        ipv4.excludedRoutes = plannedIPv4Routes.excluded.isEmpty ? nil : plannedIPv4Routes.excluded
        settings.ipv4Settings = ipv4

        let ipv6 = NEIPv6Settings(addresses: ["fd00::2"], networkPrefixLengths: [64])
        let plannedIPv6Routes = plannedIPv6Routes(for: routePolicy)
        ipv6.includedRoutes = plannedIPv6Routes.included
        ipv6.excludedRoutes = plannedIPv6Routes.excluded.isEmpty ? nil : plannedIPv6Routes.excluded
        settings.ipv6Settings = ipv6

        let dns = NEDNSSettings(servers: AppConfiguration.defaultDNSServers)
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        settings.mtu = NSNumber(value: AppConfiguration.defaultTunnelMTU)

        return settings
    }

    private func loadProviderConfigurationEnvelope() throws -> TunnelProviderConfigurationEnvelope {
        guard let protocolConfiguration = protocolConfiguration as? NETunnelProviderProtocol else {
            throw NSError(
                domain: "internet",
                code: 400,
                userInfo: [NSLocalizedDescriptionKey: "Missing tunnel provider configuration."]
            )
        }
        guard let providerConfiguration = protocolConfiguration.providerConfiguration else {
            throw NSError(
                domain: "internet",
                code: 400,
                userInfo: [NSLocalizedDescriptionKey: "Missing persisted tunnel configuration."]
            )
        }
        guard let data = providerConfiguration[AppConfiguration.tunnelProviderConfigurationEnvelopeKey] as? Data else {
            throw NSError(
                domain: "internet",
                code: 400,
                userInfo: [NSLocalizedDescriptionKey: "Missing persisted tunnel runtime configuration."]
            )
        }

        let envelope = try JSONDecoder().decode(TunnelProviderConfigurationEnvelope.self, from: data)
        guard envelope.hasValidHash else {
            throw NSError(
                domain: "internet",
                code: 422,
                userInfo: [NSLocalizedDescriptionKey: "Tunnel runtime configuration integrity check failed."]
            )
        }
        return envelope
    }

    private func writeRuntimeState(
        for providerConfiguration: TunnelProviderConfigurationEnvelope,
        phase: TunnelRuntimePhase,
        startedAt: Date? = nil,
        lastError: String? = nil,
        performance: TunnelPerformanceTimings? = nil,
        stopOrigin: TunnelStopOrigin? = nil,
        lastKnownSystemStatus: TunnelSystemStatus
    ) {
        do {
            try tunnelSessionStore.updateRuntimeState { state in
                guard state.sessionID == providerConfiguration.sessionID else {
                    return
                }
                state.activeTunnelTarget = providerConfiguration.activeTunnelTarget
                state.targetName = providerConfiguration.targetName
                state.phase = phase
                state.startedAt = startedAt ?? state.startedAt
                state.lastError = lastError
                state.configHash = providerConfiguration.configHash
                state.stopOrigin = stopOrigin
                state.lastKnownSystemStatus = lastKnownSystemStatus
                if let performance {
                    var merged = state.performance ?? TunnelPerformanceTimings()
                    merged.merge(from: performance)
                    state.performance = merged
                }
            }
        } catch {
            try? tunnelSessionStore.saveRuntimeState(
                TunnelRuntimeState(
                    sessionID: providerConfiguration.sessionID,
                    activeTunnelTarget: providerConfiguration.activeTunnelTarget,
                    targetName: providerConfiguration.targetName,
                    phase: phase,
                    startedAt: startedAt,
                    lastError: lastError,
                    configHash: providerConfiguration.configHash,
                    performance: performance,
                    stopOrigin: stopOrigin,
                    lastKnownSystemStatus: lastKnownSystemStatus
                )
            )
        }
    }

    private func plannedIPv4Routes(
        for routePolicy: TunnelRoutePolicy
    ) -> (included: [NEIPv4Route], excluded: [NEIPv4Route]) {
        switch routePolicy {
        case .disabled:
            return ([NEIPv4Route.default()], [])
        case let .include(values):
            let routes = values.compactMap(makeIPv4Route(from:))
            return (routes.isEmpty ? [NEIPv4Route.default()] : routes, [])
        case let .exclude(values):
            return ([NEIPv4Route.default()], values.compactMap(makeIPv4Route(from:)))
        }
    }

    private func plannedIPv6Routes(
        for routePolicy: TunnelRoutePolicy
    ) -> (included: [NEIPv6Route], excluded: [NEIPv6Route]) {
        switch routePolicy {
        case .disabled:
            return ([NEIPv6Route.default()], [])
        case let .include(values):
            let routes = values.compactMap(makeIPv6Route(from:))
            return (routes.isEmpty ? [NEIPv6Route.default()] : routes, [])
        case let .exclude(values):
            return ([NEIPv6Route.default()], values.compactMap(makeIPv6Route(from:)))
        }
    }

    private func makeIPv4Route(from value: String) -> NEIPv4Route? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        let parts = trimmed.split(separator: "/", omittingEmptySubsequences: false)
        guard let address = parts.first.map(String.init), !address.isEmpty, !address.contains(":") else {
            return nil
        }

        let prefixLength = parts.count > 1 ? Int(parts[1]) ?? 32 : 32
        guard (0...32).contains(prefixLength) else {
            return nil
        }
        return NEIPv4Route(destinationAddress: address, subnetMask: ipv4SubnetMask(prefixLength: prefixLength))
    }

    private func makeIPv6Route(from value: String) -> NEIPv6Route? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        let parts = trimmed.split(separator: "/", omittingEmptySubsequences: false)
        guard let address = parts.first.map(String.init), address.contains(":") else {
            return nil
        }

        let prefixLength = parts.count > 1 ? Int(parts[1]) ?? 128 : 128
        guard (0...128).contains(prefixLength) else {
            return nil
        }
        return NEIPv6Route(destinationAddress: address, networkPrefixLength: NSNumber(value: prefixLength))
    }

    private func ipv4SubnetMask(prefixLength: Int) -> String {
        guard prefixLength > 0 else {
            return "0.0.0.0"
        }

        let mask = prefixLength == 32 ? UInt32.max : ~UInt32(0) << (32 - prefixLength)
        let octets = [
            String((mask >> 24) & 0xFF),
            String((mask >> 16) & 0xFF),
            String((mask >> 8) & 0xFF),
            String(mask & 0xFF),
        ]
        return octets.joined(separator: ".")
    }

    private static func elapsedMilliseconds(since start: DispatchTime) -> Int {
        let elapsedNs = DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds
        return Int(elapsedNs / 1_000_000)
    }

    private static func isCleanExternalStartError(_ error: Error) -> Bool {
        let nsError = error as NSError
        return nsError.domain == "internet" && nsError.code == 400
    }
}
