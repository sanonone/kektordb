# Module: pkg/proxy

## Purpose

An AI reverse proxy (`AIProxy`) that sits between LLM clients (like Open WebUI) and an LLM backend (like Ollama). It intercepts chat completion requests and enriches them with Retrieval-Augmented Generation (RAG), semantic caching, and security filtering before forwarding them upstream. Built on top of KektorDB's vector search engine using HNSW indices for all intelligent features.

## Key Types & Critical Paths

**Critical structs:**
- `AIProxy` -- Main proxy: `cfg *Config`, `engine *engine.Engine`, `proxy *httputil.ReverseProxy`, `firewallPatterns []*regexp.Regexp`, `fastLLMClient`, `llmClient`, `assetURLPrefix string`.
- `responseCapturer` -- Response tee: embeds `http.ResponseWriter`, `body *bytes.Buffer`, `statusCode int`. Implements `Flush()` for SSE compatibility.
- `Config` -- Six logical groups: Proxy Server, Asset URL, Embedder, LLM, Firewall, Cache, RAG.

**Critical paths (hot functions):**
- `ServeHTTP()` -- Sequential 4-stage pipeline: rewrite -> HyDe -> dual-vector -> inject. Each request is handled in the `http.Server` goroutine.
- `checkCache()` -- Vector similarity lookup in cache index with TTL check. O(k) for top-k search.
- `handleCacheInvalidate()` -- BM25 text search on `sources` field to find dependent cache entries. Deletes via engine's `VDelete`.
- `checkStaticFirewall()` -- Pre-compiled regex patterns with `(?i)` prefix. Fastest filter, runs before any embedding work.

## Architecture & Data Flow

**Asset URL Rewriting:**

RAG contexts from PDF documents contain relative asset references (`](/assets/image.png)`). The proxy rewrites these to absolute URLs so LLMs can fetch images. Configure via:

```yaml
asset_base_url: "https://api.example.com"  # Default: http://localhost:{port}
```

**Sequential 4-stage RAG pipeline per request:**

1. **Query Rewriting (Stage 1):** If RAG is enabled and chat history has >1 message, the last query is rewritten into a standalone question using the `fastLLMClient` (lightweight model). Falls back to original query on failure.

2. **Grounded HyDe (Stages 2-3):** Instead of generating a blind hypothesis, this is "grounded": the rewritten query is embedded and used for a lightweight vector search (top 20, ef=100, threshold 0.5). If snippets are found, a `llmClient` ("smart" model) generates a hypothetical answer passage. This prevents hypothesis drift when no relevant context exists.

3. **Dual Vector Embedding (Stage 4):** Two vectors are computed: `originalVec` from the refined query (safe fallback) and `hydeVec` from the hypothesis text (experimental). RAG injection tries HyDe vector first; if no results, falls back to original vector.

4. **Context Injection:** Retrieved snippets include previous/next chunks (graph traversal), source document names, and related topic entities. The last user message's content is replaced with a templated prompt containing context + query.

**Two-layer firewall:** Static regex patterns (pre-compiled with `(?i)` at startup) run first for speed. Semantic firewall checks the original vector against a `FirewallIndex` of known malicious prompts; if nearest neighbor distance is below `FirewallThreshold` (default 0.25), the request is blocked with HTTP 403.

**Dependency-aware cache:** Cache entries store a space-separated `sources` field. Document-level invalidation (`POST /cache/invalidate`) uses BM25 text search on the `sources` field to find and delete all cache entries depending on a given document. TTL-based expiration and `MaxCacheItems` hard cap provide additional invalidation paths.

**Response capture:** `responseCapturer` wraps `http.ResponseWriter` to tee the response body for caching while simultaneously streaming it to the client. Implements `Flush()` for SSE/streaming compatibility.

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` -- Primary dependency. Uses VSearch, VSearchGraph, VDelete, VAdd for RAG, cache, and firewall.
- `pkg/embeddings` -- Embedder for query and hypothesis vector computation.
- `pkg/llm` -- Fast LLM for query rewriting, smart LLM for HyDe hypothesis generation.

**Used by:**
- `cmd/kektordb/main.go` -- Creates and starts the proxy server when `--enable-proxy` flag is set.
- External LLM clients (Open WebUI, etc.) -- Point their Base URL to the proxy's port (default :9092).

## Concurrency & Locking Rules

**No explicit mutexes in the proxy package:** The `AIProxy` struct is shared across all request goroutines but relies entirely on the engine's internal concurrency safety. `firewallPatterns` (compiled regexes) are read-only after initialization -- safe for concurrent reads.

**Strictly sequential pipeline:** Each HTTP request is handled by `ServeHTTP` in the goroutine provided by Go's `http.Server`. No parallelism between stages -- each stage depends on the previous one's output.

**Fire-and-forget cache writes:** Cache saves are dispatched via `go p.saveToCache(...)`. Cache expiration cleanup is also fire-and-forget: `go func(id string) { _ = p.engine.VDelete(...) }`. Both can fail silently -- acceptable for a cache layer.

**System task passthrough:** Requests matching known Open WebUI system task patterns (title generation, tag generation, follow-up suggestions) bypass all processing and are forwarded directly. Hardcoded string patterns -- fragile to upstream changes but avoids wasting resources on non-user queries.

## Known Pitfalls / Gotchas

- **Full request body buffering** -- The entire request body is read into memory before processing. For large payloads (e.g., long chat histories or file uploads), this can consume significant memory. No streaming request processing.
- **HyDe adds 2 LLM calls + 2 vector searches per request** -- When `RAGUseHyDe` is enabled, each request incurs: query rewrite (1 LLM), grounded search (1 vector search), hypothesis generation (1 LLM), dual embedding (2 vector computations), dual search (2 vector searches). This can add 2-5 seconds of latency.
- **Cache writes are fire-and-forget** -- `go p.saveToCache(...)` has no error handling. If the cache index is full (`MaxCacheItems` reached), the write is silently dropped. No retry, no notification.
- **System task passthrough uses hardcoded strings** -- The patterns for detecting Open WebUI system tasks are hardcoded string comparisons. If Open WebUI changes its internal task names, the passthrough will break and system tasks will go through the full RAG pipeline (wasting resources).
- **`responseCapturer` buffers the entire response** -- While it streams to the client in real-time via `Write()`, it also buffers the full response in memory for caching. For very long LLM responses, this doubles memory usage.
- **No connection pooling for upstream** -- `httputil.NewSingleHostReverseProxy` uses Go's default transport. For high-throughput scenarios, consider tuning `MaxIdleConns` and `IdleConnTimeout`.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Sequential pipeline** | No parallelism between stages | Simplicity; each stage depends on the previous one's output |
| **Fire-and-forget cache writes** | Cache writes can silently fail | Avoids blocking the response; acceptable for a cache |
| **Dual-vector with fallback** | Two embedding operations per request | HyDe improves recall; fallback ensures no regression |
| **Grounded HyDe** | Extra LLM call + extra vector search | Prevents hallucinated hypotheses when no context exists |
| **Original vector for firewall/cache** | Less precise than HyDe vector | Stability -- original vector is deterministic and reproducible |
| **MaxCacheItems hard cap** | No LRU eviction -- new entries dropped when full | Simpler implementation; could lead to cache starvation under high load |
| **No request rate limiting** | No built-in DoS protection | Assumes external reverse proxy (nginx, etc.) handles this |
| **Full body buffering** | Entire request/response in memory | Necessary for inspection/modification; problematic for very large payloads |
