#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACKAGE_PATH="${ROOT_DIR}/mobile/iosbridge"
OUTPUT_DIR="${ROOT_DIR}/mobile/ios-client/Frameworks"
OUTPUT_PATH="${OUTPUT_DIR}/XrayCore.xcframework"
GOMOBILE_BIN="${GOMOBILE_BIN:-$(command -v gomobile || true)}"
GOBIN_DIR="$(go env GOBIN 2>/dev/null || true)"
GOPATH_DIR="$(go env GOPATH 2>/dev/null || true)"

if ! command -v xcodebuild >/dev/null 2>&1 || ! xcodebuild -version >/dev/null 2>&1; then
  echo "xcodebuild is required. Install full Xcode and run:"
  echo "  sudo xcode-select -s /Applications/Xcode.app/Contents/Developer"
  exit 1
fi

if [[ -z "${GOMOBILE_BIN}" ]]; then
  if [[ -n "${GOBIN_DIR}" && -x "${GOBIN_DIR}/gomobile" ]]; then
    GOMOBILE_BIN="${GOBIN_DIR}/gomobile"
  elif [[ -n "${GOPATH_DIR}" && -x "${GOPATH_DIR}/bin/gomobile" ]]; then
    GOMOBILE_BIN="${GOPATH_DIR}/bin/gomobile"
  else
    echo "gomobile is required. Install it with:"
    echo "  go install golang.org/x/mobile/cmd/gomobile@latest"
    exit 1
  fi
fi

if [[ -n "${GOBIN_DIR}" ]]; then
  export PATH="${GOBIN_DIR}:${PATH}"
fi
if [[ -n "${GOPATH_DIR}" ]]; then
  export PATH="${GOPATH_DIR}/bin:${PATH}"
fi
if [[ -z "${GOFLAGS:-}" ]]; then
  export GOFLAGS="-mod=mod"
elif [[ " ${GOFLAGS} " != *" -mod=mod "* ]]; then
  export GOFLAGS="${GOFLAGS} -mod=mod"
fi

mkdir -p "${OUTPUT_DIR}"

pushd "${ROOT_DIR}" >/dev/null
"${GOMOBILE_BIN}" init
rm -rf "${OUTPUT_PATH}"
"${GOMOBILE_BIN}" bind -target=ios -o "${OUTPUT_PATH}" "${PACKAGE_PATH}"
popd >/dev/null

echo "Built ${OUTPUT_PATH}"
