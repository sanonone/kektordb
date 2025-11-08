// Package server implements the KektorDB HTTP API and request routing.
//
// This file defines all REST endpoints for:
//   - Vector operations (create, add, search, delete, compress)
//   - Index management
//   - System tasks (AOF rewrite, snapshot, vectorizers)
//   - Debug (pprof)
//
// All routes are manually routed via a custom router for performance and clarity.
// JSON responses are standardized using writeHTTPResponse/writeHTTPError.
//
// Endpoints are versioned under /vector/* and /system/* for future compatibility.

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

// registerHTTPHandlers sets up all HTTP routes on the provided ServeMux.
//
// This includes:
//   - Debug endpoints (/debug/pprof/*)
//   - System endpoints (/system/*)
//   - KV endpoints (/kv/*)
//   - Vector API endpoints (/vector/*)
//
// The root path "/" is routed to the main request router.
func (s *Server) registerHTTPHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", s.router)
}

// router is the main HTTP request router.
//
// It parses the request path and dispatches to the appropriate handler.
// Manual routing avoids external router overhead and allows fine-grained control.
func (s *Server) router(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// --- Debug: pprof profiling endpoints ---
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
			//s.writeHTTPError(w, http.StatusNotFound, "pprof endpoint not found")
		}
		return
	}

	// --- System: persistence and task management ---
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

	// --- System: vectorizer management ---
	if path == "/system/vectorizers" {
		s.handleGetVectorizers(w, r)
		return
	}
	if strings.HasPrefix(path, "/system/vectorizers/") {
		s.handleTriggerVectorizer(w, r)
		return
	}

	// --- KV: key-value store operations ---
	if strings.HasPrefix(path, "/kv/") {
		s.handleKV(w, r)
		return
	}

	// --- Vector API: core operations ---
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

	// Try dynamic index routes: /vector/indexes/{name}/...
	if strings.HasPrefix(path, "/vector/indexes/") {
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

	s.writeHTTPError(w, http.StatusNotFound, "Endpoint not found")
}

// handleIndexesRequest handles GET (list indexes) and POST (create index) on /vector/indexes.
func (s *Server) handleIndexesRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListIndexes(w, r)
	case http.MethodPost:
		s.handleVectorCreate(w, r)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSingleIndexRequest gestisce GET e DELETE su un singolo indice
func (s *Server) handleSingleIndexRequest(w http.ResponseWriter, r *http.Request, indexName string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetIndex(w, r, indexName)
	case http.MethodDelete:
		s.handleDeleteIndex(w, r, indexName)
	default:
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Only GET and DELETE are allowed on /vector/indexes/{name}")
	}
}

// --- Handler per KV ---

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request) {
	// estrae la chiave dall'URL. es. /kv/mia_chiave
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "Key cannot be empty")
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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Method not supported")
	}
}

func (s *Server) handleKVGet(w http.ResponseWriter, r *http.Request, key string) {
	value, found := s.db.GetKVStore().Get(key)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, "Key not found")
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
}

// Define a struct for the SET request
type KVSetRequest struct {
	Value string `json:"value"`
}

func (s *Server) handleKVSet(w http.ResponseWriter, r *http.Request, key string) {
	// Read and decode the JSON body.
	var req KVSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON, expected an object with a 'value' key")
		return
	}

	valueBytes := []byte(req.Value)

	// The AOF and store logic remains the same.
	// aofCommand := fmt.Sprintf("SET %s %s\n", key, req.Value)

	aofCommand := formatCommandAsRESP("SET", []byte(key), []byte(req.Value))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.db.GetKVStore().Set(key, valueBytes)

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENT THE COUNTER

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleKVDelete(w http.ResponseWriter, r *http.Request, key string) {
	aofCommand := formatCommandAsRESP("DEL", []byte(key))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	s.db.GetKVStore().Delete(key)

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENT THE COUNTER

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleListIndexes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the GET method")
		return
	}

	info, err := s.db.GetVectorIndexInfo()
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonBytes, _ := json.Marshal(info)
	log.Printf("[DEBUG JSON] Response for /vector/indexes: %s", string(jsonBytes))

	s.writeHTTPResponse(w, http.StatusOK, info)
}

