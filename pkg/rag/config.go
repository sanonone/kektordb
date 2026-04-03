package rag

import (
	"time"

	"github.com/sanonone/kektordb/pkg/llm"
)

// ParserConfig defines how raw text is extracted from files.
type ParserConfig struct {
	Type     string   // "internal" (default) or "cli"
	Command  []string // CLI command template, e.g. ["liteparse", "-q", "{{file_path}}"]
	Timeout  string   // CLI timeout, e.g. "2m"
	Fallback string   // Fallback strategy if CLI fails: "internal" (default)
}

// Config holds all parameters to set up a RAG pipeline.
type Config struct {
	Name            string
	SourcePath      string
	IndexName       string
	PollingInterval time.Duration

	// --- File Filtering ---
	// List of glob patterns to include (e.g. "*.md", "*.pdf"). Empty = all supported.
	IncludePatterns []string `json:"include_patterns"`
	// List of glob patterns to exclude (e.g. "*_test.go", "secret_*").
	ExcludePatterns []string `json:"exclude_patterns"`

	// --- Text Processing ---
	// "recursive", "code", "markdown", or "fixed" (legacy)
	ChunkingStrategy string `json:"chunking_strategy"`
	ChunkSize        int    `json:"chunk_size"`
	ChunkOverlap     int    `json:"chunk_overlap"`
	// Custom separators for recursive splitter. If empty, defaults based on strategy.
	CustomSeparators []string `json:"custom_separators"`

	// --- Parser ---
	// Configures how raw text is extracted from files (internal Go parsers or external CLI tools).
	Parser ParserConfig

	// --- Embedding Settings ---
	EmbedderURL   string
	EmbedderModel string
	// Timeout for the embedding request (useful for slow local LLMs)
	EmbedderTimeout time.Duration

	// --- Metadata ---
	MetadataTemplate map[string]string

	LLMConfig llm.Config
	// Graph Settings
	// If true, automatically creates 'next' and 'prev' links between sequential chunks.
	GraphEnabled           bool `json:"graph_enabled"`
	GraphEntityExtraction  bool
	EntityExtractionPrompt string

	VisionLLMConfig llm.Config

	AssetsOutputDir string

	IndexMetric         string
	IndexPrecision      string
	IndexM              int
	IndexEfConstruction int
	IndexTextLanguage   string
}

// AdaptiveContextConfig controls graph-aware context expansion for RAG retrieval.
type AdaptiveContextConfig struct {
	// Budget token
	MaxTokens     int     `json:"max_tokens"`      // default: 4096
	CharsPerToken float64 `json:"chars_per_token"` // default: 4.0

	// Strategia di espansione
	ExpansionStrategy string `json:"expansion_strategy"` // "greedy" | "density" | "graph"

	// Parametri Graph
	GraphExpansionDepth int                `json:"graph_expansion_depth"` // default: 2
	MaxExpansionNodes   int                `json:"max_expansion_nodes"`   // default: 200
	GraphRelations      []string           `json:"graph_relations"`
	EdgeWeights         map[string]float64 `json:"edge_weights"`

	// Parametri Density
	DensityMinRatio float64 `json:"density_min_ratio"` // default: 0.5

	// Pesi scoring finale
	SemanticWeight float64 `json:"semantic_weight"` // default: 0.6
	GraphWeight    float64 `json:"graph_weight"`    // default: 0.2
	DensityWeight  float64 `json:"density_weight"`  // default: 0.2
}

// DefaultAdaptiveConfig returns sensible defaults for adaptive retrieval.
func DefaultAdaptiveConfig() AdaptiveContextConfig {
	return AdaptiveContextConfig{
		MaxTokens:           4096,
		CharsPerToken:       4.0,
		ExpansionStrategy:   "graph",
		GraphExpansionDepth: 2,
		MaxExpansionNodes:   200,
		GraphRelations:      []string{"next", "prev", "parent", "child", "mentions", "related_to"},
		EdgeWeights: map[string]float64{
			"next":       0.95, // Sequenziale forte
			"prev":       0.95,
			"parent":     0.80, // Gerarchia
			"child":      0.70,
			"mentions":   0.50, // Associativo più debole
			"related_to": 0.40,
		},
		DensityMinRatio: 0.5,
		SemanticWeight:  0.6,
		GraphWeight:     0.2,
		DensityWeight:   0.2,
	}
}

func DefaultConfig() Config {
	return Config{
		PollingInterval:     1 * time.Minute,
		ChunkingStrategy:    "recursive",
		ChunkSize:           500,
		ChunkOverlap:        50,
		EmbedderTimeout:     60 * time.Second,
		IndexMetric:         "cosine",
		IndexPrecision:      "float32",
		IndexM:              16,
		IndexEfConstruction: 200,
		IndexTextLanguage:   "english",
	}
}
