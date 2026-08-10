package compiler

import (
	"reflect"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// waitForRecompiles blocks until no artifact is in-flight, or the timeout
// elapses. Recompiles run in background goroutines (B1 fix), so tests must
// wait for them before asserting state.
func waitForRecompiles(t *testing.T, w *Watcher) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.mu.RLock()
		pending := len(w.inFlight)
		w.mu.RUnlock()
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d in-flight recompiles", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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
	addArtifactSourceDataNamed(t, c, indexName, "entity_card")
}

// addArtifactSourceDataNamed is addArtifactSourceData with a configurable
// artifact template name (needed for hybrid templates like user_profile).
func addArtifactSourceDataNamed(t *testing.T, c *Compiler, indexName, artifactName string) {
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
		Name: artifactName,
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
	waitForRecompiles(t, w)

	// Verify staleness was reset (recompiled)
	w.mu.RLock()
	for _, a := range w.tracked {
		if a.StalenessScore != 0 {
			t.Errorf("expected staleness reset after recompile, got %f", a.StalenessScore)
		}
		if a.LastRecompiledAt.IsZero() {
			t.Error("expected LastRecompiledAt to be set after recompile")
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
	waitForRecompiles(t, w)

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

// slowLLM is a mock LLM that sleeps to simulate a slow provider.
type slowLLM struct {
	delay time.Duration
}

func (m *slowLLM) Chat(systemPrompt, userQuery string) (string, error) {
	time.Sleep(m.delay)
	return `{"summary": "slow response"}`, nil
}

func (m *slowLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	return "", nil
}

// TestScanArtifactsDoesNotHoldLockDuringRecompile (B1): with a slow LLM, an
// OnEvent call issued while ScanArtifacts is recompiling must complete
// quickly — the watcher lock is not held during LLM calls.
func TestScanArtifactsDoesNotHoldLockDuringRecompile(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceDataNamed(t, c, "mcp_memory", "user_profile") // hybrid: LLM-assisted

	// Force LLM-assisted compilation (slow) for the watcher's recompiles.
	c.llm = &slowLLM{delay: 500 * time.Millisecond}

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 5.0
	}
	w.mu.Unlock()

	w.ScanArtifacts()

	// While the recompile goroutine is sleeping in the LLM call, OnEvent
	// must not block: it should complete well under the LLM delay.
	start := time.Now()
	w.OnEvent(engine.Event{Type: "vector.add", ID: "user:alice:mem1"})
	elapsed := time.Since(start)

	if elapsed >= 400*time.Millisecond {
		t.Errorf("OnEvent blocked for %v during recompile — watcher lock held during LLM call", elapsed)
	}

	waitForRecompiles(t, w)
}

// TestStalenessDecayUsesLastRecompiledAt (B2): after a recompile, the
// time-based decay of the next cycle starts from the recompile time, not
// from the original (possibly ancient) compile time.
func TestStalenessDecayUsesLastRecompiledAt(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 5.0
		a.CompiledAt = time.Now().Add(-30 * 24 * time.Hour) // ancient
	}
	w.mu.Unlock()

	// First scan: recompile (async) — resets the decay base to now.
	w.ScanArtifacts()
	waitForRecompiles(t, w)

	w.mu.RLock()
	for _, a := range w.tracked {
		if a.StalenessScore != 0 {
			t.Fatalf("staleness not reset: %f", a.StalenessScore)
		}
		if !a.LastRecompiledAt.IsZero() && a.CompiledAt != a.LastRecompiledAt {
			t.Errorf("CompiledAt not refreshed to LastRecompiledAt")
		}
	}
	w.mu.RUnlock()

	// Second scan immediately after: with the decay based on the fresh
	// recompile time, hoursSinceBase ≈ 0 → staleness stays near 0 and the
	// artifact must NOT be recompiled again.
	secondScanStart := time.Now()
	w.ScanArtifacts()
	waitForRecompiles(t, w)

	w.mu.RLock()
	recompiled := 0
	for _, a := range w.tracked {
		if a.RecompileCount > 1 {
			recompiled++
		}
	}
	w.mu.RUnlock()

	if recompiled > 0 {
		t.Errorf("artifact recompiled again on the next cycle with a fresh decay base (%d artifacts)", recompiled)
	}
	if time.Since(secondScanStart) > 2*time.Second {
		t.Errorf("second scan took %v — expected no recompile work", time.Since(secondScanStart))
	}
}

// TestWatcherInFlightGuardSkipsRecompilingArtifacts (B1): a scan must not
// select an artifact that is already being recompiled by a previous cycle.
func TestWatcherInFlightGuardSkipsRecompilingArtifacts(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceDataNamed(t, c, "mcp_memory", "user_profile") // hybrid: LLM-assisted

	c.llm = &slowLLM{delay: 300 * time.Millisecond}

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 5.0
	}
	w.mu.Unlock()

	// Scan 1: selects the artifact (in-flight), recompile runs async.
	w.ScanArtifacts()

	// Scan 2 while scan 1's recompile is still running: the artifact is
	// in-flight and must not be selected again.
	w.ScanArtifacts()

	w.mu.RLock()
	inflight := len(w.inFlight)
	w.mu.RUnlock()
	if inflight != 1 {
		t.Errorf("expected 1 in-flight recompile, got %d", inflight)
	}

	waitForRecompiles(t, w)

	w.mu.RLock()
	var rc int
	for _, a := range w.tracked {
		rc = a.RecompileCount
	}
	w.mu.RUnlock()
	if rc != 1 {
		t.Errorf("expected exactly 1 recompile, got %d", rc)
	}
}

