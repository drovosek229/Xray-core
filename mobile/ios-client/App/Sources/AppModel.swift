import Foundation
import NetworkExtension
import XrayAppCore

@MainActor
final class AppModel: ObservableObject {
    @Published var manualProfiles: [ManualProfile] = []
    @Published var subscriptionSources: [SubscriptionSource] = []
    @Published var subscriptionEndpoints: [SubscriptionEndpoint] = []
    @Published var activeTunnelTarget: ProfileReference?
    @Published var tunnelState: NEVPNStatus = .disconnected
    @Published var tunnelStatus: String = TunnelRuntimePhase.idle.displayName
    @Published var tunnelPhase: TunnelRuntimePhase = .idle
    @Published var tunnelRuntimeState: TunnelRuntimeState?
    @Published var connectionStartedAt: Date?
    @Published var collapsedSectionIDs: Set<String> = []
    @Published var latencyRecords: [String: ProfileLatencyRecord] = [:]
    @Published var isRefreshingSubscriptions = false
    @Published var isTestingLatency = false
    @Published var errorMessage: String?
    @Published var logLines: [String] = []
    @Published var latestBenchmarkResult: TunnelBenchmarkResult?

    private let repository: ProfileRepository
    private let logStore: LogStore
    private let tunnelManager: TunnelManager
    private let preferencesStore: ClientPreferencesStore
    private let latencyStore: ProfileLatencyStore
    private let tunnelSessionStore: TunnelSessionStore
    private let benchmarkStore: BenchmarkStore
    private lazy var subscriptionSyncController = SubscriptionSyncController(
        repository: repository,
        logStore: logStore
    )

    init(
        repository: ProfileRepository = ProfileRepository(),
        logStore: LogStore = LogStore(),
        tunnelManager: TunnelManager? = nil,
        preferencesStore: ClientPreferencesStore = ClientPreferencesStore(),
        latencyStore: ProfileLatencyStore = ProfileLatencyStore(),
        tunnelSessionStore: TunnelSessionStore = TunnelSessionStore(),
        benchmarkStore: BenchmarkStore = BenchmarkStore()
    ) {
        self.repository = repository
        self.logStore = logStore
        self.tunnelManager = tunnelManager ?? TunnelManager()
        self.preferencesStore = preferencesStore
        self.latencyStore = latencyStore
        self.tunnelSessionStore = tunnelSessionStore
        self.benchmarkStore = benchmarkStore
        self.tunnelManager.onStatusChange = { [weak self] status, connectedDate in
            Task { @MainActor in
                await self?.handleTunnelStatusChange(status, connectedDate: connectedDate)
            }
        }
    }

    func load() async {
        do {
            manualProfiles = try repository.loadManualProfiles()
            subscriptionSources = try repository.loadSubscriptionSources()
            subscriptionEndpoints = try repository.loadSubscriptionEndpoints()
            activeTunnelTarget = try repository.activeTunnelTarget()
            collapsedSectionIDs = try preferencesStore.loadCollapsedSectionIDs()
            latencyRecords = try latencyStore.loadRecords()
            tunnelRuntimeState = try tunnelSessionStore.loadRuntimeState()
            latestBenchmarkResult = try benchmarkStore.loadLatestResult()

            try reconcileActiveTunnelTarget()
            let snapshot = try await tunnelManager.loadOrCreateManager()
            if snapshot.reprovisioned {
                appendLog("Reconciled the VPN configuration.")
            }
            await handleTunnelStatusChange(
                snapshot.systemStatus.networkExtensionStatus,
                connectedDate: snapshot.connectedDate
            )

            await refreshLatenciesIfNeeded()
        } catch {
            setError(error)
        }
    }

    func handleSceneDidBecomeActive() async {
        do {
            let snapshot = try await tunnelManager.reconcileForForeground()
            if snapshot.reprovisioned {
                appendLog("Repaired the VPN configuration after returning to the app.")
            }
            await handleTunnelStatusChange(
                snapshot.systemStatus.networkExtensionStatus,
                connectedDate: snapshot.connectedDate
            )
        } catch {
            appendLog("Failed to reconcile VPN configuration: \(error.localizedDescription)")
        }
    }

