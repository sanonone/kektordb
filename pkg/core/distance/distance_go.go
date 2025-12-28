//go:build !rust

// Package distance provides functions for calculating vector distances.
// It supports multiple metrics like Euclidean and Cosine, and different data precisions
// including float32, float16, and int8.
//
// The package uses build tags and runtime CPU detection to dispatch to the most
// optimized implementation available, such as pure Go, Gonum (BLAS/SIMD), or
// hardware-accelerated AVX2 routines.
package distance

import (
	"errors"
	"fmt"
	"log/slog"

	// "github.com/klauspost/cpuid/v2"
	"github.com/x448/float16"
	"gonum.org/v1/gonum/blas/gonum"
)

func init() {
	// Override defaults with optimized versions from Gonum.
	// Gonum handles SIMD dispatch internally.
	float32Funcs[Cosine] = dotProductAsDistanceGonum

	// if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
	// float16Funcs[Euclidean] = squaredEuclideanF16AVX2Wrapper // Use the wrapper
	// }
	slog.Info("KektorDB compute engine: using PURE GO / GONUM implementation.")
	slog.Info("  - Euclidean (float32): Pure Go")
	slog.Info("  - Cosine (float32):    Gonum (SIMD)")
	slog.Info("  - Euclidean (float16): Pure Go (Fallback)")
	slog.Info("  - Cosine (int8):       Pure Go (Fallback)")
}

// --- Public Types ---
// These types define the public contract that this package offers to the rest of the system.

// DistanceMetric defines the type of distance calculation to perform.
type DistanceMetric string

// PrecisionType defines the data type used for vector storage and calculations.
type PrecisionType string

const (
	// Euclidean represents the squared Euclidean distance metric.
	Euclidean DistanceMetric = "euclidean"
	// Cosine represents the cosine distance metric (1 - cosine similarity).
	Cosine DistanceMetric = "cosine"

	// Float32 represents single-precision floating-point numbers.
	Float32 PrecisionType = "float32"
	// Float16 represents half-precision floating-point numbers.
	Float16 PrecisionType = "float16"
	// Int8 represents 8-bit signed integers, typically for quantized vectors.
	Int8 PrecisionType = "int8"
)

// Define function types for each precision
type DistanceFuncF32 func(v1, v2 []float32) (float64, error)
type DistanceFuncF16 func(v1, v2 []uint16) (float64, error)
type DistanceFuncI8 func(v1, v2 []int8) (int32, error)

// --- REFERENCE IMPLEMENTATIONS (PURE GO) ---

// squaredEuclideanDistanceGo is the pure Go implementation for squared Euclidean distance.
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

// dotProductAsDistanceGo is the reference implementation for the Cosine metric on normalized data.
func dotProductAsDistanceGo(v1, v2 []float32) (float64, error) {
	dot, err := dotProductGo(v1, v2)
	if err != nil {
		return 0, err
	}
	return 1.0 - float64(dot), nil
}

// dotProductGo is the pure Go reference implementation for the dot product.
func dotProductGo(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("dotProduct: vectors must have the same length")
	}
	var sum float32
	for i := range v1 {
		sum += v1[i] * v2[i]
	}
	return float64(sum), nil
}

// squaredEuclideanGoFloat16 is the pure Go implementation for squared Euclidean distance on float16 vectors.
func squaredEuclideanGoFloat16(v1, v2 []uint16) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("float16 vectors must have the same length")
	}
	var sum float32
	for i := range v1 {
		f1 := float16.Frombits(v1[i]).Float32()
		f2 := float16.Frombits(v2[i]).Float32()
		diff := f1 - f2
		sum += diff * diff
	}
	return float64(sum), nil
}

// dotProductGoInt8 is the pure Go implementation for dot product on int8 vectors.
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

// --- Gonum-based Implementations (for float32) ---
var gonumEngine = gonum.Implementation{}

// dotProductAsDistanceGonum uses the Gonum BLAS library for an optimized dot product.
func dotProductAsDistanceGonum(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	dot := gonumEngine.Sdot(len(v1), v1, 1, v2, 1)
	return 1.0 - float64(dot), nil
}

// --- Function Catalogs and Dispatchers ---

// float32Funcs maps a distance metric to its corresponding float32 implementation.
var float32Funcs = map[DistanceMetric]DistanceFuncF32{
	Euclidean: squaredEuclideanDistanceGo, // default
	Cosine:    dotProductAsDistanceGo,     // default
}

// float16Funcs maps a distance metric to its corresponding float16 implementation.
var float16Funcs = map[DistanceMetric]DistanceFuncF16{
	Euclidean: squaredEuclideanGoFloat16,
}

// int8Funcs maps a distance metric to its corresponding int8 implementation.
var int8Funcs = map[DistanceMetric]DistanceFuncI8{
	Cosine: dotProductGoInt8,
}

// --- Public Getter Functions ---

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
