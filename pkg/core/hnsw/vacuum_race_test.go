package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestVacuumConcurrentWithAddAndSearch reproduces P1-9: GraphOptimizer.Vacuum
// calls reconnectNode under metaMu.Lock and rewrites node.Connections WITHOUT
// taking the per-node LockNode, while Add Phase 3 writes Connections under
// LockNode and searches read them under RLockNode. The node-shard locks and
// metaMu are independent, so Vacuum and Add/Search can touch the same
// Connections slice concurrently.
//
// Run with -race: pre-fix it should report a DATA RACE; post-fix (per-node
// lock around the Connections rewrite) it passes.
func TestVacuumConcurrentWithAddAndSearch(t *testing.T) {
	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const dim = 32
	// Seed vectors, then delete a subset so Vacuum has repair work
	// (deleted nodes with live neighbors).
	for i := 0; i < 300; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if _, err := idx.Add(fmt.Sprintf("v%d", i), vec); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	for i := 0; i < 300; i += 7 {
		idx.Delete(fmt.Sprintf("v%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Vacuum loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			idx.optimizer.Vacuum()
		}
	}()

	// Add loop (writes Connections under LockNode).
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			vec := make([]float32, dim)
			for j := range vec {
				vec[j] = rand.Float32()
			}
			idx.Add(fmt.Sprintf("n%d", i), vec)
			i++
		}
	}()

	// Search loop (reads Connections under RLockNode + metaMu.RLock).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			q := make([]float32, dim)
			for j := range q {
				q[j] = rand.Float32()
			}
			idx.SearchWithScores(q, 10, nil, 100)
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
