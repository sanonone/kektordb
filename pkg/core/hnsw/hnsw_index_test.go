package hnsw

import (
	"fmt"
	"math/rand"
	"os"
	"runtime/pprof"
	"sync/atomic"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// generateTestObjects creates a test data set with random vectors.
// It is used both for benchmarks and profiling tests to ensure
// consistency and reduce code duplication.
func generateTestObjects(numVectors int, vectorDim int) []types.BatchObject {
	// We use a fixed seed to make tests deterministic, if needed.
	// For pure performance benchmarks, a variable seed is fine.
	rng := rand.New(rand.NewSource(42))

	objects := make([]types.BatchObject, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := 0; j < vectorDim; j++ {
			vec[j] = rng.Float32()
		}
		objects[i] = types.BatchObject{
			// String allocation happens here, once for all tests.
			Id:     fmt.Sprintf("vec-%d", i),
			Vector: vec,
		}
	}
	return objects
}

// BenchmarkConcurrentInserts measures the performance of vector insertion
// in parallel. This is a crucial test to identify bottlenecks
// due to lock contention in multi-threaded scenarios.
func BenchmarkConcurrentInserts(b *testing.B) {
	// --- Setup ---
	// We prepare in advance a data set large enough not to be
	// trivial. The vector dimension is common (e.g., MiniLM models).
	const (
		numVectors = 10000 // Number of vectors to insert in the test
		vectorDim  = 384   // Common dimension for embeddings
	)

	// We create vectors only once, outside the benchmark loop,
	// to avoid measuring the cost of their generation.
	vectors := make([][]float32, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := 0; j < vectorDim; j++ {
			vec[j] = rand.Float32()
		}
		vectors[i] = vec
	}

	// --- Benchmark ---
	b.Run("ConcurrentAdd", func(b *testing.B) {
		// We create a new index for each benchmark run to start
		// from a clean state.
		idx, _ := New(16, 200, distance.Cosine, distance.Float32, "", "")

		// We start measuring time only from here, after all the setup.
		b.ResetTimer()

		// We use an atomic counter to distribute work (vectors to insert)
		// across the goroutines created by RunParallel. This avoids the need
		// for a lock to access the vector slice.
		var counter uint32

		// Runs the body code in parallel on multiple goroutines.
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// Gets a unique index atomically.
				currentIndex := atomic.AddUint32(&counter, 1) - 1

				// Make sure we don't go beyond our pre-generated vector slice.
				if int(currentIndex) >= len(vectors) {
					continue
				}

				vectorID := fmt.Sprintf("vec-%d", currentIndex)
				vector := vectors[currentIndex]

				// This is the operation we are measuring.
				// Currently, this call is expected to cause significant contention
				// on the global index lock.
				_, err := idx.Add(vectorID, vector)
				if err != nil {
					b.Errorf("Error during concurrent insertion: %v", err)
				}
			}
		})
	})
}

// --- REPLACE the profiling test WITH THIS VERSION ---
func TestLargeBatchInsertionForProfiling(t *testing.T) {
	// --- Setup: data generation happens HERE, outside of profiling ---
	const (
		totalVectors = 100000
		vectorDim    = 100
	)
	objects := generateTestObjects(totalVectors, vectorDim)

	// --- Start of profiling ---
	cpuFile, err := os.Create("cpu.pprof")
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("could not start CPU profile: %v", err)
	}
	defer pprof.StopCPUProfile()

	// The only thing we measure is creation and insertion.
	idx, _ := New(16, 200, distance.Cosine, distance.Float32, "", "")
	err = idx.AddBatch(objects)
	if err != nil {
		t.Fatal(err)
	}

	// Profiling della memoria
	memFile, err := os.Create("mem.pprof")
	if err != nil {
		t.Fatal(err)
	}
	defer memFile.Close()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		t.Fatalf("could not write heap profile: %v", err)
	}
}

// --- REPLACE the benchmark WITH THIS VERSION ---
func BenchmarkConcurrentAddBatch(b *testing.B) {
	// --- Setup Unico (fuori dal loop di benchmark) ---
	const (
		totalVectors = 100000
		vectorDim    = 100
	)
	// Test data, including ID strings, is generated only once.
	objects := generateTestObjects(totalVectors, vectorDim)

	// --- Start of Benchmark ---
	b.ReportAllocs()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		// Stop timer only for creating the clean index.
		b.StopTimer()
		idx, _ := New(16, 200, distance.Cosine, distance.Float32, "", "")
		b.StartTimer()

		// --- Operation to Measure ---
		err := idx.AddBatch(objects)
		if err != nil {
			b.Fatalf("AddBatch failed: %v", err)
		}
	}
}
