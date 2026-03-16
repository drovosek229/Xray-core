import Foundation

enum RemoteGeoAssetKind: String, CaseIterable, Codable, Sendable {
    case geoIP
    case geoSite

    var fileName: String {
        switch self {
        case .geoIP:
            return AppConfiguration.geoIPAssetFileName
        case .geoSite:
            return AppConfiguration.geoSiteAssetFileName
        }
    }

    var displayName: String {
        fileName
    }
}

struct RemoteGeoAssetSettings: Codable, Hashable, Sendable {
    var geoIPURLString: String
    var geoSiteURLString: String

    init(
        geoIPURLString: String = "",
        geoSiteURLString: String = ""
    ) {
        self.geoIPURLString = geoIPURLString
        self.geoSiteURLString = geoSiteURLString
    }

    var hasAnyConfiguredAssets: Bool {
        normalizedURLString(for: .geoIP) != nil || normalizedURLString(for: .geoSite) != nil
    }

    func normalizedURLString(for kind: RemoteGeoAssetKind) -> String? {
        let rawValue: String
        switch kind {
        case .geoIP:
            rawValue = geoIPURLString
        case .geoSite:
            rawValue = geoSiteURLString
        }

        let normalized = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        return normalized.isEmpty ? nil : normalized
    }

    func url(for kind: RemoteGeoAssetKind) -> URL? {
        guard let normalized = normalizedURLString(for: kind),
              let url = URL(string: normalized),
              url.scheme?.lowercased() == "https",
              let host = url.host,
              !host.isEmpty
        else {
            return nil
        }
        return url
    }
}

struct RemoteGeoAssetStatus: Codable, Hashable, Sendable {
    var sourceURLString: String?
    var lastSuccessfulRefreshAt: Date?
    var lastError: String?

    init(
        sourceURLString: String? = nil,
        lastSuccessfulRefreshAt: Date? = nil,
        lastError: String? = nil
    ) {
        self.sourceURLString = sourceURLString
        self.lastSuccessfulRefreshAt = lastSuccessfulRefreshAt
        self.lastError = lastError
    }
}

struct RemoteGeoAssetRefreshState: Codable, Hashable, Sendable {
    var geoIP: RemoteGeoAssetStatus
    var geoSite: RemoteGeoAssetStatus

    init(
        geoIP: RemoteGeoAssetStatus = RemoteGeoAssetStatus(),
        geoSite: RemoteGeoAssetStatus = RemoteGeoAssetStatus()
    ) {
        self.geoIP = geoIP
        self.geoSite = geoSite
    }

    func status(for kind: RemoteGeoAssetKind) -> RemoteGeoAssetStatus {
        switch kind {
        case .geoIP:
            return geoIP
        case .geoSite:
            return geoSite
        }
    }

    mutating func setStatus(_ status: RemoteGeoAssetStatus, for kind: RemoteGeoAssetKind) {
        switch kind {
        case .geoIP:
            geoIP = status
        case .geoSite:
            geoSite = status
        }
    }
}

enum RemoteGeoAssetManagerError: LocalizedError {
    case invalidURL(RemoteGeoAssetKind)
    case unexpectedStatusCode(RemoteGeoAssetKind, Int)
    case emptyPayload(RemoteGeoAssetKind)
    case malformedPayload(RemoteGeoAssetKind)

    var errorDescription: String? {
        switch self {
        case let .invalidURL(kind):
            return "\(kind.displayName) URL must use https."
        case let .unexpectedStatusCode(kind, code):
            return "Failed to download \(kind.displayName): HTTP \(code)."
        case let .emptyPayload(kind):
            return "\(kind.displayName) payload is empty."
        case let .malformedPayload(kind):
            return "\(kind.displayName) payload is invalid."
        }
    }
}

final class RemoteGeoAssetManager {
    private let appGroupStore: AppGroupStore
    private let session: URLSession

