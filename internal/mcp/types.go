package mcp

// --- Tool Arguments ---

type SaveMemoryArgs struct {
	Content   string   `json:"content" jsonschema:"The text content/fact to remember,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"The index to store data in. Defaults to 'mcp_memory'"`
	Links     []string `json:"links,omitempty" jsonschema:"List of existing Entity IDs to link this memory to (e.g. 'project_alpha', 'user_mario')"`
	Tags      []string `json:"tags,omitempty" jsonschema:"Optional tags or categories"`
	Pin       bool     `json:"pin,omitempty" jsonschema:"description=If true, this memory will never decay over time (e.g. core rules, birthdays)."`
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
	Reinforce bool   `json:"reinforce,omitempty" jsonschema:"description=If true, marks retrieved memories as 'accessed now', boosting their future relevance."`
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
	GuideQuery string   `json:"guide_query,omitempty" jsonschema:"description=Optional text concept to guide the traversal. Only nodes semantically similar to this query will be followed."`
	Threshold  float64  `json:"threshold,omitempty" jsonschema:"description=Similarity threshold (0.0-1.0) for guide_query. Default 0.5."`
	AtTime     int64    `json:"at_time,omitempty" jsonschema:"description=Unix nanoseconds timestamp to query historical data (0 = current time)"`
}

type TraverseResult struct {
	GraphDescription string `json:"graph_description"` // Textual description of connections
}

type FindConnectionArgs struct {
	SourceID  string   `json:"source_id" jsonschema:"description=Start Node ID,required"`
	TargetID  string   `json:"target_id" jsonschema:"description=End Node ID,required"`
	Relations []string `json:"relations,omitempty" jsonschema:"description=Allowed relation types to traverse (optional)"`
	AtTime    int64    `json:"at_time,omitempty" jsonschema:"description=Unix nanoseconds timestamp to query historical data (0 = current time)"`
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
