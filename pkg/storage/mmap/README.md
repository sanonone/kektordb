# Module: pkg/storage/mmap

## Purpose

Implements a memory-mapped vector storage arena that stores fixed-size embedding vectors in 64MB mmap'd chunks, providing zero-copy access to vector data while managing fragmentation through an asynchronous background compactor. This is the foundation of KektorDB's zero-allocation vector storage -- vectors never touch the Go heap.

## Key Types & Critical Paths

**Critical structs:**
- `VectorArena` -- Core arena: `chunks []*Chunk`, `slotTable []uint32`, `freeSlots []uint32`, `mu sync.RWMutex`, `slotMu sync.RWMutex`, `closed atomic.Bool`.
- `Chunk` -- `{fd int, data []byte, id int}` -- A single 64MB mmap'd file.
- `AsyncCompactor` -- Background defragmentation: `va *VectorArena`, `stopCh`, `draining atomic.Bool`, `MaintenanceCoordinator` interface.
- `MaintenanceCoordinator` -- Interface for coordinating compaction with HNSW vacuum and write-heaviness detection.

**Critical paths (hot functions):**
- `GetBytes(internalID)` -- Zero-copy read from mmap. Fast path: RLock only. Slow path: lazy chunk creation with write lock.
- `Alloc()` -- Bump allocator with LIFO free slot reuse. Pops from `freeSlots` stack or increments `nextPhysSlot`.
- `Free(internalID)` -- Pushes physical slot onto `freeSlots` stack. O(1).
- `compactChunk()` -- Moves vectors from high to low physical slots in batches of 100. Acquires both locks (slotMu then mu).

## Architecture & Data Flow

Each chunk file (`arena_NNNN.bin`) is exactly 64MB with a 64-byte header containing magic number (`0x4B414F4E` / "KARN"), version, dimension, and precision code. The remaining space holds densely packed vectors. Chunks are created lazily on first access, not upfront.

The arena uses a two-level addressing scheme: **Logical IDs** (sequential `internalID` values from the HNSW index) map to **Physical Slots** via a `slotTable []uint32`. Physical location is computed as `chunkID = physSlot / vecsPerChk`, `offset = ArenaHeaderSize + (physSlot % vecsPerChk) * vectorSize`. Freed slots go into a `freeSlots` stack (LIFO reuse), giving a bump allocator with reuse.

Zero-copy type casting uses `unsafe.Slice` to reinterpret mmap byte slices as typed slices (`[]float32`, `[]uint16`, `[]int8`) without any copying. The `Node.VectorF32` field points directly into the mmap region. On snapshot, vector data is explicitly excluded from gob serialization; on restore, vectors are re-linked via `arena.GetBytes()` + `BytesToFloat32Slice()`.

The `AsyncCompactor` runs on a configurable interval (default: 5 minutes). It analyzes fragmentation ratio (`freePhysicalSlots / totalPhysicalSlots`), skips if below threshold (default: 0.3), and moves vectors from high physical slots into lower free slots in batches of 100. Between batches it sleeps for 1ms to yield to other goroutines. Empty trailing chunks are unmapped, closed, and deleted from disk after each compaction cycle.

## Cross-Module Dependencies

**Depends on:**
- `golang.org/x/sys/unix` -- `unix.Mmap()` / `unix.Munmap()` for Unix/Darwin/Linux.
- OS-specific syscalls for Windows (`CreateFileMapping`, `MapViewOfFile`, `UnmapViewOfFile`).

**Used by:**
- `pkg/core` (HNSW index) -- Primary and only consumer. The `Index` creates the arena, calls `Alloc()`/`Free()`/`GetBytes()`, and passes it to the compactor.
- `pkg/engine` -- Indirectly via core. Engine's snapshot/restore re-links vectors through the arena.

## Concurrency & Locking Rules

**Two-lock design with strict ordering:**
- **`mu`** (`sync.RWMutex`) -- Protects the `chunks` slice (creation, deletion, access).
- **`slotMu`** (`sync.RWMutex`) -- Protects `slotTable`, `freeSlots`, and `nextPhysSlot`.

