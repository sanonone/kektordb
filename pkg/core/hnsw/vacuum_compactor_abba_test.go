package hnsw

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// --- Regressione del deadlock ABBA Vacuum ↔ compactor arena ---
//
// Ordini di lock incompatibili:
//
//	compactor moveBatch:  arena.slotMu → shard (via UpdateNodePointer)
//	Vacuum Phase 4:       shard (LockNode) → arena.slotMu (GetBytes/FreeSlot)
//
// Il ciclo diventa raggiungibile solo quando freeSlots è non vuoto, cosa che
// Fase 3 (FreeSlot in Vacuum) rende possibile. La serializzazione corretta è
// il MaintenanceCoordinator: AcquireCompactionLock in Vacuum (bloccante) e
// TryAcquireCompactionLock nel compactor (salta il ciclo se occupato).

// TestVacuumBlocksWhileCompactionLockHeld verifica deterministicamente la
// serializzazione: se il lock di compaction è occupato, Vacuum non deve mai
// procedere (nemmeno parzialmente) finché non viene rilasciato.
func TestVacuumBlocksWhileCompactionLockHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("skip lock-serialization test in -short")
	}

	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	vecs := buildVectorPool(50, 32)
	for i, v := range vecs {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	for i := 0; i < 25; i++ {
		idx.Delete(embeddingID(i))
	}

	if idx.maintenanceCoord == nil {
		t.Fatal("maintenanceCoord is nil: the serialization hook is not wired")
	}

	// Simula il compactor che sta girando: prende il lock di compaction.
	idx.maintenanceCoord.AcquireCompactionLock()

	vacDone := make(chan struct{})
	go func() {
		idx.optimizer.Vacuum()
		close(vacDone)
	}()

	// Vacuum deve restare bloccato: nessuna fase può partire.
	select {
	case <-vacDone:
		idx.maintenanceCoord.ReleaseCompactionLock()
		t.Fatal("Vacuum completed while the compaction lock was held: ABBA serialization missing")
	case <-time.After(500 * time.Millisecond):
		// atteso: bloccato
	}

	// Rilasciando il lock, Vacuum deve ripartire e completare.
	idx.maintenanceCoord.ReleaseCompactionLock()

	select {
	case <-vacDone:
		// ok
	case <-time.After(30 * time.Second):
		t.Fatal("Vacuum did not complete after the compaction lock was released")
	}

	// Il lavoro dev'essere stato fatto davvero (slot liberati).
	state := idx.GetArenaState()
	if len(state.FreeSlots) != 25 {
		t.Fatalf("freeSlots after Vacuum = %d, want 25", len(state.FreeSlots))
	}
}

// TestVacuumVsCompactorStress esegue Vacuum, Add e il compactor reale in
// parallelo. È un test di stress (la finestra di interleaving dell'ABBA è
// stretta): protegge contro regressioni future, ma la garanzia deterministica
// è data dal test sopra.
func TestVacuumVsCompactorStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skip compactor stress test in -short")
	}

	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const n = 600
	vecs := buildVectorPool(n, 64)
	for i, v := range vecs {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	// Profilo di frammentazione: buchi + vivi dopo i buchi.
	for i := 0; i < n/3; i++ {
		idx.Delete(embeddingID(i))
	}
	idx.optimizer.Vacuum()
	extra := buildVectorPool(n, 64)
	for i := 0; i < n/3; i++ {
		if _, err := idx.Add(embeddingID(n+i), extra[i]); err != nil {
			t.Fatalf("Add extra %d: %v", i, err)
		}
	}
	for i := n / 3; i < 2*n/3; i++ {
		idx.Delete(embeddingID(i))
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Vacuum loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			select {
			case <-stop:
				return
			default:
			}
			idx.optimizer.Vacuum()
		}
	}()

	// Writer loop (mantiene vivo il grafo e crea nuovi buchi)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := embeddingID(100000 + i)
			if _, err := idx.Add(id, vecs[i%n]); err == nil {
				idx.Delete(id)
			}
		}
	}()

	// Reader loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = idx.SearchWithScores(vecs[i%n], 10, nil, 50)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("DEADLOCK/HANG: Vacuum + Add + Search did not finish within 60s")
	}
	close(stop)
}