    @discardableResult
    func addManualProfile(_ profile: ManualProfile) -> Bool {
        do {
            _ = try RuntimeConfigBuilder.build(for: profile)
            try repository.saveManualProfile(profile)
            appendLog("Saved manual profile \(profile.name)")
            Task {
                await reloadDataAndRetestLatency()
            }
            return true
        } catch {
            setError(error)
            return false
        }
    }

    func deleteManualProfile(_ profile: ManualProfile) {
        do {
            try repository.deleteManualProfile(profile.id)
            latencyRecords.removeValue(forKey: profile.id.uuidString.lowercased())
            try latencyStore.saveRecords(latencyRecords)
            appendLog("Deleted manual profile \(profile.name)")
            Task {
                await load()
            }
        } catch {
            setError(error)
        }
    }

    @discardableResult
    func addSubscription(name: String, urlString: String) async -> Bool {
        do {
            try await subscriptionSyncController.importSource(name: name, urlString: urlString)
            await reloadDataAndRetestLatency()
            return true
        } catch {
            setError(error)
            return false
        }
    }

    func refresh(source: SubscriptionSource) async {
        do {
            isRefreshingSubscriptions = true
            defer { isRefreshingSubscriptions = false }
            try await subscriptionSyncController.refresh(sourceID: source.id)
            await reloadDataAndRetestLatency()
        } catch {
            isRefreshingSubscriptions = false
            setError(error)
        }
    }

    func refreshAllSubscriptions() async {
        isRefreshingSubscriptions = true
        defer { isRefreshingSubscriptions = false }

        do {
            for source in subscriptionSources {
                try await subscriptionSyncController.refresh(sourceID: source.id)
            }
            await reloadDataAndRetestLatency()
        } catch {
            setError(error)
        }
    }

    func deleteSubscription(_ source: SubscriptionSource) {
        do {
            let endpointIDs = Set(
                subscriptionEndpoints
                    .filter { $0.sourceID == source.id }
                    .map { $0.id.uuidString.lowercased() }
            )
            try repository.deleteSubscriptionSource(source.id)
            latencyRecords = latencyRecords.filter { key, _ in !endpointIDs.contains(key) }
            try latencyStore.saveRecords(latencyRecords)
            appendLog("Deleted subscription \(source.name)")
            Task {
                await load()
            }
        } catch {
            setError(error)
        }
    }

    func select(_ profile: ProfileReference) {
        do {
            try repository.setActiveTunnelTarget(profile)
            activeTunnelTarget = profile
            appendLog("Activated profile \(displayName(for: profile) ?? "unknown")")
        } catch {
            setError(error)
        }
    }

