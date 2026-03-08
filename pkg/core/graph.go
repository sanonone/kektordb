// Package core provides the fundamental data structures and logic for the KektorDB engine.
//
// File: pkg/core/graph.go
package core

import (
	"bytes"
	"hash/fnv"
	"sync"
)

// NumGraphShards defines the number of independent partitions for the graph.
// 128 is a good power-of-two number that provides high concurrency with low memory footprint.
const NumGraphShards = 128

// GraphEdge represents the full, outgoing edge.
// It acts as the Single Source of Truth (SSOT) for relationship data (weight, properties).
// Size: ~64 bytes.
type GraphEdge struct {
	TargetID  string  `json:"t"`
	CreatedAt int64   `json:"c"`
	DeletedAt int64   `json:"d,omitempty"`
	Weight    float32 `json:"w,omitempty"`
	Props     []byte  `json:"p,omitempty"` // json.RawMessage, shares underlying array
}

// ReverseEdge represents a lightweight incoming edge.
// It is used exclusively for topology navigation and time-travel filtering.
// Size: ~32 bytes (saves 50% RAM compared to full GraphEdge).
type ReverseEdge struct {
	SourceID  string `json:"s"`
	CreatedAt int64  `json:"c"`
	DeletedAt int64  `json:"d,omitempty"`
}

// GraphNode holds the adjacency lists for a single entity in RAM.
// We use slices instead of maps for the inner collections to avoid pointer-chasing
// and GC overhead. Soft deletes will be handled by updating 'DeletedAt' in place.
type GraphNode struct {
	ID       string
	OutEdges map[string][]GraphEdge   // RelationType -> List of outgoing edges
	InEdges  map[string][]ReverseEdge // RelationType -> List of incoming edges
}

// NewGraphNode initializes a node with empty maps to prevent panics.
func NewGraphNode(id string) *GraphNode {
	return &GraphNode{
		ID:       id,
		OutEdges: make(map[string][]GraphEdge),
		InEdges:  make(map[string][]ReverseEdge),
	}
}

// GraphShard represents an independent, lockable partition of the graph.
type GraphShard struct {
	mu    sync.RWMutex
	nodes map[string]*GraphNode
}

// GetShardIndex calculates a deterministic shard ID for a given string key.
// It uses FNV-1a which is very fast and has excellent distribution properties.
func GetShardIndex(id string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(id))
	return h.Sum32() & (NumGraphShards - 1) // Bitwise AND is faster than modulo
}

// LockTwoShards safely acquires exclusive write locks for two potentially different shards.
// It implements a Strict Lock Ordering (always lock the lower shard index first)
// to mathematically guarantee that Deadlocks cannot happen during cross-shard operations.
func (db *DB) LockTwoShards(id1, id2 string) (*GraphShard, *GraphShard) {
	s1 := GetShardIndex(id1)
	s2 := GetShardIndex(id2)

	if s1 < s2 {
		db.graphShards[s1].mu.Lock()
		db.graphShards[s2].mu.Lock()
	} else if s2 < s1 {
		db.graphShards[s2].mu.Lock()
		db.graphShards[s1].mu.Lock()
	} else {
		// Both IDs hash to the same shard. A single lock is sufficient.
		db.graphShards[s1].mu.Lock()
	}

	return &db.graphShards[s1], &db.graphShards[s2]
}

// UnlockTwoShards releases the locks acquired by LockTwoShards.
// The order of unlocking does not affect deadlock prevention, but we keep it clean.
func (db *DB) UnlockTwoShards(id1, id2 string) {
	s1 := GetShardIndex(id1)
	s2 := GetShardIndex(id2)

	db.graphShards[s1].mu.Unlock()
	if s1 != s2 {
		db.graphShards[s2].mu.Unlock()
	}
}

// GetGraphShard returns a specific shard for read-only or single-node operations.
func (db *DB) GetGraphShard(id string) *GraphShard {
	idx := GetShardIndex(id)
	return &db.graphShards[idx]
}

// --- Write Operations ---

