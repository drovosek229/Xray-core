import Foundation

#if canImport(XrayCore)
import XrayCore
#endif

enum XrayEngineBridgeError: LocalizedError {
    case frameworkMissing

    var errorDescription: String? {
        switch self {
        case .frameworkMissing:
            return "XrayCore.xcframework is missing. Build it with mobile/scripts/build-ios-xcframework.sh."
        }
    }
}

final class XrayEngineBridge {
    #if canImport(XrayCore)
    private var engine: IosbridgeXrayEngine?
    #endif

    func version() -> String {
        #if canImport(XrayCore)
        return engine?.version() ?? "framework-loaded"
        #else
        return "framework-missing"
        #endif
    }

    func validate(configJSON: String) throws {
        #if canImport(XrayCore)
        let engine = try resolvedEngine()
        try engine.validate(configJSON)
        #else
        throw XrayEngineBridgeError.frameworkMissing
        #endif
    }

    func start(configJSON: String, tunFD: Int = -1, assetDir: String = "") throws {
        #if canImport(XrayCore)
        let engine = try resolvedEngine()
        try engine.start(configJSON, tunFD: tunFD, assetDir: assetDir)
        #else
        throw XrayEngineBridgeError.frameworkMissing
        #endif
    }

    func stop() {
        #if canImport(XrayCore)
        guard let engine else {
            return
        }
        try? engine.stop()
        #endif
    }

    var isRunning: Bool {
        #if canImport(XrayCore)
        return engine?.isRunning() ?? false
        #else
        return false
        #endif
    }

    #if canImport(XrayCore)
    private func resolvedEngine() throws -> IosbridgeXrayEngine {
        if let engine {
            return engine
        }
        guard let created = IosbridgeNewXrayEngine() else {
            throw XrayEngineBridgeError.frameworkMissing
        }
        engine = created
        return created
    }
    #endif
}
