// codice delle API http
package server

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"log"
	"net/http"
	"net/http/pprof"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// registerHTTPHandlers imposta le route per la API REST
func (s *Server) registerHTTPHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", s.router)
}

// router è il nostro router principale manuale. Analizza l'URL e delega all'handler corretto.
func (s *Server) router(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// --- Endpoint di Debug (pprof) ---
	if strings.HasPrefix(path, "/debug/pprof") {
		// Delega agli handler di pprof in base al suffisso
		switch {
		case path == "/debug/pprof/":
			pprof.Index(w, r)
		case path == "/debug/pprof/cmdline":
			pprof.Cmdline(w, r)
		case path == "/debug/pprof/profile":
			pprof.Profile(w, r)
		case path == "/debug/pprof/symbol":
			pprof.Symbol(w, r)
		case path == "/debug/pprof/trace":
			pprof.Trace(w, r)
		default:
			s.writeHTTPError(w, http.StatusNotFound, "Endpoint pprof non trovato")
		}
		return
	}

	// --- Endpoint di Sistema ---
	if path == "/system/aof-rewrite" {
		s.handleAOFRewriteHTTP(w, r)
		return
	}
	if path == "/system/save" {
		s.handleSaveHTTP(w, r)
		return
	}
	if strings.HasPrefix(path, "/system/tasks/") {
		s.handleTaskStatus(w, r)
		return
	}

	// --- Endpoint di Sistema ---
	if path == "/system/vectorizers" {
		s.handleGetVectorizers(w, r)
		return
	}
	if strings.HasPrefix(path, "/system/vectorizers/") {
		s.handleTriggerVectorizer(w, r)
		return
	}

	// --- Endpoint KV ---
	if strings.HasPrefix(path, "/kv/") {
		s.handleKV(w, r)
		return
	}

	// --- Endpoint Vettoriali ---
	switch path {
	case "/vector/indexes":
		// Questo gestisce GET (lista) e POST (crea)
		s.handleIndexesRequest(w, r)
		return
	case "/vector/actions/create":
		s.handleVectorCreate(w, r)
		return
	case "/vector/actions/add":
		s.handleVectorAdd(w, r)
		return
	case "/vector/actions/add-batch":
		s.handleVectorAddBatch(w, r)
		return
	case "/vector/actions/search":
		s.handleVectorSearch(w, r)
		return
	case "/vector/actions/delete_vector":
		s.handleVectorDelete(w, r)
		return
	case "/vector/actions/compress":
		s.handleVectorCompress(w, r)
		return
	case "/vector/actions/get-vectors":
		s.handleGetVectorsBatch(w, r)
		return
	}

	// Gestione di URL con parametri, come /vector/indexes/{name}
	if strings.HasPrefix(path, "/vector/indexes/") {
		// Tentiamo di matchare i pattern più specifici prima
		// Pattern: /vector/indexes/{indexName}/vectors/{vectorID}
		if parts := strings.Split(path, "/vectors/"); len(parts) == 2 {
			indexName := strings.TrimPrefix(parts[0], "/vector/indexes/")
			vectorID := parts[1]
			s.handleGetVector(w, r, indexName, vectorID)
			return
		}

		// Pattern: /vector/indexes/{indexName}
		indexName := strings.TrimPrefix(path, "/vector/indexes/")
		s.handleSingleIndexRequest(w, r, indexName)
		return
	}

	// Se nessun pattern ha matchato, restituisci Not Found.
	s.writeHTTPError(w, http.StatusNotFound, "Endpoint non trovato")
}

// handleIndexesRequest gestisce sia la lista che la creazione
func (s *Server) handleIndexesRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListIndexes(w, r)
	case http.MethodPost:
		s.handleVectorCreate(w, r) // Riusiamo l'handler esistente
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Consentiti solo GET e POST su /vector/indexes")
	}
}

