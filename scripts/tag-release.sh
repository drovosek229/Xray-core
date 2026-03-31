#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Create an annotated Go module release tag for this fork.

Usage:
  scripts/tag-release.sh <tag> [ref]

Examples:
  scripts/tag-release.sh v1.260401.0
  scripts/tag-release.sh v1.260401.1 HEAD~1
EOF
}

if [[ "${1:-}" == "" ]] || [[ "${1:-}" == "-h" ]] || [[ "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ "$#" -gt 2 ]]; then
  usage >&2
  exit 1
fi

tag="$1"
ref="${2:-HEAD}"
semver_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+)*)?(\+([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+)*)?$'

if ! [[ "$tag" =~ $semver_pattern ]]; then
  echo "error: tag must be valid Go semver with a v prefix, for example v1.260401.0" >&2
  exit 1
fi

if ! git rev-parse --verify --quiet "$ref^{commit}" >/dev/null; then
  echo "error: ref $ref does not resolve to a commit" >&2
  exit 1
fi

if git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null; then
  echo "error: tag $tag already exists" >&2
  exit 1
fi

commit="$(git rev-parse --verify "$ref^{commit}")"
subject="$(git show -s --format=%s "$commit")"

git tag -a "$tag" "$commit" -m "$tag

$subject"

echo "created annotated tag $tag at $(git rev-parse --short "$commit")"
echo "push with: git push origin refs/tags/$tag"
