// Package server implements the main KektorDB server logic.
//
// This package is responsible for:
//   - Initializing and running the main application server.
//   - Handling persistence through Append-Only Files (AOF) and snapshots (.kdb).
//   - Managing data loading and recovery on startup.
//   - Running background tasks like automatic saving (SAVE) and AOF rewriting.
//   - Managing asynchronous, long-running tasks (e.g., compression).

package server

import (
	"bufio"
	"context" // For graceful shutdown (Ctrl+C)
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic" // For the dirty counter
	"time"
)

// Server holds the core components and state for the KektorDB instance.
type Server struct {
	db *core.DB // Pointer to the core database engine

	aofFile    *os.File     // The append-only file for persistence
	aofMutex   sync.Mutex   // Mutex to manage concurrent writes to the AOF file
	httpServer *http.Server // The HTTP server instance

	// State for automatic saving
	dirtyCounter int64 // Atomic counter for tracking changes (dirty state)
	lastSaveTime time.Time

	// Configuration
	savePolicies         []savePolicy
	aofRewritePercentage int
	aofBaseSize          int64 // AOF size after the last rewrite
	taskManager          *TaskManager
	vectorizerConfig     *Config
	vectorizerService    *VectorizerService
}

// savePolicy defines a rule for triggering an automatic SAVE operation.
type savePolicy struct {
	Seconds int
	Changes int64
}

// NewServer initializes and returns a new Server instance.
// It sets up the AOF file, parses save policies, and loads vectorizer configurations.
func NewServer(aofPath string, savePolicyStr string, aofRewritePerc int, vectorizersConfigPath string) (*Server, error) {
	// open or create the AOF file
	// 0666 are the file permissions
	file, err := os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("Failed to open AOF file: %v", err)
	}

	policies, err := parseSavePolicies(savePolicyStr)
	if err != nil {
		return nil, err
	}

	// --- Load Vectorizer Configuration ---
	vecConfig, err := LoadVectorizersConfig(vectorizersConfigPath)
	if err != nil {
		return nil, err
	}
	// Print a log to confirm loading
	if len(vecConfig.Vectorizers) > 0 {
		log.Printf("Loaded %d Vectorizer configurations from file '%s'", len(vecConfig.Vectorizers), vectorizersConfigPath)
	}

	s := &Server{
		db:                   core.NewDB(),
		aofFile:              file,
		savePolicies:         policies,
		aofRewritePercentage: aofRewritePerc,
		lastSaveTime:         time.Now(),
		taskManager:          NewTaskManager(),
		vectorizerConfig:     vecConfig,
	}
	vecService, err := NewVectorizerService(s)
	if err != nil {
		// This error is not fatal; the server can start even without vectorizers
		log.Printf("WARNING: Vectorizer service failed to start: %v", err)
	}
	s.vectorizerService = vecService

	return s, nil
}

// Run starts the server. It handles the initial data loading from a snapshot and/or
// AOF file, starts background tasks, and begins listening for HTTP requests.
func (s *Server) Run(httpAddr string) error {
	// Determine the file paths
	aofPath := s.aofFile.Name()
	snapshotPath := strings.TrimSuffix(aofPath, ".aof") + ".kdb"

	// Check if a snapshot exists
	if _, err := os.Stat(snapshotPath); err == nil {
		// The snapshot exists, load it.
		log.Printf("Found snapshot file '%s', starting restore from RDB...", snapshotPath)

		file, errOpen := os.Open(snapshotPath)
		if errOpen != nil {
			return fmt.Errorf("could not open snapshot file: %w", err)
		}

		err = s.db.LoadFromSnapshot(file)
		file.Close() // Chiudi il file dopo la lettura
		if err != nil {
			return fmt.Errorf("error while loading snapshot: %w", err)
		}
		log.Println("Restore from snapshot completed.")

	} else if !os.IsNotExist(err) {
		// An error other than "file not found" occurred while checking the snapshot
		return fmt.Errorf("error checking snapshot file: %w", err)
	}

	// Always replay the AOF.
	// If we loaded the snapshot, this will apply only subsequent changes.
	// If there was no snapshot, this will load the entire history.
	log.Println("Starting AOF log replay for recent changes...")
	if err := s.loadFromAOF(); err != nil {
		return fmt.Errorf("failed to load from AOF: %w", err)
	}

	// Get the initial AOF size after loading
	info, _ := s.aofFile.Stat()
	s.aofBaseSize = info.Size()

	if s.vectorizerService != nil {
		s.vectorizerService.Start() // Start all vectorizers
	}

	// --- Start the background maintenance goroutine ---
	go s.serverCron()

	// HTTP server configuration and startup
	mux := http.NewServeMux()
	s.registerHTTPHandlers(mux) // register endpoints

	s.httpServer = &http.Server{
		Addr:    httpAddr,
		Handler: mux,
	}

	log.Printf("HTTP server listening on %s", httpAddr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server startup failed: %w", err)
	}

	return nil

}

