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
- Initial hypothesis: `XmuxManager.GetXmuxClient` spends avoidable time allocating/filtering candidate slices and using cryptographic randomness for non-security-sensitive client selection.
- Secondary hypothesis: several XHTTP config fields are too brittle when left empty (xpadding/uplink data keys), which can produce malformed header/cookie names or ineffective obfuscation.
