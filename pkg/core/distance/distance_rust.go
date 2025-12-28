//go:build rust

// Package distance provides hardware-accelerated functions for calculating vector distances using a Rust core via CGO.
//
// This implementation is enabled with the 'rust' build tag. It leverages Rust's performance
// and SIMD capabilities for significant speed improvements over the pure Go version,
// especially for float16 and long int8/float32 vectors.
//
// Critical dependency: This package requires a pre-compiled Rust static library (`libkektordb_compute.a`).
package distance

/*
#cgo CFLAGS: -I${SRCDIR}/../../../native/compute/include
#cgo LDFLAGS: -lkektordb_compute
#include "kektordb_compute.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"log/slog"
	"unsafe"

	"gonum.org/v1/gonum/blas/gonum"
)

func init() {
	slog.Info("KektorDB compute engine: using SMART DISPATCH (RUST CGO + GO) implementation.")
	slog.Info("  - Euclidean (float32): Smart Dispatch (Go/Rust)")
	slog.Info("  - Cosine (float32):    Gonum (SIMD)")
	slog.Info("  - Euclidean (float16): Rust (CGO SIMD)")
	slog.Info("  - Cosine (int8):       Smart Dispatch (Go/Rust)")
}

// --- Public Types ---
// These types are redefined here because the distance_go.go file is excluded
// when the 'rust' build tag is active.

// DistanceMetric defines the type of distance calculation to perform.
type DistanceMetric string
type PrecisionType string

const (
	Euclidean DistanceMetric = "euclidean"
	Cosine    DistanceMetric = "cosine"
	Float32   PrecisionType  = "float32"
	Float16   PrecisionType  = "float16"
	Int8      PrecisionType  = "int8"
)

// DistanceFuncF32 is a function type for distance calculations on float32 vectors.
type DistanceFuncF32 func(v1, v2 []float32) (float64, error)

// DistanceFuncF16 is a function type for distance calculations on float16 (uint16) vectors.
type DistanceFuncF16 func(v1, v2 []uint16) (float64, error)

// DistanceFuncI8 is a function type for distance calculations on int8 vectors.
type DistanceFuncI8 func(v1, v2 []int8) (int32, error)

// --- Go Wrappers for Rust Functions ---

// -- Float32 --
func squaredEuclideanRustF32(v1, v2 []float32) (float64, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	if n == 0 {
		return 0, nil
	}

	res := C.squared_euclidean_f32(
		(*C.float)(unsafe.Pointer(&v1[0])),
		(*C.float)(unsafe.Pointer(&v2[0])),
		C.size_t(n),
	)
	return float64(res), nil
}

func dotProductRustF32(v1, v2 []float32) (float64, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	if n == 0 {
		return 0, nil
	}

	res := C.dot_product_f32(
		(*C.float)(unsafe.Pointer(&v1[0])),
		(*C.float)(unsafe.Pointer(&v2[0])),
		C.size_t(n),
	)
	return 1.0 - float64(res), nil
}

// -- Float16 --
func squaredEuclideanRustF16(v1, v2 []uint16) (float64, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	if n == 0 {
		return 0, nil
	}

	res := C.squared_euclidean_f16(
		(*C.uint16_t)(unsafe.Pointer(&v1[0])),
		(*C.uint16_t)(unsafe.Pointer(&v2[0])),
		C.size_t(n),
	)
	return float64(res), nil
}

// -- Int8 --
func dotProductRustI8(v1, v2 []int8) (int32, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	if n == 0 {
		return 0, nil
	}

	res := C.dot_product_i8(
		(*C.int8_t)(unsafe.Pointer(&v1[0])),
		(*C.int8_t)(unsafe.Pointer(&v2[0])),
		C.size_t(n),
	)
	return int32(res), nil
}

// --- Go/Gonum Implementations for Smart Dispatch ---
var gonumEngine = gonum.Implementation{}

func squaredEuclideanDistanceGo(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		// return 0, math.ErrUnsupported
		return 0, errors.New("squaredEuclideanDistance: vectors must have the same length")
	}
	var sum float32
	for i := range v1 {
		diff := v1[i] - v2[i]
		sum += diff * diff
	}
	return float64(sum), nil
}

func dotProductAsDistanceGonum(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	dot := gonumEngine.Sdot(len(v1), v1, 1, v2, 1)
	return 1.0 - float64(dot), nil
}

func dotProductGoInt8(v1, v2 []int8) (int32, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("int8 vectors must have the same length")
	}
	var sum int32
	for i := range v1 {
		sum += int32(v1[i]) * int32(v2[i])
	}
	return sum, nil
}

// --- Smart Dispatch Functions ---

// squaredEuclideanSmartF32 chooses the best implementation for float32 squared Euclidean distance.
// Based on benchmarks, the Rust implementation is faster for longer vectors.
func squaredEuclideanSmartF32(v1, v2 []float32) (float64, error) {
	if len(v1) >= 128 {
		return squaredEuclideanRustF32(v1, v2)
	}
	return squaredEuclideanDistanceGo(v1, v2)
}

// dotProductSmartI8 chooses the best implementation for int8 dot product.
// Based on benchmarks, the Rust implementation is faster for longer vectors.
func dotProductSmartI8(v1, v2 []int8) (int32, error) {
	if len(v1) >= 128 {
		return dotProductRustI8(v1, v2)
	}
	return dotProductGoInt8(v1, v2)
}

// --- Function Catalogs and Dispatcher (Rust Version) ---

var float32Funcs = map[DistanceMetric]DistanceFuncF32{
	Euclidean: squaredEuclideanSmartF32,  // Uses the smart dispatch function to select the most performant implementation.
	Cosine:    dotProductAsDistanceGonum, // Gonum is consistently faster for this operation.
}

var float16Funcs = map[DistanceMetric]DistanceFuncF16{
	Euclidean: squaredEuclideanRustF16, // The Rust implementation is consistently faster for this operation.
}

var int8Funcs = map[DistanceMetric]DistanceFuncI8{
	Cosine: dotProductSmartI8, // Selects the most performant implementation.
}

// --- Public Getters (identical to the pure Go version) ---

// GetFloat32Func returns the appropriate distance calculation function for a given
// metric and float32 precision. It returns an error if the metric is not supported.
func GetFloat32Func(metric DistanceMetric) (DistanceFuncF32, error) {
	fn, ok := float32Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metric '%s' not supported for float32 precision", metric)
	}
	return fn, nil
}

// GetFloat16Func returns the appropriate distance calculation function for a given
// metric and float16 precision. It returns an error if the metric is not supported.
func GetFloat16Func(metric DistanceMetric) (DistanceFuncF16, error) {
	fn, ok := float16Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metric '%s' not supported for float16 precision", metric)
	}
	return fn, nil
}

// GetInt8Func returns the appropriate distance calculation function for a given
// metric and int8 precision. It returns an error if the metric is not supported.
func GetInt8Func(metric DistanceMetric) (DistanceFuncI8, error) {
	fn, ok := int8Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metric '%s' not supported for int8 precision", metric)
	}
	return fn, nil
}
