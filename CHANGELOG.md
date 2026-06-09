# Changelog

All notable changes to KektorDB are documented here.

## [Unreleased] — feat/tui branch (beyond v0.5.2)

### Breaking Changes

- **`POST /ui/search` endpoint removed.** Text-to-vector search is now unified into `POST /vector/actions/search` via the `query_text` field. The server auto-embeds `query_text` through its `VectorizerService` when `query_vector` is empty.
  - Endpoint: `POST /ui/search` → `POST /vector/actions/search`
  - Request field: `"query"` → `"query_text"`
- **`query_vector` is now optional** in `POST /vector/actions/search`. When omitted, `query_text` auto-embeds. When both are provided, `query_vector` takes precedence.

### Added

- **Built-in ONNX embedder** (`all-MiniLM-L6-v2`, 384dim) via Rust/candle CGO wrapper. Downloads model from HuggingFace on first launch with SHA256 verification. `--embedder` flag with auto-detect (Ollama → local → error guidance).
- **MCP one-liner setup** (`kektordb setup <agent>`) — configures Claude Code, Cursor, Gemini CLI, Codex, and OpenCode with a single command.
- **Tool profiles** (`--tools=agent|admin|all`): 17 tools (agent), 6 (admin), 23 (all). Default `agent` reduces token consumption in MCP.
- **TUI terminal dashboard** (Bubble Tea v2): 5 tabs — Dashboard, Graph, Search, Timeline, Settings. Run with `--tui`.
- **Configurable K limit** (5–100) in TUI and search parameters.
- **`hydrate` field** on `POST /vector/actions/search`: returns full node metadata without traversing graph relations.
- **`Task.Snapshot()`**: thread-safe copy for JSON serialization, fixes data races in async task responses.
- **Gardener getters** (`IsEnabled`, `Mode`, `Interval`, `TargetIndexes`): expose real runtime state for `/system/stats` and `/system/gardener`.

### Fixed

- Metadata mutation bug: `compressMetadata` / `compressGraphSearchResults` clone metadata before mutating, no longer corrupt live index data.
- Task data race: all handlers use `task.Snapshot()` instead of the live pointer for JSON serialization.
- TUI focus/input management: `syncSearchFocus()` correctly handles textinput focus across tabs. `"/"` toggle no longer fires while typing. `"r"` refresh no longer conflicts with Graph tab.
- TUI graph: multi-node results shown as selectable list; target nodes navigable with `↑↓`/`Enter`/`→`.
- `handleSystemStats` / `handleSystemGardener`: read real Gardener state instead of hardcoded `"basic"`.
- `handleGraphSearchNodes`: uses `VFilter` and `IterateRaw` (no dummy zero-vector). Results sorted alphabetically. IDs collected before `VGetMany` to avoid recursive `metaMu` RLock.
- `handleTransferMemory` / `handleEmbedderReload`: return `501 Not Implemented` instead of fake `200 OK`.
- Nil-guard for `vectorizerService` in RAG handlers.
- All SDKs (Go, Python, TypeScript) and Web UI JS: updated to `query_text` and unified endpoint.
- Makefile: `ensure-protoc` target auto-downloads protoc v29.3 when missing.
- `cognitive_layers_example.yaml` interval restored to `1h` (was `2m` dev leftover).

---

## [0.5.2] — 2026-05-10

### Fixed

