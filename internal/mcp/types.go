package mcp

// --- Tool Arguments ---

type SaveMemoryArgs struct {
	Content   string   `json:"content" jsonschema:"The text content/fact to remember,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"The index to store data in. Defaults to 'mcp_memory'"`
	Layer     string   `json:"layer,omitempty" jsonschema:"Memory layer: 'episodic' (events, default), 'semantic' (facts), or 'procedural' (rules)."`
	Links     []string `json:"links,omitempty" jsonschema:"List of existing Entity IDs to link this memory to (e.g. 'project_alpha', 'user_mario')"`
	// Tags uses any instead of []string so the MCP schema doesn't force type: array.
	// LLMs frequently send comma-separated strings; normalizeTags() handles both formats.
	Tags any  `json:"tags,omitempty" jsonschema:"Optional search tags — JSON array [\"tag1\",\"tag2\"] or comma-separated string \"tag1,tag2\""`
	Pin  bool `json:"pin,omitempty" jsonschema:"If true, this memory will never decay over time (e.g. core rules, birthdays)."`
	// ExplicitPinned allows overriding the layer's pinned_by_default setting.
	// If nil, uses layer default. If set, overrides Pin and layer default.
	ExplicitPinned *bool `json:"_pinned,omitempty" jsonschema:"Explicitly set pinned status (overrides Pin and layer defaults)"`
	// SessionID optionally links this memory to a specific session.
	// If empty and a session is active, the active session is used automatically.
	SessionID string `json:"session_id,omitempty" jsonschema:"Optional session ID to link this memory to. Auto-set if session is active."`
	// UserID explicitly sets the user for this memory (for profiling).
	// If empty and session_id is provided, user_id is propagated from session.
	UserID string `json:"user_id,omitempty" jsonschema:"User ID for profiling. Auto-propagated from session if not set."`
}

type SaveMemoryResult struct {
	MemoryID string `json:"memory_id"`
	Status   string `json:"status"`
}

type CreateEntityArgs struct {
	EntityID    string `json:"entity_id" jsonschema:"Unique ID for the conceptual entity (e.g. 'project_kektor'),required"`
	Type        string `json:"type" jsonschema:"Type of entity (e.g. 'project', 'person', 'topic'),required"`
	Description string `json:"description,omitempty" jsonschema:"Description of the entity"`
	IndexName   string `json:"index_name,omitempty"`
}

type CreateEntityResult struct {
	EntityID string `json:"entity_id"`
}

type ConnectArgs struct {
	SourceID  string `json:"source_id" jsonschema:"required"`
	TargetID  string `json:"target_id" jsonschema:"required"`
	Relation  string `json:"relation" jsonschema:"The type of relationship (e.g. 'mentions', 'author_of', 'related_to'),required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"The index to operate in. Defaults to 'mcp_memory'"`
}

type RecallArgs struct {
	Query     string `json:"query" jsonschema:"The semantic query to search for,required"`
	IndexName string `json:"index_name,omitempty"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max number of results (default 5)"`
	Reinforce bool   `json:"reinforce,omitempty" jsonschema:"If true, marks retrieved memories as 'accessed now', boosting their future relevance."`
	// Layers restricts the search to specific memory layers.
	// If empty, searches all layers with weights defined in LayerWeights.
	Layers []string `json:"layers,omitempty" jsonschema:"Filter by memory layers (e.g. ['episodic', 'semantic']). Empty = all layers."`
	// LayerWeights defines scoring weights per layer. Keys: "episodic", "semantic", "procedural".
	// Defaults: semantic=0.5, episodic=0.4, procedural=0.1
	LayerWeights map[string]float64 `json:"layer_weights,omitempty" jsonschema:"Custom weights per layer for result ranking"`
}

type ScopedRecallArgs struct {
	Query     string `json:"query" jsonschema:"The semantic query,required"`
	RootID    string `json:"root_id" jsonschema:"The Graph Node ID to restrict the search within (e.g. search only inside 'project_alpha'),required"`
	Direction string `json:"direction,omitempty" jsonschema:"Direction of traversal: 'out' (children), 'in' (parents), 'both'. Default 'out',enum=out,enum=in,enum=both"`
	Depth     int    `json:"depth,omitempty" jsonschema:"Traversal depth (default 2)"`
	Limit     int    `json:"limit,omitempty"`
	IndexName string `json:"index_name,omitempty" jsonschema:"The index to search in. Defaults to 'mcp_memory'"`
}

type RecallResult struct {
	Results []string `json:"results"` // Formatted strings for the LLM
}

type TraverseArgs struct {
	RootID     string   `json:"root_id" jsonschema:"required"`
	Relations  []string `json:"relations,omitempty" jsonschema:"Filter by relation types (e.g. ['mentions'])"`
	Depth      int      `json:"depth,omitempty" jsonschema:"Depth (default 1)"`
	GuideQuery string   `json:"guide_query,omitempty" jsonschema:"Optional text concept to guide the traversal. Only nodes semantically similar to this query will be followed."`
	Threshold  float64  `json:"threshold,omitempty" jsonschema:"Similarity threshold (0.0-1.0) for guide_query. Default 0.5."`
	AtTime     int64    `json:"at_time,omitempty" jsonschema:"Unix nanoseconds timestamp to query historical data (0 = current time)"`
}

type TraverseResult struct {
	GraphDescription string `json:"graph_description"` // Textual description of connections
}

