# KektorDB

[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

English | [Italiano](README.it.md)

KektorDB is a high-performance, in-memory vector database built from scratch in Go. It provides an HNSW-based engine for approximate nearest neighbor search, advanced metadata filtering, and modern access via a REST API.

### Motivation and Philosophy

This project began as a personal learning endeavor to dive deep into complex software engineering topics, from low-level performance optimization to database architecture. While the primary goal was learning, the project has been developed with a focus on creating a robust and useful tool.

The result is not intended to be a definitive, production-ready database competitor, but rather a reflection of this engineering process. The mission is to provide a powerful, simple, and self-contained vector search engine, embodying the "SQLite of Vector DBs" philosophy.

---

### Core Features

*   **Custom HNSW Engine:** A from-scratch implementation of the HNSW algorithm for high-recall ANN search, featuring an advanced neighbor selection heuristic.
*   **Advanced Metadata Filtering:** High-performance pre-filtering on metadata. Supports equality (`tag="cat"`), range (`price<100`), and compound (`AND`/`OR`) filters.
*   **Multiple Distance Metrics:** Supports both **Euclidean (L2)** and **Cosine Similarity**, configurable per index.
*   **Vector Compression & Quantization:**
    *   **Float16:** Compresses Euclidean indexes by **50%**, maintaining >99% recall.
    *   **Int8:** Quantizes Cosine indexes by **75%**, maintaining >93% recall on large datasets.
*   **High-Performance API:** A clean REST API with batch operations and dynamic search tuning (`ef_search`).
*   **Reliable Persistence:** A hybrid **AOF + Snapshot** system ensures durability and provides near-instantaneous restarts. Features automatic, configurable background maintenance for snapshots and AOF compaction.
*   **Ecosystem:** Official clients for **Python** and **Go** for easy integration.

---

### Performance Benchmarks

Benchmarks were performed on a `12th Gen Intel(R) Core(TM) i5-12500` CPU.

#### End-to-End Search Performance

These benchmarks measure the complete system performance, including API overhead, on real-world datasets.

| Dataset / Configuration                | Vectors     | Dimensions | Recall@10 | QPS (Queries/sec) |
|----------------------------------------|-------------|------------|-----------|-------------------|
| SIFT / Euclidean `float32`             | 1,000,000   | 128        | **0.9960**  | `~344`            |
| SIFT / Euclidean `float16` (Compressed)  | 1,000,000   | 128        | **0.9910**  | `~266`            |
| GloVe / Cosine `float32`               | 400,000     | 100        | **0.9650**  | `~279`            |
| GloVe / Cosine `int8` (Quantized)      | 400,000     | 100        | **0.9330**  | `~147`            |

*Parameters: `M=16`, `efConstruction=200`, `efSearch=100` (`efSearch=200` for `int8`).*

#### Low-Level Distance Calculation Performance

These benchmarks measure the speed of the core distance functions for vectors of 128 dimensions.

| Function                          | Implementation         | Time per Operation |
|-----------------------------------|------------------------|--------------------|
| **Euclidean (`float32`)**         | Pure Go (Compiler Opt.)| `~27 ns/op`        |
| **Cosine (`float32`)**            | `gonum` (SIMD)         | `~10 ns/op`        |
| **Euclidean (`float16`)**         | Pure Go (Fallback)     | `~320 ns/op`       |
| **Euclidean (`float16`)**         | Avo     (SIMD)         | `~118 ns/op`       |
| **Cosine (`int8`)**               | Pure Go (Fallback)     | `~27 ns/op`        |

*(Note: The performance for compressed/quantized searches (`float16`/`int8`) is currently limited by the pure Go fallback for distance calculations. A key area for future improvement is replacing these with SIMD-accelerated versions.)*

---

### 🚀 Quick Start (Python)

1.  **Run the KektorDB Server:**
    ```bash
    # Download the latest binary from the Releases page
    ./kektordb -http-addr=":9091"
    ```

2.  **Install the Python Client:**
    ```bash
    pip install kektordb-client
    ```

3.  **Use KektorDB:**
    ```python
    from kektordb_client import KektorDBClient

    client = KektorDBClient(port=9091)
    index_name = "knowledge_base"
    
    client.vcreate(index_name, metric="cosine", precision="int8")

    client.vadd(
        index_name=index_name,
        item_id="doc1",
        vector=[0.1, 0.8, 0.3],
        metadata={"source": "manual_v1.pdf", "page": 42}
    )

    results = client.vsearch(
        index_name=index_name,
        k=1,
        query_vector=[0.15, 0.75, 0.35],
        filter_str='source=manuale_v1.pdf AND page>40'
    )

    print(f"Found results: {results}")
    ```

---

### 📚 API Reference

#### Key-Value Store
- `GET /kv/{key}`: Retrieves a value.
- `POST /kv/{key}`: Sets a value. Body: `{"value": "..."}`.
- `DELETE /kv/{key}`: Deletes a key.

#### Index Management
- `GET /vector/indexes`: Lists all indexes.
- `GET /vector/indexes/{name}`: Gets detailed info for a single index.
- `DELETE /vector/indexes/{name}`: Deletes an index.

#### Vector Actions
- `POST /vector/actions/create`: Creates a new vector index.
  - Body: `{"index_name": "...", "metric": "...", "precision": "...", "m": ..., "ef_construction": ...}`
- `POST /vector/actions/add`: Adds a single vector.
  - Body: `{"index_name": "...", "id": "...", "vector": [...], "metadata": {...}}`
- `POST /vector/actions/add-batch`: Adds multiple vectors.
  - Body: `{"index_name": "...", "vectors": [{"id": ..., "vector": ...}, ...]}`
- `POST /vector/actions/search`: Performs a vector search.
  - Body: `{"index_name": "...", "k": ..., "query_vector": [...], "filter": "...", "ef_search": ...}`
- `POST /vector/actions/delete_vector`: Deletes a single vector.
  - Body: `{"index_name": "...", "id": "..."}`
- `POST /vector/actions/get-vectors`: Retrieves data for multiple vectors by ID.
  - Body: `{"index_name": "...", "ids": ["...", "..."]}`
- `POST /vector/actions/compress`: Compresses an index to a lower precision.
  - Body: `{"index_name": "...", "precision": "..."}`

#### System
- `POST /system/save`: Triggers a database snapshot.
- `POST /system/aof-rewrite`: Triggers an AOF compaction.
- `GET /system/tasks/{id}`: Gets the status of an asynchronous task.
- `GET /debug/pprof/*`: Exposes Go pprof profiling endpoints.

---

### 🛣️ Roadmap & Future Work

KektorDB is an ongoing project. The next steps will focus on:

-   **Hybrid Search (BM25):** Integrating a full-text search index for powerful queries that combine keyword and semantic relevance.
-   **Performance Optimizations:** Replace the pure Go fallbacks for `float16` and `int8` distance calculations with high-performance SIMD implementations (likely via `avo`).
-   **Mobile/Edge Deployment:** Exploring a build mode that compiles KektorDB as a shared library for integration with mobile frameworks like Flutter.

---

### License

Licensed under the Apache 2.0 License. See the `LICENSE` file for details.
