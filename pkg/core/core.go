// Package core provides the fundamental data structures and logic for the KektorDB engine.
//
// This file defines the main DB struct, which orchestrates all data storage, including
// the KV store, vector indexes, and secondary indexes for metadata (inverted, B-Tree, and text).
// It also implements core functionalities like snapshotting, filtering, full-text search,
// and index management.
package core

import (
	"encoding/gob"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/textanalyzer"
	"github.com/tidwall/btree"
	"io"
	"log"
	"math"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// --- AOF Compaction Management ---

// KVPair is a struct for returning key-value pairs.
type KVPair struct {
	Key   string
	Value []byte
}

// VectorData is a struct for returning the complete data of a single vector.
type VectorData struct {
	ID       string         `json:"id"`
	Vector   []float32      `json:"vector"`
	Metadata map[string]any `json:"metadata"`
}

// IndexInfo models the public-facing information about a vector index,
// intended for serialization in API responses.
type IndexInfo struct {
	Name           string                  `json:"name"`
	Metric         distance.DistanceMetric `json:"metric"`
	Precision      distance.PrecisionType  `json:"precision"`
	M              int                     `json:"m"`
	EfConstruction int                     `json:"ef_construction"`
	VectorCount    int                     `json:"vector_count"`
	TextLanguage   string                  `json:"text_language"`
}

// --- SNAPSHOTTING ---

// Snapshot represents the complete serializable state of the database.
type Snapshot struct {
	KVData     map[string][]byte
	VectorData map[string]*IndexSnapshot
}

// IndexSnapshot represents the serializable state of a single vector index.
// It includes the index configuration and all its node data.
type IndexSnapshot struct {
	Config             IndexConfig
	Nodes              map[uint32]*NodeSnapshot // Using a map for simpler serialization/deserialization.
	ExternalToInternal map[string]uint32
	InternalCounter    uint32
	EntrypointID       uint32
	MaxLevel           int
	QuantizerState     *distance.Quantizer // Saves the quantizer's state.
	QuantizedNorms     []float32
}

// NodeSnapshot contains all the necessary data to restore a single node.
type NodeSnapshot struct {
	NodeData *hnsw.Node             // The graph data (ID, connections, etc.)
	Metadata map[string]interface{} // The associated metadata

	// Explicitly typed vector data to prevent gob from converting to float64 on decode.
	VectorF32 []float32 `json:"vector_f32,omitempty"`
	VectorF16 []uint16  `json:"vector_f16,omitempty"`
	VectorI8  []int8    `json:"vector_i8,omitempty"`
}

// IndexConfig holds the configuration parameters for a vector index.
type IndexConfig struct {
	Metric         distance.DistanceMetric
	Precision      distance.PrecisionType
	M              int
	EfConstruction int
	TextLanguage   string // e.g., "english", "italian", or "" to disable
}

// Snapshot serializes the current state of the store in gob format to an io.Writer.
// This function expects the caller to handle locking.
func (s *DB) Snapshot(writer io.Writer) error {
	// --- PHASE 1: Acquire all necessary READ locks ---

	// 1a. Lock the DB to get a stable list of indexes.
	s.mu.RLock()

	// 1b. Lock the KV store.
	s.kvStore.RLock()

	// 1c. Lock each individual HNSW index.
	// Create a list of indexes to unlock later.
	indexesToUnlock := make([]*hnsw.Index, 0, len(s.vectorIndexes))
	for _, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.RLock()
			// log.Printf("Snapshot acquired read lock on index %s", name)
			indexesToUnlock = append(indexesToUnlock, hnswIndex)
		}
	}

	// --- PHASE 2: Release the highest-level lock ---
	// Now that we have locked all child data structures, we can release the lock
	// on the parent list, allowing some operations (like GetVectorIndex) to proceed.
	s.mu.RUnlock()

	// --- PHASE 3: Ensure final unlocking of everything else ---
	defer func() {
		s.kvStore.RUnlock()
		for _, idx := range indexesToUnlock {
			idx.RUnlock()
		}
	}()

	// --- PHASE 4: Execute the Snapshot (now 100% safe) ---
	// At this point, we hold an RLock on the KVStore and on each HNSWIndex.
	// No write operations (`SET`, `VADD`) can occur. We can safely read everything.

	snapshot := Snapshot{
		KVData:     s.kvStore.data, // Safe because we hold the RLock
		VectorData: make(map[string]*IndexSnapshot),
	}

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := hnswIndex.SnapshotData()

			metric, m, efc, precision, _, textLang := hnswIndex.GetInfoUnlocked()

			nodeSnapshots := make(map[uint32]*NodeSnapshot, len(nodes))
			for internalID, node := range nodes {
				// Create the node snapshot
				snap := &NodeSnapshot{
					NodeData: node,
					Metadata: s.getMetadataForNodeUnlocked(name, internalID),
				}

				// Use a type switch to populate the correct vector field
				switch vec := node.Vector.(type) {
				case []float32:
					snap.VectorF32 = vec
				case []uint16:
					snap.VectorF16 = vec
				case []int8:
					snap.VectorI8 = vec
				default:
					// Log a warning if we encounter an unexpected type
					log.Printf("WARNING: Unknown vector type '%T' during snapshot of node %d", vec, internalID)
				}
				nodeSnapshots[internalID] = snap
			}

			snapshot.VectorData[name] = &IndexSnapshot{
				Config: IndexConfig{
					Metric:         metric,
					Precision:      precision,
					M:              m,
					EfConstruction: efc,
					TextLanguage:   textLang,
				},
				Nodes:              nodeSnapshots,
				ExternalToInternal: extToInt,
				InternalCounter:    counter,
				EntrypointID:       entrypoint,
				MaxLevel:           maxLevel,
				QuantizerState:     quantizer,
				QuantizedNorms:     norms,
			}
		}
	}

	encoder := gob.NewEncoder(writer)
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	return nil
}