type FindConnectionArgs struct {
	SourceID  string   `json:"source_id" jsonschema:"Start Node ID,required"`
	TargetID  string   `json:"target_id" jsonschema:"End Node ID,required"`
	Relations []string `json:"relations,omitempty" jsonschema:"Allowed relation types to traverse (optional)"`
	AtTime    int64    `json:"at_time,omitempty" jsonschema:"Unix nanoseconds timestamp to query historical data (0 = current time)"`
}

type FindConnectionResult struct {
	PathDescription string `json:"path_description"` // "A -> B -> C"
}

type FilterVectorsArgs struct {
	IndexName string `json:"index_name" jsonschema:"The index to search in. Defaults to 'mcp_memory'"`
	Filter    string `json:"filter" jsonschema:"Metadata filter expression (e.g. type='person' AND tag='important')"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max results to return (default 10)"`
}

type FilterVectorsResult struct {
	Results []string `json:"results"`
}

type UnpinMemoryArgs struct {
	IndexName string `json:"index_name" jsonschema:"Index name (defaults to 'mcp_memory')"`
	MemoryID  string `json:"memory_id" jsonschema:"The memory node ID to unpin"`
}

type UnpinMemoryResult struct {
	Status string `json:"status"`
}

type ConfigureAutoLinksArgs struct {
	IndexName string `json:"index_name" jsonschema:"Index name (defaults to 'mcp_memory')"`
	Rules     []struct {
		MetadataField string `json:"metadata_field" jsonschema:"The metadata key to link on (e.g. chat_id)"`
		RelationType  string `json:"relation_type" jsonschema:"The relation type to create (e.g. belongs_to_chat)"`
		CreateNode    bool   `json:"create_node,omitempty" jsonschema:"Whether to create a stub node if target doesn't exist"`
	} `json:"rules" jsonschema:"List of auto-linking rules"`
}

type ConfigureAutoLinksResult struct {
	Status string `json:"status"`
}

type ListVectorsArgs struct {
	IndexName string `json:"index_name" jsonschema:"Index name (defaults to 'mcp_memory')"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max results (default 50)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

type ListVectorsResult struct {
	Vectors []struct {
		ID       string         `json:"id"`
		Metadata map[string]any `json:"metadata"`
	} `json:"vectors"`
	HasMore bool `json:"has_more"`
}

// --- Session Management ---

type StartSessionArgs struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Optional session ID. Auto-generated if empty."`
	AgentID   string `json:"agent_id,omitempty" jsonschema:"ID of the agent/AI starting the session"`
	UserID    string `json:"user_id,omitempty" jsonschema:"ID of the user being interacted with"`
	Context   string `json:"context,omitempty" jsonschema:"Context description (e.g. 'Debugging Auth Module')"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index to store session in. Defaults to 'mcp_memory'"`
}

type StartSessionResult struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type EndSessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"The session ID to end,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index where session is stored. Defaults to 'mcp_memory'"`
}

type EndSessionResult struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// SessionContext tracks an active session for a connection.
type SessionContext struct {
	ID        string
	IndexName string
	StartTime int64          // Unix nanoseconds
	Metadata  map[string]any // agent_id, user_id, context
}

// --- Reflection / Subconscious Tools ---

type CheckSubconsciousArgs struct {
	IndexName string `json:"index_name,omitempty"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max number of reflections to retrieve (default 5)"`
}

type CheckSubconsciousResult struct {
	Reflections []string `json:"reflections"`
}

type ResolveConflictArgs struct {
	IndexName    string `json:"index_name,omitempty"`
	ReflectionID string `json:"reflection_id" jsonschema:"The ID of the reflection node to resolve,required"`
	Resolution   string `json:"resolution" jsonschema:"The logical decision made by the AI to resolve this conflict,required"`
	DiscardID    string `json:"discard_id,omitempty" jsonschema:"Optional ID of the memory to soft-delete/archive if it was deemed false or outdated"`
}

type ResolveConflictResult struct {
	Status string `json:"status"`
}

type AskMetaQuestionArgs struct {
	IndexName string `json:"index_name,omitempty"`
	Query     string `json:"query" jsonschema:"The meta-query to search within reflections (e.g. 'How did my opinion on X change?'),required"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max number of meta-memories to retrieve (default 5)"`
}

type AskMetaQuestionResult struct {
	Reflections []string `json:"reflections"`
}

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

// --- User Profile Tools ---

type GetUserProfileArgs struct {
	UserID    string `json:"user_id" jsonschema:"The user ID to retrieve profile for,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (defaults to 'mcp_memory')"`
}

type GetUserProfileResult struct {
	UserID             string   `json:"user_id"`
	Exists             bool     `json:"exists"`
	CommunicationStyle string   `json:"communication_style,omitempty"`
	Language           string   `json:"language,omitempty"`
	ExpertiseAreas     []string `json:"expertise_areas,omitempty"`
	Dislikes           []string `json:"dislikes,omitempty"`
	ResponseLength     string   `json:"response_length,omitempty"`
	Confidence         float64  `json:"confidence"`
	ProfileData        string   `json:"profile_data,omitempty"`
	LastUpdated        int64    `json:"last_updated"`
	InteractionCount   int      `json:"interaction_count"`
}

type ListUserProfilesArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (defaults to 'mcp_memory')"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max results (default 50)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

type ListUserProfilesResult struct {
	Profiles []UserProfileItem `json:"profiles"`
	HasMore  bool              `json:"has_more"`
}

type UserProfileItem struct {
	UserID             string  `json:"user_id"`
	Confidence         float64 `json:"confidence"`
	LastUpdated        int64   `json:"last_updated"`
	CommunicationStyle string  `json:"communication_style,omitempty"`
}

