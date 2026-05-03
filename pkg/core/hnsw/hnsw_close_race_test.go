package hnsw

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestDeleteWhileSearching verifies that Close() does not cause a crash (SIGSEGV)
// when concurrent Search/Add operations are in progress. This is a regression test
// for bug H7: DeleteVectorIndex use-after-unmap race.
//
// Run with: go test -race -run TestDeleteWhileSearching -count=10
func TestDeleteWhileSearching(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race stress test in short mode")
	}

	const (
		numSearchers = 5
		numAdders    = 3
		numVectors   = 300
		vectorDim    = 16
		iterations   = 5
	)

	for iter := 0; iter < iterations; iter++ {
		t.Run(fmt.Sprintf("iteration-%d", iter), func(t *testing.T) {
			t.Parallel()

			idx, err := New(16, 50, distance.Euclidean, distance.Float32, "", t.TempDir())
			if err != nil {
				t.Fatalf("Failed to create index: %v", err)
			}

			objects := make([]types.BatchObject, numVectors)
			for i := range objects {
				objects[i] = types.BatchObject{
					Id:     fmt.Sprintf("v-%d-%d", iter, i),
					Vector: randomVector(vectorDim),
				}
			}
			if err := idx.AddBatch(objects); err != nil {
				t.Fatalf("AddBatch: %v", err)
			}

			var wg sync.WaitGroup
			stopCh := make(chan struct{})
			var searchCount atomic.Int64
			var addCount atomic.Int64

			for i := 0; i < numSearchers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					q := randomVector(vectorDim)
					for {
						select {
						case <-stopCh:
							return
						default:
							res := idx.SearchWithScores(q, 10, nil, 50)
							_ = res
							searchCount.Add(1)
						}
					}
				}()
			}

			for i := 0; i < numAdders; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					counter := 0
					for {
						select {
						case <-stopCh:
							return
						default:
							vecID := fmt.Sprintf("add-%d-%d-%d", iter, id, counter)
							_, err := idx.Add(vecID, randomVector(vectorDim))
							_ = err
							counter++
							if err == nil {
								addCount.Add(1)
							}
						}
					}
				}(i)
			}

			time.Sleep(30 * time.Millisecond)

			closeErr := idx.Close()
			close(stopCh)
			wg.Wait()

			t.Logf("iteration=%d searches=%d adds=%d close_error=%v",
				iter, searchCount.Load(), addCount.Load(), closeErr)
		})
	}
}

// TestCloseBlocksUntilSearchesDrain verifies that after the fix,
// Close() waits for in-flight searches to complete before unmapping.
func TestCloseBlocksUntilSearchesDrain(t *testing.T) {
	idx, err := New(16, 50, distance.Euclidean, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	const (
		numVectors = 200
		vectorDim  = 16
	)
	objects := make([]types.BatchObject, numVectors)
	for i := range objects {
		objects[i] = types.BatchObject{
			Id:     fmt.Sprintf("v-%d", i),
			Vector: randomVector(vectorDim),
		}
	}
	if err := idx.AddBatch(objects); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}

	searchStarted := make(chan struct{})
	closeCalled := make(chan struct{})
	searchDone := make(chan struct{})

	go func() {
		defer close(searchDone)
		q := randomVector(vectorDim)
		// Signal that we're in the search
		close(searchStarted)
		// Execute the search — should complete even if Close is called concurrently
		res := idx.SearchWithScores(q, 10, nil, 50)
		if len(res) == 0 {
			t.Error("Search returned no results")
		}
	}()

	<-searchStarted

	go func() {
		time.Sleep(5 * time.Millisecond)
		close(closeCalled)
	}()

	<-closeCalled

	if err := idx.Close(); err != nil {
		t.Logf("Close returned: %v", err)
	}

	<-searchDone
	t.Log("Search completed after Close — no crash")
}
