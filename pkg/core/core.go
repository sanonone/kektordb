package core

import (
	"encoding/gob"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance" // Importa distance
	"github.com/sanonone/kektordb/pkg/core/hnsw"     // Importa hnsw
	"github.com/sanonone/kektordb/pkg/textanalyzer"
	"github.com/tidwall/btree"
	"io"
	"log"
	"math"
	"regexp"
	"runtime"
	"sort"
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
	ID       string         `json:"id"`
	Vector   []float32      `json:"vector"`
	Metadata map[string]any `json:"metadata"`
}

// struct pubblica che verrà serializzata in JSON per l'API per restituire le info dei vettori
type IndexInfo struct {
	Name           string                  `json:"name"`
	Metric         distance.DistanceMetric `json:"metric"`
	Precision      distance.PrecisionType  `json:"precision"`
	M              int                     `json:"m"`
	EfConstruction int                     `json:"ef_construction"`
	VectorCount    int                     `json:"vector_count"`
	TextLanguage   string                  `json:"text_language"`
}

/*
// VectorIndexInfo è una struct per trasportare i metadati di un indice.
type VectorIndexInfo struct {
	Name           string
	Metric         distance.DistanceMetric
	M              int
	EfConstruction int
	Precision      distance.PrecisionType
	TextLanguage   string
}
*/

// --- SNAPSHOTTING ---

// rappresenta lo stato salvabile del database, questa conterrà tutto
type Snapshot struct {
	KVData     map[string][]byte
	VectorData map[string]*IndexSnapshot
}

// rappresenta lo stato salvabile di un singolo vettore, cintiene la configurazione IndexConfig e i dati Nodes di un singolo indice HNSW
type IndexSnapshot struct {
	Config             IndexConfig
	Nodes              map[uint32]*NodeSnapshot // Salveremo i nodi del grafo ed i metadata. È map perché più semplice da serializzare e deserializzare
	ExternalToInternal map[string]uint32
	InternalCounter    uint32
	EntrypointID       uint32
	MaxLevel           int
	QuantizerState     *distance.Quantizer // Salva lo stato del quantizzatore
	// Aggiungeremo altri campi se necessario (es. stato del quantizzatore)
}

// NodeSnapshot contiene tutti i dati necessari per ripristinare un singolo nodo.
type NodeSnapshot struct {
	NodeData *hnsw.Node             // I dati del grafo (ID, connessioni, etc.)
	Metadata map[string]interface{} // I metadati associati

	// dati del vettore tipizzati esplicitamente (altrimenti fa conversione a float64 in decode gob)
	VectorF32 []float32 `json:"vector_f32,omitempty"`
	VectorF16 []uint16  `json:"vector_f16,omitempty"`
	VectorI8  []int8    `json:"vector_i8,omitempty"`
}

// contine le configurazioni di vector index,
type IndexConfig struct {
	Metric         distance.DistanceMetric
	Precision      distance.PrecisionType
	M              int
	EfConstruction int
	TextLanguage   string // "english" "italian" o "" per disabilitare
}

// Registriamo i nostri tipi custom con gob in modo che sappia come gestirli
// fondamentale quando si usano interfacce. Gob ha bisogno di sapere in anticipo quali tipi concreti potrebbero essere memorizzati in un ampo any o interface{}
/*
func init() {
	gob.Register([]float32{})
	gob.Register([]uint16{})
	gob.Register([]int8{})
}
*/

