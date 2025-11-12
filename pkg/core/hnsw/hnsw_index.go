// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// graph algorithm for efficient approximate nearest neighbor search.
//
// This file contains the core Index struct and its associated methods for building,
// searching, and managing the HNSW graph. It supports multiple distance metrics,
// data precisions (including float32, float16, and int8 quantization), and
// concurrent access.
package hnsw

import (
	"container/heap"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/x448/float16"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// Index represents the hierarchical graph structure.
type Index struct {
	// Global mutex for concurrency control. Finer-grained locking may be considered in the future.
	mu sync.RWMutex

	// HNSW algorithm parameters
	m              int // Max number of connections per node per layer > 0 [default = 16]
	mMax0          int // Max number of connections per node at layer 0
	efConstruction int // Size of the dynamic candidate list during construction/insertion [default = 200]

	// ml is a normalization factor for the level probability distribution
	ml float64

	// The ID of the first node inserted, used as the starting point for all searches and insertions
	entrypointID uint32
	// The current highest level present in the graph
	maxLevel int

	// The map containing all nodes in the graph
	// The key is an internal uint32 ID, not the string ID from the Node struct
	nodes map[uint32]*Node

	// Separate maps to translate between external and internal IDs
	externalToInternalID map[string]uint32
	internalToExternalID map[uint32]string
	internalCounter      uint32

	// Stores the precision of the index (e.g., f32, f16)
	precision distance.PrecisionType

	// Field for quantization; will be nil for non-quantized indexes.
	// It's a pointer because we need its state to be shared and mutable.
	quantizer *distance.Quantizer

	// Stores the appropriate distance function for this index.
	// Uses 'any' for flexibility with different function signatures.
	distanceFunc any

	// Random number generator for level selection
	levelRand *rand.Rand

	// Stores the distance metric type
	metric distance.DistanceMetric

	textLanguage string

	visitedPool sync.Pool
}

// New creates and initializes a new HNSW index
func New(m int, efConstruction int, metric distance.DistanceMetric, precision distance.PrecisionType, textLang string) (*Index, error) {
	// Set default values if not provided by the user
	if m <= 0 {
		m = 16 // default
	}
	if efConstruction <= 0 {
		efConstruction = 200 // default
	}

	h := &Index{
		m:                    m,
		mMax0:                m * 2, // A common heuristic is to double m for layer 0
		efConstruction:       efConstruction,
		ml:                   1.0 / math.Log(float64(m)),
		nodes:                make(map[uint32]*Node),
		externalToInternalID: make(map[string]uint32),
		internalToExternalID: make(map[uint32]string),
		internalCounter:      0,
		maxLevel:             -1, // No levels initially
		entrypointID:         0,  // Initialized on the first insertion
		levelRand:            rand.New(rand.NewSource(time.Now().UnixNano())),
		metric:               metric,
		precision:            precision,
		textLanguage:         textLang,
	}

	h.visitedPool = sync.Pool{
		New: func() any {
			return NewBitSet(256)
		},
	}

	// --- SELECTION AND VALIDATION LOGIC ---
	// Select the correct distance function based on precision and metric.
	var err error
	switch precision {
	case distance.Float32:
		h.distanceFunc, err = distance.GetFloat32Func(metric)
		if err != nil {
			return nil, err
		}
	case distance.Float16:
		// Per ora, float16 supporta solo Euclidean
		if metric != distance.Euclidean {
			return nil, fmt.Errorf("precision '%s' only supports the '%s' metric", precision, distance.Euclidean)
		}
		h.distanceFunc, err = distance.GetFloat16Func(metric)
		if err != nil {
			return nil, err
		}
	case distance.Int8:
		if metric != distance.Cosine {
			return nil, fmt.Errorf("precision '%s' only supports the '%s' metric", precision, distance.Cosine)
		}
		h.distanceFunc, err = distance.GetInt8Func(metric)
		// A quantized index requires a quantizer, which will be trained and set later.
		h.quantizer = &distance.Quantizer{}
	default:
		return nil, fmt.Errorf("unsupported precision: %s", precision)
	}

	if err != nil {
		return nil, err
	}

	return h, nil
}

// --- UPDATED CALCULATION METHODS ---

// distance calculates the distance between two stored vectors of the same type
func (h *Index) distance(v1, v2 any) (float64, error) {
	// Usiamo un type switch per chiamare la funzione corretta.
	switch fn := h.distanceFunc.(type) {
	case distance.DistanceFuncF32:
		vec1, ok1 := v1.([]float32)
		vec2, ok2 := v2.([]float32)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("vector type mismatch (expected float32)")
		}
		return fn(vec1, vec2)
	case distance.DistanceFuncF16:
		vec1, ok1 := v1.([]uint16)
		vec2, ok2 := v2.([]uint16)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("vector type mismatch (expected uint16)")
		}
		return fn(vec1, vec2)
	case distance.DistanceFuncI8:
		vec1, ok1 := v1.([]int8)
		vec2, ok2 := v2.([]int8)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("vector type mismatch (expected int8)")
		}
		dot, err := fn(vec1, vec2)

		// --- SCALING LOGIC ---
		// Calculate the "norms" of the int8 vectors to scale the result.
		var norm1, norm2 int64
		for i := range vec1 {
			norm1 += int64(vec1[i]) * int64(vec1[i])
			norm2 += int64(vec2[i]) * int64(vec2[i])
		}

		if norm1 == 0 || norm2 == 0 {
			return 1.0, nil // Max distance if a vector is zero
		}

		similarity := float64(dot) / (math.Sqrt(float64(norm1)) * math.Sqrt(float64(norm2)))

		// similarity range [-1, 1]
		if similarity > 1.0 {
			similarity = 1.0
		}
		if similarity < -1.0 {
			similarity = -1.0
		}
		return 1.0 - similarity, err
	default:
		return 0, fmt.Errorf("invalid or uninitialized distance function")
	}
}

