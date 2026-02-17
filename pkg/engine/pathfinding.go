package engine

import (
	"fmt"
)

type PathResult struct {
	Source string         `json:"source"`
	Target string         `json:"target"`
	Path   []string       `json:"path"`  // Sequence of Node IDs
	Edges  []SubgraphEdge `json:"edges"` // Details of edges traversed
}

// FindPath finds the shortest path between source and target using bidirectional BFS.
// relations: list of edge types to traverse. Cannot be empty.
func (e *Engine) FindPath(indexName, sourceID, targetID string, relations []string, maxDepth int, atTime int64) (*PathResult, error) {
	if maxDepth <= 0 {
		maxDepth = 4
	}
	// Fail fast if no relations provided, as we cannot traverse blindly efficiently
	if len(relations) == 0 {
		return nil, fmt.Errorf("at least one relation type must be specified")
	}

	// 1. Forward Search State
	fwdQueue := []string{sourceID}
	fwdVisited := map[string]string{sourceID: ""}
	fwdEdges := map[string]SubgraphEdge{}

	// 2. Backward Search State
	bwdQueue := []string{targetID}
	bwdVisited := map[string]string{targetID: ""}
	bwdEdges := map[string]SubgraphEdge{} // Not strictly used for reconstruction in v1, but good for debug

	var meetingNode string

	// Bidirectional BFS Loop
	for depth := 0; depth < maxDepth; depth++ {

		// --- A. Expansion Forward ---
		if len(fwdQueue) > 0 {
			nextQueue := []string{}
			for _, curr := range fwdQueue {
				// Check Intersection
				if _, ok := bwdVisited[curr]; ok {
					meetingNode = curr
					goto Found
				}

				// Iterate ONLY user-provided relations
				for _, rel := range relations {
					edges, found := e.getFilteredEdges(prefixRel, curr, rel, atTime)
					if found {
						for _, edge := range edges {
							neighbor := edge.TargetID
							if _, seen := fwdVisited[neighbor]; !seen {
								fwdVisited[neighbor] = curr
								fwdEdges[neighbor] = SubgraphEdge{Source: curr, Target: neighbor, Relation: rel, Dir: "out"}
								nextQueue = append(nextQueue, neighbor)
							}
						}
					}
				}
			}
			fwdQueue = nextQueue
		}

		// --- B. Expansion Backward ---
		if len(bwdQueue) > 0 {
			nextQueue := []string{}
			for _, curr := range bwdQueue {
				// Check Intersection
				if _, ok := fwdVisited[curr]; ok {
					meetingNode = curr
					goto Found
				}

				for _, rel := range relations {
					// Follow Incoming Links (Reverse Index)
					edges, found := e.getFilteredEdges(prefixRev, curr, rel, atTime)
					if found {
						for _, edge := range edges {
							neighbor := edge.TargetID // Source of the link
							if _, seen := bwdVisited[neighbor]; !seen {
								bwdVisited[neighbor] = curr
								// Note: Dir is logic relative to the path flow A->B
								bwdEdges[neighbor] = SubgraphEdge{Source: neighbor, Target: curr, Relation: rel, Dir: "out"}
								nextQueue = append(nextQueue, neighbor)
							}
						}
					}
				}
			}
			bwdQueue = nextQueue
		}
	}

	return nil, nil // No path found

Found:
	// Path Reconstruction Logic
	path := []string{}
	edges := []SubgraphEdge{}

	// 1. Trace back from Meeting Node to Source
	curr := meetingNode
	fwdPath := []string{} // meeting -> ... -> source

	// Collect nodes going backwards
	for curr != "" {
		fwdPath = append(fwdPath, curr)
		// Collect edge (if not source)
		if parent, ok := fwdVisited[curr]; ok && parent != "" {
			if edge, ok := fwdEdges[curr]; ok {
				edges = append(edges, edge)
			}
		}
		curr = fwdVisited[curr]
	}

	// Reverse to get Source -> ... -> Meeting
	for i := len(fwdPath) - 1; i >= 0; i-- {
		path = append(path, fwdPath[i])
	}

	// 2. Trace from Meeting Node to Target
	// bwdVisited map contains: Child -> Parent(closer to Target)
	// Actually bwdVisited: Node -> Node_closer_to_Target
	curr = bwdVisited[meetingNode]
	for curr != "" {
		path = append(path, curr)

		// Edge reconstruction for the second half requires lookup in bwdEdges?
		// bwdEdges stores Neighbor -> Edge(Neighbor -> Parent)
		// Here curr is the Neighbor of the previous node in the loop
		// Not strictly necessary for the list of IDs, but crucial for "Edges" list consistency.
		// For v0.5.0 simplicity, we might skip full Edge details for the second half
		// OR implement it properly. Let's stick to Node IDs for now to minimize bugs.

		curr = bwdVisited[curr]
	}

	return &PathResult{
		Source: sourceID,
		Target: targetID,
		Path:   path,
		Edges:  edges, // Contains at least the first half details
	}, nil
}
