package engine

import (
	"math"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestCalculateConsensus verifies the vector density calculation.
func TestCalculateConsensus(t *testing.T) {
	tests := []struct {
		name          string
		nodes         []EpistemicNode
		expectedScore float64
		expectedVar   float64
		shouldBeNear  bool // if true, check approximate equality
	}{
		{
			name: "identical vectors",
			nodes: []EpistemicNode{
				{Vector: []float32{1.0, 0.0, 0.0}},
				{Vector: []float32{1.0, 0.0, 0.0}},
				{Vector: []float32{1.0, 0.0, 0.0}},
			},
			expectedScore: 1.0,
			expectedVar:   0.0,
			shouldBeNear:  false,
		},
		{
			name: "single node",
			nodes: []EpistemicNode{
				{Vector: []float32{1.0, 0.0, 0.0}},
			},
			expectedScore: 1.0,
			expectedVar:   0.0,
			shouldBeNear:  false,
		},
		{
			name: "orthogonal vectors",
			nodes: []EpistemicNode{
				{Vector: []float32{1.0, 0.0, 0.0}},
				{Vector: []float32{0.0, 1.0, 0.0}},
				{Vector: []float32{0.0, 0.0, 1.0}},
			},
			expectedScore: 0.0, // Should be low due to high variance
			expectedVar:   0.0, // Don't check exact variance
			shouldBeNear:  true,
		},
		{
			name:          "empty nodes",
			nodes:         []EpistemicNode{},
			expectedScore: 0.0,
			expectedVar:   0.0,
			shouldBeNear:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, variance, centroid := CalculateConsensus(tt.nodes)

			if tt.shouldBeNear {
				// For complex cases, just verify score is in reasonable range
				if score < 0 || score > 1 {
					t.Errorf("Expected score in [0,1], got %f", score)
				}
				if centroid == nil && len(tt.nodes) > 0 {
					t.Error("Expected non-nil centroid for non-empty nodes")
				}
			} else {
				if math.Abs(score-tt.expectedScore) > 0.0001 {
					t.Errorf("Expected score %f, got %f", tt.expectedScore, score)
				}
				if math.Abs(variance-tt.expectedVar) > 0.0001 {
					t.Errorf("Expected variance %f, got %f", tt.expectedVar, variance)
				}
			}
		})
	}
}

// TestCalculateStability verifies temporal robustness calculation.
func TestCalculateStability(t *testing.T) {
	now := float64(time.Now().Unix())
	thirtyDaysAgo := now - (30 * 24 * 3600)

	tests := []struct {
		name         string
		nodes        []EpistemicNode
		decayModel   string
		minScore     float64
		maxScore     float64
		expectedDays float64
	}{
		{
			name: "recent nodes with no access",
			nodes: []EpistemicNode{
				{CreatedAt: now - 3600, AccessCount: 0}, // 1 hour ago
				{CreatedAt: now - 7200, AccessCount: 0}, // 2 hours ago
			},
			decayModel:   "exponential",
			minScore:     0.9, // Recent = high stability
			maxScore:     1.0,
			expectedDays: 0.125, // ~3 hours avg
		},
		{
			name: "old nodes with many accesses",
			nodes: []EpistemicNode{
				{CreatedAt: thirtyDaysAgo, AccessCount: 100},
				{CreatedAt: thirtyDaysAgo, AccessCount: 50},
			},
			decayModel:   "ebbinghaus",
			minScore:     0.5, // Old but reinforced
			maxScore:     1.0,
			expectedDays: 30.0,
		},
		{
			name:         "empty nodes",
			nodes:        []EpistemicNode{},
			decayModel:   "exponential",
			minScore:     0.0,
			maxScore:     0.0,
			expectedDays: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, avgAge := CalculateStability(tt.nodes, tt.decayModel)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("Expected score in [%f, %f], got %f", tt.minScore, tt.maxScore, score)
			}

			if tt.expectedDays > 0 {
				diff := math.Abs(avgAge - tt.expectedDays)
				if diff > 1.0 { // Allow 1 day tolerance
					t.Errorf("Expected avgAge ~%f days, got %f", tt.expectedDays, avgAge)
				}
			}
		})
	}
}

// TestDetermineEpistemicState verifies state machine logic.
func TestDetermineEpistemicState(t *testing.T) {
	thresholds := EpistemicThresholds{
		Crystallized: 0.85,
		Volatile:     0.40,
	}

	tests := []struct {
		name           string
		confidence     float64
		contradictions int
		invalidations  int
		expectedState  string
	}{
		{
			name:           "crystallized - high confidence",
			confidence:     0.90,
			contradictions: 0,
			invalidations:  0,
			expectedState:  StateCrystallized,
		},
		{
			name:           "stable - medium confidence",
			confidence:     0.60,
			contradictions: 0,
			invalidations:  0,
			expectedState:  StateStable,
		},
		{
			name:           "volatile - low confidence",
			confidence:     0.30,
			contradictions: 0,
			invalidations:  0,
			expectedState:  StateVolatile,
		},
		{
			name:           "contested - has contradictions",
			confidence:     0.70,
			contradictions: 1,
			invalidations:  0,
			expectedState:  StateContested,
		},
		{
			name:           "contested - has invalidations",
			confidence:     0.70,
			contradictions: 0,
			invalidations:  1,
			expectedState:  StateContested,
		},
		{
			name:           "crystallized despite contradictions - very high confidence",
			confidence:     0.90,
			contradictions: 1,
			invalidations:  0,
			expectedState:  StateCrystallized, // High confidence overrides
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := DetermineEpistemicState(tt.confidence, tt.contradictions, tt.invalidations, thresholds)
			if state != tt.expectedState {
				t.Errorf("Expected state %s, got %s", tt.expectedState, state)
			}
		})
	}
}

