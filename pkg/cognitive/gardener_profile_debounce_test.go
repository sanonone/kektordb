package cognitive

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestProfileDebounceStaleTimerDoesNotDrainPending reproduces P1-10: when a
// new schedule arrives while the previous timer's callback is still running,
// the stale callback must NOT drain pending users accumulated for the newer
// debounce window (otherwise the user is processed without the quiet period,
// or flushed twice).
//
// The test simulates the late callback deterministically by invoking
// flushPendingProfileUpdates with a stale generation.
func TestProfileDebounceStaleTimerDoesNotDrainPending(t *testing.T) {
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	if err := eng.VCreate("idx", distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	gardener := NewGardener(eng, nil, Config{
		Enabled:                true,
		Mode:                   "basic",
		TargetIndexes:          []string{"idx"},
		EnableUserProfiling:    false, // spawned UpdateUserProfile goroutines are no-ops
		ProfileUpdateThreshold: 1,
	})
	gardener.profileDebounce = time.Second // quiet period (not relied on here)

	// First schedule (generation 1).
	gardener.scheduleProfileUpdate("idx", "user_a", 1)

	// A new schedule arrives while the first timer's callback may still be
	// running (generation 2, supersedes generation 1).
	gardener.scheduleProfileUpdate("idx", "user_b", 1)

	// The stale callback (generation 1) fires late: it must skip the drain
	// entirely, leaving BOTH users for the newer callback.
	gardener.flushPendingProfileUpdates(1, 1)

	gardener.profileTimerMu.Lock()
	pending := gardener.profilePendingUsers
	gardener.profileTimerMu.Unlock()
	if len(pending) != 2 {
		t.Fatalf("stale flush drained pending users: got %d pending, want 2 (user_a, user_b)", len(pending))
	}
	if _, ok := pending["user_b"]; !ok {
		t.Fatalf("user_b missing from pending after stale flush: %v", pending)
	}

	// The current callback (generation 2) must drain normally.
	gardener.flushPendingProfileUpdates(2, 1)

	gardener.profileTimerMu.Lock()
	pending = gardener.profilePendingUsers
	gardener.profileTimerMu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("current flush did not drain pending: %v", pending)
	}
}
