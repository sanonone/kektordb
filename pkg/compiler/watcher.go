package compiler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/engine"
)

// WatchedArtifact tracks the staleness and lifecycle of a compiled artifact.
type watchedArtifact struct {
	Name          string
	EntityType    string
	EntityID      string
	Version       int
	SourceNodeIDs []string
	CompiledAt    time.Time

	StalenessScore float64
	FieldStaleness map[string]float64

	UsageCount      int64
	LastAccessedAt  time.Time
	ImportanceScore float64

	LastRecompiledAt time.Time
	RecompileCount   int

	RefreshPolicy RefreshPolicy
	IndexName     string
}

// Watcher monitors compiled artifacts and triggers recompilation
// when source nodes change. Integrated into the Gardener's lifecycle.
type Watcher struct {
	compiler *Compiler
	eng      *engine.Engine

	tracked map[string]*watchedArtifact // key: "index:name:entity_type:entity_id"
	mu      sync.RWMutex

	targetIndexes        []string
	stalenessThreshold   float64
	maxRecompilePerCycle int

	// inFlight tracks artifacts currently being recompiled in background
	// goroutines, so concurrent scans never select them again.
	inFlight map[string]bool

	recompileThisCycle int // reset per ScanArtifacts call
}

const (
	defaultStalenessThreshold   = 1.0
	defaultMaxRecompilePerCycle = 3
	stalenessIncrementOnChange  = 0.3
	stalenessDecayPerHour       = 0.05
)

// NewWatcher creates a new Artifact Watcher and registers it
// with the Gardener config via callback functions.
// targetIndexes specifies which indexes to scan for artifacts.
// Use ["*"] to auto-discover all indexes (with vector count >= 10).
// Use ["mcp_memory"] for the default single-index mode.
func NewWatcher(comp *Compiler, eng *engine.Engine, cfg *cognitive.Config, targetIndexes []string) *Watcher {
	w := &Watcher{
		compiler:             comp,
		eng:                  eng,
		tracked:              make(map[string]*watchedArtifact),
		inFlight:             make(map[string]bool),
		stalenessThreshold:   defaultStalenessThreshold,
		maxRecompilePerCycle: defaultMaxRecompilePerCycle,
	}

	// Resolve target indexes
	if len(targetIndexes) == 1 && targetIndexes[0] == "*" {
		all := eng.ListIndexes()
		for _, idx := range all {
			info, err := eng.DB.GetSingleVectorIndexInfoAPI(idx)
			if err != nil || info.VectorCount < 10 {
				slog.Debug("ArtifactWatcher: skipping index (below min vectors for auto-discover)",
					"index", idx,
					"vectors", info.VectorCount,
				)
				continue
			}
			w.targetIndexes = append(w.targetIndexes, idx)
		}
	} else {
		if len(targetIndexes) == 0 {
			w.targetIndexes = []string{"mcp_memory"}
		} else {
			w.targetIndexes = append([]string(nil), targetIndexes...)
		}
	}

	// Register callbacks with Gardener
	cfg.ArtifactScan = w.ScanArtifacts
	cfg.ArtifactEvent = w.OnEvent

	if len(w.targetIndexes) == 0 {
		slog.Warn("ArtifactWatcher initialized with NO target indexes",
			"requested", targetIndexes,
			"hint", "no indexes have enough vectors yet (min 10 for auto-discover). Watcher will scan mcp_memory once it has artifacts.",
		)
		// Still set up the default so the watcher becomes active when mcp_memory is created
		w.targetIndexes = []string{"mcp_memory"}
	}

	slog.Info("ArtifactWatcher initialized",
		"target_indexes", w.targetIndexes,
		"staleness_threshold", w.stalenessThreshold,
	)
	return w
}

// acceptsEventType reports whether an engine event should count towards the
// artifact's staleness, according to its refresh policy. An empty RecompileOn
// list accepts everything (legacy behavior); otherwise only the configured
// event kinds are tracked (B3).
func (a *watchedArtifact) acceptsEventType(eventType engine.EventType) bool {
	if len(a.RefreshPolicy.RecompileOn) == 0 {
		return true
	}
	for _, trigger := range a.RefreshPolicy.RecompileOn {
		switch trigger {
		case "entity_update":
			if eventType == engine.EventVectorAdd || eventType == engine.EventVectorUpdate {
				return true
			}
		case "new_relationship":
			if eventType == engine.EventEdgeCreate {
				return true
			}
		}
	}
	return false
}

