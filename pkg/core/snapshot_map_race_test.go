package core

import (
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestSnapshotConcurrentAddNoMapRace reproduces P1-1: Index.SnapshotData
// returned the externalToInternalID map by reference, and DB.Snapshot gob-
// encodes it AFTER the per-index lock is released. A concurrent Add/Delete
// then triggers the runtime fatal "concurrent map iteration and map write".
//
// Pre-fix this test panics; post-fix (map copied under the lock) it passes
// cleanly under -race.
func TestSnapshotConcurrentAddNoMapRace(t *testing.T) {
	tmpDir := t.TempDir()
	db := NewDB()
	defer db.Close()

	indexName := "race_snapshot"
	arenaDir := tmpDir + "/arenas/" + indexName
	if err := db.CreateVectorIndex(indexName, distance.Cosine, 16, 100, distance.Float32, "", arenaDir); err != nil {
		t.Fatalf("CreateVectorIndex: %v", err)
	}
	idx, _ := db.GetVectorIndex(indexName)

	// Seed a few vectors so the external→internal map is non-empty.
	for i := 0; i < 10; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		if _, err := idx.Add(fmt.Sprintf("seed-%d", i), vec); err != nil {
			t.Fatalf("seed Add: %v", err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: continuous Add + occasional Delete (both write the map).
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				vec := make([]float32, 32)
				for j := range vec {
					vec[j] = rand.Float32()
				}
				id := fmt.Sprintf("w%d-%d", g, i)
				if _, err := idx.Add(id, vec); err == nil && i%7 == 0 {
					idx.Delete(id)
				}
				i++
			}
		}(g)
	}

	// Snapshotter: repeated DB.Snapshot — the gob encode iterates the map.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := db.Snapshot(io.Discard); err != nil {
				t.Errorf("Snapshot: %v", err)
				return
			}
		}
	}()

	time.Sleep(1 * time.Second)
	close(stop)
	wg.Wait()
}
