// Package core provides the fundamental data structures and logic for the KektorDB engine.
//
// This file defines the VectorIndex interface, which establishes the contract for all
// vector index implementations within the database. It also includes a basic
// BruteForceIndex implementation, which serves as a simple, unoptimized baseline
// for vector search operations.

package core

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// --- VectorIndex Interface ---

// VectorIndex defines the operations that a vector index must support.
// This interface allows for different index implementations (e.g., Brute Force, HNSW)
// to be used interchangeably within the database.
type VectorIndex interface {
	// Add inserts a new vector with a unique external ID into the index.
	// It returns an internal ID used for efficient lookups.
	Add(id string, vector []float32) (uint32, error)
	// Delete removes a vector from the index using its external ID.
	Delete(id string)
	// SearchWithScores finds the K nearest neighbors to a query vector,
	// returning their internal IDs and scores (distances).
	// It supports pre-filtering with an allowList and index-specific search parameters like efSearch.
	SearchWithScores(query []float32, k int, allowList map[uint32]struct{}, efSearch int) []types.SearchResult

	// Metric returns the distance metric used by the index (e.g., Euclidean, Cosine).
	Metric() distance.DistanceMetric
	// Precision returns the data type precision used for storing vectors (e.g., float32, int8).
	Precision() distance.PrecisionType
	Close() error
}

// Close for BruteForceIndex is a no-op since it only uses GC RAM.
func (idx *BruteForceIndex) Close() error {
	return nil
}

// --- BruteForceIndex Implementation ---

// BruteForceIndex is a simple implementation of VectorIndex that stores all vectors
// and calculates the distance to every one of them during a search. It is not
// optimized for performance but is useful for testing and small datasets.
type BruteForceIndex struct {
	mu          sync.RWMutex
	vectors     map[string][]float32
	internalIDs map[string]uint32
	counter     uint32
}

// NewBruteForceIndex creates and returns a new, empty BruteForceIndex.
func NewBruteForceIndex() *BruteForceIndex {
	return &BruteForceIndex{
		vectors:     make(map[string][]float32),
		internalIDs: make(map[string]uint32),
	}
}

// Add adds a vector to the index
func (idx *BruteForceIndex) Add(id string, vector []float32) (uint32, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.vectors[id]; exists {
		return 0, fmt.Errorf("ID '%s' already exists", id)
	}

	idx.vectors[id] = vector

	// Although the brute-force index doesn't strictly use internal IDs
	// for graph navigation, we generate them for interface consistency.
	idx.counter++
	internalID := idx.counter
	idx.internalIDs[id] = internalID

	return internalID, nil
}

// searchResult is a helper struct for sorting search candidates.
type searchResult struct {
	id       string
	distance float64
}

// SearchWithScores finds the K nearest vectors to the query vector.
func (idx *BruteForceIndex) SearchWithScores(query []float32, k int, allowList map[uint32]struct{}, efSearch int) []types.SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	results := make([]searchResult, 0, len(idx.vectors))

	for id, vec := range idx.vectors {
		// Currently uses squared Euclidean distance for performance, as it avoids
		// the square root calculation and does not change the relative order of results.
		dist, err := squaredEuclideanDistance(query, vec)
		if err == nil { // ignore vectors of different dimensions
			results = append(results, searchResult{id: id, distance: dist})

		}
	}

	// sort results by increasing distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	var finalResults []types.SearchResult
	count := 0
	for _, res := range results { // 'results' is the sorted slice of candidates
		if count >= k {
			break
		}

		internalID := idx.internalIDs[res.id]
		_, ok := allowList[internalID]
		if allowList == nil || ok {
			finalResults = append(finalResults, types.SearchResult{DocID: internalID, Score: res.distance})
			count++
		}
	}
	return finalResults

}

// Delete removes a vector by its ID.
func (idx *BruteForceIndex) Delete(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.vectors, id)
}

// squaredEuclideanDistance is a helper function to calculate the squared Euclidean distance.
func squaredEuclideanDistance(v1, v2 []float32) (float64, error) {
	// Euclidean distance is only defined for vectors of the same dimension.
	if len(v1) != len(v2) {
		// return 0, math.ErrUnsupported
		return 0, errors.New("squaredEuclideanDistance: vectors must have the same length")
	}
	var sum float64
	for i := range v1 {
		diff := float64(v1[i] - v2[i])
		sum += diff * diff
	}
	return sum, nil
}