    func connect() async {
        do {
            errorMessage = nil
            tunnelPhase = .preparing
            tunnelStatus = TunnelRuntimePhase.preparing.displayName

            guard let activeTunnelTarget else {
                throw XrayAppCoreError.invalidProfile("Tap a profile before connecting.")
            }

            if case let .subscriptionEndpoint(endpointID) = activeTunnelTarget,
               let endpoint = subscriptionEndpoints.first(where: { $0.id == endpointID }) {
                try await subscriptionSyncController.refreshIfStale(sourceID: endpoint.sourceID)
                subscriptionSources = try repository.loadSubscriptionSources()
                subscriptionEndpoints = try repository.loadSubscriptionEndpoints()
            }

            guard let resolvedProfile = resolvedProfile(for: activeTunnelTarget) else {
                throw XrayAppCoreError.invalidProfile("The active profile is no longer available.")
            }

            let configBuildStartedAt = DispatchTime.now()
            let configJSON: String
            switch resolvedProfile {
            case let .manual(profile):
                configJSON = try RuntimeConfigBuilder.build(
                    for: profile,
                    context: RuntimeConfigContext(
                        dnsServers: AppConfiguration.runtimeDoHServers,
                        localSocksListenAddress: AppConfiguration.localSocksListenAddress,
                        localSocksListenPort: AppConfiguration.localSocksListenPort
                    )
                )
            case let .subscriptionEndpoint(endpoint):
                configJSON = try RuntimeConfigBuilder.build(
                    for: endpoint,
                    context: RuntimeConfigContext(
                        dnsServers: AppConfiguration.runtimeDoHServers,
                        localSocksListenAddress: AppConfiguration.localSocksListenAddress,
                        localSocksListenPort: AppConfiguration.localSocksListenPort
                    )
                )
            }
            let configBuildDurationMs = Self.elapsedMilliseconds(since: configBuildStartedAt)
            let initialPerformance = TunnelPerformanceTimings(configBuildMs: configBuildDurationMs)

            let providerConfiguration = TunnelProviderConfigurationEnvelope(
                activeTunnelTarget: activeTunnelTarget,
                targetName: profileLabel(for: resolvedProfile),
                runtimeConfigJSON: configJSON,
                routePolicy: .disabled
            )
            do {
                let snapshot = try await performConnectAttempt(
                    providerConfiguration: providerConfiguration,
                    forceReprovision: false,
                    initialPerformance: initialPerformance
                )
                if snapshot.reprovisioned {
                    appendLog("Repaired the VPN configuration before connecting \(providerConfiguration.targetName).")
                }
                appendLog(
                    "Requested VPN connection for \(providerConfiguration.targetName) "
                        + "[config \(configBuildDurationMs)ms, reconcile \(snapshot.reconcileDurationMs ?? 0)ms]"
                )
            } catch {
                guard tunnelManager.isRecoverableConfigurationError(error) else {
                    throw error
                }

                appendLog("VPN configuration was stale or disabled. Reprovisioning \(providerConfiguration.targetName) and retrying.")
                let retryConfiguration = TunnelProviderConfigurationEnvelope(
                    activeTunnelTarget: activeTunnelTarget,
                    targetName: providerConfiguration.targetName,
                    runtimeConfigJSON: configJSON,
                    routePolicy: .disabled
                )
                let snapshot = try await performConnectAttempt(
                    providerConfiguration: retryConfiguration,
                    forceReprovision: true,
                    initialPerformance: initialPerformance
                )
                if snapshot.reprovisioned {
                    appendLog("Repaired the VPN configuration for \(providerConfiguration.targetName).")
                }
                appendLog(
                    "Requested VPN connection for \(providerConfiguration.targetName) "
                        + "[config \(configBuildDurationMs)ms, reconcile \(snapshot.reconcileDurationMs ?? 0)ms]"
                )
            }
        } catch {
            let runtimeState = TunnelRuntimeState(
                activeTunnelTarget: activeTunnelTarget,
                targetName: activeTunnelTarget.flatMap { displayName(for: $0) },
                phase: .failed,
                lastError: error.localizedDescription,
                stopOrigin: .launchFailure,
                lastKnownSystemStatus: .invalid
            )
            try? tunnelSessionStore.saveRuntimeState(runtimeState)
            tunnelRuntimeState = runtimeState
            tunnelPhase = .failed
            tunnelStatus = TunnelRuntimePhase.failed.displayName
            setError(error)
        }
    }

    func disconnect() async {
        do {
            tunnelPhase = .stopping
            tunnelStatus = TunnelRuntimePhase.stopping.displayName
            let runtimeState = TunnelRuntimeState(
                sessionID: tunnelRuntimeState?.sessionID,
                activeTunnelTarget: activeTunnelTarget,
                targetName: activeTunnelTarget.flatMap { displayName(for: $0) },
                phase: .stopping,
                createdAt: tunnelRuntimeState?.createdAt ?? Date(),
                startedAt: tunnelRuntimeState?.startedAt,
                lastError: nil,
                configHash: tunnelRuntimeState?.configHash,
                stopOrigin: .app,
                lastKnownSystemStatus: .disconnecting
            )
            try? tunnelSessionStore.saveRuntimeState(runtimeState)
            tunnelRuntimeState = runtimeState
            let snapshot = try await tunnelManager.disconnect()
            if snapshot.systemStatus.isDisconnectedLike || !snapshot.managerAvailable {
                errorMessage = nil
                tunnelState = .disconnected
                connectionStartedAt = nil
                tunnelPhase = .idle
                tunnelStatus = TunnelRuntimePhase.idle.displayName
            }
            appendLog("Requested VPN disconnect")
        } catch {
            setError(error)
        }
    }

