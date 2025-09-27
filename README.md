# KektorDB 🚀

[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

KektorDB is an in-memory vector database built from scratch in Go. It provides an HNSW-based engine for approximate nearest neighbor search, advanced metadata filtering, and modern access via a REST API and official clients.

---

### Motivation and Philosophy

This project began with a primary motivation: **to learn**. It's a personal journey to dive deep into complex software engineering topics, from low-level performance optimization with SIMD to the architecture of database systems.

Throughout its development, **I have tried to apply the rigor and seriousness of a professional project**, but it's important to note that this is a learning endeavor. The result is not intended to be a definitive, production-ready database, but rather a reflection of this process. I hope the design choices and the code can be useful or interesting to other developers exploring similar topics.

Feedback and suggestions from fellow learners and experienced engineers are highly encouraged and welcome.

---

### ✨ Core Features

*   **Custom HNSW Search Engine:** A from-scratch implementation of the HNSW algorithm for approximate nearest neighbor (ANN) search, providing full control over its behavior and performance.
*   **Advanced Metadata Filtering:**
    *   **High-Performance Pre-filtering:** Filters are applied *before* the HNSW search to drastically reduce the search space.
    *   Supports equality (`tag="cat"`), range (`price<100`), and compound (`AND`/`OR`) filters.
    *   Powered by optimized secondary indexes (Inverted Index for strings, B-Tree for numerics).
*   **Multiple Distance Metrics:**
    *   **Euclidean (L2) Distance:** Ideal for data like image embeddings.
    *   **Cosine Similarity:** The standard for text-based semantic search. Vectors are automatically normalized by the server for optimal performance and accuracy.
*   **Vector Compression & Quantization:**
    *   **Float16 Compression:** Compresses Euclidean indexes to reduce memory usage by **50%** while maintaining high precision.
    *   **Int8 Quantization:** Quantizes Cosine indexes to reduce memory usage by **75%** with minimal recall loss.
*   **Reliable Persistence:**
    *   **Append-Only File (AOF):** All write operations are logged to disk for durability.
    *   **AOF Compaction:** The server features a restart-based compaction mechanism to manage log file size and recover memory from deleted items.
*   **Modern & Simple API:**
    *   A clean, consistent **REST API** using JSON.
    *   Official **Python and Go clients** for immediate, idiomatic integration.
*   **Configurable:** Server behavior (ports, AOF path) and index parameters (`M`, `efConstruction`, `metric`, `precision`) are all configurable.

---

### Performance Benchmarks

Performance is a key goal. The following benchmarks were run on a `12th Gen Intel(R) Core(TM) i5-12500` for vectors of 128 dimensions.

| Operation                        | Implementation        | Time per Operation |
|----------------------------------|-----------------------|--------------------|
| **Euclidean Distance (`float32`)** | Pure Go (Compiler Opt.) | `~27 ns/op`        |
| **Cosine Similarity (`float32`)**  | `gonum` (SIMD)        | `~10 ns/op`        |
| **Euclidean Distance (`float16`)** | Pure Go (Fallback)    | `~290 ns/op`       |
| **Cosine Similarity (`int8`)**     | Pure Go (Fallback)    | `~27 ns/op`        |

*(Note: The performance for `float32` Cosine Similarity is significantly enhanced by leveraging the SIMD-accelerated `gonum/blas` library. The `float16` and `int8` distance functions currently use pure Go fallbacks and are a primary area for future optimization.)*

---

### 🚀 Quick Start (with the Python Client)

1.  **Run the KektorDB Server:**
    ```bash
    # Download the latest binary from the Releases page
    ./kektordb-linux-amd64 -http-addr=":9091"
    ```

2.  **Install the Python Client:**
    ```bash
    pip install kektordb-client
    ```

3.  **Use KektorDB in your Python script:**
    ```python
    from kektordb_client import KektorDBClient

    client = KektorDBClient(port=9091)

    index_name = "knowledge_base"
    client.vcreate(index_name, metric="cosine", precision="int8")

    # In a real app, you would generate this vector with a model like SentenceTransformer
    embedding = [0.1, 0.8, 0.3] 
    client.vadd(
        index_name=index_name,
        item_id="doc1",
        vector=embedding,
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

### 🛣️ Roadmap & Current Limitations

KektorDB is under active development. The current version is a stable baseline, but there are clear areas for improvement that reflect its nature as an evolving project:

*   **Compute Performance:** The current pure Go fallbacks for `float16` and `int8` distance calculations are functional but not yet optimized. A high priority for the next development cycle is to explore high-performance implementations.
*   **Live AOF Compaction:** Compaction currently occurs on restart or via manual command. A background process for live compaction is a potential long-term goal.
*   **Snapshotting (RDB):** For very large datasets, an RDB snapshotting mechanism could be introduced in the future to ensure near-instantaneous restarts.
*   **Hybrid Search:** Integration of a full-text search index (e.g., BM25) is a planned area of exploration to allow for powerful hybrid queries.

---

### License

This project is licensed under the Apache 2.0 License. See the `LICENSE` file for details.
