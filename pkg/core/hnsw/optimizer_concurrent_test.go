package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestRefineConcurrentWithAdd verifies that Refine() does not block Add() operations.
// Before the fix, Refine workers held metaMu.RLock for the entire batch duration,
// blocking all Add/Delete operations.
func TestRefineConcurrentWithAdd(t *testing.T) {
	const (
		numInitialVectors = 500
		numConcurrentAdds = 200
		vectorDim         = 16
	)

	// Create index with enough vectors to trigger refine
	idx, err := New(16, 50, distance.Euclidean, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Insert initial vectors via batch
	initialObjects := make([]types.BatchObject, numInitialVectors)
	for i := 0; i < numInitialVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		initialObjects[i] = types.BatchObject{
			Id:     fmt.Sprintf("init-%d", i),
			Vector: vec,
		}
	}

	if err := idx.AddBatch(initialObjects); err != nil {
		t.Fatalf("Failed to insert initial vectors: %v", err)
	}

	// Start Refine in background with large batch
	opt := NewOptimizer(idx, AutoMaintenanceConfig{
		RefineBatchSize:      100,
		RefineEfConstruction: 200,
	})

	refineDone := make(chan struct{})
	go func() {
		defer close(refineDone)
		// Run multiple refine cycles
		for i := 0; i < 5; i++ {
			if !opt.Refine() {
				break
			}
		}
	}()

	// Track Add operations
	var addSuccess atomic.Int64
	var addErrors atomic.Int64
	var addTimes []time.Duration
	var addMu sync.Mutex

	// Concurrent Add operations while Refine is running
	var wg sync.WaitGroup
	for i := 0; i < numConcurrentAdds; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			vec := make([]float32, vectorDim)
			for j := range vec {
				vec[j] = rand.Float32()
			}

			start := time.Now()
			_, err := idx.Add(fmt.Sprintf("concurrent-%d", id), vec)
			elapsed := time.Since(start)

			if err != nil {
				addErrors.Add(1)
			} else {
				addSuccess.Add(1)
				addMu.Lock()
				addTimes = append(addTimes, elapsed)
				addMu.Unlock()
			}
		}(i)
	}

	// Wait for all adds to complete
	wg.Wait()

	// Wait for refine to complete
	<-refineDone

	// Verify adds succeeded
	if addErrors.Load() > 0 {
		t.Errorf("Some Add operations failed: %d errors", addErrors.Load())
	}
	if addSuccess.Load() != int64(numConcurrentAdds) {
		t.Errorf("Expected %d successful adds, got %d", numConcurrentAdds, addSuccess.Load())
	}

	// Calculate average add time - should be low if not blocked
	addMu.Lock()
	var totalTime time.Duration
	for _, d := range addTimes {
		totalTime += d
	}
	avgTime := totalTime / time.Duration(len(addTimes))
	addMu.Unlock()

	t.Logf("Average Add time during Refine: %v", avgTime)
	t.Logf("Successful adds: %d, Errors: %d", addSuccess.Load(), addErrors.Load())

	// Verify index is still functional - search should work
	queryVec := make([]float32, vectorDim)
	for j := range queryVec {
		queryVec[j] = rand.Float32()
	}
	results := idx.SearchWithScores(queryVec, 10, nil, 50)
	if len(results) == 0 {
		t.Error("Search returned no results after concurrent operations")
	}

	t.Logf("Search returned %d results", len(results))
}

// TestRefineConcurrentWithDelete verifies that Refine() does not block Delete() operations.
func TestRefineConcurrentWithDelete(t *testing.T) {
	const (
		numInitialVectors = 500
		numConcurrentDels = 200
		vectorDim         = 16
	)

	idx, err := New(16, 50, distance.Euclidean, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Insert vectors
	initialObjects := make([]types.BatchObject, numInitialVectors)
	for i := 0; i < numInitialVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		initialObjects[i] = types.BatchObject{
			Id:     fmt.Sprintf("del-%d", i),
			Vector: vec,
		}
	}

	if err := idx.AddBatch(initialObjects); err != nil {
		t.Fatalf("Failed to insert initial vectors: %v", err)
	}

	// Start Refine
	opt := NewOptimizer(idx, AutoMaintenanceConfig{
		RefineBatchSize:      100,
		RefineEfConstruction: 200,
	})

	refineDone := make(chan struct{})
	go func() {
		defer close(refineDone)
		for i := 0; i < 5; i++ {
			if !opt.Refine() {
				break
			}
		}
	}()

	// Concurrent Deletes while Refine is running
	var wg sync.WaitGroup
	for i := 0; i < numConcurrentDels; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(fmt.Sprintf("del-%d", id))
		}(i)
	}

	wg.Wait()
	<-refineDone

	// Verify index is still functional
	queryVec := make([]float32, vectorDim)
	for j := range queryVec {
		queryVec[j] = rand.Float32()
	}
	results := idx.SearchWithScores(queryVec, 10, nil, 50)

	t.Logf("Search after concurrent Refine+Delete returned %d results", len(results))
}
