package server

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/llm"
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

			GraphEnabled:          cfg.GraphEnabled,
			GraphEntityExtraction: cfg.GraphEntityExtraction,
			LLMConfig:             cfg.LLM,
			VisionLLMConfig:       cfg.VisionLLM,

			IndexMetric:         idxMetric,
			IndexPrecision:      idxPrec,
			IndexM:              idxM,
			IndexEfConstruction: idxEf,
			IndexTextLanguage:   idxLang,
		}

		// 4. Create Dependencies
		storeAdapter := rag.NewKektorAdapter(server.Engine)

		// Pass the timeout to the Embedder constructor
		var embedder embeddings.Embedder

		switch cfg.Embedder.Type {
		case "openai", "openai_compatible":
			embedder = embeddings.NewOpenAIEmbedder(
				ragConfig.EmbedderURL,
				ragConfig.EmbedderModel,
				cfg.Embedder.APIKey,
				ragConfig.EmbedderTimeout,
			)
			log.Printf("   -> Using OpenAI-compatible embedder for '%s'", cfg.Name)

		case "ollama", "ollama_api":
			fallthrough // Se il tipo è ollama o non specificato (default)
		default:
			embedder = embeddings.NewOllamaEmbedder(
				ragConfig.EmbedderURL,
				ragConfig.EmbedderModel,
				ragConfig.EmbedderTimeout,
			)
		}

		// --- Init Client LLM ---
		var llmClient llm.Client
		if ragConfig.GraphEntityExtraction {
			// Se l'utente non ha configurato l'URL nel blocco LLM, usiamo un default sensato o fallback
			if ragConfig.LLMConfig.BaseURL == "" {
				// Fallback ai default di pkg/llm se la sezione manca
				ragConfig.LLMConfig = llm.DefaultConfig()
			}

			log.Printf("[Vectorizer] Enabling Entity Extraction using LLM (%s)", ragConfig.LLMConfig.BaseURL)
			llmClient = llm.NewClient(ragConfig.LLMConfig)
		}

		var visionClient llm.Client
		// Se l'URL è settato, vuol dire che l'utente vuole la visione
		if ragConfig.VisionLLMConfig.BaseURL != "" {
			log.Printf("[Vectorizer] Enabling Vision Pipeline using (%s)", ragConfig.VisionLLMConfig.Model)
			visionClient = llm.NewClient(ragConfig.VisionLLMConfig)
		}

		// 5. Create Pipeline
		pipeline := rag.NewPipeline(ragConfig, storeAdapter, embedder, llmClient, visionClient)

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

// Add to internal/server/vectorizer_service.go

// GetEmbedderForIndex returns the embedder configured for a specific index.
// Useful for UI/Search endpoints that need to convert text to vector using the same model as ingestion.
func (vs *VectorizerService) GetEmbedderForIndex(indexName string) embeddings.Embedder {
	for _, cfg := range vs.server.vectorizerConfig.Vectorizers {
		if cfg.KektorIndex == indexName {
			// We need the initialized embedder, which is inside the running pipeline.
			// So we match the name.
			pipeline := vs.GetPipeline(cfg.Name)
			if pipeline != nil {
				return pipeline.GetEmbedder() // We need to expose this in rag/Pipeline
			}
		}
	}
	return nil
}
