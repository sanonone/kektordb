package server

import (
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// VectorCreateRequest defines the body for index creation.
type VectorCreateRequest struct {
	IndexName      string                      `json:"index_name"`
	Metric         string                      `json:"metric,omitempty"`
	M              int                         `json:"m,omitempty"`
	EfConstruction int                         `json:"ef_construction,omitempty"`
	Precision      string                      `json:"precision,omitempty"`
	TextLanguage   string                      `json:"text_language,omitempty"`
	Maintenance    *hnsw.AutoMaintenanceConfig `json:"maintenance,omitempty"`
}

// VectorAddRequest defines the body for adding a single vector.
type VectorAddRequest struct {
	IndexName string         `json:"index_name"`
	Id        string         `json:"id"`
	Vector    []float32      `json:"vector"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// BatchAddVectorsRequest defines the body for batch vector addition.
type BatchAddVectorsRequest struct {
	IndexName string              `json:"index_name"`
	Vectors   []types.BatchObject `json:"vectors"`
}

type BatchGetVectorsRequest struct {
	IndexName string   `json:"index_name"`
	IDs       []string `json:"ids"`
}

// VectorSearchRequest defines the body for search operations.
type VectorSearchRequest struct {
	IndexName        string    `json:"index_name"`
	K                int       `json:"k"`
	QueryVector      []float32 `json:"query_vector"`
	Filter           string    `json:"filter,omitempty"`
	EfSearch         int       `json:"ef_search,omitempty"`
	Alpha            float64   `json:"alpha,omitempty"`
	IncludeRelations []string  `json:"include_relations,omitempty"`
	HydrateRelations bool      `json:"hydrate_relations,omitempty"`
}

// VectorDeleteRequest defines the body for vector deletion.
type VectorDeleteRequest struct {
	IndexName string `json:"index_name"`
	Id        string `json:"id"`
}

// VectorCompressRequest defines the body for index compression.
type VectorCompressRequest struct {
	IndexName string `json:"index_name"`
	Precision string `json:"precision"`
}

type TriggerMaintenanceRequest struct {
	Type string `json:"type"` // "vacuum" or "refine"
}

type RagRetrieveRequest struct {
	PipelineName string `json:"pipeline_name"`
	Query        string `json:"query"`
	K            int    `json:"k"` // Default 10
}

type GraphLinkRequest struct {
	SourceID            string `json:"source_id"`
	TargetID            string `json:"target_id"`
	RelationType        string `json:"relation_type"` // e.g. "parent", "next", "cited_by"
	InverseRelationType string `json:"inverse_relation_type,omitempty"`
}

type GraphGetLinksRequest struct {
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
}

type GraphGetConnectionsRequest struct {
	IndexName    string `json:"index_name"`
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
}

type GraphTraverseRequest struct {
	IndexName string   `json:"index_name"`
	SourceID  string   `json:"source_id"`
	Paths     []string `json:"paths"` // e.g. ["parent.child"]
}