// LoadFromSnapshot deserializes a gob snapshot from an io.Reader and restores
// the store's state. It clears the current store state before loading.
func (s *DB) LoadFromSnapshot(reader io.Reader) error {
	decoder := gob.NewDecoder(reader)
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return fmt.Errorf("failed to decode snapshot: %w", err)
	}

	// Clear the current state for a clean load
	s.kvStore.data = snapshot.KVData
	if s.kvStore.data == nil {
		s.kvStore.data = make(map[string][]byte)
	}
	s.vectorIndexes = make(map[string]VectorIndex)
	s.invertedIndex = make(map[string]map[string]map[string]map[uint32]struct{})
	s.bTreeIndex = make(map[string]map[string]*btree.BTreeG[BTreeItem])
	s.textIndex = make(InvertedIndex)
	s.textIndexStats = make(map[string]map[string]*TextIndexStats)

	// Iterate over the indexes in the snapshot
	for name, indexSnap := range snapshot.VectorData {
		// Create a new empty index with the saved configuration
		idx, err := hnsw.New(indexSnap.Config.M, indexSnap.Config.EfConstruction, indexSnap.Config.Metric, indexSnap.Config.Precision, indexSnap.Config.TextLanguage)
		if err != nil {
			return fmt.Errorf("failed to recreate index '%s' from snapshot: %w", name, err)
		}

		// Prepare the map of nodes to load into HNSW
		nodesToLoad := make(map[uint32]*hnsw.Node)

		// --- Reconstruct the 'Vector' field ---
		for id, nodeSnap := range indexSnap.Nodes {
			// Reconstruct the generic 'Vector' (interface{}) field
			// based on which of the typed fields is populated.
			if nodeSnap.VectorF32 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorF32
			} else if nodeSnap.VectorF16 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorF16
			} else if nodeSnap.VectorI8 != nil {
				nodeSnap.NodeData.Vector = nodeSnap.VectorI8
			} else {
				log.Printf("WARNING: No vector data found for node %d in snapshot", id)
			}
			nodesToLoad[id] = nodeSnap.NodeData
		}

		// Load the HNSW graph data
		if err := idx.LoadSnapshotData(nodesToLoad, indexSnap.ExternalToInternal, indexSnap.InternalCounter, indexSnap.EntrypointID, indexSnap.MaxLevel, indexSnap.QuantizerState, indexSnap.QuantizedNorms); err != nil {
			return fmt.Errorf("failed to load HNSW data for index '%s': %w", name, err)
		}

		s.vectorIndexes[name] = idx

		// Initialize spaces for secondary indexes
		s.invertedIndex[name] = make(map[string]map[string]map[uint32]struct{})
		s.bTreeIndex[name] = make(map[string]*btree.BTreeG[BTreeItem])
		s.textIndex[name] = make(map[string]map[string]PostingList)
		s.textIndexStats[name] = make(map[string]*TextIndexStats)

		// Rebuild secondary indexes (metadata)
		for _, nodeSnap := range indexSnap.Nodes {
			if len(nodeSnap.Metadata) > 0 {
				s.AddMetadataUnlocked(name, nodeSnap.NodeData.InternalID, nodeSnap.Metadata)
			}
		}
	}

	return nil
}

// IterateKV iterates over all key-value pairs in the store, passing each to a callback function.
// The iteration is performed under a read lock.
func (s *DB) IterateKV(callback func(pair KVPair)) {
	s.kvStore.mu.RLock()
	defer s.kvStore.mu.RUnlock()

	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// iterateKVUnlocked performs the iteration without acquiring locks.
// The caller is responsible for ensuring thread safety.
func (s *DB) IterateKVUnlocked(callback func(pair KVPair)) {
	for key, value := range s.kvStore.data {
		callback(KVPair{Key: key, Value: value})
	}
}

// IterateVectorIndexes iterates over all vector indexes and their contents.
// The caller is responsible for locking.
func (s *DB) IterateVectorIndexes(callback func(indexName string, index *hnsw.Index, data VectorData)) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.Iterate(func(id string, vector []float32) {
				// retrieve data from the main store
				internalID := hnswIndex.GetInternalID(id)
				metadata := s.getMetadataForNode(name, internalID)

				callback(name, hnswIndex, VectorData{
					ID:       id,
					Vector:   vector,
					Metadata: metadata,
				})
			})
		}
	}
}

