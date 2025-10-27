//go:build rust

package distance

/*
#cgo CFLAGS: -I../../../native/compute/include
#cgo LDFLAGS: -L../../../native/compute/target/release -lkektordb_compute -ldl -lm
#include "kektordb_compute.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"gonum.org/v1/gonum/blas/gonum"
	"log"
	"unsafe"
)

func init() {
	log.Println("KektorDB compute engine: using SMART DISPATCH (RUST CGO + GO) implementation.")
	log.Printf("  - Euclidean (float32): Smart Dispatch (Go/Rust)")
	log.Printf("  - Cosine (float32):    Gonum (SIMD)")
	log.Printf("  - Euclidean (float16): Rust (CGO SIMD)")
	log.Printf("  - Cosine (int8):       Smart Dispatch (Go/Rust)")
}

// --- Tipi Pubblici ---
// Ridefiniamo questi tipi qui perché il file distance_go.go è escluso dalla build.
type DistanceMetric string
type PrecisionType string

const (
	Euclidean DistanceMetric = "euclidean"
	Cosine    DistanceMetric = "cosine"
	Float32   PrecisionType  = "float32"
	Float16   PrecisionType  = "float16"
	Int8      PrecisionType  = "int8"
)

type DistanceFuncF32 func(v1, v2 []float32) (float64, error)
type DistanceFuncF16 func(v1, v2 []uint16) (float64, error)
type DistanceFuncI8 func(v1, v2 []int8) (int32, error)

// --- Wrapper Go che chiamano il codice Rust ---

// -- Float32 --
func squaredEuclideanRustF32(v1, v2 []float32) (float64, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vettori di lunghezza diversa")
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
		return 0, errors.New("vettori di lunghezza diversa")
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
		return 0, errors.New("vettori di lunghezza diversa")
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
		return 0, errors.New("vettori di lunghezza diversa")
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

// --- Implementazioni Go/Gonum necessarie per il dispatch intelligente ---
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
		return 0, errors.New("vettori di lunghezza diversa")
	}
	dot := gonumEngine.Sdot(len(v1), v1, 1, v2, 1)
	return 1.0 - float64(dot), nil
}

func dotProductGoInt8(v1, v2 []int8) (int32, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("i vettori int8 devono avere la stessa lunghezza")
	}
	var sum int32
	for i := range v1 {
		sum += int32(v1[i]) * int32(v2[i])
	}
	return sum, nil
}

// --- Funzioni "Smart Dispatch" ---

func squaredEuclideanSmartF32(v1, v2 []float32) (float64, error) {
	// Basato sui benchmark, Rust è migliore per vettori lunghi
	if len(v1) >= 256 {
		return squaredEuclideanRustF32(v1, v2)
	}
	return squaredEuclideanDistanceGo(v1, v2)
}

func dotProductSmartI8(v1, v2 []int8) (int32, error) {
	// Basato sui benchmark, Rust è migliore per vettori lunghi
	if len(v1) >= 256 {
		return dotProductRustI8(v1, v2)
	}
	return dotProductGoInt8(v1, v2)
}

// --- Cataloghi e Dispatcher (Versione Rust) ---

var float32Funcs = map[DistanceMetric]DistanceFuncF32{
	Euclidean: squaredEuclideanSmartF32,  // usa la funzione smart per usare quella più conveniente
	Cosine:    dotProductAsDistanceGonum, // gonum sempre meglio
}

var float16Funcs = map[DistanceMetric]DistanceFuncF16{
	Euclidean: squaredEuclideanRustF16, // rust sempre meglio
}

var int8Funcs = map[DistanceMetric]DistanceFuncI8{
	Cosine: dotProductSmartI8, // sceglie la più conveniente
}

// --- Getters Pubblici (identici a quelli in distance_go.go) ---

func GetFloat32Func(metric DistanceMetric) (DistanceFuncF32, error) {
	fn, ok := float32Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione float32", metric)
	}
	return fn, nil
}

func GetFloat16Func(metric DistanceMetric) (DistanceFuncF16, error) {
	fn, ok := float16Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione float16", metric)
	}
	return fn, nil
}

func GetInt8Func(metric DistanceMetric) (DistanceFuncI8, error) {
	fn, ok := int8Funcs[metric]
	if !ok {
		return nil, fmt.Errorf("metrica '%s' non supportata per precisione int8", metric)
	}
	return fn, nil
}
