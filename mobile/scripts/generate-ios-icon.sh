#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SVG_PATH="${ROOT_DIR}/mobile/ios-client/Branding/internet-icon.svg"
ICONSET_DIR="${ROOT_DIR}/mobile/ios-client/App/Resources/Assets.xcassets/AppIcon.appiconset"

if ! command -v magick >/dev/null 2>&1; then
  echo "ImageMagick is required. Install it with:"
  echo "  brew install imagemagick"
  exit 1
fi

if [[ ! -f "${SVG_PATH}" ]]; then
  echo "Missing icon source: ${SVG_PATH}"
  exit 1
fi

mkdir -p "${ICONSET_DIR}"

render_icon() {
  local size="$1"
  local filename="$2"

  magick -background none "${SVG_PATH}" -resize "${size}x${size}" "${ICONSET_DIR}/${filename}"
}

render_icon 40 "internet-AppIcon-20@2x.png"
render_icon 60 "internet-AppIcon-20@3x.png"
render_icon 58 "internet-AppIcon-29@2x.png"
render_icon 87 "internet-AppIcon-29@3x.png"
render_icon 80 "internet-AppIcon-40@2x.png"
render_icon 120 "internet-AppIcon-40@3x.png"
render_icon 120 "internet-AppIcon-60@2x.png"
render_icon 180 "internet-AppIcon-60@3x.png"
render_icon 20 "internet-AppIcon-20@1x~ipad.png"
render_icon 40 "internet-AppIcon-20@2x~ipad.png"
render_icon 29 "internet-AppIcon-29@1x~ipad.png"
render_icon 58 "internet-AppIcon-29@2x~ipad.png"
render_icon 40 "internet-AppIcon-40@1x~ipad.png"
render_icon 80 "internet-AppIcon-40@2x~ipad.png"
render_icon 76 "internet-AppIcon-76@1x.png"
render_icon 152 "internet-AppIcon-76@2x.png"
render_icon 167 "internet-AppIcon-83.5@2x.png"
render_icon 1024 "internet-AppIcon-1024.png"

echo "Generated iOS app icons in ${ICONSET_DIR}"
