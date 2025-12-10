// Package server implements the main KektorDB server logic.
//
// This file defines the Go structs that correspond to the YAML configuration for
// the VectorizerService. These structs allow for type-safe parsing of the
// configuration file, defining how each vectorizer should behave, including its
// data source, embedding model, and document processing strategy.

package server

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
)

// Config represents the top-level structure of the vectorizers configuration file.
// It holds a slice of configurations, one for each vectorizer worker.
type Config struct {
	Vectorizers []VectorizerConfig `yaml:"vectorizers"`
}

// VectorizerConfig defines the configuration for a single synchronization task.
// Each VectorizerConfig corresponds to one background worker that monitors a source
// and syncs its content into a specified KektorDB index.
type VectorizerConfig struct {
	Name             string              `yaml:"name"`
	KektorIndex      string              `yaml:"kektor_index"`
	Schedule         string              `yaml:"schedule"`
	Source           SourceConfig        `yaml:"source"`
	Embedder         EmbedderConfig      `yaml:"embedder"`
	DocProcessor     DocProcessorConfig  `yaml:"document_processor"`
	MetadataTemplate map[string]string   `yaml:"metadata_template"`
	IncludePatterns  []string            `yaml:"include_patterns"`
	ExcludePatterns  []string            `yaml:"exclude_patterns"`
	IndexConfig      IndexCreationConfig `yaml:"index_config"`
}

type IndexCreationConfig struct {
	Metric         string `yaml:"metric"`          // "cosine"
	Precision      string `yaml:"precision"`       // "float32", "int8", "float16"
	M              int    `yaml:"m"`               // 16, 32...
	EfConstruction int    `yaml:"ef_construction"` // 200...
	TextLanguage   string `yaml:"text_language"`   // "italian", "english"
}

// SourceConfig defines where the data to be vectorized is read from.
type SourceConfig struct {
	Type string `yaml:"type"` // "filesystem"
	Path string `yaml:"path"`
}

// EmbedderConfig defines how to generate vector embeddings from text content.
// It specifies the service type, URL, and model to be used.
type EmbedderConfig struct {
	Type    string `yaml:"type"` // "ollama_api"
	URL     string `yaml:"url"`
	Model   string `yaml:"model"`
	Timeout string `yaml:"timeout"`
}

// DocProcessorConfig defines how source documents are processed before embedding.
// This includes the strategy for splitting documents into smaller chunks.
type DocProcessorConfig struct {
	ChunkingStrategy string   `yaml:"chunking_strategy"`
	ChunkSize        int      `yaml:"chunk_size"`
	ChunkOverlap     int      `yaml:"chunk_overlap"`
	CustomSeparators []string `yaml:"custom_separators"`
}

// LoadVectorizersConfig reads and parses the YAML configuration file from the given path.
// If the path is an empty string, it returns a valid, empty Config struct without error,
// allowing the server to run without any vectorizers configured.
func LoadVectorizersConfig(path string) (*Config, error) {
	if path == "" {
		return &Config{}, nil // No file specified, return an empty config.
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read configuration file '%s': %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("could not parse YAML file '%s': %w", path, err)
	}

	return &config, nil
}
