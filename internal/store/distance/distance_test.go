package distance

import (
	"github.com/klauspost/cpuid/v2"
	"github.com/x448/float16"
	"math"
	"math/rand"
	"testing"
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

// TestDotProductFunctions confronta l'output della funzione Go e di quella AVX2 per il prodotto scalare.
func TestDotProductFunctions(t *testing.T) {
	if !cpuid.CPU.Has(cpuid.AVX2) {
		t.Skip("Skipping AVX2 test: CPU does not support this feature")
	}

	testCases := []struct {
		name string
		v1   []float32
		v2   []float32
	}{
		{
			name: "Vettori ortogonali",
			v1:   []float32{1, 0, 1, 0, 1, 0, 1, 0},
			v2:   []float32{0, 1, 0, 1, 0, 1, 0, 1},
		},
		{
			name: "Vettori collineari",
			v1:   []float32{1, 2, 3, 4, 5, 6, 7, 8},
			v2:   []float32{2, 4, 6, 8, 10, 12, 14, 16},
		},
		{
			name: "Vettori con resto (lunghezza 10)",
			v1:   []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			v2:   []float32{10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// calcola con Go
			goResult, errGo := dotProductGo(tc.v1, tc.v2)
			if errGo != nil {
				t.Fatalf("La funzione Go ha restituito un errore inaspettato: %v", errGo)
			}

			// calcola con AVX2
			avxResult, errAvx := dotProductAVX2(tc.v1, tc.v2)
			if errAvx != nil {
				t.Fatalf("La funzione AVX2 ha restituito un errore inaspettato: %v", errAvx)
			}

			// calcola con AVX2 + FMA
			avxfmaResult, errAvxFma := dotProductAVX2FMA(tc.v1, tc.v2)
			if errAvxFma != nil {
				t.Fatalf("La funzione AVX2 ha restituito un errore inaspettato: %v", errAvx)
			}

			// confronto Go con AVX2
			if !floatsAreEqual(goResult, avxResult) {
				t.Errorf("I risultati del prodotto scalare non corrispondono!\nGo:   %.15f\nAVX2: %.15f", goResult, avxResult)
			}

			if !floatsAreEqual(goResult, avxfmaResult) {
				t.Errorf("I risultati del prodotto scalare non corrispondono!\nGo:   %.15f\nAVX2FMA: %.15f", goResult, avxfmaResult)
			}
		})
	}
}

// --- NUOVI TEST PER FLOAT16 ---
func TestDistanceFloat16(t *testing.T) {
	if !cpuid.CPU.Has(cpuid.AVX2) || !cpuid.CPU.Has(cpuid.F16C) {
		t.Skip("Skipping float16 AVX2 test: CPU does not support AVX2 and F16C")
	}

	testCases := []struct {
		name string
		v1   []float32 // Definiamo i test in float32 per facilità di lettura
		v2   []float32
	}{
		{
			name: "Vettori identici (multiplo di 8)",
			v1:   []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
			v2:   []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0},
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
		{
			name: "Vettori di lunghezza 1",
			v1:   []float32{10.0},
			v2:   []float32{-5.0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Converti i vettori di test float32 in float16 (uint16)
			f16_v1 := make([]uint16, len(tc.v1))
			f16_v2 := make([]uint16, len(tc.v2))
			for i := range tc.v1 {
				f16_v1[i] = float16.Fromfloat32(tc.v1[i]).Bits()
				f16_v2[i] = float16.Fromfloat32(tc.v2[i]).Bits()
			}

			// Calcola il risultato con la funzione Go pura (la nostra "verità")
			goResult, errGo := squaredEuclideanDistanceGoFloat16(f16_v1, f16_v2)
			if errGo != nil {
				t.Fatalf("La funzione Go ha restituito un errore inaspettato: %v", errGo)
			}

			// Calcola con AVX2
			// if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) {
			avxResult, _ := squaredEuclideanDistanceAVX2Float16(f16_v1, f16_v2)
			if !floatsAreEqual(goResult, avxResult) {
				t.Errorf("I risultati float16 AVX2 non corrispondono!\nGo: %.15f\nAVX2: %.15f", goResult, avxResult)
			}
			// }

			// Calcola con AVX2+FMA
			// if cpuid.CPU.Has(cpuid.AVX2) && cpuid.CPU.Has(cpuid.F16C) && cpuid.CPU.Has(cpuid.FMA3) {
			fmaResult, _ := squaredEuclideanDistanceAVX2Float16FMA(f16_v1, f16_v2)
			if !floatsAreEqual(goResult, fmaResult) {
				t.Errorf("I risultati float16 FMA non corrispondono!\nGo: %.15f\nFMA: %.15f", goResult, fmaResult)
			}
			// }
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
	if !cpuid.CPU.Has(cpuid.AVX2) && !cpuid.CPU.Has(cpuid.FMA3) {
		b.Skip("Skipping AVX2FMA benchmark: CPU does not support this feature")
	}
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceAVX2FMA(v1, v2)
	}
}

// Benchmark per la versione Go pura
func BenchmarkDotProductGo(b *testing.B) {
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductGo(v1, v2)
	}
}

// Benchmark per la versione AVX2
func BenchmarkDotProductAVX2(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) {
		b.Skip("Skipping AVX2 benchmark: CPU does not support this feature")
	}
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAVX2(v1, v2)
	}
}

// Benchmark per la versione AVX2 + FMA
func BenchmarkDotProductAVX2FMA(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) && !cpuid.CPU.Has(cpuid.FMA3) {
		b.Skip("Skipping AVX2FMA benchmark: CPU does not support this feature")
	}
	v1, v2 := generateVectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dotProductAVX2FMA(v1, v2)
	}
}

// --- NUOVI BENCHMARK PER FLOAT16 ---
func generateFloat16Vectors(dims int) ([]uint16, []uint16) {
	v1 := make([]uint16, dims)
	v2 := make([]uint16, dims)
	for i := 0; i < dims; i++ {
		v1[i] = float16.Fromfloat32(rand.Float32()).Bits()
		v2[i] = float16.Fromfloat32(rand.Float32()).Bits()
	}
	return v1, v2
}

func BenchmarkDistanceGoFloat16(b *testing.B) {
	v1, v2 := generateFloat16Vectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceGoFloat16(v1, v2)
	}
}

func BenchmarkDistanceAVX2Float16(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) || !cpuid.CPU.Has(cpuid.F16C) {
		b.Skip("Skipping float16 AVX2 benchmark: CPU does not support AVX2 and F16C")
	}
	v1, v2 := generateFloat16Vectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceAVX2Float16(v1, v2)
	}
}

// 2. Aggiungi il nuovo benchmark
func BenchmarkDistanceAVX2Float16FMA(b *testing.B) {
	if !cpuid.CPU.Has(cpuid.AVX2) || !cpuid.CPU.Has(cpuid.F16C) || !cpuid.CPU.Has(cpuid.FMA3) {
		b.Skip("Skipping float16 FMA benchmark: CPU does not support required features")
	}
	v1, v2 := generateFloat16Vectors(128)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		squaredEuclideanDistanceAVX2Float16FMA(v1, v2)
	}
}