// distanceToQuery calculates the distance between a query vector (always float32) and a stored vector
func (h *Index) distanceToQuery(query []float32, storedVector any) (float64, error) {
	queryToUse := query // Usa la query originale di default
	// If the index metric is Cosine, the query MUST be normalized
	// before the comparison to use the 1 - dotProduct
	if h.metric == distance.Cosine {
		// Create a COPY of the query before normalizing to avoid modifying the original slice
		queryCopy := make([]float32, len(query))
		copy(queryCopy, query)
		normalize(queryCopy)
		queryToUse = queryCopy // Usa la copia normalizzata per i calcoli
	}
	switch fn := h.distanceFunc.(type) {
	case distance.DistanceFuncF32:
		stored, ok := storedVector.([]float32)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch (expected float32)")
		}
		return fn(queryToUse, stored)
	case distance.DistanceFuncF16:
		// Convert the float32 query to float16
		queryF16 := make([]uint16, len(queryToUse))
		for i, v := range query {
			queryF16[i] = float16.Fromfloat32(v).Bits()
		}
		stored, ok := storedVector.([]uint16)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch (expected uint16)")
		}
		return fn(queryF16, stored)
	case distance.DistanceFuncI8:
		// Quantize the query on the fly
		if h.quantizer == nil {
			return 0, fmt.Errorf("index is quantized but quantizer is missing")
		}

		//log.Printf("[DEBUG SEARCH] Query float32 (prima di quantize, primi 5): %v", queryToUse[:5])

		queryI8 := h.quantizer.Quantize(queryToUse)
		stored, ok := storedVector.([]int8)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch (expected int8)")
		}

		// The int8 distance function returns a dot product.
		// Convert this to a "distance," where a smaller value is better.
		// Use the negative of the dot product.
		dot, err := fn(queryI8, stored)

		// --- SCALING LOGIC ---
		// Calculate the "norms" of the int8 vectors. These aren't true norms,
		// but they're used to scale the result.
		var norm1, norm2 int64
		for i := range queryI8 {
			norm1 += int64(queryI8[i]) * int64(queryI8[i])
			norm2 += int64(stored[i]) * int64(stored[i])
		}

		if norm1 == 0 || norm2 == 0 {
			return 1.0, nil // Maximum distance if a vector is null
		}

		similarity := float64(dot) / (math.Sqrt(float64(norm1)) * math.Sqrt(float64(norm2)))

		// Make sure the similarity is in the range [-1, 1] due to numerical errors
		if similarity > 1.0 {
			similarity = 1.0
		}
		if similarity < -1.0 {
			similarity = -1.0
		}

		return 1.0 - similarity, err // Minimizzare -dot è come massimizzare dot.
	default:
		return 0, fmt.Errorf("invalid or uninitialized distance function")
	}
}

