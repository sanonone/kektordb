package server

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"net/http"
	"net/http/pprof"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// registerHTTPHandlers sets up all HTTP routes.
func (s *Server) registerHTTPHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", s.router)
}

func (s *Server) router(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Debug endpoints
	if strings.HasPrefix(path, "/debug/pprof") {
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
			handlerName := strings.TrimPrefix(path, "/debug/pprof/")
			pprof.Handler(handlerName).ServeHTTP(w, r)
		}
		return
	}

	// System endpoints
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

	// Vectorizer management
	if path == "/system/vectorizers" {
		s.handleGetVectorizers(w, r)
		return
	}
	if strings.HasPrefix(path, "/system/vectorizers/") {
		s.handleTriggerVectorizer(w, r)
		return
	}

	// KV endpoints
	if strings.HasPrefix(path, "/kv/") {
		s.handleKV(w, r)
		return
	}

	// Vector API
	switch path {
	case "/vector/indexes":
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

	// Dynamic index routes
	if strings.HasPrefix(path, "/vector/indexes/") {
		if parts := strings.Split(path, "/vectors/"); len(parts) == 2 {
			s.handleGetVector(w, r, strings.TrimPrefix(parts[0], "/vector/indexes/"), parts[1])
			return
		}
		s.handleSingleIndexRequest(w, r, strings.TrimPrefix(path, "/vector/indexes/"))
		return
	}

	s.writeHTTPError(w, http.StatusNotFound, "Endpoint not found")
}

// --- INDEX HANDLERS ---

func (s *Server) handleIndexesRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// ENGINE CALL
		info, err := s.Engine.DB.GetVectorIndexInfo()
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, info)
	case http.MethodPost:
		s.handleVectorCreate(w, r)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleSingleIndexRequest(w http.ResponseWriter, r *http.Request, indexName string) {
	switch r.Method {
	case http.MethodGet:
		info, err := s.Engine.DB.GetSingleVectorIndexInfoAPI(indexName)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			s.writeHTTPError(w, status, err.Error())
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, info)
	case http.MethodDelete:
		// ENGINE CALL
		// Nota: VDeleteIndex non è ancora in ops.go, lo aggiungeremo o useremo raw DB delete + AOF write manuale?
		// Meglio aggiungere VDropIndex in engine. Per ora faccio manuale qui ma è brutto.
		// TODO: Aggiungere VDrop in engine/ops.go
		// Hack temporaneo per compilare:
		s.Engine.DB.DeleteVectorIndex(indexName)
		// (Manca persistenza qui se non aggiungiamo il metodo in Engine, ma per ora compila)
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Only GET and DELETE allowed")
	}
}

// --- KV HANDLERS ---

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "Key cannot be empty")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// ENGINE CALL
		value, found := s.Engine.KVGet(key)
		if !found {
			s.writeHTTPError(w, http.StatusNotFound, "Key not found")
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
	case http.MethodPost, http.MethodPut:
		var req struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		// ENGINE CALL (Automatic Persistence)
		if err := s.Engine.KVSet(key, []byte(req.Value)); err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
	case http.MethodDelete:
		// ENGINE CALL
		if err := s.Engine.KVDelete(key); err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Method not supported")
	}
}

// --- VECTOR HANDLERS ---

func (s *Server) handleVectorCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req VectorCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name required")
		return
	}

	metric := distance.DistanceMetric(req.Metric)
	if metric == "" {
		metric = distance.Euclidean
	}

	prec := distance.PrecisionType(req.Precision)
	if prec == "" {
		prec = distance.Float32
	}

	// ENGINE CALL
	err := s.Engine.VCreate(req.IndexName, metric, req.M, req.EfConstruction, prec, req.TextLanguage)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Index created"})
}

func (s *Server) handleVectorAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req VectorAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if req.IndexName == "" || req.Id == "" || len(req.Vector) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "Missing fields")
		return
	}

	// ENGINE CALL
	err := s.Engine.VAdd(req.IndexName, req.Id, req.Vector, req.Metadata)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		s.writeHTTPError(w, status, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleVectorAddBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req BatchAddVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// ENGINE CALL (Gestisce persistenza e loop internamente)
	err := s.Engine.VAddBatch(req.IndexName, req.Vectors)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "OK",
		"vectors_added": len(req.Vectors),
	})
}

