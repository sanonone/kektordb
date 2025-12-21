// This file implements the operational methods of the Engine, wrapping core
// database actions (KV set/get, Vector add/search) with persistence logic.
// It ensures that every modification is written to the Append-Only File (AOF)
// before or after being applied to the in-memory state, maintaining data durability.
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// --- KV Operations ---

// KVSet stores a key-value pair in the database.
// The operation is persisted to the AOF log.
func (e *Engine) KVSet(key string, value []byte) error {
	// 1. AOF
	cmd := persistence.FormatCommand("SET", []byte(key), value)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error (AOF write failed): %w", err)
	}

	// 2. Memory
	e.DB.GetKVStore().Set(key, value)

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// KVGet retrieves a value from the key-value store.
// Returns the value and a boolean indicating if the key was found.
func (e *Engine) KVGet(key string) ([]byte, bool) {
	return e.DB.GetKVStore().Get(key)
}

// KVDelete removes a key from the key-value store.
// The operation is persisted to the AOF log.
func (e *Engine) KVDelete(key string) error {
	cmd := persistence.FormatCommand("DEL", []byte(key))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error (AOF write failed): %w", err)
	}
	e.DB.GetKVStore().Delete(key)

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// --- Vector Operations ---

// VCreate initializes a new vector index with the specified configuration.
//
// 'metric' defines the distance function (e.g., distance.Cosine, distance.Euclidean).
// 'prec' defines the storage precision (float32, float16, int8).
// 'lang' enables hybrid search features (e.g., "english", "italian") or "" to disable.
//
// Returns an error if an index with the same name already exists.
func (e *Engine) VCreate(name string, metric distance.DistanceMetric, m, efC int, prec distance.PrecisionType, lang string, config *hnsw.AutoMaintenanceConfig) error {
	cmd := persistence.FormatCommand("VCREATE",
		[]byte(name),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(m)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efC)),
		[]byte("PRECISION"), []byte(prec),
		[]byte("TEXT_LANGUAGE"), []byte(lang),
	)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	err := e.DB.CreateVectorIndex(name, metric, m, efC, prec, lang)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)

		if config != nil {
			idx, _ := e.DB.GetVectorIndex(name)
			// Safe type assertion because CreateVectorIndex always creates HNSW
			hnswIdx := idx.(*hnsw.Index)

			// Apply in RAM
			hnswIdx.UpdateMaintenanceConfig(*config)

			// Write VCONFIG command to AOF
			cfgBytes, err := json.Marshal(*config)
			if err == nil {
				cmdConfig := persistence.FormatCommand("VCONFIG", []byte(name), cfgBytes)
				e.AOF.Write(cmdConfig)
			}
		}

		// Instant flush for single operations (durability)
		if errF := e.AOF.Flush(); errF != nil {
			return fmt.Errorf("CRITICAL: persistence flush failed: %w", errF)
		}

	}
	return err
}

// VDeleteIndex completely removes an index and all its data.
// The operation is persisted to the AOF log as a VDROP command.
func (e *Engine) VDeleteIndex(name string) error {
	cmd := persistence.FormatCommand("VDROP", []byte(name))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	err := e.DB.DeleteVectorIndex(name)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)

		// Instant flush for single operations (durability)
		if errF := e.AOF.Flush(); errF != nil {
			return fmt.Errorf("CRITICAL: persistence flush failed: %w", errF)
		}

	}
	return err
}

// --- Vector Data Operations ---

