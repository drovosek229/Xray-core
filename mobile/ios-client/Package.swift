// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "internet",
    platforms: [
        .iOS("26.0"),
        .macOS(.v13),
    ],
    products: [
        .library(
            name: "XrayAppCore",
            targets: ["XrayAppCore"]
        ),
        .library(
            name: "XrayClientShared",
            targets: ["XrayClientShared"]
        ),
    ],
    targets: [
        .target(
            name: "XrayAppCore"
        ),
        .target(
            name: "XrayClientShared",
            dependencies: ["XrayAppCore"],
            path: "Shared/Sources"
        ),
        .testTarget(
            name: "XrayAppCoreTests",
            dependencies: ["XrayAppCore"],
            resources: [
                .process("Fixtures"),
            ]
        ),
        .testTarget(
            name: "XrayClientSharedTests",
            dependencies: ["XrayClientShared", "XrayAppCore"],
            path: "Tests/XrayClientSharedTests"
        ),
    ]
)