// handleSingleIndexRequest gestisce GET e DELETE su un singolo indice
func (s *Server) handleSingleIndexRequest(w http.ResponseWriter, r *http.Request, indexName string) {
	// ... (Qui chiamiamo gli handler specifici per GET e DELETE, passando `indexName`)
	switch r.Method {
	case http.MethodGet:
		s.handleGetIndex(w, r, indexName)
	case http.MethodDelete:
		s.handleDeleteIndex(w, r, indexName)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Consentiti solo GET e DELETE su /vector/indexes/{name}")
	}
}

// --- Handler per KV ---

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request) {
	// estrae la chiave dall'URL. es. /kv/mia_chiave
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "La chiave non può essere vuota")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleKVGet(w, r, key)
	case http.MethodPost, http.MethodPut:
		s.handleKVSet(w, r, key)
	case http.MethodDelete:
		s.handleKVDelete(w, r, key)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Metodo non supportato")
	}
}

func (s *Server) handleKVGet(w http.ResponseWriter, r *http.Request, key string) {
	value, found := s.db.GetKVStore().Get(key)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, "Chiave non trovata")
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
}

// 1. Definiamo una struct per la richiesta di SET
type KVSetRequest struct {
	Value string `json:"value"`
}

func (s *Server) handleKVSet(w http.ResponseWriter, r *http.Request, key string) {
	// 2. Leggiamo e decodifichiamo il corpo JSON
	var req KVSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido, atteso un oggetto con la chiave 'value'")
		return
	}

	// Ora il valore è req.Value
	valueBytes := []byte(req.Value)

	// 3. La logica AOF e di store rimane la stessa
	// aofCommand := fmt.Sprintf("SET %s %s\n", key, req.Value) // Usiamo req.Value

	aofCommand := formatCommandAsRESP("SET", []byte(key), []byte(req.Value))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.db.GetKVStore().Set(key, valueBytes)

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleKVDelete(w http.ResponseWriter, r *http.Request, key string) {
	// aofCommand := fmt.Sprintf("DEL %s\n", key)
	aofCommand := formatCommandAsRESP("DEL", []byte(key))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.db.GetKVStore().Delete(key)

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

// 2. Implementa l'handler per la lista di indici
func (s *Server) handleListIndexes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo GET")
		return
	}

	info, err := s.db.GetVectorIndexInfo()
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonBytes, _ := json.Marshal(info)
	log.Printf("[DEBUG JSON] Risposta per /vector/indexes: %s", string(jsonBytes))

	s.writeHTTPResponse(w, http.StatusOK, info)
}

// 3. Implementa l'handler per il singolo indice
func (s *Server) handleGetIndex(w http.ResponseWriter, r *http.Request, indexName string) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo GET")
		return
	}

	info, err := s.db.GetSingleVectorIndexInfoAPI(indexName)
	if err != nil {
		// Se l'errore è "non trovato", restituisci 404
		if strings.Contains(err.Error(), "non trovato") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, info)
}

func (s *Server) handleDeleteIndex(w http.ResponseWriter, r *http.Request, indexName string) {
	// --- LOGICA AOF ---
	// Registra il comando PRIMA di eseguire l'operazione.
	// Creeremo un nuovo comando "VDROP" o "VDELETEINDEX" per l'AOF.
	// aofCommand := fmt.Sprintf("VDROP %s\n", indexName)
	aofCommand := formatCommandAsRESP("VDROP", []byte(indexName))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	// Esegui l'eliminazione
	err := s.db.DeleteVectorIndex(indexName)
	if err != nil {
		if strings.Contains(err.Error(), "non trovato") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	// Per DELETE, una risposta 204 No Content è spesso appropriata
	w.WriteHeader(http.StatusNoContent)
}

// --- Handler per Vettori (VCREATE, VADD, VSEARCH, VDEL) ---
type VectorCreateRequest struct {
	IndexName string `json:"index_name"`
	// omitempty per il campo metric così se il client non lo invia non sarà
	// presente nel json permettendo l'uso di un default
	Metric         string `json:"metric,omitempty"`
	M              int    `json:"m,omitempty"`
	EfConstruction int    `json:"ef_construction,omitempty"`
	Precision      string `json:"precision,omitempty"`
	TextLanguage   string `json:"text_language,omitempty"`
}

func (s *Server) handleVectorCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name è obbligatorio")
		return
	}

	// Se la metrica non è specificata, usiamo Euclidean come default.
	metric := distance.DistanceMetric(req.Metric)
	if metric == "" {
		metric = distance.Euclidean
	}

	precision := distance.PrecisionType(req.Precision)
	if precision == "" {
		precision = distance.Float32 // Imposta float32 come default se non specificato
	}

	// Il valore di default è "" (stringa vuota), che significa "nessuna analisi testuale".
	textLang := req.TextLanguage

	// scrittura AOF per persistenza
	// aofCommand := fmt.Sprintf("VCREATE %s METRIC %s M %d EF_CONSTRUCTION %d PRECISION %s TEXT_LANGUAGE %s\n", req.IndexName, metric, req.M, req.EfConstruction, precision, textLang)
	aofCommand := formatCommandAsRESP("VCREATE",
		[]byte(req.IndexName),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(req.M)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(req.EfConstruction)),
		[]byte("PRECISION"), []byte(string(precision)),
		[]byte("TEXT_LANGUAGE"), []byte(textLang),
	)

	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand) // gestire l'errore qui in un sistema di produzione
	s.aofMutex.Unlock()

	err := s.db.CreateVectorIndex(req.IndexName, metric, req.M, req.EfConstruction, precision, textLang)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Indice creato"})
}

