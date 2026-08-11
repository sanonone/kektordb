package core

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// newIndexedDB creates a DB with one populated index (name, n nodes with
// metadata) and returns it plus the index pointer.
func newIndexedDB(t *testing.T, name string, n int) (*DB, *hnsw.Index) {
	t.Helper()
	db := NewDB()
	arenaDir := filepath.Join(t.TempDir(), "arenas", name)
	if err := db.CreateVectorIndex(name, distance.Cosine, 16, 100, distance.Float32, "english", arenaDir); err != nil {
		t.Fatalf("CreateVectorIndex: %v", err)
	}
	idxAny, _ := db.GetVectorIndex(name)
	idx := idxAny.(*hnsw.Index)
	for i := 0; i < n; i++ {
		vec := []float32{float32(i) / 1000, 0.2, 0.3, 0.4}
		id, err := idx.Add(string(rune('a'+i%26))+itoa(i), vec)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if err := db.AddMetadata(name, id, map[string]any{"content": "node " + itoa(i), "idx": i}); err != nil {
			t.Fatalf("AddMetadata: %v", err)
		}
	}
	return db, idx
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// indexClosed reports whether the index is closed, using its public behavior:
// Add on a closed index returns "index is closed".
func indexClosed(idx *hnsw.Index) bool {
	_, err := idx.Add("closed-probe", []float32{0.1, 0.2, 0.3, 0.4})
	return err != nil && strings.Contains(err.Error(), "closed")
}

// TestLockOrderSnapshotVsClose reproduces the ABBA lock cycle deterministically:
//  1. Snapshot starts and passes phase 1: it holds each index's idxMu.RLock
//     and has released s.mu (detected via TryLock polling).
//  2. The main goroutine then acquires s.mu.Lock (DeleteVectorIndex-style)
//     while Snapshot is mid-flight.
//  3. Main calls idx.Close() → metaMu.Lock.
//
// With the fix, Snapshot holds no metaMu.RLock across the loop (SnapshotData
// self-locks internally) and never re-acquires s.mu — both complete. Without
// the fix this deadlocks (either on the reentrant metaMu.RLock inside
// SnapshotData or on the s.mu re-acquisition).
func TestLockOrderSnapshotVsClose(t *testing.T) {
	db, idx := newIndexedDB(t, "lockorder", 5000)
	defer db.Close()

	snapDone := make(chan struct{})
	go func() {
		defer close(snapDone)
		_ = db.Snapshot(io.Discard)
	}()

	// Wait until Snapshot is past phase 1: idxMu is held by Snapshot
	// (TryLock fails) while s.mu is free (TryLock succeeds).
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-snapDone:
			t.Fatal("Snapshot finished before s.mu could be acquired — interleaving not reproduced")
		default:
		}

		idxMu := db.indexLocks["lockorder"]
		idxMuHeld := false
		if idxMu.TryLock() {
			// We won the write lock — Snapshot hasn't reached phase 1 yet.
			idxMu.Unlock()
		} else {
			// Snapshot holds idxMu.RLock (phase 1 passed).
			idxMuHeld = true
		}

		if idxMuHeld && db.mu.TryLock() {
			// Snapshot is mid-flight and we hold s.mu.Lock.
			defer db.mu.Unlock()
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Snapshot to pass phase 1")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Snapshot is now mid-flight. Close must complete (metaMu is not held by
	// Snapshot) and Snapshot must complete (it needs no s.mu).
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- idx.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Logf("Close returned error (acceptable in this interleaving): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: idx.Close() blocked while Snapshot was running")
	}

	select {
	case <-snapDone:
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: Snapshot blocked while s.mu.Lock was held")
	}
}

// TestSnapshotConcurrentDeleteNoDeadlock stresses Snapshot against
// DeleteVectorIndex and DB.Close with the race detector (skipped in -short).
func TestSnapshotConcurrentDeleteNoDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	for iter := 0; iter < 30; iter++ {
		db, _ := newIndexedDB(t, "stress", 200)

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			_ = db.Snapshot(&buf)
		}()
		go func() {
			defer wg.Done()
			_ = db.DeleteVectorIndex("stress")
		}()
		go func() {
			defer wg.Done()
			_ = db.Close()
		}()
		wg.Wait()
	}
}

// TestCompressClosesOldIndex verifies Compress unmaps (closes) the old index
// and leaves a working compressed index behind.
func TestCompressClosesOldIndex(t *testing.T) {
	db, oldIdx := newIndexedDB(t, "compress", 300)
	defer db.Close()

	if err := db.Compress("compress", distance.Int8); err != nil {
		t.Fatalf("Compress: %v", err)
	}

	if !indexClosed(oldIdx) {
		t.Error("old index was not closed during compression (mmap leak)")
	}

	newIdxAny, ok := db.GetVectorIndex("compress")
	if !ok {
		t.Fatal("compressed index missing")
	}
	newIdx := newIdxAny.(*hnsw.Index)
	if indexClosed(newIdx) {
		t.Error("new compressed index should be open")
	}
	// The new index must still answer searches.
	results := newIdx.SearchWithScores([]float32{0.1, 0.2, 0.3, 0.4}, 3, nil, 100)
	if len(results) == 0 {
		t.Error("compressed index returned no results")
	}
}

// TestLoadFromSnapshotClosesExisting verifies a second LoadFromSnapshot on a
// live DB closes the previously loaded indexes (no mmap/handle leak).
func TestLoadFromSnapshotClosesExisting(t *testing.T) {
	db, firstIdx := newIndexedDB(t, "snapidx", 100)
	defer db.Close()

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// Reload from the same buffer on the same DB: the loaded index replaces
	// the original one and must close it.
	snapBytes := buf.Bytes()
	if err := db.LoadFromSnapshot(bytes.NewReader(snapBytes), filepath.Join(t.TempDir())); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}

	if !indexClosed(firstIdx) {
		t.Error("original index not closed by LoadFromSnapshot")
	}

	loadedAny, ok := db.GetVectorIndex("snapidx")
	if !ok {
		t.Fatal("loaded index missing")
	}
	if indexClosed(loadedAny.(*hnsw.Index)) {
		t.Error("loaded index should be open")
	}

	// Second load on the now-live DB (reload path with existing indexes).
	loadedIdx := loadedAny.(*hnsw.Index)
	if err := db.LoadFromSnapshot(bytes.NewReader(snapBytes), filepath.Join(t.TempDir())); err != nil {
		t.Fatalf("second LoadFromSnapshot: %v", err)
	}
	if !indexClosed(loadedIdx) {
		t.Error("previously loaded index not closed by the second load")
	}
}
