package mmap

import (
	"sync"
	"testing"
	"time"
)

// TestFindFreeSlotsReturnsCopy covers a slice that aliased the internal free
// list. The compactor calls FindFreeSlots, drops the lock, then writes vectors
// into the returned slots. While it does, a concurrent FreeSlot appends to
// va.freeSlots — and because both share a backing array, the append overwrites
// the slots the compactor is about to use.
func TestFindFreeSlotsReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	va, err := NewVectorArena(dir, 16, 4, PrecFloat32) // vectorSize=16 bytes
	if err != nil {
		t.Fatalf("NewVectorArena: %v", err)
	}
	defer va.Close()

	// The alias only bites when the free list has spare capacity: slicing from
	// the tail of a full slice yields cap == len, so an append reallocates. Free
	// slot ids accumulate over a run, so cap > len is the normal steady state.
	va.slotMu.Lock()
	va.freeSlots = make([]uint32, 5, 20)
	for i := range va.freeSlots {
		va.freeSlots[i] = uint32(100 + i)
	}
	va.slotMu.Unlock()

	got := va.FindFreeSlots(2)
	if len(got) != 2 {
		t.Fatalf("got %d slots, want 2", len(got))
	}
	snapshot := append([]uint32(nil), got...)

	// A concurrent FreeSlot appends to the internal list. With an aliased
	// result that append writes into the backing array the caller still holds.
	va.slotMu.Lock()
	va.freeSlots = append(va.freeSlots, 999, 998)
	va.slotMu.Unlock()

	for i := range snapshot {
		if got[i] != snapshot[i] {
			t.Fatalf("slot %d changed from %d to %d after an unrelated free: "+
				"the returned slice aliases the internal free list", i, snapshot[i], got[i])
		}
	}
}

// TestFindFreeSlotsPartialBranchIsCopied covers the second branch: taking all
// free slots and allocating the remainder.
func TestFindFreeSlotsPartialBranchIsCopied(t *testing.T) {
	dir := t.TempDir()
	va, err := NewVectorArena(dir, 16, 4, PrecFloat32)
	if err != nil {
		t.Fatalf("NewVectorArena: %v", err)
	}
	defer va.Close()

	// Few free slots, with spare capacity, and a request larger than the list:
	// the "take all + grow" branch.
	va.slotMu.Lock()
	va.freeSlots = make([]uint32, 2, 16)
	va.freeSlots[0], va.freeSlots[1] = 200, 201
	va.slotMu.Unlock()

	got := va.FindFreeSlots(5)
	if len(got) != 5 {
		t.Fatalf("got %d slots, want 5", len(got))
	}
	snapshot := append([]uint32(nil), got...)

	va.slotMu.Lock()
	va.freeSlots = append(va.freeSlots, 777)
	va.slotMu.Unlock()

	for i := range snapshot {
		if got[i] != snapshot[i] {
			t.Fatalf("slot %d changed from %d to %d after an unrelated free", i, snapshot[i], got[i])
		}
	}
}

// TestChunkStatsLockOrderDoesNotDeadlock exercises the stats path against the
// compactor while a writer runs.
//
// chunkFragmentation (called by GetArenaStats) takes slotMu then mu;
// getChunkStats used to take mu then slotMu. That inversion is a real ABBA, but
// it only closes when a writer is queued on one of the two mutexes at the wrong
// moment, which is not reproducible on demand: this test would have passed
// against the inverted order too. It is kept as a smoke test — it would catch a
// hang on a platform where the scheduler interleaves more aggressively — and the
// real guarantee is the documented lock order plus the code review of
// getChunkStats, which now matches chunkFragmentation.
func TestChunkStatsLockOrderDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	va, err := NewVectorArena(dir, 16, 4, PrecFloat32)
	if err != nil {
		t.Fatalf("NewVectorArena: %v", err)
	}
	defer va.Close()

	ac := NewAsyncCompactor(va, DefaultArenaCompactionConfig())
	// Some data so the loops do real work inside the locks.
	for i := 0; i < 20; i++ {
		slots := va.FindFreeSlots(1)
		for _, s := range slots {
			va.AllocSlot(s)
		}
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = va.chunkFragmentation()
			}
		}
	}()

	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = ac.getChunkStats()
			}
		}
	}()

	// A writer is needed to make the inversion bite: RLock does not block RLock,
	// so two readers never deadlock. With a Lock pending, the second RLock on the
	// same mutex queues behind it, and the opposite order closes the cycle.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				slot := uint32(500 + int(time.Now().UnixNano()%100))
				va.slotMu.Lock()
				va.freeSlots = append(va.freeSlots, slot)
				va.slotMu.Unlock()
				va.mu.Lock()
				_ = len(va.chunks)
				va.mu.Unlock()
				time.Sleep(time.Microsecond)
			}
		}
	}()

	finished := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(done)
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		// Both loops exited: no deadlock.
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: chunkFragmentation and getChunkStats acquired slotMu and mu " +
			"in opposite orders")
	}
}
