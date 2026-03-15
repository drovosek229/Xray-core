import NetworkExtension
import SwiftUI
import XrayAppCore

private enum RootTab: Hashable {
    case home
    case settings
}

private enum ActiveSheet: String, Identifiable {
    case manualProfile
    case subscriptionImport
    case logs

    var id: String { rawValue }
}

private enum TLSALPNPreset: String, CaseIterable, Identifiable {
    case automatic = "Automatic"
    case h2h3 = "h2 + h3"
    case h2http11 = "h2 + http/1.1"
    case h3Only = "h3 only"
    case http11Only = "http/1.1 only"

    var id: Self { self }

    var values: [String] {
        switch self {
        case .automatic:
            return []
        case .h2h3:
            return ["h2", "h3"]
        case .h2http11:
            return ["h2", "http/1.1"]
        case .h3Only:
            return ["h3"]
        case .http11Only:
            return ["http/1.1"]
        }
    }
}

private enum VLESSEncryptionMode: String, CaseIterable, Identifiable {
    case none = "None"
    case custom = "Custom"

    var id: Self { self }
}

private struct ProfileDetailItem: Identifiable {
    let resolvedProfile: ResolvedProfile
    let source: SubscriptionSource?

    var id: String {
        switch resolvedProfile {
        case let .manual(profile):
            return profile.id.uuidString.lowercased()
        case let .subscriptionEndpoint(endpoint):
            return endpoint.id.uuidString.lowercased()
        }
    }
}

private struct HomeProfileSection: Identifiable {
    let id: String
    let title: String
    let source: SubscriptionSource?
    let rows: [HomeProfileRow]
}

private enum HomeProfileRow: Identifiable {
    case manual(ManualProfile)
    case subscription(SubscriptionEndpoint)

    var id: UUID {
        switch self {
        case let .manual(profile):
            return profile.id
        case let .subscription(endpoint):
            return endpoint.id
        }
    }

    var reference: ProfileReference {
        switch self {
        case let .manual(profile):
            return .manual(profile.id)
        case let .subscription(endpoint):
            return .subscriptionEndpoint(endpoint.id)
        }
    }

    var resolvedProfile: ResolvedProfile {
        switch self {
        case let .manual(profile):
            return .manual(profile)
        case let .subscription(endpoint):
            return .subscriptionEndpoint(endpoint)
        }
    }

    var title: String {
        switch self {
        case let .manual(profile):
            return profile.name
        case let .subscription(endpoint):
            return endpoint.displayName
        }
    }

    var address: String {
        switch self {
        case let .manual(profile):
            return "\(profile.address):\(profile.port)"
        case let .subscription(endpoint):
            return "\(endpoint.address):\(endpoint.port)"
        }
    }
}

private func normalizedDisplayPath(_ path: String) -> String {
    let trimmed = path.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? "/" : trimmed
}

private func encryptionSummaryChip(_ encryption: String) -> String {
    let normalized = encryption.trimmingCharacters(in: .whitespacesAndNewlines)
    if normalized.isEmpty || normalized.lowercased() == "none" {
        return "Enc None"
    }
    if normalized.hasPrefix("mlkem768x25519plus.") {
        return "Enc ML-KEM"
    }
    return "Enc Custom"
}

private func summaryChips(for profile: ResolvedProfile) -> [String] {
    switch profile {
    case let .manual(manual):
        var chips = [
            manual.securityKind.displayName,
            manual.xhttpMode.displayName,
            manual.normalizedUplinkHTTPMethod,
            manual.behaviorProfile.displayName,
            encryptionSummaryChip(manual.normalizedEncryption),
        ]
        if let flow = manual.flow, !flow.isEmpty {
            chips.append(flow)
        }
        return chips
    case let .subscriptionEndpoint(endpoint):
        var chips = [
            endpoint.securityKind.displayName,
            endpoint.xhttpMode.displayName,
            endpoint.normalizedUplinkHTTPMethod,
            endpoint.behaviorProfile.displayName,
            encryptionSummaryChip(endpoint.normalizedEncryption),
        ]
        if let flow = endpoint.flow, !flow.isEmpty {
            chips.append(flow)
        }
        return chips
    }
}

private func profileTitle(_ profile: ResolvedProfile) -> String {
    switch profile {
    case let .manual(manual):
        return manual.name
    case let .subscriptionEndpoint(endpoint):
        return endpoint.displayName
    }
}

