# Detailed Performance Benchmarks

This document contains the raw results of performance tests conducted on **KektorDB v0.3.0**.

### Methodology
*   **Hardware:** Intel Core i5-12500 (Consumer Desktop), Local SSD.
*   **Environment:** Linux, Docker (host networking used for competitors to minimize latency overhead).
*   **Client:** Official Python clients for all databases, single-threaded execution.
*   **Metrics:**
    *   **Recall@10:** Accuracy compared to Brute Force (Numpy).
    *   **QPS:** Queries Per Second (sequential latency measurement).
    *   **Index Time:** Wall-clock time to ingest and build the index.

> **Note:** KektorDB is currently slower at ingestion compared to mature engines. This is partly because it builds the full queryable graph immediately upon insertion, but mostly due to the current single-graph architecture. **Optimizing bulk ingestion speed is the top priority for the next major release.**

---

## 1. NLP Workload (GloVe)
*Metric: Cosine Similarity*

### GloVe-100d (400k vectors)

| DB | M | efC | efS | Recall | QPS | Index(s) |
|:---|--:|----:|----:|-------:|----:|---------:|
| **KektorDB** | 16 | 200 | 100 | 0.9664 | **1073** | 102.9 |
| Qdrant | 16 | 200 | 100 | 0.9695 | 848 | 32.3 |
| ChromaDB | 16 | 200 | 100 | 0.9519 | 802 | 51.5 |
| **KektorDB** | 12 | 150 | 50 | 0.9123 | **1400** | 73.6 |
| Qdrant | 12 | 150 | 50 | 0.9139 | 921 | 26.7 |
| ChromaDB | 12 | 150 | 50 | 0.8775 | 866 | 42.9 |
| **KektorDB** | 16 | 200 | 20 | 0.8753 | **1563** | 102.9 |
| Qdrant | 16 | 200 | 20 | 0.8684 | 1000 | 31.1 |
| ChromaDB | 16 | 200 | 20 | 0.8414 | 874 | 52.5 |

### GloVe-200d (200k vectors)
Scaling up dimensionality.

| DB | M | efC | efS | Recall | QPS | Index(s) |
|:---|--:|----:|----:|-------:|----:|---------:|
| **KektorDB** | 16 | 200 | 100 | 0.9780 | **701** | 96.2 |
| Qdrant | 16 | 200 | 100 | 0.9823 | 668 | 26.1 |
| ChromaDB | 16 | 200 | 100 | 0.9719 | 696 | 34.6 |

### GloVe-300d (200k vectors)

| DB | M | efC | efS | Recall | QPS | Index(s) |
|:---|--:|----:|----:|-------:|----:|---------:|
| **KektorDB** | 16 | 200 | 100 | 0.9569 | 586 | 130.2 |
| Qdrant | 16 | 200 | 100 | 0.9509 | 557 | 40.7 |
| ChromaDB | 16 | 200 | 100 | 0.9464 | **683** | 46.9 |

---

## 2. Computer Vision Workload (SIFT)
*Metric: Euclidean Distance (L2)*

SIFT-1M (128d) 

| DB | M | efC | efS | Recall | QPS | Index(s) |
|:---|--:|----:|----:|-------:|----:|---------:|
| **KektorDB** | 16 | 200 | 100 | 0.9906 | **881** | 481.4 |
| Qdrant | 16 | 200 | 100 | 0.9981 | 852 | 88.9 |
| ChromaDB | 16 | 200 | 100 | 0.9939 | 752 | 210.6 |
| **KektorDB** | 12 | 150 | 50 | 0.9607 | **1262** | 324.3 |
| Qdrant | 12 | 150 | 50 | 0.988 | 966 | 72.8 |
| ChromaDB | 12 | 150 | 50 | 0.9779 | 831 | 171.9 |

*(Note: SIFT indexing time includes the overhead of CGO calls during graph construction.)*

---

## 3. Configuration Impact
How changing parameters affects KektorDB (GloVe-100d).

| Config Strategy | Params (M, efC, efS) | QPS | Recall | Notes |
| :--- | :--- | :--- | :--- | :--- |
| **Balanced** | 16, 200, 100 | 1073 | 0.9664 | Recommended default. |
| **High Accuracy** | 32, 400, 200 | 603 | 0.9977 | Near-perfect recall, 50% slower. |
| **High Speed** | 16, 200, 20 | 1563 | 0.8753 | Ultra-fast, for approximate needs. |
