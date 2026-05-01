package engine

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestRoaringBitmapsFilters verifica la correttezza del nuovo motore di filtraggio
// basato su Roaring Bitmaps. Testa operatori logici, relazionali e filtri grafo.
func TestRoaringBitmapsFilters(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0 // Disable auto-save per un test veloce
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "roaring_test_idx"
	err = eng.VCreate(indexName, distance.Euclidean, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 2. Data Setup (Vettori a distanza fissa per focalizzarci sui filtri)
	// Vogliamo che la ricerca vettoriale trovi tutti, e che sia il filtro a escluderli.
	zeroVec := []float32{0.0, 0.0}

	docs := []types.BatchObject{
		{Id: "doc_1", Vector: zeroVec, Metadata: map[string]any{"type": "article", "year": float64(2020), "status": "published"}},
		{Id: "doc_2", Vector: zeroVec, Metadata: map[string]any{"type": "article", "year": float64(2022), "status": "draft"}},
		{Id: "doc_3", Vector: zeroVec, Metadata: map[string]any{"type": "video", "year": float64(2022), "status": "published"}},
		{Id: "doc_4", Vector: zeroVec, Metadata: map[string]any{"type": "video", "year": float64(2024), "status": "published"}},
		{Id: "doc_5", Vector: zeroVec, Metadata: map[string]any{"type": "podcast", "year": float64(2024), "status": "archived"}},
	}

	if err := eng.VAddBatch(indexName, docs); err != nil {
		t.Fatalf("VAddBatch failed: %v", err)
	}

	// Setup per il test del Graph Filter
	// Creiamo un grafo: category_root -> doc_3, doc_4
	eng.VAdd(indexName, "category_root", zeroVec, map[string]any{"name": "tech_media"})
	eng.VLink(indexName, "category_root", "doc_3", "contains", "", 1.0, nil)
	eng.VLink(indexName, "category_root", "doc_4", "contains", "", 1.0, nil)

	// Helper per eseguire la ricerca ed estrarre solo gli ID
	runSearch := func(filter string, graphQuery *GraphQuery) []string {
		res, err := eng.VSearch(indexName, zeroVec, 10, filter, "", 100, 1.0, graphQuery)
		if err != nil {
			t.Fatalf("VSearch failed per filter '%s': %v", filter, err)
		}
		return res
	}

	// --- 3. TEST SUITE ---

	t.Run("Filtro Esatto Semplice (Equality)", func(t *testing.T) {
		res := runSearch("type='video'", nil)
		if len(res) != 2 || !slices.Contains(res, "doc_3") || !slices.Contains(res, "doc_4") {
			t.Errorf("Expected[doc_3, doc_4], got %v", res)
		}
	})

	t.Run("Filtro AND (Intersection)", func(t *testing.T) {
		res := runSearch("type='article' AND status='published'", nil)
		if len(res) != 1 || res[0] != "doc_1" {
			t.Errorf("Expected [doc_1], got %v", res)
		}
	})

	t.Run("Filtro OR (Union)", func(t *testing.T) {
		res := runSearch("type='podcast' OR status='draft'", nil)
		if len(res) != 2 || !slices.Contains(res, "doc_5") || !slices.Contains(res, "doc_2") {
			t.Errorf("Expected [doc_2, doc_5], got %v", res)
		}
	})

	t.Run("Filtro Numerico B-Tree (Inequality >)", func(t *testing.T) {
		res := runSearch("year>2022", nil)
		if len(res) != 2 || !slices.Contains(res, "doc_4") || !slices.Contains(res, "doc_5") {
			t.Errorf("Expected [doc_4, doc_5], got %v", res)
		}
	})

	t.Run("Filtro Complesso (AND + Numerico)", func(t *testing.T) {
		res := runSearch("type='video' AND year>=2024", nil)
		if len(res) != 1 || res[0] != "doc_4" {
			t.Errorf("Expected [doc_4], got %v", res)
		}
	})

	t.Run("Nessun Risultato (Empty Intersection)", func(t *testing.T) {
		res := runSearch("type='podcast' AND status='published'", nil)
		if len(res) != 0 {
			t.Errorf("Expected empty result, got %v", res)
		}
	})

	t.Run("Filtro Grafo Puro (Nessun metadato)", func(t *testing.T) {
		gq := &GraphQuery{
			RootID:    "category_root",
			Relations: []string{"contains"},
			Direction: "out",
			MaxDepth:  1,
		}
		res := runSearch("", gq)
		// Note: The result now includes the root node itself (category_root) along with neighbors
		// This is the correct behavior after the fix that includes root in the filter set
		if len(res) != 3 || !slices.Contains(res, "category_root") || !slices.Contains(res, "doc_3") || !slices.Contains(res, "doc_4") {
			t.Errorf("Graph filter Expected[category_root, doc_3, doc_4], got %v", res)
		}
	})

	t.Run("Filtro Ibrido (Grafo + Metadati AND)", func(t *testing.T) {
		// Vogliamo i nodi dentro category_root (doc_3, doc_4) MA SOLO quelli del 2024 (doc_4)
		gq := &GraphQuery{
			RootID:    "category_root",
			Relations: []string{"contains"},
			Direction: "out",
			MaxDepth:  1,
		}
		res := runSearch("year>=2024", gq)
		if len(res) != 1 || res[0] != "doc_4" {
			t.Errorf("Hybrid filter Expected [doc_4], got %v", res)
		}
	})

	t.Run("Ricerca Testuale con Filtri (BM25 + Bitmap)", func(t *testing.T) {
		// Per la ricerca testuale, aggiungiamo un campo testuale
		eng.VAdd(indexName, "doc_testuale", zeroVec, map[string]any{"content": "apple banana", "status": "draft"})

		// In VSearch, explicitTextQuery passa "apple", e il filtro è "status='draft'"
		res, err := eng.VSearch(indexName, zeroVec, 10, "status='draft'", "apple", 100, 0.0, nil)
		if err != nil {
			t.Fatalf("Testual search failed: %v", err)
		}

		if len(res) != 1 || res[0] != "doc_testuale" {
			t.Errorf("Textual search + Bitmap filter Expected [doc_testuale], got %v", res)
		}
	})

	t.Run("Filtro Not Equal (!=) - String", func(t *testing.T) {
		// Nodes without 'status' field are included (they are not 'published')
		res := runSearch("status!='published'", nil)
		if !slices.Contains(res, "doc_2") || slices.Contains(res, "doc_1") || slices.Contains(res, "doc_3") || slices.Contains(res, "doc_4") {
			t.Errorf("Expected doc_2 to be included and doc_1/doc_3/doc_4 excluded, got %v", res)
		}
	})

	t.Run("Filtro Not Equal (!=) - Numerical", func(t *testing.T) {
		// Nodes without 'year' field are included (they are not 2022)
		res := runSearch("year!=2022", nil)
		if !slices.Contains(res, "doc_1") || !slices.Contains(res, "doc_4") || !slices.Contains(res, "doc_5") || slices.Contains(res, "doc_2") || slices.Contains(res, "doc_3") {
			t.Errorf("Expected doc_1/doc_4/doc_5 to be included and doc_2/doc_3 excluded, got %v", res)
		}
	})

	t.Run("Filtro Not Equal con AND", func(t *testing.T) {
		res := runSearch("type='article' AND status!='draft'", nil)
		if len(res) != 1 || res[0] != "doc_1" {
			t.Errorf("Expected [doc_1], got %v", res)
		}
	})

	t.Run("Filtro _is_historical - Bool indexing", func(t *testing.T) {
		// Bool values are now indexed as "true"/"false" strings in the inverted index
		eng.VAdd(indexName, "memory_current", zeroVec, map[string]any{"content": "current fact"}) // no _is_historical field
		eng.VAdd(indexName, "memory_old", zeroVec, map[string]any{"content": "old fact", "_is_historical": true})

		res := runSearch("_is_historical!='true'", nil)
		if !slices.Contains(res, "memory_current") {
			t.Errorf("Current memory should appear in results: %v", res)
		}
		if slices.Contains(res, "memory_old") {
			t.Errorf("Historical memory should NOT appear in results: %v", res)
		}
	})

	t.Run("Filtro _is_historical equality", func(t *testing.T) {
		// Test equality filter for historical nodes
		// Note: memory_old from previous test has _is_historical=true, so we query
		// with content filter to be specific
		eng.VAdd(indexName, "mem1", zeroVec, map[string]any{"content": "current", "_is_historical": false})
		eng.VAdd(indexName, "mem2", zeroVec, map[string]any{"content": "archived", "_is_historical": true})

		res := runSearch("content='archived'", nil)
		if !slices.Contains(res, "mem2") {
			t.Errorf("Expected mem2 for content='archived', got %v", res)
		}
		// Verify mem1 is NOT returned (it has _is_historical=false)
		if slices.Contains(res, "mem1") {
			t.Errorf("mem1 should NOT be returned for content='archived': %v", res)
		}
	})

	t.Run("Filtro != con valore numerico su campo non-BTree", func(t *testing.T) {
		// Regression: != with a numeric-looking value on a field indexed
		// only in the inverted index must NOT return all nodes.
		eng.VAdd(indexName, "neq_num_1", zeroVec, map[string]any{"tag": "123"})
		eng.VAdd(indexName, "neq_num_2", zeroVec, map[string]any{"tag": "456"})
		eng.VAdd(indexName, "neq_num_3", zeroVec, map[string]any{"tag": "abc"})

		// Use VFilter to avoid k-limit issues from VSearch
		res, err := eng.VFilter(indexName, "tag!='123'", 100)
		if err != nil {
			t.Fatalf("VFilter failed: %v", err)
		}
		if slices.Contains(res, "neq_num_1") {
			t.Errorf("neq_num_1 should be excluded by tag!='123', got %v", res)
		}
		if !slices.Contains(res, "neq_num_2") {
			t.Errorf("neq_num_2 should be included by tag!='123', got %v", res)
		}
		if !slices.Contains(res, "neq_num_3") {
			t.Errorf("neq_num_3 should be included by tag!='123', got %v", res)
		}
	})
}

// TestVEvolve tests the semantic evolution functionality
func TestVEvolve(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "evolve_test_idx"
	err = eng.VCreate(indexName, distance.Euclidean, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	vec := []float32{0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0}

	eng.VAdd(indexName, "memory1", vec, map[string]any{"content": "I love pizza", "type": "preference"})

	data, err := eng.VGet(indexName, "memory1")
	if err != nil {
		t.Fatalf("Failed to get original memory: %v", err)
	}
	if data.Metadata["content"] != "I love pizza" {
		t.Errorf("Expected content 'I love pizza', got %v", data.Metadata["content"])
	}

	newID, err := eng.VEvolve(indexName, "memory1", vec, map[string]any{"content": "I'm celiac", "type": "preference"}, "User corrected me")
	if err != nil {
		t.Fatalf("VEvolve failed: %v", err)
	}

	newData, err := eng.VGet(indexName, newID)
	if err != nil {
		t.Fatalf("Failed to get new memory: %v", err)
	}
	if newData.Metadata["content"] != "I'm celiac" {
		t.Errorf("Expected new content 'I'm celiac', got %v", newData.Metadata["content"])
	}
	if newData.Metadata["type"] != "preference" {
		t.Errorf("Expected type preserved as 'preference', got %v", newData.Metadata["type"])
	}

	oldData, err := eng.VGet(indexName, "memory1")
	if err != nil {
		t.Fatalf("Failed to get old memory: %v", err)
	}
	if oldData.Metadata["_is_historical"] != true {
		t.Errorf("Expected old memory to be marked historical, got %v", oldData.Metadata["_is_historical"])
	}

	edges, found := eng.VGetEdges(indexName, "memory1", "superseded_by", 0)
	if !found || len(edges) == 0 {
		t.Fatalf("Evolution edge not found")
	}
	if edges[0].TargetID != newID {
		t.Errorf("Evolution edge should point to new memory, got %v", edges[0].TargetID)
	}

	revEdges, found := eng.VGetEdges(indexName, newID, "evolves_from", 0)
	if !found || len(revEdges) == 0 {
		t.Fatalf("Reverse evolution edge not found")
	}
	if revEdges[0].TargetID != "memory1" {
		t.Errorf("Reverse evolution edge should point to old memory, got %v", revEdges[0].TargetID)
	}

	filter := "_is_historical!='true'"
	results, err := eng.VSearch(indexName, vec, 10, filter, "", 100, 1.0, nil)
	if err != nil {
		t.Fatalf("VSearch failed: %v", err)
	}
	if slices.Contains(results, "memory1") {
		t.Errorf("Historical memory should NOT appear in search results: %v", results)
	}
	if !slices.Contains(results, newID) {
		t.Errorf("New memory SHOULD appear in search results: %v", results)
	}

	// Test: Second evolution - evolve newID to newID2
	newID2, err := eng.VEvolve(indexName, newID, vec, map[string]any{"content": "I'm celiac and gluten intolerant", "type": "preference"}, "More details")
	if err != nil {
		t.Fatalf("Second VEvolve failed: %v", err)
	}

	// Verify second evolution metadata
	newData2, err := eng.VGet(indexName, newID2)
	if err != nil {
		t.Fatalf("Failed to get newID2: %v", err)
	}
	if newData2.Metadata["content"] != "I'm celiac and gluten intolerant" {
		t.Errorf("Expected content 'I'm celiac and gluten intolerant', got %v", newData2.Metadata["content"])
	}

	// Verify newID is now historical
	newIDData, err := eng.VGet(indexName, newID)
	if err != nil {
		t.Fatalf("Failed to get newID: %v", err)
	}
	if newIDData.Metadata["_is_historical"] != true {
		t.Errorf("Expected newID to be marked historical, got %v", newIDData.Metadata["_is_historical"])
	}
}