// --- Memory Transfer Tools ---

type TransferMemoryArgs struct {
	SourceIndex    string `json:"source_index" jsonschema:"Source index name (e.g. 'researcher_memory'),required"`
	TargetIndex    string `json:"target_index" jsonschema:"Target index name (e.g. 'writer_memory'),required"`
	Query          string `json:"query" jsonschema:"Semantic query to find memories to transfer,required"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Max memories to transfer (default 50, max 500)"`
	WithGraph      bool   `json:"with_graph,omitempty" jsonschema:"If true, also transfer internal graph topology between selected nodes"`
	TransferReason string `json:"transfer_reason,omitempty" jsonschema:"Optional reason/context for the transfer, stored in metadata"`
}

type TransferMemoryResult struct {
	TransferredCount int      `json:"transferred_count"`
	SkippedCount     int      `json:"skipped_count"`
	TransferredIDs   []string `json:"transferred_ids"`
	Message          string   `json:"message"`
}

// --- Adaptive Context Retrieval ---

type AdaptiveRetrieveArgs struct {
	Query          string `json:"query" jsonschema:"The query to search for,required"`
	IndexName      string `json:"index_name,omitempty" jsonschema:"Index to search (defaults to pipeline index)"`
	K              int    `json:"k,omitempty" jsonschema:"Number of seed chunks (default: 5)"`
	MaxTokens      int    `json:"max_tokens,omitempty" jsonschema:"Token budget (default: 4096)"`
	Strategy       string `json:"strategy,omitempty" jsonschema:"Expansion strategy: greedy, density, graph (default: graph)"`
	ExpansionDepth int    `json:"expansion_depth,omitempty" jsonschema:"Graph expansion depth (default: 2)"`
}

type AdaptiveRetrieveResult struct {
	ContextText    string `json:"context_text"`
	ChunksUsed     int    `json:"chunks_used"`
	TotalTokens    int    `json:"total_tokens"`
	DocumentsUsed  int    `json:"documents_used"`
	ExpansionStats struct {
		SeedChunks     int `json:"seed_chunks"`
		ExpandedChunks int `json:"expanded_chunks"`
		TotalEvaluated int `json:"total_evaluated"`
	} `json:"expansion_stats"`
}

// --- Semantic Evolution Tools ---

type EvolveMemoryArgs struct {
	IndexName   string    `json:"index_name,omitempty" jsonschema:"Index name (defaults to 'mcp_memory')"`
	OldMemoryID string    `json:"old_memory_id" jsonschema:"ID of the memory to evolve from,required"`
	NewContent  string    `json:"new_content" jsonschema:"Updated content for the new memory"`
	NewVector   []float32 `json:"new_vector,omitempty" jsonschema:"Optional vector (will be generated from content if not provided)"`
	Layer       string    `json:"layer,omitempty" jsonschema:"Memory layer (episodic/semantic/procedural)"`
	Reason      string    `json:"reason" jsonschema:"Reason for the evolution (e.g. 'User corrected me'),required"`
}

type EvolveMemoryResult struct {
	NewMemoryID string `json:"new_memory_id"`
	OldMemoryID string `json:"old_memory_id"`
	Status      string `json:"status"`
}

type GetMemoryEvolutionArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (defaults to 'mcp_memory')"`
	MemoryID  string `json:"memory_id" jsonschema:"Current memory ID to trace back,required"`
	Direction string `json:"direction,omitempty" jsonschema:"'backward' (default) to trace origins, 'forward' to see evolutions"`
}

type MemoryEvolutionStep struct {
	MemoryID        string `json:"memory_id"`
	Content         any    `json:"content,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	IsCurrent       bool   `json:"is_current"`
	SupersededBy    string `json:"superseded_by,omitempty"`
	EvolvesFrom     string `json:"evolves_from,omitempty"`
	EvolutionReason string `json:"evolution_reason,omitempty"`
}

type GetMemoryEvolutionResult struct {
	EvolutionChain []MemoryEvolutionStep `json:"evolution_chain"`
	TotalSteps     int                   `json:"total_steps"`
}

type RequestKnowledgeArgs struct {
	Intent            string         `json:"intent" jsonschema:"Type of knowledge requested (e.g. user_profile, project_summary, entity_card),required"`
	Entity            string         `json:"entity" jsonschema:"Entity identifier,required"`
	EntityType        string         `json:"entity_type,omitempty" jsonschema:"Entity type (auto-detected if omitted)"`
	IndexName         string         `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	ConfidenceMin     float64        `json:"confidence_min,omitempty" jsonschema:"Minimum confidence threshold (0.0-1.0, default 0.5)"`
	BudgetMs          int            `json:"budget_ms,omitempty" jsonschema:"Maximum latency budget in ms (default 500). If < 100, skip fallback search"`
	IncludeProvenance bool           `json:"include_provenance" jsonschema:"Include source citations per field (default true)"`
	OutputShape       map[string]any `json:"output_shape,omitempty" jsonschema:"Desired output fields (omitted = all fields from template)"`
}

type RequestKnowledgeResult struct {
	Found           bool                     `json:"found"`
	ArtifactName    string                   `json:"artifact_name,omitempty"`
	Version         int                      `json:"version,omitempty"`
	CompiledAt      string                   `json:"compiled_at,omitempty"`
	Data            map[string]any           `json:"data,omitempty"`
	Confidence      map[string]float64       `json:"confidence,omitempty"`
	Provenance      map[string][]interface{} `json:"provenance,omitempty"`
	StalenessScore  float64                  `json:"staleness_score,omitempty"`
	CompileMode     string                   `json:"compile_mode,omitempty"`
	Status          string                   `json:"status"`
	FallbackResults []string                 `json:"fallback_results,omitempty"`
}

