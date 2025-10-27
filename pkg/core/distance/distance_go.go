//go:build !rust

// File: internal/store/distance/distance.go
package distance

import (
	"errors"
	"fmt"
	"github.com/klauspost/cpuid/v2"
	"github.com/x448/float16"
	"gonum.org/v1/gonum/blas/gonum"
	"log"
	"sync"
)

func init() {
	// Sovrascrivi i default con le versioni ottimizzate di Gonum.
	// Gonum si occupa del dispatch SIMD al suo interno.
	// float32Funcs[Euclidean] = squaredEuclideanGonum
	float32Funcs[Cosine] = dotProductAsDistanceGonum

	if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
		float16Funcs[Euclidean] = squaredEuclideanF16AVX2Wrapper // Usa il wrapper
	}
	log.Println("KektorDB compute engine: using PURE GO / GONUM implementation.")
	log.Printf("  - Euclidean (float32): Pure Go")
	log.Printf("  - Cosine (float32):    Gonum (SIMD)")
	log.Printf("  - Euclidean (float16): Pure Go (Fallback)")
	log.Printf("  - Cosine (int8):       Pure Go (Fallback)")
}

// --- Tipi Pubblici ---
// Questi tipi sono il "contratto" che il nostro package offre al resto del sistema.
type DistanceMetric string
type PrecisionType string

const (
	Euclidean DistanceMetric = "euclidean"
	Cosine    DistanceMetric = "cosine"

	Float32 PrecisionType = "float32"
	Float16 PrecisionType = "float16"
	Int8    PrecisionType = "int8"
)

// Definiamo i tipi di funzione per ogni precisione
type DistanceFuncF32 func(v1, v2 []float32) (float64, error)
type DistanceFuncF16 func(v1, v2 []uint16) (float64, error)
type DistanceFuncI8 func(v1, v2 []int8) (int32, error)

// --- WORKSPACE POOL ---
// Crea un pool di slice. Ogni chiamata a squaredEuclideanGonum
// prenderà una slice dal pool, la userà e poi la restituirà.
// Questo evita l'allocazione di memoria a ogni chiamata.
var diffWorkspace = sync.Pool{
	New: func() interface{} {
		// La dimensione qui deve essere abbastanza grande per i vettori.
		// 1536 è una dimensione comune per gli embeddings di OpenAI.
		// renderla configurabile in futuro
		s := make([]float32, 1536)
		return &s
	},
}

// Creiamo il wrapper che orchestra tutto

func squaredEuclideanF16AVX2Wrapper(v1, v2 []uint16) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("vettori di lunghezza diversa")
	}
	if len(v1) == 0 {
		return 0, nil
	}
	res := SquaredEuclideanFloat16AVX2(v1, v2)
	return float64(res), nil
}

// --- FUNZIONI DI RIFERIMENTO (GO PURO) ---
// funzione per distanza euclidea in go
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

// Questa è la nostra funzione di riferimento per la metrica Coseno su dati normalizzati.
func dotProductAsDistanceGo(v1, v2 []float32) (float64, error) {
	dot, err := dotProductGo(v1, v2)
	if err != nil {
		return 0, err
	}
	return 1.0 - float64(dot), nil
}

// Questa è la versione di riferimento in Go puro
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

func squaredEuclideanGoFloat16(v1, v2 []uint16) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("i vettori float16 devono avere la stessa lunghezza")
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

// --- Implementazioni Basate su Gonum (per float32) ---
var gonumEngine = gonum.Implementation{}

func squaredEuclideanGonum(v1, v2 []float32) (float64, error) {
	n := len(v1)
	if n != len(v2) {
		return 0, errors.New("vettori di lunghezza diversa")
	}

	// Prendi una slice dal pool
	diffPtr := diffWorkspace.Get().(*[]float32)
	defer diffWorkspace.Put(diffPtr) // Assicura che la slice torni nel pool alla fine

	// Controlla se la slice del pool è abbastanza grande
	if cap(*diffPtr) < n {
		*diffPtr = make([]float32, n)
	}
	diff := (*diffPtr)[:n] // Usa solo la porzione che ci serve

	// Ora esegui i calcoli senza allocazioni
	copy(diff, v1)
	gonumEngine.Saxpy(n, -1, v2, 1, diff, 1)
	dot := gonumEngine.Sdot(n, diff, 1, diff, 1)

	return float64(dot), nil
}

func dotProductAsDistanceGonum(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, errors.New("vettori di lunghezza diversa")
	}
	dot := gonumEngine.Sdot(len(v1), v1, 1, v2, 1)
	return 1.0 - float64(dot), nil
}

// --- Cataloghi e Dispatcher ---

var float32Funcs = map[DistanceMetric]DistanceFuncF32{
	Euclidean: squaredEuclideanDistanceGo, // default
	Cosine:    dotProductAsDistanceGo,     // default
}

var float16Funcs = map[DistanceMetric]DistanceFuncF16{
	Euclidean: squaredEuclideanGoFloat16,
}

var int8Funcs = map[DistanceMetric]DistanceFuncI8{
	Cosine: dotProductGoInt8,
}

// --- Funzioni Getter Pubbliche ---

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
