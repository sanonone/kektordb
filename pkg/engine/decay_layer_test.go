package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// decayModelFor resolves the decay model for one memory.
//
// Precedence, highest first:
//  1. per-memory override in metadata (_decay_model)
//  2. the memory's layer configuration
//  3. the global default
//
// This mirrors the resolution in the search paths of ops.go. The test asserts
// the ordering, which is what makes a per-layer setting meaningful rather than
// decorative.
func decayModelFor(global string, layers map[string]hnsw.LayerConfig, layer string, meta map[string]any) string {
	model := ""
	if layers != nil {
		if cfg, ok := layers[layer]; ok {
			model = string(cfg.DecayModel)
		}
	}
	if model == "" {
		model = global
	}
	if override, ok := meta["_decay_model"].(string); ok && override != "" {
		model = override
	}
	return model
}

// TestDecayModelPrecedence pins the three levels of configuration.
func TestDecayModelPrecedence(t *testing.T) {
	layers := map[string]hnsw.LayerConfig{
		"episodic": {DecayHalfLife: hnsw.Duration(72 * time.Hour), DecayModel: hnsw.DecayLinear},
		"semantic": {DecayHalfLife: hnsw.Duration(720 * time.Hour), DecayModel: hnsw.DecayEbbinghaus},
		"other":    {DecayHalfLife: hnsw.Duration(time.Hour)}, // no model: inherits
	}

	tests := []struct {
		name   string
		global string
		layer  string
		meta   map[string]any
		want   string
	}{
		{
			name:   "layer wins over global",
			global: "exponential",
			layer:  "semantic",
			meta:   map[string]any{},
			want:   "ebbinghaus",
		},
		{
			name:   "layer wins over global, episodic",
			global: "exponential",
			layer:  "episodic",
			meta:   map[string]any{},
			want:   "linear",
		},
		{
			name:   "layer without a model inherits the global one",
			global: "exponential",
			layer:  "other",
			meta:   map[string]any{},
			want:   "exponential",
		},
		{
			name:   "unknown layer falls back to global",
			global: "step",
			layer:  "nonexistent",
			meta:   map[string]any{},
			want:   "step",
		},
		{
			name:   "per-memory override wins over the layer",
			global: "exponential",
			layer:  "semantic",
			meta:   map[string]any{"_decay_model": "step"},
			want:   "step",
		},
		{
			name:   "empty override is ignored",
			global: "exponential",
			layer:  "semantic",
			meta:   map[string]any{"_decay_model": ""},
			want:   "ebbinghaus",
		},
		{
			name:   "non-string override is ignored",
			global: "exponential",
			layer:  "semantic",
			meta:   map[string]any{"_decay_model": 42},
			want:   "ebbinghaus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decayModelFor(tt.global, layers, tt.layer, tt.meta); got != tt.want {
				t.Errorf("model = %q, want %q (global=%q layer=%q)",
					got, tt.want, tt.global, tt.layer)
			}
		})
	}
}

// TestLayerModelsProduceDifferentScores is the behavioural check: two layers
// with different models must score the same memory differently.
func TestLayerModelsProduceDifferentScores(t *testing.T) {
	halfLife := 86400.0 // 1 day
	age := halfLife * 2 // 2 days old
	created := float64(time.Now().Unix()) - age

	linear := calculateTimeDecayModel(created, halfLife, "linear", 0)
	exponential := calculateTimeDecayModel(created, halfLife, "exponential", 0)
	ebbinghaus := calculateTimeDecayModel(created, halfLife, "ebbinghaus", 0)

	// linear: 1 - 2 = -1, clamped to 0
	if linear != 0 {
		t.Errorf("linear at 2x half-life = %v, want 0 (clamped)", linear)
	}
	// exponential: 2^-2 = 0.25
	if diff := exponential - 0.25; diff > 0.01 || diff < -0.01 {
		t.Errorf("exponential at 2x half-life = %v, want ~0.25", exponential)
	}
	// ebbinghaus with no reinforcement: e^-2 ~= 0.135
	if diff := ebbinghaus - 0.135; diff > 0.01 || diff < -0.01 {
		t.Errorf("ebbinghaus at 2x half-life = %v, want ~0.135", ebbinghaus)
	}

	if linear == exponential || exponential == ebbinghaus {
		t.Error("the three models produced identical scores: per-layer choice would be cosmetic")
	}
}
