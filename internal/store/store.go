package store

import (
	"fmt"
	"github.com/sanonone/kektordb/internal/store/distance" // Importa distance
	"github.com/sanonone/kektordb/internal/store/hnsw"     // Importa hnsw
	"github.com/tidwall/btree"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// --- Gestione compattazione AOF ---

// una struct per restituire coppie chiave-valore
type KVPair struct {
	Key   string
	Value []byte
}

// struct per restituire i dati completi di un vettore
type VectorData struct {
	ID       string
	Vector   []float32
	Metadata map[string]any
}

// VectorIndexInfo è una struct per trasportare i metadati di un indice.
type VectorIndexInfo struct {
	Name           string
	Metric         distance.DistanceMetric
	M              int
	EfConstruction int
}

// itera su tutte le coppie chiave-valore nello store e le passa a una funzione
// di callback. L'iterazione avviene in un read lock
func (s *Store) IterateKV(callback func(pair KVPair)) {
	s.kvStore.mu.RLock()
	defer s.kvStore.mu.RUnlock()

	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// iterateKVUnlocked esegue l'iterazione senza acquisire lock
func (s *Store) IterateKVUnlocked(callback func(pair KVPair)) {
	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// itera su tutti gli indici vettoriali e i loro contenuti
func (s *Store) IterateVectorIndexes(callback func(indexName string, index *hnsw.Index, data VectorData)) {
	s.mu.RUnlock()
	defer s.mu.RUnlock()

	for name, idx := range s.vectorIndexes {
		// deve accedere ai dati interni di HNSW
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.Iterate(func(id string, vector []float32) {
				// recupera i dati dallo store principale
				internalID := hnswIndex.GetInternalID(id)
				metadata := s.getMetadataForNode(name, internalID)

				callback(name, hnswIndex, VectorData{
					ID:       id,
					Vector:   vector,
					Metadata: metadata,
				})
			})
		}
	}
}

// iterateVectorIndexesUnlocked esegue l'iterazione senza acquisire lock.
func (s *Store) IterateVectorIndexesUnlocked(callback func(indexName string, index *hnsw.Index, data VectorData)) {
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.Iterate(func(id string, vector []float32) {
				// ... (logica per recuperare i metadati, che a sua volta non deve lockare!)
				// Per ora, la lasciamo così, ma in un refactoring più profondo
				// anche getMetadataForNode avrebbe una versione unlocked.
				internalID := hnswIndex.GetInternalID(id)
				metadata := s.getMetadataForNodeUnlocked(name, internalID)

				callback(name, hnswIndex, VectorData{
					ID:       id,
					Vector:   vector,
					Metadata: metadata,
				})
			})
		}
	}
}

// GetVectorIndexInfo restituisce una slice con le informazioni di configurazione
// di tutti gli indici vettoriali presenti.
func (s *Store) GetVectorIndexInfo() ([]VectorIndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infoList := make([]VectorIndexInfo, 0, len(s.vectorIndexes))

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst := hnswIndex.GetParameters()
			infoList = append(infoList, VectorIndexInfo{
				Name:           name,
				Metric:         metric,
				M:              m,
				EfConstruction: efConst,
			})
		}
		// Potremmo gestire altri tipi di indici qui in futuro.
	}

	return infoList, nil
}

func (s *Store) GetVectorIndexInfoUnlocked() ([]VectorIndexInfo, error) {
	infoList := make([]VectorIndexInfo, 0, len(s.vectorIndexes))
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst := hnswIndex.GetParameters()
			infoList = append(infoList, VectorIndexInfo{
				Name:           name,
				Metric:         metric,
				M:              m,
				EfConstruction: efConst,
			})
		}
	}
	return infoList, nil
}

// --- Fine compattazione AOF ---

// funzione helper per IterateVectorIndexes
// nota: logica inefficiente, in futuro si dovranno legare i metadata più strettamente ai nodi HNSW
func (s *Store) getMetadataForNode(indexName string, nodeID uint32) map[string]any {
	metadata := make(map[string]any)

	// fa la scansione dell'indice invertito
	if invIdx, ok := s.invertedIndex[indexName]; ok {
		for key, valueMap := range invIdx {
			for value, idSet := range valueMap {
				if _, exists := idSet[nodeID]; exists {
					metadata[key] = value
				}

			}

		}
	}

	// fare scansione b tree simile a sopra

	return metadata
}

