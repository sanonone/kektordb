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
		CacheTTL:       24 * time.Hour, // Default sensato
		MaxCacheItems:  10000,          // Limite di sicurezza

		CacheVacuumInterval:  60 * time.Second, // Pulisce ogni minuto
		CacheDeleteThreshold: 0.05,             // Se il 5% è scaduto/cancellato,
	}
}

// LoadConfig reads the YAML configuration file.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig() // Start with defaults

	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read proxy config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse proxy config: %w", err)
	}

	return cfg, nil
}
