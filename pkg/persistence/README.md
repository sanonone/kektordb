# Module: pkg/persistence

## Purpose

The on-disk durability layer for KektorDB, implementing a framed binary Append-Only File (AOF) protocol with CRC32 integrity checks, lazy batched writes for throughput, RESP command serialization, and crash recovery with automatic corruption detection and truncation.

## Key Types & Critical Paths

**Critical structs:**
- `AOFWriter` -- Synchronous writer: `file *os.File`, `buf *bufio.Writer`, `fw *FrameWriter`, `mu sync.Mutex`.
- `LazyAOFWriter` -- Async batching wrapper: `aw *AOFWriter`, `buffer []string`, `mu sync.Mutex`, `flushInterval`, `forceSyncInterval`, `stopCh`, `stopped bool`.
- `FrameWriter` -- TLVC framing: writes `[Magic(0xA5)][OpCode(1)][Length(4, LE)][CRC32(4, LE)][Payload]`.
- `Command` -- RESP parsed command: `Name string`, `Args [][]byte`.

**Critical paths (hot functions):**
- `LazyAOFWriter.Write()` -- Appends to in-memory buffer, returns immediately. O(1) amortized. Triggers background flush at 1000 entries.
- `ReadFrame()` -- Reads 10-byte header, validates magic + CRC32, reads payload. Called during AOF replay at startup.
- `FormatCommand()` -- RESP serialization using `strings.Builder`. Called on every write operation.
- `ReplaceWith()` -- Atomic file swap via `os.Rename()`. Called at end of AOF rewrite.

## Architecture & Data Flow

**TLVC Framed Protocol:** Every AOF frame has a 10-byte header: `[Magic(0xA5)][OpCode(1)][Length(4, LE)][CRC32(4, LE)]` followed by the RESP payload. The magic byte enables recovery scanning if the file stream loses synchronization. CRC32-IEEE checksums detect bit rot and partial writes.

**Two-layer AOF writer:**
- **`LazyAOFWriter`** (outer layer): Incoming `Write()` calls append to an in-memory `[]string` buffer and return immediately (non-blocking). Two background goroutines run concurrently: `flushRoutine` (ticks every 100ms, flushes to OS page cache) and `syncRoutine` (ticks every 1s, calls `fsync`). If the buffer reaches 1000 entries, a background flush is triggered immediately.
- **`AOFWriter`** (inner layer): Synchronous, mutex-protected writer with `bufio.Writer` and `FrameWriter`. Supports atomic file replacement via `os.Rename()`.

**RESP Serialization:** Commands are formatted as RESP arrays of bulk strings (`*N\r\n$len\r\n<value>\r\n...`). Binary-safe -- allows null bytes, images, and arbitrary data in arguments. Parsing uses `bufio.Reader` with `ReadString('\n')` for line-oriented protocol handling.

**Recovery flow:** Load latest `.kdb` snapshot, open AOF, iterate frame-by-frame via `ReadFrame()`. For each valid frame, parse RESP via `ParseCommand()`. Aggregate commands in memory maps (compaction during replay). If corruption is detected (invalid magic, CRC mismatch, incomplete frame), truncate the file at the last valid offset and continue startup.

## Cross-Module Dependencies

**Depends on:**
- `hash/crc32` -- CRC32-IEEE checksum computation for frame integrity.
- `bufio`, `io` -- Standard library buffered I/O.

**Used by:**
- `pkg/engine` -- Primary consumer. Creates `LazyAOFWriter` on startup, calls `Write()` for every mutation (VAdd, VDelete, VLink, VMETA, etc.). Calls `RewriteAOF()` for compaction and `SaveSnapshot()` for durability checkpoints.
- `pkg/engine/recovery.go` -- Calls `ReadFrame()` and `ParseCommand()` during AOF replay at startup.

## Concurrency & Locking Rules

**Layer 1 -- `AOFWriter.mu` (`sync.Mutex`):** Protects all operations on the underlying file, buffer, and `FrameWriter`. Every public method (`Write`, `Flush`, `Sync`, `Close`, `Truncate`, `ReplaceWith`) acquires this lock.

**Layer 2 -- `LazyAOFWriter.mu` (`sync.Mutex`):** Protects the in-memory `buffer []string` and `stopped` flag. `Write()` acquires the lock briefly to append. `Flush()` and `Sync()` acquire the lock, drain the buffer, and delegate to the underlying writer.

**Background goroutine serialization:** When the buffer fills during `Write()`, a flush is spawned as `go lw.Flush()`. Multiple flush goroutines could theoretically race, but they are serialized by `mu` in `Flush()`. Trade-off: potential goroutine churn under sustained high throughput.

**Engine-level coordination:** `adminMu` (`sync.Mutex`) in the Engine protects snapshot and rewrite operations from running concurrently. `isRewriting` (`atomic.Bool`) provides a fast-path check to skip concurrent rewrite attempts.

**No error propagation from background routines:** If `flushRoutine` or `syncRoutine` encounters an error, it logs via `slog.Error` but does not propagate to the caller. `Write()` always returns `nil` (unless the writer is stopped).

## Known Pitfalls / Gotchas

- **Background flush goroutine churn** -- Under sustained high throughput, the buffer fills repeatedly, spawning `go lw.Flush()` each time. These goroutines serialize on `mu` but still create scheduler overhead. If you see goroutine count spikes, consider increasing `maxBufferSize`.
- **`Write()` always returns nil** -- Unless the writer is stopped, `LazyAOFWriter.Write()` never returns an error. Disk failures are logged but not propagated. The caller has no way to know if data was actually written.
- **`ReplaceWith()` closes the old file** -- After atomic rename, the old `AOFWriter`'s file is closed and reopened. Any goroutine holding a reference to the old file descriptor will get EBADF. This is only called during rewrite, which is protected by `adminMu`.
- **AOF replay aggregates in memory** -- During recovery, all commands are buffered in temporary maps (`kvData`, `indexes`) before being applied. For very large AOF files, this can consume significant memory during startup.
- **No `msync` after flush** -- `Flush()` flushes the `bufio.Writer` to the OS page cache but does not call `fsync`. Only `Sync()` does. The 1-second `forceSyncInterval` means up to 1 second of data is vulnerable to power loss.
- **RESP parser is not zero-copy** -- `ReadString('\n')` allocates a new string for each line. For high-throughput workloads with many small commands, this creates GC pressure. The overhead is negligible compared to disk I/O, but worth noting.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Lazy batching with ~1s fsync** | Up to 1s of data loss on crash | 10-100x throughput improvement; acceptable for most workloads |
| **Custom TLVC framing over raw RESP** | 10-byte header with magic + CRC32 | Detects partial writes, bit rot, and file corruption during recovery |
| **Atomic file replacement** | `os.Rename()` on POSIX | Crash-safe; no partial file states visible to readers |
| **Snapshot-then-Release in RewriteAOF** | Copy data, release locks, then write | Eliminates the `s.mu -> h.metaMu` deadlock; acceptable since rewrite is background compaction |
| **No test files in persistence package** | Tests exist at engine level (`rewrite_deadlock_test.go`) | Integration-level testing captures cross-layer interactions better than unit tests |
| **RESP parser uses `bufio.Reader`** | Not zero-copy | Simple and correct for line-oriented protocol; the overhead is negligible compared to disk I/O |
