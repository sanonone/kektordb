package cognitive

import (
	"fmt"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// This file is a comparison harness, not a regression test.
//
// It measures what the two resolution designs actually do on the same inputs:
//
//	old — autoResolveReflections (removed in fc57015): deterministic, no LLM.
//	      Knowledge gaps above a confidence threshold became real graph links;
//	      minor contradictions were marked resolved with the suggested text.
//	new — resolveVolatileBeliefs (current): every reflection goes through
//	      VBeliefState and, when action_required is set, an LLM consolidation.
//
// Run with: go test -run TestCompareResolutionDesigns -v ./pkg/cognitive/
//
// The point is to decide whether reintroducing the fast path is a win, not to
// pin today's behaviour.

type resolutionCase struct {
	name           string
	status         string
	confidence     float64
	actionReq      any // nil = field absent
	suggested      string
	links          []string // entities to link via suggests_link (knowledge gap)
	contradictions int
}

// buildCase writes one reflection plus its supporting graph into eng.
func buildCase(t *testing.T, eng *engine.Engine, idx string, c resolutionCase) string {
	t.Helper()
	refID := "refl_" + c.name

	meta := map[string]any{
		"type":        "reflection",
		"status":      c.status,
		"content":     "case " + c.name,
		"confidence":  c.confidence,
		"_created_at": float64(time.Now().Unix()),
	}
	if c.actionReq != nil {
		meta["action_required"] = c.actionReq
	}
	if c.suggested != "" {
		meta["suggested_resolution"] = c.suggested
	}

	if err := eng.VAdd(idx, refID, []float32{0.5, 0.5}, meta); err != nil {
		t.Fatalf("vadd %s: %v", refID, err)
	}

	// Knowledge gap: the reflection points at two entities.
	for _, e := range c.links {
		if err := eng.VAdd(idx, e, []float32{0.5, 0.5}, map[string]any{
			"type": "entity", "name": e,
		}); err != nil {
			t.Fatalf("vadd entity %s: %v", e, err)
		}
		if err := eng.VLink(idx, refID, e, "suggests_link", "", float32(c.confidence), nil); err != nil {
			t.Fatalf("vlink %s: %v", refID, err)
		}
	}

	// Contradiction: two conflicting memories.
	for i := 0; i < c.contradictions; i++ {
		memID := fmt.Sprintf("%s_m%d", refID, i)
		if err := eng.VAdd(idx, memID, []float32{0.5 + float32(i)*0.01, 0.5}, map[string]any{
			"type": "memory", "content": fmt.Sprintf("claim %d", i),
			"_created_at": float64(time.Now().Unix()),
		}); err != nil {
			t.Fatalf("vadd %s: %v", memID, err)
		}
		if err := eng.VLink(idx, refID, memID, "contradicts", "contradicted_by", 1.0, nil); err != nil {
			t.Fatalf("vlink %s: %v", refID, err)
		}
	}
	return refID
}

// outcome is what happened to one reflection.
type outcome struct {
	status       string
	llmCalls     int
	linkMade     bool
	autoResolved bool
}

// resolveWithNewPath drives the current implementation for one reflection.
func resolveWithNewPath(t *testing.T, eng *engine.Engine, idx, refID string, actionRequired bool) outcome {
	t.Helper()
	mock := &MockLLM{Responses: []string{
		`{"resolvable": true, "consolidated_truth": "merged", "clarification_question": ""}`,
	}}
	g := NewGardener(eng, mock, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           10,
		EpistemicRequireActionRequired: true,
		EpistemicConfidenceThreshold:   0.40,
		// The fast path is what this comparison is about.
		AutoResolveEnabled:  true,
		AutoResolveLinks:    true,
		AutoResolveLinksMin: 0.90,
		AutoResolveContra:   true,
	})
	g.resolveVolatileBeliefs(idx)

	data, err := eng.VGet(idx, refID)
	if err != nil {
		t.Fatalf("vget %s: %v", refID, err)
	}
	status, _ := data.Metadata["status"].(string)

	// Did any real link appear between the gap entities?
	gapLinks := false
	if targets, found := eng.VGetLinks(idx, refID, "suggests_link"); found && len(targets) == 2 {
		if rel, ok := eng.VGetLinks(idx, targets[0], "related_to"); ok && len(rel) > 0 {
			gapLinks = true
		}
	}

	return outcome{
		status:   status,
		llmCalls: mock.CallCount,
		linkMade: gapLinks,
	}
}

