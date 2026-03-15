import Foundation
import UIKit
import XrayAppCore

enum DeviceFingerprintProvider {
    static func make(hwid: String) -> SubscriptionClientFingerprint {
        SubscriptionClientFingerprint(
            userAgent: "\(AppConfiguration.userAgentName)/\(AppConfiguration.appVersion) (iOS \(UIDevice.current.systemVersion); \(modelIdentifier()))",
            hwid: hwid,
            deviceOS: "iOS",
            osVersion: UIDevice.current.systemVersion,
            deviceModel: modelIdentifier()
        )
    }

    private static func modelIdentifier() -> String {
        var systemInfo = utsname()
        uname(&systemInfo)
        let mirror = Mirror(reflecting: systemInfo.machine)
        return mirror.children.reduce(into: "") { value, element in
            guard let valuePart = element.value as? CChar, valuePart != 0 else {
                return
            }
            value.append(Character(UnicodeScalar(UInt8(bitPattern: valuePart))))
        }
    }
}