private func profileAddress(_ profile: ResolvedProfile) -> String {
    switch profile {
    case let .manual(manual):
        return "\(manual.address):\(manual.port)"
    case let .subscriptionEndpoint(endpoint):
        return "\(endpoint.address):\(endpoint.port)"
    }
}

private func profileServerName(_ profile: ResolvedProfile) -> String {
    switch profile {
    case let .manual(manual):
        return manual.serverName
    case let .subscriptionEndpoint(endpoint):
        return endpoint.serverName
    }
}

private func profilePath(_ profile: ResolvedProfile) -> String {
    switch profile {
    case let .manual(manual):
        return manual.xhttpPath
    case let .subscriptionEndpoint(endpoint):
        return endpoint.xhttpPath
    }
}

private func profileClassification(_ profile: ResolvedProfile) -> ProfileClassification {
    switch profile {
    case let .manual(manual):
        return manual.classification
    case let .subscriptionEndpoint(endpoint):
        return endpoint.classification
    }
}

struct ContentView: View {
    @EnvironmentObject private var model: AppModel
    @State private var selectedTab: RootTab = .home
    @State private var activeSheet: ActiveSheet?
    @State private var detailItem: ProfileDetailItem?

    var body: some View {
        TabView(selection: $selectedTab) {
            HomeView(
                onAddManual: { activeSheet = .manualProfile },
                onAddSubscription: { activeSheet = .subscriptionImport },
                onShowLogs: { activeSheet = .logs },
                onShowDetails: { detailItem = $0 }
            )
            .tag(RootTab.home)
            .tabItem {
                Label("Home", systemImage: "house.fill")
            }

            SettingsView(
                onShowLogs: { activeSheet = .logs }
            )
            .tag(RootTab.settings)
            .tabItem {
                Label("Settings", systemImage: "gearshape.fill")
            }
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case .manualProfile:
                ManualProfileEditorView()
            case .subscriptionImport:
                SubscriptionImportView()
            case .logs:
                LogsView()
            }
        }
        .sheet(item: $detailItem) { item in
            ProfileDetailView(item: item)
        }
        .alert("Error", isPresented: Binding(
            get: { model.errorMessage != nil },
            set: { if !$0 { model.errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(model.errorMessage ?? "")
        }
    }
}

private struct HomeView: View {
    @EnvironmentObject private var model: AppModel

    let onAddManual: () -> Void
    let onAddSubscription: () -> Void
    let onShowLogs: () -> Void
    let onShowDetails: (ProfileDetailItem) -> Void

    @State private var isEditing = false

    private var sections: [HomeProfileSection] {
        var result: [HomeProfileSection] = []

        let localRows = model.sortedManualProfiles().map(HomeProfileRow.manual)
        if !localRows.isEmpty {
            result.append(
                HomeProfileSection(
                    id: "manual",
                    title: "LOCAL PROFILES",
                    source: nil,
                    rows: localRows
                )
            )
        }

        for source in model.subscriptionSources {
            let rows = model.sortedEndpoints(for: source.id).map(HomeProfileRow.subscription)
            guard !rows.isEmpty else {
                continue
            }
            result.append(
                HomeProfileSection(
                    id: source.id.uuidString.lowercased(),
                    title: source.name.uppercased(),
                    source: source,
                    rows: rows
                )
            )
        }

        return result
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    topBar
                    connectionHero
                    sectionsView
                    footerMeta
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
            }
            .background(InternetBackground().ignoresSafeArea())
            .toolbar(.hidden, for: .navigationBar)
        }
    }

    private var topBar: some View {
        HStack {
            Button(isEditing ? "Done" : "Edit") {
                isEditing.toggle()
            }
            .buttonStyle(.plain)
            .font(.system(size: 12, weight: .medium, design: .rounded))
            .foregroundStyle(.white)
            .padding(.horizontal, 14)
            .padding(.vertical, 9)
            .background(Color.white.opacity(0.08))
            .clipShape(Capsule())

            Spacer()

            Menu {
                Button(action: onAddManual) {
                    Label("Manual Profile", systemImage: "square.and.pencil")
                }
                Button(action: onAddSubscription) {
                    Label("Parse Subscription", systemImage: "tray.and.arrow.down")
                }
                Button(action: onShowLogs) {
                    Label("Logs", systemImage: "doc.text")
                }
            } label: {
                Image(systemName: "plus")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(.white)
                    .frame(width: 48, height: 48)
                    .background(Color.white.opacity(0.08))
                    .clipShape(Circle())
            }
        }
    }

