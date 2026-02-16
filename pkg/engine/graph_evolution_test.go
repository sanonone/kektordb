package engine

import (
	"testing"
	"time"
)

func TestGraphEvolution(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, _ := Open(opts)
	defer eng.Close()

	src := "Alice"
	tgt := "Project_X"
	rel := "member_of"

	// 1. Time T1: Create Initial Link (Role: Junior)
	props1 := map[string]any{"role": "junior"}
	eng.VLink(src, tgt, rel, "", 1.0, props1)

	// Wait a bit to ensure timestamps differ and capture a valid "past" time
	time.Sleep(2 * time.Millisecond)
	t1 := time.Now().UnixNano()

	// Wait a bit to ensure timestamps differ
	time.Sleep(2 * time.Millisecond)

	// 2. Time T2: Evolve Link (Role: Senior)
	// We don't need to store T2 variable if we query with 0 (Now)
	props2 := map[string]any{"role": "senior"}
	eng.VLink(src, tgt, rel, "", 1.0, props2)

	// Wait a bit
	time.Sleep(2 * time.Millisecond)

	// --- VERIFICA ---

	// A. Check Present (should be Senior)
	edgesNow, _ := eng.VGetEdges(src, rel, 0)
	if len(edgesNow) != 1 {
		t.Fatalf("Expected 1 active edge now, got %d", len(edgesNow))
	}
	if edgesNow[0].Props["role"] != "senior" {
		t.Errorf("Evolution failed. Expected 'senior', got %v", edgesNow[0].Props["role"])
	}

	// B. Check Past (Time Travel to T1, before the update)
	// We use t1 directly (exact timestamp of creation) or t1+1
	queryTime := t1

	edgesPast, _ := eng.VGetEdges(src, rel, queryTime)
	if len(edgesPast) != 1 {
		t.Fatalf("Time travel failed. Expected 1 edge in past, got %d", len(edgesPast))
	}
	if edgesPast[0].Props["role"] != "junior" {
		t.Errorf("Time travel history lost. Expected 'junior', got %v", edgesPast[0].Props["role"])
	}

	// C. Check Idempotency (Update with SAME props)
	// Should NOT create a new version
	eng.VLink(src, tgt, rel, "", 1.0, props2) // Same as T2

	edgesAfterNoOp, _ := eng.VGetEdges(src, rel, 0)

	// Verify that the CreatedAt timestamp hasn't changed (it's still the one from T2)
	if edgesAfterNoOp[0].CreatedAt != edgesNow[0].CreatedAt {
		t.Error("Idempotency failed. A new edge was created despite identical props.")
	}
}
