package hnsw

import (
	"container/heap"
	"fmt"
	"github.com/sanonone/kektordb/internal/store/distance"
	"github.com/x448/float16"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"
)

// rappresenta l'indice del grafo gerarchico
type Index struct {
	// mutex per la concorrenza. Al momento lock globale poi forse gestirò più nel dettaglio
	mu sync.RWMutex

	// parametri dell'algoritmo HNSW
	m              int // num massimo di connessine per nodo per livello > 0 [default = 16]
	mMax0          int // numero massimo di connessioni per nodo al livello 0
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
	internalCounter      uint32

	// memorizza se siamo in precisione f32 o f16
	precision distance.PrecisionType

	// campo per la quantizzaziojne, sarà nil per indici non quantizzati
	quantizer *distance.Quantizer // puntatore perchè ci serve lo stato condiviso e modificabile da più parti del sistema

	// memorizza la funzione che ci serve per il dato indice
	// any per flessibilità nell'usare le diverse funzioni
	distanceFunc any

	// generatore num casuali per la selezione del livello
	levelRand *rand.Rand

	// per memorizzare il tipo  di metrica
	metric distance.DistanceMetric
}

// crea ed inizializza un nuovo indice HNSW
func New(m int, efConstruction int, metric distance.DistanceMetric, precision distance.PrecisionType) (*Index, error) {
	// imposto valori di default se non sono stati passati come parametri dall'utente
	if m <= 0 {
		m = 16 // default
	}
	if efConstruction <= 0 {
		efConstruction = 200 // default
	}

	h := &Index{
		m:                    m,
		mMax0:                m * 2, // è una regola comune raddoppiare m per il livello 0
		efConstruction:       efConstruction,
		ml:                   1.0 / math.Log(float64(m)),
		nodes:                make(map[uint32]*Node),
		externalToInternalID: make(map[string]uint32),
		internalCounter:      0,
		maxLevel:             -1, // all'inizio nessun livello
		entrypointID:         0,  // inizializzato al primo inserito
		levelRand:            rand.New(rand.NewSource(time.Now().UnixNano())),
		metric:               metric,
		precision:            precision,
	}

	// --- LOGICA DI SELEZIONE E VALIDAZIONE ---
	// Seleziona la funzione di distanza corretta in base alla precisione e alla metrica.
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
			return nil, fmt.Errorf("la precisione '%s' supporta solo la metrica '%s'", precision, distance.Euclidean)
		}
		h.distanceFunc, err = distance.GetFloat16Func(metric)
		if err != nil {
			return nil, err
		}
	case distance.Int8:
		// La quantizzazione int8 ha senso solo per Coseno (prodotto scalare)
		if metric != distance.Cosine {
			return nil, fmt.Errorf("la precisione '%s' supporta solo la metrica '%s'", precision, distance.Cosine)
		}
		h.distanceFunc, err = distance.GetInt8Func(metric)
		// Per un indice quantizzato, abbiamo bisogno di un quantizzatore.
		// Verrà "addestrato" e impostato in un secondo momento.
		h.quantizer = &distance.Quantizer{}
	default:
		return nil, fmt.Errorf("precisione non supportata: %s", precision)
	}

	if err != nil {
		return nil, err
	}

	return h, nil
}

// --- METODI DI CALCOLO AGGIORNATI ---

// distance calcola la distanza tra due vettori memorizzati dello stesso tipo.
func (h *Index) distance(v1, v2 any) (float64, error) {
	// Usiamo un type switch per chiamare la funzione corretta.
	switch fn := h.distanceFunc.(type) {
	case distance.DistanceFuncF32:
		vec1, ok1 := v1.([]float32)
		vec2, ok2 := v2.([]float32)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("tipi di vettore non corrispondenti (atteso float32)")
		}
		return fn(vec1, vec2)
	case distance.DistanceFuncF16:
		vec1, ok1 := v1.([]uint16)
		vec2, ok2 := v2.([]uint16)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("tipi di vettore non corrispondenti (atteso uint16)")
		}
		return fn(vec1, vec2)
	case distance.DistanceFuncI8:
		vec1, ok1 := v1.([]int8)
		vec2, ok2 := v2.([]int8)
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("tipi di vettore non corrispondenti (atteso int8)")
		}
		dot, err := fn(vec1, vec2)
		return -float64(dot), err
	default:
		return 0, fmt.Errorf("funzione di distanza non valida o non inizializzata")
	}
}

