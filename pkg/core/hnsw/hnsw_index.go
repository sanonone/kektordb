// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// graph algorithm for efficient approximate nearest neighbor search.
//
// This file contains the core Index struct and its associated methods for building,
// searching, and managing the HNSW graph. It supports multiple distance metrics,
// data precisions (including float32, float16, and int8 quantization), and
// concurrent access.
package hnsw

import (
	// "container/heap"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/x448/float16"
	"log"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// LinkRequest rappresenta una singola operazione di modifica delle connessioni
// da applicare al grafo in modo atomico. Sarà usata dalla AddBatch concorrente
// e in futuro potrà essere estesa per la riparazione del grafo.
type LinkRequest struct {
	NodeID       uint32
	Level        int
	NewNeighbors []uint32
}

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

	nodes []*Node

	// Separate maps to translate between external and internal IDs
	externalToInternalID map[string]uint32
	internalToExternalID map[uint32]string
	nodeCounter          atomic.Uint64

	// Stores the precision of the index (e.g., f32, f16)
	precision distance.PrecisionType

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
	}

	h.nodeCounter.Store(0)

	h.visitedPool = sync.Pool{
		New: func() any {
			return NewBitSet(256)
		},
	}

	h.minHeapPool = sync.Pool{
		New: func() any { return newMinHeap(efConstruction) },
	}
	h.maxHeapPool = sync.Pool{
		New: func() any { return newMaxHeap(efConstruction) },
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
		// Per ora, float16 supporta solo Euclidean
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

// distanceToQuery ORA È "DUMB". Accetta 'query' come 'any' GIA' PREPARATA.
// Niente normalizzazioni, niente allocazioni, niente quantizzazione qui.
func (h *Index) distanceToQuery(query any, storedVector any) (float64, error) {
	switch fn := h.distanceFunc.(type) {
	case distance.DistanceFuncF32:
		// Assumiamo che query sia []float32. Panico se non lo è (segno di bug a monte)
		q := query.([]float32)
		stored, ok := storedVector.([]float32)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch")
		}
		return fn(q, stored)

	case distance.DistanceFuncF16:
		// La query DEVE arrivare già come []uint16
		q := query.([]uint16)
		stored, ok := storedVector.([]uint16)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch")
		}
		return fn(q, stored)

	case distance.DistanceFuncI8:
		// La query DEVE arrivare già come []int8
		q := query.([]int8)
		stored, ok := storedVector.([]int8)
		if !ok {
			return 0, fmt.Errorf("stored vector type mismatch")
		}

		dot, err := fn(q, stored)

		// Scaling logic (inevitabile per Int8)
		var norm1, norm2 int64
		for i := range q {
			norm1 += int64(q[i]) * int64(q[i])
			norm2 += int64(stored[i]) * int64(stored[i])
		}
		if norm1 == 0 || norm2 == 0 {
			return 1.0, nil
		}
		similarity := float64(dot) / (math.Sqrt(float64(norm1)) * math.Sqrt(float64(norm2)))
		if similarity > 1.0 {
			similarity = 1.0
		}
		if similarity < -1.0 {
			similarity = -1.0
		}
		return 1.0 - similarity, err

	default:
		return 0, fmt.Errorf("invalid distance function")
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

// searchInternal gestisce la PRE-ELABORAZIONE della query UNA VOLTA SOLA.
func (h *Index) searchInternal(query []float32, k int, allowList map[uint32]struct{}, efSearch int) ([]types.Candidate, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.maxLevel == -1 {
		return []types.Candidate{}, nil
	}

	// --- FASE 0: Preparazione della Query (O(D) una volta sola) ---
	// 1. Normalizzazione (se Cosine)
	var queryF32 []float32
	if h.metric == distance.Cosine {
		// Copia per non modificare l'originale
		queryF32 = make([]float32, len(query))
		copy(queryF32, query)
		normalize(queryF32)
	} else {
		queryF32 = query // Usa direttamente il puntatore alla slice originale se non dobbiamo normalizzare
	}

	// 2. Adattamento al tipo di precisione (any)
	var finalQuery any
	switch h.precision {
	case distance.Float32:
		finalQuery = queryF32
	case distance.Float16:
		// Converti float32 -> uint16
		qF16 := make([]uint16, len(queryF32))
		for i, v := range queryF32 {
			qF16[i] = float16.Fromfloat32(v).Bits()
		}
		finalQuery = qF16
	case distance.Int8:
		// Quantizza float32 -> int8
		if h.quantizer == nil {
			return nil, fmt.Errorf("quantizer missing")
		}
		finalQuery = h.quantizer.Quantize(queryF32)
	}
	// --- FINE FASE 0 ---

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
		nearest, err := h.searchLayerUnlocked(finalQuery, currentEntryPoint, 1, l, allowList, 0, uint32(h.nodeCounter.Load()))
		if err != nil {
			return nil, err
		}
		if len(nearest) == 0 {
			return []types.Candidate{}, fmt.Errorf("search failed at level %d", l)
		}
		currentEntryPoint = nearest[0].Id
	}

	// 2) Base layer search
	nearestNeighbors, err := h.searchLayerUnlocked(finalQuery, currentEntryPoint, k, 0, allowList, efSearch, uint32(h.nodeCounter.Load()))
	if err != nil {
		return nil, err
	}

	return nearestNeighbors, nil
}

