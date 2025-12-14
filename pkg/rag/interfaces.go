package rag

import (
	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// Store defines the storage requirements for the RAG pipeline.
// It needs to save vectors and track file processing state (KV).
type Store interface {
	// Vector Ops
	AddBatch(indexName string, items []types.BatchObject) error
	Delete(indexName string, id string) error
	CreateVectorIndex(name string, metric string, m int, efC int, precision string, lang string) error
	// IndexExists checks if the index is already there
	IndexExists(name string) bool

	// State Ops (to avoid reprocessing identical files)
	SetState(key string, value []byte) error
	GetState(key string) ([]byte, bool)

	// Search returns the IDs of the nearest neighbors
	Search(indexName string, query []float32, k int) ([]string, error)
	// GetMany retrieves full data (including metadata) for vectors
	GetMany(indexName string, ids []string) ([]core.VectorData, error)

	// Graph Ops
	Link(sourceID, targetID, relationType string) error
}