// VAdd inserts or updates a single vector in the specified index.
//
// This operation updates the in-memory graph immediately and appends the operation
// to the AOF (Append-Only File) for durability.
//
// 'metadata' can be nil. If provided, it enables filtering and hybrid search.
func (e *Engine) VAdd(indexName, id string, vector []float32, metadata map[string]any) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// 1. Memory Add
	internalID, err := idx.Add(id, vector)
	if err != nil {
		return err
	}

	// 2. Metadata
	if len(metadata) > 0 {
		e.DB.AddMetadata(indexName, internalID, metadata)
	}

	// 3. Persistence
	vecStr := float32SliceToString(vector)
	var metaBytes []byte
	if len(metadata) > 0 {
		var err error
		metaBytes, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(id), []byte(vecStr), metaBytes)
	if err := e.AOF.Write(cmd); err != nil {
		// Warn: Persistence failed but memory success. Inconsistency risk.
		return fmt.Errorf("CRITICAL: persistence failed (data in RAM only): %w", err)
	}

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VDelete marks a vector as deleted in the specified index.
// The node remains in the graph but is excluded from search results.
func (e *Engine) VDelete(indexName, id string) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// Memory
	idx.Delete(id)

	// Disk
	cmd := persistence.FormatCommand("VDEL", []byte(indexName), []byte(id))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VGet retrieves the full data (vector + metadata) for a single ID.
func (e *Engine) VGet(indexName, id string) (core.VectorData, error) {
	return e.DB.GetVector(indexName, id)
}

// VGetMany retrieves data for multiple IDs in parallel.
func (e *Engine) VGetMany(indexName string, ids []string) ([]core.VectorData, error) {
	return e.DB.GetVectors(indexName, ids)
}

// GraphNode represents a node in the graph with its data and nested connections.
// This allows representing trees like Chunk -> Parent -> Children.
type GraphNode struct {
	core.VectorData
	// Nested relationship map: "child" -> [List of Nodes]
	Connections map[string][]GraphNode `json:"connections,omitempty"`
}

// Public structure for graph results
type GraphSearchResult struct {
	ID    string    `json:"id"`
	Score float64   `json:"score"`
	Node  GraphNode `json:"node"`
}

// VSearch performs a vector, text, or hybrid search.
//
// 'k': number of results to return.
// 'filter': supports SQL-like metadata filtering and "CONTAINS(field, 'text')".
// 'efSearch': tuning parameter for HNSW accuracy (0 = default).
// 'alpha': weight for hybrid fusion (1.0 = vector only, 0.0 = text only, 0.5 = balanced).
//
// Returns a list of external IDs sorted by relevance.
func (e *Engine) VSearch(indexName string, query []float32, k int, filter string, efSearch int, alpha float64) ([]string, error) {
	// Calls internal helper
	results, err := e.searchWithFusion(indexName, query, k, filter, efSearch, alpha)
	if err != nil {
		return nil, err
	}

	// Map only to IDs
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.id
	}
	return ids, nil
}

// VSearchGraph performs a search and traverses the graph based on relation paths.
// relations example: ["prev", "next", "parent.child"]
func (e *Engine) VSearchGraph(indexName string, query []float32, k int, filter string, efSearch int, alpha float64, relations []string, hydrate bool) ([]GraphSearchResult, error) {
	// 1. Core Search
	rawResults, err := e.searchWithFusion(indexName, query, k, filter, efSearch, alpha)
	if err != nil {
		return nil, err
	}

	output := make([]GraphSearchResult, len(rawResults))

	// 2. Traversal
	for i, res := range rawResults {
		// Retrieve root node data
		rootData, err := e.VGet(indexName, res.id)
		if err != nil {
			// If root node is missing (rare), use placeholder or skip
			rootData = core.VectorData{ID: res.id}
		}

		rootNode := GraphNode{VectorData: rootData}

		// If requested, navigate relationships
		if len(relations) > 0 {
			rootNode.Connections = make(map[string][]GraphNode)

			// For each requested path (e.g., "parent.child")
			for _, path := range relations {
				// Split the path: ["parent", "child"]
				parts := strings.Split(path, ".")

				// Execute recursive traversal
				connectedNodes := e.traversePath(indexName, res.id, parts, hydrate)

				// Add to result using full path name as key
				// or last step? Better to use full path for clarity in JSON.
				if len(connectedNodes) > 0 {
					rootNode.Connections[path] = connectedNodes
				}
			}
		}

		output[i] = GraphSearchResult{
			ID:    res.id,
			Score: res.score,
			Node:  rootNode,
		}
	}

	return output, nil
}

