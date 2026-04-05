# Module: pkg/client/cognitive

## Purpose

High-level cognitive abstractions built on top of the core KektorDB client, providing session lifecycle management with thread-safe context tracking, adaptive RAG context retrieval with configurable strategies, and multi-agent orchestration with sequential pipeline and parallel execution patterns.

## Key Types & Critical Paths

**Critical structs:**
- `SessionManager` -- `{client *Client, sessions map[string]*Session, mu sync.RWMutex, autoEnd bool, defaultTTL time.Duration}`. Two-level locking: manager mutex for session map, per-session mutex for context.
- `Session` -- `{ID string, client *Client, messages []ConversationMessage, mu sync.RWMutex}`. Local conversation context synced with server.
- `ManagedSession` -- Embeds `*Session` + `context.Context` + `context.CancelFunc`. RAII-style wrapper for `WithSession`/`WithSessionContext`.
- `ContextAssembler` -- `{client *Client, session *Session, strategy string, maxTokens int, expansionDepth int, semanticWeight/ graphWeight/ densityWeight float64}`. Immutable after construction.
- `MultiAgentCoordinator` -- `{agents map[string]*Agent, agentOrder []string, sessionManager *SessionManager, assembler *ContextAssembler, sharedState map[string]any, mu sync.RWMutex}`.
- `Agent` -- `{config AgentConfig, session *Session, coordinator *MultiAgentCoordinator}`. Each agent gets a dedicated session.

**Critical paths (hot functions):**
- `WithSession()` / `WithSessionContext()` -- RAII helpers: create session, run callback, guarantee cleanup via `defer` (cancel + end session + remove from manager).
- `ExecutePipeline()` -- Sequential agent execution: each agent's output becomes next agent's input. Stops on first failure.
- `ExecuteParallel()` -- Concurrent agent execution: `sync.WaitGroup` with pre-allocated results slice. Each goroutine writes to its own index.
- `RetrieveWithContext()` -- Adaptive retrieval within a session: auto-logs query and response summary as session messages.

## Architecture & Data Flow

**Session Management (`session.go`):** Two-level architecture -- `SessionManager` holds a `map[string]*Session` of active sessions; each `Session` wraps a server-side session ID with a local `[]ConversationMessage` context slice. RAII-style helpers (`WithSession`, `WithSessionContext`) create a session, execute a callback, and guarantee cleanup via `defer` (cancel context + end session on server + remove from manager). `GetContext()` returns a copy of the message slice to prevent external mutation.

**Adaptive Retrieval (`adaptive.go`):** `ContextAssembler` wraps the server's `/rag/retrieve-adaptive` endpoint with configurable strategies (`StrategyGreedy`, `StrategyDensity`, `StrategyGraph`), token budget, expansion depth, and three weighting factors (semantic, graph, density). Three retrieval methods: `Retrieve` (direct), `RetrieveWithContext` (within a session, auto-logs query/response as messages), `RetrieveWithCancel` (goroutine with `context.Context` cancellation). Post-processing utilities: `FormatSources`, `FilterSources`, `GroupSourcesByDocument`.

**Multi-Agent Orchestration (`multi_agent.go`):** `MultiAgentCoordinator` holds a map of agents, an ordered list (`agentOrder`), a `SessionManager`, an optional `ContextAssembler`, and a `sharedState` map. Each agent gets its own dedicated session. Four predefined roles: `AgentRolePlanner`, `AgentRoleRetriever`, `AgentRoleAnalyzer`, `AgentRoleValidator`. `ExecutePipeline` runs agents sequentially (each agent's output becomes the next agent's input, stops on first failure). `ExecuteParallel` uses `sync.WaitGroup` to fan out agents concurrently, writing results to a pre-allocated slice indexed by position.

## Cross-Module Dependencies

**Depends on:**
- `pkg/client` -- Core HTTP client. All cognitive operations funnel through `Client` methods (`StartSession`, `EndSession`, `AdaptiveRetrieve`).
- `context` -- Standard library for cancellation propagation in `WithSessionContext` and `RetrieveWithCancel`.