- Data race between `Refine` and concurrent `Add` in HNSW (`node.Connections` snapshot under per-node `RLock`).
- Deadlock between `GetVectors` workers and `Close` (extracted `getMetadataForNodeLocked` to skip redundant `s.mu.RLock`).
- Body size limit raised to 512 MB.
- Meta-node contradiction loop prevention (skip `reflection`, `consolidated_memory`, `consolidated_belief`, `evolved_memory`).
- `EvolveMemory` content handling aligned with HTTP handler (only set when `NewContent` is non-empty).
- Cascading delete of outgoing edges in `VDelete` (prevents ghost nodes).
- Graph edge cleanup on `VDEL` during AOF replay.
- Missing locks in `LoadFromSnapshot` (`s.mu`, `kvStore.mu`, per-shard `mu`).
- HTTP request limits and timeouts for DoS protection.
- Serialized `think()` calls via channel (`thinkWorker`) to prevent reentrancy race in Gardener.
- Concurrent map read/write panic in metadata lookups (`getMetadataForNode` instead of `_Unlocked`).
- `!=` operator early return skipping inverted index fallback.
- Consolidated truth used as evolved node content in `executeConsolidation`.
- Stale entries in secondary indexes on metadata overwrite (`removeOldIndexEntries`).
- LazyAOFWriter snapshot mode shadow buffer (prevents write loss during snapshot + truncate).
- Lock-free HNSW node snapshots and fine-grained sharded locking for `VReinforce`/`VSetMetadata`.

---

## [0.5.1] — 2026-04-14

### Changed

- `scanCursors` and `lastThinkTime` now use `sync.RWMutex` for thread-safe Gardener access.
- Extracted `getHNSWIndex()` helper, replaced bubble sort with `sort.Slice`.
- Removed redundant `metaMu.RLock` from Refine workers, AOF background flusher for memory-efficient writes.

### Fixed

- Write loss during snapshot via shadow buffer in LazyAOFWriter.
- Background goroutine tracking for graceful shutdown (`context` + `WaitGroup`).
- Graph filter root inclusion in allowedSet and metadata cleanup on delete.
- OOM from corrupted AOF frame payload length.
- Use-after-free segfault on mmap vector pointers (deferred chunk unmap + `atomic.Pointer`).
- Gardener/RAG/MCP concurrency and lifecycle issues.
- HNSW `randomLevel` distribution corrected to match paper.
- Metadata race with per-node fine-grained locking.

---

## [0.5.0-beta1] — 2026-03-06

### Added

- `VGetAllRelations` / `VGetAllIncoming` API and endpoints for complete graph discovery.
- Go and Python client methods for the above.

### Fixed

- `RewriteAOF` deadlock and HNSW data races.
- Lock-free RCU implementation for reduced GC pressure.
- Data races eliminated across HNSW engine.
- Cascading graph deletes across connected nodes.
- Safe teardown and physical cleanup for mmap arenas.
- Quantizer data race and snapshot bloat.

### Changed

- `map[string]any` → `json.RawMessage` in GraphEdge to eliminate GC overhead.
- `VReinforce` optimized with `VMETA` command (lock contention and write amplification reduced).
- Slot Table indirection for physical storage recycling.
- TOCTOU race condition fix in arena allocation slow path.
- Zero-copy mmap arena for vector data.

---

## [0.4.7] — 2026-02-20

Documentation update and serialization / concurrency fixes.

---

## [0.4.6-beta1] — 2026-02-15

### Added

- `VReinforce` op and reinforcement logic for metadata updates.
- Edge versioning and evolution logic.
- Rich edges with properties, weights, and soft-deletes.
- Time-weighted ranking engine and time travel / maintenance logic.
- Semantic subgraph extraction and initial pathfinding.
- Graph intelligent navigation and pathfinding (`FindPath` with explicit relation types).
- MCP tools upgrade and `reinforce` API.
- Memory engine enabled by default for MCP indexes.

### Fixed

- Race conditions in self-repair.
- Config persistence error handling.
- Vector dimension validation.
- Graph traversal recursion limits.
- Graph properties sanitization.
- Writer starvation and fine-grained sharded locking for `VAdd`.
- AOF lazy flush and pre-compiled regex cleanup tasks.
- Snapshot serialization problem.
- `rewriteAOF` deadlock during `VAddBatch`.

---

## [0.4.5] — 2026-02-11

### Added

- Native Model Context Protocol (MCP) server implementation.
