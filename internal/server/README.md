# Module: internal/server

## Purpose

The HTTP API layer for KektorDB, serving as the primary interface between external clients (API consumers, UI dashboard, MCP tools) and the underlying database engine. Manages 50+ RESTful endpoints across vector operations, graph traversal, RAG retrieval, cognitive engine control, RBAC authentication, and system administration.

## Key Types & Critical Paths

**Critical structs:**
- `Server` -- Central orchestrator: `Engine *engine.Engine`, `taskManager *TaskManager`, `vectorizerConfig *Config`, `vectorizerService *VectorizerService`, `authToken string`, `authService *auth.AuthService`, `gardener *cognitive.Gardener`.
- `TaskManager` -- Async task tracking: `tasks map[string]*Task`, `mu sync.RWMutex`, cleanup goroutine (every 10 min, max age 1 hour).
- `Task` -- Individual async operation: `ID`, `Status`, `Error`, `Progress`, `CreatedAt`, `mu sync.RWMutex`. Has `Refresh()` and `Wait(interval, timeout)` methods.
- `VectorizerService` -- Manages RAG pipelines: `pipelines []*rag.Pipeline`, `config *Config`.

**Critical paths (hot functions):**
- Auth middleware (`middleware.go`) -- Three-tier: no token (open), master bypass, RBAC policy check. Extracts namespace from URL path or JSON body (reads entire body).
- `writeHTTPResponse()` / `writeHTTPError()` -- Centralized JSON response formatting. 500 errors sanitized (actual error logged, generic message returned).
- SSE event stream (`/events/stream`) -- Subscribes to engine's EventBus (buffer: 128). Uses `http.Flusher` and context cancellation.
- `extractNamespaceFromRequest()` -- Reads full body, parses JSON for `index_name`, regenerates `r.Body` via `io.NopCloser`.

## Architecture & Data Flow

**Handler chain order (innermost to outermost):** `Mux -> Auth -> Logging -> Recovery -> rootMux`. Recovery is the outermost wrapper (catches all panics), then Logging, then Auth, then the actual mux handlers. The `/healthz` endpoint sits on `rootMux` and bypasses all middleware -- health checks work even during auth failures.

**Startup sequence (`NewServer`):** Loads vectorizer YAML config (with env-var expansion and strict YAML parsing), creates the `assets/` directory, initializes `AuthService` backed by the engine's KV store, loads cognitive engine config, conditionally creates an LLM client for gardener mode "advanced"/"meta", instantiates `VectorizerService` (RAG pipelines), then builds the HTTP handler chain.

**Handler patterns:**
- **Synchronous:** Most handlers (KV ops, vector add, graph ops) execute inline and return results directly.
- **Asynchronous:** Heavy operations (AOF rewrite, vector compress, index maintenance) return `202 Accepted` with a `Task` UUID. Clients poll `/system/tasks/{id}` for status.
- **Streaming:** `/events/stream` SSE endpoint uses `http.Flusher` and context cancellation for clean client disconnect handling.
- **Dual-path:** Some endpoints exist on both legacy paths (`/vector/actions/create`) and RESTful paths (`POST /vector/indexes`) for backward compatibility.

**RBAC middleware:** Three-tier authorization: (1) No token configured = open access. (2) Master token bypass = full admin. (3) RBAC policy check via KV store lookup. Role determination classifies paths as `read`/`write`/`admin`; POST endpoints that are read-like (search, traverse, rag/retrieve) are classified as `read`. Namespace enforcement extracts the target index from URL path or JSON body, reading and regenerating `r.Body` via `io.NopCloser(bytes.NewBuffer(bodyBytes))`.