// serverCron runs background maintenance tasks, such as automatic saving
// and AOF rewriting, at regular intervals.
func (s *Server) serverCron() {
	// Run a check every second.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Acquire a lightweight lock to safely read the counters
		s.aofMutex.Lock()
		dirty := atomic.LoadInt64(&s.dirtyCounter)
		lastSave := s.lastSaveTime
		s.aofMutex.Unlock()

		// --- Automatic SAVE Logic ---
		for _, policy := range s.savePolicies {
			if time.Since(lastSave).Seconds() >= float64(policy.Seconds) && dirty >= policy.Changes {
				log.Println("Automatic save condition met. Starting SAVE...")
				if err := s.Save(); err == nil {
					// Reset counters only if SAVE succeeds
					s.aofMutex.Lock()
					atomic.StoreInt64(&s.dirtyCounter, 0)
					s.lastSaveTime = time.Now()
					s.aofMutex.Unlock()
					log.Println("Automatic SAVE completed.")
				} else {
					log.Printf("ERROR during automatic SAVE: %v", err)
				}
				break // Execute only the first matching policy
			}
		}

		// --- Automatic AOF REWRITE Logic ---
		if s.aofRewritePercentage > 0 {
			info, err := s.aofFile.Stat()
			if err == nil {
				currentSize := info.Size()
				// If base size is 0, avoid division by zero. Use a minimum threshold.
				threshold := s.aofBaseSize + (s.aofBaseSize * int64(s.aofRewritePercentage) / 100)
				if s.aofBaseSize > 0 && currentSize > threshold {
					log.Println("Automatic AOF rewrite condition met. Starting AOF REWRITE...")
					if err := s.RewriteAOF(); err == nil {
						newInfo, _ := s.aofFile.Stat()
						s.aofBaseSize = newInfo.Size() // Update the base size
						log.Println("Automatic AOF REWRITE completed.")
					} else {
						log.Printf("ERROR during automatic AOF REWRITE: %v", err)
					}
				}
			}
		}
	}
}

// vectorIndexState holds the aggregated state of a single vector index,
// used during AOF loading.
type vectorIndexState struct {
	metric         distance.DistanceMetric
	m              int
	efConstruction int
	precision      distance.PrecisionType
	entries        map[string]vectorEntry // map[vectorID] -> entry
	textLanguage   string
}

// aofState represents the final aggregated state of the database after
// reading the entire AOF file.
type aofState struct {
	kvData        map[string][]byte
	vectorIndexes map[string]vectorIndexState // map[indexName] -> state
}

// vectorEntry holds the information for a single vector.
type vectorEntry struct {
	vector   []float32
	metadata map[string]any
}