// SearchWithScores finds the K nearest neighbors to a query vector, returning their scores (distances)
func (h *Index) SearchWithScores(query []float32, k int, allowList map[uint32]struct{}, efSearch int) []types.SearchResult {
	h.mu.RLock()
	defer h.mu.RUnlock()

	candidates, err := h.searchInternal(query, k, allowList, efSearch)
	if err != nil {
		log.Printf("Error during HNSW search: %v", err)
		return []types.SearchResult{}
	}

	results := make([]types.SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = types.SearchResult{DocID: c.Id, Score: c.Distance}
	}
	return results
}

// searchInternal is the private function for finding the K nearest neighbors
func (h *Index) searchInternal(query []float32, k int, allowList map[uint32]struct{}, efSearch int) ([]types.Candidate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// if the graph is empty return without results
	if h.maxLevel == -1 {
		return []types.Candidate{}, nil
	}

	// start the search from the global entry point at the highest level
	currentEntryPoint := h.entrypointID

	// --- Smart Entry Point Selection ---
	// If the default entry point is filtered, we can't use it.
	// We choose a random starting point from the allow list.
	if allowList != nil {
		if _, ok := allowList[currentEntryPoint]; !ok {
			// Get any element from the map.
			foundNewEntryPoint := false
			for id := range allowList {
				currentEntryPoint = id
				foundNewEntryPoint = true
				break
			}
			if !foundNewEntryPoint { // The allow list was empty
				return []types.Candidate{}, nil
			}
		}
	}

	// 1) Iterative top-down search to move towards the base layer.
	for l := h.maxLevel; l > 0; l-- {
		// For top-down search, k=1 is sufficient; efSearch is not needed.
		// We pass 0 to use the default.
		nearest, err := h.searchLayer(query, currentEntryPoint, 1, l, allowList, 0)
		if err != nil {
			return nil, err
		}
		if len(nearest) == 0 {
			return []types.Candidate{}, fmt.Errorf("search failed at level %d, graph may be inconsistent", l)
		}
		currentEntryPoint = nearest[0].Id
	}

	// 2) Actual search at layer 0 to find the query's neighbors.
	nearestNeighbors, err := h.searchLayer(query, currentEntryPoint, k, 0, allowList, efSearch)
	if err != nil {
		return nil, err
	}

	return nearestNeighbors, nil
}

