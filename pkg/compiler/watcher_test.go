package compiler

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func newTestCompilerAndWatcher(t *testing.T) (*Compiler, *Watcher, *cognitive.Config) {
	t.Helper()
	eng := newTestEngine(t)
	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	c := NewCompiler(eng, nil, nil)
	cfg := &cognitive.Config{Enabled: true}
	w := NewWatcher(c, eng, cfg, []string{indexName})

	return c, w, cfg
}

func addArtifactSourceData(t *testing.T, c *Compiler, indexName string) {
	t.Helper()
	eng := c.eng

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice prefers concise code",
	})
	eng.VAdd(indexName, "user:alice:mem2", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice uses Vim",
	})
	eng.VLink(indexName, "user:alice", "user:alice:mem1", "has_interaction", "interaction_of", 1.0, nil)
	eng.VLink(indexName, "user:alice", "user:alice:mem2", "has_interaction", "interaction_of", 1.0, nil)

	_, err := c.Compile(CompileRequest{
		Name: "entity_card",
		Sources: SourceSpec{
			Type:   "graph_query",
			Entity: EntityRef{Type: "user", ID: "alice"},
			Depth:  2,
		},
		IndexName: indexName,
	})
	if err != nil {
		t.Fatalf("compile artifact failed: %v", err)
	}
}

func TestWatcherLoadsArtifacts(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	err := w.loadArtifacts()
	w.mu.Unlock()
	if err != nil {
		t.Fatalf("loadArtifacts failed: %v", err)
	}

	w.mu.RLock()
	count := len(w.tracked)
	w.mu.RUnlock()

	if count < 1 {
		t.Errorf("expected at least 1 tracked artifact, got %d", count)
	}
}

