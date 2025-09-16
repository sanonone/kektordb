package store

import (
	//"math"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// --- interfaccia ---

// definisce le operazioni che un indice vettoriale deve supportare
type VectorIndex interface {
	Add(id string, vector []float32) (uint32, error)
	Search(query []float32, k int, allowlist map[uint32]struct{}) []string
	Delete(id string)
}

// --- implementazione Brute Force Index ---
// memorizza tutti i vettori e durante la ricerca calcola la distanza
// rispetto ad ognuno
type BruteForceIndex struct {
	mu          sync.RWMutex
	vectors     map[string][]float32
	internalIDs map[string]uint32
	counter     uint32
}

// crea un nuovo indice di brute force
func NewBruteForceIndex() *BruteForceIndex {
	return &BruteForceIndex{
		vectors:     make(map[string][]float32),
		internalIDs: make(map[string]uint32),
	}
}

// aggiunge un vettore all'indice
func (idx *BruteForceIndex) Add(id string, vector []float32) (uint32, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.vectors[id]; exists {
		return 0, fmt.Errorf("ID '%s' già esistente", id)
	}

	idx.vectors[id] = vector

	// Anche se l'indice brute-force non usa veramente gli ID interni
	// per la navigazione, li generiamo per coerenza con l'interfaccia.
	idx.counter++
	internalID := idx.counter
	idx.internalIDs[id] = internalID

	return internalID, nil
}

// per l'ordinamento dei risultati
type searchResult struct {
	id       string
	distance float64
}

// cerca i K vettori più vicini al vettore di query
func (idx *BruteForceIndex) Search(query []float32, k int, allowList map[uint32]struct{}) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	results := make([]searchResult, 0, len(idx.vectors))

	for id, vec := range idx.vectors {
		// per il momento uso la distanza Euclidea al quadrato, è più veloce
		// perché evita la radice quadrata e l'ordine non cambia
		dist, err := squaredEuclideanDistance(query, vec)
		if err == nil { // ignora vettori di dimensione diversa
			results = append(results, searchResult{id: id, distance: dist})

		}
	}

	// ordina i risultati per distanza crescente
	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	var finalIDs []string
	count := 0
	for _, res := range results {
		if count >= k {
			break
		}

		// Ottieni l'ID interno per il controllo
		internalID := idx.internalIDs[res.id]

		// --- LOGICA DI CONTROLLO CORRETTA ---
		// Controlliamo prima se il filtro è attivo (allowList != nil).
		// Se lo è, controlliamo se l'ID esiste nella mappa.

		passesFilter := true // Assumiamo che passi, a meno che non fallisca il check
		if allowList != nil {
			// L'idioma "comma, ok" ci dà un booleano che indica l'esistenza
			_, ok := allowList[internalID]
			if !ok {
				passesFilter = false // La chiave non è nell'allow list, quindi non passa
			}
		}

		if passesFilter {
			finalIDs = append(finalIDs, res.id)
			count++
		}
	}
	return finalIDs

	/* // parte pre filtri, da eliminare se funziona tutto
	// estrae i K id migliori
	var finalIDs []string
	for i := 0; i < k && i < len(results); i++ {
		finalIDs = append(finalIDs, results[i].id)
	}

	return finalIDs
	*/

}

func (idx *BruteForceIndex) Delete(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.vectors, id)
}

// funzione helper per calcolare la distanza euclidea al quadrato
func squaredEuclideanDistance(v1, v2 []float32) (float64, error) {
	// la distanza Euclidea è definita solo per vettori della stessa dimensione
	if len(v1) != len(v2) {
		// return 0, math.ErrUnsupported
		return 0, errors.New("squaredEuclideanDistance: vectors must have the same length")
	}
	var sum float64
	for i := range v1 {
		diff := float64(v1[i] - v2[i])
		sum += diff * diff
	}
	return sum, nil
}