    init(
        appGroupStore: AppGroupStore = AppGroupStore(),
        session: URLSession = .shared
    ) {
        self.appGroupStore = appGroupStore
        self.session = session
    }

    func loadRefreshState() throws -> RemoteGeoAssetRefreshState {
        try appGroupStore.load(
            RemoteGeoAssetRefreshState.self,
            forKey: AppConfiguration.remoteGeoAssetRefreshStateKey
        ) ?? RemoteGeoAssetRefreshState()
    }

    func saveRefreshState(_ state: RemoteGeoAssetRefreshState) throws {
        try appGroupStore.save(state, forKey: AppConfiguration.remoteGeoAssetRefreshStateKey)
    }

    func assetDirectory(for settings: RemoteGeoAssetSettings) -> String? {
        settings.hasAnyConfiguredAssets ? appGroupStore.directoryURL().path : nil
    }

    func refreshIfNeeded(
        settings: RemoteGeoAssetSettings,
        force: Bool = false
    ) async throws -> RemoteGeoAssetRefreshState {
        var refreshState = try loadRefreshState()

        for kind in RemoteGeoAssetKind.allCases {
            refreshState = try await refreshAsset(
                kind: kind,
                settings: settings,
                refreshState: refreshState,
                force: force
            )
        }

        try saveRefreshState(refreshState)
        return refreshState
    }

    func refreshIfNeededBlocking(
        settings: RemoteGeoAssetSettings,
        force: Bool = false
    ) throws -> RemoteGeoAssetRefreshState {
        let semaphore = DispatchSemaphore(value: 0)
        let resultQueue = DispatchQueue(label: "internet.remote-geo-assets.blocking")
        var result: Result<RemoteGeoAssetRefreshState, Error>?

        Task {
            defer { semaphore.signal() }
            let value: Result<RemoteGeoAssetRefreshState, Error>
            do {
                value = .success(try await refreshIfNeeded(settings: settings, force: force))
            } catch {
                value = .failure(error)
            }
            resultQueue.sync {
                result = value
            }
        }

        semaphore.wait()
        return try resultQueue.sync {
            try result!.get()
        }
    }

    private func refreshAsset(
        kind: RemoteGeoAssetKind,
        settings: RemoteGeoAssetSettings,
        refreshState: RemoteGeoAssetRefreshState,
        force: Bool
    ) async throws -> RemoteGeoAssetRefreshState {
        var updatedState = refreshState
        var currentStatus = refreshState.status(for: kind)
        let fileURL = appGroupStore.fileURL(named: kind.fileName)

        guard let configuredURLString = settings.normalizedURLString(for: kind) else {
            currentStatus = RemoteGeoAssetStatus()
            updatedState.setStatus(currentStatus, for: kind)
            removeItemIfPresent(at: fileURL)
            return updatedState
        }

        guard let assetURL = settings.url(for: kind) else {
            currentStatus.sourceURLString = configuredURLString
            currentStatus.lastError = RemoteGeoAssetManagerError.invalidURL(kind).localizedDescription
            updatedState.setStatus(currentStatus, for: kind)
            try saveRefreshState(updatedState)
            throw RemoteGeoAssetManagerError.invalidURL(kind)
        }

        let hasMatchingValidCache =
            currentStatus.sourceURLString == assetURL.absoluteString &&
            validateLocalFileIfPresent(at: fileURL, kind: kind)

        if !force,
           hasMatchingValidCache,
           let lastSuccessfulRefreshAt = currentStatus.lastSuccessfulRefreshAt,
           Date().timeIntervalSince(lastSuccessfulRefreshAt) < AppConfiguration.remoteGeoAssetRefreshInterval
        {
            currentStatus.lastError = nil
            updatedState.setStatus(currentStatus, for: kind)
            return updatedState
        }

        do {
            let data = try await downloadAsset(kind: kind, url: assetURL)
            try RemoteGeoAssetPayloadValidator.validate(data: data, kind: kind)
            try writeAtomically(data: data, to: fileURL)

            currentStatus = RemoteGeoAssetStatus(
                sourceURLString: assetURL.absoluteString,
                lastSuccessfulRefreshAt: Date(),
                lastError: nil
            )
            updatedState.setStatus(currentStatus, for: kind)
            return updatedState
        } catch {
            if hasMatchingValidCache {
                currentStatus.lastError = error.localizedDescription
                updatedState.setStatus(currentStatus, for: kind)
                return updatedState
            }

            currentStatus.sourceURLString = assetURL.absoluteString
            currentStatus.lastError = error.localizedDescription
            updatedState.setStatus(currentStatus, for: kind)
            try saveRefreshState(updatedState)
            throw error
        }
    }