// VTraverse performs a deep graph traversal starting from a specific node ID.
// Unlike VSearchGraph, this does not perform a vector search but starts from a known ID.
// paths example: ["parent", "parent.child", "next"]
func (e *Engine) VTraverse(indexName, startID string, paths []string) (*GraphNode, error) {
	// 1. Retrieve root node
	rootVectorData, err := e.VGet(indexName, startID)
	if err != nil {
		return nil, err
	}

	// Construct root node
	rootNode := &GraphNode{
		VectorData: rootVectorData,
	}

	// 2. If no paths, return only the node
	if len(paths) == 0 {
		return rootNode, nil
	}

	// 3. Recursive Navigation
	rootNode.Connections = make(map[string][]GraphNode)

	for _, pathStr := range paths {
		// Split dot notation (e.g., "parent.child" -> ["parent", "child"])
		parts := strings.Split(pathStr, ".")

		// Use existing internal helper traversePath
		// Note: traversePath must be accessible (same package)
		connectedNodes := e.traversePath(indexName, startID, parts, true) // true = always hydrate for traverse

		if len(connectedNodes) > 0 {
			rootNode.Connections[pathStr] = connectedNodes
		}
	}

	return rootNode, nil
}

// traversePath walks the graph recursively.
// currentID: ID of the starting node
// path: list of relationships to follow (e.g., ["parent", "child"])
func (e *Engine) traversePath(indexName, currentID string, path []string, hydrate bool) []GraphNode {
	if len(path) == 0 {
		return nil
	}

	relType := path[0]        // "parent"
	remainingPath := path[1:] // ["child"]

	// 1. Get Links (IDs)
	targetIDs, found := e.VGetLinks(currentID, relType)
	if !found || len(targetIDs) == 0 {
		return nil
	}

	// 2. Fetch Data (Hydration)
	var nodesData []core.VectorData
	if hydrate {
		nodesData, _ = e.DB.GetVectors(indexName, targetIDs)
	} else {
		// Mock data with only ID
		for _, id := range targetIDs {
			nodesData = append(nodesData, core.VectorData{ID: id})
		}
	}

	results := make([]GraphNode, 0, len(nodesData))

	// 3. Recurse (Deep Traversal)
	for _, nodeData := range nodesData {
		gNode := GraphNode{VectorData: nodeData}

		if len(remainingPath) > 0 {
			// Descend to next level
			children := e.traversePath(indexName, nodeData.ID, remainingPath, hydrate)
			if len(children) > 0 {
				if gNode.Connections == nil {
					gNode.Connections = make(map[string][]GraphNode)
				}
				// Use remaining path as key (e.g., "child")
				gNode.Connections[strings.Join(remainingPath, ".")] = children
			}
		}
		results = append(results, gNode)
	}

	return results
}

