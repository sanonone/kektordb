# Module: pkg/llm

## Purpose

A unified, provider-agnostic LLM client that talks to any OpenAI-compatible `/chat/completions` endpoint. A single implementation serves all providers (OpenAI, Ollama, HuggingFace, LocalAI, vLLM) through URL normalization. Used throughout KektorDB by the RAG pipeline, cognitive gardener, MCP server, HTTP handlers, and proxy layer.

## Key Types & Critical Paths

**Critical structs:**
- `Client` (interface) -- `Chat(systemPrompt, userQuery string) (string, error)` and `ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error)`.
- `OpenAIClient` -- Concrete impl: `cfg Config`, `httpClient *http.Client` (120s timeout). Single struct for all providers.
- `Config` -- `{Provider, BaseURL, APIKey, Model string, Temperature float64, MaxTokens int}`. YAML/JSON tagged.
- `ChatRequest` / `Message` / `ChatResponse` -- Internal payload types for JSON marshaling/unmarshaling.

**Critical paths (hot functions):**
- `sendRequest()` -- Core HTTP dispatcher: marshal payload, create POST request, set headers, execute with 120s timeout, read body, check status, parse response. Used by both `Chat` and `ChatWithImages`.
- `NewClient()` -- URL normalization based on provider string. Default URLs: `ollama` -> `localhost:11434/v1`, `openai` -> `https://api.openai.com/v1`, `huggingface` -> `https://api-inference.huggingface.co/models/`.

## Architecture & Data Flow

**Single struct, all providers:** `OpenAIClient` holds a `Config` (Provider, BaseURL, APIKey, Model, Temperature, MaxTokens) and a dedicated `*http.Client`. `NewClient` normalizes the `BaseURL` based on the `Provider` string (e.g., `ollama` -> `localhost:11434/v1`, `openai` -> `https://api.openai.com/v1`) then uses one shared HTTP transport for all requests.

**Blocking-only chat:** `Chat(systemPrompt, userQuery)` and `ChatWithImages(systemPrompt, userQuery, images)` are the two public methods. Both use `sendRequest()` which marshals the payload to JSON, creates a `POST` to `{BaseURL}/chat/completions`, sets `Content-Type: application/json` and `Authorization: Bearer {APIKey}`, and executes with a hardcoded 120-second timeout. Streaming is explicitly disabled (`Stream: false`) -- the system uses LLM calls as part of synchronous internal pipelines where a full response is needed before proceeding.

**Vision support:** `ChatWithImages` uses `map[string]interface{}` for the payload (not the typed `ChatRequest` struct) because multi-modal content arrays require a different JSON structure. This loses compile-time type safety for the vision path but is necessary for the dynamic payload shape.

**Default configuration:** `DefaultConfig()` returns safe Ollama-local defaults: `localhost:11434/v1`, model `qwen3:4b`, temperature `0.0`.

## Cross-Module Dependencies

**Depends on:**
- `net/http`, `encoding/json`, `io` -- Standard library HTTP and JSON.
- No external dependencies beyond the Go standard library.

**Used by:**
- `pkg/cognitive` -- Gardener uses `Chat()` for contradiction detection, user preferences, failure diagnosis, knowledge evolution, and consolidation synthesis.
- `pkg/rag` -- Pipeline uses `Chat()` for entity extraction and vision analysis (PDF image descriptions).
- `internal/server` -- Creates LLM client for gardener advanced/meta mode.
- `internal/mcp` -- Indirectly via engine and gardener.
- `pkg/proxy` -- Uses `Chat()` for query rewriting (fast LLM) and HyDe hypothesis generation (smart LLM).

## Concurrency & Locking Rules

**Read-only after construction:** The `OpenAIClient` struct has only `cfg` and `httpClient` fields, both set in `NewClient` and never mutated. `http.Client` is safe for concurrent use by multiple goroutines. A single `OpenAIClient` instance can be safely shared across goroutines without synchronization.

**No explicit mutexes, channels, or goroutines:** The package is purely synchronous HTTP client code. Each call blocks until the response is received or the 120-second timeout fires.

**No context cancellation:** No `context.Context` is passed through the call chain. Callers cannot cancel in-flight requests or propagate deadlines.

**No retry logic:** A failed request returns the error immediately to the caller. Transient network errors or rate limits must be handled by callers.

## Known Pitfalls / Gotchas

- **Hardcoded 120s timeout is not configurable** -- Every `Chat()` call has the same 120-second timeout. For fast models (e.g., `qwen3:4b`), this is excessive. For large outputs or slow providers, it may be insufficient. The timeout is set on the `http.Client`, not per-request.
- **`io.ReadAll` before JSON parse** -- The entire response body is read into memory before unmarshaling. For very large LLM responses (e.g., long summaries), this can cause memory spikes. The embedding packages use `json.Decoder` which is more memory-efficient.
- **No streaming support** -- `Stream` is always `false`. This is by design for internal pipeline use but makes the client unsuitable for interactive chat UIs where token-by-token streaming is expected.
- **`interface{}` payload for vision loses type safety** -- `ChatWithImages` uses `map[string]interface{}` instead of a typed struct. A typo in the map key will not be caught at compile time and will cause a runtime API error.
- **No rate limit handling** -- If the provider returns HTTP 429 (Too Many Requests), the error is propagated directly to the caller. No exponential backoff, no retry-after header parsing.
- **Provider URL normalization trims trailing slashes** -- `strings.TrimSuffix(baseURL, "/")` is applied. If your provider requires a trailing slash (unusual but possible), it will be silently removed.
- **API key is optional** -- If `APIKey` is empty, no `Authorization` header is sent. This works for Ollama (no auth) but will fail for cloud providers that require authentication.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Single implementation for all providers** | URL normalization instead of per-provider structs | All supported providers speak the OpenAI-compatible protocol; one code path reduces duplication |
| **`interface{}` payload for vision** | Loses compile-time type safety | Necessary for multi-modal content arrays which have a different JSON structure |
| **No streaming** | Always `Stream: false` | Simplifies the API; matches the synchronous pipeline use case (HyDe, entity extraction) |
| **Hardcoded 120s timeout** | Not configurable per-call | Reasonable for generation tasks; may be too long for fast models or too short for large outputs |
| **No `context.Context`** | Simpler API surface | Callers cannot cancel in-flight requests; acceptable for internal pipeline use |
| **`io.ReadAll` before parse** | Reads entire response into memory | Simple and correct; could be memory-intensive for very large responses |
| **Provider-specific URL defaults** | Auto-filled in `NewClient` | Consistent with the LLM client; unlike the Ollama embedder which requires caller-provided URL |
