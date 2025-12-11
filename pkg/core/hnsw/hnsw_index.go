// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// (HNSW) graph algorithm for efficient approximate nearest neighbor search.
//
// This package contains the core Index struct and its associated methods for building,
// searching, and managing the HNSW graph. It supports multiple distance metrics
// (Euclidean, Cosine), various data precisions (float32, float16, int8 quantization),
// and concurrent access patterns including batch insertions.
package hnsw

import (
	// "container/heap"
	"fmt"
	"log"
	"math"
	"math/rand"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/x448/float16"
)

// LinkRequest represents a single atomic connection modification operation to be
// applied to the graph. It is used by the concurrent AddBatch method and can be
// extended for future graph repair mechanisms.
type LinkRequest struct {
	NodeID       uint32
	Level        int
	NewNeighbors []uint32
}

const NumShards = 128

// Index represents the hierarchical graph structure.
type Index struct {
	// Global mutex for concurrency control. Finer-grained locking may be considered in the future.
	metaMu   sync.RWMutex
	shardsMu []sync.RWMutex

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

	nodes []*Node

	// Separate maps to translate between external and internal IDs
	externalToInternalID map[string]uint32
	internalToExternalID map[uint32]string
	nodeCounter          atomic.Uint64

	// Stores the precision of the index (e.g., f32, f16)
	precision distance.PrecisionType

	quantizedNorms []float32

	// Field for quantization; will be nil for non-quantized indexes.
	// It's a pointer because we need its state to be shared and mutable.
	quantizer *distance.Quantizer

	// Stores the appropriate distance function for this index.
	// Uses 'any' for flexibility with different function signatures.
	distanceFunc any
	distFuncF32  distance.DistanceFuncF32
	distFuncF16  distance.DistanceFuncF16
	distFuncI8   distance.DistanceFuncI8

	// Stores the distance metric type
	metric distance.DistanceMetric

	textLanguage string

	visitedPool sync.Pool

	minHeapPool sync.Pool
	maxHeapPool sync.Pool
	// candidateObjectPool sync.Pool

	optimizer *GraphOptimizer
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
		nodes:                make([]*Node, 0, 10000),
		externalToInternalID: make(map[string]uint32),
		internalToExternalID: make(map[uint32]string),
		maxLevel:             -1, // No levels initially
		entrypointID:         0,  // Initialized on the first insertion
		metric:               metric,
		precision:            precision,
		textLanguage:         textLang,
		quantizedNorms:       make([]float32, 0),
		shardsMu:             make([]sync.RWMutex, NumShards),
	}

	h.nodeCounter.Store(0)

	h.visitedPool = sync.Pool{
		New: func() any {
			// Pre-allocate larger BitSet to avoid grow() during batch operations
			// 65536 covers most use cases without excessive memory
			return NewBitSet(20480)
		},
	}

	h.minHeapPool = sync.Pool{
		New: func() any { return newMinHeap(efConstruction) },
	}
	h.maxHeapPool = sync.Pool{
		New: func() any { return newMaxHeap(efConstruction) },
	}

	// Pre-allocate if we know it's Int8
	if precision == distance.Int8 {
		h.quantizedNorms = make([]float32, 0, 10000)
	}

	// --- SELECTION AND VALIDATION LOGIC ---
	// Select the correct distance function based on precision and metric.
	var err error
	switch precision {
	case distance.Float32:
		h.distFuncF32, err = distance.GetFloat32Func(metric)
		if err != nil {
			return nil, err
		}
		h.distanceFunc = h.distFuncF32
	case distance.Float16:
		if metric != distance.Euclidean {
			return nil, fmt.Errorf("precision '%s' only supports the '%s' metric", precision, distance.Euclidean)
		}
		h.distFuncF16, err = distance.GetFloat16Func(metric)
		if err != nil {
			return nil, err
		}
		h.distanceFunc = h.distFuncF16
	case distance.Int8:
		if metric != distance.Cosine {
			return nil, fmt.Errorf("precision '%s' only supports the '%s' metric", precision, distance.Cosine)
		}
		h.distFuncI8, err = distance.GetInt8Func(metric)
		h.distanceFunc = h.distFuncI8
		// A quantized index requires a quantizer, which will be trained and set later.
		h.quantizer = &distance.Quantizer{}
	default:
		return nil, fmt.Errorf("unsupported precision: %s", precision)
	}

	if err != nil {
		return nil, err
	}

	// Init Optimizer with defaults
	h.optimizer = NewOptimizer(h, DefaultMaintenanceConfig())

	return h, nil
}

// distanceBetweenNodes calculates the distance between two nodes avoiding boxing
func (h *Index) distanceBetweenNodes(n1, n2 *Node) (float64, error) {
	switch h.precision {
	case distance.Float32:
		return h.distFuncF32(n1.VectorF32, n2.VectorF32)
	case distance.Float16:
		return h.distFuncF16(n1.VectorF16, n2.VectorF16)
	case distance.Int8:
		dot, err := h.distFuncI8(n1.VectorI8, n2.VectorI8)
		// Scaling logic
		norm1 := h.quantizedNorms[n1.InternalID]
		norm2 := h.quantizedNorms[n2.InternalID]
		if norm1 == 0 || norm2 == 0 {
			return 1.0, nil
		}

		similarity := float64(dot) / (float64(norm1) * float64(norm2))
		if similarity > 1.0 {
			similarity = 1.0
		}
		if similarity < -1.0 {
			similarity = -1.0
		}
		return 1.0 - similarity, err
	default:
		return 0, fmt.Errorf("invalid precision")
	}
}

/*
// distanceToNode calculates distance from query to a node
func (h *Index) distanceToNode(query any, node *Node) (float64, error) {
	switch h.precision {
	case distance.Float32:
		return h.distFuncF32(query.([]float32), node.VectorF32)
	case distance.Float16:
		return h.distFuncF16(query.([]uint16), node.VectorF16)
	case distance.Int8:
		q := query.([]int8)
		stored := node.VectorI8
		dot, err := h.distFuncI8(q, stored)


		var norm1 int64
		for i := range q {
			norm1 += int64(q[i]) * int64(q[i])
		}

		// Use cached norm for stored vector
		norm2 := h.quantizedNorms[node.InternalID]

		if norm1 == 0 || norm2 == 0 {
			return 1.0, nil
		}
		similarity := float64(dot) / (math.Sqrt(float64(norm1)) * float64(norm2))
		if similarity > 1.0 {
			similarity = 1.0
		}
		if similarity < -1.0 {
			similarity = -1.0
		}
		return 1.0 - similarity, err
	default:
		return 0, fmt.Errorf("invalid precision")
	}
}
*/

