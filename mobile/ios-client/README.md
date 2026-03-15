# internet

This directory contains a native iOS client scaffold for your forked Xray core. The current visible app brand is `internet`.

- `Package.swift`: shared Swift package with models, subscription parsing, and Xray runtime config generation.
- `App/`: SwiftUI host app sources.
- `PacketTunnel/`: `NEPacketTunnelProvider` sources.
- `Shared/`: storage, logging, and repository code shared between the app and the tunnel extension.
- `project.yml`: XcodeGen project spec.
- `Branding/internet-icon.svg`: vector source for the app icon.

## Prerequisites

1. Install full Xcode and select it:
   ```bash
   sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
   ```
2. Install `gomobile`:
   ```bash
   go install golang.org/x/mobile/cmd/gomobile@latest
   ```
3. Install XcodeGen:
   ```bash
   brew install xcodegen
   ```

## Build the Xray bridge

```bash
./mobile/scripts/build-ios-xcframework.sh
```

This produces:

```text
mobile/ios-client/Frameworks/XrayCore.xcframework
```

## Generate the Xcode project

```bash
./mobile/scripts/generate-ios-project.sh
```

This produces:

```text
mobile/ios-client/XrayIOSClient.xcodeproj
```

## Local-only iOS identifiers

Tracked files now use generic placeholder identifiers. Put your real Team ID and bundle/app-group values in:

```text
mobile/ios-client/Config/Local.xcconfig
```

That file is gitignored.

If you need to recreate it on a fresh machine:

```bash
cp mobile/ios-client/Config/Local.xcconfig.example mobile/ios-client/Config/Local.xcconfig
```

Then fill in:

- `DEVELOPMENT_TEAM_ID`
- `APP_BUNDLE_IDENTIFIER`
- `PACKET_TUNNEL_BUNDLE_IDENTIFIER`
- `APP_GROUP_IDENTIFIER`
- `SHARED_KEYCHAIN_SERVICE`
- `SHARED_KEYCHAIN_SUFFIX`

## Branding

The visible app branding now comes from `project.yml` and the icon generator:

- App name: `APP_DISPLAY_NAME`
- VPN label: `APP_TUNNEL_DISPLAY_NAME`
- User-Agent product label: `APP_USER_AGENT_NAME`

To change the brand later:

1. Edit these values in `mobile/ios-client/project.yml`.
2. Edit `mobile/ios-client/Branding/internet-icon.svg` if you want a different icon.
3. Regenerate the PNG icon set:
   ```bash
   ./mobile/scripts/generate-ios-icon.sh
   ```
4. Regenerate the Xcode project:
   ```bash
   SKIP_BRIDGE_BUILD=1 ./mobile/scripts/generate-ios-project.sh
   ```
5. Rebuild/install from Xcode.

The internal project file still stays at:

```text
mobile/ios-client/XrayIOSClient.xcodeproj
```

## Before installing to a real device

Then:

1. Connect the iPhone.
2. Enable Developer Mode on the device.
3. Enable the `Network Extensions` and `Personal VPN` capabilities in Xcode.
4. Build and install the `XrayIOSClient` target on an iPhone running iOS 26 or newer. The installed app name will be `internet`.

## Notes

- The Swift package tests run locally with `swift test --package-path mobile/ios-client`.
- The Go bridge tests run with `go test ./mobile/iosbridge`.
- The first version expects a supported subscription link that emits a `VLESS + XHTTP` Xray JSON template over `REALITY` or `TLS`.
- The app and tunnel extension now target iOS 26 as the minimum deployment version.
