package engine

import (
	"math"
	"time"
)

// EpistemicConfig holds per-index configuration for the epistemic engine.
// It follows the same pattern as MemoryConfig and can be set during VCreate.
type EpistemicConfig struct {
	Enabled    bool                `json:"enabled"`
	Weights    EpistemicWeights    `json:"weights"`
	Thresholds EpistemicThresholds `json:"thresholds"`
	DecayModel string              `json:"decay_model,omitempty"` // "ebbinghaus", "exponential", "linear"
}

// EpistemicWeights defines the mathematical weights for the three pillars of epistemic state.
type EpistemicWeights struct {
	Consensus float64 `json:"consensus" yaml:"consensus"` // Default: 0.40
	Stability float64 `json:"stability" yaml:"stability"` // Default: 0.30
	Friction  float64 `json:"friction" yaml:"friction"`   // Default: 0.30
}

// EpistemicThresholds defines the confidence thresholds for state determination.
type EpistemicThresholds struct {
	Crystallized float64 `json:"crystallized" yaml:"crystallized"` // Default: 0.85 (above = crystallized)
	Volatile     float64 `json:"volatile" yaml:"volatile"`         // Default: 0.40 (below = volatile)
}

// DefaultEpistemicConfig returns a sensible default configuration.
func DefaultEpistemicConfig() *EpistemicConfig {
	return &EpistemicConfig{
		Enabled: true,
		Weights: EpistemicWeights{
			Consensus: 0.40,
			Stability: 0.30,
			Friction:  0.30,
		},
		Thresholds: EpistemicThresholds{
			Crystallized: 0.85,
			Volatile:     0.40,
		},
		DecayModel: "ebbinghaus",
	}
}

// EpistemicState represents the complete epistemic assessment of a belief.
type EpistemicState struct {
	Confidence float64               `json:"confidence"` // 0.0-1.0 aggregated score
	State      string                `json:"state"`      // "crystallized", "stable", "volatile", "contested"
	Evidence   EpistemicEvidence     `json:"evidence"`
	Caveat     string                `json:"caveat,omitempty"`
	Nodes      []EpistemicNodeResult `json:"nodes"`
}

// EpistemicEvidence holds the detailed evidence for each pillar.
type EpistemicEvidence struct {
	Consensus ConsensusEvidence `json:"consensus"`
	Stability StabilityEvidence `json:"stability"`
	Friction  FrictionEvidence  `json:"friction"`
}

// ConsensusEvidence tracks vector density (semantic convergence).
type ConsensusEvidence struct {
	Score          float64   `json:"score"`              // 0.0-1.0
	Sources        int       `json:"sources"`            // Number of nodes analyzed
	VectorVariance float64   `json:"vector_variance"`    // Raw variance metric
	Centroid       []float32 `json:"centroid,omitempty"` // Computed centroid (optional, for debugging)
}

// StabilityEvidence tracks temporal robustness (age + reinforcements).
type StabilityEvidence struct {
	Score       float64 `json:"score"`              // 0.0-1.0
	AvgAgeDays  float64 `json:"avg_age_days"`       // Average age in days
	TotalAccess int     `json:"total_access_count"` // Sum of access counts
}

// FrictionEvidence tracks topological contradictions.
type FrictionEvidence struct {
	Score          float64 `json:"score"`          // 0.0-1.0 (1.0 = no friction)
	Contradictions int     `json:"contradictions"` // Count of contradicts relations
	Invalidations  int     `json:"invalidations"`  // Count of invalidated_by relations
}

// EpistemicNode is an internal representation of a candidate node for analysis.
type EpistemicNode struct {
	ID           string
	Vector       []float32
	Metadata     map[string]any
	CreatedAt    float64 // Unix timestamp
	AccessCount  int
	IsHistorical bool
	Similarity   float64 // Cosine similarity to query
}