// iterateVectorIndexesUnlocked performs the iteration without acquiring locks.
// The caller is responsible for ensuring thread safety.
func (s *DB) IterateVectorIndexesUnlocked(callback func(indexName string, index *hnsw.Index, data VectorData)) {
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			hnswIndex.Iterate(func(id string, vector []float32) {
				internalID := hnswIndex.GetInternalID(id)
				metadata := s.getMetadataForNodeUnlocked(name, internalID)

				callback(name, hnswIndex, VectorData{
					ID:       id,
					Vector:   vector,
					Metadata: metadata,
				})
			})
		}
	}
}

// GetVectorIndexInfo returns a slice containing the configuration and status
// of all existing vector indexes.
func (s *DB) GetVectorIndexInfo() ([]IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				Precision:      precision,
				M:              m,
				EfConstruction: efConst,
				VectorCount:    count,
				TextLanguage:   textLang,
			})
		}
	}

	return infoList, nil
}

// GetVectorIndexInfoUnlocked returns index information without acquiring a lock.
// The caller is responsible for ensuring thread safety.
func (s *DB) GetVectorIndexInfoUnlocked() ([]IndexInfo, error) {
	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))
	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				M:              m,
				EfConstruction: efConst,
				TextLanguage:   textLang,
				Precision:      precision,
				VectorCount:    count,
			})
		}
	}
	return infoList, nil
}

// GetVectorIndexInfoAPI returns configuration and status information for all vector indexes,
// suitable for an API response.
func (s *DB) GetVectorIndexInfoAPI() ([]IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infoList := make([]IndexInfo, 0, len(s.vectorIndexes))

	for name, idx := range s.vectorIndexes {
		if hnswIndex, ok := idx.(*hnsw.Index); ok {
			metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo() // Creeremo questo metodo
			infoList = append(infoList, IndexInfo{
				Name:           name,
				Metric:         metric,
				Precision:      precision,
				M:              m,
				EfConstruction: efConst,
				VectorCount:    count,
				TextLanguage:   textLang,
			})
		}
	}

	// Sort the list by name for a consistent API response.
	sort.Slice(infoList, func(i, j int) bool {
		return infoList[i].Name < infoList[j].Name
	})

	return infoList, nil
}

// GetSingleVectorIndexInfoAPI returns information for a single index by name.
func (s *DB) GetSingleVectorIndexInfoAPI(name string) (IndexInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.vectorIndexes[name]
	if !ok {
		return IndexInfo{}, fmt.Errorf("index '%s' not found", name)
	}

	if hnswIndex, ok := idx.(*hnsw.Index); ok {
		metric, m, efConst, precision, count, textLang := hnswIndex.GetInfo()
		return IndexInfo{
			Name:           name,
			Metric:         metric,
			Precision:      precision,
			M:              m,
			EfConstruction: efConst,
			VectorCount:    count,
			TextLanguage:   textLang,
		}, nil
	}

	return IndexInfo{}, fmt.Errorf("index type not supported for introspection")
}

// GetVector retrieves the complete data for a single vector given its external ID.
func (s *DB) GetVector(indexName, vectorID string) (VectorData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return VectorData{}, fmt.Errorf("index '%s' not found", indexName)
	}

	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return VectorData{}, fmt.Errorf("index type not supported for data retrieval")
	}

	nodeData, found := hnswIndex.GetNodeData(vectorID)
	if !found {
		return VectorData{}, fmt.Errorf("vector with ID '%s' not found in index '%s'", vectorID, indexName)
	}

	metadata := s.getMetadataForNodeUnlocked(indexName, nodeData.InternalID)

	return VectorData{
		ID:       vectorID,
		Vector:   nodeData.Vector,
		Metadata: metadata,
	}, nil
}

// GetVectors retrieves the complete data for a slice of vector IDs in parallel.
// If an ID is not found, it is simply omitted from the returned slice.
func (s *DB) GetVectors(indexName string, vectorIDs []string) ([]VectorData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}
	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("unsupported index type")
	}

	// --- PARALLELISM LOGIC ---

	// Channel to distribute work (IDs to fetch)
	jobs := make(chan string, len(vectorIDs))
	// Channel to collect results
	resultsChan := make(chan VectorData, len(vectorIDs))
	// WaitGroup to know when all workers are done
	var wg sync.WaitGroup

	// Determine the number of workers. number of available CPUs as a reasonable limit.
	numWorkers := runtime.NumCPU()
	if len(vectorIDs) < numWorkers {
		numWorkers = len(vectorIDs)
	}
	if numWorkers == 0 {
		return []VectorData{}, nil
	}

	// Start Workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker takes an ID from the jobs channel, processes it, and sends the result
			for vectorID := range jobs {
				nodeData, found := hnswIndex.GetNodeData(vectorID)
				if !found {
					continue
				}
				metadata := s.getMetadataForNodeUnlocked(indexName, nodeData.InternalID)

				resultsChan <- VectorData{
					ID:       vectorID,
					Vector:   nodeData.Vector,
					Metadata: metadata,
				}
			}
		}()
	}

	// Send Jobs
	for _, id := range vectorIDs {
		jobs <- id
	}
	close(jobs) // Close the channel to signal workers that there are no more jobs

	// Wait for all workers to finish
	wg.Wait()
	close(resultsChan)

	// Collect all results
	finalResults := make([]VectorData, 0, len(resultsChan))
	for result := range resultsChan {
		finalResults = append(finalResults, result)
	}

	return finalResults, nil
}