// distanceToQuery calcola la distanza tra un vettore di query (sempre float32) e un vettore memorizzato.
func (h *Index) distanceToQuery(query []float32, storedVector any) (float64, error) {
	queryToUse := query // Usa la query originale di default
	// Se la metrica dell'indice è Coseno, la query DEVE essere normalizzata
	// prima del confronto per usare la formula 1 - dotProduct.
	if h.metric == distance.Cosine {
		// Crea una COPIA della query prima di normalizzarla,
		// per non modificare la slice originale.
		queryCopy := make([]float32, len(query))
		copy(queryCopy, query)
		normalize(queryCopy)
		queryToUse = queryCopy // Usa la copia normalizzata per i calcoli
	}
	// Usiamo un type switch per determinare come gestire la query.
	switch fn := h.distanceFunc.(type) {
	case distance.DistanceFuncF32:
		stored, ok := storedVector.([]float32)
		if !ok {
			return 0, fmt.Errorf("tipo di vettore memorizzato non corrispondente (atteso float32)")
		}
		return fn(queryToUse, stored)
	case distance.DistanceFuncF16:
		// Converti la query float32 in float16 al volo
		queryF16 := make([]uint16, len(queryToUse))
		for i, v := range query {
			queryF16[i] = float16.Fromfloat32(v).Bits()
		}
		stored, ok := storedVector.([]uint16)
		if !ok {
			return 0, fmt.Errorf("tipo di vettore memorizzato non corrispondente (atteso uint16)")
		}
		return fn(queryF16, stored)
	case distance.DistanceFuncI8:
		// quantizzare la query al volo
		if h.quantizer == nil {
			return 0, fmt.Errorf("l'indice è quantizzato ma manca il quantizzatore")
		}
		queryI8 := h.quantizer.Quantize(query)
		stored, ok := storedVector.([]int8)
		if !ok {
			return 0, fmt.Errorf("tipo di vettore memorizzato non corrispondente (atteso int8)")
		}

		// La funzione di distanza int8 restituisce un prodotto scalare.
		// Va convertirlo in una "distanza" dove un valore più piccolo è migliore.
		// Usa il negativo del prodotto scalare.
		dot, err := fn(queryI8, stored)
		return -float64(dot), err // Minimizzare -dot è come massimizzare dot.
	default:
		return 0, fmt.Errorf("funzione di distanza non valida o non inizializzata")
	}
}

// implementa il metodo dell'interfaccia VectorIndex
func (h *Index) Search(query []float32, k int, allowList map[uint32]struct{}) []string {
	// la logica interna è la vecchia Search, ma deve restituire
	// solo []string, non ([]string, error).

	// h.mu.Lock()
	// defer h.mu.RUnlock()

	results, err := h.searchInternal(query, k, allowList) // richiama l'effettiva search
	if err != nil {
		log.Printf("Errore durante la ricerca HNSW: %v", err)
		return []string{} // risultato vuoto in caso di errore
	}
	return results
}

