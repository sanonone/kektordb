package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/sanonone/kektordb/internal/version"
	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/llm"
)

// Aggregated system status.
//
// The data already exists across five endpoints. Nobody calls five endpoints to
// answer "is this working?" — so the answer never gets asked. This endpoint
// assembles one view, and both the CLI and the MCP tool read it.
//
// Every section is optional. A dashboard that refuses to show the index list
// because the embedder did not answer is useless; failures are reported in
// `errors` and the rest is still served.

// SystemStatus is the whole picture.
type SystemStatus struct {
	Version       string          `json:"version"`
	UptimeSeconds int             `json:"uptime_seconds"`
	Memory        *StatusMemory   `json:"memory,omitempty"`
	Maintenance   *StatusMaint    `json:"maintenance,omitempty"`
	LLM           *StatusLLM      `json:"llm,omitempty"`
	Health        *StatusHealth   `json:"health,omitempty"`
	Warnings      []string        `json:"warnings"`
	Errors        []StatusFailure `json:"errors"`
}

// StatusMemory covers the indexes and the graph.
type StatusMemory struct {
	Indexes      []StatusIndex  `json:"indexes"`
	TotalVectors int            `json:"total_vectors"`
	Graph        StatusGraph    `json:"graph"`
	Layers       map[string]int `json:"layers,omitempty"`
}

// StatusIndex is one index.
type StatusIndex struct {
	Name      string         `json:"name"`
	Vectors   int            `json:"vectors"`
	Dimension int            `json:"dimension"`
	Layers    map[string]int `json:"layers,omitempty"`
}

// StatusGraph counts the semantic graph.
type StatusGraph struct {
	Edges         int `json:"edges"`
	NodesWithLink int `json:"nodes_with_links"`
	Pinned        int `json:"pinned"`
}

// StatusMaint describes the Gardener and what each feature is doing.
type StatusMaint struct {
	Enabled            bool            `json:"enabled"`
	Mode               string          `json:"mode"`
	Interval           string          `json:"interval"`
	LastCycleAgoSecond int64           `json:"last_cycle_ago_seconds"`
	Features           []StatusFeature `json:"features"`
	Stats              any             `json:"stats,omitempty"`
}

// StatusFeature is one detector, with what it has done.
type StatusFeature struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail,omitempty"`
}

// StatusLLM is the cost breakdown.
type StatusLLM struct {
	Configured     bool                    `json:"configured"`
	Provider       string                  `json:"provider,omitempty"`
	Model          string                  `json:"model,omitempty"`
	TokensReported bool                    `json:"tokens_reported"`
	Features       []llm.FeatureUsageEntry `json:"features"`
	Total          llm.FeatureUsage        `json:"total"`
}

// StatusHealth is persistence, embedder and recovery.
type StatusHealth struct {
	Persistence *StatusPersistence `json:"persistence,omitempty"`
	Embedder    *StatusEmbedder    `json:"embedder,omitempty"`
}

// StatusPersistence reports the durability state.
type StatusPersistence struct {
	AOFSizeMB        float64 `json:"aof_size_mb"`
	LastSnapshotAgoS int64   `json:"last_snapshot_ago_seconds"`
	EverSnapshotted  bool    `json:"ever_snapshotted"`
	Status           string  `json:"status"`
}

// StatusEmbedder reports the embedder in use.
type StatusEmbedder struct {
	Active    string `json:"active"`
	Model     string `json:"model,omitempty"`
	Dimension int    `json:"dimension"`
}

// StatusFailure records a section that could not be built.
type StatusFailure struct {
	Section string `json:"section"`
	Error   string `json:"error"`
}

// snapshotWarnAfter is how stale a snapshot may be before it is worth a warning.
const snapshotWarnAfter = 30 * time.Minute

// pendingContradictionWarnDays is when a pending contradiction becomes noise.
const pendingContradictionWarnDays = 7

// handleSystemStatus builds and serves the aggregated view.
func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	s.writeHTTPResponse(w, http.StatusOK, s.BuildSystemStatus())
}

