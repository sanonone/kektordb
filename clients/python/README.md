# KektorDB 

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![GitHub Sponsors](https://img.shields.io/badge/Sponsor-sanonone-pink?logo=github)](https://github.com/sponsors/sanonone)
[![Ko-fi](https://img.shields.io/badge/Support%20me-Ko--fi-orange?logo=ko-fi)](https://ko-fi.com/sanon)
[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

[English](README.md) | [Italiano](README.it.md)

> [!TIP]
> **Docker Support:** Prefer containers? A `Dockerfile` is included in the root for building your own images.

**KektorDB is an embeddable, in-memory vector database designed for simplicity and speed.**

It bridges the gap between raw HNSW libraries and complex distributed databases. Written in pure Go, it runs as a single binary or embeds directly into your application, providing a complete **RAG Pipeline**, **Hybrid Search**, and **Persistence** out of the box.

> *Built with the philosophy of SQLite: Serverless option, zero dependencies, and easy to manage.*

---

## Why KektorDB?

Most vector databases require Docker containers, external dependencies, or Python pipelines just to index a few documents. KektorDB takes a different approach: **"Batteries Included"**.

*   **Zero Setup:** No Python scripts required. Point KektorDB to some folders of PDFs/Markdown/Code/Text files, and it handles chunking, embedding, and indexing automatically.
*   **Embeddable:** Building a Go app? Import `pkg/engine` and run the database inside your process. No network overhead.
*   **AI Gateway:** Acts as a transparent proxy for OpenAI/Ollama clients (like **Open WebUI**). It automatically injects RAG context and caches responses without you writing a single line of code.

---

## Use Cases

KektorDB is not designed to replace distributed clusters handling billions of vectors. Instead, it shines in specific, high-value scenarios:

### 1. Local RAG & Knowledge Base
Perfect for desktop applications or local AI agents.
*   **Scenario:** You have a some folders of documentation (`.md`, `.pdf`).
*   **Solution:** KektorDB watches the folder, syncs changes instantly, and provides a search API with automatic context window (prev/next chunks).
*   **Benefit:** Setup time drops from hours to minutes.

### 2. Embedded Go Search
Ideal for Go developers building monoliths or microservices.
*   **Scenario:** You need to add semantic search to your Go backend (e.g., "Find similar products").
*   **Solution:** `import "github.com/sanonone/kektordb/pkg/engine"`.
*   **Benefit:** Zero deployment complexity. The DB lives and dies with your app process.

### 3. "Drop-in" RAG Backend
Connect your favorite AI interface directly to your data.
*   **Scenario:** You want to chat with your local documents using a UI like **Open WebUI**, but don't want to set up complex pipelines.
*   **Solution:** Configure your UI to use `http://localhost:9092/v1` (KektorDB Proxy) instead of the LLM directly.
*   **Benefit:** KektorDB intercepts the chat, injects relevant context from your files into the prompt, and forwards it to the LLM.

---

## Zero-Code RAG (Open WebUI Integration)

KektorDB can function as a **smart middleware** between your Chat UI and your LLM. It intercepts requests, performs retrieval, and injects context automatically.

**Architecture:**
`Open WebUI` -> `KektorDB Proxy (9092)` -> `Ollama / LocalAI (11434)`

**How to set it up:**

1.  **Run KektorDB** with the proxy enabled:
    ```bash
    ./kektordb -vectorizers-config vectorizers.yaml -enable-proxy -proxy-target "http://localhost:11434"
    ```
2.  **Configure Open WebUI:**
    *   **Base URL:** `http://localhost:9092/v1`
    *   **API Key:** `kektor` (or any string).
3.  **Chat:** Just ask questions about your documents. KektorDB handles the rest.

👉 **[Read the Full Guide: Building a Fast RAG System with Open WebUI](docs/guides/zero_code_rag.md)**

---

## ✨ Core Features

*   **HNSW Engine:** Custom implementation optimized for high-concurrency reading.
*   **Hybrid Search:** Combines Vector Similarity + BM25 (Keyword) + Metadata Filtering.
*   **Graph Engine:** Supports arbitrary semantic links between vectors. The automated RAG pipeline leverages this to link sequential chunks (`prev`/`next`) for context window retrieval, but custom relationships can be defined via API.
*   **Memory Efficiency:** Supports **Int8 Quantization** (75% RAM savings) and **Float16**.
*   **Automated Ingestion:** Built-in pipelines for PDF, Text, and Code files.
*   **Persistence:** A hybrid **AOF + Snapshot** system ensures durability across restarts. 
*   **Maintenance & Optimization:**
    *   **Vacuum:** A background process that cleans up deleted nodes to reclaim memory and repair graph connections.
    *   **Refine:** An ongoing optimization that re-evaluates graph connections to improve search quality (recall) over time. 
*   **AI Gateway & Middleware:** Acts as a smart proxy for OpenAI/Ollama compatible clients. Features **Semantic Caching** to serve instant responses for recurring queries and a **Semantic Firewall** to block malicious prompts based on vector similarity, independently of the RAG pipeline.
*   **Dual Mode:** Run as a standalone **REST Server** or as a **Go Library**.

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

## Installation

### As a Server (Docker)
The easiest way to run KektorDB.

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

## API Reference (Summary)

For a complete guide to all features and API endpoints, please see the **[Full Documentation](DOCUMENTATION.md)**.

*   `POST /vector/actions/search`: Hybrid vector search.
*   `POST /vector/actions/import`: High-speed bulk loading.
*   `POST /vector/indexes`: Create and manage indexes.
*   `POST /graph/actions/link`: Create semantic relationships.
*   `POST /rag/retrieve`: Get text chunks for RAG.
*   `GET /system/tasks/{id}`: Monitor long-running tasks.
*   `POST /system/save`: Manual snapshot.

---

## 🛣️ Roadmap

KektorDB is a young project under active development.

*   **Near-Term:** Roaring Bitmaps for filtering, Native Snapshotting, Concurrency Polish.
*   **Long-Term:** Disk-Based Indexes, Replication.

---

## 🤝 Contributing

**KektorDB is a personal project born from a desire to learn.**

As the sole maintainer, I built this engine to explore CGO, SIMD, and low-level Go optimizations. I am proud of the performance achieved so far, but I know there is always a better way to write code.

If you spot race conditions, missed optimizations, or unidiomatic Go patterns, **please open an Issue or a PR**.

---

### License

Licensed under the Apache 2.0 License. See the `LICENSE` file for details.

---

## ☕ Support the Project

KektorDB is an open-source project developed by a single maintainer. 
If you find this tool useful for your local RAG setup or your Go applications, please consider supporting the development. 

Your support helps me dedicate more time to maintenance, new features, and documentation.

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>