// Snapshot serializza lo stato corrente dello store in formato gob su un io.Writer.
// Questa funzione si aspetta che il chiamante gestisca il locking.
func (s *DB) Snapshot(writer io.Writer) error {
	// snapshot assume che il chiamante abbia già il lock di scrittura
	/*
		s.mu.RLock() // Usiamo RLock perché stiamo solo leggendo lo stato.
		defer s.mu.RUnlock()
	*/

	/*
		s.mu.Lock()
		defer s.mu.Unlock()

		snapshot := Snapshot{
			KVData:     s.kvStore.data,
			VectorData: make(map[string]*IndexSnapshot),
		}*/

	// --- FASE 1: Acquisisci tutti i lock di LETTURA necessari ---

	// 1a. Blocca lo Store per ottenere una lista stabile di indici.
	s.mu.RLock()

	// 1b. Blocca il KV store.
	s.kvStore.RLock()

	// 1c. Blocca ogni singolo indice HNSW.
	// Creiamo una lista degli indici per poterli sbloccare dopo.
	indexesToUnlock := make([]*hnsw.Index, 0, len(s.vectorIndexes))
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.RLock()
			log.Printf("In snapshot è stato preso il lock su index %s", name)
			indexesToUnlock = append(indexesToUnlock, hnswIndex)
		}
	}

	// --- FASE 2: Rilascia il lock di più alto livello ---
	// Ora che abbiamo bloccato tutte le strutture dati "figlie", possiamo rilasciare
	// il lock sulla lista "genitore", permettendo ad alcune operazioni
	// (come GetVectorIndex) di procedere.
	s.mu.RUnlock()

	// --- FASE 3: Assicura lo sblocco finale di tutto il resto ---
	defer func() {
		s.kvStore.RUnlock()
		for _, idx := range indexesToUnlock {
			idx.RUnlock()
		}
	}()

	// --- FASE 4: Esegui lo Snapshot (ora è 100% sicuro) ---
	// A questo punto, possediamo un RLock su KVStore e su ogni HNSWIndex.
	// Nessuna scrittura (`SET`, `VADD`) può avvenire. Possiamo leggere tutto.
	log.Println("Fase 4")

	snapshot := Snapshot{
		KVData:     s.kvStore.data, // Sicuro, perché abbiamo il RLock
		VectorData: make(map[string]*IndexSnapshot),
	}

	for name, idx := range s.vectorIndexes {
		log.Println("Fase 4")
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			nodes, extToInt, counter, entrypoint, maxLevel, quantizer := hnswIndex.SnapshotData()

			// Chiama le versioni "Unlocked" dei getter (che dobbiamo creare).
			metric, m, efc, precision, _, textLang := hnswIndex.GetInfoUnlocked()

			nodeSnapshots := make(map[uint32]*NodeSnapshot, len(nodes))
			for internalID, node := range nodes {
				// Crea lo snapshot del nodo
				snap := &NodeSnapshot{
					NodeData: node,
					Metadata: s.getMetadataForNodeUnlocked(name, internalID),
				}

				// Esegui un type switch per popolare il campo corretto del vettore
				switch vec := node.Vector.(type) {
				case []float32:
					snap.VectorF32 = vec
				case []uint16:
					snap.VectorF16 = vec
				case []int8:
					snap.VectorI8 = vec
				default:
					// Logga un avviso se troviamo un tipo inaspettato
					log.Printf("ATTENZIONE: tipo di vettore sconosciuto '%T' durante lo snapshot del nodo %d", vec, internalID)
				}
				nodeSnapshots[internalID] = snap
			}

			snapshot.VectorData[name] = &IndexSnapshot{
				Config: IndexConfig{
					Metric:         metric,
					Precision:      precision,
					M:              m,
					EfConstruction: efc,
					TextLanguage:   textLang,
				},
				Nodes:              nodeSnapshots, // Usa la nuova mappa
				ExternalToInternal: extToInt,
				InternalCounter:    counter,
				EntrypointID:       entrypoint,
				MaxLevel:           maxLevel,
				QuantizerState:     quantizer,
			}
			log.Println("Fase 4")
		}
	}

	encoder := gob.NewEncoder(writer)
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("impossibile codificare lo snapshot: %w", err)
	}

	return nil
}

