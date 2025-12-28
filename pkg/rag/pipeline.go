package rag

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/llm"
)

type extractionJob struct {
	ChunkID string
	Text    string
}

// Pipeline orchestrates the ingestion process: Load -> Split -> Embed -> Store.
type Pipeline struct {
	cfg       Config
	loader    Loader
	splitter  Splitter
	embedder  embeddings.Embedder
	llmClient llm.Client
	store     Store

	stopCh chan struct{}

	// isScanning is 1 if a scan is in progress, 0 otherwise
	isScanning     int32
	extractionChan chan extractionJob
}

// fileState tracks the state of the last file indexing
type fileState struct {
	ModTime    int64 `json:"mod_time"`
	ChunkCount int   `json:"chunk_count"`
}

// NewPipeline creates a ready-to-run pipeline.
// We inject the Embedder to allow testing with mocks or swapping providers.
func NewPipeline(cfg Config, store Store, embedder embeddings.Embedder, llmClient llm.Client) *Pipeline {
	return &Pipeline{
		cfg:            cfg,
		loader:         NewAutoLoader(),
		splitter:       NewSplitterFactory(cfg),
		embedder:       embedder,
		llmClient:      llmClient,
		store:          store,
		stopCh:         make(chan struct{}),
		extractionChan: make(chan extractionJob, 100),
	}
}

// Start launches the background watcher in a goroutine.
func (p *Pipeline) Start() {
	slog.Info("[RAG] Starting pipeline", "name", p.cfg.Name, "path", p.cfg.SourcePath)
	go p.loop()

	if p.cfg.GraphEntityExtraction {
		go p.extractionWorker()
	}
}

func (p *Pipeline) extractionWorker() {
	for job := range p.extractionChan {
		// Chiama la funzione di estrazione (quella con l'idempotenza che abbiamo scritto prima)
		if err := p.extractAndLinkEntities(job.ChunkID, job.Text); err != nil {
			// Logghiamo solo warning per non intasare
			slog.Warn("[RAG-Background] Extraction failed", "chunk_id", job.ChunkID, "error", err)
		}
	}
}

// Stop halts the background watcher.
func (p *Pipeline) Stop() {
	close(p.stopCh)
}

func (p *Pipeline) loop() {
	ticker := time.NewTicker(p.cfg.PollingInterval)
	defer ticker.Stop()

	// Initial run
	p.scanAndProcess()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.scanAndProcess()
		}
	}
}

