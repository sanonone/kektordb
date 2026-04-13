# Module: internal/mcp

## Purpose

Exposes KektorDB as an MCP (Model Context Protocol) server over JSON-RPC 2.0 via stdio, allowing LLM agents (Claude, Cursor, VS Code, or any MCP-compatible client) to interact with KektorDB's memory system using natural language tools. Identifies as "KektorDB Memory" v0.5.1 with 17 registered tools across memory CRUD, graph operations, search, session management, meta-cognition, user profiling, and administration.

## Key Types & Critical Paths

**Critical structs:**
- `Service` -- Business logic: `engine *engine.Engine`, `embedder embeddings.Embedder`, `sessions map[string]*SessionContext`, `sessionsMu sync.RWMutex`.
- `SessionContext` -- `{ID, IndexName string, StartTime int64, Metadata map[string]any}`.
- Tool Args/Result structs -- All in `types.go` with `json` and `jsonschema` tags for MCP schema generation.

**Critical paths (hot functions):**
- `SaveMemory` -- Validates layer, embeds content, builds metadata, stores via VAdd, creates graph links. Most frequently called tool.
- `Recall` -- Hybrid search with layer filtering. Fires non-blocking `go s.engine.VReinforce(...)` when `Reinforce` flag is set.
- `TransferMemory` -- Cross-agent knowledge sharing: search, dimension handling, merge semantics, provenance tracking, graph topology copy, agent proxy node creation.
- `ListVectors` -- Two-phase: iterate under read lock, then batch-fetch metadata via `VGetMany` outside lock.

## Architecture & Data Flow

**Three-layer architecture:** MCP SDK (`github.com/modelcontextprotocol/go-sdk/mcp`) -> `server.go` (entry point, tool registration) -> `service.go` (business logic, 17 tool handlers) -> `engine.Engine` (persistence). The embedder is injected for text-to-vector conversion in search and save operations.

**Declarative tool registration:** Uses `mcp.AddTool(s, &mcp.Tool{...}, service.Method)`. The SDK inspects the method's argument/result structs via reflection to auto-generate JSON schemas from `jsonschema` struct tags. Adding a new tool requires only: define Args/Result structs with jsonschema tags, write the handler, register in `server.go`.

**Default index:** All tools default to `"mcp_memory"`, lazily created via `ensureIndex()` with cosine distance, 30-day decay half-life, and 16/200 HNSW parameters.

**Key tool patterns:**
- **`save_memory`:** Validates layer (episodic/semantic/procedural), embeds content, builds metadata (timestamp, source, layer, pinning, session_id, user_id), stores via `VAdd`, creates graph links (manual `Links` arg + auto-link to session entity).
- **`recall`:** Hybrid search with optional layer filtering and layer-weighted re-ranking. Supports `Reinforce` flag that fires a non-blocking goroutine (`go s.engine.VReinforce(...)`) to boost retrieved memories' relevance.
- **`traverse`:** Subgraph extraction with optional semantic guidance -- a `GuideQuery` vector and similarity threshold filter which only follows nodes semantically similar to a concept. Supports `AtTime` temporal querying.
- **`transfer_memory`:** Cross-agent knowledge sharing with dimension mismatch handling (mismatched vectors become zero-vector entity nodes), metadata provenance tracking (`_transferred_from`, `_transferred_at`), optional graph topology deep copy, and agent proxy node creation for traceability.
- **`adaptive_retrieve`:** RAG-optimized retrieval using `rag.AdaptiveRetriever` with graph-aware context expansion and token budget management.