// loadFromAOF reads the AOF file, aggregates commands into a final state,
// and then rebuilds the in-memory database from that state. This approach
// handles deletions and out-of-order commands correctly.
func (s *Server) loadFromAOF() error {
	log.Println("Caricamento e compattazione dati dal file AOF...")

	// Move the cursor to the beginning of the file for reading.
	if _, err := s.aofFile.Seek(0, 0); err != nil {
		return fmt.Errorf("seek aof: %w", err)
	}

	// --- PHASE 1 & 2: Read and Aggregate State ---
	state := &aofState{
		kvData:        make(map[string][]byte),
		vectorIndexes: make(map[string]vectorIndexState),
	}

	reader := bufio.NewReader(s.aofFile)
	for {
		cmd, err := ParseRESP(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[AOF] Error reading RESP command, potential file corruption: %v. Aborting.", err)
			// With RESP, a parsing error is more serious; it might be better to stop.
			// For now, we continue trying.
			continue
		}

		switch cmd.Name {
		case "SET":
			if len(cmd.Args) == 2 {
				state.kvData[string(cmd.Args[0])] = cmd.Args[1]
			} else {
				log.Printf("[AOF] SET with unexpected arguments (%d)", len(cmd.Args))
			}

		case "DEL":
			if len(cmd.Args) == 1 {
				delete(state.kvData, string(cmd.Args[0]))
			} else {
				log.Printf("[AOF] DEL with unexpected arguments (%d)", len(cmd.Args))
			}

		case "VDROP":
			if len(cmd.Args) == 1 {
				indexName := string(cmd.Args[0])
				// Remove the index from our temporary state map
				delete(state.vectorIndexes, indexName)
			}

		case "VCREATE":
			// Expected AOF format: VCREATE <index_name> [METRIC <metric>] [M <m_val>] [EF_CONSTRUCTION <ef_val>] [PRECISION <p>]
			if len(cmd.Args) >= 1 {
				indexName := string(cmd.Args[0])

				// Default values
				metric := distance.Euclidean
				precision := distance.Float32 // Default float32
				m := 0                        // 0 to use the default in NewHNSWIndex
				efConstruction := 0
				textLang := ""

				// Parsing optional arguments
				i := 1 // Start at the index of the first possible argument
				for i < len(cmd.Args) {
					if i+1 >= len(cmd.Args) { // Incomplete parameter
						log.Printf("[AOF] Ignored invalid VCREATE '%s': incomplete parameter", cmd.Name)
						break
					}

					key := strings.ToUpper(string(cmd.Args[i]))
					value := string(cmd.Args[i+1])

					switch key {
					case "METRIC":
						metric = distance.DistanceMetric(value)
					case "M":
						val, err := strconv.Atoi(value)
						if err == nil {
							m = val
						} // Ignore if not a valid number
					case "EF_CONSTRUCTION":
						val, err := strconv.Atoi(value)
						if err == nil {
							efConstruction = val
						} // Ignore if not a valid number
					case "PRECISION":
						precision = distance.PrecisionType(value)
					case "TEXT_LANGUAGE":
						textLang = value
					default:
						// Ignore unknown parameters
					}
					i += 2 // Advance by 2 for the next key-value pair
				}

				// Create space for this index in the state (if it doesn't already exist)
				if _, ok := state.vectorIndexes[indexName]; !ok {
					state.vectorIndexes[indexName] = vectorIndexState{
						metric:         metric,
						m:              m,
						efConstruction: efConstruction,
						precision:      precision,
						textLanguage:   textLang,
						entries:        make(map[string]vectorEntry),
					}
				}
			}

		case "VADD":
			// Expected RESP format: VADD, indexName, vectorID, vectorString, [metadataJSON]
			if len(cmd.Args) < 3 {
				log.Printf("[AOF] VADD with insufficient arguments, skipped.")
				continue
			}

			indexName := string(cmd.Args[0])
			vectorID := string(cmd.Args[1])
			vectorStr := string(cmd.Args[2])

			if _, ok := state.vectorIndexes[indexName]; !ok {
				continue // Index not created, ignore
			}

			vector, err := parseVectorFromString(vectorStr)
			if err != nil {
				log.Printf("AOF Warning: VADD for '%s' has a malformed vector, skipped. Error: %v", vectorID, err)
				continue
			}

			entry := vectorEntry{vector: vector}

			// Check if the optional metadata argument exists
			if len(cmd.Args) == 4 && len(cmd.Args[3]) > 0 {
				metadataJSON := cmd.Args[3]
				// Ignore the 'null' value that a Python client might send
				if string(metadataJSON) != "null" {
					var metadata map[string]interface{}
					if json.Unmarshal(metadataJSON, &metadata) == nil {
						entry.metadata = metadata
					}
				}
			}

			state.vectorIndexes[indexName].entries[vectorID] = entry

		case "VDEL":
			// Hard delete by removing the entry from the state map
			if len(cmd.Args) == 2 {
				indexName := string(cmd.Args[0])
				vectorID := string(cmd.Args[1])
				if index, ok := state.vectorIndexes[indexName]; ok {
					delete(index.entries, vectorID)
				}
			}

		default:
			// ignore other commands or log them if needed
			// log.Printf("[AOF] line %d: ignored command: %s", lineNo, cmd.Name)
		}
	}

	// --- PHASE 3: Rebuild the State in the Core Engine ---
	log.Println("Rebuilding compacted state in memory...")

	// Rebuild the KV store
	for key, value := range state.kvData {
		s.db.GetKVStore().Set(key, value)
	}

	// Rebuild the vector indexes
	totalVectors := 0
	addedVectors := 0
	skippedDeleted := 0
	for indexName, indexState := range state.vectorIndexes {
		log.Printf("[AOF] Rebuilding index '%s' (Metric: %s, Precision: %s) - Vectors: %d",
			indexName, indexState.metric, indexState.precision, len(indexState.entries))

		err := s.db.CreateVectorIndex(indexName, indexState.metric, indexState.m, indexState.efConstruction, indexState.precision, indexState.textLanguage)
		if err != nil {
			log.Printf("[AOF] ERROR: failed to create index '%s' with metric %s, M=%d, EF=%d: %v",
				indexName, indexState.metric, indexState.m, indexState.efConstruction, err)
			continue
		}
		idx, found := s.db.GetVectorIndex(indexName)
		if !found {
			log.Printf("[AOF] failed to get index '%s'", indexName)
			continue
		}

		for vectorID, entry := range indexState.entries {
			totalVectors++

			if entry.metadata != nil {
				if vdel, ok := entry.metadata["__deleted"]; ok {
					if b, ok := vdel.(bool); ok && b {
						skippedDeleted++
						continue
					}
				}
			}

			// If vector data is missing, we can't add it
			if entry.vector == nil || len(entry.vector) == 0 {
				log.Printf("[AOF] index '%s' id '%s': missing vector -> skip", indexName, vectorID)
				continue
			}

			internalID, err := idx.Add(vectorID, entry.vector)
			if err != nil {
				log.Printf("[AOF] Error during HNSW reconstruction for '%s' (index '%s'): %v", vectorID, indexName, err)
				continue
			}
			addedVectors++

			// add metadata (if present)
			if len(entry.metadata) > 0 {
				// rimuoviamo il flag interno prima di salvare i metadata, se presente
				if _, ok := entry.metadata["__deleted"]; ok {
					delete(entry.metadata, "__deleted")
				}
				if len(entry.metadata) > 0 {
					s.db.AddMetadataUnlocked(indexName, internalID, entry.metadata)
				}
			}
		}

		log.Printf("[AOF] index '%s' builded: added=%d, skipped=%d", indexName, addedVectors, skippedDeleted)
	}

	log.Printf("AOF loading completed. totalVectors=%d addedVectors=%d skippedDeleted=%d", totalVectors, addedVectors, skippedDeleted)
	return nil
}

