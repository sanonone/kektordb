package cognitive

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// blockingProfileLLM blocks profile-update calls until released.
type blockingProfileLLM struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newBlockingProfileLLM() *blockingProfileLLM {
	return &blockingProfileLLM{release: make(chan struct{}), entered: make(chan struct{})}
}

func (b *blockingProfileLLM) Chat(string, string) (string, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return `{}`, nil
}

func (b *blockingProfileLLM) ChatWithImages(string, string, [][]byte) (string, error) {
	return b.Chat("", "")
}

// TestStopWaitsForProfileUpdates covers goroutines that were started by the
// shutdown flush but never waited for.
//
// flushPendingProfileUpdates launches one goroutine per user; those call the LLM
// and then write the profile back to the index. Stop returned immediately, so a
// process exiting right after Stop could kill them mid-write.
func TestStopWaitsForProfileUpdates(t *testing.T) {
	eng, idx := newDebounceTestGardenerEngine(t, "stop_profiles")
	llm := newBlockingProfileLLM()

	g := NewGardener(eng, llm, Config{
		Enabled:                true,
		Interval:               time.Hour,
		Mode:                   "basic",
		TargetIndexes:          []string{idx},
		EnableUserProfiling:    true,
		ProfileUpdateThreshold: 1,
	})

	// Stop waits for thinkDone, which Start closes on exit: a gardener that was
	// never started blocks there. That is its own defect, tracked separately;
	// here we exercise the profile-update wait, so start it first.
	g.Start()

	// UpdateUserProfile reads the user's memories; without at least one it
	// returns before reaching the LLM.
	if err := eng.VAdd(idx, "mem_alice", []float32{0.1, 0.2}, map[string]any{
		"type": "memory", "user_id": "alice", "content": "likes strong coffee",
		"_created_at": float64(time.Now().Unix()),
	}); err != nil {
		t.Fatalf("vadd: %v", err)
	}

	// Queue a pending profile update without arming a timer that fires early.
	g.profileTimerMu.Lock()
	g.profilePendingUsers["alice"] = idx
	g.profileTimerMu.Unlock()

	stopped := make(chan struct{})
	go func() {
		g.Stop()
		close(stopped)
	}()

	// Stop must not return while the update is in flight...
	select {
	case <-stopped:
		t.Fatal("Stop returned while a profile update was still running")
	case <-time.After(150 * time.Millisecond):
	}

	// ...and must return once the update finishes.
	close(llm.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the profile update completed")
	}
}

// TestStopIsBoundedWhenProfileUpdateHangs keeps shutdown from hanging forever on
// a provider that never answers.
func TestStopIsBoundedWhenProfileUpdateHangs(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the shutdown timeout")
	}
	eng, idx := newDebounceTestGardenerEngine(t, "stop_hang")
	llm := newBlockingProfileLLM()
	defer close(llm.release)

	g := NewGardener(eng, llm, Config{
		Enabled:                true,
		Interval:               time.Hour,
		Mode:                   "basic",
		TargetIndexes:          []string{idx},
		EnableUserProfiling:    true,
		ProfileUpdateThreshold: 1,
	})
	g.Start()
	if err := eng.VAdd(idx, "mem_bob", []float32{0.1, 0.2}, map[string]any{
		"type": "memory", "user_id": "bob", "content": "prefers tea",
		"_created_at": float64(time.Now().Unix()),
	}); err != nil {
		t.Fatalf("vadd: %v", err)
	}
	g.profileTimerMu.Lock()
	g.profilePendingUsers["bob"] = idx
	g.profileTimerMu.Unlock()

	stopped := make(chan struct{})
	start := time.Now()
	go func() {
		g.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		if elapsed := time.Since(start); elapsed < gardenerStopTimeout {
			t.Logf("Stop returned after %v, before the %v deadline: acceptable", elapsed, gardenerStopTimeout)
		}
	case <-time.After(gardenerStopTimeout + 5*time.Second):
		t.Fatal("Stop never returned: the wait for profile updates has no bound")
	}
}

// newDebounceTestGardenerEngine builds an engine plus a target index for the
// tests above, without starting a gardener.
func newDebounceTestGardenerEngine(t *testing.T, idx string) (*engine.Engine, string) {
	t.Helper()
	opts := engine.DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return eng, idx
}

// TestStopWithoutStartDoesNotBlock covers a shutdown hang: Stop waited on
// thinkDone, which only Start's worker closes. A gardener created but never
// started — a configuration error, or an early return before Start — blocked
// Stop forever, and Stop is on the shutdown path.
func TestStopWithoutStartDoesNotBlock(t *testing.T) {
	eng, idx := newDebounceTestGardenerEngine(t, "never_started")
	g := NewGardener(eng, nil, Config{
		Enabled:       true,
		Interval:      time.Hour,
		Mode:          "basic",
		TargetIndexes: []string{idx},
	})
	// Deliberately no g.Start().

	done := make(chan struct{})
	go func() {
		g.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked on a gardener that was never started: nothing closes thinkDone")
	}
}

// TestStopAfterStartStillWaits keeps the normal path: a started gardener does
// wait for its worker.
func TestStopAfterStartStillWaits(t *testing.T) {
	eng, idx := newDebounceTestGardenerEngine(t, "started")
	g := NewGardener(eng, nil, Config{
		Enabled:       true,
		Interval:      time.Hour,
		Mode:          "basic",
		TargetIndexes: []string{idx},
	})
	g.Start()

	done := make(chan struct{})
	go func() {
		g.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return for a started gardener")
	}
}
