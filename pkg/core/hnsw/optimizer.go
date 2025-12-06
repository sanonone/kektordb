package hnsw

import (
	"log"
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
func (o *GraphOptimizer) Vacuum() bool {
	o.index.metaMu.Lock()
	defer o.index.metaMu.Unlock()

	nodes := o.index.nodes
	deletedSet := make(map[uint32]struct{})

	// 1. Identify
	for i, node := range nodes {
		if node != nil && node.Deleted {
			deletedSet[uint32(i)] = struct{}{}
		}
	}

	if len(deletedSet) == 0 {
		return false
	}

	log.Printf("[Optimizer] Vacuum: Found %d deleted nodes. Repairing graph...", len(deletedSet))

	// 2. Scan & Repair (Linear scan of all nodes to find parents)
	repairedCount := 0
	for _, node := range nodes {
		if node == nil || node.Deleted {
			continue
		}

		needsRepair := false
		// Check if any neighbor is dead
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
			o.reconnectNode(node, deletedSet)
			repairedCount++
		}
	}

	// --- FIX ENTRY POINT ---
	// Se stiamo cancellando l'Entry Point corrente, dobbiamo eleggerne uno nuovo tra i vivi.
	if _, entryIsDead := deletedSet[o.index.entrypointID]; entryIsDead {
		log.Println("[Optimizer] Entry point was deleted. Electing new entry point...")
		newEntryFound := false

		// Cerchiamo il primo nodo vivo
		for i, node := range nodes {
			// Nota: i nodi in deletedSet hanno node.Deleted=true, quindi questo check basta
			if node != nil && !node.Deleted {
				o.index.entrypointID = uint32(i)
				// Aggiorniamo anche il maxLevel per sicurezza prendendo quello del nuovo entry
				o.index.maxLevel = len(node.Connections) - 1
				newEntryFound = true
				break
			}
		}

		if !newEntryFound {
			// Il grafo è vuoto
			o.index.entrypointID = 0
			o.index.maxLevel = -1
			log.Println("[Optimizer] Graph is now empty.")
		}
	}

	// 3. Physical Cleanup
	for deadID := range deletedSet {
		if extID, ok := o.index.internalToExternalID[deadID]; ok {
			delete(o.index.externalToInternalID, extID)
			delete(o.index.internalToExternalID, deadID)
		}
		nodes[deadID] = nil // Free RAM

		// Note: We do not compact the 'nodes' slice or 'quantizedNorms'
		// to preserve ID mapping. 'nil' slots will be reused in future versions
		// or just left as holes.
	}

	log.Printf("[Optimizer] Vacuum complete. Repaired %d parents, removed %d nodes.", repairedCount, len(deletedSet))
	return true
}

// Refine improves the graph quality by re-evaluating neighbors for a batch of nodes.
func (o *GraphOptimizer) Refine() bool {
	o.index.metaMu.Lock()
	defer o.index.metaMu.Unlock()

	nodes := o.index.nodes
	totalNodes := len(nodes)
	if totalNodes == 0 {
		return false
	}

	o.mu.Lock()
	start := o.lastScanIdx
	batchSize := o.config.RefineBatchSize
	o.mu.Unlock()

	if start >= totalNodes {
		start = 0
	}
	end := start + batchSize
	if end > totalNodes {
		end = totalNodes
	}

	// Update cursor for next run
	nextStart := end
	if nextStart >= totalNodes {
		nextStart = 0
	}

	o.mu.Lock()
	o.lastScanIdx = nextStart
	o.mu.Unlock()

	refinedCount := 0
	for i := start; i < end; i++ {
		node := nodes[i]
		if node == nil || node.Deleted {
			continue
		}
		// Reconnect without ignoring anything (just finding better neighbors)
		o.reconnectNode(node, nil)
		refinedCount++
	}

	return refinedCount > 0
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
