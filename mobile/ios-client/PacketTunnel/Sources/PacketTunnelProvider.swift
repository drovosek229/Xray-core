import Foundation
import Network
import NetworkExtension

private struct LocalRuntimeGeneration {
    let id: UUID
    let bridge: XrayEngineBridge
    let tun2Socks: Tun2SocksBridge
}

private struct LocalRuntimeStartResult {
    let generation: LocalRuntimeGeneration
    let performance: TunnelPerformanceTimings
}

private struct LocalRuntimeStartFailure: Error {
    let reason: TunnelLocalFailureReason
    let message: String
    let performance: TunnelPerformanceTimings
}

private struct ProviderSupervisorState {
    var activeConfiguration: TunnelProviderConfigurationEnvelope?
    var currentGeneration: LocalRuntimeGeneration?
    var isStopping = false
    var isRecovering = false
    var recoveryAttempt = 0
    var hasEverConnected = false
    var lastHealthyAt: Date?
    var healthCheckInFlight = false
    var healthTimer: DispatchSourceTimer?
}

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logStore = LogStore()
    private let tunnelSessionStore = TunnelSessionStore()
    private let recoveryPolicy = TunnelRecoveryPolicy.default
    private let stateQueue = DispatchQueue(label: "internet.packet-tunnel.state")
    private let healthQueue = DispatchQueue(label: "internet.packet-tunnel.health")
    private let pathMonitorQueue = DispatchQueue(label: "internet.packet-tunnel.path")

    private var supervisorState = ProviderSupervisorState()
    private var pathMonitor: NWPathMonitor?

    override func startTunnel(
        options _: [String: NSObject]?,
        completionHandler: @escaping (Error?) -> Void
    ) {
        stateQueue.sync {
            supervisorState = ProviderSupervisorState()
            supervisorState.isStopping = false
        }
        startPathMonitorIfNeeded()

        do {
            let providerConfiguration = try loadProviderConfigurationEnvelope()
            stateQueue.sync {
                supervisorState.activeConfiguration = providerConfiguration
            }
            writeRuntimeState(
                for: providerConfiguration,
                phase: .starting,
                runtimeStage: .startup,
                lastKnownSystemStatus: .connecting
            )
            applyInitialTunnelSettings(
                for: providerConfiguration,
                completionHandler: completionHandler
            )
        } catch {
            let runtimeState: TunnelRuntimeState
            if Self.isCleanExternalStartError(error) {
                runtimeState = TunnelRuntimeState(
                    phase: .idle,
                    stopReason: TunnelStopReason.none,
                    stopOrigin: .system,
                    lastKnownSystemStatus: .disconnected
                )
                logStore.append("Ignored tunnel start without persisted runtime configuration: \(error.localizedDescription)")
            } else {
                runtimeState = TunnelRuntimeState(
                    phase: .failed,
                    runtimeStage: .startup,
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
            supervisorState.isStopping = true
            stopHealthMonitorLocked()
            stopCurrentRuntimeLocked()
        }
        stopPathMonitor()

        let previousState = try? tunnelSessionStore.loadRuntimeState()
        let stopReason = TunnelStopReason(rawValue: reason.rawValue) ?? .unknown
        let classification = stopReason.classify(previousState: previousState)

        try? tunnelSessionStore.saveRuntimeState(
            TunnelRuntimeState(
                sessionID: previousState?.sessionID,
                activeTunnelTarget: previousState?.activeTunnelTarget,
                targetName: previousState?.targetName,
                phase: classification.phase,
                runtimeStage: previousState?.runtimeStage,
                createdAt: previousState?.createdAt ?? Date(),
                startedAt: previousState?.startedAt,
                lastError: classification.phase == .failed
                    ? previousState?.lastError ?? stopReason.fallbackErrorDescription
                    : nil,
                configHash: previousState?.configHash,
                performance: previousState?.performance,
                stopReason: stopReason,
                stopOrigin: classification.origin,
                lastKnownSystemStatus: .disconnected,
                recoveryAttempt: previousState?.recoveryAttempt,
                lastRecoveryTrigger: previousState?.lastRecoveryTrigger,
                lastHealthyAt: previousState?.lastHealthyAt
            )
        )
        logStore.append("Tunnel stopped with reason \(stopReason.rawValue)")
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        let version = stateQueue.sync { supervisorState.currentGeneration?.bridge.version() } ?? XrayEngineBridge().version()
        completionHandler?("internet core \(version)".data(using: .utf8))
    }

    private func applyInitialTunnelSettings(
        for providerConfiguration: TunnelProviderConfigurationEnvelope,
        completionHandler: @escaping (Error?) -> Void
    ) {
        let settings = makeTunnelSettings(routePolicy: providerConfiguration.routePolicy)
        let settingsStartedAt = DispatchTime.now()
        setTunnelNetworkSettings(settings) { [weak self] error in
            guard let self else {
                completionHandler(nil)
                return
            }

            let settingsDurationMs = Self.elapsedMilliseconds(since: settingsStartedAt)
            self.stateQueue.async {
                if let error {
                    self.writeRuntimeState(
                        for: providerConfiguration,
                        phase: .failed,
                        runtimeStage: .startup,
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
                    let result = try self.startFreshLocalRuntime(
                        for: providerConfiguration,
                        runtimeStage: .startup,
                        basePerformance: TunnelPerformanceTimings(
                            setTunnelNetworkSettingsMs: settingsDurationMs
                        )
                    )
                    let startedAt = Date()
                    self.supervisorState.hasEverConnected = true
                    self.supervisorState.lastHealthyAt = startedAt
                    self.startHealthMonitorLocked()
                    self.writeRuntimeState(
                        for: providerConfiguration,
                        phase: .connected,
                        runtimeStage: .steadyState,
                        startedAt: startedAt,
                        lastError: nil,
                        performance: result.performance,
                        lastKnownSystemStatus: .connected,
                        recoveryAttempt: 0,
                        lastHealthyAt: startedAt
                    )
                    self.logStore.append(
                        "Tunnel started with \(providerConfiguration.targetName) "
                            + "[settings \(settingsDurationMs)ms, validate \(result.performance.configValidateMs ?? 0)ms, "
                            + "engine \(result.performance.xrayEngineStartMs ?? 0)ms, tun2socks \(result.performance.tun2SocksStartMs ?? 0)ms]"
                    )
                    completionHandler(nil)
                } catch let failure as LocalRuntimeStartFailure {
                    self.writeRuntimeState(
                        for: providerConfiguration,
                        phase: .failed,
                        runtimeStage: .startup,
                        lastError: failure.message,
                        performance: failure.performance,
                        stopOrigin: .launchFailure,
                        lastKnownSystemStatus: .invalid,
                        lastRecoveryTrigger: failure.reason
                    )
                    self.logStore.append("Tunnel start failed: \(failure.message)")
                    completionHandler(failure)
                } catch {
                    self.writeRuntimeState(
                        for: providerConfiguration,
                        phase: .failed,
                        runtimeStage: .startup,
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
    }

    private func startFreshLocalRuntime(
        for providerConfiguration: TunnelProviderConfigurationEnvelope,
        runtimeStage: TunnelRuntimeStage,
        basePerformance: TunnelPerformanceTimings = TunnelPerformanceTimings()
    ) throws -> LocalRuntimeStartResult {
        stopCurrentRuntimeLocked()

        let bridge = XrayEngineBridge()
        let tun2Socks = Tun2SocksBridge()
        let generation = LocalRuntimeGeneration(
            id: UUID(),
            bridge: bridge,
            tun2Socks: tun2Socks
        )

        var performance = basePerformance

        let validateStartedAt = DispatchTime.now()
        do {
            try bridge.validate(configJSON: providerConfiguration.runtimeConfigJSON)
        } catch {
            performance.configValidateMs = Self.elapsedMilliseconds(since: validateStartedAt)
            throw LocalRuntimeStartFailure(
                reason: .xrayStartFailed,
                message: error.localizedDescription,
                performance: performance
            )
        }
        performance.configValidateMs = Self.elapsedMilliseconds(since: validateStartedAt)

        let engineStartedAt = DispatchTime.now()
        do {
            try bridge.start(
                configJSON: providerConfiguration.runtimeConfigJSON,
                tunFD: -1,
                assetDir: Bundle.main.resourceURL?.path ?? ""
            )
        } catch {
            performance.xrayEngineStartMs = Self.elapsedMilliseconds(since: engineStartedAt)
            throw LocalRuntimeStartFailure(
                reason: .xrayStartFailed,
                message: error.localizedDescription,
                performance: performance
            )
        }
        performance.xrayEngineStartMs = Self.elapsedMilliseconds(since: engineStartedAt)

        let socksReady = Socks5ReadinessProbe.waitUntilReady(
            host: AppConfiguration.localSocksListenAddress,
            port: AppConfiguration.localSocksListenPort,
            timeout: recoveryPolicy.socksReadinessTimeout,
            retryInterval: recoveryPolicy.socksReadinessRetryInterval
        )
        guard socksReady else {
            bridge.stop()
            throw LocalRuntimeStartFailure(
                reason: .socksNotReady,
                message: TunnelLocalFailureReason.socksNotReady.displayName,
                performance: performance
            )
        }

        let tun2SocksStartedAt = DispatchTime.now()
        tun2Socks.start(
            configuration: Tun2SocksConfiguration(
                socksAddress: AppConfiguration.localSocksListenAddress,
                socksPort: AppConfiguration.localSocksListenPort,
                mtu: AppConfiguration.defaultTunnelMTU
            )
        ) { [weak self] code in
            self?.handleTun2SocksExit(code: code, generationID: generation.id)
        }
        performance.tun2SocksStartMs = Self.elapsedMilliseconds(since: tun2SocksStartedAt)

        supervisorState.currentGeneration = generation
        supervisorState.activeConfiguration = providerConfiguration
        if runtimeStage == .recovery {
            supervisorState.isRecovering = true
        }

        return LocalRuntimeStartResult(
            generation: generation,
            performance: performance
        )
    }

    private func handleTun2SocksExit(code: Int32, generationID: UUID) {
        stateQueue.async {
            guard !self.supervisorState.isStopping else {
                self.logStore.append("tun2socks exited during tunnel shutdown with code \(code)")
                return
            }
            guard self.supervisorState.currentGeneration?.id == generationID else {
                return
            }

            let message = code == 0
                ? "tun2socks exited unexpectedly."
                : "tun2socks exited with code \(code)."
            self.beginRecovery(
                trigger: .tun2SocksExited,
                message: message
            )
        }
    }

    private func beginRecovery(trigger: TunnelLocalFailureReason, message: String) {
        guard !supervisorState.isStopping else {
            return
        }
        guard let providerConfiguration = supervisorState.activeConfiguration else {
            failAndTearDown(
                reason: .recoveryBudgetExceeded,
                message: message
            )
            return
        }
        guard !supervisorState.isRecovering else {
            return
        }

        supervisorState.isRecovering = true
        supervisorState.recoveryAttempt = 0
        stopHealthMonitorLocked()
        stopCurrentRuntimeLocked()

        writeRuntimeState(
            for: providerConfiguration,
            phase: .recovering,
            runtimeStage: .recovery,
            lastError: message,
            stopOrigin: .provider,
            lastKnownSystemStatus: .connected,
            recoveryAttempt: 0,
            lastRecoveryTrigger: trigger,
            lastHealthyAt: supervisorState.lastHealthyAt
        )
        logStore.append("Starting local runtime recovery for \(providerConfiguration.targetName): \(message)")
        scheduleNextRecoveryAttemptLocked(for: providerConfiguration, lastTrigger: trigger)
    }

    private func scheduleNextRecoveryAttemptLocked(
        for providerConfiguration: TunnelProviderConfigurationEnvelope,
        lastTrigger: TunnelLocalFailureReason
    ) {
        let attempt = supervisorState.recoveryAttempt + 1
        guard let backoff = recoveryPolicy.backoff(forAttempt: attempt) else {
            failAndTearDown(
                reason: .recoveryBudgetExceeded,
                message: "Local runtime recovery failed after \(supervisorState.recoveryAttempt) attempts."
            )
            return
        }

        supervisorState.recoveryAttempt = attempt
        writeRuntimeState(
            for: providerConfiguration,
            phase: .recovering,
            runtimeStage: .recovery,
            lastError: "Recovering local runtime (attempt \(attempt)/\(recoveryPolicy.maxRecoveryAttempts)).",
            stopOrigin: .provider,
            lastKnownSystemStatus: .connected,
            recoveryAttempt: attempt,
            lastRecoveryTrigger: lastTrigger,
            lastHealthyAt: supervisorState.lastHealthyAt
        )

        stateQueue.asyncAfter(deadline: .now() + backoff) { [weak self] in
            self?.performRecoveryAttempt(
                for: providerConfiguration,
                attempt: attempt,
                trigger: lastTrigger
            )
        }
    }

    private func performRecoveryAttempt(
        for providerConfiguration: TunnelProviderConfigurationEnvelope,
        attempt: Int,
        trigger: TunnelLocalFailureReason
    ) {
        guard !supervisorState.isStopping else {
            return
        }
        guard supervisorState.isRecovering else {
            return
        }
        guard supervisorState.activeConfiguration?.sessionID == providerConfiguration.sessionID else {
            return
        }

        do {
            let result = try startFreshLocalRuntime(
                for: providerConfiguration,
                runtimeStage: .recovery
            )
            let now = Date()
            supervisorState.isRecovering = false
            supervisorState.recoveryAttempt = 0
            supervisorState.hasEverConnected = true
            supervisorState.lastHealthyAt = now
            startHealthMonitorLocked()
            writeRuntimeState(
                for: providerConfiguration,
                phase: .connected,
                runtimeStage: .steadyState,
                lastError: nil,
                performance: result.performance,
                lastKnownSystemStatus: .connected,
                recoveryAttempt: 0,
                lastRecoveryTrigger: trigger,
                lastHealthyAt: now
            )
            logStore.append("Recovered local runtime for \(providerConfiguration.targetName) on attempt \(attempt).")
        } catch let failure as LocalRuntimeStartFailure {
            logStore.append("Recovery attempt \(attempt) failed: \(failure.message)")
            writeRuntimeState(
                for: providerConfiguration,
                phase: .recovering,
                runtimeStage: .recovery,
                lastError: failure.message,
                performance: failure.performance,
                stopOrigin: .provider,
                lastKnownSystemStatus: .connected,
                recoveryAttempt: attempt,
                lastRecoveryTrigger: failure.reason,
                lastHealthyAt: supervisorState.lastHealthyAt
            )
            scheduleNextRecoveryAttemptLocked(for: providerConfiguration, lastTrigger: failure.reason)
        } catch {
            failAndTearDown(
                reason: .recoveryBudgetExceeded,
                message: error.localizedDescription
            )
        }
    }

    private func performHealthCheck() {
        stateQueue.async {
            guard !self.supervisorState.isStopping else {
                return
            }
            guard !self.supervisorState.isRecovering else {
                return
            }
            guard !self.supervisorState.healthCheckInFlight else {
                return
            }
            guard let providerConfiguration = self.supervisorState.activeConfiguration,
                  let generation = self.supervisorState.currentGeneration
            else {
                return
            }

            self.supervisorState.healthCheckInFlight = true
            self.healthQueue.async { [weak self] in
                guard let self else {
                    return
                }
                let engineRunning = generation.bridge.isRunning

                self.stateQueue.async {
                    self.supervisorState.healthCheckInFlight = false
                    guard !self.supervisorState.isStopping else {
                        return
                    }
                    guard self.supervisorState.currentGeneration?.id == generation.id else {
                        return
                    }

                    if engineRunning {
                        let now = Date()
                        self.supervisorState.lastHealthyAt = now
                        self.writeRuntimeState(
                            for: providerConfiguration,
                            phase: .connected,
                            runtimeStage: .steadyState,
                            lastError: nil,
                            lastKnownSystemStatus: .connected,
                            recoveryAttempt: 0,
                            lastHealthyAt: now
                        )
                        return
                    }

                    self.beginRecovery(
                        trigger: .xrayRuntimeStopped,
                        message: TunnelLocalFailureReason.xrayRuntimeStopped.displayName
                    )
                }
            }
        }
    }

    private func failAndTearDown(reason: TunnelLocalFailureReason, message: String) {
        guard let providerConfiguration = supervisorState.activeConfiguration else {
            cancelTunnelWithError(
                NSError(
                    domain: "internet",
                    code: 503,
                    userInfo: [NSLocalizedDescriptionKey: message]
                )
            )
            return
        }

        supervisorState.isRecovering = false
        stopHealthMonitorLocked()
        stopCurrentRuntimeLocked()
        writeRuntimeState(
            for: providerConfiguration,
            phase: .failed,
            runtimeStage: .recovery,
            lastError: message,
            stopOrigin: .provider,
            lastKnownSystemStatus: .disconnecting,
            recoveryAttempt: supervisorState.recoveryAttempt,
            lastRecoveryTrigger: reason,
            lastHealthyAt: supervisorState.lastHealthyAt
        )
        logStore.append("Tearing down tunnel after recovery failure: \(message)")
        cancelTunnelWithError(
            NSError(
                domain: "internet",
                code: 503,
                userInfo: [NSLocalizedDescriptionKey: message]
            )
        )
    }

    private func startHealthMonitorLocked() {
        stopHealthMonitorLocked()

        let timer = DispatchSource.makeTimerSource(queue: stateQueue)
        timer.schedule(
            deadline: .now() + recoveryPolicy.healthCheckInterval,
            repeating: recoveryPolicy.healthCheckInterval
        )
        timer.setEventHandler { [weak self] in
            self?.performHealthCheck()
        }
        supervisorState.healthTimer = timer
        timer.resume()
    }

    private func stopHealthMonitorLocked() {
        supervisorState.healthTimer?.cancel()
        supervisorState.healthTimer = nil
        supervisorState.healthCheckInFlight = false
    }

    private func stopCurrentRuntimeLocked() {
        let generation = supervisorState.currentGeneration
        supervisorState.currentGeneration = nil
        generation?.tun2Socks.stop()
        generation?.bridge.stop()
    }

    private func startPathMonitorIfNeeded() {
        guard pathMonitor == nil else {
            return
        }

        let monitor = NWPathMonitor()
        monitor.pathUpdateHandler = { [weak self] path in
            let status: String
            switch path.status {
            case .satisfied:
                status = "satisfied"
            case .requiresConnection:
                status = "requires-connection"
            case .unsatisfied:
                status = "unsatisfied"
            @unknown default:
                status = "unknown"
            }
            let interfaces = path.availableInterfaces.map(\.debugDescription).joined(separator: ",")
            self?.logStore.append("Network path update: \(status) [\(interfaces)]")
        }
        monitor.start(queue: pathMonitorQueue)
        pathMonitor = monitor
    }

    private func stopPathMonitor() {
        pathMonitor?.cancel()
        pathMonitor = nil
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
        runtimeStage: TunnelRuntimeStage? = nil,
        startedAt: Date? = nil,
        lastError: String? = nil,
        performance: TunnelPerformanceTimings? = nil,
        stopOrigin: TunnelStopOrigin? = nil,
        lastKnownSystemStatus: TunnelSystemStatus,
        recoveryAttempt: Int? = nil,
        lastRecoveryTrigger: TunnelLocalFailureReason? = nil,
        lastHealthyAt: Date? = nil
    ) {
        do {
            try tunnelSessionStore.updateRuntimeState { state in
                guard state.sessionID == nil || state.sessionID == providerConfiguration.sessionID else {
                    return
                }
                state.sessionID = providerConfiguration.sessionID
                state.activeTunnelTarget = providerConfiguration.activeTunnelTarget
                state.targetName = providerConfiguration.targetName
                state.phase = phase
                state.runtimeStage = runtimeStage ?? state.runtimeStage
                state.startedAt = startedAt ?? state.startedAt
                state.lastError = lastError
                state.configHash = providerConfiguration.configHash
                state.stopReason = nil
                state.stopOrigin = stopOrigin
                state.lastKnownSystemStatus = lastKnownSystemStatus
                state.recoveryAttempt = recoveryAttempt ?? state.recoveryAttempt
                state.lastRecoveryTrigger = lastRecoveryTrigger ?? state.lastRecoveryTrigger
                state.lastHealthyAt = lastHealthyAt ?? state.lastHealthyAt
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
                    runtimeStage: runtimeStage,
                    startedAt: startedAt,
                    lastError: lastError,
                    configHash: providerConfiguration.configHash,
                    performance: performance,
                    stopOrigin: stopOrigin,
                    lastKnownSystemStatus: lastKnownSystemStatus,
                    recoveryAttempt: recoveryAttempt,
                    lastRecoveryTrigger: lastRecoveryTrigger,
                    lastHealthyAt: lastHealthyAt
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
