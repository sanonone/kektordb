package compiler

import "time"

// CompileMode indicates how the artifact was compiled.
type CompileMode string

const (
	CompileModeDeterministic CompileMode = "deterministic"
	CompileModeLLM           CompileMode = "llm"
	CompileModeHybrid        CompileMode = "hybrid"
	CompileModeAuto          CompileMode = "auto"
)

// CompileStatus indicates the current state of an artifact compilation.
type CompileStatus string

const (
	CompileStatusPending   CompileStatus = "pending"
	CompileStatusCompiling CompileStatus = "compiling"
	CompileStatusComplete  CompileStatus = "complete"
	CompileStatusFailed    CompileStatus = "failed"
	CompileStatusStale     CompileStatus = "stale"
)

// Provenance records the source of a compiled field value.
type Provenance struct {
	SourceID   string  `json:"source_id"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
	Role       string  `json:"role"` // "primary", "supporting", "contextual", "computed", "inferred"
}

// Artifact is the central data structure of the Knowledge Engine.
// It is stored as a pinned graph node with type="knowledge_artifact".
type Artifact struct {
	ID         string `json:"id,omitempty"` // Node ID in the graph
	Name       string `json:"name"`
	Version    int    `json:"version"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`

	Data          map[string]any          `json:"data"`
	Provenance    map[string][]Provenance `json:"provenance,omitempty"`
	Confidence    map[string]float64      `json:"confidence,omitempty"`
	SourceNodeIDs []string                `json:"source_node_ids"`

	CompileMode    CompileMode   `json:"compile_mode"`
	Status         CompileStatus `json:"status"`
	StalenessScore float64       `json:"staleness_score"`
	CompiledAt     time.Time     `json:"compiled_at"`

	Schema   *OutputSchema `json:"schema,omitempty"`
	TaskSpec *TaskSpec     `json:"task_spec,omitempty"`
}

// CompileRequest is the input for compiling a knowledge artifact.
type CompileRequest struct {
	Name          string        `json:"name"`
	Template      string        `json:"template,omitempty"`
	TaskSpec      *TaskSpec     `json:"task_spec,omitempty"`
	Sources       SourceSpec    `json:"sources"`
	RefreshPolicy RefreshPolicy `json:"refresh_policy,omitempty"`
	CompileMode   CompileMode   `json:"compile_mode,omitempty"`
	IndexName     string        `json:"index_name,omitempty"`
}

// SourceSpec describes where to gather data for compilation.
type SourceSpec struct {
	Type   string    `json:"type"`            // "graph_query", "semantic_search", "all"
	Entity EntityRef `json:"entity"`          // Target entity
	Depth  int       `json:"depth,omitempty"` // Graph traversal depth (default 2)
	Query  string    `json:"query,omitempty"` // Optional semantic search query
}

// EntityRef identifies an entity in the knowledge graph.
type EntityRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// RefreshPolicy controls when and how an artifact is recompiled.
type RefreshPolicy struct {
	Mode           string   `json:"mode,omitempty"`                // "on_source_change", "scheduled", "manual"
	MaxStalenessH  int      `json:"max_staleness_hours,omitempty"` // 0 = no limit
	RecompileOn    []string `json:"recompile_on,omitempty"`        // "entity_update", "new_relationship", "contradiction"
	KeepHistory    bool     `json:"keep_history,omitempty"`        // Keep all versions (default true)
	MaxVersions    int      `json:"max_versions,omitempty"`        // Max versions to keep (0 = unlimited)
	PruneAfterDays int      `json:"prune_after_days,omitempty"`    // Auto-delete versions older than N days (0 = never)
}

// TaskSpec defines the agent role and desired output shape for compilation.
type TaskSpec struct {
	AgentRole     string        `json:"agent_role,omitempty"`
	Description   string        `json:"description,omitempty"`
	OutputSchema  OutputSchema  `json:"output_schema,omitempty"`
	ConfidenceMin float64       `json:"confidence_min,omitempty"`
	RefreshPolicy RefreshPolicy `json:"refresh_policy,omitempty"`
}

// CompileTemplate defines a built-in compilation recipe.
type CompileTemplate struct {
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	EntityTypes   []string      `json:"entity_types"`
	Schema        OutputSchema  `json:"schema"`
	Sources       SourceSpec    `json:"sources"`
	CompileMode   CompileMode   `json:"compile_mode"`
	RefreshPolicy RefreshPolicy `json:"refresh_policy,omitempty"`
}
