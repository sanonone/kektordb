# Module: pkg/compiler

## Purpose

Knowledge artifact compilation for KektorDB. Compiles structured knowledge artifacts (entity cards, topic overviews, user profiles, timelines, session summaries) from graph queries, combining deterministic field computation with optional LLM-assisted generation. Manages asynchronous compilation tasks with TTL-based cleanup.

## Key Types & Critical Paths

**Critical structs:**
- `Compiler` — Orchestrator: `eng *engine.Engine`, `llm llm.Client`, `embedder embeddings.Embedder`, `templates map[string]CompileTemplate`, `taskManager *compileTaskManager`.
- `CompileTemplate` — Defines a knowledge artifact schema: list of `FieldDef` with type, source, LLM flag, and priority.
- `CompileRequest` — Input: `Template string`, `Sources SourceSpec`, `CompileMode`, `Overrides`.
- `Artifact` — Output: pinned graph node with `Data map[string]any` (compiled fields), `Status CompileStatus`, `Provenance []SourceNode`, version tracking.
- `compileTaskManager` — Async task tracker: `tasks map[string]*compileTask` with TTL-based eviction (24h default) via background sweep goroutine.

**Built-in templates (5):**
| Template | Description |
|----------|-------------|
| `entity_card` | Single-entity summary with key attributes, importance, context |
| `topic_overview` | Multi-source topic synthesis with thematic clustering |
| `user_profile` | User personality profile: communication style, expertise, preferences |
| `timeline` | Chronological narrative of key events linked to an entity |
| `session_summary` | Consolidated summary of a session's interactions |

**Critical paths:**
- `Compile()` — Synchronous compile: resolves template → resolves mode → resolves schema → queries graph for sources → for each field: if LLM required, calls LLM with assembled context; otherwise computes deterministically → stores artifact as pinned graph node → returns `*Artifact`.
- `StartAsyncCompile()` — Fire-and-forget: generates task ID (atomic counter), inserts into task map, spawns goroutine that calls `Compile()`. Returns task ID for polling via `GetTaskStatus()`.
- `NeedsAsync()` — Returns true if at least one field in the resolved schema has `LLM: true` and an LLM client is configured. Used by MCP `request_knowledge` to decide sync vs async.

## Architecture & Data Flow

**Compile pipeline:**
```
CompileRequest
  → resolveTemplate (find template by name)
  → resolveMode (deterministic or LLM-assisted)
  → resolveSchema (field definitions)
  → querySources (graph traversal with depth + filters)
  → for each field:
      deterministic: computeField (aggregate metadata)
      LLM: buildContext → llm.Chat() → parseResponse
  → storeArtifact (VAdd with pinned metadata)
  → return Artifact
```

**Async task lifecycle:**
1. `StartAsyncCompile` → inserts task with `Status: pending` → spawns goroutine
2. Goroutine sets `Status: compiling` → calls `Compile()` → sets `Status: complete` or `Status: failed` with error
3. Client polls via `GetTaskStatus(taskID)`
4. Background sweep goroutine (every hour) evicts tasks with `DoneAt > 24h`

**Artifact Watcher:** The `pkg/compiler/watcher.go` monitors artifact staleness: subscribes to `EventBus` for graph changes, tracks which artifacts need recompilation, and triggers `ScanArtifacts()` periodically. Integrated into the Gardener's think cycle for autonomous lifecycle management.

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` — Graph queries (`VSearch`, `VGetLinks`, `VGetRelations`, `FindPath`), artifact storage (`VAdd`, `VSetMetadata`), graph entity creation.
- `pkg/llm` — LLM client for AI-assisted field compilation (text generation, sentiment, tagging).
- `pkg/embeddings` — Embedder for semantic sourcing and artifact vector averaging.
- `sync/atomic` — `taskIDCounter` uses `atomic.Uint64` for concurrent-safe ID generation.

**Used by:**
- `internal/mcp` — `request_knowledge` tool delegates to `Compiler.Compile()` or `StartAsyncCompile()`.
- `internal/server` — `POST /compile` endpoint, Artifact REST API endpoints.
- `pkg/cognitive` — Gardener triggers recompilation when source nodes change.

## Concurrency & Locking Rules

**Artifact-level locking:** `Compiler.muPerArtifact sync.Map` provides artifact-level serialization for concurrent compiles targeting the same entity. Different entities can compile in parallel.

**Task ID generation:** `taskIDCounter` is `atomic.Uint64` — safe for concurrent `StartAsyncCompile` calls from multiple goroutines. Fixed in v0.6.0 (E5).

**Task map eviction:** `compileTaskManager` sweep goroutine acquires `mu.Lock()` briefly to iterate and delete expired tasks. `GetTaskStatus` uses `mu.RLock()`. No deadlock risk: sweep is fast (iterate + delete) and runs hourly.

**Artifact Watcher:** Uses two-phase locking: Phase 1 acquires `w.mu.RLock` to identify stale artifacts, releases it, then Phase 2 acquires `w.mu.Lock` only for metadata updates. This prevents holding the lock during LLM compilation (seconds/minutes).

## Design Trade-offs & Known Pitfalls

**Sync vs Async:** `Compile()` is synchronous and can block for LLM call duration (seconds). `StartAsyncCompile()` is fire-and-forget with polling. MCP `request_knowledge` uses `NeedsAsync()` to decide which path to take.

**LLM dependency:** Templates with LLM fields require a configured LLM client. Without one, `CompileMode` falls back to deterministic, and LLM fields are skipped.

**Artifact caching:** The MCP `request_knowledge` tool caches compiled artifacts in-memory. When source nodes change, the Artifact Watcher invalidates the cache and marks the artifact as stale. Subsequent requests trigger recompilation.