// la funzione pubblica per trovare i K vicini più prossimi a un vettore di query
func (h *Index) searchInternal(query []float32, k int, allowList map[uint32]struct{}) ([]string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// se il grafo è vuoto ritorno senza risultati
	if h.maxLevel == -1 {
		return []string{}, nil
	}

	// inizia la ricerca dal punto di ingresso globale al livello più alto
	currentEntryPoint := h.entrypointID

	// --- Selezione Intelligente dell'Entry Point ---
	// Se l'entry point di default è filtrato, non possiamo usarlo.
	// Scegliamo un punto di partenza casuale dall'allow list.
	if allowList != nil {
		if _, ok := allowList[currentEntryPoint]; !ok {
			// Prendi un elemento qualsiasi dalla mappa.
			foundNewEntryPoint := false
			for id := range allowList {
				currentEntryPoint = id
				foundNewEntryPoint = true
				break
			}
			if !foundNewEntryPoint { // L'allow list era vuota
				return []string{}, nil
			}
		}
	}

	// 1) ricerca top-down iterativa per muovermi verso il layer base
	for l := h.maxLevel; l > 0; l-- {
		nearest, err := h.searchLayer(query, currentEntryPoint, 1, l, allowList)
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
	nearestNeighbors, err := h.searchLayer(query, currentEntryPoint, k, 0, allowList)
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
func (h *Index) Add(id string, vector []float32) (uint32, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// controlo tramite mappa string-int se esiste già un
	// nodo con l'id di quello che voglio inserire
	if _, exists := h.externalToInternalID[id]; exists {
		return 0, fmt.Errorf("ID '%s' già esistente", id)
	}

	// Se l'indice usa la metrica Coseno, normalizza il vettore in ingresso.
	// Questo garantisce che tutti i vettori memorizzati siano a lunghezza unitaria.
	if h.metric == distance.Cosine {
		normalize(vector)
	}

	var storedVector interface{}
	// --- NUOVA LOGICA DI CONVERSIONE/QUANTIZZAZIONE ---
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
			// Questo è un errore di stato. L'indice quantizzato deve essere
			// "addestrato" prima di poter aggiungere vettori.
			return 0, fmt.Errorf("l'indice è in modalità int8 ma il quantizzatore non è addestrato")
		}
		storedVector = h.quantizer.Quantize(vector)
	}

	// assegnamento ID interno
	h.internalCounter++
	internalID := h.internalCounter

	// creazione nuovo nodo
	node := &Node{
		id:     id,
		vector: storedVector,
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
		return internalID, nil
	}

	// 1) --- fase di ricerca top-down per trovare i punti di ingresso a ogni livello ---

	// la ricerca inizia dal punto di ingresso globale al livello più alto [entrypointID]
	currentEntryPoint := h.entrypointID

	// scende dal livello più alto fino al livello superiore di quello assegnato al nuovo nodo (ovvero livello del nuovo nodo + 1)
	for l := h.maxLevel; l > level; l-- {
		// trova il vicino più prossimo nel livello attuale e lo usa come entry point per il livello inferiore
		nearest, err := h.searchLayer(vector, currentEntryPoint, 1, l, nil)
		if err != nil {
			return 0, fmt.Errorf("Errore durante linserimento del nodo")
		}
		// si assime che searchLayer restituisca sempre un risultato se l'entry point è valido
		currentEntryPoint = nearest[0].id
	}

	// 2) --- inserimento bottom up e connessione dei vicini ---
	// per ogni livello dal più basso fino al livello 0
	// trova i vicini e li connette al nuovo nodo
	for l := min(level, h.maxLevel); l >= 0; l-- {
		// trova gli efConstruction candidati più vicini per questo livello
		neighbors, err := h.searchLayer(vector, currentEntryPoint, h.efConstruction, l, nil)
		if err != nil {
			return 0, fmt.Errorf("Errore durante l'inserimento del nodo")
		}

		// seleziona gli M migliori vicini da connettere
		maxConns := h.m
		if l == 0 {
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
					// confronto tra due nodi interni, neighborNode e il suo vicino
					dist, _ := h.distance(neighborNode.vector, h.nodes[currentNeighborOfNeighborID].vector)
					if dist > maxDist {
						maxDist = dist
						//worstNeighborID = currentNeighborOfNeighborID
						worstNeighborIndex = i
					}
				}

				// Calcola la distanza tra il vicino e il nostro nuovo nodo.
				distToNewNode, _ := h.distance(neighborNode.vector, node.vector)

				// Se il nostro nuovo nodo è più vicino del vicino più lontano,
				// allora lo sostituiamo.
				if distToNewNode < maxDist && worstNeighborIndex != -1 {
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
	return internalID, nil

}

// marca un nodo come eliminato (soft delete)
// [per il momento] non rimuove il nodo dal grafo per mantenere la stabilità
// della struttura
func (h *Index) Delete(id string) {
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
func (h *Index) searchLayer(query []float32, entrypointID uint32, k int, level int, allowList map[uint32]struct{}) ([]candidate, error) {
	//log.Printf("DEBUG: Inizio searchLayer a livello %d con entrypoint %d\n", level, entrypointID)
	// si tracciano i nodi già visitati per non entrare in loop
	visited := make(map[uint32]bool)

	// coda candidati da esplorare (i più vicini prima)
	candidates := newMinHeap()
	// lista risultati trovati (il più lontano in cima, per rimpiazzarlo velocemente con uno migliore)
	results := newMaxHeap()

	// clacola la distanza tra la query ed il punto di ingresso
	// inizia con il punto di ingresso
	dist, err := h.distanceToQuery(query, h.nodes[entrypointID].vector)
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
		if results.Len() >= k && current.distance > (*results)[0].distance {
			break
		}

		// esplora i vicini del candidato corrente
		currentNode := h.nodes[current.id]
		// verifica che il nodo abbia connessioni a questo livello
		if level >= len(currentNode.connections) {
			continue
		}

		//log.Printf("DEBUG: Livello %d, esploro i vicini di %d (%d vicini)\n", level, current.id, len(currentNode.connections[level]))

		for _, neighborID := range currentNode.connections[level] {
			// CHECK 1: Se c'è una allowList, il vicino DEVE essere in essa.
			// `allowList != nil` è il check principale.
			if allowList != nil {
				// `_, ok := ...` è un lookup O(1) in una mappa/set.
				if _, ok := allowList[neighborID]; !ok {
					continue // Salta al prossimo vicino, questo è scartato.
				}
			}

			// questo vicino non è mai stato visitato? allora vado (previene loop infiniti)
			if !visited[neighborID] {
				visited[neighborID] = true

				// calcola la distanza tra la query ed il vicino
				dist, err := h.distanceToQuery(query, h.nodes[neighborID].vector)
				if err != nil {
					// ignora e continua in caso di errore di calcolo
					continue
				}

				// --- LOG DI DEBUG CHIAVE ---
				//log.Printf("[DEBUG Cosine] Query vs. Nodo %d (%s): Distanza Calcolata = %f",
				//	neighborID, h.nodes[neighborID].id, dist)
				// --- FINE LOG ---

				// se abbiamo ancora spazio nei risultati o se questo vicino
				// è più vicino del nostro peggior risultato, lo aggiunge
				if results.Len() < k || dist < (*results)[0].distance {
					//log.Printf("DEBUG: Livello %d, aggiungo candidato %d con distanza %f\n", level, neighborID, dist)

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
	//log.Printf("DEBUG: Fine searchLayer a livello %d dopo %d iterazioni\n", level, loopCount)

	// L'heap non è ordinato, dobbiamo estrarre tutti gli elementi e ordinarli.
	finalResults := make([]candidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		finalResults[i] = heap.Pop(results).(candidate)
	}

	return finalResults, nil

}

// sceglie un livello casuale per un nuovo nodo
func (h *Index) randomLevel() int {
	// Questo si basa su una distribuzione esponenziale decrescente
	level := 0
	for h.levelRand.Float64() < 0.5 && level < h.maxLevel+1 { // Aggiungiamo un limite per sicurezza
		level++
	}
	return level
}

// sceglie i migliori vicini da una lista di candidati
// per ora, implementa l'euristica semplice: prende i più vicini
func (h *Index) selectNeighbors(candidates []candidate, m int) []candidate {
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

// --- Helper ---
// Iterate itera su tutti i nodi non eliminati e passa l'ID e il vettore
// (sempre come []float32) a una funzione callback.
func (h *Index) Iterate(callback func(id string, vector []float32)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, node := range h.nodes {
		if !node.deleted {
			// --- LOGICA DI CONVERSIONE ---
			var vectorF32 []float32

			// Controlla il tipo del vettore memorizzato
			switch vec := node.vector.(type) {
			case []float32:
				vectorF32 = vec
			case []uint16:
				// Se è float16, dobbiamo de-comprimerlo in float32
				vectorF32 = make([]float32, len(vec))
				for i, v := range vec {
					vectorF32[i] = float16.Frombits(v).Float32()
				}
			case []int8: // <-- NUOVO CASE
				// Se è int8, dobbiamo de-quantizzarlo.
				if h.quantizer == nil {
					log.Printf("ATTENZIONE: indice int8 senza quantizzatore per il nodo %s", node.id)
					continue
				}
				vectorF32 = h.quantizer.Dequantize(vec)
			default:
				// Tipo sconosciuto, saltiamo questo nodo per sicurezza
				log.Printf("ATTENZIONE: tipo di vettore sconosciuto durante l'iterazione per il nodo %s", node.id)
				continue
			}

			callback(node.id, vectorF32)
		}
	}
}

func (h *Index) GetInternalID(externalID string) uint32 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.externalToInternalID[externalID]
}

// GetParameters restituisce i parametri di configurazione dell'indice.
func (h *Index) GetParameters() (distance.DistanceMetric, int, int) {
	return h.metric, h.m, h.efConstruction
}

// NodeData è una struct per trasportare i dati di un nodo fuori dal package.
type NodeData struct {
	ID       string
	Vector   []float32
	Metadata map[string]interface{}
}

func (h *Index) TrainQuantizer(vectors [][]float32) {
	if h.quantizer != nil {
		h.quantizer.Train(vectors)
	}
}

// normalize normalizza un vettore a lunghezza unitaria (norma L2).
// Modifica la slice in-place.
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