// --- B3: refresh policy wiring ---

func TestResolveRefreshPolicyExplicitNoHistory(t *testing.T) {
	c := NewCompiler(nil, nil, nil)

	// TaskSpec with KeepHistory=false must win over the template default
	// (previously silently ignored).
	req := CompileRequest{
		TaskSpec: &TaskSpec{RefreshPolicy: RefreshPolicy{KeepHistory: false, MaxVersions: 1}},
	}
	tmpl, _ := GetTemplate("user_profile")
	policy := c.resolveRefreshPolicy(req, tmpl)
	if policy.KeepHistory {
		t.Error("explicit KeepHistory=false in TaskSpec was ignored")
	}
	if policy.MaxVersions != 1 {
		t.Errorf("MaxVersions = %d, want 1", policy.MaxVersions)
	}

	// No policy anywhere → built-in default.
	policy = c.resolveRefreshPolicy(CompileRequest{}, nil)
	def := DefaultRefreshPolicy()
	if !reflect.DeepEqual(policy, def) {
		t.Errorf("expected DefaultRefreshPolicy, got %+v", policy)
	}
}

func TestWatcherRespectsManualMode(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 100.0
		a.RefreshPolicy = RefreshPolicy{Mode: RefreshModeManual, KeepHistory: true}
	}
	w.mu.Unlock()

	w.ScanArtifacts()
	waitForRecompiles(t, w)

	w.mu.RLock()
	for _, a := range w.tracked {
		if a.RecompileCount != 0 {
			t.Error("manual-mode artifact was recompiled automatically")
		}
	}
	w.mu.RUnlock()
}

func TestWatcherScheduledMode(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	// Artifact with MaxStalenessH=1 and a very old decay base → scheduled trigger.
	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 0 // score-based trigger would NOT fire
		a.CompiledAt = time.Now().Add(-2 * time.Hour)
		a.RefreshPolicy = RefreshPolicy{
			Mode:          RefreshModeScheduled,
			MaxStalenessH: 1,
			KeepHistory:   true,
		}
	}
	w.mu.Unlock()

	w.ScanArtifacts()
	waitForRecompiles(t, w)

	w.mu.RLock()
	var rc int
	for _, a := range w.tracked {
		rc = a.RecompileCount
	}
	w.mu.RUnlock()
	if rc != 1 {
		t.Errorf("expected scheduled recompile (age > MaxStalenessH), got %d", rc)
	}
}

func TestWatcherScheduledModeNotDue(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.StalenessScore = 100.0 // score-based trigger WOULD fire, but scheduled mode says no
		a.CompiledAt = time.Now()
		a.RefreshPolicy = RefreshPolicy{
			Mode:          RefreshModeScheduled,
			MaxStalenessH: 24,
			KeepHistory:   true,
		}
	}
	w.mu.Unlock()

	w.ScanArtifacts()
	waitForRecompiles(t, w)

	w.mu.RLock()
	var rc int
	for _, a := range w.tracked {
		rc = a.RecompileCount
	}
	w.mu.RUnlock()
	if rc != 0 {
		t.Errorf("scheduled artifact not due yet — should not recompile, got %d", rc)
	}
}

func TestWatcherRecompileOnFilter(t *testing.T) {
	c, w, _ := newTestCompilerAndWatcher(t)
	addArtifactSourceData(t, c, "mcp_memory")

	w.mu.Lock()
	w.loadArtifacts()
	for _, a := range w.tracked {
		a.SourceNodeIDs = []string{"user:alice:mem1"}
		a.RefreshPolicy = RefreshPolicy{
			Mode:        RefreshModeOnSourceChange,
			RecompileOn: []string{"new_relationship"},
			KeepHistory: true,
		}
	}
	w.mu.Unlock()

	// vector.add event: filtered OUT by the policy → staleness unchanged.
	w.OnEvent(engine.Event{Type: engine.EventVectorAdd, ID: "user:alice:mem1"})
	w.mu.RLock()
	var stale float64
	for _, a := range w.tracked {
		stale = a.StalenessScore
	}
	w.mu.RUnlock()
	if stale != 0 {
		t.Errorf("vector.add should be filtered by RecompileOn, staleness = %f", stale)
	}

	// edge.create event: allowed → staleness increments.
	w.OnEvent(engine.Event{Type: engine.EventEdgeCreate, ID: "user:alice:mem1"})
	w.mu.RLock()
	for _, a := range w.tracked {
		stale = a.StalenessScore
	}
	w.mu.RUnlock()
	if stale != stalenessIncrementOnChange {
		t.Errorf("edge.create should increment staleness, got %f", stale)
	}
}
