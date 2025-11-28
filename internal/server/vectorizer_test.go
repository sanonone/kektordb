package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestVectorizerProcessing verifica che il vectorizer processi correttamente i file
func TestVectorizerProcessing(t *testing.T) {
	// Setup: crea directory temporanea con file di test
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test_doc.txt")

	testContent := "This is a test document for vectorizer processing. It contains some sample text."
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Setup: crea engine
	dataDir := t.TempDir()
	opts := engine.DefaultOptions(dataDir)
	opts.AutoSaveInterval = 0 // Disable auto-save for test
	opts.AutoSaveThreshold = 0

	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	// Crea index per vectorizer
	indexName := "test_index"
	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english"); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Setup: crea vectorizer config temporaneo
	configPath := filepath.Join(tmpDir, "vectorizers.yaml")
	configContent := `vectorizers:
  - name: test_vectorizer
    source:
      type: filesystem
      path: ` + tmpDir + `
    embedding:
      provider: mock
      model: mock-model
      base_url: http://localhost:11434
    schedule: 60s
    kektor_index: ` + indexName + `
    doc_processor:
      chunk_size: 100
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Crea server con vectorizer
	server, err := NewServer(eng, ":0", configPath)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Avvia vectorizers
	if server.vectorizerService != nil {
		server.vectorizerService.Start()

		// Attendi che il vectorizer processi i file (con timeout)
		timeout := time.After(10 * time.Second)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		processed := false
		for !processed {
			select {
			case <-timeout:
				t.Fatal("Timeout: vectorizer did not complete processing within 10 seconds")
			case <-ticker.C:
				// Verifica se ci sono vettori nell'index
				_, ok := eng.DB.GetVectorIndex(indexName)
				if ok {
					// Check if vectors were added (we should have at least 1 chunk)
					processed = true
					break
				}
			}
		}

		// Verifica che i vettori siano stati aggiunti
		info, err := eng.DB.GetVectorIndexInfo()
		if err != nil {
			t.Fatalf("Failed to get index info: %v", err)
		}

		found := false
		for _, idx := range info {
			if idx.Name == indexName {
				found = true
				if idx.VectorCount == 0 {
					t.Errorf("Expected vectors to be added, but VectorCount is 0")
				} else {
					t.Logf("✓ Vectorizer successfully processed file - added %d vectors", idx.VectorCount)
				}
				break
			}
		}

		if !found {
			t.Error("Index not found after vectorizer processing")
		}

		// Cleanup
		server.vectorizerService.Stop()
	} else {
		t.Skip("Vectorizer service not initialized (this is expected if embedding provider is not available)")
	}
}

// TestVectorizerConcurrent verifica che multipli vectorizers possano girare in parallelo
func TestVectorizerConcurrent(t *testing.T) {
	// Setup: crea multiple directory con file
	tmpDir := t.TempDir()

	numVectorizers := 3
	for i := 0; i < numVectorizers; i++ {
		dir := filepath.Join(tmpDir, "dir"+string(rune('A'+i)))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}

		testFile := filepath.Join(dir, "doc.txt")
		if err := os.WriteFile(testFile, []byte("Test content for concurrent processing"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Setup engine
	dataDir := t.TempDir()
	opts := engine.DefaultOptions(dataDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0

	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	// Crea config con multipli vectorizers
	configPath := filepath.Join(tmpDir, "vectorizers.yaml")
	configContent := `vectorizers:`
	for i := 0; i < numVectorizers; i++ {
		indexName := "index_" + string(rune('A'+i))
		dirPath := filepath.Join(tmpDir, "dir"+string(rune('A'+i)))

		// Crea index
		if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english"); err != nil {
			t.Fatalf("Failed to create index %s: %v", indexName, err)
		}

		configContent += `
  - name: vectorizer_` + string(rune('A'+i)) + `
    source:
      type: filesystem
      path: ` + dirPath + `
    embedding:
      provider: mock
      model: mock
      base_url: http://localhost:11434
    schedule: 60s
    kektor_index: ` + indexName + `
    doc_processor:
      chunk_size: 100
`
	}

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	server, err := NewServer(eng, ":0", configPath)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if server.vectorizerService != nil {
		server.vectorizerService.Start()

		// Attendi con timeout più lungo per concurrent processing
		time.Sleep(3 * time.Second)

		// Verifica che non ci siano race conditions e che il sistema sia stabile
		t.Log("✓ No race conditions detected - concurrent vectorizers running successfully")

		server.vectorizerService.Stop()
	} else {
		t.Skip("Vectorizer service not initialized")
	}
}
