package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/rag"
)

// MockEmbedder implements embeddings.Embedder
type MockEmbedder struct {
	Val []float32
}

func (m *MockEmbedder) Embed(text string) ([]float32, error) {
	if m.Val == nil {
		// Default vector
		return []float32{0.1, 0.1, 0.1}, nil
	}
	return m.Val, nil
}

func TestRAGThresholdFiltering(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "rag_test_index"
	// Create index (float32, cosine)
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// 2. Insert Data
	// Query Vector will be {0.1, 0.1, 0.1}

	// Vector A: Very Close (Identity) -> Distance 0.0
	// Use slightly different but very close vector if needed, or identity.
	eng.VAdd(indexName, "doc_close", []float32{0.1, 0.1, 0.1}, map[string]interface{}{
		"content": "This is relevant content.",
	})

	// Vector B: Far/Opposite
	// {-0.1, -0.1, -0.1} is opposite direction. Cosine dist = 2.0 (if range 0..2) or 1.0 depending on impl.
	// In kektordb distance typically: 0 = identical.
	eng.VAdd(indexName, "doc_far", []float32{-0.1, -0.1, -0.1}, map[string]interface{}{
		"content": "This is irrelevant noise.",
	})

	// 3. Setup Mock LLM Backend (Target)
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		t.Logf("LLM Received Body Length: %d", len(body))
		// Log a snippet
		// t.Logf("LLM Received Snippet: %s", string(body))

		// Check Prompt
		if !bytes.Contains(body, []byte("relevant content")) {
			t.Error("FAIL: Expected 'relevant content' in prompt, found none")
		}
		if bytes.Contains(body, []byte("irrelevant noise")) {
			t.Error("FAIL: Found 'irrelevant noise' in prompt, expected it to be filtered by threshold")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockLLM.Close()

	// 4. Setup Proxy Config
	cfg := Config{
		TargetURL:    mockLLM.URL,
		RAGEnabled:   true,
		RAGIndex:     indexName,
		RAGTopK:      5,
		RAGEfSearch:  50,
		RAGThreshold: 0.5, // Threshold set to 0.5. Identity is 0.0 (<0.5), Opposite is ~2.0 (>0.5).
		// RAGUseGraph:     false,
		Embedder: &MockEmbedder{Val: []float32{0.1, 0.1, 0.1}},
	}

	proxy, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// 5. Send Request
	reqBody := `{"messages": [{"role": "user", "content": "Query"}]}`
	req := httptest.NewRequest("POST", "http://localhost/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	// Check if proxy executed without error
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("Proxy returned status %d", w.Result().StatusCode)
	}
}

func TestRAGAdaptiveEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "adaptive_test_index"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	eng.VAdd(indexName, "doc1", []float32{0.1, 0.1, 0.1}, map[string]interface{}{
		"content": "This is relevant content about Go programming.",
	})
	eng.VAdd(indexName, "doc2", []float32{0.15, 0.15, 0.15}, map[string]interface{}{
		"content": "This is also relevant content about programming.",
	})

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("relevant content")) {
			t.Error("FAIL: Expected 'relevant content' in adaptive prompt")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockLLM.Close()

	cfg := Config{
		TargetURL:      mockLLM.URL,
		RAGEnabled:     true,
		RAGIndex:       indexName,
		RAGTopK:        5,
		RAGUseGraph:    true,
		RAGUseAdaptive: true,
		RAGGraphConfig: GraphConfig{
			ExpansionStrategy: "graph",
			ExpansionDepth:    2,
			MaxTokens:         4096,
			Relations:         []string{"next", "prev"},
			EdgeWeights: map[string]float64{
				"next": 0.95,
				"prev": 0.95,
			},
			SemanticWeight: 0.6,
			GraphWeight:    0.2,
			DensityWeight:  0.2,
		},
		Embedder: &MockEmbedder{Val: []float32{0.1, 0.1, 0.1}},
	}

	proxy, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	reqBody := `{"messages": [{"role": "user", "content": "Tell me about Go"}]}`
	req := httptest.NewRequest("POST", "http://localhost/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("Proxy returned status %d", w.Result().StatusCode)
	}
}

func TestRAGAdaptiveCustomRelations(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "custom_relations_test"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	eng.VAdd(indexName, "doc1", []float32{0.1, 0.1, 0.1}, map[string]interface{}{
		"content": "This is relevant content about testing.",
	})

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("relevant content")) {
			t.Error("FAIL: Expected 'relevant content' in prompt")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockLLM.Close()

	cfg := Config{
		TargetURL:      mockLLM.URL,
		RAGEnabled:     true,
		RAGIndex:       indexName,
		RAGTopK:        5,
		RAGUseGraph:    true,
		RAGUseAdaptive: true,
		RAGGraphConfig: GraphConfig{
			ExpansionStrategy: "graph",
			ExpansionDepth:    1,
			MaxTokens:         4096,
			Relations:         []string{"parent", "child"},
		},
		Embedder: &MockEmbedder{Val: []float32{0.1, 0.1, 0.1}},
	}

	proxy, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	reqBody := `{"messages": [{"role": "user", "content": "Tell me about testing"}]}`
	req := httptest.NewRequest("POST", "http://localhost/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("Proxy returned status %d", w.Result().StatusCode)
	}
}