// getMetadataForNode is a helper function to retrieve metadata for a given node ID.
// Note: This implementation is inefficient as it scans the entire inverted index.
func (s *DB) getMetadataForNode(indexName string, nodeID uint32) map[string]any {
	metadata := make(map[string]any)

	// scans the inverted index
	if invIdx, ok := s.invertedIndex[indexName]; ok {
		for key, valueMap := range invIdx {
			for value, idSet := range valueMap {
				if _, exists := idSet[nodeID]; exists {
					metadata[key] = value
				}

			}

		}
	}

	return metadata
}

// getMetadataForNodeUnlocked is the lock-free version of getMetadataForNode.
func (s *DB) getMetadataForNodeUnlocked(indexName string, nodeID uint32) map[string]any {
	metadata := make(map[string]any)

	// Scan the inverted index
	if invIdx, ok := s.invertedIndex[indexName]; ok {
		for key, valueMap := range invIdx {
			for value, idSet := range valueMap {
				if _, exists := idSet[nodeID]; exists {
					metadata[key] = value
				}

			}

		}
	}

	return metadata
}

// BTreeItem is a struct used by the B-Tree to associate a numerical metadata value with a node ID.
type BTreeItem struct {
	Value  float64
	NodeID uint32
}

// PostingEntry contains the document ID and term frequency for an entry in a posting list.
type PostingEntry struct {
	DocID         uint32
	TermFrequency int
}

// PostingList is a slice of PostingEntry structs.
type PostingList []PostingEntry

// InvertedIndex is the data structure for full-text search.
// It maps a token (word) to a list of documents containing it.
// Structure: map[vector_index_name] -> map[metadata_key] -> map[token] -> PostingList
type InvertedIndex map[string]map[string]map[string]PostingList

// TextIndexStats holds statistics for a text index, used by ranking algorithms like BM25.
type TextIndexStats struct {
	// Total number of documents in an indexed field
	TotalDocs int
	// Average length of a field across all documents
	AvgFieldLength float64
	// Map of DocID -> field length
	DocLengths map[uint32]int
}

// DB is the main container for all KektorDB data types. It orchestrates the KV store,
// vector indexes, and all secondary indexes for metadata.
type DB struct {
	mu            sync.RWMutex
	kvStore       *KVStore
	vectorIndexes map[string]VectorIndex

	// invertedIndex for metadata filtering.
	// structure: map[vector index name] -> map[metadata key] -> map[metadata value] -> set[internal node IDs]
	// e.g., invertedIndex["my_images"]["tags"]["cat"]={1: {}, 5: {}, 34:{}}
	// meaning that in the "my_images" index, nodes with internal IDs 1, 5, and 34 have the metadata "tags" with the value "cat".
	invertedIndex map[string]map[string]map[string]map[uint32]struct{}

	// bTreeIndex for numerical metadata.
	// structure: map[vector index name] -> map[metadata key] -> BTree
	// The B-Tree stores BTreeItems, allowing for fast range queries.
	bTreeIndex     map[string]map[string]*btree.BTreeG[BTreeItem]
	textIndex      InvertedIndex
	textIndexStats map[string]map[string]*TextIndexStats // map[indexName][fieldName] -> stats
}

// NewDB creates and returns a new, initialized DB instance.
func NewDB() *DB {
	return &DB{
		kvStore:        NewKVStore(),
		vectorIndexes:  make(map[string]VectorIndex),
		invertedIndex:  make(map[string]map[string]map[string]map[uint32]struct{}),
		bTreeIndex:     make(map[string]map[string]*btree.BTreeG[BTreeItem]),
		textIndex:      make(InvertedIndex),
		textIndexStats: make(map[string]map[string]*TextIndexStats),
	}
}

// GetKVStore returns the underlying key-value store
func (s *DB) GetKVStore() *KVStore {
	return s.kvStore
}

// CreateVectorIndex creates a new vector index with the specified configuration.
func (s *DB) CreateVectorIndex(name string, metric distance.DistanceMetric, m, efConstruction int, precision distance.PrecisionType, textLang string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.vectorIndexes[name]; ok {
		return fmt.Errorf("index '%s' already exists", name)
	}

	const (
		defaultM       = 16
		defaultEfConst = 200
	)
	idx, err := hnsw.New(m, efConstruction, metric, precision, textLang)
	if err != nil {
		return err
	}

	s.vectorIndexes[name] = idx

	// Initialize the metadata space for this new index.
	s.invertedIndex[name] = make(map[string]map[string]map[uint32]struct{})
	s.bTreeIndex[name] = make(map[string]*btree.BTreeG[BTreeItem])
	s.textIndex[name] = make(map[string]map[string]PostingList)
	s.textIndexStats[name] = make(map[string]*TextIndexStats)

	return nil
}

// GetVectorIndex retrieves a vector index by its name.
func (s *DB) GetVectorIndex(name string) (VectorIndex, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, found := s.vectorIndexes[name]
	return idx, found
}

// DeleteVectorIndex removes an entire vector index and all of its associated data.
func (s *DB) DeleteVectorIndex(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.vectorIndexes[name]
	if !ok {
		return fmt.Errorf("index '%s' not found", name)
	}

	delete(s.vectorIndexes, name)

	delete(s.invertedIndex, name)

	delete(s.bTreeIndex, name)

	delete(s.textIndex, name)

	delete(s.textIndexStats, name)

	log.Printf("Index '%s' and all associated data have been deleted.", name)
	return nil
}