**VectorizerService:** Manages RAG pipelines that automatically ingest, chunk, embed, and index documents. Per-vectorizer config from YAML: source path, embedder type, chunking strategy, parser config, graph entity extraction, file include/exclude patterns, metadata templates.

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` -- Primary dependency. All data operations delegate to engine methods.
- `pkg/auth` -- RBAC policy management, API key verification.
- `pkg/cognitive` -- Gardener creation and lifecycle management.
- `pkg/rag` -- VectorizerService creates and manages RAG pipelines.
- `pkg/embeddings` -- Embedder instances for vectorizer pipelines.
- `pkg/llm` -- Optional LLM client for gardener advanced/meta mode.
- `pkg/metrics` -- Prometheus metrics recording in logging middleware.

**Used by:**
- `cmd/kektordb/main.go` -- Creates `NewServer()` and calls `Run()` in HTTP mode.
- External clients (Go client, Python client, curl, browsers) -- All HTTP API consumers.

## MCP (Model Context Protocol) Mode

The server can also run as an MCP (Model Context Protocol) server instead of HTTP:

```bash
kektordb --mcp  # Start in MCP mode
```

**MCP Tools:** 17 tools exposing database operations:
- **Vector:** create_index, add, search, delete, get
- **Graph:** link, unlink, get_connections, get_relations, find_path
- **Metadata:** set_meta, get_meta, delete_meta, find_by_filter
- **Memory:** reinforce, get_stats
- **Maintenance:** maintenance_run

MCP mode uses stdio JSON-RPC for communication with MCP clients (Claude Desktop, etc.).

## Concurrency & Locking Rules

**HTTP handlers run in `net/http` goroutines:** One goroutine per request, managed by Go's default server. No explicit request-level rate limiting or connection pooling at this layer.

**TaskManager uses two-level `sync.RWMutex`:** Read-heavy workload (many status polls, few writes). The task map is protected by a manager-level mutex; each individual `Task` has its own mutex for state updates. Background cleanup goroutine runs every 10 minutes, removing tasks older than 1 hour.

**ResponseWrapper delegates `Flush()` and `Hijack()`:** The logging middleware's `responseWrapper` properly delegates to the underlying `http.Flusher` (required for SSE) and `http.Hijacker` (required for WebSocket upgrades), preserving streaming through the middleware chain.

**Namespace extraction reads entire body:** `extractNamespaceFromRequest()` reads the full request body into memory to parse the namespace, then regenerates `r.Body`. For large batch uploads, this doubles memory usage temporarily.

**Graceful shutdown:** `http.Server.Shutdown(ctx)` with 5-second timeout. Stops vectorizers and gardener. Explicitly does NOT close the Engine -- that is managed by `main.go` for proper lifecycle ordering.

## Known Pitfalls / Gotchas

- **No request timeouts on `http.Server`** -- `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are not set. Slow clients can hold connections open indefinitely, exhausting file descriptors. Assumes external reverse proxy handles this.
- **Body buffering doubles memory for large uploads** -- `extractNamespaceFromRequest()` reads the entire body into a `[]byte`, then wraps it in `io.NopCloser(bytes.NewBuffer(bodyBytes))`. For `VAddBatch` with thousands of vectors, this temporarily doubles memory usage.
- **Task cleanup is time-based, not status-based** -- Tasks are removed after 1 hour regardless of status. A long-running task (e.g., compress on a large index) that takes >1 hour would be removed from tracking while still executing. The client's `Task.Wait()` would return an error.
- **`GetEmbedderForIndex` is O(n)** -- Linear iteration through the pipeline config slice to find the embedder for a given index. Acceptable for small numbers of pipelines but degrades with many vectorizers.
- **`/healthz` bypasses auth** -- Anyone can probe the health endpoint. This is intentional but means the endpoint reveals server liveness to unauthenticated parties.
- **Async handlers return 202 with no completion guarantee** -- If the background goroutine panics, the task status is never updated. The client polling `/system/tasks/{id}` will see `"started"` forever (until the 1-hour cleanup removes it).
- **`decodeJSON()` uses `DisallowUnknownFields()`** -- Strict JSON parsing. If a client sends an extra field, the request is rejected with 400. This catches typos but breaks backward compatibility when new fields are added to request structs.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Large handler file** | `http_handlers.go` is 2221 lines | Simplicity of single-file navigation; would benefit from splitting by domain |
| **Body buffering for RBAC** | Reads entire request body for namespace extraction | Necessary for namespace enforcement when index name is in JSON body; doubles memory for large uploads |
| **No request timeouts** | No `ReadTimeout`, `WriteTimeout`, or `IdleTimeout` on `http.Server` | Assumes external reverse proxy handles slow-client attacks; could lead to resource exhaustion |
| **Health check bypasses auth** | `/healthz` on `rootMux` outside middleware chain | Intentional -- health checks should work regardless of auth state |
| **Task cleanup is time-based** | Removed after 1 hour regardless of status | Simple; a long-running task >1 hour would be removed from tracking while still executing |
| **Strict parsing everywhere** | JSON: `DisallowUnknownFields()`; YAML: `KnownFields(true)` | Catches configuration typos early; prevents silent misconfiguration |
| **500 errors sanitized** | Actual error logged, generic message returned to client | Prevents information leakage; internal details never exposed to clients |
