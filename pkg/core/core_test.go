package core

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

func TestCompressParallel(t *testing.T) {
	db := NewDB()
	indexName := "test_compress"

	// 1. Create initial Float32 Index
	err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "")
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