    private var connectionHero: some View {
        VStack(spacing: 10) {
            VStack(spacing: 2) {
                Text("Connection Time")
                    .font(.system(size: 10, weight: .medium, design: .rounded))
                    .foregroundStyle(.white.opacity(0.84))
                ConnectionTimerView(startDate: model.connectionStartedAt)
            }

            Button(action: primaryAction) {
                ZStack {
                    RoundedRectangle(cornerRadius: 18, style: .continuous)
                        .fill(Color.white.opacity(0.08))
                        .frame(width: 96, height: 96)

                    Circle()
                        .fill(Color(red: 0.27, green: 0.56, blue: 0.98))
                        .frame(width: 52, height: 52)

                    Image(systemName: powerButtonSymbol)
                        .font(.system(size: 20, weight: .semibold))
                        .foregroundStyle(Color.black.opacity(0.85))
                }
            }
            .buttonStyle(.plain)
            .disabled(primaryActionDisabled)

            StatusPill(
                text: displayedTunnelStatus,
                isConnected: model.tunnelState == .connected || model.tunnelState == .reasserting
            )
        }
        .frame(maxWidth: .infinity)
    }

    @ViewBuilder
    private var sectionsView: some View {
        if sections.isEmpty {
            InternetCard {
                VStack(alignment: .leading, spacing: 14) {
                    Text("No profiles yet")
                        .font(.system(size: 13, weight: .medium, design: .rounded))
                        .foregroundStyle(.white)
                    Text("Use the plus button to create a manual profile or parse a supported subscription.")
                        .font(.system(size: 10, weight: .regular, design: .rounded))
                        .foregroundStyle(.white.opacity(0.64))
                }
            }
        } else {
            ForEach(sections) { section in
                ProfileGroupCard(
                    section: section,
                    isCollapsed: model.isSectionCollapsed(section.id),
                    isEditing: isEditing,
                    activeTunnelTarget: model.activeTunnelTarget,
                    latencyText: { model.latencyText(for: $0) },
                    latencyLabel: { model.latencyAccessibilityLabel(for: $0) },
                    subtitle: { model.subtitle(for: $0) },
                    toggleCollapse: { model.toggleSectionCollapsed(section.id) },
                    selectProfile: { model.select($0.reference) },
                    testSectionLatency: {
                        Task {
                            if let source = section.source {
                                await model.testLatency(forSourceID: source.id)
                            } else {
                                await model.testLatencyForManualProfiles()
                            }
                        }
                    },
                    showDetails: { row in
                        onShowDetails(
                            ProfileDetailItem(
                                resolvedProfile: row.resolvedProfile,
                                source: section.source
                            )
                        )
                    },
                    deleteManual: { profile in
                        model.deleteManualProfile(profile)
                    },
                    testRowLatency: { row in
                        Task {
                            await model.testLatency(for: row.reference)
                        }
                    },
                    refreshSource: {
                        guard let source = section.source else {
                            return
                        }
                        Task {
                            await model.refresh(source: source)
                        }
                    },
                    deleteSource: {
                        guard let source = section.source else {
                            return
                        }
                        model.deleteSubscription(source)
                    }
                )
            }
        }
    }

    private var footerMeta: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("↓ \(model.subscriptionEndpoints.count) imported • ↑ \(model.manualProfiles.count) local")
            if model.isTestingLatency {
                Text("Testing latency…")
            } else if model.isRefreshingSubscriptions {
                Text("Refreshing subscriptions…")
            }
        }
        .font(.system(size: 10, weight: .regular, design: .rounded))
        .foregroundStyle(.white.opacity(0.52))
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.bottom, 12)
    }

    private var displayedTunnelStatus: String {
        if sections.isEmpty {
            return "No profiles"
        }

        if model.activeTunnelTarget == nil && model.tunnelPhase == .idle {
            return "Tap a profile"
        }

        if model.tunnelPhase == .failed {
            return model.tunnelRuntimeState?.lastError?.isEmpty == false ? "Failed" : model.tunnelPhase.displayName
        }

        return model.tunnelStatus
    }

    private var powerButtonSymbol: String {
        switch model.tunnelState {
        case .connected, .reasserting:
            return "pause.fill"
        case .connecting, .disconnecting:
            return "hourglass"
        default:
            return "play.fill"
        }
    }

    private var primaryActionDisabled: Bool {
        if model.tunnelPhase == .stopping || model.tunnelState == .disconnecting {
            return true
        }
        if model.activeTunnelTarget == nil && (model.tunnelPhase == .idle || model.tunnelPhase == .failed) {
            return true
        }
        return false
    }

    private func primaryAction() {
        switch model.tunnelState {
        case .connected, .connecting, .reasserting:
            Task {
                await model.disconnect()
            }
        case .disconnecting:
            break
        default:
            Task {
                await model.connect()
            }
        }
    }
}