    func toggleSectionCollapsed(_ sectionID: String) {
        do {
            if collapsedSectionIDs.contains(sectionID) {
                collapsedSectionIDs.remove(sectionID)
            } else {
                collapsedSectionIDs.insert(sectionID)
            }
            try preferencesStore.saveCollapsedSectionIDs(collapsedSectionIDs)
        } catch {
            setError(error)
        }
    }

    func isSectionCollapsed(_ sectionID: String) -> Bool {
        collapsedSectionIDs.contains(sectionID)
    }

    func testAllLatencies(force: Bool = true) async {
        let resolved = allResolvedProfiles()
        guard !resolved.isEmpty else {
            return
        }

        let targets = LatencyProber.targets(from: resolved).filter { target in
            force || latencyIsStale(for: target.id)
        }
        guard !targets.isEmpty else {
            return
        }

        isTestingLatency = true
        defer { isTestingLatency = false }

        let results = await LatencyProber.probe(targets: targets)
        for (id, record) in results {
            latencyRecords[id.uuidString.lowercased()] = record
        }

        do {
            try latencyStore.saveRecords(latencyRecords)
            appendLog("Measured latency for \(results.count) profile\(results.count == 1 ? "" : "s")")
        } catch {
            setError(error)
        }
    }

    func testLatency(for reference: ProfileReference) async {
        guard let resolved = resolvedProfile(for: reference) else {
            return
        }
        await updateLatency(for: LatencyProber.targets(from: [resolved]), logLabel: profileLabel(for: resolved))
    }

    func testLatencyForManualProfiles() async {
        let profiles = sortedManualProfiles().map(ResolvedProfile.manual)
        await updateLatency(for: LatencyProber.targets(from: profiles), logLabel: "Local Profiles")
    }

    func testLatency(forSourceID sourceID: UUID) async {
        let profiles = sortedEndpoints(for: sourceID).map(ResolvedProfile.subscriptionEndpoint)
        let label = source(for: sourceID)?.name ?? "Subscription"
        await updateLatency(for: LatencyProber.targets(from: profiles), logLabel: label)
    }

    func latencyRecord(for profileID: UUID) -> ProfileLatencyRecord {
        latencyRecords[profileID.uuidString.lowercased()] ?? .idle
    }

    func latencyText(for profileID: UUID) -> String {
        let record = latencyRecord(for: profileID)
        switch record.state {
        case .idle:
            return "--"
        case .available:
            if let latencyMs = record.latencyMs {
                return "\(latencyMs)ms"
            }
            return "--"
        case .failed:
            return "Fail"
        }
    }

    func latencyAccessibilityLabel(for profileID: UUID) -> String {
        let record = latencyRecord(for: profileID)
        switch record.state {
        case .idle:
            return "Latency not measured"
        case .available:
            return record.latencyMs.map { "\($0) milliseconds" } ?? "Latency measured"
        case .failed:
            return record.detail ?? "Latency check failed"
        }
    }

    func displayName(for reference: ProfileReference) -> String? {
        switch reference {
        case let .manual(id):
            return manualProfiles.first(where: { $0.id == id })?.name
        case let .subscriptionEndpoint(id):
            return subscriptionEndpoints.first(where: { $0.id == id })?.displayName
        }
    }

    func resolvedProfile(for reference: ProfileReference) -> ResolvedProfile? {
        switch reference {
        case let .manual(id):
            guard let profile = manualProfiles.first(where: { $0.id == id }) else {
                return nil
            }
            return .manual(profile)
        case let .subscriptionEndpoint(id):
            guard let endpoint = subscriptionEndpoints.first(where: { $0.id == id }) else {
                return nil
            }
            return .subscriptionEndpoint(endpoint)
        }
    }

    func source(for id: UUID) -> SubscriptionSource? {
        subscriptionSources.first(where: { $0.id == id })
    }

