package engine

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

func TestGraphFiltering(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "graph_test"
	err = eng.VCreate(indexName, distance.Euclidean, 16, 200, distance.Float32, "english", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Data Setup
	// Target vector: [0.1, 0.1]
	// We insert 3 nodes.
	// Node A: Perfect match, Linked to Root
	// Node B: Perfect match, NOT Linked
	// Node C: Bad match, Linked to Root

	rootID := "Project_Alpha"

	// Create Root (Conceptual node, can be empty vector or just link endpoint)
	// We add it to have it in the system, though VLink implicitly creates it in KV.
	// But resolveGraphFilter checks if ID exists in HNSW to add to allowed list.
	// Note: The root itself doesn't need to be in the result, its children do.

	// Insert Node A
	eng.VAdd(indexName, "doc_A", []float32{0.1, 0.1}, nil)
	eng.VLink("doc_A", rootID, "belongs_to", "", 1.0, nil) // Link A -> Root

	// Insert Node B (The Distracter)
	eng.VAdd(indexName, "doc_B", []float32{0.1, 0.1}, nil)
	// No link for B

	// Insert Node C (The Noise)
	eng.VAdd(indexName, "doc_C", []float32{0.9, 0.9}, nil)
	eng.VLink("doc_C", rootID, "belongs_to", "", 1.0, nil) // Link C -> Root

	// 3. Test Standard Search (Control Group)
	// Should find A and B (closest vectors)
	query := []float32{0.1, 0.1}
	results, _ := eng.VSearch(indexName, query, 2, "", "", 100, 0.5, nil)

	if !slices.Contains(results, "doc_A") || !slices.Contains(results, "doc_B") {
		t.Errorf("Standard search failed. Expected A and B, got: %v", results)
	}

	// 4. Test Graph Filtered Search
	// Query: Find vectors close to [0.1, 0.1] BUT only if linked to Project_Alpha via 'belongs_to' (incoming to root)

	// Since we linked "doc_A" -> "Project_Alpha", from Project_Alpha's perspective,
	// doc_A is an INCOMING link (rev index).

	filter := &GraphQuery{
		RootID:    rootID,
		Relations: []string{"belongs_to"},
		Direction: "in", // We want nodes that point TO the root
		MaxDepth:  1,
	}

	filteredResults, err := eng.VSearch(indexName, query, 10, "", "", 100, 0.5, filter)
	if err != nil {
		t.Fatalf("Filtered search error: %v", err)
	}

	// 5. Assertions
	// We expect "doc_A" (Good Vector + Linked).
	// We expect "doc_C" (Bad Vector + Linked) -> Depending on K, might appear lower.
	// We MUST NOT see "doc_B" (Good Vector + NOT Linked).

	if slices.Contains(filteredResults, "doc_B") {
		t.Errorf("Filter failed! Found 'doc_B' which is not linked to %s", rootID)
	}

	if !slices.Contains(filteredResults, "doc_A") {
		t.Errorf("Filter too aggressive! Lost 'doc_A' which is valid. Got: %v", filteredResults)
	}

	t.Logf("Filtered Results: %v", filteredResults)
}
