# Module: internal/tui

## Purpose

Terminal Dashboard (TUI) for KektorDB. Provides a Bubble Tea v2 interactive terminal interface with live statistics, graph exploration, semantic search, SSE timeline, and settings management. Designed as a developer/operator tool for monitoring and experimenting with a running KektorDB instance.

**Status: Experimental.** Under active development. Known limitations include shutdown delays under heavy pipeline load and incomplete SSE delivery during startup. The Web UI (`/ui/`) remains the stable dashboard option.

## Launch

```bash
./kektordb --tui
```

Use `q` or `Ctrl+C` to exit.

## Tabs

| Tab | Description |
|-----|-------------|
| **Dashboard** | Live stats (vector count, graph edges), Gardener counters (reflections, contradictions, decayed memories), recent SSE events |
| **Graph** | Interactive entity explorer with relation traversal. `f` find entity, `l` list all, `↑↓` navigate, `Enter` expand node, `→` dive |
| **Search** | Vector/hybrid search with advanced controls: K 5-100, alpha 0.0-1.0, SQL-like metadata filter |
| **Timeline** | Real-time SSE event stream with pause/resume and type filter (`vector.add`, `edge.create`, etc.) |
| **Settings** | Embedder mode switcher (auto/ollama/openai/local), Gardener/Server status indicators |

## Architecture

**Model-View-Update (Bubble Tea):** `Model` holds all application state (active tab, search query, graph nodes, SSE stream, settings). `Update()` handles messages (key presses, timer ticks, SSE events). `View()` renders the current tab. The `tui.go` entry point creates the program, connects to the SSE stream, and runs the bubbletea loop.

**Concurrent concerns:** SSE events arrive on a background goroutine and are sent to the model via a channel. Bubble Tea's `tea.Batch` and `tea.Cmd` patterns ensure serialized state updates — no explicit mutexes needed.

**WebKit dependency:** SSE client uses `net/http` for streaming. The reconnect loop uses fixed 2s delay with no backoff — may hammer the server during outages.

## Known Limitations

- **Shutdown lag:** Under heavy pipeline load (e.g., embedding generation), the TUI may hang for several seconds during `Close()`.
- **SSE delivery incomplete:** Events emitted during startup (before TUI connects) are not buffered and are lost.
- **No backoff on reconnect:** SSE reconnect uses fixed 2s delay without exponential backoff.
- **Embedder model download:** Switching to local embedder may trigger a 90MB HuggingFace model download, blocking the UI.
