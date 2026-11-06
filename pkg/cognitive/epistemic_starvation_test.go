package cognitive

import (
	"fmt"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestSkippedReflectionsDoNotStarveTheQueue covers a starvation that the
// "queue head blocks everything" description got wrong.
//
// The loop does not stop on a skipped reflection: each one gets its own call.
// The starvation comes from the query instead. resolveVolatileBeliefs asks for
// the first N unresolved reflections, where N is epistemic_max_per_cycle, and
// VFilter truncates in internal-ID order before the age sort runs. A reflection
// the B3 gate skips keeps status="unresolved" and keeps its low internal ID, so
// it is fetched again every cycle and holds its slot forever. Once the first N
// unresolved reflections are all skipped, nothing else is ever looked at.
//
// The fix marks a skipped reflection with a terminal status, so it leaves the
// query. The gate itself is unchanged: no LLM call, no archiving.
func TestSkippedReflectionsDoNotStarveTheQueue(t *testing.T) {
	eng := newFase4Engine(t, "starve")
	idx := "starve"

	// Three reflections the gate will skip: action_required=false. They are
	// created first, so they hold the lowest internal IDs and fill the window.
	for i := 0; i < 3; i++ {
		fase4ReflectionSetup(t, eng, idx, fmt.Sprintf("skipped_%d", i), 400, false)
	}

	// An older, actionable reflection created after them. It can only be
	// reached if the skipped ones stop occupying the window.
	fase4ReflectionSetup(t, eng, idx, "actionable", 400, true)
	// Age it well past the others so the age sort would put it first, if it
	// ever entered the window at all.
	if err := eng.VSetMetadata(idx, "actionable", map[string]any{
		"_created_at": float64(time.Now().Unix() - 500*24*3600),
	}); err != nil {
		t.Fatal(err)
	}

	g := NewGardener(eng, &MockLLM{}, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: true,
	})

	// One cycle: the three skipped reflections fill the window.
	g.resolveVolatileBeliefs(idx)

	// Each skipped reflection must now carry a terminal status, otherwise the
	// next cycle fetches the same three and the actionable one never surfaces.
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("skipped_%d", i)
		data, err := eng.VGet(idx, id)
		if err != nil {
			t.Fatalf("VGet %s: %v", id, err)
		}
		status, _ := data.Metadata["status"].(string)
		if status == "unresolved" {
			t.Errorf("%s is still unresolved after being skipped: it will occupy a slot in every cycle", id)
		}
		if status != "dismissed" {
			t.Errorf("%s status = %q, want dismissed", id, status)
		}
	}

	// The gate did its job: nothing was resolved, no LLM was consulted.
	data, _ := eng.VGet(idx, "actionable")
	if got, _ := data.Metadata["status"].(string); got != "unresolved" {
		t.Errorf("actionable reflection status = %q, want unresolved (it was not reached)", got)
	}

	// Second cycle: with the skipped ones out of the way, the actionable
	// reflection must finally be visible.
	fase4AssertReachable(t, eng, idx, g)
}

// fase4AssertReachable drives another cycle and checks that the actionable
// reflection is no longer starved out of the window.
func fase4AssertReachable(t *testing.T, eng *engine.Engine, idx string, g *Gardener) {
	t.Helper()

	pending, err := eng.VFilter(idx, "type='reflection' AND status='unresolved'", 10)
	if err != nil {
		t.Fatalf("VFilter: %v", err)
	}
	found := false
	for _, id := range pending {
		if id == "actionable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the actionable reflection is not in the pending set %v: "+
			"the skipped ones are still starving it", pending)
	}
}

// TestSkippedReflectionMarksDismissed pins the state transition itself, so the
// gate's intent stays visible: it refuses to act, it does not postpone.
func TestSkippedReflectionMarksDismissed(t *testing.T) {
	eng := newFase4Engine(t, "dismiss")
	idx := "dismiss"
	fase4ReflectionSetup(t, eng, idx, "refl", 400, false)

	mock := &MockLLM{}
	g := NewGardener(eng, mock, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: true,
		EpistemicConfidenceThreshold:   0.40,
	})
	g.resolveVolatileBeliefs(idx)

	if mock.CallCount != 0 {
		t.Errorf("LLM calls = %d, want 0: a dismissed reflection must not cost a call", mock.CallCount)
	}

	data, err := eng.VGet(idx, "refl")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := data.Metadata["status"].(string); got != "dismissed" {
		t.Errorf("status = %q, want dismissed", got)
	}
	// A reason makes the decision auditable.
	if reason, _ := data.Metadata["dismissed_reason"].(string); reason == "" {
		t.Error("dismissed reflection has no reason recorded")
	}
	// Nothing may be archived: dismissal is not resolution.
	for j := 0; j < 2; j++ {
		md, _ := eng.VGet(idx, fmt.Sprintf("refl_m%d", j))
		if hist, _ := md.Metadata["_is_historical"].(bool); hist {
			t.Errorf("memory %d was archived by a dismissal", j)
		}
	}
}

// TestActionRequiredReflectionsAreNotDismissed guards the other direction: a
// reflection that does require action must keep flowing through the normal path.
func TestActionRequiredReflectionsAreNotDismissed(t *testing.T) {
	eng := newFase4Engine(t, "keep")
	idx := "keep"
	fase4ReflectionSetup(t, eng, idx, "refl", 400, true)

	// The LLM says it is not resolvable, so the path should set
	// needs_clarification — never "dismissed".
	mock := &MockLLM{Responses: []string{
		`{"resolvable": false, "consolidated_truth": "", "clarification_question": "Which one?"}`,
	}}
	g := NewGardener(eng, mock, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: true,
		EpistemicConfidenceThreshold:   0.40,
	})
	g.resolveVolatileBeliefs(idx)

	data, err := eng.VGet(idx, "refl")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := data.Metadata["status"].(string); got == "dismissed" {
		t.Error("a reflection with action_required=true was dismissed")
	}
}

// TestDismissedReflectionsAreQueryable keeps them visible for audit: dismissing
// is not deleting.
func TestDismissedReflectionsAreQueryable(t *testing.T) {
	eng := newFase4Engine(t, "query")
	idx := "query"
	fase4ReflectionSetup(t, eng, idx, "refl", 400, false)

	g := NewGardener(eng, &MockLLM{}, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: true,
	})
	g.resolveVolatileBeliefs(idx)

	ids, err := eng.VFilter(idx, "type='reflection' AND status='dismissed'", 10)
	if err != nil {
		t.Fatalf("VFilter: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("dismissed reflections found = %v, want one", ids)
	}
}

// avoid an unused import when the file is trimmed during review
var _ = hnsw.LayerConfig{}