private struct SettingsView: View {
    @EnvironmentObject private var model: AppModel

    let onShowLogs: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section("Tunnel") {
                    Text("Automatic reconnect is disabled in this revision while tunnel startup reliability is being stabilized.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Section("Diagnostics") {
                    Button {
                        Task {
                            await model.runBenchmark()
                        }
                    } label: {
                        Label("Run Benchmark", systemImage: "gauge.with.dots.needle.67percent")
                    }

                    Button {
                        Task {
                            await model.testAllLatencies(force: true)
                        }
                    } label: {
                        Label("Test Latency", systemImage: "speedometer")
                    }

                    Button {
                        Task {
                            await model.refreshAllSubscriptions()
                        }
                    } label: {
                        Label("Refresh Subscriptions", systemImage: "arrow.clockwise")
                    }

                    Button(action: onShowLogs) {
                        Label("Open Logs", systemImage: "doc.text")
                    }

                    if let benchmark = model.latestBenchmarkResult {
                        VStack(alignment: .leading, spacing: 6) {
                            Text("Latest Benchmark")
                                .font(.subheadline.weight(.semibold))
                            Text(benchmark.targetName)
                                .font(.footnote)
                            Text("Cold \(benchmark.cold.totalMs)ms • Warm \(benchmark.warm.totalMs)ms")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Text("DNS \(benchmarkMetric(benchmark.cold.dnsLookupMs)) • Connect \(benchmarkMetric(benchmark.cold.outboundConnectMs)) • First byte \(benchmarkMetric(benchmark.cold.firstByteMs))")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                            Text("Use Release or ad hoc builds for realistic numbers.")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 4)
                    }
                }

                Section("Active Target") {
                    if let selected = model.currentResolvedActiveTarget() {
                        VStack(alignment: .leading, spacing: 8) {
                            Text(profileTitle(selected))
                                .font(.headline)
                            Text(model.subtitle(for: selected))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 6)
                    } else {
                        Text("Tap a profile on Home to make it active.")
                            .foregroundStyle(.secondary)
                    }
                }

                Section("About") {
                    LabeledContent("App", value: AppConfiguration.appDisplayName)
                    LabeledContent("Version", value: AppConfiguration.appVersion)
                    LabeledContent("VPN Label", value: AppConfiguration.vpnDisplayName)
                }
            }
            .navigationTitle("Settings")
        }
    }
}

private func benchmarkMetric(_ value: Int?) -> String {
    guard let value else {
        return "n/a"
    }
    return "\(value)ms"
}

