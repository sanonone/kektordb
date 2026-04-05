# Module: pkg/embeddings

## Purpose

Defines a minimal interface for converting text into float32 vector embeddings, with concrete implementations for OpenAI's embedding API and Ollama's local embedding endpoint. The foundation of all vector operations in KektorDB -- used by the RAG pipeline, vectorizer services, MCP layer, proxy, and the main application entry point.

## Key Types & Critical Paths

**Critical types:**
- `Embedder` (interface) -- Single method: `Embed(text string) ([]float32, error)`. TODO: `EmbedBatch(texts []string)`.
- `OpenAIEmbedder` -- `{url, model, apiKey string, timeout time.Duration, httpClient *http.Client}`. Default URL: `https://api.openai.com/v1/embeddings`.
- `OllamaEmbedder` -- `{url, model string, timeout time.Duration, httpClient *http.Client}`. No default URL -- caller must provide.

**Critical paths (hot functions):**
- `Embed()` -- HTTP POST with JSON payload, response parsing via `json.Decoder`. Called for every text that needs vectorization (ingestion, search, cache, firewall).
- `NewOpenAIEmbedder()` / `NewOllamaEmbedder()` -- Constructor with timeout defaulting to 60s if `<= 0`.

## Architecture & Data Flow

**Minimal interface:** `Embedder` has a single method: `Embed(text string) ([]float32, error)`. A `TODO` comment indicates batch embedding (`EmbedBatch`) is planned but not yet implemented.

**OpenAI embedder:** `POST` to `{url}/embeddings` with payload `{"input": text, "model": model}`. Expects response `{"data": [{"embedding": [...]}]}`. Returns the first embedding from the `data` array. Default URL: `https://api.openai.com/v1/embeddings`. Default timeout: 60 seconds. Uses `json.NewDecoder(resp.Body).Decode()` for memory-efficient streaming JSON parsing.

**Ollama embedder:** `POST` to `{url}/api/embeddings` with payload `{"model": model, "prompt": text}`. Note: uses `"prompt"` not `"input"`, matching Ollama's endpoint format. Expects response `{"embedding": [...]}` -- flat structure, not wrapped in `data` array. No API key (Ollama is local/auth-less). No default URL -- caller must provide it.

**Error handling:** Both providers wrap errors with provider-prefixed messages (`"openai request failed"`, `"ollama request failed"`) for easy debugging. Non-200 status codes return descriptive errors. Empty data responses are caught and reported.

## Cross-Module Dependencies

**Depends on:**
- `net/http`, `encoding/json`, `bytes`, `io` -- Standard library HTTP and JSON.
- No external dependencies beyond the Go standard library.

**Used by:**
- `pkg/rag` -- Pipeline embedder for document ingestion (chunk -> vector).
- `internal/server` -- VectorizerService creates embedder instances from YAML config.
- `internal/mcp` -- Injected into MCPServer for `save_memory`, `recall`, `create_entity` tools.
- `pkg/proxy` -- Embedder for query rewriting, HyDe hypothesis, cache lookup, and firewall checks.
- `cmd/kektordb/main.go` -- Creates Ollama embedder for MCP mode and proxy mode.

## Concurrency & Locking Rules

**Immutable after construction:** Both embedder structs have all fields set in the constructor and never mutated. `http.Client` is goroutine-safe. Both implementations are safe for concurrent use without synchronization.

**No mutexes, channels, or goroutine spawning:** Pure synchronous HTTP client code. Each `Embed()` call blocks until the response is received or the timeout fires.

**No retry logic:** Transient failures must be handled by callers. No exponential backoff, no circuit breaker.

**No `context.Context`:** Callers cannot cancel long-running requests (e.g., if a user disconnects from an HTTP handler).

## Known Pitfalls / Gotchas

- **No default URL for Ollama embedder** -- Unlike `llm.NewClient` which auto-fills defaults, `NewOllamaEmbedder` requires the caller to provide the URL. This is an inconsistency between the two packages. If you pass an empty string, the HTTP request will fail.
- **`json.Decoder` vs `io.ReadAll` inconsistency** -- The embedding packages use the more memory-efficient `json.Decoder`, while the LLM client uses `io.ReadAll` + `json.Unmarshal`. Both are correct, but the inconsistency is notable.
- **Anonymous response structs** -- Response types are defined inline inside the `Embed()` methods. If another method needs the same response shape, it would be duplicated. Not a current issue but limits extensibility.
- **No shared HTTP transport** -- Each embedder creates its own `*http.Client`. In high-throughput scenarios with many embedder instances, this means multiple independent connection pools. A shared `http.Transport` could improve connection reuse.
- **EmbedBatch is a TODO** -- When ingesting thousands of documents, calling `Embed()` one at a time is slow. The planned `EmbedBatch` would reduce HTTP round-trips. Until then, callers must implement their own batching.
- **Ollama uses `"prompt"` not `"input"`** -- The payload key differs from OpenAI's API. If you try to use an OpenAI-compatible endpoint with the Ollama embedder, it will fail because the key is wrong.
- **Timeout defaults to 60s** -- If you pass `timeout <= 0`, it defaults to 60 seconds. For large documents or slow models, this may be insufficient. The LLM client uses 120s, which is more appropriate for generation tasks.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Minimal interface (single method)** | Clean, focused abstraction | No batch support yet (noted as TODO); easy to implement for new providers |
| **Separate structs per provider** | Different URL formats, payload shapes, response structures | Some duplicated HTTP boilerplate; cleaner than a single struct with provider-specific branching |
| **`json.Decoder` vs `io.ReadAll`** | Streaming JSON parsing | More memory-efficient than reading the full body first; better practice |
| **Configurable timeout** | Passed via constructor (default 60s) | Unlike the LLM client's hardcoded 120s; more flexible |
| **No default URL for Ollama** | Caller must provide the URL | Inconsistent with `llm.NewClient` which auto-fills defaults; slight API friction |
| **Anonymous response structs** | Response types defined inline inside `Embed()` methods | Clean (no exported types needed) but not reusable; duplication if another method needs the same shape |
| **No shared HTTP transport** | Each embedder creates its own `*http.Client` | Multiple independent connection pools in high-throughput scenarios; a shared `http.Transport` could improve reuse |
