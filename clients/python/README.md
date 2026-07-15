# KektorDB

*The cognitive memory layer for AI agents.*

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![GitHub Sponsors](https://img.shields.io/badge/Sponsor-sanonone-pink?logo=github)](https://github.com/sponsors/sanonone)
[![Ko-fi](https://img.shields.io/badge/Support%20me-Ko--fi-orange?logo=ko-fi)](https://ko-fi.com/sanon)
[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

<p align="center">
  <a href="DOCUMENTATION.md">Documentation</a> •
  <a href="CONTRIBUTING.md">Contributing</a> •
  <a href="docs/guides/zero_code_rag.md">GraphRAG Guide</a>
</p>

[English](README.md) | [Italiano](README.it.md)

KektorDB is an **AI memory system** - not a database that stores data, but an engine that **understands** what it stores. It combines high-performance vector search (HNSW) with a temporal knowledge graph and a cognitive engine that continuously analyzes your data, detects contradictions, tracks importance, and lets irrelevant information fade naturally.

<p align="center">
  <img src="docs/images/kektordb-demo.gif" alt="KektorDB Demo" width="800">
</p>

**In 30 seconds - give your AI agent persistent memory:**

```bash
# 1. Install and configure (once)
kektordb setup opencode

# 2. Launch the memory server
kektordb --mcp --tools=agent
```

Your agent can now call `save_memory`, `recall_memory`, `start_session` and 46 other tools. Memories persist across sessions, decay naturally when unused, and the cognitive engine detects contradictions and builds user profiles autonomously.

The 49 agent tools cover: **memory CRUD** (`save_memory`, `recall_memory`, `adaptive_retrieve`), **graph operations** (`connect_entities`, `find_path`, `explore_connections`), **session management** (`start_session`, `end_session`), **cognitive features** (`check_subconscious`, `get_user_profile`, `ask_meta_question`), **knowledge compilation** (`request_knowledge` with artifact caching), plus indexing, config, and KV store tools. Run `--tools=all` for the full 57.

---

## What KektorDB Does

### Cognitive & Memory Engine

**Gardener - Self-Managing Memory.** A background process (3 modes: basic, advanced, meta) that continuously analyzes the knowledge graph. It consolidates duplicate memories, detects when new facts contradict established ones, identifies knowledge gaps, and surfaces insights via 11 specialized detectors. Enable with `--cognitive-config cognitive.yaml`.

**Epistemic Engine - Know What You Know.** A three-pillar mathematical framework (Consensus 40%, Stability 30%, Friction 30%) assigns confidence scores to every memory. Identify whether a fact is *crystallized*, *stable*, *volatile*, or *contested*. Query via `POST /vector/actions/belief-assessment`.

**Contradiction Detection - Catch Conflicts Early.** The Gardener uses LLM-based analysis to detect when new information conflicts with established facts. In advanced mode, it proposes resolutions and can auto-resolve minor contradictions. Agents check pending contradictions via `check_subconscious`.

**Semantic Git - Evolve, Don't Overwrite.** Version control for memories. When information changes, `VEvolve` creates a new version linked to the old one via `superseded_by`/`evolves_from` edges. Full history preserved with `_is_historical` markers. Query the state of knowledge at any point in time.

**User Profiling - Know Your Users.** KektorDB autonomously builds and maintains user profiles: communication style, language preferences, expertise areas, dislikes, stated vs observed behavior. Profiles update after a configurable threshold of interactions. Query via `get_user_profile`.

**Knowledge Engine - Pre-Compiled Artifacts.** Compiles structured knowledge artifacts from graph queries using built-in templates (`entity_card`, `topic_overview`, `user_profile`, `timeline`, `session_summary`). Fields are computed deterministically or via LLM. Artifacts are cached (<50ms hit, zero token consumption) and automatically recompiled when source data changes via the integrated Artifact Watcher. Trigger compilation via `request_knowledge` in MCP or `POST /compile` in REST.

**Time-Aware Memory - Decay & Reinforcement.** Unifies short and long-term memory. Nodes lose relevance over time if not accessed, reinforced upon retrieval. Pin core facts to prevent them from ever fading. Configure decay per memory layer (episodic, semantic, procedural) with exponential, linear, step, or Ebbinghaus models.

### Graph & Search

**Knowledge Graph - Relationships as First-Class Citizens.** Weighted property graphs with bidirectional navigation, time travel (query state at any timestamp), N-hop traversal, and bidirectional BFS path finding. Auto-linking rules create connections from metadata fields (e.g., `parent_id` → `child_of`). Graph entities can exist without vectors - represent users, sessions, or abstract concepts.

**Hybrid Search - Vector + Keyword + Graph.** Combines HNSW vector similarity with BM25 keyword matching and metadata filtering via roaring bitmaps. Graph-aware search restricts results to subgraphs reachable from a root node. Adaptive retrieval expands context following semantic neighbors up to a token budget.

### Engineering

**Persistence You Can Trust.** Hybrid AOF + Snapshot architecture. The lazy AOF writer batches operations (10-100x throughput improvement) with periodic fsync. Binary TLVC framing with CRC32 integrity checks. Automatic crash recovery with corruption resync - valid data is preserved even if the AOF file is partially corrupted. Background compaction via AOF rewrite with snapshot mode prevents data loss during maintenance.

**JWT Authentication.** Self-contained ES256 (ECDSA P-256) tokens with role-based access control (admin, write, read) and optional namespace isolation. JWKS endpoint for third-party verification. `jti` denylist for token revocation. No server-side token storage - the token carries its own claims.

**Self-Optimizing Graph.** Background maintenance keeps the index healthy: **Vacuum** reclaims memory from deleted nodes and repairs graph connections. **Refine** continuously re-evaluates graph connections to improve search recall over time -- the longer KektorDB runs, the better your search results become. Both are configurable per-index and run autonomously.

**Run It Your Way.** Standalone REST server, embedded Go library (zero network overhead), MCP server for AI agents, AI Gateway/Proxy for zero-code RAG, or any combination. Python, TypeScript, and Go client SDKs. ONNX embedder built-in (`all-MiniLM-L6-v2`, 384 dim) for zero-config local embeddings. External CLI tools can parse complex documents via the SmartLoader -- configure a command template in `vectorizers.yaml` and KektorDB falls back to built-in parsers on failure.

---

## How to Use KektorDB

| Mode | Command | Best for |
|------|---------|----------|
| **MCP Server** | `kektordb --mcp --tools=agent` | AI agent memory (Claude, Cursor, Codex, Gemini CLI, OpenCode) |
| **REST Server** | `./kektordb` | HTTP API backend, any language |
| **Go Library** | `import "github.com/sanonone/kektordb/pkg/engine"` | Embedded in-process, zero network overhead |
| **AI Gateway** | `./kektordb -enable-proxy -proxy-config=proxy.yaml` | Zero-code RAG between Chat UI and LLM |
| **Python/TS Client** | `pip install kektordb-client` | Application integration |

---

## Quick Start

### Python (REST API)

```python
from kektordb_client import KektorDBClient
from kektordb_client.cognitive import CognitiveSession

client = KektorDBClient(port=9091)
client.vcreate("agent_memory", metric="cosine")

# Save memories linked to a session
with CognitiveSession(client, "agent_memory", user_id="user_42") as session:
    session.save_memory("User is building a Go project called KektorDB",
                        layer="episodic", tags=["project", "go"])
    session.save_memory("User prefers concise answers with examples",
                        layer="semantic", tags=["preference"])

# Search
results = client.vsearch("agent_memory", query_vector=embed("latest project"), k=5)
print(f"Found {len(results)} relevant memories")

# Check what KektorDB knows about this user
profile = client.get_user_profile("user_42", "agent_memory")
print(f"Style: {profile.get('communication_style')}")
```

### Go (Embedded)

```go
import "github.com/sanonone/kektordb/pkg/engine"

db, _ := engine.Open(engine.DefaultOptions("./data"))
defer db.Close()

db.VCreate("docs", distance.Cosine, 16, 200, distance.Float32, "", nil, nil, nil)
db.VAdd("docs", "vec1", []float32{0.1, 0.2, 0.3, 0.4}, map[string]any{"title": "Hello"})

results, _ := db.VSearch("docs", []float32{0.15, 0.25, 0.35, 0.45}, 10, "", "", 100, 1.0, nil)
for _, id := range results {
    data, _ := db.VGet("docs", id)
    fmt.Println(data.ID, data.Metadata["title"])
}
```

### Download & Run

```bash
# Download binary from GitHub Releases
wget https://github.com/sanonone/kektordb/releases/latest/download/kektordb-linux-amd64
chmod +x kektordb-linux-amd64

# Start the server
./kektordb-linux-amd64

# Or use Docker
docker build -t kektordb .
docker run -p 9091:9091 -v $(pwd)/data:/data kektordb
```

---

## Benchmarks

Desktop hardware (Intel i5-12500, consumer SSD). Comparison against Qdrant and ChromaDB via Docker host networking.

| Database | NLP QPS | Vision QPS | Recall@10 |
|----------|---------|------------|-----------|
| **KektorDB** | **1073** | **881** | 0.97 |
| Qdrant | 848 | 845 | 0.97 |
| ChromaDB | 802 | 735 | 0.96 |

> KektorDB is optimized for embedded, single-node scenarios. For billion-scale distributed deployments, consider specialized solutions. [Full report →](BENCHMARKS.md)

---

## Built-in Embedding (Optional)

KektorDB includes an optional built-in ONNX embedder (`all-MiniLM-L6-v2`, 384 dimensions) powered by Rust/Candle for zero-config local embeddings - no Ollama required.

**Build with Rust support:**
```bash
make build-rust-native    # requires protoc (auto-downloaded by Makefile)
make run-rust
```

The ONNX model (~90 MB) is downloaded automatically from HuggingFace on first launch with SHA256 verification.

| Mode | Description |
|------|-------------|
| `auto` | Auto-detect: local ONNX if available, Ollama as fallback (default) |
| `ollama` / `ollama_api` | Use Ollama embedding API |
| `openai` / `openai_compatible` | Use OpenAI-compatible embedding API |
| `gemini` / `google` | Use Gemini `embedContent` API |
| `local` | Built-in ONNX model (requires `-tags rust` build) |

---

## Ecosystem

| Resource | Description |
|----------|-------------|
| [Documentation](DOCUMENTATION.md) | Full technical reference: architecture, API, configuration |
| [Contributing](CONTRIBUTING.md) | Build instructions, code style, PR process |
| [RAG Guide](docs/guides/zero_code_rag.md) | Zero-code RAG with Open WebUI in 5 steps |
| [Go Client](pkg/client/README.md) | Go SDK reference |
| [Python Client](https://pypi.org/project/kektordb-client/) | `pip install kektordb-client` |
| [TypeScript Client](https://www.npmjs.com/package/kektordb-client) | `npm install kektordb-client` |
| [LangChain](https://python.langchain.com/) | `from kektordb_client.langchain import KektorVectorStore` |

---

## Roadmap

### v0.6.0 (current)

- **Engine stability:** 6 P1+P2 bugs fixed - memory-before-AOF reorder, nil-pointer Stat(), AOF corruption recovery, silent data loss on close, taskIDCounter race, async task leak
- **MCP:** 57 tools (49 agent + 8 admin), `memory_instructions` prompt, Gemini API support
- **Auth:** JWT ES256 tokens, RBAC, JWKS endpoint, `jti` denylist
- **Recovery:** AOF resync after corruption, RewriteAOF with snapshot mode

### On the Horizon

- Interactive setup wizard (`kektordb init`) — configure everything in 30 seconds
- Docker Hub + Docker Compose
- Git Sync (push/pull memories across machines)
- SIMD/AVX optimizations for more distance metrics
- Native backup/restore API

> [Open an Issue](https://github.com/sanonone/kektordb/issues) to influence the roadmap.

---

## Contributing

If you spot race conditions, missed optimizations, or unidiomatic Go patterns, **open an Issue or a PR** - all contributions are welcome.

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions and code conventions.

---

## Current Limitations

**Single-node:** KektorDB does not support clustering. It scales vertically within the limits of a single machine.

---

## License

Apache 2.0 - see [LICENSE](LICENSE).

---

## Support

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>
