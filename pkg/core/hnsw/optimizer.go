package hnsw

import (
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// GraphOptimizer manages background maintenance tasks.
type GraphOptimizer struct {
	index  *Index
	config AutoMaintenanceConfig

	lastVacuumTime time.Time
	lastRefineTime time.Time
	lastScanIdx    int // Cyclic cursor for refinement

	mu sync.Mutex
}

func NewOptimizer(index *Index, cfg AutoMaintenanceConfig) *GraphOptimizer {
	return &GraphOptimizer{
		index:  index,
		config: cfg,
	}
}

// UpdateConfig updates settings on the fly.
func (o *GraphOptimizer) UpdateConfig(cfg AutoMaintenanceConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.config = cfg
	slog.Info("[Optimizer] Config updated for index", "vacuum", cfg.VacuumInterval, "refine", cfg.RefineEnabled)
}

// GetConfig returns the current configuration.
func (o *GraphOptimizer) GetConfig() AutoMaintenanceConfig {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.config
}

// RunCycle checks timers and executes tasks. Called by the Engine ticker.
func (o *GraphOptimizer) RunCycle(forceType string) bool {
	// Acquire the optimizer lock to read state and config
	o.mu.Lock()
	cfg := o.config
	lastVac := o.lastVacuumTime
	lastRef := o.lastRefineTime
	o.mu.Unlock()

	now := time.Now()
	didWork := false

	// --- 1. VACUUM CHECK ---
	shouldVacuum := (forceType == "vacuum")

	// If not forced, check the policies
	if !shouldVacuum && time.Duration(cfg.VacuumInterval) > 0 {
		// A. Check Time Interval
		timePassed := now.Sub(lastVac) >= time.Duration(cfg.VacuumInterval)

		// B. Check Delete Threshold (if enabled)
		thresholdMet := false
		if timePassed {
			// To avoid heavy locks, we do a quick estimate using RLock on the index
			o.index.metaMu.RLock()
			nodes := o.index.nodes
			total := len(nodes)
			deleted := 0
			// Fast scan for statistics (very fast in RAM)
			for _, n := range nodes {
				if n != nil && n.Deleted.Load() {
					deleted++
				}
			}
			o.index.metaMu.RUnlock()

			if total > 0 {
				ratio := float64(deleted) / float64(total)
				// If ratio > threshold (e.g. 0.1), activate
				if ratio >= cfg.DeleteThreshold {
					thresholdMet = true
				}
			}
		}

		if timePassed && thresholdMet {
			shouldVacuum = true
		}
	}

	if shouldVacuum {
		if o.Vacuum() {
			didWork = true
		}
		o.mu.Lock()
		o.lastVacuumTime = now
		o.mu.Unlock()
		return didWork // Vacuum has priority over Refine
	}

	// --- 2. REFINE CHECK ---
	shouldRefine := (forceType == "refine")
	if !shouldRefine && cfg.RefineEnabled && time.Duration(cfg.RefineInterval) > 0 {
		if now.Sub(lastRef) >= time.Duration(cfg.RefineInterval) {
			shouldRefine = true
		}
	}

	if shouldRefine {
		if o.Refine() {
			didWork = true
		}
		o.mu.Lock()
		o.lastRefineTime = now
		o.mu.Unlock()
	}

	return didWork
}

// Vacuum scans for deleted nodes, repairs links pointing to them, and frees memory.
// This implementation uses an incremental approach to minimize lock contention:
// Phase 1: Identify deleted nodes (RLock)
// Phase 2: Repair affected nodes in batches (Lock per batch, released between batches)
// Phase 3: Final cleanup (Lock)
func (o *GraphOptimizer) Vacuum() bool {
	const repairBatchSize = 100 // Nodes to repair per lock acquisition

	// =========================================================================
	// PHASE 1: IDENTIFY DELETED NODES (RLock - allows concurrent queries)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes := o.index.nodes
	totalNodes := len(nodes)

	deletedSet := make(map[uint32]struct{})
	for i, node := range nodes {
		if node != nil && node.Deleted.Load() {
			deletedSet[uint32(i)] = struct{}{}
		}
	}
	o.index.metaMu.RUnlock()

	if len(deletedSet) == 0 {
		return false
	}

	slog.Info("[Optimizer] Vacuum: Found deleted nodes. Repairing graph", "deleted_nodes", len(deletedSet))

	// =========================================================================
	// PHASE 2: IDENTIFY NODES NEEDING REPAIR (RLock)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes = o.index.nodes // Refresh reference

	// Collect nodes that need repair
	nodesToRepair := make([]*Node, 0, totalNodes/10) // Heuristic pre-allocation
	for _, node := range nodes {
		if node == nil || node.Deleted.Load() {
			continue
		}
		// Check if any neighbor is dead
		needsRepair := false
		for _, layer := range node.Connections {
			for _, neighborID := range layer {
				if _, isDead := deletedSet[neighborID]; isDead {
					needsRepair = true
					break
				}
			}
			if needsRepair {
				break
			}
		}
		if needsRepair {
			nodesToRepair = append(nodesToRepair, node)
		}
	}
	o.index.metaMu.RUnlock()

	// =========================================================================
	// PHASE 3: REPAIR IN BATCHES (Lock per batch, released between batches)
	// =========================================================================
	repairedCount := 0
	for i := 0; i < len(nodesToRepair); i += repairBatchSize {
		end := i + repairBatchSize
		if end > len(nodesToRepair) {
			end = len(nodesToRepair)
		}
		batch := nodesToRepair[i:end]

		// Lock for this batch
		o.index.metaMu.Lock()

		for _, node := range batch {
			// Verify the node is still valid (it may have changed)
			if node == nil || node.Deleted.Load() {
				continue
			}
			o.reconnectNode(node, deletedSet)
			repairedCount++
		}

		o.index.metaMu.Unlock()
		// Lock released - queries can execute between batches
	}

	// =========================================================================
	// PHASE 4: FINAL CLEANUP (Exclusive Lock)
	// =========================================================================
	o.index.metaMu.Lock()
	defer o.index.metaMu.Unlock()

	nodes = o.index.nodes // Refresh reference

	// Entry Point Fix
	if _, entryIsDead := deletedSet[o.index.entrypointID.Load()]; entryIsDead {
		slog.Info("[Optimizer] Entry point was deleted. Electing new entry point...")
		newEntryFound := false

		for i, node := range nodes {
			if node != nil && !node.Deleted.Load() {
				o.index.entrypointID.Store(uint32(i))
				o.index.maxLevel.Store(int32(len(node.Connections) - 1))
				newEntryFound = true
				break
			}
		}

		if !newEntryFound {
			o.index.entrypointID.Store(0)
			o.index.maxLevel.Store(-1)
			slog.Info("[Optimizer] Graph is now empty")
		}
	}

	// Physical Cleanup
	for deadID := range deletedSet {
		if extID, ok := o.index.internalToExternalID[deadID]; ok {
			delete(o.index.externalToInternalID, extID)
			delete(o.index.internalToExternalID, deadID)
		}
		nodes[deadID] = nil // Free RAM
	}

	slog.Info("[Optimizer] Vacuum complete", "repaired_parents", repairedCount, "removed_nodes", len(deletedSet))
	return true
}

// refineResult holds the computed new connections for a single node.
// Used to separate computation (read-only) from commit (write).
type refineResult struct {
	nodeID      uint32
	connections [][]uint32 // [layer] -> []neighborIDs
}

// Refine improves the graph quality by re-evaluating neighbors for a batch of nodes.
// This implementation uses parallel computation with sharded commit for better performance.
func (o *GraphOptimizer) Refine() bool {
	// =========================================================================
	// PHASE 0: PREPARATION (Brief lock to read state)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes := o.index.nodes
	totalNodes := len(nodes)
	entryPoint := o.index.entrypointID.Load()
	maxID := uint32(o.index.nodeCounter.Load())
	o.index.metaMu.RUnlock()

	if totalNodes == 0 {
		return false
	}

	// Read optimizer configuration
	o.mu.Lock()
	start := o.lastScanIdx
	batchSize := o.config.RefineBatchSize
	ef := o.config.RefineEfConstruction
	o.mu.Unlock()

	if ef == 0 {
		ef = o.index.efConstruction
	}

	// Calculate range
	if start >= totalNodes {
		start = 0
	}
	end := start + batchSize
	if end > totalNodes {
		end = totalNodes
	}

	// Update cursor for next cycle
	nextStart := end
	if nextStart >= totalNodes {
		nextStart = 0
	}
	o.mu.Lock()
	o.lastScanIdx = nextStart
	o.mu.Unlock()

	// Collect valid nodes to process
	nodesToProcess := make([]*Node, 0, end-start)
	o.index.metaMu.RLock()
	for i := start; i < end; i++ {
		node := nodes[i]
		if node != nil && !node.Deleted.Load() {
			nodesToProcess = append(nodesToProcess, node)
		}
	}
	o.index.metaMu.RUnlock()

	if len(nodesToProcess) == 0 {
		return false
	}

	// =========================================================================
	// PHASE 1: PARALLEL COMPUTATION (Read-Only, CPU Bound)
	// =========================================================================
	numWorkers := runtime.NumCPU()
	if len(nodesToProcess) < numWorkers {
		numWorkers = len(nodesToProcess)
	}

	// Each worker writes to its own slice (no lock needed)
	workerResults := make([][]refineResult, numWorkers)
	nodesPerWorker := (len(nodesToProcess) + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wStart := w * nodesPerWorker
		wEnd := wStart + nodesPerWorker
		if wEnd > len(nodesToProcess) {
			wEnd = len(nodesToProcess)
		}
		if wStart >= wEnd {
			continue
		}

		wg.Add(1)
		go func(workerID int, nodeSlice []*Node) {
			defer wg.Done()

			localResults := make([]refineResult, 0, len(nodeSlice))

			// RLock to read the graph during search
			o.index.metaMu.RLock()
			defer o.index.metaMu.RUnlock()

			for _, node := range nodeSlice {
				newConns := o.computeNewConnections(node, entryPoint, ef, maxID, nil)
				if newConns != nil {
					localResults = append(localResults, refineResult{
						nodeID:      node.InternalID,
						connections: newConns,
					})
				}
			}

			workerResults[workerID] = localResults
		}(w, nodesToProcess[wStart:wEnd])
	}

	wg.Wait()

	// =========================================================================
	// PHASE 2: SHARDED COMMIT (Write, Lock per Shard)
	// =========================================================================
	// Partition results by shard
	shardedResults := make([][]refineResult, NumShards)
	for _, results := range workerResults {
		for _, res := range results {
			shardIdx := res.nodeID & (NumShards - 1)
			shardedResults[shardIdx] = append(shardedResults[shardIdx], res)
		}
	}

	// Parallel commit by shard
	var wgCommit sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	for shardID := 0; shardID < NumShards; shardID++ {
		results := shardedResults[shardID]
		if len(results) == 0 {
			continue
		}

		wgCommit.Add(1)
		sem <- struct{}{}

		go func(sID int, shardResults []refineResult) {
			defer wgCommit.Done()
			defer func() { <-sem }()

			// Lock only this shard
			o.index.shardsMu[sID].Lock()
			defer o.index.shardsMu[sID].Unlock()

			for _, res := range shardResults {
				node := o.index.nodes[res.nodeID]
				if node == nil || node.Deleted.Load() {
					continue // Node deleted during computation
				}

				// Apply the new connections
				for l := 0; l < len(res.connections) && l < len(node.Connections); l++ {
					if res.connections[l] != nil {
						node.Connections[l] = res.connections[l]
					}
				}
			}
		}(shardID, results)
	}

	wgCommit.Wait()

	return len(nodesToProcess) > 0
}

// computeNewConnections calculates the best neighbors for a node without modifying it.
// Returns nil if no valid connections could be computed.
// Caller must hold at least RLock on metaMu.
func (o *GraphOptimizer) computeNewConnections(node *Node, entryPoint uint32, ef int, maxID uint32, ignoreSet map[uint32]struct{}) [][]uint32 {
	var queryObj any
	switch o.index.precision {
	case distance.Float32:
		queryObj = node.VectorF32
	case distance.Float16:
		queryObj = node.VectorF16
	case distance.Int8:
		queryObj = node.VectorI8
	}

	numLayers := len(node.Connections)
	if numLayers == 0 {
		return nil
	}

	newConnections := make([][]uint32, numLayers)

	for l := 0; l < numLayers; l++ {
		// 1. Search for candidates
		candidates, err := o.index.searchLayerUnlocked(queryObj, entryPoint, ef, l, nil, ef, maxID, nil)
		if err != nil {
			// Keep existing connections for this layer
			newConnections[l] = node.Connections[l]
			continue
		}

		// 2. Add valid existing neighbors
		currentNeighbors := node.Connections[l]
		for _, nID := range currentNeighbors {
			if ignoreSet != nil {
				if _, ignore := ignoreSet[nID]; ignore {
					continue
				}
			}

			// Avoid duplicates
			alreadyIn := false
			for _, c := range candidates {
				if c.Id == nID {
					alreadyIn = true
					break
				}
			}

			if !alreadyIn {
				target := o.index.nodes[nID]
				if target != nil && !target.Deleted.Load() {
					dist, _ := o.index.distanceBetweenNodes(node, target)
					candidates = append(candidates, types.Candidate{Id: nID, Distance: dist})
				}
			}
		}

		// 3. Filter self and ignoreSet
		validCandidates := make([]types.Candidate, 0, len(candidates))
		for _, c := range candidates {
			if c.Id == node.InternalID {
				continue
			}
			if ignoreSet != nil {
				if _, ignore := ignoreSet[c.Id]; ignore {
					continue
				}
			}
			validCandidates = append(validCandidates, c)
		}

		// 4. Sort by distance
		sort.Slice(validCandidates, func(i, j int) bool {
			return validCandidates[i].Distance < validCandidates[j].Distance
		})

		// 5. Select neighbors
		maxM := o.index.m
		if l == 0 {
			maxM = o.index.mMax0
		}

		selected := o.index.selectNeighbors(validCandidates, maxM)

		// 6. Convert to ID slice
		newConns := make([]uint32, len(selected))
		for i, c := range selected {
			newConns[i] = c.Id
		}
		newConnections[l] = newConns
	}

	return newConnections
}

// reconnectNode performs a local HNSW search to find the best neighbors for 'node',
// excluding any ID in 'ignoreSet'. Used for both repair and refinement.
// Caller must hold Index lock.
func (o *GraphOptimizer) reconnectNode(node *Node, ignoreSet map[uint32]struct{}) {
	var queryObj any
	switch o.index.precision {
	case distance.Float32:
		queryObj = node.VectorF32
	case distance.Float16:
		queryObj = node.VectorF16
	case distance.Int8:
		queryObj = node.VectorI8
	}

	ef := o.config.RefineEfConstruction
	if ef == 0 {
		ef = o.index.efConstruction
	}

	/*
		if ignoreSet != nil {
			// VACUUM BOOST: Double ef during repair to skip over holes
			ef = ef * 2
		}
	*/

	// We re-link the node at each layer it belongs to
	for l := 0; l < len(node.Connections); l++ {
		// 1. Search for candidates using the node itself as query
		// Pass 'nil' as allowList because we want global candidates.
		candidates, err := o.index.searchLayerUnlocked(queryObj, o.index.entrypointID.Load(), ef, l, nil, ef, uint32(o.index.nodeCounter.Load()), nil)
		if err != nil {
			continue
		}

		// 2. Filter dead nodes & Add existing valid neighbors
		// We want to keep existing good connections that might not be found by search
		currentNeighbors := node.Connections[l]
		for _, nID := range currentNeighbors {
			// Check if ignored (deleted)
			if ignoreSet != nil {
				if _, ignore := ignoreSet[nID]; ignore {
					continue
				}
			}

			// Avoid duplicates in candidates list (simple check)
			alreadyIn := false
			for _, c := range candidates {
				if c.Id == nID {
					alreadyIn = true
					break
				}
			}

			if !alreadyIn {
				target := o.index.nodes[nID]
				if target != nil && !target.Deleted.Load() {
					dist, _ := o.index.distanceBetweenNodes(node, target)
					candidates = append(candidates, types.Candidate{Id: nID, Distance: dist})
				}
			}
		}

		// 3. Remove any candidates that are in ignoreSet or are the node itself
		validCandidates := make([]types.Candidate, 0, len(candidates))
		for _, c := range candidates {
			if c.Id == node.InternalID {
				continue
			}
			if ignoreSet != nil {
				if _, ignore := ignoreSet[c.Id]; ignore {
					continue
				}
			}
			validCandidates = append(validCandidates, c)
		}

		// 4. Sort
		sort.Slice(validCandidates, func(i, j int) bool {
			return validCandidates[i].Distance < validCandidates[j].Distance
		})

		// 5. Select Neighbors (Using Heuristic)
		maxM := o.index.m
		if l == 0 {
			maxM = o.index.mMax0
		}

		selected := o.index.selectNeighbors(validCandidates, maxM)

		// 6. Update
		newConns := make([]uint32, len(selected))
		for i, c := range selected {
			newConns[i] = c.Id
		}
		node.Connections[l] = newConns
	}
}