// versione senza lock
func (s *Store) getMetadataForNodeUnlocked(indexName string, nodeID uint32) map[string]any {
	metadata := make(map[string]any)

	// fa la scansione dell'indice invertito
	if invIdx, ok := s.invertedIndex[indexName]; ok {
		for key, valueMap := range invIdx {
			for value, idSet := range valueMap {
				if _, exists := idSet[nodeID]; exists {
					metadata[key] = value
				}

			}

		}
	}

	// fare scansione b tree simile a sopra

	return metadata
}

// è una struttura dati che userà il b tree per associare a un valore (metadato) un ID di nodo
type BTreeItem struct {
	Value  float64
	NodeID uint32
}

// store è il contenitore principale che contiene tutti i tipi di dato di kektorDB
type Store struct {
	mu            sync.RWMutex
	kvStore       *KVStore
	vectorIndexes map[string]VectorIndex

	// indice invertito per i metadata
	// struttura = map[nome indice vettoriale] -> map[chiave metadato] -> map[valore meta] -> set[ID interni nodi]
	// es. invertedIndex["my_images"]["tags"]["gatto"]={1: {}, 5: {}, 34:{}}
	// cioè nell'indice my_images i nodi con ID 1,5 e 34 hanno il metadato "tags" con valore "gatto"
	invertedIndex map[string]map[string]map[string]map[uint32]struct{}

	// indice B-Tree per i metadati numerici
	// strutture: map[nome indice vettoriale]->map[chiave metadato]->BTree
	// B-Tree memorizzerà BTreeItem, permettendo ricerche di range veloci
	bTreeIndex map[string]map[string]*btree.BTreeG[BTreeItem]
}

func NewStore() *Store {
	return &Store{
		kvStore:       NewKVStore(),
		vectorIndexes: make(map[string]VectorIndex),

		// iniziizza la mappa principale dell'indice, le mappe interne
		// saranno create on demand quando necessario
		invertedIndex: make(map[string]map[string]map[string]map[uint32]struct{}),
		bTreeIndex:    make(map[string]map[string]*btree.BTreeG[BTreeItem]),
	}
}

// restituisce lo store KVStore
func (s *Store) GetKVStore() *KVStore {
	return s.kvStore
}

// crea un nuovo indice vettoriale
func (s *Store) CreateVectorIndex(name string, metric distance.DistanceMetric, m, efConstruction int, precision distance.PrecisionType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.vectorIndexes[name]; ok {
		return fmt.Errorf("indice '%s' già esistente", name)
	}

	// parametri che dovrebbero essere configurabili ma al
	// momento uso come valori di default
	const (
		defaultM       = 16
		defaultEfConst = 200
	)
	idx, err := hnsw.New(m, efConstruction, metric, precision)
	if err != nil {
		return err
	}

	s.vectorIndexes[name] = idx

	// Inizializziamo lo spazio per i metadati di questo nuovo indice.
	s.invertedIndex[name] = make(map[string]map[string]map[uint32]struct{})
	s.bTreeIndex[name] = make(map[string]*btree.BTreeG[BTreeItem])

	return nil
}

// recupera un indice vettoriale per nome
func (s *Store) GetVectorIndex(name string) (VectorIndex, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, found := s.vectorIndexes[name]
	return idx, found
}

