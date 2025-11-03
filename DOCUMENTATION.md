# KektorDB Documentation (v0.2.0)

## Overview

**KektorDB** is a high-performance, in-memory vector database built from scratch in Go. It was developed as a software engineering deep-dive project with the goal of creating a powerful, simple, and self-contained tool for semantic search. Its philosophy is to be **"the SQLite of Vector DBs"**: a robust search engine that doesn't require complex infrastructure, making it ideal for integration into Go applications, rapid prototyping in Python, or any scenario needing vector search capabilities without the overhead of distributed systems.

KektorDB supports efficient approximate nearest neighbor (ANN) searches using a custom HNSW (Hierarchical Navigable Small World) implementation, advanced metadata filtering, multiple distance metrics, and compression/quantization techniques for memory optimization. It includes a modern REST API (with asynchronous operations), a subset of RESP protocol for AOF persistence, hybrid persistence (AOF + binary snapshots), automatic vectorization from external sources, and official clients for Python and Go.

This documentation serves as both a user guide and a technical reference.

## Key Features

- **Custom HNSW Search Engine**: A performant implementation of the HNSW algorithm for ANN searches, with configurable parameters like `M`, `efSearch` and `efConstruction`.
- **Advanced Metadata Filtering**: Combine vector similarity with complex metadata filters (e.g., equality `tag="cat"`, ranges `price<100`, and logical `AND/OR` combinations) applied via pre-filtering for efficiency.
- **Multiple Distance Metrics**: Supports **Euclidean (L2)** and **Cosine Similarity**, configurable per index. Optimized implementations include pure Go, Gonum (BLAS/SIMD), and Rust/CGO for hardware acceleration.
- **Compression and Quantization**: Optimize memory usage with:
  - **Float16**: Compression for Euclidean indexes (~50% memory savings) using half-precision floats.
  - **Int8**: Symmetric scalar quantization for Cosine indexes (~75% memory savings), robust against outliers via quantile-based training.
- **Modern Asynchronous API**: A clean, JSON-based REST API. Long-running operations (e.g., compression, AOF rewrite) are handled asynchronously with task tracking.
- **Hybrid Persistence**: 
  - **AOF + Snapshots (RDB)**: An Append-Only File (AOF) ensures durability using a RESP-like protocol subset for binary-safe logging. Binary snapshots (`.kdb`) enable near-instant restarts.
  - **Automatic Maintenance**: Background processes handle snapshotting and AOF compaction based on configurable policies.
- **Vectorizer Service**: Background workers for automatic synchronization of embeddings from external sources (e.g., filesystem folders) using embedders like Ollama API. Supports scheduling, chunking strategies, and metadata templating.
- **Hybrid Search (BM25 Integration)**: Combines keyword and semantic relevance with configurable fusion via `alpha`.
- **Ready-to-Use Ecosystem**: Official clients for **Python** and **Go**, with automated binary distribution via GitHub Actions. Build variants include pure Go for portability and Rust/CGO for performance.

## Installation

KektorDB is distributed as a single executable with no dependencies.

