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
	MemoryConfig   *hnsw.MemoryConfig          `json:"memory_config,omitempty"`
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

type VectorImportCommitRequest struct {
	IndexName string `json:"index_name"`
}

type BatchGetVectorsRequest struct {
	IndexName string   `json:"index_name"`
	IDs       []string `json:"ids"`
}

// VectorSearchRequest defines the body for search operations.
type VectorSearchRequest struct {
	IndexName        string             `json:"index_name"`
	K                int                `json:"k"`
	QueryVector      []float32          `json:"query_vector,omitempty"` // Optional - if empty/missing and filter provided, performs filter-only search
	Filter           string             `json:"filter,omitempty"`
	EfSearch         int                `json:"ef_search,omitempty"`
	Alpha            float64            `json:"alpha,omitempty"`
	IncludeRelations []string           `json:"include_relations,omitempty"`
	HydrateRelations bool               `json:"hydrate_relations,omitempty"`
	GraphFilter      *engine.GraphQuery `json:"graph_filter,omitempty"`
}

// VectorSearchWithScoresRequest defines the body for search with scores operations.
type VectorSearchWithScoresRequest struct {
	IndexName   string    `json:"index_name"`
	K           int       `json:"k"`
	QueryVector []float32 `json:"query_vector"`
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

type VectorReinforceRequest struct {
	IndexName string   `json:"index_name"`
	IDs       []string `json:"ids"`
}

type TriggerMaintenanceRequest struct {
	Type string `json:"type"` // "vacuum" or "refine"
}

type RagRetrieveRequest struct {
	PipelineName      string `json:"pipeline_name"`
	Query             string `json:"query"`
	K                 int    `json:"k"`                            // Default 10
	IncludeProvenance bool   `json:"include_provenance,omitempty"` // NEW: include source attribution
}

type GraphLinkRequest struct {
	IndexName           string                 `json:"index_name"`
	SourceID            string                 `json:"source_id"`
	TargetID            string                 `json:"target_id"`
	RelationType        string                 `json:"relation_type"` // e.g. "parent", "next", "cited_by"
	InverseRelationType string                 `json:"inverse_relation_type,omitempty"`
	Weight              float32                `json:"weight,omitempty"` // Default 1.0 if 0
	Props               map[string]interface{} `json:"props,omitempty"`
}

type GraphUnlinkRequest struct {
	IndexName           string `json:"index_name"`
	SourceID            string `json:"source_id"`
	TargetID            string `json:"target_id"`
	RelationType        string `json:"relation_type"`
	InverseRelationType string `json:"inverse_relation_type,omitempty"`
	HardDelete          bool   `json:"hard_delete,omitempty"` // Default false (Soft)
}

type GraphGetLinksRequest struct {
	IndexName    string `json:"index_name"`
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
	IndexName    string `json:"index_name"`
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
	IndexName    string `json:"index_name"`
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
	// If GuideVector is provided, the traversal only follows nodes semantically similar to this vector.
	GuideVector       []float32 `json:"guide_vector,omitempty"`
	SemanticThreshold float64   `json:"semantic_threshold,omitempty"` // E.g., 0.5 for Cosine distance
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

type GraphFindPathRequest struct {
	IndexName string   `json:"index_name"`
	SourceID  string   `json:"source_id"`
	TargetID  string   `json:"target_id"`
	Relations []string `json:"relations"`
	MaxDepth  int      `json:"max_depth"`
	AtTime    int64    `json:"at_time"` // Optional: find path as it existed in the past
}

// --- Graph Discovery (All Relations) ---

type GraphGetAllRelationsRequest struct {
	IndexName string `json:"index_name"`
	NodeID    string `json:"node_id"`
}

type GraphGetAllRelationsResponse struct {
	NodeID    string              `json:"node_id"`
	Relations map[string][]string `json:"relations"`
}

// Response uses engine.SubgraphResult directly

type UIExploreRequest struct {
	IndexName string `json:"index_name"`
	Limit     int    `json:"limit"`
}

type UpdateAutoLinksRequest struct {
	Rules []hnsw.AutoLinkRule `json:"rules"`
}

type UpdateAutoLinksResponse struct {
	Status string `json:"status"`
}

type GetAutoLinksResponse struct {
	Rules []hnsw.AutoLinkRule `json:"rules"`
}

type ExportVectorItem struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

type ExportVectorsResponse struct {
	Data       []ExportVectorItem `json:"data"`
	HasMore    bool               `json:"has_more"`
	NextOffset int                `json:"next_offset"`
	TotalCount int                `json:"total_count"`
}

// --- Cognitive Engine REST API ---

type ResolveReflectionRequest struct {
	Resolution string `json:"resolution"`
	DiscardID  string `json:"discard_id,omitempty"` // ID of the memory to archive
}

type ReflectionItem struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

type GetReflectionsResponse struct {
	Reflections []ReflectionItem `json:"reflections"`
}

// --- Source Attribution & Provenance ---

// GraphPathNode represents a node in the provenance path
type GraphPathNode struct {
	ID    string `json:"id"`    // Node ID
	Type  string `json:"type"`  // "chunk", "document", "entity"
	Label string `json:"label"` // Human-readable label
}

// GraphPathEdge represents an edge in the provenance path
type GraphPathEdge struct {
	Source   string `json:"source"`   // Source node ID
	Target   string `json:"target"`   // Target node ID
	Relation string `json:"relation"` // Relation type
}

// GraphPath represents the complete path structure
type GraphPath struct {
	Nodes     []GraphPathNode `json:"nodes"`     // Traversed nodes
	Edges     []GraphPathEdge `json:"edges"`     // Traversed edges
	Formatted string          `json:"formatted"` // String version "a → b → c"
}

// SourceAttribution represents a source with full provenance
type SourceAttribution struct {
	ChunkID    string    `json:"chunk_id"`              // Chunk ID
	DocumentID string    `json:"document_id"`           // Parent document ID
	SourceFile string    `json:"source_file"`           // Full file path
	Filename   string    `json:"filename"`              // Just filename
	ChunkIndex int       `json:"chunk_index"`           // Position in document
	PageNumber int       `json:"page_number,omitempty"` // PDF page (if available)
	Content    string    `json:"content"`               // Chunk text
	Relevance  float64   `json:"relevance"`             // Relevance score
	GraphDepth int       `json:"graph_depth"`           // Depth in graph
	GraphPath  GraphPath `json:"graph_path,omitempty"`  // Path structure
	Verified   bool      `json:"verified"`              // Path verified
}

// --- Adaptive Context Retrieval ---

// RagAdaptiveRetrieveRequest represents a request for adaptive RAG retrieval with graph-aware context expansion.
type RagAdaptiveRetrieveRequest struct {
	PipelineName      string  `json:"pipeline_name"`
	Query             string  `json:"query"`
	K                 int     `json:"k,omitempty"`
	MaxTokens         int     `json:"max_tokens,omitempty"`
	Strategy          string  `json:"strategy,omitempty"` // "greedy", "density", "graph"
	ExpansionDepth    int     `json:"expansion_depth,omitempty"`
	SemanticWeight    float64 `json:"semantic_weight,omitempty"`
	GraphWeight       float64 `json:"graph_weight,omitempty"`
	DensityWeight     float64 `json:"density_weight,omitempty"`
	CharsPerToken     float64 `json:"chars_per_token,omitempty"`
	IncludeProvenance bool    `json:"include_provenance,omitempty"` // NEW: include source attribution
}

// RagAdaptiveRetrieveResponse represents the response from adaptive RAG retrieval.
type RagAdaptiveRetrieveResponse struct {
	ContextText    string              `json:"context_text"`
	ChunksUsed     int                 `json:"chunks_used"`
	TotalTokens    int                 `json:"total_tokens"`
	DocumentsUsed  int                 `json:"documents_used"`
	Sources        []SourceAttribution `json:"sources,omitempty"` // NEW: source attribution
	Provenance     bool                `json:"provenance"`        // NEW: provenance calculated?
	ExpansionStats struct {
		SeedChunks     int `json:"seed_chunks"`
		ExpandedChunks int `json:"expanded_chunks"`
		TotalEvaluated int `json:"total_evaluated"`
	} `json:"expansion_stats"`
}

// --- Enhanced RAG Response ---

// RagRetrieveResponse extended with source attribution
type RagRetrieveResponse struct {
	Results     []string            `json:"results,omitempty"` // Legacy compatibility
	Response    string              `json:"response"`          // Assembled text
	Sources     []SourceAttribution `json:"sources"`           // Sources with provenance
	Confidence  float64             `json:"confidence"`        // Average confidence
	TotalTokens int                 `json:"total_tokens"`      // Total tokens
	Provenance  bool                `json:"provenance"`        // Provenance calculated?
}

// --- User Profile Types ---

// UserProfileResponse represents a user's personality profile
type UserProfileResponse struct {
	UserID             string   `json:"user_id"`
	CommunicationStyle string   `json:"communication_style,omitempty"`
	Language           string   `json:"language,omitempty"`
	ExpertiseAreas     []string `json:"expertise_areas,omitempty"`
	Dislikes           []string `json:"dislikes,omitempty"`
	ResponseLength     string   `json:"response_length,omitempty"`
	Confidence         float64  `json:"confidence"`
	LastUpdated        int64    `json:"last_updated"`
	ProfileData        string   `json:"profile_data,omitempty"`
}

// UserProfileListResponse represents a list of user profiles
type UserProfileListResponse struct {
	Profiles []UserProfileItem `json:"profiles"`
	Count    int               `json:"count"`
}

// UserProfileItem is a lightweight profile item for listing
type UserProfileItem struct {
	UserID             string  `json:"user_id"`
	CommunicationStyle string  `json:"communication_style,omitempty"`
	Confidence         float64 `json:"confidence"`
	LastUpdated        int64   `json:"last_updated"`
}
