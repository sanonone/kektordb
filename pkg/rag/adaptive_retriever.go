package rag

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/engine"
)

// AdaptiveStore defines the storage interface needed by AdaptiveRetriever
type AdaptiveStore interface {
	VSearch(indexName string, query []float32, k int, filter string, explicitTextQuery string, efSearch int, alpha float64, graphQuery *engine.GraphQuery) ([]string, error)
	VGetRelations(indexName, sourceID string) map[string][]string
	VGet(indexName, id string) (core.VectorData, error)
}

// ScoredChunk represents a chunk with its scoring information
type ScoredChunk struct {
	Chunk        core.VectorData
	Content      string
	DerivedScore float64 // Score propagato dal grafo
	Depth        int     // Distanza dal seed
	Density      float64 // Densità informativa
	FinalScore   float64 // Score finale combinato
}

// ExpansionNode represents a node during BFS expansion
type ExpansionNode struct {
	ID    string
	Depth int
	Score float64
	Path  []string
}

// ContextWindow represents the assembled context
type ContextWindow struct {
	Chunks        []core.VectorData
	ContextText   string
	TotalTokens   int
	TotalChunks   int
	DocumentsUsed int
	Stats         ExpansionStats
}

// ExpansionStats contains statistics about the expansion process
type ExpansionStats struct {
	SeedChunks        int
	ExpandedChunks    int
	FilteredByDensity int
	TotalEvaluated    int
}

// AdaptiveRetriever performs graph-aware context expansion
type AdaptiveRetriever struct {
	store  AdaptiveStore
	config AdaptiveContextConfig
}

// NewAdaptiveRetriever creates a new adaptive retriever
func NewAdaptiveRetriever(store AdaptiveStore, config AdaptiveContextConfig) *AdaptiveRetriever {
	// Apply defaults for zero values
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	if config.CharsPerToken == 0 {
		config.CharsPerToken = 4.0
	}
	if config.ExpansionStrategy == "" {
		config.ExpansionStrategy = "graph"
	}
	if config.GraphExpansionDepth == 0 {
		config.GraphExpansionDepth = 2
	}
	if config.MaxExpansionNodes == 0 {
		config.MaxExpansionNodes = 200
	}
	if len(config.GraphRelations) == 0 {
		config.GraphRelations = []string{"next", "prev", "parent", "child", "mentions", "related_to"}
	}
	if config.DensityMinRatio == 0 {
		config.DensityMinRatio = 0.5
	}
	if config.SemanticWeight == 0 && config.GraphWeight == 0 && config.DensityWeight == 0 {
		config.SemanticWeight = 0.6
		config.GraphWeight = 0.2
		config.DensityWeight = 0.2
	}

	return &AdaptiveRetriever{
		store:  store,
		config: config,
	}
}

// RetrieveWithContext performs adaptive retrieval with graph expansion
func (ar *AdaptiveRetriever) RetrieveWithContext(
	indexName string,
	queryVec []float32,
	k int,
) (*ContextWindow, error) {
	// 1. Semantic retrieval (seed chunks)
	seedIDs, err := ar.store.VSearch(indexName, queryVec, k, "", "", 0, 0.5, nil)
	if err != nil {
		return nil, fmt.Errorf("semantic search failed: %w", err)
	}

	if len(seedIDs) == 0 {
		return &ContextWindow{
			Chunks:      []core.VectorData{},
			ContextText: "",
			Stats:       ExpansionStats{},
		}, nil
	}

	// Create map of seed scores (all seeds have score 1.0 initially, normalized later)
	seedScores := make(map[string]float64)
	for _, id := range seedIDs {
		seedScores[id] = 1.0
	}

	// 2. Graph expansion based on strategy
	var candidates []ScoredChunk
	switch ar.config.ExpansionStrategy {
	case "greedy":
		candidates = ar.expandGreedy(indexName, seedIDs, seedScores)
	case "density":
		candidates = ar.expandWithDensityFilter(indexName, seedIDs, seedScores)
	case "graph":
		candidates = ar.expandGraphBFS(indexName, seedIDs, seedScores)
	default:
		candidates = ar.expandGraphBFS(indexName, seedIDs, seedScores)
	}

	// 3. Assemble context respecting token budget
	return ar.assembleContext(indexName, candidates), nil
}

