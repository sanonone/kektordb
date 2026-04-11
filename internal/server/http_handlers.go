// Package server implements the KektorDB HTTP API and request routing.
//
// This file defines all REST endpoints for:
//   - Vector operations (create, add, search, delete, compress, import)
//   - Index management
//   - System tasks (AOF rewrite, snapshot, task monitoring, vectorizers)
//   - Debug (pprof)
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sanonone/kektordb/internal/server/ui"
	"github.com/sanonone/kektordb/pkg/auth"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/rag"
	"github.com/sanonone/kektordb/pkg/textanalyzer"
)

// registerHTTPHandlers sets up all HTTP routes using Go 1.22+ routing.
func (s *Server) registerHTTPHandlers(mux *http.ServeMux) {
	// Debug endpoints
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// System endpoints
	mux.HandleFunc("POST /system/aof-rewrite", s.handleAOFRewriteHTTP)
	mux.HandleFunc("POST /system/save", s.handleSaveHTTP)
	mux.HandleFunc("GET /system/tasks/{id}", s.handleTaskStatus)

	// Event stream (SSE)
	mux.HandleFunc("GET /events/stream", s.handleEventStream)

	// Vectorizer management
	mux.HandleFunc("GET /system/vectorizers", s.handleGetVectorizers)
	mux.HandleFunc("POST /system/vectorizers/{name}/trigger", s.handleTriggerVectorizer)

	// KV endpoints
	mux.HandleFunc("GET /kv/{key}", s.handleKVGet)
	mux.HandleFunc("POST /kv/{key}", s.handleKVSet)
	mux.HandleFunc("PUT /kv/{key}", s.handleKVSet)
	mux.HandleFunc("DELETE /kv/{key}", s.handleKVDelete)

	// Vector API - Indexes
	mux.HandleFunc("GET /vector/indexes", s.handleIndexesGet)
	mux.HandleFunc("POST /vector/indexes", s.handleVectorCreate)

	// Vector API - Actions
	// Keeping these paths for backward compatibility, though RESTful would be better.
	mux.HandleFunc("POST /vector/actions/create", s.handleVectorCreate)
	mux.HandleFunc("POST /vector/actions/add", s.handleVectorAdd)
	mux.HandleFunc("POST /vector/actions/add-batch", s.handleVectorAddBatch)
	mux.HandleFunc("POST /vector/actions/import", s.handleVectorImport)
	mux.HandleFunc("POST /vector/actions/import/commit", s.handleVectorImportCommit)
	mux.HandleFunc("POST /vector/actions/search", s.handleVectorSearch)
	mux.HandleFunc("POST /vector/actions/search-with-scores", s.handleVectorSearchWithScores)
	mux.HandleFunc("POST /vector/actions/delete_vector", s.handleVectorDelete)
	mux.HandleFunc("POST /vector/actions/compress", s.handleVectorCompress)
	mux.HandleFunc("POST /vector/actions/get-vectors", s.handleGetVectorsBatch)
	mux.HandleFunc("POST /vector/actions/reinforce", s.handleVectorReinforce)

	// Graph API
	mux.HandleFunc("POST /graph/actions/link", s.handleGraphLink)
	mux.HandleFunc("POST /graph/actions/unlink", s.handleGraphUnlink)
	mux.HandleFunc("POST /graph/actions/get-links", s.handleGraphGetLinks)
	mux.HandleFunc("POST /graph/actions/get-connections", s.handleGraphGetConnections)
	mux.HandleFunc("POST /graph/actions/traverse", s.handleGraphTraverse)
	mux.HandleFunc("POST /graph/actions/get-incoming", s.handleGraphGetIncoming)
	mux.HandleFunc("POST /graph/actions/extract-subgraph", s.handleGraphExtractSubgraph)
	mux.HandleFunc("POST /graph/actions/set-node-properties", s.handleGraphSetProperties)
	mux.HandleFunc("POST /graph/actions/get-node-properties", s.handleGraphGetProperties)
	mux.HandleFunc("POST /graph/actions/search-nodes", s.handleGraphSearchNodes)
	mux.HandleFunc("POST /graph/actions/get-edges", s.handleGraphGetEdges)
	mux.HandleFunc("POST /graph/actions/find-path", s.handleGraphFindPath)
	mux.HandleFunc("POST /graph/actions/get-all-relations", s.handleGraphGetAllRelations)
	mux.HandleFunc("POST /graph/actions/get-all-incoming", s.handleGraphGetAllIncoming)

	// Cognitive Engine API
	mux.HandleFunc("GET /vector/indexes/{name}/reflections", s.handleGetReflections)
	mux.HandleFunc("POST /vector/indexes/{name}/reflections/{id}/resolve", s.handleResolveReflection)
	mux.HandleFunc("POST /vector/indexes/{name}/cognitive/think", s.handleTriggerCognitive)

	// Session Management API
	mux.HandleFunc("POST /sessions", s.handleStartSession)
	mux.HandleFunc("POST /sessions/{id}/end", s.handleEndSession)

	// Memory Transfer API
	mux.HandleFunc("POST /transfer/memory", s.handleTransferMemory)

	mux.HandleFunc("POST /rag/retrieve", s.handleRagRetrieve)
	mux.HandleFunc("POST /rag/retrieve-adaptive", s.handleAdaptiveRagRetrieve)

	// Dynamic index routes
	mux.HandleFunc("GET /vector/indexes/{name}", s.handleSingleIndexGet)
	mux.HandleFunc("DELETE /vector/indexes/{name}", s.handleSingleIndexDelete)

	mux.HandleFunc("POST /vector/indexes/{name}/config", s.handleIndexConfig)
	mux.HandleFunc("POST /vector/indexes/{name}/maintenance", s.handleIndexMaintenance)
	mux.HandleFunc("PUT /vector/indexes/{name}/auto-links", s.handleUpdateAutoLinks)
	mux.HandleFunc("GET /vector/indexes/{name}/auto-links", s.handleGetAutoLinks)
	mux.HandleFunc("GET /vector/indexes/{name}/export", s.handleExportVectors)

	// Specific vector retrieval
	mux.HandleFunc("GET /vector/indexes/{name}/vectors/{id}", s.handleGetVector)

	// 1. UI Routes
	// Note: Go 1.22 routing uses "/ui/" for subtree matching.
	// We use http.StripPrefix because the FileServer expects the root path.
	mux.Handle("GET /ui/", http.StripPrefix("/ui/", ui.GetHandler()))

	// 2. UI Helper Endpoint (Text to Vector Search)
	mux.HandleFunc("POST /ui/search", s.handleUISearch)
	mux.HandleFunc("POST /ui/explore", s.handleUIExplore)

	// promhttp.Handler() Create a standard handler that formats data for Prometheus
	mux.Handle("GET /metrics", promhttp.Handler())

	// Auth endpoints
	mux.HandleFunc("POST /auth/keys", s.handleCreateAPIKey)
	mux.HandleFunc("GET /auth/keys", s.handleListAPIKeys)
	mux.HandleFunc("DELETE /auth/keys/{id}", s.handleRevokeAPIKey)

	// User Profile endpoints
	mux.HandleFunc("GET /users/{id}/profile", s.handleGetUserProfile)
	mux.HandleFunc("GET /users", s.handleListUserProfiles)
}