// OnEvent handles engine write events. If a changed node is a source
// of a tracked artifact, increments its staleness score.
// Events are filtered by index: only artifacts in the event's index are checked.
//
// FIX (bugs #3.2, #5.4): Uses a two-phase approach to avoid the reentrant
// RLock→Unlock→Lock→Unlock→RLock lock dance. Phase 1 collects keys under
// RLock (read-only), Phase 2 applies updates under Lock (write-only).
// This eliminates the deferred RUnlock panic (#3.2) and the concurrent
// map iteration panic (#5.4).
func (w *Watcher) OnEvent(event engine.Event) {
	if event.ID == "" {
		return
	}

	// Phase 1: Collect artifact keys that need updating (under RLock)
	w.mu.RLock()
	var toUpdate []string
	for key, a := range w.tracked {
		if event.IndexName != "" && event.IndexName != a.IndexName {
			continue
		}
		if !a.acceptsEventType(event.Type) {
			continue
		}
		for _, srcID := range a.SourceNodeIDs {
			if srcID == event.ID || event.TargetID == srcID {
				toUpdate = append(toUpdate, key)
				break
			}
		}
	}
	w.mu.RUnlock()

	if len(toUpdate) == 0 {
		return
	}

	// Phase 2: Apply staleness updates (under Lock)
	w.mu.Lock()
	for _, key := range toUpdate {
		a, ok := w.tracked[key]
		if !ok {
			continue
		}
		a.StalenessScore += stalenessIncrementOnChange
		for field := range a.FieldStaleness {
			a.FieldStaleness[field] += stalenessIncrementOnChange
		}
		slog.Debug("ArtifactWatcher: source changed, staleness updated",
			"artifact", key,
			"staleness", a.StalenessScore,
		)
	}
	w.mu.Unlock()
}

// decayBase returns the timestamp the time-based staleness decay is measured
// from: the last recompilation when available, otherwise the original compile
// time. Using LastRecompiledAt prevents stale artifacts from accumulating
// decay forever and being recompiled on every cycle (B2 fix).
func (a *watchedArtifact) decayBase() time.Time {
	if !a.LastRecompiledAt.IsZero() {
		return a.LastRecompiledAt
	}
	return a.CompiledAt
}

// ScanArtifacts loads artifacts from the graph, checks staleness
// thresholds, and triggers recompilation. Called by the Gardener.
//
// Two-phase design (B1 fix): the watcher lock is only held for the decision
// phase (load + staleness accounting + candidate selection). The actual
// recompilations — which make LLM calls and take seconds — run in background
// goroutines WITHOUT the lock, so OnEvent and the Gardener think() cycle are
// never blocked by them.
func (w *Watcher) ScanArtifacts() {
	w.recompileThisCycle = 0

	// Phase A: under lock — load, decay, and select recompile candidates.
	w.mu.Lock()
	if err := w.loadArtifacts(); err != nil {
		slog.Warn("ArtifactWatcher: failed to load artifacts", "error", err)
	}

	var toRecompile []*watchedArtifact
	for key, a := range w.tracked {
		if w.inFlight[key] {
			continue
		}

		// Update importance based on access patterns
		w.updateImportance(a)

		// Calculate dynamic threshold
		threshold := w.getStalenessThreshold(a)

		// Apply time-based staleness decay since the last recompile/compile.
		hoursSinceBase := time.Since(a.decayBase()).Hours()
		a.StalenessScore += hoursSinceBase * stalenessDecayPerHour

		// Trigger decision per refresh policy (B3):
		//   manual        → never auto-recompile
		//   scheduled     → recompile when age >= MaxStalenessH
		//   default       → staleness score over the dynamic threshold
		trigger := false
		switch a.RefreshPolicy.Mode {
		case RefreshModeManual:
			trigger = false
		case RefreshModeScheduled:
			if a.RefreshPolicy.MaxStalenessH > 0 {
				trigger = hoursSinceBase >= float64(a.RefreshPolicy.MaxStalenessH)
			}
		default:
			trigger = a.StalenessScore >= threshold
		}

		if trigger && w.recompileThisCycle < w.maxRecompilePerCycle {
			slog.Info("ArtifactWatcher: recompiling stale artifact",
				"artifact", key,
				"staleness", a.StalenessScore,
				"threshold", threshold,
				"mode", a.RefreshPolicy.Mode,
			)
			w.inFlight[key] = true
			w.recompileThisCycle++
			toRecompile = append(toRecompile, a)
		}
	}

	// Lifecycle management: prune old versions
	w.manageLifecycle()
	w.mu.Unlock()

	// Phase B: recompile outside the lock (LLM calls take seconds). The
	// per-artifact state update happens in Phase C when each compile ends.
	for _, a := range toRecompile {
		w.recompileAsync(a)
	}
}

