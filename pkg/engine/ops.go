// This file implements the operational methods of the Engine, wrapping core
// database actions (KV set/get, Vector add/search) with persistence logic.
// It ensures that every modification is written to the Append-Only File (AOF)
// before or after being applied to the in-memory state, maintaining data durability.
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// --- KV Operations ---

// KVSet stores a key-value pair in the database.
// The operation is persisted to the AOF log.
func (e *Engine) KVSet(key string, value []byte) error {
	// 1. AOF
	cmd := persistence.FormatCommand("SET", []byte(key), value)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error (AOF write failed): %w", err)
	}

	// 2. Memory
	e.DB.GetKVStore().Set(key, value)

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// KVGet retrieves a value from the key-value store.
// Returns the value and a boolean indicating if the key was found.
func (e *Engine) KVGet(key string) ([]byte, bool) {
	return e.DB.GetKVStore().Get(key)
}

// KVDelete removes a key from the key-value store.
// The operation is persisted to the AOF log.
func (e *Engine) KVDelete(key string) error {
	cmd := persistence.FormatCommand("DEL", []byte(key))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error (AOF write failed): %w", err)
	}
	e.DB.GetKVStore().Delete(key)

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// --- Vector Operations ---

// VCreate initializes a new vector index with the specified configuration.
//
// 'metric' defines the distance function (e.g., distance.Cosine, distance.Euclidean).
// 'prec' defines the storage precision (float32, float16, int8).
// 'lang' enables hybrid search features (e.g., "english", "italian") or "" to disable.
//
// Returns an error if an index with the same name already exists.
func (e *Engine) VCreate(name string, metric distance.DistanceMetric, m, efC int, prec distance.PrecisionType, lang string) error {
	cmd := persistence.FormatCommand("VCREATE",
		[]byte(name),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(m)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efC)),
		[]byte("PRECISION"), []byte(prec),
		[]byte("TEXT_LANGUAGE"), []byte(lang),
	)
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	err := e.DB.CreateVectorIndex(name, metric, m, efC, prec, lang)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)

		// Instant flush for single operations (durability)
		if errF := e.AOF.Flush(); errF != nil {
			return fmt.Errorf("CRITICAL: persistence flush failed: %w", errF)
		}

	}
	return err
}

// VDeleteIndex completely removes an index and all its data.
// The operation is persisted to the AOF log as a VDROP command.
func (e *Engine) VDeleteIndex(name string) error {
	cmd := persistence.FormatCommand("VDROP", []byte(name))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	err := e.DB.DeleteVectorIndex(name)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)

		// Instant flush for single operations (durability)
		if errF := e.AOF.Flush(); errF != nil {
			return fmt.Errorf("CRITICAL: persistence flush failed: %w", errF)
		}

	}
	return err
}

// --- Vector Data Operations ---

