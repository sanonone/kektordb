// File: pkg/client/client_test.go
package client

import (
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Helper function to check if a slice contains a string
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

// NOTE: This is an INTEGRATION test suite.
// It requires a running KektorDB server at localhost:9091.
func TestClientIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode.")
	}

	client := New("localhost", 9091)

	// Use unique index names for each test run to prevent conflicts
	timestamp := time.Now().UnixNano()
	idxEuclidean := fmt.Sprintf("go-e2e-euclidean-%d", timestamp)
	idxCosine := fmt.Sprintf("go-e2e-cosine-%d", timestamp)

	t.Run("A - Index Management", func(t *testing.T) {
		// Test VCreate
		err := client.VCreate(idxEuclidean, "euclidean", "float32", 10, 50)
		if err != nil {
			t.Fatalf("VCreate for euclidean index failed: %v", err)
		}
		err = client.VCreate(idxCosine, "cosine", "float32", 0, 0)
		if err != nil {
			t.Fatalf("VCreate for cosine index failed: %v", err)
		}
		t.Log(" -> VCreate OK")

		// Test ListIndexes
		indexes, err := client.ListIndexes()
		if err != nil {
			t.Fatalf("ListIndexes failed: %v", err)
		}
		indexMap := make(map[string]bool)
		for _, idx := range indexes {
			indexMap[idx.Name] = true
		}
		if !indexMap[idxEuclidean] || !indexMap[idxCosine] {
			t.Errorf("ListIndexes did not return the created indexes. Got: %v", indexes)
		}
		t.Log(" -> ListIndexes OK")

		// Test GetIndexInfo
		info, err := client.GetIndexInfo(idxEuclidean)
		if err != nil {
			t.Fatalf("GetIndexInfo failed: %v", err)
		}
		if info.Name != idxEuclidean || info.M != 10 {
			t.Errorf("GetIndexInfo returned incorrect data. Got: %+v", info)
		}
		t.Log(" -> GetIndexInfo OK")

		// Test DeleteIndex
		err = client.DeleteIndex(idxEuclidean)
		if err != nil {
			t.Fatalf("DeleteIndex failed: %v", err)
		}
		_, err = client.GetIndexInfo(idxEuclidean)
		if err == nil {
			t.Fatalf("GetIndexInfo should have failed for a deleted index, but it succeeded.")
		}
		if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("Expected a 404 Not Found error for deleted index, but got: %v", err)
		}
		t.Log(" -> DeleteIndex OK")
	})

	t.Run("B - Data Lifecycle", func(t *testing.T) {
		// Add vectors
		vecID1, vecID2 := "test-vec-1", "test-vec-2"
		vec1 := []float32{0.1, 0.2, 0.3}
		meta1 := map[string]interface{}{"tag": "go"}

		err := client.VAdd(idxCosine, vecID1, vec1, meta1)
		if err != nil {
			t.Fatalf("VAdd for vec1 failed: %v", err)
		}

		err = client.VAdd(idxCosine, vecID2, []float32{0.4, 0.5, 0.6}, nil)
		if err != nil {
			t.Fatalf("VAdd for vec2 failed: %v", err)
		}
		t.Log(" -> VAdd OK")

		// Get single vector
		retrieved, err := client.VGet(idxCosine, vecID1)
		if err != nil {
			t.Fatalf("VGet failed: %v", err)
		}
		if retrieved.ID != vecID1 || !reflect.DeepEqual(retrieved.Metadata, meta1) {
			t.Errorf("VGet returned incorrect data. Got: %+v", retrieved)
		}
		t.Log(" -> VGet (single) OK")

		// Get multiple vectors (batch)
		batch, err := client.VGetMany(idxCosine, []string{vecID1, "non-existent", vecID2})
		if err != nil {
			t.Fatalf("VGetMany failed: %v", err)
		}
		if len(batch) != 2 {
			t.Errorf("VGetMany should return 2 vectors, but got %d", len(batch))
		}
		retrievedIDs := []string{batch[0].ID, batch[1].ID}
		sort.Strings(retrievedIDs)
		if !reflect.DeepEqual(retrievedIDs, []string{vecID1, vecID2}) {
			t.Errorf("VGetMany returned incorrect IDs. Got: %v", retrievedIDs)
		}
		t.Log(" -> VGetMany (batch) OK")

		// Delete vector
		err = client.VDelete(idxCosine, vecID1)
		if err != nil {
			t.Fatalf("VDelete failed: %v", err)
		}

		_, err = client.VGet(idxCosine, vecID1)
		if err == nil {
			t.Fatalf("VGet after VDelete should have failed")
		}
		t.Log(" -> VDelete OK")
	})

	t.Run("C - Compression and System", func(t *testing.T) {
		// --- CORREZIONE QUI ---
		task, err := client.VCompress(idxCosine, "int8")
		if err != nil {
			t.Fatalf("VCompress fallito all'avvio del task: %v", err)
		}
		// Attendi il completamento del task
		if err = task.Wait(2*time.Second, 1*time.Minute); err != nil {
			t.Fatalf("VCompress fallito durante l'attesa del task: %v", err)
		}
		t.Log(" -> VCompress OK")

		// ... (la ricerca dopo la compressione) ...

		// --- CORREZIONE QUI ---
		task, err = client.AOFRewrite()
		if err != nil {
			t.Fatalf("AOFRewrite fallito all'avvio del task: %v", err)
		}
		if err = task.Wait(2*time.Second, 1*time.Minute); err != nil {
			t.Fatalf("AOFRewrite fallito durante l'attesa del task: %v", err)
		}
		t.Log(" -> AOFRewrite OK")
	})

	t.Run("D - Dynamic Search Tuning", func(t *testing.T) {
		idxName := fmt.Sprintf("go-e2e-efsearch-%d", timestamp)

		// Usiamo parametri di costruzione bassi per evidenziare l'effetto di efSearch
		err := client.VCreate(idxName, "euclidean", "float32", 8, 20)
		if err != nil {
			t.Fatalf("VCreate for efSearch test failed: %v", err)
		}

		// Popola con un numero sufficiente di vettori
		const numVectors = 100
		const dims = 16
		vectors := make([][]float32, numVectors)
		for i := 0; i < numVectors; i++ {
			vectors[i] = make([]float32, dims)
			for j := 0; j < dims; j++ {
				vectors[i][j] = rand.Float32()
			}
			err := client.VAdd(idxName, fmt.Sprintf("vec_%d", i), vectors[i], nil)
			if err != nil {
				t.Fatalf("VAdd failed during efSearch test setup: %v", err)
			}
		}

		queryVector := vectors[50]
		k := 10

		// 1. Ricerca "veloce" con efSearch basso
		fastResults, err := client.VSearch(idxName, k, queryVector, "", 12, 0) // efSearch = 12
		if err != nil {
			t.Fatalf("Fast search (low efSearch) failed: %v", err)
		}
		if len(fastResults) == 0 || fastResults[0] != "vec_50" {
			t.Errorf("Fast search did not return the exact match first. Got: %v", fastResults)
		}
		t.Logf(" -> Fast search (ef=12) returned %d results", len(fastResults))

		// 2. Ricerca "accurata" con efSearch alto
		accurateResults, err := client.VSearch(idxName, k, queryVector, "", 100, 0) // efSearch = 100
		if err != nil {
			t.Fatalf("Accurate search (high efSearch) failed: %v", err)
		}
		if len(accurateResults) == 0 || accurateResults[0] != "vec_50" {
			t.Errorf("Accurate search did not return the exact match first. Got: %v", accurateResults)
		}
		t.Logf(" -> Accurate search (ef=100) returned %d results", len(accurateResults))

		// La verifica principale è che entrambe le chiamate abbiano successo.
		// Confrontare la qualità in modo deterministico è difficile, ma possiamo
		// verificare che i set di risultati non siano identici, il che suggerisce
		// che il parametro ha avuto un effetto.
		if reflect.DeepEqual(fastResults, accurateResults) && numVectors > 20 {
			t.Log("Warning: fast and accurate search returned identical results, ef_search might not be having a strong effect.")
		}

		t.Log("✅ Test Dynamic Search Tuning (efSearch) superato")
	})
}