// SearchWithScores finds the K nearest neighbors to a query vector, returning their scores (distances)
func (h *Index) SearchWithScores(query []float32, k int, allowList map[uint32]struct{}, efSearch int) []types.SearchResult {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

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

// searchInternal handles query pre-processing (normalization/quantization) once and orchestrates the search.
func (h *Index) searchInternal(query []float32, k int, allowList map[uint32]struct{}, efSearch int) ([]types.Candidate, error) {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	if h.maxLevel == -1 {
		return []types.Candidate{}, nil
	}

	// Size based on efSearch because it's the maximum that searchLayerUnlocked can return
	scratchOut := make([]types.Candidate, 0, efSearch)

	// --- PHASE 0: Query Preparation ---
	// Normalization (if Cosine)
	var queryF32 []float32
	if h.metric == distance.Cosine {
		// Copy to avoid modifying original
		queryF32 = make([]float32, len(query))
		copy(queryF32, query)
		normalize(queryF32)
	} else {
		queryF32 = query
	}

	// Adapt to precision type (any)
	var finalQuery any
	switch h.precision {
	case distance.Float32:
		finalQuery = queryF32
	case distance.Float16:
		// Convert float32 -> uint16
		qF16 := make([]uint16, len(queryF32))
		for i, v := range queryF32 {
			qF16[i] = float16.Fromfloat32(v).Bits()
		}
		finalQuery = qF16
	case distance.Int8:
		// Quantize float32 -> int8
		if h.quantizer == nil {
			return nil, fmt.Errorf("quantizer missing")
		}
		finalQuery = h.quantizer.Quantize(queryF32)
	}

	currentEntryPoint := h.entrypointID

	// Smart Entry Point Selection
	if allowList != nil {
		if _, ok := allowList[currentEntryPoint]; !ok {
			foundNewEntryPoint := false
			for id := range allowList {
				currentEntryPoint = id
				foundNewEntryPoint = true
				break
			}
			if !foundNewEntryPoint {
				return []types.Candidate{}, nil
			}
		}
	}

	// 1) Iterative top-down search
	for l := h.maxLevel; l > 0; l-- {
		nearest, err := h.searchLayerUnlocked(finalQuery, currentEntryPoint, 1, l, allowList, 0, uint32(h.nodeCounter.Load()), scratchOut)
		if err != nil {
			return nil, err
		}
		if len(nearest) == 0 {
			return []types.Candidate{}, fmt.Errorf("search failed at level %d", l)
		}
		currentEntryPoint = nearest[0].Id
	}

	// 2) Base layer search
	nearestNeighbors, err := h.searchLayerUnlocked(finalQuery, currentEntryPoint, k, 0, allowList, efSearch, uint32(h.nodeCounter.Load()), scratchOut)
	if err != nil {
		return nil, err
	}

	return nearestNeighbors, nil
}

// Add inserts a new vector (Single Insert)
func (h *Index) Add(id string, vector []float32) (uint32, error) {
	h.metaMu.Lock()
	defer h.metaMu.Unlock()

	// Buffer for searchLayerUnlocked output.
	// Must be at least as large as efConstruction
	scratchOut := make([]types.Candidate, 0, h.efConstruction)

	if _, exists := h.externalToInternalID[id]; exists {
		return 0, fmt.Errorf("ID '%s' already exists", id)
	}

	// Local pre-processing
	if h.metric == distance.Cosine && h.precision == distance.Float32 {
		normalize(vector)
	}

	var storedVector interface{}
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
		// Auto-Train if needed (Fallback for single insert)
		if h.quantizer == nil {
			h.quantizer = &distance.Quantizer{}
		}
		if h.quantizer.AbsMax == 0 {
			log.Printf("[HNSW] Auto-training quantizer on single vector (suboptimal for quality but necessary for progress)")
			h.quantizer.Train([][]float32{vector})
		}

		storedVector = h.quantizer.Quantize(vector)
	}

	internalID := uint32(h.nodeCounter.Add(1))
	h.growNodes(internalID)

	if h.precision == distance.Int8 {
		if vecI8, ok := storedVector.([]int8); ok {
			h.quantizedNorms[internalID] = computeInt8Norm(vecI8)
		}
	}

	node := &Node{Id: id, InternalID: internalID}
	switch h.precision {
	case distance.Float32:
		node.VectorF32 = storedVector.([]float32)
	case distance.Float16:
		node.VectorF16 = storedVector.([]uint16)
	case distance.Int8:
		node.VectorI8 = storedVector.([]int8)
	}
	h.nodes[internalID] = node
	h.externalToInternalID[id] = internalID
	h.internalToExternalID[internalID] = id

	level := h.randomLevel()
	node.Connections = make([][]uint32, level+1)

	if h.maxLevel == -1 {
		h.entrypointID = internalID
		h.maxLevel = 0
		node.Connections = make([][]uint32, 1)
		return internalID, nil
	}

	// --- Prepare vector for search (Query Object) ---
	// Note: Add uses the inserted vector as the query to find neighbors.
	// Since 'storedVector' is already the correct type (f32, f16, or i8), we use it directly.
	queryObj := storedVector

	currentEntryPoint := h.entrypointID
	for l := h.maxLevel; l > level; l-- {
		nearest, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, 1, l, nil, 1, uint32(h.nodeCounter.Load()), scratchOut)
		if err != nil {
			return 0, err
		}
		currentEntryPoint = nearest[0].Id
	}

	for l := min(level, h.maxLevel); l >= 0; l-- {
		neighbors, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, h.efConstruction, l, nil, h.efConstruction, uint32(h.nodeCounter.Load()), scratchOut)
		if err != nil {
			return 0, err
		}

		maxConns := h.m
		if l == 0 {
			maxConns = h.mMax0
		}

		selectedNeighbors := h.selectNeighbors(neighbors, maxConns)

		node.Connections[l] = make([]uint32, len(selectedNeighbors))
		for i, neighborCandidate := range selectedNeighbors {
			node.Connections[l][i] = neighborCandidate.Id
		}

		// Bidirectional connections (simplified here for brevity, logic follows original)
		for _, neighborCandidate := range selectedNeighbors {
			neighborNode := h.nodes[neighborCandidate.Id]
			if l > len(neighborNode.Connections)-1 {
				continue
			}

			neighborConnections := neighborNode.Connections[l]
			if len(neighborConnections) < maxConns {
				neighborNode.Connections[l] = append(neighborConnections, internalID)
			} else {
				// Pruning logic
				maxDist := -1.0
				worstNeighborIndex := -1
				for i, nID := range neighborConnections {
					d, _ := h.distanceBetweenNodes(neighborNode, h.nodes[nID])
					if d > maxDist {
						maxDist = d
						worstNeighborIndex = i
					}
				}
				distToNew, _ := h.distanceBetweenNodes(neighborNode, node)
				if distToNew < maxDist && worstNeighborIndex != -1 {
					neighborNode.Connections[l][worstNeighborIndex] = internalID
				}
			}
		}
		if len(neighbors) > 0 {
			currentEntryPoint = neighbors[0].Id
		}
	}

	if level > h.maxLevel {
		h.maxLevel = level
		h.entrypointID = internalID
	}
	return internalID, nil
}

// Public methods to access the Optimizer

func (h *Index) UpdateMaintenanceConfig(cfg AutoMaintenanceConfig) {
	if h.optimizer != nil {
		h.optimizer.UpdateConfig(cfg)
	}
}

func (h *Index) GetMaintenanceConfig() AutoMaintenanceConfig {
	if h.optimizer != nil {
		return h.optimizer.GetConfig()
	}
	return DefaultMaintenanceConfig()
}

func (h *Index) MaintenanceRun(forceType string) bool {
	if h.optimizer != nil {
		return h.optimizer.RunCycle(forceType)
	}
	return false
}

// AddBatch inserts a large batch of vectors concurrently.
// It partitions the data, allocates nodes in parallel, finds neighbors
// in parallel, and then commits all link changes in a final, sequential step.
// This method is optimized for throughput, not for single-insert latency.
// AddBatch optimized