// LoadFromSnapshot deserializza uno snapshot gob da un io.Reader e ripristina
// lo stato dello store. Svuota lo store prima di caricare.
func (s *DB) LoadFromSnapshot(reader io.Reader) error {
	decoder := gob.NewDecoder(reader)
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return fmt.Errorf("impossibile decodificare lo snapshot: %w", err)
	}

	// Svuota lo stato corrente per un caricamento pulito
	s.kvStore.data = snapshot.KVData
	if s.kvStore.data == nil {
		s.kvStore.data = make(map[string][]byte)
	}
	s.vectorIndexes = make(map[string]VectorIndex)
	s.invertedIndex = make(map[string]map[string]map[string]map[uint32]struct{})
	s.bTreeIndex = make(map[string]map[string]*btree.BTreeG[BTreeItem])

	// Itera sugli indici presenti nello snapshot
	for name, indexSnap := range snapshot.VectorData {
		// Crea un nuovo indice vuoto con la configurazione salvata
		idx, err := hnsw.New(indexSnap.Config.M, indexSnap.Config.EfConstruction, indexSnap.Config.Metric, indexSnap.Config.Precision, indexSnap.Config.TextLanguage)
		if err != nil {
			return fmt.Errorf("impossibile ricreare l'indice '%s' dallo snapshot: %w", name, err)
		}

		// Prepara la mappa di nodi da caricare in HNSW
		nodesToLoad := make(map[uint32]*hnsw.Node)

		// --- NUOVA LOGICA: Ricostruzione del campo 'Vector' ---
		for id, nodeSnap := range indexSnap.Nodes {
			// Ricostruisci il campo generico 'Vector' (interface{})
			// basandoti su quale dei campi tipizzati è popolato.
			if nodeSnap.VectorF32 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorF32
			} else if nodeSnap.VectorF16 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorF16
			} else if nodeSnap.VectorI8 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorI8
			} else {
				log.Printf("ATTENZIONE: Nessun dato vettoriale trovato per il nodo %d nello snapshot", id)
			}
			nodesToLoad[id] = nodeSnap.NodeData
		}
		// --- FINE NUOVA LOGICA ---

		// Carica i dati del grafo HNSW
		if err := idx.LoadSnapshotData(nodesToLoad, indexSnap.ExternalToInternal, indexSnap.InternalCounter, indexSnap.EntrypointID, indexSnap.MaxLevel, indexSnap.QuantizerState); err != nil {
			return fmt.Errorf("impossibile caricare i dati HNSW per l'indice '%s': %w", name, err)
		}

		s.vectorIndexes[name] = idx

		// Inizializza gli spazi per gli indici secondari
		s.invertedIndex[name] = make(map[string]map[string]map[uint32]struct{})
		s.bTreeIndex[name] = make(map[string]*btree.BTreeG[BTreeItem])

		// Ricostruisci gli indici secondari (metadati)
		for _, nodeSnap := range indexSnap.Nodes {
			if len(nodeSnap.Metadata) > 0 {
				s.AddMetadataUnlocked(name, nodeSnap.NodeData.InternalID, nodeSnap.Metadata)
			}
		}
	}

	return nil
}

// itera su tutte le coppie chiave-valore nello store e le passa a una funzione
// di callback. L'iterazione avviene in un read lock
func (s *DB) IterateKV(callback func(pair KVPair)) {
	s.kvStore.mu.RLock()
	defer s.kvStore.mu.RUnlock()

	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// iterateKVUnlocked esegue l'iterazione senza acquisire lock
func (s *DB) IterateKVUnlocked(callback func(pair KVPair)) {
	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// itera su tutti gli indici vettoriali e i loro contenuti
func (s *DB) IterateVectorIndexes(callback func(indexName string, index *hnsw.Index, data VectorData)) {
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
func (s *DB) IterateVectorIndexesUnlocked(callback func(indexName string, index *hnsw.Index, data VectorData)) {
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
func (s *DB) GetVectorIndexInfo() ([]IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				Precision:      precision,
				M:              m,
				EfConstruction: efConst,
				VectorCount:    count,
				TextLanguage:   textLang,
			})
		}
		// Potremmo gestire altri tipi di indici qui in futuro.
	}

	return infoList, nil
}

func (s *DB) GetVectorIndexInfoUnlocked() ([]IndexInfo, error) {
	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				M:              m,
				EfConstruction: efConst,
				TextLanguage:   textLang,
				Precision:      precision,
				VectorCount:    count,
			})
		}
	}
	return infoList, nil
}

// GetVectorIndexInfo restituisce le informazioni di configurazione e stato
// per tutti gli indici vettoriali.
func (s *DB) GetVectorIndexInfoAPI() ([]IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo() // Creeremo questo metodo
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				Precision:      precision,
				M:              m,
				EfConstruction: efConst,
				VectorCount:    count,
				TextLanguage:   textLang,
			})
		}
	}

	// Ordina la lista per nome per una risposta API consistente
	sort.Slice(infoList, func(i, j int) bool {
		return infoList[i].Name < infoList[j].Name
	})

	return infoList, nil
}

// GetSingleVectorIndexInfo restituisce le informazioni per un singolo indice.
func (s *DB) GetSingleVectorIndexInfoAPI(name string) (IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.vectorIndexes[name]
	if !ok {
		return IndexInfo{}, fmt.Errorf("indice '%s' non trovato", name)
	}

	if hnswIndex, ok := idx.(*hnsw.Index); ok {
		metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
		return IndexInfo{
			Name:           name,
			Metric:         metric,
			Precision:      precision,
			M:              m,
			EfConstruction: efConst,
			VectorCount:    count,
			TextLanguage:   textLang,
		}, nil
	}

	return IndexInfo{}, fmt.Errorf("tipo di indice non supportato per l'introspezione")
}