// FusedResult is the struct for the final sorting, with EXTERNAL ID.
type FusedResult struct {
	ID    string // External ID
	Score float64
}

func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// 1. Recupero Indice tramite Engine
	idx, found := s.Engine.DB.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Index '%s' not found", req.IndexName))
		return
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		s.writeHTTPError(w, http.StatusInternalServerError, "Index is not of type HNSW, hybrid search is not supported")
		return
	}

	// 2. Parsing Filtri (Ibrido: Testo + Booleano)
	booleanFilters, textQuery, textQueryField := parseHybridFilter(req.Filter)

	// 3. Pre-Filtering Booleano (Metadata)
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		// Chiamata diretta al DB dell'Engine
		allowList, err = s.Engine.DB.FindIDsByFilter(req.IndexName, booleanFilters)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("Invalid boolean filter: %v", err))
			return
		}
		// Se il filtro non trova nulla, possiamo fermarci subito
		if len(allowList) == 0 {
			s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": []string{}})
			return
		}
	}

	// 4. Controllo Query Vettoriale (se è vuota/zero)
	isVectorQueryEmpty := true
	if len(req.QueryVector) > 0 {
		for _, v := range req.QueryVector {
			if v != 0 {
				isVectorQueryEmpty = false
				break
			}
		}
	} else {
		isVectorQueryEmpty = true
	}

	// --- CASO A: SOLO RICERCA TESTUALE (o solo filtri senza vettore) ---
	if isVectorQueryEmpty && textQuery != "" {
		textResults, _ := s.Engine.DB.FindIDsByTextSearch(req.IndexName, textQueryField, textQuery)

		finalIDs := make([]string, 0, req.K)
		count := 0
		for _, res := range textResults {
			if count >= req.K {
				break
			}
			// Applica allowList se presente
			if allowList != nil {
				if _, ok := allowList[res.DocID]; !ok {
					continue
				}
			}
			externalID, _ := hnswIndex.GetExternalID(res.DocID)
			finalIDs = append(finalIDs, externalID)
			count++
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})
		return
	}

	// --- CASO B: RICERCA VETTORIALE O IBRIDA ---
	var vectorResults []types.SearchResult
	var textResults []types.SearchResult
	var wg sync.WaitGroup

	// B.1 Ricerca Vettoriale (Parallela)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Chiamata diretta all'indice HNSW
		vectorResults = idx.SearchWithScores(req.QueryVector, req.K, allowList, req.EfSearch)
	}()

	// B.2 Ricerca Testuale (Parallela, se c'è una query di testo)
	if textQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, _ := s.Engine.DB.FindIDsByTextSearch(req.IndexName, textQueryField, textQuery)

			// Applichiamo l'allowList anche ai risultati testuali
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

	wg.Wait() // Attesa completamento

	// 5. Fusione Risultati (Reciprocal Rank Fusion o Weighted Sum)

	// Se non c'era testo, ritorniamo solo i risultati vettoriali
	if textQuery == "" {
		finalIDs := make([]string, len(vectorResults))
		for i, res := range vectorResults {
			externalID, _ := hnswIndex.GetExternalID(res.DocID)
			finalIDs[i] = externalID
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})
		return
	}

	// --- Logica di Fusione (Weighted Sum) ---
	alpha := req.Alpha
	if alpha == 0 {
		alpha = 0.5 // Default bilanciato
	} else if alpha < 0 || alpha > 1 {
		s.writeHTTPError(w, http.StatusBadRequest, "alpha must be between 0 and 1")
		return
	}

	normalizeVectorScores(vectorResults)
	normalizeTextScores(textResults)

	fusedScores := make(map[uint32]float64)

	// Score Vettoriale
	for _, res := range vectorResults {
		fusedScores[res.DocID] += alpha * res.Score
	}

	// Score Testuale
	for _, res := range textResults {
		fusedScores[res.DocID] += (1 - alpha) * res.Score
	}

	// 6. Ordinamento Finale e Conversione ID
	finalResults := make([]FusedResult, 0, len(fusedScores))
	for id, score := range fusedScores {
		externalID, found := hnswIndex.GetExternalID(id)
		if !found {
			continue
		}
		finalResults = append(finalResults, FusedResult{ID: externalID, Score: score})
	}

	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].Score > finalResults[j].Score
	})

	// Top K
	finalIDs := make([]string, 0, req.K)
	for i := 0; i < req.K && i < len(finalResults); i++ {
		finalIDs = append(finalIDs, finalResults[i].ID)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})
}

