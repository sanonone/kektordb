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
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sanonone/kektordb/internal/server/ui"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
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
	mux.HandleFunc("POST /vector/actions/search", s.handleVectorSearch)
	mux.HandleFunc("POST /vector/actions/delete_vector", s.handleVectorDelete)
	mux.HandleFunc("POST /vector/actions/compress", s.handleVectorCompress)
	mux.HandleFunc("POST /vector/actions/get-vectors", s.handleGetVectorsBatch)

	// Graph API
	mux.HandleFunc("POST /graph/actions/link", s.handleGraphLink)
	mux.HandleFunc("POST /graph/actions/unlink", s.handleGraphUnlink)
	mux.HandleFunc("POST /graph/actions/get-links", s.handleGraphGetLinks)
	mux.HandleFunc("POST /graph/actions/get-connections", s.handleGraphGetConnections)
	mux.HandleFunc("POST /graph/actions/traverse", s.handleGraphTraverse)
	mux.HandleFunc("POST /graph/actions/get-incoming", s.handleGraphGetIncoming)
	mux.HandleFunc("POST /graph/actions/extract-subgraph", s.handleGraphExtractSubgraph)

	mux.HandleFunc("POST /rag/retrieve", s.handleRagRetrieve)

	// Dynamic index routes
	mux.HandleFunc("GET /vector/indexes/{name}", s.handleSingleIndexGet)
	mux.HandleFunc("DELETE /vector/indexes/{name}", s.handleSingleIndexDelete)

	mux.HandleFunc("POST /vector/indexes/{name}/config", s.handleIndexConfig)
	mux.HandleFunc("POST /vector/indexes/{name}/maintenance", s.handleIndexMaintenance)

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
	// Check if exists first to return 404? Or just try delete.
	// DB.DeleteVectorIndex returns error if not found.
	err := s.Engine.DB.DeleteVectorIndex(indexName)
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

	err := s.Engine.VCreate(req.IndexName, metric, req.M, req.EfConstruction, prec, req.TextLanguage, req.Maintenance)
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

	// Create the Task
	task := s.taskManager.NewTask()

	// Execute in background
	go func() {
		// ENGINE CALL (VImport)
		// Note: VImport is blocking, so the goroutine lives until it finishes.
		if err := s.Engine.VImport(req.IndexName, req.Vectors); err != nil {
			task.SetError(err)
		} else {
			task.SetStatus(TaskStatusCompleted)
			task.SetProgress(fmt.Sprintf("Imported %d vectors", len(req.Vectors)))
		}
	}()

	// Return immediately with 202 Accepted and the Task ID
	s.writeHTTPResponse(w, http.StatusAccepted, task)
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
		)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				s.writeHTTPError(w, http.StatusNotFound, err)
			} else {
				s.writeHTTPError(w, http.StatusInternalServerError, err)
			}
			return
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
	s.writeHTTPResponse(w, http.StatusOK, data)
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

	if req.SourceID == "" || req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_id, target_id, and relation_type are required"))
		return
	}

	if err := s.Engine.VLink(req.SourceID, req.TargetID, req.RelationType, req.InverseRelationType); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("Error in Vlink"))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Link created"})
}

func (s *Server) handleGraphUnlink(w http.ResponseWriter, r *http.Request) {
	var req GraphLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}

	if req.SourceID == "" || req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_id, target_id, and relation_type are required"))
		return
	}

	if err := s.Engine.VUnlink(req.SourceID, req.TargetID, req.RelationType, req.InverseRelationType); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("Error in Unlink"))
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

	if req.SourceID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("source_id and relation_type are required"))
		return
	}

	targets, found := s.Engine.VGetLinks(req.SourceID, req.RelationType)
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

	if req.TargetID == "" || req.RelationType == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("target_id and relation_type are required"))
		return
	}

	sources, found := s.Engine.VGetIncoming(req.TargetID, req.RelationType)
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

	result, err := s.Engine.VExtractSubgraph(req.IndexName, req.RootID, req.Relations, req.MaxDepth)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, result)
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
	// (We need to expose a method to get a pipeline by name)
	pipeline := s.vectorizerService.GetPipeline(req.PipelineName)
	if pipeline == nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("pipeline '%s' not found", req.PipelineName))
		return
	}

	// Execute Retrieval
	texts, err := pipeline.Retrieve(req.Query, req.K)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, fmt.Errorf("error while retrieving"))
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"results": texts,
	})
}

// --- SYSTEM HANDLERS ---

func (s *Server) handleSaveHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.Engine.SaveSnapshot(); err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, map[string]string{"status": "OK", "message": "Snapshot created"})
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
	)

	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
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
			targetIDs, found := s.Engine.VGetLinks(id, rel)
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