// GetVector recupera i dati completi per un singolo vettore dato il suo ID esterno.
func (s *DB) GetVector(indexName, vectorID string) (VectorData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Controlla che l'indice esista
	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return VectorData{}, fmt.Errorf("indice '%s' non trovato", indexName)
	}

	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return VectorData{}, fmt.Errorf("tipo di indice non supportato per il recupero dati")
	}

	// 2. Ottieni il nodo dall'indice HNSW
	// Dobbiamo creare un nuovo metodo in HNSW per questo.
	nodeData, found := hnswIndex.GetNodeData(vectorID)
	if !found {
		return VectorData{}, fmt.Errorf("vettore con ID '%s' non trovato nell'indice '%s'", vectorID, indexName)
	}

	// 3. Recupera i metadati associati
	// La funzione getMetadataForNodeUnlocked è perfetta per questo,
	// dato che abbiamo già un RLock.
	metadata := s.getMetadataForNodeUnlocked(indexName, nodeData.InternalID)

	return VectorData{
		ID:       vectorID,
		Vector:   nodeData.Vector,
		Metadata: metadata,
	}, nil
}

// GetVectors recupera i dati completi per una slice di ID di vettori in parallelo.
// Restituisce una slice di VectorData. Se un ID non viene trovato,
// semplicemente non sarà presente nella slice di ritorno.
func (s *DB) GetVectors(indexName string, vectorIDs []string) ([]VectorData, error) {
	// Acquisiamo il lock di lettura una sola volta per l'intera operazione.
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return nil, fmt.Errorf("indice '%s' non trovato", indexName)
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("tipo di indice non supportato")
	}

	// --- LOGICA DI PARALLELISMO ---

	// Canale per distribuire il "lavoro" (gli ID da cercare)
	jobs := make(chan string, len(vectorIDs))
	// Canale per raccogliere i risultati
	resultsChan := make(chan VectorData, len(vectorIDs))
	// WaitGroup per sapere quando tutti i worker hanno finito
	var wg sync.WaitGroup

	// Determina il numero di worker. Usiamo il numero di CPU disponibili come limite ragionevole.
	numWorkers := runtime.NumCPU()
	if len(vectorIDs) < numWorkers {
		numWorkers = len(vectorIDs)
	}
	if numWorkers == 0 {
		return []VectorData{}, nil
	}

	// 1. Avvia i Worker
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Ogni worker prende un ID dal canale `jobs`, lavora e invia il risultato.
			for vectorID := range jobs {
				nodeData, found := hnswIndex.GetNodeData(vectorID)
				if !found {
					continue
				}
				metadata := s.getMetadataForNodeUnlocked(indexName, nodeData.InternalID)

				resultsChan <- VectorData{
					ID:       vectorID,
					Vector:   nodeData.Vector,
					Metadata: metadata,
				}
			}
		}()
	}

	// 2. Invia i Lavori
	for _, id := range vectorIDs {
		jobs <- id
	}
	close(jobs) // Chiudi il canale per segnalare ai worker che non ci sono più lavori

	// 3. Attendi che tutti i worker finiscano
	wg.Wait()
	close(resultsChan) // Chiudi il canale dei risultati

	// 4. Raccogli tutti i risultati
	finalResults := make([]VectorData, 0, len(resultsChan))
	for result := range resultsChan {
		finalResults = append(finalResults, result)
	}

	return finalResults, nil
}

// --- Fine compattazione AOF ---

