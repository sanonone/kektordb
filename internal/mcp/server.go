// Package mcp implements the Model Context Protocol server for KektorDB.
//
// It exposes 51 tools across agent (43 tools) and admin (8 tools) profiles,
// including memory management, graph traversal, knowledge compilation,
// and agent lifecycle commands. Also provides MCP setup/installer commands
// for Claude Code, Cursor, Gemini CLI, Codex, and OpenCode.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/compiler"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

// NewMCPServer creates an MCP server with the given engine, embedder, compiler, and tool allowlist.
// allowlist is a set of tool names to register. If nil or empty, all tools are registered.
// compiler may be nil — the request_knowledge tool falls back gracefully.
// gardener may be nil — end_session uses deterministic summarization as fallback.
func NewMCPServer(eng *engine.Engine, embedder embeddings.Embedder, allowlist map[string]bool, comp *compiler.Compiler, gardener *cognitive.Gardener) *mcp.Server {
	service := NewService(eng, embedder, comp, gardener)

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "KektorDB Memory",
		Version: "0.5.3",
	}, nil)

	registerTools(s, service, allowlist)
	registerPrompts(s, service)
	return s
}

// registerPrompts attaches MCP prompts to the server. Prompts are on-demand
// (not auto-injected) and available to any client that implements the MCP
// `prompts/list` + `prompts/get` protocol. See internal/mcp/prompts.go for
// the per-prompt documentation.
func registerPrompts(s *mcp.Server, service *Service) {
	s.AddPrompt(&mcp.Prompt{
		Name:        PromptMemoryInstructions,
		Description: "KektorDB memory protocol — when and how to save/recall memories.",
	}, service.GetMemoryInstructionsPrompt)
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
			Description: "End a session and generate a deterministic bullet-point summary of its memories (supports up to ~10 memories). If the Cognitive Engine (Gardener) is active, it will augment this with deeper semantic summarization.",
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

	// --- P1 expansion: 11 new tools ---

	if shouldRegister(ToolGetMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetMemory,
			Description: "Fetch a single memory by ID with full metadata and vector. Use this to inspect a specific memory returned by find_connection, get_memory_evolution, or any other tool that emits IDs.",
		}, service.GetMemory)
	}

	if shouldRegister(ToolGetMemories, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetMemories,
			Description: "Batch fetch multiple memories by ID in a single call. More efficient than calling get_memory repeatedly.",
		}, service.GetMemories)
	}

	if shouldRegister(ToolDeleteMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolDeleteMemory,
			Description: "Delete a memory. Default is soft delete (preserves AOF history and recovery). Set hard_delete=true for irreversible removal that also unlinks related graph edges.",
		}, service.DeleteMemory)
	}

	if shouldRegister(ToolUnlinkEntities, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolUnlinkEntities,
			Description: "Remove a graph relationship between two nodes. Inverse of connect_entities. Default soft delete (preserves history); set hard_delete=true for permanent removal.",
		}, service.UnlinkEntities)
	}

	if shouldRegister(ToolListTemplates, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListTemplates,
			Description: "List all built-in knowledge compiler templates (e.g. 'user_profile', 'project_summary', 'entity_card'). Use the returned names as `intent` values for request_knowledge.",
		}, service.ListTemplates)
	}

	if shouldRegister(ToolGetArtifactHistory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetArtifactHistory,
			Description: "Return all compiled versions of a knowledge artifact (e.g. how the user profile evolved over time). Newest first.",
		}, service.GetArtifactHistory)
	}

	if shouldRegister(ToolGetArtifactStaleness, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetArtifactStaleness,
			Description: "Return staleness metrics for a compiled artifact. Use this to decide whether to re-request_knowledge or wait for the Gardener's Watcher to recompile.",
		}, service.GetArtifactStaleness)
	}

	if shouldRegister(ToolTriggerReflection, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolTriggerReflection,
			Description: "Force the Gardener to run a think cycle immediately on the given index. Useful after heavy memory writes when waiting for the next scheduled cycle is undesirable. Requires Gardener to be enabled.",
		}, service.TriggerReflection)
	}

	if shouldRegister(ToolAssessBelief, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolAssessBelief,
			Description: "Assess epistemic confidence for a memory or query. Returns 3-pillar evidence: consensus (how widely supported), stability (how long consistent), friction (how much contradiction). Useful for 'should I trust this memory?' reasoning.",
		}, service.AssessBelief)
	}

	if shouldRegister(ToolSearchWithScores, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolSearchWithScores,
			Description: "Semantic search that returns similarity scores alongside each result. Useful when you need to know HOW confident a recall was (e.g. 'I'm 0.62 confident this is the answer').",
		}, service.SearchWithScores)
	}

	if shouldRegister(ToolListIndexes, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListIndexes,
			Description: "List all vector indexes in the engine with their stats. Useful for multi-tenant agents and for discovering available targets before transfer_memory.",
		}, service.ListIndexes)
	}

	// --- Fase 1 P2 expansion: 5 more tools for agent visibility ---

	if shouldRegister(ToolGetRelations, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetRelations,
			Description: "Return a structured map of a node's outgoing and incoming relationships ({relation_type: [target_id]}). Useful for programmatic graph inspection (vs prose from explore_connections).",
		}, service.GetRelations)
	}

	if shouldRegister(ToolGetGardenerStatus, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetGardenerStatus,
			Description: "Introspect the Gardener: mode, interval, last think time, total reflections, contradictions, and decayed memories. Enables self-diagnostics.",
		}, service.GetGardenerStatus)
	}

	if shouldRegister(ToolListArtifacts, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListArtifacts,
			Description: "List all compiled knowledge artifacts in an index with name, version, status, and staleness score. Enables discovery of what's been compiled.",
		}, service.ListArtifacts)
	}

	if shouldRegister(ToolListReflections, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListReflections,
			Description: "List Gardener-generated reflections (contradictions, consolidations, importance shifts), filterable by status. Complement to check_subconscious but returns structured data.",
		}, service.ListReflections)
	}

	if shouldRegister(ToolForceRecompile, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolForceRecompile,
			Description: "Synchronously trigger (re)compilation of a knowledge artifact. Use after a reflection cycle or when an artifact is stale. Blocks the agent until done (typically a few seconds).",
		}, service.ForceRecompile)
	}

	// --- Fase 2 P2 expansion: Batch 1 — wrapper semplici ---

	if shouldRegister(ToolSaveSnapshot, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolSaveSnapshot,
			Description: "Force a snapshot of the engine state to disk. Useful before risky operations or at logical checkpoints.",
		}, service.SaveSnapshot)
	}

	if shouldRegister(ToolCompactAOF, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolCompactAOF,
			Description: "Trigger an asynchronous AOF rewrite to reclaim disk space. Runs in the background; check log for completion.",
		}, service.CompactAOF)
	}

	if shouldRegister(ToolGetEmbedderStatus, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetEmbedderStatus,
			Description: "Introspect the active embedder: model name, output dimension, and availability. Useful for diagnosing semantic tool failures.",
		}, service.GetEmbedderStatus)
	}

	if shouldRegister(ToolKVGet, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolKVGet,
			Description: "Read a value from the engine's key-value store. Binary values are base64-encoded. Useful for agent scratch state (counters, last cursor).",
		}, service.KVGet)
	}

	if shouldRegister(ToolKVSet, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolKVSet,
			Description: "Write a string value to the engine's key-value store. Useful for storing small agent state without polluting the vector space.",
		}, service.KVSet)
	}

	if shouldRegister(ToolKVDelete, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolKVDelete,
			Description: "Delete a key from the engine's key-value store.",
		}, service.KVDelete)
	}

	// --- Fase 2 P2 expansion: Batch 2 — engine stats + profile refresh ---

	if shouldRegister(ToolGetStats, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetStats,
			Description: "Self-diagnostics: total vectors, indexes, embedder status. If index_name is given, returns stats for that index; otherwise aggregate.",
		}, service.GetStats)
	}

	if shouldRegister(ToolGetPersistenceStatus, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetPersistenceStatus,
			Description: "AOF file stats (path, size in bytes). Useful for monitoring disk usage.",
		}, service.GetPersistenceStatus)
	}

	if shouldRegister(ToolRefreshUserProfile, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolRefreshUserProfile,
			Description: "Force the Gardener to re-process a user profile now, bypassing the threshold check. Useful after a heavy interaction session.",
		}, service.RefreshUserProfile)
	}

	// --- Fase 2 P2 expansion: Batch 3 — graph edges + artifact diff + summarize ---

	if shouldRegister(ToolGetEdgeDetails, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolGetEdgeDetails,
			Description: "Return full edge details (timestamps, weight, props) for outgoing edges of a source node, optionally filtered by relation type. Useful for auditing graph history.",
		}, service.GetEdgeDetails)
	}

	if shouldRegister(ToolDiffArtifactVersions, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolDiffArtifactVersions,
			Description: "Compare two versions of a knowledge artifact and report added, removed, and modified fields. Useful for auditing profile or project summary drift.",
		}, service.DiffArtifactVersions)
	}

	if shouldRegister(ToolSummarizeMemories, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolSummarizeMemories,
			Description: "Generate a bullet-point summary of a custom set of memories. The summary is stored as a new memory node for later retrieval. Differs from end_session (which is session-scoped).",
		}, service.SummarizeMemories)
	}

	// --- Fase 3 P2 expansion: 6 final tools closing the MCP interface ---

	if shouldRegister(ToolFindPath, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolFindPath,
			Description: "Find a structured path between two nodes in the graph. Returns the ordered list of node IDs and the edges traversed. Configurable max_depth (default 4, max 10). Complement to find_connection which returns prose.",
		}, service.FindPath)
	}

	if shouldRegister(ToolReinforceMemory, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolReinforceMemory,
			Description: "Explicitly mark memories as accessed. Updates _last_accessed timestamp and increments _access_count for each valid ID. Returns which IDs were skipped (not found). Used to boost important memories' relevance over time.",
		}, service.ReinforceMemory)
	}

	if shouldRegister(ToolListSessions, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolListSessions,
			Description: "List all active sessions tracked in the current MCP server process. Optionally filter by user_id. Note: session list is in-memory only and is lost on server restart; for persistent session data, query memories with session_id filter.",
		}, service.ListSessions)
	}

	if shouldRegister(ToolCreateIndex, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolCreateIndex,
			Description: "Create a new vector index. Admin tool. Supports full HNSW configuration (M, ef_construction), metric (cosine/euclidean), precision (float32/float16/int8), text language, auto-linking rules, and memory/maintenance configs. Validates dimension against the active embedder.",
		}, service.CreateIndex)
	}

	if shouldRegister(ToolDeleteIndex, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolDeleteIndex,
			Description: "Delete an existing index. Admin tool. IRREVERSIBLE: removes all vectors, metadata, and graph data. Two-step safety: call without confirm=true for a preview (vector count, arena path); pass confirm=true to actually delete. Arena file removal is asynchronous.",
		}, service.DeleteIndex)
	}

	if shouldRegister(ToolExtractSubgraph, allowlist) {
		mcp.AddTool(s, &mcp.Tool{
			Name:        ToolExtractSubgraph,
			Description: "Extract a subgraph centered on a root node via BFS traversal. Returns structured nodes and edges as JSON (vs. traverse which returns prose). Configurable depth (default 2, max 5). Useful for inspecting the local neighborhood of any node.",
		}, service.ExtractSubgraph)
	}
}
