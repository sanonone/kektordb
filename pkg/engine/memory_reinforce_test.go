package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

func TestMemoryReinforcement(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	eng, _ := Open(opts)
	defer eng.Close()

	indexName := "mem_test"
	// Half-Life molto breve: 1 secondo
	cfg := &hnsw.MemoryConfig{Enabled: true, DecayHalfLife: hnsw.Duration(1 * time.Second)}
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, cfg)

	vec := []float32{1.0, 0.0}

	// 1. Insert Memories
	now := time.Now()
	oldTime := float64(now.Add(-2 * time.Second).Unix()) // 2 Half-lives ago -> Score 0.25

	// Mem A: Old, Standard
	eng.VAdd(indexName, "A", vec, map[string]any{"_created_at": oldTime})

	// Mem B: Old, but PINNED
	eng.VAdd(indexName, "B", vec, map[string]any{"_created_at": oldTime, "_pinned": true})

	// Mem C: Old, but REINFORCED (Last Accessed Now)
	eng.VAdd(indexName, "C", vec, map[string]any{"_created_at": oldTime})
	eng.VReinforce(indexName, []string{"C"}) // Should set _last_accessed to Now

	// 2. Search using VSearchGraph (which uses searchWithFusion -> applies Decay)
	// Arguments: idx, query, k, filter, textQuery, ef, alpha, relations, hydrate, graphQuery
	graphResults, err := eng.VSearchGraph(indexName, vec, 10, "", "", 100, 1.0, nil, false, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// 3. Verify Ranks
	scores := make(map[string]float64)
	for _, r := range graphResults {
		scores[r.ID] = r.Score
	}

	if scores["B"] < 0.99 {
		t.Errorf("Pinned B decayed! Score: %f", scores["B"])
	}
	if scores["C"] < 0.99 {
		t.Errorf("Reinforced C decayed! Score: %f", scores["C"])
	}
	if scores["A"] > 0.3 {
		t.Errorf("Standard A did not decay! Score: %f", scores["A"])
	}
}
