package engine

// GraphEdge represents a rich, versioned connection between nodes.
// Used to store the adjacency list in the KV store.
type GraphEdge struct {
	TargetID  string         `json:"t"`           // Target Node ID
	CreatedAt int64          `json:"c"`           // Creation Timestamp (Unix Nano)
	DeletedAt int64          `json:"d,omitempty"` // Deletion Timestamp (0 = Active)
	Weight    float32        `json:"w,omitempty"` // Weight (default 1.0, omitted if 0/1 logic handled externally)
	Props     map[string]any `json:"p,omitempty"` // Property Graph attributes
}

// EdgeList is the structure actually stored in the KV Value.
// It replaces the old []string format.
type EdgeList []GraphEdge

// Active returns true if the edge is valid at the current time (not soft-deleted).
func (e GraphEdge) IsActive() bool {
	return e.DeletedAt == 0
}
