package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestSnapshotConcurrentVacuumAddNoRace closes the coverage gap of the P1-9
// fix: SnapshotData, Vacuum and Add all touch node state (Connections under
// RLockNode/LockNode, the node slice under metaMu), and node.InternalID must
// never be written outside the Node literal. Run with -race: pre-fix (the
// InternalID writes added in Vacuum/Refine) this reports a DATA RACE between
// SnapshotData's write and Vacuum's read/write; post-fix it passes.
func TestSnapshotConcurrentVacuumAddNoRace(t *testing.T) {
	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const dim = 32
	// Seed, then delete a subset so Vacuum has repair work.
	for i := 0; i < 200; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if _, err := idx.Add(fmt.Sprintf("v%d", i), vec); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	for i := 0; i < 200; i += 5 {
		idx.Delete(fmt.Sprintf("v%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Snapshotter: repeated SnapshotData (writes InternalID pre-fix).
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

	// Vacuum loop (repairs connections, reads InternalID for shard locks).
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

	// Add loop.
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

	time.Sleep(1 * time.Second)
	close(stop)
	wg.Wait()

	// Sanity: the graph must still be usable after all the churn.
	idx.SearchWithScores(make([]float32, dim), 5, nil, 50)
}

// TestDBSnapshotConcurrentVacuum is defined in pkg/core (snapshot_map_race_test.go
// companion) because NewDB lives there — kept at the DB level to exercise the
// gob encode path alongside the index-level test above.