func (h *Index) AddBatch(objects []types.BatchObject) error {
	numVectors := len(objects)
	if numVectors == 0 {
		return nil
	}

	// If the graph is too small, parallel insertion doesn't work well
	// because nodes don't "see" each other during neighbor search.
	// We need to populate the initial skeleton of the graph sequentially.

	h.metaMu.RLock()
	currentSize := h.nodeCounter.Load()
	h.metaMu.RUnlock()

	// We use efConstruction as a heuristic threshold.
	// Until we have at least 'efConstruction' nodes, we use standard sequential Add.
	// This ensures that the first nodes are well connected.
	if currentSize < uint64(h.efConstruction) {
		// Debug log (optional)
		// fmt.Println("Small/empty graph: switching to sequential insertion to boost recall")
		for _, obj := range objects {
			_, err := h.Add(obj.Id, obj.Vector)
			if err != nil {
				return err
			}
		}
		return nil
	}

	// =========================================================================
	// PHASE 0: GLOBAL PREPARATION (Metadata & Allocation)
	// =========================================================================
	// Here we take the global lock briefly to reserve IDs
	// and allocate space. No heavy computation here.

	h.metaMu.Lock()

	// 1. Reserve a range of atomic IDs
	startID := h.nodeCounter.Add(uint64(numVectors)) - uint64(numVectors)
	lastID := uint32(startID + uint64(numVectors) - 1)

	// 2. Memory allocation for nodes
	h.growNodes(lastID)

	// 3. Node creation and population (Data Insertion)
	newNodes := make([]*Node, numVectors)

	for i, obj := range objects {
		internalID := uint32(startID + uint64(i))

		// Check for duplicate external IDs
		if _, exists := h.externalToInternalID[obj.Id]; exists {
			h.metaMu.Unlock()
			return fmt.Errorf("ID '%s' already exists", obj.Id)
		}

		// -- Normalization/Quantization Logic --
		if h.metric == distance.Cosine && h.precision == distance.Float32 {
			normalize(obj.Vector)
		}

		var storedVector interface{}
		switch h.precision {
		case distance.Float32:
			storedVector = obj.Vector
		case distance.Float16:
			f16Vec := make([]uint16, len(obj.Vector))
			for j, v := range obj.Vector {
				f16Vec[j] = float16.Fromfloat32(v).Bits()
			}
			storedVector = f16Vec
		case distance.Int8:
			// Auto-Train if needed
			if h.quantizer == nil {
				h.quantizer = &distance.Quantizer{}
			}
			if h.quantizer.AbsMax == 0 {
				// Prevent multiple concurrent trainings
				// Although AddBatch is technically thread-safe w.r.t other AddBatches if called correctly,
				// we are inside the global lock here (h.metaMu.Lock()), so we are safe.

				// Collect all vectors to train
				trainingData := make([][]float32, numVectors)
				for k, objTrain := range objects {
					trainingData[k] = objTrain.Vector // Assuming they are not modified during train
				}
				log.Printf("[HNSW] Auto-training quantizer on batch of %d vectors", numVectors)
				h.quantizer.Train(trainingData)
			}

			storedVector = h.quantizer.Quantize(obj.Vector)
		}

		if h.precision == distance.Int8 {
			if vecI8, ok := storedVector.([]int8); ok {
				h.quantizedNorms[internalID] = computeInt8Norm(vecI8)
			}
		}

		node := &Node{Id: obj.Id, InternalID: internalID}
		switch h.precision {
		case distance.Float32:
			node.VectorF32 = storedVector.([]float32)
		case distance.Float16:
			node.VectorF16 = storedVector.([]uint16)
		case distance.Int8:
			node.VectorI8 = storedVector.([]int8)
		}

		// Random level assignment
		level := h.randomLevel()
		node.Connections = make([][]uint32, level+1)

		// Save to global slice and mappings
		newNodes[i] = node
		h.nodes[internalID] = node
		h.externalToInternalID[node.Id] = internalID
		h.internalToExternalID[internalID] = node.Id
	}

	// If this is the very first insertion, set the entrypoint
	if h.maxLevel == -1 {
		h.entrypointID = newNodes[0].InternalID
		h.maxLevel = 0 // Will be updated at the end if necessary
	}

	// Release the global lock. Now the nodes exist in memory and we can read them.
	h.metaMu.Unlock()

	// =========================================================================
	// PHASE 1: PARALLEL NEIGHBOR CALCULATION (CPU Bound)
	// =========================================================================
	// Each worker calculates neighbors for its subset of nodes.
	// We DON'T use channels. Each worker writes to a local slice.

	numWorkers := runtime.NumCPU()
	if numVectors < numWorkers {
		numWorkers = numVectors
	}

	// Worker output: slice of slices of requests
	workerResults := make([][]LinkRequest, numWorkers)

	var wg sync.WaitGroup
	batchSize := (numVectors + numWorkers - 1) / numWorkers

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		start := i * batchSize
		end := start + batchSize
		if end > numVectors {
			end = numVectors
		}
		if start >= end {
			wg.Done()
			continue
		}

		go func(wID int, nodesSubset []*Node) {
			defer wg.Done()

			// Heuristic pre-allocation to avoid re-allocations
			localReqs := make([]LinkRequest, 0, len(nodesSubset)*h.maxLevel*2)
			scratchBuffer := make([]types.Candidate, 0, h.efConstruction+50)

			// Read current entrypoint (with fast RLock or atomic if possible, here we use RLock on meta)
			h.metaMu.RLock()
			entryPointID := h.entrypointID
			currentMaxLevel := h.maxLevel
			maxID := uint32(h.nodeCounter.Load())
			h.metaMu.RUnlock()

			for _, node := range nodesSubset {
				// Query object preparation
				var queryObj any
				switch h.precision {
				case distance.Float32:
					queryObj = node.VectorF32
				case distance.Float16:
					queryObj = node.VectorF16
				case distance.Int8:
					queryObj = node.VectorI8
				}

				nodeLevel := len(node.Connections) - 1
				currEp := entryPointID

				// 1. Zoom-in from higher levels (no neighbor saving, just approach)
				for l := currentMaxLevel; l > nodeLevel; l-- {
					nearest, err := h.searchLayerUnlocked(queryObj, currEp, 1, l, nil, 1, maxID, scratchBuffer)
					if err == nil && len(nearest) > 0 {
						currEp = nearest[0].Id
					}
				}

				// 2. Insertion at node levels
				for l := min(nodeLevel, currentMaxLevel); l >= 0; l-- {
					// Search for efConstruction candidates
					candidates, err := h.searchLayerUnlocked(queryObj, currEp, h.efConstruction, l, nil, h.efConstruction, maxID, scratchBuffer)
					if err != nil || len(candidates) == 0 {
						continue
					}

					// Save results to local slice
					neighborIDs := make([]uint32, len(candidates))
					for k, cand := range candidates {
						neighborIDs[k] = cand.Id
					}

					localReqs = append(localReqs, LinkRequest{
						NodeID:       node.InternalID,
						Level:        l,
						NewNeighbors: neighborIDs,
					})

					// Update entrypoint for the level below
					currEp = candidates[0].Id
				}
			}
			workerResults[wID] = localReqs
		}(i, newNodes[start:end])
	}
	wg.Wait()

	// =========================================================================
	// PHASE 2: PARTITIONING (SHUFFLE) - Memory Bound (Fast)
	// =========================================================================
	// We organize requests into "buckets" based on Shard ID.
	// We also generate reverse links here.

	// NumShards is the constant 128 defined in the package
	shardedReqs := make([][]LinkRequest, NumShards)

	// Estimate allocation to avoid resize
	estReqs := (numVectors * h.maxLevel * 2) / NumShards
	for i := range shardedReqs {
		shardedReqs[i] = make([]LinkRequest, 0, estReqs)
	}

	for _, reqs := range workerResults {
		for _, req := range reqs {
			// A. Direct Link: The new node connects to found neighbors
			// (This is used to populate the new node's Connections)
			shardIdx := req.NodeID & (NumShards - 1)
			shardedReqs[shardIdx] = append(shardedReqs[shardIdx], req)

			// B. Reverse Link: Found neighbors should (possibly) connect to the new node
			// HNSW is an approximate bidirectional graph.
			for _, neighborID := range req.NewNeighbors {
				revShardIdx := neighborID & (NumShards - 1)
				shardedReqs[revShardIdx] = append(shardedReqs[revShardIdx], LinkRequest{
					NodeID:       neighborID,
					Level:        req.Level,
					NewNeighbors: []uint32{req.NodeID}, // Only 1 neighbor: the new node
				})
			}
		}
	}

	// =========================================================================
	// PHASE 3: PARALLEL COMMIT PER SHARD (IO/Lock Bound)
	// =========================================================================
	// We process each bucket independently. No lock contention.

	var wgCommit sync.WaitGroup

	// Semaphore to avoid overloading the scheduler if NumShards > CPU
	sem := make(chan struct{}, runtime.NumCPU())

	for i := 0; i < NumShards; i++ {
		reqs := shardedReqs[i]
		if len(reqs) == 0 {
			continue
		}

		wgCommit.Add(1)
		sem <- struct{}{}

		go func(shardID int, requests []LinkRequest) {
			defer wgCommit.Done()
			defer func() { <-sem }()

			// FINE-GRAINED LOCK: Lock only this DB fragment
			h.shardsMu[shardID].Lock()
			defer h.shardsMu[shardID].Unlock()

			// Sort by NodeID to group updates for the same node
			slices.SortFunc(requests, func(a, b LinkRequest) int {
				if a.NodeID < b.NodeID {
					return -1
				}
				if a.NodeID > b.NodeID {
					return 1
				}
				return 0
			})

			// --- REUSABLE BUFFERS FOR THIS WORKER ---
			candidatesScratch := make([]types.Candidate, 0, h.m*4)
			uniqueIDs := make([]uint32, 0, h.m*4)

			// Process one node at a time, aggregating all its requests
			for idx := 0; idx < len(requests); {
				currentNodeID := requests[idx].NodeID
				node := h.nodes[currentNodeID]

				if node == nil {
					idx++ // Should not happen
					continue
				}

				// Find the end of the block for this node
				endIdx := idx
				for endIdx < len(requests) && requests[endIdx].NodeID == currentNodeID {
					endIdx++
				}

				// Find the max level involved
				maxLvl := -1
				if len(node.Connections) > 0 {
					maxLvl = len(node.Connections) - 1
				}
				for k := idx; k < endIdx; k++ {
					if requests[k].Level > maxLvl {
						maxLvl = requests[k].Level
					}
				}

				for lvl := 0; lvl <= maxLvl; lvl++ {
					// 1. Reset Buffer
					candidatesScratch = candidatesScratch[:0]
					uniqueIDs = uniqueIDs[:0]

					hasUpdates := false

					// 2. Collect current neighbors
					if lvl < len(node.Connections) {
						uniqueIDs = append(uniqueIDs, node.Connections[lvl]...)
					}

					// 3. Collect new candidates from requests (Linear scan is OK here, few reqs)
					for k := idx; k < endIdx; k++ {
						if requests[k].Level == lvl {
							uniqueIDs = append(uniqueIDs, requests[k].NewNeighbors...)
							hasUpdates = true
						}
					}

					if !hasUpdates && lvl < len(node.Connections) {
						continue // No changes for this level
					}
					if len(uniqueIDs) == 0 {
						continue
					}

					// 4. Deduplicate and Remove Self-Loop (Without Maps!)
					// Sorting uint32 is very fast
					// Note: If you have a sortUint32 helper, use it. Otherwise slice
					slices.Sort(uniqueIDs)

					// Deduplicate in-place
					uniqCount := 0
					if len(uniqueIDs) > 0 {
						// Skip self-loop if present
						readHead := 0
						if uniqueIDs[0] == currentNodeID {
							readHead = 1
						}

						if readHead < len(uniqueIDs) {
							uniqueIDs[0] = uniqueIDs[readHead] // Move first valid element to position 0
							uniqCount = 1
							for r := readHead + 1; r < len(uniqueIDs); r++ {
								val := uniqueIDs[r]
								if val != currentNodeID && val != uniqueIDs[uniqCount-1] {
									uniqueIDs[uniqCount] = val
									uniqCount++
								}
							}
						}
					}
					// Now uniqueIDs[:uniqCount] is the clean list of IDs.

					// 5. Pruning Logic
					maxM := h.m
					if lvl == 0 {
						maxM = h.mMax0
					}

					if uniqCount <= maxM {
						// Fast path: direct copy
						finalList := make([]uint32, uniqCount)
						copy(finalList, uniqueIDs[:uniqCount])

						// Resize node connections if needed
						if lvl >= len(node.Connections) {
							// Grow connections slice
							newConns := make([][]uint32, lvl+1)
							copy(newConns, node.Connections)
							node.Connections = newConns
						}
						node.Connections[lvl] = finalList
					} else {
						// Slow path: Calculate distances
						// Fill candidatesScratch
						for i := 0; i < uniqCount; i++ {
							id := uniqueIDs[i]
							targetNode := h.nodes[id]
							dist, _ := h.distanceBetweenNodes(node, targetNode)
							candidatesScratch = append(candidatesScratch, types.Candidate{Id: id, Distance: dist})
						}

						slices.SortFunc(candidatesScratch, func(a, b types.Candidate) int {
							// Explicit comparison for float64
							if a.Distance < b.Distance {
								return -1
							}
							if a.Distance > b.Distance {
								return 1
							}
							return 0
						})

						selected := h.selectNeighbors(candidatesScratch, maxM)

						finalList := make([]uint32, len(selected))
						for k, s := range selected {
							finalList[k] = s.Id
						}

						if lvl >= len(node.Connections) {
							newConns := make([][]uint32, lvl+1)
							copy(newConns, node.Connections)
							node.Connections = newConns
						}
						node.Connections[lvl] = finalList
					}
				}
				idx = endIdx
			}
		}(i, reqs)
	}

	wgCommit.Wait()

	// =========================================================================
	// PHASE 4: GLOBAL ENTRYPOINT UPDATE
	// =========================================================================

	h.metaMu.Lock()
	updatedMaxLevel := h.maxLevel
	updatedEntrypoint := h.entrypointID

	for _, node := range newNodes {
		l := len(node.Connections) - 1
		if l > updatedMaxLevel {
			updatedMaxLevel = l
			updatedEntrypoint = node.InternalID
		}
	}

	h.maxLevel = updatedMaxLevel
	h.entrypointID = updatedEntrypoint
	h.metaMu.Unlock()

	return nil
}

