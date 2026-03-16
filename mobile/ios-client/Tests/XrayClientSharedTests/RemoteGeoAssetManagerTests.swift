import Foundation
import XCTest
@testable import XrayClientShared

final class RemoteGeoAssetManagerTests: XCTestCase {
    override func tearDown() {
        MockURLProtocol.reset()
        super.tearDown()
    }

    func testRemoteGeoAssetSettingsRoundTripThroughPreferencesStore() throws {
        let appGroupStore = AppGroupStore(appGroupIdentifier: "tests.internet.\(UUID().uuidString.lowercased())")
        let preferencesStore = ClientPreferencesStore(appGroupStore: appGroupStore)
        let settings = RemoteGeoAssetSettings(
            geoIPURLString: "https://example.com/geoip.dat",
            geoSiteURLString: "https://example.com/geosite.dat"
        )

        try preferencesStore.saveRemoteGeoAssetSettings(settings)

        XCTAssertEqual(try preferencesStore.loadRemoteGeoAssetSettings(), settings)
    }

    func testRefreshWritesFilesAndState() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(
            geoIPURLString: "https://example.com/geoip.dat",
            geoSiteURLString: "https://example.com/geosite.dat"
        )
        MockURLProtocol.setHandler { request in
            switch request.url?.lastPathComponent {
            case "geoip.dat":
                return (.ok(request.url!), validGeoIPPayload())
            case "geosite.dat":
                return (.ok(request.url!), validGeoSitePayload())
            default:
                XCTFail("Unexpected request: \(String(describing: request.url))")
                return (.ok(request.url!), Data())
            }
        }

        let state = try await harness.manager.refreshIfNeeded(settings: settings, force: true)

        XCTAssertEqual(MockURLProtocol.requestCount, 2)
        XCTAssertEqual(try Data(contentsOf: harness.appGroupStore.fileURL(named: "geoip.dat")), validGeoIPPayload())
        XCTAssertEqual(try Data(contentsOf: harness.appGroupStore.fileURL(named: "geosite.dat")), validGeoSitePayload())
        XCTAssertEqual(state.geoIP.sourceURLString, "https://example.com/geoip.dat")
        XCTAssertEqual(state.geoSite.sourceURLString, "https://example.com/geosite.dat")
        XCTAssertNotNil(state.geoIP.lastSuccessfulRefreshAt)
        XCTAssertNotNil(state.geoSite.lastSuccessfulRefreshAt)
        XCTAssertNil(state.geoIP.lastError)
        XCTAssertNil(state.geoSite.lastError)
    }

    func testFreshCacheSkipsNetworkRefresh() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        let fileURL = harness.appGroupStore.fileURL(named: "geoip.dat")

        try validGeoIPPayload().write(to: fileURL)
        try harness.manager.saveRefreshState(
            RemoteGeoAssetRefreshState(
                geoIP: RemoteGeoAssetStatus(
                    sourceURLString: "https://example.com/geoip.dat",
                    lastSuccessfulRefreshAt: Date(),
                    lastError: "stale"
                )
            )
        )
        MockURLProtocol.setHandler { request in
            XCTFail("Unexpected network request: \(String(describing: request.url))")
            return (.ok(request.url!), validGeoIPPayload())
        }

        let state = try await harness.manager.refreshIfNeeded(settings: settings)

        XCTAssertEqual(MockURLProtocol.requestCount, 0)
        XCTAssertEqual(try Data(contentsOf: fileURL), validGeoIPPayload())
        XCTAssertNil(state.geoIP.lastError)
    }

    func testStaleCacheRefreshesAfter24Hours() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        let fileURL = harness.appGroupStore.fileURL(named: "geoip.dat")
        let staleDate = Date(timeIntervalSinceNow: -(AppConfiguration.remoteGeoAssetRefreshInterval + 1))

        try validGeoIPPayload().write(to: fileURL)
        try harness.manager.saveRefreshState(
            RemoteGeoAssetRefreshState(
                geoIP: RemoteGeoAssetStatus(
                    sourceURLString: "https://example.com/geoip.dat",
                    lastSuccessfulRefreshAt: staleDate
                )
            )
        )
        let replacementPayload = validGeoIPPayload(octet: 9)
        MockURLProtocol.setHandler { request in
            XCTAssertEqual(request.url?.absoluteString, "https://example.com/geoip.dat")
            return (.ok(request.url!), replacementPayload)
        }

        let state = try await harness.manager.refreshIfNeeded(settings: settings)

        XCTAssertEqual(MockURLProtocol.requestCount, 1)
        XCTAssertEqual(try Data(contentsOf: fileURL), replacementPayload)
        XCTAssertNotNil(state.geoIP.lastSuccessfulRefreshAt)
        XCTAssertNil(state.geoIP.lastError)
    }

    func testStaleRefreshFailureFallsBackToMatchingCache() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        let fileURL = harness.appGroupStore.fileURL(named: "geoip.dat")
        let staleDate = Date(timeIntervalSinceNow: -(AppConfiguration.remoteGeoAssetRefreshInterval + 1))

        try validGeoIPPayload().write(to: fileURL)
        try harness.manager.saveRefreshState(
            RemoteGeoAssetRefreshState(
                geoIP: RemoteGeoAssetStatus(
                    sourceURLString: "https://example.com/geoip.dat",
                    lastSuccessfulRefreshAt: staleDate
                )
            )
        )
        MockURLProtocol.setHandler { request in
            (.response(request.url!, statusCode: 500), Data())
        }

        let state = try await harness.manager.refreshIfNeeded(settings: settings)

        XCTAssertEqual(MockURLProtocol.requestCount, 1)
        XCTAssertEqual(try Data(contentsOf: fileURL), validGeoIPPayload())
        XCTAssertEqual(state.geoIP.sourceURLString, "https://example.com/geoip.dat")
        XCTAssertNotNil(state.geoIP.lastError)
    }

    func testStaleRefreshFailureWithoutValidCacheThrows() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        MockURLProtocol.setHandler { request in
            (.response(request.url!, statusCode: 500), Data())
        }

        do {
            _ = try await harness.manager.refreshIfNeeded(settings: settings)
            XCTFail("Expected refresh to fail without a usable cache")
        } catch {
            XCTAssertEqual(MockURLProtocol.requestCount, 1)
            let state = try harness.manager.loadRefreshState()
            XCTAssertEqual(state.geoIP.sourceURLString, "https://example.com/geoip.dat")
            XCTAssertNotNil(state.geoIP.lastError)
        }
    }

    func testInvalidDownloadedPayloadDoesNotReplaceValidCache() async throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        let fileURL = harness.appGroupStore.fileURL(named: "geoip.dat")
        let staleDate = Date(timeIntervalSinceNow: -(AppConfiguration.remoteGeoAssetRefreshInterval + 1))

        try validGeoIPPayload().write(to: fileURL)
        try harness.manager.saveRefreshState(
            RemoteGeoAssetRefreshState(
                geoIP: RemoteGeoAssetStatus(
                    sourceURLString: "https://example.com/geoip.dat",
                    lastSuccessfulRefreshAt: staleDate
                )
            )
        )
        MockURLProtocol.setHandler { request in
            (.ok(request.url!), Data([0x01, 0x02, 0x03]))
        }

        let state = try await harness.manager.refreshIfNeeded(settings: settings)

        XCTAssertEqual(MockURLProtocol.requestCount, 1)
        XCTAssertEqual(try Data(contentsOf: fileURL), validGeoIPPayload())
        XCTAssertNotNil(state.geoIP.lastError)
    }

    func testRuntimePreparationUsesAppGroupAssetDirectoryAndRefreshes() throws {
        let harness = makeHarness()
        let settings = RemoteGeoAssetSettings(geoIPURLString: "https://example.com/geoip.dat")
        MockURLProtocol.setHandler { request in
            (.ok(request.url!), validGeoIPPayload())
        }

        let assetDirectory = try RemoteGeoAssetRuntimePreparation.assetDirectory(
            settings: settings,
            manager: harness.manager,
            bundledAssetDirectory: "/bundle/assets"
        )

        XCTAssertEqual(MockURLProtocol.requestCount, 1)
        XCTAssertEqual(assetDirectory, harness.appGroupStore.directoryURL().path)
        XCTAssertTrue(FileManager.default.fileExists(atPath: harness.appGroupStore.fileURL(named: "geoip.dat").path))
    }
}

