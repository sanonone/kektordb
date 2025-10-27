package distance

import (
	"fmt"
	"github.com/x448/float16"
	"math"
	"math/rand"
	"testing"
)

// Helper di normalizzazione SOLO per i test
func normalizeTest(v []float32) {
	var norm float32
	for _, val := range v {
		norm += val * val
	}
	if norm > 0 {
		norm = float32(math.Sqrt(float64(norm)))
		for i := range v {
			v[i] /= norm
		}
	}
}

// Helper per il confronto con tolleranza
func floatsAreEqual(a, b float64) bool {
	const tolerance = 1e-6
	return math.Abs(a-b) < tolerance
}

// --- TEST DI CORRETTEZZA UNIFICATI ---
// Questi test funzionano sia in modalità Go-pura che Rust,
// perché usano i getter pubblici che si adattano dinamicamente.

func TestImplementations(t *testing.T) {
	// Questi test usano Get...Func(), quindi testano
	// automaticamente la versione attiva (Go o Rust).

	t.Run("EuclideanF32", func(t *testing.T) {
		fn, _ := GetFloat32Func(Euclidean)
		v1, v2 := []float32{1, 2}, []float32{3, 4}
		expected := 8.0 // (3-1)^2 + (4-2)^2 = 4 + 4 = 8
		dist, _ := fn(v1, v2)
		if !floatsAreEqual(dist, expected) {
			t.Errorf("got %f, want %f", dist, expected)
		}
	})

	t.Run("CosineF32", func(t *testing.T) {
		fn, _ := GetFloat32Func(Cosine)
		v1 := []float32{1, 2, 3}
		normalizeTest(v1)                // Normalizza per il test
		v2 := append([]float32{}, v1...) // Crea una copia
		expected := 0.0
		dist, _ := fn(v1, v2)
		if !floatsAreEqual(dist, expected) {
			t.Errorf("got %.15f, want %.15f", dist, expected)
		}
	})

	t.Run("EuclideanF16", func(t *testing.T) {
		fn, _ := GetFloat16Func(Euclidean)
		v1f, v2f := []float32{1, 2}, []float32{3, 4}
		expected := 8.0
		v1 := make([]uint16, len(v1f))
		v2 := make([]uint16, len(v2f))
		for i := range v1f {
			v1[i] = float16.Fromfloat32(v1f[i]).Bits()
			v2[i] = float16.Fromfloat32(v2f[i]).Bits()
		}
		dist, _ := fn(v1, v2)
		if !floatsAreEqual(dist, expected) {
			t.Errorf("got %f, want %f", dist, expected)
		}
	})

	t.Run("CosineInt8", func(t *testing.T) {
		fn, _ := GetInt8Func(Cosine)
		v1 := []int8{10, 20}
		v2 := []int8{2, 3}
		expected := int32(80) // 10*2 + 20*3 = 80
		dist, _ := fn(v1, v2)
		if int32(dist) != expected {
			t.Errorf("got %d, want %d", dist, expected)
		}
	})
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

func BenchmarkFloat32(b *testing.B) {
	eucFunc, _ := GetFloat32Func(Euclidean)
	cosFunc, _ := GetFloat32Func(Cosine)
	dims := []int{64, 128, 256, 512, 1024, 1536}

	for _, d := range dims {
		b.Run(fmt.Sprintf("Euclidean_%dD", d), func(b *testing.B) {
			v1, v2 := generateVectors(d)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				eucFunc(v1, v2)
			}
		})

		b.Run(fmt.Sprintf("Cosine_%dD", d), func(b *testing.B) {
			v1, v2 := generateVectors(d)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cosFunc(v1, v2)
			}
		})
	}
}

func BenchmarkFloat16(b *testing.B) {
	f16Func, _ := GetFloat16Func(Euclidean)
	dims := []int{64, 128, 256, 512, 1024, 1536}

	for _, d := range dims {
		b.Run(fmt.Sprintf("Euclidean_%dD", d), func(b *testing.B) {
			v1, v2 := generateFloat16Vectors(d)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f16Func(v1, v2)
			}
		})
	}
}

func BenchmarkInt8(b *testing.B) {
	i8Func, _ := GetInt8Func(Cosine)
	dims := []int{64, 128, 256, 512, 1024}
	for _, d := range dims {
		b.Run(fmt.Sprintf("Cosine_%dD", d), func(b *testing.B) {
			v1, v2 := generateInt8Vectors(d)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				i8Func(v1, v2)
			}
		})
	}
}