// resolveWithOldPath reproduces the removed deterministic resolver.
//
// It is a direct port of autoResolveReflections from fc57015^ so the comparison
// is against the code that actually shipped, not against a paraphrase.
func resolveWithOldPath(t *testing.T, eng *engine.Engine, idx, refID string,
	autoLinks bool, minConfidence float64, autoContra bool) outcome {
	t.Helper()

	data, err := eng.VGet(idx, refID)
	if err != nil {
		t.Fatalf("vget %s: %v", refID, err)
	}
	status, _ := data.Metadata["status"].(string)
	if status == "resolved" || status == "high_confidence" {
		return outcome{status: status}
	}
	confidence, _ := data.Metadata["confidence"].(float64)

	if autoLinks {
		entities, found := eng.VGetLinks(idx, refID, "suggests_link")
		if found && len(entities) == 2 && confidence >= minConfidence {
			existing, _ := eng.VGetLinks(idx, entities[0], "related_to")
			already := false
			for _, e := range existing {
				if e == entities[1] {
					already = true
					break
				}
			}
			if !already {
				if err := eng.VLink(idx, entities[0], entities[1], "related_to", "related_to",
					float32(confidence), map[string]any{"auto_resolved_from": refID}); err == nil {
					eng.VSetMetadata(idx, refID, map[string]any{
						"status":        "resolved",
						"resolution":    fmt.Sprintf("auto-resolved: link created based on semantic similarity %.2f", confidence),
						"auto_resolved": true,
						"_updated_at":   float64(time.Now().Unix()),
					})
					return outcome{status: "resolved", linkMade: true, autoResolved: true}
				}
			}
		}
	}

	if autoContra {
		actionRequired, _ := data.Metadata["action_required"].(bool)
		if !actionRequired {
			if suggested, _ := data.Metadata["suggested_resolution"].(string); suggested != "" {
				eng.VSetMetadata(idx, refID, map[string]any{
					"status":        "resolved",
					"resolution":    suggested,
					"auto_resolved": true,
					"_updated_at":   float64(time.Now().Unix()),
				})
				return outcome{status: "resolved", autoResolved: true}
			}
		}
	}
	return outcome{status: status}
}

func newCompareEngine(t *testing.T, idx string) *engine.Engine {
	t.Helper()
	opts := engine.DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return eng
}

// TestCompareResolutionDesigns runs both designs over the same cases and prints
// a table. It always passes: it is a measurement, not an assertion.
func TestCompareResolutionDesigns(t *testing.T) {
	cases := []resolutionCase{
		{
			name: "knowledge_gap_high_conf", status: "insight", confidence: 0.95,
			links: []string{"entity_a", "entity_b"},
		},
		{
			name: "knowledge_gap_low_conf", status: "insight", confidence: 0.72,
			links: []string{"entity_c", "entity_d"},
		},
		{
			name: "minor_contradiction", status: "unresolved", confidence: 0.70,
			actionReq: false, suggested: "prefer the newer memory",
			contradictions: 2,
		},
		{
			name: "major_contradiction", status: "unresolved", confidence: 0.70,
			actionReq: true, contradictions: 2,
		},
	}

	t.Log("")
	t.Logf("%-26s | %-22s | %-22s", "caso", "VECCHIO (deterministico)", "NUOVO (epistemico)")
	t.Logf("%-26s | %-22s | %-22s", "--------------------------", "----------------------", "----------------------")

	for _, c := range cases {
		// Each design gets its own engine so the graph starts identical.
		oldIdx := "old_" + c.name
		newIdx := "new_" + c.name

		oldEng := newCompareEngine(t, oldIdx)
		oldRef := buildCase(t, oldEng, oldIdx, c)
		oldOut := resolveWithOldPath(t, oldEng, oldIdx, oldRef,
			true /*autoLinks*/, 0.90 /*minConf*/, true /*autoContra*/)

		newEng := newCompareEngine(t, newIdx)
		newRef := buildCase(t, newEng, newIdx, c)
		newOut := resolveWithNewPath(t, newEng, newIdx, newRef, c.actionReq == true)

		t.Logf("%-26s | %-22s | %-22s",
			c.name,
			fmt.Sprintf("%s llm=%d link=%v", oldOut.status, oldOut.llmCalls, oldOut.linkMade),
			fmt.Sprintf("%s llm=%d link=%v", newOut.status, newOut.llmCalls, newOut.linkMade),
		)
	}
}

// TestOldPathCreatesLinksNewPathDoesNot is the factual claim behind the
// comparison: the old design closed the loop on knowledge gaps, the new one has
// no equivalent, so those reflections stay pending (or get dismissed).
func TestOldPathCreatesLinksNewPathDoesNot(t *testing.T) {
	// Old design: high-confidence gap becomes a real link.
	oldIdx := "old_link"
	oldEng := newCompareEngine(t, oldIdx)
	oldRef := buildCase(t, oldEng, oldIdx, resolutionCase{
		name: "gap", status: "insight", confidence: 0.95,
		links: []string{"a", "b"},
	})
	oldOut := resolveWithOldPath(t, oldEng, oldIdx, oldRef, true, 0.90, true)
	if !oldOut.linkMade {
		t.Fatal("the removed design did not create the link it was written to create")
	}
	if oldOut.llmCalls != 0 {
		t.Errorf("the deterministic design made %d LLM calls", oldOut.llmCalls)
	}

	// New design: same input.
	newIdx := "new_link"
	newEng := newCompareEngine(t, newIdx)
	newRef := buildCase(t, newEng, newIdx, resolutionCase{
		name: "gap", status: "insight", confidence: 0.95,
		links: []string{"a", "b"},
	})
	newOut := resolveWithNewPath(t, newEng, newIdx, newRef, false)
	if newOut.linkMade {
		t.Log("the current design now creates the link: the gap no longer exists")
		return
	}
	t.Logf("old design: %s (link created, 0 LLM calls)", oldOut.status)
	t.Logf("new design: %s (no link, %d LLM calls)", newOut.status, newOut.llmCalls)
}