    func subtitle(for profile: ResolvedProfile) -> String {
        switch profile {
        case let .manual(manual):
            return "VLESS / XHTTP / \(manual.securityKind.displayName)"
        case let .subscriptionEndpoint(endpoint):
            return "VLESS / XHTTP / \(endpoint.securityKind.displayName)"
        }
    }

    func summaryDetail(for profile: ResolvedProfile) -> String {
        switch profile {
        case let .manual(manual):
            return "\(manual.serverName) • \(normalizedPath(manual.xhttpPath))"
        case let .subscriptionEndpoint(endpoint):
            return "\(endpoint.serverName) • \(normalizedPath(endpoint.xhttpPath))"
        }
    }

    func currentResolvedActiveTarget() -> ResolvedProfile? {
        guard let activeTunnelTarget else {
            return nil
        }
        return resolvedProfile(for: activeTunnelTarget)
    }

    func sortedManualProfiles() -> [ManualProfile] {
        manualProfiles.sorted { lhs, rhs in
            compare(
                lhsName: lhs.name,
                lhsID: lhs.id,
                rhsName: rhs.name,
                rhsID: rhs.id
            )
        }
    }

    func sortedEndpoints(for sourceID: UUID) -> [SubscriptionEndpoint] {
        subscriptionEndpoints
            .filter { $0.sourceID == sourceID }
            .sorted { lhs, rhs in
                compare(
                    lhsName: lhs.displayName,
                    lhsID: lhs.id,
                    rhsName: rhs.displayName,
                    rhsID: rhs.id
                )
            }
    }

    func reloadLogs() {
        logLines = logStore.readLines()
    }

    func clearLogs() {
        logStore.clear()
        reloadLogs()
    }

    func runBenchmark() async {
        do {
            guard tunnelState == .connected || tunnelState == .reasserting else {
                throw XrayAppCoreError.invalidProfile("Connect the tunnel before running a benchmark.")
            }
            guard let activeProfile = currentResolvedActiveTarget() else {
                throw XrayAppCoreError.invalidProfile("Tap a profile before running a benchmark.")
            }
            guard let benchmarkURL = URL(string: AppConfiguration.benchmarkProbeURLString) else {
                throw XrayAppCoreError.invalidProfile("The benchmark URL is invalid.")
            }

            let result = try await BenchmarkRunner.run(
                url: benchmarkURL,
                timeout: AppConfiguration.benchmarkRequestTimeout,
                targetName: profileLabel(for: activeProfile),
                profileShape: profileShape(for: activeProfile),
                sessionTimings: tunnelRuntimeState?.performance
            )

            latestBenchmarkResult = result
            try benchmarkStore.saveLatestResult(result)
            recordBenchmarkTimings(result.cold)
            appendLog(
                "Benchmark \(result.targetName) [\(result.profileShape)] "
                    + "cold dns \(format(result.cold.dnsLookupMs)), connect \(format(result.cold.outboundConnectMs)), "
                    + "first-byte \(format(result.cold.firstByteMs)), total \(result.cold.totalMs)ms; "
                    + "warm dns \(format(result.warm.dnsLookupMs)), connect \(format(result.warm.outboundConnectMs)), "
                    + "first-byte \(format(result.warm.firstByteMs)), total \(result.warm.totalMs)ms"
            )
        } catch {
            setError(error)
        }
    }

    private func reloadDataAndRetestLatency() async {
        manualProfiles = (try? repository.loadManualProfiles()) ?? manualProfiles
        subscriptionSources = (try? repository.loadSubscriptionSources()) ?? subscriptionSources
        subscriptionEndpoints = (try? repository.loadSubscriptionEndpoints()) ?? subscriptionEndpoints
        activeTunnelTarget = (try? repository.activeTunnelTarget()) ?? activeTunnelTarget
        do {
            try reconcileActiveTunnelTarget()
        } catch {
            setError(error)
        }
        await testAllLatencies(force: true)
    }

    private func refreshLatenciesIfNeeded() async {
        guard allResolvedProfiles().contains(where: { latencyIsStale(for: $0.id) }) else {
            return
        }
        await testAllLatencies(force: false)
    }