// --- Context Compression Helpers ---

// compressGraphSearchResults applies safe lexical compression to text fields
// in graph search results. This reduces token count by 20-35% for LLM context
// while preserving semantic meaning (negations and logical operators are kept).
//
// TODO: Implement result caching for repeated compression of the same content.
// Cache could use a map[contentHash]compressedContent with TTL.
func compressGraphSearchResults(results []engine.GraphSearchResult, lang string) {
	for i := range results {
		// Compress main node content
		compressMetadata(results[i].Node.Metadata, lang)

		// Compress connected nodes recursively
		for relType := range results[i].Node.Connections {
			for j := range results[i].Node.Connections[relType] {
				compressMetadata(results[i].Node.Connections[relType][j].Metadata, lang)
			}
		}
	}
}

// compressMetadata compresses text fields in metadata map in-place.
// It looks for common text field names like "content", "text", "summary".
func compressMetadata(metadata map[string]any, lang string) {
	if metadata == nil {
		return
	}

	// List of fields to compress
	textFields := []string{"content", "text", "summary", "description", "title", "label"}

	for _, field := range textFields {
		if val, ok := metadata[field].(string); ok && val != "" {
			metadata[field] = textanalyzer.Compress(val, lang)
		}
	}
}

// compressSourceAttributions compresses text in source attributions (RAG responses).
func compressSourceAttributions(sources []SourceAttribution, lang string) {
	for i := range sources {
		sources[i].Content = textanalyzer.Compress(sources[i].Content, lang)
	}
}

// compressGraphNode compresses text fields in a GraphNode and its connections recursively.
func compressGraphNode(node *engine.GraphNode, lang string) {
	if node == nil {
		return
	}
	compressMetadata(node.Metadata, lang)
	for relType := range node.Connections {
		for i := range node.Connections[relType] {
			compressGraphNode(&node.Connections[relType][i], lang)
		}
	}
}

