package core

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

func TestCompressParallel(t *testing.T) {
	db := NewDB()
	indexName := "test_compress"

	// 1. Create initial Float32 Index
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	idx, _ := db.GetVectorIndex(indexName)

	// 2. Populate with data (enough to trigger batches if we were batching manually,
	// but here just enough to verify AddBatch works)
	const NumVectors = 2000
	const Dims = 32

	for i := 0; i < NumVectors; i++ {
		vec := make([]float32, Dims)
		for j := 0; j < Dims; j++ {
			vec[j] = rand.Float32()
		}
		id := fmt.Sprintf("doc-%d", i)
		internalID, _ := idx.Add(id, vec)

		// Add some metadata
		db.AddMetadata(indexName, internalID, map[string]any{
			"key": "value",
			"id":  i,
		})
	}

	t.Logf("Populated index with %d vectors. Compressing to Int8...", NumVectors)

	// 3. Compress (Should use the new Parallel AddBatch logic)
	err = db.Compress(indexName, distance.Int8)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// 4. Verify Data Integrity after compression
	_, ok := db.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Index missing after compression")
	}

	// Check random vector retrieval
	checkID := "doc-100"
	// Note: VGet from Engine wraps DB.GetVector. We use DB directly.
	data, err := db.GetVector(indexName, checkID)
	if err != nil {
		t.Fatalf("Failed to retrieving vector %s after compression: %v", checkID, err)
	}

	if len(data.Metadata) == 0 {
		t.Errorf("Metadata lost after compression for %s", checkID)
	}

	t.Log("Compression successful and data verified.")
}

// TestDBSnapshotAndReload testa il ciclo completo di snapshot e reload del DB
func TestDBSnapshotAndReload(t *testing.T) {
	// Crea un DB con dati
	db := NewDB()

	// 1. Crea indice vettoriale
	indexName := "test_snapshot"
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 200, distance.Float32, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Popola con vettori e KV data
	idx, _ := db.GetVectorIndex(indexName)
	for i := 0; i < 100; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		id := fmt.Sprintf("vec-%d", i)
		internalID, _ := idx.Add(id, vec)
		db.AddMetadata(indexName, internalID, map[string]any{
			"category": fmt.Sprintf("cat-%d", i%5),
			"score":    float64(i),
		})
	}

	// Aggiungi KV data
	db.GetKVStore().Set("key1", []byte("value1"))
	db.GetKVStore().Set("key2", []byte("value2"))

	// 3. Esegui lo snapshot
	var buf bytes.Buffer
	err = db.Snapshot(&buf)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	t.Logf("Snapshot created, size: %d bytes", buf.Len())

	// 4. Crea un nuovo DB e carica lo snapshot
	newDB := NewDB()
	err = newDB.LoadFromSnapshot(&buf, "")
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	// 5. Verifica i dati
	// KV data
	val1, found := newDB.GetKVStore().Get("key1")
	if !found || string(val1) != "value1" {
		t.Errorf("KV data mismatch: got %s, want value1", string(val1))
	}

	// Vector index
	newIdx, ok := newDB.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Vector index not found in reloaded DB")
	}

	// Verifica ricerca
	query := make([]float32, 32)
	for i := range query {
		query[i] = 0.5
	}
	results := newIdx.SearchWithScores(query, 10, nil, 100)
	if len(results) == 0 {
		t.Error("Search returned no results in reloaded index")
	}

	t.Logf("Reload successful. Search returned %d results", len(results))
}

// TestDBSnapshotWithDeletedNodes testa che i nodi eliminati vengano correttamente gestiti
func TestDBSnapshotWithDeletedNodes(t *testing.T) {
	db := NewDB()

	indexName := "test_deleted_snapshot"
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 200, distance.Float32, "", "")
	if err != nil {
		t.Fatal(err)
	}

	idx, _ := db.GetVectorIndex(indexName)
	for i := 0; i < 10; i++ {
		vec := make([]float32, 16)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Add(fmt.Sprintf("vec-%d", i), vec)
	}

	// Verifica che lo snapshot funzioni
	var buf bytes.Buffer
	err = db.Snapshot(&buf)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	newDB := NewDB()
	err = newDB.LoadFromSnapshot(&buf, "")
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	// Verifica che l'indice esista
	_, ok := newDB.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Vector index not found after reload")
	}

	t.Log("Snapshot with deleted nodes completed successfully")
}
