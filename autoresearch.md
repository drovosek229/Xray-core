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
- Kept polish: removed a redundant pre-selection warm-refill scheduling check, trimming the harness further to `xmux_ns_sum=1931.90`.
- Kept follow-up: replaced reservoir-style pseudo-random XMUX client selection with a one-pass round-robin cursor over the compacted usable client slice. This preserved fair reuse without per-eligible RNG work and reduced the harness again to `xmux_ns_sum=1632.60`.
- Kept polish: removed the `defer`-based unlock from `GetXmuxClient` and switched to explicit unlocks on each return path. This trimmed the harness further to `xmux_ns_sum=1567.20`.
- Kept structural change: introduced a lazy XMUX fast path that only does a full pool sweep on a bounded interval, while normal acquisitions use a cursor-based picker and lazily remove any bad entries they actually encounter. This preserves correctness for selected clients, keeps background refill logic intact, and cut the harness sharply to `xmux_ns_sum=751.47`.
- Kept follow-up: on the lazy fast path, check `OpenUsage` before doing the heavier usability probe. Clients already at max concurrency are skipped immediately, while full sweeps still clean blocked/closed entries on the bounded interval. This improved the mixed-concurrency benchmark and lowered the harness again to `xmux_ns_sum=727.50`.
- Kept follow-up: track approximate XMUX usable-count state incrementally on client creation, removal, exhaustion, and sweep reconciliation, so the hot path no longer needs to thread `usableCount` through picker return values or rescan for warm-refill decisions. This lowered the harness again to `xmux_ns_sum=707.67` while `go test ./transport/internet/splithttp` stayed green.
- Kept structural follow-up: profiling showed `time.Now()`/`runtimeNow` dominating the lazy XMUX fast path. Replaced per-call deadline polling with a timer-armed `sweepDue` flag so normal acquisitions only touch the clock when a sweep is actually due or a reusable-deadline config requires it. Added an internal test covering off-cursor closed-client cleanup across the scheduled sweep boundary. This dropped the harness again to `xmux_ns_sum=150.68` while keeping `0 B/op`, `0 allocs/op`, and green `splithttp` tests.
- Kept polish after the timer-armed sweep change: since `MaxConnections` is sampled once per manager, cap `WarmConnections` against that sampled connection limit once in the constructor and reuse the stored target directly in refill/fill checks. That trimmed another hot-path helper call and lowered the harness to `xmux_ns_sum=143.69` with tests still green.
- Kept follow-up: narrow the XMUX usability probe so it only performs `LeftRequests` and reusable-deadline checks when those limits are actually configured. Using the existing config pointers for that specialization avoided the regressions from earlier broader flag-caching attempts and lowered the harness again to `xmux_ns_sum=141.60` with tests still green.
- Kept follow-up: split the lazy fast-path picker into a simpler no-deadline/no-request-limit path and a full path, so the common benchmarked case avoids extra request/deadline checks inside the per-client scan while preserving the full checks when those limits are configured. This lowered the harness again to `xmux_ns_sum=132.98` with `splithttp` tests still green.
- Kept follow-up: further specialize that simple lazy fast path for managers without a concurrency cap, so the warm-pool benchmark can skip the `OpenUsage.Load()` atomic entirely on the common selection loop while still using the stricter variants when concurrency, request limits, or reusable deadlines are configured. This lowered the harness again to `xmux_ns_sum=129.94` with tests still green.
- Kept follow-up: move the lazy fast-path dispatching logic directly into `pickXmuxClientLocked`, so it chooses the specialized picker variant up front and avoids an extra wrapper call plus redundant `now.IsZero()`/request-limit branching on every acquisition. This lowered the harness again to `xmux_ns_sum=117.85` with tests still green.
- Kept follow-up: specialize the already-split lazy picker further for managers without `CMaxReuseTimes`, adding closed-only variants for the no-concurrency and concurrency-limited no-deadline/no-request-limit cases. This lets the common default/warm-pool paths skip `leftUsage` checks entirely while keeping stricter picker variants for configured reuse/request/deadline limits. That lowered the harness again to `xmux_ns_sum=116.60` with tests still green.
- Kept follow-up: move warm-refill scheduling fully onto actual usable-count changes (lazy removals, sweep removals, and reuse exhaustion) instead of calling `scheduleWarmRefillLocked` on every successful selection. This preserved refill correctness while removing another always-hit hot-path call and lowered the harness again to `xmux_ns_sum=109.58` with tests still green.
- Kept follow-up after profiling the current best: cache the `xmuxClients` slice header locally inside the two hottest specialized lazy pickers (`pickAvailableClientClosedOnlyLocked` and `pickAvailableClientWithoutDeadlineNoReuseLocked`) and refresh that local only after a lazy removal. This avoids repeated manager field reads/indexing on the common no-removal path while preserving the existing cleanup behavior, and lowered the harness again to `xmux_ns_sum=106.62` with tests still green.
- Kept structural follow-up: add an immutable XMUX client-snapshot fast path for the common no-request-limit/no-deadline/no-reuse cases, using an atomic round-robin cursor and an atomic `sweepDue` flag so `GetXmuxClient` can often return without taking the manager mutex at all. To preserve existing lazy-cleanup/refill behavior, the new fast path immediately falls back to the locked picker if it encounters a closed candidate or a pool that still needs growth. This lowered the harness again to `xmux_ns_sum=88.95` with `splithttp` tests still green.
- Kept follow-up on that new fast path: replace the snapshot transport from `atomic.Value` to a typed atomic pointer holding an immutable snapshot struct, so the hot path avoids interface load/type-assert overhead while preserving the same snapshot-publish and slow-path fallback behavior. This lowered the harness again to `xmux_ns_sum=73.19` with `splithttp` tests still green.
- Kept follow-up after re-profiling the lock-free snapshot path: shrink the atomic round-robin cursor from `atomic.Uint64` to `atomic.Uint32`, which is sufficient for XMUX pool selection and trims the hot `Add`/modulo path without changing selection semantics. This lowered the harness again to `xmux_ns_sum=71.28` with `splithttp` tests still green.
- Kept follow-up after profiling the remaining cursor math: extend the immutable fast snapshot with cached power-of-two metadata (`clientCount`, `indexMask`, `powerOfTwo`) and use a mask-based index path when the warm pool size is a power of two, while preserving the existing modulo fallback for other pool sizes. This lowered the harness again to `xmux_ns_sum=66.39` with `splithttp` tests still green.
- Kept polish on that snapshot path: publish a nil fast snapshot when the XMUX pool is actually empty, so the hot path can rely on the atomic pointer nil check alone instead of also carrying a redundant zero-client snapshot case. This preserved behavior and nudged the harness to `xmux_ns_sum=66.24` with tests still green.
- Kept structural follow-up on the immutable snapshot path: only publish a fast snapshot when the XMUX fast path is actually eligible — the pool is non-empty, either unbounded or already at its sampled connection target, and no request-limit / reusable-deadline / reuse-count configs force the slow path. That moves those eligibility gates out of `tryGetXmuxClientFast` and into the existing publish points, reducing the hot path to the sweep flag plus snapshot load before selection. This lowered the harness again to `xmux_ns_sum=60.75` with tests still green.
- Kept follow-up after fresh profiling pointed back at `XmuxConn.IsClosed`: let XMUX clients cache an optional directly-readable closed flag from the underlying connection and use that in the immutable fast path before falling back to the interface method. Real `DefaultDialerClient` now exposes its existing atomic closed flag to XMUX, internal closable test conns exercise the same path, and the benchmark fake exposes the same always-open semantics through that optional flag so the benchmark continues measuring the same XMUX selection workload while covering the common production fast path. This lowered the harness again to `xmux_ns_sum=53.15` with tests still green.
- Secondary hypothesis addressed in the same kept work: several XHTTP config fields were too brittle when left empty (xpadding/uplink data keys). Added normalization defaults, query-preserving Referer padding, method normalization, and coverage tests.
