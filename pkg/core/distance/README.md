# Module: pkg/core/distance

## Purpose

Provides hardware-accelerated vector distance computation with a build-tag gated dual implementation: pure Go + Gonum BLAS for the default build, and Rust SIMD (AVX2/FMA/F16C on x86_64, NEON on aarch64) when compiled with `-tags rust`. This module is the computational backbone for all HNSW nearest-neighbor searches in KektorDB.

## Key Types & Critical Paths

**Critical types:**
- `DistanceFuncF32/F16/I8` -- Function type aliases for distance computation per precision. Captured as closures at query start for devirtualization.
- `Quantizer` -- `{mu sync.RWMutex, AbsMax float32}` -- Symmetric scalar quantizer for int8 precision. Trains on 99.9th percentile.
- `DistanceMetric` -- Enum: `"euclidean"`, `"cosine"`.
- `PrecisionType` -- Enum: `"float32"`, `"float16"`, `"int8"`.

**Critical paths (hot functions):**
- `SquaredEuclideanF32()` -- Dispatches to Rust SIMD (>=128 dims) or pure Go. Called millions of times per search.
- `CosineDistanceI8()` -- Same dispatch pattern. Used after online quantization from float32.
- `CosineDistanceF32()` -- Always uses Gonum BLAS. Never routes to Rust.
- `Quantizer.Train()` -- Stride-based sampling for large datasets (capped at 25K vectors). Acquires write lock once.

## Architecture & Data Flow

Distance functions are dispatched through a smart threshold system. For float32 Euclidean and int8 Cosine, vectors with length >= 128 are routed to the Rust SIMD library via CGO; shorter vectors use pure Go loops. This 128-element threshold was determined empirically -- below it, the CGO call overhead exceeds the SIMD speedup. Float32 Cosine always uses Gonum BLAS regardless of length, as Gonum's internal SIMD is already optimal for dot-product-heavy operations. Float16 Euclidean always uses Rust since Go has no SIMD support for f16.

The Rust side (`native/compute/`) performs runtime CPU feature detection on every call (`is_x86_feature_detected!("fma")`, etc.), selecting the appropriate SIMD implementation (AVX2+FMA, F16C, NEON) or falling back to scalar loops. All SIMD functions use unaligned loads (`_mm256_loadu_ps`) to avoid branch mispredictions from alignment checks.

The `Quantizer` supports symmetric scalar quantization for int8 precision. It trains on the 99.9th percentile of absolute values (not the absolute max) for outlier robustness. For datasets >10,000 vectors, it uses stride-based deterministic sampling capped at 25,000 vectors -- cache-friendly traversal with zero lock contention during training.

## Cross-Module Dependencies

**Depends on:**
- `gonum.org/v1/gonum/blas` -- BLAS routines for float32 cosine distance.
- `native/compute` (via CGO, `//go:build rust`) -- Rust SIMD library for Euclidean F32, Euclidean F16, and Dot Product I8.

**Used by:**
- `pkg/core` (HNSW index) -- Primary consumer. Distance functions are captured as closures at search start and called in the innermost loop of `searchLayerUnlocked()`.
- `pkg/engine` -- Indirectly via core's search operations.
- `pkg/proxy` -- Indirectly via engine's vector search for RAG and cache lookups.

## Concurrency & Locking Rules

**Quantizer uses `sync.RWMutex`:** The `AbsMax` field is protected by `mu sync.RWMutex`. Training acquires a write lock; distance computation acquires a read lock. The quantizer is designed for read-heavy workloads -- once trained, thousands of concurrent distance computations proceed under `RLock`.

**Pure Go distance functions are stateless:** All pure Go implementations (`distance_go.go`) have no mutable state and are trivially goroutine-safe. Multiple goroutines can call `SquaredEuclideanF32` or `CosineDistanceI8` simultaneously with zero contention.

**Rust SIMD functions are stateless:** The `#[unsafe(no_mangle)]` extern "C" functions in `lib.rs` take pointers and lengths, perform computation, and return a scalar. No internal state, no mutexes, fully reentrant.

**`panic = "abort"` in Rust:** The Rust crate is compiled with `panic = "abort"` to prevent unwinding across the FFI boundary. A Rust panic unwinding into Go would cause undefined behavior; aborting is the correct safety mechanism.

## Known Pitfalls / Gotchas

- **Do NOT add CGO calls inside the search loop** -- The CGO dispatch threshold (128 dims) exists because FFI overhead dominates for short vectors. If you add a new distance function, benchmark it at multiple dimensions before deciding the dispatch strategy.
- **Quantizer `Train()` is not idempotent** -- Calling `Train()` multiple times with different data overwrites `AbsMax`. Train once on representative data, then use read-only for distance computation.
- **Gonum BLAS requires contiguous memory** -- The float32 slice passed to `CosineDistanceF32` must be a contiguous `[]float32`. If you slice a larger array, ensure it's not a strided view.
- **Build tag mismatch** -- `distance_rust.go` requires `-tags rust` AND the Rust static library to be built first (`cd native/compute && cargo build --release`). Without it, the build falls back to `distance_go.go` silently.
- **F16 on ARM falls back to scalar** -- No NEON F16 implementation exists yet. On Apple Silicon or ARM servers, float16 Euclidean distance is significantly slower than on x86_64.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **CGO dispatch threshold at 128** | Vectors < 128 dims use pure Go; >= 128 use Rust SIMD | CGO call overhead (~50-100ns) dominates computation for short vectors; SIMD wins only when computation time exceeds FFI overhead |
| **Gonum BLAS for float32 cosine** | Never uses Rust for f32 cosine | Gonum's BLAS implementation already uses platform-native SIMD; adding a CGO layer would be pure overhead |
| **Runtime feature detection per call** | Checks CPU features on every invocation, not once at startup | Guarantees correctness if the binary is migrated between machines with different CPU capabilities; the check is a single atomic load, negligible overhead |
| **Unaligned loads everywhere** | `_mm256_loadu_ps` instead of aligned `_mm256_load_ps` | Avoids branch mispredictions from alignment checks; on modern CPUs (post-Sandy Bridge), unaligned loads are nearly as fast as aligned when data happens to be aligned |
| **No F16 NEON implementation** | Float16 Euclidean falls back to scalar on AArch64 | Stable F16 NEON intrinsics are not yet available; this is a known performance gap on Apple Silicon and ARM servers |
| **Build-tag gated dual implementation** | `distance_rust.go` (+tags rust) vs `distance_go.go` (!tags rust) | Zero-dependency default build; users who want maximum performance can compile with Rust SIMD |