func (s *Server) handleVectorDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req VectorDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// ENGINE CALL
	if err := s.Engine.VDelete(req.IndexName, req.Id); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleVectorCompress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req VectorCompressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	prec := distance.PrecisionType(req.Precision)

	task := s.taskManager.NewTask()
	go func() {
		// ENGINE CALL (Direct DB access for heavy ops is ok inside Engine,
		// but here we access DB directly via Engine pointer)
		err := s.Engine.DB.Compress(req.IndexName, prec)
		// NOTE: Compression changes memory but doesn't rewrite AOF automatically yet.
		// User should trigger Snapshot or we should trigger it here.
		if err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
		}
	}()

	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

func (s *Server) handleGetVectorsBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	var req BatchGetVectorsRequest
	json.NewDecoder(r.Body).Decode(&req)

	// ENGINE CALL
	data, err := s.Engine.DB.GetVectors(req.IndexName, req.IDs)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, data)
}

func (s *Server) handleGetVector(w http.ResponseWriter, r *http.Request, indexName, vectorID string) {
	data, err := s.Engine.DB.GetVector(indexName, vectorID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		s.writeHTTPError(w, status, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, data)
}

// --- SYSTEM HANDLERS ---

func (s *Server) handleSaveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	// ENGINE CALL
	if err := s.Engine.SaveSnapshot(); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Snapshot created"})
}

func (s *Server) handleAOFRewriteHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	task := s.taskManager.NewTask()
	go func() {
		// ENGINE CALL
		if err := s.Engine.RewriteAOF(); err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
		}
	}()
	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

// Helpers
func (s *Server) writeHTTPResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeHTTPError(w http.ResponseWriter, statusCode int, message string) {
	s.writeHTTPResponse(w, statusCode, map[string]string{"error": message})
}

// --- SYSTEM & VECTORIZER HANDLERS ---

func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use GET")
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/system/tasks/")
	if taskID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "Task ID missing")
		return
	}

	task, found := s.taskManager.GetTask(taskID)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, "Task not found")
		return
	}

	task.mu.RLock()
	defer task.mu.RUnlock()
	s.writeHTTPResponse(w, http.StatusOK, task)
}

func (s *Server) handleGetVectorizers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use GET")
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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/system/vectorizers/")
	name = strings.TrimSuffix(name, "/trigger")

	if s.vectorizerService == nil {
		s.writeHTTPError(w, http.StatusNotFound, "VectorizerService not active")
		return
	}

	err := s.vectorizerService.Trigger(name)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status":  "OK",
		"message": fmt.Sprintf("Synchronization for vectorizer '%s' triggered.", name),
	})
}

// --- HELPER FUNCTIONS FOR SEARCH ---

var containsRegex = regexp.MustCompile(`(?i)\s*CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

func parseHybridFilter(filter string) (booleanFilter, textQuery, textField string) {
	matches := containsRegex.FindStringSubmatch(filter)

	if len(matches) == 0 {
		// Nessuna parte testuale, tutto è filtro booleano
		return filter, "", ""
	}

	// matches[0] è l'intera stringa "CONTAINS(...)"
	textField = matches[1]
	textQuery = matches[2]

	// Rimuovi la parte CONTAINS dalla stringa originale per lasciare solo i filtri booleani
	booleanFilter = strings.Replace(filter, matches[0], "", 1)

	// Pulizia AND/OR residui
	booleanFilter = strings.TrimSpace(booleanFilter)
	booleanFilter = strings.TrimPrefix(booleanFilter, "AND ")
	booleanFilter = strings.TrimSuffix(booleanFilter, " AND")
	booleanFilter = strings.TrimSpace(booleanFilter)

	return booleanFilter, textQuery, textField
}

func normalizeVectorScores(results []types.SearchResult) {
	// Simple normalization: 1 / (1 + distance)
	for i := range results {
		results[i].Score = 1.0 / (1.0 + results[i].Score)
	}
}

func normalizeTextScores(results []types.SearchResult) {
	if len(results) == 0 {
		return
	}
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
