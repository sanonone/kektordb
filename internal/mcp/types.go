package mcp

// --- Tool Arguments ---

type SaveMemoryArgs struct {
	Content   string   `json:"content" jsonschema:"The text content/fact to remember,required"`
	IndexName string   `json:"index_name,omitempty" jsonschema:"The index to store data in. Defaults to 'mcp_memory'"`
	Links     []string `json:"links,omitempty" jsonschema:"List of existing Entity IDs to link this memory to (e.g. 'project_alpha', 'user_mario')"`
	Tags      []string `json:"tags,omitempty" jsonschema:"Optional tags or categories"`
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
	RootID    string   `json:"root_id" jsonschema:"required"`
	Relations []string `json:"relations,omitempty" jsonschema:"Filter by relation types (e.g. ['mentions'])"`
	Depth     int      `json:"depth,omitempty" jsonschema:"Depth (default 1)"`
}

type TraverseResult struct {
	GraphDescription string `json:"graph_description"` // Textual description of connections
}