// Add inserts a new vector into the graph
func (h *Index) Add(id string, vector []float32) (uint32, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// I check via a string-int map whether a node with the id of the one I want to insert already exists.
	if _, exists := h.externalToInternalID[id]; exists {
		return 0, fmt.Errorf("ID '%s' already exists", id)
	}

	// If the index uses the Cosine metric, normalize the input vector.
	// This ensures that all stored vectors are of unit length.
	if h.metric == distance.Cosine && h.precision == distance.Float32 {
		normalize(vector)
	}

	var storedVector interface{}
	// --- CONVERSION/QUANTIZATION LOGIC ---
	switch h.precision {
	case distance.Float32:
		storedVector = vector
	case distance.Float16:
		f16Vec := make([]uint16, len(vector))
		for i, v := range vector {
			f16Vec[i] = float16.Fromfloat32(v).Bits()
		}
		storedVector = f16Vec
	case distance.Int8:
		if h.quantizer == nil || h.quantizer.AbsMax == 0 {
			return 0, fmt.Errorf("index is in int8 mode but the quantizer is not trained")
		}
		storedVector = h.quantizer.Quantize(vector)
	}

	// --- LOG DI DEBUG FONDAMENTALE ---
	//if f32vec, ok := storedVector.([]float32); ok {
	//log.Printf("[DEBUG ADD] Memorizzazione vettore float32 (primi 5): %v", f32vec[:5])
	//}
	// --- FINE LOG ---

	// internal ID assignment
	h.internalCounter++
	internalID := h.internalCounter

	// create new node
	node := &Node{
		Id:     id,
		Vector: storedVector,
	}

	// add node to tracking maps
	h.nodes[internalID] = node
	h.externalToInternalID[id] = internalID
	h.internalToExternalID[internalID] = id

	// choose a random level for the new node
	level := h.randomLevel()

	node.Connections = make([][]uint32, level+1)

	// if this is the first node then end, become the entrypoint and return
	if h.maxLevel == -1 {
		h.entrypointID = internalID
		h.maxLevel = 0
		node.Connections = make([][]uint32, 1) // solo il livello 0
		return internalID, nil
	}

	// 1) --- Top-down search phase to find entry points at each level ---

	// The search starts from the global entry point at the highest level [entrypointID]
	currentEntryPoint := h.entrypointID

	// descend from the highest level to the level above that assigned to the new node (i.e., the new node's level + 1)
	for l := h.maxLevel; l > level; l-- {
		// trova il vicino più prossimo nel livello attuale e lo usa come entry point per il livello inferiore
		nearest, err := h.searchLayerUnlocked(vector, currentEntryPoint, 1, l, nil, 1, h.internalCounter)
		if err != nil {
			return 0, fmt.Errorf("error during node insertion")
		}
		// Assume that searchLayer always returns a result if the entry point is valid
		currentEntryPoint = nearest[0].Id
	}

	// 2) --- Bottom-up insertion and neighbor connection ---
	// For each level from the lowest to level 0
	// Find neighbors and connect them to the new node
	for l := min(level, h.maxLevel); l >= 0; l-- {
		// trova gli efConstruction candidati più vicini per questo livello
		neighbors, err := h.searchLayerUnlocked(vector, currentEntryPoint, h.efConstruction, l, nil, h.efConstruction, h.internalCounter)
		if err != nil {
			return 0, fmt.Errorf("error during node insertion")
		}

		// select the M best neighbors to connect
		maxConns := h.m
		if l == 0 {
			maxConns = h.mMax0
		}

		selectedNeighbors := h.selectNeighbors(neighbors, maxConns)

		// connect the new node to the selected neighbors
		node.Connections[l] = make([]uint32, len(selectedNeighbors))
		for i, neighborCandidate := range selectedNeighbors {
			node.Connections[l][i] = neighborCandidate.Id
		}

		// also connect neighbors to the new node (bidirectional)
		for _, neighborCandidate := range selectedNeighbors {
			neighborNode := h.nodes[neighborCandidate.Id]

			neighborLevel := len(neighborNode.Connections) - 1
			// if our insertion level 'l' is higher than the neighbor's maximum level, the neighbor cannot have a return connection.
			if l > neighborLevel {
				continue
			}

			// get the list of current neighbors at that level
			neighborConnections := neighborNode.Connections[l]

			// check that the neighbor does not exceed its maximum number of connections
			if len(neighborConnections) < maxConns { // there is space, just add the connection
				neighborNode.Connections[l] = append(neighborConnections, internalID)
			} else {

				// --- SIMPLIFIED PRUNING IMPLEMENTATION ---
				// The neighbor is full. We need to decide if our new node
				// is a better candidate than one of its current neighbors.

				// Let's find the furthest neighbor of our `neighborNode`.
				maxDist := -1.0
				//worstNeighborID := uint32(0)
				worstNeighborIndex := -1

				for i, currentNeighborOfNeighborID := range neighborConnections {
					// comparison between two internal nodes, neighborNode and its neighbor
					dist, _ := h.distance(neighborNode.Vector, h.nodes[currentNeighborOfNeighborID].Vector)
					if dist > maxDist {
						maxDist = dist
						//worstNeighborID = currentNeighborOfNeighborID
						worstNeighborIndex = i
					}
				}

				// Calculate the distance between the neighbor and our new node.
				distToNewNode, _ := h.distance(neighborNode.Vector, node.Vector)

				// If our new node is closer than its furthest neighbor,
				// then we replace it.
				if distToNewNode < maxDist && worstNeighborIndex != -1 {
					neighborNode.Connections[l][worstNeighborIndex] = internalID
				}
			}
		}

		// For the underlying levels, the nearest neighbor found is used
		// as an entry point to improve efficiency
		currentEntryPoint = neighbors[0].Id
	}

	// If the new node has a higher level than any other,
	// it becomes the new global entry point
	if level > h.maxLevel {
		h.maxLevel = level
		h.entrypointID = internalID
	}
	return internalID, nil

}

// GetExternalID retrieves the external string ID for a given internal uint32 ID.
func (h *Index) GetExternalID(internalID uint32) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	externalID, found := h.internalToExternalID[internalID]
	return externalID, found
}

