package rag

import (
	"fmt"
	"testing"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/engine"
)

// mockAdaptiveStore implements AdaptiveStore for testing
type mockAdaptiveStore struct {
	chunks    map[string]core.VectorData
	relations map[string]map[string][]string
}

func newMockAdaptiveStore() *mockAdaptiveStore {
	return &mockAdaptiveStore{
		chunks:    make(map[string]core.VectorData),
		relations: make(map[string]map[string][]string),
	}
}

func (m *mockAdaptiveStore) VSearch(indexName string, query []float32, k int, filter string, explicitTextQuery string, efSearch int, alpha float64, graphQuery *engine.GraphQuery) ([]string, error) {
	// Return first k chunk IDs
	ids := make([]string, 0, k)
	for id := range m.chunks {
		if len(ids) >= k {
			break
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *mockAdaptiveStore) VGetRelations(indexName, sourceID string) map[string][]string {
	if rels, ok := m.relations[sourceID]; ok {
		return rels
	}
	return make(map[string][]string)
}

func (m *mockAdaptiveStore) VGet(indexName, id string) (core.VectorData, error) {
	if chunk, ok := m.chunks[id]; ok {
		return chunk, nil
	}
	return core.VectorData{}, nil
}

func (m *mockAdaptiveStore) AddChunk(id string, content string, metadata map[string]any) {
	m.chunks[id] = core.VectorData{
		ID:       id,
		Metadata: metadata,
	}
}

func (m *mockAdaptiveStore) AddRelation(sourceID, relType, targetID string) {
	if m.relations[sourceID] == nil {
		m.relations[sourceID] = make(map[string][]string)
	}
	m.relations[sourceID][relType] = append(m.relations[sourceID][relType], targetID)
}

func TestAdaptiveRetriever_GreedyStrategy(t *testing.T) {
	store := newMockAdaptiveStore()

	// Create test chunks
	store.AddChunk("chunk_1", "First chunk about Go programming", map[string]any{
		"content":     "First chunk about Go programming",
		"parent_id":   "doc_1",
		"chunk_index": 0,
	})
	store.AddChunk("chunk_2", "Second chunk about concurrency", map[string]any{
		"content":     "Second chunk about concurrency",
		"parent_id":   "doc_1",
		"chunk_index": 1,
	})
	store.AddChunk("chunk_3", "Third chunk about channels", map[string]any{
		"content":     "Third chunk about channels",
		"parent_id":   "doc_1",
		"chunk_index": 2,
	})

	// Add relations
	store.AddRelation("chunk_1", "next", "chunk_2")
	store.AddRelation("chunk_2", "next", "chunk_3")
	store.AddRelation("chunk_2", "prev", "chunk_1")
	store.AddRelation("chunk_3", "prev", "chunk_2")

	config := AdaptiveContextConfig{
		MaxTokens:           1000,
		CharsPerToken:       4.0,
		ExpansionStrategy:   "greedy",
		GraphExpansionDepth: 1,
		MaxExpansionNodes:   10,
		GraphRelations:      []string{"next", "prev"},
		EdgeWeights: map[string]float64{
			"next": 0.95,
			"prev": 0.95,
		},
		SemanticWeight: 0.6,
		GraphWeight:    0.2,
		DensityWeight:  0.2,
	}

	retriever := NewAdaptiveRetriever(store, config)
	queryVec := []float32{0.1, 0.2, 0.3}

	window, err := retriever.RetrieveWithContext("test_index", queryVec, 1)
	if err != nil {
		t.Fatalf("RetrieveWithContext failed: %v", err)
	}

	if window.TotalChunks == 0 {
		t.Error("Expected at least one chunk in context window")
	}

	t.Logf("Retrieved %d chunks, %d tokens", window.TotalChunks, window.TotalTokens)
}

func TestAdaptiveRetriever_GraphStrategy(t *testing.T) {
	store := newMockAdaptiveStore()

	// Create test chunks
	store.AddChunk("seed", "Seed chunk about machine learning", map[string]any{
		"content":   "Seed chunk about machine learning",
		"parent_id": "doc_1",
	})
	store.AddChunk("related_1", "Related chunk about neural networks", map[string]any{
		"content":   "Related chunk about neural networks",
		"parent_id": "doc_2",
	})
	store.AddChunk("related_2", "Related chunk about deep learning", map[string]any{
		"content":   "Related chunk about deep learning",
		"parent_id": "doc_3",
	})

	// Add relations
	store.AddRelation("seed", "mentions", "related_1")
	store.AddRelation("seed", "mentions", "related_2")

	config := AdaptiveContextConfig{
		MaxTokens:           2000,
		CharsPerToken:       4.0,
		ExpansionStrategy:   "graph",
		GraphExpansionDepth: 2,
		MaxExpansionNodes:   50,
		GraphRelations:      []string{"mentions", "related_to"},
		EdgeWeights: map[string]float64{
			"mentions":   0.5,
			"related_to": 0.4,
		},
		SemanticWeight: 0.6,
		GraphWeight:    0.2,
		DensityWeight:  0.2,
	}

	retriever := NewAdaptiveRetriever(store, config)
	queryVec := []float32{0.1, 0.2, 0.3}

	window, err := retriever.RetrieveWithContext("test_index", queryVec, 1)
	if err != nil {
		t.Fatalf("RetrieveWithContext failed: %v", err)
	}

	if window.TotalChunks == 0 {
		t.Error("Expected chunks in context window")
	}

	t.Logf("Graph strategy: Retrieved %d chunks from %d documents",
		window.TotalChunks, window.DocumentsUsed)
}

func TestCalculateInformationDensity(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected float64
		min      float64
		max      float64
	}{
		{
			name:     "High density - technical text",
			text:     "The quick brown fox jumps over the lazy dog",
			expected: 1.0, // All words unique
			min:      0.85,
			max:      1.0,
		},
		{
			name:     "Low density - repetitive text",
			text:     "the the the the the the the the",
			expected: 0.125, // 1 unique / 8 total
			min:      0.1,
			max:      0.15,
		},
		{
			name: "Medium density - normal text",
			text: "The cat sat on the mat. The cat was happy.",
			min:  0.5,
			max:  0.8,
		},
		{
			name:     "Empty text",
			text:     "",
			expected: 0.0,
			min:      0.0,
			max:      0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			density := calculateInformationDensity(tt.text)
			if density < tt.min || density > tt.max {
				t.Errorf("calculateInformationDensity(%q) = %f, want between %f and %f",
					tt.text, density, tt.min, tt.max)
			}
		})
	}
}