// Compress converts an existing index to a new precision
func (s *DB) Compress(indexName string, newPrecision distance.PrecisionType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldIndex, ok := s.vectorIndexes[indexName]
	if !ok {
		return fmt.Errorf("index '%s' not found", indexName)
	}

	// Controlla che sia un indice HNSW
	oldHNSWIndex, ok := oldIndex.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("compression is only supported for HNSW indexes")
	}

	// We collect RAW data. We expect them to be []float32
	// because we only compress from float32
	type rawData struct {
		ID       string
		Vector   []float32
		Metadata map[string]interface{}
	}
	var allVectors []rawData

	oldHNSWIndex.IterateRaw(func(id string, vector interface{}) {
		// We make a type assertion to be sure
		if vecF32, ok := vector.([]float32); ok {
			internalID := oldHNSWIndex.GetInternalID(id)
			metadata := s.getMetadataForNodeUnlocked(indexName, internalID)
			allVectors = append(allVectors, rawData{
				ID:       id,
				Vector:   vecF32,
				Metadata: metadata,
			})
		}
	})

	if len(allVectors) == 0 {
		return fmt.Errorf("Cannot compress an empty index '%s'. Operation skipped.", indexName)
	}

	metric, m, efConst := oldHNSWIndex.GetParameters()

	textLang := oldHNSWIndex.TextLanguage()

	newIndex, err := hnsw.New(m, efConst, metric, newPrecision, textLang)
	if err != nil {
		return fmt.Errorf("failed to create new compressed index: %w", err)
	}

	if newPrecision == distance.Int8 {
		floatVectors := make([][]float32, len(allVectors))
		for i, data := range allVectors {
			floatVectors[i] = data.Vector
		}

		newIndex.TrainQuantizer(floatVectors)

	}

	// log.Printf("[DEBUG COMPRESS] Clearing secondary indexes for '%s'", indexName)
	s.invertedIndex[indexName] = make(map[string]map[string]map[uint32]struct{})
	s.bTreeIndex[indexName] = make(map[string]*btree.BTreeG[BTreeItem])
	//    s.metadataStore[indexName] = make(map[uint32]map[string]interface{})

	// log.Printf("[DEBUG COMPRESS] Repopulating new index and secondary indexes...")
	for _, data := range allVectors {
		internalID, err := newIndex.Add(data.ID, data.Vector)
		if err != nil {
			log.Printf("WARNING: Failed to add vector %s during compression: %v", data.ID, err)
			continue
		}
		// Re-associate metadata
		if len(data.Metadata) > 0 {
			s.AddMetadataUnlocked(indexName, internalID, data.Metadata)
		}
	}

	s.vectorIndexes[indexName] = newIndex

	log.Printf("Index '%s' successfully compressed to precision '%s'", indexName, newPrecision)
	return nil
}

// AddMetadata associates metadata with a node ID and updates the secondary indexes.
func (s *DB) AddMetadata(indexName string, nodeID uint32, metadata map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return nil
	} // L'indice non esiste, non fare nulla

	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil
	}

	var analyzer textanalyzer.Analyzer
	switch hnswIndex.TextLanguage() {
	case "english":
		analyzer = textanalyzer.NewEnglishStemmer()
	case "italian":
		analyzer = textanalyzer.NewItalianStemmer()
	default:
	}

	for key, value := range metadata {
		switch v := value.(type) {
		case string:
			indexMetadata, ok := s.invertedIndex[indexName]
			if !ok {
				return fmt.Errorf("metadata index for '%s' not found", indexName)
			}
			if _, ok := indexMetadata[key]; !ok {
				indexMetadata[key] = make(map[string]map[uint32]struct{})
			}
			if _, ok := indexMetadata[key][v]; !ok {
				indexMetadata[key][v] = make(map[uint32]struct{})
			}
			indexMetadata[key][v][nodeID] = struct{}{}

			if analyzer != nil {
				tokens := analyzer.Analyze(v)

				if _, ok := s.textIndex[indexName][key]; !ok {
					s.textIndex[indexName][key] = make(map[string]PostingList)
				}
				if _, ok := s.textIndexStats[indexName][key]; !ok {
					s.textIndexStats[indexName][key] = &TextIndexStats{
						DocLengths: make(map[uint32]int),
					}
				}

				stats := s.textIndexStats[indexName][key]

				if _, docExists := stats.DocLengths[nodeID]; !docExists {
					stats.TotalDocs++
				}
				stats.DocLengths[nodeID] = len(tokens)

				termFrequencies := make(map[string]int)
				for _, token := range tokens {
					termFrequencies[token]++
				}

				for token, freq := range termFrequencies {
					list := s.textIndex[indexName][key][token]

					found := false
					for _, entry := range list {
						if entry.DocID == nodeID {
							found = true
							break
						}
					}
					if !found {
						s.textIndex[indexName][key][token] = append(list, PostingEntry{
							DocID:         nodeID,
							TermFrequency: freq,
						})
					}
				}
			}

		case float64:
			// --- NUOVA LOGICA B-TREE ---
			indexBTree, ok := s.bTreeIndex[indexName]
			if !ok {
				return fmt.Errorf("b-tree index for '%s' not found", indexName)
			}

			if _, ok := indexBTree[key]; !ok {
				indexBTree[key] = btree.NewBTreeG[BTreeItem](btreeItemLess)
			}

			indexBTree[key].Set(BTreeItem{Value: v, NodeID: nodeID})

		default:
			continue
		}
	}

	s.recalculateAvgFieldLengths(indexName)

	return nil

}

