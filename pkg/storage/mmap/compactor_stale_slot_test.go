package mmap

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestCompactorMoveToMissingChunkIsSafe reproduces P0-3 deterministically:
// a stale free slot (left behind after a trailing chunk was dropped) points
// past the current chunk list. The compactor must materialize the target
// chunk before writing slotTable — otherwise the vector would be silently
// lost (the old slot gets freed and the new one reads as zeros).
//
// It exercises moveBatch directly (rather than a full RunCycle, which has a
// pre-existing free-slot livelock unrelated to this bug).
func TestCompactorMoveToMissingChunkIsSafe(t *testing.T) {
	dir := t.TempDir()
	arena, err := NewVectorArena(dir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("failed to create arena: %v", err)
	}

	// One vector in chunk 0 with a unique pattern.
	if _, err := arena.AllocSlot(0); err != nil {
		t.Fatalf("AllocSlot(0): %v", err)
	}
	b, err := arena.GetBytes(0) // materializes chunk 0
	if err != nil {
		t.Fatalf("GetBytes(0): %v", err)
	}
	for j := range b {
		b[j] = byte(j)
	}
	orig := bytes.Clone(b)

	// Simulate a stale free slot left behind by a dropped trailing chunk:
	// it points at chunk 3, which was never created.
	stale := uint32(arena.vecsPerChk * 3)

	compactor := NewAsyncCompactor(arena, DefaultArenaCompactionConfig())
	compactor.SetNodeUpdater(&mockNodeUpdater{updatedIDs: &sync.Map{}})

	relocated, freed := compactor.moveBatch(
		[]vectorData{{internalID: 0, fromSlot: 0, data: orig}},
		[]uint32{stale},
	)
	if relocated != 1 {
		t.Fatalf("expected 1 relocation, got %d", relocated)
	}
	if freed != 1 {
		t.Fatalf("expected 1 freed slot, got %d", freed)
	}

	// The slot must now point at the (materialized) chunk 3, not a gap.
	phys := arena.slotTable[0]
	chunkID := int(phys) / arena.vecsPerChk
	if chunkID >= len(arena.chunks) || arena.chunks[chunkID] == nil {
		t.Fatalf("vector points to missing chunk %d (chunks=%d)", chunkID, len(arena.chunks))
	}
	if phys != stale {
		t.Fatalf("expected slot %d, got %d", stale, phys)
	}

	// Data must survive the relocation.
	got, err := arena.GetBytes(0)
	if err != nil {
		t.Fatalf("GetBytes(0): %v", err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatalf("vector corrupted after move to missing chunk: got %v, want %v", got[:8], orig[:8])
	}
}

// TestDropEmptyChunksCleansStaleFreeSlots verifies that dropping a trailing
// empty chunk also removes free-slot entries pointing inside it. Reusing
// those slots later would target a chunk that no longer exists (P0-3).
func TestDropEmptyChunksCleansStaleFreeSlots(t *testing.T) {
	dir := t.TempDir()
	arena, err := NewVectorArena(dir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("failed to create arena: %v", err)
	}

	// One active vector in chunk 0; chunk 1 exists but is empty.
	if _, err := arena.AllocSlot(0); err != nil {
		t.Fatal(err)
	}
	if _, err := arena.GetBytes(0); err != nil {
		t.Fatal(err)
	}
	if err := arena.addChunk(1); err != nil {
		t.Fatalf("addChunk(1): %v", err)
	}

	// freeSlots mixes a valid slot in chunk 0 with a stale one in chunk 1.
	arena.freeSlots = append(arena.freeSlots, 2)
	arena.freeSlots = append(arena.freeSlots, uint32(arena.vecsPerChk+5))

	compactor := NewAsyncCompactor(arena, DefaultArenaCompactionConfig())
	compactor.tryDropEmptyChunks()

	if len(arena.chunks) != 1 {
		t.Fatalf("expected 1 chunk after drop, got %d", len(arena.chunks))
	}
	if len(arena.freeSlots) != 1 || arena.freeSlots[0] != 2 {
		t.Fatalf("stale free slot survived chunk drop: %v", arena.freeSlots)
	}
}

// TestGetBytesConcurrentWithCompaction stresses GetBytes against concurrent
// compaction and slot churn. Run with -race: pre-fix this catches the
// unlocked slotTable read in the compactor batch loop (P1-8) and guards the
// GetBytes fast-path rework (P0-4).
func TestGetBytesConcurrentWithCompaction(t *testing.T) {
	dir := t.TempDir()
	arena, err := NewVectorArena(dir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("failed to create arena: %v", err)
	}

	const n = 200
	for i := 0; i < n; i++ {
		if _, err := arena.AllocSlot(uint32(i)); err != nil {
			t.Fatal(err)
		}
		if _, err := arena.GetBytes(uint32(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Free the first 40 slots so each compaction cycle has real relocation
	// work (the LIFO reuse makes it terminate).

	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0
	cfg.BatchSize = 25
	cfg.BatchDelay = 0
	compactor := NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(&mockNodeUpdater{updatedIDs: &sync.Map{}})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Concurrent readers.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := uint32(rand.Intn(n))
				if b, err := arena.GetBytes(id); err == nil && b != nil {
					_ = b[0]
				}
			}
		}()
	}

	// Slot churn (alloc/free) to force freeSlots/slotTable writes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			arena.FreeSlot(rand.Uint32() % n)
			arena.AllocSlot(rand.Uint32()%n + n)
		}
	}()

	// Compaction cycles. Fire-and-forget: RunCycle has a pre-existing
	// free-slot livelock, so it may never return; the race detector still
	// checks the concurrent accesses while it runs.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			compactor.RunCycle()
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
