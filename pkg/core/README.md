# Module: pkg/core

## Purpose

The central data engine of KektorDB, unifying four storage paradigms into a single process: an HNSW vector index for approximate nearest neighbor search, a sharded property graph with soft deletes and time-travel queries, a thread-safe key-value store, and three secondary indexes (roaring bitmap inverted index, B-tree for range queries, and BM25 full-text search). The `DB` struct is the top-level orchestrator that binds all subsystems together.

## Key Types & Critical Paths

**Critical structs:**
- `DB` (core.go:775) -- Top-level container: holds KVStore, vector indexes map, graph shards [128], inverted index, B-tree, text index.
- `Index` (hnsw) -- HNSW graph: `nodes atomic.Value`, `shardsMu [128]sync.RWMutex`, `metaMu sync.RWMutex`, `closed atomic.Bool`.
- `Node` -- HNSW node: `InternalID uint32`, `Connections [][]uint32`, `Deleted atomic.Bool`, vector pointers into mmap arena.
- `GraphShard` -- `{mu sync.RWMutex, nodes map[string]*GraphNode}` -- one of 128 independent partitions.
- `GraphNode` -- `{ID, OutEdges map[string][]GraphEdge, InEdges map[string][]ReverseEdge}`.
- `BitSet` -- Custom bitmap using `[]uint64` buckets for visited-node tracking (bitwise ops, no allocation).
- `minHeap` / `maxHeap` -- Store `types.Candidate` by **value** (16 bytes), not pointer, for CPU cache locality.

**Critical paths (hot functions):**
- `searchLayerUnlocked()` -- The innermost HNSW search loop. Gets/pools `BitSet` and heaps on every call. Zero allocations target.
- `AddBatch()` -- Parallel insertion: spawns `runtime.NumCPU()` workers, local accumulators, sharded commit. Throughput-critical.
- `LockTwoShards()` -- Deadlock prevention: always locks lower-indexed shard first.
- `BytesToFloat32Slice()` -- Zero-copy `unsafe.Slice` casting from mmap bytes to `[]float32`.

## Architecture & Data Flow

Vector data enters via `Add()` or `AddBatch()` and flows through a dual-path insertion system. Single inserts go through the fine-grained per-shard lock path; batch inserts spawn `runtime.NumCPU()` workers that write to local `[]LinkRequest` accumulators (no channel contention), then shard by node ID and commit in parallel. Vectors are stored in the mmap-backed `VectorArena` (see `pkg/storage/mmap`), with `Node.VectorF32` pointing directly into the mmap region via `unsafe.Slice` -- zero copies, zero heap pressure.

The graph layer stores forward edges (`GraphEdge`, ~64 bytes with TargetID, timestamps, weight, and `json.RawMessage` props) and reverse edges (`ReverseEdge`, ~32 bytes with only SourceID and timestamps). This asymmetric design saves 50% RAM on incoming edges, shifting the burden to lazy hydration during read-time traversal.

Secondary indexes are maintained in parallel: the inverted index maps string metadata values to `roaring.Bitmap` of node IDs for O(1) equality/OR/AND operations; the B-tree handles numerical range queries (`<`, `<=`, `>`, `>=`, `=`); and the full-text index uses tokenized BM25 ranking with English/Italian stemming support.

Search uses devirtualized distance functions -- the metric function is captured as a closure at query start, avoiding switch-in-loop overhead in the hot path. Pre-filtering via roaring bitmap allowlists intersects metadata constraints before vector search begins, pruning the search space. When the global HNSW entrypoint is excluded by the allowlist, smart entrypoint selection finds the nearest allowed node to start the search.

## Cross-Module Dependencies

**Depends on:**
- `pkg/storage/mmap` -- VectorArena for zero-copy vector storage. The `Index` holds a reference to the arena and calls `GetBytes()` for vector access.
- `pkg/core/distance` -- Distance functions (Euclidean, Cosine) dispatched by precision type (F32/F16/I8).
- `pkg/textanalyzer` -- Stemmers for BM25 full-text index tokenization.

**Used by:**
- `pkg/engine` -- Primary consumer. Calls `VCreate`, `VAdd`, `VSearch`, graph ops, metadata ops. Wraps core with persistence and high-level APIs.
- `internal/server` -- Indirectly via engine. Exposes core operations over HTTP.
- `internal/mcp` -- Indirectly via engine. Exposes core operations as MCP tools.
- `pkg/persistence` -- Reads `DB.Snapshot()` for gob serialization; core provides the data, persistence writes it.

## Concurrency & Locking Rules

**Five-level lock hierarchy (always acquire in order, release in reverse):**

1. **`DB.mu`** (`sync.RWMutex`) -- Top-level lock protecting the map of vector indexes, graph shards, and secondary index maps. Held briefly for map lookups.