1. **Download the Binary**: Visit the [Releases page](https://github.com/sanonone/kektordb/releases) on GitHub and download the archive for your OS (e.g., `kektordb-v0.2.0-linux-amd64.tar.gz`).
2. **Extract the Archive**: Unzip to get the `kektordb` executable.
3. **Run the Server**: Open a terminal and execute:
   ```
   ./kektordb
   ```
   The server will start listening on `http://localhost:9091` by default.


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

- **Set a Value** (`POST /kv/{key}`):
  ```
  curl -X POST -d '{"value": "important data"}' http://localhost:9091/kv/my-key
  ```

- **Get a Value** (`GET /kv/{key}`):
  ```
  curl http://localhost:9091/kv/my-key
  ```

- **Delete a Value** (`DELETE /kv/{key}`):
  ```
  curl -X DELETE http://localhost:9091/kv/my-key
  ```

#### Vector Index Lifecycle

1. **Create an Index** (`POST /vector/actions/create`):
   ```
   curl -X POST -d '{
     "index_name": "knowledge-base",
     "metric": "cosine",
     "text_language": "english"
   }' http://localhost:9091/vector/actions/create
   ```
   - `text_language`: Enables hybrid search (e.g., "english" or "italian").

2. **List Indexes** (`GET /vector/indexes`):
   ```
   curl http://localhost:9091/vector/indexes
   ```

3. **Get Index Details** (`GET /vector/indexes/{name}`):
   ```
   curl http://localhost:9091/vector/indexes/knowledge-base
   ```

4. **Delete an Index** (`DELETE /vector/indexes/{name}`):
   ```
   curl -X DELETE http://localhost:9091/vector/indexes/knowledge-base
   ```

#### Adding and Managing Vectors

- **Add a Vector** (`POST /vector/actions/add`):
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "id": "doc-123",
    "vector": [0.1, 0.2, 0.3],
    "metadata": {"source": "report.pdf", "page": 5}
  }' http://localhost:9091/vector/actions/add
  ```

- **Add in Batch** (`POST /vector/actions/add-batch`):
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "vectors": [
      {"id": "doc-124", "vector": [...], "metadata": {...}},
      {"id": "doc-125", "vector": [...], "metadata": {...}}
    ]
  }' http://localhost:9091/vector/actions/add-batch
  ```

- **Get a Vector** (`GET /vector/indexes/{name}/vectors/{id}`):
  ```
  curl http://localhost:9091/vector/indexes/knowledge-base/vectors/doc-123
  ```

- **Get Vectors in Batch** (`POST /vector/actions/get-vectors`):
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "ids": ["doc-123", "doc-124"]
  }' http://localhost:9091/vector/actions/get-vectors
  ```

- **Delete a Vector** (`POST /vector/actions/delete_vector`):
  ```
  curl -X POST -d '{"index_name": "knowledge-base", "id": "doc-123"}' http://localhost:9091/vector/actions/delete_vector
  ```

#### Performing Searches

Use `POST /vector/actions/search` for all searches.

- **Simple Vector Search**:
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "k": 5,
    "query_vector": [0.15, 0.25, 0.35]
  }' http://localhost:9091/vector/actions/search
  ```

- **Search with Pre-Filtering**:
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "k": 5,
    "query_vector": [0.15, 0.25, 0.35],
    "filter": "source=report.pdf AND page>3"
  }' http://localhost:9091/vector/actions/search
  ```

- **Control Speed vs. Accuracy** (`ef_search`):
  Higher `ef_search` improves recall but slows searches.
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "k": 5,
    "query_vector": [...],
    "ef_search": 200
  }' http://localhost:9091/vector/actions/search
  ```

#### Hybrid Search Guide

Hybrid search combines vector similarity with keyword relevance (BM25). Requires `text_language` on index creation.