// loadArtifacts scans all configured indexes for knowledge_artifact nodes
// and registers them for tracking.
func (w *Watcher) loadArtifacts() error {
	for _, idx := range w.targetIndexes {
		if !w.eng.IndexExists(idx) {
			continue
		}
		ids, err := w.eng.VFilter(idx, "type='knowledge_artifact'", 100000)
		if err != nil {
			continue
		}

		for _, id := range ids {
			data, err := w.eng.VGet(idx, id)
			if err != nil {
				continue
			}
			if hist, ok := data.Metadata["_is_historical"].(bool); ok && hist {
				continue
			}

			name, _ := data.Metadata["artifact_name"].(string)
			entityType, _ := data.Metadata["entity_type"].(string)
			entityID, _ := data.Metadata["entity_id"].(string)
			if name == "" || entityType == "" || entityID == "" {
				continue
			}

			key := fmt.Sprintf("%s:%s:%s:%s", idx, name, entityType, entityID)

			// Extract version
			version := 1
			if v, ok := data.Metadata["version"].(float64); ok {
				version = int(v)
			}

			// Extract compiled_at
			var compiledAt time.Time
			if ca, ok := data.Metadata["_created_at"].(float64); ok {
				compiledAt = time.Unix(int64(ca), int64((ca-float64(int64(ca)))*1e9))
			}

			// Already tracked: refresh version/compile time if the graph has
			// a newer version than we know about (e.g. recompiled through a
			// different path than the watcher). This keeps the staleness
			// decay base honest (B2). Also refresh the refresh policy when
			// the stored task_spec changed (B3).
			if existing, exists := w.tracked[key]; exists {
				if version > existing.Version {
					existing.Version = version
					if !compiledAt.IsZero() {
						existing.CompiledAt = compiledAt
						if existing.LastRecompiledAt.IsZero() {
							existing.LastRecompiledAt = compiledAt
						}
					}
				}
				if taskStr, ok := data.Metadata["task_spec"].(string); ok && taskStr != "" {
					var taskSpec TaskSpec
					if json.Unmarshal([]byte(taskStr), &taskSpec) == nil && !reflect.DeepEqual(taskSpec.RefreshPolicy, existing.RefreshPolicy) {
						existing.RefreshPolicy = taskSpec.RefreshPolicy
					}
				}
				continue
			}

			// Extract source node IDs from compiled_from edges
			edges, _ := w.eng.VGetEdges(idx, id, "compiled_from", 0)
			sourceIDs := make([]string, 0, len(edges))
			for _, e := range edges {
				sourceIDs = append(sourceIDs, e.TargetID)
			}

			// Extract staleness score
			staleness := 0.0
			if s, ok := data.Metadata["staleness_score"].(float64); ok {
				staleness = s
			}

			wa := &watchedArtifact{
				Name:           name,
				EntityType:     entityType,
				EntityID:       entityID,
				Version:        version,
				SourceNodeIDs:  sourceIDs,
				CompiledAt:     compiledAt,
				StalenessScore: staleness,
				FieldStaleness: make(map[string]float64),
				RefreshPolicy:  DefaultRefreshPolicy(),
				IndexName:      idx,
			}

			// Use stored refresh policy if available (B3: only override the
			// built-in default when the policy is explicitly set).
			if taskStr, ok := data.Metadata["task_spec"].(string); ok && taskStr != "" {
				var taskSpec TaskSpec
				if json.Unmarshal([]byte(taskStr), &taskSpec) == nil && !IsZeroPolicy(taskSpec.RefreshPolicy) {
					wa.RefreshPolicy = taskSpec.RefreshPolicy
				}
			}

			w.tracked[key] = wa
		}
	}

	return nil
}

