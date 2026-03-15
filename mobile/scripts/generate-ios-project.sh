#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROJECT_SPEC="${ROOT_DIR}/mobile/ios-client/project.yml"
PROJECT_ROOT="${ROOT_DIR}/mobile/ios-client"
PROJECT_DIR="${PROJECT_ROOT}/XrayIOSClient.xcodeproj"

if ! command -v xcodegen >/dev/null 2>&1; then
  echo "xcodegen is required. Install it with:"
  echo "  brew install xcodegen"
  exit 1
fi

if [[ "${SKIP_BRIDGE_BUILD:-0}" != "1" ]]; then
  "${ROOT_DIR}/mobile/scripts/build-ios-xcframework.sh"
fi

rm -rf "${PROJECT_DIR}"
xcodegen generate --spec "${PROJECT_SPEC}" --project "${PROJECT_ROOT}"

echo "Generated ${PROJECT_DIR}"
