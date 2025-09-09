package store

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"
	"container/heap"
)

// rappresenta l'indice del grafo gerarchico
type HNSWIndex struct {
	// mutex per la concorrenza. Al momento lock globale poi forse gestirò più nel dettaglio 
	mu sync.RWMutex

	// parametri dell'algoritmo HNSW 
	m int // num massimo di connessine per nodo per livello > 0 [default = 16]
	mMax0 int // numero massimo di connessioni per nodo al livello 0 
	efConstruction int // dimensione della lista dinamica dei candidati durante la costruzione/inserimento [default = 200] 

	// ml è un fattore di normalizzazione per la distribuzione di probabilità dei livelli 
	ml float64

	// l'ID del primo nnodo inserito, usato come punto di partenza per tutte le ricerche ed inserimenti
	entrypointID uint32 
	// l'attuale massimo livello presente nel grafo 
	maxLevel int 

	// la mappa che contiene tutti i nodi del grafo.
	// la chiave è un id interno uint32, non l'id stringa del Node
	nodes map[uint32]*Node 

	// mappa separata per tradurre gli id esterni in id interni
	externalToInternalID map[string]uint32 
	internalCounter uint32 

	// funzione di distanza 
	distanceFunc func([]float32, []float32) (float64, error)

	// generatore num casuali per la selezione del livello 
	levelRand *rand.Rand 
}

// crea ed inizializza un nuovo indice HNSW 
func NewHNSWIndex(m int, efConstruction int) *HNSWIndex {
	return &HNSWIndex{
		m: m,
		mMax0: m*2, // è una regola comune raddoppiare m per il livello 0 
		efConstruction: efConstruction, 
		ml: 1.0 / math.Log(float64(m)),
		nodes: make(map[uint32]*Node),
		externalToInternalID: make(map[string]uint32),
		internalCounter: 0,
		maxLevel: -1, // all'inizio nessun livello 
		entrypointID: 0, // inizializzato al primo inserito 
		distanceFunc: squaredEuclideanDistance, // la funz ottimizzata 
		levelRand: rand.New(rand.NewSource(time.Now().UnixNano())),	
	}
}

// implementa il metodo dell'interfaccia VectorIndex
func (h *HNSWIndex) Search(query []float32, k int) []string {
    // la logica interna è la vecchia Search, ma deve restituire
    // solo []string, non ([]string, error).

	// h.mu.Lock()
	// defer h.mu.RUnlock()
    
    results, err := h.searchInternal(query, k) // richiama l'effettiva search 
    if err != nil {
        log.Printf("Errore durante la ricerca HNSW: %v", err)
        return []string{} // risultato vuoto in caso di errore
    }
    return results
}

// la funzione pubblica per trovare i K vicini più prossimi a un vettore di query 
func (h *HNSWIndex) searchInternal(query []float32, k int) ([]string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// se il grafo è vuoto ritorno senza risultati
	if h.maxLevel == -1 {
		return []string{}, nil 
	}

	// inizia la ricerca dal punto di ingresso globale al livello più alto 
	currentEntryPoint := h.entrypointID

	// 1) ricerca top-down iterativa per muovermi verso il layer base 
	for l := h.maxLevel; l > 0; l-- {
		nearest, err := h.searchLayer(query, currentEntryPoint, 1, l)
		if err != nil {
			return nil, err
		}
		if len(nearest) == 0 {
			// dovrebbe capitare in casi complessi o errori
			return []string{}, fmt.Errorf("ricerca fallita al livello %d, grafo potenzialmente inconsistente", l)
		}
		currentEntryPoint = nearest[0].id 
	}

	// 2) ricerca affetiva al livello 0 per trovare i vicini alla query 
	// efConstruction lo usiamo per definire la qualità della ricerca 
	nearestNeighbors, err := h.searchLayer(query, currentEntryPoint, k, 0)
	if err != nil {
		return nil, err 
	}

	// estrae gli ID esterni (string) dai risultati 
	results := make([]string, 0, len(nearestNeighbors))
	for _, neighbor := range nearestNeighbors { 
		// check per ignorare i nodi marcati come eliminati 
		if !h.nodes[neighbor.id].deleted {
			results = append(results, h.nodes[neighbor.id].id)
		}
	}

	// la lista potrebbe risultare più corta di k se ci sono nodi eliminati
	// o se il numero totali di nodi è inferiore a k 
	if len(results) > k {
		return results[:k], nil 
	}

	return results, nil 
}

