package hnsw

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// Configurazione Benchmark
const (
	BenchDim      = 128
	BenchM        = 16
	BenchEf       = 200
	BenchNumItems = 10000   // Elementi di base per il test misto
	benchPoolSize = 100_000 // Fixed pool size — does NOT scale with b.N
)

func randomVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := 0; i < dim; i++ {
		v[i] = rand.Float32()
	}
	return v
}

// buildVectorPool generates a fixed-size pool of random vectors.
// Using a pool instead of b.N vectors prevents O(b.N) setup cost
// that causes benchmarks to run far longer than -benchtime specifies.
func buildVectorPool(n, dim int) [][]float32 {
	pool := make([][]float32, n)
	for i := range pool {
		pool[i] = randomVector(dim)
	}
	return pool
}

// 1. Benchmark Solo Scrittura (VAdd concorrente)
func BenchmarkConcurrentWrite(b *testing.B) {
	// Fixed pool: setup is O(poolSize), not O(b.N).
	// b.N can reach millions; allocating b.N vectors causes 500MB+ setup overhead
	// that makes -benchtime=10s run for minutes instead of seconds.
	vectors := buildVectorPool(benchPoolSize, BenchDim)

	index, _ := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")
	var counter int64

	b.ResetTimer()

	// Esegue Add in parallelo su GOMAXPROCS goroutine
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := atomic.AddInt64(&counter, 1) - 1
			id := fmt.Sprintf("vec-%d", i)
			index.Add(id, vectors[i%int64(benchPoolSize)])
		}
	})
}

// 2. Benchmark Misto (Lettura e Scrittura estreme)
// Simula un carico reale: mentre inseriamo, cerchiamo.
// Con il vecchio Global Lock, questo test dovrebbe essere lentissimo.
// Con i Shard Lock, dovrebbe volare.
func BenchmarkMixedReadWrite(b *testing.B) {
	// Fixed pool: avoids O(b.N) allocation and global rand mutex contention
	// inside the hot loop.
	vectors := buildVectorPool(benchPoolSize, BenchDim)

	index, _ := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")

	// Popoliamo un po' l'indice inizialmente
	initialObjs := make([]types.BatchObject, 1000)
	for i := 0; i < 1000; i++ {
		initialObjs[i] = types.BatchObject{
			Id:     fmt.Sprintf("init-%d", i),
			Vector: vectors[i],
		}
	}
	index.AddBatch(initialObjs)

	var writeCounter int64
	queryVec := vectors[0]

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		isWriter := false

		for pb.Next() {
			isWriter = !isWriter // 50% Read, 50% Write

			if isWriter {
				i := atomic.AddInt64(&writeCounter, 1)
				index.Add(fmt.Sprintf("new-%d", i), vectors[i%int64(benchPoolSize)])
			} else {
				index.SearchWithScores(queryVec, 10, nil, 100)
			}
		}
	})
}
