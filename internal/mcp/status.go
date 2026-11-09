package mcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sanonone/kektordb/pkg/llm"
)

// get_status: the memory dashboard, for the agent.
//
// A coding agent that is told "you have a memory" should be able to ask how it is
// doing. Without this tool it can save and recall but cannot report whether
// maintenance is running, what it costs, or what is broken — so when the user
// asks "why is memory slow?" the model has nothing to answer with.
//
// The shape is the same as /system/status on the HTTP side; the assembly lives
// here because the MCP server is a separate binary mode with its own wiring.

// GetStatusResult is the answer.
type GetStatusResult struct {
	Summary     string        `json:"summary"`
	Memory      *StatusMemory `json:"memory,omitempty"`
	Maintenance *StatusMaint  `json:"maintenance,omitempty"`
	LLM         *StatusLLM    `json:"llm,omitempty"`
	Health      *StatusHealth `json:"health,omitempty"`
	Warnings    []string      `json:"warnings"`
}

// StatusMemory is the index picture.
type StatusMemory struct {
	Indexes      []StatusIndex `json:"indexes"`
	TotalVectors int           `json:"total_vectors"`
	Pinned       int           `json:"pinned"`
}

// StatusIndex is one index.
type StatusIndex struct {
	Name    string         `json:"name"`
	Vectors int            `json:"vectors"`
	Layers  map[string]int `json:"layers,omitempty"`
}

// StatusMaint is what the Gardener is doing.
type StatusMaint struct {
	Enabled      bool            `json:"enabled"`
	Mode         string          `json:"mode,omitempty"`
	Interval     string          `json:"interval,omitempty"`
	LastCycleAgo string          `json:"last_cycle_ago,omitempty"`
	Features     []StatusFeature `json:"features,omitempty"`
}

// StatusFeature is one detector.
type StatusFeature struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail,omitempty"`
}

// StatusLLM is the cost breakdown.
type StatusLLM struct {
	Configured     bool                    `json:"configured"`
	TokensReported bool                    `json:"tokens_reported"`
	Features       []llm.FeatureUsageEntry `json:"features,omitempty"`
	TotalCalls     int64                   `json:"total_calls"`
}

// StatusHealth is persistence and embedder.
type StatusHealth struct {
	AOFSizeMB          string `json:"aof_size_mb"`
	LastSnapshotAgo    string `json:"last_snapshot_ago,omitempty"`
	EverSnapshotted    bool   `json:"ever_snapshotted"`
	EmbedderDimensions int    `json:"embedder_dimensions,omitempty"`
}

// GetStatusArgs takes no parameters: the answer is about the whole store. An
// index filter was considered and left out — the interesting question is "is the
// system healthy", and a per-index view is what get_stats already gives.
type GetStatusArgs struct{}

// GetStatus answers "is the memory working?".
func (s *Service) GetStatus(ctx context.Context, req *mcp.CallToolRequest, args GetStatusArgs) (*mcp.CallToolResult, GetStatusResult, error) {
	result := GetStatusResult{Warnings: []string{}}

	result.Memory = s.statusMemory()
	result.Maintenance = s.statusMaintenance()
	result.LLM = s.statusLLM()
	result.Health = s.statusHealth()

	result.Warnings = s.statusWarnings(result)
	result.Summary = statusSummary(result)

	return nil, result, nil
}

func (s *Service) statusMemory() *StatusMemory {
	indexes, err := s.engine.DB.GetVectorIndexInfo()
	if err != nil {
		return nil
	}

	out := &StatusMemory{Indexes: make([]StatusIndex, 0, len(indexes))}
	for _, idx := range indexes {
		out.Indexes = append(out.Indexes, StatusIndex{
			Name:    idx.Name,
			Vectors: idx.VectorCount,
		})
		out.TotalVectors += idx.VectorCount
		if pinned, err := s.engine.VFilter(idx.Name, "_pinned=true", 1_000_000); err == nil {
			out.Pinned += len(pinned)
		}
	}
	sort.Slice(out.Indexes, func(i, j int) bool { return out.Indexes[i].Name < out.Indexes[j].Name })
	return out
}

