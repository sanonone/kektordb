package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// Graph Relationship Model
//
// We store relationships in two directions to enable O(1) traversal both ways.
//
// 1. Forward Index: "rel:<source_id>:<relation_type>" -> ["target_A", "target_B"]
//    Use case: "What does Source link to?" (e.g., Get children, Get references)
//
// 2. Reverse Index: "rev:<target_id>:<relation_type>" -> ["source_A", "source_B"]
//    Use case: "Who links to Target?" (e.g., Get parents, Get backlinks)

const (
	prefixRel = "rel" // Forward prefix
	prefixRev = "rev" // Reverse prefix
)

// makeGraphKey generates the storage key for a specific direction.
// prefix: "rel" or "rev"
func makeGraphKey(prefix, nodeID, relType string) string {
	return fmt.Sprintf("%s:%s:%s", prefix, nodeID, relType)
}

// VLink creates a directed edge between two nodes and automatically updates the reverse index.
// It ensures the graph is navigable in both directions.
func (e *Engine) VLink(sourceID, targetID, relationType, inverseRelationType string) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// 1. Forward Link: Source -> Target
	// Key: rel:source:type -> [target]
	if err := e.updateAdjacencyList(prefixRel, sourceID, relationType, targetID, true); err != nil {
		return err
	}

	// 2. Reverse Link: Target <- Source (Implicit)
	// Key: rev:target:type -> [source]
	// This allows asking: "Who points to Target via 'relationType'?"
	if err := e.updateAdjacencyList(prefixRev, targetID, relationType, sourceID, true); err != nil {
		return err
	}

	// 3. Inverse Relation (Explicit, e.g. "parent" <-> "child")
	// If the user specified an explicit inverse semantic, we link that too.
	if inverseRelationType != "" {
		// Forward: Target -> Source
		if err := e.updateAdjacencyList(prefixRel, targetID, inverseRelationType, sourceID, true); err != nil {
			return err
		}
		// Reverse: Source <- Target
		if err := e.updateAdjacencyList(prefixRev, sourceID, inverseRelationType, targetID, true); err != nil {
			return err
		}
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VUnlink removes a directed edge and its reverse entry.
func (e *Engine) VUnlink(sourceID, targetID, relationType, inverseRelationType string) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// 1. Remove Forward
	if err := e.updateAdjacencyList(prefixRel, sourceID, relationType, targetID, false); err != nil {
		return err
	}

	// 2. Remove Reverse
	if err := e.updateAdjacencyList(prefixRev, targetID, relationType, sourceID, false); err != nil {
		return err
	}

	// 3. Remove Inverse Explicit
	if inverseRelationType != "" {
		if err := e.updateAdjacencyList(prefixRel, targetID, inverseRelationType, sourceID, false); err != nil {
			return err
		}
		if err := e.updateAdjacencyList(prefixRev, sourceID, inverseRelationType, targetID, false); err != nil {
			return err
		}
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// updateAdjacencyList adds or removes a value from the adjacency list of a node.
// prefix: "rel" or "rev"
// rootID: the node owning the list
// relType: type of relationship
// valueID: the ID to add/remove from the list
// isAdd: true to add, false to remove
func (e *Engine) updateAdjacencyList(prefix, rootID, relType, valueID string, isAdd bool) error {
	key := makeGraphKey(prefix, rootID, relType)

	var targets []string
	val, found := e.DB.GetKVStore().Get(key)
	if found {
		_ = json.Unmarshal(val, &targets)
	}

	// Check if modification is needed
	exists := slices.Contains(targets, valueID)

	if isAdd {
		if exists {
			return nil // Idempotent: already exists
		}
		targets = append(targets, valueID)
	} else {
		if !exists {
			return nil // Idempotent: already removed
		}
		// Filter out the valueID
		newTargets := make([]string, 0, len(targets))
		for _, t := range targets {
			if t != valueID {
				newTargets = append(newTargets, t)
			}
		}
		targets = newTargets
	}

	// Persist changes
	if len(targets) == 0 {
		// If list is empty, delete the key to save space
		cmd := persistence.FormatCommand("DEL", []byte(key))
		if err := e.AOF.Write(cmd); err != nil {
			return err
		}
		e.DB.GetKVStore().Delete(key)
	} else {
		// Save updated list
		newVal, err := json.Marshal(targets)
		if err != nil {
			return err
		}
		cmd := persistence.FormatCommand("SET", []byte(key), newVal)
		if err := e.AOF.Write(cmd); err != nil {
			return err
		}
		e.DB.GetKVStore().Set(key, newVal)
	}

	return nil
}

// VGetLinks retrieves outgoing links (Forward Index).
// Example: VGetLinks("doc_1", "mentions") -> ["entity_python", "entity_go"]
func (e *Engine) VGetLinks(sourceID, relationType string) ([]string, bool) {
	return e.getAdjacencyList(prefixRel, sourceID, relationType)
}

// VGetIncoming retrieves incoming links (Reverse Index).
// Example: VGetIncoming("entity_python", "mentions") -> ["doc_1", "doc_5"]
// This answers "Who points to me?" efficiently.
func (e *Engine) VGetIncoming(targetID, relationType string) ([]string, bool) {
	return e.getAdjacencyList(prefixRev, targetID, relationType)
}

// getAdjacencyList is a read-only helper to fetch list from KV store.
func (e *Engine) getAdjacencyList(prefix, nodeID, relType string) ([]string, bool) {
	key := makeGraphKey(prefix, nodeID, relType)
	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil, false
	}

	var targets []string
	if err := json.Unmarshal(val, &targets); err != nil {
		return nil, false
	}
	return targets, true
}

// resolveGraphFilter traverses the graph and returns a set of allowed Internal IDs.
func (e *Engine) resolveGraphFilter(indexName string, q GraphQuery) (map[uint32]struct{}, error) {
	if q.RootID == "" {
		return nil, nil // No filter applied if RootID is empty
	}

	// 1. Get the Index
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index not found")
	}
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("index does not support graph operations")
	}

	allowedSet := make(map[uint32]struct{})
	visited := make(map[string]struct{})

	// Queue holds ID and Depth
	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{id: q.RootID, depth: 0}}
	visited[q.RootID] = struct{}{}

	// 2. Add Root Node to allowed set (if it exists as a vector)
	// FIX: Use the updated GetInternalID signature
	if rootIntID, found := hnswIdx.GetInternalID(q.RootID); found {
		allowedSet[rootIntID] = struct{}{}
	}

	// Safety caps
	maxDepth := q.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 5 {
		maxDepth = 5 // Hard cap
	}

	// 3. BFS Traversal
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr.depth >= maxDepth {
			continue
		}

		// Helper to process neighbors list
		processNeighbors := func(neighbors []string) {
			for _, target := range neighbors {
				// Only visit if not already visited
				if _, seen := visited[target]; !seen {
					visited[target] = struct{}{}

					// FIX: Check if this graph node corresponds to a vector node
					// Only vector nodes can be returned by VSearch
					if internalID, found := hnswIdx.GetInternalID(target); found {
						allowedSet[internalID] = struct{}{}
					}

					// Add to queue for next hop
					queue = append(queue, queueItem{id: target, depth: curr.depth + 1})
				}
			}
		}

		// A. Forward Links (Source -> Target)
		if q.Direction == "out" || q.Direction == "both" || q.Direction == "" {
			for _, rel := range q.Relations {
				// VGetLinks is safe (uses RLock internally on KVStore)
				targets, _ := e.VGetLinks(curr.id, rel)
				processNeighbors(targets)
			}
		}

		// B. Backward Links (Target <- Source)
		if q.Direction == "in" || q.Direction == "both" {
			for _, rel := range q.Relations {
				sources, _ := e.VGetIncoming(curr.id, rel)
				processNeighbors(sources)
			}
		}
	}

	return allowedSet, nil
}