// Add inserts a new vector (Single Insert)
func (h *Index) Add(id string, vector []float32) (uint32, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.externalToInternalID[id]; exists {
		return 0, fmt.Errorf("ID '%s' already exists", id)
	}

	// Pre-processing locale
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
		if h.quantizer == nil || h.quantizer.AbsMax == 0 {
			return 0, fmt.Errorf("quantizer not trained")
		}
		storedVector = h.quantizer.Quantize(vector)
	}

	internalID := uint32(h.nodeCounter.Add(1))
	h.growNodes(internalID) // Assicura spazio
	node := &Node{Id: id, Vector: storedVector}
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

	// --- Prepara il vettore per la ricerca (Query Object) ---
	// Nota: Add usa il vettore inserito come query per trovare i vicini.
	// Poiché 'storedVector' è già del tipo corretto (f32, f16, o i8), lo usiamo direttamente.
	queryObj := storedVector

	currentEntryPoint := h.entrypointID
	for l := h.maxLevel; l > level; l-- {
		nearest, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, 1, l, nil, 1, uint32(h.nodeCounter.Load()))
		if err != nil {
			return 0, err
		}
		currentEntryPoint = nearest[0].Id
	}

	for l := min(level, h.maxLevel); l >= 0; l-- {
		neighbors, err := h.searchLayerUnlocked(queryObj, currentEntryPoint, h.efConstruction, l, nil, h.efConstruction, uint32(h.nodeCounter.Load()))
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
					d, _ := h.distance(neighborNode.Vector, h.nodes[nID].Vector)
					if d > maxDist {
						maxDist = d
						worstNeighborIndex = i
					}
				}
				distToNew, _ := h.distance(neighborNode.Vector, node.Vector)
				if distToNew < maxDist && worstNeighborIndex != -1 {
					neighborNode.Connections[l][worstNeighborIndex] = internalID
				}
			}
		}
		currentEntryPoint = neighbors[0].Id
	}

	if level > h.maxLevel {
		h.maxLevel = level
		h.entrypointID = internalID
	}
	return internalID, nil
}

