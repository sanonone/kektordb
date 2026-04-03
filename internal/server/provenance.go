package server

import (
	"strings"

	"github.com/sanonone/kektordb/pkg/engine"
)

// ProvenanceService handles provenance calculation for source attribution
type ProvenanceService struct {
	engine    *engine.Engine
	indexName string
}

// NewProvenanceService creates a new provenance service
func NewProvenanceService(eng *engine.Engine, indexName string) *ProvenanceService {
	return &ProvenanceService{
		engine:    eng,
		indexName: indexName,
	}
}

// BuildGraphPath builds the path from chunk to parent document with JSON structure
// Returns the path and a boolean indicating if verification succeeded
func (ps *ProvenanceService) BuildGraphPath(chunkID string, parentID string) (*GraphPath, bool) {
	if ps.engine == nil || parentID == "" {
		return nil, false
	}

	// Execute FindPath to verify the relationship
	result, err := ps.engine.FindPath(
		ps.indexName,
		chunkID,
		parentID,
		[]string{"parent"},
		1, // maxDepth 1 (direct)
		0, // now
	)
	if err != nil || result == nil || len(result.Path) == 0 {
		// If FindPath fails, we can still construct a basic path from metadata
		// but mark it as unverified
		return ps.buildUnverifiedPath(chunkID, parentID), false
	}

	// Build structured path from FindPath result
	path := &GraphPath{
		Nodes: make([]GraphPathNode, 0, len(result.Path)),
		Edges: make([]GraphPathEdge, 0, len(result.Edges)),
	}

	// Add nodes
	for i, nodeID := range result.Path {
		nodeType := "chunk"
		if i == len(result.Path)-1 {
			nodeType = "document"
		}

		path.Nodes = append(path.Nodes, GraphPathNode{
			ID:    nodeID,
			Type:  nodeType,
			Label: truncateIDForDisplay(nodeID, 50),
		})
	}

	// Add edges
	for _, edge := range result.Edges {
		path.Edges = append(path.Edges, GraphPathEdge{
			Source:   edge.Source,
			Target:   edge.Target,
			Relation: edge.Relation,
		})
	}

	// Generate formatted string representation
	path.Formatted = formatPathString(path.Nodes)

	return path, true
}

// buildUnverifiedPath creates a basic path from metadata when FindPath fails
func (ps *ProvenanceService) buildUnverifiedPath(chunkID string, parentID string) *GraphPath {
	path := &GraphPath{
		Nodes: []GraphPathNode{
			{ID: chunkID, Type: "chunk", Label: truncateIDForDisplay(chunkID, 50)},
			{ID: parentID, Type: "document", Label: truncateIDForDisplay(parentID, 50)},
		},
		Edges: []GraphPathEdge{
			{Source: chunkID, Target: parentID, Relation: "parent"},
		},
	}
	path.Formatted = formatPathString(path.Nodes)
	return path
}

// ExtractSourceMetadata extracts source metadata from chunk metadata
func ExtractSourceMetadata(metadata map[string]interface{}) (sourceFile, filename string, chunkIndex, pageNumber int) {
	// Source file path
	if src, ok := metadata["source"].(string); ok {
		sourceFile = src
		filename = extractFilenameFromPath(src)
	}

	// Chunk index
	if idx, ok := metadata["chunk_index"].(float64); ok {
		chunkIndex = int(idx)
	}

	// Page number (from PDF loader if available)
	if page, ok := metadata["page_number"].(float64); ok {
		pageNumber = int(page)
	}

	return
}

// extractFilenameFromPath extracts just the filename from a full path
func extractFilenameFromPath(path string) string {
	if path == "" {
		return ""
	}

	// Handle both / and \ separators
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		idx = strings.LastIndex(path, "\\")
	}
	if idx == -1 {
		return path
	}
	return path[idx+1:]
}

// truncateIDForDisplay truncates long IDs for display purposes
func truncateIDForDisplay(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen] + "..."
}

// formatPathString creates a human-readable arrow-separated path
func formatPathString(nodes []GraphPathNode) string {
	if len(nodes) == 0 {
		return ""
	}

	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = n.Label
	}

	return strings.Join(parts, " → ")
}

// CalculateConfidence calculates average confidence from sources
func CalculateConfidence(sources []SourceAttribution) float64 {
	if len(sources) == 0 {
		return 0.0
	}

	total := 0.0
	for _, s := range sources {
		total += s.Relevance
	}
	return total / float64(len(sources))
}

// EstimateTokens estimates token count from text parts
func EstimateTokens(parts []string, charsPerToken float64) int {
	if charsPerToken <= 0 {
		charsPerToken = 4.0 // Default
	}

	total := 0
	for _, p := range parts {
		total += int(float64(len(p)) / charsPerToken)
	}
	return total
}