    private func downloadAsset(kind: RemoteGeoAssetKind, url: URL) async throws -> Data {
        let (data, response) = try await session.data(from: url)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw RemoteGeoAssetManagerError.malformedPayload(kind)
        }
        guard httpResponse.statusCode == 200 else {
            throw RemoteGeoAssetManagerError.unexpectedStatusCode(kind, httpResponse.statusCode)
        }
        guard !data.isEmpty else {
            throw RemoteGeoAssetManagerError.emptyPayload(kind)
        }
        return data
    }

    private func validateLocalFileIfPresent(at fileURL: URL, kind: RemoteGeoAssetKind) -> Bool {
        guard let data = try? Data(contentsOf: fileURL) else {
            return false
        }
        do {
            try RemoteGeoAssetPayloadValidator.validate(data: data, kind: kind)
            return true
        } catch {
            return false
        }
    }

    private func writeAtomically(data: Data, to fileURL: URL) throws {
        let directoryURL = fileURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directoryURL, withIntermediateDirectories: true)

        let tempURL = directoryURL.appendingPathComponent(UUID().uuidString).appendingPathExtension("tmp")
        try data.write(to: tempURL, options: .atomic)
        removeItemIfPresent(at: fileURL)
        try FileManager.default.moveItem(at: tempURL, to: fileURL)
    }

    private func removeItemIfPresent(at fileURL: URL) {
        if FileManager.default.fileExists(atPath: fileURL.path) {
            try? FileManager.default.removeItem(at: fileURL)
        }
    }
}

struct RemoteGeoAssetRuntimePreparation {
    static func assetDirectory(
        settings: RemoteGeoAssetSettings?,
        manager: RemoteGeoAssetManager = RemoteGeoAssetManager(),
        bundledAssetDirectory: String
    ) throws -> String {
        guard let settings, settings.hasAnyConfiguredAssets else {
            return bundledAssetDirectory
        }

        _ = try manager.refreshIfNeededBlocking(settings: settings)
        return manager.assetDirectory(for: settings) ?? bundledAssetDirectory
    }
}

private enum RemoteGeoAssetPayloadValidator {
    private enum WireType: UInt64 {
        case varint = 0
        case fixed64 = 1
        case lengthDelimited = 2
        case fixed32 = 5
    }

    private struct Field {
        let number: Int
        let wireType: WireType
        let data: Data?
        let integer: UInt64?
    }

    static func validate(data: Data, kind: RemoteGeoAssetKind) throws {
        let fields = try parseFields(data, kind: kind)
        guard let entryField = fields.first(where: { $0.number == 1 && $0.wireType == .lengthDelimited }),
              let entryData = entryField.data
        else {
            throw RemoteGeoAssetManagerError.malformedPayload(kind)
        }

        let entryFields = try parseFields(entryData, kind: kind)
        guard let countryCodeField = entryFields.first(where: { $0.number == 1 && $0.wireType == .lengthDelimited }),
              let countryCodeData = countryCodeField.data,
              let countryCode = String(data: countryCodeData, encoding: .utf8),
              !countryCode.isEmpty
        else {
            throw RemoteGeoAssetManagerError.malformedPayload(kind)
        }

        guard let payloadField = entryFields.first(where: { $0.number == 2 && $0.wireType == .lengthDelimited }),
              let payloadData = payloadField.data
        else {
            throw RemoteGeoAssetManagerError.malformedPayload(kind)
        }

        switch kind {
        case .geoIP:
            try validateGeoIPPayload(payloadData)
        case .geoSite:
            try validateGeoSitePayload(payloadData)
        }
    }

