package hnsw

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestReentrantRLock_GetNodeData_CONFIRM_BUG reproduces the root cause
// behind bug #4.1: metaMu is not reentrant, so calling GetNodeData
// (which acquires metaMu.RLock()) while metaMu.RLock() is already held
// AND a concurrent writer is waiting on metaMu.Lock() causes a deadlock.
//
// This is the exact scenario that handleUIExplore triggers:
//
//	IterateRaw → metaMu.RLock()
//	  callback → VGet → GetNodeData → metaMu.RLock() (REENTRANT)
//
// The fix for #4.1 removes VGet/GetNodeData calls from inside the
// IterateRaw callback in handleUIExplore, avoiding this pattern.
func TestReentrantRLock_GetNodeData_CONFIRM_BUG(t *testing.T) {
	idx, err := New(16, 100, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// Add one test node.
	vec := make([]float32, 32)
	for j := range vec {
		vec[j] = 1.0
	}
	if _, err := idx.Add("node_0", vec); err != nil {
		t.Fatal(err)
	}

	// Step 1: Main goroutine holds metaMu.RLock.
	// This simulates what IterateRaw does when it holds the lock
	// while executing its callback.
	idx.metaMu.RLock()

	// Step 2: Start a writer goroutine that will block on metaMu.Lock.
	// The writer is pending → Go's RWMutex blocks new RLocks.
	writerBlocked := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(writerBlocked) // Signal: I'm about to try Lock
		idx.metaMu.Lock()    // Blocks until main goroutine releases RLock
		idx.metaMu.Unlock()
	}()
	<-writerBlocked
	time.Sleep(50 * time.Millisecond) // Writer is now waiting

	// Step 3: Try GetNodeData from another goroutine.
	// GetNodeData tries metaMu.RLock() → blocks because writer is pending
	// AND the main goroutine still holds metaMu.RLock().
	// This is a DEADLOCK: writer waits for reader, reader waits for writer.
	done := make(chan struct{}, 1)
	go func() {
		idx.GetNodeData("node_0")
		close(done)
	}()

	select {
	case <-done:
		t.Error("UNEXPECTED: GetNodeData succeeded despite concurrent writer pending on metaMu.Lock")
	case <-time.After(3 * time.Second):
		// Expected: deadlock confirmed.
		// This is the root cause of bug #4.1.
		// The fix at the HTTP handler level (handleUIExplore) avoids
		// calling GetNodeData inside IterateRaw callbacks.
		t.Logf("CONFIRMED: reentrant metaMu.RLock deadlock (bug #4.1 root cause)")
	}

	idx.metaMu.RUnlock()
	wg.Wait()

	// After releasing the RLock, the writer can proceed.
	// Verify that GetNodeData works outside the locked context.
	d, found := idx.GetNodeData("node_0")
	if !found {
		t.Error("GetNodeData should work outside locked context")
	}
	_ = d
}

// TestIterateRawCallback_NoGetNodeData_SAFE_PATTERN verifies that
// collecting IDs inside the callback and fetching data outside is
// safe — the pattern used by the handleUIExplore fix.
func TestIterateRawCallback_NoGetNodeData_SAFE_PATTERN(t *testing.T) {
	idx, err := New(16, 100, distance.Cosine, distance.Float32, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	const N = 50
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = 1.0
		}
		id := fmt.Sprintf("node_%d", i)
		ids[i] = id
		if _, err := idx.Add(id, vec); err != nil {
			t.Fatal(err)
		}
	}

	// Start concurrent writers creating metaMu contention
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				idx.metaMu.Lock()
				// Simulate write under lock
				_ = idx.getNodes()
				idx.metaMu.Unlock()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// The SAFE pattern: collect IDs inside callback, fetch outside.
	var collected []string
	idx.IterateRaw(func(id string, _ interface{}) {
		collected = append(collected, id)
	})

	// Now outside the callback (and outside IterateRaw's metaMu.RLock):
	for _, id := range collected {
		d, found := idx.GetNodeData(id)
		if !found {
			t.Errorf("GetNodeData failed for %s", id)
		}
		_ = d
	}

	close(stopCh)
	wg.Wait()
}