// Delete marks a node as deleted (soft delete).
// It does not remove the node from the graph to maintain structural stability.
func (h *Index) Delete(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// lookup internal ID matching external ID
	internalID, ok := h.externalToInternalID[id]
	if !ok {
		return
	}

	// find the node then set the flag
	node, ok := h.nodes[internalID]
	if ok {
		node.Deleted = true
	}

	// Remove ID from the external lookup map to prevent
	// the same ID from being added again in the future
	delete(h.externalToInternalID, id)
}

func (h *Index) searchLayer(query []float32, entrypointID uint32, k int, level int, allowList map[uint32]struct{}, efSearch int) ([]types.Candidate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Passa h.internalCounter come maxID
	return h.searchLayerUnlocked(query, entrypointID, k, level, allowList, efSearch, h.internalCounter)
}

// searchLayerUnlocked finds the nearest neighbors in a single layer of the graph
func (h *Index) searchLayerUnlocked(query []float32, entrypointID uint32, k int, level int, allowList map[uint32]struct{}, efSearch int, maxID uint32) ([]types.Candidate, error) {
	//log.Printf("DEBUG: Inizio searchLayer a livello %d con entrypoint %d\n", level, entrypointID)
	// the nodes already visited are traced to avoid entering a loop
	// visited := make(map[uint32]bool)
	visited := h.visitedPool.Get().(*BitSet)

	defer func() {
		visited.Clear()
		h.visitedPool.Put(visited)
	}()

	// h.mu.RLock()
	// maxID := h.internalCounter
	// h.mu.RUnlock()

	visited.EnsureCapacity(maxID)

	// --- CORRECT efSearch LOGIC ---
	// ef (effective search size) must be at least k
	var ef int
	if efSearch > 0 {
		// The user specified a value, let's use that.
		ef = efSearch
	} else {
		// The user did not specify a value.
		// The default for the search is k.
		ef = k
	}

	// Let's always make sure that ef is at least as large as k.
	if ef < k {
		// This case occurs if the user passes an efSearch < k,
		// which doesn't make sense. We'll fix it.
		ef = k
	}

	// candidate queue to explore (closest first)
	candidates := newMinHeap(ef)
	// list of results found (the furthest one at the top, to quickly replace it with a better one)
	results := newMaxHeap(ef)

	// Calculate the distance between the query and the entry point
	// Start with the entry point
	dist, err := h.distanceToQuery(query, h.nodes[entrypointID].Vector)
	if err != nil {
		return nil, err
	}

	// insert the nodes into the heaps
	heap.Push(candidates, types.Candidate{Id: entrypointID, Distance: dist})
	heap.Push(results, types.Candidate{Id: entrypointID, Distance: dist})
	// visited[entrypointID] = true // set the node as visited in the map
	visited.Add(entrypointID)

	loopCount := 0

	for candidates.Len() > 0 {
		loopCount++
		// pops the closest element from the queue
		current := heap.Pop(candidates).(types.Candidate)

		// If this candidate is further away than the worst result (the top results) that
		// we've found so far, we can stop exploring this branch.
		if results.Len() >= ef && current.Distance > (*results)[0].Distance {
			break
		}

		// explore the neighbors of the current candidate
		currentNode := h.nodes[current.Id]
		// check that the node has connections at this level
		if level >= len(currentNode.Connections) {
			continue
		}

		//log.Printf("DEBUG: Livello %d, esploro i vicini di %d (%d vicini)\n", level, current.id, len(currentNode.connections[level]))

		for _, neighborID := range currentNode.Connections[level] {
			// CHECK 1: If there is an allowList, the neighbor MUST be in it.
			// `allowList != nil` is the primary check.
			if allowList != nil {
				// `_, ok := ...` is an O(1) lookup into a map/set.
				if _, ok := allowList[neighborID]; !ok {
					continue // Jump to the next neighbor, this one is discarded.
				}
			}

			// Has this neighbor never been visited? Then I'll go (prevents infinite loops)
			if !visited.Has(neighborID) {
				visited.Add(neighborID)

				// calculate the distance between the query and the neighbor
				dist, err := h.distanceToQuery(query, h.nodes[neighborID].Vector)
				if err != nil {
					// ignore and continue on calculation error
					continue
				}

				// --- LOG DI DEBUG CHIAVE ---
				//log.Printf("[DEBUG Cosine] Query vs. Nodo %d (%s): Distanza Calcolata = %f",
				//	neighborID, h.nodes[neighborID].id, dist)
				// --- FINE LOG ---

				// if we still have room in the results or if this neighbor
				// is closer than our worst result, add it
				if results.Len() < ef || dist < (*results)[0].Distance {
					//log.Printf("DEBUG: Livello %d, aggiungo candidato %d con distanza %f\n", level, neighborID, dist)

					heap.Push(candidates, types.Candidate{Id: neighborID, Distance: dist})
					heap.Push(results, types.Candidate{Id: neighborID, Distance: dist})

					// if there is no more space in the result list then we remove the worst one (which is the first one)
					if results.Len() > ef {
						heap.Pop(results)
					}
				}
			}

		}

	}
	//log.Printf("DEBUG: Fine searchLayer a livello %d dopo %d iterazioni\n", level, loopCount)

	// if there is no more space in the result list then we remove the worst one (which is the first one)
	finalResults := make([]types.Candidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		finalResults[i] = heap.Pop(results).(types.Candidate)
	}

	// Return only the best 'k' results. The `finalResults` slice is already sorted from best to worst.
	if len(finalResults) > k {
		return finalResults[:k], nil
	}

	// 2. Sort the slice explicitly and correctly.
	// Sort by 'distance' in ascending order (smallest to largest).
	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].Distance < finalResults[j].Distance
	})

	return finalResults, nil

}