// expandGreedy expands seeds with all directly connected nodes
func (ar *AdaptiveRetriever) expandGreedy(indexName string, seedIDs []string, seedScores map[string]float64) []ScoredChunk {
	visited := make(map[string]bool)
	results := make([]ScoredChunk, 0, len(seedIDs)*3)

	for _, seedID := range seedIDs {
		if visited[seedID] {
			continue
		}
		visited[seedID] = true

		// Add seed
		chunk := ar.getChunkData(indexName, seedID)
		if chunk != nil {
			results = append(results, ScoredChunk{
				Chunk:        *chunk,
				Content:      ar.extractContent(chunk.Metadata),
				DerivedScore: seedScores[seedID],
				Depth:        0,
			})
		}

		// Expand one level
		relations := ar.store.VGetRelations(indexName, seedID)
		for relType, targets := range relations {
			if !ar.isRelationAllowed(relType) {
				continue
			}
			weight := ar.getEdgeWeight(relType)

			for _, targetID := range targets {
				if visited[targetID] {
					continue
				}
				visited[targetID] = true

				targetChunk := ar.getChunkData(indexName, targetID)
				if targetChunk != nil {
					results = append(results, ScoredChunk{
						Chunk:        *targetChunk,
						Content:      ar.extractContent(targetChunk.Metadata),
						DerivedScore: seedScores[seedID] * weight,
						Depth:        1,
					})
				}
			}
		}
	}

	return results
}

// expandWithDensityFilter expands and filters by information density
func (ar *AdaptiveRetriever) expandWithDensityFilter(indexName string, seedIDs []string, seedScores map[string]float64) []ScoredChunk {
	candidates := ar.expandGreedy(indexName, seedIDs, seedScores)
	filtered := make([]ScoredChunk, 0, len(candidates))

	for _, c := range candidates {
		density := calculateInformationDensity(c.Content)
		c.Density = density

		if density >= ar.config.DensityMinRatio {
			filtered = append(filtered, c)
		}
	}

	return filtered
}

// expandGraphBFS performs BFS expansion with shortest-path deduplication
func (ar *AdaptiveRetriever) expandGraphBFS(indexName string, seedIDs []string, seedScores map[string]float64) []ScoredChunk {
	// visited tracks minimum depth found for each node
	visited := make(map[string]int)
	queue := make([]ExpansionNode, 0, 100)
	results := make([]ScoredChunk, 0, ar.config.MaxExpansionNodes)

	// Initialize with seed chunks
	for _, id := range seedIDs {
		visited[id] = 0
		queue = append(queue, ExpansionNode{
			ID:    id,
			Depth: 0,
			Score: seedScores[id],
			Path:  []string{id},
		})

		chunk := ar.getChunkData(indexName, id)
		if chunk != nil {
			results = append(results, ScoredChunk{
				Chunk:        *chunk,
				Content:      ar.extractContent(chunk.Metadata),
				DerivedScore: seedScores[id],
				Depth:        0,
			})
		}
	}

	// BFS
	head := 0
	for head < len(queue) && len(visited) < ar.config.MaxExpansionNodes {
		current := queue[head]
		head++

		if current.Depth >= ar.config.GraphExpansionDepth {
			continue
		}

		// Get relations
		relations := ar.store.VGetRelations(indexName, current.ID)

		for relType, targets := range relations {
			if !ar.isRelationAllowed(relType) {
				continue
			}

			weight := ar.getEdgeWeight(relType)
			derivedScore := current.Score * weight

			for _, targetID := range targets {
				newDepth := current.Depth + 1

				// Deduplication: shortest path wins
				if existingDepth, exists := visited[targetID]; exists {
					if newDepth < existingDepth {
						// Found better path, update depth
						visited[targetID] = newDepth
						ar.updateChunkScore(results, targetID, derivedScore, newDepth)
					}
					continue
				}

				// New node
				visited[targetID] = newDepth
				queue = append(queue, ExpansionNode{
					ID:    targetID,
					Depth: newDepth,
					Score: derivedScore,
					Path:  append(append([]string{}, current.Path...), targetID),
				})

				chunk := ar.getChunkData(indexName, targetID)
				if chunk != nil {
					results = append(results, ScoredChunk{
						Chunk:        *chunk,
						Content:      ar.extractContent(chunk.Metadata),
						DerivedScore: derivedScore,
						Depth:        newDepth,
					})
				}
			}
		}
	}

	return results
}

