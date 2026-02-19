package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestConcurrencyChaos simula un ambiente ostile con multipli attori.
// Deve essere eseguito con: go test -race
func TestConcurrencyChaos(t *testing.T) {
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "")
	if err != nil {
		t.Fatal(err)
	}

	// Configurazione Chaos
	const (
		NumSingleWriters = 10 // Utenti che fanno VAdd singole
		NumBatchWriters  = 2  // Utenti che fanno VAddBatch (massivo)
		NumReaders       = 20 // Utenti che cercano (Search)
		TestDuration     = 3 * time.Second
		VectorDim        = 64
	)

	var wg sync.WaitGroup
	ctxDone := make(chan struct{})

	// Timer per fermare il test
	go func() {
		time.Sleep(TestDuration)
		close(ctxDone)
	}()

	// --- ATTORE 1: Single Writers (VAdd) ---
	for i := 0; i < NumSingleWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-ctxDone:
					return
				default:
					vecID := fmt.Sprintf("single-%d-%d", id, counter)
					_, err := idx.Add(vecID, randomVector(VectorDim))
					if err != nil {
						t.Errorf("Single Add Error: %v", err)
					}
					counter++
					// Piccola pausa random per variare il carico
					time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
				}
			}
		}(i)
	}

	// --- ATTORE 2: Batch Writers (AddBatch) ---
	// AddBatch usa una logica di lock diversa (Map-Reduce).
	// Testiamo se confligge con VAdd (Single) e Search.
	for i := 0; i < NumBatchWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-ctxDone:
					return
				default:
					// Batch di 50 elementi
					batch := make([]types.BatchObject, 50)
					for j := 0; j < 50; j++ {
						batch[j] = types.BatchObject{
							Id:     fmt.Sprintf("batch-%d-%d-%d", id, counter, j),
							Vector: randomVector(VectorDim),
						}
					}

					err := idx.AddBatch(batch)
					if err != nil {
						t.Errorf("Batch Add Error: %v", err)
					}
					counter++
					time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
				}
			}
		}(i)
	}

	// --- ATTORE 3: Readers (SearchWithScores) ---
	// Questi stressano il Read Path (RLockNode).
	for i := 0; i < NumReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := randomVector(VectorDim)
			for {
				select {
				case <-ctxDone:
					return
				default:
					// Cerchiamo K=20
					res := idx.SearchWithScores(query, 20, nil, 100)
					// Basic sanity check
					if len(res) > 20 {
						t.Errorf("Search returned too many results")
					}
					// No sleep here, hammer the reads!
				}
			}
		}()
	}

	// Attendiamo la fine
	wg.Wait()

	// Validazione finale
	infoMetric, m, ef, _, count, _ := idx.GetInfo()
	t.Logf("Chaos Test Completed Successfully.")
	t.Logf("Final Index State: Count=%d, M=%d, Ef=%d, Metric=%s", count, m, ef, infoMetric)

	if count == 0 {
		t.Error("Index is empty after chaos test, something blocked insertions.")
	}
}