private struct RemoteGeoAssetHarness {
    let appGroupStore: AppGroupStore
    let manager: RemoteGeoAssetManager
}

private func makeHarness() -> RemoteGeoAssetHarness {
    let appGroupStore = AppGroupStore(appGroupIdentifier: "tests.internet.\(UUID().uuidString.lowercased())")
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [MockURLProtocol.self]
    let session = URLSession(configuration: configuration)
    return RemoteGeoAssetHarness(
        appGroupStore: appGroupStore,
        manager: RemoteGeoAssetManager(
            appGroupStore: appGroupStore,
            session: session
        )
    )
}

private final class MockURLProtocol: URLProtocol {
    private static let lock = NSLock()
    private static var currentHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))?
    private static var currentRequestCount = 0

    static var requestCount: Int {
        lock.lock()
        defer { lock.unlock() }
        return currentRequestCount
    }

    static func setHandler(_ handler: @escaping (URLRequest) throws -> (HTTPURLResponse, Data)) {
        lock.lock()
        currentHandler = handler
        currentRequestCount = 0
        lock.unlock()
    }

    static func reset() {
        lock.lock()
        currentHandler = nil
        currentRequestCount = 0
        lock.unlock()
    }

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        Self.lock.lock()
        Self.currentRequestCount += 1
        let handler = Self.currentHandler
        Self.lock.unlock()

        guard let handler else {
            client?.urlProtocol(self, didFailWithError: NSError(domain: "tests", code: 0))
            return
        }

        do {
            let (response, data) = try handler(request)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}
}

private extension HTTPURLResponse {
    static func ok(_ url: URL) -> HTTPURLResponse {
        response(url, statusCode: 200)
    }

    static func response(_ url: URL, statusCode: Int) -> HTTPURLResponse {
        HTTPURLResponse(url: url, statusCode: statusCode, httpVersion: nil, headerFields: nil)!
    }
}

private func validGeoIPPayload(octet: UInt8 = 1) -> Data {
    Data([
        0x0A, 0x0E,
        0x0A, 0x02, 0x55, 0x53,
        0x12, 0x08,
        0x0A, 0x04, octet, octet, octet, 0x00,
        0x10, 0x18,
    ])
}

private func validGeoSitePayload() -> Data {
    let domain = Array("example.com".utf8)
    return Data(
        [
            0x0A, UInt8(10 + domain.count),
            0x0A, 0x02, 0x55, 0x53,
            0x12, UInt8(4 + domain.count),
            0x08, 0x01,
            0x12, UInt8(domain.count),
        ] + domain
    )
}