func (s *Service) statusMaintenance() *StatusMaint {
	if s.gardener == nil || !s.gardener.IsEnabled() {
		return &StatusMaint{Enabled: false}
	}

	out := &StatusMaint{
		Enabled:  true,
		Mode:     s.gardener.Mode(),
		Interval: s.gardener.Interval().String(),
	}
	if last := s.gardener.LastThinkTime(); !last.IsZero() {
		out.LastCycleAgo = relativeTime(time.Since(last))
	}

	st := s.gardener.Stats()
	out.Features = []StatusFeature{
		{Name: "consolidation", Enabled: true, Detail: fmt.Sprintf("%d merged", st.MemoriesMerged)},
		{Name: "decay", Enabled: true, Detail: fmt.Sprintf("%d decayed", st.Decayed)},
		{Name: "contradiction", Enabled: true, Detail: fmt.Sprintf("%d found", st.ContradictionsFound)},
		{Name: "core_facts", Enabled: true, Detail: fmt.Sprintf("%d extracted", st.CoreFactsExtracted)},
		{Name: "entity_linking", Enabled: true, Detail: fmt.Sprintf("%d linked", st.EntitiesLinked)},
	}
	return out
}

func (s *Service) statusLLM() *StatusLLM {
	snap := llm.Default.Snapshot()
	return &StatusLLM{
		Configured:     snap.Total.Calls > 0 || len(snap.Features) > 0,
		TokensReported: snap.ProviderTokensReported,
		Features:       snap.Features,
		TotalCalls:     int64(snap.Total.Calls),
	}
}

func (s *Service) statusHealth() *StatusHealth {
	out := &StatusHealth{}

	if fi, err := os.Stat(s.engine.AOFPath()); err == nil {
		out.AOFSizeMB = formatMB(float64(fi.Size()) / (1024 * 1024))
	}
	last := s.engine.LastSnapshotTime()
	out.EverSnapshotted = !last.IsZero()
	if out.EverSnapshotted {
		out.LastSnapshotAgo = relativeTime(time.Since(last))
	}
	if s.embedder != nil {
		// A dimension probe is cheap and tells the agent whether semantic search
		// is usable at all.
		if v, err := s.embedder.Embed("probe"); err == nil {
			out.EmbedderDimensions = len(v)
		}
	}
	return out
}

// statusWarnings lists what deserves attention, and nothing else.
func (s *Service) statusWarnings(r GetStatusResult) []string {
	warnings := []string{}

	if r.Memory != nil {
		for _, idx := range r.Memory.Indexes {
			if idx.Vectors == 0 {
				warnings = append(warnings, fmt.Sprintf("index %q is empty", idx.Name))
			}
		}
	}

	if r.Maintenance != nil && !r.Maintenance.Enabled {
		warnings = append(warnings, "maintenance is disabled: duplicates accumulate and memories never decay")
	}

	if r.Health != nil {
		if r.Health.EmbedderDimensions == 0 {
			warnings = append(warnings, "embedder unavailable: semantic search is degraded")
		}
		if !r.Health.EverSnapshotted {
			warnings = append(warnings, "no snapshot has been written: a crash would lose the AOF")
		}
	}

	if r.LLM != nil && r.LLM.Configured && len(r.LLM.Features) == 0 {
		warnings = append(warnings, "an LLM is configured but no call has been recorded")
	}

	return warnings
}

// statusSummary is the one line an agent can act on.
func statusSummary(r GetStatusResult) string {
	parts := []string{}

	if r.Memory != nil {
		parts = append(parts, fmt.Sprintf("%d memories in %d indexes",
			r.Memory.TotalVectors, len(r.Memory.Indexes)))
	}
	if r.Maintenance != nil {
		if r.Maintenance.Enabled {
			parts = append(parts, "maintenance active")
		} else {
			parts = append(parts, "maintenance off")
		}
	}
	if len(r.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", len(r.Warnings)))
	} else {
		parts = append(parts, "no warnings")
	}

	return strings.Join(parts, "; ")
}

// relativeTime renders a duration the way a reader scans it.
func relativeTime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// formatMB picks a readable unit.
func formatMB(mb float64) string {
	switch {
	case mb >= 1024:
		return fmt.Sprintf("%.1f GB", mb/1024)
	case mb >= 1:
		return fmt.Sprintf("%.1f MB", mb)
	default:
		return fmt.Sprintf("%.0f KB", mb*1024)
	}
}
