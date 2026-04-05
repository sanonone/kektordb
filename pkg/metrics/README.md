# Module: pkg/metrics

## Purpose

Defines a set of Prometheus metrics for monitoring KektorDB's HTTP layer and vector indexing subsystem. Three metric types (counter, histogram, gauge) with custom histogram buckets deliberately wide enough to capture everything from 5ms cache hits to 60s LLM generation latencies.

## Key Types & Critical Paths

**Critical variables (package-level globals via `promauto`):**
- `HttpRequestsTotal` -- `prometheus.CounterVec` with labels: `method`, `path`, `status`.
- `HttpRequestDuration` -- `prometheus.HistogramVec` with labels: `method`, `path`. Buckets: `[0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60]` seconds.
- `TotalVectors` -- `prometheus.GaugeVec` with labels: `index_name`.

**Critical paths (hot functions):**
- `HttpRequestDuration.With(labels).Observe(duration)` -- Called by logging middleware after every HTTP request. Histogram observation.
- `HttpRequestsTotal.With(labels).Inc()` -- Called by logging middleware after every HTTP request. Counter increment.
- `TotalVectors.With(labels).Set(count)` -- Called by engine/index operations when vector count changes.

## Architecture & Data Flow

**Three metrics using `promauto` for auto-registration:**

| Metric | Type | Name | Labels | Purpose |
|--------|------|------|--------|---------|
| `HttpRequestsTotal` | `CounterVec` | `kektordb_http_requests_total` | `method`, `path`, `status` | Total HTTP request count by endpoint and response code |
| `HttpRequestDuration` | `HistogramVec` | `kektordb_http_request_duration_seconds` | `method`, `path` | Request latency distribution |
| `TotalVectors` | `GaugeVec` | `kektordb_vectors_total` | `index_name` | Current vector count per index |

**Histogram buckets:** `[0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60]` seconds. Deliberately wide range to monitor if HyDe is slowing things down (comparing 5ms cache hits vs 60s LLM generation).

**Global variables:** Metrics are package-level globals, initialized at `init()` time via `promauto`. This is the standard Prometheus Go pattern -- no manual registration boilerplate.

## Cross-Module Dependencies

**Depends on:**
- `github.com/prometheus/client_golang/prometheus` / `promauto` -- Prometheus Go client library.

**Used by:**
- `internal/server/middleware.go` -- Logging middleware calls `HttpRequestsTotal.Inc()` and `HttpRequestDuration.Observe()` after every request.
- `pkg/engine` -- Updates `TotalVectors` when vectors are added or deleted from an index.
- `/metrics` HTTP endpoint -- Exposed by the server (likely via `promhttp.Handler()`), scraped by Prometheus.

## Concurrency & Locking Rules

**Prometheus handles all concurrency:** `CounterVec`, `HistogramVec`, and `GaugeVec` are all goroutine-safe internally. The `promauto` package registers metrics at package initialization time, so there is no race on registration.

**No explicit synchronization needed:** This module only declares metrics. The actual HTTP middleware that increments counters and observes histograms lives in the server layer (`internal/server/middleware.go`).

## Known Pitfalls / Gotchas

- **Metrics persist across tests** -- Since metrics are package-level globals registered at `init()` time, they persist across test runs. If you run multiple tests that increment counters, the values accumulate. You need to explicitly reset metrics between tests or use a custom registry.
- **No custom registry** -- Uses the default Prometheus registry (`prometheus.DefaultRegisterer`). If you import other libraries that also register metrics, there could be name collisions. For production isolation, a custom `prometheus.NewRegistry()` would be safer.
- **Limited metric coverage** -- Only 3 metrics are defined. Missing potentially useful metrics: error rates by endpoint, active connections, KV store operation latency, cache hit/miss ratios, authentication failure counts, GC stats, HNSW search latency, graph traversal depth distribution.
- **Histogram buckets may miss sub-millisecond latency** -- The smallest bucket is 5ms. For very fast operations (KV Get, which can be <1ms), all observations fall into the `0.005` bucket, losing granularity.
- **`TotalVectors` is a gauge, not a counter** -- It reflects the current count, not the rate of change. If you want to track ingestion rate, you need to compute the derivative in Prometheus (e.g., `rate(kektordb_vectors_total[5m])`), which only works if the gauge is monotonically increasing (it's not -- vectors can be deleted).

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Global variables** | Package-level globals via `promauto` | Standard Prometheus Go pattern; makes testing harder (metrics persist across tests) |
| **No middleware defined here** | Metrics declared separately from collection | Separation of concerns; middleware lives in `internal/server` |
| **Limited metric coverage** | Only 3 metrics defined | Missing: error rates, active connections, KV operation latency, cache hit/miss ratios, auth failure counts, GC stats |
| **No custom registry** | Uses default Prometheus registry | Simpler; for production isolation, a custom `prometheus.NewRegistry()` would avoid name collisions |
| **Wide histogram buckets** | 5ms to 60s range | Captures the full spectrum from fast KV ops to slow LLM calls; fewer buckets would lose granularity |
