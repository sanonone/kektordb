// This file implements the operational methods of the Engine, wrapping core
// database actions (KV set/get, Vector add/search) with persistence logic.
// It ensures that every modification is written to the Append-Only File (AOF)
// before or after being applied to the in-memory state, maintaining data durability.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/metrics"
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

	metrics.TotalVectors.WithLabelValues(key).Dec()

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// IndexExists checks if an index with the given name exists in the database.
func (e *Engine) IndexExists(name string) bool {
	_, found := e.DB.GetVectorIndex(name)
	return found
}

// --- Vector Operations ---

// VCreate initializes a new vector index with the specified configuration.
//
// 'metric' defines the distance function (e.g., distance.Cosine, distance.Euclidean).
// 'prec' defines the storage precision (float32, float16, int8).
// 'lang' enables hybrid search features (e.g., "english", "italian") or "" to disable.
// accept AutoLinkRules.
//
// Returns an error if an index with the same name already exists.
func (e *Engine) VCreate(name string, metric distance.DistanceMetric, m, efC int, prec distance.PrecisionType, lang string, config *hnsw.AutoMaintenanceConfig, autoLinks []hnsw.AutoLinkRule, memoryConfig *hnsw.MemoryConfig) error {
	// 1. Prepare AOF Command Arguments
	args := [][]byte{
		[]byte(name),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(m)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efC)),
		[]byte("PRECISION"), []byte(prec),
		[]byte("TEXT_LANGUAGE"), []byte(lang),
	}

	// 2. Serialize AutoLinks to JSON for AOF
	if len(autoLinks) > 0 {
		rulesBytes, err := json.Marshal(autoLinks)
		if err == nil {
			args = append(args, []byte("AUTO_LINKS"), rulesBytes)
		}
	}

	// Serialize MemoryConfig
	if memoryConfig != nil && memoryConfig.Enabled {
		memBytes, err := json.Marshal(memoryConfig)
		if err == nil {
			args = append(args, []byte("MEMORY_CONFIG"), memBytes)
		}
	}

	// 3. Write to AOF
	cmd := persistence.FormatCommand("VCREATE", args...)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	// 4. Create Index in Memory
	err := e.DB.CreateVectorIndex(name, metric, m, efC, prec, lang)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)

		// 5. Apply Configurations
		idx, _ := e.DB.GetVectorIndex(name)
		// Safe assertion (we know it's HNSW)
		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			// Apply Maintenance Config
			if config != nil {
				hnswIdx.UpdateMaintenanceConfig(*config)
				// Persist config separately (legacy behavior kept for compatibility)
				cfgBytes, _ := json.Marshal(*config)
				cmdConfig := persistence.FormatCommand("VCONFIG", []byte(name), cfgBytes)
				e.AOF.Write(cmdConfig)
			}

			// Apply AutoLink Rules
			if len(autoLinks) > 0 {
				hnswIdx.SetAutoLinks(autoLinks)
			}

			// Apply Memory Config
			if memoryConfig != nil {
				hnswIdx.SetMemoryConfig(*memoryConfig)
			}
		}

		// Instant flush
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

	// --- MEMORY TIMESTAMPING ---
	// If this index is a Memory, ensure data has a creation timestamp.
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		memCfg := hnswIdx.GetMemoryConfig()
		if memCfg.Enabled {
			if metadata == nil {
				metadata = make(map[string]any)
			}
			// Only inject if missing (allows importing historical data)
			if _, exists := metadata["_created_at"]; !exists {
				// We use float64 because JSON unmarshaling treats numbers as floats by default.
				// Storing it as float64 now avoids type assertion headaches later.
				metadata["_created_at"] = float64(time.Now().Unix())
			}
		}
	}

	// --- ZERO VECTOR LOGIC ---
	if len(vector) == 0 {
		// We need to fetch the HNSW index to ask for dimension
		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			dim := hnswIdx.GetDimension()
			if dim == 0 {
				return fmt.Errorf("cannot add entity without vector to an empty index (dimension unknown)")
			}
			// Create a zero-vector of the correct size
			vector = make([]float32, dim)
		} else {
			return fmt.Errorf("index type does not support inference")
		}
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

	metrics.TotalVectors.WithLabelValues(indexName).Inc()

	atomic.AddInt64(&e.dirtyCounter, 1)

	// AUTO-LINKING (NEW)
	// We check if the index has rules and apply them.
	// Safe casting to HNSW index to retrieve rules.
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		rules := hnswIdx.GetAutoLinks()
		if len(rules) > 0 {
			// This performs VLink calls which have their own locking (adminMu).
			// VAdd holds no locks at this point, so it is safe.
			e.processAutoLinks(indexName, id, metadata, rules)
		}
	}

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
func (e *Engine) VSearch(indexName string, query []float32, k int, filter string, explicitTextQuery string, efSearch int, alpha float64, graphQuery *GraphQuery) ([]string, error) {
	// Calls internal helper
	results, err := e.searchWithFusion(indexName, query, k, filter, explicitTextQuery, efSearch, alpha, graphQuery)
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
func (e *Engine) VSearchGraph(indexName string, query []float32, k int, filter string, explicitTextQuery string, efSearch int, alpha float64, relations []string, hydrate bool, graphQuery *GraphQuery) ([]GraphSearchResult, error) {
	// 1. Core Search
	rawResults, err := e.searchWithFusion(indexName, query, k, filter, explicitTextQuery, efSearch, alpha, graphQuery)
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
func (e *Engine) searchWithFusion(indexName string, query []float32, k int, filter string, explicitTextQuery string, efSearch int, alpha float64, graphQuery *GraphQuery) ([]fusedResult, error) {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("index is not HNSW")
	}

	// Parsing Filters
	var booleanFilters, textQuery, textQueryField string

	if explicitTextQuery != "" {
		booleanFilters = filter // Il filtro è puro (es. category='A')
		textQuery = explicitTextQuery
		// Auto-detect text field instead of hardcoded "content"
		textQueryField = e.detectTextFieldForIndex(indexName)
		if textQueryField == "" {
			slog.Warn("[Hybrid Search] No text field found in index, falling back to vector-only search",
				"index", indexName,
				"hint", "Make sure metadata contains one of: content, text, page_content, body, description")
			textQuery = "" // Disable hybrid search
		}
	} else {
		// Fallback alla vecchia logica CONTAINS
		booleanFilters, textQuery, textQueryField = parseHybridFilter(filter)
	}

	// Pre-Filtering
	// metadata allowlist
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		allowList, err = e.DB.FindIDsByFilter(indexName, booleanFilters)
		if err != nil {
			return nil, fmt.Errorf("invalid filter: %w", err)
		}
	}

	// Graph AllowList (NEW)
	if graphQuery != nil && graphQuery.RootID != "" {
		graphAllowList, err := e.resolveGraphFilter(indexName, *graphQuery)
		if err != nil {
			return nil, err
		}

		// INTERSECTION Logic
		if allowList == nil {
			// No metadata filter, just use graph filter
			allowList = graphAllowList
		} else {
			// Both filters exist: calculate Intersection (AND)
			// Keep only IDs present in BOTH lists
			intersected := make(map[uint32]struct{})
			for id := range allowList {
				if _, ok := graphAllowList[id]; ok {
					intersected[id] = struct{}{}
				}
			}
			allowList = intersected
		}

		// If intersection resulted in empty set, return immediately
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

	// Diagnostic Logging (Fix #5)
	slog.Info("[Hybrid Search] Search completed",
		"vector_results", len(vectorResults),
		"text_results", len(textResults),
		"text_field", textQueryField,
		"alpha", alpha,
	)

	// Warning if text search failed
	if len(textResults) == 0 && textQuery != "" {
		slog.Warn("[Hybrid Search] Text search returned 0 results",
			"query", textQuery,
			"field", textQueryField,
			"hint", "Check if field exists in metadata and is properly indexed")
	}

	// Debug: Show top results before normalization (for tuning)
	if len(vectorResults) > 0 && slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		slog.Debug("[Hybrid Search] Top 3 vector results (raw distances)")
		for i := 0; i < min(3, len(vectorResults)); i++ {
			slog.Debug(fmt.Sprintf("  #%d distance: %.4f", i+1, vectorResults[i].Score))
		}
	}
	if len(textResults) > 0 && slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		slog.Debug("[Hybrid Search] Top 3 text results (raw BM25 scores)")
		for i := 0; i < min(3, len(textResults)); i++ {
			slog.Debug(fmt.Sprintf("  #%d score: %.4f", i+1, textResults[i].Score))
		}
	}

	// --- SETUP MEMORY PARAMETERS ---
	var useDecay bool
	var halfLife float64

	memCfg := hnswIndex.GetMemoryConfig()
	if memCfg.Enabled {
		useDecay = true
		halfLife = float64(time.Duration(memCfg.DecayHalfLife).Seconds())
		if halfLife <= 0 {
			halfLife = 604800
		}
	}

	// --- FUSION PREPARATION ---
	// Normalize scores BEFORE fusion
	normalizeVectorScores(vectorResults)
	if textQuery != "" {
		normalizeTextScores(textResults)
	}

	// Initialize Fused Map
	fusedScores := make(map[uint32]float64)

	// CASE A: Only Vector (Common case)
	if textQuery == "" {
		for _, res := range vectorResults {
			fusedScores[res.DocID] = res.Score // Alpha is implicitly 1.0 here relative to text
		}
	} else {
		// CASE B: Hybrid
		if alpha < 0 || alpha > 1 {
			alpha = 0.5
		}
		for _, res := range vectorResults {
			fusedScores[res.DocID] += alpha * res.Score
		}
		for _, res := range textResults {
			fusedScores[res.DocID] += (1 - alpha) * res.Score
		}
	}

	// --- TIME DECAY APPLICATION (Common for both cases) ---
	if useDecay {
		e.DB.RLock()
		for docID, score := range fusedScores {
			meta := e.DB.GetMetadataForNodeUnlocked(indexName, docID)

			if val, ok := meta["_created_at"]; ok {
				var created float64
				switch v := val.(type) {
				case float64:
					created = v
				case int64:
					created = float64(v)
				case int:
					created = float64(v)
				}

				if created > 0 {
					factor := calculateTimeDecay(created, halfLife) // Uses global func
					// Recalculate age here if calculateTimeDecay doesn't accept 'now'
					// Or update calculateTimeDecay to take (now - created)

					// Let's assume calculateTimeDecay(created, halfLife) does:
					// age = time.Now().Unix() - created.
					fusedScores[docID] = score * factor
				}
			}
		}
		e.DB.RUnlock()
	}

	// --- FINALIZE ---
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
					_ = e.VUnlink(sourceID, deadID, relationType, "", false)
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

