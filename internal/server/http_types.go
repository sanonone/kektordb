package server

import (
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/engine"
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
	AutoLinks      []hnsw.AutoLinkRule         `json:"auto_links,omitempty"`
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
	IndexName        string             `json:"index_name"`
	K                int                `json:"k"`
	QueryVector      []float32          `json:"query_vector"`
	Filter           string             `json:"filter,omitempty"`
	EfSearch         int                `json:"ef_search,omitempty"`
	Alpha            float64            `json:"alpha,omitempty"`
	IncludeRelations []string           `json:"include_relations,omitempty"`
	HydrateRelations bool               `json:"hydrate_relations,omitempty"`
	GraphFilter      *engine.GraphQuery `json:"graph_filter,omitempty"`
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
	SourceID            string                 `json:"source_id"`
	TargetID            string                 `json:"target_id"`
	RelationType        string                 `json:"relation_type"` // e.g. "parent", "next", "cited_by"
	InverseRelationType string                 `json:"inverse_relation_type,omitempty"`
	Weight              float32                `json:"weight,omitempty"` // Default 1.0 if 0
	Props               map[string]interface{} `json:"props,omitempty"`
}

type GraphUnlinkRequest struct {
	SourceID            string `json:"source_id"`
	TargetID            string `json:"target_id"`
	RelationType        string `json:"relation_type"`
	InverseRelationType string `json:"inverse_relation_type,omitempty"`
	HardDelete          bool   `json:"hard_delete,omitempty"` // Default false (Soft)
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

// GraphGetEdgesRequest is used to fetch rich edges and perform time travel.
type GraphGetEdgesRequest struct {
	SourceID     string `json:"source_id"`     // Required for Forward
	TargetID     string `json:"target_id"`     // Required for Incoming
	RelationType string `json:"relation_type"` // Required
	Direction    string `json:"direction"`     // "out" (default) or "in"
	AtTime       int64  `json:"at_time"`       // Optional: Unix Nano timestamp. 0 = Now.
}

// GraphGetEdgesResponse returns the list of rich edges.
type GraphGetEdgesResponse struct {
	Edges []engine.GraphEdge `json:"edges"`
}

type GraphTraverseRequest struct {
	IndexName string   `json:"index_name"`
	SourceID  string   `json:"source_id"`
	Paths     []string `json:"paths"` // e.g. ["parent.child"]
}

type GraphGetIncomingRequest struct {
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

type GraphGetIncomingResponse struct {
	TargetID     string   `json:"target_id"`
	RelationType string   `json:"relation_type"`
	Sources      []string `json:"sources"`
}

type GraphExtractSubgraphRequest struct {
	IndexName string   `json:"index_name"`
	RootID    string   `json:"root_id"`
	Relations []string `json:"relations"` // List of relation types to follow
	MaxDepth  int      `json:"max_depth"`
	AtTime    int64    `json:"at_time"`
}

type GraphSetPropertiesRequest struct {
	IndexName  string                 `json:"index_name"`
	NodeID     string                 `json:"node_id"`
	Properties map[string]interface{} `json:"properties"`
}

type GraphGetPropertiesRequest struct {
	IndexName string `json:"index_name"`
	NodeID    string `json:"node_id"`
}

type GraphSearchNodesRequest struct {
	IndexName      string `json:"index_name"`
	PropertyFilter string `json:"property_filter"` // e.g. "type='person'"
	Limit          int    `json:"limit"`
}

// Response uses engine.SubgraphResult directly

type UIExploreRequest struct {
	IndexName string `json:"index_name"`
	Limit     int    `json:"limit"`
}
