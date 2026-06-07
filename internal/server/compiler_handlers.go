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
	mux.HandleFunc("GET /artifact/{name}/stale", s.handleArtifactStale)
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
	versionStr := r.URL.Query().Get("version")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	if entityType == "" || entityID == "" {
		s.writeHTTPError(w, http.StatusBadRequest,
			fmt.Errorf("missing query params: entity_type and entity_id are required"))
		return
	}

	version := 0
	if versionStr != "" {
		var err error
		version, err = strconv.Atoi(versionStr)
		if err != nil {
			s.writeHTTPError(w, http.StatusBadRequest,
				fmt.Errorf("invalid version: %s", versionStr))
			return
		}
	}

	artifact, err := s.compiler.GetArtifact(name, entityType, entityID, indexName, version)
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

	history, err := s.compiler.GetArtifactHistory(name, entityType, entityID, indexName)
	if err != nil {
		s.writeHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"name":    name,
		"count":   len(history),
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
	name := r.PathValue("name")
	v1Str := r.URL.Query().Get("v1")
	v2Str := r.URL.Query().Get("v2")

	if v1Str == "" || v2Str == "" {
		s.writeHTTPError(w, http.StatusBadRequest,
			fmt.Errorf("missing query params: v1 and v2 are required"))
		return
	}

	v1, err := strconv.Atoi(v1Str)
	if err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid v1: %s", v1Str))
		return
	}
	v2, err := strconv.Atoi(v2Str)
	if err != nil {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("invalid v2: %s", v2Str))
		return
	}

	entityType := r.URL.Query().Get("entity_type")
	entityID := r.URL.Query().Get("entity_id")
	indexName := r.URL.Query().Get("index")
	if indexName == "" {
		indexName = "mcp_memory"
	}

	a1, err := s.compiler.GetArtifact(name, entityType, entityID, indexName, v1)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("v1 not found: %w", err))
		return
	}
	a2, err := s.compiler.GetArtifact(name, entityType, entityID, indexName, v2)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("v2 not found: %w", err))
		return
	}

	diff := map[string]any{
		"added":    map[string]any{},
		"removed":  map[string]any{},
		"modified": map[string]any{},
	}

	for k, v := range a2.Data {
		if _, ok := a1.Data[k]; !ok {
			diff["added"].(map[string]any)[k] = v
		} else if fmt.Sprintf("%v", a1.Data[k]) != fmt.Sprintf("%v", v) {
			diff["modified"].(map[string]any)[k] = map[string]any{
				"v1": a1.Data[k],
				"v2": v,
			}
		}
	}
	for k := range a1.Data {
		if _, ok := a2.Data[k]; !ok {
			diff["removed"].(map[string]any)[k] = a1.Data[k]
		}
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"name": name,
		"v1":   v1,
		"v2":   v2,
		"diff": diff,
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

// handleArtifactStale GET /artifact/{name}/stale?entity_type=&entity_id=
func (s *Server) handleArtifactStale(w http.ResponseWriter, r *http.Request) {
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

	artifact, err := s.compiler.GetArtifact(name, entityType, entityID, indexName, 0)
	if err != nil {
		s.writeHTTPError(w, http.StatusNotFound, err)
		return
	}

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"name":            artifact.Name,
		"entity_type":     artifact.EntityType,
		"entity_id":       artifact.EntityID,
		"version":         artifact.Version,
		"staleness_score": artifact.StalenessScore,
		"status":          artifact.Status,
		"compiled_at":     artifact.CompiledAt,
	})
}