func TestAdaptiveRetriever_TokenBudget(t *testing.T) {
	store := newMockAdaptiveStore()

	// Create many chunks
	for i := 0; i < 20; i++ {
		store.AddChunk(fmt.Sprintf("chunk_%d", i),
			fmt.Sprintf("This is chunk number %d with some content about programming and software development", i),
			map[string]any{
				"content":     fmt.Sprintf("This is chunk number %d with some content about programming and software development", i),
				"parent_id":   "doc_1",
				"chunk_index": i,
			})
	}

	config := AdaptiveContextConfig{
		MaxTokens:           100, // Small budget
		CharsPerToken:       4.0,
		ExpansionStrategy:   "greedy",
		GraphExpansionDepth: 1,
		MaxExpansionNodes:   100,
		SemanticWeight:      1.0, // Only use semantic score
		GraphWeight:         0.0,
		DensityWeight:       0.0,
	}

	retriever := NewAdaptiveRetriever(store, config)
	queryVec := []float32{0.1, 0.2, 0.3}

	window, err := retriever.RetrieveWithContext("test_index", queryVec, 5)
	if err != nil {
		t.Fatalf("RetrieveWithContext failed: %v", err)
	}

	// Should respect token budget
	if window.TotalTokens > config.MaxTokens {
		t.Errorf("Total tokens %d exceeds budget %d", window.TotalTokens, config.MaxTokens)
	}

	t.Logf("Token budget test: %d chunks, %d tokens (budget: %d)",
		window.TotalChunks, window.TotalTokens, config.MaxTokens)
}
