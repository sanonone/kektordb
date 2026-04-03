package server

import (
	"testing"
)

func TestExtractFilenameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/docs/manual.pdf", "manual.pdf"},
		{"C:\\Users\\doc\\file.txt", "file.txt"},
		{"simple.txt", "simple.txt"},
		{"", ""},
		{"/path/to/deep/nested/file.md", "file.md"},
	}

	for _, tt := range tests {
		result := extractFilenameFromPath(tt.path)
		if result != tt.expected {
			t.Errorf("extractFilenameFromPath(%q) = %q, want %q", tt.path, result, tt.expected)
		}
	}
}

func TestTruncateIDForDisplay(t *testing.T) {
	tests := []struct {
		id       string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"verylongidthatexceedsthelimit", 10, "verylongid..."},
		{"exactlyten", 10, "exactlyten"},
		{"", 10, ""},
	}

	for _, tt := range tests {
		result := truncateIDForDisplay(tt.id, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateIDForDisplay(%q, %d) = %q, want %q", tt.id, tt.maxLen, result, tt.expected)
		}
	}
}

func TestFormatPathString(t *testing.T) {
	nodes := []GraphPathNode{
		{ID: "chunk1", Type: "chunk", Label: "doc.pdf_1"},
		{ID: "doc1", Type: "document", Label: "doc.pdf"},
	}

	result := formatPathString(nodes)
	expected := "doc.pdf_1 → doc.pdf"

	if result != expected {
		t.Errorf("formatPathString() = %q, want %q", result, expected)
	}
}

func TestFormatPathStringEmpty(t *testing.T) {
	result := formatPathString([]GraphPathNode{})
	if result != "" {
		t.Errorf("formatPathString(empty) = %q, want empty string", result)
	}
}

func TestCalculateConfidence(t *testing.T) {
	sources := []SourceAttribution{
		{Relevance: 0.9},
		{Relevance: 0.8},
		{Relevance: 0.7},
	}

	result := CalculateConfidence(sources)
	expected := 0.8 // (0.9 + 0.8 + 0.7) / 3

	// Use tolerance for floating point comparison
	if result < expected-0.001 || result > expected+0.001 {
		t.Errorf("CalculateConfidence() = %f, want %f", result, expected)
	}
}

func TestCalculateConfidenceEmpty(t *testing.T) {
	result := CalculateConfidence([]SourceAttribution{})
	if result != 0.0 {
		t.Errorf("CalculateConfidence(empty) = %f, want 0.0", result)
	}
}

func TestEstimateTokens(t *testing.T) {
	parts := []string{"hello", "world", "test"}
	result := EstimateTokens(parts, 4.0)
	// (5 + 5 + 4) / 4 = 14/4 = 3.5 → 3
	if result != 3 {
		t.Errorf("EstimateTokens() = %d, want 3", result)
	}
}

func TestEstimateTokensDefault(t *testing.T) {
	parts := []string{"test"}
	result := EstimateTokens(parts, 0) // Should use default 4.0
	// 4 / 4 = 1
	if result != 1 {
		t.Errorf("EstimateTokens(default) = %d, want 1", result)
	}
}

func TestExtractSourceMetadata(t *testing.T) {
	metadata := map[string]interface{}{
		"source":      "/docs/file.pdf",
		"chunk_index": float64(5),
		"page_number": float64(2),
	}

	sourceFile, filename, chunkIndex, pageNumber := ExtractSourceMetadata(metadata)

	if sourceFile != "/docs/file.pdf" {
		t.Errorf("sourceFile = %q, want /docs/file.pdf", sourceFile)
	}
	if filename != "file.pdf" {
		t.Errorf("filename = %q, want file.pdf", filename)
	}
	if chunkIndex != 5 {
		t.Errorf("chunkIndex = %d, want 5", chunkIndex)
	}
	if pageNumber != 2 {
		t.Errorf("pageNumber = %d, want 2", pageNumber)
	}
}

func TestRagRetrieveRequestWithProvenance(t *testing.T) {
	// Test struct with include_provenance flag
	var req RagRetrieveRequest
	req.PipelineName = "test"
	req.Query = "test query"
	req.K = 5
	req.IncludeProvenance = true

	if !req.IncludeProvenance {
		t.Error("IncludeProvenance should be true")
	}
}

func TestGraphPathStructure(t *testing.T) {
	path := GraphPath{
		Nodes: []GraphPathNode{
			{ID: "chunk1", Type: "chunk", Label: "doc_1"},
			{ID: "doc1", Type: "document", Label: "doc"},
		},
		Edges: []GraphPathEdge{
			{Source: "chunk1", Target: "doc1", Relation: "parent"},
		},
		Formatted: "doc_1 → doc",
	}

	if len(path.Nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(path.Nodes))
	}
	if len(path.Edges) != 1 {
		t.Errorf("Expected 1 edge, got %d", len(path.Edges))
	}
	if path.Formatted != "doc_1 → doc" {
		t.Errorf("Formatted = %q, want 'doc_1 → doc'", path.Formatted)
	}
}
