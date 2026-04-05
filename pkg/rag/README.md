# Module: pkg/rag

## Purpose

The Retrieval-Augmented Generation pipeline that provides end-to-end document ingestion (Load -> Split -> Embed -> Store), file-system watching for incremental updates, semantic retrieval, and graph-aware adaptive retrieval that expands context by traversing relationships between chunks. Tightly coupled to KektorDB's `Engine` via an adapter pattern, but uses interfaces throughout for testability.

## Key Types & Critical Paths

**Critical structs:**
- `Pipeline` -- Orchestrator: `cfg *Config`, `loader Loader`, `splitter Splitter`, `embedder Embedder`, `store Store`, `stopCh`, `isScanning int32` (atomic), `extractionChan chan ExtractionJob` (buffered: 100).
- `AdaptiveRetriever` -- Graph-aware context expansion: `cfg *AdaptiveContextConfig`, `store AdaptiveStore`. Immutable after construction.
- `KektorAdapter` -- Bridges `Store`/`AdaptiveStore` to `engine.Engine`. Implements vector ops, KV state, graph linking.
- `SmartLoader` -- Two-tier loader: optional `CLILoader` + mandatory `AutoLoader` (fallback).
- `RecursiveCharacterSplitter` -- Hierarchical splitting with strategy-based separator lists.

**Critical paths (hot functions):**
- `scanAndProcess()` -- File watcher loop: walks directory, checks mod times, processes new/changed files. Atomic CAS prevents concurrent scans.
- `RetrieveAdaptive()` -- Seed retrieval -> graph expansion (BFS) -> scoring -> context assembly. Falls back to standard retrieval if store doesn't implement `AdaptiveStore`.
- `expandGraphBFS()` -- Queue-based BFS from seed nodes. Tracks visited with minimum depth. Respects `MaxExpansionNodes` (default: 200).
- `extractionWorker()` -- Single goroutine consuming from `extractionChan`. Calls LLM for entity extraction, creates graph nodes and edges.

## Architecture & Data Flow

**Ingestion pipeline:** Documents enter through a two-tier loader system. `SmartLoader` tries an external CLI parser first (if configured), then silently falls back to `AutoLoader` on failure. `AutoLoader` routes by file extension: `PDFAdvancedLoader` (text via ledongthuc/pdf + image extraction via pdfcpu with SHA-256 content-addressed deduplication), `DocxLoader` (streaming XML parser with heading preservation), or `TextLoader` (plain text).

**Splitting:** `RecursiveCharacterSplitter` uses hierarchical recursive splitting -- tries coarse separators first (`\n\n`), then recurses to finer ones (`\n`, ` `, ``) for oversized chunks. Strategy-based factory produces separator lists for text (paragraph->sentence->word), code (function/type boundaries), and markdown (header-aware). Overlap management uses UTF-8 rune counting for Unicode safety.

**Adaptive retrieval:** The `AdaptiveRetriever` implements graph-aware context expansion beyond simple top-k search. Three strategies: `greedy` (one-hop from seeds), `density` (filters by information density), and `graph` (BFS expansion with configurable depth, shortest-path deduplication, and edge-weighted scoring). BFS respects `GraphExpansionDepth` (default: 2) and `MaxExpansionNodes` (default: 200). Scoring formula: `FinalScore = SemanticWeight * DerivedScore + GraphWeight * DepthPenalty + DensityWeight * NormalizedDensity`.

**Context assembly:** Groups chunks by `parent_id`, sorts by `chunk_index` within each document, sorts documents by highest seed score, then fills the context window up to `MaxTokens` (default: 4096) using `CharsPerToken` (default: 4.0) for estimation.

**Background file watcher:** Runs in a dedicated goroutine started by `Pipeline.Start()`, using `time.Ticker` for periodic scanning at `PollingInterval` (default: 1 minute). Entity extraction uses a single-worker goroutine consuming from a buffered channel (size 100); if the channel is full, extraction jobs are skipped with a warning.

## Cross-Module Dependencies