// Compress converte un indice esistente in una nuova precisione.
func (s *Store) Compress(indexName string, newPrecision distance.PrecisionType) error {
	s.mu.Lock() // Usiamo un Lock() esclusivo perché modifichiamo la struttura dello store
	defer s.mu.Unlock()

	// 1. Controlla che l'indice esista
	oldIndex, ok := s.vectorIndexes[indexName]
	if !ok {
		return fmt.Errorf("indice '%s' non trovato", indexName)
	}

	// Controlla che sia un indice HNSW
	oldHNSWIndex, ok := oldIndex.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("la compressione è supportata solo per indici HNSW")
	}

	// 2. Raccogli tutti i dati "vivi" dall'indice esistente
	var allVectors []hnsw.NodeData // Creiamo una nuova struct per trasportare i dati
	oldHNSWIndex.Iterate(func(id string, vector []float32) {
		internalID := oldHNSWIndex.GetInternalID(id)
		metadata := s.getMetadataForNodeUnlocked(indexName, internalID)
		allVectors = append(allVectors, hnsw.NodeData{
			ID:       id,
			Vector:   vector,
			Metadata: metadata,
		})
	})

	if len(allVectors) == 0 {
		return fmt.Errorf("impossibile comprimere un indice vuoto")
	}

	// 3. Crea e "addestra" il nuovo indice
	metric, m, efConst := oldHNSWIndex.GetParameters()

	newIndex, err := hnsw.New(m, efConst, metric, newPrecision)
	if err != nil {
		return fmt.Errorf("impossibile creare il nuovo indice compresso: %w", err)
	}

	// Se la nuova precisione è int8, dobbiamo addestrare il quantizzatore
	if newPrecision == distance.Int8 {
		// Estrai solo i vettori per il training
		floatVectors := make([][]float32, len(allVectors))
		for i, data := range allVectors {
			floatVectors[i] = data.Vector
		}
		newIndex.TrainQuantizer(floatVectors) // Dobbiamo creare questo metodo
	}

	// 4. Popola il nuovo indice con i dati
	for _, data := range allVectors {
		internalID, err := newIndex.Add(data.ID, data.Vector)
		if err != nil {
			log.Printf("ATTENZIONE: Fallimento nell'aggiungere il vettore %s durante la compressione: %v", data.ID, err)
			continue
		}
		// Ri-associa i metadati
		if len(data.Metadata) > 0 {
			s.AddMetadataUnlocked(indexName, internalID, data.Metadata)
		}
	}

	// 5. Sostituisci atomicamente il vecchio indice con quello nuovo
	s.vectorIndexes[indexName] = newIndex

	log.Printf("Indice '%s' compresso con successo a precisione '%s'", indexName, newPrecision)
	return nil
}

// AddMetadata associa i metadati a un ID di nodo e aggiorna gli indici secondari.
func (s *Store) AddMetadata(indexName string, nodeID uint32, metadata map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, value := range metadata {
		switch v := value.(type) { // controlla il tipo della variabile any
		case string:
			// --- LOGICA INDICE INVERTITO (invariata) ---
			indexMetadata, ok := s.invertedIndex[indexName]
			if !ok {
				return fmt.Errorf("indice di metadati per '%s' non trovato", indexName)
			}
			if _, ok := indexMetadata[key]; !ok {
				indexMetadata[key] = make(map[string]map[uint32]struct{})
			}
			if _, ok := indexMetadata[key][v]; !ok {
				indexMetadata[key][v] = make(map[uint32]struct{})
			}
			indexMetadata[key][v][nodeID] = struct{}{}

		case float64:
			// --- NUOVA LOGICA B-TREE ---
			indexBTree, ok := s.bTreeIndex[indexName]
			if !ok {
				return fmt.Errorf("indice b-tree per '%s' non trovato", indexName)
			}

			// Controlla se un B-Tree per questa chiave esiste già, altrimenti crealo.
			if _, ok := indexBTree[key]; !ok {
				indexBTree[key] = btree.NewBTreeG[BTreeItem](btreeItemLess)
			}

			// Inserisci l'item nel B-Tree.
			indexBTree[key].Set(BTreeItem{Value: v, NodeID: nodeID})

		default:
			// Per ora ignoriamo altri tipi (bool, etc.)
			continue
		}
	}

	return nil
}

func (s *Store) AddMetadataUnlocked(indexName string, nodeID uint32, metadata map[string]any) error {
	for key, value := range metadata {
		switch v := value.(type) { // controlla il tipo della variabile any
		case string:
			// --- LOGICA INDICE INVERTITO (invariata) ---
			indexMetadata, ok := s.invertedIndex[indexName]
			if !ok {
				return fmt.Errorf("indice di metadati per '%s' non trovato", indexName)
			}
			if _, ok := indexMetadata[key]; !ok {
				indexMetadata[key] = make(map[string]map[uint32]struct{})
			}
			if _, ok := indexMetadata[key][v]; !ok {
				indexMetadata[key][v] = make(map[uint32]struct{})
			}
			indexMetadata[key][v][nodeID] = struct{}{}

		case float64:
			// --- NUOVA LOGICA B-TREE ---
			indexBTree, ok := s.bTreeIndex[indexName]
			if !ok {
				return fmt.Errorf("indice b-tree per '%s' non trovato", indexName)
			}

			// Controlla se un B-Tree per questa chiave esiste già, altrimenti crealo.
			if _, ok := indexBTree[key]; !ok {
				indexBTree[key] = btree.NewBTreeG[BTreeItem](btreeItemLess)
			}

			// Inserisci l'item nel B-Tree.
			indexBTree[key].Set(BTreeItem{Value: v, NodeID: nodeID})

		default:
			// Per ora ignoriamo altri tipi (bool, etc.)
			continue
		}
	}

	return nil
}

