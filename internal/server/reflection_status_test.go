package server

import "testing"

// TestValidReflectionStatusesCoversTheLifecycle keeps the API filter in step
// with the statuses the Gardener actually writes.
//
// The list exists to stop filter injection, but it also decides what an operator
// can inspect. It was missing dismissed, stable, needs_clarification and
// needs_manual_review — states the engine produces — so those reflections could
// not be queried at all.
func TestValidReflectionStatusesCoversTheLifecycle(t *testing.T) {
	// Every status written by pkg/cognitive for a reflection.
	produced := []string{
		"unresolved",          // created by the contradiction detector
		"dismissed",           // gate B3: action_required=false
		"stable",              // confidence above threshold
		"resolved",            // consolidated
		"needs_clarification", // LLM says it needs a question answered
		"needs_manual_review", // escalated by the consolidation step
		// produced by the other detectors
		"insight",
		"active",
		"high_confidence",
	}

	for _, status := range produced {
		if !validReflectionStatuses[status] {
			t.Errorf("status %q is written by the Gardener but rejected by the API filter, "+
				"so those reflections cannot be inspected", status)
		}
	}
}