// Implement the handler for a single index.
func (s *Server) handleGetIndex(w http.ResponseWriter, r *http.Request, indexName string) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the GET method")
		return
	}

	info, err := s.db.GetSingleVectorIndexInfoAPI(indexName)
	if err != nil {
		// If the error is "not found", return 404.
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, info)
}

func (s *Server) handleDeleteIndex(w http.ResponseWriter, r *http.Request, indexName string) {
	// --- AOF LOGIC ---
	// Record the command BEFORE executing the operation.
	aofCommand := formatCommandAsRESP("VDROP", []byte(indexName))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	// Execute the deletion.
	err := s.db.DeleteVectorIndex(indexName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	atomic.AddInt64(&s.dirtyCounter, 1) // <-- INCREMENT THE COUNTER

	w.WriteHeader(http.StatusNoContent)
}

// --- Vector Handlers (VCREATE, VADD, VSEARCH, VDEL) ---
type VectorCreateRequest struct {
	IndexName string `json:"index_name"`
	// omitempty for the metric field so if the client doesn't send it, it won't be
	// present in the json, allowing a default to be used.
	Metric         string `json:"metric,omitempty"`
	M              int    `json:"m,omitempty"`
	EfConstruction int    `json:"ef_construction,omitempty"`
	Precision      string `json:"precision,omitempty"`
	TextLanguage   string `json:"text_language,omitempty"`
}

func (s *Server) handleVectorCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req VectorCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name is required")
		return
	}

	// If the metric is not specified, we use Euclidean as the default.
	metric := distance.DistanceMetric(req.Metric)
	if metric == "" {
		metric = distance.Euclidean
	}

	precision := distance.PrecisionType(req.Precision)
	if precision == "" {
		precision = distance.Float32 // Set float32 as default if not specified.
	}

	// The default value is "" (empty string), which means "no text analysis".
	textLang := req.TextLanguage

	// AOF write for persistence.
	aofCommand := formatCommandAsRESP("VCREATE",
		[]byte(req.IndexName),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(req.M)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(req.EfConstruction)),
		[]byte("PRECISION"), []byte(string(precision)),
		[]byte("TEXT_LANGUAGE"), []byte(textLang),
	)

	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	err := s.db.CreateVectorIndex(req.IndexName, metric, req.M, req.EfConstruction, precision, textLang)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}

	atomic.AddInt64(&s.dirtyCounter, 1)

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Index created"})
}

