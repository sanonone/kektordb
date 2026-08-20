package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// TestRunGraphVacuumConcurrentIndexChurnNoRace reproduces P1-7:
// RunGraphVacuum iterated the index map via GetVectorIndexInfoUnlocked
// without holding s.mu, racing with concurrent create/delete index.
//
// Run with -race: pre-fix it reports a DATA RACE; post-fix it passes.
func TestRunGraphVacuumConcurrentIndexChurnNoRace(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	// Seed a maintenance config with graph retention so the vacuum does work.
	createTestIndex(t, eng, "base")
	retentionCfg := hnsw.AutoMaintenanceConfig{GraphRetention: hnsw.Duration(24 * time.Hour)}
	if err := eng.VUpdateIndexConfig("base", retentionCfg); err != nil {
		t.Fatalf("VUpdateIndexConfig: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Index churn: create/delete scratch indexes (writes to the index map).
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
			name := fmt.Sprintf("scratch%d", i%3)
			eng.VCreate(name, distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil)
			eng.VDeleteIndex(name)
			i++
		}
	}()

	// Graph vacuum loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			eng.RunGraphVacuum()
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
