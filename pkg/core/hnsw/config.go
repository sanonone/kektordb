package hnsw

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration is a wrapper around time.Duration that supports JSON string parsing (e.g. "1m", "10s").
type Duration time.Duration

// UnmarshalJSON implements custom decoding logic.
// It handles both numbers (nanoseconds) and strings ("10s").
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)
		return nil
	default:
		return fmt.Errorf("invalid duration")
	}
}

// MarshalJSON serializes the duration back to a readable string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// AutoMaintenanceConfig defines the behavior of background tasks per index.
type AutoMaintenanceConfig struct {
	// Vacuum (Cleanup) Settings
	// Interval between vacuum checks. Default: 1m.
	VacuumInterval Duration `json:"vacuum_interval"`
	// Percentage of deleted nodes (0.0-1.0) to trigger vacuum. Default: 0.1 (10%).
	DeleteThreshold float64 `json:"delete_threshold"`

	// Graph Vacuum Settings
	// How often to scan for expired edges (e.g. "1h").
	GraphVacuumInterval Duration `json:"graph_vacuum_interval"`
	// How long to keep soft-deleted edges (e.g. "720h" = 30 days).
	// If 0, history is kept forever (Time Travel).
	GraphRetention Duration `json:"graph_retention"`

	// Refine (Optimization) Settings
	// If true, runs background optimization to improve recall. Default: false.
	RefineEnabled bool `json:"refine_enabled"`
	// Interval between refinement cycles. Default: 30s.
	RefineInterval Duration `json:"refine_interval"`
	// Number of nodes to re-process per cycle. Default: 100.
	RefineBatchSize int `json:"refine_batch_size"`
	// Search breadth during refinement. Higher = better quality but slower.
	// If 0, uses the index's efConstruction default.
	RefineEfConstruction int `json:"refine_ef_construction"`
}

// DefaultMaintenanceConfig returns safe defaults: Vacuum ON, Refine OFF.
func DefaultMaintenanceConfig() AutoMaintenanceConfig {
	return AutoMaintenanceConfig{
		VacuumInterval:  Duration(5 * time.Minute),
		DeleteThreshold: 0.1, // 10% dirty
		// Default: Run check every day, but keep history forever (Safety first)
		GraphVacuumInterval:  Duration(24 * time.Hour),
		GraphRetention:       Duration(0),
		RefineEnabled:        false,
		RefineInterval:       Duration(30 * time.Minute),
		RefineBatchSize:      500,
		RefineEfConstruction: 0,
	}
}

// AutoLinkRule defines a rule for automatically creating graph connections
// based on metadata fields during vector insertion.
type AutoLinkRule struct {
	// MetadataField is the key in the metadata map to look for (e.g., "chat_id").
	MetadataField string `json:"metadata_field"`

	// RelationType is the type of the link to create (e.g., "belongs_to_chat").
	RelationType string `json:"relation_type"`

	// CreateNode indicates whether to create a "stub" node for the target
	// if it doesn't exist. Default should be true for most cases.
	CreateNode bool `json:"create_node"`
}