// RewriteAOF compacts the AOF file by writing the current database state
// into a new temporary file and then atomically replacing the old one.
func (s *Server) RewriteAOF() error {
	log.Println("Starting AOF rewrite...")

	// Use the public lock methods of the core DB.
	s.db.Lock()
	defer s.db.Unlock()

	// the path to the directory of the original AOF file
	aofDir := filepath.Dir(s.aofFile.Name())

	tempFile, err := os.CreateTemp(aofDir, "kektordb-aof-rewrite-*.aof")
	if err != nil {
		return fmt.Errorf("could not create temporary AOF file in '%s': %w", aofDir, err)
	}
	defer os.Remove(tempFile.Name()) // Ensure cleanup on error

	// --- PHASE 1: Write State Creation Commands ---

	// 1a. Write SET commands for the KV store
	s.db.IterateKVUnlocked(func(pair core.KVPair) {
		cmd := formatCommandAsRESP("SET", []byte(pair.Key), pair.Value)
		tempFile.WriteString(cmd)
	})

	// 1b. Write VCREATE commands for each vector index
	vectorIndexInfo, err := s.db.GetVectorIndexInfoUnlocked()
	if err != nil {
		return fmt.Errorf("could not get vector index info: %w", err)
	}

	for _, info := range vectorIndexInfo {
		cmd := formatCommandAsRESP("VCREATE",
			[]byte(info.Name),
			[]byte("METRIC"), []byte(info.Metric),
			[]byte("PRECISION"), []byte(info.Precision),
			[]byte("M"), []byte(strconv.Itoa(info.M)),
			[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(info.EfConstruction)),
			[]byte("TEXT_LANGUAGE"), []byte(info.TextLanguage),
		)
		tempFile.WriteString(cmd)
	}

	// 1c. Write VADD commands for each vector in each index
	s.db.IterateVectorIndexesUnlocked(func(indexName string, index *hnsw.Index, data core.VectorData) {
		vectorStr := float32SliceToString(data.Vector)
		var metadataBytes []byte
		if len(data.Metadata) > 0 {
			metadataBytes, _ = json.Marshal(data.Metadata)
		}

		cmd := formatCommandAsRESP("VADD",
			[]byte(indexName),
			[]byte(data.ID),
			[]byte(vectorStr),
			metadataBytes, // Will be nil if no metadata, handled by formatCommandAsRESP
		)
		tempFile.WriteString(cmd)
	})

	// --- PHASE 2: Atomic File Replacement ---

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("could not close temporary AOF file: %w", err)
	}

	s.aofMutex.Lock()
	defer s.aofMutex.Unlock()

	// The path of the original AOF file
	aofPath := s.aofFile.Name()

	// Close the current AOF file before replacing it.
	if err := s.aofFile.Close(); err != nil {
		// Try to reopen it to not leave the server in an inconsistent state.
		s.aofFile, _ = os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
		return fmt.Errorf("could not close original AOF file before rewrite: %w", err)
	}

	if err := os.Rename(tempFile.Name(), aofPath); err != nil {
		s.aofFile, _ = os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
		return fmt.Errorf("failed to replace AOF file: %w", err)
	}

	newAOFFile, err := os.OpenFile(aofPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		// This is a serious error; the server might no longer be able to persist data.
		return fmt.Errorf("failed to reopen new AOF file after rewrite: %w", err)
	}
	s.aofFile = newAOFFile

	log.Println("AOF rewrite completed successfully.")
	return nil
}

