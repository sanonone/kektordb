package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sanonone/kektordb/pkg/compiler"
)

// registerCompilerRoutes registers HTTP endpoints for the Knowledge Engine.
func (s *Server) registerCompilerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /compile", s.handleCompile)
	mux.HandleFunc("GET /compile/templates", s.handleListTemplates)
	mux.HandleFunc("GET /compile/status", s.handleCompileStatus)
	mux.HandleFunc("GET /artifacts", s.handleListArtifacts)
	mux.HandleFunc("GET /artifact/{name}", s.handleGetArtifact)
	mux.HandleFunc("GET /artifact/{name}/history", s.handleArtifactHistory)
	mux.HandleFunc("GET /artifact/{name}/at", s.handleArtifactAtTime)
	mux.HandleFunc("GET /artifact/{name}/diff", s.handleArtifactDiff)
	mux.HandleFunc("POST /compile/validate", s.handleValidateCompile)
}

// handleCompile POST /compile
func (s *Server) handleCompile(w http.ResponseWriter, r *http.Request) {
	var req compiler.CompileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Name == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("missing required field: name"))
		return
	}
	if req.Sources.Entity.ID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("missing required field: sources.entity.id"))
		return
	}
	if req.IndexName == "" {
		req.IndexName = "mcp_memory"
	}

	// Route to async if LLM fields present and LLM available
	if s.compiler.NeedsAsync(req) {
		taskID, err := s.compiler.StartAsyncCompile(req)
		if err != nil {
			s.writeHTTPError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Location", "/compile/status?task_id="+taskID)
		s.writeHTTPResponse(w, http.StatusAccepted, map[string]any{
			"task_id": taskID,
			"status":  "compiling",
			"poll":    "/compile/status?task_id=" + taskID,
		})
		return
	}

	artifact, err := s.compiler.Compile(req)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, artifact)
}

// handleCompileStatus GET /compile/status?task_id=
func (s *Server) handleCompileStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("missing query parameter: task_id"))
		return
	}

	task, err := s.compiler.GetTaskStatus(taskID)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, task)
}

// handleListTemplates GET /compile/templates
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"templates": compiler.BuiltinTemplates,
		"names":     compiler.ListTemplates(),
	})
}

// handleListArtifacts GET /artifacts
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	indexName := r.URL.Query().Get("index")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	artifacts, err := s.compiler.ListArtifacts(indexName)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"count":     len(artifacts),
		"artifacts": artifacts,
	})
}

// handleGetArtifact GET /artifact/{name}
func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entityType := r.URL.Query().Get("entity_type")
	entityID := r.URL.Query().Get("entity_id")
	indexName := r.URL.Query().Get("index")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	if entityType == "" || entityID == "" {
		s.writeHTTPError(w, http.StatusBadRequest,
			fmt.Errorf("missing query params: entity_type and entity_id are required"))
		return
	}

	artifact, err := s.compiler.GetArtifact(name, entityType, entityID, indexName)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, artifact)
}

// handleArtifactHistory GET /artifact/{name}/history
func (s *Server) handleArtifactHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entityType := r.URL.Query().Get("entity_type")
	entityID := r.URL.Query().Get("entity_id")
	indexName := r.URL.Query().Get("index")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	if entityType == "" || entityID == "" {
		s.writeHTTPError(w, http.StatusBadRequest,
			fmt.Errorf("missing query params: entity_type and entity_id are required"))
		return
	}

	filter := fmt.Sprintf(
		"type='knowledge_artifact' AND artifact_name='%s' AND entity_type='%s' AND entity_id='%s'",
		name, entityType, entityID,
	)
	ids, err := s.Engine.VFilter(indexName, filter, 20)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	type versionEntry struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	history := make([]versionEntry, 0, len(ids))
	for _, id := range ids {
		data, err := s.Engine.VGet(indexName, id)
		if err != nil {
			continue
		}
		v := 0
		if ver, ok := data.Metadata["version"].(float64); ok {
			v = int(ver)
		}
		history = append(history, versionEntry{ID: id, Version: v})
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"name":    name,
		"history": history,
	})
}

// handleArtifactAtTime GET /artifact/{name}/at?time=...
func (s *Server) handleArtifactAtTime(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entityType := r.URL.Query().Get("entity_type")
	entityID := r.URL.Query().Get("entity_id")
	indexName := r.URL.Query().Get("index")
	timeParam := r.URL.Query().Get("time")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	if entityType == "" || entityID == "" {
		s.writeHTTPError(w, http.StatusBadRequest,
			fmt.Errorf("missing query params: entity_type and entity_id are required"))
		return
	}

	atTime, err := strconv.ParseInt(timeParam, 10, 64)
	if err != nil {
		// Try unix seconds
		atTime, err = strconv.ParseInt(timeParam, 10, 64)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest,
				fmt.Errorf("invalid time parameter: %s (use unix seconds)", timeParam))
			return
		}
	}

	// Filter artifacts with all versions, then find the most recent version before atTime
	filter := fmt.Sprintf(
		"type='knowledge_artifact' AND artifact_name='%s' AND entity_type='%s' AND entity_id='%s'",
		name, entityType, entityID,
	)
	ids, err := s.Engine.VFilter(indexName, filter, 20)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	var bestMatch *compiler.Artifact
	var bestMatchTime int64
	for _, id := range ids {
		data, err := s.Engine.VGet(indexName, id)
		if err != nil {
			continue
		}
		if ca, ok := data.Metadata["_created_at"].(float64); ok {
			nodeTime := int64(ca)
			if nodeTime <= atTime && nodeTime > bestMatchTime {
				bestMatchTime = nodeTime
				a := compiler.ArtifactFromMetadata(id, data.Metadata)
				bestMatch = a
			}
		}
	}

	if bestMatch == nil {
		s.writeHTTPResponse(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("no artifact found at or before time %d", atTime),
		})
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, bestMatch)
}

// handleArtifactDiff GET /artifact/{name}/diff?v1=&v2=
func (s *Server) handleArtifactDiff(w http.ResponseWriter, r *http.Request) {
	s.writeHTTPResponse(w, http.StatusNotImplemented, map[string]string{
		"message": "artifact diff not yet implemented",
	})
}

// handleValidateCompile POST /compile/validate
func (s *Server) handleValidateCompile(w http.ResponseWriter, r *http.Request) {
	var req compiler.CompileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	errors := make([]string, 0)

	if req.Name == "" {
		errors = append(errors, "missing required field: name")
	}
	if req.Sources.Entity.ID == "" {
		errors = append(errors, "missing required field: sources.entity.id")
	}

	if req.TaskSpec != nil && req.TaskSpec.OutputSchema.Type != "" {
		for fieldName, fieldDef := range req.TaskSpec.OutputSchema.Properties {
			if fieldDef.Type == "" {
				errors = append(errors, fmt.Sprintf("field '%s': missing type", fieldName))
			}
		}
	}

	if len(errors) > 0 {
		s.writeHTTPResponse(w, http.StatusBadRequest, map[string]any{
			"valid":  false,
			"errors": errors,
		})
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"valid": true,
	})
}
