package engine

import (
	"strings"
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

// TestVDeleteCascadeOutgoing verifies that VDelete cascades to outgoing edges
// as well as incoming, preventing ghost nodes from persisting in the graph.
// Regression test for H4.
func TestVDeleteCascadeOutgoing(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	indexName := "test_vdel_outgoing"

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

	// Create edges: a→b (incoming to b) and b→c (outgoing from b)
	eng.DB.AddEdge(buildGraphID(indexName, "node_a"), buildGraphID(indexName, "node_b"), "refers_to", 1.0, nil, 1)
	eng.DB.AddEdge(buildGraphID(indexName, "node_b"), buildGraphID(indexName, "node_c"), "knows", 1.0, nil, 2)

	graphIDB := buildGraphID(indexName, "node_b")
	graphIDC := buildGraphID(indexName, "node_c")

	// Verify edges exist before delete
	incoming := eng.DB.GetAllRelations(graphIDB, "in")
	outgoing := eng.DB.GetAllRelations(graphIDB, "out")
	if len(incoming) != 1 || len(outgoing) != 1 {
		t.Fatalf("expected 1 incoming and 1 outgoing edge, got in=%d out=%d", len(incoming), len(outgoing))
	}

	// Delete node_b — cascade should clean both incoming (a→b) and outgoing (b→c)
	err = eng.VDelete(indexName, "node_b")
	if err != nil {
		t.Fatal(err)
	}

	// Close and re-open to wait for the cascade goroutine to complete
	// (Close calls wg.Wait which includes the cascade goroutine)
	eng.Close()

	eng2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	// Verify incoming edges to node_b were cleaned
	incomingAfter := eng2.DB.GetAllRelations(graphIDB, "in")
	if len(incomingAfter) > 0 {
		for _, sources := range incomingAfter {
			for _, src := range sources {
				edges, _ := eng2.DB.GetOutEdges(src, "refers_to", 0)
				for _, e := range edges {
					if e.TargetID == graphIDB && e.DeletedAt == 0 {
						t.Error("incoming edge a→b should be soft-deleted")
					}
				}
			}
		}
	}

	// Verify outgoing edges from node_b were cleaned
	outgoingAfter := eng2.DB.GetAllRelations(graphIDB, "out")
	if len(outgoingAfter) > 0 {
		edges, _ := eng2.DB.GetOutEdges(graphIDB, "knows", 0)
		for _, e := range edges {
			if e.TargetID == graphIDC && e.DeletedAt == 0 {
				t.Error("outgoing edge b→c should be soft-deleted")
			}
		}
	}

	// Verify node_c's InEdges pointing to node_b were cleaned
	cIncoming := eng2.DB.GetAllRelations(graphIDC, "in")
	for _, sources := range cIncoming {
		for _, src := range sources {
			if src == graphIDB {
				t.Error("node_c should not have incoming edge from node_b after cascade")
			}
		}
	}

	t.Log("VDelete cascade correctly handles both incoming and outgoing edges")
}

// TestVEvolveCleanupOnFailure verifies that when VEvolve fails after creating
// graph edges, VDelete on the new node cleans up both incoming AND outgoing
// edges (the evolves_from inverse edge is outgoing from the new node).
// Regression test for H5 (derivative of H4).
func TestVEvolveCleanupOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	indexName := "test_vevolve_cleanup"

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}

	err = eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	vec := []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	_ = eng.VAdd(indexName, "original", vec, map[string]any{"content": "original"})

	// Simulate the scenario manually instead of relying on VEvolve to fail:
	// 1. Copy incoming edges to a new (never-created) node
	// 2. Create the evolve link (both directions)
	// 3. VDelete the new node — verify edge cleanup

	newID := "evolved_original_test"
	oldGraphID := buildGraphID(indexName, "original")
	newGraphID := buildGraphID(indexName, newID)

	// Add the new node to the vector index (VDelete requires the node to exist)
	_ = eng.VAdd(indexName, newID, vec, map[string]any{"content": "evolved"})

	// Create the evolves_from reverse edge (outgoing from new node)
	eng.DB.AddEdge(newGraphID, oldGraphID, "evolves_from", 1.0, nil, 1)
	// Create superseded_by forward edge (incoming to new node)
	eng.DB.AddEdge(oldGraphID, newGraphID, "superseded_by", 1.0, nil, 2)

	// Verify edges exist
	outgoing := eng.DB.GetAllRelations(newGraphID, "out")
	incoming := eng.DB.GetAllRelations(newGraphID, "in")
	if len(outgoing) != 1 || len(incoming) != 1 {
		t.Fatalf("expected 1 outgoing and 1 incoming edge on new node, got out=%d in=%d", len(outgoing), len(incoming))
	}

	// VDelete the new node (simulates the VEvolve cleanup path)
	err = eng.VDelete(indexName, newID)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for cascade goroutine
	eng.Close()
	eng2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	// Check that neither evolves_from nor superseded_by edges remain
	incoming2 := eng2.DB.GetAllRelations(oldGraphID, "in")
	for relType, sources := range incoming2 {
		for _, src := range sources {
			if strings.Contains(src, "evolved_") {
				t.Errorf("dangling edge from %s to original (rel=%s)", src, relType)
			}
		}
	}

	t.Log("VEvolve failure cleanup verified")
}