func TestWatcherOnEventIncrementsStaleness(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	// Load and manually populate source node IDs
	w.mu.Lock()
	w.loadArtifacts()
	// Populate source node IDs for tracking (the artifact was compiled from these nodes)
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1", "user:alice:mem2"}
	}
	w.mu.Unlock()

	// Get current staleness
	w.mu.RLock()
	var initialStaleness float64
	for _, a := range w.tracked {
		initialStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	// Simulate source node change
	w.OnEvent(engine.Event{
		Type: "vector.add",
		ID:   "user:alice:mem1",
	})

	// Check staleness increased
	w.mu.RLock()
	var newStaleness float64
	for _, a := range w.tracked {
		newStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	if newStaleness <= initialStaleness {
		t.Errorf("expected staleness to increase (was %f, now %f)", initialStaleness, newStaleness)
	}
}

func TestWatcherOnEventIgnoredForUnrelatedNodes(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
	}
	w.mu.Unlock()

	w.mu.RLock()
	var initialStaleness float64
	for _, a := range w.tracked {
		initialStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	// Change unrelated node
	w.OnEvent(engine.Event{
		Type: "vector.add",
		ID:   "user:bob", // not a source
	})

	w.mu.RLock()
	var newStaleness float64
	for _, a := range w.tracked {
		newStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	if newStaleness != initialStaleness {
		t.Errorf("expected staleness unchanged for unrelated node")
	}
}

func TestWatcherScanArtifactsRecompilesStale(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 5.0
		a.CompiledAt = time.Now().Add(-2 * time.Hour)
	}
	w.mu.Unlock()

	// Trigger scan
	w.ScanArtifacts()

	// Verify staleness was reset (recompiled)
	w.mu.RLock()
	for _, a := range w.tracked {
		if a.StalenessScore != 0 {
			t.Errorf("expected staleness reset after recompile, got %f", a.StalenessScore)
		}
	}
	w.mu.RUnlock()
}

func TestWatcherImportanceScoring(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()

	var a *watchedArtifact
	for _, tracked := range w.tracked {
		a = tracked
		break
	}
	w.mu.Unlock()

	if a == nil {
		t.Fatal("no tracked artifact found")
	}

	// With no usage data
	w.updateImportance(a)
	if a.ImportanceScore != 0 {
		t.Logf("importance score (no usage): %f", a.ImportanceScore)
	}

	// With high usage
	a.UsageCount = 100
	a.LastAccessedAt = time.Now()
	w.updateImportance(a)
	if a.ImportanceScore <= 0 {
		t.Errorf("expected importance > 0 with usage=100, got %f", a.ImportanceScore)
	}

	// Threshold should be lower for important artifact
	if a.ImportanceScore > 8.0 {
		threshold := w.getStalenessThreshold(a)
		if threshold >= w.stalenessThreshold {
			t.Errorf("expected lower threshold for important artifact, got %f (base=%f)", threshold, w.stalenessThreshold)
		}
	}
}

func TestWatcherMaxRecompilePerCycle(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	indexName := "mcp_memory"
	eng := c.eng

	// Create 5 projects, each with their own artifact
	for i := 0; i < 5; i++ {
		projID := string(rune('A' + i))
		eng.VAdd(indexName, "project:p"+projID, make([]float32, 384), map[string]any{
			"type": "project", "entity_id": "p" + projID, "_pinned": true,
		})
		eng.VAdd(indexName, "project:p"+projID+":mem1", make([]float32, 384), map[string]any{
			"type": "memory", "content": "memory for project " + projID,
		})
		eng.VLink(indexName, "project:p"+projID, "project:p"+projID+":mem1", "has_memory", "memory_of", 1.0, nil)

		_, err := c.Compile(CompileRequest{
			Name: "entity_card",
			Sources: SourceSpec{
				Type:   "graph_query",
				Entity: EntityRef{Type: "project", ID: "p" + projID},
				Depth:  1,
			},
			IndexName: indexName,
		})
		if err != nil {
			t.Fatalf("compile artifact %d failed: %v", i, err)
		}
	}

	w.mu.Lock()
	w.loadArtifacts()

	// Make all stale above threshold
	for _, a := range w.tracked {
		a.StalenessScore = 5.0
	}
	w.mu.Unlock()

	// Scan should only recompile maxRecompilePerCycle (3)
	w.ScanArtifacts()

	w.mu.RLock()
	recompiled := 0
	for _, a := range w.tracked {
		if a.RecompileCount > 0 {
			recompiled++
		}
	}
	w.mu.RUnlock()

	if recompiled > 3 {
		t.Errorf("expected max 3 recompiles per cycle, got %d", recompiled)
	}
	t.Logf("recompiled %d out of %d artifacts (max %d per cycle)",
		recompiled, len(w.tracked), w.maxRecompilePerCycle)
}

func TestGetStalenessThreshold(t *testing.T) {
	w := &Watcher{stalenessThreshold: 1.0}

	// High importance: lower threshold
	a1 := &watchedArtifact{ImportanceScore: 9.0, UsageCount: 100}
	th1 := w.getStalenessThreshold(a1)
	if th1 >= 1.0 {
		t.Errorf("expected threshold < 1.0 for high importance, got %f", th1)
	}

	// Medium importance: moderate threshold
	a2 := &watchedArtifact{ImportanceScore: 6.0}
	th2 := w.getStalenessThreshold(a2)
	if th2 >= 1.0 {
		t.Errorf("expected threshold < 1.0 for medium importance, got %f", th2)
	}

	// Low usage: higher threshold
	a3 := &watchedArtifact{ImportanceScore: 0, UsageCount: 5}
	th3 := w.getStalenessThreshold(a3)
	if th3 <= 1.0 {
		t.Errorf("expected threshold > 1.0 for low usage, got %f", th3)
	}
}

func TestOnEvent_ConcurrentScanArtifacts_NoPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent stress test in short mode")
	}

	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	// Load and populate source node IDs
	w.mu.Lock()
	w.loadArtifacts()
	for key, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1", "user:alice:mem2", key}
	}
	w.mu.Unlock()

	stopCh := make(chan struct{})
	panicCh := make(chan any, 4)

	// Goroutine A: OnEvent in loop (simulates engine event stream)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicCh <- r
			}
		}()
		for i := 0; ; i++ {
			select {
			case <-stopCh:
				return
			default:
			}
			w.OnEvent(engine.Event{
				Type:      "vector.add",
				ID:        "user:alice:mem1",
				IndexName: "mcp_memory",
			})
			w.OnEvent(engine.Event{
				Type:      "vector.add",
				ID:        "user:alice:mem2",
				IndexName: "mcp_memory",
			})
		}
	}()

	// Goroutine B: ScanArtifacts in loop (simulates Gardener scan cycle)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicCh <- r
			}
		}()
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			w.ScanArtifacts()
		}
	}()

	// Run for a few seconds to trigger race conditions
	time.Sleep(3 * time.Second)
	close(stopCh)

	select {
	case r := <-panicCh:
		t.Fatalf("PANIC during concurrent OnEvent/ScanArtifacts: %v", r)
	default:
		// No panic — test passed
	}
}

// TestOnEvent_LockDance_NoLockUpgrade verifies that the OnEvent
// lock dance (RLock→RUnlock→Lock→Unlock→RLock) does NOT cause
// a deadlock or panic under non-concurrent usage.
// This test exercises the internal staleness update path directly.
func TestOnEvent_LockDance_NoLockUpgrade(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
	}
	w.mu.Unlock()

	// Read initial staleness
	w.mu.RLock()
	var initialStaleness float64
	for _, a := range w.tracked {
		initialStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	// Send multiple events (each triggers the lock upgrade path)
	for i := 0; i < 10; i++ {
		w.OnEvent(engine.Event{
			Type: "vector.add",
			ID:   "user:alice:mem1",
		})
	}

	// Verify staleness was incremented correctly (0.3 per event)
	w.mu.RLock()
	var finalStaleness float64
	for _, a := range w.tracked {
		finalStaleness = a.StalenessScore
		break
	}
	w.mu.RUnlock()

	expected := initialStaleness + float64(10)*stalenessIncrementOnChange
	delta := finalStaleness - expected
	if delta < 0 {
		delta = -delta
	}
	if delta > 0.001 {
		t.Errorf("staleness mismatch: initial=%.2f, final=%.2f, expected=%.2f",
			initialStaleness, finalStaleness, expected)
	}
}
