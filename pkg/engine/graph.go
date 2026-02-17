package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sync/atomic"
	"time"

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
	prefixRel          = "rel" // Forward prefix
	prefixRev          = "rev" // Reverse prefix
	maxPropKeyLength   = 256   // Maximum length for property keys
	maxPropValueLength = 4096  // Maximum length for property values (strings)
	maxPropsPerEdge    = 100   // Maximum number of properties per edge
)

// makeGraphKey generates the storage key for a specific direction.
// prefix: "rel" or "rev"
func makeGraphKey(prefix, nodeID, relType string) string {
	return fmt.Sprintf("%s:%s:%s", prefix, nodeID, relType)
}

// validateProps sanitizes and validates user-provided properties to prevent injection and abuse.
func validateProps(props map[string]any) error {
	if len(props) > maxPropsPerEdge {
		return fmt.Errorf("too many properties: %d (max %d)", len(props), maxPropsPerEdge)
	}

	for key, value := range props {
		// Validate key
		if len(key) > maxPropKeyLength {
			return fmt.Errorf("property key too long: %d chars (max %d)", len(key), maxPropKeyLength)
		}

		// Check for valid key characters (alphanumeric, underscore, hyphen)
		for _, r := range key {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				return fmt.Errorf("invalid character in property key: %q (allowed: alphanumeric, _, -)", r)
			}
		}

		// Validate value (only check length for strings)
		if str, ok := value.(string); ok {
			if len(str) > maxPropValueLength {
				return fmt.Errorf("property value for key %q too long: %d chars (max %d)", key, len(str), maxPropValueLength)
			}
		}
	}

	return nil
}

