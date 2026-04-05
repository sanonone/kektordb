# Module: native/compute

## Purpose

A Rust static library (`libkektordb_compute.a`) providing hardware-accelerated vector distance functions consumed by Go via CGO. Implements architecture-specific SIMD: AVX2 + FMA + F16C on x86_64, NEON on aarch64, with scalar fallbacks. Runtime CPU feature detection dispatches to the optimal implementation on every call.

## Key Types & Critical Paths

**Exported functions (C ABI, `#[unsafe(no_mangle)]`):**
- `squared_euclidean_f32(const float *x, const float *y, size_t len)` -- L2 distance for float32 vectors.
- `dot_product_f32(const float *x, const float *y, size_t len)` -- Dot product for float32 vectors.
- `squared_euclidean_f16(const uint16_t *x, const uint16_t *y, size_t len)` -- L2 distance for float16 vectors.
- `dot_product_i8(const int8_t *x, const int8_t *y, size_t len)` -- Dot product for int8 vectors.

**Implementation modules:**
- `x86_64_impl` -- AVX2+FMA for f32 (8 floats/cycle via `_mm256_fmadd_ps`), AVX2+FMA+F16C for f16 (`_mm256_cvtph_ps`), AVX2 for i8 (32 int8/cycle, widened to i16, `_mm256_madd_epi16`).
- `aarch64_impl` -- NEON for f32 (4 floats/cycle via `vfmaq_f32`), NEON for i8 (16 int8/cycle, widened through i16, `vmlal_s16`). No F16 NEON.
- Fallback functions -- Pure scalar Rust loops, always compiled.

**Build configuration:** `Cargo.toml` -- `staticlib` crate type, `libkektordb_compute.a`. `profile.release` sets `panic = "abort"`.

## Architecture & Data Flow

**Four exported functions via C ABI:**
- `squared_euclidean_f32(const float *x, const float *y, size_t len)` -- L2 distance for float32 vectors
- `dot_product_f32(const float *x, const float *y, size_t len)` -- Dot product for float32 vectors
- `squared_euclidean_f16(const uint16_t *x, const uint16_t *y, size_t len)` -- L2 distance for float16 vectors
- `dot_product_i8(const int8_t *x, const int8_t *y, size_t len)` -- Dot product for int8 vectors

**Three-layer implementation:**
1. **Architecture-specific modules:** `x86_64_impl` (compiled only on x86_64) uses AVX2+FMA for f32 (8 floats at a time via `_mm256_fmadd_ps`), AVX2+FMA+F16C for f16 (8 packed f16 converted to f32 via `_mm256_cvtph_ps`), and AVX2 for i8 (32 int8 at a time, widened to i16, accumulated via `_mm256_madd_epi16`). `aarch64_impl` (compiled only on aarch64) uses NEON for f32 (4 floats at a time via `vfmaq_f32`) and i8 (16 int8, widened through i16, accumulated via `vmlal_s16`). No F16 NEON implementation exists yet.
2. **Fallback functions:** Pure scalar Rust loops, always compiled.
3. **Exported public functions:** Each performs runtime CPU feature detection (`is_x86_feature_detected!("fma")`, `is_aarch64_feature_detected!("neon")`), then dispatches to the appropriate SIMD or fallback implementation.

**CGO bridge:** `pkg/core/distance/distance_rust.go` (`//go:build rust`) links against the static library via `#cgo LDFLAGS: -lkektordb_compute`. Smart dispatch at the Go level: vectors >= 128 dims use Rust SIMD; shorter vectors use pure Go (CGO call overhead exceeds SIMD speedup for short vectors). Float32 cosine always uses Gonum BLAS.

**Build configuration:** `Cargo.toml` produces a `staticlib` crate type (`libkektordb_compute.a`). `profile.release` sets `panic = "abort"` to avoid Rust unwinding across the FFI boundary.

## Cross-Module Dependencies

**Depends on:**
- `half` crate (v2.1.0) -- Float16 (f16) bit manipulation for F16C conversion.
- `mem` / `transmute` crates -- Low-level memory utilities.
- Rust standard library -- CPU feature detection (`is_x86_feature_detected!`, `is_aarch64_feature_detected!`).

**Used by:**
- `pkg/core/distance` (via CGO, `//go:build rust`) -- Primary and only consumer. Go code calls the C ABI functions through `kektordb_compute.h`.
- Go build system -- `cargo build --release` must be run in `native/compute/` before `go build -tags rust`.

## Concurrency & Locking Rules

**All exported functions are stateless:** The `#[unsafe(no_mangle)]` extern "C" functions take pointers and lengths, perform computation, and return a scalar. No internal state, no mutexes, fully reentrant. Multiple threads can call these functions simultaneously with zero contention.

**`panic = "abort"`:** Essential for FFI safety. If Rust panics while unwinding across the C/Go boundary, it causes undefined behavior. Aborting is the correct choice.

**Runtime feature detection per call:** Feature checks happen on every function invocation, not once at startup. The check is a single atomic load -- negligible overhead. Guarantees correctness if the binary is migrated between machines with different CPU capabilities.

## Known Pitfalls / Gotchas

- **Must build Rust library before Go** -- `cargo build --release` in `native/compute/` must succeed before `go build -tags rust`. If the Rust library is missing or outdated, the Go build will fail with a linker error (`cannot find -lkektordb_compute`).
- **CGO requires a C compiler** -- Even though the library is Rust, CGO needs a C compiler (gcc/clang) to link the static library. On systems without a C toolchain, the `-tags rust` build will fail.
- **F16 NEON is not implemented** -- On AArch64 (Apple Silicon, ARM servers), float16 Euclidean distance falls back to scalar Rust. This is significantly slower than the x86_64 F16C implementation. If you run KektorDB on ARM with float16 vectors, expect lower performance.
- **Runtime dispatch per call, not per startup** -- CPU feature detection happens on every function call. While the overhead is negligible (single atomic load), it means the branch predictor must handle this check millions of times during a search. In theory, startup-time detection with function pointer dispatch would be slightly faster.
- **Unaligned loads everywhere** -- `_mm256_loadu_ps` is used exclusively. On very old CPUs (pre-Sandy Bridge), this can be slower than aligned loads. On modern CPUs, the difference is negligible. If you target old hardware, consider adding alignment checks.
- **`panic = "abort"` means no error recovery** -- If a bug in the Rust code causes a panic (e.g., out-of-bounds access), the entire process aborts. There is no way to catch this from Go. The `#[target_feature]` annotations should prevent illegal instruction faults, but bugs in the SIMD logic can still crash the process.
- **Static library is platform-specific** -- `libkektordb_compute.a` built on Linux x86_64 will not work on macOS ARM. You must rebuild the Rust library for each target platform.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Runtime dispatch per call** | Feature detection on every invocation | Guarantees correctness across machine migrations; negligible overhead (single atomic load) |
| **Unaligned loads everywhere** | `_mm256_loadu_ps` instead of aligned loads | Avoids branch mispredictions from alignment checks; on modern CPUs, unaligned loads are nearly as fast |
| **`panic = "abort"`** | No unwinding across FFI | Essential safety -- Rust panic unwinding into Go causes undefined behavior |
| **No F16 NEON** | Float16 Euclidean falls back to scalar on AArch64 | Stable F16 NEON intrinsics not yet available; known performance gap on Apple Silicon |
| **CGO dispatch threshold at 128** | Vectors < 128 dims use pure Go | CGO call overhead (~50-100ns) dominates computation for short vectors |
| **Static library output** | `staticlib` crate type, not `cdylib` | Linked at Go build time via `#cgo LDFLAGS`; no runtime library dependency |