// handleTransferMemory handles memory transfer between indexes
func (s *Server) handleTransferMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceIndex    string `json:"source_index"`
		TargetIndex    string `json:"target_index"`
		Query          string `json:"query"`
		Limit          int    `json:"limit"`
		WithGraph      bool   `json:"with_graph"`
		TransferReason string `json:"transfer_reason"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	// Validation
	if req.SourceIndex == "" || req.TargetIndex == "" || req.Query == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_index, target_index, and query are required"))
		return
	}
	if req.SourceIndex == req.TargetIndex {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source and target cannot be the same"))
		return
	}

	// Limit validation
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 500 {
		req.Limit = 500
	}

	// Check indices exist
	if !s.Engine.IndexExists(req.SourceIndex) {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("source index not found"))
		return
	}
	if !s.Engine.IndexExists(req.TargetIndex) {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("target index not found"))
		return
	}

	// Get embedder from server (need to access it)
	// For now, return not implemented - the actual transfer should be done via MCP
	// or we need to inject the embedder into the server
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"status":  "accepted",
		"message": "Use MCP tool 'transfer_memory' for actual transfer. HTTP endpoint validates parameters.",
		"params": map[string]any{
			"source_index": req.SourceIndex,
			"target_index": req.TargetIndex,
			"query":        req.Query,
			"limit":        req.Limit,
			"with_graph":   req.WithGraph,
		},
	})
}

// --- INDEX HANDLERS ---

func (s *Server) handleIndexesGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.Engine.DB.GetVectorIndexInfo()
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, info)
}

func (s *Server) handleSingleIndexGet(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	info, err := s.Engine.DB.GetSingleVectorIndexInfoAPI(indexName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err)
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
		}
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, info)
}

func (s *Server) handleSingleIndexDelete(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	// Use VDeleteIndex to ensure AOF persistence, dirty counter, and arena cleanup.
	err := s.Engine.VDeleteIndex(indexName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err)
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleIndexConfig updates the maintenance configuration for an index.
func (s *Server) handleIndexConfig(w http.ResponseWriter, r *http.Request) {
	// The method is guaranteed to be POST by the mux
	indexName := r.PathValue("name")

	var config hnsw.AutoMaintenanceConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON config: %v", err))
		return
	}

	if err := s.Engine.VUpdateIndexConfig(indexName, config); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Configuration updated"})
}

// handleIndexMaintenance starts an asynchronous maintenance task.
func (s *Server) handleIndexMaintenance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, fmt.Errorf("Use POST"))
		return
	}

	var req TriggerMaintenanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %v", err))
		return
	}

	if req.Type != "vacuum" && req.Type != "refine" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid maintenance type"))
		return
	}

	indexName := r.PathValue("name")

	task := s.taskManager.NewTask()

	go func() {
		if err := s.Engine.VTriggerMaintenance(indexName, req.Type); err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
			task.SetProgress(fmt.Sprintf("%s cycle completed", req.Type))
		}
	}()

	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

// --- KV HANDLERS ---

func (s *Server) handleKVGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("key cannot be empty"))
		return
	}
	value, found := s.Engine.KVGet(key)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("key not found"))
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"key": key, "value": string(value)})
}

func (s *Server) handleKVSet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("key cannot be empty"))
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.Engine.KVSet(key, []byte(req.Value)); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleKVDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("key cannot be empty"))
		return
	}
	if err := s.Engine.KVDelete(key); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

// --- VECTOR HANDLERS ---

func (s *Server) handleVectorCreate(w http.ResponseWriter, r *http.Request) {
	var req VectorCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
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

	err := s.Engine.VCreate(req.IndexName, metric, req.M, req.EfConstruction, prec, req.TextLanguage, req.Maintenance, req.AutoLinks, req.MemoryConfig)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Index created"})
}

func (s *Server) handleVectorAdd(w http.ResponseWriter, r *http.Request) {
	var req VectorAddRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" || req.Id == "" || len(req.Vector) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("missing fields: index_name, id, or vector"))
		return
	}

	// Validate vector dimension against index configuration
	idx, ok := s.Engine.DB.GetVectorIndex(req.IndexName)
	if !ok {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index '%s' not found", req.IndexName))
		return
	}

	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		expectedDim := hnswIdx.GetDimension()
		if expectedDim > 0 && len(req.Vector) != expectedDim {
			s.writeHTTPError(w, http.StatusBadRequest,
				fmt.Errorf("vector dimension mismatch: expected %d, got %d", expectedDim, len(req.Vector)))
			return
		}
	}

	err := s.Engine.VAdd(req.IndexName, req.Id, req.Vector, req.Metadata)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err)
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
		}
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleVectorAddBatch(w http.ResponseWriter, r *http.Request) {
	var req BatchAddVectorsRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	err := s.Engine.VAddBatch(req.IndexName, req.Vectors)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "OK",
		"vectors_added": len(req.Vectors),
	})
}

func (s *Server) handleVectorImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeHTTPError(w, http.StatusMethodNotAllowed, fmt.Errorf("Use POST"))
		return
	}
	var req BatchAddVectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}
	if req.IndexName == "" || len(req.Vectors) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Missing index_name or vectors"))
		return
	}

	if err := s.Engine.VImport(req.IndexName, req.Vectors); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]interface{}{
		"status":        "OK",
		"vectors_added": len(req.Vectors),
	})
}

func (s *Server) handleVectorImportCommit(w http.ResponseWriter, r *http.Request) {
	var req VectorImportCommitRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	// Questa è un'operazione "pesante" (salva su disco), ma vogliamo aspettare che finisca
	// prima di dire "OK" all'utente, per garantire la durabilità dei dati.
	if err := s.Engine.VImportCommit(req.IndexName); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status":  "OK",
		"message": "Import committed to disk. Turbo Refine started in background.",
	})
}

func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	var req VectorSearchRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	// FILTER-ONLY: If QueryVector is empty/missing and filter is provided
	isQueryVectorEmpty := len(req.QueryVector) == 0
	if isQueryVectorEmpty && req.Filter != "" {
		ids, err := s.Engine.VFilter(req.IndexName, req.Filter, req.K)
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
			return
		}
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": ids})
		return
	}

	if len(req.IncludeRelations) > 0 {
		// --- GRAPH SEARCH ---
		results, err := s.Engine.VSearchGraph(
			req.IndexName,
			req.QueryVector,
			req.K,
			req.Filter,
			"",
			req.EfSearch,
			req.Alpha,
			req.IncludeRelations,
			req.HydrateRelations,
			req.GraphFilter,
		)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				s.writeHTTPError(w, http.StatusNotFound, err)
			} else {
				s.writeHTTPError(w, http.StatusInternalServerError, err)
			}
			return
		}

		// Apply compression if requested
		if req.CompressContext {
			lang := s.Engine.GetIndexLanguage(req.IndexName)
			compressGraphSearchResults(results, lang)
		}

		// Return the rich object
		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": results})

	} else {
		// --- STANDARD SEARCH ---
		// Identical to above, returns only strings for compatibility
		ids, err := s.Engine.VSearch(
			req.IndexName,
			req.QueryVector,
			req.K,
			req.Filter,
			"",
			req.EfSearch,
			req.Alpha,
			req.GraphFilter,
		)

		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				s.writeHTTPError(w, http.StatusNotFound, err)
			} else {
				s.writeHTTPError(w, http.StatusInternalServerError, err)
			}
			return
		}

		s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": ids})
	}
}

func (s *Server) handleVectorSearchWithScores(w http.ResponseWriter, r *http.Request) {
	var req VectorSearchWithScoresRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	results, err := s.Engine.VSearchWithScores(req.IndexName, req.QueryVector, req.K)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err)
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
		}
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleVectorDelete(w http.ResponseWriter, r *http.Request) {
	var req VectorDeleteRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" || req.Id == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and id required"))
		return
	}

	if err := s.Engine.VDelete(req.IndexName, req.Id); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Server) handleVectorCompress(w http.ResponseWriter, r *http.Request) {
	var req VectorCompressRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	prec := distance.PrecisionType(req.Precision)

	task := s.taskManager.NewTask()
	go func() {
		err := s.Engine.DB.Compress(req.IndexName, prec)
		if err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
		}
	}()

	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

func (s *Server) handleGetVectorsBatch(w http.ResponseWriter, r *http.Request) {
	var req BatchGetVectorsRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	data, err := s.Engine.DB.GetVectors(req.IndexName, req.IDs)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply compression if requested
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		for i := range data {
			compressMetadata(data[i].Metadata, lang)
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, data)
}

func (s *Server) handleVectorReinforce(w http.ResponseWriter, r *http.Request) {
	var req VectorReinforceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.Engine.VReinforce(req.IndexName, req.IDs); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "reinforced"})
}

func (s *Server) handleGetVector(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	vectorID := r.PathValue("id")
	if indexName == "" || vectorID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name and vector id required"))
		return
	}

	data, err := s.Engine.DB.GetVector(indexName, vectorID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusNotFound, err)
		} else {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
		}
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, data)
}

// --- GRAPH HANDLERS ---

func (s *Server) handleGraphLink(w http.ResponseWriter, r *http.Request) {
	var req GraphLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" || req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name, source_id, target_id, and relation_type are required"))
		return
	}

	// Default weight logic
	weight := req.Weight
	if weight == 0 {
		weight = 1.0
	}

	if err := s.Engine.VLink(req.IndexName, req.SourceID, req.TargetID, req.RelationType, req.InverseRelationType, weight, req.Props); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("Error in Vlink: %v", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Link created"})
}

func (s *Server) handleGraphUnlink(w http.ResponseWriter, r *http.Request) {
	var req GraphUnlinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" || req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name, source_id, target_id, and relation_type are required"))
		return
	}

	if err := s.Engine.VUnlink(req.IndexName, req.SourceID, req.TargetID, req.RelationType, req.InverseRelationType, req.HardDelete); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("Error in Unlink: %v", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Link removed"})
}

func (s *Server) handleGraphGetLinks(w http.ResponseWriter, r *http.Request) {
	var req GraphGetLinksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_id and relation_type are required"))
		return
	}

	targets, found := s.Engine.VGetLinks(req.IndexName, req.SourceID, req.RelationType)
	if !found {
		// We return an empty list instead of 404 to facilitate the client
		targets = []string{}
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"source_id":     req.SourceID,
		"relation_type": req.RelationType,
		"targets":       targets,
	})
}

func (s *Server) handleGraphGetConnections(w http.ResponseWriter, r *http.Request) {
	var req GraphGetConnectionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name, source_id, and relation_type are required"))
		return
	}

	results, err := s.Engine.VGetConnections(req.IndexName, req.SourceID, req.RelationType)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("Error VGetConnection"))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"results": results,
	})
}

// Implementazione Handler:
func (s *Server) handleGraphTraverse(w http.ResponseWriter, r *http.Request) {
	var req GraphTraverseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and source_id required"))
		return
	}

	result, err := s.Engine.VTraverse(req.IndexName, req.SourceID, req.Paths)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("VTraverse error"))
		return
	}

	// Apply compression if requested
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		compressGraphNode(result, lang)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"result": result,
	})
}

func (s *Server) handleGraphGetIncoming(w http.ResponseWriter, r *http.Request) {
	var req GraphGetIncomingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("target_id and relation_type are required"))
		return
	}

	sources, found := s.Engine.VGetIncoming(req.IndexName, req.TargetID, req.RelationType)
	if !found {
		sources = []string{}
	}

	s.writeHTTPResponse(w, http.StatusOK, GraphGetIncomingResponse{
		TargetID:     req.TargetID,
		RelationType: req.RelationType,
		Sources:      sources,
	})
}

func (s *Server) handleGraphExtractSubgraph(w http.ResponseWriter, r *http.Request) {
	var req GraphExtractSubgraphRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.RootID == "" || req.IndexName == "" || len(req.Relations) == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("root_id, index_name and relations list are required"))
		return
	}

	result, err := s.Engine.VExtractSubgraph(req.IndexName, req.RootID, req.Relations, req.MaxDepth, req.AtTime, req.GuideVector, req.SemanticThreshold)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply compression if requested
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		for i := range result.Nodes {
			compressMetadata(result.Nodes[i].Metadata, lang)
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, result)
}

func (s *Server) handleGraphSetProperties(w http.ResponseWriter, r *http.Request) {
	var req GraphSetPropertiesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}
	if req.IndexName == "" || req.NodeID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and node_id required"))
		return
	}

	// Check if node exists
	data, err := s.Engine.VGet(req.IndexName, req.NodeID)
	if err != nil {
		// If error is NOT "not found", return error
		if !strings.Contains(err.Error(), "not found") {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("failed to get node: %w", err))
			return
		}
		// Node doesn't exist - create it with a zero-vector
		var vec []float32
		if idx, ok := s.Engine.DB.GetVectorIndex(req.IndexName); ok {
			if hnswIdx, ok := idx.(*hnsw.Index); ok {
				dim := hnswIdx.GetDimension()
				if dim > 0 {
					vec = make([]float32, dim)
				}
			}
		}

		err = s.Engine.VAdd(req.IndexName, req.NodeID, vec, req.Properties)
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
			return
		}

		s.writeHTTPResponse(w, http.StatusOK, map[string]any{
			"node_id":    req.NodeID,
			"properties": req.Properties,
			"status":     "created",
		})
		return
	}

	// Node exists - update metadata
	merged := make(map[string]any)
	for k, v := range data.Metadata {
		merged[k] = v
	}
	for k, v := range req.Properties {
		merged[k] = v
	}
	if err := s.Engine.VSetMetadata(req.IndexName, req.NodeID, merged); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"node_id":    req.NodeID,
		"properties": merged,
		"status":     "updated",
	})
}

func (s *Server) handleGraphGetProperties(w http.ResponseWriter, r *http.Request) {
	var req GraphGetPropertiesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	// Use VGet to retrieve metadata
	data, err := s.Engine.VGet(req.IndexName, req.NodeID)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err)
		return
	}

	// Apply compression if requested
	metadata := data.Metadata
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		// Create a copy to avoid modifying the original
		metadata = make(map[string]any, len(data.Metadata))
		for k, v := range data.Metadata {
			metadata[k] = v
		}
		compressMetadata(metadata, lang)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"node_id":    data.ID,
		"properties": metadata,
	})
}

func (s *Server) handleGraphSearchNodes(w http.ResponseWriter, r *http.Request) {
	var req GraphSearchNodesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}
	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}

	// "Search Nodes" is a Vector Search with NO query vector (only filter).
	// Currently VSearch requires a query vector.
	// We can pass a zero-vector if we knew the dimension, OR we can implement a pure-filter search.
	// Since VSearch implementation supports "Text Only" (Alpha=0), let's try to leverage that or add logic.

	// OPTION A: Add a specialized method in Engine for "Filter Only".
	// OPTION B: Use VSearch with a dummy vector.

	// Let's use Option B for reuse, fetching dimension first.
	// This is a bit inefficient but safe for this sprint.

	// 1. Get dimension
	idx, ok := s.Engine.DB.GetVectorIndex(req.IndexName)
	if !ok {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index not found"))
		return
	}

	var dim int
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		dim = hnswIdx.GetDimension()
	}
	if dim == 0 {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index empty or invalid"))
		return
	}

	dummyQuery := make([]float32, dim) // Zero vector

	// Execute Search
	// We use Alpha=0.0 to rely mostly on text/filter?
	// Actually if vector is 0, distance will be constant or 0.
	// The filter is a hard constraint (allowList).

	results, err := s.Engine.VSearch(
		req.IndexName,
		dummyQuery,
		req.Limit,
		req.PropertyFilter,
		"", // no text query
		0,
		1.0, // Alpha 1.0 (Vector) - but vector is 0. Filter does the job.
		nil,
	)

	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// Hydrate results (we need properties back)
	fullData, err := s.Engine.VGetMany(req.IndexName, results)

	// Format response to hide the vector part (we only want node properties)
	var nodes []map[string]any
	for _, item := range fullData {
		metadata := item.Metadata
		// Apply compression if requested
		if req.CompressContext {
			lang := s.Engine.GetIndexLanguage(req.IndexName)
			// Create a copy to avoid modifying original
			metadata = make(map[string]any, len(item.Metadata))
			for k, v := range item.Metadata {
				metadata[k] = v
			}
			compressMetadata(metadata, lang)
		}
		nodes = append(nodes, map[string]any{
			"id":         item.ID,
			"properties": metadata,
		})
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"nodes": nodes,
	})
}

func (s *Server) handleGraphGetEdges(w http.ResponseWriter, r *http.Request) {
	var req GraphGetEdgesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and relation_type required"))
		return
	}

	var edges []engine.GraphEdge
	var found bool

	// Default direction is Out (Forward)
	if req.Direction == "in" {
		if req.TargetID == "" {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("target_id required for 'in' direction"))
			return
		}
		edges, found = s.Engine.VGetIncomingEdges(req.IndexName, req.TargetID, req.RelationType, req.AtTime)
	} else {
		// Out / Forward
		if req.SourceID == "" {
			s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_id required for 'out' direction"))
			return
		}
		edges, found = s.Engine.VGetEdges(req.IndexName, req.SourceID, req.RelationType, req.AtTime)
	}

	if !found {
		edges = []engine.GraphEdge{}
	}

	s.writeHTTPResponse(w, http.StatusOK, GraphGetEdgesResponse{Edges: edges})
}

func (s *Server) handleGraphFindPath(w http.ResponseWriter, r *http.Request) {
	var req GraphFindPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.SourceID == "" || req.TargetID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name, source_id and target_id are required"))
		return
	}

	// Let's enforce it to be explicit, OR provide a very broad default list
	if len(req.Relations) == 0 {
		// Fallback to standard set if user is lazy, similar to Explore
		req.Relations = []string{"related_to", "mentions", "parent", "child", "next", "prev"}
	}

	// Call Engine logic
	result, err := s.Engine.FindPath(req.IndexName, req.SourceID, req.TargetID, req.Relations, req.MaxDepth, req.AtTime)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	if result == nil {
		// Path not found is not a 500 error, just empty result or 404
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("path not found"))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, result)
}

// handleGraphGetAllRelations returns all outgoing links from a node, grouped by relation type.
func (s *Server) handleGraphGetAllRelations(w http.ResponseWriter, r *http.Request) {
	var req GraphGetAllRelationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.NodeID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and node_id are required"))
		return
	}
	relations := s.Engine.VGetRelations(req.IndexName, req.NodeID)

	if relations == nil {
		// Ritorniamo una mappa vuota invece di null per comodità dei client
		relations = make(map[string][]string)
	}

	s.writeHTTPResponse(w, http.StatusOK, GraphGetAllRelationsResponse{
		NodeID:    req.NodeID,
		Relations: relations,
	})
}

// handleGraphGetAllIncoming returns all incoming links to a node, grouped by relation type.
func (s *Server) handleGraphGetAllIncoming(w http.ResponseWriter, r *http.Request) {
	var req GraphGetAllRelationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.NodeID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and node_id are required"))
		return
	}
	relations := s.Engine.VGetIncomingRelations(req.IndexName, req.NodeID)

	if relations == nil {
		relations = make(map[string][]string)
	}

	s.writeHTTPResponse(w, http.StatusOK, GraphGetAllRelationsResponse{
		NodeID:    req.NodeID,
		Relations: relations,
	})
}

// --- COGNITIVE ENGINE HANDLERS ---

// Valid reflection statuses to prevent filter injection attacks
var validReflectionStatuses = map[string]bool{
	"unresolved":      true,
	"insight":         true,
	"active":          true,
	"high_confidence": true,
	"resolved":        true,
}

func (s *Server) handleGetReflections(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	// Permettiamo di filtrare per status tramite query string (es. ?status=unresolved)
	status := r.URL.Query().Get("status")

	// Validate status to prevent filter injection attacks
	if status != "" && !validReflectionStatuses[status] {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid status value: %s", status))
		return
	}

	// Sfruttiamo le Roaring Bitmaps e il VFilter!
	filter := "type='reflection' OR type='user_profile_insight' OR type='failure_pattern' OR type='knowledge_evolution'"
	if status != "" {
		filter += fmt.Sprintf(" AND status='%s'", status)
	}

	// Limit opzionale
	limit := 50 // Default

	reflectionIDs, err := s.Engine.VFilter(indexName, filter, limit)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	if len(reflectionIDs) == 0 {
		s.writeHTTPResponse(w, http.StatusOK, GetReflectionsResponse{Reflections: []ReflectionItem{}})
		return
	}

	// Fetch parallelo dei dati (Hydration)
	data, _ := s.Engine.VGetMany(indexName, reflectionIDs)
	var items []ReflectionItem

	for _, item := range data {
		items = append(items, ReflectionItem{
			ID:       item.ID,
			Metadata: item.Metadata,
		})
	}

	s.writeHTTPResponse(w, http.StatusOK, GetReflectionsResponse{Reflections: items})
}

func (s *Server) handleResolveReflection(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	reflectionID := r.PathValue("id")

	if indexName == "" || reflectionID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and reflection id required"))
		return
	}

	var req ResolveReflectionRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	// 1. Aggiorna lo stato della Reflection
	updateProps := map[string]any{
		"status":      "resolved",
		"resolution":  req.Resolution,
		"_updated_at": float64(time.Now().Unix()),
	}

	if err := s.Engine.VSetMetadata(indexName, reflectionID, updateProps); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// 2. Se l'utente umano/dashboard ha specificato un ID da scartare, lo archiviamo
	if req.DiscardID != "" {
		discardProps := map[string]any{
			"_archived":      true,
			"invalidated_by": reflectionID,
		}
		_ = s.Engine.VSetMetadata(indexName, req.DiscardID, discardProps)
		_ = s.Engine.VDelete(indexName, req.DiscardID)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status": "resolved",
		"id":     reflectionID,
	})
}

func (s *Server) handleTriggerCognitive(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}

	if s.gardener == nil {
		s.writeHTTPError(w, http.StatusServiceUnavailable, fmt.Errorf("cognitive engine is disabled on this server"))
		return
	}

	// Eseguiamo il ciclo in background per non bloccare la risposta HTTP
	go s.gardener.ForceThink(indexName)

	s.writeHTTPResponse(w, http.StatusAccepted, map[string]string{
		"status":  "accepted",
		"message": "Cognitive reflection cycle triggered in background.",
	})
}

// handleStartSession creates a new session entity.
func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id,omitempty"`
		AgentID   string `json:"agent_id,omitempty"`
		UserID    string `json:"user_id,omitempty"`
		Context   string `json:"context,omitempty"`
		IndexName string `json:"index_name,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	idx := req.IndexName
	if idx == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name is required"))
		return
	}

	// Ensure index exists
	if !s.Engine.IndexExists(idx) {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index '%s' not found", idx))
		return
	}

	// Generate session ID if not provided
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session::%d", time.Now().UnixNano())
	}

	// Create session entity metadata
	meta := map[string]any{
		"type":           "session",
		"session_status": "active",
		"started_at":     time.Now().Format(time.RFC3339),
	}
	if req.AgentID != "" {
		meta["agent_id"] = req.AgentID
	}
	if req.UserID != "" {
		meta["user_id"] = req.UserID
	}
	if req.Context != "" {
		meta["context"] = req.Context
	}

	// Try zero-vector insert. VAdd will auto-create a zero-vector matching
	// the index dimension. If the index is empty (dimension unknown), return
	// a clear error instead of 500.
	if err := s.Engine.VAdd(idx, sessionID, nil, meta); err != nil {
		if err.Error() == "cannot add entity without vector to an empty index (dimension unknown)" {
			s.writeHTTPError(w, http.StatusBadRequest,
				fmt.Errorf("cannot create session on an empty index: add at least one vector first to establish dimension"))
			return
		}
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("failed to create session: %w", err))
		return
	}

	s.writeHTTPResponse(w, http.StatusCreated, map[string]string{
		"session_id": sessionID,
		"status":     "active",
		"message":    "Session started successfully",
	})
}

// handleEndSession closes a session and triggers Gardener summarization.
func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("session id is required"))
		return
	}

	var req struct {
		IndexName string `json:"index_name,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Body is optional, but index_name will be checked below
		req.IndexName = ""
	}

	idx := req.IndexName
	if idx == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name is required in request body"))
		return
	}

	// Verify session exists
	data, err := s.Engine.VGet(idx, sessionID)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("session not found: %w", err))
		return
	}

	// Update session status to ended
	updateProps := map[string]any{
		"session_status": "ended",
		"ended_at":       time.Now().Format(time.RFC3339),
	}

	// Preserve existing metadata
	for k, v := range data.Metadata {
		if _, exists := updateProps[k]; !exists {
			updateProps[k] = v
		}
	}

	if err := s.Engine.VSetMetadata(idx, sessionID, updateProps); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("failed to end session: %w", err))
		return
	}

	// Trigger Gardener summarization in background if available
	if s.gardener != nil {
		go func() {
			if err := s.gardener.SummarizeSession(idx, sessionID); err != nil {
				slog.Warn("[Session] Gardener summarization failed", "error", err, "session", sessionID)
			}
		}()
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"session_id": sessionID,
		"status":     "ended",
		"message":    "Session ended. Summarization triggered in background.",
	})
}

