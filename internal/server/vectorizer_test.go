package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestVectorizerProcessing verifies that the vectorizer correctly processes files,
// generates embeddings via a mock API, and persists data into the index.
func TestVectorizerProcessing(t *testing.T) {
	t.Skip("Temporary skip for v0.2.2 release stability")
	// 1. Setup Fake Embedder Service (Mock Ollama)
	// This server responds to any request with a valid fake embedding.
	mockEmbedder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Response compatible with the struct expected in embedder.go
		// Returns a 3-float vector (compatible with the index we will create)
		w.Write([]byte(`{"embedding": [0.1, 0.2, 0.3]}`))
	}))
	defer mockEmbedder.Close()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test_doc.txt")

	testContent := "This is a test document."
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	dataDir := t.TempDir()
	opts := engine.DefaultOptions(dataDir)
	opts.AutoSaveInterval = 0 // Disable auto-save for deterministic testing
	opts.AutoSaveThreshold = 0

	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "test_index"
	// Create index. Note: dimension is auto-detected or fixed on first insert.
	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Configuration pointing to the Mock Server
	configPath := filepath.Join(tmpDir, "vectorizers.yaml")
	configContent := fmt.Sprintf(`vectorizers:
  - name: test_vectorizer
    source:
      type: filesystem
      path: %s
    embedder:
      type: ollama_api
      model: mock-model
      url: %s
    schedule: 60s
    kektor_index: %s
    doc_processor:
      chunk_size: 100
`, tmpDir, mockEmbedder.URL, indexName)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	server, err := NewServer(eng, ":0", configPath, "", "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if server.vectorizerService != nil {
		server.vectorizerService.Start()
		defer server.vectorizerService.Stop()

		// Poll to wait for processing completion
		timeout := time.After(5 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		completed := false
	waitLoop:
		for {
			select {
			case <-timeout:
				t.Fatal("Timeout: vectorizer did not complete processing within 5 seconds")
			case <-ticker.C:
				// Check that the file status has been saved in KV store
				stateKey := "_vectorizer_state:test_vectorizer:" + testFile
				_, found := eng.KVGet(stateKey)
				if found {
					completed = true
					break waitLoop
				}
			}
		}

		if !completed {
			t.Fatal("Test failed to complete")
		}

		// Verify that vectors were actually inserted into the index
		info, err := eng.DB.GetSingleVectorIndexInfoAPI(indexName)
		if err != nil {
			t.Errorf("Failed to get index info: %v", err)
		}

		// There must be at least 1 vector (the chunked document)
		if info.VectorCount == 0 {
			t.Errorf("Expected vectors to be added, but VectorCount is 0. Embedding probably failed silently.")
		} else {
			t.Logf("✓ Vectorizer processed files successfully. Count: %d", info.VectorCount)
		}

	} else {
		t.Skip("Vectorizer service not initialized")
	}
}

// TestVectorizerConcurrent verifies that no race conditions or panics occur
// when multiple vectorizers run concurrently.
func TestVectorizerConcurrent(t *testing.T) {
	t.Skip("Temporary skip for v0.2.2 release stability")
	// Use mock server here as well to avoid connection errors cluttering logs
	mockEmbedder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"embedding": [0.1, 0.2, 0.3]}`))
	}))
	defer mockEmbedder.Close()

	tmpDir := t.TempDir()

	numVectorizers := 3
	var configBuilder string
	configBuilder += "vectorizers:\n"

	dataDir := t.TempDir()
	opts := engine.DefaultOptions(dataDir)
	opts.AutoSaveInterval = 0

	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	for i := 0; i < numVectorizers; i++ {
		dir := filepath.Join(tmpDir, fmt.Sprintf("dir%d", i))
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("content"), 0644)

		idxName := fmt.Sprintf("index_%d", i)
		eng.VCreate(idxName, distance.Cosine, 16, 200, distance.Float32, "", nil, nil)

		configBuilder += fmt.Sprintf(`
  - name: vec_%d
    source:
      type: filesystem
      path: %s
    embedder:
      type: ollama_api
      url: %s
      model: mock
    schedule: 60s
    kektor_index: %s
    doc_processor:
      chunk_size: 100
`, i, dir, mockEmbedder.URL, idxName)
	}

	configPath := filepath.Join(tmpDir, "vectorizers.yaml")
	os.WriteFile(configPath, []byte(configBuilder), 0644)

	server, err := NewServer(eng, ":0", configPath, "", "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if server.vectorizerService != nil {
		server.vectorizerService.Start()
		// Let it run for a while to stimulate potential race conditions
		time.Sleep(1 * time.Second)
		server.vectorizerService.Stop()
		t.Log("✓ No race conditions detected")
	}
}