type VectorAddRequest struct {
	IndexName string         `json:"index_name"`
	Id        string         `json:"id"`
	Vector    []float32      `json:"vector"`
	Metadata  map[string]any `json:"metadata,omitempty"` // omitempty = se il campo è nullo non apparirà nel json
}

func (s *Server) handleVectorAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	// Validazione base dell'input
	if req.IndexName == "" || req.Id == "" || len(req.Vector) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name, id, e vector sono campi obbligatori")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	/*
		// scrittura AOF per persistenza
		// ricostruisce la stringa del vettore separata da spazi
		vectorStr := float32SliceToString(req.Vector)
		aofCommand := fmt.Sprintf("VADD %s %s %s\n", req.IndexName, req.Id, vectorStr)
		s.aofMutex.Lock()
		s.aofFile.WriteString(aofCommand)
		s.aofMutex.Unlock()
	*/

	internalID, err := idx.Add(req.Id, req.Vector)
	if err != nil {
		// Controlla se l'errore è "ID già esistente"
		// In un'API REST, questo corrisponde a un errore 409 Conflict
		if strings.Contains(err.Error(), "già esistente") {
			s.writeHTTPError(w, http.StatusConflict, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("errore nell'aggiungere il vettore: %v", err))
		}
		return
	}

	// se ci sono metadati li indicizza
	if req.Metadata != nil {
		if err := s.db.AddMetadataUnlocked(req.IndexName, internalID, req.Metadata); err != nil {
			// operazione critica, se fallisce dovremmo disfare l'aggiunta del vettore (rollback)
			// --- LOGICA DI ROLLBACK ---
			log.Printf("ERRORE: Fallimento nell'indicizzare i metadati per '%s'. Avvio rollback.", req.Id)
			// Rimuovi il nodo appena aggiunto per mantenere la consistenza.
			idx.Delete(req.Id)
			// --- FINE ROLLBACK ---
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("errore nell'indicizzare i metadati: %v", err))
			return
		}
	}

	// --- LOGICA AOF ---
	/*
		// Costruisci il comando AOF solo dopo che tutte le validazioni sono passate.
		aofCommand, err := buildVAddAOFCommand(req.IndexName, req.Id, req.Vector, req.Metadata)
		if err != nil {
			// Anche qui, se la creazione del comando fallisce fare il rollback
			idx.Delete(req.Id)
			s.writeHTTPError(w, http.StatusInternalServerError, "errore nella creazione del comando AOF")
			return
		}
	*/
	vectorStr := float32SliceToString(req.Vector)
	metadataBytes, _ := json.Marshal(req.Metadata)

	aofCommand := formatCommandAsRESP("VADD",
		[]byte(req.IndexName),
		[]byte(req.Id),
		[]byte(vectorStr),
		metadataBytes,
	)

	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()
	// --- FINE LOGICA AOF ---

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Vettore aggiunto"})
}