private struct ManualProfileEditorView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var model: AppModel

    @State private var name = ""
    @State private var address = ""
    @State private var port = "443"
    @State private var uuid = ""
    @State private var securityKind: ProfileSecurityKind = .reality
    @State private var flow: VLESSFlow = .none
    @State private var serverName = ""
    @State private var fingerprint: ClientFingerprintPreset = .chrome
    @State private var publicKey = ""
    @State private var shortId = ""
    @State private var spiderX = "/"
    @State private var tlsALPNPreset: TLSALPNPreset = .automatic
    @State private var tlsPinnedPeerCertSha256 = ""
    @State private var tlsVerifyPeerCertByName = ""
    @State private var xhttpHost = ""
    @State private var xhttpPath = "/"
    @State private var xhttpMode: XHTTPMode = .auto
    @State private var behaviorProfile: BehaviorProfile = .balanced
    @State private var uplinkHTTPMethod: ManualUplinkHTTPMethod = .post
    @State private var encryptionMode: VLESSEncryptionMode = .none
    @State private var customEncryption = ""
    @State private var methodCompatibilityNotice: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Essentials") {
                    TextField("Profile name", text: $name)
                    TextField("Server address", text: $address)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Port", text: $port)
                        .keyboardType(.numberPad)
                    TextField("UUID", text: $uuid)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Picker("Security", selection: $securityKind) {
                        ForEach(ProfileSecurityKind.allCases, id: \.self) { kind in
                            Text(kind.displayName).tag(kind)
                        }
                    }
                    TextField("\(securityKind.displayName) serverName", text: $serverName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    if securityKind == .reality {
                        TextField("REALITY publicKey", text: $publicKey)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                }

                Section {
                    Picker("Flow", selection: $flow) {
                        ForEach(availableFlowOptions, id: \.self) { option in
                            Text(option.displayName).tag(option)
                        }
                    }
                    .disabled(securityKind == .tls)

                    Picker("Fingerprint", selection: $fingerprint) {
                        ForEach(ClientFingerprintPreset.allCases, id: \.self) { option in
                            Text(option.displayName).tag(option)
                        }
                    }
                } header: {
                    Text("Connection Defaults")
                } footer: {
                    Text("TLS keeps flow on None. Use XTLS Vision only for REALITY when your provider explicitly requires it.")
                }

                Section {
                    Picker("Body Encryption", selection: $encryptionMode) {
                        ForEach(VLESSEncryptionMode.allCases) { mode in
                            Text(mode.rawValue).tag(mode)
                        }
                    }
                    if encryptionMode == .custom {
                        TextField("Raw encryption string", text: $customEncryption)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                } header: {
                    Text("VLESS")
                } footer: {
                    Text("Leave encryption on None unless your provider gave you a specific VLESS body-encryption string.")
                }

                Section {
                    TextField("Host (defaults to serverName)", text: $xhttpHost)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Path", text: $xhttpPath)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Picker("HTTP Method", selection: $uplinkHTTPMethod) {
                        ForEach(availableHTTPMethods, id: \.self) { method in
                            Text(method.displayName).tag(method)
                        }
                    }
                    Picker("Mode", selection: $xhttpMode) {
                        ForEach(XHTTPMode.allCases, id: \.self) { mode in
                            Text(mode.displayName).tag(mode)
                        }
                    }
                    Picker("Behavior", selection: $behaviorProfile) {
                        ForEach(BehaviorProfile.allCases, id: \.self) { profile in
                            Text(profile.displayName).tag(profile)
                        }
                    }
                } header: {
                    Text("XHTTP")
                } footer: {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Mode controls the XHTTP transport shape. Method controls the actual HTTP verb.")
                        if let methodCompatibilityNotice {
                            Text(methodCompatibilityNotice)
                        }
                    }
                }

                Section("Advanced") {
                    if securityKind == .reality {
                        TextField("Short ID", text: $shortId)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        TextField("SpiderX", text: $spiderX)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    } else {
                        Picker("ALPN", selection: $tlsALPNPreset) {
                            ForEach(TLSALPNPreset.allCases) { preset in
                                Text(preset.rawValue).tag(preset)
                            }
                        }
                        TextField("Pinned Peer Cert SHA256", text: $tlsPinnedPeerCertSha256)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        TextField("Verify Peer Cert By Name", text: $tlsVerifyPeerCertByName)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                }
            }
            .navigationTitle("Manual Profile")
            .navigationBarTitleDisplayMode(.inline)
            .onChange(of: securityKind, initial: false) { _, newKind in
                if newKind == .tls {
                    flow = .none
                }
            }
            .onChange(of: encryptionMode, initial: false) { _, newMode in
                if newMode == .none {
                    customEncryption = ""
                }
            }
            .onChange(of: xhttpMode, initial: false) { _, newMode in
                if newMode != .packetUp, uplinkHTTPMethod == .get {
                    uplinkHTTPMethod = .post
                    methodCompatibilityNotice = "GET only works with Packet Upload mode, so the method was reset to POST."
                } else {
                    methodCompatibilityNotice = nil
                }
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }

                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        saveProfile()
                    }
                    .disabled(!canSave)
                }
            }
        }
        .presentationDetents([.large])
    }

    private var canSave: Bool {
        !name.trimmed.isEmpty &&
        !address.trimmed.isEmpty &&
        (Int(port) ?? 0) > 0 &&
        !uuid.trimmed.isEmpty &&
        !serverName.trimmed.isEmpty &&
        (securityKind != .reality || !publicKey.trimmed.isEmpty) &&
        (encryptionMode != .custom || !customEncryption.trimmed.isEmpty)
    }

    private var availableFlowOptions: [VLESSFlow] {
        securityKind == .tls ? [.none] : VLESSFlow.allCases
    }

    private var availableHTTPMethods: [ManualUplinkHTTPMethod] {
        if xhttpMode == .packetUp {
            return ManualUplinkHTTPMethod.allCases
        }
        return ManualUplinkHTTPMethod.allCases.filter { $0 != .get }
    }

    private var selectedEncryption: String {
        encryptionMode == .custom ? customEncryption.trimmed : "none"
    }

    private func saveProfile() {
        let profile: ManualProfile

        switch securityKind {
        case .reality:
            profile = ManualProfile(
                name: name.trimmed,
                address: address.trimmed,
                port: Int(port) ?? 0,
                uuid: uuid.trimmed,
                flow: flow.runtimeValue,
                securityKind: .reality,
                realitySettings: RealitySecuritySettings(
                    serverName: serverName.trimmed,
                    fingerprint: fingerprint.rawValue,
                    publicKey: publicKey.trimmed,
                    shortId: shortId.trimmed.isEmpty ? nil : shortId.trimmed,
                    spiderX: spiderX.trimmed.isEmpty ? nil : spiderX.trimmed
                ),
                encryption: selectedEncryption,
                xhttpHost: xhttpHost.trimmed,
                xhttpPath: xhttpPath.trimmed,
                xhttpMode: xhttpMode,
                behaviorProfile: behaviorProfile,
                uplinkHTTPMethod: uplinkHTTPMethod.rawValue
            )
        case .tls:
            profile = ManualProfile(
                name: name.trimmed,
                address: address.trimmed,
                port: Int(port) ?? 0,
                uuid: uuid.trimmed,
                flow: flow.runtimeValue,
                securityKind: .tls,
                tlsSettings: TLSSecuritySettings(
                    serverName: serverName.trimmed,
                    fingerprint: fingerprint.rawValue,
                    alpn: tlsALPNPreset.values,
                    pinnedPeerCertSha256: tlsPinnedPeerCertSha256.trimmed.isEmpty ? nil : tlsPinnedPeerCertSha256.trimmed,
                    verifyPeerCertByName: tlsVerifyPeerCertByName.trimmed.isEmpty ? nil : tlsVerifyPeerCertByName.trimmed
                ),
                encryption: selectedEncryption,
                xhttpHost: xhttpHost.trimmed,
                xhttpPath: xhttpPath.trimmed,
                xhttpMode: xhttpMode,
                behaviorProfile: behaviorProfile,
                uplinkHTTPMethod: uplinkHTTPMethod.rawValue
            )
        }

        if model.addManualProfile(profile) {
            dismiss()
        }
    }
}