// FindIDsByFilter funge da query planner per i filtri.
// Supporta OR e AND. OR ha precedenza più bassa (prima si scompone sugli OR,
// ogni blocco OR è valutato come AND di sottofiltri).
func (s *Store) FindIDsByFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, fmt.Errorf("filtro vuoto")
	}

	// Split case-insensitive per "OR" senza alterare il resto della stringa
	reOr := regexp.MustCompile(`(?i)\s+OR\s+`)
	orBlocks := reOr.Split(filter, -1)

	finalIDSet := make(map[uint32]struct{})

	// regex per AND (case-insensitive)
	reAnd := regexp.MustCompile(`(?i)\s+AND\s+`)

	for _, orBlock := range orBlocks {
		orBlock = strings.TrimSpace(orBlock)
		if orBlock == "" {
			continue
		}

		// ogni orBlock può contenere più sottofiltri separati da AND
		andFilters := reAnd.Split(orBlock, -1)

		var blockIDSet map[uint32]struct{}
		isFirst := true

		for _, subFilter := range andFilters {
			subFilter = strings.TrimSpace(subFilter)
			if subFilter == "" {
				continue
			}

			currentIDSet, err := s.evaluateSingleFilter(indexName, subFilter)
			if err != nil {
				return nil, fmt.Errorf("errore nel filtro '%s': %w", subFilter, err)
			}

			if isFirst {
				// copia per sicurezza (in modo che non si aliasi)
				blockIDSet = make(map[uint32]struct{}, len(currentIDSet))
				for id := range currentIDSet {
					blockIDSet[id] = struct{}{}
				}
				isFirst = false
			} else {
				blockIDSet = intersectSets(blockIDSet, currentIDSet)
			}

			// ottimizzazione
			if len(blockIDSet) == 0 {
				break
			}
		}

		// unione (OR) col risultato finale
		finalIDSet = unionSets(finalIDSet, blockIDSet)
	}

	// se non ci sono risultati, ritorniamo empty map (coerente).
	if len(finalIDSet) == 0 {
		return make(map[uint32]struct{}), nil
	}

	return finalIDSet, nil
}

