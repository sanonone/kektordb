package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

func NewMCPServer(eng *engine.Engine, embedder embeddings.Embedder) *mcp.Server {
	service := NewService(eng, embedder)

	// Create Server instance
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "KektorDB Memory",
		Version: "0.4.7",
	}, nil) // Options can be nil for default

	// Register Tools using the Generic AddTool which inspects structs!

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_memory",
		Description: "Save text/facts into long-term memory. Can be linked to existing entities.",
	}, service.SaveMemory)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_entity",
		Description: "Create a conceptual entity (node) without text content, to organize memories (e.g. 'Project X').",
	}, service.CreateEntity)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "connect_entities",
		Description: "Create a relationship link between two memory items/entities.",
	}, service.Connect)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "recall_memory",
		Description: "Search for memories semantically by query.",
	}, service.Recall)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "scoped_recall",
		Description: "Search memories semantically BUT restricted to a specific graph context (e.g. 'search bugs in Project X').",
	}, service.ScopedRecall)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "explore_connections",
		Description: "Explore the graph neighborhood of a specific node to understand context.",
	}, service.Traverse)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_connection",
		Description: "Discover how two concepts or memories are connected in the graph (Pathfinding).",
	}, service.FindConnection)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "filter_vectors",
		Description: "Search vectors by metadata filter only, without vector similarity. Useful for exact matches on tags, types, or properties.",
	}, service.FilterVectors)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "unpin_memory",
		Description: "Remove the pinned status from a memory, allowing it to decay naturally over time.",
	}, service.UnpinMemory)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "configure_auto_links",
		Description: "Configure automatic link creation rules for an index. Rules define which metadata fields trigger automatic graph connections.",
	}, service.ConfigureAutoLinks)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_vectors",
		Description: "List all vectors in an index with pagination. Useful for exporting or auditing stored data.",
	}, service.ListVectors)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "check_subconscious",
		Description: "Queries the database's background reflection engine for unresolved contradictions, pattern shifts, or important insights generated recently.",
	}, service.CheckSubconscious)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "resolve_conflict",
		Description: "Resolves a pending contradiction/reflection by providing a logical conclusion and optionally discarding the incorrect memory.",
	}, service.ResolveConflict)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ask_meta_question",
		Description: "Search strictly within the agent's meta-knowledge (insights, consolidated memories, and past reflections) to understand how concepts or behaviors evolved over time. Do not use this for raw fact retrieval.",
	}, service.AskMetaQuestion)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "start_session",
		Description: "Start a new conversational session. All subsequent memories can be linked to this session. Returns a session ID to use with save_memory and end_session.",
	}, service.StartSession)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "end_session",
		Description: "End a session and trigger automatic summarization. The Gardener will create a semantic summary of all session memories and archive the episodic ones.",
	}, service.EndSession)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_user_profile",
		Description: "Retrieve the personality profile of a user. Returns communication style, expertise areas, preferences, and dislikes.",
	}, service.GetUserProfile)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_user_profiles",
		Description: "List all user profiles in the database. Useful for multi-user dashboards or admin interfaces.",
	}, service.ListUserProfiles)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "transfer_memory",
		Description: "Transfer memories from one agent index to another. Preserves metadata, handles dimension mismatches, and optionally copies graph topology. Useful for multi-agent systems where agents need to share knowledge.",
	}, service.TransferMemory)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "adaptive_retrieve",
		Description: "Perform graph-aware adaptive retrieval for RAG. Retrieves seed chunks via semantic search, expands following graph relations (parent, child, next, prev, mentions), and assembles a context window respecting token budget. Uses information density scoring to prioritize high-quality chunks. Strategies: greedy (simple expansion), density (filter by token uniqueness), graph (full BFS with scoring).",
	}, service.AdaptiveRetrieve)

	return s
}