/*
func (h *Index) AddBatchOld(objects []types.BatchObject) error {
	numVectors := len(objects)
	if numVectors == 0 {
		return nil
	}
	numWorkers := runtime.NumCPU()

	h.metaMu.RLock()
	currentSize := h.nodeCounter.Load()
	h.metaMu.RUnlock()

	if currentSize < uint64(h.efConstruction) {
		for _, obj := range objects {
			h.Add(obj.Id, obj.Vector)
		}
		return nil
	}

	if numVectors < numWorkers {
		numWorkers = numVectors
	}

	h.metaMu.Lock()
	startID := h.nodeCounter.Add(uint64(numVectors)) - uint64(numVectors)
	lastID := uint32(startID + uint64(numVectors) - 1)

	// Pre-allocate all necessary space at once
	h.growNodes(lastID)
	// Note: newNodes is used to pass nodes to workers, but we also populate the global h.nodes
	newNodes := make([]*Node, numVectors)

	for i, obj := range objects {
		internalID := uint32(startID + uint64(i))

		// Normalize in-place if F32/Cosine
		if h.metric == distance.Cosine && h.precision == distance.Float32 {
			normalize(obj.Vector)
		}

		var storedVector interface{}
		switch h.precision {
		case distance.Float32:
			storedVector = obj.Vector
		case distance.Float16:
			f16Vec := make([]uint16, len(obj.Vector))
			for j, v := range obj.Vector {
				f16Vec[j] = float16.Fromfloat32(v).Bits()
			}
			storedVector = f16Vec
		case distance.Int8:
			if h.quantizer == nil || h.quantizer.AbsMax == 0 {
				h.metaMu.Unlock()
				return fmt.Errorf("quantizer not trained")
			}
			storedVector = h.quantizer.Quantize(obj.Vector)
		}

		if h.precision == distance.Int8 {
			if vecI8, ok := storedVector.([]int8); ok {
				h.quantizedNorms[internalID] = computeInt8Norm(vecI8)
			}
		}

		node := &Node{Id: obj.Id, InternalID: internalID}
		switch h.precision {
		case distance.Float32:
			node.VectorF32 = storedVector.([]float32)
		case distance.Float16:
			node.VectorF16 = storedVector.([]uint16)
		case distance.Int8:
			node.VectorI8 = storedVector.([]int8)
		}
		newNodes[i] = node
		h.nodes[internalID] = node
		h.externalToInternalID[node.Id] = internalID
		h.internalToExternalID[internalID] = node.Id
	}
	h.metaMu.Unlock()

	linkQueue := make(chan LinkRequest, numVectors*h.mMax0)
	var wg sync.WaitGroup
	batchSizePerWorker := numVectors / numWorkers

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		start := i * batchSizePerWorker
		end := start + batchSizePerWorker
		if i == numWorkers-1 {
			end = numVectors
		}
		go h.batchWorker(newNodes[start:end], linkQueue, &wg)
	}

	wg.Wait()
	close(linkQueue)
	h.commitLinks(linkQueue, newNodes)
	return nil
}

// batchWorker processes a subset of nodes for AddBatch, calculating their
// optimal neighbors without modifying the global graph topology yet.
func (h *Index) batchWorker(nodesToProcess []*Node, linkQueue chan<- LinkRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	for _, node := range nodesToProcess {
		// The vector in the node is already "stored" (f32 normalized, f16 or i8).
		// We can use it directly as a query for distanceToQuery.
		var queryObj any
		switch h.precision {
		case distance.Float32:
			queryObj = node.VectorF32
		case distance.Float16:
			queryObj = node.VectorF16
		case distance.Int8:
			queryObj = node.VectorI8
		}

		level := h.randomLevel()
		node.Connections = make([][]uint32, level+1)

		if h.maxLevel == -1 {
			continue
		}

		currentEntryPoint := h.entrypointID
		for l := h.maxLevel; l > level; l-- {
			nearest, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, 1, l, nil, 1, uint32(h.nodeCounter.Load()))
			if err != nil || len(nearest) == 0 {
				currentEntryPoint = h.entrypointID
				continue
			}
			currentEntryPoint = nearest[0].Id
		}

		for l := min(level, h.maxLevel); l >= 0; l-- {
			candidates, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, h.efConstruction, l, nil, h.efConstruction, uint32(h.nodeCounter.Load()))
			if err != nil || len(candidates) == 0 {
				currentEntryPoint = h.entrypointID
				continue
			}

			neighborIDs := make([]uint32, len(candidates))
			for i, neighbor := range candidates {
				neighborIDs[i] = neighbor.Id
			}
			linkQueue <- LinkRequest{
				NodeID:       node.InternalID,
				Level:        l,
				NewNeighbors: neighborIDs,
			}
			currentEntryPoint = candidates[0].Id
		}
	}
}
*/

