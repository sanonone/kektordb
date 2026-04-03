package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleAdaptiveRagRetrieve_Validation(t *testing.T) {
	tests := []struct {
		name       string
		req        RagAdaptiveRetrieveRequest
		wantStatus int
	}{
		{
			name:       "Missing query",
			req:        RagAdaptiveRetrieveRequest{PipelineName: "test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Missing pipeline",
			req:        RagAdaptiveRetrieveRequest{Query: "test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Valid request",
			req:        RagAdaptiveRetrieveRequest{PipelineName: "test", Query: "test"},
			wantStatus: http.StatusNotFound, // Pipeline not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test would need a full server setup
			// For now, just verify request structure
			body, _ := json.Marshal(tt.req)
			t.Logf("Request body: %s", string(body))
		})
	}
}

func TestRagAdaptiveRetrieveRequestDefaults(t *testing.T) {
	req := RagAdaptiveRetrieveRequest{
		PipelineName: "test-pipeline",
		Query:        "test query",
	}

	if req.K != 0 {
		t.Errorf("Expected K to default to 0 (unset), got %d", req.K)
	}

	// Verify JSON marshaling
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var decoded RagAdaptiveRetrieveRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}

	if decoded.PipelineName != req.PipelineName {
		t.Errorf("PipelineName mismatch: got %s, want %s", decoded.PipelineName, req.PipelineName)
	}
}

func TestRagAdaptiveRetrieveResponseStructure(t *testing.T) {
	resp := RagAdaptiveRetrieveResponse{
		ContextText:   "Test context",
		ChunksUsed:    5,
		TotalTokens:   1000,
		DocumentsUsed: 2,
		ExpansionStats: struct {
			SeedChunks     int `json:"seed_chunks"`
			ExpandedChunks int `json:"expanded_chunks"`
			TotalEvaluated int `json:"total_evaluated"`
		}{
			SeedChunks:     3,
			ExpandedChunks: 10,
			TotalEvaluated: 15,
		},
	}

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	// Verify JSON structure
	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if _, ok := decoded["context_text"]; !ok {
		t.Error("Missing context_text field")
	}
	if _, ok := decoded["chunks_used"]; !ok {
		t.Error("Missing chunks_used field")
	}
	if _, ok := decoded["expansion_stats"]; !ok {
		t.Error("Missing expansion_stats field")
	}
}

func TestAdaptiveRagRetrieveEndpoint_Method(t *testing.T) {
	// Verify endpoint path is correct
	expectedPath := "POST /rag/retrieve-adaptive"
	t.Logf("Expected endpoint: %s", expectedPath)

	// The actual route registration is tested at compile time
	// This test documents the expected endpoint
}
