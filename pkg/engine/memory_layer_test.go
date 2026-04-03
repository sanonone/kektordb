package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// TestMemoryLayerDecay verifies that different memory layers have different decay rates.
func TestMemoryLayerDecay(t *testing.T) {
	testDir := t.TempDir()
	opts := DefaultOptions(testDir)
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Create index with memory layers configured
	dim := 10
	memCfg := hnsw.MemoryConfig{
		Enabled: true,
		Layers: map[string]hnsw.LayerConfig{
			"episodic": {
				DecayHalfLife: hnsw.Duration(1 * time.Hour), // Fast decay
			},
			"semantic": {
				DecayHalfLife: hnsw.Duration(100 * time.Hour), // Slow decay
			},
			"procedural": {
				DecayHalfLife: 0, // No decay
			},
		},
	}

	// Create index and set memory config
	err = eng.VCreate("test", "cosine", 16, 200, "float32", "english", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Set memory config via the HNSW index
	idx, _ := eng.DB.GetVectorIndex("test")
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		hnswIdx.SetMemoryConfig(memCfg)
	}

	// Create test vectors
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = 0.1
	}

	// Add memories with different layers (2 hours ago)
	oldTime := time.Now().Add(-2 * time.Hour).Unix()

	// Episodic memory - should have significant decay
	err = eng.VAdd("test", "episodic_mem", vec, map[string]any{
		"memory_layer": "episodic",
		"_created_at":  float64(oldTime),
		"content":      "I saw a cat yesterday",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Semantic memory - should have minimal decay
	err = eng.VAdd("test", "semantic_mem", vec, map[string]any{
		"memory_layer": "semantic",
		"_created_at":  float64(oldTime),
		"content":      "Cats are mammals",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Procedural memory - should have no decay
	err = eng.VAdd("test", "procedural_mem", vec, map[string]any{
		"memory_layer": "procedural",
		"_created_at":  float64(oldTime),
		"content":      "Always check permissions",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Search and verify decay behavior
	results, err := eng.VSearchWithScores("test", vec, 10)
	if err != nil {
		t.Fatal(err)
	}

	var episodicScore, semanticScore, proceduralScore float64
	for _, r := range results {
		switch r.ID {
		case "episodic_mem":
			episodicScore = r.Score
		case "semantic_mem":
			semanticScore = r.Score
		case "procedural_mem":
			proceduralScore = r.Score
		}
	}

	// Verify decay ordering: procedural > semantic > episodic
	if proceduralScore <= semanticScore {
		t.Errorf("Procedural should have higher score than semantic: proc=%f, sem=%f", proceduralScore, semanticScore)
	}
	if semanticScore <= episodicScore {
		t.Errorf("Semantic should have higher score than episodic: sem=%f, ep=%f", semanticScore, episodicScore)
	}

	// Procedural should have no decay (score ≈ 1.0)
	if proceduralScore < 0.99 {
		t.Errorf("Procedural memory should have no decay (score ≈ 1.0), got %f", proceduralScore)
	}

	// Episodic should have significant decay (after 2 hours with 1 hour half-life)
	// Decay factor = 2^(-2/1) = 0.25
	if episodicScore > 0.5 {
		t.Errorf("Episodic memory should have significant decay, got score %f", episodicScore)
	}
}

// TestProceduralAutoPin verifies that procedural memories are auto-pinned.
func TestProceduralAutoPin(t *testing.T) {
	testDir := t.TempDir()
	opts := DefaultOptions(testDir)
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Create index with memory layers
	memCfg := hnsw.MemoryConfig{
		Enabled: true,
		Layers: map[string]hnsw.LayerConfig{
			"episodic": {
				PinnedByDefault: false,
			},
			"procedural": {
				PinnedByDefault: true,
			},
		},
	}

	err = eng.VCreate("test", "cosine", 16, 200, "float32", "english", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	idx, _ := eng.DB.GetVectorIndex("test")
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		hnswIdx.SetMemoryConfig(memCfg)
	}

	vec := []float32{0.1, 0.2, 0.3}

	// Add procedural memory (should be auto-pinned)
	err = eng.VAdd("test", "proc1", vec, map[string]any{
		"memory_layer": "procedural",
		"content":      "System rule",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add episodic memory (should NOT be auto-pinned)
	err = eng.VAdd("test", "ep1", vec, map[string]any{
		"memory_layer": "episodic",
		"content":      "Daily event",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify procedural is pinned
	procData, err := eng.VGet("test", "proc1")
	if err != nil {
		t.Fatal(err)
	}
	if pinned, ok := procData.Metadata["_pinned"].(bool); !ok || !pinned {
		t.Errorf("Procedural memory should be auto-pinned, got _pinned=%v", procData.Metadata["_pinned"])
	}

	// Verify episodic is NOT pinned
	epData, err := eng.VGet("test", "ep1")
	if err != nil {
		t.Fatal(err)
	}
	if pinned, ok := epData.Metadata["_pinned"].(bool); ok && pinned {
		t.Errorf("Episodic memory should NOT be auto-pinned, got _pinned=%v", epData.Metadata["_pinned"])
	}
}

// TestDefaultLayer verifies that memories without explicit layer default to episodic.
func TestDefaultLayer(t *testing.T) {
	testDir := t.TempDir()
	opts := DefaultOptions(testDir)
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	memCfg := hnsw.MemoryConfig{
		Enabled: true,
		Layers: map[string]hnsw.LayerConfig{
			"episodic": {},
		},
	}

	err = eng.VCreate("test", "cosine", 16, 200, "float32", "english", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	idx, _ := eng.DB.GetVectorIndex("test")
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		hnswIdx.SetMemoryConfig(memCfg)
	}

	vec := []float32{0.1, 0.2, 0.3}

	// Add memory without explicit layer
	err = eng.VAdd("test", "mem1", vec, map[string]any{
		"content": "Some memory",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify it defaults to episodic
	data, err := eng.VGet("test", "mem1")
	if err != nil {
		t.Fatal(err)
	}
	layer, ok := data.Metadata["memory_layer"].(string)
	if !ok || layer != "episodic" {
		t.Errorf("Memory should default to episodic layer, got layer=%v", data.Metadata["memory_layer"])
	}
}

// TestExplicitPinnedOverride verifies that explicit _pinned overrides layer defaults.
func TestExplicitPinnedOverride(t *testing.T) {
	testDir := t.TempDir()
	opts := DefaultOptions(testDir)
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	memCfg := hnsw.MemoryConfig{
		Enabled: true,
		Layers: map[string]hnsw.LayerConfig{
			"procedural": {
				PinnedByDefault: true, // Normally auto-pins
			},
		},
	}

	err = eng.VCreate("test", "cosine", 16, 200, "float32", "english", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	idx, _ := eng.DB.GetVectorIndex("test")
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		hnswIdx.SetMemoryConfig(memCfg)
	}

	vec := []float32{0.1, 0.2, 0.3}

	// Add procedural memory with explicit _pinned=false
	err = eng.VAdd("test", "proc1", vec, map[string]any{
		"memory_layer": "procedural",
		"_pinned":      false, // Explicit override
		"content":      "Temporary rule",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's NOT pinned despite being procedural
	data, err := eng.VGet("test", "proc1")
	if err != nil {
		t.Fatal(err)
	}
	if pinned, ok := data.Metadata["_pinned"].(bool); ok && pinned {
		t.Errorf("Explicit _pinned=false should override layer default, got _pinned=%v", data.Metadata["_pinned"])
	}
}