// AddMetadataUnlocked adds metadata without acquiring a lock. The caller must ensure thread safety.
func (s *DB) AddMetadataUnlocked(indexName string, nodeID uint32, metadata map[string]any) error {

	// Get the index configuration to determine which text analyzer to use.
	idx, ok := s.vectorIndexes[indexName]
	if !ok {
		return nil
	}

	hnswIndex, ok := idx.(*hnsw.Index)
	if !ok {
		return nil
	}

	var analyzer textanalyzer.Analyzer
	switch hnswIndex.TextLanguage() {
	case "english":
		analyzer = textanalyzer.NewEnglishStemmer()
	case "italian":
		analyzer = textanalyzer.NewItalianStemmer()
	default:

	}

	for key, value := range metadata {
		switch v := value.(type) { // Check the type of the 'any' variable
		case string:
			// --- LOGICA INDICE INVERTITO (invariata) ---
			indexMetadata, ok := s.invertedIndex[indexName]
			if !ok {
				return fmt.Errorf("metadata index for '%s' not found", indexName)
			}
			if _, ok := indexMetadata[key]; !ok {
				indexMetadata[key] = make(map[string]map[uint32]struct{})
			}
			if _, ok := indexMetadata[key][v]; !ok {
				indexMetadata[key][v] = make(map[uint32]struct{})
			}
			indexMetadata[key][v][nodeID] = struct{}{}

			// --- FULL-TEXT INDEXING (with analyzer) ---
			if analyzer != nil {
				tokens := analyzer.Analyze(v)

				// Initialize maps if they don't exist
				if _, ok := s.textIndex[indexName][key]; !ok {
					s.textIndex[indexName][key] = make(map[string]PostingList)
				}
				if _, ok := s.textIndexStats[indexName][key]; !ok {
					s.textIndexStats[indexName][key] = &TextIndexStats{
						DocLengths: make(map[uint32]int),
					}
				}

				stats := s.textIndexStats[indexName][key]

				// --- BM25 LOGIC ---

				// Update document statistics
				if _, docExists := stats.DocLengths[nodeID]; !docExists {
					stats.TotalDocs++
				}
				stats.DocLengths[nodeID] = len(tokens)

				// 2. Calculate term frequencies (TF) for this document
				termFrequencies := make(map[string]int)
				for _, token := range tokens {
					termFrequencies[token]++
				}

				// 3. Update the inverted index with TF
				for token, freq := range termFrequencies {
					list := s.textIndex[indexName][key][token]

					// Check if the docID is already in the list to avoid duplicates
					found := false
					for _, entry := range list {
						if entry.DocID == nodeID {
							found = true
							break
						}
					}
					if !found {
						s.textIndex[indexName][key][token] = append(list, PostingEntry{
							DocID:         nodeID,
							TermFrequency: freq,
						})
					}
				}
			}

		case float64:
			// --- B-TREE LOGIC (for numerical data) ---
			indexBTree, ok := s.bTreeIndex[indexName]
			if !ok {
				return fmt.Errorf("b-tree index for '%s' not found", indexName)
			}

			// Check if a B-Tree for this key already exists; otherwise, create it
			if _, ok := indexBTree[key]; !ok {
				indexBTree[key] = btree.NewBTreeG[BTreeItem](btreeItemLess)
			}

			// Insert the item into the B-Tree
			indexBTree[key].Set(BTreeItem{Value: v, NodeID: nodeID})

		default:
			// For now, we ignore other types (bool, etc.)
			continue
		}
	}

	// Recalculate the average field length
	s.recalculateAvgFieldLengths(indexName)

	return nil
}

// recalculateAvgFieldLengths updates the average field length statistic for text indexes.
// This is necessary for BM25 ranking.
func (s *DB) recalculateAvgFieldLengths(indexName string) {
	if indexStats, ok := s.textIndexStats[indexName]; ok {
		for _, fieldStats := range indexStats {
			var totalLength int
			for _, length := range fieldStats.DocLengths {
				totalLength += length
			}
			if fieldStats.TotalDocs > 0 {
				fieldStats.AvgFieldLength = float64(totalLength) / float64(fieldStats.TotalDocs)
			}
		}
	}
}

