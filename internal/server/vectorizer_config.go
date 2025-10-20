package server

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
)

/*
*   Creato una struct per ogni "sezione" del file YAML.
*   I tag `yaml:"..."` dicono al parser come mappare le chiavi del file YAML ai campi delle struct Go.
*   La funzione `LoadVectorizersConfig` apre il file, lo legge e usa `yaml.Unmarshal` per popolarne le struct.
 */

// Config rappresenta l'intero file di configurazione dei vectorizer.
type Config struct {
	Vectorizers []VectorizerConfig `yaml:"vectorizers"`
}

// VectorizerConfig definisce un singolo task di sincronizzazione.
type VectorizerConfig struct {
	Name             string             `yaml:"name"`
	KektorIndex      string             `yaml:"kektor_index"`
	Schedule         string             `yaml:"schedule"`
	Source           SourceConfig       `yaml:"source"`
	Embedder         EmbedderConfig     `yaml:"embedder"`
	DocProcessor     DocProcessorConfig `yaml:"document_processor"`
	MetadataTemplate map[string]string  `yaml:"metadata_template"`
}

// SourceConfig definisce da dove leggere i dati.
type SourceConfig struct {
	Type string `yaml:"type"` // "filesystem"
	Path string `yaml:"path"`
}

// EmbedderConfig definisce come generare gli embeddings.
type EmbedderConfig struct {
	Type  string `yaml:"type"` // "ollama_api"
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

// DocProcessorConfig definisce come dividere i documenti.
type DocProcessorConfig struct {
	ChunkingStrategy string `yaml:"chunking_strategy"` // "lines", "fixed_size"
	ChunkSize        int    `yaml:"chunk_size"`
}

// LoadVectorizersConfig legge e parsa il file di configurazione YAML.
func LoadVectorizersConfig(path string) (*Config, error) {
	if path == "" {
		return &Config{}, nil // Nessun file specificato, restituisce una config vuota
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("impossibile leggere il file di configurazione '%s': %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("impossibile parsare il file YAML '%s': %w", path, err)
	}

	return &config, nil
}