// TestCosineDistance verifies the distance calculation.
func TestCosineDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
		tol      float64 // tolerance
	}{
		{
			name:     "identical vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 0.0, // Distance = 0 for identical vectors
			tol:      0.0001,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{0.0, 1.0, 0.0},
			expected: 1.0, // Distance = 1 for orthogonal vectors
			tol:      0.0001,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{-1.0, 0.0, 0.0},
			expected: 1.0, // Distance = 1 for opposite vectors (similarity = -1, clamped to 0)
			tol:      0.0001,
		},
		{
			name:     "different dimensions",
			a:        []float32{1.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 1.0, // Max distance for incompatible vectors
			tol:      0.0,
		},
		{
			name:     "zero vector",
			a:        []float32{0.0, 0.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 1.0, // Max distance for zero vector
			tol:      0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := CosineDistance(tt.a, tt.b)
			diff := math.Abs(dist - tt.expected)
			if diff > tt.tol {
				t.Errorf("Expected distance %f, got %f (diff %f)", tt.expected, dist, diff)
			}
		})
	}
}

// TestDefaultEpistemicConfig verifies default configuration.
func TestDefaultEpistemicConfig(t *testing.T) {
	cfg := DefaultEpistemicConfig()

	if !cfg.Enabled {
		t.Error("Expected Enabled to be true by default")
	}

	if cfg.Weights.Consensus != 0.40 {
		t.Errorf("Expected Consensus weight 0.40, got %f", cfg.Weights.Consensus)
	}

	if cfg.Weights.Stability != 0.30 {
		t.Errorf("Expected Stability weight 0.30, got %f", cfg.Weights.Stability)
	}

	if cfg.Weights.Friction != 0.30 {
		t.Errorf("Expected Friction weight 0.30, got %f", cfg.Weights.Friction)
	}

	if cfg.Thresholds.Crystallized != 0.85 {
		t.Errorf("Expected Crystallized threshold 0.85, got %f", cfg.Thresholds.Crystallized)
	}

	if cfg.Thresholds.Volatile != 0.40 {
		t.Errorf("Expected Volatile threshold 0.40, got %f", cfg.Thresholds.Volatile)
	}

	if cfg.DecayModel != "ebbinghaus" {
		t.Errorf("Expected DecayModel 'ebbinghaus', got %s", cfg.DecayModel)
	}
}

// TestVBeliefStateIntegration performs an end-to-end test with real engine.
func TestVBeliefStateIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "belief_test_idx"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Create test vectors (all identical for high consensus)
	vec := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6}

	// Add nodes with different temporal properties
	now := float64(time.Now().Unix())

	eng.VAdd(indexName, "mem1", vec, map[string]any{
		"content":       "Test memory 1",
		"_created_at":   now - (30 * 24 * 3600), // 30 days ago
		"_access_count": 50,
	})

	eng.VAdd(indexName, "mem2", vec, map[string]any{
		"content":       "Test memory 2",
		"_created_at":   now - (30 * 24 * 3600),
		"_access_count": 50,
	})

	eng.VAdd(indexName, "mem3", vec, map[string]any{
		"content":       "Test memory 3",
		"_created_at":   now - (30 * 24 * 3600),
		"_access_count": 50,
	})

	// Configure epistemic
	cfg := DefaultEpistemicConfig()

	// Run belief assessment
	state, err := eng.VBeliefState(indexName, vec, 5, *cfg)
	if err != nil {
		t.Fatalf("VBeliefState failed: %v", err)
	}

	// Verify results
	if state.Confidence <= 0 || state.Confidence > 1 {
		t.Errorf("Expected confidence in (0,1], got %f", state.Confidence)
	}

	if state.State == "" {
		t.Error("Expected non-empty state")
	}

	if len(state.Nodes) != 3 {
		t.Errorf("Expected 3 nodes, got %d", len(state.Nodes))
	}

	// With identical vectors and good temporal properties, should be high confidence
	if state.Evidence.Consensus.Score < 0.9 {
		t.Errorf("Expected high consensus for identical vectors, got %f", state.Evidence.Consensus.Score)
	}

	t.Logf("BeliefState: confidence=%f, state=%s, consensus=%f, stability=%f, friction=%f",
		state.Confidence, state.State, state.Evidence.Consensus.Score,
		state.Evidence.Stability.Score, state.Evidence.Friction.Score)
}
