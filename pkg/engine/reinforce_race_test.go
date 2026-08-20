package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestVReinforceConcurrentIndexChurnNoDeadlock reproduces P1-5: VReinforce
// wrapped GetMetadataForNode in an outer e.DB.RLock(), but
// GetMetadataForNode re-acquires s.mu.RLock internally. Go RWMutex is not
// reentrant under writer-preference: with a writer (VDeleteIndex/VCreate)
// waiting for s.mu.Lock, the second RLock blocks forever → deadlock.
//
// Post-fix VReinforce must complete while index create/delete churn runs.
func TestVReinforceConcurrentIndexChurnNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	createTestIndex(t, eng, "target")
	for i := 0; i < 50; i++ {
		if err := eng.VAdd("target", fmt.Sprintf("v%d", i), []float32{float32(i), 0, 0, 0}, map[string]any{"k": "v"}); err != nil {
			t.Fatalf("VAdd: %v", err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer churn: repeatedly create/delete a scratch index (takes s.mu.Lock).
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

	// VReinforce loop: must never block (pre-fix it deadlocks under churn).
	ids := make([]string, 50)
	for i := range ids {
		ids[i] = fmt.Sprintf("v%d", i)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := eng.VReinforce("target", ids); err != nil {
				t.Errorf("VReinforce: %v", err)
				return
			}
		}
	}()

	select {
	case <-done:
		t.Fatal("VReinforce returned before stop (unexpected)")
	case <-time.After(3 * time.Second):
		// Still running: that is the expected state — it must not be a
		// permanent block. Stop and require a clean return within 5s.
		close(stop)
		select {
		case <-done:
			// OK: VReinforce loop exited cleanly after stop.
		case <-time.After(5 * time.Second):
			t.Fatal("VReinforce deadlocked with concurrent index churn (P1-5)")
		}
	}
	wg.Wait()
}

// TestVReinforceConcurrentAddNoRace reproduces P1-6: VReinforce read the
// external→internal map via GetInternalIDUnlocked while Add/Delete write it
// under metaMu. Run with -race: pre-fix it reports a DATA RACE; post-fix
// (locked GetInternalID) it passes.
func TestVReinforceConcurrentAddNoRace(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	createTestIndex(t, eng, "idx")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: continuous Add.
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
			eng.VAdd("idx", fmt.Sprintf("a%d", i), []float32{1, 0, 0, 0}, nil)
			i++
		}
	}()

	// Reinforcer: continuous VReinforce on a mix of existing and new IDs.
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
			eng.VReinforce("idx", []string{fmt.Sprintf("a%d", i%200), "missing-id"})
			i++
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