- **Hybrid Query**:
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "k": 5,
    "query_vector": [0.15, 0.25, 0.35],
    "filter": "CONTAINS(content, \"database performance\") AND page<10",
    "alpha": 0.7
  }' http://localhost:9091/vector/actions/search
  ```
  - `CONTAINS(field, "text")`: Activates text search on metadata field `content`.
  - `alpha` (0.0 to 1.0): Balances fusion (1.0 = 100% vector, 0.0 = 100% text, 0.5 = balanced).

- **Text-Only Search**: Use an empty/zero query vector.
  ```
  curl -X POST -d '{
    "index_name": "knowledge-base",
    "k": 5,
    "query_vector": [0,0,0],
    "filter": "CONTAINS(content, \"database performance\")"
  }' http://localhost:9091/vector/actions/search
  ```

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

## API Reference

The REST API is organized under `/vector/*` for vector operations and `/system/*` for administrative tasks. All requests/responses are JSON. AOF uses a subset of RESP for binary-safe command logging.

### Vector Endpoints

- `GET /vector/indexes`: List all indexes.
- `POST /vector/actions/create`: Create index (body: `{ "index_name": "...", "metric": "cosine|euclidean", "text_language": "english|italian" }`).
- `GET /vector/indexes/{name}`: Get index details.
- `DELETE /vector/indexes/{name}`: Delete index.
- `POST /vector/actions/add`: Add single vector.
- `POST /vector/actions/add-batch`: Add multiple vectors.
- `POST /vector/actions/search`: Perform search (body includes `query_vector`, `k`, `filter`, `ef_search`, `alpha`).
- `POST /vector/actions/delete_vector`: Delete vector by ID.
- `GET /vector/indexes/{name}/vectors/{id}`: Get single vector.
- `POST /vector/actions/get-vectors`: Get vectors by IDs.
- `POST /vector/actions/compress`: Start compression (async).

### System Endpoints

- `POST /system/save`: Trigger snapshot.
- `POST /system/aof-rewrite`: Trigger AOF compaction (async).
- `GET /system/tasks/{id}`: Check task status.
- `GET /system/vectorizers`: List vectorizer statuses.
- `POST /system/vectorizers/{name}/trigger`: Force vectorizer sync.

### KV Endpoints

- `GET /kv/{key}`: Get value.
- `POST /kv/{key}`: Set value.
- `DELETE /kv/{key}`: Delete key.

## Persistence

KektorDB uses a hybrid model:
- **AOF (Append-Only File)**: Logs all write operations using a subset of RESP protocol for durability and binary safety. Replayed on startup via a parser that handles commands like `SET`, `VADD`.
- **Snapshots (.kdb)**: Gob-encoded binary dumps for fast restarts, including KV data, vector nodes, and quantizer state. Triggered automatically via `-save` policies or manually.
- **AOF Compaction**: Background rewriting to reduce file size when growth exceeds `-aof-rewrite-percentage`. Uses mutex for safe concurrent writes.

On startup:
1. Load snapshot if available.
2. Replay AOF for any subsequent changes.

## Architecture

KektorDB is structured around the `core` package for data logic and `server` for runtime management.

### Core Components

- **KVStore** (`kv.go`): Thread-safe in-memory key-value store using `sync.RWMutex`.
- **VectorIndex Interface** (`vector_index.go`): Defines operations for vector indexes. Includes a basic `BruteForceIndex` for testing.
- **HNSW Index** (`hnsw_index.go`): Implements the HNSW graph for ANN searches. Supports multiple precisions, metrics, and snapshotting.
- **DB Struct** (`core.go`): Orchestrates KV, vector indexes, metadata indexes (inverted, B-Tree, text), filtering, full-text search (BM25), and snapshotting.
- **Distance Calculations** (`distance_go.go`, `distance_rust.go`): Metric and precision-specific functions with smart dispatch (pure Go, Gonum, Rust/CGO). Rust library (`lib.rs`) provides SIMD-optimized implementations for Euclidean (float32/float16) and Cosine (int8) on x86_64/aarch64.
- **Quantizer** (`quantizer.go`): Symmetric scalar quantization for `int8`, trained with outlier-robust quantiles.

### Server Components

- **Server** (`server.go`): Manages startup, persistence (AOF/snapshots), background tasks (saving, AOF rewrite, task manager), and HTTP serving.
- **HTTP Handlers** (`http_handlers.go`): Defines REST endpoints and routing.
- **AOF Parser** (`aof_parser.go`): Parses/formats RESP subset for AOF logging and replay.
- **Vectorizer Service** (`vectorizer_service.go`, `vectorizer.go`, `embedder.go`, `vectorizer_config.go`): Manages background workers for data sync, embedding (e.g., Ollama API), chunking, and upserting.
- **Main Entry** (`main.go`): Parses flags, starts server, handles graceful shutdown.

### Concurrency

- Uses `sync.RWMutex` for read-heavy operations.
- Asynchronous tasks managed via `TaskManager` and goroutines.
- Vectorizer workers use tickers, channels for stopping, and atomic state.

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

### Release Commands (CI/CD)

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

Contributions are welcome! Please:
1. Fork the repo.
2. Create a feature branch.
3. Submit a pull request with clear descriptions and tests.

Follow Go best practices. Run `make test` and `make bench` before submitting.

## License

Released under the Apache 2.0 License. See [LICENSE](https://github.com/sanonone/kektordb/blob/main/LICENSE) for details.

For questions, open an issue on [GitHub](https://github.com/sanonone/kektordb).