/*
// BatchObject è un wrapper per i dati di input di AddBatch.
type BatchObject struct {
	ID     string
	Vector []float32
}
*/

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
	numWorkers := runtime.NumCPU()

	h.mu.RLock()
	currentSize := h.nodeCounter.Load()
	h.mu.RUnlock()

	if currentSize < uint64(h.efConstruction) {
		for _, obj := range objects {
			h.Add(obj.Id, obj.Vector)
		}
		return nil
	}

	if numVectors < numWorkers {
		numWorkers = numVectors
	}

	h.mu.Lock()
	startID := h.nodeCounter.Add(uint64(numVectors)) - uint64(numVectors)
	lastID := uint32(startID + uint64(numVectors) - 1)

	// Pre-alloca tutto lo spazio necessario in un colpo solo
	h.growNodes(lastID)
	// Nota: newNodes serve ancora per passare i nodi ai worker,
	// ma popoliamo anche h.nodes globale.
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
				h.mu.Unlock()
				return fmt.Errorf("quantizer not trained")
			}
			storedVector = h.quantizer.Quantize(obj.Vector)
		}

		node := &Node{Id: obj.Id, InternalID: internalID, Vector: storedVector}
		newNodes[i] = node
		h.nodes[internalID] = node
		h.externalToInternalID[node.Id] = internalID
		h.internalToExternalID[internalID] = node.Id
	}
	h.mu.Unlock()

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