// EpistemicNodeResult is the public representation returned in API responses.
type EpistemicNodeResult struct {
	ID             string  `json:"id"`
	Content        string  `json:"content"`    // From metadata["content"]
	Score          float64 `json:"score"`      // Cosine similarity
	CreatedAt      int64   `json:"created_at"` // Unix timestamp
	AccessCount    int     `json:"access_count"`
	IsHistorical   bool    `json:"is_historical"`
	Contradictions int     `json:"contradictions_count"`
	Invalidations  int     `json:"invalidations_count"`
}

// EpistemicStates constants for the state machine.
const (
	StateCrystallized = "crystallized"
	StateStable       = "stable"
	StateVolatile     = "volatile"
	StateContested    = "contested"
)

// Relation types for epistemic friction calculation.
const (
	RelationContradicts    = "contradicts"
	RelationContradictedBy = "contradicted_by"
	RelationInvalidates    = "invalidates"
	RelationInvalidatedBy  = "invalidated_by"
)

// CalculateConsensus computes the vector density score (Pilastro 1).
// It calculates the centroid of all node vectors and measures variance.
func CalculateConsensus(nodes []EpistemicNode) (score, variance float64, centroid []float32) {
	if len(nodes) == 0 {
		return 0, 0, nil
	}
	if len(nodes) == 1 {
		return 1.0, 0.0, nodes[0].Vector
	}

	dim := len(nodes[0].Vector)
	centroid = make([]float32, dim)

	// Calculate centroid (mean of all vectors)
	for _, node := range nodes {
		for i := 0; i < dim; i++ {
			centroid[i] += node.Vector[i]
		}
	}
	for i := 0; i < dim; i++ {
		centroid[i] /= float32(len(nodes))
	}

	// Calculate variance (mean squared distance from centroid)
	var sumVariance float64
	for _, node := range nodes {
		dist := CosineDistance(node.Vector, centroid)
		sumVariance += dist * dist
	}
	variance = sumVariance / float64(len(nodes))

	// Find max variance between any pair (for normalization)
	maxVar := 0.0
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			d := CosineDistance(nodes[i].Vector, nodes[j].Vector)
			if d > maxVar {
				maxVar = d
			}
		}
	}

	// Normalize: score = 1 - (variance / max_variance), capped at [0, 1]
	// FIX: Use maxVar*maxVar as denominator since variance is in units of distance^2
	// while maxVar is a linear distance. This ensures proper dimensional consistency.
	// Also handle floating point errors: if maxVar is very small (near-zero), treat as identical vectors.
	const epsilon = 1e-10
	if maxVar < epsilon {
		score = 1.0 // All vectors are effectively identical
	} else {
		score = 1.0 - math.Min(variance/(maxVar*maxVar), 1.0)
	}

	return score, variance, centroid
}

// CalculateStability computes temporal robustness (Pilastro 2).
// Uses access count and age with configurable decay model.
func CalculateStability(nodes []EpistemicNode, decayModel string) (score, avgAgeDays float64) {
	if len(nodes) == 0 {
		return 0, 0
	}

	now := float64(time.Now().Unix())
	var totalStability, totalAge float64

	for _, node := range nodes {
		age := now - node.CreatedAt // seconds
		ageDays := age / (24 * 3600)
		totalAge += ageDays

		// Calculate stability based on decay model
		var nodeStability float64
		switch decayModel {
		case "ebbinghaus":
			// S = halfLife * (1 + ln(1 + accessCount))
			// Decay = e^(-age/S)
			baseHalfLife := 30.0 * 24 * 3600 // 30 days in seconds
			stability := baseHalfLife * (1.0 + math.Log1p(float64(node.AccessCount)))
			nodeStability = math.Exp(-age / stability)
		case "exponential":
			halfLife := 30.0 * 24 * 3600 // 30 days
			nodeStability = math.Pow(2, -age/halfLife)
		case "linear":
			halfLife := 30.0 * 24 * 3600
			nodeStability = math.Max(0.0, 1.0-age/halfLife)
		default:
			// Default to exponential
			halfLife := 30.0 * 24 * 3600
			nodeStability = math.Pow(2, -age/halfLife)
		}

		// Bonus for reinforcements (logarithmic scaling)
		accessBonus := math.Log1p(float64(node.AccessCount)) / 10.0
		nodeStability = math.Min(1.0, nodeStability+accessBonus)

		totalStability += nodeStability
	}

	score = totalStability / float64(len(nodes))
	avgAgeDays = totalAge / float64(len(nodes))
	return score, avgAgeDays
}

