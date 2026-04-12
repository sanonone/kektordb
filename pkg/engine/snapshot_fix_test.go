package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestSnapshotEndSnapshotModeCalledOnce verifies that EndSnapshotMode is called exactly once
// and doesn't produce the "snapshot mode not active" error.
// This is a regression test for the bug where EndSnapshotMode was called twice.
func TestSnapshotEndSnapshotModeCalledOnce(t *testing.T) {
	tempDir := t.TempDir()
	opts := DefaultOptions(tempDir)

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Create an index and add some data
	if err := eng.VCreate("test_idx", distance.Euclidean, 8, 100, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add some vectors
	for i := 0; i < 10; i++ {
		vec := []float32{float32(i), 0, 0, 0, 0, 0, 0, 0}
		if err := eng.VAdd("test_idx", string(rune('A'+i)), vec, nil); err != nil {
			t.Fatalf("Failed to add vector: %v", err)
		}
	}

	// Flush to ensure data is persisted
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Trigger multiple snapshots rapidly to ensure no race conditions
	for i := 0; i < 3; i++ {
		if err := eng.SaveSnapshot(); err != nil {
			t.Fatalf("Snapshot %d failed: %v", i+1, err)
		}
	}

	// Close engine
	if err := eng.Close(); err != nil {
		t.Fatalf("Failed to close engine: %v", err)
	}

	// Restart and verify
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to restart engine: %v", err)
	}
	defer eng2.Close()

	// Verify index exists
	if _, exists := eng2.DB.GetVectorIndex("test_idx"); !exists {
		t.Fatal("Index not found after restart")
	}

	t.Log("EndSnapshotMode called once test passed")
}

// TestSnapshotWithConcurrentWritesDuringSnapshot verifies the shadow buffer works
// correctly when there are concurrent writes during snapshot.
func TestSnapshotWithConcurrentWritesDuringSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	opts := DefaultOptions(tempDir)

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Create index
	if err := eng.VCreate("concurrent_idx", distance.Euclidean, 8, 100, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add initial vectors
	for i := 0; i < 5; i++ {
		vec := []float32{float32(i), 0, 0, 0, 0, 0, 0, 0}
		if err := eng.VAdd("concurrent_idx", string(rune('A'+i)), vec, nil); err != nil {
			t.Fatalf("Failed to add initial vector: %v", err)
		}
	}

	// Flush initial data
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Start concurrent writers
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
					key := string(rune('a'+id)) + string(rune('0'+counter%10))
					vec := []float32{float32(id*100 + counter), 1, 2, 3, 4, 5, 6, 7}
					eng.VAdd("concurrent_idx", key, vec, nil)
					counter++
					time.Sleep(time.Millisecond)
				}
			}
		}(worker)
	}

	// Let writers start
	time.Sleep(10 * time.Millisecond)

	// Perform multiple snapshots while writers are active
	for i := 0; i < 3; i++ {
		time.Sleep(20 * time.Millisecond)
		if err := eng.SaveSnapshot(); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("Snapshot %d failed: %v", i+1, err)
		}
		t.Logf("Snapshot %d completed successfully", i+1)
	}

	// Stop writers
	close(stop)
	wg.Wait()

	// Close and reopen
	if err := eng.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to restart: %v", err)
	}
	defer eng2.Close()

	// Verify index exists
	if _, exists := eng2.DB.GetVectorIndex("concurrent_idx"); !exists {
		t.Fatal("Index not found after restart")
	}

	t.Log("Concurrent writes during snapshot test passed")
}