// inserisce un nuovo vettore nel grafo 
func (h *HNSWIndex) Add(id string, vector []float32) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// controlo tramite mappa string-int se esiste già un 
	// nodo con l'id di quello che voglio inserire
	if _, exists := h.externalToInternalID[id]; exists {
		log.Printf("ID '%s' già esistente", id)
		return 
	}

	// assegnamento ID interno 
	h.internalCounter++
	internalID := h.internalCounter

	// creazione nuovo nodo 
	node := &Node{
		id: id, 
		vector: vector,
	}

	// aggiunta nodo all; mappe di tracciamento 
	h.nodes[internalID] = node 
	h.externalToInternalID[id] = internalID

	// scelta di un livello casuale per il nuovo nodo
	level := h.randomLevel()

	node.connections = make([][]uint32, level+1)

	// se questo è il primo nodo allora fine, diventa l'entrypoint e ritorna
	if h.maxLevel == -1 {
		h.entrypointID = internalID
		h.maxLevel = 0 
		node.connections = make([][]uint32, 1) // solo il livello 0 
		return 
	}

	// 1) --- fase di ricerca top-down per trovare i punti di ingresso a ogni livello ---

	// la ricerca inizia dal punto di ingresso globale al livello più alto [entrypointID]
	currentEntryPoint := h.entrypointID

	// scende dal livello più alto fino al livello superiore di quello assegnato al nuovo nodo (ovvero livello del nuovo nodo + 1)
	for l := h.maxLevel; l > level; l-- {
		// trova il vicino più prossimo nel livello attuale e lo usa come entry point per il livello inferiore 
		nearest, err := h.searchLayer(vector, currentEntryPoint, 1, l)
		if err != nil {
			return 
		}
		// si assime che searchLayer restituisca sempre un risultato se l'entry point è valido 
		currentEntryPoint = nearest[0].id 
	}

	// 2) --- inserimento bottom up e connessione dei vicini ---
	// per ogni livello dal più basso fino al livello 0 
	// trova i vicini e li connette al nuovo nodo
	for l := min(level, h.maxLevel); l>= 0; l-- {
		// trova gli efConstruction candidati più vicini per questo livello 
		neighbors, err := h.searchLayer(vector, currentEntryPoint, h.efConstruction, l)
		if err != nil {
			 return 
		}

		// seleziona gli M migliori vicini da connettere 
		maxConns := h.m 
		if l == 0{
			maxConns = h.mMax0
		}

		selectedNeighbors := h.selectNeighbors(neighbors, maxConns)

		// connette il nuovo nodo ai vicini selezionati 
		node.connections[l] = make([]uint32, len(selectedNeighbors))
		for i, neighborCandidate := range selectedNeighbors {
			node.connections[l][i] = neighborCandidate.id
		}

		// connette anche i vicini al nuovo nodo (bidirezionale)
		for _, neighborCandidate := range selectedNeighbors {
			neighborNode := h.nodes[neighborCandidate.id]

			neighborLevel := len(neighborNode.connections) - 1 
			// se il nostro livello di inserimento 'l' è superiore al livello 
			// massimo del vicino, il vicino non può avere una connessione di ritorno 
			if l > neighborLevel {
				continue 
			}

			// prendo la lista dei vicini attuali a quel livello 
			neighborConnections := neighborNode.connections[l]




			// controllo che il vicino non superi il suo numero massimo di connessioni 
			if len(neighborConnections) < maxConns { // c'è spazio, aggiunge semplicemente la connessione 
				neighborNode.connections[l] = append(neighborConnections, internalID)
			} else {

				// --- IMPLEMENTAZIONE DEL PRUNING SEMPLIFICATO ---
				// Il vicino è pieno. Dobbiamo decidere se il nostro nuovo nodo
				// è un candidato migliore di uno dei suoi vicini attuali.

				// Troviamo il vicino più lontano del nostro `neighborNode`.
				maxDist := -1.0
				//worstNeighborID := uint32(0)
				worstNeighborIndex := -1

				for i, currentNeighborOfNeighborID := range neighborConnections {
					dist, _ := h.distanceFunc(neighborNode.vector, h.nodes[currentNeighborOfNeighborID].vector)
					if dist > maxDist {
						maxDist = dist
						//worstNeighborID = currentNeighborOfNeighborID
						worstNeighborIndex = i
					}
				}
				
				// Calcola la distanza tra il vicino e il nostro nuovo nodo.
				distToNewNode, _ := h.distanceFunc(neighborNode.vector, node.vector)

				// Se il nostro nuovo nodo è più vicino del vicino più lontano,
				// allora lo sostituiamo.
				if distToNewNode < maxDist {
					neighborNode.connections[l][worstNeighborIndex] = internalID
				}
			}
		}

		// per i livelli sottostanti si usa il vicino più vicino trovato 
		// come punto di ingresso per migliorare l'efficienza 
		currentEntryPoint = neighbors[0].id 
	}

		// se il nuovo nodo ha un livello più alto di qualsiasi altro,
		// diventa il nuovo punto di ingresso globale 
		if level > h.maxLevel {
			h.maxLevel = level 
			h.entrypointID = internalID
		}
		return  

}