// Save performs a "stop-the-world" snapshot of the database state to disk
// and then truncates the AOF file.
func (s *Server) Save() error {
	log.Println("Starting snapshot process (SAVE)...")

	// 2. Create the temporary file
	aofPath := s.aofFile.Name()
	snapshotPath := strings.TrimSuffix(aofPath, ".aof") + ".kdb"
	tempSnapshotPath := snapshotPath + ".tmp"

	file, err := os.Create(tempSnapshotPath)
	if err != nil {
		return fmt.Errorf("could not create temporary snapshot file: %w", err)
	}
	defer file.Close()
	defer os.Remove(tempSnapshotPath) // Clean up on error

	// 3. Write the snapshot to the temporary file
	log.Println("Writing in-memory state to snapshot...")
	if err := s.db.Snapshot(file); err != nil {
		return fmt.Errorf("failed during snapshot write: %w", err)
	}
	log.Println("Snapshot write completed.")

	// 4. Atomic Replacement
	if err := os.Rename(tempSnapshotPath, snapshotPath); err != nil {
		return fmt.Errorf("failed to replace snapshot file: %w", err)
	}

	// 5. Truncate the AOF file
	// This is the most delicate part. We must lock AOF writes,
	// truncate the file, and then unlock.
	log.Println("Truncating AOF file...")
	s.aofMutex.Lock()
	defer s.aofMutex.Unlock()

	// Truncate the file to size 0
	if err := s.aofFile.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate AOF file: %w", err)
	}
	// Move the cursor back to the start for new writes
	if _, err := s.aofFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to reset AOF cursor: %w", err)
	}

	log.Println("Snapshot process (SAVE) completed successfully.")
	return nil
}