// assembleContext assembles final context respecting token budget
func (ar *AdaptiveRetriever) assembleContext(indexName string, chunks []ScoredChunk) *ContextWindow {
	if len(chunks) == 0 {
		return &ContextWindow{
			Chunks:      []core.VectorData{},
			ContextText: "",
			Stats:       ExpansionStats{},
		}
	}

	// 1. Calculate final combined score for each chunk
	for i := range chunks {
		density := calculateInformationDensity(chunks[i].Content)
		chunks[i].Density = density

		// Normalize density (typical range 0.3-0.9)
		normalizedDensity := math.Min(1.0, math.Max(0.0, (density-0.3)/0.6))

		// Penalità per depth (più lontano = meno rilevante)
		depthPenalty := 1.0 - float64(chunks[i].Depth)*0.15
		if depthPenalty < 0.3 {
			depthPenalty = 0.3
		}

		chunks[i].FinalScore =
			ar.config.SemanticWeight*chunks[i].DerivedScore +
				ar.config.GraphWeight*depthPenalty +
				ar.config.DensityWeight*normalizedDensity
	}

	// 2. Group by document (parent_id)
	byDocument := make(map[string][]ScoredChunk)
	for _, c := range chunks {
		docID := ""
		if val, ok := c.Chunk.Metadata["parent_id"]; ok {
			docID = fmt.Sprintf("%v", val)
		}
		if docID == "" {
			docID = "orphan"
		}
		byDocument[docID] = append(byDocument[docID], c)
	}

	// 3. Sort internally by chunk_index or follow next chain
	for docID := range byDocument {
		sort.Slice(byDocument[docID], func(i, j int) bool {
			// Try ordering by chunk_index
			idx1 := ar.getChunkIndex(byDocument[docID][i].Chunk.Metadata)
			idx2 := ar.getChunkIndex(byDocument[docID][j].Chunk.Metadata)
			return idx1 < idx2
		})
	}

	// 4. Sort documents by max semantic score of their seeds
	docScores := make(map[string]float64)
	for docID, docChunks := range byDocument {
		maxScore := 0.0
		for _, c := range docChunks {
			if c.Depth == 0 && c.DerivedScore > maxScore {
				maxScore = c.DerivedScore
			}
		}
		docScores[docID] = maxScore
	}

	sortedDocs := make([]string, 0, len(byDocument))
	for docID := range byDocument {
		sortedDocs = append(sortedDocs, docID)
	}
	sort.Slice(sortedDocs, func(i, j int) bool {
		return docScores[sortedDocs[i]] > docScores[sortedDocs[j]]
	})

	// 5. Assemble respecting token budget
	budget := ar.config.MaxTokens
	selected := make([]core.VectorData, 0)
	totalTokens := 0

	for _, docID := range sortedDocs {
		for _, chunk := range byDocument[docID] {
			chunkTokens := int(float64(len(chunk.Content)) / ar.config.CharsPerToken)

			if totalTokens+chunkTokens > budget {
				break // Budget exhausted
			}

			selected = append(selected, chunk.Chunk)
			totalTokens += chunkTokens
		}
	}

	// Build context text
	var contextParts []string
	for _, chunk := range selected {
		content := ar.extractContent(chunk.Metadata)
		if content != "" {
			contextParts = append(contextParts, content)
		}
	}

	return &ContextWindow{
		Chunks:        selected,
		ContextText:   strings.Join(contextParts, "\n\n---\n\n"),
		TotalTokens:   totalTokens,
		TotalChunks:   len(selected),
		DocumentsUsed: len(sortedDocs),
		Stats: ExpansionStats{
			SeedChunks:     len(chunks),
			ExpandedChunks: len(chunks),
			TotalEvaluated: len(chunks),
		},
	}
}

// Helper methods

func (ar *AdaptiveRetriever) getChunkData(indexName, id string) *core.VectorData {
	data, err := ar.store.VGet(indexName, id)
	if err != nil {
		return nil
	}
	return &data
}

func (ar *AdaptiveRetriever) extractContent(metadata map[string]any) string {
	if val, ok := metadata["content"]; ok {
		return fmt.Sprintf("%v", val)
	}
	if val, ok := metadata["text"]; ok {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func (ar *AdaptiveRetriever) isRelationAllowed(relType string) bool {
	for _, allowed := range ar.config.GraphRelations {
		if allowed == relType {
			return true
		}
	}
	return false
}

func (ar *AdaptiveRetriever) getEdgeWeight(relType string) float64 {
	if weight, ok := ar.config.EdgeWeights[relType]; ok {
		return weight
	}
	return 0.3 // default
}

func (ar *AdaptiveRetriever) updateChunkScore(chunks []ScoredChunk, id string, newScore float64, newDepth int) {
	for i := range chunks {
		if chunks[i].Chunk.ID == id {
			if newScore > chunks[i].DerivedScore {
				chunks[i].DerivedScore = newScore
				chunks[i].Depth = newDepth
			}
			break
		}
	}
}

func (ar *AdaptiveRetriever) getChunkIndex(metadata map[string]any) int {
	if val, ok := metadata["chunk_index"]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			if idx, err := strconv.Atoi(v); err == nil {
				return idx
			}
		}
	}
	return 0
}

// calculateInformationDensity computes the ratio of unique tokens to total tokens
func calculateInformationDensity(text string) float64 {
	if text == "" {
		return 0.0
	}

	// Tokenize (split by spaces and punctuation)
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})

	if len(tokens) == 0 {
		return 0.0
	}

	// Count unique tokens (case-insensitive)
	unique := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		unique[strings.ToLower(t)] = struct{}{}
	}

	return float64(len(unique)) / float64(len(tokens))
}
