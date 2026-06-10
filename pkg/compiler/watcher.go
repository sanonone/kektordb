package compiler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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

	targetIndexes         []string
	stalenessThreshold    float64
	maxRecompilePerCycle  int

	recompileThisCycle int // reset per ScanArtifacts call
}

const (
	defaultStalenessThreshold = 1.0
	defaultMaxRecompilePerCycle = 3
	stalenessIncrementOnChange = 0.3
	stalenessDecayPerHour      = 0.05
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
		stalenessThreshold:   defaultStalenessThreshold,
		maxRecompilePerCycle: defaultMaxRecompilePerCycle,
	}

	// Resolve target indexes
	if len(targetIndexes) == 1 && targetIndexes[0] == "*" {
		all := eng.ListIndexes()
		for _, idx := range all {
			info, err := eng.DB.GetSingleVectorIndexInfoAPI(idx)
			if err != nil || info.VectorCount < 10 {
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

	slog.Info("ArtifactWatcher initialized",
		"target_indexes", w.targetIndexes,
		"staleness_threshold", w.stalenessThreshold,
	)
	return w
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

// ScanArtifacts loads artifacts from the graph, checks staleness
// thresholds, and triggers recompilation. Called by the Gardener.
func (w *Watcher) ScanArtifacts() {
	w.recompileThisCycle = 0

	w.mu.Lock()
	defer w.mu.Unlock()

	// Load all non-historical artifacts from the graph
	if err := w.loadArtifacts(); err != nil {
		slog.Warn("ArtifactWatcher: failed to load artifacts", "error", err)
		return
	}

	for key, a := range w.tracked {
		// Update importance based on access patterns
		w.updateImportance(a)

		// Calculate dynamic threshold
		threshold := w.getStalenessThreshold(a)

		// Apply time-based staleness decay
		hoursSinceCompile := time.Since(a.CompiledAt).Hours()
		a.StalenessScore += hoursSinceCompile * stalenessDecayPerHour

		// Trigger recompilation if threshold exceeded
		if a.StalenessScore >= threshold && w.recompileThisCycle < w.maxRecompilePerCycle {
			slog.Info("ArtifactWatcher: recompiling stale artifact",
				"artifact", key,
				"staleness", a.StalenessScore,
				"threshold", threshold,
			)
			w.recompile(a)
		}
	}

	// Lifecycle management: prune old versions
	w.manageLifecycle()
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

			// Skip if already tracked
			if _, exists := w.tracked[key]; exists {
				continue
			}

			// Extract version
			version := 1
			if v, ok := data.Metadata["version"].(float64); ok {
				version = int(v)
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

			// Extract compiled_at
			var compiledAt time.Time
			if ca, ok := data.Metadata["_created_at"].(float64); ok {
				compiledAt = time.Unix(int64(ca), int64((ca-float64(int64(ca)))*1e9))
			}

			wa := &watchedArtifact{
				Name:           name,
				EntityType:     entityType,
				EntityID:       entityID,
				Version:        version,
				SourceNodeIDs:  sourceIDs,
				CompiledAt:     compiledAt,
				StalenessScore: staleness,
				FieldStaleness:  make(map[string]float64),
				RefreshPolicy: RefreshPolicy{
					KeepHistory:    true,
					MaxVersions:    20,
					PruneAfterDays: 90,
				},
				IndexName: idx,
			}

			// Use stored refresh policy if available
			if taskStr, ok := data.Metadata["task_spec"].(string); ok && taskStr != "" {
				var taskSpec TaskSpec
				if json.Unmarshal([]byte(taskStr), &taskSpec) == nil {
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

// recompile triggers a full recompilation of the artifact.
func (w *Watcher) recompile(a *watchedArtifact) {
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
		return
	}

	a.StalenessScore = 0
	a.FieldStaleness = make(map[string]float64)
	a.LastRecompiledAt = time.Now()
	a.RecompileCount++
	w.recompileThisCycle++

	slog.Info("ArtifactWatcher: artifact recompiled",
		"artifact", a.Name,
		"entity", fmt.Sprintf("%s:%s", a.EntityType, a.EntityID),
		"version", a.Version,
	)
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
			"_archived":  true,
			"_pinned":    false,
		})

		// Remove from tracking
		delete(w.tracked, key)
	}
}
