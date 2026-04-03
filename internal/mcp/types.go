package mcp

// --- Tool Arguments ---

type SaveMemoryArgs struct {
	Content   string   `json:"content" jsonschema:"The text content/fact to remember,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"The index to store data in. Defaults to 'mcp_memory'"`
	Layer     string   `json:"layer,omitempty" jsonschema:"Memory layer: 'episodic' (events, default), 'semantic' (facts), or 'procedural' (rules)."`
	Links     []string `json:"links,omitempty" jsonschema:"List of existing Entity IDs to link this memory to (e.g. 'project_alpha', 'user_mario')"`
	Tags      []string `json:"tags,omitempty" jsonschema:"Optional tags or categories"`
	Pin       bool     `json:"pin,omitempty" jsonschema:"If true, this memory will never decay over time (e.g. core rules, birthdays)."`
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
	SourceID string `json:"source_id" jsonschema:"required"`
	TargetID string `json:"target_id" jsonschema:"required"`
	Relation string `json:"relation" jsonschema:"The type of relationship (e.g. 'mentions', 'author_of', 'related_to'),required"`
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
