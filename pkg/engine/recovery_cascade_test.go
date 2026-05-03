package engine

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// TestVDELRecoveryCleansEdges verifies that VDEL during AOF replay cascades
// graph edge cleanup even when GUNLINK commands are missing from the AOF.
// Regression test for H9: crash mid-VDelete leaves dead links after recovery.
func TestVDELRecoveryCleansEdges(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	indexName := "test_vdel_cascade"

	// Step 1: Create engine, add vectors, then close (AOF has VCREATE + VADD)
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	err = eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	_ = eng.VAdd(indexName, "node_a", vec, map[string]any{"content": "a"})
	_ = eng.VAdd(indexName, "node_b", vec, map[string]any{"content": "b"})
	_ = eng.VAdd(indexName, "node_c", vec, map[string]any{"content": "c"})
	eng.Close()

	// Step 2: Reopen engine and directly create graph edges without GLINK AOF commands
	eng2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}

	graphIDA := buildGraphID(indexName, "node_a")
	graphIDB := buildGraphID(indexName, "node_b")
	graphIDC := buildGraphID(indexName, "node_c")

	eng2.DB.AddEdge(graphIDA, graphIDB, "refers_to", 1.0, nil, 1)
	eng2.DB.AddEdge(graphIDC, graphIDB, "mentions", 1.0, nil, 2)

	incoming := eng2.DB.GetAllRelations(graphIDB, "in")
	if len(incoming) != 2 {
		t.Fatalf("expected 2 incoming edges to node_b, got %d", len(incoming))
	}

	// Step 3: Write VDEL to AOF without cascade GUNLINKs (simulating crash mid-cascade)
	cmd := persistence.FormatCommand("VDEL", []byte(indexName), []byte("node_b"))
	if err := eng2.AOF.Write(cmd); err != nil {
		t.Fatal(err)
	}
	if err := eng2.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	eng2.Close()

	// Step 4: Re-open — recovery replays AOF, VDEL handler cascades edge cleanup
	eng3, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng3.Close()

	incoming3 := eng3.DB.GetAllRelations(graphIDB, "in")
	if len(incoming3) > 0 {
		t.Errorf("expected 0 incoming edges to node_b after recovery cascade, got %d", len(incoming3))
		for relType, sources := range incoming3 {
			for _, src := range sources {
				t.Logf("  dangling edge: %s -[%s]-> node_b", src, relType)
			}
		}
	}

	_, err = eng3.VGet(indexName, "node_b")
	if err == nil {
		t.Error("node_b should not be retrievable after VDEL")
	} else {
		t.Logf("node_b correctly not found: %v", err)
	}

	// Verify other nodes still intact
	if _, err := eng3.VGet(indexName, "node_a"); err != nil {
		t.Error("node_a should still exist")
	}
	if _, err := eng3.VGet(indexName, "node_c"); err != nil {
		t.Error("node_c should still exist")
	}
}

// TestVDELRecoveryEmptyIncoming verifies no-op when deleted node has no incoming edges.
func TestVDELRecoveryEmptyIncoming(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	indexName := "test_vdel_noedges"

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	err = eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	_ = eng.VAdd(indexName, "isolated", vec, map[string]any{"content": "no edges"})
	_ = eng.VAdd(indexName, "other", vec, map[string]any{"content": "other"})

	// Write VDEL without cascade (node has no incoming edges)
	cmd := persistence.FormatCommand("VDEL", []byte(indexName), []byte("isolated"))
	if err := eng.AOF.Write(cmd); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	eng2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	graphID := buildGraphID(indexName, "isolated")
	incoming := eng2.DB.GetAllRelations(graphID, "in")
	if len(incoming) != 0 {
		t.Errorf("expected 0 incoming edges, got %d", len(incoming))
	}

	_, err = eng2.VGet(indexName, "other")
	if err != nil {
		t.Errorf("other node should still exist: %v", err)
	}

	t.Log("VDEL recovery with no incoming edges handled correctly")
}

// TestVDELRuntimeCascade verifies the full VDelete cycle works end-to-end
// with close/re-open. The cascade goroutine writes GUNLINKs, and recovery
// replays them (happy path). Edge cleanup is verified.
func TestVDELRuntimeCascade(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	indexName := "test_vdel_runtime"

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	err = eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	vec := []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	_ = eng.VAdd(indexName, "src", vec, map[string]any{"content": "source"})
	_ = eng.VAdd(indexName, "tgt", vec, map[string]any{"content": "target"})

	// Create edge src→tgt directly in DB (avoids GLINK AOF format issue)
	graphID := buildGraphID(indexName, "tgt")
	eng.DB.AddEdge(buildGraphID(indexName, "src"), graphID, "knows", 1.0, nil, 1)

	incoming := eng.DB.GetAllRelations(graphID, "in")
	if len(incoming) != 1 {
		t.Fatalf("expected 1 incoming edge, got %d", len(incoming))
	}

	// Runtime VDelete — writes VDEL to AOF and starts cascade goroutine
	err = eng.VDelete(indexName, "tgt")
	if err != nil {
		t.Fatal(err)
	}

	// Close waits for cascade goroutine to complete (wg.Wait)
	eng.Close()

	// Re-open — recovery replays AOF (VDEL + cascade GUNLINKs should be present)
	eng2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	// Verify edges were cleaned up
	incoming2 := eng2.DB.GetAllRelations(graphID, "in")
	if len(incoming2) > 0 {
		t.Errorf("expected 0 incoming edges after VDelete + recovery, got %d", len(incoming2))
	}
}
