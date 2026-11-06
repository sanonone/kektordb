package cognitive

import (
	"os"
	"testing"
	"time"
)

// TestBuildMemoryConfigPropagatesLayerDecayModel covers a gap where a per-layer
// decay model was accepted by the YAML parser and then dropped.
//
// buildMemoryConfig used episodic.decay_model as the global default and let
// semantic/procedural decay_model act only as a trigger to create the layer. A
// user who wrote `semantic: {decay_model: ebbinghaus}` got exponential decay and
// no indication that their setting was ignored.
//
// The four models are implemented in pkg/engine/search_utils.go; this test pins
// that each layer carries its own choice through configuration.
func TestBuildMemoryConfigPropagatesLayerDecayModel(t *testing.T) {
	yamlCfg := MemoryLayersConfigYAML{
		Episodic: MemoryLayerConfigYAML{
			DecayModel:    "linear",
			DecayHalfLife: "72h",
		},
		Semantic: MemoryLayerConfigYAML{
			DecayModel:    "ebbinghaus",
			DecayHalfLife: "720h",
		},
		Procedural: MemoryLayerConfigYAML{
			DecayModel:      "step",
			DecayHalfLife:   "8760h",
			PinnedByDefault: true,
		},
	}

	memCfg := buildMemoryConfig(yamlCfg)

	want := map[string]string{
		"episodic":   "linear",
		"semantic":   "ebbinghaus",
		"procedural": "step",
	}
	for layer, model := range want {
		layerCfg, ok := memCfg.Layers[layer]
		if !ok {
			t.Errorf("layer %q missing from config", layer)
			continue
		}
		if got := string(layerCfg.DecayModel); got != model {
			t.Errorf("layer %q decay model = %q, want %q: "+
				"the YAML value was accepted and then dropped", layer, got, model)
		}
	}
}

// TestBuildMemoryConfigLayerDecayModelFallsBack keeps the inheritance rule: a
// layer that does not set a model uses the global one.
func TestBuildMemoryConfigLayerDecayModelFallsBack(t *testing.T) {
	yamlCfg := MemoryLayersConfigYAML{
		Episodic: MemoryLayerConfigYAML{
			DecayModel:    "linear",
			DecayHalfLife: "72h",
		},
		Semantic: MemoryLayerConfigYAML{
			DecayHalfLife: "720h", // no decay_model: inherits at read time
		},
	}

	memCfg := buildMemoryConfig(yamlCfg)

	if got := string(memCfg.DecayModel); got != "linear" {
		t.Errorf("global decay model = %q, want linear", got)
	}
	sem, ok := memCfg.Layers["semantic"]
	if !ok {
		t.Fatal("semantic layer missing")
	}
	// Empty means "use the global one" when the score is computed; the builder
	// must not invent a different model here.
	if sem.DecayModel != "" {
		t.Errorf("semantic decay model = %q, want empty so it inherits the global one", sem.DecayModel)
	}
}

// TestDefaultMemoryConfigLayerModels pins the shipped defaults, so changing a
// layer's model becomes a deliberate act.
func TestDefaultMemoryConfigLayerModels(t *testing.T) {
	cfg := buildMemoryConfig(MemoryLayersConfigYAML{})

	if cfg.DecayModel == "" {
		t.Error("default config has no global decay model")
	}
	for _, layer := range []string{"episodic", "semantic", "procedural"} {
		layerCfg, ok := cfg.Layers[layer]
		if !ok {
			t.Errorf("defaults missing layer %q", layer)
			continue
		}
		if layerCfg.DecayModel == "" {
			t.Errorf("layer %q has no decay model in the defaults, so it silently "+
				"depends on the global value", layer)
		}
	}
}

// TestLayerDecayHalfLifeStillParsed guards against the fix disturbing the
// half-life handling that already worked.
func TestLayerDecayHalfLifeStillParsed(t *testing.T) {
	memCfg := buildMemoryConfig(MemoryLayersConfigYAML{
		Episodic: MemoryLayerConfigYAML{DecayHalfLife: "48h", DecayModel: "linear"},
	})

	ep, ok := memCfg.Layers["episodic"]
	if !ok {
		t.Fatal("episodic layer missing")
	}
	if got := time.Duration(ep.DecayHalfLife); got != 48*time.Hour {
		t.Errorf("episodic half-life = %v, want 48h", got)
	}
}

// TestLayerDecayModelsFromYAML parses a real cognitive.yaml, the way an operator
// writes it, and checks that each layer keeps its own model.
//
// This is the end-to-end shape of the gap: the YAML was accepted, the layer was
// created, and the model was dropped on the floor.
func TestLayerDecayModelsFromYAML(t *testing.T) {
	const doc = `
gardener:
  memory_layers:
    episodic:
      decay_model: linear
      decay_half_life: "72h"
    semantic:
      decay_model: ebbinghaus
      decay_half_life: "720h"
    procedural:
      decay_model: step
      decay_half_life: "8760h"
      pinned_by_default: true
`
	path := t.TempDir() + "/cognitive.yaml"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	mem := cfg.MemoryConfig
	for layer, want := range map[string]string{
		"episodic":   "linear",
		"semantic":   "ebbinghaus",
		"procedural": "step",
	} {
		if got := string(mem.Layers[layer].DecayModel); got != want {
			t.Errorf("layer %q model = %q, want %q", layer, got, want)
		}
	}
}
