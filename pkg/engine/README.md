# Module: pkg/engine

## Purpose

The high-level orchestration layer that unifies vector CRUD, property graph operations, hybrid vector+text search, and human-memory-inspired ranking into a single coherent API. Sits between the low-level `core` package (HNSW, KV, metadata) and the `persistence` package (AOF, snapshots), providing the primary interface for all data operations in KektorDB.

## Key Types & Critical Paths

**Critical structs:**
- `Engine` -- Top-level orchestrator: `*core.DB`, `*persistence.LazyAOFWriter`, `graphLocks [128]sync.Mutex`, `adminMu sync.Mutex`, `dirtyCounter int64`, `aofBaseSize int64`, `isRewriting atomic.Bool`, `closeOnce sync.Once`, `wg sync.WaitGroup`.
- `GraphEdge` -- `{TargetID, CreatedAt, DeletedAt int64, Weight float32, Props json.RawMessage}` -- Stored in KV store, not in core's graph.
- `GraphQuery` -- `{RootID, Relations []string, Direction string, MaxDepth int}` -- Traversal criteria for graph-filtered search.
- `EventBus` -- Fan-out pub/sub: `subscribers map[int]chan Event`, `mu sync.RWMutex`. Non-blocking sends.

**Critical paths (hot functions):**
- `VSearch()` / `searchWithFusion()` -- Hybrid search: parallel vector + text goroutines, score normalization, alpha fusion, time-decay.
- `VLink()` / `VUnlink()` -- Graph edge read-modify-write under 128-shard locks. AOF write after mutation.
- `VReinforce()` -- Updates `_last_accessed` and `_access_count`. High-frequency call for memory ranking.
- `FindPath()` -- Bidirectional BFS. Default max depth 4. Requires at least one relation type.
- `RewriteAOF()` -- Deadlock-free v2: Snapshot + Release pattern. Uses `atomic.CompareAndSwap` on `isRewriting`.

## Architecture & Data Flow

**Hybrid search pipeline (`VSearch`):** Parses SQL-like boolean filters and `CONTAINS(field, 'text')` regex clauses, builds a roaring bitmap allowlist from metadata filters, optionally intersects with a graph-traversal allowlist (BFS from `GraphQuery.RootID`, max depth 5), then runs vector search and text search in parallel goroutines with `sync.WaitGroup`. Results are normalized (vector: `1/(1+distance)`, text: min-max to 0..1) and fused with configurable `alpha` weight. Time-decay factors are applied if memory mode is enabled.

**Graph operations:** Edges are stored in the KV store with namespace-aware keys (`indexName::nodeID`) using `rel:` and `rev:` prefixes for forward and reverse indices. `VLink`/`VUnlink` perform read-modify-write cycles under 128-shard graph locks. `VExtractSubgraph` uses BFS (max depth 3) with optional semantic gating -- a `guideQuery` vector prunes neighbors that are semantically dissimilar from the guide concept.

**Pathfinding (`FindPath`):** Bidirectional BFS for shortest-path discovery. Maintains separate forward and backward search frontiers, expands both simultaneously, and checks for intersection at each level. Default max depth: 4; requires at least one relation type (no blind traversal).

**Memory reinforcement (`VReinforce`):** Updates `_last_accessed` timestamp and increments `_access_count`. Four decay models are supported: Exponential (`2^(-age/halfLife)`), Linear (`max(0, 1 - age/halfLife)`), Step (cliff-edge at half-life), and Ebbinghaus (`e^(-age/S)` where `S = halfLife * (1 + ln(1 + accessCount))` -- the spacing effect). Decay uses the newer of `_created_at` or `_last_accessed`, so frequently accessed memories stay fresh.

**Persistence:** AOF uses lazy batching (100ms flush, 1s fsync). Snapshot writes to `.tmp` then atomic `os.Rename`. AOF Rewrite uses the "Snapshot + Release" pattern to avoid deadlocks.

## Cross-Module Dependencies

**Depends on:**
- `pkg/core` -- Primary dependency. All vector/graph/metadata operations delegate to `core.DB`.
- `pkg/persistence` -- AOF writer (`LazyAOFWriter`), snapshot saving, AOF rewriting, crash recovery.
- `pkg/textanalyzer` -- Indirectly via core's BM25 full-text index.