// randomLevel selects a random level for a new node based on an exponentially decaying probability distribution.
func (h *Index) randomLevel() int {
	// This is based on an exponentially decreasing distribution
	level := 0
	for h.levelRand.Float64() < 0.5 && level < h.maxLevel+1 { // Let's add a limit for safety
		level++
	}
	return level
}

// selectNeighbors implements the advanced neighbor selection heuristic from the HNSW paper.
// It aims to select a diverse set of neighbors.
func (h *Index) selectNeighbors(candidates []types.Candidate, m int) []types.Candidate {
	if len(candidates) <= m {
		return candidates
	}

	results := make([]types.Candidate, 0, m)

	// We keep a copy of the job candidates from which we can "discard" elements.
	// The `candidates` slice is already sorted by increasing distance.
	worklist := candidates

	for len(worklist) > 0 && len(results) < m {
		// Take the best candidate (the first one on the list)
		e := worklist[0]
		worklist = worklist[1:] // Remove it from the worklist

		// If it's the first result, always add it.
		if len(results) == 0 {
			results = append(results, e)
			continue
		}

		// Diversity condition: 'e' must be closer to the query
		// than any neighbor already selected in 'results'.
		isGoodCandidate := true
		for _, r := range results {
			dist_e_r, err := h.distance(h.nodes[e.Id].Vector, h.nodes[r.Id].Vector)
			if err != nil {
				// If it fails, consider it a bad candidate for safety.
				isGoodCandidate = false
				break
			}

			// --- EXPERIMENTAL CHANGE ---
			// If the metric is Cosine, we use a "less than or equal" comparison
			// to be less aggressive in discarding candidates.
			var condition bool
			if h.metric == distance.Cosine {
				condition = (dist_e_r <= e.Distance)
			} else {
				condition = (dist_e_r < e.Distance)
			}

			if condition { // If 'e' is close to or the same distance from 'r'...
				isGoodCandidate = false
				break
			}

			/*
				// Se 'e' è più vicino a un vicino già scelto 'r' di quanto 'e' non sia
				// alla query, allora è ridondante.
				if dist_e_r < e.distance {
					isGoodCandidate = false
					break
				}
			*/
		}

		if isGoodCandidate {
			results = append(results, e)
		}
	}

	return results
}

