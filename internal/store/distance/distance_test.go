package distance

import (
	"github.com/x448/float16"
	"math"
	"math/rand"
	"testing"
)

// altrimenti il test da segna come differenti due valori praticamente uguali
func floatsAreEqual(a, b float64) bool {
	const tolerance = 1e-6
	return math.Abs(a-b) < tolerance
}

// --- TEST DI CORRETTEZZA ---
// Dato che ora non abbiamo più implementazioni separate (Go vs SIMD),
// testiamo che le nostre implementazioni diano risultati ragionevoli.
// Un test più approfondito confronterebbe Gonum con l'implementazione Go pura.

func TestSquaredEuclideanGonum(t *testing.T) {
	v1 := []float32{1, 2, 3}
	v2 := []float32{4, 5, 6}
	// (4-1)^2 + (5-2)^2 + (6-3)^2 = 9 + 9 + 9 = 27
	expected := 27.0

	dist, err := squaredEuclideanGonum(v1, v2)
	if err != nil {
		t.Fatalf("Errore inatteso: %v", err)
	}
	if dist != expected {
		t.Errorf("Risultato errato: ottenuto %f, atteso %f", dist, expected)
	}
}

func TestDotProductAsDistanceGonum(t *testing.T) {
	// Vettori normalizzati
	v1 := []float32{0.26726124, 0.5345225, 0.8017837}
	v2 := []float32{0.26726124, 0.5345225, 0.8017837}
	// dot = 1.0; 1 - dot = 0.0
	expected := 0.0

	dist, err := dotProductAsDistanceGonum(v1, v2)
	if err != nil {
		t.Fatalf("Errore inatteso: %v", err)
	}
	// Usa il confronto con tolleranza
	if !floatsAreEqual(dist, expected) {
		t.Errorf("Risultato errato: ottenuto %.15f, atteso %.15f", dist, expected)
	}
}

// --- BENCHMARK ---
// Ora i benchmark misurano le performance di Gonum e dei fallback Go.

func generateVectors(dims int) ([]float32, []float32) {
	v1 := make([]float32, dims)
	v2 := make([]float32, dims)
	for i := 0; i < dims; i++ {
		v1[i] = rand.Float32()
		v2[i] = rand.Float32()
	}
	return v1, v2
}

func generateFloat16Vectors(dims int) ([]uint16, []uint16) {
	v1 := make([]uint16, dims)
	v2 := make([]uint16, dims)
	for i := 0; i < dims; i++ {
		v1[i] = float16.Fromfloat32(rand.Float32()).Bits()
		v2[i] = float16.Fromfloat32(rand.Float32()).Bits()
	}
	return v1, v2
}

func generateInt8Vectors(dims int) ([]int8, []int8) {
	v1 := make([]int8, dims)
	v2 := make([]int8, dims)
	for i := 0; i < dims; i++ {
		v1[i] = int8(rand.Intn(256) - 128)
		v2[i] = int8(rand.Intn(256) - 128)
	}
	return v1, v2
}

// Benchmark per Euclidean: Go vs Gonum
func BenchmarkEuclideanGo(b *testing.B) {
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceGo(v1, v2)
	}
}
func BenchmarkEuclideanGonum(b *testing.B) {
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanGonum(v1, v2)
	}
}

// Benchmark per Coseno: Go vs Gonum
func BenchmarkCosineGo(b *testing.B) {
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAsDistanceGo(v1, v2)
	}
}
func BenchmarkCosineGonum(b *testing.B) {
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAsDistanceGonum(v1, v2)
	}
}

func BenchmarkEuclideanFloat16(b *testing.B) {
	v1, v2 := generateFloat16Vectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanGoFloat16(v1, v2)
	}
}

func BenchmarkCosineInt8(b *testing.B) {
	v1, v2 := generateInt8Vectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductGoInt8(v1, v2)
	}
}

// --- BENCHMARK SU VETTORI LUNGHI (768 dimensioni, tipico per BERT) ---

const longVectorDims = 768

func BenchmarkEuclideanGo_Long(b *testing.B) {
	v1, v2 := generateVectors(longVectorDims)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceGo(v1, v2)
	}
}

func BenchmarkEuclideanGonum_Long(b *testing.B) {
	v1, v2 := generateVectors(longVectorDims)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanGonum(v1, v2)
	}
}

func BenchmarkCosineGo_Long(b *testing.B) {
	v1, v2 := generateVectors(longVectorDims)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAsDistanceGo(v1, v2)
	}
}

func BenchmarkCosineGonum_Long(b *testing.B) {
	v1, v2 := generateVectors(longVectorDims)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAsDistanceGonum(v1, v2)
	}
}
