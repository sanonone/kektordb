package proxy

import (
	"time"

	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/llm"
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

	// --- LLM Settings ---
	FastLLM llm.Config `yaml:"fast_llm"`
	LLM     llm.Config `yaml:"llm"`

	// Firewall (Prompt Guard)
	FirewallEnabled   bool     `yaml:"firewall_enabled"`
	FirewallDenyList  []string `yaml:"firewall_deny_list"`
	FirewallIndex     string   `yaml:"firewall_index"`
	FirewallThreshold float32  `yaml:"firewall_threshold"` // e.g., 0.25
	BlockMessage      string   `yaml:"block_message"`      // Custom error msg

	// Semantic Cache
	CacheEnabled         bool          `yaml:"cache_enabled"`
	CacheIndex           string        `yaml:"cache_index"`
	CacheThreshold       float32       `yaml:"cache_threshold"` // e.g., 0.05
	CacheTTL             time.Duration `yaml:"cache_ttl"`       // Validity duration (e.g., 24h)
	MaxCacheItems        int           `yaml:"max_cache_items"` // Stop caching if full
	CacheVacuumInterval  time.Duration `yaml:"cache_vacuum_interval"`
	CacheDeleteThreshold float64       `yaml:"cache_delete_threshold"`

	// --- RAG Injection Settings ---
	RAGEnabled            bool    `yaml:"rag_enabled"`
	RAGIndex              string  `yaml:"rag_index"`        // The index where you can search for documents
	RAGTopK               int     `yaml:"rag_top_k"`        // How many chunks to retrieve (e.g. 3 or 5)
	RAGEfSearch           int     `yaml:"rag_ef_search"`    // HNSW search precision (default 100)
	RAGThreshold          float32 `yaml:"rag_threshold"`    // Maximum distance to consider a chunk useful
	RAGUseHybrid          bool    `yaml:"rag_use_hybrid"`   // BM25
	RAGHybridAlpha        float64 `yaml:"rag_hybrid_alpha"` // 0.5 default alpha
	RAGUseGraph           bool    `yaml:"rag_use_graph"`    // prev/next
	RAGUseHyDe            bool    `yaml:"rag_use_hyde"`
	RAGSystemPrompt       string  `yaml:"rag_system_prompt"`
	RAGRewriterPrompt     string  `yaml:"rag_rewriter_prompt"`
	RAGGroundedHyDePrompt string  `yaml:"rag_grounded_hyde_prompt"`
}