// updateImportance computes the importance score for an artifact
// based on access count and recency.
func (w *Watcher) updateImportance(a *watchedArtifact) {
	// Try to read access metrics from the graph node
	nodeID := artifactNodeID(&Artifact{
		Name: a.Name, EntityType: a.EntityType, EntityID: a.EntityID,
		Version: a.Version,
	})

	data, err := w.eng.VGet(a.IndexName, nodeID)
	if err == nil {
		if ac, ok := data.Metadata["_access_count"].(float64); ok {
			a.UsageCount = int64(ac)
		}
		if la, ok := data.Metadata["_last_accessed"].(float64); ok {
			a.LastAccessedAt = time.Unix(int64(la), 0)
		}
	}

	recencyHours := time.Since(a.LastAccessedAt).Hours()
	recencyWeight := math.Exp(-recencyHours / 168.0) // 7-day half-life

	score := float64(a.UsageCount) * recencyWeight * 0.3

	// Pinned bonus
	if len(a.SourceNodeIDs) > 5 {
		score += 1.0
	}

	a.ImportanceScore = math.Min(score, 10.0)
}

// getStalenessThreshold returns a dynamic threshold based on importance.
// More important artifacts are recompiled more eagerly (lower threshold).
func (w *Watcher) getStalenessThreshold(a *watchedArtifact) float64 {
	base := w.stalenessThreshold

	if a.ImportanceScore > 8.0 {
		return base * 0.5
	}
	if a.ImportanceScore > 5.0 {
		return base * 0.7
	}
	if a.UsageCount < 10 {
		return base * 2.0
	}

	return base
}

// recompileAsync launches the recompilation in a background goroutine so the
// watcher lock and the Gardener think() cycle are never blocked by LLM calls.
func (w *Watcher) recompileAsync(a *watchedArtifact) {
	go w.recompile(a)
}

// recompile triggers a full recompilation of the artifact.
// Phase C: on completion, the tracked state is updated under the watcher lock
// and the in-flight guard is released.
func (w *Watcher) recompile(a *watchedArtifact) {
	key := fmt.Sprintf("%s:%s:%s:%s", a.IndexName, a.Name, a.EntityType, a.EntityID)

	req := CompileRequest{
		Name:     a.Name,
		Template: a.Name,
		Sources: SourceSpec{
			Type:   "graph_query",
			Entity: EntityRef{Type: a.EntityType, ID: a.EntityID},
			Depth:  2,
		},
		IndexName: a.IndexName,
	}

	_, err := w.compiler.Compile(req)
	if err != nil {
		slog.Warn("ArtifactWatcher: recompile failed",
			"artifact", a.Name,
			"entity", fmt.Sprintf("%s:%s", a.EntityType, a.EntityID),
			"error", err,
		)
	}

	// Phase C: state update under lock.
	w.mu.Lock()
	defer w.mu.Unlock()

	cur, ok := w.tracked[key]
	if ok {
		delete(w.inFlight, key)
		if err == nil {
			// Reset staleness and refresh the decay base (B2): the next
			// cycle measures decay from now, not from the original compile.
			cur.StalenessScore = 0
			cur.FieldStaleness = make(map[string]float64)
			cur.LastRecompiledAt = time.Now()
			cur.CompiledAt = cur.LastRecompiledAt
			cur.RecompileCount++
			cur.Version++

			slog.Info("ArtifactWatcher: artifact recompiled",
				"artifact", a.Name,
				"entity", fmt.Sprintf("%s:%s", a.EntityType, a.EntityID),
				"version", cur.Version,
			)
		}
		// On compile error the staleness stays high so the next cycle retries.
	} else {
		// Artifact was archived while recompiling — just release the guard.
		delete(w.inFlight, key)
	}
}

// manageLifecycle checks artifacts for lifecycle events:
// archiving if unused for >30 days.
func (w *Watcher) manageLifecycle() {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	for key, a := range w.tracked {
		// Skip if recently used
		if !a.LastAccessedAt.IsZero() && a.LastAccessedAt.After(cutoff) {
			continue
		}
		// Skip if no usage data at all
		if a.LastAccessedAt.IsZero() && time.Since(a.CompiledAt) < 30*24*time.Hour {
			continue
		}

		slog.Info("ArtifactWatcher: archiving unused artifact",
			"artifact", key,
			"last_accessed", a.LastAccessedAt,
			"compiled_at", a.CompiledAt,
		)

		// Mark as archived (soft-delete)
		nodeID := artifactNodeID(&Artifact{
			Name: a.Name, EntityType: a.EntityType, EntityID: a.EntityID,
			Version: a.Version,
		})
		_ = w.eng.VSetMetadata(a.IndexName, nodeID, map[string]any{
			"_archived": true,
			"_pinned":   false,
		})

		// Remove from tracking
		delete(w.tracked, key)
	}
}
