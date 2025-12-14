package server

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/rag"
)

// VectorizerService manages the lifecycle of RAG pipelines.
type VectorizerService struct {
	server    *Server
	pipelines []*rag.Pipeline
	wg        sync.WaitGroup
}

// NewVectorizerService initializes the service based on the YAML config.
func NewVectorizerService(server *Server) (*VectorizerService, error) {
	service := &VectorizerService{
		server: server,
	}

	for _, cfg := range server.vectorizerConfig.Vectorizers {
		// 1. Parse Duration (Schedule and Embedder Timeout)
		schedule, err := time.ParseDuration(cfg.Schedule)
		if err != nil {
			log.Printf("ERROR: Invalid schedule for vectorizer '%s': %v", cfg.Name, err)
			continue
		}

		embedTimeout, _ := time.ParseDuration(cfg.Embedder.Timeout)
		if embedTimeout == 0 {
			embedTimeout = 60 * time.Second // Safe default if not specified
		}

		// 2. Calculate Overlap (if not specified, use 10% of ChunkSize)
		overlap := cfg.DocProcessor.ChunkOverlap
		if overlap == 0 && cfg.DocProcessor.ChunkSize > 0 {
			overlap = cfg.DocProcessor.ChunkSize / 10
		}

		idxMetric := cfg.IndexConfig.Metric
		if idxMetric == "" {
			idxMetric = "cosine"
		}

		idxPrec := cfg.IndexConfig.Precision
		if idxPrec == "" {
			idxPrec = "float32"
		}

		idxM := cfg.IndexConfig.M
		if idxM == 0 {
			idxM = 16
		}

		idxEf := cfg.IndexConfig.EfConstruction
		if idxEf == 0 {
			idxEf = 200
		}

		idxLang := cfg.IndexConfig.TextLanguage
		if idxLang == "" {
			idxLang = "english"
		}

		// 3. Map YAML Configuration -> RAG Configuration
		ragConfig := rag.Config{
			Name:            cfg.Name,
			SourcePath:      cfg.Source.Path,
			IndexName:       cfg.KektorIndex,
			PollingInterval: schedule,

			// File Filters
			IncludePatterns: cfg.IncludePatterns,
			ExcludePatterns: cfg.ExcludePatterns,

			// Advanced Text Processing
			ChunkingStrategy: cfg.DocProcessor.ChunkingStrategy, // es. "recursive", "markdown", "code"
			ChunkSize:        cfg.DocProcessor.ChunkSize,
			ChunkOverlap:     overlap,
			CustomSeparators: cfg.DocProcessor.CustomSeparators,

			// Embedding
			EmbedderURL:     cfg.Embedder.URL,
			EmbedderModel:   cfg.Embedder.Model,
			EmbedderTimeout: embedTimeout, // Pass the parsed timeout

			MetadataTemplate: cfg.MetadataTemplate,

			IndexMetric:         idxMetric,
			IndexPrecision:      idxPrec,
			IndexM:              idxM,
			IndexEfConstruction: idxEf,
			IndexTextLanguage:   idxLang,
		}

		// 4. Create Dependencies
		storeAdapter := rag.NewKektorAdapter(server.Engine)

		// Pass the timeout to the Embedder constructor
		embedder := embeddings.NewOllamaEmbedder(ragConfig.EmbedderURL, ragConfig.EmbedderModel, ragConfig.EmbedderTimeout)

		// 5. Create Pipeline
		pipeline := rag.NewPipeline(ragConfig, storeAdapter, embedder)

		service.pipelines = append(service.pipelines, pipeline)
		log.Printf("RAG Pipeline '%s' configured (Mode: %s, Source: %s)", cfg.Name, ragConfig.ChunkingStrategy, cfg.Source.Path)
	}

	return service, nil
}

// Start launches all pipelines.
func (vs *VectorizerService) Start() {
	if vs == nil || len(vs.pipelines) == 0 {
		return
	}
	log.Println("Starting RAG Pipelines...")
	for _, p := range vs.pipelines {
		p.Start()
	}
}

// Stop halts all pipelines.
func (vs *VectorizerService) Stop() {
	log.Println("Stopping RAG Pipelines...")
	for _, p := range vs.pipelines {
		p.Stop()
	}
}

// Trigger manually forces a scan for a specific pipeline.
func (vs *VectorizerService) Trigger(name string) error {
	// Note: rag.Pipeline does not publicly expose the name in the struct,
	// but we can find it by iterating over the original config or modifying Pipeline to expose Name.
	// For now, we assume the order is maintained or modify Pipeline to have GetName().

	// Clean solution: Look in the server config
	for i, cfg := range vs.server.vectorizerConfig.Vectorizers {
		if cfg.Name == name {
			if i < len(vs.pipelines) {
				vs.pipelines[i].Trigger()
				return nil
			}
		}
	}
	return fmt.Errorf("vectorizer pipeline '%s' not found", name)
}

// GetStatuses returns a summary (simplified for now).
func (vs *VectorizerService) GetStatuses() []VectorizerStatus {
	var statuses []VectorizerStatus
	for _, cfg := range vs.server.vectorizerConfig.Vectorizers {
		statuses = append(statuses, VectorizerStatus{
			Name:         cfg.Name,
			IsRunning:    true,
			CurrentState: "active (managed by rag pkg)",
		})
	}
	return statuses
}

// GetPipeline returns a running pipeline by name, or nil if not found.
func (vs *VectorizerService) GetPipeline(name string) *rag.Pipeline {
	for i, cfg := range vs.server.vectorizerConfig.Vectorizers {
		if cfg.Name == name {
			if i < len(vs.pipelines) {
				return vs.pipelines[i]
			}
		}
	}
	return nil
}

// VectorizerStatus is a public-facing struct for the API, containing no internal fields.
// It provides a snapshot of a vectorizer's current state.
type VectorizerStatus struct {
	Name         string    `json:"name"`
	IsRunning    bool      `json:"is_running"`
	LastRun      time.Time `json:"last_run,omitempty"`
	CurrentState string    `json:"current_state"`
}
