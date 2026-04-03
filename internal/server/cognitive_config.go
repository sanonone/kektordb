package server

import (
	"fmt"
	"os"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/llm"
	"gopkg.in/yaml.v3"
)

// CognitiveConfigFile is the top-level structure for cognitive.yaml.
type CognitiveConfigFile struct {
	Gardener    GardenerConfigYAML    `yaml:"gardener"`
	AutoResolve AutoResolveConfigYAML `yaml:"auto_resolve"`
	LLM         LLMConfigYAML         `yaml:"llm"`
}

// MemoryLayerConfigYAML holds per-layer configuration.
type MemoryLayerConfigYAML struct {
	DecayHalfLife   string `yaml:"decay_half_life"`
	PinnedByDefault bool   `yaml:"pinned_by_default"`
	AutoSummarize   bool   `yaml:"auto_summarize"`
	Description     string `yaml:"description,omitempty"`
}

// ConsolidationConfigYAML holds Gardener consolidation settings.
type ConsolidationConfigYAML struct {
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
	MaxEpisodicAge      string  `yaml:"max_episodic_age"`
}

// MemoryLayersConfigYAML holds all memory layer configurations.
type MemoryLayersConfigYAML struct {
	Episodic      MemoryLayerConfigYAML   `yaml:"episodic"`
	Semantic      MemoryLayerConfigYAML   `yaml:"semantic"`
	Procedural    MemoryLayerConfigYAML   `yaml:"procedural"`
	Consolidation ConsolidationConfigYAML `yaml:"consolidation"`
}

// GardenerConfigYAML holds the Gardener settings from cognitive.yaml.
type GardenerConfigYAML struct {
	Enabled             bool                   `yaml:"enabled"`
	Mode                string                 `yaml:"mode"`                  // "basic", "advanced", "meta"
	Interval            string                 `yaml:"interval"`              // e.g. "30s", "2m"
	TargetIndexes       []string               `yaml:"target_indexes"`        // ["*"] for all, or ["idx1", "idx2"]
	AdaptiveThreshold   int64                  `yaml:"adaptive_threshold"`    // Write count to trigger early think
	AdaptiveMinInterval string                 `yaml:"adaptive_min_interval"` // Min time between forced thinks
	MemoryLayers        MemoryLayersConfigYAML `yaml:"memory_layers"`         // Per-layer memory configuration

	// User Profiling
	EnableUserProfiling    bool `yaml:"enable_user_profiling"`    // Enable user personality profiling
	ProfileUpdateThreshold int  `yaml:"profile_update_threshold"` // Number of interactions before profile update (default: 20)
}

// AutoResolveConfigYAML holds the auto-resolve settings.
type AutoResolveConfigYAML struct {
	Enabled bool                   `yaml:"enabled"`
	Actions AutoResolveActionsYAML `yaml:"actions"`
}

// AutoResolveActionsYAML holds per-action settings for the auto-resolver.
type AutoResolveActionsYAML struct {
	CreateSuggestedLinks    AutoLinkResolveYAML   `yaml:"create_suggested_links"`
	MarkMinorContradictions AutoActionResolveYAML `yaml:"mark_minor_contradictions"`
}

// AutoLinkResolveYAML configures auto-creation of suggested graph links.
type AutoLinkResolveYAML struct {
	Enabled       bool    `yaml:"enabled"`
	MinConfidence float64 `yaml:"min_confidence"`
}

// AutoActionResolveYAML configures auto-resolution of minor contradictions.
type AutoActionResolveYAML struct {
	Enabled bool `yaml:"enabled"`
}

