package hnsw

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestCloseTimeoutDoesNotUnmapArena reproduces P1-2: when Close times out
// waiting for in-flight operations, it used to ForceClose (munmap) the arena
// while those operations may still hold mmap-backed slices — a use-after-unmap
// SIGSEGV once they resume. Post-fix the arena is left mapped on timeout.
func TestCloseTimeoutDoesNotUnmapArena(t *testing.T) {
	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const dim = 32
	for i := 0; i < 50; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = float32(i*7 + j)
		}
		if _, err := idx.Add(string(rune('a'+i%26))+string(rune('0'+i/26)), vec); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	// Keep a "stuck" in-flight reader: it holds activeMu.RLock and a vector
	// slice that points into the arena mapping.
	idx.activeMu.RLock()
	defer idx.activeMu.RUnlock()

	// Shorten the drain timeout so the test completes quickly.
	idx.closeTimeout = 100 * time.Millisecond

	closeErr := idx.Close()
	if closeErr == nil {
		t.Fatal("expected Close to time out, got nil error")
	}

	// The arena must still be mapped and readable: any slice obtained before
	// Close must not fault, and GetBytes must still work.
	if idx.arena == nil {
		t.Fatal("arena was closed despite timed-out drain (P1-2)")
	}
	if internalID, ok := idx.GetInternalID("a0"); !ok {
		t.Fatal("first vector missing")
	} else if _, err := idx.arena.GetBytes(internalID); err != nil {
		t.Fatalf("arena unmapped after timed-out Close: %v", err)
	}
}