// AddEdge creates or updates a directed edge from sourceID to targetID.
// It automatically updates the reverse index (InEdges) on the target node.
func (db *DB) AddEdge(sourceID, targetID, relationType string, weight float32, props []byte, timestamp int64) {
	shardSource, shardTarget := db.LockTwoShards(sourceID, targetID)
	defer db.UnlockTwoShards(sourceID, targetID)

	// 1. Ensure Nodes exist
	sourceNode, exists := shardSource.nodes[sourceID]
	if !exists {
		sourceNode = NewGraphNode(sourceID)
		shardSource.nodes[sourceID] = sourceNode
	}
	targetNode, exists := shardTarget.nodes[targetID]
	if !exists {
		targetNode = NewGraphNode(targetID)
		shardTarget.nodes[targetID] = targetNode
	}

	// 2. Update Forward Edge (Source -> Target)
	outList := sourceNode.OutEdges[relationType]
	activeIdx := -1

	// Cerca l'arco correntemente attivo
	for i := range outList {
		if outList[i].TargetID == targetID && outList[i].DeletedAt == 0 {
			activeIdx = i
			break
		}
	}

	if activeIdx >= 0 {
		// EVOLUZIONE: L'arco esiste. È cambiato qualcosa?
		propsChanged := !bytes.Equal(outList[activeIdx].Props, props)
		weightChanged := outList[activeIdx].Weight != weight

		if propsChanged || weightChanged {
			// CHANGE DETECTED -> Soft delete del vecchio, Append del nuovo
			outList[activeIdx].DeletedAt = timestamp
			sourceNode.OutEdges[relationType] = append(outList, GraphEdge{
				TargetID:  targetID,
				CreatedAt: timestamp,
				Weight:    weight,
				Props:     props,
			})
		}
		// Se i dati sono identici, è un no-op (Idempotenza, fa felice il test!)
	} else {
		// Non c'è un arco attivo, lo creiamo
		sourceNode.OutEdges[relationType] = append(outList, GraphEdge{
			TargetID:  targetID,
			CreatedAt: timestamp,
			Weight:    weight,
			Props:     props,
		})
	}

	// 3. Update Reverse Edge (Target <- Source)
	// Essendo leggero e usato solo per topologia, l'InEdge basta che esista attivo.
	inList := targetNode.InEdges[relationType]
	foundIn := false
	for i := range inList {
		if inList[i].SourceID == sourceID && inList[i].DeletedAt == 0 {
			foundIn = true
			break
		}
	}
	if !foundIn {
		targetNode.InEdges[relationType] = append(inList, ReverseEdge{
			SourceID:  sourceID,
			CreatedAt: timestamp,
		})
	}
}

// RemoveEdge removes a directed edge.
// If hardDelete is true, it physically filters the slice (O(N)).
// If hardDelete is false, it uses Soft Delete (O(1) in-place update) for Time Travel support.
func (db *DB) RemoveEdge(sourceID, targetID, relationType string, hardDelete bool, timestamp int64) {
	shardSource, shardTarget := db.LockTwoShards(sourceID, targetID)
	defer db.UnlockTwoShards(sourceID, targetID)

	sourceNode, ok1 := shardSource.nodes[sourceID]
	targetNode, ok2 := shardTarget.nodes[targetID]

	// 1. Remove Forward Edge
	if ok1 {
		if outList, ok := sourceNode.OutEdges[relationType]; ok {
			if hardDelete {
				// Filter in-place without allocating a new slice
				newOut := outList[:0]
				for _, edge := range outList {
					if edge.TargetID != targetID {
						newOut = append(newOut, edge)
					}
				}
				sourceNode.OutEdges[relationType] = newOut
			} else {
				// Soft Delete
				for i := range outList {
					if outList[i].TargetID == targetID {
						outList[i].DeletedAt = timestamp
						break
					}
				}
			}
		}
	}

	// 2. Remove Reverse Edge
	if ok2 {
		if inList, ok := targetNode.InEdges[relationType]; ok {
			if hardDelete {
				newIn := inList[:0]
				for _, edge := range inList {
					if edge.SourceID != sourceID {
						newIn = append(newIn, edge)
					}
				}
				targetNode.InEdges[relationType] = newIn
			} else {
				// Soft Delete
				for i := range inList {
					if inList[i].SourceID == sourceID {
						inList[i].DeletedAt = timestamp
						break
					}
				}
			}
		}
	}
}

// --- Read Operations (Zero-Copy & Thread-Safe) ---