// Vicino alle altre struct di richiesta
type VectorAddObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type BatchAddVectorsRequest struct {
	IndexName string            `json:"index_name"`
	Vectors   []VectorAddObject `json:"vectors"`
}

func (s *Server) handleVectorAddBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req BatchAddVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	if req.IndexName == "" || len(req.Vectors) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "'index_name' e 'vectors' sono obbligatori")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	// Iteriamo e aggiungiamo i vettori uno per uno.
	// La logica di Add, metadati e AOF è la stessa della VADD singola.
	var addedCount int
	for _, vec := range req.Vectors {
		// Qui potremmo riutilizzare la logica di handleVectorAdd, ma è più pulito
		// duplicare la parte di business logic.
		internalID, err := idx.Add(vec.Id, vec.Vector)
		if err != nil {
			log.Printf("Errore durante l'inserimento batch per l'ID '%s': %v. Interruzione.", vec.Id, err)
			// In un'implementazione più avanzata, potremmo restituire errori parziali.
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("fallimento all'inserimento di '%s': %v", vec.Id, err))
			return
		}

		if len(vec.Metadata) > 0 {
			if err := s.db.AddMetadataUnlocked(req.IndexName, internalID, vec.Metadata); err != nil {
				// Esegui il rollback per questo singolo vettore
				idx.Delete(vec.Id)
				log.Printf("Errore metadati per '%s', rollback eseguito. Errore: %v", vec.Id, err)
				s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("fallimento metadati per '%s': %v", vec.Id, err))
				return
			}
		}

		// Scrivi sull'AOF per ogni vettore aggiunto con successo
		// aofCommand, _ := buildVAddAOFCommand(req.IndexName, vec.Id, vec.Vector, vec.Metadata)
		vectorStr := float32SliceToString(vec.Vector)
		metadataBytes, _ := json.Marshal(vec.Metadata)

		aofCommand := formatCommandAsRESP("VADD",
			[]byte(req.IndexName),
			[]byte(vec.Id),
			[]byte(vectorStr),
			metadataBytes,
		)
		s.aofMutex.Lock()
		s.aofFile.WriteString(aofCommand)
		s.aofMutex.Unlock()

		addedCount++
	}

	// Incrementa il contatore globale del numero di vettori effettivamente aggiunti.
	if addedCount > 0 {
		atomic.AddInt64(&s.dirtyCounter, int64(addedCount))
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "OK",
		"vectors_added": addedCount,
	})
}

type VectorSearchRequest struct {
	IndexName   string    `json:"index_name"`
	K           int       `json:"k"`
	QueryVector []float32 `json:"query_vector"`
	Filter      string    `json:"filter,omitempty"` // filtro opzionale
	EfSearch    int       `json:"ef_search,omitempty"`
	Alpha       float64   `json:"alpha,omitempty"`
}

// FusedResult è la struct per l'ordinamento finale, con ID ESTERNO.
type FusedResult struct {
	ID    string // ID Esterno
	Score float64
}

// normalizeVectorScores normalizza le distanze (dove più piccolo è meglio) in un punteggio [0, 1]
// (dove più grande è meglio).
func normalizeVectorScores(results []types.SearchResult) {
	// Una semplice normalizzazione: 1 / (1 + distanza)
	// Distanza 0 -> Punteggio 1
	// Distanza grande -> Punteggio vicino a 0
	for i := range results {
		results[i].Score = 1.0 / (1.0 + results[i].Score)
	}
}

// normalizeTextScores normalizza i punteggi BM25 (dove più grande è meglio) in un range [0, 1].
func normalizeTextScores(results []types.SearchResult) {
	if len(results) == 0 {
		return
	}
	// Normalizzazione Min-Max: (score - min) / (max - min)
	// Dato che il minimo score BM25 è > 0, possiamo semplificare a score / max_score
	maxScore := 0.0
	for _, res := range results {
		if res.Score > maxScore {
			maxScore = res.Score
		}
	}
	if maxScore > 0 {
		for i := range results {
			results[i].Score /= maxScore
		}
	}
}