**Depends on:**
- `pkg/embeddings` -- `Embedder` interface for text-to-vector conversion.
- `pkg/llm` -- LLM client for entity extraction and vision analysis (optional).
- `pkg/engine` -- Via `KektorAdapter`. Vector ops, KV state, graph linking.
- External: `ledongthuc/pdf` (PDF text), `pdfcpu` (PDF images), `github.com/unidoc/unioffice` (DOCX).

**Used by:**
- `internal/server` -- `VectorizerService` creates and manages `Pipeline` instances. HTTP handlers call `RetrieveAdaptive`.
- `internal/mcp` -- `AdaptiveRetrieve` tool uses `AdaptiveRetriever` directly.
- `pkg/proxy` -- Indirectly via server's vectorizer service for RAG context injection.

## Concurrency & Locking Rules

**Scan deduplication via atomic CAS:** `atomic.CompareAndSwapInt32` on `isScanning` (0 -> 1) prevents concurrent scans. If a scan is already running, new attempts are silently skipped. Atomic store resets the flag on completion (`defer atomic.StoreInt32(&p.isScanning, 0)`).

**Single extraction worker:** Only one goroutine processes entity extraction jobs from the buffered channel. No parallelism for LLM calls. Non-blocking enqueue: if the channel is full, the job is skipped. This prevents blocking the main ingestion pipeline on potentially slow LLM calls.

**Stateless retrieval:** Each `Retrieve` call is independent. The `AdaptiveRetriever` itself is immutable after construction -- all fields are set in the constructor and never mutated. Safe for concurrent use.

**Trigger spawns untracked goroutine:** `Pipeline.Trigger()` launches `scanAndProcess()` in a new goroutine with no way to await completion. This is by design -- fire-and-forget for background ingestion.

**Fallback behavior:** If the store does not implement `AdaptiveStore`, `RetrieveAdaptive` falls back to standard `Retrieve()` and wraps results in a `ContextWindow`. Adaptive retrieval is an opt-in enhancement, not a hard requirement.

## Known Pitfalls / Gotchas

- **`Trigger()` goroutines are untracked** -- There is no `sync.WaitGroup` or channel to wait for scan completion. If you call `Trigger()` multiple times rapidly, only the first one runs (atomic CAS dedup). Subsequent calls are silently dropped.
- **Extraction channel can silently drop jobs** -- If the buffered channel (size 100) is full, entity extraction jobs are skipped with a warning. During bulk ingestion of image-heavy PDFs, this can result in missing entity nodes.
- **`KektorAdapter` is tightly coupled to `engine.Engine`** -- The adapter directly references engine types, making the RAG package not truly portable. If you try to use this package with a different vector store, you must implement your own `Store` adapter.
- **Token estimation is crude** -- `CharsPerToken = 4.0` is a rough approximation. For non-English text or code, the actual token count can differ significantly. This can cause context window overflow or underutilization.
- **State tracking by full file path** -- The KV state key includes the full file path. Moving a file to a different directory causes full reprocessing, as the old key is not found.
- **`WithSession` context is fetched but unused** -- In `RetrieveWithContext`, the session context is fetched but only used for logging. The `_ = ctx` comment notes it "Could be used for query expansion in the future."

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **CLI-first with silent fallback** | Try external parser, fall back to internal on any failure | "Best of both worlds" -- leverage powerful external parsers when available, but never fail ingestion |
| **Polling-based file watcher** | `filepath.Walk` on a timer, not inotify/fsevents | Cross-platform compatibility; simpler implementation at the cost of latency and CPU usage |
| **Token estimation by character count** | `CharsPerToken = 4.0`, no actual tokenizer | Zero dependencies; crude but adequate for context window budgeting |
| **Single extraction worker** | No parallelism for entity extraction LLM calls | Prevents overwhelming the LLM API; backpressure via channel buffer |
| **Tight coupling to KektorDB** | `KektorAdapter` directly references `engine.Engine` types | Performance over portability; the adapter is thin and avoids abstraction overhead |
| **SHA-256 image deduplication** | Content-addressed image storage | Prevents storing the same image multiple times across pages or documents |
| **Greedy overlap algorithm** | Acknowledged as "greedy" in code comments | Simple and correct for most cases; TODO for token-aware overlap exists |