**STRICT LOCK ORDERING: `slotMu` MUST be acquired before `mu`.** Violating this ordering causes deadlock. This is enforced in `compactChunk()` and `tryDropEmptyChunks()`.

**TOCTOU protection in compaction:** Before writing to a new physical slot, the compactor verifies `slotTable[internalID] == fromSlot` to ensure the slot hasn't been reassigned since the read phase. This prevents data corruption from concurrent slot reassignment.

**Double-checked locking in `GetBytes()`:** After releasing the read lock and acquiring a write lock to create a chunk, the code re-checks `chunkID >= len(va.chunks)` because another goroutine may have created the chunk between releasing the read lock and acquiring the write lock.

**`closed` atomic flag:** An `atomic.Bool` prevents operations after `Close()`. Every public method checks `va.closed.Load()` as its first action.

**Graceful shutdown with draining:** `Stop()` sets an `atomic.Bool` draining flag, closes `stopCh`, and waits with a 5-second timeout. Every loop iteration and chunk boundary in the compactor checks this flag for cooperative shutdown.

**Copy-on-read for state:** `GetState()` creates deep copies of `slotTable` and `freeSlots` to prevent external mutation of internal state.

## Known Pitfalls / Gotchas

- **NEVER acquire `mu` before `slotMu`** -- This is the single most important rule. The compactor acquires both locks in `compactChunk()` and `tryDropEmptyChunks()`. If you add a new method that needs both, follow the same order.
- **`GetBytes()` returns a live mmap slice** -- The returned `[]byte` points directly into kernel-managed memory. Do NOT hold references across compaction cycles -- the compactor may move the vector to a different physical slot, invalidating your pointer. Always call `GetBytes()` fresh.
- **LIFO free slot reuse causes hot-spotting** -- Recently freed slots are reused first, concentrating writes on specific pages. The compactor mitigates this over time, but during high churn you may see uneven page cache pressure.
- **No explicit `msync`** -- Data durability depends on OS page cache flushing. A hard power loss may lose the last ~seconds of writes even if they returned successfully. For crash-critical workloads, consider adding explicit `msync`.
- **Compactor's `MaintenanceCoordinator` is an interface** -- The compactor calls `TryLock()` and checks write-heaviness before starting a cycle. If the coordinator is not properly implemented, the compactor may run during HNSW vacuum, causing conflicts.
- **Chunk header validation is minimal** -- Magic number, version, dimension, and precision are checked on load. No CRC or checksum on the data portion. Corrupted data in the middle of a chunk will not be detected until a read returns garbage.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **LIFO free slot reuse** | Stack-based free list (last freed, first reused) | Simplest implementation; can cause hot-spotting on specific pages but amortizes well with compaction |
| **64MB fixed chunk size** | Hardcoded, not configurable | Balances file management overhead with defragmentation granularity; ~131K vectors per chunk for 128-dim float32 |
| **No explicit msync** | Relies on `MAP_SHARED` for automatic write-back | Simpler; OS handles dirty page flushing. Trade-off: no guaranteed durability ordering for crash recovery |
| **O(n) fragmentation analysis** | `analyzeFragmentation()` scans entire `slotTable` | Acceptable for typical arena sizes; a per-chunk bitmap would be more efficient but adds complexity |
| **No checksums in chunk data** | Header validates magic/version/dim/precision only | Data integrity is assumed from the OS page cache; CRC would add I/O overhead on every read |
| **1ms batch delay in compaction** | Sleeps between batches to yield CPU | Prevents the compactor from starving concurrent reads/writes; tunable via `BatchDelay` config |
| **Platform-specific mmap** | Unix: `unix.Mmap()` (21 lines); Windows: `CreateFileMapping`+`MapViewOfFile` (50 lines) | Unix is simpler because `golang.org/x/sys/unix.Mmap` returns `[]byte` directly; Windows requires manual handle management |