// LLMConfigYAML holds the LLM settings for the cognitive engine.
type LLMConfigYAML struct {
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	APIKey      string  `yaml:"api_key"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
}

// ParseDuration parses a duration string with fallback to default.
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

// buildMemoryConfig converts YAML layer config to hnsw.MemoryConfig.
func buildMemoryConfig(yamlCfg MemoryLayersConfigYAML) hnsw.MemoryConfig {
	memCfg := hnsw.DefaultMemoryConfig()

	// Override with YAML values if provided
	if yamlCfg.Episodic.DecayHalfLife != "" {
		d := parseDuration(yamlCfg.Episodic.DecayHalfLife, 72*time.Hour)
		memCfg.Layers["episodic"] = hnsw.LayerConfig{
			DecayHalfLife:   hnsw.Duration(d),
			PinnedByDefault: yamlCfg.Episodic.PinnedByDefault,
			AutoSummarize:   yamlCfg.Episodic.AutoSummarize,
			Description:     yamlCfg.Episodic.Description,
		}
	}

	if yamlCfg.Semantic.DecayHalfLife != "" {
		d := parseDuration(yamlCfg.Semantic.DecayHalfLife, 720*time.Hour)
		memCfg.Layers["semantic"] = hnsw.LayerConfig{
			DecayHalfLife:   hnsw.Duration(d),
			PinnedByDefault: yamlCfg.Semantic.PinnedByDefault,
			AutoSummarize:   yamlCfg.Semantic.AutoSummarize,
			Description:     yamlCfg.Semantic.Description,
		}
	}

	if yamlCfg.Procedural.DecayHalfLife != "" || yamlCfg.Procedural.PinnedByDefault {
		d := parseDuration(yamlCfg.Procedural.DecayHalfLife, 0)
		memCfg.Layers["procedural"] = hnsw.LayerConfig{
			DecayHalfLife:   hnsw.Duration(d),
			PinnedByDefault: yamlCfg.Procedural.PinnedByDefault,
			AutoSummarize:   yamlCfg.Procedural.AutoSummarize,
			Description:     yamlCfg.Procedural.Description,
		}
	}

	// Consolidation config
	if yamlCfg.Consolidation.SimilarityThreshold != 0 {
		memCfg.Consolidation.SimilarityThreshold = yamlCfg.Consolidation.SimilarityThreshold
	}
	if yamlCfg.Consolidation.MaxEpisodicAge != "" {
		memCfg.Consolidation.MaxEpisodicAge = hnsw.Duration(parseDuration(yamlCfg.Consolidation.MaxEpisodicAge, 168*time.Hour))
	}

	return memCfg
}

// LoadCognitiveConfig reads and parses a cognitive.yaml file.
// Returns a cognitive.Config and an llm.Config ready for use.
// If path is empty, returns defaults (disabled).
func LoadCognitiveConfig(path string) (cognitive.Config, llm.Config, error) {
	defaults := cognitive.Config{
		Enabled:             false,
		Mode:                "basic",
		Interval:            30 * time.Second,
		TargetIndexes:       []string{"*"},
		AdaptiveThreshold:   50,
		AdaptiveMinInterval: 30 * time.Second,
	}
	llmDefaults := llm.DefaultConfig()

	if path == "" {
		return defaults, llmDefaults, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return defaults, llmDefaults, fmt.Errorf("failed to read cognitive config %s: %w", path, err)
	}

	var cfg CognitiveConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaults, llmDefaults, fmt.Errorf("failed to parse cognitive config: %w", err)
	}

	// Build Gardener config
	gardener := cognitive.Config{
		Enabled:                cfg.Gardener.Enabled,
		Mode:                   cfg.Gardener.Mode,
		Interval:               parseDuration(cfg.Gardener.Interval, 30*time.Second),
		TargetIndexes:          cfg.Gardener.TargetIndexes,
		AdaptiveThreshold:      cfg.Gardener.AdaptiveThreshold,
		AdaptiveMinInterval:    parseDuration(cfg.Gardener.AdaptiveMinInterval, 30*time.Second),
		AutoResolveEnabled:     cfg.AutoResolve.Enabled,
		AutoResolveLinks:       cfg.AutoResolve.Actions.CreateSuggestedLinks.Enabled,
		AutoResolveLinksMin:    cfg.AutoResolve.Actions.CreateSuggestedLinks.MinConfidence,
		AutoResolveContra:      cfg.AutoResolve.Actions.MarkMinorContradictions.Enabled,
		MemoryConfig:           buildMemoryConfig(cfg.Gardener.MemoryLayers),
		EnableUserProfiling:    cfg.Gardener.EnableUserProfiling,
		ProfileUpdateThreshold: cfg.Gardener.ProfileUpdateThreshold,
	}

	// Apply defaults for empty/zero values
	if gardener.Mode == "" {
		gardener.Mode = "basic"
	}
	if len(gardener.TargetIndexes) == 0 {
		gardener.TargetIndexes = []string{"*"}
	}
	if gardener.AdaptiveThreshold == 0 {
		gardener.AdaptiveThreshold = 50
	}
	if gardener.AdaptiveMinInterval == 0 {
		gardener.AdaptiveMinInterval = 30 * time.Second
	}

	// Build LLM config
	llmCfg := llm.DefaultConfig()
	if cfg.LLM.BaseURL != "" {
		llmCfg.BaseURL = cfg.LLM.BaseURL
	}
	if cfg.LLM.Model != "" {
		llmCfg.Model = cfg.LLM.Model
	}
	if cfg.LLM.APIKey != "" {
		llmCfg.APIKey = cfg.LLM.APIKey
	}
	if cfg.LLM.Temperature != 0 {
		llmCfg.Temperature = cfg.LLM.Temperature
	}
	if cfg.LLM.MaxTokens != 0 {
		llmCfg.MaxTokens = cfg.LLM.MaxTokens
	}

	return gardener, llmCfg, nil
}
