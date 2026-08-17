package mmap

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestCompactorReadAfterCloseIsSafe reproduces the compactor-vs-arena-Close
// race deterministically:
//
//  1. The arena lock is held by the test (compaction reads block on va.mu.RLock).
//  2. arena.Close() is issued while the lock is held (queues as a writer).
//  3. The lock is released: Go's writer-preference hands it to Close first,
//     which munmaps every chunk. The compactor's read then resumes on
//     unmapped memory.
//
// Pre-fix this SIGSEGVs (unmapped chunk.Data read). Post-fix the chunk Data
// is nil-ed on close and the compactor skips it — the cycle exits cleanly.
func TestCompactorReadAfterCloseIsSafe(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("failed to create arena: %v", err)
	}

	// 100 vectors; free every other slot so each chunk mixes free and active
	// slots → the compactor finds vectors to relocate and reaches the
	// chunk.Data read. GetBytes materializes the chunk files.
	for i := 0; i < 100; i++ {
		if _, err := arena.AllocSlot(uint32(i)); err != nil {
			t.Fatalf("AllocSlot(%d): %v", i, err)
		}
		if _, err := arena.GetBytes(uint32(i)); err != nil {
			t.Fatalf("GetBytes(%d): %v", i, err)
		}
	}
	for i := 0; i < 100; i += 2 {
		arena.FreeSlot(uint32(i))
	}

	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0.0 // always compact
	cfg.BatchSize = 20
	cfg.BatchDelay = 0
	compactor := NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(&mockNodeUpdater{updatedIDs: &sync.Map{}})
	arena.compactor = compactor

	// Hold the arena lock, then queue Close as a writer FIRST: Go's RWMutex
	// sets writerPending, so the compactor's later RLock blocks behind it.
	arena.mu.Lock()

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- arena.Close()
	}()
	time.Sleep(50 * time.Millisecond) // Close is now queued as writer

	done := make(chan struct{})
	go func() {
		defer close(done)
		compactor.RunCycle()
	}()
	time.Sleep(100 * time.Millisecond) // compactor blocked on va.mu.RLock

	// Release the lock: Close (writer) wins over the compactor's reader,
	// unmaps every chunk, then the compactor resumes reading unmapped memory.
	// Post-fix the nil-ed Data makes the late read a safe no-op.
	arena.mu.Unlock()
	closeErr := <-closeDone

	select {
	case <-done:
		t.Logf("compactor cycle exited cleanly after arena close (close_err=%v)", closeErr)
	case <-time.After(5 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("compactor cycle did not finish after arena close\n%s", buf[:n])
	}
}