// handleRagRetrieve performs a semantic search using a configured pipeline.
func (s *Server) handleRagRetrieve(w http.ResponseWriter, r *http.Request) {
	var req RagRetrieveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.Query == "" || req.PipelineName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("pipeline_name and query are required"))
		return
	}
	if req.K <= 0 {
		req.K = 3
	}

	// Find the correct pipeline in the VectorizerService
	pipeline := s.vectorizerService.GetPipeline(req.PipelineName)
	if pipeline == nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("pipeline '%s' not found", req.PipelineName))
		return
	}

	// NEW: If provenance requested, use RetrieveWithSources
	if req.IncludeProvenance {
		chunks, err := pipeline.RetrieveWithSources(req.Query, req.K)
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("retrieval failed: %w", err))
			return
		}

		// Build source attributions
		sources := make([]SourceAttribution, 0, len(chunks))
		var provService *ProvenanceService
		if s.Engine != nil {
			provService = NewProvenanceService(s.Engine, pipeline.GetIndexName())
		}

		for _, chunk := range chunks {
			source := SourceAttribution{
				ChunkID:    chunk.ID,
				Content:    chunk.Content,
				Relevance:  chunk.Score,
				GraphDepth: 0,
			}

			// Extract metadata
			source.SourceFile, source.Filename, source.ChunkIndex, source.PageNumber =
				ExtractSourceMetadata(chunk.Metadata)

			if parentID, ok := chunk.Metadata["parent_id"].(string); ok {
				source.DocumentID = parentID
			}

			// Calculate provenance path if service available
			if provService != nil && source.DocumentID != "" {
				if path, verified := provService.BuildGraphPath(chunk.ID, source.DocumentID); path != nil {
					source.GraphPath = *path
					source.Verified = verified
				}
			}

			sources = append(sources, source)
		}

		// Calculate confidence
		confidence := CalculateConfidence(sources)

		// Apply compression if requested
		if req.CompressContext {
			// Get language from pipeline's index
			lang := ""
			if s.Engine != nil {
				lang = s.Engine.GetIndexLanguage(pipeline.GetIndexName())
			}
			compressSourceAttributions(sources, lang)
		}

		// Build response text (after compression if applied)
		var parts []string
		for _, s := range sources {
			parts = append(parts, s.Content)
		}

		response := RagRetrieveResponse{
			Results:     parts, // Legacy compatibility
			Response:    strings.Join(parts, "\n\n---\n\n"),
			Sources:     sources,
			Confidence:  confidence,
			TotalTokens: EstimateTokens(parts, 4.0),
			Provenance:  true,
		}

		s.writeHTTPResponse(w, http.StatusOK, response)
		return
	}

	// Execute standard retrieval (legacy behavior)
	texts, err := pipeline.Retrieve(req.Query, req.K)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("error while retrieving"))
		return
	}

	// Apply compression if requested (legacy path)
	if req.CompressContext {
		lang := ""
		if s.Engine != nil {
			lang = s.Engine.GetIndexLanguage(pipeline.GetIndexName())
		}
		for i := range texts {
			texts[i] = textanalyzer.Compress(texts[i], lang)
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"results": texts,
	})
}

