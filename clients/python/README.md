# KektorDB 

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![GitHub Sponsors](https://img.shields.io/badge/Sponsor-sanonone-pink?logo=github)](https://github.com/sponsors/sanonone)
[![Ko-fi](https://img.shields.io/badge/Support%20me-Ko--fi-orange?logo=ko-fi)](https://ko-fi.com/sanon)
[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

<p align="center">
  <a href="DOCUMENTATION.md">📚 Documentation</a> •
  <a href="CONTRIBUTING.md">🤝 Contributing</a> •
  <a href="docs/guides/zero_code_rag.md">🤖 RAG Open WebUI Guide</a>
</p>

[English](README.md) | [Italiano](README.it.md)

> [!TIP]
> **Docker Support:** Prefer containers? A `Dockerfile` is included in the root for building your own images.

**KektorDB is an embeddable, in-memory Vector + Graph database written in pure Go.**

It fuses a robust **HNSW Engine** with a **lightweight Semantic Graph**, allowing you to combine vector similarity with explicit relationships (like `parent`, `next`, `mentions`) without the complexity and overhead of a full-blown Graph Database.

> *Built with the philosophy of SQLite: Serverless option, zero dependencies, and easy to manage.*

<p align="center">
  <img src="docs/images/kektordb-demo.gif" alt="KektorDB Graph Demo" width="800">
</p>

---

## Why KektorDB?

KektorDB simplifies the stack by unifying the **Database**, the **Search Engine**, and the **AI Middleware** into a single, dependency-free binary.

*   **Database First:** A robust, persistent (AOF+Snapshot) vector store with hybrid search (BM25 + Vector) and metadata filtering.
*   **Embeddable Architecture:** Designed to run alongside your application process (like SQLite) or as a lightweight microservice, removing network overhead.
*   **Lightweight Graph Layer:** Unlike flat vector stores, KektorDB understands connections. It provides a streamlined graph engine optimized for **N-Hop traversal** and **Context Retrieval**, bridging the gap between similarity and structure.

---

## Use Cases

KektorDB is not designed to replace distributed clusters handling billions of vectors. Instead, it shines in specific, high-value scenarios:

### 1. Embedded Search for Go Applications
Ideal for developers building monoliths or microservices who need semantic search without the operational overhead of managing a distributed cluster.
*   **Scenario:** Implementing "Related Products" or "Semantic Search" in a Go backend.
*   **Solution:** `import "github.com/sanonone/kektordb/pkg/engine"` to run the DB in-process.
*   **Benefit:** Zero deployment complexity. The DB scales with your app.

### 2. Local RAG & Knowledge Base
Perfect for desktop applications, local AI agents, or private documentation search where data privacy is paramount.
*   **Scenario:** You need to index sensitive PDF/Markdown/Docx files and chat with them using local LLMs (Ollama).
*   **Solution:** Point KektorDB to your folder. It handles the full ingestion pipeline (OCR, chunking, linking).
*   **Benefit:** Setup time drops from days to minutes. No data leaves your machine.

### 3. AI Gateway & Semantic Cache
Acts as a smart proxy to optimize costs and latency for LLM applications.
*   **Scenario:** You are building a Chatbot using OpenAI/Anthropic APIs.
*   **Solution:** Use KektorDB as a middleware. It caches responses semantically (saving API costs) and acts as a firewall to block malicious prompts before they reach the LLM.
*   **Benefit:** Immediate performance boost and cost reduction with zero code changes in your client.

---

## Zero-Code RAG (Open WebUI Integration)

KektorDB can function as a **smart middleware** between your Chat UI and your LLM. It intercepts requests, performs retrieval, and injects context automatically.

**Architecture:**
`Open WebUI` -> `KektorDB Proxy (9092)` -> `Ollama / LocalAI (11434)`

**How to set it up:**

1.  **Configure `vectorizers.yaml`** to point to your documents and enable Entity Extraction.
2.  **Configure `proxy.yaml`** to point to your Local LLM (Ollama) or OpenAI.
3.  **Run KektorDB** with the proxy enabled:
    ```bash
    ./kektordb -vectorizers-config='vectorizers.yaml' -enable-proxy -proxy-config='proxy.yaml'
    ```
4.  **Configure Open WebUI:**
    *   **Base URL:** `http://localhost:9092/v1`
    *   **API Key:** `kektor` (or any string).
5.  **Chat:** Just ask questions about your documents. KektorDB handles the rest.

👉 **[Read the Full Guide: Building a Fast RAG System with Open WebUI](docs/guides/zero_code_rag.md)**

---

## ✨ Core Features

### ⚡ Performance & Engineering
*   **HNSW Engine:** Custom implementation optimized for high-concurrency reading.
*   **Hybrid Search:** Combines Vector Similarity + BM25 (Keyword) + Metadata Filtering.
*   **Memory Efficiency:** Supports **Int8 Quantization** (75% RAM savings) with zero-shot auto-training and **Float16**.
*   **Maintenance & Optimization:**
    *   **Vacuum:** A background process that cleans up deleted nodes to reclaim memory and repair graph connections.
    *   **Refine:** An ongoing optimization that re-evaluates graph connections to improve search quality (recall) over time. 
*   **AI Gateway & Middleware:** Acts as a smart proxy for OpenAI/Ollama compatible clients. Features **Semantic Caching** to serve instant responses for recurring queries and a **Semantic Firewall** to block malicious prompts based on vector similarity, independently of the RAG pipeline.
*   **Persistence:** Hybrid **AOF + Snapshot** ensures durability.
*   **Observability:** Prometheus metrics (`/metrics`) and structured logging.
*   **Dual Mode:** Run as a standalone **REST Server** or as a **Go Library**.

### 🧠 Agentic RAG Pipeline
*   **Query Rewriting (CQR):** Automatically rewrites user questions based on chat history (e.g., "How to install it?" -> "How to install KektorDB?"). Solves short-term memory issues.
*   **Grounded HyDe:** Generates hypothetical answers to improve recall on vague queries, using real data fragments to ground the hallucination.
*   **Safety Net:** Automatically falls back to standard vector search if the advanced pipeline fails to find relevant context.

### 🕸️ Semantic Graph Engine
*   **Automated Entity Extraction:** Uses a local LLM to identify concepts (People, Projects, Tech) during ingestion and links related documents together ("Connecting the dots").
*   **Graph Traversal:** Search traverses `prev`, `next`, `parent`, and `mentions` links to provide a holistic context window.

<p align="center">
  <img src="docs/images/kektordb-graph-entities.png" alt="Knowledge Graph Visualization" width="700">
  <br>
  <em>Visualizing semantic connections between documents via extracted entities.</em>
</p>

### 🖥️ Embedded Dashboard
Available at `http://localhost:9091/ui/`.
*   **Graph Explorer:** Visualize your knowledge graph with a force-directed layout.
*   **Search Debugger:** Test your queries and see exactly why a document was retrieved.

---

## Installation

### As a Server (Docker)

```bash
docker run -p 9091:9091 -p 9092:9092 -v $(pwd)/data:/data sanonone/kektordb:latest
```

### As a Server (Binary)
Download the pre-compiled binary from the [Releases page](https://github.com/sanonone/kektordb/releases).

```bash
# Linux/macOS
./kektordb
```

> **Compatibility Note:** All development and testing were performed on **Linux (x86_64)**. Pure Go builds are expected to work on Windows/Mac/ARM.

---

## Using as an Embedded Go Library

KektorDB can be imported directly into your Go application, removing the need for external services or containers.

```bash
go get github.com/sanonone/kektordb
```

```go
package main

import (
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func main() {
	// 1. Initialize the Engine (handles persistence automatically)
	opts := engine.DefaultOptions("./kektor_data")
	db, err := engine.Open(opts)
	if err != nil { panic(err) }
	defer db.Close()

	// 2. Create Index
	db.VCreate("products", distance.Cosine, 16, 200, distance.Float32, "english", nil)

	// 3. Add Data
	db.VAdd("products", "p1", []float32{0.1, 0.2}, map[string]any{"category": "electronics"})

	// 4. Search
	results, _ := db.VSearch("products", []float32{0.1, 0.2}, 10, "category=electronics", 100, 0.5)
	fmt.Println("Found IDs:", results)
}
```

---

### 🚀 Quick Start (Python)

This example demonstrates a complete workflow: creating an index, batch-inserting data with metadata, and performing a Hybrid Search (Vector + Keyword).

1.  **Install Client & Utilities:**
    ```bash
    pip install kektordb-client sentence-transformers
    ```

2.  **Run the script:**

    ```python
    from kektordb_client import KektorDBClient
    from sentence_transformers import SentenceTransformer

    # 1. Initialize
    client = KektorDBClient(port=9091)
    model = SentenceTransformer('all-MiniLM-L6-v2') 
    index = "quickstart"

    # 2. Create Index (Hybrid enabled)
    try: client.delete_index(index)
    except: pass
    client.vcreate(index, metric="cosine", text_language="english")

    # 3. Add Data (Batch)
    docs = [
        {"text": "Go is efficient for backend systems.", "type": "code"},
        {"text": "Rust guarantees memory safety.", "type": "code"},
        {"text": "Pizza margherita is classic Italian food.", "type": "food"},
    ]
    
    batch = []
    for i, doc in enumerate(docs):
        batch.append({
            "id": f"doc_{i}",
            "vector": model.encode(doc["text"]).tolist(),
            "metadata": {"content": doc["text"], "category": doc["type"]}
        })
    
    client.vadd_batch(index, batch)
    print(f"Indexed {len(batch)} documents.")

    # 4. Search (Hybrid: Vector + Metadata Filter)
    # Finding "fast programming languages" BUT only in 'code' category
    query_vec = model.encode("fast programming languages").tolist()
    
    results = client.vsearch(
        index,
        query_vector=query_vec,
        k=2,
        filter_str="category='code'", # Metadata Filter
        alpha=0.7 # 70% Vector sim, 30% Keyword rank
    )

    print(f"Top Result ID: {results[0]}")
    ```

---

### 🦜 Integration with LangChain

KektorDB includes a built-in wrapper for **LangChain Python**, allowing you to plug it directly into your existing AI pipelines.

```python
from kektordb_client.langchain import KektorVectorStore
```

---

## Preliminary Benchmarks

Benchmarks were performed on a local Linux machine (Consumer Hardware, Intel i5-12500). The comparison runs against **Qdrant** and **ChromaDB** (via Docker with host networking) to ensure a fair baseline.

> **Disclaimer:** Benchmarking databases is complex. These results reflect a specific scenario (**single-node, read-heavy, Python client**) on my development machine. They are intended to demonstrate KektorDB's capabilities as a high-performance embedded engine, not to claim absolute superiority in distributed production scenarios.

#### 1. NLP Workload (GloVe-100d, Cosine)
*400k vectors, float32 precision.*
KektorDB leverages optimized Go Assembly (Gonum) for Cosine similarity. In this specific setup, it shows very high throughput.

| Database | Recall@10 | **QPS (Queries/sec)** | Indexing Time (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9664 | **1073** | 102.9s |
| Qdrant | 0.9695 | 848 | **32.3s** |
| ChromaDB | 0.9519 | 802 | 51.5s |

#### 2. Computer Vision Workload (SIFT-1M, Euclidean)
*1 Million vectors, float32 precision.*
KektorDB uses a hybrid Go/Rust engine (`-tags rust`) for this test. Despite the CGO overhead for 128d vectors, performance is competitive with native C++/Rust engines.

| Database | Recall@10 | **QPS (Queries/sec)** | Indexing Time (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9906 | **881** | 481.4s |
| Qdrant | 0.998 | 845 | **88.5s** |
| ChromaDB | 0.9956 | 735 | 211.2s |

> *Note on Indexing Speed:* KektorDB is currently slower at ingestion compared to mature engines. This is partly because it builds the full queryable graph immediately upon insertion, but mostly due to the current single-graph architecture. **Optimizing bulk ingestion speed is the top priority for the next major release.**

> [!TIP]
> **Performance Optimization: "Ingest Fast, Refine Later"**
>
> If you need to index large datasets quickly, create the index with a lower `ef_construction` (e.g., 40). This significantly reduces indexing time. 
> You can then enable the **Refine** process in the background with a higher target quality (e.g., 200). KektorDB will progressively optimize the graph connections in the background while remaining available for queries.

#### Memory Efficiency (Compression & Quantization)
KektorDB offers significant memory savings through quantization and compression, allowing you to fit larger datasets into RAM with minimal impact on performance or recall.

| Scenario | Config | Memory Impact | QPS | Recall |
| :--- | :--- | :--- | :--- | :--- |
| **NLP (GloVe-100d)** | Float32 | 100% (Baseline) | ~1073 | 0.9664 |
| | **Int8** | **~25%** | ~858 | 0.905 |
| **Vision (SIFT-1M)** | Float32 | 100% (Baseline) | ~881 | 0.9906 |
| | **Float16** | **~50%** | ~834 | 0.9770 |

*(The "Smart Dispatch" logic in the Rust-accelerated build automatically selects the best implementation—Go, Gonum, or Rust—for each operation based on vector dimensions. The pure Go `float16` and `int8` versions serve as portable fallbacks.)*

[Full Benchmark Report](BENCHMARKS.md)

---

## API Reference (Summary)

For a complete guide to all features and API endpoints, please see the **[Full Documentation](DOCUMENTATION.md)**.

*   `POST /vector/actions/search`: Hybrid vector search.
*   `POST /vector/actions/import`: High-speed bulk loading.
*   `POST /vector/indexes`: Create and manage indexes.
*   `POST /graph/actions/link`: Create semantic relationships.
*   `POST /graph/actions/traverse`: Deep graph traversal (N-Hop) starting from a specific node ID.
*   `POST /rag/retrieve`: Get text chunks for RAG.
*   `GET /system/tasks/{id}`: Monitor long-running tasks.
*   `POST /system/save`: Manual snapshot.

---

## 🛣️ Roadmap

KektorDB is a young project under active development.

### Coming Next (v0.5.0) - The Scalability & Integrity Update
The next major milestone focuses on breaking the RAM limit and improving data consistency guarantees.
*   [ ] **Hybrid Disk Storage:** Implement a pluggable storage engine. Keep the HNSW graph in RAM (or Int8) for speed, but offload full vector data to disk using standard I/O or memory mapping.
*   [ ] **Transactional Graph Integrity:** Introduction of **Atomic Batches** to ensure data consistency when creating bidirectional links or updating vectors (ACID-like behavior for the Graph layer).
*   [ ] **Reverse Indexing:** Automatic management of incoming edges to enable O(1) retrieval of "who points to node X", essential for efficient graph traversal and cleanup.
*   [ ] **Native Backup/Restore:** Simple API to snapshot data to S3/MinIO/Local without stopping the server.

### Planned (Short Term)
Features I intend to build to make KektorDB production-ready and faster.
*   [ ] **Graph Filtering:** Combine vector search with graph topology filters (e.g. "search only children of Doc X"), powered by Roaring Bitmaps.
*   [ ] **Configurable RAG Relations:** Allow users to define custom graph traversal paths in `proxy.yaml` instead of relying on hardcoded defaults.
*   [ ] **SIMD/AVX Optimizations:** Extending pure Go Assembly optimizations (currently used for Cosine) to Euclidean distance and Float16 operations to maximize throughput on modern CPUs.
*   [ ] **Roaring Bitmaps:** Replace current map-based filtering with Roaring Bitmaps for lightning-fast metadata filtering (e.g. `WHERE user_id = X`).
*   [ ] **RBAC & Security:** Implement Role-Based Access Control (Admin vs Read-Only tokens) and finer granularity for multi-tenant apps.
*   [ ] **Official TypeScript Client:** To better serve the JS/Node.js AI ecosystem.

### Future Vision (Long Term)
Features under research. Their implementation depends on real-world adoption, feedback, and available development time.
*   **Property Graphs:** Support for "Rich Edges" with attributes (weights, timestamps) to enable complex recommendation algorithms.
*   **Distributed Replication:** Raft-based consensus for High Availability (Leader-Follower).
*   **Semantic "Gardener":** A background process that uses LLMs to merge duplicate chunks and resolve conflicting information in the Knowledge Graph automatically.

> **Want to influence the roadmap?** [Open an Issue](https://github.com/sanonone/kektordb/issues) or vote on existing ones!

---

## 🛑 Current Limitations (v0.4.0)
*   **Single Node:** KektorDB does not currently support clustering. It scales vertically within the limits of the machine's resources.
*   **RAM Bound:** Until v0.5.0 (Disk Storage), your dataset must fit in RAM.
*   **Beta Software:** While stable for personal use, APIs might evolve.

---

### ⚠️ Project Status & Development Philosophy

While the ambition is high, the development pace depends on available free time and community contributions. The roadmap represents the **vision** for the project, but priorities may shift based on user feedback and stability requirements.

If you like the vision and want to speed up the process, **Pull Requests are highly welcome!**

---

## 🤝 Contributing

**KektorDB is a personal project born from a desire to learn.**

As the sole maintainer, I built this engine to explore CGO, SIMD, and low-level Go optimizations. I am proud of the performance achieved so far, but I know there is always a better way to write code.

If you spot race conditions, missed optimizations, or unidiomatic Go patterns, **please open an Issue or a PR**.

👉 **[Read more](CONTRIBUTING.md)**

---

### License

Licensed under the Apache 2.0 License. See the `LICENSE` file for details.

---

## ☕ Support the Project

If you find this tool useful for your local RAG setup or your Go applications, please consider supporting the development. 

Your support helps me dedicate more time to maintenance, new features, and documentation.

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>