private struct SubscriptionImportView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var model: AppModel

    @State private var subscriptionName = ""
    @State private var subscriptionURL = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Parsing") {
                    TextField("Name", text: $subscriptionName)
                    TextField("Subscription URL", text: $subscriptionURL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                }

                Section {
                    Text("Supported formats: Xray JSON outbounds, raw VLESS links, and base64/plain-text link lists with XHTTP overrides.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Parse Subscription")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }

                ToolbarItem(placement: .confirmationAction) {
                    Button("Import") {
                        Task {
                            let didImport = await model.addSubscription(
                                name: subscriptionName.trimmed,
                                urlString: subscriptionURL.trimmed
                            )
                            if didImport {
                                dismiss()
                            }
                        }
                    }
                    .disabled(!canImport)
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private var canImport: Bool {
        guard let url = URL(string: subscriptionURL.trimmed) else {
            return false
        }
        return url.scheme != nil && url.host != nil
    }
}

private struct LogsView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
            Group {
                if model.logLines.isEmpty {
                    VStack(alignment: .leading, spacing: 14) {
                        Text("No logs available")
                            .font(.headline)
                        Text("Connect, refresh a subscription, or edit a profile to generate logs here.")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    .padding(24)
                    .background(Color(.systemGroupedBackground).ignoresSafeArea())
                } else {
                    ScrollView {
                        Text(model.logLines.joined(separator: "\n"))
                            .font(.system(.caption, design: .monospaced))
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(18)
                            .background(
                                RoundedRectangle(cornerRadius: 20, style: .continuous)
                                    .fill(Color(.secondarySystemGroupedBackground))
                            )
                            .padding(20)
                            .textSelection(.enabled)
                    }
                    .background(Color(.systemGroupedBackground).ignoresSafeArea())
                }
            }
            .navigationTitle("Logs")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") {
                        dismiss()
                    }
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button("Refresh") {
                        model.reloadLogs()
                    }
                    Button("Clear", role: .destructive) {
                        model.clearLogs()
                    }
                }
            }
        }
        .onAppear {
            model.reloadLogs()
        }
        .presentationDetents([.medium, .large])
    }
}

private struct ProfileDetailView: View {
    @Environment(\.dismiss) private var dismiss

    let item: ProfileDetailItem