    private func latencyIsStale(for profileID: UUID) -> Bool {
        let record = latencyRecord(for: profileID)
        guard let measuredAt = record.measuredAt else {
            return true
        }
        return Date().timeIntervalSince(measuredAt) >= AppConfiguration.latencyRefreshInterval
    }

    private func allResolvedProfiles() -> [ResolvedProfile] {
        let manual = manualProfiles.map(ResolvedProfile.manual)
        let imported = subscriptionEndpoints.map(ResolvedProfile.subscriptionEndpoint)
        return manual + imported
    }

    private func reconcileActiveTunnelTarget() throws {
        let allReferences: [ProfileReference] =
            manualProfiles.map { .manual($0.id) }
            + subscriptionEndpoints.map { .subscriptionEndpoint($0.id) }

        guard !allReferences.isEmpty else {
            try repository.clearActiveTunnelTarget()
            activeTunnelTarget = nil
            return
        }

        guard let activeTunnelTarget else {
            return
        }

        guard allReferences.contains(activeTunnelTarget) else {
            try repository.clearActiveTunnelTarget()
            self.activeTunnelTarget = nil
            return
        }
    }

    private func compare(
        lhsName: String,
        lhsID: UUID,
        rhsName: String,
        rhsID: UUID
    ) -> Bool {
        let lhsLatency = latencyRecord(for: lhsID).latencyMs ?? Int.max
        let rhsLatency = latencyRecord(for: rhsID).latencyMs ?? Int.max
        if lhsLatency == rhsLatency {
            return lhsName.localizedCaseInsensitiveCompare(rhsName) == .orderedAscending
        }
        return lhsLatency < rhsLatency
    }

    private func setError(_ error: Error) {
        errorMessage = error.localizedDescription
        appendLog("Error: \(error.localizedDescription)")
    }

    private func normalizedPath(_ path: String) -> String {
        let trimmed = path.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "/" : trimmed
    }

    private func updateLatency(
        for targets: [ProfileLatencyTarget],
        logLabel: String
    ) async {
        guard !targets.isEmpty else {
            return
        }

        isTestingLatency = true
        defer { isTestingLatency = false }

        let results = await LatencyProber.probe(targets: targets)
        for (id, record) in results {
            latencyRecords[id.uuidString.lowercased()] = record
        }

        do {
            try latencyStore.saveRecords(latencyRecords)
            appendLog("Measured latency for \(logLabel)")
        } catch {
            setError(error)
        }
    }

    private func appendLog(_ message: String) {
        logStore.append(message)
    }

    private func performConnectAttempt(
        providerConfiguration: TunnelProviderConfigurationEnvelope,
        forceReprovision: Bool,
        initialPerformance: TunnelPerformanceTimings
    ) async throws -> TunnelManagerSnapshot {
        let runtimeState = TunnelRuntimeState(
            sessionID: providerConfiguration.sessionID,
            activeTunnelTarget: providerConfiguration.activeTunnelTarget,
            targetName: providerConfiguration.targetName,
            phase: .preparing,
            configHash: providerConfiguration.configHash,
            performance: initialPerformance,
            lastKnownSystemStatus: .connecting
        )
        try tunnelSessionStore.saveRuntimeState(runtimeState)
        tunnelRuntimeState = runtimeState

        tunnelPhase = .starting
        tunnelStatus = TunnelRuntimePhase.starting.displayName
        let snapshot = try await tunnelManager.connect(
            providerConfiguration: providerConfiguration,
            forceReprovision: forceReprovision
        )
        updateRuntimePerformance(
            for: providerConfiguration.sessionID,
            timings: TunnelPerformanceTimings(managerReconcileMs: snapshot.reconcileDurationMs)
        )
        tunnelRuntimeState = try? tunnelSessionStore.loadRuntimeState()
        return snapshot
    }

