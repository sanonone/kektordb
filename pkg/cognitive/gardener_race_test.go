package cognitive

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestConcurrentThinkNoRace verifies that concurrent think() calls don't cause race conditions.
// This test specifically checks the fix for scanCursors and lastThinkTime race conditions.
func TestConcurrentThinkNoRace(t *testing.T) {
	// Create a test engine
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	// Create a test index with some data
	indexName := "test_memory"
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add some test vectors
	for i := 0; i < 100; i++ {
		vec := []float32{float32(i), 0, 0, 0, 0, 0, 0, 0}
		metadata := map[string]any{
			"content":      "test content",
			"memory_layer": "episodic",
		}
		if err := eng.VAdd(indexName, string(rune('A'+i%26))+string(rune('0'+i/26)), vec, metadata); err != nil {
			t.Fatalf("Failed to add vector: %v", err)
		}
	}

	// Create gardener with basic config
	cfg := Config{
		Enabled:             true,
		Interval:            100 * time.Millisecond, // Short interval for testing
		Mode:                "basic",
		TargetIndexes:       []string{indexName},
		AdaptiveThreshold:   10,
		AdaptiveMinInterval: 50 * time.Millisecond,
	}

	gardener := NewGardener(eng, nil, cfg)

	// Test concurrent cursor access
	t.Run("ConcurrentCursorAccess", func(t *testing.T) {
		var wg sync.WaitGroup
		numGoroutines := 10
		numOperations := 100

		// Concurrent writes to different cursors
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cursorKey := string(rune('A' + id%26))
				for j := 0; j < numOperations; j++ {
					gardener.setCursor(cursorKey, uint32(j))
					_ = gardener.getCursor(cursorKey)
				}
			}(i)
		}

		wg.Wait()
	})

	// Test concurrent lastThinkTime access
	t.Run("ConcurrentLastThinkTimeAccess", func(t *testing.T) {
		var wg sync.WaitGroup
		numGoroutines := 10
		numOperations := 100

		// Concurrent reads and writes to lastThinkTime
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					if j%2 == 0 {
						gardener.setLastThinkTime(time.Now())
					} else {
						_ = gardener.getLastThinkTime()
					}
				}
			}(i)
		}

		wg.Wait()
	})

	// Test concurrent think() calls routed through the serialization channel
	t.Run("ConcurrentThinkCalls", func(t *testing.T) {
		gardener.thinkReqs = make(chan struct{}, 1)
		go gardener.thinkWorker()
		defer gardener.Stop()

		var wg sync.WaitGroup
		numRequests := 5

		// Send multiple concurrent requests through the channel
		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gardener.requestThink()
			}()
		}

		// Also test ForceThink (blocking send)
		gardener.thinkReqs <- struct{}{}

		wg.Wait()
		t.Log("Concurrent think requests serialized without panic")
	})
}

// TestGardenerConcurrentEventHandling verifies that onEvent handles concurrent events safely.
func TestGardenerConcurrentEventHandling(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Enabled:             true,
		Interval:            1 * time.Second,
		Mode:                "basic",
		TargetIndexes:       []string{"*"},
		AdaptiveThreshold:   5,
		AdaptiveMinInterval: 100 * time.Millisecond,
	}

	gardener := NewGardener(eng, nil, cfg)

	// Simulate rapid concurrent events
	var wg sync.WaitGroup
	numEvents := 100

	for i := 0; i < numEvents; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			event := engine.Event{
				Type:      "vector.add",
				IndexName: "test_idx",
				ID:        string(rune('A' + id%26)),
			}
			gardener.onEvent(event)
		}(i)
	}

	wg.Wait()

	// Verify writeCounter reflects at least some of the events
	// Note: The counter may be reset by adaptive trigger logic, so we just verify
	// it's in a valid range and doesn't crash
	count := gardener.writeCounter.Load()
	if count < 0 || count > int64(numEvents) {
		t.Errorf("writeCounter out of valid range: got %d, expected 0-%d", count, numEvents)
	}

	// Verify lastThinkTime can be read safely
	_ = gardener.getLastThinkTime()
}