    private var profile: ResolvedProfile {
        item.resolvedProfile
    }

    var body: some View {
        NavigationStack {
            List {
                Section("Profile") {
                    LabeledContent("Name", value: profileTitle(profile))
                    LabeledContent("Address", value: profileAddress(profile))
                    LabeledContent("Security", value: securityName)
                    LabeledContent("Path", value: normalizedDisplayPath(profilePath(profile)))
                }

                Section("Transport") {
                    ForEach(summaryChips(for: profile), id: \.self) { chip in
                        Text(chip)
                    }
                }

                if let source = item.source {
                    Section("Source") {
                        LabeledContent("Subscription", value: source.name)
                        LabeledContent("URL", value: source.subscriptionURL.absoluteString)
                    }
                }
            }
            .navigationTitle("Profile Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") {
                        dismiss()
                    }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private var securityName: String {
        switch profile {
        case let .manual(manual):
            return manual.securityKind.displayName
        case let .subscriptionEndpoint(endpoint):
            return endpoint.securityKind.displayName
        }
    }
}

private struct InternetBackground: View {
    var body: some View {
        LinearGradient(
            colors: [
                Color.black,
                Color(red: 0.05, green: 0.05, blue: 0.07),
                Color.black,
            ],
            startPoint: .top,
            endPoint: .bottom
        )
        .overlay(
            Circle()
                .fill(Color(red: 0.10, green: 0.24, blue: 0.48).opacity(0.22))
                .frame(width: 320, height: 320)
                .offset(x: -120, y: -260)
        )
    }
}

private struct InternetCard<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        content
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(14)
            .background(
                RoundedRectangle(cornerRadius: 22, style: .continuous)
                    .fill(Color.white.opacity(0.08))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 22, style: .continuous)
                    .stroke(Color.white.opacity(0.05), lineWidth: 1)
            )
    }
}

private struct StatusPill: View {
    let text: String
    let isConnected: Bool

    var body: some View {
        HStack(spacing: 6) {
            Text(text)
                .font(.system(size: 10, weight: .medium, design: .monospaced))
                .foregroundStyle(.white)
            Image(systemName: "chevron.right")
                .foregroundStyle(Color(red: 0.28, green: 0.56, blue: 0.98))
                .font(.system(size: 9, weight: .semibold))
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(isConnected ? Color.white.opacity(0.10) : Color.white.opacity(0.08))
        .clipShape(Capsule())
    }
}

private struct ProfileGroupCard: View {
    let section: HomeProfileSection
    let isCollapsed: Bool
    let isEditing: Bool
    let activeTunnelTarget: ProfileReference?
    let latencyText: (UUID) -> String
    let latencyLabel: (UUID) -> String
    let subtitle: (ResolvedProfile) -> String
    let toggleCollapse: () -> Void
    let selectProfile: (HomeProfileRow) -> Void
    let testSectionLatency: () -> Void
    let showDetails: (HomeProfileRow) -> Void
    let deleteManual: (ManualProfile) -> Void
    let testRowLatency: (HomeProfileRow) -> Void
    let refreshSource: () -> Void
    let deleteSource: () -> Void