func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}
	hnswIndex, ok := idx.(*hnsw.Index) // Assumiamo sia un HNSW
	if !ok {
		s.writeHTTPError(w, http.StatusInternalServerError, "L'indice non è di tipo HNSW, la ricerca ibrida non è supportata")
		return
	}

	// --- 2. Separazione dei Filtri ---
	booleanFilters, textQuery, textQueryField := parseHybridFilter(req.Filter)

	// --- 3. Esecuzione del Pre-Filtering Booleano ---
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		allowList, err = s.db.FindIDsByFilter(req.IndexName, booleanFilters)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("Filtro booleano invalido: %v", err))
			return
		}
		// Se i filtri booleani non producono risultati, possiamo terminare subito
		if len(allowList) == 0 {
			s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": []string{}})
			return
		}
	}

	// --- 4. Esecuzione delle Ricerche Parallele ---

	// Controlla se la ricerca è puramente testuale
	isVectorQueryEmpty := true
	for _, v := range req.QueryVector {
		if v != 0 {
			isVectorQueryEmpty = false
			break
		}
	}

	// CASO A: RICERCA PURAMENTE TESTUALE (o solo filtri booleani senza query vettoriale)
	if isVectorQueryEmpty && textQuery != "" {
		textResults, _ := s.db.FindIDsByTextSearch(req.IndexName, textQueryField, textQuery)

		finalIDs := make([]string, 0, req.K)
		count := 0
		for _, res := range textResults {
			if count >= req.K {
				break
			}
			_, ok := allowList[res.DocID]
			if allowList == nil || ok {
				externalID, _ := hnswIndex.GetExternalID(res.DocID)
				finalIDs = append(finalIDs, externalID)
				count++
			}
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})
		return
	}

	// CASO B: RICERCA VETTORIALE O IBRIDA
	var vectorResults []types.SearchResult
	var textResults []types.SearchResult
	var wg sync.WaitGroup

	// Ricerca Vettoriale
	wg.Add(1)
	go func() {
		defer wg.Done()
		// idx.Search restituisce []string, dobbiamo convertirlo in []SearchResult
		// Modifichiamo Search per restituire anche le distanze!
		vectorResults = idx.SearchWithScores(req.QueryVector, req.K, allowList, req.EfSearch)
	}()

	// Ricerca Testuale (se presente)
	if textQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// db.findIDsByTextSearch già restituisce []SearchResult
			results, _ := s.db.FindIDsByTextSearch(req.IndexName, textQueryField, textQuery)
			// Applica l'allow list anche qui
			if allowList != nil {
				var filteredTextResults []types.SearchResult
				for _, res := range results {
					if _, ok := allowList[res.DocID]; ok {
						filteredTextResults = append(filteredTextResults, res)
					}
				}
				textResults = filteredTextResults
			} else {
				textResults = results
			}
		}()
	}

	wg.Wait() // Attendi che entrambe le ricerche finiscano

	// --- 5. Fase di Fusione (Reciprocal Rank Fusion) ---
	// Se non c'è stata una ricerca testuale, i risultati sono semplicemente quelli vettoriali.
	if textQuery == "" {
		finalIDs := make([]string, len(vectorResults))
		for i, res := range vectorResults {
			// Converti l'ID interno in esterno
			externalID, _ := hnswIndex.GetExternalID(res.DocID)
			finalIDs[i] = externalID
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})
		return
	}

	/*
		// versione RRF usata inizialmente, ignora i punteggi grezzi e si basa
		// solo sul rank (posizione) di un documento in ogni lista
		// non richiede normalizzazione ma perde l'informazione contenuta nei punteggi
		// per il momento mantengo ma forse da eliminare
		fusedScores := make(map[uint32]float64)
		const rrf_k = 60 // Costante di tuning standard

		// Aggiungi punteggi dalla ricerca vettoriale
		for rank, res := range vectorResults {
			// Per la distanza, un rank più basso è migliore. Il punteggio è 1 / (k + rank)
			fusedScores[res.DocID] += 1.0 / (float64(rrf_k + rank))
		}

		// Aggiungi punteggi dalla ricerca testuale
		for rank, res := range textResults {
			// Per BM25, un rank più basso è migliore. Usiamo la stessa formula.
			fusedScores[res.DocID] += 1.0 / (float64(rrf_k + rank))
		}
	*/

	// Imposta il valore di alpha. Default a 0.5 se non fornito.
	alpha := req.Alpha
	if alpha == 0 {
		alpha = 0.5
	} else if alpha < 0 || alpha > 1 {
		s.writeHTTPError(w, http.StatusBadRequest, "alpha deve essere compreso tra 0.1 e 1, 0 = default alpha 0.5")
		return
	}

	// Normalizza entrambi i set di punteggi in un range [0, 1]
	normalizeVectorScores(vectorResults)
	normalizeTextScores(textResults)

	// Crea una mappa per unire i risultati in modo efficiente
	fusedScores := make(map[uint32]float64)

	// Aggiungi punteggi dalla ricerca vettoriale
	for _, res := range vectorResults {
		fusedScores[res.DocID] += alpha * res.Score
	}

	// Aggiungi punteggi dalla ricerca testuale
	for _, res := range textResults {
		fusedScores[res.DocID] += (1 - alpha) * res.Score
	}

	// --- 6. Ordinamento Finale e Restituzione ---

	// Converti la mappa dei punteggi fusi in una slice
	finalResults := make([]FusedResult, 0, len(fusedScores))
	for id, score := range fusedScores {
		// Dobbiamo convertire l'ID interno in esterno
		externalID, found := hnswIndex.GetExternalID(id)
		if !found {
			continue
		} // Sicurezza: ignora se non troviamo la corrispondenza
		finalResults = append(finalResults, FusedResult{ID: externalID, Score: score})
	}

	// Ordina per punteggio decrescente
	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].Score > finalResults[j].Score
	})

	// Prendi i primi 'k' risultati
	finalIDs := make([]string, 0, req.K)
	for i := 0; i < req.K && i < len(finalResults); i++ {
		finalIDs = append(finalIDs, finalResults[i].ID)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})

}