2. **`DB.indexLocks`** (`map[string]*sync.RWMutex`) -- Per-index granular locks for metadata operations. Allows concurrent metadata writes to different indexes without contending on `DB.mu`.

3. **`Index.metaMu`** (`sync.RWMutex`) -- Global HNSW index lock protecting ID maps, node slice pointer, entrypoint, maxLevel, and quantizer.

4. **`Index.shardsMu`** (`[128]sync.RWMutex`) -- 128 shard locks for node-level operations. Node `internalID & 127` determines shard. This is the finest-grained lock, enabling 128 concurrent node operations.

5. **`GraphShard.mu`** (`sync.RWMutex`) -- 128 graph shard locks. `GetShardIndex()` uses FNV-1a hash with `& (NumGraphShards-1)` for O(1) shard lookup.

**Strict cross-shard lock ordering:** `LockTwoShards(id1, id2)` always locks the lower-indexed shard first, mathematically preventing deadlocks during cross-shard edge operations.

**Copy-on-Write for atomic node slices:** The `nodes` slice is stored as `atomic.Value`. On growth, a new backing array is allocated, old contents copied, and the atomic pointer swapped. Readers always see a consistent snapshot without any lock.

**Copy-Before-Release pattern:** During neighbor linking, a node's connections are copied under `RLock`, then the lock is released *before* CPU-intensive distance calculations. Only the final pointer swap requires a brief write lock.

**sync.Pool for zero-allocation hot paths:** `BitSet`, `minHeap`, `maxHeap`, and neighbor buffers are pooled. The search loop (`searchLayerUnlocked`) gets/puts these on every call, eliminating heap allocations in the critical path.

**Atomic lifecycle flags:** `Index.closed` (`atomic.Bool`) is checked at the start of every public method. Set *before* acquiring locks in `Close()` to prevent new operations from starting during shutdown.

**Value-semantics heaps:** `minHeap` and `maxHeap` store `types.Candidate` by value (not pointer). The struct is only 16 bytes (`uint32` + `float64`), so copying is cheaper than GC pressure from pointer indirection.

## Known Pitfalls / Gotchas

- **NEVER hold `metaMu` while acquiring `adminMu`** -- This was the root cause of the v1 AOF Rewrite deadlock. The fix (v2) uses "Snapshot + Release": copy data under `RLock`, release it, then iterate indexes independently.
- **`AddBatch` mutates `Index.needsRefine`** -- After bulk import, the index is marked as needing refinement. Search auto-boosts `efSearch` when this flag is set. Don't forget to call `TurboRefine` after `VImportCommit`.
- **`Node.VectorF32` points into mmap** -- Never modify vector bytes directly. Use `arena.SetBytes()` which handles the unsafe cast safely. Direct mutation bypasses the arena's slot table and can corrupt data during compaction.
- **`BitSet` must be returned to pool** -- Every `poolBitSet.Get()` must have a matching `poolBitSet.Put()` via `defer`. Leaking BitSets causes unbounded memory growth under sustained search load.
- **`closed` flag is set BEFORE acquiring locks** -- In `Close()`, the atomic flag is set first to reject new operations, then locks are acquired for cleanup. If you add a new public method, check `closed.Load()` as the very first action.
- **gob serialization excludes vectors** -- `Node.GobEncode()` explicitly skips vector slices. On restore, vectors are re-linked via the arena. If you add new fields to `Node`, remember to update both `GobEncode` and `GobDecode`.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Soft deletes over hard deletes** | `atomic.Bool Deleted` flag + `DeletedAt` timestamps | Maintains graph structural stability; enables time-travel queries; vacuum runs asynchronously |
| **128 shards (power-of-two)** | Fixed count, not dynamic | `& (N-1)` is faster than `% N`; 128 provides high concurrency with minimal memory overhead |
| **gob + JSON for metadata** | Custom `GobEncode` serializes `map[string]any` as JSON bytes | Avoids gob's inability to handle `[]interface{}` and nested maps natively |
| **Two HNSW insert paths** | `Add()` (single, latency) vs `AddBatch()` (parallel, throughput) | Single insert prioritizes low latency; batch prioritizes throughput with sharded commit |
| **Smart dispatch for distance** | Rust CGO for vectors >= 128 dims, pure Go/Gonum for shorter | CGO call overhead is amortized only for longer vectors where SIMD benefits dominate |
| **No vector data in snapshots** | Vectors live in mmap, excluded from gob serialization | Snapshots stay small; vectors are re-mapped on restore from persistent .bin files |
| **BM25 with linear scan for TF** | Posting list is `[]PostingEntry`, not a map | Simpler, lower memory overhead for typical document lengths; O(N) scan is acceptable |
| **Reverse edges store only SourceID** | `ReverseEdge` is ~32 bytes vs `GraphEdge` at ~64 bytes | Saves 50% RAM on incoming edges; full edge data is lazily hydrated from forward edges during traversal |