// handleAdaptiveRagRetrieve performs adaptive context retrieval using graph expansion.
// It retrieves seed chunks via semantic search, expands following graph relations,
// and assembles a context window respecting the token budget.
func (s *Server) handleAdaptiveRagRetrieve(w http.ResponseWriter, r *http.Request) {
	var req RagAdaptiveRetrieveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	// Validation
	if req.Query == "" || req.PipelineName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("pipeline_name and query are required"))
		return
	}
	if req.K <= 0 {
		req.K = 5
	}

	// Get pipeline
	pipeline := s.vectorizerService.GetPipeline(req.PipelineName)
	if pipeline == nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("pipeline '%s' not found", req.PipelineName))
		return
	}

	// Build config with defaults
	config := rag.DefaultAdaptiveConfig()
	if req.MaxTokens > 0 {
		config.MaxTokens = req.MaxTokens
	}
	if req.Strategy != "" {
		config.ExpansionStrategy = req.Strategy
	}
	if req.ExpansionDepth > 0 {
		config.GraphExpansionDepth = req.ExpansionDepth
	}
	if req.SemanticWeight > 0 {
		config.SemanticWeight = req.SemanticWeight
	}
	if req.GraphWeight > 0 {
		config.GraphWeight = req.GraphWeight
	}
	if req.DensityWeight > 0 {
		config.DensityWeight = req.DensityWeight
	}
	if req.CharsPerToken > 0 {
		config.CharsPerToken = req.CharsPerToken
	}

	// Execute adaptive retrieval
	window, err := pipeline.RetrieveAdaptive(req.Query, req.K, config)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("adaptive retrieval failed: %w", err))
		return
	}

	// NEW: Build source attributions if provenance requested
	var sources []SourceAttribution
	if req.IncludeProvenance {
		var provService *ProvenanceService
		if s.Engine != nil {
			provService = NewProvenanceService(s.Engine, pipeline.GetIndexName())
		}

		sources = make([]SourceAttribution, 0, len(window.Chunks))
		for _, chunk := range window.Chunks {
			source := SourceAttribution{
				ChunkID:    chunk.ID,
				GraphDepth: 0, // Will be set from metadata if available
			}

			// Extract content
			if val, ok := chunk.Metadata["content"]; ok {
				source.Content = fmt.Sprintf("%v", val)
			}

			// Extract metadata
			source.SourceFile, source.Filename, source.ChunkIndex, source.PageNumber =
				ExtractSourceMetadata(chunk.Metadata)

			if parentID, ok := chunk.Metadata["parent_id"].(string); ok {
				source.DocumentID = parentID
			}

			// Try to get depth from metadata (set by adaptive retriever)
			if depth, ok := chunk.Metadata["_depth"].(float64); ok {
				source.GraphDepth = int(depth)
			}

			// Calculate provenance path if service available
			if provService != nil && source.DocumentID != "" {
				if path, verified := provService.BuildGraphPath(chunk.ID, source.DocumentID); path != nil {
					source.GraphPath = *path
					source.Verified = verified
				}
			}

			sources = append(sources, source)
		}
	}

	// Apply compression if requested
	if req.CompressContext {
		lang := ""
		if s.Engine != nil {
			lang = s.Engine.GetIndexLanguage(pipeline.GetIndexName())
		}
		// Compress context text
		window.ContextText = textanalyzer.Compress(window.ContextText, lang)
		// Compress sources
		compressSourceAttributions(sources, lang)
	}

	// Format response
	response := RagAdaptiveRetrieveResponse{
		ContextText:   window.ContextText,
		ChunksUsed:    window.TotalChunks,
		TotalTokens:   window.TotalTokens,
		DocumentsUsed: window.DocumentsUsed,
		Sources:       sources,
		Provenance:    req.IncludeProvenance,
		ExpansionStats: struct {
			SeedChunks     int `json:"seed_chunks"`
			ExpandedChunks int `json:"expanded_chunks"`
			TotalEvaluated int `json:"total_evaluated"`
		}{
			SeedChunks:     window.Stats.SeedChunks,
			ExpandedChunks: window.Stats.ExpandedChunks,
			TotalEvaluated: window.Stats.TotalEvaluated,
		},
	}

	s.writeHTTPResponse(w, http.StatusOK, response)
}

