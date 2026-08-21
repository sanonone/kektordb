package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestStressMaintenanceSnapshotSearch exercises the full maintenance surface
// concurrently for a sustained period: Vacuum, Refine, snapshot, search, add
// and delete all at once. This is the long-running validation for the P1-9
// refactor of reconnectNode (per-node shard locks around Connections) and the
// InternalID cleanup: any remaining race, deadlock, or SIGSEGV surfaces here.
//
// Run with -race; duration is bounded (~8s of churn).
func TestStressMaintenanceSnapshotSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("long stress test skipped in -short mode")
	}

	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const dim = 32
	const seedN = 400
	for i := 0; i < seedN; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if _, err := idx.Add(fmt.Sprintf("v%d", i), vec); err != nil {
			t.Fatalf("seed Add: %v", err)
		}
	}
	// Delete a third of the seed so Vacuum always has repair work.
	for i := 0; i < seedN; i += 3 {
		idx.Delete(fmt.Sprintf("v%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var errOnce sync.Once
	fail := func(format string, args ...any) {
		errOnce.Do(func() { t.Errorf(format, args...) })
	}

	// 1. Maintenance loop: vacuum + refine alternating.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			idx.MaintenanceRun("vacuum")
			idx.MaintenanceRun("refine")
		}
	}()

	// 2. Snapshot loop (gob encode path).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			idx.SnapshotData()
		}
	}()

	// 3. Add loop.
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
			if _, err := idx.Add(fmt.Sprintf("n%d", i), vec); err != nil {
				fail("Add: %v", err)
				return
			}
			i++
		}
	}()

	// 4. Delete loop (churns the deleted set, feeding Vacuum).
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
			idx.Delete(fmt.Sprintf("n%d", i))
			i++
		}
	}()

	// 5. Search loops.
	for g := 0; g < 3; g++ {
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
	}

	time.Sleep(8 * time.Second)
	close(stop)
	wg.Wait()

	// Sanity: search still works and returns sane results.
	results := idx.SearchWithScores(make([]float32, dim), 5, nil, 100)
	if len(results) > 5 {
		t.Fatalf("expected at most 5 results, got %d", len(results))
	}
}