    var body: some View {
        InternetCard {
            VStack(alignment: .leading, spacing: 12) {
                header

                if !isCollapsed {
                    Divider()
                        .overlay(Color.white.opacity(0.08))

                    VStack(spacing: 0) {
                        ForEach(Array(section.rows.enumerated()), id: \.element.id) { index, row in
                            ProfileRowView(
                                row: row,
                                isSelected: activeTunnelTarget == row.reference,
                                subtitle: subtitle(row.resolvedProfile),
                                classification: profileClassification(row.resolvedProfile),
                                latency: latencyText(row.id),
                                latencyLabel: latencyLabel(row.id),
                                isEditing: isEditing,
                                onSelect: { selectProfile(row) },
                                onTestLatency: { testRowLatency(row) },
                                onShowDetails: { showDetails(row) },
                                onDeleteManual: {
                                    if case let .manual(profile) = row {
                                        deleteManual(profile)
                                    }
                                }
                            )

                            if index != section.rows.count - 1 {
                                Divider()
                                    .overlay(Color.white.opacity(0.08))
                            }
                        }
                    }
                }
            }
        }
        .contextMenu {
            Button {
                testSectionLatency()
            } label: {
                Label("Test Latency", systemImage: "speedometer")
            }

            if section.source != nil {
                Button {
                    refreshSource()
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 10) {
            Button(action: toggleCollapse) {
                Text(section.title)
                    .font(.system(size: 11, weight: .medium, design: .rounded))
                    .foregroundStyle(.white)
            }
            .buttonStyle(.plain)

            Spacer()

            HStack(spacing: 10) {
                if let _ = section.source, !isEditing {
                    Button(action: refreshSource) {
                        Image(systemName: "arrow.clockwise")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(Color(red: 0.28, green: 0.56, blue: 0.98))
                    }
                    .buttonStyle(.plain)
                }

                if let _ = section.source, isEditing {
                    Button(role: .destructive, action: deleteSource) {
                        Image(systemName: "trash")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(Color.red.opacity(0.92))
                    }
                    .buttonStyle(.plain)
                }

                Image(systemName: isCollapsed ? "chevron.down" : "chevron.up")
                    .foregroundStyle(.white)
                    .font(.system(size: 11, weight: .medium))
                    .padding(.top, 2)
            }
        }
    }
}

private struct ProfileRowView: View {
    let row: HomeProfileRow
    let isSelected: Bool
    let subtitle: String
    let classification: ProfileClassification
    let latency: String
    let latencyLabel: String
    let isEditing: Bool
    let onSelect: () -> Void
    let onTestLatency: () -> Void
    let onShowDetails: () -> Void
    let onDeleteManual: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Circle()
                .fill(isSelected ? Color(red: 1.0, green: 0.85, blue: 0.27) : Color.clear)
                .overlay(Circle().stroke(Color.white.opacity(0.24), lineWidth: isSelected ? 0 : 1))
                .frame(width: 10, height: 10)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(row.title)
                        .font(.system(size: 11, weight: .medium, design: .rounded))
                        .foregroundStyle(.white)
                    if classification == .recommendedFast {
                        Text(classification.displayName.uppercased())
                            .font(.system(size: 7, weight: .bold, design: .rounded))
                            .foregroundStyle(Color(red: 0.10, green: 0.12, blue: 0.16))
                            .padding(.horizontal, 5)
                            .padding(.vertical, 2)
                            .background(Color(red: 0.66, green: 0.92, blue: 0.78))
                            .clipShape(Capsule())
                    }
                }
                Text(subtitle)
                    .font(.system(size: 9, weight: .regular, design: .rounded))
                    .foregroundStyle(.white.opacity(0.42))
            }

            Spacer()

            Text(latency)
                .font(.system(size: 11, weight: .medium, design: .rounded))
                .foregroundStyle(latencyColor)
                .accessibilityLabel(latencyLabel)

            Button(action: onShowDetails) {
                Image(systemName: "info.circle")
                    .font(.system(size: 13))
                    .foregroundStyle(Color(red: 0.28, green: 0.56, blue: 0.98))
            }
            .buttonStyle(.plain)

            if isEditing, case .manual = row {
                Button(role: .destructive, action: onDeleteManual) {
                    Image(systemName: "trash")
                        .font(.system(size: 12))
                        .foregroundStyle(Color.red.opacity(0.92))
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.vertical, 10)
        .contentShape(Rectangle())
        .onTapGesture(perform: onSelect)
        .contextMenu {
            Button {
                onTestLatency()
            } label: {
                Label("Test Latency", systemImage: "speedometer")
            }

            Button {
                onShowDetails()
            } label: {
                Label("Details", systemImage: "info.circle")
            }
        }
    }

    private var latencyColor: Color {
        switch latency {
        case "Fail":
            return .orange
        case "--":
            return Color.white.opacity(0.34)
        default:
            return .green
        }
    }
}

private struct InternetChipCloud: View {
    let chips: [String]

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 108), spacing: 8)], alignment: .leading, spacing: 8) {
            ForEach(chips, id: \.self) { chip in
                Text(chip)
                    .font(.system(size: 12, weight: .semibold, design: .rounded))
                    .foregroundStyle(Color(red: 0.62, green: 0.78, blue: 1.0))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(red: 0.22, green: 0.32, blue: 0.54).opacity(0.34))
                    .clipShape(Capsule())
            }
        }
    }
}

private struct ConnectionTimerView: View {
    let startDate: Date?

    var body: some View {
        Group {
            if let startDate {
                Text(startDate, style: .timer)
            } else {
                Text("00:00:00")
            }
        }
        .monospacedDigit()
        .font(.system(size: 12, weight: .medium, design: .monospaced))
        .foregroundStyle(.white)
    }
}

private extension String {
    var trimmed: String {
        trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