// Contains all Parsing, Filtering, Hybrid Fusion logic
func (e *Engine) searchWithFusion(indexName string, query []float32, k int, filter string, efSearch int, alpha float64) ([]fusedResult, error) {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("index is not HNSW")
	}

	// Parsing Filters
	booleanFilters, textQuery, textQueryField := parseHybridFilter(filter)

	// Pre-Filtering
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		allowList, err = e.DB.FindIDsByFilter(indexName, booleanFilters)
		if err != nil {
			return nil, fmt.Errorf("invalid filter: %w", err)
		}
		if len(allowList) == 0 {
			return []fusedResult{}, nil
		}
	}

	// Check Vector Query
	isVectorQueryEmpty := true
	if len(query) > 0 {
		for _, v := range query {
			if v != 0 {
				isVectorQueryEmpty = false
				break
			}
		}
	}

	// CASE A: TEXT ONLY
	if isVectorQueryEmpty && textQuery != "" {
		textResults, _ := e.DB.FindIDsByTextSearch(indexName, textQueryField, textQuery)
		var finalRes []fusedResult
		count := 0
		for _, res := range textResults {
			if count >= k {
				break
			}
			if allowList != nil {
				if _, ok := allowList[res.DocID]; !ok {
					continue
				}
			}
			extID, _ := hnswIndex.GetExternalID(res.DocID)
			finalRes = append(finalRes, fusedResult{id: extID, score: res.Score}) // Score not normalized here, but ok for text-only
			count++
		}
		return finalRes, nil
	}

	// CASE B: HYBRID / VECTOR
	var vectorResults []types.SearchResult
	var textResults []types.SearchResult
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		vectorResults = idx.SearchWithScores(query, k, allowList, efSearch)
	}()

	if textQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, _ := e.DB.FindIDsByTextSearch(indexName, textQueryField, textQuery)
			if allowList != nil {
				var filtered []types.SearchResult
				for _, res := range results {
					if _, ok := allowList[res.DocID]; ok {
						filtered = append(filtered, res)
					}
				}
				textResults = filtered
			} else {
				textResults = results
			}
		}()
	}
	wg.Wait()

	// Fusion Logic
	if textQuery == "" {
		normalizeVectorScores(vectorResults)
		finalRes := make([]fusedResult, len(vectorResults))
		for i, r := range vectorResults {
			extID, _ := hnswIndex.GetExternalID(r.DocID)
			finalRes[i] = fusedResult{id: extID, score: r.Score}
		}
		return finalRes, nil
	}

	if alpha < 0 || alpha > 1 {
		alpha = 0.5
	}

	normalizeVectorScores(vectorResults)
	normalizeTextScores(textResults)

	fusedScores := make(map[uint32]float64)
	for _, res := range vectorResults {
		fusedScores[res.DocID] += alpha * res.Score
	}
	for _, res := range textResults {
		fusedScores[res.DocID] += (1 - alpha) * res.Score
	}

	finalRes := make([]fusedResult, 0, len(fusedScores))
	for id, score := range fusedScores {
		extID, found := hnswIndex.GetExternalID(id)
		if !found {
			continue
		}
		finalRes = append(finalRes, fusedResult{id: extID, score: score})
	}

	sort.Slice(finalRes, func(i, j int) bool {
		return finalRes[i].score > finalRes[j].score
	})

	if len(finalRes) > k {
		finalRes = finalRes[:k]
	}

	return finalRes, nil

}

// VGetConnections retrieves the full data of nodes linked to a source node.
// It performs a Graph Traversal (1-hop) and Hydration in one step.
// SELF-REPAIR: If it encounters links to non-existent nodes, it removes them in background.
func (e *Engine) VGetConnections(indexName, sourceID, relationType string) ([]core.VectorData, error) {
	// 1. Retrieve link IDs from KV Store
	targetIDs, found := e.VGetLinks(sourceID, relationType)
	if !found || len(targetIDs) == 0 {
		return []core.VectorData{}, nil
	}

	// 2. Hydration (Retrieve data from HNSW/Metadata)
	results, err := e.DB.GetVectors(indexName, targetIDs)
	if err != nil {
		return nil, err
	}

	// 3. Self-Repair Logic (Lazy Cleanup)
	// If we found fewer vectors than IDs, some links are dead.
	if len(results) < len(targetIDs) {
		// Create set of found (alive) IDs
		foundSet := make(map[string]struct{}, len(results))
		for _, res := range results {
			foundSet[res.ID] = struct{}{}
		}

		// Identify dead IDs and cleanup
		for _, targetID := range targetIDs {
			if _, ok := foundSet[targetID]; !ok {
				// This ID was in links but not in DB. It is dead.
				// Launch background cleanup to avoid slowing down current read.
				go func(deadID string) {
					// VUnlink is thread-safe and handles lock and AOF
					_ = e.VUnlink(sourceID, deadID, relationType, "")
				}(targetID)
			}
		}
	}

	return results, nil
}

// SearchResult represents a match with its similarity score/distance.
type SearchResult struct {
	ID    string
	Score float64
}

