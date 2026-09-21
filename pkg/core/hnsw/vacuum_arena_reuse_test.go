package hnsw

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/storage/mmap"
)

// TestVacuumFreesArenaSlots verifica Fase 3: dopo soft-delete + Vacuum,
// gli slot fisici sono liberati e riusati, l'arena non cresce.
func TestVacuumFreesArenaSlots(t *testing.T) {
	if testing.Short() {
		t.Skip("skip arena reuse test in -short")
	}
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const n = 200
	vectors := buildVectorPool(n, 128)
	for i, v := range vectors {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	before := idx.GetArenaState()
	if before.NextPhysSlot != uint32(n) {
		t.Fatalf("before vacuum: NextPhysSlot %d want %d", before.NextPhysSlot, n)
	}
	if len(before.FreeSlots) != 0 {
		t.Fatalf("before vacuum: freeSlots %d want 0", len(before.FreeSlots))
	}
	if got := len(before.SlotTable); got < n {
		t.Fatalf("slotTable len %d want >=%d", got, n)
	}

	// Soft-delete metà
	const toDelete = 100
	for i := 0; i < toDelete; i++ {
		idx.Delete(embeddingID(i))
	}
	mid := idx.GetArenaState()
	if len(mid.FreeSlots) != 0 {
		t.Fatalf("after soft-delete but before vacuum: freeSlots %d want 0 (vacuum not yet)", len(mid.FreeSlots))
	}

	// Vacuum deve liberare
	didWork := idx.optimizer.Vacuum()
	if !didWork {
		t.Fatalf("Vacuum returned false, want true")
	}
	afterVac := idx.GetArenaState()
	if len(afterVac.FreeSlots) != toDelete {
		t.Fatalf("after vacuum: freeSlots %d want %d", len(afterVac.FreeSlots), toDelete)
	}
	if afterVac.NextPhysSlot != before.NextPhysSlot {
		t.Fatalf("after vacuum: NextPhysSlot grew from %d to %d (should stay)", before.NextPhysSlot, afterVac.NextPhysSlot)
	}
	// Count UnallocatedSlot
	unalloc := 0
	for _, s := range afterVac.SlotTable {
		if s == mmap.UnallocatedSlot {
			unalloc++
		}
	}
	if unalloc < toDelete {
		t.Fatalf("after vacuum: UnallocatedSlot count %d want >=%d", unalloc, toDelete)
	}

	// Snapshot round-trip: ArenaState must survive
	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, _, _, dim := idx.SnapshotData()
	// Create fresh index and load snapshot data
	idx2, err := New(16, 200, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("New idx2: %v", err)
	}
	defer idx2.Close()
	if err := idx2.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, afterVac, dim); err != nil {
		t.Fatalf("LoadSnapshotData: %v", err)
	}

	// Reuse: add toDelete new vectors, should reuse freeSlots
	newVecs := buildVectorPool(toDelete, 128)
	for i := 0; i < toDelete; i++ {
		if _, err := idx.Add(embeddingID(n+i), newVecs[i]); err != nil {
			t.Fatalf("Add reuse %d: %v", i, err)
		}
	}
	afterReuse := idx.GetArenaState()
	if len(afterReuse.FreeSlots) != 0 {
		t.Fatalf("after reuse: freeSlots %d want 0 (should have been consumed)", len(afterReuse.FreeSlots))
	}
	if afterReuse.NextPhysSlot != before.NextPhysSlot {
		t.Fatalf("after reuse: NextPhysSlot %d want %d (should reuse, not grow)", afterReuse.NextPhysSlot, before.NextPhysSlot)
	}
	// Search should not return deleted IDs
	q := vectors[toDelete] // query near a live vector
	res := idx.SearchWithScores(q, 20, nil, 100)
	for _, r := range res {
		if r.DocID >= 1 && r.DocID <= uint32(toDelete) {
			t.Fatalf("search returned deleted DocID %d", r.DocID)
		}
	}
}

// TestVacuumArenaStatePreservedViaSnapshot verifies that ArenaState.freeSlots
// survives SnapshotData/LoadSnapshotData (persistence contract).
func TestVacuumArenaStatePreservedViaSnapshot(t *testing.T) {
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const n = 50
	vecs := buildVectorPool(n, 64)
	for i, v := range vecs {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	for i := 0; i < 20; i++ {
		idx.Delete(embeddingID(i))
	}
	if !idx.optimizer.Vacuum() {
		t.Fatalf("Vacuum false")
	}
	state := idx.GetArenaState()
	if len(state.FreeSlots) != 20 {
		t.Fatalf("freeSlots %d want 20", len(state.FreeSlots))
	}
	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, _, _, dim := idx.SnapshotData()
	idx2, err := New(16, 200, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("New idx2: %v", err)
	}
	defer idx2.Close()
	if err := idx2.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, state, dim); err != nil {
		t.Fatalf("LoadSnapshotData: %v", err)
	}
	state2 := idx2.GetArenaState()
	if len(state2.FreeSlots) != 20 {
		t.Fatalf("after load: freeSlots %d want 20", len(state2.FreeSlots))
	}
	if state2.NextPhysSlot != state.NextPhysSlot {
		t.Fatalf("after load: NextPhysSlot %d want %d", state2.NextPhysSlot, state.NextPhysSlot)
	}
}