func TestRAGAdaptiveCustomWeights(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "custom_weights_test"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	eng.VAdd(indexName, "doc1", []float32{0.1, 0.1, 0.1}, map[string]interface{}{
		"content": "Important content here.",
	})

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("Important content")) {
			t.Error("FAIL: Expected 'Important content' in prompt")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockLLM.Close()

	cfg := Config{
		TargetURL:      mockLLM.URL,
		RAGEnabled:     true,
		RAGIndex:       indexName,
		RAGTopK:        5,
		RAGUseGraph:    true,
		RAGUseAdaptive: true,
		RAGGraphConfig: GraphConfig{
			ExpansionStrategy: "graph",
			ExpansionDepth:    2,
			MaxTokens:         4096,
			Relations:         []string{"next", "prev"},
			EdgeWeights: map[string]float64{
				"next": 0.99,
				"prev": 0.50,
			},
			SemanticWeight: 0.6,
			GraphWeight:    0.2,
			DensityWeight:  0.2,
		},
		Embedder: &MockEmbedder{Val: []float32{0.1, 0.1, 0.1}},
	}

	proxy, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	reqBody := `{"messages": [{"role": "user", "content": "What is important?"}]}`
	req := httptest.NewRequest("POST", "http://localhost/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("Proxy returned status %d", w.Result().StatusCode)
	}
}

func TestRAGBackwardsCompatible(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "backwards_compat_test"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	eng.VAdd(indexName, "doc_close", []float32{0.1, 0.1, 0.1}, map[string]interface{}{
		"content": "This is relevant content.",
	})

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("relevant content")) {
			t.Error("FAIL: Expected 'relevant content' in prompt with standard RAG")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer mockLLM.Close()

	cfg := Config{
		TargetURL:      mockLLM.URL,
		RAGEnabled:     true,
		RAGIndex:       indexName,
		RAGTopK:        5,
		RAGThreshold:   0.5,
		RAGUseGraph:    true,
		RAGUseAdaptive: false,
		Embedder:       &MockEmbedder{Val: []float32{0.1, 0.1, 0.1}},
	}

	proxy, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	reqBody := `{"messages": [{"role": "user", "content": "Query"}]}`
	req := httptest.NewRequest("POST", "http://localhost/chat/completions", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	proxy.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("Proxy returned status %d", w.Result().StatusCode)
	}
}

func TestDefaultAdaptiveConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.RAGUseAdaptive {
		t.Error("RAGUseAdaptive should default to false")
	}

	gc := cfg.RAGGraphConfig
	if gc.ExpansionStrategy != "graph" {
		t.Errorf("Expected expansion_strategy 'graph', got '%s'", gc.ExpansionStrategy)
	}
	if gc.ExpansionDepth != 2 {
		t.Errorf("Expected expansion_depth 2, got %d", gc.ExpansionDepth)
	}
	if gc.MaxTokens != 4096 {
		t.Errorf("Expected max_tokens 4096, got %d", gc.MaxTokens)
	}
	expectedRels := []string{"next", "prev", "parent", "child", "mentions"}
	if len(gc.Relations) != len(expectedRels) {
		t.Errorf("Expected %d relations, got %d", len(expectedRels), len(gc.Relations))
	}
	if gc.SemanticWeight != 0.6 {
		t.Errorf("Expected semantic_weight 0.6, got %f", gc.SemanticWeight)
	}
	if gc.GraphWeight != 0.2 {
		t.Errorf("Expected graph_weight 0.2, got %f", gc.GraphWeight)
	}
	if gc.DensityWeight != 0.2 {
		t.Errorf("Expected density_weight 0.2, got %f", gc.DensityWeight)
	}
	if _, ok := gc.EdgeWeights["next"]; !ok {
		t.Error("Expected 'next' in edge_weights")
	}
	if gc.EdgeWeights["next"] != 0.95 {
		t.Errorf("Expected edge_weights['next'] = 0.95, got %f", gc.EdgeWeights["next"])
	}
}

func TestAdaptiveContextConfigFromRAGConfig(t *testing.T) {
	cfg := GraphConfig{
		ExpansionStrategy: "density",
		ExpansionDepth:    3,
		MaxTokens:         8192,
		Relations:         []string{"parent", "mentions"},
		EdgeWeights: map[string]float64{
			"parent":   0.85,
			"mentions": 0.60,
		},
		SemanticWeight: 0.5,
		GraphWeight:    0.3,
		DensityWeight:  0.2,
	}

	adaptiveCfg := rag.AdaptiveContextConfig{
		MaxTokens:           cfg.MaxTokens,
		ExpansionStrategy:   cfg.ExpansionStrategy,
		GraphExpansionDepth: cfg.ExpansionDepth,
		GraphRelations:      cfg.Relations,
		EdgeWeights:         cfg.EdgeWeights,
		SemanticWeight:      cfg.SemanticWeight,
		GraphWeight:         cfg.GraphWeight,
		DensityWeight:       cfg.DensityWeight,
	}

	if adaptiveCfg.MaxTokens != 8192 {
		t.Errorf("Expected MaxTokens 8192, got %d", adaptiveCfg.MaxTokens)
	}
	if adaptiveCfg.ExpansionStrategy != "density" {
		t.Errorf("Expected ExpansionStrategy 'density', got '%s'", adaptiveCfg.ExpansionStrategy)
	}
	if adaptiveCfg.GraphExpansionDepth != 3 {
		t.Errorf("Expected GraphExpansionDepth 3, got %d", adaptiveCfg.GraphExpansionDepth)
	}
	if len(adaptiveCfg.GraphRelations) != 2 {
		t.Errorf("Expected 2 relations, got %d", len(adaptiveCfg.GraphRelations))
	}
}
