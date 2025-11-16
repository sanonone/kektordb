// Package types defines shared data structures used across the KektorDB application.
//
// This includes common types for search results, internal candidates for algorithms like HNSW,
// and data transfer objects (DTOs) for API responses, such as IndexInfo.

package types

import "github.com/sanonone/kektordb/pkg/core/distance"

// SearchResult represents a single result from a query, including its score.
// This is typically used as a public-facing or cross-package result type.
type SearchResult struct {
	DocID uint32
	Score float64
}

// Candidate is the internal struct used by the HNSW algorithm to manage potential
// results, containing an internal ID and a distance score.
type Candidate struct {
	Id       uint32
	Distance float64
}

// NodeData is a data transfer object (DTO) used to transport the data of a single
// node out of the HNSW package in a structured way.
type NodeData struct {
	ID         string
	InternalID uint32
	Vector     []float32
	Metadata   map[string]interface{}
}

// IndexInfo models the public-facing information about a vector index,
// typically for use in API responses.
type IndexInfo struct {
	Name           string                  `json:"name"`
	Metric         distance.DistanceMetric `json:"metric"`
	Precision      distance.PrecisionType  `json:"precision"`
	M              int                     `json:"m"`
	EfConstruction int                     `json:"ef_construction"`
	VectorCount    int                     `json:"vector_count"`
	TextLanguage   string                  `json:"text_language"`
}

// BatchObject definisce la struttura per un singolo elemento in un inserimento batch.
type BatchObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}
