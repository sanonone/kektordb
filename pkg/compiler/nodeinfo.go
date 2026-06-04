package compiler

import "time"

// NodeInfo is an intermediate representation of a graph node
// collected during source gathering, used by relevance scoring
// and field compilation.
type NodeInfo struct {
	ID            string
	Content       string
	Metadata      map[string]any
	CreatedAt     time.Time
	Score         float64
	RelationCount int
	RelationTypes []string
	IsPinned      bool
}

func nodeIDs(nodes []NodeInfo) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

func deduplicateNodes(nodes []NodeInfo) []NodeInfo {
	seen := make(map[string]bool, len(nodes))
	out := make([]NodeInfo, 0, len(nodes))
	for _, n := range nodes {
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}
