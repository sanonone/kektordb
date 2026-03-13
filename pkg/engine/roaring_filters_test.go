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
		if len(res) != 2 || !slices.Contains(res, "doc_3") || !slices.Contains(res, "doc_4") {
			t.Errorf("Graph filter Expected[doc_3, doc_4], got %v", res)
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
}