// commitLinks applies the queued connection changes to the graph.
// It handles bidirectional connections and performs pruning.
// OPTIMIZATION: Performs pruning and updating in parallel to reduce Lock time.
func (h *Index) commitLinks(linkQueue <-chan LinkRequest, newNodes []*Node) {
	// Aggregate all requests into a temporary map
	linkCandidates := make(map[uint32]map[int][]uint32)

	for req := range linkQueue {
		if _, ok := linkCandidates[req.NodeID]; !ok {
			linkCandidates[req.NodeID] = make(map[int][]uint32)
		}
		linkCandidates[req.NodeID][req.Level] = append(linkCandidates[req.NodeID][req.Level], req.NewNeighbors...)
	}

	// Add bidirectional links (if A -> B, then B should consider A)
	for nodeID, levels := range linkCandidates {
		for level, neighbors := range levels {
			for _, neighborID := range neighbors {
				if _, ok := linkCandidates[neighborID]; !ok {
					linkCandidates[neighborID] = make(map[int][]uint32)
				}
				linkCandidates[neighborID][level] = append(linkCandidates[neighborID][level], nodeID)
			}
		}
	}

	// Set to quickly identify new nodes (used in heuristics)
	newNodeIDSet := make(map[uint32]struct{}, len(newNodes))
	for _, node := range newNodes {
		newNodeIDSet[node.InternalID] = struct{}{}
	}

	// --- CRITICAL SECTION (Global Lock) ---
	// h.metaMu.Lock()
	// defer h.metaMu.Unlock()

	// Prepare Jobs for Parallel Execution
	// Convert map to slice for division among workers
	type updateJob struct {
		NodeID uint32
		Levels map[int][]uint32
	}

	jobs := make([]updateJob, 0, len(linkCandidates))
	for nid, lvls := range linkCandidates {
		jobs = append(jobs, updateJob{NodeID: nid, Levels: lvls})
	}

	// Parallel Execution
	// Each worker handles a disjoint subset of nodes.
	// Thread-safe because each node is modified by only one worker.
	numWorkers := runtime.NumCPU()
	var wg sync.WaitGroup

	var sharedJobIdx uint64
	totalJobs := uint64(len(jobs))

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				// Grab next available job index
				currentIdx := atomic.AddUint64(&sharedJobIdx, 1) - 1
				if currentIdx >= totalJobs {
					return // No more jobs
				}

				job := jobs[currentIdx]

				// getting shrd lock (only for this node)
				shardParams := h.getShard(job.NodeID)

				shardParams.Lock()

				node := h.nodes[job.NodeID]
				if node == nil {
					shardParams.Unlock()
					continue
				}

				for level, candidates := range job.Levels {
					// Ensure Connections slice is large enough
					if level >= len(node.Connections) {
						newConnections := make([][]uint32, level+1)
						copy(newConnections, node.Connections)
						node.Connections = newConnections
					}

					maxConns := h.m
					if level == 0 {
						maxConns = h.mMax0
					}

					// --- Neighbor Selection (Pruning) ---

					// Merge existing neighbors + new candidates
					candidateSet := make(map[uint32]struct{})
					for _, id := range node.Connections[level] {
						candidateSet[id] = struct{}{}
					}
					for _, id := range candidates {
						candidateSet[id] = struct{}{}
					}
					delete(candidateSet, job.NodeID) // No self-loops

					// Fast Path: if within limit, keep everything
					if len(candidateSet) <= maxConns {
						finalNeighbors := make([]uint32, 0, len(candidateSet))
						for id := range candidateSet {
							finalNeighbors = append(finalNeighbors, id)
						}
						node.Connections[level] = finalNeighbors
						continue
					}

					// Heavy Path: need to select best neighbors
					uniqueCandidates := make([]uint32, 0, len(candidateSet))
					for id := range candidateSet {
						uniqueCandidates = append(uniqueCandidates, id)
					}

					// Pre-sampling if too many candidates (avoid CPU stall)
					const pruningThreshold = 2000
					if len(uniqueCandidates) > pruningThreshold {
						// Simple shuffle
						rand.Shuffle(len(uniqueCandidates), func(i, j int) {
							uniqueCandidates[i], uniqueCandidates[j] = uniqueCandidates[j], uniqueCandidates[i]
						})
						uniqueCandidates = uniqueCandidates[:pruningThreshold]
					}

					// Calculate distances
					allCandidates := make([]types.Candidate, 0, len(uniqueCandidates))
					for _, id := range uniqueCandidates {
						targetNode := h.nodes[id]
						if targetNode == nil {
							continue
						}
						dist, _ := h.distanceBetweenNodes(node, targetNode)
						allCandidates = append(allCandidates, types.Candidate{Id: id, Distance: dist})
					}

					// Sort by distance (using slices.SortFunc - faster than sort.Slice)
					slices.SortFunc(allCandidates, func(a, b types.Candidate) int {
						// Confronto esplicito per float64
						if a.Distance < b.Distance {
							return -1
						}
						if a.Distance > b.Distance {
							return 1
						}
						return 0
					})

					// Final Selection
					var prunedNeighbors []types.Candidate
					prunedNeighbors = h.selectNeighbors(allCandidates, maxConns)
					/*
						if _, isNewNode := newNodeIDSet[job.NodeID]; isNewNode {
							// Full heuristic for new nodes (better quality)
							prunedNeighbors = h.selectNeighbors(allCandidates, maxConns)
						} else {
							// Keep-Best for existing nodes (faster)
							limit := maxConns
							if limit > len(allCandidates) {
								limit = len(allCandidates)
							}
							prunedNeighbors = allCandidates[:limit]
						}
					*/

					// Final update
					prunedIDs := make([]uint32, len(prunedNeighbors))
					for k, p := range prunedNeighbors {
						prunedIDs[k] = p.Id
					}
					node.Connections[level] = prunedIDs
				}
				shardParams.Unlock()
			}
		}()
	}

	wg.Wait() // Wait for all workers to finish pruning

	// Update EntryPoint and MaxLevel
	var overallMaxLevel = h.maxLevel
	var bestEntryPoint = h.entrypointID
	h.metaMu.Lock()
	for _, node := range newNodes {
		nodeLevel := len(node.Connections) - 1
		if nodeLevel > overallMaxLevel {
			overallMaxLevel = nodeLevel
			bestEntryPoint = node.InternalID
		}
	}
	h.maxLevel = overallMaxLevel
	h.entrypointID = bestEntryPoint
	h.metaMu.Unlock()
}