    private func handleTunnelStatusChange(_ status: NEVPNStatus, connectedDate: Date?) async {
        tunnelState = status
        connectionStartedAt = connectedDate
        tunnelRuntimeState = try? tunnelSessionStore.loadRuntimeState()

        switch status {
        case .connected, .reasserting:
            errorMessage = nil
            tunnelPhase = .connected
            tunnelStatus = TunnelRuntimePhase.connected.displayName
        case .connecting:
            if tunnelPhase == .preparing {
                tunnelStatus = TunnelRuntimePhase.preparing.displayName
            } else {
                tunnelPhase = .starting
                tunnelStatus = TunnelRuntimePhase.starting.displayName
            }
        case .disconnecting:
            tunnelPhase = .stopping
            tunnelStatus = TunnelRuntimePhase.stopping.displayName
        case .disconnected, .invalid:
            connectionStartedAt = nil
            let startupInFlight = tunnelRuntimeState?.phase == .preparing || tunnelRuntimeState?.phase == .starting
            if startupInFlight {
                errorMessage = nil
                tunnelPhase = tunnelRuntimeState?.phase ?? .starting
                tunnelStatus = tunnelPhase.displayName
            } else if tunnelRuntimeState?.isCleanStop == true || tunnelRuntimeState?.phase == .stopping {
                errorMessage = nil
                tunnelPhase = .idle
                tunnelStatus = TunnelRuntimePhase.idle.displayName
            } else {
                errorMessage = nil
                tunnelPhase = .idle
                tunnelStatus = TunnelRuntimePhase.idle.displayName
            }
        @unknown default:
            tunnelPhase = .failed
            tunnelStatus = "Unknown"
        }
    }

    private func profileLabel(for profile: ResolvedProfile) -> String {
        switch profile {
        case let .manual(manual):
            return manual.name
        case let .subscriptionEndpoint(endpoint):
            return endpoint.displayName
        }
    }

    private func profileShape(for profile: ResolvedProfile) -> String {
        let subtitle = subtitle(for: profile)
        switch profile {
        case let .manual(manual):
            return "\(manual.classification.rawValue) • \(subtitle) • \(manual.xhttpMode.rawValue) • \(manual.normalizedUplinkHTTPMethod) • \(manual.normalizedEncryption)"
        case let .subscriptionEndpoint(endpoint):
            return "\(endpoint.classification.rawValue) • \(subtitle) • \(endpoint.xhttpMode.rawValue) • \(endpoint.normalizedUplinkHTTPMethod) • \(endpoint.normalizedEncryption)"
        }
    }

    private func updateRuntimePerformance(for sessionID: UUID, timings: TunnelPerformanceTimings) {
        do {
            try tunnelSessionStore.updateRuntimeState { state in
                guard state.sessionID == sessionID else {
                    return
                }
                var merged = state.performance ?? TunnelPerformanceTimings()
                merged.merge(from: timings)
                state.performance = merged
            }
        } catch {
            appendLog("Failed to save runtime timings: \(error.localizedDescription)")
        }
    }

    private func recordBenchmarkTimings(_ sample: HTTPBenchmarkSample) {
        guard let sessionID = tunnelRuntimeState?.sessionID else {
            return
        }
        updateRuntimePerformance(
            for: sessionID,
            timings: TunnelPerformanceTimings(
                firstDNSAnswerMs: sample.dnsLookupMs,
                firstOutboundConnectMs: sample.outboundConnectMs,
                firstByteMs: sample.firstByteMs
            )
        )
        tunnelRuntimeState = try? tunnelSessionStore.loadRuntimeState()
    }

    private func format(_ value: Int?) -> String {
        guard let value else {
            return "n/a"
        }
        return "\(value)ms"
    }

    private static func elapsedMilliseconds(since start: DispatchTime) -> Int {
        let elapsedNs = DispatchTime.now().uptimeNanoseconds - start.uptimeNanoseconds
        return Int(elapsedNs / 1_000_000)
    }
}

private extension ResolvedProfile {
    var id: UUID {
        switch self {
        case let .manual(profile):
            return profile.id
        case let .subscriptionEndpoint(endpoint):
            return endpoint.id
        }
    }
}

private extension TunnelSystemStatus {
    var networkExtensionStatus: NEVPNStatus {
        NEVPNStatus(rawValue: rawValue) ?? .invalid
    }
}
