// Package mcp implements the Model Context Protocol server for KektorDB.
//
// It exposes 23 tools across agent (17 tools) and admin (6 tools) profiles,
// including memory management, graph traversal, knowledge compilation,
// and agent lifecycle commands. Also provides MCP setup/installer commands
// for Claude Code, Cursor, Gemini CLI, Codex, and OpenCode.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/compiler"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

// NewMCPServer creates an MCP server with the given engine, embedder, compiler, and tool allowlist.
// allowlist is a set of tool names to register. If nil or empty, all tools are registered.
// compiler may be nil — the request_knowledge tool falls back gracefully.
func NewMCPServer(eng *engine.Engine, embedder embeddings.Embedder, allowlist map[string]bool, comp *compiler.Compiler) *mcp.Server {
	service := NewService(eng, embedder, comp)

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "KektorDB Memory",
		Version: "0.5.3",
	}, nil)

	registerTools(s, service, allowlist)
	return s
}

// shouldRegister returns true if the tool should be registered based on the allowlist.
// nil allowlist means all tools are registered.
func shouldRegister(name string, allowlist map[string]bool) bool {
	if allowlist == nil {
		return true
	}
	return allowlist[name]
}

func registerTools(s *mcp.Server, service *Service, allowlist map[string]bool) {
	if shouldRegister(ToolSaveMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolSaveMemory,
			Description: "Save text/facts into long-term memory. Can be linked to existing entities.",
		}, service.SaveMemory)
	}

	if shouldRegister(ToolCreateEntity, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolCreateEntity,
			Description: "Create a conceptual entity (node) without text content, to organize memories (e.g. 'Project X').",
		}, service.CreateEntity)
	}

	if shouldRegister(ToolConnectEntities, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolConnectEntities,
			Description: "Create a relationship link between two memory items/entities.",
		}, service.Connect)
	}

	if shouldRegister(ToolRecallMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolRecallMemory,
			Description: "Search for memories semantically by query.",
		}, service.Recall)
	}

	if shouldRegister(ToolScopedRecall, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolScopedRecall,
			Description: "Search memories semantically BUT restricted to a specific graph context (e.g. 'search bugs in Project X').",
		}, service.ScopedRecall)
	}

	if shouldRegister(ToolExploreConnections, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolExploreConnections,
			Description: "Explore the graph neighborhood of a specific node to understand context.",
		}, service.Traverse)
	}

	if shouldRegister(ToolFindConnection, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolFindConnection,
			Description: "Discover how two concepts or memories are connected in the graph (Pathfinding).",
		}, service.FindConnection)
	}

	if shouldRegister(ToolFilterVectors, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolFilterVectors,
			Description: "Search vectors by metadata filter only, without vector similarity. Useful for exact matches on tags, types, or properties.",
		}, service.FilterVectors)
	}

	if shouldRegister(ToolUnpinMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolUnpinMemory,
			Description: "Remove the pinned status from a memory, allowing it to decay naturally over time.",
		}, service.UnpinMemory)
	}

	if shouldRegister(ToolConfigureAutoLinks, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolConfigureAutoLinks,
			Description: "Configure automatic link creation rules for an index. Rules define which metadata fields trigger automatic graph connections.",
		}, service.ConfigureAutoLinks)
	}

	if shouldRegister(ToolListVectors, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListVectors,
			Description: "List all vectors in an index with pagination. Useful for exporting or auditing stored data.",
		}, service.ListVectors)
	}

	if shouldRegister(ToolCheckSubconscious, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolCheckSubconscious,
			Description: "Queries the database's background reflection engine for unresolved contradictions, pattern shifts, or important insights generated recently.",
		}, service.CheckSubconscious)
	}

	if shouldRegister(ToolResolveConflict, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolResolveConflict,
			Description: "Resolves a pending contradiction/reflection by providing a logical conclusion and optionally discarding the incorrect memory.",
		}, service.ResolveConflict)
	}

	if shouldRegister(ToolAskMetaQuestion, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolAskMetaQuestion,
			Description: "Search strictly within the agent's meta-knowledge (insights, consolidated memories, and past reflections) to understand how concepts or behaviors evolved over time. Do not use this for raw fact retrieval.",
		}, service.AskMetaQuestion)
	}

	if shouldRegister(ToolStartSession, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolStartSession,
			Description: "Start a new conversational session. All subsequent memories can be linked to this session. Returns a session ID to use with save_memory and end_session.",
		}, service.StartSession)
	}

	if shouldRegister(ToolEndSession, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolEndSession,
			Description: "End a session and trigger automatic summarization. The Gardener will create a semantic summary of all session memories and archive the episodic ones.",
		}, service.EndSession)
	}

	if shouldRegister(ToolGetUserProfile, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetUserProfile,
			Description: "Retrieve the personality profile of a user. Returns communication style, expertise areas, preferences, and dislikes.",
		}, service.GetUserProfile)
	}

	if shouldRegister(ToolListUserProfiles, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListUserProfiles,
			Description: "List all user profiles in the database. Useful for multi-user dashboards or admin interfaces.",
		}, service.ListUserProfiles)
	}

	if shouldRegister(ToolTransferMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolTransferMemory,
			Description: "Transfer memories from one agent index to another. Preserves metadata, handles dimension mismatches, and optionally copies graph topology. Useful for multi-agent systems where agents need to share knowledge.",
		}, service.TransferMemory)
	}

	if shouldRegister(ToolAdaptiveRetrieve, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolAdaptiveRetrieve,
			Description: "Perform graph-aware adaptive retrieval for RAG. Retrieves seed chunks via semantic search, expands following graph relations (parent, child, next, prev, mentions), and assembles a context window respecting token budget. Uses information density scoring to prioritize high-quality chunks. Strategies: greedy (simple expansion), density (filter by token uniqueness), graph (full BFS with scoring).",
		}, service.AdaptiveRetrieve)
	}

	if shouldRegister(ToolEvolveMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolEvolveMemory,
			Description: "Evolves a memory when new information supersedes it. Creates a new node with updated data, links old to new via 'superseded_by', copies incoming edges, and marks old as historical.",
		}, service.EvolveMemory)
	}

	if shouldRegister(ToolGetMemoryEvolution, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetMemoryEvolution,
			Description: "Traces the evolution chain of a memory node by following superseded_by/evolves_from edges. Returns the history of how a piece of information changed over time.",
		}, service.GetMemoryEvolution)
	}

	if shouldRegister(ToolRequestKnowledge, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolRequestKnowledge,
			Description: "Request structured knowledge about an entity. Uses cached artifacts when available (returns in <50ms with zero token cost). Falls back to semantic search and triggers async compilation when not cached.",
		}, service.RequestKnowledge)
	}
}