// Definiamo la regex a livello di package per compilarla una sola volta.
var containsRegex = regexp.MustCompile(`(?i)\s*CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

// parseHybridFilter separa il filtro testuale (CONTAINS) dai filtri booleani.
func parseHybridFilter(filter string) (booleanFilter, textQuery, textField string) {
	// Trova la clausola CONTAINS
	matches := containsRegex.FindStringSubmatch(filter)

	if len(matches) == 0 {
		// Nessuna clausola CONTAINS, tutto il filtro è booleano.
		return filter, "", ""
	}

	// Estrai le parti della clausola CONTAINS
	fullMatch := matches[0]
	textField = matches[1]
	textQuery = matches[2]

	// Rimuovi la clausola CONTAINS dalla stringa di filtro originale
	// per ottenere solo i filtri booleani.
	booleanFilter = strings.Replace(filter, fullMatch, "", 1)

	// Pulisci eventuali "AND" rimasti appesi
	booleanFilter = strings.TrimSpace(booleanFilter)
	booleanFilter = strings.TrimPrefix(booleanFilter, "AND ")
	booleanFilter = strings.TrimSuffix(booleanFilter, " AND")
	booleanFilter = strings.TrimSpace(booleanFilter)
	// booleanFilter = strings.Trim(booleanFilter, "AND") // Trim taglia da entrambi i lati

	return booleanFilter, textQuery, textField
}

/*
func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	// --- NUOVA LOGICA DI FILTRAGGIO ---
	var allowList map[uint32]struct{}
	var err error

	if req.Filter != "" {
		allowList, err = s.db.FindIDsByFilter(req.IndexName, req.Filter)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("filtro invalido: %v", err))
			return
		}

		// --- LOG DI DEBUG ---
		log.Printf("[DEBUG FILTER] Filtro: '%s'. ID permessi trovati: %d. Lista: %v", req.Filter, len(allowList), allowList)
		// --- FINE LOG ---
		// Se il filtro non produce risultati, può terminare subito
		if len(allowList) == 0 {
			s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": []string{}})
			return
		}
	}

	results := idx.Search(req.QueryVector, req.K, allowList, req.EfSearch)
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": results})
}
*/

// struct della richiesta batch
type BatchGetVectorsRequest struct {
	IndexName string   `json:"index_name"`
	IDs       []string `json:"ids"`
}

// handler per la richiesta batch
func (s *Server) handleGetVectorsBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req BatchGetVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido, atteso un oggetto con 'index_name' e 'ids'")
		return
	}

	if req.IndexName == "" || len(req.IDs) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "'index_name' e 'ids' sono obbligatori")
		return
	}

	// La logica di chiamata allo store è la stessa
	vectorData, err := s.db.GetVectors(req.IndexName, req.IDs)
	if err != nil {
		if strings.Contains(err.Error(), "non trovato") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, vectorData)
}

type VectorDeleteRequest struct {
	IndexName string `json:"index_name"`
	Id        string `json:"id"`
}

func (s *Server) handleVectorDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { // Usiamo POST per coerenza con le altre azioni di modifica
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Indice '%s' non trovato", req.IndexName))
		return
	}

	// Logica AOF
	// aofCommand := fmt.Sprintf("VDEL %s %s\n", req.IndexName, req.Id)

	aofCommand := formatCommandAsRESP("VDEL", []byte(req.IndexName), []byte(req.Id))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	idx.Delete(req.Id)

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Vettore eliminato"})
}

// funzione handler per l'endpoint per compattazione AOF
/*
func (s *Server) handleAOFRewriteHTTP(w http.ResponseWriter, r *http.Request) {
	// Accettiamo solo richieste POST per azioni che modificano lo stato del server.
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST per avviare la riscrittura AOF")
		return
	}

	err := s.RewriteAOF()
	if err != nil {
		log.Printf("ERRORE CRITICO durante la riscrittura AOF via HTTP: %v", err)
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("fallimento riscrittura AOF: %v", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Riscrittura AOF completata con successo"})
}
*/

// versione async
func (s *Server) handleAOFRewriteHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { /* ... */
	}

	task := s.taskManager.NewTask()
	log.Printf("Avvio task di riscrittura AOF asincrono con ID: %s", task.ID)

	go func() {
		err := s.RewriteAOF()
		if err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
		}
	}()

	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

func (s *Server) handleSaveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST per avviare SAVE")
		return
	}

	// Aggiungi il comando SAVE all'AOF. Questo è importante perché se il server
	// crasha durante il SAVE, al riavvio saprà che deve fidarsi dello snapshot.
	// Per ora, omettiamo questa complessità e ci concentriamo sul flusso principale.

	if err := s.Save(); err != nil {
		log.Printf("ERRORE CRITICO durante SAVE via HTTP: %v", err)
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("fallimento processo SAVE: %v", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Snapshot del database creato con successo."})
}

func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo GET")
		return
	}

	// Estrai il task ID dall'URL
	taskID := strings.TrimPrefix(r.URL.Path, "/system/tasks/")
	if taskID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "Task ID mancante nell'URL")
		return
	}

	// Recupera il task dal manager
	task, found := s.taskManager.GetTask(taskID)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Task con ID '%s' non trovato", taskID))
		return
	}

	// Leggi lo stato del task in modo sicuro e restituiscilo
	task.mu.RLock()
	defer task.mu.RUnlock()
	s.writeHTTPResponse(w, http.StatusOK, task)
}

type VectorCompressRequest struct {
	IndexName string `json:"index_name"`
	Precision string `json:"precision"`
}

func (s *Server) handleGetVectorizers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo GET")
		return
	}
	if s.vectorizerService == nil {
		s.writeHTTPResponse(w, http.StatusOK, []interface{}{})
		return
	}
	statuses := s.vectorizerService.GetStatuses()
	s.writeHTTPResponse(w, http.StatusOK, statuses)
}

func (s *Server) handleTriggerVectorizer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/system/vectorizers/")
	name = strings.TrimSuffix(name, "/trigger") // Rimuove il suffisso

	if s.vectorizerService == nil {
		s.writeHTTPError(w, http.StatusNotFound, "VectorizerService non è attivo")
		return
	}

	err := s.vectorizerService.Trigger(name)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status":  "OK",
		"message": fmt.Sprintf("Sincronizzazione per il vectorizer '%s' avviata in background.", name),
	})
}

// compressione di un indice ad una determinata precisione (float16 o int8)
/*
func (s *Server) handleVectorCompress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorCompressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	newPrecision := distance.PrecisionType(req.Precision)
	if newPrecision != distance.Float16 && newPrecision != distance.Int8 {
		s.writeHTTPError(w, http.StatusBadRequest, "Precisione non valida, usare 'float16' or 'int8'")
		return
	}

	// Aggiungi all'AOF PRIMA di eseguire l'operazione
	//aofCommand := fmt.Sprintf("VCOMPRESS %s %s\n", req.IndexName, newPrecision)
	//s.aofMutex.Lock()
	//s.aofFile.WriteString(aofCommand)
	//s.aofMutex.Unlock()

	// Esegui la compressione
	if err := s.store.Compress(req.IndexName, newPrecision); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENTA IL CONTATORE

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Indice compresso con successo"})
}
*/

// versione async
func (s *Server) handleVectorCompress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorCompressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "JSON invalido")
		return
	}

	newPrecision := distance.PrecisionType(req.Precision)
	if newPrecision != distance.Float16 && newPrecision != distance.Int8 {
		s.writeHTTPError(w, http.StatusBadRequest, "Precisione non valida, usare 'float16' or 'int8'")
		return
	}

	// 1. Crea un nuovo task per questa operazione
	task := s.taskManager.NewTask()
	log.Printf("Avvio task di compressione asincrono con ID: %s", task.ID)

	// 2. Avvia il lavoro pesante in una nuova goroutine
	go func() {
		// Chiama la logica di compressione esistente
		err := s.db.Compress(req.IndexName, newPrecision)

		// 3. Aggiorna lo stato del task in base al risultato
		if err != nil {
			log.Printf("Task di compressione %s fallito: %v", task.ID, err)
			task.SetError(err)
		} else {
			log.Printf("Task di compressione %s completato con successo.", task.ID)
			task.SetStatus(TaskStatusCompleted)
		}
	}()

	// 4. Restituisci immediatamente una risposta 202 Accepted con il task ID
	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

// 4. Implementa la logica finale in handleGetVector
func (s *Server) handleGetVector(w http.ResponseWriter, r *http.Request, indexName, vectorID string) {
	vectorData, err := s.db.GetVector(indexName, vectorID)
	if err != nil {
		if strings.Contains(err.Error(), "non trovato") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, vectorData)
}

// --- Helper per le Risposte HTTP ---

func (s *Server) writeHTTPResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeHTTPError(w http.ResponseWriter, statusCode int, message string) {
	s.writeHTTPResponse(w, statusCode, map[string]string{"error": message})
}

func float32SliceToString(slice []float32) string {
	// strings.Builder è il modo più efficiente per costruire stringhe in Go
	var b strings.Builder
	for i, v := range slice {
		if i > 0 {
			b.WriteString(" ")
		}
		// 'f' per formato, -1 per la minima precisione necessaria, 32 per float32
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	return b.String()
}

// Funzione helper per costruire la stringa del comando AOF
func buildVAddAOFCommand(indexName, id string, vector []float32, metadata map[string]any) (string, error) {
	vectorStr := float32SliceToString(vector) // Usiamo la funzione che avevamo già

	if len(metadata) == 0 {
		return fmt.Sprintf("VADD %s %s %s\n", indexName, id, vectorStr), nil
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}

	// Racchiudiamo il JSON tra virgolette singole per sicurezza, anche se il nostro
	// parser TCP attuale non le gestisce (è una buona pratica per il futuro).
	return fmt.Sprintf("VADD %s %s %s %s\n", indexName, id, vectorStr, string(metadataBytes)), nil
}