// min is a helper function for the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Helper ---
// Iterate loops over all non-deleted nodes and passes the external ID and vector
// (always as []float32) to a callback function.
func (h *Index) Iterate(callback func(id string, vector []float32)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, node := range h.nodes {
		if !node.Deleted {
			// --- CONVERSION LOGIC ---
			var vectorF32 []float32

			// Check the type of the stored vector
			switch vec := node.Vector.(type) {
			case []float32:
				vectorF32 = vec
			case []uint16:
				// If it's float16, we need to unpack it into float32
				vectorF32 = make([]float32, len(vec))
				for i, v := range vec {
					vectorF32[i] = float16.Frombits(v).Float32()
				}
			case []int8:
				// If it's int8, we need to de-quantize it.
				if h.quantizer == nil {
					log.Printf("WARNING: int8 index missing quantizer for node %s", node.Id)
					continue
				}
				vectorF32 = h.quantizer.Dequantize(vec)
			default:
				// Unknown type, let's skip this node to be safe
				log.Printf("WARNING: Unknown vector type during iteration for node %s", node.Id)
				continue
			}

			callback(node.Id, vectorF32)
		}
	}
}

// IterateRaw iterates over non-deleted nodes and passes the external ID and the RAW vector
// (as an interface{}) to the callback.
func (h *Index) IterateRaw(callback func(id string, vector interface{})) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, node := range h.nodes {
		if !node.Deleted {
			callback(node.Id, node.Vector)
		}
	}
}

// GetInternalID retrieves the internal ID for a given external ID.
func (h *Index) GetInternalID(externalID string) uint32 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.externalToInternalID[externalID]
}

// GetInternalIDUnlocked retrieves the internal ID without locking. Caller must ensure safety.
func (h *Index) GetInternalIDUnlocked(externalID string) uint32 {
	return h.externalToInternalID[externalID]
}

// GetParameters returns the configuration parameters of the index.
func (h *Index) GetParameters() (distance.DistanceMetric, int, int) {
	return h.metric, h.m, h.efConstruction
}

// GetNodeData retrieves the complete data for a node (decompressed/dequantized)
// given its external ID.
func (h *Index) GetNodeData(externalID string) (types.NodeData, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	internalID, ok := h.externalToInternalID[externalID]
	if !ok {
		return types.NodeData{}, false
	}

	node, ok := h.nodes[internalID]
	if !ok || node.Deleted {
		return types.NodeData{}, false
	}

	// Use the same logic as 'Iterate' to de-compress/de-quantize
	var vectorF32 []float32
	switch vec := node.Vector.(type) {
	case []float32:
		vectorF32 = vec
	case []uint16:
		vectorF32 = make([]float32, len(vec))
		for i, v := range vec {
			vectorF32[i] = float16.Frombits(v).Float32()
		}
	case []int8:
		if h.quantizer == nil {
			return types.NodeData{}, false // Inconsistent state
		}
		vectorF32 = h.quantizer.Dequantize(vec)
	default:
		return types.NodeData{}, false // Type unknown
	}

	return types.NodeData{
		ID:         externalID,
		InternalID: internalID,
		Vector:     vectorF32,
	}, true
}

// TrainQuantizer trains the index's quantizer on a sample of vectors.
func (h *Index) TrainQuantizer(vectors [][]float32) {
	if h.quantizer != nil {
		h.quantizer.Train(vectors)
	}
}

// GetInfo returns all public parameters and the state of the index.
func (h *Index) GetInfo() (distance.DistanceMetric, int, int, distance.PrecisionType, int, string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	// --- LOG DI DEBUG ---
	log.Printf("[DEBUG GetInfo] Nome Esterno: (non disponibile qui), Metrica: %s, Precisione: %s", h.metric, h.precision)
	// --- FINE LOG ---

	// We only count the non-deleted nodes
	count := 0
	for _, node := range h.nodes {
		if !node.Deleted {
			count++
		}
	}

	return h.metric, h.m, h.efConstruction, h.precision, count, h.textLanguage
}

// GetInfoUnlocked returns index information without acquiring a lock.
func (h *Index) GetInfoUnlocked() (distance.DistanceMetric, int, int, distance.PrecisionType, int, string) {
	// Count non-deleted nodes (without lock, assumes caller has RLock)
	count := 0
	for _, node := range h.nodes {
		if !node.Deleted {
			count++
		}
	}
	return h.metric, h.m, h.efConstruction, h.precision, count, h.textLanguage
}