// VSearchWithScores performs a search and returns results with their scores.
// If the index has MemoryConfig enabled, it applies time decay ranking.
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

	// Convert distance to score (1 / (1 + distance))
	for i := range internalResults {
		internalResults[i].Score = 1.0 / (1.0 + internalResults[i].Score)
	}

	// Apply time decay if memory config is enabled
	memCfg := hnswIdx.GetMemoryConfig()
	if memCfg.Enabled {
		halfLife := float64(time.Duration(memCfg.DecayHalfLife).Seconds())
		if halfLife <= 0 {
			halfLife = 604800 // 7 days default
		}

		e.DB.RLock()
		for i := range internalResults {
			meta := e.DB.GetMetadataForNodeUnlocked(indexName, internalResults[i].DocID)
			if val, ok := meta["_created_at"]; ok {
				var created float64
				switch v := val.(type) {
				case float64:
					created = v
				case int64:
					created = float64(v)
				case int:
					created = float64(v)
				}

				if created > 0 {
					factor := calculateTimeDecay(created, halfLife)
					internalResults[i].Score *= factor
				}
			}
		}
		e.DB.RUnlock()
	}

	// Sort by score (highest first)
	for i := 0; i < len(internalResults)-1; i++ {
		for j := i + 1; j < len(internalResults); j++ {
			if internalResults[j].Score > internalResults[i].Score {
				internalResults[i], internalResults[j] = internalResults[j], internalResults[i]
			}
		}
	}

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

	// --- MEMORY TIMESTAMPING ---
	memCfg := hnswIdx.GetMemoryConfig()
	if memCfg.Enabled {
		now := float64(time.Now().Unix())
		for i := range items {
			if items[i].Metadata == nil {
				items[i].Metadata = make(map[string]any)
			}
			if _, exists := items[i].Metadata["_created_at"]; !exists {
				items[i].Metadata["_created_at"] = now
			}
		}
	}

	// --- ZERO VECTOR LOGIC ---

	// 1. Determine Target Dimension
	targetDim := hnswIdx.GetDimension()

	// If index is empty (0), try to find dimension from the batch itself
	if targetDim == 0 {
		for _, item := range items {
			if len(item.Vector) > 0 {
				targetDim = len(item.Vector)
				break
			}
		}
	}

	// 2. Fix vectors in the batch
	for i := range items {
		// Use a pointer to the item in the slice to ensure modification applies
		item := &items[i]

		if len(item.Vector) == 0 {
			if targetDim == 0 {
				return fmt.Errorf("cannot add zero-vector entity: index is empty and batch contains no reference vectors")
			}
			// Allocate zero vector
			item.Vector = make([]float32, targetDim)
		} else {
			// Validation: Ensure provided vector matches dimension
			if targetDim > 0 && len(item.Vector) != targetDim {
				return fmt.Errorf("dimension mismatch for item %s: expected %d, got %d", item.Id, targetDim, len(item.Vector))
			}
		}
	}

	// 1. Memory Batch
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Persistence Loop & Metadata
	e.DB.Lock()
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id, _ := hnswIdx.GetInternalID(item.Id)
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

	metrics.TotalVectors.WithLabelValues(indexName).Add(float64(len(items)))

	atomic.AddInt64(&e.dirtyCounter, int64(len(items)))

	// AUTO-LINKING (NEW)
	// Retrieve rules once
	rules := hnswIdx.GetAutoLinks()
	if len(rules) > 0 {
		for _, item := range items {
			if len(item.Metadata) > 0 {
				e.processAutoLinks(indexName, item.Id, item.Metadata, rules)
			}
		}
	}

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
			id, _ := hnswIdx.GetInternalID(item.Id)
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

