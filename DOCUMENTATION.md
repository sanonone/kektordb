# KektorDB Documentation (v0.2.2)

## Overview

**KektorDB** is an in-memory vector database built in Go. It is designed to operate as either a standalone server or an embeddable library, aiming to function as **"the SQLite of Vector DBs"**: a self-contained search engine that integrates into applications without requiring complex distributed infrastructure.

It supports approximate nearest neighbor (ANN) searches using the HNSW (Hierarchical Navigable Small World) algorithm, alongside hybrid search capabilities (BM25), metadata filtering, and automatic persistence.

KektorDB is available in two forms:
1.  **Standalone Server:** A binary exposing a REST API over HTTP.
2.  **Go Library:** An embeddable module (`pkg/engine`) for direct use within Go applications.

---

## Architecture

The system is structured in three distinct layers:

1.  **Core (`pkg/core`):** Contains the raw in-memory data structures (HNSW graph, KV store, Inverted Index). It handles data logic but is unaware of the file system or network.
2.  **Engine (`pkg/engine`):** The high-level controller. It manages the Core, handles data persistence (AOF writing/replaying, Snapshots), and coordinates background maintenance tasks. This is the entry point for embedded usage.
3.  **Server (`internal/server`):** An HTTP wrapper around the Engine. It provides the REST API, Vectorizer background workers, and task management.

---

## Key Features

-   **HNSW Search Engine:** An implementation of the HNSW algorithm for ANN searches with configurable graph parameters (`M`, `efConstruction`).
-   **Embedded Mode:** Can be imported directly as a Go library, bypassing network overhead and external process management.
-   **Hybrid Search:** Combines vector similarity scores with keyword relevance (BM25) using a configurable fusion parameter (`alpha`).
-   **Metadata Filtering:** Supports pre-filtering on metadata fields using equality, ranges (e.g., `price < 100`), and boolean logic (`AND`/`OR`).
-   **Distance Metrics:** Supports **Euclidean (L2)** and **Cosine Similarity**. Includes implementations in pure Go (using Gonum) and an optional Rust-accelerated build (via CGO) for SIMD optimization on supported hardware.
-   **Compression & Quantization:** Reduces memory footprint for large datasets:
    -   **Float16:** Reduces Euclidean index size by ~50%.
    -   **Int8:** Symmetric scalar quantization for Cosine indexes (~75% reduction), using pre-computed norms to maintain search speed.
-   **REST API & Task Management:** A JSON-based API supporting batch operations and asynchronous handling for long-running tasks (like index compression or AOF rewriting).
-   **Hybrid Persistence:**
    -   **AOF (Append-Only File):** Logs write operations using a RESP-like protocol for durability.
    -   **Snapshots:** Periodic binary dumps of the in-memory state for faster restarts.
-   **Vectorizer Service:** A background worker that monitors external data sources (e.g., filesystem directories), chunks text, and syncs embeddings using external APIs (like Ollama).

## Installation