// Shutdown performs a graceful shutdown of the server, closing the HTTP listener
// and the AOF file.
func (s *Server) Shutdown() {
	log.Println("Starting graceful shutdown...")

	// Stop the HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	} else {
		log.Println("HTTP server stopped.")
	}

	// Close the AOF file
	if s.aofFile != nil {
		s.aofFile.Close()
	}

	// Stop the vectorizer service
	if s.vectorizerService != nil {
		s.vectorizerService.Stop()
	}

	log.Println("Shutdown complete.")
}

// Close cleans up server resources, like closing the AOF file.
func (s *Server) Close() {
	if s.aofFile != nil {
		s.aofFile.Close()
	}
}

// parseVectorFromString parses a single string containing space-separated numbers.
func parseVectorFromString(s string) ([]float32, error) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return nil, fmt.Errorf("vector string is empty")
	}
	vector := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(part, 32)
		if err != nil {
			// Return the original error for better debugging
			return nil, err
		}
		vector[i] = float32(val)
	}
	return vector, nil
}

// parseSavePolicies parses the save policy string (e.g., "60 1000 300 10")
// and converts it into a slice of savePolicy structs.
func parseSavePolicies(policyStr string) ([]savePolicy, error) {
	policyStr = strings.TrimSpace(policyStr)
	if policyStr == "" {
		return nil, nil // No policy is a valid case.
	}

	parts := strings.Fields(policyStr)
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("invalid save policy format: odd number of arguments")
	}

	var policies []savePolicy
	for i := 0; i < len(parts); i += 2 {
		seconds, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, fmt.Errorf("invalid seconds in save policy: '%s'", parts[i])
		}

		changes, err := strconv.ParseInt(parts[i+1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number of changes in save policy: '%s'", parts[i+1])
		}

		if seconds <= 0 || changes <= 0 {
			return nil, fmt.Errorf("seconds and changes must be greater than zero")
		}

		policies = append(policies, savePolicy{
			Seconds: seconds,
			Changes: changes,
		})
	}

	// Sort policies from the most stringent to the least stringent (by time)
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Seconds < policies[j].Seconds
	})

	return policies, nil
}

// --- ASYNCHRONOUS TASK MANAGEMENT STRUCTS ---

// TaskStatus defines the possible states of a task.
type TaskStatus string

const (
	TaskStatusStarted   TaskStatus = "started"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task represents a long-running operation.
type Task struct {
	ID              string     `json:"id"`
	Status          TaskStatus `json:"status"`
	ProgressMessage string     `json:"progress_message,omitempty"`
	Error           string     `json:"error,omitempty"`
	mu              sync.RWMutex
}

// TaskManager tracks all running asynchronous tasks.
type TaskManager struct {
	tasks map[string]*Task
	mu    sync.RWMutex
}

// NewTaskManager creates a new task manager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

// NewTask creates a new task, registers it, and returns it.
func (tm *TaskManager) NewTask() *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task := &Task{
		ID:     uuid.New().String(), // Generate a unique ID
		Status: TaskStatusStarted,
	}
	tm.tasks[task.ID] = task
	return task
}

// GetTask safely retrieves a task by its ID.
func (tm *TaskManager) GetTask(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task, found := tm.tasks[id]
	return task, found
}

// --- Methods for updating a Task ---
// (These methods will be called by the background goroutine)

// SetStatus updates the status of the task.
func (t *Task) SetStatus(status TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
}

// SetError marks the task as failed and records the error message.
func (t *Task) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = TaskStatusFailed
	t.Error = err.Error()
}

// SetProgress updates the progress message for the task.
func (t *Task) SetProgress(message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ProgressMessage = message
}