// --- Tool 1: get_memory ---
// Fetches a single memory node with full metadata by ID.
// Useful for inspecting a memory returned by find_connection, get_memory_evolution,
// or any other tool that returns IDs.

type GetMemoryArgs struct {
	MemoryID  string `json:"memory_id" jsonschema:"The memory/entity ID to fetch,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type GetMemoryResult struct {
	Found    bool                   `json:"found"`
	MemoryID string                 `json:"memory_id,omitempty"`
	Vector   []float32              `json:"vector,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// --- Tool 2: get_memories ---
// Batch lookup of multiple memory nodes by ID in a single call.

type GetMemoriesArgs struct {
	MemoryIDs []string `json:"memory_ids" jsonschema:"List of memory/entity IDs to fetch,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type GetMemoriesResult struct {
	Found    int                    `json:"found"`
	Memories []GetMemoryResultEntry `json:"memories"`
}

type GetMemoryResultEntry struct {
	Found    bool                   `json:"found"`
	MemoryID string                 `json:"memory_id"`
	Vector   []float32              `json:"vector,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// --- Tool 3: delete_memory ---
// Soft-deletes a memory (preserves AOF history). Use hard_delete=true for
// irreversible removal (use with care).

type DeleteMemoryArgs struct {
	MemoryID   string `json:"memory_id" jsonschema:"The memory/entity ID to delete,required"`
	IndexName  string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	HardDelete bool   `json:"hard_delete,omitempty" jsonschema:"If true, physically remove the vector and all related edges. Default: soft delete (preserves AOF history)"`
}

type DeleteMemoryResult struct {
	Status     string `json:"status"`
	MemoryID   string `json:"memory_id"`
	HardDelete bool   `json:"hard_delete"`
}

// --- Tool 4: unlink_entities ---
// Removes a relationship edge between two nodes. Inverse of connect_entities.

type UnlinkEntitiesArgs struct {
	SourceID        string `json:"source_id" jsonschema:"Source node ID,required"`
	TargetID        string `json:"target_id" jsonschema:"Target node ID,required"`
	Relation        string `json:"relation" jsonschema:"Relation type to remove (e.g. 'mentions', 'parent'),required"`
	IndexName       string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	HardDelete      bool   `json:"hard_delete,omitempty" jsonschema:"If true, permanently remove the edge. Default: soft delete (preserves history)"`
	InverseRelation string `json:"inverse_relation,omitempty" jsonschema:"Optional inverse relation type (e.g. 'mentioned_by') to also remove"`
}

type UnlinkEntitiesResult struct {
	Status   string `json:"status"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Relation string `json:"relation"`
}

// --- Tool 5: list_templates ---
// Lists all built-in knowledge compiler templates with their schemas.
// Use this to discover valid `intent` values for request_knowledge.

type ListTemplatesResult struct {
	Templates []TemplateInfo `json:"templates"`
}

type TemplateInfo struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	EntityTypes   string `json:"entity_types,omitempty"` // CSV
	IsLLM         bool   `json:"is_llm"`                 // requires LLM
	Deterministic bool   `json:"deterministic"`          // can compile without LLM
}

// --- Tool 6: get_artifact_history ---
// Returns all versions of a compiled knowledge artifact, newest first.

type GetArtifactHistoryArgs struct {
	Intent     string `json:"intent" jsonschema:"Template name (e.g. 'user_profile', 'project_summary'),required"`
	Entity     string `json:"entity" jsonschema:"Entity identifier,required"`
	EntityType string `json:"entity_type,omitempty" jsonschema:"Entity type (auto-detected if omitted)"`
	IndexName  string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Max versions to return (default 20, max 100)"`
}

type GetArtifactHistoryResult struct {
	Entity        string                `json:"entity"`
	Intent        string                `json:"intent"`
	TotalVersions int                   `json:"total_versions"`
	Versions      []ArtifactVersionInfo `json:"versions"`
	Message       string                `json:"message,omitempty"`
}

type ArtifactVersionInfo struct {
	Version        int     `json:"version"`
	CompiledAt     string  `json:"compiled_at"`
	CompileMode    string  `json:"compile_mode"` // "deterministic" | "llm" | "degraded"
	StalenessScore float64 `json:"staleness_score"`
	Status         string  `json:"status"`
	IsHistorical   bool    `json:"is_historical"`
	UsageCount     int     `json:"usage_count,omitempty"`
}

// --- Tool 7: get_artifact_staleness ---
// Returns staleness metrics for a compiled artifact, useful for deciding
// whether to re-request_knowledge or wait for the Watcher to recompile.

type GetArtifactStalenessArgs struct {
	Intent     string `json:"intent" jsonschema:"Template name,required"`
	Entity     string `json:"entity" jsonschema:"Entity identifier,required"`
	EntityType string `json:"entity_type,omitempty" jsonschema:"Entity type (auto-detected if omitted)"`
	IndexName  string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type GetArtifactStalenessResult struct {
	Found           bool               `json:"found"`
	Intent          string             `json:"intent"`
	Entity          string             `json:"entity"`
	Version         int                `json:"version,omitempty"`
	CompiledAt      string             `json:"compiled_at,omitempty"`
	StalenessScore  float64            `json:"staleness_score,omitempty"`
	Status          string             `json:"status,omitempty"`
	UsageCount      int                `json:"usage_count,omitempty"`
	LastAccessedAt  string             `json:"last_accessed_at,omitempty"`
	ImportanceScore float64            `json:"importance_score,omitempty"`
	FieldStaleness  map[string]float64 `json:"field_staleness,omitempty"`
	Message         string             `json:"message,omitempty"`
}

// --- Tool 8: trigger_reflection ---
// Forces the Gardener to run a think cycle immediately on the given index.
// Useful after heavy memory writes when you don't want to wait for the next
// scheduled cycle.

type TriggerReflectionArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type TriggerReflectionResult struct {
	Status    string `json:"status"`
	IndexName string `json:"index_name"`
	Message   string `json:"message"`
}

// --- Tool 9: assess_belief ---
// Returns epistemic confidence for a memory or query, with 3-pillar evidence:
// consensus (how widely supported), stability (how long consistent),
// friction (how much contradiction). Useful for "should I trust this?" reasoning.

type AssessBeliefArgs struct {
	Query     string `json:"query" jsonschema:"Memory ID or natural language query to assess,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Number of supporting memories to analyze (default 10)"`
}

type AssessBeliefResult struct {
	Query      string           `json:"query"`
	Confidence float64          `json:"confidence"` // 0.0-1.0
	Consensus  float64          `json:"consensus"`  // 0.0-1.0: how widely the belief is supported
	Stability  float64          `json:"stability"`  // 0.0-1.0: how long the belief has been consistent
	Friction   float64          `json:"friction"`   // 0.0-1.0: how much contradiction exists
	Verdict    string           `json:"verdict"`    // "well_supported" | "contested" | "fading" | "fresh"
	Evidence   []BeliefEvidence `json:"evidence"`
	Message    string           `json:"message,omitempty"`
}

type BeliefEvidence struct {
	MemoryID  string  `json:"memory_id"`
	Stance    string  `json:"stance"` // "supports" | "contradicts" | "neutral"
	Weight    float64 `json:"weight"` // 0.0-1.0
	Timestamp int64   `json:"timestamp,omitempty"`
}

// --- Tool 10: search_with_scores ---
// Semantic search with similarity scores returned. Useful when the agent
// needs to know HOW confident a recall was (e.g. "I think this is the
// answer but only 0.62 confidence").

type SearchWithScoresArgs struct {
	Query     string `json:"query" jsonschema:"Natural language search query,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	K         int    `json:"k,omitempty" jsonschema:"Number of results to return (default 5, max 100)"`
}

type SearchWithScoresResult struct {
	Results []ScoredResult `json:"results"`
}

type ScoredResult struct {
	MemoryID string                 `json:"memory_id"`
	Score    float64                `json:"score"` // cosine similarity, 0.0-1.0+
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// --- Tool 11: list_indexes ---
// Returns all vector indexes in the engine. Useful for multi-tenant agents
// or for discovering available targets before transfer_memory.

type ListIndexesResult struct {
	Indexes []IndexInfo `json:"indexes"`
}

type IndexInfo struct {
	Name        string `json:"name"`
	VectorCount int    `json:"vector_count"`
	Dimension   int    `json:"dimension"`
	Metric      string `json:"metric"`
}

// --- Fase 1 P2: 5 nuovi tool per chiudere gap visibilità agent ---

// GetRelations: ritorna la mappa delle relazioni (outgoing + incoming) di un nodo.
// Utile per inspection strutturata del grafo (oggi solo prose via explore_connections).
type GetRelationsArgs struct {
	NodeID    string `json:"node_id" jsonschema:"The node ID to inspect,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type GetRelationsResult struct {
	NodeID   string              `json:"node_id"`
	Outgoing map[string][]string `json:"outgoing"` // relation_type -> [target_id]
	Incoming map[string][]string `json:"incoming"` // relation_type -> [source_id]
	OutCount int                 `json:"out_count"`
	InCount  int                 `json:"in_count"`
	Message  string              `json:"message,omitempty"`
}

// GetGardenerStatus: introspection del Gardener (mode, interval, last think, totals).
// Self-diagnostics: l'agent sa se il Gardener è in salute senza chiederlo all'utente.
type GetGardenerStatusArgs struct{}

type GetGardenerStatusResult struct {
	Enabled             bool     `json:"enabled"`
	Mode                string   `json:"mode"` // "basic" | "advanced" | "meta"
	IntervalSeconds     int      `json:"interval_seconds"`
	TargetIndexes       []string `json:"target_indexes"`
	LastThinkAgo        string   `json:"last_think_ago,omitempty"` // human-readable
	LastThinkUnix       int64    `json:"last_think_unix"`
	TotalReflections    int64    `json:"total_reflections"`
	TotalContradictions int64    `json:"total_contradictions"`
	TotalDecayed        int64    `json:"total_decayed"`
	Message             string   `json:"message,omitempty"`
}

// ListArtifacts: lista tutti i knowledge artifacts compilati con status/versione.
type ListArtifactsArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max artifacts to return (default 50, max 200)"`
}

type ListArtifactsResult struct {
	IndexName string            `json:"index_name"`
	Total     int               `json:"total"`
	Artifacts []ArtifactSummary `json:"artifacts"`
	Message   string            `json:"message,omitempty"`
}

type ArtifactSummary struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Version        int     `json:"version"`
	EntityType     string  `json:"entity_type"`
	EntityID       string  `json:"entity_id"`
	Status         string  `json:"status"`
	CompileMode    string  `json:"compile_mode"`
	StalenessScore float64 `json:"staleness_score"`
	CompiledAt     string  `json:"compiled_at,omitempty"`
}

// ListReflections: lista contraddizioni/insights generati dal Gardener, filtrabili per status.
type ListReflectionsArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	Status    string `json:"status,omitempty" jsonschema:"Filter by status: 'unresolved', 'resolved', or empty for all"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max reflections to return (default 20, max 100)"`
}