func (h *Index) batchWorker(nodesToProcess []*Node, linkQueue chan<- LinkRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, node := range nodesToProcess {
		// Il vettore nel nodo è già "stored" (quindi f32 normalizzato, f16 o i8).
		// Possiamo usarlo direttamente come query per distanceToQuery.
		queryObj := node.Vector

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

// commitLinks completata e ottimizzata.
func (h *Index) commitLinks(linkQueue <-chan LinkRequest, newNodes []*Node) {
	linkCandidates := make(map[uint32]map[int][]uint32)

	for req := range linkQueue {
		if _, ok := linkCandidates[req.NodeID]; !ok {
			linkCandidates[req.NodeID] = make(map[int][]uint32)
		}
		linkCandidates[req.NodeID][req.Level] = append(linkCandidates[req.NodeID][req.Level], req.NewNeighbors...)
	}

	// Aggiungi link bidirezionali
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

	newNodeIDSet := make(map[uint32]struct{}, len(newNodes))
	for _, node := range newNodes {
		newNodeIDSet[node.InternalID] = struct{}{}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for nodeID, levels := range linkCandidates {
		node := h.nodes[nodeID]
		if node == nil {
			continue
		}

		for level, candidates := range levels {
			if level >= len(node.Connections) {
				newConnections := make([][]uint32, level+1)
				copy(newConnections, node.Connections)
				node.Connections = newConnections
			}

			maxConns := h.m
			if level == 0 {
				maxConns = h.mMax0
			}

			// --- LOGICA DI CAMPIONAMENTO AGGIORNATA ---

			// Filtriamo e deduplichiamo i candidati
			candidateSet := make(map[uint32]struct{})
			// Aggiungi i vicini attuali
			for _, id := range node.Connections[level] {
				candidateSet[id] = struct{}{}
			}
			// Aggiungi i nuovi candidati proposti
			for _, id := range candidates {
				candidateSet[id] = struct{}{}
			}

			delete(candidateSet, nodeID) // Rimuovi self-loops

			if len(candidateSet) <= maxConns {
				finalNeighbors := make([]uint32, 0, len(candidateSet))
				for id := range candidateSet {
					finalNeighbors = append(finalNeighbors, id)
				}
				node.Connections[level] = finalNeighbors
				continue
			}

			// Se abbiamo troppi candidati, dobbiamo selezionare i migliori.
			// Creiamo la lista per selectNeighbors.

			// OTTIMIZZAZIONE: Se candidateSet è ENORME (es. > 500), calcolare la distanza
			// per tutti è lentissimo. Facciamo un pre-campionamento se necessario.
			// Questo serve a evitare lock contention prolungato.

			uniqueCandidates := make([]uint32, 0, len(candidateSet))
			for id := range candidateSet {
				uniqueCandidates = append(uniqueCandidates, id)
			}

			// Se superiamo una soglia critica, riduciamo il numero di calcoli distanza.
			const pruningThreshold = 3000
			if len(uniqueCandidates) > pruningThreshold {
				// Mescoliamo solo se necessario
				rand.Shuffle(len(uniqueCandidates), func(i, j int) {
					uniqueCandidates[i], uniqueCandidates[j] = uniqueCandidates[j], uniqueCandidates[i]
				})
				uniqueCandidates = uniqueCandidates[:pruningThreshold]
			}

			allCandidates := make([]types.Candidate, 0, len(uniqueCandidates))
			for _, id := range uniqueCandidates {
				targetNode := h.nodes[id]
				if targetNode == nil {
					continue
				} // Paranoia check

				dist, _ := h.distance(node.Vector, targetNode.Vector)
				allCandidates = append(allCandidates, types.Candidate{Id: id, Distance: dist})
			}

			sort.Slice(allCandidates, func(i, j int) bool { return allCandidates[i].Distance < allCandidates[j].Distance })

			var prunedNeighbors []types.Candidate
			if _, isNewNode := newNodeIDSet[nodeID]; isNewNode {
				// Euristica HNSW completa per i nodi nuovi
				prunedNeighbors = h.selectNeighbors(allCandidates, maxConns)
			} else {
				// Semplice "Keep Best" per manutenzione nodi esistenti
				limit := maxConns
				if limit > len(allCandidates) {
					limit = len(allCandidates)
				}
				prunedNeighbors = allCandidates[:limit]
			}

			prunedIDs := make([]uint32, len(prunedNeighbors))
			for i, p := range prunedNeighbors {
				prunedIDs[i] = p.Id
			}
			node.Connections[level] = prunedIDs
		}
	}

	var overallMaxLevel = h.maxLevel
	var bestEntryPoint = h.entrypointID
	for _, node := range newNodes {
		nodeLevel := len(node.Connections) - 1
		if nodeLevel > overallMaxLevel {
			overallMaxLevel = nodeLevel
			bestEntryPoint = node.InternalID
		}
	}
	h.maxLevel = overallMaxLevel
	h.entrypointID = bestEntryPoint
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

	// --- NUOVA LOGICA SICUREZZA SLICE ---
	// Verifica se l'ID esiste nella slice
	if internalID < uint32(len(h.nodes)) {
		node := h.nodes[internalID]
		// Verifica se il nodo è effettivamente inizializzato
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
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Passa h.internalCounter come maxID
	return h.searchLayerUnlocked(query, entrypointID, k, level, allowList, efSearch, uint32(h.nodeCounter.Load()))
}

// searchLayerUnlocked esegue la ricerca greedy su un livello specifico.
// OTTIMIZZATO: Value Semantics + Devirtualizzazione Loop.
func (h *Index) searchLayerUnlocked(query any, entrypointID uint32, k int, level int, allowList map[uint32]struct{}, efSearch int, maxID uint32) ([]types.Candidate, error) {

	// 1. Setup Strutture Dati (Zero Alloc)
	visited := h.visitedPool.Get().(*BitSet)
	candidates := h.minHeapPool.Get().(*minHeap)
	results := h.maxHeapPool.Get().(*maxHeap)

	// Reset rapido (mantiene la capacità della slice sottostante)
	*candidates = (*candidates)[:0]
	*results = (*results)[:0]

	// Assicuriamo la pulizia al ritorno
	defer func() {
		visited.Clear()
		h.visitedPool.Put(visited)
		h.minHeapPool.Put(candidates)
		h.maxHeapPool.Put(results)
	}()

	visited.EnsureCapacity(maxID)

	// Calcolo ef effettivo
	ef := efSearch
	if ef < k {
		ef = k
	}

	// 2. DEVIRTUALIZZAZIONE (Solleva lo switch fuori dal loop)
	// Creiamo una funzione locale 'distFn' specializzata per questa chiamata.
	// Questo permette alla CPU di sapere esattamente cosa chiamare dentro il for.

	var distFn func(node *Node) (float64, error)

	// Nota: purtroppo Node.Vector è ancora 'any', quindi un cast serve.
	// Ma lo facciamo in un contesto tipizzato specifico.
	switch h.precision {
	case distance.Float32:
		q := query.([]float32) // Cast query una volta sola
		fn := h.distFuncF32    // Puntatore diretto alla funzione (es. Assembly AVX)

		distFn = func(node *Node) (float64, error) {
			// Unsafe optimization (se ci fidiamo ciecamente):
			// v := *(*[]float32)(unsafe.Pointer(&node.Vector))
			// Safe version:
			v, ok := node.Vector.([]float32)
			if !ok {
				return 0, fmt.Errorf("type mismatch")
			}
			return fn(q, v)
		}

	case distance.Float16:
		q := query.([]uint16)
		fn := h.distFuncF16
		distFn = func(node *Node) (float64, error) {
			v, ok := node.Vector.([]uint16)
			if !ok {
				return 0, fmt.Errorf("type mismatch")
			}
			return fn(q, v)
		}

	case distance.Int8:
		q := query.([]int8)
		fn := h.distFuncI8

		// Per Int8 serve la logica di normalizzazione extra
		distFn = func(node *Node) (float64, error) {
			stored, ok := node.Vector.([]int8)
			if !ok {
				return 0, fmt.Errorf("type mismatch")
			}

			dot, err := fn(q, stored)
			if err != nil {
				return 0, err
			}

			// Scaling logic inline per evitare call overhead
			var norm1, norm2 int64
			for i := range q {
				norm1 += int64(q[i]) * int64(q[i])
				norm2 += int64(stored[i]) * int64(stored[i])
			}
			if norm1 == 0 || norm2 == 0 {
				return 1.0, nil
			}

			similarity := float64(dot) / (math.Sqrt(float64(norm1)) * math.Sqrt(float64(norm2)))
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

	// 3. Inizializzazione Entry Point
	entryNode := h.nodes[entrypointID]
	if entryNode == nil {
		return nil, fmt.Errorf("entry point node %d not found", entrypointID)
	}

	// Calcolo distanza iniziale usando la funzione ottimizzata
	dist, err := distFn(entryNode)
	if err != nil {
		return nil, err
	}

	// Creazione Value Type (sullo stack)
	ep := types.Candidate{Id: entrypointID, Distance: dist}
	candidates.Push(ep)
	results.Push(ep)
	visited.Add(entrypointID)

	// 4. HOT LOOP (Il collo di bottiglia)
	for candidates.Len() > 0 {
		current := candidates.Pop() // Restituisce valore, non puntatore

		// Ottimizzazione "Lower Bound":
		// Se il candidato migliore che abbiamo estratto è peggiore del peggiore risultato che teniamo,
		// non possiamo trovare nulla di meglio seguendo questo percorso.
		if results.Len() >= ef {
			worstResult := results.Peek() // MaxHeap: Peek restituisce il più distante (peggiore)
			if current.Distance > worstResult.Distance {
				break
			}
		}

		// Accesso sicuro alla slice (Bounds Check Elimination hint per il compilatore)
		if current.Id >= uint32(len(h.nodes)) {
			continue
		}
		currentNode := h.nodes[current.Id]

		// Skip nodi nil o livelli non esistenti
		if currentNode == nil || level >= len(currentNode.Connections) {
			continue
		}

		// Iterazione sui vicini
		for _, neighborID := range currentNode.Connections[level] {
			// Filtro BitSet (molto veloce)
			if visited.Has(neighborID) {
				continue
			}
			visited.Add(neighborID)

			// Filtro AllowList (per filtri booleani)
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

			// --- CALCOLO DISTANZA ---
			// Qui usiamo la closure 'distFn' invece di 'h.distanceToQuery'.
			// Risparmiamo lo switch e chiamate di funzione inutili.
			d, err := distFn(neighborNode)
			if err != nil {
				continue
			}

			// Logica di aggiornamento risultati
			worstDist := float64(math.MaxFloat64)
			if results.Len() > 0 {
				worstDist = results.Peek().Distance
			}

			if results.Len() < ef || d < worstDist {
				neighborCandidate := types.Candidate{Id: neighborID, Distance: d}

				// Aggiungiamo SEMPRE ai candidati per continuare l'esplorazione del grafo
				candidates.Push(neighborCandidate)

				// FIX IMPORTANTE: Aggiungiamo ai risultati SOLO se non è cancellato.
				// I nodi cancellati servono da ponte ma non devono essere ritornati.
				if !neighborNode.Deleted {
					results.Push(neighborCandidate)

					// Manteniamo la dimensione fissa 'ef'
					if results.Len() > ef {
						results.Pop() // Rimuovi il più lontano
					}
				}
			}
		}
	}

	// 5. Finalizzazione Risultati
	// Estraiamo dall'heap. Poiché è un MaxHeap, Pop() restituisce dal più grande al più piccolo.
	// Dobbiamo invertire l'ordine se vogliamo (Vicino -> Lontano), ma selectNeighbors gestisce l'ordine.
	// Tuttavia, per coerenza con searchInternal, restituiamo ordinati asc (distanza minore prima).
	count := results.Len()
	finalResults := make([]types.Candidate, count)
	for i := count - 1; i >= 0; i-- {
		finalResults[i] = results.Pop()
	}

	// Se abbiamo trovato più di k risultati, tagliamo (dopo aver ordinato implicitamente col loop sopra)
	if len(finalResults) > k {
		return finalResults[:k], nil
	}

	return finalResults, nil
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
	worklist := candidates // Copia dello slice header (economico)

	for len(worklist) > 0 && len(results) < m {
		e := worklist[0]
		worklist = worklist[1:]

		if len(results) == 0 {
			results = append(results, e)
			continue
		}

		isGoodCandidate := true
		for _, r := range results {
			// Qui usiamo 'r' direttamente perché è un valore, non un puntatore.
			// Nota: Accesso a h.nodes potrebbe causare cache miss, ma è euristica.
			dist_e_r, err := h.distance(h.nodes[e.Id].Vector, h.nodes[r.Id].Vector)
			if err != nil {
				isGoodCandidate = false
				break
			}

			var condition bool
			if h.metric == distance.Cosine {
				condition = (dist_e_r <= e.Distance)
			} else {
				condition = (dist_e_r < e.Distance)
			}

			if condition {
				isGoodCandidate = false
				break
			}
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

// growNodes assicura che la slice nodes sia abbastanza grande per contenere id.
// Deve essere chiamata sotto Lock.
func (h *Index) growNodes(id uint32) {
	if uint32(len(h.nodes)) <= id {
		// Se l'ID è fuori range, espandiamo.
		// Strategia di raddoppio per ammortizzare i costi di allocazione.
		newCap := uint32(cap(h.nodes))
		if newCap == 0 {
			newCap = 1024
		}
		for newCap <= id {
			newCap *= 2
		}

		newNodes := make([]*Node, newCap)
		copy(newNodes, h.nodes)

		// La parte "nuova" della slice è nil, che va bene.
		// Aggiorniamo la slice principale ma impostiamo la lunghezza corretta
		// per includere il nuovo ID.
		h.nodes = newNodes
	}

	// Estendiamo la lunghezza logica (len) se necessario per coprire id
	if uint32(len(h.nodes)) <= id {
		h.nodes = h.nodes[:id+1]
	}
}

// Iterate loops over all non-deleted nodes and passes the external ID and vector
// (always as []float32) to a callback function.
func (h *Index) Iterate(callback func(id string, vector []float32)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, node := range h.nodes {
		if node == nil {
			continue
		}
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
		if node == nil {
			continue
		}
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

	// --- NUOVA LOGICA SICUREZZA SLICE ---
	// 1. Controllo Limiti: L'ID deve essere inferiore alla lunghezza della slice
	if internalID >= uint32(len(h.nodes)) {
		return types.NodeData{}, false
	}

	// 2. Accesso sicuro
	node := h.nodes[internalID]

	// 3. Controllo Nil e Deleted
	if node == nil || node.Deleted {
		return types.NodeData{}, false
	}
	// ------------------------------------

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

	// Calcoliamo la nuova lunghezza per verifica
	var finalNorm float32
	for _, val := range v {
		finalNorm += val * val
	}

}

// Funzione helper per l'inverse square root (puoi trovarne implementazioni
// in Go online o usare math.Float32bits e una traduzione diretta dall'originale in C).
// Per semplicità, usiamo una versione leggermente ottimizzata.
func invSqrt(n float32) float32 {
	return 1.0 / float32(math.Sqrt(float64(n)))
}

func normalize(v []float32) {
	var normSq float32
	for _, val := range v {
		normSq += val * val
	}
	if normSq > 0 {
		// Calcola 1 / norm invece di norm, in un solo passaggio.
		invNorm := invSqrt(normSq)
		for i := range v {
			// Moltiplicazione invece di divisione.
			v[i] *= invNorm
		}
	}
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

	// Creiamo una mappa temporanea per rispettare la firma della funzione e il formato di salvataggio
	nodesMap := make(map[uint32]*Node, len(h.nodes))

	for internalID, node := range h.nodes {
		// Importante: Saltiamo i nil e i nodi vuoti se ce ne sono
		if node != nil {
			node.InternalID = uint32(internalID) // Assicuriamoci che l'ID sia syncato
			nodesMap[uint32(internalID)] = node
		}
	}

	return nodesMap, h.externalToInternalID, uint32(h.nodeCounter.Load()), h.entrypointID, h.maxLevel, h.quantizer
}

// LoadSnapshotData restores the internal state of the index from snapshot data.
// It expects the index to be empty and the caller to handle locking.
func (h *Index) LoadSnapshotData(
	nodesMap map[uint32]*Node, // Rinominato 'nodes' in 'nodesMap' per chiarezza
	extToInt map[string]uint32,
	counter uint32,
	entrypoint uint32,
	maxLevel int,
	quantizer *distance.Quantizer,
) error {
	// 1. Ricostruzione della slice h.nodes dalla mappa in input.
	// La capacità deve coprire fino all'ID massimo (counter).
	// +1 perché gli ID partono da 1 (o comunque vogliamo coprire l'indice == counter).
	capacity := counter + 1
	h.nodes = make([]*Node, capacity)

	for id, node := range nodesMap {
		if id >= uint32(len(h.nodes)) {
			// Questo è un controllo di sanità. Se il file di snapshot è coerente,
			// 'counter' dovrebbe essere >= di qualsiasi ID nella mappa.
			return fmt.Errorf("node ID %d found in snapshot is larger than the recorded max counter %d", id, counter)
		}
		h.nodes[id] = node
	}

	// 2. Ripristino degli altri campi di stato.
	h.externalToInternalID = extToInt
	h.nodeCounter.Store(uint64(counter))
	h.entrypointID = entrypoint
	h.maxLevel = maxLevel
	h.quantizer = quantizer

	// 3. Check di consistenza di base.
	if h.nodes == nil {
		// Se la mappa era vuota, inizializziamo una slice vuota con capacity di base.
		h.nodes = make([]*Node, 0, 1000)
	}
	if h.externalToInternalID == nil {
		h.externalToInternalID = make(map[string]uint32)
	}

	// 4. Ricostruzione della mappa inversa (Internal -> External).
	h.internalToExternalID = make(map[uint32]string)

	// Iteriamo sulla slice h.nodes appena popolata.
	// ATTENZIONE: Dobbiamo gestire i "buchi" (nil) nella slice.
	for i, node := range h.nodes {
		if node == nil {
			continue // Salta gli slot vuoti
		}

		internalID := uint32(i) // L'indice è l'ID interno

		if node.Id == "" {
			return fmt.Errorf("node with internal ID %d has an empty external ID", internalID)
		}

		h.internalToExternalID[internalID] = node.Id

		// Verifica di coerenza incrociata con la mappa External -> Internal caricata.
		if existingInternal, ok := h.externalToInternalID[node.Id]; ok && existingInternal != internalID {
			return fmt.Errorf("ID inconsistency: external '%s' is mapped to %d but node at index %d has this ID", node.Id, existingInternal, internalID)
		}

		// Ripristina la mappa se mancante (o sovrascrivi per sicurezza)
		h.externalToInternalID[node.Id] = internalID

		// Assicura che il campo InternalID del nodo sia sincronizzato con la sua posizione
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