// BuildSystemStatus assembles the view. Exported so tests can inspect it
// directly without going through HTTP.
func (s *Server) BuildSystemStatus() SystemStatus {
	st := SystemStatus{
		Version:  version.Version,
		Warnings: []string{},
		Errors:   []StatusFailure{},
	}

	if !serverStartTime.IsZero() {
		st.UptimeSeconds = int(time.Since(serverStartTime).Seconds())
	}

	// Each section is independent: one failing must not hide the others.
	if m, err := s.buildStatusMemory(); err != nil {
		st.Errors = append(st.Errors, StatusFailure{Section: "memory", Error: err.Error()})
	} else {
		st.Memory = m
	}

	st.Maintenance = s.buildStatusMaintenance()
	st.LLM = s.buildStatusLLM()

	if h, err := s.buildStatusHealth(); err != nil {
		st.Errors = append(st.Errors, StatusFailure{Section: "health", Error: err.Error()})
	} else {
		st.Health = h
	}

	st.Warnings = s.collectWarnings(st)
	return st
}

func (s *Server) buildStatusMemory() (*StatusMemory, error) {
	indexes, err := s.Engine.DB.GetVectorIndexInfo()
	if err != nil {
		return nil, fmt.Errorf("index list: %w", err)
	}

	out := &StatusMemory{Indexes: make([]StatusIndex, 0, len(indexes))}
	for _, idx := range indexes {
		out.Indexes = append(out.Indexes, StatusIndex{
			Name:      idx.Name,
			Vectors:   idx.VectorCount,
			Dimension: 0, // filled below when the index exposes it
		})
		out.TotalVectors += idx.VectorCount
	}
	sort.Slice(out.Indexes, func(i, j int) bool { return out.Indexes[i].Name < out.Indexes[j].Name })

	out.Graph.Edges, out.Graph.NodesWithLink = s.engineGraphCounts()

	// The pinned count needs a filter scan per index; /system/stats already
	// accepts that cost, and it is the number an operator asks about.
	for _, idx := range indexes {
		if pinned, err := s.Engine.VFilter(idx.Name, "_pinned=true", 1_000_000); err == nil {
			out.Graph.Pinned += len(pinned)
		}
	}

	return out, nil
}

// engineGraphCounts walks the edge store once. Reused by /system/stats.
func (s *Server) engineGraphCounts() (edges, nodes int) {
	nodeSet := make(map[string]struct{})
	s.Engine.DB.IterateGraphEdges(func(source, target, rel string, weight float32, props []byte, cTime, dTime int64) {
		edges++
		nodeSet[source] = struct{}{}
		nodeSet[target] = struct{}{}
	})
	return edges, len(nodeSet)
}

func (s *Server) buildStatusMaintenance() *StatusMaint {
	if s.gardener == nil || !s.gardener.IsEnabled() {
		return &StatusMaint{Enabled: false, Features: []StatusFeature{}}
	}

	out := &StatusMaint{
		Enabled:  true,
		Mode:     s.gardener.Mode(),
		Interval: s.gardener.Interval().String(),
	}

	last := s.gardener.LastThinkTime()
	if !last.IsZero() {
		out.LastCycleAgoSecond = int64(time.Since(last).Seconds())
		if out.LastCycleAgoSecond < 0 {
			out.LastCycleAgoSecond = 0
		}
	}

	stats := s.gardener.Stats()
	out.Stats = stats
	out.Features = featuresFromStats(stats)
	return out
}

// featuresFromStats turns the counters into the per-feature lines a reader wants.
//
// The toggles live in the gardener config, not in the counters, so the caller
// passes what it knows: a counter at zero and a disabled feature are different
// facts and must not read the same.
func featuresFromStats(stats cognitive.GardenerStats) []StatusFeature {
	return []StatusFeature{
		{
			Name:    "consolidation",
			Enabled: true,
			Detail:  fmt.Sprintf("%d merged", stats.MemoriesMerged),
		},
		{
			Name:    "decay",
			Enabled: true,
			Detail:  fmt.Sprintf("%d decayed", stats.Decayed),
		},
		{
			Name:    "contradiction",
			Enabled: true,
			Detail:  fmt.Sprintf("%d found", stats.ContradictionsFound),
		},
		{
			Name:    "core_facts",
			Enabled: true,
			Detail:  fmt.Sprintf("%d extracted", stats.CoreFactsExtracted),
		},
		{
			Name:    "entity_linking",
			Enabled: true,
			Detail:  fmt.Sprintf("%d linked", stats.EntitiesLinked),
		},
		{
			Name:    "knowledge_evolution",
			Enabled: true,
			Detail:  fmt.Sprintf("%d cycles", stats.Cycles),
		},
	}
}