// scanAndProcess walks the directory and processes modified files.
func (p *Pipeline) scanAndProcess() {
	// Try to acquire lock (0 -> 1)
	if !atomic.CompareAndSwapInt32(&p.isScanning, 0, 1) {
		slog.Info("[RAG] Scan already in progress, skipping")
		return
	}
	defer atomic.StoreInt32(&p.isScanning, 0)
	// Ensure index exists
	if !p.store.IndexExists(p.cfg.IndexName) {
		slog.Info("[RAG] Index missing. Auto-creating...", "index", p.cfg.IndexName)

		// Pass parameters from config
		err := p.store.CreateVectorIndex(
			p.cfg.IndexName,
			p.cfg.IndexMetric,
			p.cfg.IndexM,
			p.cfg.IndexEfConstruction,
			p.cfg.IndexPrecision,
			p.cfg.IndexTextLanguage,
		)

		if err != nil {
			slog.Error("[RAG] Failed to create index", "error", err)
			return
		}
	}

	err := filepath.Walk(p.cfg.SourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable files
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "kektor_data" || info.Name() == "temp_rag_data" {
				return filepath.SkipDir // Skip entire directories
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Ignore DB files and explicitly unsupported binary files
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".aof" || ext == ".kdb" || ext == ".tmp" {
			return nil
		}

		// Check Includes (Whitelist) - If empty, accept everything
		if len(p.cfg.IncludePatterns) > 0 {
			matched := false
			for _, pattern := range p.cfg.IncludePatterns {
				// filepath.Match checks only the filename, not the full path
				if ok, _ := filepath.Match(pattern, info.Name()); ok {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			} // Skip if not in whitelist
		}

		// Check Excludes (Blacklist)
		if len(p.cfg.ExcludePatterns) > 0 {
			for _, pattern := range p.cfg.ExcludePatterns {
				if ok, _ := filepath.Match(pattern, info.Name()); ok {
					return nil // Skip if in blacklist
				}
			}
		}

		shouldProcess, oldState := p.needsProcessing(path, info)
		if shouldProcess {
			if err := p.processFile(path, info, oldState); err != nil {
				slog.Error("[RAG] Error processing file", "path", path, "error", err)
			}
		}
		return nil
	})

	if err != nil {
		slog.Error("[RAG] Scan error", "error", err)
	}
}

// needsProcessing checks KV store to see if file was modified since last run.
func (p *Pipeline) needsProcessing(path string, info os.FileInfo) (bool, *fileState) {
	stateKey := fmt.Sprintf("_rag_state:%s:%s", p.cfg.Name, path)

	val, found := p.store.GetState(stateKey)
	if !found {
		return true, nil // Never processed
	}

	var state fileState
	if err := json.Unmarshal(val, &state); err != nil {
		// If format is old or corrupt, reprocess
		return true, nil
	}

	// If the file on disk is newer, process
	return info.ModTime().UnixNano() > state.ModTime, &state
}

// processFile executes the Load -> Split -> Embed -> Store flow.
func (p *Pipeline) processFile(path string, info os.FileInfo, oldState *fileState) error {
	slog.Info("[RAG] Processing file", "path", path)

	// 0. CLEANUP: Remove old chunks to avoid orphan IDs
	if oldState != nil && oldState.ChunkCount > 0 {
		for i := 0; i < oldState.ChunkCount; i++ {
			oldID := fmt.Sprintf("%s_%d", path, i)
			_ = p.store.Delete(p.cfg.IndexName, oldID)
		}
		// Optional: Delete the old Parent node
		// (Not strictly necessary because VAdd performs upsert/overwrite, but cleaner)
		oldParentID := fmt.Sprintf("doc:%s", path)
		_ = p.store.Delete(p.cfg.IndexName, oldParentID)
	}

	// 1. Load Text
	text, err := p.loader.Load(path)
	if err != nil {
		return err
	}
	if text == "" {
		return nil // Empty file
	}

	// 2. Split
	chunks := p.splitter.SplitText(text)
	if len(chunks) == 0 {
		return nil
	}

	// Variabili per l'estrazione Parent-Level
	var fullTextPreview strings.Builder
	const maxPreviewChars = 6000 // Abbastanza per dare contesto all'LLM

	// 3. Embed & Prepare Batch
	var batch []types.BatchObject
	modTimeStr := info.ModTime().Format(time.RFC3339)

	var prevChunkID string

	// Variables to calculate the "Average" vector of the document (Parent)
	var docVectorSum []float32
	var docVectorCount int

	// Unique Parent ID (based on file path)
	parentID := fmt.Sprintf("doc:%s", path)

	for i, chunkText := range chunks {
		// --- Accumula testo per l'estrazione sul Parent ---
		if fullTextPreview.Len() < maxPreviewChars {
			fullTextPreview.WriteString(chunkText)
			fullTextPreview.WriteString("\n")
		}

		// Embed
		vec, err := p.embedder.Embed(chunkText)
		if err != nil {
			slog.Error("[RAG] Embedding failed", "chunk_index", i, "path", path, "error", err)
			continue
		}

		// Accumulate for Parent average (only if GraphEnabled)
		if p.cfg.GraphEnabled {
			if docVectorSum == nil {
				docVectorSum = make([]float32, len(vec))
			}
			if len(vec) == len(docVectorSum) {
				for k, v := range vec {
					docVectorSum[k] += v
				}
				docVectorCount++
			}
		}

		// Chunk ID
		id := fmt.Sprintf("%s_%d", path, i)

		// Metadata Construction
		meta := make(map[string]interface{})
		meta["source"] = path
		meta["chunk_index"] = i
		meta["content"] = chunkText
		meta["type"] = "chunk"

		// Save Parent ID in metadata (useful for debugging or filtering)
		if p.cfg.GraphEnabled {
			meta["parent_id"] = parentID
		}

		// Apply User Templates
		for k, v := range p.cfg.MetadataTemplate {
			val := v
			val = strings.ReplaceAll(val, "{{file_path}}", path)
			val = strings.ReplaceAll(val, "{{filename}}", info.Name())
			val = strings.ReplaceAll(val, "{{mod_time}}", modTimeStr)
			meta[k] = val
		}

		batch = append(batch, types.BatchObject{
			Id:       id,
			Vector:   vec,
			Metadata: meta,
		})

		// --- GRAPH AUTO-LINKING LOGIC ---
		if p.cfg.GraphEnabled {
			// A. Sequential Link (Prev/Next)
			if prevChunkID != "" {
				// Atomic bidirectional: Prev <-> Curr
				err := p.store.Link(prevChunkID, id, "next", "prev")
				if err != nil {
					slog.Warn("[RAG] Link seq error", "error", err)
				}
			}
			prevChunkID = id

			// B. Hierarchical Link (Parent/Child)
			// Chunk --(parent)--> Doc
			// Doc --(child)--> Chunk
			err := p.store.Link(id, parentID, "parent", "child")
			if err != nil {
				slog.Warn("[RAG] Link parent error", "error", err)
			}

		}
	}

	// --- PARENT NODE CREATION ---
	if p.cfg.GraphEnabled && docVectorCount > 0 {
		// Calculate average
		avgVector := make([]float32, len(docVectorSum))
		for k, v := range docVectorSum {
			avgVector[k] = v / float32(docVectorCount)
		}

		// Parent Metadata
		docMeta := make(map[string]interface{})
		docMeta["source"] = path
		docMeta["filename"] = info.Name()
		docMeta["type"] = "document"
		docMeta["chunk_count"] = len(chunks)
		docMeta["mod_time"] = modTimeStr
		// (Optional) Add user templates to the parent as well
		for k, v := range p.cfg.MetadataTemplate {
			val := v
			val = strings.ReplaceAll(val, "{{file_path}}", path)
			val = strings.ReplaceAll(val, "{{filename}}", info.Name())
			val = strings.ReplaceAll(val, "{{mod_time}}", modTimeStr)
			docMeta[k] = val
		}

		// Add Parent to the batch
		batch = append(batch, types.BatchObject{
			Id:       parentID,
			Vector:   avgVector,
			Metadata: docMeta,
		})

		// === NUOVA LOGICA: ESTRAZIONE SUL PARENT (Una volta per file) ===
		if p.cfg.GraphEntityExtraction && p.llmClient != nil {
			// Usiamo il worker asincrono per non bloccare, ma lavoriamo sul PARENT ID
			// Il testo passato è la preview accumulata (i primi X caratteri del file)

			jobText := fullTextPreview.String()

			// Se il testo è troppo breve, magari non vale la pena? (Opzionale)
			if len(jobText) > 50 {
				select {
				case p.extractionChan <- extractionJob{ChunkID: parentID, Text: jobText}:
					// Job accodato
				default:
					slog.Warn("[RAG] Extraction queue full, skipping entity extraction", "doc_id", parentID)
				}
			}
		}

	}

	if len(batch) == 0 {
		return fmt.Errorf("no chunks successfully embedded")
	}

	// 4. Store Batch
	if err := p.store.AddBatch(p.cfg.IndexName, batch); err != nil {
		return err
	}

	// 5. Update State
	stateKey := fmt.Sprintf("_rag_state:%s:%s", p.cfg.Name, path)
	newState := fileState{
		ModTime:    info.ModTime().UnixNano(),
		ChunkCount: len(chunks),
	}
	stateBytes, _ := json.Marshal(newState)

	return p.store.SetState(stateKey, stateBytes)
}

// Trigger forces an immediate scan and process cycle.
// It is thread-safe (can be called concurrently with the background loop).
func (p *Pipeline) Trigger() {
	// Run in a goroutine to avoid blocking the caller
	go p.scanAndProcess()
}

// Retrieve performs a semantic search on the pipeline's index.
// 1. Embeds the text query using the configured Embedder.
// 2. Searches the Vector DB.
// 3. Retrieves the text content from metadata.
func (p *Pipeline) Retrieve(text string, k int) ([]string, error) {
	// 1. Embed
	queryVec, err := p.embedder.Embed(text)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	// 2. Search IDs
	ids, err := p.store.Search(p.cfg.IndexName, queryVec, k)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(ids) == 0 {
		return []string{}, nil
	}

	// 3. Hydrate (Get Metadata)
	// Note: We assume GetMany returns a struct with Metadata field
	items, err := p.store.GetMany(p.cfg.IndexName, ids)
	if err != nil {
		return nil, fmt.Errorf("fetch data failed: %w", err)
	}

	// 4. Extract Text
	results := make([]string, 0, len(items))
	for _, item := range items {
		// Look for "content", "text", or "page_content" field
		if val, ok := item.Metadata["content"]; ok {
			results = append(results, fmt.Sprintf("%v", val))
		} else if val, ok := item.Metadata["text"]; ok {
			results = append(results, fmt.Sprintf("%v", val))
		}
	}

	return results, nil
}

// GetEmbedder returns the embedder instance used by this pipeline.
func (p *Pipeline) GetEmbedder() embeddings.Embedder {
	return p.embedder
}

// NUOVA FUNZIONE: Estrae entità e crea nodi/link
func (p *Pipeline) extractAndLinkEntities(chunkID, text string) error {
	// 1. Costruisci il prompt
	sysPrompt := "You are an entity extraction system. Identify the top 3-5 key entities (Concepts, Projects, Technologies, People) in the text. Return a JSON array of strings. Example: [\"Project Alpha\", \"Golang\"]. Return ONLY JSON."

	if p.cfg.EntityExtractionPrompt != "" {
		sysPrompt = p.cfg.EntityExtractionPrompt
	}

	// 2. Chiama LLM
	jsonResponse, err := p.llmClient.Chat(sysPrompt, text)
	if err != nil {
		return err
	}

	// 3. Pulisci la risposta
	jsonResponse = strings.ReplaceAll(jsonResponse, "```json", "")
	jsonResponse = strings.ReplaceAll(jsonResponse, "```", "")
	jsonResponse = strings.TrimSpace(jsonResponse)

	// 4. Parse JSON
	var entities []string
	if err := json.Unmarshal([]byte(jsonResponse), &entities); err != nil {
		// Logghiamo ma non blocchiamo: gli LLM piccoli a volte sbagliano il JSON
		return fmt.Errorf("llm json error: %v", err)
	}

	if len(entities) == 0 {
		return nil
	}

	// --- FASE DI FILTRAGGIO (Idempotenza) ---

	// Mappa per tenere traccia degli ID e dei nomi originali
	// Map[EntityID] -> OriginalName
	candidates := make(map[string]string)
	var candidateIDs []string

	for _, entityName := range entities {
		safeName := strings.ToLower(strings.TrimSpace(entityName))
		safeName = strings.ReplaceAll(safeName, " ", "_")
		// Pulizia base caratteri illegali per ID se necessario
		safeName = strings.ReplaceAll(safeName, "'", "")
		safeName = strings.ReplaceAll(safeName, "\"", "")

		entityID := fmt.Sprintf("entity:%s", safeName)

		// Linkiamo SUBITO (VLink è safe e gestisce i duplicati internamente)
		if err := p.store.Link(chunkID, entityID, "mentions", "mentioned_in"); err != nil {
			slog.Warn("[RAG] Failed to link entity", "entity_name", entityName, "error", err)
		}

		candidates[entityID] = entityName
		candidateIDs = append(candidateIDs, entityID)
	}

	// Controlliamo quali entità esistono già nel DB
	// GetMany ritorna solo quelli trovati.
	existingItems, err := p.store.GetMany(p.cfg.IndexName, candidateIDs)
	if err != nil {
		return err
	}

	// Creiamo un Set degli esistenti
	existingSet := make(map[string]struct{})
	for _, item := range existingItems {
		existingSet[item.ID] = struct{}{}
	}

	// --- CREAZIONE BATCH (Solo Nuovi) ---
	var entityBatch []types.BatchObject

	for entityID, entityName := range candidates {
		// Se esiste già, saltiamo la creazione del nodo
		if _, exists := existingSet[entityID]; exists {
			continue
		}

		// Se è nuovo, calcoliamo il vettore e lo aggiungiamo
		vec, err := p.embedder.Embed(entityName)
		if err != nil {
			continue
		}

		entityBatch = append(entityBatch, types.BatchObject{
			Id:     entityID,
			Vector: vec,
			Metadata: map[string]interface{}{
				"type":    "entity",
				"name":    entityName,
				"content": fmt.Sprintf("Entity: %s", entityName),
			},
		})
	}

	// 6. Salva i nodi entità nel DB (Se ce ne sono di nuovi)
	if len(entityBatch) > 0 {
		return p.store.AddBatch(p.cfg.IndexName, entityBatch)
	}

	return nil
}