// funzione helper per IterateVectorIndexes
// nota: logica inefficiente, in futuro si dovranno legare i metadata più strettamente ai nodi HNSW
func (s *DB) getMetadataForNode(indexName string, nodeID uint32) map[string]any {
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
func (s *DB) getMetadataForNodeUnlocked(indexName string, nodeID uint32) map[string]any {
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

// una lista di ID di documenti (gli id interni uint32)
type PostingList []uint32

// struttura dati per la ricerca testuale
// mappa un token (parola) a una lista di documenti che la contengono
// struttura: map[nome_indice_vettoriale] -> map[chiave_metadato] -> map[token] -> PostingList
type InvertedIndex map[string]map[string]map[string]PostingList

// store è il contenitore principale che contiene tutti i tipi di dato di kektorDB
type DB struct {
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
	textIndex  InvertedIndex
}

func NewDB() *DB {
	return &DB{
		kvStore:       NewKVStore(),
		vectorIndexes: make(map[string]VectorIndex),

		// iniziizza la mappa principale dell'indice, le mappe interne
		// saranno create on demand quando necessario
		invertedIndex: make(map[string]map[string]map[string]map[uint32]struct{}),
		bTreeIndex:    make(map[string]map[string]*btree.BTreeG[BTreeItem]),
		textIndex:     make(InvertedIndex),
	}
}

// restituisce lo store KVStore
func (s *DB) GetKVStore() *KVStore {
	return s.kvStore
}

// crea un nuovo indice vettoriale
func (s *DB) CreateVectorIndex(name string, metric distance.DistanceMetric, m, efConstruction int, precision distance.PrecisionType, textLang string) error {
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
	idx, err := hnsw.New(m, efConstruction, metric, precision, textLang)
	if err != nil {
		return err
	}

	s.vectorIndexes[name] = idx

	// Inizializziamo lo spazio per i metadati di questo nuovo indice.
	s.invertedIndex[name] = make(map[string]map[string]map[uint32]struct{})
	s.bTreeIndex[name] = make(map[string]*btree.BTreeG[BTreeItem])
	s.textIndex[name] = make(map[string]map[string]PostingList)

	return nil
}

// recupera un indice vettoriale per nome
func (s *DB) GetVectorIndex(name string) (VectorIndex, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, found := s.vectorIndexes[name]
	return idx, found
}

// rimuove un intero indice vettoriale e tutti i suoi dati associati
func (s *DB) DeleteVectorIndex(name string) error {
	s.mu.Lock() // Usiamo un Lock() esclusivo perché stiamo modificando tutte le strutture
	defer s.mu.Unlock()

	// Controlla che l'indice esista
	_, ok := s.vectorIndexes[name]
	if !ok {
		return fmt.Errorf("indice '%s' non trovato", name)
	}

	// Rimuovi l'indice HNSW dalla mappa principale
	delete(s.vectorIndexes, name)

	// Rimuovi i dati associati dall'indice invertito
	delete(s.invertedIndex, name)

	// Rimuovi i dati associati dall'indice B-Tree
	delete(s.bTreeIndex, name)

	// rimuove i dati associati all'indice testuale
	delete(s.textIndex, name)

	log.Printf("Indice '%s' e tutti i dati associati sono stati eliminati.", name)
	return nil
}

// Compress converte un indice esistente in una nuova precisione.
func (s *DB) Compress(indexName string, newPrecision distance.PrecisionType) error {
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

	/*
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
	*/

	// --- CORREZIONE CHIAVE ---
	// Raccogliamo i dati GREZZI. Ci aspettiamo che siano []float32
	// perché comprimiamo solo da float32.
	type rawData struct {
		ID       string
		Vector   []float32
		Metadata map[string]interface{}
	}
	var allVectors []rawData

	oldHNSWIndex.IterateRaw(func(id string, vector interface{}) {
		// Facciamo un type assertion per essere sicuri
		if vecF32, ok := vector.([]float32); ok {
			internalID := oldHNSWIndex.GetInternalID(id) // Necessario un metodo unlocked
			metadata := s.getMetadataForNodeUnlocked(indexName, internalID)
			allVectors = append(allVectors, rawData{
				ID:       id,
				Vector:   vecF32,
				Metadata: metadata,
			})
		}
	})
	// --- FINE CORREZIONE ---

	if len(allVectors) == 0 {
		return fmt.Errorf("impossibile comprimere un indice vuoto")
	}

	// 3. Crea e "addestra" il nuovo indice
	metric, m, efConst := oldHNSWIndex.GetParameters()

	textLang := oldHNSWIndex.TextLanguage()

	newIndex, err := hnsw.New(m, efConst, metric, newPrecision, textLang)
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

		//log.Printf("[DEBUG COMPRESS] Training quantizer su %d vettori.", len(floatVectors))
		//log.Printf("[DEBUG COMPRESS] Esempio vettore di training (primi 5 elementi): %v", floatVectors[0][:5])

		newIndex.TrainQuantizer(floatVectors) // Dobbiamo creare questo metodo

		// --- NUOVO: ANALISI DELLA DISTRIBUZIONE ---
		log.Println("Analisi della distribuzione dei dati quantizzati...")

		// Crea un istogramma per contare le occorrenze di ogni valore int8
		histogram := make(map[int8]int)
		totalValues := 0

		for _, vec := range floatVectors {
			quantizedVec := newIndex.Quantizer().Quantize(vec) // Dobbiamo esporre il quantizzatore
			for _, val := range quantizedVec {
				histogram[val]++
				totalValues++
			}
		}

		// Stampa l'istogramma
		log.Println("--- Istogramma Valori Int8 ---")
		// Ordina le chiavi per una stampa pulita
		keys := make([]int, 0, len(histogram))
		for k := range histogram {
			keys = append(keys, int(k))
		}
		sort.Ints(keys)

		for _, k := range keys {
			count := histogram[int8(k)]
			percentage := float64(count) / float64(totalValues) * 100
			log.Printf("Valore: %4d | Conteggio: %8d | Percentuale: %.2f%%", k, count, percentage)
		}
		log.Println("-----------------------------")
		// --- FINE ANALISI ---
	}

	// --- CORREZIONE CHIAVE: Pulisci i vecchi indici secondari ---
	log.Printf("[DEBUG COMPRESS] Pulizia indici secondari per '%s'", indexName)
	s.invertedIndex[indexName] = make(map[string]map[string]map[uint32]struct{})
	s.bTreeIndex[indexName] = make(map[string]*btree.BTreeG[BTreeItem])
	//    s.metadataStore[indexName] = make(map[uint32]map[string]interface{})
	// --- FINE CORREZIONE ---

	// Popola il nuovo indice e ri-popola gli indici secondari da zero
	log.Printf("[DEBUG COMPRESS] Ripopolamento del nuovo indice e degli indici secondari...")
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
func (s *DB) AddMetadata(indexName string, nodeID uint32, metadata map[string]any) error {
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

func (s *DB) AddMetadataUnlocked(indexName string, nodeID uint32, metadata map[string]any) error {
	// Ottieni la configurazione dell'indice per sapere quale analizzatore usare
	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return nil
	} // L'indice non esiste, non fare nulla

	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil
	}

	var analyzer textanalyzer.Analyzer
	switch hnswIndex.TextLanguage() { // Dobbiamo creare questo metodo getter
	case "english":
		analyzer = textanalyzer.NewEnglishStemmer()
	case "italian":
		analyzer = textanalyzer.NewItalianStemmer()
	default:
		// Nessun analizzatore se la lingua non è impostata o non è supportata
	}

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

			// --- Indicizzazione Full-Text (con analyzer) ---
			if analyzer != nil {
				tokens := analyzer.Analyze(v)                                         // Usa il tuo analizzatore!
				log.Printf("[DEBUG TEXT INDEX] Testo: '%s' -> Tokens: %v", v, tokens) // <-- LOG 1

				if _, ok := s.textIndex[indexName][key]; !ok {
					s.textIndex[indexName][key] = make(map[string]PostingList)
				}

				for _, token := range tokens {
					log.Printf("[DEBUG TEXT INDEX] Indicizzazione token '%s' per nodo %d", token, nodeID) // <-- LOG 2
					list := s.textIndex[indexName][key][token]
					if len(list) == 0 || list[len(list)-1] != nodeID {
						s.textIndex[indexName][key][token] = append(list, nodeID)
					}
				}
			} else {
				log.Printf("[DEBUG TEXT INDEX] Analyzer è nil per l'indice '%s'", indexName) // <-- LOG 3
			}

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
	// --- LOG DI DEBUG ---
	log.Printf("[DEBUG TEXT INDEX] Stato dell'indice testuale per '%s' dopo l'aggiunta del nodo %d:", indexName, nodeID)
	log.Printf("%+v", s.textIndex[indexName])
	// --- FINE LOG DI DEBUG ---

	return nil
}

// FindIDsByFilter funge da query planner per i filtri.
// Supporta OR e AND. OR ha precedenza più bassa (prima si scompone sugli OR,
// ogni blocco OR è valutato come AND di sottofiltri).
func (s *DB) FindIDsByFilter(indexName string, filter string) (map[uint32]struct{}, error) {
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

// regex per parsare la funzione CONTAINS
var containsRegex = regexp.MustCompile(`(?i)CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

// evaluateSingleFilter valuta una singola espressione come "price>=10" o "name=Alice".
// Restituisce un set di ID (map[uint32]struct{}) e un errore.
func (s *DB) evaluateSingleFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	filter = strings.TrimSpace(filter)

	// --- Check per CONTAINS ---
	matches := containsRegex.FindStringSubmatch(filter)
	if len(matches) == 3 {
		// Abbiamo trovato una corrispondenza per CONTAINS(campo, "valore")
		// matches[0] = stringa intera
		// matches[1] = nome del campo (es. "descrizione")
		// matches[2] = testo della query (es. "scarpe rosse")
		fieldName := matches[1]
		queryText := matches[2]
		return s.findIDsByTextSearch(indexName, fieldName, queryText)
	}

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
func (s *DB) RLock() {
	s.mu.RLock()
}

// RUnlock rilascia il read lock.
func (s *DB) RUnlock() {
	s.mu.RUnlock()
}

// Lock acquisisce un write lock sullo store.
func (s *DB) Lock() {
	s.mu.Lock()
}

// Unlock rilascia il write lock.
func (s *DB) Unlock() {
	s.mu.Unlock()
}

// helper esegue una ricerca sull'indice testuale
func (db *DB) findIDsByTextSearch(indexName, fieldName, queryText string) (map[uint32]struct{}, error) {
	// Ottieni l'analizzatore corretto per questo indice
	idx, ok := db.vectorIndexes[indexName]
	if !ok {
		return nil, fmt.Errorf("indice '%s' non trovato", indexName)
	}

	hnswIndex, _ := idx.(*hnsw.Index)
	var analyzer textanalyzer.Analyzer
	switch hnswIndex.TextLanguage() {
	case "english":
		analyzer = textanalyzer.NewEnglishStemmer()
	case "italian":
		analyzer = textanalyzer.NewItalianStemmer()
	default:
		return nil, fmt.Errorf("l'indice '%s' non ha un analizzatore di testo configurato", indexName)
	}

	// 1. Analizza la query per ottenere i token da cercare
	queryTokens := analyzer.Analyze(queryText)
	log.Printf("[DEBUG TEXT SEARCH] Query: '%s' -> Tokens: %v", queryText, queryTokens)
	if len(queryTokens) == 0 {
		return make(map[uint32]struct{}), nil // Query vuota, nessun risultato
	}

	// 2. Recupera le "posting list" per ogni token
	var postingLists [][]uint32
	textIndexForField, ok := db.textIndex[indexName][fieldName]
	if !ok {
		log.Printf("[DEBUG TEXT SEARCH] Campo '%s' non trovato nell'indice testuale.", fieldName)
		return make(map[uint32]struct{}), nil // Il campo non è mai stato indicizzato
	}

	for _, token := range queryTokens {
		list, found := textIndexForField[token]
		if !found {
			log.Printf("[DEBUG TEXT SEARCH] Token '%s' non trovato nell'indice.", token)
			// Se anche solo un token non viene trovato, la ricerca AND fallisce.
			return make(map[uint32]struct{}), nil
		}
		log.Printf("[DEBUG TEXT SEARCH] Trovata posting list per il token '%s': %v", token, list)
		postingLists = append(postingLists, list)
	}

	// 3. Calcola l'intersezione delle posting list
	// (Questa è l'operazione chiave per la ricerca "boolean AND")
	if len(postingLists) == 1 {
		// Se c'è un solo token, non serve l'intersezione
		resultSet := make(map[uint32]struct{})
		for _, id := range postingLists[0] {
			resultSet[id] = struct{}{}
		}
		return resultSet, nil
	}

	// Ordina le liste per lunghezza per un'intersezione più efficiente
	sort.Slice(postingLists, func(i, j int) bool {
		return len(postingLists[i]) < len(postingLists[j])
	})

	// Inizia l'intersezione
	resultSet := make(map[uint32]struct{})
	for _, id := range postingLists[0] {
		resultSet[id] = struct{}{}
	}

	for _, list := range postingLists[1:] {
		intersection := make(map[uint32]struct{})
		for _, id := range list {
			if _, ok := resultSet[id]; ok {
				intersection[id] = struct{}{}
			}
		}
		resultSet = intersection
		if len(resultSet) == 0 {
			break
		} // Ottimizzazione
	}

	return resultSet, nil
}
