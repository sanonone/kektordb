package rag

import (
	"time"

	"github.com/sanonone/kektordb/pkg/llm"
)

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