// TestHelperMethodsThreadSafety verifies that cursor and time helper methods are thread-safe.
func TestHelperMethodsThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	cfg := Config{
		Enabled:       true,
		Interval:      1 * time.Hour, // Long interval to prevent automatic think
		Mode:          "basic",
		TargetIndexes: []string{"*"},
	}

	gardener := NewGardener(eng, nil, cfg)

	// Initialize some cursors
	gardener.setCursor("test_cursor", 100)
	gardener.setLastThinkTime(time.Now())

	var wg sync.WaitGroup
	numGoroutines := 20
	iterations := 50

	// Mix of reads and writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch j % 4 {
				case 0:
					gardener.setCursor("test_cursor", uint32(id*iterations+j))
				case 1:
					_ = gardener.getCursor("test_cursor")
				case 2:
					gardener.setLastThinkTime(time.Now())
				case 3:
					_ = gardener.getLastThinkTime()
				}
			}
		}(i)
	}

	wg.Wait()

	// If we got here without panic or race detector warning, the test passes
	t.Log("Helper methods are thread-safe")
}

// TestThinkChannelSerialization verifies that the thinkReqs channel serializes
// think() calls, preventing the reentrancy race on newReflections and scanCursors.
// Regression test for H10.
func TestThinkChannelSerialization(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	indexName := "test_memory"
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	for i := 0; i < 50; i++ {
		vec := []float32{float32(i), 0, 0, 0, 0, 0, 0, 0}
		id := string(rune('A'+i%26)) + string(rune('0'+i/26))
		_ = eng.VAdd(indexName, id, vec, map[string]any{
			"content":      "test content",
			"memory_layer": "episodic",
		})
	}

	cfg := Config{
		Enabled:             true,
		Interval:            1 * time.Hour, // Prevent automatic think
		Mode:                "basic",
		TargetIndexes:       []string{indexName},
		AdaptiveThreshold:   5,
		AdaptiveMinInterval: 10 * time.Millisecond,
	}

	gardener := NewGardener(eng, nil, cfg)
	gardener.thinkReqs = make(chan struct{}, 1)
	go gardener.thinkWorker()

	// Simulate concurrent triggers: ticker + adaptive + ForceThink
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gardener.requestThink()
		}()
	}

	// ForceThink: blocking send through the channel
	gardener.thinkReqs <- struct{}{}

	wg.Wait()
	gardener.Stop()
	t.Log("Channel serialization test passed without races")
}

// TestProfileUpdateDebouncer verifies that rapid scheduleProfileUpdate calls
// are coalesced into a single deferred fire, not N concurrent goroutines.
func TestProfileUpdateDebouncer(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	indexName := "test_profile"
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Use a short debounce (50ms) so the test runs fast.
	cfg := Config{
		Enabled:                 true,
		Interval:                1 * time.Hour, // prevent auto-think
		Mode:                    "basic",
		TargetIndexes:           []string{indexName},
		AdaptiveThreshold:       100,
		AdaptiveMinInterval:     1 * time.Hour,
		EnableUserProfiling:     true,
		ProfileUpdateThreshold:  3,
	}
	gardener := NewGardener(eng, nil, cfg)
	gardener.profileDebounce = 50 * time.Millisecond

	// Simulate 10 rapid scheduleProfileUpdate calls (e.g. 10 quick save_memory
	// in 10ms). They should all reset the same timer, NOT start 10 goroutines.
	for i := 0; i < 10; i++ {
		gardener.scheduleProfileUpdate(indexName, "dash", i+3)
		// Small sleep to simulate real-world delay between saves
		time.Sleep(2 * time.Millisecond)
	}

	// After the debounce period, only one timer should be active.
	gardener.profileTimerMu.Lock()
	if gardener.profileTimer == nil {
		t.Fatal("Profile timer should be active immediately after scheduling")
	}
	pendingCount := len(gardener.profilePendingUsers)
	gardener.profileTimerMu.Unlock()

	if pendingCount != 1 {
		t.Errorf("Expected 1 pending user, got %d", pendingCount)
	}

	// Wait for the debounce to fire.
	time.Sleep(200 * time.Millisecond)

	// After firing, the timer should be cleared and pending map empty.
	gardener.profileTimerMu.Lock()
	timerActive := gardener.profileTimer != nil
	pendingCount = len(gardener.profilePendingUsers)
	gardener.profileTimerMu.Unlock()

	if timerActive {
		t.Error("Profile timer should be nil after debounce fired")
	}
	if pendingCount != 0 {
		t.Errorf("Expected 0 pending users after debounce fired, got %d", pendingCount)
	}

	t.Log("Debouncer correctly coalesced 10 rapid calls into 1 timer reset")
}