**Used by:**
- Application code -- Direct import by Go applications that need session management, adaptive retrieval, or multi-agent coordination.
- `examples/` -- Sample code demonstrating session lifecycle and multi-agent patterns.

## Concurrency & Locking Rules

**Two-level session locking:** `SessionManager` has its own `sync.RWMutex` for the session map; each `Session` has its own `sync.RWMutex` for the conversation context. This avoids holding the manager lock while manipulating individual session state.

**Copy-on-read for session context:** `GetContext()` returns a copy of the message slice to prevent external mutation. Lock granularity: `AddMessage` takes a write lock, `GetContext` takes a read lock, `GetUpdatedAt` takes a read lock.

**Stateless ContextAssembler:** All fields are set in the constructor and never mutated. Safe for concurrent use without synchronization.

**Parallel agent execution:** `ExecuteParallel` uses `sync.WaitGroup` with a pre-allocated results slice. Each goroutine writes to its own index -- no contention. Errors are collected in a parallel slice and checked after `wg.Wait()`.

**Shared state mutex:** `GetSharedState`/`SetSharedState` are guarded by the coordinator's `sync.RWMutex` for thread-safe inter-agent communication.

**Client-side cancellation only:** `RetrieveWithCancel` spawns a goroutine and uses `select` on `ctx.Done()` vs the result channel. If the context is cancelled, the goroutine continues running (no server-side cancellation) -- it is a client-side timeout only.

## Known Pitfalls / Gotchas

- **`defaultTTL` is dead code** -- The field is set in the constructor but no background goroutine enforces it. Sessions never expire automatically. If you rely on TTL for cleanup, it will not work.
- **`ExecuteAgent` has inconsistent error handling** -- When adaptive retrieval fails, the method returns `AgentResult{Success: false}` with the error stored in the result, but the outer `error` return is `nil`. This means `ExecutePipeline` and `ExecuteParallel` will NOT detect this as a failure via the error return -- they must check `result.Success`.
- **In-memory session map is not reconciled with server** -- If a session expires server-side, the local map still holds a reference. Subsequent operations on that session will fail with a server error. The local map must be manually cleaned up.
- **`RetrieveWithContext` fetches session context but doesn't use it** -- The session context is retrieved but only used for logging (`_ = ctx` with a TODO comment). This is placeholder code that adds an unnecessary HTTP round-trip.
- **`RetrieveWithCancel` goroutine leaks on timeout** -- If the context is cancelled, the retrieval goroutine continues running on the server side. There is no server-side cancellation mechanism. The goroutine completes but its result is discarded.
- **`WithSessionContext` runs callback in a goroutine** -- This means the callback's panics are not recovered by the caller. If the callback panics, the session cleanup still runs (via `defer`), but the panic is lost.
- **Parallel execution has all-or-nothing error handling** -- If any agent fails, the entire `ExecuteParallel` returns an error, even if other agents succeeded. The results slice still contains partial results, but the caller only sees the error.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **In-memory session map** | No persistence, no server reconciliation | Fast lookups; lost on process restart. Server-side sessions may persist independently |
| **Server-heavy adaptive retrieval** | All "adaptive" logic happens server-side | Client is purely a configuration passthrough; no local retrieval intelligence |
| **Session-per-agent isolation** | Every agent gets its own server-side session | Clean isolation but creates server-side resource pressure with many agents |
| **Pipeline stops on first failure** | No retry, no skip, no partial success | Simple error semantics; caller can implement retry at a higher level |
| **`ExecuteAgent` inconsistent error handling** | Returns nil error on retrieval failure, sets `result.Success: false` | Pipeline/Parallel must check `result.Success`, not just the error return |
| **`defaultTTL` is dead code** | Set but never used by any background goroutine | Placeholder for future TTL enforcement; currently no session expiration |
| **Hardcoded provenance** | `IncludeProvenance` always `true` in `Retrieve()` | Caller cannot disable provenance; ensures traceability but adds payload size |
