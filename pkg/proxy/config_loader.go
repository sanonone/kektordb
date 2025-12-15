package proxy

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfig returns a working configuration for local Ollama.
func DefaultConfig() Config {
	return Config{
		Port:      ":9092",
		TargetURL: "http://localhost:11434",

		EmbedderType:    "ollama_api",
		EmbedderURL:     "http://localhost:11434/api/embeddings",
		EmbedderModel:   "nomic-embed-text",
		EmbedderTimeout: 60 * time.Second,

		FirewallEnabled:   false,
		FirewallIndex:     "prompt_guard",
		FirewallThreshold: 0.25,

		CacheEnabled:   false,
		CacheIndex:     "semantic_cache",
		CacheThreshold: 0.1,
		CacheTTL:       24 * time.Hour, // Sensible default
		MaxCacheItems:  10000,          // Safety limit

		CacheVacuumInterval:  60 * time.Second, // Cleans every minute
		CacheDeleteThreshold: 0.05,             // If 5% is expired/deleted,

		RAGEnabled:     false,
		RAGIndex:       "knowledge_base",
		RAGTopK:        3,
		RAGThreshold:   0.5,
		RAGUseHybrid:   false, // Off by default to keep it simple
		RAGHybridAlpha: 0.5,
		RAGUseGraph:    true, // On by default because it's the killer feature
	}
}

// LoadConfig reads the YAML configuration file using strict parsing.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig() // Start with defaults

	if path == "" {
		return cfg, nil
	}

	// 1. Open File
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to open proxy config: %w", err)
	}
	defer file.Close()

	// 2. Setup Strict Decoder
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)

	// 3. Decode
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("YAML syntax error in proxy config: %w", err)
	}

	return cfg, nil
}