// evaluateSingleFilter valuta una singola espressione come "price>=10" o "name=Alice".
// Restituisce un set di ID (map[uint32]struct{}) e un errore.
func (s *Store) evaluateSingleFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	// Trova operatore (ordinale per lunghezza per gestire <= e >=)
	var op string
	opIndex := -1
	for _, operator := range []string{"<=", ">=", "=", "<", ">"} {
		if idx := strings.Index(filter, operator); idx != -1 {
			op = operator
			opIndex = idx
			break
		}
	}
	if opIndex == -1 {
		return nil, fmt.Errorf("formato filtro invalido, operatore non trovato (usare =, <, >, <=, >=)")
	}

	key := strings.TrimSpace(filter[:opIndex])
	valueStr := strings.TrimSpace(filter[opIndex+len(op):])

	// set risultato
	idSet := make(map[uint32]struct{})

	// Preleva il mapping dei B-Tree (se esiste) e l'indice invertito se necessario.
	indexBTree, hasBTree := s.bTreeIndex[indexName]
	indexInv, hasInv := s.invertedIndex[indexName]

	// Dispatch sugli operatori
	switch op {
	case "=":
		// Provo a trattare valueStr come numero
		numValue, err := strconv.ParseFloat(valueStr, 64)
		if err == nil && hasBTree {
			// è un numero -> uso BTree per la chiave
			tree, ok := indexBTree[key]
			if !ok {
				// chiave non indicizzata: risultato vuoto
				return make(map[uint32]struct{}), nil
			}

			// cerchiamo gli elementi esattamente uguali a numValue
			pivot := BTreeItem{Value: numValue}
			tree.Ascend(pivot, func(item BTreeItem) bool {
				if item.Value != numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
			return idSet, nil
		}

		// altrimenti trattiamo come stringa -> inverted index
		if !hasInv {
			return nil, fmt.Errorf("indice invertito '%s' non trovato", indexName)
		}
		keyMetadata, ok := indexInv[key]
		if !ok {
			return make(map[uint32]struct{}), nil
		}
		valSet, ok := keyMetadata[valueStr]
		if !ok {
			return make(map[uint32]struct{}), nil
		}
		// copia difensiva
		for id := range valSet {
			idSet[id] = struct{}{}
		}
		return idSet, nil

	case "<", "<=", ">", ">=":
		// Questi operatori sono numeric-only
		numValue, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			return nil, fmt.Errorf("il valore per l'operatore '%s' deve essere numerico: '%s'", op, valueStr)
		}
		if !hasBTree {
			return nil, fmt.Errorf("indice numerico '%s' non trovato", indexName)
		}

		tree, ok := indexBTree[key]
		if !ok {
			// chiave non indicizzata: risultato vuoto
			return make(map[uint32]struct{}), nil
		}

		switch op {
		case "<":
			// ascend dagli -inf e prendi fino a < numValue
			tree.Ascend(BTreeItem{Value: math.Inf(-1)}, func(item BTreeItem) bool {
				if item.Value >= numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case "<=":
			tree.Ascend(BTreeItem{Value: math.Inf(-1)}, func(item BTreeItem) bool {
				if item.Value > numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case ">":
			// descend dal +inf e prendi > numValue
			tree.Descend(BTreeItem{Value: math.Inf(+1)}, func(item BTreeItem) bool {
				if item.Value <= numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case ">=":
			tree.Descend(BTreeItem{Value: math.Inf(+1)}, func(item BTreeItem) bool {
				if item.Value < numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		}

		return idSet, nil

	default:
		return nil, fmt.Errorf("operatore '%s' non supportato", op)
	}
}

// intersectSets calcola l'intersezione dei due set (a ∩ b).
func intersectSets(a, b map[uint32]struct{}) map[uint32]struct{} {
	if a == nil || b == nil {
		return make(map[uint32]struct{})
	}
	// itera sul più piccolo per efficienza
	if len(a) > len(b) {
		a, b = b, a
	}
	res := make(map[uint32]struct{})
	for id := range a {
		if _, ok := b[id]; ok {
			res[id] = struct{}{}
		}
	}
	return res
}

// unionSets calcola l'unione dei due set (a ∪ b).
func unionSets(a, b map[uint32]struct{}) map[uint32]struct{} {
	res := make(map[uint32]struct{}, len(a)+len(b))
	for id := range a {
		res[id] = struct{}{}
	}
	for id := range b {
		res[id] = struct{}{}
	}
	return res
}

/*
// interroga l'indice invertito e restituisce un set di ID dei nodi che corrispondono al filtro.
// al momento supporta solo il singolo filtro '=', poi si aggiungeranno altri
func (s *Store) FindIDsByFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1) parsa il filtro '='
	parts := strings.SplitN(filter, "=", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("formato filtro invalido, usare 'chiave=valore'")
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	// 2) controlla l'esistenza dell'indice
	indexMetadata, ok := s.invertedIndex[indexName]
	if !ok {
		return nil, fmt.Errorf("indice '%s' non trovato", indexName)
	}

	// 3) cerca nell'indice invertito
	keyMetadata, ok := indexMetadata[key]
	if !ok {
		// se non trova la chiave allora nessun risultato
		return make(map[uint32]struct{}), nil
	}

	idSet, ok := keyMetadata[value]
	if !ok {
		// se non trova il risultato allora nessun risultato
		return make(map[uint32]struct{}), nil
	}

	// 4) restituisce una copia del set per sicurezza
	idSetCopy := make(map[uint32]struct{}, len(idSet))
	for id := range idSet {
		idSetCopy[id] = struct{}{}
	}

	return idSetCopy, nil

}
*/

// funzione Less per BTree items. Ordinerà gli item sul value float64
func btreeItemLess(a, b BTreeItem) bool {
	if a.Value < b.Value {
		return true
	}
	if a.Value > b.Value {
		return false
	}
	// se il valore è uguale, ordina tramite NodeID per mantenere gli items distinti
	return a.NodeID < b.NodeID
}

// RLock acquisisce un read lock sullo store.
func (s *Store) RLock() {
	s.mu.RLock()
}

// RUnlock rilascia il read lock.
func (s *Store) RUnlock() {
	s.mu.RUnlock()
}

// Lock acquisisce un write lock sullo store.
func (s *Store) Lock() {
	s.mu.Lock()
}

// Unlock rilascia il write lock.
func (s *Store) Unlock() {
	s.mu.Unlock()
}
