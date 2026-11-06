package cognitive

import (
	"testing"
	"time"
)

// Tests for the deterministic fast path that runs before the epistemic engine.
//
// It restores two behaviours the epistemic rewrite dropped (see
// resolution_compare_test.go for the measurement): closing a knowledge gap by
// writing the link the detector suggested, and recording the resolution the
// contradiction detector already produced for reflections it marked
// action_required=false. Neither needs an LLM.

// newAutoResolveGardener builds a gardener with the fast path enabled.
func newAutoResolveGardener(t *testing.T, idx string, autoLinks, autoContra bool, minConf float64) *Gardener {
	t.Helper()
	g, _ := func() (*Gardener, any) {
		g, eng := newDebounceTestGardener(t, idx)
		_ = eng
		return g, nil
	}()
	g.cfg.AutoResolveEnabled = true
	g.cfg.AutoResolveLinks = autoLinks
	g.cfg.AutoResolveLinksMin = minConf
	g.cfg.AutoResolveContra = autoContra
	g.cfg.EpistemicResolutionEnabled = true
	g.cfg.EpistemicMaxPerCycle = 10
	return g
}

// TestAutoResolveCreatesTheSuggestedLink is the behaviour that was lost.
func TestAutoResolveCreatesTheSuggestedLink(t *testing.T) {
	idx := "auto_link"
	g := newAutoResolveGardener(t, idx, true, true, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "gap", status: "insight", confidence: 0.95,
		links: []string{"alpha", "beta"},
	})

	g.resolveVolatileBeliefs(idx)

	// The link exists in both directions with the confidence as weight.
	rels, ok := g.eng.VGetLinks(idx, "alpha", "related_to")
	if !ok || len(rels) == 0 {
		t.Fatalf("the knowledge gap was not closed: no related_to edge from alpha")
	}
	found := false
	for _, r := range rels {
		if r == "beta" {
			found = true
		}
	}
	if !found {
		t.Errorf("related_to from alpha points at %v, want beta", rels)
	}

	data, err := g.eng.VGet(idx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := data.Metadata["status"].(string); got != "resolved" {
		t.Errorf("reflection status = %q, want resolved", got)
	}
	if auto, _ := data.Metadata["auto_resolved"].(bool); !auto {
		t.Error("the reflection does not record that it was resolved automatically")
	}
	if res, _ := data.Metadata["resolution"].(string); res == "" {
		t.Error("the reflection has no resolution text: the decision is not auditable")
	}
}

// TestAutoResolveRespectsMinConfidence keeps the threshold meaningful.
func TestAutoResolveRespectsMinConfidence(t *testing.T) {
	idx := "auto_threshold"
	g := newAutoResolveGardener(t, idx, true, true, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "gap_low", status: "insight", confidence: 0.72,
		links: []string{"gamma", "delta"},
	})

	g.resolveVolatileBeliefs(idx)

	if rels, ok := g.eng.VGetLinks(idx, "gamma", "related_to"); ok && len(rels) > 0 {
		t.Fatalf("a link was created below the configured threshold (edges: %v)", rels)
	}
	data, _ := g.eng.VGet(idx, ref)
	if got, _ := data.Metadata["status"].(string); got == "resolved" {
		t.Error("a reflection below the threshold was marked resolved")
	}
}

// TestAutoResolveSkipsExistingLinkButSettlesTheReflection covers the dedup path:
// the link exists, so nothing is written, but the reflection must not stay
// pending and be re-examined every cycle.
func TestAutoResolveSkipsExistingLinkButSettlesTheReflection(t *testing.T) {
	idx := "auto_dedup"
	g := newAutoResolveGardener(t, idx, true, true, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "gap_dup", status: "insight", confidence: 0.95,
		links: []string{"eps", "zeta"},
	})

	// The link is already there.
	if err := g.eng.VLink(idx, "eps", "zeta", "related_to", "related_to", 0.99, nil); err != nil {
		t.Fatal(err)
	}

	g.resolveVolatileBeliefs(idx)

	data, _ := g.eng.VGet(idx, ref)
	if got, _ := data.Metadata["status"].(string); got != "resolved" {
		t.Errorf("status = %q, want resolved: an already-linked gap stays pending forever", got)
	}
}

