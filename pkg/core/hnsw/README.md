# Module: pkg/core/hnsw

## Purpose

HNSW (Hierarchical Navigable Small World) implementation for Approximate Nearest Neighbor (ANN) search. Optimized for high-throughput concurrent operations with 128-way sharded locking, memory-mapped vector storage, and SIMD-accelerated distance functions.

## Key Types & Critical Paths

**Critical structs:**
- `Index` -- HNSW graph: `nodes atomic.Value`, `shardsMu [128]sync.RWMutex`, `metaMu sync.RWMutex`, `arena *mmap.VectorArena`, `closed atomic.Bool`.
- `Node` -- HNSW node: `InternalID uint32`, `Connections [][]uint32`, `Deleted atomic.Bool`, vector pointers into mmap arena.
- `GraphOptimizer` -- Background maintenance: `index *Index`, `config AutoMaintenanceConfig`, `lastScanIdx int`.
- `minHeap` / `maxHeap` -- Store `types.Candidate` by **value** (16 bytes), not pointer, for CPU cache locality.

**Critical paths (hot functions):**
- `searchLayerUnlocked()` -- Innermost search loop. Zero-allocation target using pooled `BitSet` and heaps.
- `AddBatch()` -- Parallel insertion with `runtime.NumCPU()` workers, local accumulators, sharded commit.
- `Refine()` -- Graph optimization: parallel computation with sharded commit for lock efficiency.
- `LockTwoShards()` -- Deadlock prevention: always locks lower-indexed shard first.

## Architecture & Data Flow

**Dual-path insertion:** Single inserts use fine-grained per-shard locks. Batch inserts spawn workers that write to local `[]LinkRequest` accumulators, then shard by node ID and commit in parallel.

**Sharded locking:** 128 shards enable high concurrency. Node `internalID & 127` determines shard. `LockTwoShards(id1, id2)` orders locks by shard index to prevent deadlocks.

**Copy-on-Write slices:** The `nodes` slice is stored as `atomic.Value`. Growth allocates new backing array, copies contents, and swaps atomically. Readers see consistent snapshots without locks.

**Search devirtualization:** Distance function is captured as a closure at query start, avoiding switch-in-loop overhead in the hot path.

## Background Maintenance

**Refine (Graph Optimization):**
Re-evaluates neighbor connections for a batch of nodes to improve graph quality and search recall.

- Parallel computation phase reads graph state (uses per-node shard locks via `searchLayerUnlocked`)
- Sharded commit phase locks only affected shards
- No global locks held during computation

**Vacuum:**
Removes soft-deleted nodes and compacts the arena. Runs periodically or on demand.

## Cross-Module Dependencies

**Depends on:**
- `pkg/storage/mmap` -- `VectorArena` for zero-copy vector storage.
- `pkg/core/distance` -- Distance functions dispatched by precision type.
- `pkg/textanalyzer` -- Stemmers for BM25 full-text index.

**Used by:**
- `pkg/core` -- `DB` manages HNSW indexes.
- `pkg/engine` -- Primary consumer for vector operations.

## Concurrency & Locking Rules

**Five-level lock hierarchy:**
1. **`DB.mu`** -- Top-level map of indexes
2. **`DB.indexLocks`** -- Per-index metadata operations
3. **`Index.metaMu`** -- Global index state (entrypoint, maxLevel, quantizer)
4. **`Index.shardsMu`** -- 128 shard locks for node operations
5. **`GraphShard.mu`** -- Graph edges (separate from vector shards)

**Cross-shard ordering:** `LockTwoShards()` always acquires lower-indexed shard first, preventing deadlocks.

**Copy-Before-Release:** During neighbor linking, copy connections under `RLock`, release, compute distances (CPU-intensive), then acquire brief `Lock` for final swap.

**sync.Pool for hot paths:** `BitSet`, `minHeap`, `maxHeap`, and neighbor buffers eliminate heap allocations in search loops.

## Common Patterns for Contributors

**Per-node shard locking:** Use `RLockNode(id)` / `RUnlockNode(id)` for node-level operations:

```go
h.RLockNode(nodeID)
node := h.getNodes()[nodeID]
// read node data
h.RUnlockNode(nodeID)
```

**Avoid:** Don't hold `metaMu.RLock` during long computations. Copy needed data, release, then process.

## Known Pitfalls / Gotchas

- **Never modify vector bytes directly** -- `Node.VectorF32` points into mmap. Use `arena.SetBytes()` for safe updates.
- **BitSet must be returned to pool** -- Every `Get()` needs matching `Put()` via `defer`.
- **`closed` flag is checked first** -- Set atomically before cleanup in `Close()` to reject new operations.
- **Value semantics for heaps** -- Store `Candidate` by value (16 bytes), not pointer, for cache locality.
- **gob serialization excludes vectors** -- `Node.GobEncode()` skips vector slices. Vectors are re-linked via arena on restore.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **128 shards (power-of-two)** | Fixed count | `& (N-1)` is faster than `% N`; 128 balances concurrency vs memory |
| **Soft deletes** | `atomic.Bool Deleted` flag | Maintains graph structure; vacuum runs asynchronously |
| **Two insert paths** | `Add()` vs `AddBatch()` | Single: low latency; Batch: high throughput |
| **gob + JSON for metadata** | Custom `GobEncode` | Avoids gob's limitations with nested maps |
| **No vector data in snapshots** | Excluded from gob | Snapshots stay small; vectors re-mapped from .bin files |
| **BM25 linear scan** | `[]PostingEntry` not map | Lower memory; O(N) scan acceptable for typical docs |
| **Reverse edges minimal** | `ReverseEdge` ~32 bytes vs `GraphEdge` ~64 bytes | Saves 50% RAM on incoming edges |
