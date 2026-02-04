package engine

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

func TestGraphEntities(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "knowledge_graph"
	// Create index
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// ----------------------------------------------------------------
	// SCENARIO A: Fail on Empty Index
	// We try to add a zero-vector entity to an empty index.
	// It SHOULD fail because the engine doesn't know the vector dimension yet.
	// ----------------------------------------------------------------
	t.Run("FailOnUnknownDimension", func(t *testing.T) {
		err := eng.VAdd(indexName, "ghost_node", nil, map[string]any{"type": "ghost"})
		if err == nil {
			t.Error("Expected error when adding nil vector to empty index, got nil")
		} else {
			t.Logf("Correctly rejected unknown dimension: %v", err)
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO B: Dimension Bootstrapping & Zero Vector Creation
	// ----------------------------------------------------------------
	t.Run("BootstrapAndEntityCreation", func(t *testing.T) {
		// 1. Insert a "Real" Document (Dimension = 3)
		docVec := []float32{0.1, 0.2, 0.3}
		if err := eng.VAdd(indexName, "doc_1", docVec, map[string]any{"kind": "document"}); err != nil {
			t.Fatalf("Failed to insert bootstrap doc: %v", err)
		}

		// 2. Now insert a "First-Class Entity" (Vector = nil)
		// It should infer dimension 3 and create [0, 0, 0]
		meta := map[string]any{"kind": "entity", "name": "Golang"}
		if err := eng.VAdd(indexName, "entity_golang", nil, meta); err != nil {
			t.Fatalf("Failed to insert entity with nil vector: %v", err)
		}

		// 3. Verify Data
		data, err := eng.VGet(indexName, "entity_golang")
		if err != nil {
			t.Fatalf("Failed to retrieve entity: %v", err)
		}

		// Check dimension
		if len(data.Vector) != 3 {
			t.Errorf("Wrong dimension inferred. Expected 3, got %d", len(data.Vector))
		}
		// Check zero values
		if data.Vector[0] != 0 || data.Vector[1] != 0 || data.Vector[2] != 0 {
			t.Errorf("Vector should be zeroed. Got: %v", data.Vector)
		}
		// Check metadata
		if data.Metadata["name"] != "Golang" {
			t.Errorf("Metadata lost")
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO C: Mixed Batch Handling
	// VAddBatch should handle a mix of vectors and nil vectors,
	// inferring dimension from the non-nil ones in the same batch.
	// ----------------------------------------------------------------
	t.Run("MixedBatchInsertion", func(t *testing.T) {
		batch := []types.BatchObject{
			// Item 1: Real Vector
			{
				Id:       "doc_2",
				Vector:   []float32{0.9, 0.8, 0.7}, // Dim 3
				Metadata: map[string]any{"kind": "document"},
			},
			// Item 2: Nil Vector (Should learn Dim 3 from doc_2 or index)
			{
				Id:       "entity_rust",
				Vector:   nil,
				Metadata: map[string]any{"kind": "entity", "name": "Rust"},
			},
		}

		if err := eng.VAddBatch(indexName, batch); err != nil {
			t.Fatalf("VAddBatch failed: %v", err)
		}

		// Verify Entity Rust
		data, err := eng.VGet(indexName, "entity_rust")
		if err != nil {
			t.Fatalf("Failed to get batch entity: %v", err)
		}
		if len(data.Vector) != 3 {
			t.Errorf("Batch inference failed. Expected dim 3, got %d", len(data.Vector))
		}
	})

	// ----------------------------------------------------------------
	// SCENARIO D: Search by Properties (Graph Search)
	// We search for "kind='entity'". We should find "entity_golang" and "entity_rust"
	// but NOT "doc_1" or "doc_2".
	// ----------------------------------------------------------------
	t.Run("SearchByProperties", func(t *testing.T) {
		// We perform a search with a dummy query (all zeros) and a strong filter.
		// Since our entities are [0,0,0], they are actually identical to the query.
		// Distance will be 0 (max similarity).

		dummyQuery := []float32{0, 0, 0}
		filter := "kind='entity'"

		results, err := eng.VSearch(indexName, dummyQuery, 10, filter, "", 100, 1.0, nil)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if !slices.Contains(results, "entity_golang") {
			t.Errorf("Search missed entity_golang. Results: %v", results)
		}
		if !slices.Contains(results, "entity_rust") {
			t.Errorf("Search missed entity_rust. Results: %v", results)
		}
		if slices.Contains(results, "doc_1") {
			t.Errorf("Filter failed. Included doc_1 (which is kind='document').")
		}
	})
}