// handleGetUserProfile returns a user's personality profile
func (s *Server) handleGetUserProfile(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if userID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("user id is required"))
		return
	}

	// Get index_name from query parameter (required)
	idx := r.URL.Query().Get("index_name")
	if idx == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name query parameter is required"))
		return
	}

	// If index doesn't exist, return 404 for the profile (not 500)
	if !s.Engine.IndexExists(idx) {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("profile not found for user %s", userID))
		return
	}

	profileID := fmt.Sprintf("_profile::%s", userID)
	data, err := s.Engine.VGet(idx, profileID)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("profile not found for user %s", userID))
		return
	}

	// Parse expertise areas and dislikes from comma-separated strings
	var expertiseAreas, dislikes []string
	if areas, ok := data.Metadata["expertise_areas"].(string); ok && areas != "" {
		expertiseAreas = strings.Split(areas, ",")
	}
	if d, ok := data.Metadata["dislikes"].(string); ok && d != "" {
		dislikes = strings.Split(d, ",")
	}

	response := UserProfileResponse{
		UserID:             userID,
		CommunicationStyle: getString(data.Metadata, "communication_style"),
		Language:           getString(data.Metadata, "language"),
		ExpertiseAreas:     expertiseAreas,
		Dislikes:           dislikes,
		ResponseLength:     getString(data.Metadata, "response_length"),
		Confidence:         getFloat(data.Metadata, "confidence"),
		LastUpdated:        int64(getFloat(data.Metadata, "last_updated")),
		ProfileData:        getString(data.Metadata, "profile_data"),
	}

	s.writeHTTPResponse(w, http.StatusOK, response)
}