// TestAutoResolveRecordsMinorContradiction is the second restored behaviour.
func TestAutoResolveRecordsMinorContradiction(t *testing.T) {
	idx := "auto_contra"
	g := newAutoResolveGardener(t, idx, true, true, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "minor", status: "unresolved", confidence: 0.70,
		actionReq: false, suggested: "prefer the newer memory",
		contradictions: 2,
	})

	g.resolveVolatileBeliefs(idx)

	data, err := g.eng.VGet(idx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := data.Metadata["status"].(string); got != "resolved" {
		t.Errorf("status = %q, want resolved", got)
	}
	if res, _ := data.Metadata["resolution"].(string); res != "prefer the newer memory" {
		t.Errorf("resolution = %q, want the detector's suggestion: the reason is what "+
			"makes the auto-resolution auditable", res)
	}
}

// TestAutoResolveLeavesMajorContradictionsToTheEpistemicPath keeps the division
// of labour: what needs a judgement still gets one.
func TestAutoResolveLeavesMajorContradictionsToTheEpistemicPath(t *testing.T) {
	idx := "auto_major"
	g := newAutoResolveGardener(t, idx, true, true, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "major", status: "unresolved", confidence: 0.70,
		actionReq: true, contradictions: 2,
	})

	g.resolveVolatileBeliefs(idx)

	data, _ := g.eng.VGet(idx, ref)
	status, _ := data.Metadata["status"].(string)
	if status == "resolved" {
		t.Error("a contradiction requiring action was auto-resolved without a judgement")
	}
	if status == "unresolved" {
		t.Error("a contradiction requiring action was left unresolved: the epistemic " +
			"path should still have looked at it")
	}
}

// TestAutoResolveRespectsDisableFlags: with the fast path off, nothing changes.
func TestAutoResolveRespectsDisableFlags(t *testing.T) {
	idx := "auto_off"
	g := newAutoResolveGardener(t, idx, false, false, 0.90)
	ref := buildCase(t, g.eng, idx, resolutionCase{
		name: "off", status: "insight", confidence: 0.95,
		links: []string{"eta", "theta"},
	})

	g.resolveVolatileBeliefs(idx)

	if rels, ok := g.eng.VGetLinks(idx, "eta", "related_to"); ok && len(rels) > 0 {
		t.Fatal("a link was created although AutoResolveLinks is off")
	}
	data, _ := g.eng.VGet(idx, ref)
	if got, _ := data.Metadata["status"].(string); got == "resolved" {
		t.Error("a reflection was resolved although the fast path is off")
	}
}

// TestAutoResolveMakesNoLLMCalls guards the reason the fast path exists: these
// cases must not cost a request.
func TestAutoResolveMakesNoLLMCalls(t *testing.T) {
	idx := "auto_nollm"
	g, _ := newDebounceTestGardener(t, idx)
	eng := g.eng
	mock := &MockLLM{}
	g = NewGardener(eng, mock, Config{
		Enabled:                    true,
		Interval:                   time.Hour,
		EpistemicResolutionEnabled: true,
		EpistemicMaxPerCycle:       10,
		AutoResolveEnabled:         true,
		AutoResolveLinks:           true,
		AutoResolveLinksMin:        0.90,
		AutoResolveContra:          true,
	})

	buildCase(t, eng, idx, resolutionCase{
		name: "gap", status: "insight", confidence: 0.95,
		links: []string{"iota", "kappa"},
	})
	buildCase(t, eng, idx, resolutionCase{
		name: "minor", status: "unresolved", confidence: 0.70,
		actionReq: false, suggested: "keep the newer", contradictions: 2,
	})

	g.resolveVolatileBeliefs(idx)

	if mock.CallCount != 0 {
		t.Errorf("LLM calls = %d, want 0: the deterministic path must not pay for a request",
			mock.CallCount)
	}
}
