package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

func TestMemoryEngine(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Common vector for all items (perfect match for query)
	targetVec := []float32{1.0, 0.0, 0.0}
	queryVec := []float32{1.0, 0.0, 0.0}

	// ----------------------------------------------------------------
	// SCENARIO A: Regression Test (Standard Index)
	// Ensure that without MemoryConfig, time is ignored.
	// ----------------------------------------------------------------
	t.Run("StandardIndexRegression", func(t *testing.T) {
		idxName := "standard_idx"
		// Create standard index (nil memory config)
		err := eng.VCreate(idxName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
		if err != nil {
			t.Fatalf("VCreate failed: %v", err)
		}

		// Insert Doc OLD (manually set date to 1 year ago)
		// Since Memory is disabled, _created_at should be ignored by ranking
		oldTime := float64(time.Now().AddDate(-1, 0, 0).Unix())
		eng.VAdd(idxName, "doc_old", targetVec, map[string]any{"_created_at": oldTime})

		// Insert Doc NEW (now)
		newTime := float64(time.Now().Unix())
		eng.VAdd(idxName, "doc_new", targetVec, map[string]any{"_created_at": newTime})

		// Search
		results, err := eng.VSearch(idxName, queryVec, 10, "", "", 100, 1.0, nil)
		if err != nil {
			t.Fatalf("VSearch failed: %v", err)
		}

		// Since vectors are identical, scores should be identical (1.0).
		// HNSW might return them in any order, or insertion order.
		// We just check that both are present.
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}
		t.Logf("Regression passed. Results: %v", results)
	})

	// ----------------------------------------------------------------
	// SCENARIO B: Time Decay Test
	// Ensure that recent memories rank higher.
	// ----------------------------------------------------------------
	t.Run("TimeDecayRanking", func(t *testing.T) {
		idxName := "memory_idx"

		// Configure Memory: Half-Life = 1 Hour (3600 seconds)
		memConfig := &hnsw.MemoryConfig{
			Enabled:       true,
			DecayHalfLife: hnsw.Duration(1 * time.Hour),
		}

		err := eng.VCreate(idxName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, memConfig)
		if err != nil {
			t.Fatalf("VCreate Memory failed: %v", err)
		}

		now := time.Now()

		// 1. FRESH Memory (Created Now) -> Age 0 -> Decay 1.0
		// Score should be ~1.0
		eng.VAdd(idxName, "mem_fresh", targetVec, map[string]any{
			"_created_at": float64(now.Unix()),
		})

		// 2. OLD Memory (Created 1 Hour ago) -> Age = HalfLife -> Decay 0.5
		// Score should be ~0.5
		eng.VAdd(idxName, "mem_old", targetVec, map[string]any{
			"_created_at": float64(now.Add(-1 * time.Hour).Unix()),
		})

		// 3. ANCIENT Memory (Created 2 Hours ago) -> Age = 2*HalfLife -> Decay 0.25
		// Score should be ~0.25
		eng.VAdd(idxName, "mem_ancient", targetVec, map[string]any{
			"_created_at": float64(now.Add(-2 * time.Hour).Unix()),
		})

		// Search
		results, err := eng.VSearchWithScores(idxName, queryVec, 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// Verify Order
		if len(results) != 3 {
			t.Fatalf("Expected 3 results, got %d", len(results))
		}

		// Check 1st: Fresh
		if results[0].ID != "mem_fresh" {
			t.Errorf("Top result should be 'mem_fresh', got '%s' (Score: %f)", results[0].ID, results[0].Score)
		}

		// Check 2nd: Old
		if results[1].ID != "mem_old" {
			t.Errorf("Second result should be 'mem_old', got '%s' (Score: %f)", results[1].ID, results[1].Score)
		}

		// Check Scores (Approximate check due to float precision)
		// Cosine Similarity 1.0 (Distance 0.0 -> Normalized 1.0)
		// Expected Scores: Fresh ~1.0, Old ~0.5, Ancient ~0.25

		if results[0].Score < 0.9 {
			t.Errorf("Fresh score too low: %f", results[0].Score)
		}
		if results[1].Score > 0.6 || results[1].Score < 0.4 {
			t.Errorf("Old score mismatch (expected ~0.5): %f", results[1].Score)
		}
		if results[2].Score > 0.3 || results[2].Score < 0.2 {
			t.Errorf("Ancient score mismatch (expected ~0.25): %f", results[2].Score)
		}

		t.Logf("Decay Test Passed. Scores: Fresh=%.2f, Old=%.2f, Ancient=%.2f",
			results[0].Score, results[1].Score, results[2].Score)
	})
}
