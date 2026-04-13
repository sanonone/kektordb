# Module: cmd/kektordb

## Purpose

The sole entry point for the KektorDB server application (v0.5.1). Orchestrates the entire startup lifecycle, configures all subsystems, supports dual-mode operation (HTTP server or MCP stdio server), and handles graceful shutdown with ordered teardown of proxy, API server, and engine.

## Key Types & Critical Paths

**Critical functions:**
- `main()` -- Entry point: env vars -> flags -> logger -> engine -> mode branch (HTTP/MCP) -> signal wait -> ordered shutdown.
- `getEnv()` -- Helper: reads env var with fallback default. Used for `KEKTOR_PORT`, `KEKTOR_DATA_DIR`, `KEKTOR_TOKEN`.
- Signal handler -- `make(chan os.Signal, 1)` with `signal.Notify` for SIGINT/SIGTERM. Blocks on `<-stopChan`.

**Critical paths (hot functions):**
- Shutdown sequence -- 10-second timeout context: (A) `proxyServer.Shutdown(ctx)`, (B) `srv.Shutdown()`, (C) `eng.Close()`. Ordered teardown ensures data integrity.
- Engine initialization -- `engine.Open(opts)` with `log.Fatalf` on failure. No recovery from engine startup errors.

## Architecture & Data Flow

**Three-tier configuration precedence:** Environment variables (lowest) -> CLI flags (highest). `getEnv()` helper provides defaults from env vars, but `flag.String()` overrides them with its own defaults unless the flag is explicitly passed. YAML configs are loaded lazily for optional subsystems (vectorizers, cognitive engine, proxy).

**Startup sequence (linear flow):**
1. Environment variable resolution: `KEKTOR_PORT` (default `:9091`), `KEKTOR_DATA_DIR`, `KEKTOR_TOKEN`.
2. Flag parsing: `--http-addr`, `--aof-path`, `--save`, `--auth-token`, `--aof-rewrite-percentage`, `--vectorizers-config`, `--cognitive-config`, `--log-level`, `--enable-proxy`, `--proxy-config`, `--mcp`.
3. Logger setup: Global `slog` text handler at requested level.
4. Engine initialization: `engine.Open(opts)` with `log.Fatalf` on failure.
5. Mode branch: MCP mode (redirects logs to `kektordb_mcp.log`, creates Ollama embedder, runs on stdio) or HTTP mode (creates `server.NewServer`, launches HTTP listener in goroutine).
6. Optional AI proxy: If `--enable-proxy`, loads proxy config, initializes embedder, creates `proxy.NewAIProxy`, launches separate `http.Server`.

**Graceful shutdown (ordered A -> B -> C):** Signal handler catches SIGINT/SIGTERM, creates a 10-second timeout context, then: (A) Stops the Proxy server via `proxyServer.Shutdown(ctx)` (graceful HTTP drain), (B) Stops the API server via `srv.Shutdown()` (stops vectorizers too), (C) Closes the Engine via `eng.Close()` (final AOF flush to disk).

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` -- Core database engine. Created first, closed last.
- `internal/server` -- HTTP API server (created in HTTP mode).
- `internal/mcp` -- MCP server (created in MCP mode).
- `pkg/proxy` -- AI reverse proxy (created when `--enable-proxy` is set).
- `pkg/embeddings` -- Ollama embedder for MCP mode and proxy mode.
- All config packages -- YAML loading for vectorizers, cognitive engine, proxy.

**Used by:**
- Nothing. This is the application entry point. It is the consumer of all other modules.

## Concurrency & Locking Rules

**Signal handling:** Buffered `chan os.Signal` (size 1) with `signal.Notify` for SIGINT and SIGTERM. Blocks on `<-stopChan` until a signal arrives.

**Goroutine lifecycle:** HTTP server and proxy server run in separate goroutines. If either crashes, it calls `os.Exit(1)` directly -- terminating the process without graceful cleanup of the other goroutines or the engine. This is a potential data-loss risk.

**MCP mode log redirection:** Logs are redirected to `kektordb_mcp.log` to keep stdio clean for JSON-RPC. The log file path is hardcoded to the current working directory.

**No explicit mutexes:** This is an application entry point, not a library. Concurrency is managed by the subsystems it initializes (engine, server, proxy).

## Known Pitfalls / Gotchas

- **`os.Exit(1)` in goroutines bypasses cleanup** -- If the HTTP server or proxy crashes (e.g., panic not caught by recovery middleware), it calls `os.Exit(1)` directly. This terminates the process immediately without closing the engine, flushing the AOF, or stopping other goroutines. Potential data loss.
- **Embedder type switch is a no-op** -- The `switch proxyCfg.EmbedderType` always creates `NewOllamaEmbedder` regardless of whether the type is `"openai"` or anything else. If you configure `embedder_type: openai` in proxy.yaml, it still uses Ollama. This is a placeholder for future provider support.
- **MCP mode log file path is hardcoded** -- Logs go to `kektordb_mcp.log` in the current working directory. If you run KektorDB from different directories, the log file ends up in different places. No flag to configure the log path.
- **10-second shutdown timeout may be insufficient** -- For large databases, `eng.Close()` (final AOF flush) can take longer than 10 seconds. If the timeout fires, the context is cancelled and the engine may not finish flushing. This can result in data loss.
- **No health check before accepting traffic** -- The HTTP server starts listening immediately after creation. If the engine is still loading the AOF or snapshot, requests may fail. There is no "ready" endpoint that waits for full initialization.
- **Flag precedence can be confusing** -- `getEnv()` reads env vars, but `flag.String()` has its own defaults. The env var is only used if the flag is not explicitly passed. This means `KEKTOR_PORT=:8080` is overridden by the flag's default `:9091` unless you also pass `--http-addr`.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Single-file main** | All startup logic in one file | Simplicity; harder to test individual startup phases |
| **`os.Exit(1)` in goroutines** | Crashes terminate process immediately | No graceful cleanup of other goroutines or engine; potential data loss |
| **Embedder type switch is a no-op** | Always creates `NewOllamaEmbedder` regardless of `EmbedderType` | Placeholder for future provider support |
| **MCP mode redirects logs to file** | Hardcoded `kektordb_mcp.log` | Pragmatic choice to avoid corrupting JSON-RPC stream |
| **Dual-mode (HTTP/MCP)** | `--mcp` flag switches between modes | Same binary serves both use cases; no separate MCP binary needed |
| **10-second shutdown timeout** | Context timeout for entire shutdown sequence | Balances between graceful drain and forced termination |