// GetExternalID returns the external string ID associated with an internal uint32 ID
func (h *Index) GetExternalID(internalID uint32) (string, bool) {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	externalID, found := h.internalToExternalID[internalID]
	return externalID, found
}

// Delete marks a node as deleted (soft delete).
// It does not remove the node from the graph to maintain structural stability.
func (h *Index) Delete(id string) {
	h.metaMu.Lock()
	defer h.metaMu.Unlock()

	// lookup internal ID matching external ID
	internalID, ok := h.externalToInternalID[id]
	if !ok {
		return
	}

	// Verify ID exists within slice bounds
	if internalID < uint32(len(h.nodes)) {
		node := h.nodes[internalID]
		// Verify node is actually initialized
		if node != nil {
			node.Deleted = true
		}
	}
	// ------------------------------------

	// Remove ID from the external lookup map to prevent
	// the same ID from being added again in the future
	delete(h.externalToInternalID, id)
}

func (h *Index) searchLayer(query []float32, entrypointID uint32, k int, level int, allowList map[uint32]struct{}, efSearch int) ([]types.Candidate, error) {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	// Buffer for searchLayerUnlocked output.
	// Must be at least as large as efConstruction
	scratchOut := make([]types.Candidate, 0, h.efConstruction)

	return h.searchLayerUnlocked(query, entrypointID, k, level, allowList, efSearch, uint32(h.nodeCounter.Load()), scratchOut)
}