type ListReflectionsResult struct {
	IndexName   string              `json:"index_name"`
	Status      string              `json:"status,omitempty"`
	Total       int                 `json:"total"`
	Reflections []ReflectionSummary `json:"reflections"`
	Message     string              `json:"message,omitempty"`
}

type ReflectionSummary struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // contradiction, consolidation, importance, core_fact
	Content     string   `json:"content"`
	Confidence  float64  `json:"confidence"`
	Status      string   `json:"status"`
	DerivedFrom []string `json:"derived_from,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
}

// ForceRecompile: trigger sincrono di (re)compilazione di un artifact.
// Complementare a trigger_reflection (forza think cycle del Gardener).
type ForceRecompileArgs struct {
	Intent     string `json:"intent" jsonschema:"Template name (e.g. 'user_profile', 'project_summary'),required"`
	Entity     string `json:"entity" jsonschema:"Entity identifier,required"`
	EntityType string `json:"entity_type,omitempty" jsonschema:"Entity type (auto-detected if omitted)"`
	IndexName  string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	TimeoutMs  int    `json:"timeout_ms,omitempty" jsonschema:"Max time to wait (default 30000, max 120000)"`
}

type ForceRecompileResult struct {
	Status     string `json:"status"` // "compiled" | "failed"
	Intent     string `json:"intent"`
	Entity     string `json:"entity"`
	Version    int    `json:"version,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	Message    string `json:"message,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// --- Fase 2 P2: Batch 1 — wrapper semplici ---