// FindIDsByFilter acts as a query planner for metadata filters.
// It supports AND and OR logic. OR has lower precedence (the filter is first split
// by OR, and each resulting block is evaluated as an AND of its sub-filters).
func (s *DB) FindIDsByFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, fmt.Errorf("empty filter")
	}

	// Case-insensitive split for "OR" without altering the rest of the string.
	reOr := regexp.MustCompile(`(?i)\s+OR\s+`)
	orBlocks := reOr.Split(filter, -1)

	finalIDSet := make(map[uint32]struct{})

	// Case-insensitive regex for "AND".
	reAnd := regexp.MustCompile(`(?i)\s+AND\s+`)

	for _, orBlock := range orBlocks {
		orBlock = strings.TrimSpace(orBlock)
		if orBlock == "" {
			continue
		}

		// Each orBlock can contain multiple sub-filters separated by AND.
		andFilters := reAnd.Split(orBlock, -1)

		var blockIDSet map[uint32]struct{}
		isFirst := true

		for _, subFilter := range andFilters {
			subFilter = strings.TrimSpace(subFilter)
			if subFilter == "" {
				continue
			}

			currentIDSet, err := s.evaluateBooleanFilter(indexName, subFilter)
			if err != nil {
				return nil, fmt.Errorf("error in filter '%s': %w", subFilter, err)
			}

			if isFirst {
				// Defensive copy to prevent aliasing.
				blockIDSet = make(map[uint32]struct{}, len(currentIDSet))
				for id := range currentIDSet {
					blockIDSet[id] = struct{}{}
				}
				isFirst = false
			} else {
				// Intersect the current block's results with the new results.
				blockIDSet = intersectSets(blockIDSet, currentIDSet)
			}

			// Optimization: if the intersection is empty, no need to continue this AND block.
			if len(blockIDSet) == 0 {
				break
			}
		}

		// Union (OR) with the final result set.
		finalIDSet = unionSets(finalIDSet, blockIDSet)
	}

	if len(finalIDSet) == 0 {
		return make(map[uint32]struct{}), nil
	}

	return finalIDSet, nil
}