    private static func validateGeoIPPayload(_ data: Data) throws {
        let fields = try parseFields(data, kind: .geoIP)
        guard let ipField = fields.first(where: { $0.number == 1 && $0.wireType == .lengthDelimited }),
              let ipData = ipField.data,
              ipData.count == 4 || ipData.count == 16,
              fields.contains(where: { $0.number == 2 && $0.wireType == .varint })
        else {
            throw RemoteGeoAssetManagerError.malformedPayload(.geoIP)
        }
    }

    private static func validateGeoSitePayload(_ data: Data) throws {
        let fields = try parseFields(data, kind: .geoSite)
        guard let typeField = fields.first(where: { $0.number == 1 && $0.wireType == .varint }),
              let typeValue = typeField.integer,
              (0...3).contains(typeValue),
              let valueField = fields.first(where: { $0.number == 2 && $0.wireType == .lengthDelimited }),
              let valueData = valueField.data,
              let value = String(data: valueData, encoding: .utf8),
              !value.isEmpty
        else {
            throw RemoteGeoAssetManagerError.malformedPayload(.geoSite)
        }
    }

    private static func parseFields(
        _ data: Data,
        kind: RemoteGeoAssetKind
    ) throws -> [Field] {
        let bytes = Array(data)
        var index = 0
        var fields: [Field] = []

        while index < bytes.count {
            let key = try readVarint(bytes, index: &index, kind: kind)
            let number = Int(key >> 3)
            guard number > 0 else {
                throw RemoteGeoAssetManagerError.malformedPayload(kind)
            }

            guard let wireType = WireType(rawValue: key & 0x7) else {
                throw RemoteGeoAssetManagerError.malformedPayload(kind)
            }

            switch wireType {
            case .varint:
                let value = try readVarint(bytes, index: &index, kind: kind)
                fields.append(Field(number: number, wireType: wireType, data: nil, integer: value))
            case .lengthDelimited:
                let length = Int(try readVarint(bytes, index: &index, kind: kind))
                guard length >= 0, index + length <= bytes.count else {
                    throw RemoteGeoAssetManagerError.malformedPayload(kind)
                }
                let fieldData = Data(bytes[index ..< index + length])
                index += length
                fields.append(Field(number: number, wireType: wireType, data: fieldData, integer: nil))
            case .fixed64:
                guard index + 8 <= bytes.count else {
                    throw RemoteGeoAssetManagerError.malformedPayload(kind)
                }
                index += 8
                fields.append(Field(number: number, wireType: wireType, data: nil, integer: nil))
            case .fixed32:
                guard index + 4 <= bytes.count else {
                    throw RemoteGeoAssetManagerError.malformedPayload(kind)
                }
                index += 4
                fields.append(Field(number: number, wireType: wireType, data: nil, integer: nil))
            }
        }

        return fields
    }

    private static func readVarint(
        _ bytes: [UInt8],
        index: inout Int,
        kind: RemoteGeoAssetKind
    ) throws -> UInt64 {
        var result: UInt64 = 0
        var shift: UInt64 = 0

        while index < bytes.count {
            let byte = bytes[index]
            index += 1

            result |= UInt64(byte & 0x7F) << shift
            if byte & 0x80 == 0 {
                return result
            }

            shift += 7
            if shift >= 64 {
                throw RemoteGeoAssetManagerError.malformedPayload(kind)
            }
        }

        throw RemoteGeoAssetManagerError.malformedPayload(kind)
    }
}