// SaveSnapshot: force a snapshot of the engine to disk.
// Useful before risky operations or at logical checkpoints.
type SaveSnapshotArgs struct{}

type SaveSnapshotResult struct {
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
	Message    string `json:"message,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// CompactAOF: trigger an AOF rewrite to reclaim disk space.
// Runs asynchronously in the background.
type CompactAOFArgs struct{}

type CompactAOFResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
}

// GetEmbedderStatus: introspect the active embedder (model, dimension, availability).
type GetEmbedderStatusResult struct {
	Active    bool   `json:"active"`
	Model     string `json:"model,omitempty"`
	Dimension int    `json:"dimension"`
	Mode      string `json:"mode,omitempty"`
	Message   string `json:"message,omitempty"`
}

// KVGet: read a value from the engine's key-value store.
type KVGetArgs struct {
	Key string `json:"key" jsonschema:"The key to read,required"`
}

type KVGetResult struct {
	Key     string `json:"key"`
	Found   bool   `json:"found"`
	Value   string `json:"value,omitempty"` // base64-encoded for binary safety
	Message string `json:"message,omitempty"`
}

// KVSet: write a value to the engine's key-value store.
type KVSetArgs struct {
	Key   string `json:"key" jsonschema:"The key to write,required"`
	Value string `json:"value" jsonschema:"The value to write (string).required"`
}

type KVSetResult struct {
	Status  string `json:"status"`
	Key     string `json:"key"`
	Message string `json:"message,omitempty"`
}

// KVDelete: delete a key from the engine's key-value store.
type KVDeleteArgs struct {
	Key string `json:"key" jsonschema:"The key to delete,required"`
}

type KVDeleteResult struct {
	Status  string `json:"status"`
	Key     string `json:"key"`
	Message string `json:"message,omitempty"`
}

// --- Fase 2 P2: Batch 2 — engine stats + profile refresh ---

// GetStats: self-diagnostics of the engine. Returns aggregate stats useful
// for monitoring and debugging.
type GetStatsArgs struct {
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default: aggregate across all indexes)"`
}

type GetStatsResult struct {
	IndexName    string         `json:"index_name,omitempty"`
	TotalVectors int            `json:"total_vectors"`
	TotalIndexes int            `json:"total_indexes"`
	Indexes      []IndexSummary `json:"indexes,omitempty"`
	Embedder     string         `json:"embedder,omitempty"` // "active" | "noop" | "error"
	Message      string         `json:"message,omitempty"`
}

type IndexSummary struct {
	Name        string `json:"name"`
	VectorCount int    `json:"vector_count"`
	Dimension   int    `json:"dimension"`
	Metric      string `json:"metric"`
}

// GetPersistenceStatus: AOF stats for monitoring disk usage and durability.
type GetPersistenceStatusResult struct {
	AOFPath         string `json:"aof_path"`
	AOFSizeBytes    int64  `json:"aof_size_bytes"`
	WriteQueueDepth int    `json:"write_queue_depth,omitempty"`
	LastSyncAgoMs   int64  `json:"last_sync_ago_ms,omitempty"`
	FlushIntervalMs int    `json:"flush_interval_ms,omitempty"`
	ForceSyncIntvMs int    `json:"force_sync_interval_ms,omitempty"`
	Message         string `json:"message,omitempty"`
}

