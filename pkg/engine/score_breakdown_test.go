package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// TestVSearchWithScoresBreakdown_NoDecay verifies the breakdown is always
// present: similarity from distance, decay factor 1 when no memory config.
func TestVSearchWithScoresBreakdown_NoDecay(t *testing.T) {
	testDir := t.TempDir()
	eng, err := Open(DefaultOptions(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.VCreate("idx", "cosine", 16, 200, "float32", "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	vec := []float32{0.1, 0.2, 0.3, 0.4}
	if err := eng.VAdd("idx", "v1", vec, map[string]any{"content": "one"}); err != nil {
		t.Fatal(err)
	}

	results, err := eng.VSearchWithScores("idx", vec, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	r := results[0]
	if r.ID != "v1" {
		t.Errorf("expected v1, got %s", r.ID)
	}
	if r.Breakdown == nil {
		t.Fatal("expected score breakdown, got nil")
	}
	if r.Breakdown.Similarity <= 0 || r.Breakdown.Similarity > 1 {
		t.Errorf("similarity out of range: %f", r.Breakdown.Similarity)
	}
	if r.Breakdown.DecayFactor != 1 {
		t.Errorf("expected decay factor 1 without memory config, got %f", r.Breakdown.DecayFactor)
	}
	if r.Score != r.Breakdown.Similarity {
		t.Errorf("score %f != similarity %f", r.Score, r.Breakdown.Similarity)
	}
}

// TestVSearchWithScoresBreakdown_Decay verifies the breakdown reflects the
// time-decay factor per memory layer: fast decay for old episodic memories,
// slower for semantic, none for procedural.
func TestVSearchWithScoresBreakdown_Decay(t *testing.T) {
	testDir := t.TempDir()
	eng, err := Open(DefaultOptions(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.VCreate("idx", "cosine", 16, 200, "float32", "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	memCfg := hnsw.MemoryConfig{
		Enabled: true,
		Layers: map[string]hnsw.LayerConfig{
			"episodic":   {DecayHalfLife: hnsw.Duration(1 * time.Hour)},
			"semantic":   {DecayHalfLife: hnsw.Duration(100 * time.Hour)},
			"procedural": {DecayHalfLife: 0},
		},
	}
	idx, _ := eng.DB.GetVectorIndex("idx")
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		hnswIdx.SetMemoryConfig(memCfg)
	}

	oldTime := float64(time.Now().Add(-2 * time.Hour).Unix())
	vec := make([]float32, 10)
	for i := range vec {
		vec[i] = 0.1
	}

	memories := []struct {
		id    string
		layer string
	}{
		{"episodic_mem", "episodic"},
		{"semantic_mem", "semantic"},
		{"procedural_mem", "procedural"},
	}
	for _, m := range memories {
		if err := eng.VAdd("idx", m.id, vec, map[string]any{
			"memory_layer": m.layer,
			"_created_at":  oldTime,
		}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := eng.VSearchWithScores("idx", vec, 5)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]SearchResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	epi, ok := byID["episodic_mem"]
	if !ok || epi.Breakdown == nil {
		t.Fatal("episodic_mem missing or no breakdown")
	}
	if epi.Breakdown.DecayFactor >= 1 {
		t.Errorf("episodic decay factor should be < 1, got %f", epi.Breakdown.DecayFactor)
	}
	if epi.Breakdown.DecayFactor > 0.5 {
		t.Errorf("episodic (2h old, 1h half-life) decay factor should be <= 0.5, got %f", epi.Breakdown.DecayFactor)
	}
	if got := epi.Score; got != epi.Breakdown.Similarity*epi.Breakdown.DecayFactor {
		t.Errorf("score %f != similarity %f * decay %f", got, epi.Breakdown.Similarity, epi.Breakdown.DecayFactor)
	}

	sem, ok := byID["semantic_mem"]
	if !ok || sem.Breakdown == nil {
		t.Fatal("semantic_mem missing or no breakdown")
	}
	if sem.Breakdown.DecayFactor >= 1 {
		t.Errorf("semantic decay factor should be < 1, got %f", sem.Breakdown.DecayFactor)
	}
	if sem.Breakdown.DecayFactor < epi.Breakdown.DecayFactor {
		t.Errorf("semantic decay %f should be larger (slower decay) than episodic %f",
			sem.Breakdown.DecayFactor, epi.Breakdown.DecayFactor)
	}

	proc, ok := byID["procedural_mem"]
	if !ok || proc.Breakdown == nil {
		t.Fatal("procedural_mem missing or no breakdown")
	}
	if proc.Breakdown.DecayFactor != 1 {
		t.Errorf("procedural layer has no decay, expected factor 1, got %f", proc.Breakdown.DecayFactor)
	}
}