// normalize normalizes a vector to unit length (L2 norm).
// This method modifies the slice in-place.
func normalize(v []float32) {
	// --- LOG DI DEBUG ---
	// Stampa i primi elementi del vettore PRIMA della normalizzazione
	// (stampiamo solo i primi 5 per non inondare il log)
	//limit := 5
	//if len(v) < 5 {
	//	limit = len(v)
	//}
	//log.Printf("[DEBUG NORMALIZE] Vettore INGRESSO (primi %d elementi): %v", limit, v[:limit])
	// --- FINE LOG ---
	var norm float32
	for _, val := range v {
		norm += val * val
	}
	if norm > 0 {
		norm = float32(math.Sqrt(float64(norm)))
		for i := range v {
			v[i] /= norm
		}
	}

	// --- LOG DI DEBUG ---
	// Stampa i primi elementi del vettore DOPO la normalizzazione
	//log.Printf("[DEBUG NORMALIZE] Vettore USCITA (primi %d elementi): %v", limit, v[:limit])
	// Calcoliamo la nuova lunghezza per verifica
	var finalNorm float32
	for _, val := range v {
		finalNorm += val * val
	}
	//log.Printf("[DEBUG NORMALIZE] Lunghezza (norma L2) calcolata DOPO: %f", math.Sqrt(float64(finalNorm)))
	// --- FINE LOG ---
}

// RLock acquires a read lock on the index. For use by external callers like the DB.
func (h *Index) RLock() {
	h.mu.RLock()
}

// RUnlock releases a read lock.
func (h *Index) RUnlock() {
	h.mu.RUnlock()
}

// GetParametersUnlocked returns parameters without a lock.
func (h *Index) GetParametersUnlocked() (distance.DistanceMetric, distance.PrecisionType, int, int) {
	return h.metric, h.precision, h.m, h.efConstruction
}

// SnapshotData exports the internal data of the index for persistence.
// It expects the caller to handle locking.
func (h *Index) SnapshotData() (map[uint32]*Node, map[string]uint32, uint32, uint32, int, *distance.Quantizer) {
	// This method expects the caller to have already acquired a lock.

	// Before saving, let's make sure each node has its InternalID populated.
	for id, node := range h.nodes {
		node.InternalID = id
	}

	return h.nodes, h.externalToInternalID, h.internalCounter, h.entrypointID, h.maxLevel, h.quantizer
}

// LoadSnapshotData restores the internal state of the index from snapshot data.
// It expects the index to be empty and the caller to handle locking.
func (h *Index) LoadSnapshotData(
	nodes map[uint32]*Node,
	extToInt map[string]uint32,
	counter uint32,
	entrypoint uint32,
	maxLevel int,
	quantizer *distance.Quantizer,
) error {
	h.nodes = nodes
	h.externalToInternalID = extToInt
	h.internalCounter = counter
	h.entrypointID = entrypoint
	h.maxLevel = maxLevel
	h.quantizer = quantizer

	// Check consistency
	if h.nodes == nil {
		h.nodes = make(map[uint32]*Node)
	}
	if h.externalToInternalID == nil {
		h.externalToInternalID = make(map[string]uint32)
	}

	// Rebuild internalToExternalID from nodes
	h.internalToExternalID = make(map[uint32]string) // Initialize (if not already done in New)
	for internalID, node := range h.nodes {
		if node.Id == "" {
			return fmt.Errorf("node with internal ID %d has an empty external ID", internalID)
		}
		h.internalToExternalID[internalID] = node.Id

		// Ensure consistency with externalToInternal
		if existingInternal, ok := h.externalToInternalID[node.Id]; ok && existingInternal != internalID {
			return fmt.Errorf("ID inconsistency: external '%s' is mapped to %d but node has %d", node.Id, existingInternal, internalID)
		}
		h.externalToInternalID[node.Id] = internalID // Overwrite if necessary for security

		// Ensures node.InternalID
		node.InternalID = internalID
	}

	return nil
}

// Metric returns the distance metric used by the index.
func (h *Index) Metric() distance.DistanceMetric {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.metric
}

// Precision returns the data precision used by the index.
func (h *Index) Precision() distance.PrecisionType {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.precision
}

// M returns the HNSW M parameter (max connections for layer > 0).
func (h *Index) M() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.m
}

// EfConstruction returns the HNSW efConstruction parameter.
func (h *Index) EfConstruction() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.efConstruction
}

// Quantizer returns a pointer to the index's quantizer.
func (h *Index) Quantizer() *distance.Quantizer {
	return h.quantizer
}

// TextLanguage returns the language configured for text analysis.
func (h *Index) TextLanguage() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.textLanguage
}