// searchLayerUnlocked performs a greedy search on a specific layer.
// OPTIMIZED: Uses value semantics and loop devirtualization.
func (h *Index) searchLayerUnlocked(query any, entrypointID uint32, k int, level int, allowList map[uint32]struct{}, efSearch int, maxID uint32, out []types.Candidate) ([]types.Candidate, error) {

	// 1. Setup Data Structures (Zero Alloc)
	visited := h.visitedPool.Get().(*BitSet)
	candidates := h.minHeapPool.Get().(*minHeap)
	results := h.maxHeapPool.Get().(*maxHeap)

	// Fast reset (keeps underlying slice capacity)
	*candidates = (*candidates)[:0]
	*results = (*results)[:0]

	// Ensure cleanup on return
	defer func() {
		visited.Clear()
		h.visitedPool.Put(visited)
		h.minHeapPool.Put(candidates)
		h.maxHeapPool.Put(results)
	}()

	visited.EnsureCapacity(maxID)

	// Calculate effective ef
	ef := efSearch
	if ef < k {
		ef = k
	}

	// 2. DEVIRTUALIZATION (Lift switch out of loop)
	// Create a local 'distFn' specialized for this call.
	// This allows the CPU to predict exactly what to call inside the loop

	var distFn func(node *Node) (float64, error)

	switch h.precision {
	case distance.Float32:
		q := query.([]float32) // Cast query once
		fn := h.distFuncF32    // Direct pointer to function (e.g., AVX)

		distFn = func(node *Node) (float64, error) {
			v := node.VectorF32
			return fn(q, v)
		}

	case distance.Float16:
		q := query.([]uint16)
		fn := h.distFuncF16
		distFn = func(node *Node) (float64, error) {
			v := node.VectorF16
			return fn(q, v)
		}

	case distance.Int8:
		q := query.([]int8)
		fn := h.distFuncI8

		// Pre-calculate query norm once
		var qNormSq int64
		for _, v := range q {
			qNormSq += int64(v) * int64(v)
		}
		qNorm := float32(math.Sqrt(float64(qNormSq)))
		if qNorm == 0 {
			qNorm = 1
		}

		distFn = func(node *Node) (float64, error) {
			stored := node.VectorI8

			dot, err := fn(q, stored)
			if err != nil {
				return 0, err
			}

			storedNorm := h.quantizedNorms[node.InternalID]
			if storedNorm == 0 {
				return 1.0, nil
			}

			/*

				// Inline scaling logic to avoid call overhead
				var norm1, norm2 int64
				for i := range q {
					norm1 += int64(q[i]) * int64(q[i])
					norm2 += int64(stored[i]) * int64(stored[i])
				}
				if norm1 == 0 || norm2 == 0 {
					return 1.0, nil
				}
			*/

			similarity := float64(dot) / (float64(qNorm) * float64(storedNorm))
			if similarity > 1.0 {
				similarity = 1.0
			}
			if similarity < -1.0 {
				similarity = -1.0
			}
			return 1.0 - similarity, nil
		}

	default:
		return nil, fmt.Errorf("precision not setup")
	}

	// 3. Initialize Entry Point
	entryNode := h.nodes[entrypointID]
	if entryNode == nil {
		return nil, fmt.Errorf("entry point node %d not found", entrypointID)
	}

	// Calculate initial distance using optimized function
	dist, err := distFn(entryNode)
	if err != nil {
		return nil, err
	}

	// Create Value Type (on stack)
	ep := types.Candidate{Id: entrypointID, Distance: dist}
	candidates.Push(ep)
	visited.Add(entrypointID)

	isEpValid := true
	if allowList != nil {
		if _, ok := allowList[entrypointID]; !ok {
			isEpValid = false
		}
	}

	if isEpValid && !entryNode.Deleted {
		results.Push(ep)
	}

	// 4. HOT LOOP (The bottleneck)
	for candidates.Len() > 0 {
		current := candidates.Pop() // Returns value, not pointer

		// "Lower Bound" Optimization:
		// If the best candidate we extracted is worse than the worst result we are keeping,
		// we cannot find anything better by following this path.
		if results.Len() >= ef {
			worstResult := results.Peek() // MaxHeap: Peek returns the farthest (worst)
			if current.Distance > worstResult.Distance {
				break
			}
		}

		// Safe slice access (Bounds Check Elimination hint for compiler)
		if current.Id >= uint32(len(h.nodes)) {
			continue
		}
		currentNode := h.nodes[current.Id]

		// Skip nil nodes or non-existent levels
		if currentNode == nil || level >= len(currentNode.Connections) {
			continue
		}

		// Iterate over neighbors
		for _, neighborID := range currentNode.Connections[level] {
			// BitSet filter (very fast)
			if visited.Has(neighborID) {
				continue
			}
			visited.Add(neighborID)

			// AllowList filter (for boolean filters)
			if allowList != nil {
				if _, ok := allowList[neighborID]; !ok {
					continue
				}
			}

			if neighborID >= uint32(len(h.nodes)) {
				continue
			}
			neighborNode := h.nodes[neighborID]
			if neighborNode == nil {
				continue
			}

			// --- DISTANCE CALCULATION ---
			// Using closure 'distFn' instead of 'h.distanceToQuery'.
			// Saves switch and useless function calls.
			d, err := distFn(neighborNode)
			if err != nil {
				continue
			}

			// Result update logic
			worstDist := float64(math.MaxFloat64)
			if results.Len() > 0 {
				worstDist = results.Peek().Distance
			}

			if results.Len() < ef || d < worstDist {
				neighborCandidate := types.Candidate{Id: neighborID, Distance: d}

				// always add to candidates to continue graph exploration
				candidates.Push(neighborCandidate)

				// Add to results ONLY if not deleted
				if !neighborNode.Deleted {
					results.Push(neighborCandidate)

					if results.Len() > ef {
						results.Pop() // Remove the farthest
					}
				}
			}
		}
	}

	// 5. Finalize Results
	count := results.Len()
	if cap(out) < count {
		out = make([]types.Candidate, count)
	}
	out = out[:count] // Logical resize

	for i := count - 1; i >= 0; i-- {
		out[i] = results.Pop()
	}

	if len(out) > k {
		return out[:k], nil
	}

	return out, nil
}

