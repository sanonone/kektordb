// Package server implements the main KektorDB server logic.
//
// This file defines the Vectorizer worker, which is responsible for a single
// synchronization task. It monitors a source directory, detects file changes,
// processes documents by chunking and embedding them, and persists the resulting
// vectors and metadata into the database.

package server

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/text"
	"log"
	"os"
	"path/filepath" // For manipulating file paths
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Vectorizer is the worker that represents a single synchronization task.
// It acts as a guardian for the folder to be monitored.
type Vectorizer struct {
	config       VectorizerConfig
	server       *Server // Reference to the server to access the store, etc.
	ticker       *time.Ticker
	stopCh       chan struct{}
	lastRun      time.Time
	currentState atomic.Value // To store the state in a thread-safe manner
	wg           *sync.WaitGroup
}

// NewVectorizer creates and starts a new worker for a synchronization task.
func NewVectorizer(config VectorizerConfig, server *Server, wg *sync.WaitGroup) (*Vectorizer, error) {
	// Parsa la durata della schedulazione (es. "30s", "5m")
	schedule, err := time.ParseDuration(config.Schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule format for vectorizer '%s': %w", config.Name, err)
	}

	vec := &Vectorizer{
		config: config,
		server: server,
		ticker: time.NewTicker(schedule),
		stopCh: make(chan struct{}),
		wg:     wg,
	}

	// Aggiunge 1 al contatore del WaitGroup PRIMA di avviare la goroutine
	// vec.wg.Add(1)
	// Avvia il loop principale del worker in una goroutine
	// go vec.run()

	log.Printf("Vectorizer '%s' initialized. Will check every %s.", config.Name, config.Schedule)
	return vec, nil
}

// run is the core loop of the worker.
// It performs an initial synchronization and then waits for ticker events or a stop signal.
func (v *Vectorizer) run() {
	// Signal that this goroutine is finished when the function exits
	defer v.wg.Done()
	defer log.Printf("Vectorizer '%s' stopped.", v.config.Name)

	// Perform an initial check immediately upon startup.
	log.Printf("Vectorizer '%s': Performing initial synchronization check...", v.config.Name)
	v.currentState.Store("idle") // Set the initial state
	v.synchronize()

	for {
		select {
		case <-v.ticker.C:
			// The ticker has fired, execute the synchronization.
			log.Printf("Vectorizer '%s': Periodic check started...", v.config.Name)
			v.currentState.Store("synchronizing")
			v.synchronize()
			v.lastRun = time.Now()
			v.currentState.Store("idle")
		case <-v.stopCh:
			// We have received a stop signal.
			v.ticker.Stop()
			return
		}
	}
}

// synchronize contains the business logic for the vectorizer:
// 1. Checks the source for changes.
// 2. Calls the embedder for new/modified files.
// 3. Saves the results to KektorDB.
func (v *Vectorizer) synchronize() {
	sourcePath := v.config.Source.Path
	if v.config.Source.Type != "filesystem" {
		log.Printf("Vectorizer '%s': source type '%s' is not supported.", v.config.Name, v.config.Source.Type)
		return
	}

	// 1. Find files that have changed since the last synchronization.
	changedFiles, err := v.findChangedFiles(sourcePath)
	if err != nil {
		log.Printf("ERROR in Vectorizer '%s': failed to scan source: %v", v.config.Name, err)
		return
	}

	if len(changedFiles) == 0 {
		log.Printf("Vectorizer '%s': No new or modified files found.", v.config.Name)
		return
	}

	log.Printf("Vectorizer '%s': Found %d files to process.", v.config.Name, len(changedFiles))

	// 2. Process each changed file.
	var successful, failed int
	for _, filePath := range changedFiles {
		err := v.processFile(filePath)
		if err != nil {
			log.Printf("ERROR in Vectorizer '%s': failed to process file '%s': %v", v.config.Name, filePath, err)
			failed++
		} else {
			successful++
		}
	}

	log.Printf("Vectorizer '%s': Synchronization complete. Successful: %d, Failed: %d.", v.config.Name, successful, failed)

}

// Stop gracefully stops the worker
func (v *Vectorizer) Stop() {
	close(v.stopCh)
}

// HELPER FUNCTIONS

// findChangedFiles scans a directory and returns a list of files
// that are new or have been modified since the last check.
func (v *Vectorizer) findChangedFiles(root string) ([]string, error) {
	var changedFiles []string

	// filepath.Walk is a standard Go function for recursively visiting a directory.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Ignore directories.
		if info.IsDir() {
			return nil
		}

		// --- State Logic ---
		// We use the KV store to store the last processed timestamp for each file.
		// The key will be unique to this vectorizer and this file.
		stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, path)

		lastModTimeBytes, found := v.server.db.GetKVStore().Get(stateKey)

		lastModTime := int64(0)
		if found {
			lastModTime, _ = strconv.ParseInt(string(lastModTimeBytes), 10, 64)
		}

		currentModTime := info.ModTime().UnixNano()

		// If the file has been modified (or is new), we add it to the list.
		if currentModTime > lastModTime {
			changedFiles = append(changedFiles, path)
		}

		return nil
	})

	return changedFiles, err

}

// GetStatus returns the current status of the vectorizer.
func (v *Vectorizer) GetStatus() VectorizerStatus {
	return VectorizerStatus{
		Name:         v.config.Name,
		IsRunning:    true, // If the object exists, it is considered running.
		LastRun:      v.lastRun,
		CurrentState: v.currentState.Load().(string),
	}
}

