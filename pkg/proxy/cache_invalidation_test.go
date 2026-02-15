package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func TestSmartCacheInvalidation(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// 2. Setup Config
	cfg := DefaultConfig()
	cfg.CacheEnabled = true
	cfg.CacheIndex = "semantic_cache"
	cfg.FirewallEnabled = false // Disable firewall to skip init checks

	// 3. Init Proxy
	// Note: We don't need a real LLM target for this test
	p, err := NewAIProxy(cfg, eng)
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}

	// 4. Create Cache Index explicitly to ensure text search is enabled
	// We MUST set a language (e.g. "english") for text indexing to work on the "sources" field.
	err = eng.VCreate(cfg.CacheIndex, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create cache index: %v", err)
	}

	// 5. Populate Cache Manually (Simulation)
	// We verify that saveToCache correctly indexes the source IDs string.
	// Since saveToCache is internal/async in the flow, we call it directly here for determinism.
	// Or even better, we construct the data manually to test the Invalidation Logic specifically.

	// Entry 1: Depends on doc_1
	// sources string: "doc_1"
	eng.VAdd(cfg.CacheIndex, "cache_entry_A", []float32{0.1, 0.1}, map[string]any{
		"response": "Answer A",
		"sources":  "doc_1",
	})

	// Entry 2: Depends on doc_2
	// sources string: "doc_2"
	eng.VAdd(cfg.CacheIndex, "cache_entry_B", []float32{0.2, 0.2}, map[string]any{
		"response": "Answer B",
		"sources":  "doc_2",
	})

	// Entry 3: Depends on doc_1 AND doc_2
	// sources string: "doc_1 doc_2"
	eng.VAdd(cfg.CacheIndex, "cache_entry_C", []float32{0.3, 0.3}, map[string]any{
		"response": "Answer C",
		"sources":  "doc_1 doc_2",
	})

	// Allow minimal time for indexing if async (usually sync in VAdd)
	time.Sleep(100 * time.Millisecond)

	// 6. Execute Invalidation for "doc_1"
	// Expected: cache_entry_A (direct) and cache_entry_C (shared) should be deleted.

	reqBody := map[string]string{"document_id": "doc_1"}
	jsonBody, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/cache/invalidate", bytes.NewBuffer(jsonBody))
	w := httptest.NewRecorder()

	// Call the handler directly via ServeHTTP (routing logic check)
	p.ServeHTTP(w, req)

	// 7. Verify Response
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	// Check reported deleted count
	// JSON numbers are float64
	deletedCount := result["deleted"].(float64)
	if deletedCount != 2 {
		t.Errorf("Expected 2 items deleted, reported %v", deletedCount)
	}

	// 8. Verify Engine State
	// cache_entry_A -> Should be gone
	if _, err := eng.VGet(cfg.CacheIndex, "cache_entry_A"); err == nil {
		t.Error("cache_entry_A should have been deleted (depended on doc_1)")
	}

	// cache_entry_C -> Should be gone
	if _, err := eng.VGet(cfg.CacheIndex, "cache_entry_C"); err == nil {
		t.Error("cache_entry_C should have been deleted (depended on doc_1)")
	}

	// cache_entry_B -> Should remain
	if _, err := eng.VGet(cfg.CacheIndex, "cache_entry_B"); err != nil {
		t.Error("cache_entry_B should NOT have been deleted (depended only on doc_2)")
	}

	t.Log("Smart Cache Invalidation Test Passed")
}
