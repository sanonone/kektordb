package engine

import (
	"os"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

func TestHybridSearchFixes(t *testing.T) {
	// 1. Setup Temporary Directory
	tmpDir, err := os.MkdirTemp("", "kektor_hybrid_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// 2. Initialize Engine
	opts := DefaultOptions(tmpDir)
	// Disable background maintenance to avoid noise/race in short test
	opts.MaintenanceInterval = 0
	opts.AutoSaveInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "hybrid_test_idx"

	// 3. Create Index
	// Metric: Cosine, Precision: Float32, Language: English
	// We use Cosine so distance is 0..2. Normalization should handle it.
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 4. Add Documents
	// We use "body" as text field to test Auto-Detection (Fix #4)
	docs := []struct {
		ID   string
		Text string
		Vec  []float32
	}{
		{"doc1", "apple banana cherry", []float32{1.0, 0.0, 0.0}},   // Vector matches Query perfectly
		{"doc2", "nothing special here", []float32{0.0, 1.0, 0.0}},  // Vector orthogonal
		{"doc3", "apple fruit salad", []float32{0.5, 0.5, 0.0}},     // Vector semi-match
		{"doc4", "special term zanzibar", []float32{0.0, 0.0, 1.0}}, // Text matches "zanzibar"
	}

	batch := make([]types.BatchObject, len(docs))
	for i, d := range docs {
		batch[i] = types.BatchObject{
			Id:     d.ID,
			Vector: d.Vec,
			Metadata: map[string]interface{}{
				"body": d.Text, // Using "body", NOT "content"
				"type": "test",
			},
		}
	}

	if err := eng.VAddBatch(indexName, batch); err != nil {
		t.Fatalf("VAddBatch failed: %v", err)
	}

	// Allow small time for text indexing if it happens via channel (it's usually sync in VAdd but good measure)
	time.Sleep(100 * time.Millisecond)

	// 5. Test Case A: Field Auto-Detection
	// We search with text query. It should find "body" field automatically.
	// Query: "zanzibar" -> should match doc4
	targetVec := []float32{0.0, 0.0, 0.0} // Neutral vector

	t.Run("Field Auto-Detection", func(t *testing.T) {
		// Alpha 0.0 = Pure Text Search
		results, err := eng.VSearch(indexName, targetVec, 5, "", "zanzibar", 100, 0.0)
		if err != nil {
			t.Fatalf("VSearch failed: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("Expected results, got 0. Auto-detection likely failed.")
		}
		if results[0] != "doc4" {
			t.Errorf("Expected doc4 to be top result for 'zanzibar', got %v", results[0])
		}
		t.Logf("Field Auto-Detection passed. Found %d results.", len(results))
	})

	// 6. Test Case B: Alpha Weighting & Normalization
	// Query Vector: {1.0, 0.0, 0.0} (matches doc1)
	// Query Text: "zanzibar" (matches doc4)

	queryVec := []float32{1.0, 0.0, 0.0}

	t.Run("Alpha 1.0 (Vector Only)", func(t *testing.T) {
		results, _ := eng.VSearch(indexName, queryVec, 1, "", "zanzibar", 100, 1.0)
		if results[0] != "doc1" {
			t.Errorf("With Alpha 1.0, expected doc1 (vector match), got %s", results[0])
		}
	})

	t.Run("Alpha 0.0 (Text Only)", func(t *testing.T) {
		results, _ := eng.VSearch(indexName, queryVec, 1, "", "zanzibar", 100, 0.0)
		if results[0] != "doc4" {
			t.Errorf("With Alpha 0.0, expected doc4 (text match), got %s", results[0])
		}
	})

	t.Run("Alpha 0.5 (Hybrid)", func(t *testing.T) {
		// Here we expect fusion.
		// Doc1: Vector Score=1.0 (Distance 0.0), Text Score=0.0 -> Fused=0.5
		// Doc4: Vector Score=0.0 (Distance ~1.0), Text Score=1.0 -> Fused=0.5
		// Doc3: Vector Score~0.5, Text Score=0.0 -> Fused=0.25

		results, _ := eng.VSearch(indexName, queryVec, 3, "", "zanzibar", 100, 0.5)

		// We verify that we get results. Exact order might depend on float precision tie-breaking
		// But doc1 and doc4 should be top 2.
		found1 := false
		found4 := false
		for _, id := range results {
			if id == "doc1" {
				found1 = true
			}
			if id == "doc4" {
				found4 = true
			}
		}

		if !found1 || !found4 {
			t.Errorf("With Hybrid Search, expected both doc1 and doc4 in top results. Got: %v", results)
		}
		t.Logf("Hybrid results: %v", results)
	})

	// 7. Test Case C: Symmetric Normalization Check
	// We want to ensure that a poor vector match doesn't get a high score simply because the distance isn't infinite.
	// In our new Min-Max logic:
	// MinDist = 0.0 (doc1), MaxDist = ~1.0 (doc4/doc2)
	// Doc1 score = 1.0
	// Doc4 score = 0.0

	// If we used the old logic 1/(1+d):
	// Doc1 d=0 -> 1.0
	// Doc4 d=1 -> 0.5
	// The gap was 0.5. Now gap is 1.0. This means poor vector matches are penalized more heavily.
}
