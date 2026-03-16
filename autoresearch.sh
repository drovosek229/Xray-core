#!/bin/bash
set -euo pipefail

image="${XHTTP_GO_IMAGE:-golang:1.26}"
repo_dir="$(pwd)"

docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image" >/dev/null

output="$(docker run --rm -v "$repo_dir":/src -w /src "$image" bash -lc '
  /usr/local/go/bin/go test ./transport/internet/splithttp -run "^$" -bench "^(BenchmarkXmuxManagerGetXmuxClient|BenchmarkXHTTPRequestShaping)" -benchmem -count=5
' 2>&1)"

printf '%s
' "$output"

xhttp_ns_sum="$(printf '%s
' "$output" | awk '
  /ns\/op/ {
    for (i = 1; i <= NF; i++) if ($i == "ns/op") { sum += $(i - 1); count++ }
  }
  END { if (count == 0) exit 1; printf "%.2f", sum }
')"
bytes_sum="$(printf '%s
' "$output" | awk '
  /B\/op/ {
    for (i = 1; i <= NF; i++) if ($i == "B/op") sum += $(i - 1)
  }
  END { printf "%.2f", sum }
')"
allocs_sum="$(printf '%s
' "$output" | awk '
  /allocs\/op/ {
    for (i = 1; i <= NF; i++) if ($i == "allocs/op") sum += $(i - 1)
  }
  END { printf "%.2f", sum }
')"

echo "METRIC xhttp_ns_sum=${xhttp_ns_sum}"
echo "METRIC bytes_sum=${bytes_sum}"
echo "METRIC allocs_sum=${allocs_sum}"