// GetOutEdges returns all outgoing edges for a node of a specific type.
// It applies Time Travel filtering if atTime > 0.
// Returns a COPY of the active edges to prevent race conditions during iteration.
func (db *DB) GetOutEdges(sourceID, relationType string, atTime int64) ([]GraphEdge, bool) {
	shard := db.GetGraphShard(sourceID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	node, exists := shard.nodes[sourceID]
	if !exists {
		return nil, false
	}

	outList, exists := node.OutEdges[relationType]
	if !exists || len(outList) == 0 {
		return nil, false
	}

	// Filter active edges and copy them
	var activeEdges []GraphEdge
	for _, edge := range outList {
		if isActiveAtTime(edge.CreatedAt, edge.DeletedAt, atTime) {
			activeEdges = append(activeEdges, edge)
		}
	}

	return activeEdges, len(activeEdges) > 0
}

// GetInEdges returns all incoming source IDs for a target node.
// Resolves the Reverse Index instantly.
func (db *DB) GetInEdges(targetID, relationType string, atTime int64) ([]string, bool) {
	shard := db.GetGraphShard(targetID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	node, exists := shard.nodes[targetID]
	if !exists {
		return nil, false
	}

	inList, exists := node.InEdges[relationType]
	if !exists || len(inList) == 0 {
		return nil, false
	}

	var activeSources []string
	for _, edge := range inList {
		if isActiveAtTime(edge.CreatedAt, edge.DeletedAt, atTime) {
			activeSources = append(activeSources, edge.SourceID)
		}
	}

	return activeSources, len(activeSources) > 0
}

// GetAllRelations returns a map of all active relationships for a node.
// direction can be "out" or "in".
// Complexity: O(1) shard lock + slice iteration. Massively faster than prefix scan.
func (db *DB) GetAllRelations(nodeID string, direction string) map[string][]string {
	shard := db.GetGraphShard(nodeID)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	node, exists := shard.nodes[nodeID]
	if !exists {
		return nil
	}

	result := make(map[string][]string)

	if direction == "out" || direction == "both" {
		for relType, edges := range node.OutEdges {
			var targets []string
			for _, edge := range edges {
				if edge.DeletedAt == 0 { // Only current active
					targets = append(targets, edge.TargetID)
				}
			}
			if len(targets) > 0 {
				result[relType] = targets
			}
		}
	}

	if direction == "in" || direction == "both" {
		for relType, edges := range node.InEdges {
			var sources []string
			for _, edge := range edges {
				if edge.DeletedAt == 0 {
					sources = append(sources, edge.SourceID)
				}
			}
			if len(sources) > 0 {
				result[relType] = sources
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// isActiveAtTime checks if an edge is valid given a query timestamp.
func isActiveAtTime(createdAt, deletedAt, queryTime int64) bool {
	if queryTime == 0 {
		// Normal query (Now): keep if never deleted
		return deletedAt == 0
	}
	// Time Travel: Must exist before queryTime AND (not deleted OR deleted AFTER queryTime)
	if createdAt <= queryTime {
		if deletedAt == 0 || deletedAt > queryTime {
			return true
		}
	}
	return false
}

// VacuumGraph physically removes edges that have been soft-deleted for longer than the cutoff time.
// It returns the number of edges physically deleted.
// It cleans up empty nodes to prevent memory leaks over time.
func (db *DB) VacuumGraph(cutoffTime int64) int {
	deletedCount := 0

	for i := 0; i < NumGraphShards; i++ {
		shard := &db.graphShards[i]
		shard.mu.Lock()

		for nodeID, node := range shard.nodes {
			// Prune OutEdges
			for rel, edges := range node.OutEdges {
				newOut := edges[:0] // In-place filtering pattern
				for _, e := range edges {
					if e.DeletedAt == 0 || e.DeletedAt > cutoffTime {
						newOut = append(newOut, e)
					} else {
						deletedCount++
					}
				}
				if len(newOut) == 0 {
					delete(node.OutEdges, rel)
				} else {
					node.OutEdges[rel] = newOut
				}
			}

			// Prune InEdges
			for rel, edges := range node.InEdges {
				newIn := edges[:0]
				for _, e := range edges {
					if e.DeletedAt == 0 || e.DeletedAt > cutoffTime {
						newIn = append(newIn, e)
					}
				}
				if len(newIn) == 0 {
					delete(node.InEdges, rel)
				} else {
					node.InEdges[rel] = newIn
				}
			}

			// If the node has no more edges in any direction, we can remove it entirely from RAM
			if len(node.OutEdges) == 0 && len(node.InEdges) == 0 {
				delete(shard.nodes, nodeID)
			}
		}
		shard.mu.Unlock()
	}

	return deletedCount
}
