package server

import (
	"fmt"
	"os"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/llm"
	"gopkg.in/yaml.v3"
)

// CognitiveConfigFile is the top-level structure for cognitive.yaml.
type CognitiveConfigFile struct {
	Gardener GardenerConfigYAML `yaml:"gardener"`
	LLM      LLMConfigYAML      `yaml:"llm"`
}

// GardenerConfigYAML holds the Gardener settings from cognitive.yaml.
type GardenerConfigYAML struct {
	Enabled             bool     `yaml:"enabled"`
	Mode                string   `yaml:"mode"`                  // "basic", "advanced", "meta"
	Interval            string   `yaml:"interval"`              // e.g. "30s", "2m"
	TargetIndexes       []string `yaml:"target_indexes"`        // ["*"] for all, or ["idx1", "idx2"]
	AdaptiveThreshold   int64    `yaml:"adaptive_threshold"`    // Write count to trigger early think
	AdaptiveMinInterval string   `yaml:"adaptive_min_interval"` // Min time between forced thinks
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
		Enabled:             cfg.Gardener.Enabled,
		Mode:                cfg.Gardener.Mode,
		Interval:            parseDuration(cfg.Gardener.Interval, 30*time.Second),
		TargetIndexes:       cfg.Gardener.TargetIndexes,
		AdaptiveThreshold:   cfg.Gardener.AdaptiveThreshold,
		AdaptiveMinInterval: parseDuration(cfg.Gardener.AdaptiveMinInterval, 30*time.Second),
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