// VAdd inserts or updates a single vector in the specified index.
//
// This operation updates the in-memory graph immediately and appends the operation
// to the AOF (Append-Only File) for durability.
//
// 'metadata' can be nil. If provided, it enables filtering and hybrid search.
func (e *Engine) VAdd(indexName, id string, vector []float32, metadata map[string]any) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// 1. Memory Add
	internalID, err := idx.Add(id, vector)
	if err != nil {
		return err
	}

	// 2. Metadata
	if len(metadata) > 0 {
		e.DB.AddMetadataUnlocked(indexName, internalID, metadata)
	}

	// 3. Persistence
	vecStr := float32SliceToString(vector)
	var metaBytes []byte
	if len(metadata) > 0 {
		metaBytes, _ = json.Marshal(metadata)
	}

	cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(id), []byte(vecStr), metaBytes)
	if err := e.AOF.Write(cmd); err != nil {
		// Warn: Persistence failed but memory success. Inconsistency risk.
		return fmt.Errorf("CRITICAL: persistence failed (data in RAM only): %w", err)
	}

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VDelete marks a vector as deleted in the specified index.
// The node remains in the graph but is excluded from search results.
func (e *Engine) VDelete(indexName, id string) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// Memory
	idx.Delete(id)

	// Disk
	cmd := persistence.FormatCommand("VDEL", []byte(indexName), []byte(id))
	if err := e.AOF.Write(cmd); err != nil {
		return fmt.Errorf("persistence error: %w", err)
	}

	// Instant flush for single operations (durability)
	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("CRITICAL: persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VGet retrieves the full data (vector + metadata) for a single ID.
func (e *Engine) VGet(indexName, id string) (core.VectorData, error) {
	return e.DB.GetVector(indexName, id)
}

// VGetMany retrieves data for multiple IDs in parallel.
func (e *Engine) VGetMany(indexName string, ids []string) ([]core.VectorData, error) {
	return e.DB.GetVectors(indexName, ids)
}

// VSearch performs a vector, text, or hybrid search.
//
// 'k': number of results to return.
// 'filter': supports SQL-like metadata filtering and "CONTAINS(field, 'text')".
// 'efSearch': tuning parameter for HNSW accuracy (0 = default).
// 'alpha': weight for hybrid fusion (1.0 = vector only, 0.0 = text only, 0.5 = balanced).
//
// Returns a list of external IDs sorted by relevance.
func (e *Engine) VSearch(indexName string, query []float32, k int, filter string, efSearch int, alpha float64) ([]string, error) {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("index is not HNSW, hybrid search not supported")
	}

	// 1. Parse Filters
	booleanFilters, textQuery, textQueryField := parseHybridFilter(filter)

	// 2. Boolean Pre-Filtering
	var allowList map[uint32]struct{}
	var err error
	if booleanFilters != "" {
		allowList, err = e.DB.FindIDsByFilter(indexName, booleanFilters)
		if err != nil {
			return nil, fmt.Errorf("invalid filter: %w", err)
		}
		if len(allowList) == 0 {
			return []string{}, nil // No matches
		}
	}

	// 3. Check if Vector Query is empty
	isVectorQueryEmpty := true
	if len(query) > 0 {
		for _, v := range query {
			if v != 0 {
				isVectorQueryEmpty = false
				break
			}
		}
	}

	// --- CASE A: TEXT ONLY (or Filter Only) ---
	if isVectorQueryEmpty && textQuery != "" {
		textResults, _ := e.DB.FindIDsByTextSearch(indexName, textQueryField, textQuery)

		finalIDs := make([]string, 0, k)
		count := 0
		for _, res := range textResults {
			if count >= k {
				break
			}
			if allowList != nil {
				if _, ok := allowList[res.DocID]; !ok {
					continue
				}
			}
			extID, _ := hnswIndex.GetExternalID(res.DocID)
			finalIDs = append(finalIDs, extID)
			count++
		}
		return finalIDs, nil
	}

	// --- CASE B: HYBRID / VECTOR SEARCH ---
	var vectorResults []types.SearchResult
	var textResults []types.SearchResult
	var wg sync.WaitGroup

	// Vector Search
	wg.Add(1)
	go func() {
		defer wg.Done()
		vectorResults = idx.SearchWithScores(query, k, allowList, efSearch)
	}()

	// Text Search (if applicable)
	if textQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, _ := e.DB.FindIDsByTextSearch(indexName, textQueryField, textQuery)

			// Apply allowList to text results too
			if allowList != nil {
				var filtered []types.SearchResult
				for _, res := range results {
					if _, ok := allowList[res.DocID]; ok {
						filtered = append(filtered, res)
					}
				}
				textResults = filtered
			} else {
				textResults = results
			}
		}()
	}
	wg.Wait()

	// 4. Fusion
	if textQuery == "" {
		// Pure Vector Search return
		ids := make([]string, len(vectorResults))
		for i, r := range vectorResults {
			ids[i], _ = hnswIndex.GetExternalID(r.DocID)
		}
		return ids, nil
	}

	// Hybrid Fusion (Weighted Sum)
	if alpha < 0 || alpha > 1 {
		alpha = 0.5 // Default safe fallback
	}

	normalizeVectorScores(vectorResults)
	normalizeTextScores(textResults)

	fusedScores := make(map[uint32]float64)
	for _, res := range vectorResults {
		fusedScores[res.DocID] += alpha * res.Score
	}
	for _, res := range textResults {
		fusedScores[res.DocID] += (1 - alpha) * res.Score
	}

	// 5. Sort & Format
	finalResults := make([]fusedResult, 0, len(fusedScores))
	for id, score := range fusedScores {
		extID, found := hnswIndex.GetExternalID(id)
		if !found {
			continue
		}
		finalResults = append(finalResults, fusedResult{id: extID, score: score})
	}

	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].score > finalResults[j].score
	})

	// Top K
	ids := make([]string, 0, k)
	for i := 0; i < k && i < len(finalResults); i++ {
		ids = append(ids, finalResults[i].id)
	}

	return ids, nil
}

// VAddBatch inserts multiple vectors concurrently into an index.
// It writes to the AOF log for each vector, ensuring durability but with higher I/O overhead.
// Use VImport for faster initial loading.
func (e *Engine) VAddBatch(indexName string, items []types.BatchObject) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("not hnsw")
	}

	// 1. Memory Batch
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Persistence Loop & Metadata
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id := hnswIdx.GetInternalID(item.Id)
			e.DB.AddMetadata(indexName, id, item.Metadata) // Use thread-safe version for concurrent vectorizer workers
		}

		vecStr := float32SliceToString(item.Vector)
		var meta []byte
		if len(item.Metadata) > 0 {
			meta, _ = json.Marshal(item.Metadata)
		}

		cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(item.Id), []byte(vecStr), meta)

		if err := e.AOF.Write(cmd); err != nil {
			return fmt.Errorf("batch persistence partial failure: %w", err)
		}
	}

	if err := e.AOF.Flush(); err != nil {
		return fmt.Errorf("batch persistence flush failed: %w", err)
	}

	atomic.AddInt64(&e.dirtyCounter, int64(len(items)))
	return nil
}

// VImport performs a high-speed bulk insertion of vectors.
//
// Unlike VAddBatch, this method bypasses the AOF log to maximize throughput.
// It creates a full database Snapshot (.kdb) upon completion.
//
// Use this method for initial dataset loading or massive restores.
// Note: This operation acquires a global administrative lock to ensure consistency during the snapshot.
func (e *Engine) VImport(indexName string, items []types.BatchObject) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("index is not HNSW")
	}

	// Block administrative operations (like other saves/rewrites) during import
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// 1. Mass insertion in memory (HNSW)
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Add Metadata
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id := hnswIdx.GetInternalID(item.Id)
			e.DB.AddMetadata(indexName, id, item.Metadata)
		}
	}

	// 3. Immediate Snapshot (Bulk Persistence)
	// Uses the private version that assumes adminMu is already held.
	if err := e.saveSnapshotLocked(); err != nil {
		return fmt.Errorf("import memory success but snapshot failed: %w", err)
	}

	return nil
}

// VCompress changes the precision of an existing index (e.g., float32 -> int8).
// This operation rebuilds the index in memory.
func (e *Engine) VCompress(indexName string, newPrecision distance.PrecisionType) error {
	// Acquisisce il lock sul DB tramite il metodo esposto da Core, se necessario,
	// oppure delega a DB.Compress che gestisce i suoi lock.
	return e.DB.Compress(indexName, newPrecision)
}
