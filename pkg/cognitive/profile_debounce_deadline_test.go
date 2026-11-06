package cognitive

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// newDebounceTestGardener builds a gardener with a short debounce so the test
// stays fast, profiling on, and no background thinking.
func newDebounceTestGardener(t *testing.T, idx string) (*Gardener, *engine.Engine) {
	t.Helper()
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.VCreate(idx, distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	g := NewGardener(eng, nil, Config{
		Enabled:                true,
		Interval:               time.Hour, // no automatic cycles
		Mode:                   "basic",
		TargetIndexes:          []string{idx},
		AdaptiveThreshold:      1000,
		AdaptiveMinInterval:    time.Hour,
		EnableUserProfiling:    true,
		ProfileUpdateThreshold: 1,
	})
	g.profileDebounce = 40 * time.Millisecond
	g.profileMaxWait = 120 * time.Millisecond
	return g, eng
}

// TestProfileDebounceDoesNotStarveUnderContinuousLoad covers a trailing-edge
// debounce with no upper bound: every event stops and re-arms the timer, so a
// stream of saves spaced closer than the quiet period postpones the flush
// indefinitely. In an active session the profile is never updated, and the
// delay compounds across every user because the timer is global.
//
// The fix keeps the trailing edge (a short burst still coalesces into one
// update) but adds a deadline: the timer is not re-armed once a window has been
// open for profileMaxWait, so the flush happens at the latest then.
func TestProfileDebounceDoesNotStarveUnderContinuousLoad(t *testing.T) {
	g, _ := newDebounceTestGardener(t, "starve_debounce")

	// Events arriving faster than the quiet period, for well over the maximum
	// wait. Without a deadline the timer keeps being pushed back and never fires.
	stop := time.Now().Add(400 * time.Millisecond)
	events := 0
	for time.Now().Before(stop) {
		g.scheduleProfileUpdate("starve_debounce", "alice", events+1)
		events++
		time.Sleep(10 * time.Millisecond) // 4x faster than the 40ms quiet period
	}

	// The window must have flushed at least once while the load was running.
	// Checking "pending is empty right now" would be wrong: another event may
	// have just opened a fresh window.
	if got := g.profileFlushes.Load(); got == 0 {
		t.Fatalf("after %d events over 400ms the profile never flushed: "+
			"continuous load starved the debounce", events)
	}

	// And once the stream stops, the last window drains.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g.profileTimerMu.Lock()
		pending := len(g.profilePendingUsers)
		g.profileTimerMu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the stream stopped but the last window never flushed")
}

// TestProfileDebounceStillCoalescesABurst keeps the original intent: a short
// burst must produce one flush, not one per event.
func TestProfileDebounceStillCoalescesABurst(t *testing.T) {
	g, _ := newDebounceTestGardener(t, "burst")

	// Five events inside the quiet period.
	for i := 0; i < 5; i++ {
		g.scheduleProfileUpdate("burst", "bob", i+1)
		time.Sleep(5 * time.Millisecond)
	}

	// Immediately after: still pending, the quiet period has not elapsed.
	g.profileTimerMu.Lock()
	pendingNow := len(g.profilePendingUsers)
	g.profileTimerMu.Unlock()
	if pendingNow == 0 {
		t.Fatal("the burst flushed immediately: the debounce lost its coalescing behaviour")
	}

	// After the quiet period: flushed once.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		g.profileTimerMu.Lock()
		pending := len(g.profilePendingUsers)
		g.profileTimerMu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the burst never flushed after the quiet period")
}

// TestProfileDebounceDeadlineIsBounded pins the deadline value: it must be
// finite and larger than the quiet period, otherwise the trailing edge is gone.
func TestProfileDebounceDeadlineIsBounded(t *testing.T) {
	if profileMaxWaitDefault <= 0 {
		t.Fatal("profileMaxWait must be a positive duration")
	}
	if profileMaxWaitDefault <= 5*time.Second {
		t.Errorf("profileMaxWaitDefault = %v: must exceed the default quiet period (5s), "+
			"or a normal burst would flush before it settles", profileMaxWaitDefault)
	}
}

// TestProfileDebounceRearmsAfterDeadlineFlush keeps repeated cycles working: a
// second continuous load must flush again, not stay stuck on the first window.
func TestProfileDebounceRearmsAfterDeadlineFlush(t *testing.T) {
	g, _ := newDebounceTestGardener(t, "repeat")

	runLoad := func(d time.Duration, from int) int {
		stop := time.Now().Add(d)
		n := from
		for time.Now().Before(stop) {
			g.scheduleProfileUpdate("repeat", "carol", n+1)
			n++
			time.Sleep(10 * time.Millisecond)
		}
		return n
	}

	runLoad(400*time.Millisecond, 0)
	afterFirst := g.profileFlushes.Load()
	if afterFirst == 0 {
		t.Fatal("first window did not flush")
	}

	// A second stream must open a new window and flush again.
	runLoad(400*time.Millisecond, 100)
	if got := g.profileFlushes.Load(); got <= afterFirst {
		t.Fatalf("second window did not flush (%d flushes, was %d): a flush "+
			"deadline must reset, not latch closed", got, afterFirst)
	}
}
