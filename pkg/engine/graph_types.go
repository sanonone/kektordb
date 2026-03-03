package engine

import "encoding/json"

// GraphEdge represents a rich, versioned connection between nodes.
// Used to store the adjacency list in the KV store.
type GraphEdge struct {
	TargetID  string  `json:"t"`           // Target Node ID
	CreatedAt int64   `json:"c"`           // Creation Timestamp (Unix Nano)
	DeletedAt int64   `json:"d,omitempty"` // Deletion Timestamp (0 = Active)
	Weight    float32 `json:"w,omitempty"` // Weight (default 1.0, omitted if 0/1 logic handled externally)
	// Props stores arbitrary metadata.
	// We use json.RawMessage ([]byte) instead of map[string]interface{}.
	// This prevents the Go Garbage Collector from scanning complex map structures
	// for millions of edges, drastically improving STW (Stop The World) pause times.
	Props json.RawMessage `json:"p,omitempty"` // property graph attributes
}

// EdgeList is the structure actually stored in the KV Value.
// It replaces the old []string format.
type EdgeList []GraphEdge

// Active returns true if the edge is valid at the current time (not soft-deleted).
func (e GraphEdge) IsActive() bool {
	return e.DeletedAt == 0
}

// GetProps decodes the raw JSON bytes into a usable map.
// This should only be called when the user explicitly requests edge properties.
func (e GraphEdge) GetProps() map[string]interface{} {
	if len(e.Props) == 0 {
		return nil
	}
	var props map[string]interface{}
	_ = json.Unmarshal(e.Props, &props)
	return props
}