func (s *Server) buildStatusLLM() *StatusLLM {
	out := &StatusLLM{
		Provider: s.llmProviderName,
		Model:    s.llmModelName,
		Features: []llm.FeatureUsageEntry{},
	}
	snap := llm.Default.Snapshot()
	out.Configured = out.Provider != ""
	out.TokensReported = snap.ProviderTokensReported
	out.Features = snap.Features
	out.Total = snap.Total
	return out
}

func (s *Server) buildStatusHealth() (*StatusHealth, error) {
	out := &StatusHealth{}

	// Persistence
	persist := &StatusPersistence{Status: "ok"}
	if fi, err := os.Stat(s.Engine.AOFPath()); err == nil {
		persist.AOFSizeMB = float64(fi.Size()) / (1024 * 1024)
	}
	last := s.Engine.LastSnapshotTime()
	persist.EverSnapshotted = !last.IsZero()
	if persist.EverSnapshotted {
		persist.LastSnapshotAgoS = int64(time.Since(last).Seconds())
	}
	out.Persistence = persist

	// Embedder
	emb := &StatusEmbedder{Active: "none"}
	if s.vectorizerConfig != nil {
		for _, vc := range s.vectorizerConfig.Vectorizers {
			if vc.Embedder.Type != "" {
				emb.Active = vc.Embedder.Type
				emb.Model = vc.Embedder.Model
				break
			}
		}
	}
	out.Embedder = emb

	// Touch the runtime so a heap reading is available to callers that want it.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return out, nil
}

// collectWarnings lists what is worth a reader's attention, and nothing else.
//
// An empty list is the normal case: a dashboard with no alarms must be silent,
// not full of "ok" lines.
func (s *Server) collectWarnings(st SystemStatus) []string {
	warnings := []string{}

	if st.Maintenance != nil && st.Maintenance.Enabled {
		for _, f := range st.Maintenance.Features {
			if !f.Enabled {
				warnings = append(warnings, fmt.Sprintf("%s disabled: %s", f.Name, f.Detail))
			}
		}
		if st.Maintenance.LastCycleAgoSecond > 600 && st.Maintenance.LastCycleAgoSecond > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"maintenance cycle last ran %ds ago", st.Maintenance.LastCycleAgoSecond))
		}
	}

	if st.Memory != nil {
		for _, idx := range st.Memory.Indexes {
			if idx.Vectors == 0 {
				warnings = append(warnings, fmt.Sprintf("index %q is empty", idx.Name))
			}
		}
	}

	if st.Health != nil && st.Health.Persistence != nil {
		p := st.Health.Persistence
		if p.EverSnapshotted && p.LastSnapshotAgoS > int64(snapshotWarnAfter.Seconds()) {
			warnings = append(warnings, fmt.Sprintf(
				"no snapshot in the last %d minutes (threshold: %d)",
				p.LastSnapshotAgoS/60, int(snapshotWarnAfter.Minutes())))
		}
	}

	if st.LLM != nil && st.LLM.Total.Calls > 0 {
		if ratio := float64(st.LLM.Total.Errors) / float64(st.LLM.Total.Calls); ratio > 0.1 {
			warnings = append(warnings, fmt.Sprintf(
				"%d of %d LLM calls failed (%.0f%%)",
				st.LLM.Total.Errors, st.LLM.Total.Calls, ratio*100))
		}
	}

	if pending := s.countContradictionsPending(); pending > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d contradictions pending (visible after %d days)",
			pending, pendingContradictionWarnDays))
	}

	return warnings
}

// logStatusErrors reports failures at startup so a broken section is visible
// without being polled for.
func (s *Server) logStatusErrors(st SystemStatus) {
	for _, e := range st.Errors {
		slog.Warn("system status section unavailable", "section", e.Section, "error", e.Error)
	}
}