type VectorAddRequest struct {
	IndexName string         `json:"index_name"`
	Id        string         `json:"id"`
	Vector    []float32      `json:"vector"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (s *Server) handleVectorAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req VectorAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Basic input validation.
	if req.IndexName == "" || req.Id == "" || len(req.Vector) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "index_name, id, and vector are required fields")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Index '%s' not found", req.IndexName))
		return
	}

	internalID, err := idx.Add(req.Id, req.Vector)
	if err != nil {
		// Check if the error is "ID already exists".
		// In a REST API, this corresponds to a 409 Conflict error.
		if strings.Contains(err.Error(), "already exists") {
			s.writeHTTPError(w, http.StatusConflict, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("error adding vector: %v", err))
		}
		return
	}

	// If metadata exists, index it.
	if req.Metadata != nil {
		if err := s.db.AddMetadataUnlocked(req.IndexName, internalID, req.Metadata); err != nil {
			// Critical operation, if it fails we should undo the vector addition (rollback).
			// --- ROLLBACK LOGIC ---
			log.Printf("ERROR: Failed to index metadata for '%s'. Starting rollback.", req.Id)
			// Remove the newly added node to maintain consistency.
			idx.Delete(req.Id)
			// --- END ROLLBACK ---
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("error indexing metadata: %v", err))
			return
		}
	}

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

	atomic.AddInt64(&s.dirtyCounter, 1)

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Vector added"})
}

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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req BatchAddVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.IndexName == "" || len(req.Vectors) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "'index_name' and 'vectors' are required")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Index '%s' not found", req.IndexName))
		return
	}
	s.db.RLock()
	defer s.db.RUnlock()

	// iterate and add the vectors one by one.
	// The Add, metadata, and AOF logic is the same as for a single VADD.
	var addedCount int
	for _, vec := range req.Vectors {
		internalID, err := idx.Add(vec.Id, vec.Vector)
		if err != nil {
			log.Printf("Error during batch insertion for ID '%s': %v. Aborting.", vec.Id, err)
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("failed to insert '%s': %v", vec.Id, err))
			return
		}

		if len(vec.Metadata) > 0 {
			if err := s.db.AddMetadataUnlocked(req.IndexName, internalID, vec.Metadata); err != nil {
				// Perform rollback for this single vector.
				idx.Delete(vec.Id)
				log.Printf("Metadata error for '%s', rollback performed. Error: %v", vec.Id, err)
				s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("metadata failure for '%s': %v", vec.Id, err))
				return
			}
		}

		// Write to AOF for each successfully added vector.
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

	// Increment the global counter by the number of vectors actually added.
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
	Filter      string    `json:"filter,omitempty"`
	EfSearch    int       `json:"ef_search,omitempty"`
	Alpha       float64   `json:"alpha,omitempty"`
}

// FusedResult is the struct for the final sorting, with EXTERNAL ID.
type FusedResult struct {
	ID    string // External ID
	Score float64
}

// normalizeVectorScores normalizes distances (where smaller is better) into a [0, 1] score
// (where larger is better).
func normalizeVectorScores(results []types.SearchResult) {
	// A simple normalization: 1 / (1 + distance)
	// Distance 0 -> Score 1
	// Large distance -> Score close to 0
	for i := range results {
		results[i].Score = 1.0 / (1.0 + results[i].Score)
	}
}

// normalizeTextScores normalizes BM25 scores (where larger is better) to a [0, 1] range.
func normalizeTextScores(results []types.SearchResult) {
	if len(results) == 0 {
		return
	}
	// Min-Max normalization: (score - min) / (max - min)
	// Since the minimum BM25 score is > 0, we can simplify to score / max_score.
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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Index '%s' not found", req.IndexName))
		return
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		s.writeHTTPError(w, http.StatusInternalServerError, "Index is not of type HNSW, hybrid search is not supported")
		return
	}

	// --- Filter Separation ---
	booleanFilters, textQuery, textQueryField := parseHybridFilter(req.Filter)

	// --- Boolean Pre-Filtering Execution ---
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		allowList, err = s.db.FindIDsByFilter(req.IndexName, booleanFilters)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("Invalid boolean filter: %v", err))
			return
		}
		// If the boolean filters yield no results, we can terminate immediately.
		if len(allowList) == 0 {
			s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": []string{}})
			return
		}
	}

	// --- Parallel Search Execution ---

	// Check if the search is purely text-based.
	isVectorQueryEmpty := true
	for _, v := range req.QueryVector {
		if v != 0 {
			isVectorQueryEmpty = false
			break
		}
	}

	// CASE A: PURELY TEXT-BASED SEARCH (or only boolean filters without a vector query).
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

	// CASE B: VECTOR OR HYBRID SEARCH.
	var vectorResults []types.SearchResult
	var textResults []types.SearchResult
	var wg sync.WaitGroup

	// Vector Search.
	wg.Add(1)
	go func() {
		defer wg.Done()
		vectorResults = idx.SearchWithScores(req.QueryVector, req.K, allowList, req.EfSearch)
	}()

	// Text Search (if present).
	if textQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// db.findIDsByTextSearch already returns []SearchResult.
			results, _ := s.db.FindIDsByTextSearch(req.IndexName, textQueryField, textQuery)
			// Apply the allow list here as well.
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

	wg.Wait() // Wait for both searches to finish.

	// --- 5. Fusion Phase (Reciprocal Rank Fusion) ---
	// If there was no text search, the results are simply the vector search results.
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

	// Set the alpha value. Defaults to 0.5 if not provided.
	alpha := req.Alpha
	if alpha == 0 {
		alpha = 0.5
	} else if alpha < 0 || alpha > 1 {
		s.writeHTTPError(w, http.StatusBadRequest, "alpha must be between 0.1 and 1, 0 = default alpha 0.5")
		return
	}

	// Normalize both sets of scores to a [0, 1] range.
	normalizeVectorScores(vectorResults)
	normalizeTextScores(textResults)

	// Create a map to merge the results efficiently.
	fusedScores := make(map[uint32]float64)

	// Add scores from vector search.
	for _, res := range vectorResults {
		fusedScores[res.DocID] += alpha * res.Score
	}

	// Add scores from text search.
	for _, res := range textResults {
		fusedScores[res.DocID] += (1 - alpha) * res.Score
	}

	// --- Final Sorting and Response ---

	// Convert the map of fused scores into a slice.
	finalResults := make([]FusedResult, 0, len(fusedScores))
	for id, score := range fusedScores {
		// We need to convert the internal ID to an external one.
		externalID, found := hnswIndex.GetExternalID(id)
		if !found {
			continue
		} // Safety check: ignore if we don't find a match.
		finalResults = append(finalResults, FusedResult{ID: externalID, Score: score})
	}

	// Sort by score in descending order.
	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].Score > finalResults[j].Score
	})

	// Take the top 'k' results.
	finalIDs := make([]string, 0, req.K)
	for i := 0; i < req.K && i < len(finalResults); i++ {
		finalIDs = append(finalIDs, finalResults[i].ID)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": finalIDs})

}

// Define the regex at the package level to compile it only once.
var containsRegex = regexp.MustCompile(`(?i)\s*CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

// parseHybridFilter separates the text filter (CONTAINS) from the boolean filters.
func parseHybridFilter(filter string) (booleanFilter, textQuery, textField string) {
	// Find the CONTAINS clause.
	matches := containsRegex.FindStringSubmatch(filter)

	if len(matches) == 0 {
		// No CONTAINS clause, the entire filter is boolean.
		return filter, "", ""
	}

	// Extract the parts of the CONTAINS clause.
	fullMatch := matches[0]
	textField = matches[1]
	textQuery = matches[2]

	// Remove the CONTAINS clause from the original filter string
	// to get only the boolean filters.
	booleanFilter = strings.Replace(filter, fullMatch, "", 1)

	// Clean up any dangling "AND".
	booleanFilter = strings.TrimSpace(booleanFilter)
	booleanFilter = strings.TrimPrefix(booleanFilter, "AND ")
	booleanFilter = strings.TrimSuffix(booleanFilter, " AND")
	booleanFilter = strings.TrimSpace(booleanFilter)
	// booleanFilter = strings.Trim(booleanFilter, "AND") // Trim taglia da entrambi i lati

	return booleanFilter, textQuery, textField
}

// Batch request struct.
type BatchGetVectorsRequest struct {
	IndexName string   `json:"index_name"`
	IDs       []string `json:"ids"`
}

// Handler for the batch request.
func (s *Server) handleGetVectorsBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req BatchGetVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON, expected an object with 'index_name' and 'ids'")
		return
	}

	if req.IndexName == "" || len(req.IDs) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, "'index_name' and 'ids' are required")
		return
	}

	// The logic for calling the store
	vectorData, err := s.db.GetVectors(req.IndexName, req.IDs)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
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
	if r.Method != http.MethodPost { // POST for consistency with other modification actions.
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Usare il metodo POST")
		return
	}

	var req VectorDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	idx, found := s.db.GetVectorIndex(req.IndexName)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Index '%s' not found", req.IndexName))
		return
	}

	// Logica AOF
	// aofCommand := fmt.Sprintf("VDEL %s %s\n", req.IndexName, req.Id)

	aofCommand := formatCommandAsRESP("VDEL", []byte(req.IndexName), []byte(req.Id))
	s.aofMutex.Lock()
	s.aofFile.WriteString(aofCommand)
	s.aofMutex.Unlock()

	idx.Delete(req.Id)

	atomic.AddInt64(&s.dirtyCounter, 1)

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Vector deleted"})
}

