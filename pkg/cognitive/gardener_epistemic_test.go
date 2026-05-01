package cognitive

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestEpistemicResolutionBasics tests the basic flow of epistemic resolution.
func TestEpistemicResolutionBasics(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "epistemic_test"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Create test nodes with conflicting information
	vec1 := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	vec2 := []float32{0.11, 0.21, 0.31, 0.41, 0.51, 0.61, 0.71, 0.81, 0.91, 1.01, 0.11, 0.21, 0.31, 0.41, 0.51, 0.61}

	now := float64(time.Now().Unix())

	eng.VAdd(indexName, "mem1", vec1, map[string]any{
		"content":       "Il server è in Europa",
		"_created_at":   now - (30 * 24 * 3600), // 30 days ago
		"_access_count": 2,
	})

	eng.VAdd(indexName, "mem2", vec2, map[string]any{
		"content":       "Il server è stato migrato in USA",
		"_created_at":   now - (5 * 24 * 3600), // 5 days ago
		"_access_count": 45,
	})

	// Create a reflection node manually (normally created by detectContradictions)
	reflectionID := "reflection_test_1"
	avgVec := make([]float32, len(vec1))
	for i := range vec1 {
		avgVec[i] = (vec1[i] + vec2[i]) / 2.0
	}

	eng.VAdd(indexName, reflectionID, avgVec, map[string]any{
		"type":    "reflection",
		"status":  "unresolved",
		"content": "Conflict: server location",
	})

	// Link nodes to reflection
	eng.VLink(indexName, reflectionID, "mem1", "contradicts", "contradicted_by", 1.0, nil)
	eng.VLink(indexName, reflectionID, "mem2", "contradicts", "contradicted_by", 1.0, nil)

	// Test: Check that VFilter finds the reflection
	refs, err := eng.VFilter(indexName, "type='reflection' AND status='unresolved'", 10)
	if err != nil {
		t.Fatalf("VFilter failed: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("Expected 1 unresolved reflection, got %d", len(refs))
	}

	// Test: Check that we can get the edges
	edges, found := eng.VGetEdges(indexName, reflectionID, "contradicts", 0)
	if !found {
		t.Error("Expected to find contradicts edges")
	}
	if len(edges) != 2 {
		t.Errorf("Expected 2 contradicts edges, got %d", len(edges))
	}

	// Test: Verify nodes are accessible
	for _, edge := range edges {
		data, err := eng.VGet(indexName, edge.TargetID)
		if err != nil {
			t.Errorf("Failed to get node %s: %v", edge.TargetID, err)
		}
		if data.Metadata["content"] == nil {
			t.Errorf("Node %s has no content", edge.TargetID)
		}
	}

	t.Log("Epistemic resolution setup verified successfully")
}

// TestParseEpistemicResponse tests the JSON parsing from LLM responses.
func TestParseEpistemicResponse(t *testing.T) {
	g := &Gardener{}

	tests := []struct {
		name     string
		input    string
		expected *epistemicResolution
		wantErr  bool
	}{
		{
			name:  "valid json",
			input: `{"resolvable": true, "consolidated_truth": "Test truth", "clarification_question": ""}`,
			expected: &epistemicResolution{
				Resolvable:            true,
				ConsolidatedTruth:     "Test truth",
				ClarificationQuestion: "",
			},
			wantErr: false,
		},
		{
			name:  "json with markdown",
			input: "Here's the result:\n\n```json\n{\"resolvable\": false, \"consolidated_truth\": \"\", \"clarification_question\": \"What is X?\"}\n```",
			expected: &epistemicResolution{
				Resolvable:            false,
				ConsolidatedTruth:     "",
				ClarificationQuestion: "What is X?",
			},
			wantErr: false,
		},
		{
			name:     "no json",
			input:    "This is just text without JSON",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "invalid json",
			input:    `{"resolvable": true, "consolidated_truth": }`,
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.parseEpistemicResponse(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result.Resolvable != tt.expected.Resolvable {
				t.Errorf("Expected Resolvable=%v, got %v", tt.expected.Resolvable, result.Resolvable)
			}
			if result.ConsolidatedTruth != tt.expected.ConsolidatedTruth {
				t.Errorf("Expected ConsolidatedTruth=%q, got %q", tt.expected.ConsolidatedTruth, result.ConsolidatedTruth)
			}
			if result.ClarificationQuestion != tt.expected.ClarificationQuestion {
				t.Errorf("Expected ClarificationQuestion=%q, got %q", tt.expected.ClarificationQuestion, result.ClarificationQuestion)
			}
		})
	}
}

// TestMetadataHelpers tests the helper functions.
func TestMetadataHelpers(t *testing.T) {
	m := map[string]any{
		"string_val": "test",
		"float_val":  3.14,
		"int_val":    42,
		"nil_val":    nil,
	}

	// Test getMetadataString
	if s := getMetadataString(m, "string_val"); s != "test" {
		t.Errorf("Expected 'test', got %q", s)
	}
	if s := getMetadataString(m, "missing"); s != "" {
		t.Errorf("Expected empty string, got %q", s)
	}

	// Test getMetadataFloat
	if f := getMetadataFloat(m, "float_val"); f != 3.14 {
		t.Errorf("Expected 3.14, got %f", f)
	}
	if f := getMetadataFloat(m, "int_val"); f != 42 {
		t.Errorf("Expected 42, got %f", f)
	}
	if f := getMetadataFloat(m, "missing"); f != 0 {
		t.Errorf("Expected 0, got %f", f)
	}

	// Test getMetadataInt
	if i := getMetadataInt(m, "int_val"); i != 42 {
		t.Errorf("Expected 42, got %d", i)
	}
	if i := getMetadataInt(m, "float_val"); i != 3 {
		t.Errorf("Expected 3, got %d", i)
	}
	if i := getMetadataInt(m, "missing"); i != 0 {
		t.Errorf("Expected 0, got %d", i)
	}
}

// TestExecuteConsolidationEvolvedContent verifies that evolved nodes created during
// epistemic consolidation contain the consolidated truth, not the original contradictory content.
func TestExecuteConsolidationEvolvedContent(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "consolidation_content_test"
	err = eng.VCreate(indexName, distance.Euclidean, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 1. Create conflicting nodes
	vec1 := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	vec2 := []float32{0.11, 0.21, 0.31, 0.41, 0.51, 0.61, 0.71, 0.81, 0.91, 1.01, 0.11, 0.21, 0.31, 0.41, 0.51, 0.61}

	now := float64(time.Now().Unix())

	eng.VAdd(indexName, "mem1", vec1, map[string]any{
		"content":       "Il server è in Europa",
		"type":          "memory",
		"_created_at":   now - 30*24*3600,
		"_access_count": 2,
	})
	eng.VAdd(indexName, "mem2", vec2, map[string]any{
		"content":       "Il server è stato migrato in USA",
		"type":          "memory",
		"_created_at":   now - 5*24*3600,
		"_access_count": 45,
	})

	// 2. Create reflection node
	reflectionID := "reflection_consolidation_test"
	centroid := make([]float32, len(vec1))
	for i := range vec1 {
		centroid[i] = (vec1[i] + vec2[i]) / 2.0
	}
	eng.VAdd(indexName, reflectionID, centroid, map[string]any{
		"type":    "reflection",
		"status":  "unresolved",
		"content": "Conflict: server location",
	})

	// 3. Build internal node data
	consolidatedTruth := "Il server è attualmente negli USA"
	resolution := &epistemicResolution{
		Resolvable:        true,
		ConsolidatedTruth: consolidatedTruth,
	}
	nodesData := []epistemicNodeInternal{
		{id: "mem1", vector: vec1, metadata: map[string]any{"content": "Il server è in Europa", "type": "memory"}},
		{id: "mem2", vector: vec2, metadata: map[string]any{"content": "Il server è stato migrato in USA", "type": "memory"}},
	}

	// 4. Create Gardener and call executeConsolidation directly
	g := &Gardener{
		eng:        eng,
		scanCursors: make(map[string]uint32),
	}
	g.executeConsolidation(indexName, reflectionID, nodesData, resolution, centroid)

	// 5. Verify old nodes are marked historical
	oldData1, _ := eng.VGet(indexName, "mem1")
	if hist, _ := oldData1.Metadata["_is_historical"].(bool); !hist {
		t.Error("mem1 should be marked _is_historical=true after consolidation")
	}
	oldData2, _ := eng.VGet(indexName, "mem2")
	if hist, _ := oldData2.Metadata["_is_historical"].(bool); !hist {
		t.Error("mem2 should be marked _is_historical=true after consolidation")
	}

	// 6. Find evolved nodes via edges
	for _, nodeID := range []string{"mem1", "mem2"} {
		edges, found := eng.VGetEdges(indexName, nodeID, "superseded_by", 0)
		if !found || len(edges) == 0 {
			t.Errorf("Expected superseded_by edge from %s to evolved node", nodeID)
			continue
		}
		evolvedID := edges[0].TargetID
		evolvedData, err := eng.VGet(indexName, evolvedID)
		if err != nil {
			t.Errorf("Failed to get evolved node %s: %v", evolvedID, err)
			continue
		}
		if evolvedData.Metadata["content"] != consolidatedTruth {
			t.Errorf("Evolved node %s content = %q, want %q (consolidated truth)",
				evolvedID, evolvedData.Metadata["content"], consolidatedTruth)
		}
		if evolvedData.Metadata["type"] != "evolved_memory" {
			t.Errorf("Evolved node %s type = %v, want 'evolved_memory'",
				evolvedID, evolvedData.Metadata["type"])
		}
		if evolvedData.Metadata["_consolidated_into"] == nil {
			t.Errorf("Evolved node %s missing _consolidated_into reference to master", evolvedID)
		}
	}

	// 7. Verify master belief exists
	bp, err := eng.VFilter(indexName, "type='consolidated_belief'", 10)
	if err != nil || len(bp) == 0 {
		t.Fatal("Master consolidated_belief not found after consolidation")
	}

	t.Log("executeConsolidation correctly uses consolidated truth for evolved nodes")
}
