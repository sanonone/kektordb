package hnsw

import (
	"log"
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
	lastScanIdx    int // Cursore ciclico per il refinement

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
	log.Printf("[Optimizer] Config updated for index. Vacuum: %v, Refine: %v", cfg.VacuumInterval, cfg.RefineEnabled)
}

// GetConfig returns the current configuration.
func (o *GraphOptimizer) GetConfig() AutoMaintenanceConfig {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.config
}

// RunCycle checks timers and executes tasks. Called by the Engine ticker.
func (o *GraphOptimizer) RunCycle(forceType string) bool {
	// Acquisiamo il lock dell'optimizer per leggere stato e config
	o.mu.Lock()
	cfg := o.config
	lastVac := o.lastVacuumTime
	lastRef := o.lastRefineTime
	o.mu.Unlock()

	now := time.Now()
	didWork := false

	// --- 1. VACUUM CHECK ---
	shouldVacuum := (forceType == "vacuum")

	// Se non forzato, controlliamo le policy
	if !shouldVacuum && time.Duration(cfg.VacuumInterval) > 0 {
		// A. Check Intervallo Temporale
		timePassed := now.Sub(lastVac) >= time.Duration(cfg.VacuumInterval)

		// B. Check Delete Threshold (se abilitato)
		thresholdMet := false
		if timePassed {
			// Per evitare lock pesanti, facciamo una stima rapida leggendo RLock sull'indice
			o.index.metaMu.RLock()
			nodes := o.index.nodes
			total := len(nodes)
			deleted := 0
			// Scansione rapida per statistiche (molto veloce in RAM)
			for _, n := range nodes {
				if n != nil && n.Deleted {
					deleted++
				}
			}
			o.index.metaMu.RUnlock()

			if total > 0 {
				ratio := float64(deleted) / float64(total)
				// Se ratio > threshold (es. 0.1), attiviamo
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
		return didWork // Vacuum ha priorità su Refine
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
	const repairBatchSize = 100 // Nodi da riparare per lock acquisition

	// =========================================================================
	// PHASE 1: IDENTIFY DELETED NODES (RLock - permette query concorrenti)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes := o.index.nodes
	totalNodes := len(nodes)

	deletedSet := make(map[uint32]struct{})
	for i, node := range nodes {
		if node != nil && node.Deleted {
			deletedSet[uint32(i)] = struct{}{}
		}
	}
	o.index.metaMu.RUnlock()

	if len(deletedSet) == 0 {
		return false
	}

	log.Printf("[Optimizer] Vacuum: Found %d deleted nodes. Repairing graph...", len(deletedSet))

	// =========================================================================
	// PHASE 2: IDENTIFY NODES NEEDING REPAIR (RLock)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes = o.index.nodes // Refresh reference

	// Raccolta nodi che necessitano riparazione
	nodesToRepair := make([]*Node, 0, totalNodes/10) // Pre-allocazione euristica
	for _, node := range nodes {
		if node == nil || node.Deleted {
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
	// PHASE 3: REPAIR IN BATCHES (Lock per batch, rilasciato tra batch)
	// =========================================================================
	repairedCount := 0
	for i := 0; i < len(nodesToRepair); i += repairBatchSize {
		end := i + repairBatchSize
		if end > len(nodesToRepair) {
			end = len(nodesToRepair)
		}
		batch := nodesToRepair[i:end]

		// Lock per questo batch
		o.index.metaMu.Lock()

		for _, node := range batch {
			// Verifica che il nodo sia ancora valido (potrebbe essere cambiato)
			if node == nil || node.Deleted {
				continue
			}
			o.reconnectNode(node, deletedSet)
			repairedCount++
		}

		o.index.metaMu.Unlock()
		// Lock rilasciato - le query possono eseguire tra i batch
	}

	// =========================================================================
	// PHASE 4: FINAL CLEANUP (Lock esclusivo)
	// =========================================================================
	o.index.metaMu.Lock()
	defer o.index.metaMu.Unlock()

	nodes = o.index.nodes // Refresh reference

	// Entry Point Fix
	if _, entryIsDead := deletedSet[o.index.entrypointID]; entryIsDead {
		log.Println("[Optimizer] Entry point was deleted. Electing new entry point...")
		newEntryFound := false

		for i, node := range nodes {
			if node != nil && !node.Deleted {
				o.index.entrypointID = uint32(i)
				o.index.maxLevel = len(node.Connections) - 1
				newEntryFound = true
				break
			}
		}

		if !newEntryFound {
			o.index.entrypointID = 0
			o.index.maxLevel = -1
			log.Println("[Optimizer] Graph is now empty.")
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

	log.Printf("[Optimizer] Vacuum complete. Repaired %d parents, removed %d nodes.", repairedCount, len(deletedSet))
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
	// PHASE 0: PREPARATION (Lock breve per leggere stato)
	// =========================================================================
	o.index.metaMu.RLock()
	nodes := o.index.nodes
	totalNodes := len(nodes)
	entryPoint := o.index.entrypointID
	maxID := uint32(o.index.nodeCounter.Load())
	o.index.metaMu.RUnlock()

	if totalNodes == 0 {
		return false
	}

	// Leggi configurazione optimizer
	o.mu.Lock()
	start := o.lastScanIdx
	batchSize := o.config.RefineBatchSize
	ef := o.config.RefineEfConstruction
	o.mu.Unlock()

	if ef == 0 {
		ef = o.index.efConstruction
	}

	// Calcola range
	if start >= totalNodes {
		start = 0
	}
	end := start + batchSize
	if end > totalNodes {
		end = totalNodes
	}

	// Aggiorna cursore per prossimo ciclo
	nextStart := end
	if nextStart >= totalNodes {
		nextStart = 0
	}
	o.mu.Lock()
	o.lastScanIdx = nextStart
	o.mu.Unlock()

	// Raccogli nodi validi da processare
	nodesToProcess := make([]*Node, 0, end-start)
	o.index.metaMu.RLock()
	for i := start; i < end; i++ {
		node := nodes[i]
		if node != nil && !node.Deleted {
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

	// Ogni worker scrive nella propria slice (no lock necessario)
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

			// RLock per leggere il grafo durante la ricerca
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
	// Partiziona risultati per shard
	shardedResults := make([][]refineResult, NumShards)
	for _, results := range workerResults {
		for _, res := range results {
			shardIdx := res.nodeID & (NumShards - 1)
			shardedResults[shardIdx] = append(shardedResults[shardIdx], res)
		}
	}

	// Commit parallelo per shard
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

			// Lock solo questo shard
			o.index.shardsMu[sID].Lock()
			defer o.index.shardsMu[sID].Unlock()

			for _, res := range shardResults {
				node := o.index.nodes[res.nodeID]
				if node == nil || node.Deleted {
					continue // Nodo cancellato durante il calcolo
				}

				// Applica le nuove connessioni
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
			// Mantieni connessioni esistenti per questo layer
			newConnections[l] = node.Connections[l]
			continue
		}

		// 2. Aggiungi vicini esistenti validi
		currentNeighbors := node.Connections[l]
		for _, nID := range currentNeighbors {
			if ignoreSet != nil {
				if _, ignore := ignoreSet[nID]; ignore {
					continue
				}
			}

			// Evita duplicati
			alreadyIn := false
			for _, c := range candidates {
				if c.Id == nID {
					alreadyIn = true
					break
				}
			}

			if !alreadyIn {
				target := o.index.nodes[nID]
				if target != nil && !target.Deleted {
					dist, _ := o.index.distanceBetweenNodes(node, target)
					candidates = append(candidates, types.Candidate{Id: nID, Distance: dist})
				}
			}
		}

		// 3. Filtra self e ignoreSet
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

		// 4. Ordina per distanza
		sort.Slice(validCandidates, func(i, j int) bool {
			return validCandidates[i].Distance < validCandidates[j].Distance
		})

		// 5. Seleziona vicini
		maxM := o.index.m
		if l == 0 {
			maxM = o.index.mMax0
		}

		selected := o.index.selectNeighbors(validCandidates, maxM)

		// 6. Converti in slice di ID
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
			// VACUUM BOOST: Raddoppiamo ef durante la riparazione per saltare i buchi
			ef = ef * 2
		}
	*/

	// We re-link the node at each layer it belongs to
	for l := 0; l < len(node.Connections); l++ {
		// 1. Search for candidates using the node itself as query
		// Pass 'nil' as allowList because we want global candidates.
		candidates, err := o.index.searchLayerUnlocked(queryObj, o.index.entrypointID, ef, l, nil, ef, uint32(o.index.nodeCounter.Load()), nil)
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
				if target != nil && !target.Deleted {
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