// Async version.
func (s *Server) handleAOFRewriteHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { /* ... */
	}

	task := s.taskManager.NewTask()
	log.Printf("Starting async AOF rewrite task with ID: %s", task.ID)

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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method to start SAVE")
		return
	}

	// Aggiungi il comando SAVE all'AOF. Questo è importante perché se il server
	// crasha durante il SAVE, al riavvio saprà che deve fidarsi dello snapshot.
	// Per ora, omettiamo questa complessità e ci concentriamo sul flusso principale.

	if err := s.Save(); err != nil {
		log.Printf("CRITICAL ERROR during SAVE via HTTP: %v", err)
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Sprintf("SAVE process failed: %v", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Database snapshot created successfully."})
}

func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the GET method")
		return
	}

	// Estrai il task ID dall'URL
	taskID := strings.TrimPrefix(r.URL.Path, "/system/tasks/")
	if taskID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, "Task ID missing in URL")
		return
	}

	// Retrieve the task from the manager.
	task, found := s.taskManager.GetTask(taskID)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found", taskID))
		return
	}

	// Read the task status safely and return it.
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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the GET method")
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
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/system/vectorizers/")
	name = strings.TrimSuffix(name, "/trigger") // Removes the suffix.

	if s.vectorizerService == nil {
		s.writeHTTPError(w, http.StatusNotFound, "VectorizerService is not active")
		return
	}

	err := s.vectorizerService.Trigger(name)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status":  "OK",
		"message": fmt.Sprintf("Synchronization for vectorizer '%s' started in the background.", name),
	})
}