// VLink creates a rich directed edge between two nodes and automatically updates the reverse index.
// It ensures the graph is navigable in both directions.
//
// LOCK ORDERING: This function acquires adminMu first, then DB.mu (via updateAdjacencyList).
// To prevent deadlocks, always acquire locks in this order: adminMu -> DB.mu
func (e *Engine) VLink(sourceID, targetID, relationType, inverseRelationType string, weight float32, props map[string]any) error {
	// Validate user-provided properties before acquiring locks
	if props != nil {
		if err := validateProps(props); err != nil {
			return fmt.Errorf("invalid properties: %w", err)
		}
	}

	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	now := time.Now().UnixNano()

	// 1. Forward Link: Source -> Target
	// Key: rel:source:type -> [target]
	if err := e.updateAdjacencyList(prefixRel, sourceID, relationType, targetID, true, false, now, weight, props); err != nil {
		return err
	}

	// 2. Reverse Link: Target <- Source (Implicit)
	// Key: rev:target:type -> [source]
	// This allows asking: "Who points to Target via 'relationType'?"
	if err := e.updateAdjacencyList(prefixRev, targetID, relationType, sourceID, true, false, now, weight, props); err != nil {
		return err
	}

	// 3. Inverse Relation (Explicit, e.g. "parent" <-> "child")
	// If the user specified an explicit inverse semantic, we link that too.
	if inverseRelationType != "" {
		// Forward: Target -> Source
		if err := e.updateAdjacencyList(prefixRel, targetID, inverseRelationType, sourceID, true, false, now, weight, props); err != nil {
			return err
		}
		// Reverse: Source <- Target
		if err := e.updateAdjacencyList(prefixRev, sourceID, inverseRelationType, targetID, true, false, now, weight, props); err != nil {
			return err
		}
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VUnlink removes a directed edge and its reverse entry.
// If hardDelete is true, the record is physically removed (cannot be recovered/time-traveled).
// If hardDelete is false, it sets DeletedAt (Soft Delete).
func (e *Engine) VUnlink(sourceID, targetID, relationType, inverseRelationType string, hardDelete bool) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	now := time.Now().UnixNano()

	// 1. Remove Forward (SOFT DELETE)
	if err := e.updateAdjacencyList(prefixRel, sourceID, relationType, targetID, false, hardDelete, now, 0, nil); err != nil {
		return err
	}

	// 2. Remove Reverse (SOFT DELETE)
	if err := e.updateAdjacencyList(prefixRev, targetID, relationType, sourceID, false, hardDelete, now, 0, nil); err != nil {
		return err
	}

	// 3. Remove Inverse Explicit
	if inverseRelationType != "" {
		if err := e.updateAdjacencyList(prefixRel, targetID, inverseRelationType, sourceID, false, hardDelete, now, 0, nil); err != nil {
			return err
		}
		if err := e.updateAdjacencyList(prefixRev, sourceID, inverseRelationType, targetID, false, hardDelete, now, 0, nil); err != nil {
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
func (e *Engine) updateAdjacencyList(prefix, rootID, relType, targetID string, isAdd bool, hardDelete bool, timestamp int64, weight float32, props map[string]any) error {
	key := makeGraphKey(prefix, rootID, relType)

	var edges EdgeList
	val, found := e.DB.GetKVStore().Get(key)

	if found {
		// FAST PATH: Direct Unmarshal. No checks for legacy strings.
		if err := json.Unmarshal(val, &edges); err != nil {
			// If this fails, the DB is corrupt or contains legacy data we chose to ignore.
			return fmt.Errorf("graph format error for %s: %w", key, err)
		}
	} else {
		edges = make(EdgeList, 0)
	}

	activeIdx := -1
	for i, edge := range edges {
		if edge.TargetID == targetID && edge.DeletedAt == 0 {
			// We look for the entry matching the target, regardless if active or deleted.
			// (If multiple versions existed, we'd take the last one, but we keep 1 entry per target for now)
			activeIdx = i
			break
		}
	}

	hasChanges := false

	if isAdd {
		// --- EVOLUTION LOGIC ---
		if activeIdx >= 0 {
			// An active edge exists. Check if it differs.
			currentEdge := edges[activeIdx]

			// Compare properties and weight
			// Note: reflect.DeepEqual handles nil maps vs empty maps slightly differently,
			// but for our purpose it's a good enough check for "Has Changed".
			propsChanged := !reflect.DeepEqual(currentEdge.Props, props)
			weightChanged := currentEdge.Weight != weight

			if propsChanged || weightChanged {
				// CHANGE DETECTED -> EVOLVE
				// 1. Soft Delete the old version
				edges[activeIdx].DeletedAt = timestamp

				// 2. Append new version
				newEdge := GraphEdge{
					TargetID:  targetID,
					CreatedAt: timestamp, // Born Now
					DeletedAt: 0,         // Active
					Weight:    weight,
					Props:     props,
				}
				edges = append(edges, newEdge)
				hasChanges = true
			} else {
				// IDENTICAL -> NO-OP
				// We do nothing. This preserves the original CreatedAt.
			}
		} else {
			// No active edge found (either never existed or was deleted previously).
			// CREATE NEW.
			newEdge := GraphEdge{
				TargetID:  targetID,
				CreatedAt: timestamp,
				DeletedAt: 0,
				Weight:    weight,
				Props:     props,
			}
			edges = append(edges, newEdge)
			hasChanges = true
		}

		/*
			// UPSERT LOGIC
			if activeIdx >= 0 {
				// Update existing
				edges[activeIdx].DeletedAt = 0 // Reactivate
				edges[activeIdx].Weight = weight
				edges[activeIdx].Props = props
				// We DO NOT update CreatedAt to preserve history, unless it was 0
				if edges[activeIdx].CreatedAt == 0 {
					edges[activeIdx].CreatedAt = timestamp
				}
			} else {
				// Append new
				edges = append(edges, GraphEdge{
					TargetID:  targetID,
					CreatedAt: timestamp,
					Weight:    weight,
					Props:     props,
				})
			}
		*/
	} else {
		// DELETE LOGIC
		if hardDelete {
			// Find ALL instances of this target (active or history) and nuke them
			// Filtering in-place
			newEdges := edges[:0]
			for _, edge := range edges {
				if edge.TargetID != targetID {
					newEdges = append(newEdges, edge)
				} else {
					hasChanges = true
				}
			}
			edges = newEdges
		} else {
			// Soft Delete ONLY the active one
			if activeIdx >= 0 {
				edges[activeIdx].DeletedAt = timestamp
				hasChanges = true
			}
		}
	}

	// Optimization: If no changes (e.g. redundant VLink), skip disk write
	if !hasChanges {
		return nil
	}

	// Persist
	if len(edges) == 0 {
		cmd := persistence.FormatCommand("DEL", []byte(key))
		if err := e.AOF.Write(cmd); err != nil {
			return err
		}
		e.DB.GetKVStore().Delete(key)
	} else {
		newVal, err := json.Marshal(edges)
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

// VGetLinks retrieves ACTIVE links.
func (e *Engine) VGetLinks(sourceID, relationType string) ([]string, bool) {
	return e.getFilteredList(prefixRel, sourceID, relationType)
}

// VGetIncoming retrieves ACTIVE incoming links.
func (e *Engine) VGetIncoming(targetID, relationType string) ([]string, bool) {
	return e.getFilteredList(prefixRev, targetID, relationType)
}

func (e *Engine) getFilteredList(prefix, nodeID, relType string) ([]string, bool) {
	key := makeGraphKey(prefix, nodeID, relType)
	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil, false
	}

	var edges EdgeList
	if err := json.Unmarshal(val, &edges); err != nil {
		return nil, false
	}

	var targets []string
	for _, edge := range edges {
		if edge.IsActive() {
			targets = append(targets, edge.TargetID)
		}
	}

	if len(targets) == 0 {
		return nil, false
	}

	return targets, true
}

// TODO: VGetLinksAtTime(t)

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
func (e *Engine) VExtractSubgraph(indexName, rootID string, relations []string, maxDepth int, atTime int64, guideQuery []float32, threshold float64) (*SubgraphResult, error) {
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

	// Helper for semantic check
	shouldTraverse := func(targetID string) bool {
		// If no guide query, always traverse
		if len(guideQuery) == 0 {
			return true
		}
		// Calculate distance
		dist, err := e.computeDistance(indexName, targetID, guideQuery)
		if err != nil {
			// If node has no vector (Ghost Node), we INCLUDE it structurally?
			// Or EXCLUDE it?
			// Decision: INCLUDE. Structure nodes (Categories) are bridges.
			return true
		}

		// Check threshold.
		// Distance is 0..2 (Cosine) or 0..Inf (Euclidean).
		// User must provide appropriate threshold (e.g. 0.4 for Cosine).
		return dist <= threshold
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		// Explore neighbors for each relation type requested
		for _, rel := range relations {
			// A. Outgoing Edges (Source -> Target) Time aware
			// We use getFilteredEdges directly to respect 'atTime'
			// prefixRel = "rel"
			outEdges, found := e.getFilteredEdges(prefixRel, current.id, rel, atTime)
			if found {
				for _, edge := range outEdges {
					target := edge.TargetID

					// --- SEMANTIC GATING ---
					if !shouldTraverse(target) {
						continue // Prune this branch
					}

					edges = append(edges, SubgraphEdge{
						Source:   current.id,
						Target:   target,
						Relation: rel,
						Dir:      "out",
					})

					if !visited[target] {
						visited[target] = true
						data, _ := e.VGet(indexName, target)
						nodesMap[target] = SubgraphNode{ID: target, Metadata: data.Metadata}
						queue = append(queue, queueItem{id: target, depth: current.depth + 1})
					}
				}
			}

			// B. Incoming Edges (Source <- Target)
			// prefixRev = "rev"
			inEdges, found := e.getFilteredEdges(prefixRev, current.id, rel, atTime)
			if found {
				for _, edge := range inEdges {
					source := edge.TargetID // In reverse index, TargetID is the Source

					// --- SEMANTIC GATING ---
					if !shouldTraverse(source) {
						continue
					}

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

// --- Time Travel & Rich Data Access ---

// VGetEdges retrieves full edge details (weights, props) relative to a specific point in time.
// atTime: Unix Nano timestamp. If 0, returns the current active state.
func (e *Engine) VGetEdges(sourceID, relationType string, atTime int64) ([]GraphEdge, bool) {
	return e.getFilteredEdges(prefixRel, sourceID, relationType, atTime)
}

// VGetIncomingEdges retrieves incoming rich edges relative to a specific point in time.
func (e *Engine) VGetIncomingEdges(targetID, relationType string, atTime int64) ([]GraphEdge, bool) {
	return e.getFilteredEdges(prefixRev, targetID, relationType, atTime)
}

// getFilteredEdges reads the raw list and applies the Time Travel logic.
func (e *Engine) getFilteredEdges(prefix, nodeID, relType string, atTime int64) ([]GraphEdge, bool) {
	key := makeGraphKey(prefix, nodeID, relType)
	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil, false
	}

	var allEdges EdgeList
	if err := json.Unmarshal(val, &allEdges); err != nil {
		return nil, false
	}

	var visibleEdges []GraphEdge

	// TIME TRAVEL LOGIC
	for _, edge := range allEdges {
		if atTime == 0 {
			// Mode: CURRENT STATE
			// We only want currently active edges.
			if edge.DeletedAt == 0 {
				visibleEdges = append(visibleEdges, edge)
			}
		} else {
			// Mode: SNAPSHOT AT 'atTime'
			// 1. Must exist at that time
			if edge.CreatedAt <= atTime {
				// 2. Must NOT be deleted yet at that time
				// (DeletedAt == 0 means never deleted, > atTime means deleted in the future relative to query)
				if edge.DeletedAt == 0 || edge.DeletedAt > atTime {
					visibleEdges = append(visibleEdges, edge)
				}
			}
		}
	}

	if len(visibleEdges) == 0 {
		return nil, false
	}

	return visibleEdges, true
}

// PruneEdgeList removes edges that have been soft-deleted for longer than the retention period.
// Returns true if modifications were made.
// retention: Duration. If 0, nothing is pruned.
func (e *Engine) PruneEdgeList(key string, retention time.Duration) (bool, error) {
	if retention <= 0 {
		return false, nil
	}

	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return false, nil // Key might have been deleted concurrently
	}

	// Unmarshal directly (assuming migrated data)
	var edges EdgeList
	if err := json.Unmarshal(val, &edges); err != nil {
		// If unmarshal fails (e.g. legacy data or corrupted), we skip it safely.
		// We could log a warning here.
		return false, nil
	}

	cutoffTime := time.Now().Add(-retention).UnixNano()
	changed := false
	activeEdges := make(EdgeList, 0, len(edges))

	for _, edge := range edges {
		// Condition to KEEP the edge:
		// 1. It is active (DeletedAt == 0)
		// 2. OR It is deleted, but RECENTLY (DeletedAt > cutoffTime)
		if edge.DeletedAt == 0 || edge.DeletedAt > cutoffTime {
			activeEdges = append(activeEdges, edge)
		} else {
			changed = true // We are dropping this edge (Hard Delete)
		}
	}

	if !changed {
		return false, nil
	}

	// Persist changes
	if len(activeEdges) == 0 {
		// List became empty -> Delete Key
		cmd := persistence.FormatCommand("DEL", []byte(key))
		if err := e.AOF.Write(cmd); err != nil {
			return false, err
		}
		e.DB.GetKVStore().Delete(key)
	} else {
		// Update List
		newVal, err := json.Marshal(activeEdges)
		if err != nil {
			return false, err
		}
		cmd := persistence.FormatCommand("SET", []byte(key), newVal)
		if err := e.AOF.Write(cmd); err != nil {
			return false, err
		}
		e.DB.GetKVStore().Set(key, newVal)
	}

	return true, nil
}

// RunGraphVacuum executes the cleanup of old soft-deleted edges.
// It scans ALL keys in the KV store (Global Vacuum).
func (e *Engine) RunGraphVacuum() {
	// 1. Determine Retention Policy
	// Since graph is global, we need a global policy or we pick the policy from the first index?
	// Current design puts config inside Index. This is a mismatch for global KV.
	// STRATEGY: Use the retention config from the first available index as the "System Policy",
	// or add a global config to Engine Options.

	// Let's use Engine Options for simplicity in v0.5.0
	// But we didn't add it there. Let's iterate indexes and take the MAX retention found (safest).

	infos, _ := e.DB.GetVectorIndexInfoUnlocked()
	var retention time.Duration

	for _, info := range infos {
		idx, _ := e.DB.GetVectorIndex(info.Name)
		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			cfg := hnswIdx.GetMaintenanceConfig()
			if cfg.GraphRetention > 0 {
				// Pick the logic: if ANY index wants to keep history, we keep it?
				// Or if ANY index wants to delete, we delete?
				// Let's assume uniform config for now or take the first non-zero.
				retention = time.Duration(cfg.GraphRetention)
				break
			}
		}
	}

	if retention <= 0 {
		return // Keep forever
	}

	// 2. Scan & Prune
	keys := e.DB.GetKVStore().Keys()
	prunedTotal := 0

	for i, key := range keys {
		if len(key) > 4 && (key[:4] == "rel:" || key[:4] == "rev:") {
			changed, _ := e.PruneEdgeList(key, retention)
			if changed {
				prunedTotal++
			}
		}

		// Yield to avoid blocking writer lock in PruneEdgeList for too long continuously
		if i%100 == 0 && i > 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	if prunedTotal > 0 {
		slog.Info("[GraphVacuum] Cleanup complete", "pruned_keys", prunedTotal)
	}
}