### As a Server (Binary)
Download the pre-compiled binary from the [Releases page](https://github.com/sanonone/kektordb/releases).

```bash
# Linux/macOS
./kektordb
```

### As a Library (Go Module)
To use KektorDB inside your own Go application:

```bash
go get github.com/sanonone/kektordb
```

> **Compatibility Note:** All development and testing were performed on **Linux (x86_64)**.
> *   **Pure Go Builds:** Expected to run seamlessly on Windows, macOS (Intel/M1), and ARM, though not manually verified yet.
> *   **Rust-Accelerated Builds:** Leverage CGO and specific SIMD instructions. These builds have currently **only been verified on Linux**.

---

## Using as a Go Library (Embedded)

This is the most efficient way to use KektorDB if you are writing Go software. You bypass HTTP serialization and network latency entirely.

```go
package main

import (
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func main() {
	// 1. Configure the Engine
	// Data will be persisted in the "./data" directory
	opts := engine.DefaultOptions("./data")
	
	// 2. Open the Database
	// This automatically replays the AOF and loads snapshots.
	db, err := engine.Open(opts)
	if err != nil {
		panic(err)
	}
	defer db.Close() // Ensures clean shutdown and final save

	// 3. Create an Index (if it doesn't exist)
	// Metric: Cosine, Precision: Float32, M: 16, efConstruction: 200
	_ = db.VCreate("products", distance.Cosine, 16, 200, distance.Float32, "english")

	// 4. Add Data (Vector + Metadata)
	vector := []float32{0.1, 0.2, 0.3}
	meta := map[string]any{"category": "books", "price": 15.5}
	
	err = db.VAdd("products", "item_1", vector, meta)
	if err != nil {
		fmt.Println("Error adding vector:", err)
	}

	// 5. Search
	// Finds 5 nearest neighbors. Filters for price < 20.
	// efSearch: 100 (higher = more accurate but slower)
	results, _ := db.VSearch("products", vector, 5, "price < 20", 100, 0.5)
	
	fmt.Println("Results:", results)
}
```

---


## Usage Guide

This section walks you through starting, configuring, and using KektorDB, from basic key-value operations to advanced vector searches, vectorizer synchronization, and persistence management.

### Starting and Configuring the Server

Launch the server with custom flags:
```
./kektordb [flags]
```

#### Configuration Flags

| Flag                      | Description                                                                | Default         | Example                                              |
| ------------------------- | -------------------------------------------------------------------------- | --------------- | ---------------------------------------------------- |
| `-http-addr`              | Address and port for the REST API.                                         | `:9091`         | `-http-addr="127.0.0.1:8000"`                        |
| `-aof-path`               | Path to the AOF persistence file (and associated `.kdb` snapshot).         | `kektordb.aof`  | `-aof-path="/data/db/kektordb.aof"`                  |
| `-save`                   | Snapshot policy in `"seconds changes"` format. Multiple policies allowed.  | `"60 1000"`     | `-save="300 10"` (Save after 300s if 10 changes)     |
| `-aof-rewrite-percentage` | Triggers AOF compaction when file grows by this percentage (0 to disable). | `100`           | `-aof-rewrite-percentage=50` (Rewrite at 50% growth) |
| `-vectorizers-config`     | Path to YAML config for the Vectorizer service.                            | `""` (disabled) | `-vectorizers-config="vectorizers.yaml"`             |

Example with custom config:
```
./kektordb -http-addr=":8000" -aof-path="/data/kektordb.aof" -save="300 100" -vectorizers-config="vectorizers.yaml"
```

### Interacting with the REST API

All operations use HTTP requests (e.g., via `curl`, Python/Go clients, or tools like Postman). 
#### Key-Value Operations

A simple persistent store for string data, useful for storing application state or configurations alongside your vectors.

*   **Set Value:** `POST /kv/{key}`
    *   **Body:** `{"value": "your data string"}`
    *   Stores or updates a value.
*   **Get Value:** `GET /kv/{key}`
    *   Returns the stored value for the given key.
*   **Delete Value:** `DELETE /kv/{key}`
    *   Removes the key and its value.

### Vector Index Lifecycle

Manage the containers (indexes) that hold your vector data.

*   **Create Index:** `POST /vector/actions/create`
    *   **Body:**
        ```json
        {
          "index_name": "knowledge-base",
          "metric": "cosine",       // Options: "cosine", "euclidean"
          "precision": "float32",   // Options: "float32", "float16", "int8"
          "text_language": "english", // Options: "english", "italian", or "" (none)
          "m": 16,                  // HNSW max connections (default: 16)
          "ef_construction": 200    // HNSW construction depth (default: 200)
        }
        ```
    *   *Note:* `text_language` is required to enable Hybrid Search (BM25) on this index.
*   **List Indexes:** `GET /vector/indexes`
    *   Returns a summary of all existing indexes.
*   **Get Details:** `GET /vector/indexes/{name}`
    *   Returns configuration and statistics (vector count) for a specific index.
*   **Delete Index:** `DELETE /vector/indexes/{name}`
    *   Permanently removes the index and all associated data.

### Vector Ingestion

KektorDB offers three methods for inserting data, depending on your throughput and consistency needs.

*   **Single Add:** `POST /vector/actions/add`
    *   **Body:**
        ```json
        {
          "index_name": "knowledge-base",
          "id": "doc-123",
          "vector": [0.1, 0.2, 0.3],
          "metadata": {"source": "report.pdf", "page": 5}
        }
        ```
    *   Adds a single vector. Changes are immediately written to the AOF log.
*   **Batch Add (Standard):** `POST /vector/actions/add-batch`
    *   **Body:** `{"index_name": "...", "vectors": [{...}, {...}]}`
    *   **Best for:** Live updates and incremental ingestion.
    *   *Behavior:* Inserts vectors concurrently in memory and appends operations to the AOF log for durability.
*   **Bulk Import (High Speed):** `POST /vector/actions/import`
    *   **Body:** `{"index_name": "...", "vectors": [{...}, {...}]}`
    *   **Best for:** Initial dataset loading or massive restores.
    *   *Behavior:* Optimized for speed. It **bypasses the AOF log** entirely to reduce I/O overhead and forces a full binary **Snapshot** to disk upon completion.
    *   *Warning:* If the server crashes during an import, the data in that specific batch is lost (not logged), but the database remains consistent to the last snapshot.

### Data Retrieval & Deletion

*   **Get Single Vector:** `GET /vector/indexes/{name}/vectors/{id}`
    *   Retrieves the vector and metadata for a specific ID.
*   **Get Multiple Vectors:** `POST /vector/actions/get-vectors`
    *   **Body:** `{"index_name": "...", "ids": ["doc-1", "doc-2"]}`
    *   Retrieves data for multiple IDs in a single request.
*   **Delete Vector:** `POST /vector/actions/delete_vector`
    *   **Body:** `{"index_name": "...", "id": "doc-1"}`
    *   Performs a soft delete. The vector is removed from search results but remains in the graph structure until a rebuild/vacuum (planned feature).

### Search Engine

All search operations use a single unified endpoint that supports vector, text, and hybrid queries.

*   **Execute Search:** `POST /vector/actions/search`
    *   **Body:**
        ```json
        {
          "index_name": "knowledge-base",
          "k": 10,                        // Number of results to return
          "query_vector": [0.1, 0.2, ...], // Required for vector/hybrid search
          "filter": "category='books' AND price < 50", // Optional metadata filter
          "ef_search": 100,               // Optional accuracy tuning (default: 100)
          "alpha": 0.5                    // Optional hybrid fusion weight (default: 0.5)
        }
        ```

#### Search Capabilities:

1.  **Vector Search:** Provide `query_vector`. Finds the nearest neighbors based on the index metric.
2.  **Filtered Search:** Add a `filter` string. Supports operators `=`, `<`, `>`, `<=`, `>=` and logic `AND`, `OR`.
3.  **Hybrid Search:** Combine vector similarity with keyword relevance.
    *   Use `CONTAINS(field, 'text')` in the filter string.
    *   Set `alpha` to control weighting:
        *   `1.0`: Pure Vector search.
        *   `0.0`: Pure Keyword (BM25) search.
        *   `0.5`: Balanced (Reciprocal Rank Fusion / Weighted Sum).
4.  **Text-Only Search:** Provide an empty or zeroed `query_vector` and use `CONTAINS(...)` in the filter.
5.  **Accuracy Tuning:** Increase `ef_search` for higher recall (more accurate) at the cost of latency, or decrease it for faster but approximate results.

#### Compression and Quantization

Reduce memory usage on existing `float32` indexes. Asynchronous operation. Quantization uses a symmetric scalar quantizer trained on data samples, robust to outliers via 99.9th percentile clipping.

1. **Start Compression** (`POST /vector/actions/compress`):
   - For Euclidean: `{"index_name": "my-index", "precision": "float16"}`
   - For Cosine: `{"index_name": "my-index-cos", "precision": "int8"}`
   ```
   curl -X POST -d '{"index_name": "my-index", "precision": "float16"}' http://localhost:9091/vector/actions/compress
   ```
   Returns a `task_id` (202 Accepted).

2. **Check Task Status** (`GET /system/tasks/{task_id}`):
   ```
   curl http://localhost:9091/system/tasks/{task_id}
   ```

Compressed data is not persisted until the next snapshot or AOF rewrite (automatic or manual).

#### Vectorizer: Automatic Synchronization

The Vectorizer service runs background workers to sync indexes with external data sources (e.g., filesystem). Each worker monitors changes, chunks documents, generates embeddings (via embedders like Ollama), and upserts vectors with templated metadata.

1. **Create a Config File** (e.g., `vectorizers.yaml`):
   ```yaml
   vectorizers:
     - name: "project_docs"
       kektor_index: "project_index"
       schedule: "5m"  # Check every 5 minutes (e.g., "30s", "1h")
       source:
         type: "filesystem"
         path: "/path/to/your/documents"
       embedder:
         type: "ollama_api"
         url: "http://localhost:11434/api/embeddings"
         model: "nomic-embed-text"
       document_processor:
         chunking_strategy: "lines"  # or "fixed_size"
         chunk_size: 500
       metadata_template:  # Optional templating
         source_type: "document"
         file_path: "{{file_path}}"
   ```

2. **Start Server with Config**:
   ```
   ./kektordb -vectorizers-config="vectorizers.yaml"
   ```
   Workers start immediately, perform initial sync, and run on schedule. State is tracked in KV store to detect changes.

- **Manage Vectorizers**:
  - List statuses: `GET /system/vectorizers` (returns name, running state, last run, current state).
  - Trigger sync: `POST /system/vectorizers/{name}/trigger` (runs async).

#### Administration and Maintenance

- **Create Snapshot** (`POST /system/save`): Saves state to `.kdb` and truncates AOF.
  ```
  curl -X POST http://localhost:9091/system/save
  ```

- **Compact AOF** (`POST /system/aof-rewrite`): Asynchronous compaction using RESP-like protocol.
  ```
  curl -X POST http://localhost:9091/system/aof-rewrite
  ```

## Persistence

KektorDB uses a hybrid model:
- **AOF (Append-Only File)**: Logs all write operations using a subset of RESP protocol for durability and binary safety. Replayed on startup via a parser that handles commands like `SET`, `VADD`.
- **Snapshots (.kdb)**: Gob-encoded binary dumps for fast restarts, including KV data, vector nodes, and quantizer state. Triggered automatically via `-save` policies or manually.
- **AOF Compaction**: Background rewriting to reduce file size when growth exceeds `-aof-rewrite-percentage`. Uses mutex for safe concurrent writes.

On startup:
1. Load snapshot if available.
2. Replay AOF for any subsequent changes.


## Building from Source

KektorDB uses a `Makefile` for builds, tests, and benchmarks. Supports pure Go (portable) and Rust/CGO (optimized) variants.

### Main Commands

- `make` (or `make all`): Runs `make test` (default).
- `make clean`: Cleans generated files, Rust artifacts, binaries, and Go test cache.

### Pure Go / AVO Commands

- `make generate-avo`: Generates AVO assembly files (run if modifying generators).
- `make test`: Runs unit tests for pure Go version.
- `make bench`: Runs benchmarks for pure Go.

### Optimized (Rust / CGO) Commands

- `make build-rust-native`: Builds Rust library (`libkektordb_compute.a`) for current architecture.
- `make test-rust`: Runs tests with Rust integration (`go test -tags rust`).
- `make bench-rust`: Runs benchmarks with Rust.

### Release Commands

- `make release`: Cross-compiles binaries for Linux, Windows, macOS (amd64/arm64) using Zig. Outputs to `release/`. Requires Rust library built for targets.

## Client Libraries

- **Go Client** (`client.go`): Provides type-safe methods for KV operations, vector management, searches, and admin tasks. Handles errors like `APIError`. Example usage:
  ```go
  client := client.NewClient("http://localhost:9091")
  err := client.VCreate("my-index", "cosine")
  ```

- **Python Client** (`client.py`): Official client with methods mirroring the API, including task waiting. Supports timeouts and custom exceptions. Example usage:
  ```python
  from kektordb_client import KektorDBClient
  client = KektorDBClient("localhost", 9091)
  client.vcreate("my-index", "cosine")
  ```

## Contributing

**KektorDB is a personal project born from a desire to learn the internals of vector databases.**

As the sole maintainer, I built this engine to explore CGO, SIMD, and low-level Go optimizations. I am proud of the performance achieved so far, but I know there is always a better way to write code.

If you spot race conditions, missed optimizations, or unidiomatic Go patterns, **please open an Issue or a PR**. I treat every contribution as a learning opportunity and I am looking for people who want to build this together.

### Areas for Contribution
The project is currently in `v0.2.2`. I would appreciate help with:

1.  **Core Optimization:** Reviewing the HNSW implementation and locking strategies.
2.  **Features:** Implementing Roaring Bitmaps or Graph Healing (see Roadmap).
3.  **Clients:** Making the Python/Go clients more idiomatic.
4.  **Testing:** Adding edge-case tests and fuzzing.

### Development Setup
1.  Fork the repository.
2.  Clone your fork.
3.  Run `make test` to ensure everything is working.
4.  Create a feature branch.
5.  Commit and open a **Pull Request**.


## License

Released under the Apache 2.0 License. See [LICENSE](https://github.com/sanonone/kektordb/blob/main/LICENSE) for details.

For questions, open an issue on [GitHub](https://github.com/sanonone/kektordb).

