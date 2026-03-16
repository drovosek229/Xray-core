# Autoresearch: xhttp xmux and request-shaping improvements

## Objective
Improve XHTTP (`transport/internet/splithttp`) with a focus on XMUX hot-path efficiency, safer/default-friendly request shaping, and compatibility fixes around xpadding/uplink metadata handling.

## Metrics
- **Primary**: `xmux_ns_sum` (ns, lower is better)
- **Secondary**: `bytes_sum`, `allocs_sum`

## How to Run
`./autoresearch.sh` — runs the XHTTP XMUX microbenchmarks in `golang:1.26` and prints `METRIC name=value` lines.

## Files in Scope
- `transport/internet/splithttp/mux.go` — XMUX client pool selection and warm-connection behavior.
- `transport/internet/splithttp/config.go` — XHTTP config normalization and request metadata helpers.
- `transport/internet/splithttp/behavior.go` — balanced-profile request shaping.
- `transport/internet/splithttp/xpadding.go` — xpadding placement/extraction helpers.
- `transport/internet/splithttp/hub.go` — server-side request validation / payload extraction.
- `transport/internet/splithttp/*_test.go` — targeted tests and benchmarks for XHTTP.

## Off Limits
- Generated protobuf files (`*.pb.go`) unless absolutely required.
- Non-XHTTP transports and unrelated proxy packages.
- Public API churn outside the existing XHTTP config surface.

## Constraints
- No new Go dependencies.
- Preserve existing XHTTP behavior unless the change is clearly a bug fix or compatibility improvement.
- `docker run golang:1.26 go test ./transport/internet/splithttp` must pass for kept changes.
- Keep benchmark harness Docker-based because the host toolchain cannot run `go 1.26` natively.

## What's Been Tried
- Added a dedicated XMUX benchmark harness so pool-selection changes can be measured directly.
- Baseline on the new harness: `xmux_ns_sum=25229`.
- Initial hypothesis confirmed: `XmuxManager.GetXmuxClient` spent avoidable time allocating/filtering candidate slices and using cryptographic randomness for non-security-sensitive client selection.
- Kept candidate: switched XMUX selection to a per-manager pseudo-random picker under the existing mutex, removed per-call candidate-slice allocation, and compacted unusable-client removal in place. This dropped the benchmark to `xmux_ns_sum=4170.20` with `0 B/op` and `0 allocs/op` on the benchmark harness.
- Kept follow-up: fused XMUX unusable-client sweeping, eligible-client selection, and warm-pool accounting into the same locked pass. That reduced the harness again to `xmux_ns_sum=2043.80` while preserving `0 B/op` and `0 allocs/op`.
- Secondary hypothesis addressed in the same kept work: several XHTTP config fields were too brittle when left empty (xpadding/uplink data keys). Added normalization defaults, query-preserving Referer padding, method normalization, and coverage tests.
