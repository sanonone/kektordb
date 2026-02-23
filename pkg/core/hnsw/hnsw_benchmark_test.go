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
	BenchNumItems = 10000 // Elementi di base per il test misto
)

func randomVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := 0; i < dim; i++ {
		v[i] = rand.Float32()
	}
	return v
}

// 1. Benchmark Solo Scrittura (VAdd concorrente)
func BenchmarkConcurrentWrite(b *testing.B) {
	// Setup: Creiamo i vettori prima per non misurare la generazione random
	vectors := make([][]float32, b.N)
	for i := 0; i < b.N; i++ {
		vectors[i] = randomVector(BenchDim)
	}

	index, _ := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")
	var counter int64

	b.ResetTimer()

	// Esegue Add in parallelo su GOMAXPROCS goroutine
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := atomic.AddInt64(&counter, 1) - 1
			id := fmt.Sprintf("vec-%d", i)
			// Qui testiamo la contenzione del lock in VAdd
			index.Add(id, vectors[i%int64(len(vectors))])
		}
	})
}

// 2. Benchmark Misto (Lettura e Scrittura estreme)
// Simula un carico reale: mentre inseriamo, cerchiamo.
// Con il vecchio Global Lock, questo test dovrebbe essere lentissimo.
// Con i Shard Lock, dovrebbe volare.
func BenchmarkMixedReadWrite(b *testing.B) {
	// Setup iniziale
	index, _ := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")

	// Popoliamo un po' l'indice inizialmente
	initialObjs := make([]types.BatchObject, 1000)
	for i := 0; i < 1000; i++ {
		initialObjs[i] = types.BatchObject{
			Id:     fmt.Sprintf("init-%d", i),
			Vector: randomVector(BenchDim),
		}
	}
	index.AddBatch(initialObjs)

	var writeCounter int64
	queryVec := randomVector(BenchDim)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		// Decidiamo casualmente se leggere o scrivere per simulare traffico misto
		// Usiamo un counter locale o random per evitare lock su rand
		// Qui alterniamo semplicemente.
		isWriter := false

		for pb.Next() {
			isWriter = !isWriter // 50% Read, 50% Write

			if isWriter {
				i := atomic.AddInt64(&writeCounter, 1)
				index.Add(fmt.Sprintf("new-%d", i), randomVector(BenchDim))
			} else {
				// Search deve acquisire RLockNode (shardato)
				index.SearchWithScores(queryVec, 10, nil, 100)
			}
		}
	})
}