// detectTextFieldForIndex identifies which field should be used for text search.
// It checks a list of common field names against the text index.
func (e *Engine) detectTextFieldForIndex(indexName string) string {
	// 1. Get the text index map for this index
	// Note: We access DB structure directly. Ensure lock if necessary,
	// but textIndex map reference itself is stable usually.
	// DB.textIndex is map[string]map[string]map[string]PostingList
	// i.e., map[indexName][fieldName][token]...

	e.DB.RLock()
	defer e.DB.RUnlock()

	idxTextFields, ok := e.DB.GetTextIndexMap(indexName)
	if !ok || len(idxTextFields) == 0 {
		return ""
	}

	// 2. Priority list of fields
	candidates := []string{"content", "text", "page_content", "body", "description", "summary"}

	for _, field := range candidates {
		if _, exists := idxTextFields[field]; exists {
			slog.Debug("[Hybrid Search] Auto-detected text field", "index", indexName, "field", field)
			return field
		}
	}

	// 3. Fallback: return the first available field if any
	for field := range idxTextFields {
		slog.Debug("[Hybrid Search] Fallback to first available text field", "index", indexName, "field", field)
		return field
	}

	return ""
}

// processAutoLinks evaluates the index rules against the provided metadata
// and creates graph connections automatically.
// It logs errors but does not stop execution (best effort).
func (e *Engine) processAutoLinks(indexName, sourceID string, metadata map[string]any, rules []hnsw.AutoLinkRule) {
	if len(metadata) == 0 || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		// Check if the metadata field exists
		val, ok := metadata[rule.MetadataField]
		if !ok {
			continue
		}

		// The target ID must be a string.
		// If it's a number/bool, we convert it to string format.
		targetID := fmt.Sprintf("%v", val)
		if targetID == "" {
			continue
		}

		// Create the Link (Bidirectional by default via VLink)
		// Source -> (Relation) -> Target
		// Example: Chunk_1 -> (belongs_to_chat) -> Chat_123
		if err := e.VLink(sourceID, targetID, rule.RelationType, "", 1.0, nil); err != nil {
			slog.Warn("Auto-linking failed",
				"index", indexName,
				"source", sourceID,
				"target", targetID,
				"rel", rule.RelationType,
				"error", err)
		}

		// NOTE: 'rule.CreateNode' is ignored for now.
		// VLink implicitly creates the graph node in the KV store.
		// If we wanted to create a searchable vector node, we would need to call VAdd
		// with a zero-vector, which might pollute the index.
	}
}
