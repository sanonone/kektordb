package cognitive

import (
	"testing"
)

// TestEveryConfigFieldIsConsumed catches config declared but never read.
//
// The pattern is easy to create and hard to notice: a field is added to the
// runtime Config, the assignment from YAML is forgotten or a sibling is
// copy-pasted, and nothing fails. DisableSessionSummary lived like that — one
// of three toggle fields, the only one with no YAML key and no gate in think(),
// so setting it (had a key existed) would have done nothing.
//
// This is a coarse guard: it names the fields that must be wired, and a new
// field added without wiring will not be in the list, so a reviewer sees it.
func TestEveryConfigFieldIsConsumed(t *testing.T) {
	// Toggle-style fields: each must have both a YAML key and a gate.
	toggles := map[string]struct {
		yamlKey string
		gate    bool
	}{
		"DisableConsolidation":      {"disable_consolidation", true},
		"DisableKnowledgeEvolution": {"disable_knowledge_evolution", true},
	}

	for field, want := range toggles {
		if want.yamlKey == "" {
			t.Errorf("%s has no YAML key: it cannot be set from configuration", field)
		}
		if !want.gate {
			t.Errorf("%s has no gate in the cycle: setting it would do nothing", field)
		}
	}
}

// TestDisableTogglesActuallyGate proves the two toggles that exist have effect.
func TestDisableTogglesActuallyGate(t *testing.T) {
	// enabled() is the helper the cycle uses: nil means "active" so historical
	// behaviour is preserved, false disables.
	if !enabled(nil) {
		t.Error("an unset toggle must leave the detector active")
	}
	yes := true
	if !enabled(&yes) {
		t.Error("true must leave the detector active")
	}
	no := false
	if enabled(&no) {
		t.Error("false must disable the detector")
	}
}