// TestProfileUpdateDebouncerMultipleUsers verifies that the debouncer
// correctly accumulates multiple distinct userIDs and processes each
// exactly once when the timer fires.
func TestProfileUpdateDebouncerMultipleUsers(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	indexName := "test_profile"
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	cfg := Config{
		Enabled:                true,
		Interval:               1 * time.Hour,
		Mode:                   "basic",
		TargetIndexes:          []string{indexName},
		AdaptiveThreshold:      100,
		AdaptiveMinInterval:    1 * time.Hour,
		EnableUserProfiling:    true,
		ProfileUpdateThreshold: 3,
	}
	gardener := NewGardener(eng, nil, cfg)
	gardener.profileDebounce = 50 * time.Millisecond

	// Schedule updates for 3 different users interleaved.
	gardener.scheduleProfileUpdate(indexName, "alice", 3)
	time.Sleep(5 * time.Millisecond)
	gardener.scheduleProfileUpdate(indexName, "bob", 3)
	time.Sleep(5 * time.Millisecond)
	gardener.scheduleProfileUpdate(indexName, "charlie", 3)

	// All 3 users should be pending under one timer.
	gardener.profileTimerMu.Lock()
	pendingCount := len(gardener.profilePendingUsers)
	gardener.profileTimerMu.Unlock()

	if pendingCount != 3 {
		t.Errorf("Expected 3 pending users, got %d", pendingCount)
	}

	// Wait for debounce to fire and flush.
	time.Sleep(200 * time.Millisecond)

	gardener.profileTimerMu.Lock()
	pendingCount = len(gardener.profilePendingUsers)
	gardener.profileTimerMu.Unlock()

	if pendingCount != 0 {
		t.Errorf("Expected 0 pending users after debounce fired, got %d", pendingCount)
	}

	t.Log("Debouncer correctly accumulated 3 distinct users and flushed them")
}

// TestProfileUpdateDebouncerConcurrent ensures the debouncer is thread-safe
// under concurrent scheduleProfileUpdate calls (race detector would catch
// any unprotected access).
func TestProfileUpdateDebouncerConcurrent(t *testing.T) {
	tempDir := t.TempDir()
	opts := engine.DefaultOptions(tempDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	indexName := "test_profile"
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	cfg := Config{
		Enabled:                true,
		Interval:               1 * time.Hour,
		Mode:                   "basic",
		TargetIndexes:          []string{indexName},
		AdaptiveThreshold:      100,
		AdaptiveMinInterval:    1 * time.Hour,
		EnableUserProfiling:    true,
		ProfileUpdateThreshold: 3,
	}
	gardener := NewGardener(eng, nil, cfg)
	gardener.profileDebounce = 100 * time.Millisecond

	// 20 concurrent goroutines all calling scheduleProfileUpdate for
	// the same userID. The debouncer should keep resetting the same timer
	// without panicking or racing.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gardener.scheduleProfileUpdate(indexName, "dash", i+3)
		}(i)
	}
	wg.Wait()

	// After all 20 finish, only 1 timer should be active for 1 user.
	gardener.profileTimerMu.Lock()
	pendingCount := len(gardener.profilePendingUsers)
	gardener.profileTimerMu.Unlock()

	if pendingCount != 1 {
		t.Errorf("Expected 1 pending user, got %d", pendingCount)
	}

	// Wait for the debounce to fire.
	time.Sleep(250 * time.Millisecond)

	t.Log("Debouncer handled 20 concurrent calls without race")
}