// regex to parse the CONTAINS function
var containsRegex = regexp.MustCompile(`(?i)CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

// evaluateBooleanFilter evaluates a single expression like "price >= 10" or "name = 'Alice'".
// It returns a set of matching node IDs (map[uint32]struct{}) and an error.
func (s *DB) evaluateBooleanFilter(indexName string, filter string) (map[uint32]struct{}, error) {
	filter = strings.TrimSpace(filter)

	// Find the operator (ordered by length to handle <= and >= correctly).
	var op string
	opIndex := -1
	for _, operator := range []string{"<=", ">=", "=", "<", ">"} {
		if idx := strings.Index(filter, operator); idx != -1 {
			op = operator
			opIndex = idx
			break
		}
	}
	if opIndex == -1 {
		return nil, fmt.Errorf("invalid filter format, operator not found (use =, <, >, <=, >=)")
	}

	key := strings.TrimSpace(filter[:opIndex])
	valueStr := strings.TrimSpace(filter[opIndex+len(op):])

	// Result set
	idSet := make(map[uint32]struct{})

	// Fetch the B-Tree and inverted index mappings if they exist
	indexBTree, hasBTree := s.bTreeIndex[indexName]
	indexInv, hasInv := s.invertedIndex[indexName]

	// Dispatch based on the operator
	switch op {
	case "=":
		// Try to treat the value as a number first
		numValue, err := strconv.ParseFloat(valueStr, 64)
		if err == nil && hasBTree {
			// It's a number -> use the B-Tree for this key
			tree, ok := indexBTree[key]
			if !ok {
				// The key is not indexed: return an empty result
				return make(map[uint32]struct{}), nil
			}

			// Search for elements exactly equal to numValue
			pivot := BTreeItem{Value: numValue}
			tree.Ascend(pivot, func(item BTreeItem) bool {
				if item.Value != numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
			return idSet, nil
		}

		// Otherwise, treat it as a string -> use the inverted index
		if !hasInv {
			return nil, fmt.Errorf("inverted index for '%s' not found", indexName)
		}
		keyMetadata, ok := indexInv[key]
		if !ok {
			return make(map[uint32]struct{}), nil
		}
		valSet, ok := keyMetadata[valueStr]
		if !ok {
			return make(map[uint32]struct{}), nil
		}
		// Defensive copy.
		for id := range valSet {
			idSet[id] = struct{}{}
		}
		return idSet, nil

	case "<", "<=", ">", ">=":
		// These operators are numeric-only
		numValue, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			return nil, fmt.Errorf("value for operator '%s' must be numeric: '%s'", op, valueStr)
		}
		if !hasBTree {
			return nil, fmt.Errorf("numeric index for '%s' not found", indexName)
		}

		tree, ok := indexBTree[key]
		if !ok {
			// The key is not indexed: return an empty result
			return make(map[uint32]struct{}), nil
		}

		switch op {
		case "<":
			// Ascend from -inf and take all values < numValue
			tree.Ascend(BTreeItem{Value: math.Inf(-1)}, func(item BTreeItem) bool {
				if item.Value >= numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case "<=":
			tree.Ascend(BTreeItem{Value: math.Inf(-1)}, func(item BTreeItem) bool {
				if item.Value > numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case ">":
			// Descend from +inf and take all values > numValue
			tree.Descend(BTreeItem{Value: math.Inf(+1)}, func(item BTreeItem) bool {
				if item.Value <= numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		case ">=":
			tree.Descend(BTreeItem{Value: math.Inf(+1)}, func(item BTreeItem) bool {
				if item.Value < numValue {
					return false
				}
				idSet[item.NodeID] = struct{}{}
				return true
			})
		}

		return idSet, nil

	default:
		return nil, fmt.Errorf("operator '%s' not supported", op)
	}
}

// intersectSets calculates the intersection of two sets (a ∩ b)
func intersectSets(a, b map[uint32]struct{}) map[uint32]struct{} {
	if a == nil || b == nil {
		return make(map[uint32]struct{})
	}
	// Iterate over the smaller set for efficiency
	if len(a) > len(b) {
		a, b = b, a
	}
	res := make(map[uint32]struct{})
	for id := range a {
		if _, ok := b[id]; ok {
			res[id] = struct{}{}
		}
	}
	return res
}

// unionSets calculates the union of two sets (a ∪ b)
func unionSets(a, b map[uint32]struct{}) map[uint32]struct{} {
	res := make(map[uint32]struct{}, len(a)+len(b))
	for id := range a {
		res[id] = struct{}{}
	}
	for id := range b {
		res[id] = struct{}{}
	}
	return res
}

// btreeItemLess is the less function for BTree items. It sorts items by their float64 value,
// using NodeID as a tie-breaker to ensure distinct items
func btreeItemLess(a, b BTreeItem) bool {
	if a.Value < b.Value {
		return true
	}
	if a.Value > b.Value {
		return false
	}
	// If values are equal, sort by NodeID to keep items distinct.
	return a.NodeID < b.NodeID
}

// RLock acquires a read lock on the store.
func (s *DB) RLock() {
	s.mu.RLock()
}

// RUnlock releases the read lock.
func (s *DB) RUnlock() {
	s.mu.RUnlock()
}

// Lock acquires a write lock on the store.
func (s *DB) Lock() {
	s.mu.Lock()
}

// Unlock releases the write lock.
func (s *DB) Unlock() {
	s.mu.Unlock()
}

// Standard parameters for the BM25 algorithm.
const (
	bm25k1 = 1.2
	bm25b  = 0.75
)

// FindIDsByTextSearch performs a search on the text index for a given query.
// It uses the BM25 ranking algorithm to score and sort documents based on relevance.
func (db *DB) FindIDsByTextSearch(indexName, fieldName, queryText string) ([]types.SearchResult, error) {
	idx, ok := db.vectorIndexes[indexName]
	if !ok {
		return nil, fmt.Errorf("index '%s' not found", indexName)
	}

	hnswIndex, _ := idx.(*hnsw.Index)
	var analyzer textanalyzer.Analyzer
	switch hnswIndex.TextLanguage() {
	case "english":
		analyzer = textanalyzer.NewEnglishStemmer()
	case "italian":
		analyzer = textanalyzer.NewItalianStemmer()
	default:
		return nil, fmt.Errorf("index '%s' does not have a configured text analyzer", indexName)
	}

	// 1. Analyze the query to get the search tokens.
	queryTokens := analyzer.Analyze(queryText)
	if len(queryTokens) == 0 {
		return nil, nil // Empty query, no results.
	}

	stats, ok := db.textIndexStats[indexName][fieldName]
	if !ok || stats.TotalDocs == 0 {
		return nil, nil // No documents indexed for this field
	}

	// 2. Gather all candidate documents (union of posting lists)
	candidateDocs := make(map[uint32]map[string]int) // map[docID] -> map[token] -> termFrequency
	// postingLists := make(map[string]PostingList) // Mappa di token -> lista
	textIndexForField, ok := db.textIndex[indexName][fieldName]
	if !ok {
		return nil, nil
	}

	for _, token := range queryTokens {
		list, found := textIndexForField[token]
		if found {
			for _, entry := range list {
				if _, ok := candidateDocs[entry.DocID]; !ok {
					candidateDocs[entry.DocID] = make(map[string]int)
				}
				candidateDocs[entry.DocID][token] = entry.TermFrequency
			}
		}
	}

	// 3. Calculate the BM25 score for each candidate document.
	results := make([]types.SearchResult, 0, len(candidateDocs))
	for docID, termFreqs := range candidateDocs {
		score := 0.0
		for _, token := range queryTokens {
			// Calculate the score of this token for this document.
			tf := termFreqs[token] // Will be 0 if the token is not in the document.
			score += db.calculateBM25TermScore(token, docID, tf, stats, textIndexForField)
		}
		results = append(results, types.SearchResult{DocID: docID, Score: score})
	}

	// 4. Sort the results by score in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// calculateBM25TermScore computes the BM25 relevance score for a single term within a single document.
func (db *DB) calculateBM25TermScore(token string, docID uint32, tf int, stats *TextIndexStats, textIndexForField map[string]PostingList) float64 {
	// list, ok := postingLists[token]
	list, ok := textIndexForField[token]
	if !ok {
		return 0.0
	}

	if tf == 0 {
		return 0.0
	}

	// Calculate IDF (Inverse Document Frequency).
	docFreq := len(list)
	idf := math.Log(1 + (float64(stats.TotalDocs)-float64(docFreq)+0.5)/(float64(docFreq)+0.5))

	// Calculate the term score component.
	docLen := float64(stats.DocLengths[docID])
	avgLen := stats.AvgFieldLength

	tfFloat := float64(tf)
	numerator := tfFloat * (bm25k1 + 1)
	denominator := tfFloat + bm25k1*(1-bm25b+bm25b*(docLen/avgLen))

	return idf * (numerator / denominator)
}
