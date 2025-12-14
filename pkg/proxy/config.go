package proxy

import (
	"time"

	"github.com/sanonone/kektordb/pkg/embeddings"
)

type Config struct {
	// Proxy Server Settings
	Port      string `yaml:"port"`       // ":9092"
	TargetURL string `yaml:"target_url"` // "http://localhost:11434"

	// Embedder Settings (Specific for Proxy)
	EmbedderType    string              `yaml:"embedder_type"` // "ollama_api" (future: "openai")
	EmbedderURL     string              `yaml:"embedder_url"`
	EmbedderModel   string              `yaml:"embedder_model"`
	EmbedderTimeout time.Duration       `yaml:"embedder_timeout"`
	Embedder        embeddings.Embedder `yaml:"-" json:"-"`

	// Firewall (Prompt Guard)
	FirewallEnabled   bool    `yaml:"firewall_enabled"`
	FirewallIndex     string  `yaml:"firewall_index"`
	FirewallThreshold float32 `yaml:"firewall_threshold"` // e.g., 0.25
	BlockMessage      string  `yaml:"block_message"`      // Custom error msg

	// Semantic Cache
	CacheEnabled         bool          `yaml:"cache_enabled"`
	CacheIndex           string        `yaml:"cache_index"`
	CacheThreshold       float32       `yaml:"cache_threshold"` // e.g., 0.05
	CacheTTL             time.Duration `yaml:"cache_ttl"`       // Validity duration (e.g., 24h)
	MaxCacheItems        int           `yaml:"max_cache_items"` // Stop caching if full
	CacheVacuumInterval  time.Duration `yaml:"cache_vacuum_interval"`
	CacheDeleteThreshold float64       `yaml:"cache_delete_threshold"`
}