// VSearchWithScores performs a search and returns results with their distances.
func (e *Engine) VSearchWithScores(indexName string, query []float32, k int) ([]SearchResult, error) {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index not found")
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("not hnsw")
	}

	internalResults := hnswIdx.SearchWithScores(query, k, nil, 0)

	results := make([]SearchResult, len(internalResults))
	for i, r := range internalResults {
		extID, _ := hnswIdx.GetExternalID(r.DocID)
		results[i] = SearchResult{
			ID:    extID,
			Score: r.Score,
		}
	}
	return results, nil
}

// VAddBatch inserts multiple vectors concurrently into an index.
// It writes to the AOF log for each vector, ensuring durability but with higher I/O overhead.
// Use VImport for faster initial loading.
func (e *Engine) VAddBatch(indexName string, items []types.BatchObject) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("not hnsw")
	}

	// 1. Memory Batch
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Persistence Loop & Metadata
	e.DB.Lock()
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id := hnswIdx.GetInternalID(item.Id)
			e.DB.AddMetadataUnlocked(indexName, id, item.Metadata) // Covered by e.DB.Lock()
		}
	}
	e.DB.Unlock()

	for _, item := range items {

		vecStr := float32SliceToString(item.Vector)
		var meta []byte
		if len(item.Metadata) > 0 {
			var err error
			meta, err = json.Marshal(item.Metadata)
			if err != nil {
				return fmt.Errorf("failed to marshal metadata for item %s: %w", item.Id, err)
			}
		}

		cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(item.Id), []byte(vecStr), meta)

		if err := e.AOF.Write(cmd); err != nil {
			return fmt.Errorf("batch persistence partial failure: %w", err)
		}
	}

	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("batch persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, int64(len(items)))
	return nil
}

// VImport performs a high-speed bulk insertion of vectors.
//
// Unlike VAddBatch, this method bypasses the AOF log to maximize throughput.
// It creates a full database Snapshot (.kdb) upon completion.
//
// Use this method for initial dataset loading or massive restores.
// Note: This operation acquires a global administrative lock to ensure consistency during the snapshot.
func (e *Engine) VImport(indexName string, items []types.BatchObject) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("index is not HNSW")
	}

	// Block administrative operations (like other saves/rewrites) during import
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// 1. Mass insertion in memory (HNSW)
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Add Metadata
	e.DB.Lock()
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id := hnswIdx.GetInternalID(item.Id)
			e.DB.AddMetadataUnlocked(indexName, id, item.Metadata)
		}
	}
	e.DB.Unlock()

	// 3. Immediate Snapshot (Bulk Persistence)
	// Uses the private version that assumes adminMu is already held.
	if err := e.saveSnapshotLocked(); err != nil {
		return fmt.Errorf("import memory success but snapshot failed: %w", err)
	}

	return nil
}

// VCompress changes the precision of an existing index (e.g., float32 -> int8).
// This operation rebuilds the index in memory.
func (e *Engine) VCompress(indexName string, newPrecision distance.PrecisionType) error {
	// Acquires the lock on DB through the method exposed by Core, if needed,
	// or delegates to DB.Compress which manages its own locks.
	return e.DB.Compress(indexName, newPrecision)
}

func (e *Engine) VUpdateIndexConfig(indexName string, config hnsw.AutoMaintenanceConfig) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("index is not HNSW")
	}

	// 1. Update Memory
	hnswIdx.UpdateMaintenanceConfig(config)

	// 2. Persistence (AOF)
	// Serialize the config to JSON to save it in the AOF command
	cfgBytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Command: VCONFIG <indexName> <jsonConfig>
	cmd := persistence.FormatCommand("VCONFIG", []byte(indexName), cfgBytes)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	// Flush for safety since this is a rare configuration change
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("persistence flush error: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

func (e *Engine) VTriggerMaintenance(indexName string, taskType string) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("index is not HNSW")
	}

	hnswIdx.MaintenanceRun(taskType) // forceType = "vacuum" or "refine"
	return nil
}
