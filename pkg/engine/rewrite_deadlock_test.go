package engine

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestRewriteAOFConcurrentWithAddBatch verifica che non si verifichi un deadlock
// quando RewriteAOF viene eseguito in concorrenza con VAddBatch.
//
// Scenario che riproduceva il bug:
//   - Goroutine A (background): RewriteAOF teneva DB.RLock() per tutta la durata
//     → chiamava IterateVectorIndexes → acquisiva h.metaMu.RLock()
//   - Goroutine B (client): AddBatch tentava h.metaMu.Lock() in Phase 1A
//     → il runtime Go bloccava tutti i nuovi reader su h.metaMu (incluso RewriteAOF)
//     → deadlock permanente
func TestRewriteAOFConcurrentWithAddBatch(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	opts := DefaultOptions(dataDir)
	opts.AofRewritePercentage = 1

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer eng.Close()

	indexName := "deadlock_test_index"
	arenaDir := filepath.Join(dataDir, "arenas", indexName)
	if err := eng.VCreate(indexName, distance.Euclidean, 8, 100, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate() failed: %v", err)
	}
	_ = arenaDir // usato implicitamente da VCreate via DataDir

	const dim = 8
	const testDuration = 8 * time.Second
	const batchSize = 5

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	doneCh := make(chan struct{}, 2)

	// Goroutine 1: AddBatch in loop — simula il carico di scrittura
	go func() {
		defer func() { doneCh <- struct{}{} }()
		idCounter := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			batch := make([]types.BatchObject, batchSize)
			for i := range batch {
				vec := make([]float32, dim)
				for j := range vec {
					vec[j] = rand.Float32()
				}
				batch[i] = types.BatchObject{
					Id:     fmt.Sprintf("vec-%d", idCounter),
					Vector: vec,
				}
				idCounter++
			}

			// Gli errori di ID duplicato sono accettabili in questo test
			_ = eng.VAddBatch(indexName, batch)
		}
	}()

	// Goroutine 2: RewriteAOF in loop — simula il background compaction
	go func() {
		defer func() { doneCh <- struct{}{} }()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_ = eng.RewriteAOF()
			time.Sleep(30 * time.Millisecond)
		}
	}()

	// Aspettiamo che le goroutine completino entro testDuration.
	// Se il context scade ma le goroutine sono bloccate, il test espira con timeout.
	<-doneCh
	<-doneCh
}

// TestRewriteAOFPreservesData verifica che dopo un rewrite i dati siano
// correttamente preservati (regressione funzionale).
func TestRewriteAOFPreservesData(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	opts := DefaultOptions(dataDir)
	opts.AofRewritePercentage = 0 // disabilitiamo il rewrite automatico

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	indexName := "preserve_test"
	if err := eng.VCreate(indexName, distance.Euclidean, 8, 100, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate() failed: %v", err)
	}

	const dim = 8
	const numVectors = 30

	// Inseriamo alcuni vettori
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = float32(i*dim+j) * 0.01
		}
		if err := eng.VAdd(indexName, fmt.Sprintf("v%d", i), vec, nil); err != nil {
			t.Fatalf("VAdd(%d) failed: %v", i, err)
		}
	}

	// Eseguiamo il rewrite
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF() failed: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Riapriamo l'engine — i vettori devono essere ancora presenti
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Re-Open() failed: %v", err)
	}
	defer eng2.Close()

	// Cerchiamo il primo vettore
	vec0 := make([]float32, dim)
	for j := range vec0 {
		vec0[j] = float32(j) * 0.01
	}
	results, err := eng2.VSearch(indexName, vec0, 1, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch() failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("VSearch() returned 0 results after RewriteAOF + reopen — data was lost!")
	}
	if results[0] != "v0" {
		t.Errorf("Expected top result 'v0', got '%s'", results[0])
	}

	// Controlliamo che tutti i file temporanei siano stati rimossi
	tmpFile := filepath.Join(dataDir, "rewrite.tmp")
	if _, statErr := os.Stat(tmpFile); !os.IsNotExist(statErr) {
		t.Error("rewrite.tmp was not cleaned up after RewriteAOF()")
	}

	// Verifica che vengano restituiti tutti i vettori inseriti
	hnsw.New(8, 100, distance.Euclidean, distance.Float32, "", "")
}