// CalculateFriction computes topological contradiction penalties (Pilastro 3).
// Returns score (1.0 = no friction), and counts of negative relations.
func CalculateFriction(nodes []EpistemicNode, indexName string, getIncomingEdges func(string, string, string, int64) ([]GraphEdge, bool)) (score float64, contradictions, invalidations int) {
	const (
		contradictionWeight = 0.20
		invalidationWeight  = 0.50
	)

	for _, node := range nodes {
		// O(1) lookup using the provided function
		if getIncomingEdges != nil {
			// FIX: Look for incoming edges of type "contradicts" and "invalidates"
			// These are the relations that OTHER nodes have pointing TO this node
			edges, _ := getIncomingEdges(indexName, node.ID, RelationContradicts, 0)
			contradictions += len(edges)

			edges, _ = getIncomingEdges(indexName, node.ID, RelationInvalidates, 0)
			invalidations += len(edges)
		}
	}

	// Calculate malus
	totalMalus := float64(contradictions)*contradictionWeight +
		float64(invalidations)*invalidationWeight

	score = math.Max(0.0, 1.0-totalMalus)
	return score, contradictions, invalidations
}

// DetermineEpistemicState selects the state based on final confidence and friction.
func DetermineEpistemicState(confidence float64, contradictions, invalidations int, thresholds EpistemicThresholds) string {
	// Contested state: has contradictions/invalidations AND not crystallized
	if (contradictions > 0 || invalidations > 0) && confidence < thresholds.Crystallized {
		return StateContested
	}

	// Crystallized: high confidence, regardless of friction
	if confidence >= thresholds.Crystallized {
		return StateCrystallized
	}

	// Volatile: low confidence
	if confidence <= thresholds.Volatile {
		return StateVolatile
	}

	// Default: stable
	return StateStable
}

// GenerateCaveat creates a human-readable explanation of the epistemic state.
func GenerateCaveat(state string, contradictions, invalidations int, consensusScore, variance float64) string {
	switch state {
	case StateCrystallized:
		return "Questo fatto è solidamente stabilito: alta convergenza semantica, storicità confermata e nessuna contraddizione significativa."
	case StateStable:
		return "Questo fatto è generalmente affidabile, ma potrebbe beneficiare di ulteriori conferme."
	case StateVolatile:
		return "ATTENZIONE: Memoria recente o poco accessata. La veridicità non è ancora stabilita - trattare con scetticismo."
	case StateContested:
		if invalidations > 0 {
			return "CRITICO: Questa memoria è stata invalidata. Esaminare le fonti contraddittorie prima di usarla."
		}
		if contradictions > 0 {
			return "ATTENZIONE: Rilevate contraddizioni nella knowledge base. La veridicità è in discussione."
		}
		return "Questa memoria ha attrito epistemico - esaminare con cautela."
	default:
		return ""
	}
}

// CosineDistance calculates cosine distance (1 - similarity) between two vectors.
func CosineDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return 1.0 // Max distance for incompatible vectors
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 1.0 // Max distance for zero vectors
	}

	similarity := dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
	// Clamp to [0, 1] to handle floating point errors
	similarity = math.Max(0.0, math.Min(1.0, similarity))

	return 1.0 - similarity
}
