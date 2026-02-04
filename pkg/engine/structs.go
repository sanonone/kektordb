package engine

// GraphQuery defines the criteria to select a subset of nodes based on graph topology.
type GraphQuery struct {
	RootID string `json:"root_id"`
	// Relation types to follow (e.g., "mentions", "parent").
	// If empty, follows all relations (though usually you want to be specific).
	Relations []string `json:"relations"`

	// Direction of traversal: "out" (Source->Target), "in" (Target<-Source), or "both".
	// Default: "out".
	Direction string `json:"direction"`

	// How deep to traverse. 1 = immediate neighbors. -1 = infinite (dangerous, use limit).
	MaxDepth int `json:"max_depth"`
}