// Async version.
func (s *Server) handleVectorCompress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, "Use the POST method")
		return
	}

	var req VectorCompressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	newPrecision := distance.PrecisionType(req.Precision)
	if newPrecision != distance.Float16 && newPrecision != distance.Int8 {
		s.writeHTTPError(w, http.StatusBadRequest, "Invalid precision, use 'float16' or 'int8'")
		return
	}

	// Create a new task for this operation.
	task := s.taskManager.NewTask()
	log.Printf("Starting async compression task with ID: %s", task.ID)

	// Start the heavy work in a new goroutine.
	go func() {
		// Call the existing compression logic
		err := s.db.Compress(req.IndexName, newPrecision)

		// Update the task status based on the result.
		if err != nil {
			log.Printf("Compression task %s failed: %v", task.ID, err)
			task.SetError(err)
		} else {
			log.Printf("Compression task %s completed successfully.", task.ID)
			task.SetStatus(TaskStatusCompleted)
		}
	}()

	// Immediately return a 202 Accepted response with the task ID.
	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

// Implement the final logic in handleGetVector.
func (s *Server) handleGetVector(w http.ResponseWriter, r *http.Request, indexName, vectorID string) {
	vectorData, err := s.db.GetVector(indexName, vectorID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, vectorData)
}

// --- HTTP Response Helpers ---

func (s *Server) writeHTTPResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeHTTPError(w http.ResponseWriter, statusCode int, message string) {
	s.writeHTTPResponse(w, statusCode, map[string]string{"error": message})
}

func float32SliceToString(slice []float32) string {
	var b strings.Builder
	for i, v := range slice {
		if i > 0 {
			b.WriteString(" ")
		}
		// 'f' for format, -1 for the minimum necessary precision, 32 for float32.
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	return b.String()
}

// Helper function to build the AOF command string.
func buildVAddAOFCommand(indexName, id string, vector []float32, metadata map[string]any) (string, error) {
	vectorStr := float32SliceToString(vector)

	if len(metadata) == 0 {
		return fmt.Sprintf("VADD %s %s %s\n", indexName, id, vectorStr), nil
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("VADD %s %s %s %s\n", indexName, id, vectorStr, string(metadataBytes)), nil
}
