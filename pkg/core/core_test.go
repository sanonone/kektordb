package core

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

func TestCompressParallel(t *testing.T) {
	tmpDir := t.TempDir()
	db := NewDB()
	defer db.Close() // Assicurati che le risorse vengano liberate

	indexName := "test_compress"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	// 1. Create initial Float32 Index con Arena valida
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	idx, _ := db.GetVectorIndex(indexName)

	// 2. Populate with data
	const NumVectors = 2000
	const Dims = 32

	for i := 0; i < NumVectors; i++ {
		vec := make([]float32, Dims)
		for j := 0; j < Dims; j++ {
			vec[j] = rand.Float32()
		}
		id := fmt.Sprintf("doc-%d", i)
		internalID, _ := idx.Add(id, vec)

		db.AddMetadata(indexName, internalID, map[string]any{
			"key": "value",
			"id":  i,
		})
	}

	t.Logf("Populated index with %d vectors. Compressing to Int8...", NumVectors)

	// 3. Compress
	err = db.Compress(indexName, distance.Int8)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// 4. Verify Data Integrity
	_, ok := db.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Index missing after compression")
	}

	checkID := "doc-100"
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
	tmpDir := t.TempDir()

	// Crea un DB con dati
	db := NewDB()

	// 1. Crea indice vettoriale (Usando l'Arena!)
	indexName := "test_snapshot"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 200, distance.Float32, "", arenaDir)
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

	// IMPORTANTE: Chiudiamo il vecchio DB per sganciare i file Mmap (Cruciale su Windows!)
	db.Close()

	// 4. Crea un nuovo DB e carica lo snapshot
	newDB := NewDB()
	defer newDB.Close()

	// Passiamo tmpDir come basePath per far ritrovare l'Arena!
	err = newDB.LoadFromSnapshot(&buf, tmpDir)
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	// 5. Verifica i dati
	val1, found := newDB.GetKVStore().Get("key1")
	if !found || string(val1) != "value1" {
		t.Errorf("KV data mismatch: got %s, want value1", string(val1))
	}

	newIdx, ok := newDB.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Vector index not found in reloaded DB")
	}

	// Verifica ricerca (Se l'Arena non fosse stata ricaricata, i vettori sarebbero vuoti)
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
	tmpDir := t.TempDir()
	db := NewDB()

	indexName := "test_deleted_snapshot"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 200, distance.Float32, "", arenaDir)
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

	var buf bytes.Buffer
	err = db.Snapshot(&buf)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Sgancia l'Arena
	db.Close()

	newDB := NewDB()
	defer newDB.Close()

	err = newDB.LoadFromSnapshot(&buf, tmpDir)
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	_, ok := newDB.GetVectorIndex(indexName)
	if !ok {
		t.Fatal("Vector index not found after reload")
	}

	t.Log("Snapshot with deleted nodes completed successfully")
}

func TestSnapshotWithComplexMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	db := NewDB()
	defer db.Close()

	indexName := "test_complex_metadata"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	// Create index
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	idx, _ := db.GetVectorIndex(indexName)

	// Add node with complex metadata (arrays and nested objects)
	vec := []float32{0.1, 0.2, 0.3, 0.4}
	internalID, _ := idx.Add("vec1", vec)

	// This is the problematic metadata that causes gob errors
	err = db.AddMetadata(indexName, internalID, map[string]any{
		"tags":     []string{"important", "urgent"},
		"nested":   map[string]any{"key": "value", "count": 42},
		"scores":   []float64{1.5, 2.5, 3.5},
		"metadata": map[string]any{"array": []int{1, 2, 3}},
	})
	if err != nil {
		t.Fatalf("Failed to add metadata: %v", err)
	}

	// Create snapshot
	var buf bytes.Buffer
	err = db.Snapshot(&buf)
	if err != nil {
		t.Fatalf("Snapshot failed with complex metadata: %v", err)
	}

	// Reload from snapshot
	newDB := NewDB()
	defer newDB.Close()

	err = newDB.LoadFromSnapshot(&buf, tmpDir)
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	// Verify metadata integrity
	meta := newDB.GetMetadataForNode(indexName, internalID)
	if meta == nil {
		t.Fatal("Metadata not found after reload")
	}

	// Verify array preservation
	tags, ok := meta["tags"].([]interface{})
	if !ok || len(tags) != 2 {
		t.Fatalf("Tags not preserved correctly: %v", meta["tags"])
	}

	t.Log("Snapshot with complex metadata completed successfully")
}
