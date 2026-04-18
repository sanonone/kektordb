package engine

import (
	"fmt"
	"math"
	"sync"
)

// VBeliefState performs an epistemic assessment of a belief.
// It evaluates the truth and robustness of memories matching the query vector
// by analyzing three pillars: Consensus (vector density), Stability (temporal),
// and Friction (topological contradictions).
//
// Parameters:
//   - indexName: The vector index to search
//   - queryVec: The query vector to search for
//   - k: Number of candidate memories to analyze (default 10, max 50)
//   - cfg: Epistemic configuration (weights and thresholds)
//
// Returns:
//   - EpistemicState: Complete assessment with confidence, state, evidence, and nodes
func (e *Engine) VBeliefState(indexName string, queryVec []float32, k int, cfg EpistemicConfig) (*EpistemicState, error) {
	if !e.IndexExists(indexName) {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}

	// Validate and cap k
	if k <= 0 {
		k = 10
	}
	if k > 50 {
		k = 50 // Safety cap
	}

	// Use default config if not provided properly
	if cfg.Weights.Consensus == 0 && cfg.Weights.Stability == 0 && cfg.Weights.Friction == 0 {
		defaultCfg := DefaultEpistemicConfig()
		cfg = *defaultCfg
	}

	// Step 1: Retrieve candidate nodes via standard vector search
	// We use VSearch to get top-k similar nodes
	candidateIDs, err := e.VSearch(indexName, queryVec, k, "", "", 100, 0.5, nil)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(candidateIDs) == 0 {
		return nil, fmt.Errorf("no candidate memories found for query")
	}

	// Step 2: Materialize nodes with vectors and metadata
	nodes := make([]EpistemicNode, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		data, err := e.VGet(indexName, id)
		if err != nil {
			continue // Skip inaccessible nodes
		}

		// Handle potential nil vector (shouldn't happen for normal memories)
		if len(data.Vector) == 0 {
			continue
		}

		// Decompress if needed - for now we assume vectors are already float32
		// Int8 decompression would happen in a real implementation
		vector := data.Vector

		// Extract metadata
		createdAt := getMetadataFloat(data.Metadata, "_created_at")
		// Note: VectorData doesn't have CreatedAt field, we rely on metadata

		accessCount := getMetadataInt(data.Metadata, "_access_count")

		isHistorical := false
		if hist, ok := data.Metadata["_is_historical"].(bool); ok {
			isHistorical = hist
		}

		// Calculate similarity to query for the result
		similarity := 1.0 - CosineDistance(vector, queryVec)

		nodes = append(nodes, EpistemicNode{
			ID:           id,
			Vector:       vector,
			Metadata:     data.Metadata,
			CreatedAt:    createdAt,
			AccessCount:  accessCount,
			IsHistorical: isHistorical,
			Similarity:   similarity,
		})

		// Safety check: if we have enough nodes, break
		// FIX: Changed from i >= k-1 to len(nodes) >= k to process exactly k candidates (off-by-one bug)
		if len(nodes) >= k {
			break
		}
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no valid nodes found after materialization")
	}

	// FIX: Filter out historical nodes - they shouldn't contribute to belief assessment
	// Historical nodes are obsolete versions (evolved memories) and would skew the scores
	var activeNodes []EpistemicNode
	for _, node := range nodes {
		if !node.IsHistorical {
			activeNodes = append(activeNodes, node)
		}
	}

	if len(activeNodes) == 0 {
		return nil, fmt.Errorf("no active (non-historical) nodes found for assessment")
	}

	// Step 3: Calculate Pilastro 1 - Consensus (Vector Density)
	consensusScore, variance, _ := CalculateConsensus(activeNodes)

	// Step 4: Calculate Pilastro 2 - Stability (Temporal)
	decayModel := cfg.DecayModel
	if decayModel == "" {
		decayModel = "ebbinghaus"
	}
	stabilityScore, avgAgeDays := CalculateStability(activeNodes, decayModel)

	// Step 5: Calculate Pilastro 3 - Friction (Topological)
	// Create a closure to pass to CalculateFriction
	getIncomingEdges := func(idxName, nodeID, relType string, atTime int64) ([]GraphEdge, bool) {
		return e.VGetIncomingEdges(idxName, nodeID, relType, atTime)
	}
	frictionScore, contradictions, invalidations := CalculateFriction(activeNodes, indexName, getIncomingEdges)

	// Step 6: Calculate Final Confidence
	// Confidence = (Consensus * W_c) + (Stability * W_s) + (Friction * W_f)
	finalConfidence :=
		consensusScore*cfg.Weights.Consensus +
			stabilityScore*cfg.Weights.Stability +
			frictionScore*cfg.Weights.Friction

	// Clamp to [0, 1]
	finalConfidence = math.Max(0.0, math.Min(1.0, finalConfidence))

	// Step 7: Determine State
	state := DetermineEpistemicState(finalConfidence, contradictions, invalidations, cfg.Thresholds)

	// Step 8: Generate Caveat
	caveat := GenerateCaveat(state, contradictions, invalidations, consensusScore, variance)

	// Step 9: Format Node Results
	nodeResults := formatEpistemicNodeResults(nodes, indexName, e)

	// Calculate total access count
	totalAccess := 0
	for _, node := range nodes {
		totalAccess += node.AccessCount
	}

	return &EpistemicState{
		Confidence: finalConfidence,
		State:      state,
		Evidence: EpistemicEvidence{
			Consensus: ConsensusEvidence{
				Score:          consensusScore,
				Sources:        len(nodes),
				VectorVariance: variance,
			},
			Stability: StabilityEvidence{
				Score:       stabilityScore,
				AvgAgeDays:  avgAgeDays,
				TotalAccess: totalAccess,
			},
			Friction: FrictionEvidence{
				Score:          frictionScore,
				Contradictions: contradictions,
				Invalidations:  invalidations,
			},
		},
		Caveat: caveat,
		Nodes:  nodeResults,
	}, nil
}

