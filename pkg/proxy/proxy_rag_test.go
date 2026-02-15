package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
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