// processFile handles the complete workflow for a single file.
func (v *Vectorizer) processFile(filePath string) error {
	log.Printf("  -> Processing '%s'...", filePath)

	// 1. Read the file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}

	textContent := string(content)

	// Get chunking parameters from the configuration.
	// Use sensible defaults if they are not specified.
	chunkSize := v.config.DocProcessor.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 500 // Default to 500 characters
	}
	overlapSize := chunkSize / 10 // Default to 10% overlap

	// Split the document into chunks.
	chunks := text.FixedSizeChunker(textContent, chunkSize, overlapSize)
	log.Printf("     -> File split into %d chunks.", len(chunks))

	idx, ok := v.server.db.GetVectorIndex(v.config.KektorIndex)
	if !ok {
		log.Printf("Vectorizer '%s': destination index '%s' not found. Attempting to auto-create...", v.config.Name, v.config.KektorIndex)

		// Define default parameters for auto-creation.
		metric := distance.Cosine
		precision := distance.Float32
		textLang := "english"
		m := 16               // Reasonable default
		efConstruction := 200 // Reasonable default

		// --- AOF FIX FOR VCREATE ---
		// Write the create command to the AOF BEFORE creating the index.
		aofCommand := formatCommandAsRESP("VCREATE",
			[]byte(v.config.KektorIndex),
			[]byte("METRIC"), []byte(metric),
			[]byte("PRECISION"), []byte(precision),
			[]byte("M"), []byte(strconv.Itoa(m)),
			[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efConstruction)),
			[]byte("TEXT_LANGUAGE"), []byte(textLang),
		)
		v.server.aofMutex.Lock()
		v.server.aofFile.WriteString(aofCommand)
		v.server.aofMutex.Unlock()
		atomic.AddInt64(&v.server.dirtyCounter, 1)

		// Now create the index in memory.
		err := v.server.db.CreateVectorIndex(v.config.KektorIndex, metric, m, efConstruction, precision, textLang)
		if err != nil {
			return fmt.Errorf("failed to auto-create index '%s': %w", v.config.KektorIndex, err)
		}

		idx, _ = v.server.db.GetVectorIndex(v.config.KektorIndex)
		log.Printf("Index '%s' auto-created.", v.config.KektorIndex)
	}

	// Retrieve file info once.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("could not get file info for '%s': %w", filePath, err)
	}
	modTimeStr := fileInfo.ModTime().Format(time.RFC3339) // Standard ISO 8601 format

	// --- CHUNK LOOP ---
	var failedChunks int
	for _, chunk := range chunks {
		// Get the embedding for the chunk's content.
		vector, err := v.getEmbedding(chunk.Content)
		if err != nil {
			// If a chunk fails, log the error but continue with the others.
			log.Printf("ERROR (Chunk %d): Could not get embedding for '%s': %v", chunk.ChunkNumber, filePath, err)
			failedChunks++
			continue
		}

		// Create a unique ID for the chunk.
		vectorID := fmt.Sprintf("%s-chunk-%d", filePath, chunk.ChunkNumber)

		// Build the metadata.
		metadata := map[string]interface{}{
			"source_file":   filePath,
			"chunk_number":  chunk.ChunkNumber,
			"content_chunk": chunk.Content, // Store the chunk text for RAG applications.
		}

		// Apply the user-defined template.
		for key, valueTpl := range v.config.MetadataTemplate {
			val := valueTpl // Start with the template value.
			val = strings.ReplaceAll(val, "{{file_path}}", filePath)
			val = strings.ReplaceAll(val, "{{mod_time}}", modTimeStr)
			val = strings.ReplaceAll(val, "{{chunk_num}}", strconv.Itoa(chunk.ChunkNumber))
			metadata[key] = val
		}

		// Add the vector and metadata to KektorDB.
		internalID, err := idx.Add(vectorID, vector)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				// If the ID already exists, we are updating it.
				// For now, we'll just skip it.
				continue
			}
			log.Printf("ERROR: Could not add vector for chunk %d of '%s': %v", chunk.ChunkNumber, filePath, err)
			continue
		}
		if err := v.server.db.AddMetadata(v.config.KektorIndex, internalID, metadata); err != nil {
			idx.Delete(vectorID) // Rollback
			log.Printf("ERROR: Could not add metadata for chunk %d of '%s': %v", chunk.ChunkNumber, filePath, err)
			continue
		}

		// --- AOF FIX FOR VADD ---
		// After the in-memory operation succeeds, write to the AOF.
		vectorStr := float32SliceToString(vector)
		metadataBytes, _ := json.Marshal(metadata)

		aofCommand := formatCommandAsRESP("VADD",
			[]byte(v.config.KektorIndex),
			[]byte(vectorID),
			[]byte(vectorStr),
			metadataBytes,
		)
		v.server.aofMutex.Lock()
		v.server.aofFile.WriteString(aofCommand)
		v.server.aofMutex.Unlock()

		atomic.AddInt64(&v.server.dirtyCounter, 1)
	}
	// --- END LOOP ---

	// Update the state in the KV store for the ENTIRE file.
	info, _ := os.Stat(filePath)
	stateKey := fmt.Sprintf("_vectorizer_state:%s:%s", v.config.Name, filePath)
	newModTime := fmt.Sprintf("%d", info.ModTime().UnixNano())

	aofCommand := formatCommandAsRESP("SET",
		[]byte(stateKey),
		[]byte(newModTime),
	)
	v.server.aofMutex.Lock()
	v.server.aofFile.WriteString(aofCommand)
	v.server.aofMutex.Unlock()

	v.server.db.GetKVStore().Set(stateKey, []byte(newModTime))
	atomic.AddInt64(&v.server.dirtyCounter, 1)

	// If all chunks failed, return an error for the entire file.
	if failedChunks == len(chunks) && len(chunks) > 0 {
		return fmt.Errorf("failed to get embedding for all %d chunks", len(chunks))
	}

	return nil

}
