package cognitive

import (
	"fmt"
	"log/slog"
	"time"
)

// Deterministic auto-resolution.
//
// This is the fast path that ran before the epistemic engine replaced it in
// fc57015, brought back alongside it rather than instead of it. The two designs
// solve different problems:
//
//   - A knowledge gap is a reflection that already knows its own answer: two
//     entities the detectors found to be similar but unlinked. Closing it needs
//     a graph write, not a judgement call. Above the configured confidence the
//     old resolver created the link and marked the reflection resolved, with no
//     LLM involved. The epistemic path has no equivalent, so after fc57015 those
//     reflections were never resolved at all — they stayed "insight" forever.
//
//   - A minor contradiction (the detector already answered action_required=false)
//     carries a suggested resolution. Recording it costs nothing; sending it to
//     an LLM to be told what the detector already said does not.
//
// Everything else — reflections that need a real judgement — still goes through
// resolveVolatileBeliefs. This function only handles the cases where the answer
// is already known.

// autoResolveDeterministic applies the configured deterministic resolutions to
// the given reflections and returns the IDs it settled.
//
// A reflection it settles is removed from the epistemic queue by its new status,
// so the two paths never work on the same node.
func (g *Gardener) autoResolveDeterministic(indexName string, refIDs []string) []string {
	if !g.cfg.AutoResolveEnabled {
		return nil
	}
	if !g.cfg.AutoResolveLinks && !g.cfg.AutoResolveContra {
		return nil
	}

	var settled []string
	for _, refID := range refIDs {
		if g.tryAutoResolveLink(indexName, refID) {
			settled = append(settled, refID)
			continue
		}
		if g.tryAutoResolveMinorContradiction(indexName, refID) {
			settled = append(settled, refID)
		}
	}
	return settled
}

// tryAutoResolveLink closes a knowledge gap: the reflection points at two
// entities that are similar but not linked, so the link is written.
//
// Returns true when the reflection was settled (linked now, or found already
// linked by a previous run).
func (g *Gardener) tryAutoResolveLink(indexName, refID string) bool {
	if !g.cfg.AutoResolveLinks {
		return false
	}

	entities, found := g.eng.VGetLinks(indexName, refID, "suggests_link")
	if !found || len(entities) != 2 {
		return false
	}

	refData, err := g.eng.VGet(indexName, refID)
	if err != nil {
		return false
	}
	confidence, _ := refData.Metadata["confidence"].(float64)

	minConfidence := g.cfg.AutoResolveLinksMin
	if minConfidence <= 0 {
		minConfidence = 0.90
	}
	if confidence < minConfidence {
		return false
	}

	source, target := entities[0], entities[1]

	// Dedup: an earlier cycle, or the detector itself, may have created the link.
	if existing, ok := g.eng.VGetLinks(indexName, source, "related_to"); ok {
		for _, e := range existing {
			if e == target {
				// The link exists: settle the reflection instead of leaving it to
				// be re-examined forever.
				g.markAutoResolved(indexName, refID,
					fmt.Sprintf("auto-resolved: link %s -> %s already existed", source, target))
				return true
			}
		}
	}

	if err := g.eng.VLink(indexName, source, target, "related_to", "related_to",
		float32(confidence), map[string]any{"auto_resolved_from": refID}); err != nil {
		slog.Warn("[Gardener] Auto-resolve: failed to create link",
			"reflection", refID, "from", source, "to", target, "error", err)
		return false
	}

	g.markAutoResolved(indexName, refID,
		fmt.Sprintf("auto-resolved: link created based on semantic similarity %.2f", confidence))
	slog.Info("[Gardener] Auto-resolved: created suggested link",
		"from", source, "to", target, "confidence", confidence, "reflection", refID)
	return true
}

// tryAutoResolveMinorContradiction records the resolution the detector already
// produced for a contradiction it flagged as not needing action.
func (g *Gardener) tryAutoResolveMinorContradiction(indexName, refID string) bool {
	if !g.cfg.AutoResolveContra {
		return false
	}

	refData, err := g.eng.VGet(indexName, refID)
	if err != nil {
		return false
	}

	// Only for reflections that exist because a contradiction was detected.
	// Knowledge gaps and the other detectors do not set action_required.
	if _, isContradiction := refData.Metadata["suggested_resolution"]; !isContradiction {
		return false
	}
	if actionRequired, ok := refData.Metadata["action_required"].(bool); !ok || actionRequired {
		return false
	}

	suggested, _ := refData.Metadata["suggested_resolution"].(string)
	if suggested == "" {
		return false
	}

	g.markAutoResolved(indexName, refID, suggested)
	slog.Info("[Gardener] Auto-resolved: minor contradiction",
		"reflection", refID, "resolution", suggested)
	return true
}

// markAutoResolved records a deterministic resolution.
func (g *Gardener) markAutoResolved(indexName, refID, resolution string) {
	if err := g.eng.VSetMetadata(indexName, refID, map[string]any{
		"status":        "resolved",
		"resolution":    resolution,
		"auto_resolved": true,
		"_updated_at":   float64(time.Now().Unix()),
	}); err != nil {
		slog.Warn("[Gardener] Auto-resolve: failed to mark reflection resolved",
			"reflection", refID, "error", err)
	}
}

// autoResolveCandidates returns the reflections the fast path may act on:
// unresolved ones plus the insight ones the detectors produce for knowledge
// gaps. The epistemic path only looks at "unresolved", so without this the
// insight reflections were never examined by anyone.
func (g *Gardener) autoResolveCandidates(indexName string, limit int) []string {
	if !g.cfg.AutoResolveEnabled || (!g.cfg.AutoResolveLinks && !g.cfg.AutoResolveContra) {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	refs, err := g.eng.VFilter(indexName,
		"(type='reflection' AND status='unresolved') OR (type='reflection' AND status='insight')",
		limit)
	if err != nil {
		slog.Warn("[Gardener] Auto-resolve: could not list candidate reflections",
			"index", indexName, "error", err)
		return nil
	}
	return refs
}