// marca un nodo come eliminato (soft delete)
// [per il momento] non rimuove il nodo dal grafo per mantenere la stabilità
// della struttura
func (h *HNSWIndex) Delete(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// ricerca ID interno corrispondente all'ID esterno 
	internalID, ok := h.externalToInternalID[id]
	if !ok {
		// non esiste il nodo quindi non fa nulla 
		return 
	}

	// trova il nodo quindi imposta il flag
	node, ok := h.nodes[internalID]
	if ok {
		node.deleted = true 
	}

	// rimozione ID dalla mappa di lookup esterna per impedire 
	// che lo stesso ID venga riaggiunto in futuro
	// (si potrebbe anche ermettere)
	delete(h.externalToInternalID, id)
}

// fa la ricerca dei vicini più prossimi in un singolo livello del grafo 
func (h *HNSWIndex) searchLayer(query []float32, entrypointID uint32, k int, level int) ([]candidate, error) {
	log.Printf("DEBUG: Inizio searchLayer a livello %d con entrypoint %d\n", level, entrypointID)
	// si tracciano i nodi già visitati per non entrare in loop 
	visited := make(map[uint32]bool)

	// coda candidati da esplorare (i più vicini prima)
	candidates := newMinHeap()
	// lista risultati trovati (il più lontano in cima, per rimpiazzarlo velocemente con uno migliore)
	results := newMaxHeap()
	
	// inizia con il punto di ingresso 
	dist, err := h.distanceFunc(query, h.nodes[entrypointID].vector)
	if err != nil {
		return nil, err 
	}
	
	// inserisco i nodi nelle heap 
	heap.Push(candidates, candidate{id: entrypointID, distance: dist})
	heap.Push(results, candidate{id: entrypointID, distance: dist})
	visited[entrypointID] = true // setto il nodo come visitato nella mappa 
	
	loopCount := 0

	for candidates.Len() > 0 {
		loopCount++
		// estrae l'elemento più vicino dalla coda 
		current := heap.Pop(candidates).(candidate)

		// se questo candidato è più lontano del peggiore risultato (ele in cima di results) che 
		// abbiamo trovato finora, possiamo fermare l'esplorazione da questo ramo
		if results.Len() >= k && current.distance > (*results)[0]. distance {
			break
		}
	
		// esplora i vicini del candidato corrente 
		currentNode := h.nodes[current.id]
		// verifica che il nodo abbia connessioni a questo livello 
		if level >= len(currentNode.connections) {
			continue 
		}

		log.Printf("DEBUG: Livello %d, esploro i vicini di %d (%d vicini)\n", level, current.id, len(currentNode.connections[level]))

		for _, neighborID := range currentNode.connections[level] {
			if !visited[neighborID] {
				visited[neighborID] = true

				dist, err := h.distanceFunc(query, h.nodes[neighborID].vector)
				if err != nil {
					// ignora e continua in caso di errore di calcolo 
					continue
				}

				// se abbiamo ancora spazio nei risultati o se questo vicino 
				// è più vicino del nostro peggior risultato, lo aggiunge
				if results.Len() < k || dist < (*results)[0].distance {
					log.Printf("DEBUG: Livello %d, aggiungo candidato %d con distanza %f\n", level, neighborID, dist)

					heap.Push(candidates, candidate{id: neighborID, distance: dist})
					heap.Push(results, candidate{id: neighborID, distance: dist})

					// se non c'è più posto nella lista di risultati allora rimuoviamo il peggiore (che è il primo) 
					if results.Len() > k {
						heap.Pop(results)
					}
				}
			}

		}

	}
	log.Printf("DEBUG: Fine searchLayer a livello %d dopo %d iterazioni\n", level, loopCount)

	// L'heap non è ordinato, dobbiamo estrarre tutti gli elementi e ordinarli.
	finalResults := make([]candidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		finalResults[i] = heap.Pop(results).(candidate)
	}

	return finalResults, nil

}

// sceglie un livello casuale per un nuovo nodo
func (h *HNSWIndex) randomLevel() int {
	// Questo si basa su una distribuzione esponenziale decrescente
	level := 0
	for h.levelRand.Float64() < 0.5 && level < h.maxLevel+1 { // Aggiungiamo un limite per sicurezza
		level++
	}
	return level
}

// sceglie i migliori vicini da una lista di candidati
// per ora, implementa l'euristica semplice: prende i più vicini
func (h *HNSWIndex) selectNeighbors(candidates []candidate, m int) []candidate {
	if len(candidates) <= m {
		return candidates
	}
	// la lista di candidati da searchLayer è già ordinata per distanza
	return candidates[:m]
}

// funzione helper min che non c'è in Go
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
/*
// Funzione helper per calcolare la distanza euclidea al quadrato.
func squaredEuclideanDistance(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, fmt.Errorf("vettori di dimensioni diverse: %d vs %d", len(v1), len(v2))
	}
	var sum float64
	for i := range v1 {
		diff := float64(v1[i] - v2[i])
		sum += diff * diff
	}
	return sum, nil
}
*/