// VGetRelations retrieves ALL relationships for a node is technically possible by scanning keys,
// but efficiently supported only via known types for now.
func (e *Engine) VGetRelations(sourceID string) map[string][]string {
	// Not implemented efficiently in v0.4.1 (Requires prefix scan support in KVStore).
	// Placeholder for future implementation.
	return nil
}

// --- Advanced Graph Operations ---

type SubgraphNode struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

type SubgraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Dir      string `json:"dir"` // "out" or "in" relative to the traversal
}

type SubgraphResult struct {
	RootID string         `json:"root_id"`
	Nodes  []SubgraphNode `json:"nodes"`
	Edges  []SubgraphEdge `json:"edges"`
}

// VExtractSubgraph performs a Breadth-First Search (BFS) to retrieve the local neighborhood
// of a root node up to a specified depth.
// It traverses both outgoing ("rel") and incoming ("rev") edges for the specified relation types.
func (e *Engine) VExtractSubgraph(indexName, rootID string, relations []string, maxDepth int) (*SubgraphResult, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 3 {
		maxDepth = 3 // Safety cap to prevent explosion
	}

	visited := make(map[string]bool)
	nodesMap := make(map[string]SubgraphNode)
	edges := make([]SubgraphEdge, 0)

	// Queue for BFS: holds NodeID and current Depth
	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{id: rootID, depth: 0}}
	visited[rootID] = true

	// Hydrate Root immediately
	rootData, _ := e.VGet(indexName, rootID)
	// If VGet fails/empty, we still return the node with ID
	nodesMap[rootID] = SubgraphNode{ID: rootID, Metadata: rootData.Metadata}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		// Explore neighbors for each relation type requested
		for _, rel := range relations {
			// A. Outgoing Edges (Source -> Target)
			targets, found := e.VGetLinks(current.id, rel)
			if found {
				for _, target := range targets {
					// Add Edge
					edges = append(edges, SubgraphEdge{
						Source:   current.id,
						Target:   target,
						Relation: rel,
						Dir:      "out",
					})

					// Visit Node
					if !visited[target] {
						visited[target] = true
						// Fetch Metadata (Hydration)
						data, _ := e.VGet(indexName, target)
						nodesMap[target] = SubgraphNode{ID: target, Metadata: data.Metadata}
						// Enqueue
						queue = append(queue, queueItem{id: target, depth: current.depth + 1})
					}
				}
			}

			// B. Incoming Edges (Source <- Target)
			sources, found := e.VGetIncoming(current.id, rel)
			if found {
				for _, source := range sources {
					// Add Edge (Direction is IN relative to current)
					edges = append(edges, SubgraphEdge{
						Source:   source,
						Target:   current.id,
						Relation: rel,
						Dir:      "in",
					})

					if !visited[source] {
						visited[source] = true
						data, _ := e.VGet(indexName, source)
						nodesMap[source] = SubgraphNode{ID: source, Metadata: data.Metadata}
						queue = append(queue, queueItem{id: source, depth: current.depth + 1})
					}
				}
			}
		}
	}

	// Convert map to slice
	nodeList := make([]SubgraphNode, 0, len(nodesMap))
	for _, n := range nodesMap {
		nodeList = append(nodeList, n)
	}

	return &SubgraphResult{
		RootID: rootID,
		Nodes:  nodeList,
		Edges:  edges,
	}, nil
}
