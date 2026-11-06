package cognitive

import (
	"math"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// newCrossValidatorEngine builds an engine with an entity and two reflections
// pointing at it through two different detector edges.
func newCrossValidatorEngine(t *testing.T, idx string) *engine.Engine {
	t.Helper()
	opts := engine.DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd(idx, "entity", []float32{1.0, 0.0}, map[string]any{
		"name": "Python", "type": "entity",
	}); err != nil {
		t.Fatalf("vadd entity: %v", err)
	}
	return eng
}

// findCrossReflection returns the id and confidence of the cross-validated
// reflection the detector produced, or ok=false if it produced none.
func findCrossReflection(t *testing.T, eng *engine.Engine, idx string) (string, float64, bool) {
	t.Helper()
	ids, err := eng.VFilter(idx, "type='reflection' AND status='high_confidence'", 10)
	if err != nil {
		t.Fatalf("vfilter: %v", err)
	}
	if len(ids) == 0 {
		return "", 0, false
	}
	data, err := eng.VGet(idx, ids[0])
	if err != nil {
		t.Fatalf("vget: %v", err)
	}
	conf, _ := data.Metadata["confidence"].(float64)
	return ids[0], conf, true
}

// TestCrossValidatorRejectsZeroEvidence covers a confidence that was computed
// from nothing and came out at the top of the scale.
//
// The composite is a geometric mean: product starts at 1.0 and is multiplied by
// each confidence. With an empty slice the loop never runs, the exponent is
// 1.0/0 = +Inf, and math.Pow(1.0, +Inf) is 1.0 — so a reflection backed by no
// measurement at all was stored as high_confidence with a near-maximum score.
//
// Two detectors flagged the entity (so the diversity gate passes), but neither
// reflection carries a confidence: VGet fails, or the field is missing, or it is
// not a float64. That is reachable: the reflection id is appended to
// newReflections before VAdd, so a failed insert leaves the id pointing at
// nothing.
func TestCrossValidatorRejectsZeroEvidence(t *testing.T) {
	idx := "zero_evidence"
	eng := newCrossValidatorEngine(t, idx)

	// Two reflections with detector edges but no confidence metadata.
	for _, name := range []string{"ref_a", "ref_b"} {
		if err := eng.VAdd(idx, name, []float32{0.5, 0.5}, map[string]any{
			"type": "reflection", "content": "no confidence here", "status": "insight",
		}); err != nil {
			t.Fatalf("vadd %s: %v", name, err)
		}
	}
	if err := eng.VLink(idx, "ref_a", "entity", "focus_shifted", "focus_shifted_by", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(idx, "ref_b", "entity", "sentiment_shift", "sentiment_shifted_by", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	g := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	g.newReflections = []string{"ref_a", "ref_b"}
	g.detectCrossValidator(idx)

	id, conf, ok := findCrossReflection(t, eng, idx)
	if !ok {
		// Refusing to emit anything is a valid answer: no evidence, no verdict.
		return
	}
	if conf >= 0.5 {
		t.Errorf("reflection %s has confidence %.3f built from zero measurements: "+
			"an empty evidence set produced a near-maximum score", id, conf)
	}
}

// TestCrossValidatorConfidenceIsGeometricMean is the correctness check for the
// normal path: the score must reflect the inputs.
func TestCrossValidatorConfidenceIsGeometricMean(t *testing.T) {
	idx := "geomean"
	eng := newCrossValidatorEngine(t, idx)

	for name, conf := range map[string]float64{"ref_a": 0.8, "ref_b": 0.45} {
		if err := eng.VAdd(idx, name, []float32{0.5, 0.5}, map[string]any{
			"type": "reflection", "content": "x", "status": "insight", "confidence": conf,
		}); err != nil {
			t.Fatalf("vadd %s: %v", name, err)
		}
	}
	eng.VLink(idx, "ref_a", "entity", "focus_shifted", "focus_shifted_by", 1.0, nil)
	eng.VLink(idx, "ref_b", "entity", "sentiment_shift", "sentiment_shifted_by", 1.0, nil)

	g := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	g.newReflections = []string{"ref_a", "ref_b"}
	g.detectCrossValidator(idx)

	_, conf, ok := findCrossReflection(t, eng, idx)
	if !ok {
		t.Fatal("no cross-validated reflection produced for two detectors with evidence")
	}

	// sqrt(0.8*0.45) = 0.6 ; diversity boost with 2 detectors = 0.7+0.3*(2/3) = 0.9
	want := math.Min(1.0, math.Sqrt(0.8*0.45)*0.9)
	if math.Abs(conf-want) > 0.001 {
		t.Errorf("confidence = %.4f, want %.4f (geometric mean of the inputs x diversity)", conf, want)
	}
	if conf >= 1.0 {
		t.Error("confidence saturated to 1.0 from inputs below 1.0")
	}
}

// TestCrossValidatorSingleDetectorDoesNotQualify keeps the diversity gate: one
// detector alone is not corroboration.
func TestCrossValidatorSingleDetectorDoesNotQualify(t *testing.T) {
	idx := "single"
	eng := newCrossValidatorEngine(t, idx)

	eng.VAdd(idx, "ref_a", []float32{0.5, 0.5}, map[string]any{
		"type": "reflection", "content": "x", "status": "insight", "confidence": 0.9,
	})
	eng.VLink(idx, "ref_a", "entity", "focus_shifted", "focus_shifted_by", 1.0, nil)

	g := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	g.newReflections = []string{"ref_a"}
	g.detectCrossValidator(idx)

	if _, _, ok := findCrossReflection(t, eng, idx); ok {
		t.Error("a single detector produced a cross-validated reflection")
	}
}

// TestCrossValidatorDanglingIDs: a reflection listed in newReflections but not
// present in the index (VAdd failed, or it was deleted) must not count as
// evidence. This is the reachable path from production.
func TestCrossValidatorDanglingIDs(t *testing.T) {
	idx := "dangling"
	eng := newCrossValidatorEngine(t, idx)

	// Only the edges exist; the reflections themselves never landed.
	eng.VAdd(idx, "ref_real", []float32{0.5, 0.5}, map[string]any{
		"type": "reflection", "content": "x", "status": "insight", "confidence": 0.5,
	})
	eng.VLink(idx, "ref_real", "entity", "focus_shifted", "focus_shifted_by", 1.0, nil)
	// A second detector edge from a reflection that does not exist.
	eng.VLink(idx, "ghost", "entity", "sentiment_shift", "sentiment_shifted_by", 1.0, nil)

	g := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	g.newReflections = []string{"ref_real", "ghost"}
	g.detectCrossValidator(idx)

	id, conf, ok := findCrossReflection(t, eng, idx)
	if !ok {
		return // no verdict is fine
	}
	// One real measurement (0.5) exists, so the score must be built from that
	// alone — not from the geometric mean of an empty set.
	if conf >= 0.9 {
		t.Errorf("reflection %s confidence = %.3f: a missing reflection was "+
			"counted as evidence of strength", id, conf)
	}
}