// handleListUserProfiles lists all user profiles in the database
func (s *Server) handleListUserProfiles(w http.ResponseWriter, r *http.Request) {
	// Get index_name from query parameter (required)
	idx := r.URL.Query().Get("index_name")
	if idx == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name query parameter is required"))
		return
	}

	// If index doesn't exist, return empty list (not 500)
	if !s.Engine.IndexExists(idx) {
		s.writeHTTPResponse(w, http.StatusOK, UserProfileListResponse{
			Profiles: []UserProfileItem{},
			Count:    0,
		})
		return
	}

	// Filter for user_profile type
	ids, err := s.Engine.VFilter(idx, "type='user_profile'", 100)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("failed to list profiles: %w", err))
		return
	}

	profiles := make([]UserProfileItem, 0, len(ids))
	for _, id := range ids {
		data, err := s.Engine.VGet(idx, id)
		if err != nil || data.ID == "" {
			continue
		}

		userID := strings.TrimPrefix(id, "_profile::")
		profiles = append(profiles, UserProfileItem{
			UserID:             userID,
			CommunicationStyle: getString(data.Metadata, "communication_style"),
			Confidence:         getFloat(data.Metadata, "confidence"),
			LastUpdated:        int64(getFloat(data.Metadata, "last_updated")),
		})
	}

	response := UserProfileListResponse{
		Profiles: profiles,
		Count:    len(profiles),
	}

	s.writeHTTPResponse(w, http.StatusOK, response)
}

// --- SYSTEM HANDLERS ---

func (s *Server) handleSaveHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.Engine.SaveSnapshot(); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Snapshot created"})
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	if s.Engine == nil || s.Engine.EventBus == nil {
		http.Error(w, "Event bus not available", http.StatusInternalServerError)
		return
	}

	ch := s.Engine.EventBus.Subscribe(128)
	defer s.Engine.EventBus.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
			flusher.Flush()
		}
	}
}

func (s *Server) handleAOFRewriteHTTP(w http.ResponseWriter, r *http.Request) {
	task := s.taskManager.NewTask()
	go func() {
		if err := s.Engine.RewriteAOF(); err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
		}
	}()
	s.writeHTTPResponse(w, http.StatusAccepted, task)
}

// --- SYSTEM & VECTORIZER HANDLERS ---

func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if taskID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("task ID missing"))
		return
	}

	task, found := s.taskManager.GetTask(taskID)
	if !found {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("task not found"))
		return
	}

	task.mu.RLock()
	defer task.mu.RUnlock()
	s.writeHTTPResponse(w, http.StatusOK, task)
}

func (s *Server) handleGetVectorizers(w http.ResponseWriter, r *http.Request) {
	if s.vectorizerService == nil {
		s.writeHTTPResponse(w, http.StatusOK, []interface{}{})
		return
	}
	statuses := s.vectorizerService.GetStatuses()
	s.writeHTTPResponse(w, http.StatusOK, statuses)
}

func (s *Server) handleTriggerVectorizer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("vectorizer name missing"))
		return
	}

	if s.vectorizerService == nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("VectorizerService not active"))
		return
	}

	err := s.vectorizerService.Trigger(name)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{
		"status":  "OK",
		"message": fmt.Sprintf("Synchronization for vectorizer '%s' triggered.", name),
	})
}

// --- UI HANDLERS ---
// UISearchRequest defines the specific payload for the dashboard.
type UISearchRequest struct {
	IndexName        string   `json:"index_name"`
	Query            string   `json:"query"`
	K                int      `json:"k"`
	IncludeRelations []string `json:"include_relations"`
	Hydrate          bool     `json:"hydrate"`
	CompressContext  bool     `json:"compress_context,omitempty"` // NEW: Enable safe lexical compression
}

