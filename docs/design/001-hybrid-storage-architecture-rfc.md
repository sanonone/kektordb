# RFC 001: KektorDB Hybrid Storage Architecture

**Status:** Draft / Planned for v0.5.0 **Date:** 2025-12-25 **Context:** Scalability beyond RAM limits while preserving a simple architecture.

## 1. Executive Summary

Currently, KektorDB is a **pure in-memory** database. Both vectors and the HNSW graph reside entirely in the Go heap. This guarantees extremely low latency but limits the dataset size to the available physical RAM.

This RFC proposes introducing a **pluggable storage architecture** that allows choosing, **per index**, whether data is kept in RAM (speed priority) or on disk (scalability priority), adopting a pragmatic **hybrid approach** (graph in RAM, vectors on disk).

---

## 2. Core Design Decisions

### 2.1 Hybrid Architecture (Hybrid DiskStore)

Instead of moving everything to disk (as in DiskANN, which is complex), we adopt a pragmatic hybrid approach:

1. **Structure (RAM):** The HNSW graph (node links) and, optionally, compressed vectors (`int8`) remain in RAM. This ensures the *navigation* phase is as fast as the in-memory version.
2. **Data (Disk):** The original vectors (`float32`) are written to a binary file on disk and read only when needed (re-ranking phase or data retrieval).

### 2.2 Standard I/O vs mmap

We choose **standard I/O (**`** with **`**)** instead of memory mapping (`mmap`).

> **Rationale:**
>
> 1. **Windows stability:** `mmap` on Windows has well-known file-locking issues that prevent critical operations such as resizing or file rotation without complex handle-management logic.
> 2. **Predictability:** With standard I/O, the database has explicit control over when reads occur. With `mmap`, the kernel decides when to swap pages, potentially causing unpredictable latency spikes (page faults) that are hard to debug in Go.
> 3. **Portability:** Using `os.File` is guaranteed to work on any architecture (ARM, x86, WASM) and any OS supported by Go, without platform-specific or `unsafe` code.

### 2.3 Per-Index Granularity

The `storage_type` configuration is applied at index creation time (`VCreate`). This enables mixed scenarios:

- `hot_index`: In-memory for frequently accessed data.
- `archive_index`: On-disk for large historical datasets.

---

## 3. Technical Implementation Plan

### Phase 1: Abstraction (Refactoring)

The goal is to decouple HNSW logic from vector storage details.

**Action:** Introduce the `VectorProvider` interface.

```go
// pkg/core/storage/types.go

// VectorProvider defines how vectors are stored and retrieved.
type VectorProvider interface {
    // Set stores a vector for a given internal ID.
    Set(id uint32, vector []float32) error
    
    // Get retrieves a vector by internal ID.
    // NOTE: Implementation should make this thread-safe.
    Get(id uint32) ([]float32, error)
    
    // Size returns the number of vectors stored.
    Size() uint32
    
    // Close cleans up resources (e.g., closes file handles).
    Close() error
}
```

**Impact on ****\`\`****:** The `Node` structure will no longer contain `VectorF32`. It will only contain links. The `hnsw.Index` will hold a reference to the `VectorProvider` to compute distances.

### Phase 2: Memory Implementation

Implement the interface while preserving current behavior.

**Action:** Create `MemoryStore`.

```go
type MemoryStore struct {
    vectors [][]float32 // Simple slice of slices, as today
    mu      sync.RWMutex
}
```

This step validates that the abstraction works without performance regressions before touching disk storage.

### Phase 3: Disk Implementation

Implement binary file–based storage.

**Action:** Create `DiskStore`.

- **File format:** Flat binary.
  - Optional header: version, dimension (`uint32`).
  - Body: contiguous sequence of `float32` values.
- **Addressing:**
  - Byte offset for ID `i` = `HeaderSize + (i * Dimension * 4 bytes)`.
- **Reads:** Use `file.ReadAt` for concurrent reads without locking the file descriptor.

### Phase 4: Integration & Re-ranking

Modify the HNSW search algorithm to support re-ranking.

**Logic:**

1. **Search (RAM):** If the index uses `int8`, HNSW navigates using compressed vectors resident in RAM (inside the `Node`).
2. **Refine (Disk):** If the index is disk-based, after search completion, fetch the original vectors from `DiskStore` for the top-K candidates and recompute exact scores.

---

## 4. Configuration Changes

### `vectorizers.yaml`

```yaml
vectorizers:
  - name: "huge_docs"
    # ...
    index_config:
      metric: "cosine"
      precision: "int8" # Recommended for Hybrid: lightweight graph in RAM
      
      # NEW FIELD
      storage_type: "disk" # Options: "memory" (default), "disk"
```

### API `VCreate`

The API endpoint `POST /vector/indexes` will accept a new JSON field: `"storage_type"`.

---

## 5. Persistence Strategy (Critical)

How does this interact with AOF and snapshots?

- **MemoryStore:**
  - **Snapshot:** Serialize everything into the `.kdb` file (as today).
- **DiskStore:**
  - **Live:** Vectors are written immediately to the `.bin` file (append or write-at).
  - **Snapshot:** The `.kdb` file stores **only the graph and metadata**. Vectors are not duplicated.
  - **Restore:** On restart, KektorDB loads the graph into RAM from `.kdb` and opens a handle to the existing `.bin` file. *Startup time is drastically reduced.*

---

## 6. Migration Path

No automatic in-place migration is planned. To move from `memory` to `disk`:

1. Create a new index with `storage_type: "disk"`.
2. Use a migration pipeline (read from old index → write to new index) or re-ingest data.

---

## 7. Known Limitations

1. **I/O latency:** Disk access (even SSD) is orders of magnitude slower than RAM. Without `int8` vectors in RAM for navigation, performance will collapse. *Mitigation: hybrid mode is mandatory or strongly recommended.*
2. **Backups:** Backups now require copying two files (`.kdb` and `.bin`) instead of one.

---