// randomLevel selects a random level for a new node based on an exponentially decaying probability distribution.
func (h *Index) randomLevel() int {
	// This is based on an exponentially decreasing distribution
	level := 0
	for rand.Float64() < 0.5 && level < h.maxLevel+1 { // Let's add a limit for safety
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
	discarded := make([]types.Candidate, 0, m) // Keep track of discarded candidates
	worklist := candidates

	for len(worklist) > 0 && len(results) < m {
		e := worklist[0]
		worklist = worklist[1:]

		if len(results) == 0 {
			results = append(results, e)
			continue
		}

		isGoodCandidate := true
		for _, r := range results {
			d, err := h.distanceBetweenNodes(h.nodes[e.Id], h.nodes[r.Id])
			if err != nil {
				isGoodCandidate = false
				break
			}

			var condition bool
			if h.metric == distance.Cosine {
				condition = (d < e.Distance)
			} else {
				condition = (d < e.Distance)
			}

			if condition {
				isGoodCandidate = false
				break
			}
		}

		if isGoodCandidate {
			results = append(results, e)
		} else {
			discarded = append(discarded, e)
		}
	}

	// 2. Strategy Boosts Recall
	// If the heuristic has been too aggressive and there are fewer than M connections,
	// we fill the remaining slots with the best discarded candidates.
	// This should prevent the creation of isolated or weakly connected nodes.
	if len(results) < m {
		needed := m - len(results)
		for _, cand := range discarded {
			results = append(results, cand)
			needed--
			if needed == 0 {
				break
			}
		}
	}

	return results
}

// --- Helper ---

// Helper to get the correct lock
func (h *Index) getShard(id uint32) *sync.RWMutex {
	return &h.shardsMu[id&(NumShards-1)] // id % 128
}

// growNodes ensures the internal nodes slice has enough capacity for the given ID.
// Must be called under Lock.
func (h *Index) growNodes(id uint32) {
	if uint32(len(h.nodes)) <= id {
		// If ID is out of range, expand.
		// Doubling strategy to amortize allocation costs
		newCap := uint32(cap(h.nodes))
		if newCap == 0 {
			newCap = 1024
		}
		for newCap <= id {
			newCap *= 2
		}

		newNodes := make([]*Node, newCap)
		copy(newNodes, h.nodes)

		// The "new" part of the slice is nil
		// Update main slice but set correct length to include new ID
		h.nodes = newNodes

		if h.precision == distance.Int8 {
			newNorms := make([]float32, newCap)
			copy(newNorms, h.quantizedNorms)
			h.quantizedNorms = newNorms
		}
	}

	// Extend logical length (len) if necessary to cover the ID
	if uint32(len(h.nodes)) <= id {
		h.nodes = h.nodes[:id+1]

		if h.precision == distance.Int8 {
			h.quantizedNorms = h.quantizedNorms[:id+1]
		}
	}
}

// Iterate loops over all non-deleted nodes and passes the external ID and vector
// (always as []float32) to a callback function.
func (h *Index) Iterate(callback func(id string, vector []float32)) {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	for _, node := range h.nodes {
		if node == nil {
			continue
		}
		if !node.Deleted {
			// --- CONVERSION LOGIC ---
			var vectorF32 []float32

			// Check the type of the stored vector
			switch h.precision {
			case distance.Float32:
				vectorF32 = node.VectorF32
			case distance.Float16:
				// If it's float16, we need to unpack it into float32
				vectorF32 = make([]float32, len(node.VectorF16))
				for i, v := range node.VectorF16 {
					vectorF32[i] = float16.Frombits(v).Float32()
				}
			case distance.Int8:
				// If it's int8, we need to de-quantize it.
				if h.quantizer == nil {
					log.Printf("WARNING: int8 index missing quantizer for node %s", node.Id)
					continue
				}
				vectorF32 = h.quantizer.Dequantize(node.VectorI8)
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
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	for _, node := range h.nodes {
		if node == nil {
			continue
		}
		if !node.Deleted {
			switch h.precision {
			case distance.Float32:
				callback(node.Id, node.VectorF32)
			case distance.Float16:
				callback(node.Id, node.VectorF16)
			case distance.Int8:
				callback(node.Id, node.VectorI8)
			}
		}
	}
}

// GetInternalID retrieves the internal ID for a given external ID.
func (h *Index) GetInternalID(externalID string) uint32 {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
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
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	internalID, ok := h.externalToInternalID[externalID]
	if !ok {
		return types.NodeData{}, false
	}

	// --- SLICE SAFETY LOGIC ---
	// Bounds check: ID must be less than slice length
	if internalID >= uint32(len(h.nodes)) {
		return types.NodeData{}, false
	}

	// Safe access
	node := h.nodes[internalID]

	// Nil and Deleted check
	if node == nil || node.Deleted {
		return types.NodeData{}, false
	}
	// ------------------------------------

	// Use the same logic as 'Iterate' to de-compress/de-quantize
	var vectorF32 []float32
	switch h.precision {
	case distance.Float32:
		vectorF32 = node.VectorF32
	case distance.Float16:
		vectorF32 = make([]float32, len(node.VectorF16))
		for i, v := range node.VectorF16 {
			vectorF32[i] = float16.Frombits(v).Float32()
		}
	case distance.Int8:
		if h.quantizer == nil {
			return types.NodeData{}, false // Inconsistent state
		}
		vectorF32 = h.quantizer.Dequantize(node.VectorI8)
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
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()

	// We only count the non-deleted nodes
	count := 0
	for _, node := range h.nodes {
		if node == nil {
			continue
		}
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
		if node == nil {
			continue
		}
		if !node.Deleted {
			count++
		}
	}
	return h.metric, h.m, h.efConstruction, h.precision, count, h.textLanguage
}

// normalize normalizes a vector to unit length (L2 norm).
// This method modifies the slice in-place.
func normalizeOld(v []float32) {

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

	// calculate the new length for verification
	var finalNorm float32
	for _, val := range v {
		finalNorm += val * val
	}

}

// inverse square root helper
func invSqrt(n float32) float32 {
	return 1.0 / float32(math.Sqrt(float64(n)))
}

func normalize(v []float32) {
	var normSq float32
	for _, val := range v {
		normSq += val * val
	}
	if normSq > 0 {
		invNorm := invSqrt(normSq)
		for i := range v {
			v[i] *= invNorm
		}
	}
}

// RLock acquires a read lock on the index. For use by external callers like the DB.
func (h *Index) RLock() {
	h.metaMu.RLock()
}

// RUnlock releases a read lock.
func (h *Index) RUnlock() {
	h.metaMu.RUnlock()
}

// GetParametersUnlocked returns parameters without a lock.
func (h *Index) GetParametersUnlocked() (distance.DistanceMetric, distance.PrecisionType, int, int) {
	return h.metric, h.precision, h.m, h.efConstruction
}

// SnapshotData exports the internal data of the index for persistence.
// It expects the caller to handle locking.
func (h *Index) SnapshotData() (map[uint32]*Node, map[string]uint32, uint32, uint32, int, *distance.Quantizer, []float32) {
	// This method expects the caller to have already acquired a lock.

	nodesMap := make(map[uint32]*Node, len(h.nodes))

	for internalID, node := range h.nodes {
		if node != nil {
			node.InternalID = uint32(internalID)
			nodesMap[uint32(internalID)] = node
		}
	}

	normsCopy := make([]float32, len(h.quantizedNorms))
	copy(normsCopy, h.quantizedNorms)

	return nodesMap, h.externalToInternalID, uint32(h.nodeCounter.Load()), h.entrypointID, h.maxLevel, h.quantizer, normsCopy
}

// LoadSnapshotData restores the internal state of the index from snapshot data.
// It expects the index to be empty and the caller to handle locking.
func (h *Index) LoadSnapshotData(
	nodesMap map[uint32]*Node, // Renamed 'nodes' to 'nodesMap' for clarity
	extToInt map[string]uint32,
	counter uint32,
	entrypoint uint32,
	maxLevel int,
	quantizer *distance.Quantizer,
	norms []float32,
) error {
	// 1. Reconstruct h.nodes slice from input map.
	// Capacity must cover up to the maximum ID (counter)
	capacity := counter + 1
	h.nodes = make([]*Node, capacity)

	for id, node := range nodesMap {
		if id >= uint32(len(h.nodes)) {
			// Sanity check. If snapshot file is consistent, 'counter' should be >= any ID in the map
			return fmt.Errorf("node ID %d found in snapshot is larger than the recorded max counter %d", id, counter)
		}
		h.nodes[id] = node
	}

	// 2. Restore other state fields
	h.externalToInternalID = extToInt
	h.nodeCounter.Store(uint64(counter))
	h.entrypointID = entrypoint
	h.maxLevel = maxLevel
	h.quantizer = quantizer

	// 3. Basic consistency checks
	if h.nodes == nil {
		// If map was empty, init empty slice with base capacity
		h.nodes = make([]*Node, 0, 1000)
	}
	if h.externalToInternalID == nil {
		h.externalToInternalID = make(map[string]uint32)
	}

	// 4. Reconstruct inverse map (Internal -> External)
	h.internalToExternalID = make(map[uint32]string)

	// Iterate over newly populated h.nodes slice
	// Handle "holes" (nil) in the slice
	for i, node := range h.nodes {
		if node == nil {
			continue // Skip empty slots
		}

		internalID := uint32(i) // Index is the internal ID

		if node.Id == "" {
			return fmt.Errorf("node with internal ID %d has an empty external ID", internalID)
		}

		h.internalToExternalID[internalID] = node.Id

		// Cross-check consistency with loaded External -> Internal map
		if existingInternal, ok := h.externalToInternalID[node.Id]; ok && existingInternal != internalID {
			return fmt.Errorf("ID inconsistency: external '%s' is mapped to %d but node at index %d has this ID", node.Id, existingInternal, internalID)
		}

		// Restore map if missing
		h.externalToInternalID[node.Id] = internalID

		// Ensure InternalID field is synced with its position
		node.InternalID = internalID
	}

	if h.precision == distance.Int8 {
		h.quantizedNorms = norms
	}

	return nil
}

// Metric returns the distance metric used by the index.
func (h *Index) Metric() distance.DistanceMetric {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	return h.metric
}

// Precision returns the data precision used by the index.
func (h *Index) Precision() distance.PrecisionType {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	return h.precision
}

// M returns the HNSW M parameter (max connections for layer > 0).
func (h *Index) M() int {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	return h.m
}

// EfConstruction returns the HNSW efConstruction parameter.
func (h *Index) EfConstruction() int {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	return h.efConstruction
}

// Quantizer returns a pointer to the index's quantizer.
func (h *Index) Quantizer() *distance.Quantizer {
	return h.quantizer
}

// TextLanguage returns the language configured for text analysis.
func (h *Index) TextLanguage() string {
	h.metaMu.RLock()
	defer h.metaMu.RUnlock()
	return h.textLanguage
}

func computeInt8Norm(vec []int8) float32 {
	var sum int64
	for _, v := range vec {
		sum += int64(v) * int64(v)
	}
	return float32(math.Sqrt(float64(sum)))
}