// handleUISearch bridges the gap between text query and vector search for the UI.
// It looks up the correct embedder from the VectorizerService.
func (s *Server) handleUISearch(w http.ResponseWriter, r *http.Request) {
	var req UISearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" || req.Query == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name and query required"))
		return
	}

	// 1. Resolve Embedder
	// We need to find a pipeline that targets this index to use its embedder.
	// If multiple pipelines target the same index, any will do for embedding purposes.
	var embedder embeddings.Embedder

	// We iterate through running pipelines to find one matching the index
	if s.vectorizerService != nil {
		// Accessing pipelines via a new method we need to add to VectorizerService
		// OR simply iterating if we expose the list.
		// Ideally VectorizerService should have a method `GetEmbedderForIndex(indexName)`.
		// For now, let's assume we add that helper.
		embedder = s.vectorizerService.GetEmbedderForIndex(req.IndexName)
	}

	var queryVec []float32
	var err error

	if embedder != nil {
		// 2a. Embed the query
		queryVec, err = embedder.Embed(req.Query)
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("embedding failed: %v", err))
			return
		}
	} else {
		// 2b. Fallback: Check if the request manually provided a vector?
		// The UI assumes text. If no embedder is found (e.g. index created manually via API),
		// we cannot search by text.
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("no embedder configured for index '%s'. Cannot perform text search.", req.IndexName))
		return
	}

	// 3. Execute Graph Search
	// We reuse the powerful VSearchGraph method from the Engine
	results, err := s.Engine.VSearchGraph(
		req.IndexName,
		queryVec,
		req.K,
		"", // No filter for simple UI
		"",
		0,   // Default ef
		0.5, // Default alpha
		req.IncludeRelations,
		req.Hydrate,
		nil,
	)

	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply compression if requested
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		compressGraphSearchResults(results, lang)
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUIExplore(w http.ResponseWriter, r *http.Request) {
	var req UIExploreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}

	if req.IndexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index_name required"))
		return
	}
	// If limit is 0, set default.
	// We allow up to 10,000 nodes. It's up to the client (browser) to handle the load.
	if req.Limit <= 0 {
		req.Limit = 200 // Default safe value
	}
	if req.Limit > 10000 {
		req.Limit = 10000 // Server-side safety cap
	}

	idx, ok := s.Engine.DB.GetVectorIndex(req.IndexName)
	if !ok {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index not found"))
		return
	}

	// Usiamo HNSW per iterare velocemente
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("not hnsw index"))
		return
	}

	// FIX 1: Usa engine.GraphNode invece di GraphNode
	var nodes []engine.GraphNode
	count := 0

	// 1. Iteriamo sui nodi dell'indice
	hnswIdx.IterateRaw(func(id string, _ interface{}) {
		if count >= req.Limit {
			return // Stop
		}

		// Recuperiamo i dati completi
		vData, err := s.Engine.VGet(req.IndexName, id)
		if err != nil {
			return
		}

		// FIX 2: Usa engine.GraphNode
		gNode := engine.GraphNode{VectorData: vData}
		gNode.Connections = make(map[string][]engine.GraphNode)

		relationsToCheck := []string{"next", "prev", "parent", "child", "mentions", "mentioned_in"}

		// FIX 3: Rimossa variabile inutilizzata 'hasRelations'

		for _, rel := range relationsToCheck {
			targetIDs, found := s.Engine.VGetLinks(req.IndexName, id, rel)
			if found && len(targetIDs) > 0 {

				// FIX 4: Usa engine.GraphNode
				var children []engine.GraphNode
				for _, tid := range targetIDs {
					// Fetch light metadata for target to display label
					tData, _ := s.Engine.VGet(req.IndexName, tid)
					if tData.ID == "" {
						tData.ID = tid
					} // Fallback if not found
					children = append(children, engine.GraphNode{VectorData: tData})
				}
				gNode.Connections[rel] = children
			}
		}

		nodes = append(nodes, gNode)
		count++
	})

	// Apply compression if requested
	if req.CompressContext {
		lang := s.Engine.GetIndexLanguage(req.IndexName)
		for i := range nodes {
			compressMetadata(nodes[i].Metadata, lang)
			for relType := range nodes[i].Connections {
				for j := range nodes[i].Connections[relType] {
					compressMetadata(nodes[i].Connections[relType][j].Metadata, lang)
				}
			}
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{"results": nodes})
}

// Helpers

func (s *Server) writeHTTPResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

// writeHTTPError writes an error response.
// If statusCode is 500, it logs the actual error internally and returns a generic message to the client
// to prevent information leakage.
func (s *Server) writeHTTPError(w http.ResponseWriter, statusCode int, err error) {
	msg := err.Error()
	if statusCode == http.StatusInternalServerError {
		log.Printf("INTERNAL SERVER ERROR: %v", err)
		msg = "Internal Server Error"
	}
	s.writeHTTPResponse(w, statusCode, map[string]string{"error": msg})
}

// decodeJSON decodes the request body into v and disallows unknown fields.
func (s *Server) decodeJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// --- AUTH HANDLERS ---

type CreateKeyRequest struct {
	Description string   `json:"description"`
	Role        string   `json:"role"`
	Namespaces  []string `json:"namespaces"`
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req CreateKeyRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	if len(req.Namespaces) == 0 {
		req.Namespaces = []string{"*"} // Default a tutti i namespace
	}

	clearToken, policy, err := s.authService.GenerateKey(req.Description, req.Role, req.Namespaces)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	// Ritorniamo il token in chiaro SOLO in questo momento.
	// Dopo questa risposta, non sarà mai più recuperabile.
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"token":   clearToken,
		"policy":  policy,
		"warning": "Copy this token now. You won't be able to see it again.",
	})
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	// Questo richiede una scansione del KV store per le chiavi che iniziano con "_sys_auth::"
	keys := s.Engine.DB.GetKVStore().GetKeysWithPrefix("_sys_auth::", 0)

	var policies []*auth.APIKeyPolicy
	for _, key := range keys {
		if val, found := s.Engine.DB.GetKVStore().Get(key); found {
			var policy auth.APIKeyPolicy
			if err := json.Unmarshal(val, &policy); err == nil {
				policies = append(policies, &policy)
			}
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, policies)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	// L'ID nell'URL è in realtà l'hash (che si può recuperare da GET /auth/keys)
	hashedKey := r.PathValue("id")
	if hashedKey == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("key id missing"))
		return
	}

	s.authService.RevokeKey(hashedKey)
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "revoked"})
}

/*
// authMiddleware wraps an http.Handler and checks for the Bearer token.
// It acts as a gatekeeper for all incoming requests.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)

		if token != s.authToken {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
*/

func (s *Server) handleUpdateAutoLinks(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name required"))
		return
	}

	var req UpdateAutoLinksRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.Engine.VUpdateAutoLinks(indexName, req.Rules); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, UpdateAutoLinksResponse{Status: "OK"})
}

func (s *Server) handleGetAutoLinks(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name required"))
		return
	}

	rules, err := s.Engine.VGetAutoLinks(indexName)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, GetAutoLinksResponse{Rules: rules})
}

func (s *Server) handleExportVectors(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name required"))
		return
	}

	limit := 1000
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	idx, ok := s.Engine.DB.GetVectorIndex(indexName)
	if !ok {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index not found"))
		return
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("not an hnsw index"))
		return
	}

	var results []ExportVectorItem
	var idsToFetch []string
	count := 0
	skipped := 0

	hnswIdx.IterateRaw(func(id string, vec interface{}) {
		if skipped < offset {
			skipped++
			return
		}
		if count >= limit {
			return
		}
		idsToFetch = append(idsToFetch, id)
		count++
	})

	// Safely fetch data in batch without holding the Index Read Lock
	if len(idsToFetch) > 0 {
		vectorDataList, _ := s.Engine.VGetMany(indexName, idsToFetch)
		for _, vData := range vectorDataList {
			results = append(results, ExportVectorItem{
				ID:       vData.ID,
				Metadata: vData.Metadata,
			})
		}
	}

	hasMore := count == limit

	s.writeHTTPResponse(w, http.StatusOK, ExportVectorsResponse{
		Data:       results,
		HasMore:    hasMore,
		NextOffset: offset + count,
		TotalCount: offset + count,
	})
}

// --- HELPER FUNCTIONS ---

// getString safely extracts a string value from metadata
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getFloat safely extracts a float64 value from metadata
func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}