// RefreshUserProfile: force the Gardener to re-process a user profile now.
// Useful after a heavy interaction session when the threshold hasn't been
// reached yet but the agent wants the profile updated immediately.
type RefreshUserProfileArgs struct {
	UserID    string `json:"user_id" jsonschema:"The user ID to refresh,required"`
	IndexName string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type RefreshUserProfileResult struct {
	Status           string  `json:"status"`
	UserID           string  `json:"user_id"`
	IndexName        string  `json:"index_name"`
	Confidence       float64 `json:"confidence,omitempty"`
	InteractionCount int     `json:"interaction_count,omitempty"`
	Message          string  `json:"message,omitempty"`
}

// --- Fase 2 P2: Batch 3 — graph edges + artifact diff + summarize ---

// GetEdgeDetails: returns full edge details (timestamps, weight, props) for
// all outgoing edges of a source node, optionally filtered by relation type.
type GetEdgeDetailsArgs struct {
	SourceID     string `json:"source_id" jsonschema:"The source node ID,required"`
	RelationType string `json:"relation_type,omitempty" jsonschema:"Filter by relation type (e.g. 'mentions', 'parent'). Empty = all relations"`
	IndexName    string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	AtTime       int64  `json:"at_time,omitempty" jsonschema:"Unix timestamp for time-travel queries (0 = latest)"`
}

type GetEdgeDetailsResult struct {
	SourceID     string       `json:"source_id"`
	RelationType string       `json:"relation_type,omitempty"`
	EdgeCount    int          `json:"edge_count"`
	Edges        []EdgeDetail `json:"edges"`
	Message      string       `json:"message,omitempty"`
}

type EdgeDetail struct {
	TargetID  string  `json:"target_id"`
	Weight    float32 `json:"weight"`
	CreatedAt int64   `json:"created_at"`           // Unix nano
	DeletedAt int64   `json:"deleted_at,omitempty"` // 0 = active
	Active    bool    `json:"active"`
	Props     string  `json:"props,omitempty"` // raw JSON props
}

// DiffArtifactVersions: compares two versions of a knowledge artifact and
// reports added, removed, and modified fields.
type DiffArtifactVersionsArgs struct {
	Intent     string `json:"intent" jsonschema:"Template name,required"`
	Entity     string `json:"entity" jsonschema:"Entity identifier,required"`
	EntityType string `json:"entity_type,omitempty" jsonschema:"Entity type (auto-detected if omitted)"`
	IndexName  string `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	V1         int    `json:"v1" jsonschema:"First version to compare,required"`
	V2         int    `json:"v2" jsonschema:"Second version to compare,required"`
}

type DiffArtifactVersionsResult struct {
	Intent   string         `json:"intent"`
	Entity   string         `json:"entity"`
	V1       int            `json:"v1"`
	V2       int            `json:"v2"`
	Added    map[string]any `json:"added"`
	Removed  map[string]any `json:"removed"`
	Modified map[string]any `json:"modified"` // field -> {v1, v2}
	Message  string         `json:"message,omitempty"`
}

// SummarizeMemories: generates a bullet-point summary of a custom set of
// memories (vs end_session which is session-specific).
type SummarizeMemoriesArgs struct {
	MemoryIDs []string `json:"memory_ids" jsonschema:"List of memory IDs to summarize,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
	Title     string   `json:"title,omitempty" jsonschema:"Optional title for the summary (default 'Memories Summary')"`
}

type SummarizeMemoriesResult struct {
	Title        string `json:"title"`
	SummaryID    string `json:"summary_id"`
	Summary      string `json:"summary"`
	ContentCount int    `json:"content_count"`
	Message      string `json:"message,omitempty"`
}

// --- Fase 3 P2 expansion: 6 final tools closing MCP interface ---

// FindPath: returns a structured path between two nodes with configurable
// max_depth. Complement to find_connection (which returns prose).
type FindPathArgs struct {
	SourceID  string   `json:"source_id" jsonschema:"Start node ID,required"`
	TargetID  string   `json:"target_id" jsonschema:"End node ID,required"`
	Relations []string `json:"relations,omitempty" jsonschema:"Allowed relation types to traverse (auto-derived if empty)"`
	MaxDepth  int      `json:"max_depth,omitempty" jsonschema:"Max BFS depth (default 4, max 10)"`
	AtTime    int64    `json:"at_time,omitempty" jsonschema:"Unix nanoseconds timestamp to query historical data (0 = current time)"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type FindPathResult struct {
	SourceID  string     `json:"source_id"`
	TargetID  string     `json:"target_id"`
	Path      []string   `json:"path"`            // sequence of node IDs from source to target
	Edges     []PathEdge `json:"edges,omitempty"` // edges traversed in order
	StepCount int        `json:"step_count"`
	Found     bool       `json:"found"`
	Message   string     `json:"message,omitempty"`
}

// PathEdge: one edge along a path, mirroring engine.SubgraphEdge.
type PathEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Dir      string `json:"dir"` // "out" | "in"
}

// ReinforceMemory: explicitly mark memories as accessed (updates _last_accessed
// and _access_count metadata). Returns which IDs were actually found.
type ReinforceMemoryArgs struct {
	MemoryIDs []string `json:"memory_ids" jsonschema:"List of memory node IDs to reinforce,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type ReinforceMemoryResult struct {
	IndexName  string   `json:"index_name"`
	Requested  int      `json:"requested"`
	Reinforced int      `json:"reinforced"`
	Skipped    []string `json:"skipped,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// ListSessions: enumerates active sessions tracked in the MCP server process.
// Optional user_id filter. Note: in-memory only (lost on restart).
type ListSessionsArgs struct {
	UserID string `json:"user_id,omitempty" jsonschema:"Filter sessions by user_id (empty = all)"`
}

type ListSessionsResult struct {
	Sessions []SessionInfo `json:"sessions"`
	Total    int           `json:"total"`
	Message  string        `json:"message,omitempty"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	IndexName string `json:"index_name"`
	StartTime int64  `json:"start_time"` // Unix nanoseconds
	UserID    string `json:"user_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Context   string `json:"context,omitempty"`
}

// CreateIndex: creates a new vector index with full HNSW configuration.
// Admin tool. Validates dimension against the active embedder.
type CreateIndexArgs struct {
	Name           string              `json:"name" jsonschema:"Index name (alphanumeric, underscore, dash),required"`
	Metric         string              `json:"metric,omitempty" jsonschema:"Distance metric: 'cosine' or 'euclidean' (default cosine)"`
	Dimension      int                 `json:"dimension" jsonschema:"Vector dimension (must match embedder if active),required"`
	Precision      string              `json:"precision,omitempty" jsonschema:"Precision: 'float32' (default), 'float16', 'int8'"`
	M              int                 `json:"m,omitempty" jsonschema:"HNSW M parameter (default 16)"`
	EfConstruction int                 `json:"ef_construction,omitempty" jsonschema:"HNSW efConstruction parameter (default 200)"`
	TextLanguage   string              `json:"text_language,omitempty" jsonschema:"Text language for hybrid search: 'english', 'italian', '' (default empty)"`
	AutoLinks      []AutoLinkRuleInput `json:"auto_links,omitempty" jsonschema:"Auto-linking rules (optional)"`
	MemoryConfig   *MemoryConfigInput  `json:"memory_config,omitempty" jsonschema:"Memory decay/pinning configuration (optional)"`
	Maintenance    *MaintenanceInput   `json:"maintenance,omitempty" jsonschema:"Maintenance configuration (optional)"`
}

type AutoLinkRuleInput struct {
	Field      string `json:"field" jsonschema:"Metadata field to extract,required"`
	Relation   string `json:"relation" jsonschema:"Relation type for auto-link,required"`
	CreateNode bool   `json:"create_node,omitempty" jsonschema:"If true, create a new node when the field is non-empty"`
}

type MemoryConfigInput struct {
	Enabled      bool         `json:"enabled,omitempty" jsonschema:"Enable memory decay/pinning"`
	DecayModel   string       `json:"decay_model,omitempty" jsonschema:"Decay model: 'exponential', 'linear', 'step', 'ebbinghaus' (default exponential)"`
	HalfLifeDays float64      `json:"half_life_days,omitempty" jsonschema:"Half-life in days (default 30)"`
	Layers       []LayerInput `json:"layers,omitempty" jsonschema:"Per-layer decay configuration"`
}

type LayerInput struct {
	Name         string  `json:"name" jsonschema:"Layer name: 'episodic', 'semantic', 'procedural',required"`
	HalfLifeDays float64 `json:"half_life_days,omitempty" jsonschema:"Half-life in days (default 30)"`
	Strength     float64 `json:"strength,omitempty" jsonschema:"Layer strength multiplier (default 1.0)"`
}

type MaintenanceInput struct {
	Vacuum          *IntervalInput `json:"vacuum,omitempty"`
	Refine          *IntervalInput `json:"refine,omitempty"`
	GraphRetention  *IntervalInput `json:"graph_retention,omitempty"`
	ArenaCompaction *IntervalInput `json:"arena_compaction,omitempty"`
}

type IntervalInput struct {
	Enabled     bool `json:"enabled,omitempty"`
	IntervalSec int  `json:"interval_sec,omitempty" jsonschema:"Interval in seconds"`
}

type CreateIndexResult struct {
	Status       string `json:"status"`
	Name         string `json:"name"`
	Metric       string `json:"metric"`
	Precision    string `json:"precision"`
	Dimension    int    `json:"dimension"`
	TextLanguage string `json:"text_language,omitempty"`
	Message      string `json:"message,omitempty"`
}

// DeleteIndex: deletes an existing index. Two-step safety: without confirm=true
// returns a preview; with confirm=true performs the deletion.
type DeleteIndexArgs struct {
	Name    string `json:"name" jsonschema:"Index name to delete,required"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"Must be true to actually delete. Without confirm=true, returns a preview only."`
}

type DeleteIndexResult struct {
	Status      string `json:"status"` // "preview" | "deleted" | "not_found" | "error"
	Name        string `json:"name"`
	VectorCount int64  `json:"vector_count,omitempty"`
	ArenaPath   string `json:"arena_path,omitempty"`
	Message     string `json:"message,omitempty"`
}

// ExtractSubgraph: BFS traversal returning structured nodes/edges JSON.
type ExtractSubgraphArgs struct {
	RootID    string   `json:"root_id" jsonschema:"Root node ID for BFS,required"`
	Relations []string `json:"relations,omitempty" jsonschema:"Relation types to traverse (auto-derived if empty)"`
	Depth     int      `json:"depth,omitempty" jsonschema:"BFS depth (default 2, max 5)"`
	AtTime    int64    `json:"at_time,omitempty" jsonschema:"Unix nanoseconds timestamp for time-travel (0 = latest)"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"Index name (default mcp_memory)"`
}

type ExtractSubgraphResult struct {
	RootID    string             `json:"root_id"`
	Depth     int                `json:"depth"`
	NodeCount int                `json:"node_count"`
	EdgeCount int                `json:"edge_count"`
	Nodes     []SubgraphNodeJSON `json:"nodes"`
	Edges     []SubgraphEdgeJSON `json:"edges"`
	Message   string             `json:"message,omitempty"`
}

type SubgraphNodeJSON struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type SubgraphEdgeJSON struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Dir      string `json:"dir"`
}
