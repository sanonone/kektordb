package store

import (
	"math"
	"math/rand"
	"testing"

	"github.com/klauspost/cpuid/v2"
)

// testFloatsAreEqual confronta due float64 con una piccola tolleranza per
// possibili imprecisioni in virgola mobile.
func floatsAreEqual(a, b float64) bool {
	const tolerance = 1e-9
	return math.Abs(a-b) < tolerance
}

// TestDistanceFunctions confronta l'output della funzione Go e di quella AVX2.
func TestDistanceFunctions(t *testing.T) {
	// Se la CPU non supporta AVX2, non possiamo eseguire questo test.
	if !cpuid.CPU.Has(cpuid.AVX2) {
		t.Skip("Skipping AVX2 test: CPU does not support this feature")
	}

	// Creiamo alcuni scenari di test
	testCases := []struct {
		name string
		v1   []float32
		v2   []float32
	}{
		{
			name: "Vettori identici",
			v1:   []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
			v2:   []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
		},
		{
			name: "Vettori diversi (multiplo di 8)",
			v1:   []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
			v2:   []float32{8.0, 7.0, 6.0, 5.0, 4.0, 3.0, 2.0, 1.0},
		},
		{
			name: "Vettori con resto (lunghezza 10)",
			v1:   []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			v2:   []float32{10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		},
		{
			name: "Vettori con solo resto (lunghezza 3)",
			v1:   []float32{0.1, 0.2, 0.3},
			v2:   []float32{0.4, 0.5, 0.6},
		},
		{
			name: "Vettori vuoti",
			v1:   []float32{},
			v2:   []float32{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Calcola il risultato con la funzione Go 
			goResult, errGo := squaredEuclideanDistanceGo(tc.v1, tc.v2)
			if errGo != nil {
				t.Fatalf("La funzione Go ha restituito un errore inaspettato: %v", errGo)
			}

			// Calcola il risultato con la funzione AVX2
			avxResult, errAvx := squaredEuclideanDistanceAVX2(tc.v1, tc.v2)
			if errAvx != nil {
				t.Fatalf("La funzione AVX2 ha restituito un errore inaspettato: %v", errAvx)
			}

			// Calcola il risultato con la funzione AVX2FMA
			avxfmaResult, errAvxFma := squaredEuclideanDistanceAVX2FMA(tc.v1, tc.v2)
			if errAvxFma != nil {
				t.Fatalf("La funzione AVX2FMA ha restituito un errore inaspettato: %v", errAvxFma)
			}
	

			// Confronta i risultati go vs avx
			if !floatsAreEqual(goResult, avxResult) {
				t.Errorf("I risultati non corrispondono! Go: %.15f, AVX2: %.15f", goResult, avxResult)
			}

			// Confronta i risultati go vs avx + fma
			if !floatsAreEqual(goResult, avxfmaResult) {
				t.Errorf("I risultati non corrispondono! Go: %.15f, AVX2FMA: %.15f", goResult, avxfmaResult)
			}

		})
	}
}

// generateVectors crea due vettori casuali di una data dimensione.
func generateVectors(dims int) ([]float32, []float32) {
	v1 := make([]float32, dims)
	v2 := make([]float32, dims)
	for i := 0; i < dims; i++ {
		v1[i] = rand.Float32()
		v2[i] = rand.Float32()
	}
	return v1, v2
}

// Benchmark per la versione Go pura
func BenchmarkDistanceGo(b *testing.B) {
	v1, v2 := generateVectors(128) // Dimensione tipica per embeddings
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceGo(v1, v2)
	}
}

// Benchmark per la versione AVX2
func BenchmarkDistanceAVX2(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) {
		b.Skip("Skipping AVX2 benchmark: CPU does not support this feature")
	}
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceAVX2(v1, v2)
	}
}

// Benchmark per la versione AVX2FMA
func BenchmarkDistanceAVX2FMA(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) && !cpuid.CPU.Has(cpuid.FMA3)  {
		b.Skip("Skipping AVX2FMA benchmark: CPU does not support this feature")
	}
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceAVX2FMA(v1, v2)
	}
}
