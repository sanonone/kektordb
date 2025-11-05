//go:build avo && !rust && amd64

package distance

import (
	"errors"
	"github.com/klauspost/cpuid/v2"
	"log"
)

// Wrapper ibrido
func squaredEuclideanF16AVX2Wrapper(v1, v2 []uint16) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("vectors must have the same length")
	}
	if len(v1) == 0 {
		return 0, nil
	}
	res := SquaredEuclideanFloat16AVX2(v1, v2)
	return float64(res), nil
}

func init() {
	log.Println("KektorDB compute engine: using AVO/SIMD optimizations where available.")
	// Sovrascrivi il default con la versione AVO
	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
		float16Funcs[Euclidean] = squaredEuclideanF16AVX2Wrapper
	}
}