// formatEpistemicNodeResults converts internal nodes to public results.
func formatEpistemicNodeResults(nodes []EpistemicNode, indexName string, e *Engine) []EpistemicNodeResult {
	results := make([]EpistemicNodeResult, len(nodes))

	for i, node := range nodes {
		// Get content from metadata
		content := ""
		if c, ok := node.Metadata["content"].(string); ok {
			content = c
		}

		// Count contradictions and invalidations for this node
		contradictions := 0
		invalidations := 0

		// FIX: Look for incoming edges of type "contradicts" and "invalidates"
		// These are the relations that OTHER nodes have pointing TO this node
		edges, _ := e.VGetIncomingEdges(indexName, node.ID, RelationContradicts, 0)
		contradictions = len(edges)

		edges, _ = e.VGetIncomingEdges(indexName, node.ID, RelationInvalidates, 0)
		invalidations = len(edges)

		results[i] = EpistemicNodeResult{
			ID:             node.ID,
			Content:        content,
			Score:          node.Similarity,
			CreatedAt:      int64(node.CreatedAt),
			AccessCount:    node.AccessCount,
			IsHistorical:   node.IsHistorical,
			Contradictions: contradictions,
			Invalidations:  invalidations,
		}
	}

	return results
}

// Helper functions for metadata extraction
func getMetadataFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}

	val, ok := m[key]
	if !ok {
		return 0
	}

	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func getMetadataInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}

	val, ok := m[key]
	if !ok {
		return 0
	}

	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

// EpistemicConfig storage helpers (for per-index config)

// epistemicConfigMap is a simple in-memory store for epistemic configs per index.
// In production, this should be persisted via AOF (VCONFIG command).
// FIX: Protected by mutex to prevent race conditions with concurrent HTTP handlers.
var (
	epistemicConfigMap = make(map[string]EpistemicConfig)
	epistemicConfigMu  sync.RWMutex
)

// GetEpistemicConfig retrieves the config for an index.
// Returns default config if not explicitly set.
func (e *Engine) GetEpistemicConfig(indexName string) EpistemicConfig {
	epistemicConfigMu.RLock()
	cfg, ok := epistemicConfigMap[indexName]
	epistemicConfigMu.RUnlock()
	if ok {
		return cfg
	}
	return *DefaultEpistemicConfig()
}

// SetEpistemicConfig sets the epistemic config for an index.
func (e *Engine) SetEpistemicConfig(indexName string, cfg EpistemicConfig) {
	epistemicConfigMu.Lock()
	epistemicConfigMap[indexName] = cfg
	epistemicConfigMu.Unlock()
}