**Used by:**
- `internal/server` -- Primary consumer. Every HTTP handler calls engine methods (VAdd, VSearch, VLink, etc.).
- `internal/mcp` -- Primary consumer. All 17 MCP tools delegate to engine methods.
- `pkg/proxy` -- Uses `VSearch`, `VSearchGraph`, `VDelete` for RAG, cache invalidation, and semantic firewall.
- `pkg/rag` -- `KektorAdapter` wraps engine to implement the `Store` and `AdaptiveStore` interfaces.
- `pkg/cognitive` -- Gardener uses VSearch, VLink, VUnlink, VGetLinks, VGetRelations, VReinforce for reflection analysis.

## Concurrency & Locking Rules

**Multi-tiered locking:**
1. **`adminMu`** (`sync.Mutex`) -- Protects engine-level administrative operations (snapshot, AOF rewrite). Rare, long-running, needs global consistency.
2. **`graphLocks`** (`[128]sync.Mutex`) -- 128 shard mutexes keyed by FNV-1a hash of the graph key. Allows high concurrency on different nodes while protecting read-modify-write cycles.
3. **Core DB locks** -- Delegated to `core.DB`'s internal granular locks (`sync.RWMutex` for store, per-index `metaMu` for HNSW).

**Atomic counters:** `dirtyCounter` (int64), `aofBaseSize` (int64), `isRewriting` (bool) all use `sync/atomic` for lock-free coordination with background tasks.

**Deadlock-free AOF Rewrite (v2 fix):** Uses "Snapshot + Release" -- briefly acquires `DB.RLock()` to copy data, releases it, then iterates over HNSW indexes independently. This prevents the classic deadlock where Rewrite held `s.mu` while acquiring `h.metaMu`, blocking `AddBatch` which held `h.metaMu` and waited for `s.mu`.

**Background goroutines for cascade operations:** Cascade delete and lazy self-repair launch `go func()` goroutines to avoid blocking the API response. These are best-effort and thread-safe via `VUnlink`.

**Non-blocking EventBus:** Fan-out pub/sub with `sync.RWMutex` protecting the subscriber map. Uses non-blocking sends (`select` with `default`) to drop events for slow consumers rather than blocking the write path.

**Close-once pattern:** `closeOnce sync.Once` ensures `Close()` is idempotent. `sync.WaitGroup` + `closed` channel for clean shutdown of all background tasks.

## Known Pitfalls / Gotchas

- **NEVER hold `adminMu` while calling core methods that acquire `metaMu`** -- This was the v1 deadlock. The v2 fix copies data under brief `RLock`, releases it, then processes. If you add a new admin operation, follow this pattern.
- **`VImport` bypasses AOF** -- Data is NOT durable until `VImportCommit` is called (which forces a snapshot). If the process crashes between import and commit, all imported data is lost.
- **Cascade delete is fire-and-forget** -- `VDelete` launches a background goroutine to soft-delete incoming edges. If the process crashes before the goroutine completes, dangling edges remain. Lazy self-repair in `VGetConnections` cleans them on next read.
- **EventBus drops events for slow subscribers** -- If the cognitive gardener's event channel (buffer: 64) fills up, events are silently dropped. This means the gardener may miss write events during sustained high-throughput periods.
- **`RewriteAOF` uses `atomic.CompareAndSwap`** -- Only one rewrite can run at a time. If you call it while another is running, it returns immediately with no error. Check the return value.
- **Graph edges use `json.RawMessage` for props** -- This avoids GC pressure but means you must manually `json.Unmarshal` when reading. Never store untrusted JSON in edge props -- it's passed directly to the KV store.
- **Parallel search goroutines share no state** -- Vector and text search run in parallel goroutines with `sync.WaitGroup`. They are safe because both are read-only. Do NOT add shared mutable state between them.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **AOF lazy batching** | Up to 1s of data loss on crash | 10-100x throughput improvement; AOF is append-only so replay recovers everything up to the last flush |
| **Background cascade delete** | Temporary graph inconsistency after node deletion | Non-blocking API; self-repair cleans up on next read |
| **EventBus drops slow consumers** | Event loss under load | Prevents backpressure from blocking the database write path |
| **Map-Reduce AOF replay** | Higher memory during startup | Clean state reconstruction; later commands naturally override earlier ones |
| **JSON RawMessage for edge props** | Manual unmarshaling required | Avoids GC scanning millions of edge map structures, drastically reducing STW pauses |
| **Max depth caps** (BFS=5, subgraph=3, traverse=10) | Cannot traverse arbitrarily deep graphs | Prevents runaway traversals and stack overflows in production |
| **VImport bypasses AOF** | Data not durable until VImportCommit | Maximum throughput for bulk loading; commit forces snapshot for durability |
| **Parallel vector+text search** | Goroutines + WaitGroup for concurrent execution | Both are read-only operations; parallelism cuts latency in half for hybrid queries |