**Session management:** Sessions are tracked in an in-memory `map[string]*SessionContext` AND as entity nodes in the database. `save_memory` with a `session_id` links the memory to the session entity in the graph AND propagates `user_id` from the session context if available.

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` -- Primary dependency. All 17 tools delegate to engine methods (VAdd, VSearch, VLink, VUnlink, VGet, VGetMany, VReinforce, FindPath, etc.).
- `pkg/embeddings` -- Embedder for text-to-vector conversion in `save_memory`, `recall`, `create_entity`, and search tools.
- `pkg/rag` -- `AdaptiveRetrieve` tool uses `rag.AdaptiveRetriever` directly.

**Used by:**
- `cmd/kektordb/main.go` -- Creates `NewMCPServer(eng, embedder)` when `--mcp` flag is set. Runs on stdio transport.
- MCP clients (Claude Desktop, Cursor, VS Code) -- Connect via stdio and call tools using JSON-RPC 2.0.

## Concurrency & Locking Rules

**Session map protected by `sync.RWMutex`:** `sessionsMu` guards the `sessions` map. `getSession` uses `RLock`, `setSession` and `removeSession` use `Lock`.

**Fire-and-forget reinforcement in `Recall`:** `go s.engine.VReinforce(...)` has no error handling, no context propagation, and no way to signal failure to the caller. If reinforcement fails, it silently drops.

**Two-phase `ListVectors`:** Iterates raw index entries under read lock, then batch-fetches metadata via `VGetMany` outside the lock. Avoids holding the index read lock during metadata hydration, preventing write starvation.

**All engine operations delegated:** VAdd, VSearch, VLink, etc. are assumed thread-safe internally by `engine.Engine`. No per-tool mutex or request rate limiting exists at the MCP layer.

**No connection-level session binding:** The session map is global across all MCP connections. Any client can access any session by ID -- sessions are keyed by session ID, not connection ID.

## Known Pitfalls / Gotchas

- **In-memory session state is lost on restart** -- The `sessions` map is not persisted. After a process restart, `save_memory` won't auto-propagate `user_id` from sessions unless the session is explicitly re-tracked via `start_session`. Session entities persist in the DB, but the in-memory tracking map does not.
- **Commented-out dead code** -- Both `CreateEntity` (lines 203-224) and `Traverse` (lines 410-456) have older versions commented in the source. These are development artifacts that create confusion about which version is canonical.
- **Hardcoded defaults scattered across handlers** -- Default index name `"mcp_memory"`, default relations list, default limit values, and default layer weights are not centralized. Changing a default requires editing multiple handler functions.
- **`VGetMany` error is ignored in `formatResults`** -- The call `s.engine.VGetMany(idx, ids)` ignores the error return value (line 634). Failed lookups are silently dropped, resulting in incomplete results with no indication to the LLM.
- **`ResolveConflict` soft delete semantics are unclear** -- Uses `VDelete` but the comment says "Non la cancelliamo fisicamente". The actual behavior depends on the engine's delete implementation (soft vs hard). This ambiguity could lead to unexpected data loss.
- **No pagination on most list operations** -- Only `ListVectors` and `ListUserProfiles` support offset/limit. `CheckSubconscious` and `AskMetaQuestion` have limits but no offset, making it impossible to page through large result sets.
- **Bootstrap fallback generates a real embedding** -- When `create_entity` is called on an empty index, it calls the embedder to bootstrap dimensions. This adds latency to the first entity creation and can fail if the embedder is unavailable.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Declarative tool registration via reflection** | `jsonschema` tags auto-generate MCP schemas | Eliminates boilerplate; adding a tool is just defining structs + handler |
| **In-memory session state** | Sessions tracked in a map, not persisted | Fast lookups; lost on restart but session entities persist in DB |
| **Bootstrap fallback for empty indices** | Generate real embedding from entity description to bootstrap dimensions | Handles the chicken-and-egg problem of creating the first node in an empty index |
| **Zero-vector entity nodes** | Nodes can be created without embeddings | Useful for graph-only entities that don't need semantic search |
| **Commented-out dead code** | Older versions of `CreateEntity` and `Traverse` commented in source | Active development artifacts; creates maintenance burden |
| **Hardcoded defaults scattered** | Default index name, relations, limits spread across handlers | Simpler than a centralized config struct; harder to audit and change |
| **Soft delete inconsistency in `ResolveConflict`** | Uses `VDelete` but comment says "non la cancelliamo fisicamente" | Behavior depends on engine's delete semantics; could be clearer |
