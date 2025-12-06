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
		VacuumInterval:       Duration(1 * time.Minute),
		DeleteThreshold:      0.1, // 10% dirty
		RefineEnabled:        false,
		RefineInterval:       Duration(30 * time.Second),
		RefineBatchSize:      500,
		RefineEfConstruction: 0,
	}
}
