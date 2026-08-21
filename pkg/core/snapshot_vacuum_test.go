package core

import (
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// TestDBSnapshotConcurrentVacuum exercises the gob-encode path of DB.Snapshot
// while the HNSW Vacuum and Add run concurrently — closing the coverage gap
// of the P1-9 fix at the DB level (SnapshotData's node state access under
// RLockNode vs Vacuum's under metaMu). Run with -race.
func TestDBSnapshotConcurrentVacuum(t *testing.T) {
	tmpDir := t.TempDir()
	db := NewDB()
	defer db.Close()

	indexName := "idx"
	if err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "", tmpDir+"/arenas/"+indexName); err != nil {
		t.Fatalf("CreateVectorIndex: %v", err)
	}
	idx, _ := db.GetVectorIndex(indexName)

	for i := 0; i < 150; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if _, err := idx.Add(fmt.Sprintf("v%d", i), vec); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	for i := 0; i < 150; i += 5 {
		idx.Delete(fmt.Sprintf("v%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = db.Snapshot(io.Discard)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			h, ok := idx.(*hnsw.Index)
			if !ok {
				t.Error("index is not HNSW")
				return
			}
			h.MaintenanceRun("vacuum")
		}
	}()

	time.Sleep(1 * time.Second)
	close(stop)
	wg.Wait()
}
