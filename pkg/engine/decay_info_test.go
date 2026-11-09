package engine

import (
	"math"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// Tests for the two accessors that let a caller inspect and explain what the
// engine did: last snapshot time, and the decay applied to one memory.

func newDecayEngine(t *testing.T, idx string) *Engine {
	t.Helper()
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	opts.MaintenanceInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return eng
}

// TestLastSnapshotTimeIsZeroBeforeAnySnapshot pins the "never" case: a zero time
// is distinguishable from "a long time ago", which is what a status view needs.
func TestLastSnapshotTimeIsZeroBeforeAnySnapshot(t *testing.T) {
	eng := newDecayEngine(t, "snap")
	if got := eng.LastSnapshotTime(); !got.IsZero() {
		t.Fatalf("LastSnapshotTime = %v before any snapshot, want zero", got)
	}
}

// TestLastSnapshotTimeTracksSaveSnapshot is the positive case.
func TestLastSnapshotTimeTracksSaveSnapshot(t *testing.T) {
	eng := newDecayEngine(t, "snap2")
	if err := eng.VAdd("snap2", "a", []float32{1, 0, 0, 0}, map[string]any{"content": "x"}); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	got := eng.LastSnapshotTime()
	if got.IsZero() {
		t.Fatal("LastSnapshotTime is zero after a snapshot")
	}
	if got.Before(before.Add(-time.Second)) {
		t.Errorf("LastSnapshotTime = %v, earlier than the snapshot we just took", got)
	}
	if age := time.Since(got); age > 5*time.Second {
		t.Errorf("LastSnapshotTime is %v old right after a snapshot", age)
	}
}

// TestDecayExplanationMatchesSearchFactor is the property that matters: the
// number shown to a reader must be the number that shaped the ranking. If these
// two ever diverge, the diagnostic view lies.
func TestDecayExplanationMatchesSearchFactor(t *testing.T) {
	const idx = "decay"
	eng := newDecayEngine(t, idx)

	// A memory 30 days old, in a layer with a 30-day half-life: one half-life
	// has passed, so the exponential factor should be 0.5.
	created := float64(time.Now().Add(-30 * 24 * time.Hour).Unix())
	if err := eng.VAdd(idx, "old", []float32{1, 0, 0, 0}, map[string]any{
		"content":      "an old memory",
		"memory_layer": "semantic",
		"_created_at":  created,
	}); err != nil {
		t.Fatal(err)
	}

	info, err := eng.DecayExplanation(idx, "old")
	if err != nil {
		t.Fatalf("DecayExplanation: %v", err)
	}
	if info.Model == "" || info.Model == "none" {
		t.Fatalf("DecayExplanation reported model %q for a timestamped memory", info.Model)
	}
	if info.Layer != "semantic" {
		t.Errorf("layer = %q, want semantic", info.Layer)
	}
	if info.AgeDays < 29 || info.AgeDays > 31 {
		t.Errorf("age = %.1f days, want ~30", info.AgeDays)
	}

	// The default semantic half-life is 720h = 30 days.
	if math.Abs(info.HalfLifeHours-720) > 1 {
		t.Errorf("half-life = %.0fh, want 720h", info.HalfLifeHours)
	}

	// Recompute what the search path would use and compare.
	memCfg := hnsw.DefaultMemoryConfig()
	p, ok := eng.resolveDecay(map[string]any{
		"memory_layer": "semantic",
		"_created_at":  created,
	}, memCfg)
	if !ok {
		t.Fatal("resolveDecay found no reference time")
	}
	wantFactor := calculateTimeDecayModel(p.referenceTime, p.halfLifeSecond, p.model, p.accessCount)
	if math.Abs(info.Factor-wantFactor) > 1e-9 {
		t.Errorf("factor = %.6f, the search path would use %.6f", info.Factor, wantFactor)
	}

	if info.Explanation == "" {
		t.Error("no explanation: a reader cannot understand the number")
	}
}

// TestDecayExplanationEbbinghausReinforcement covers the model where access count
// changes the outcome, and checks the explanation says so.
func TestDecayExplanationEbbinghausReinforcement(t *testing.T) {
	const idx = "ebb"
	eng := newDecayEngine(t, idx)

	created := float64(time.Now().Add(-15 * 24 * time.Hour).Unix())
	for id, access := range map[string]float64{"never": 0, "reinforced": 9} {
		meta := map[string]any{
			"content":      id,
			"memory_layer": "semantic",
			"_created_at":  created,
			"_decay_model": "ebbinghaus",
		}
		if access > 0 {
			meta["_access_count"] = access
		}
		if err := eng.VAdd(idx, id, []float32{1, 0, 0, 0}, meta); err != nil {
			t.Fatal(err)
		}
	}

	never, err := eng.DecayExplanation(idx, "never")
	if err != nil {
		t.Fatal(err)
	}
	reinforced, err := eng.DecayExplanation(idx, "reinforced")
	if err != nil {
		t.Fatal(err)
	}

	if never.Model != "ebbinghaus" || reinforced.Model != "ebbinghaus" {
		t.Fatalf("models = %q / %q, want ebbinghaus", never.Model, reinforced.Model)
	}
	if reinforced.Factor <= never.Factor {
		t.Errorf("reinforced factor %.4f does not exceed never-reinforced %.4f: "+
			"reinforcement has no effect", reinforced.Factor, never.Factor)
	}
	if reinforced.EffectiveHalfLifeHours <= reinforced.HalfLifeHours {
		t.Errorf("effective half-life %.0fh not extended past %.0fh",
			reinforced.EffectiveHalfLifeHours, reinforced.HalfLifeHours)
	}
	if never.EffectiveHalfLifeHours != never.HalfLifeHours {
		t.Errorf("a never-reinforced memory reports an extended half-life: %.0f vs %.0f",
			never.EffectiveHalfLifeHours, never.HalfLifeHours)
	}
}

// TestDecayExplanationNoTimestamp: a memory without a reference time does not
// decay, and the explanation must say that rather than report a factor of 0.
func TestDecayExplanationNoTimestamp(t *testing.T) {
	const idx = "nots"
	eng := newDecayEngine(t, idx)

	if err := eng.VAdd(idx, "plain", []float32{1, 0, 0, 0}, map[string]any{"content": "no ts"}); err != nil {
		t.Fatal(err)
	}
	info, err := eng.DecayExplanation(idx, "plain")
	if err != nil {
		t.Fatalf("DecayExplanation: %v", err)
	}
	if info.Factor != 1.0 {
		t.Errorf("factor = %v for a memory with no timestamp, want 1.0", info.Factor)
	}
	if info.Explanation == "" {
		t.Error("no explanation for the no-decay case")
	}
}

// TestDecayExplanationMissingMemory is the error path.
func TestDecayExplanationMissingMemory(t *testing.T) {
	eng := newDecayEngine(t, "missing")
	if _, err := eng.DecayExplanation("missing", "does-not-exist"); err == nil {
		t.Fatal("DecayExplanation returned no error for a missing memory")
	}
}

// TestDecayExplanationProceduralLayerDoesNotDecay keeps the "no decay" layer
// honest: procedural memories have a zero half-life.
func TestDecayExplanationProceduralLayerDoesNotDecay(t *testing.T) {
	const idx = "proc"
	eng := newDecayEngine(t, idx)

	if err := eng.VAdd(idx, "rule", []float32{1, 0, 0, 0}, map[string]any{
		"content":      "a rule",
		"memory_layer": "procedural",
		"_created_at":  float64(time.Now().Add(-365 * 24 * time.Hour).Unix()),
	}); err != nil {
		t.Fatal(err)
	}

	info, err := eng.DecayExplanation(idx, "rule")
	if err != nil {
		t.Fatal(err)
	}
	if info.HalfLifeHours != 0 {
		t.Errorf("half-life = %.0fh for a procedural memory, want 0", info.HalfLifeHours)
	}
	if info.Explanation == "" {
		t.Error("no explanation for the no-decay layer")
	}
}
