package rag

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sanonone/kektordb/pkg/core/types"
)

// Pipeline orchestrates the ingestion process: Load -> Split -> Embed -> Store.
type Pipeline struct {
	cfg      Config
	loader   Loader
	splitter Splitter
	embedder Embedder
	store    Store

	stopCh chan struct{}

	// isScanning is 1 if a scan is in progress, 0 otherwise
	isScanning int32
}

// fileState traccia lo stato dell'ultima indicizzazione di un file
type fileState struct {
	ModTime    int64 `json:"mod_time"`
	ChunkCount int   `json:"chunk_count"`
}

// NewPipeline creates a ready-to-run pipeline.
// We inject the Embedder to allow testing with mocks or swapping providers.
func NewPipeline(cfg Config, store Store, embedder Embedder) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		loader:   NewAutoLoader(),
		splitter: NewSplitterFactory(cfg),
		embedder: embedder,
		store:    store,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background watcher in a goroutine.
func (p *Pipeline) Start() {
	log.Printf("[RAG] Starting pipeline '%s' watching '%s'", p.cfg.Name, p.cfg.SourcePath)
	go p.loop()
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
		log.Printf("[RAG] Scan already in progress, skipping.")
		return
	}
	defer atomic.StoreInt32(&p.isScanning, 0)
	// Ensure index exists
	if !p.store.IndexExists(p.cfg.IndexName) {
		log.Printf("[RAG] Index '%s' missing. Auto-creating...", p.cfg.IndexName)

		// Passiamo i parametri dalla config
		err := p.store.CreateVectorIndex(
			p.cfg.IndexName,
			p.cfg.IndexMetric,
			p.cfg.IndexM,
			p.cfg.IndexEfConstruction,
			p.cfg.IndexPrecision,
			p.cfg.IndexTextLanguage,
		)

		if err != nil {
			log.Printf("[RAG] Failed to create index: %v", err)
			return
		}
	}

	err := filepath.Walk(p.cfg.SourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable files
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "kektor_data" || info.Name() == "temp_rag_data" {
				return filepath.SkipDir // Salta intere cartelle
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Ignora file del DB e file binari non supportati esplicitamente
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".aof" || ext == ".kdb" || ext == ".tmp" {
			return nil
		}

		// Check Includes (Whitelist) - Se vuoto, accetta tutto
		if len(p.cfg.IncludePatterns) > 0 {
			matched := false
			for _, pattern := range p.cfg.IncludePatterns {
				// filepath.Match controlla solo il nome del file, non il path completo
				if ok, _ := filepath.Match(pattern, info.Name()); ok {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			} // Skip se non in whitelist
		}

		// Check Excludes (Blacklist)
		if len(p.cfg.ExcludePatterns) > 0 {
			for _, pattern := range p.cfg.ExcludePatterns {
				if ok, _ := filepath.Match(pattern, info.Name()); ok {
					return nil // Skip se in blacklist
				}
			}
		}

		shouldProcess, oldState := p.needsProcessing(path, info)
		if shouldProcess {
			if err := p.processFile(path, info, oldState); err != nil {
				log.Printf("[RAG] Error processing '%s': %v", path, err)
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("[RAG] Scan error: %v", err)
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
		// Se il formato è vecchio o corrotto, riprocessiamo
		return true, nil
	}

	// Se il file su disco è più recente, processiamo
	return info.ModTime().UnixNano() > state.ModTime, &state
}

// processFile executes the Load -> Split -> Embed -> Store flow.
func (p *Pipeline) processFile(path string, info os.FileInfo, oldState *fileState) error {
	log.Printf("[RAG] Processing: %s", path)

	// Prima di inserire, dobbiamo rimuovere le versioni precedenti per evitare conflitti ID
	// e per rimuovere chunk "orfani" se il file si è accorciato.
	if oldState != nil && oldState.ChunkCount > 0 {
		for i := 0; i < oldState.ChunkCount; i++ {
			oldID := fmt.Sprintf("%s_%d", path, i)
			// Ignoriamo errori di cancellazione (magari non esiste già più)
			_ = p.store.Delete(p.cfg.IndexName, oldID)
		}
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

	// 3. Embed & Prepare Batch
	var batch []types.BatchObject

	// Pre-calculate modTime string for template
	modTimeStr := info.ModTime().Format(time.RFC3339)

	for i, chunkText := range chunks {
		// Embed
		vec, err := p.embedder.Embed(chunkText)
		if err != nil {
			log.Printf("[RAG] Embedding failed for chunk %d of %s: %v", i, path, err)
			continue
		}

		// ID Deterministic: path + chunk index
		id := fmt.Sprintf("%s_%d", path, i)

		// Metadata Construction
		meta := make(map[string]interface{})

		// Always include core info
		meta["source"] = path
		meta["chunk_index"] = i
		meta["content"] = chunkText // Important: store text for retrieval!

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
		ChunkCount: len(chunks), // Salviamo quanti chunk abbiamo fatto
	}
	stateBytes, _ := json.Marshal(newState)

	return p.store.SetState(stateKey, stateBytes)
}

// Trigger forces an immediate scan and process cycle.
// It is thread-safe (can be called concurrently with the background loop).
func (p *Pipeline) Trigger() {
	// Eseguiamo in una goroutine per non bloccare il chiamante
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
	// Nota: Assumiamo che GetMany ritorni una struct con campo Metadata
	items, err := p.store.GetMany(p.cfg.IndexName, ids)
	if err != nil {
		return nil, fmt.Errorf("fetch data failed: %w", err)
	}

	// 4. Extract Text
	results := make([]string, 0, len(items))
	for _, item := range items {
		// Cerchiamo il campo "content" o "text" o "page_content"
		if val, ok := item.Metadata["content"]; ok {
			results = append(results, fmt.Sprintf("%v", val))
		} else if val, ok := item.Metadata["text"]; ok {
			results = append(results, fmt.Sprintf("%v", val))
		}
	}

	return results, nil
}
