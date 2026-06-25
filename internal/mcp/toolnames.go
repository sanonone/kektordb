package mcp

import (
	"log/slog"
	"strings"
)

// Tool name constants — used by MCP server registration and setup allowlist.
const (
	ToolSaveMemory         = "save_memory"
	ToolRecallMemory       = "recall_memory"
	ToolScopedRecall       = "scoped_recall"
	ToolCreateEntity       = "create_entity"
	ToolConnectEntities    = "connect_entities"
	ToolExploreConnections = "explore_connections"
	ToolFindConnection     = "find_connection"
	ToolStartSession       = "start_session"
	ToolEndSession         = "end_session"
	ToolGetUserProfile     = "get_user_profile"
	ToolListUserProfiles   = "list_user_profiles"
	ToolTransferMemory     = "transfer_memory"
	ToolAdaptiveRetrieve   = "adaptive_retrieve"
	ToolFilterVectors      = "filter_vectors"
	ToolListVectors        = "list_vectors"
	ToolCheckSubconscious  = "check_subconscious"
	ToolResolveConflict    = "resolve_conflict"
	ToolAskMetaQuestion    = "ask_meta_question"
	ToolEvolveMemory       = "evolve_memory"
	ToolGetMemoryEvolution = "get_memory_evolution"
	ToolUnpinMemory        = "unpin_memory"
	ToolConfigureAutoLinks = "configure_auto_links"
	ToolRequestKnowledge   = "request_knowledge"
	// P1 expansion: 11 new tools to close the gap between MCP and core engine.
	ToolGetMemory            = "get_memory"
	ToolGetMemories          = "get_memories"
	ToolDeleteMemory         = "delete_memory"
	ToolUnlinkEntities       = "unlink_entities"
	ToolListTemplates        = "list_templates"
	ToolGetArtifactHistory   = "get_artifact_history"
	ToolGetArtifactStaleness = "get_artifact_staleness"
	ToolTriggerReflection    = "trigger_reflection"
	ToolAssessBelief         = "assess_belief"
	ToolSearchWithScores     = "search_with_scores"
	ToolListIndexes          = "list_indexes"
	// Fase 1 P2 expansion: 5 more tools for agent visibility
	ToolGetRelations      = "get_relations"
	ToolGetGardenerStatus = "get_gardener_status"
	ToolListArtifacts     = "list_artifacts"
	ToolListReflections   = "list_reflections"
	ToolForceRecompile    = "force_recompile"
	// Fase 2 P2 expansion Batch 1: wrapper semplici
	ToolSaveSnapshot      = "save_snapshot"
	ToolCompactAOF        = "compact_aof"
	ToolGetEmbedderStatus = "get_embedder_status"
	ToolKVGet             = "kv_get"
	ToolKVSet             = "kv_set"
	ToolKVDelete          = "kv_delete"
	// Fase 2 P2 expansion Batch 2: engine stats + profile refresh
	ToolGetStats             = "get_stats"
	ToolGetPersistenceStatus = "get_persistence_status"
	ToolRefreshUserProfile   = "refresh_user_profile"
	// Fase 2 P2 expansion Batch 3: graph edges + artifact diff + summarize
	ToolGetEdgeDetails       = "get_edge_details"
	ToolDiffArtifactVersions = "diff_artifact_versions"
	ToolSummarizeMemories    = "summarize_memories"
	// Fase 3 P2 expansion: 6 final tools closing the MCP interface
	ToolFindPath        = "find_path"
	ToolReinforceMemory = "reinforce_memory"
	ToolListSessions    = "list_sessions"
	ToolCreateIndex     = "create_index"
	ToolDeleteIndex     = "delete_index"
	ToolExtractSubgraph = "extract_subgraph"
)

// allToolNames maps every known MCP tool name for validation.
var allToolNames = map[string]bool{
	ToolSaveMemory:           true,
	ToolRecallMemory:         true,
	ToolScopedRecall:         true,
	ToolCreateEntity:         true,
	ToolConnectEntities:      true,
	ToolExploreConnections:   true,
	ToolFindConnection:       true,
	ToolStartSession:         true,
	ToolEndSession:           true,
	ToolGetUserProfile:       true,
	ToolListUserProfiles:     true,
	ToolTransferMemory:       true,
	ToolAdaptiveRetrieve:     true,
	ToolFilterVectors:        true,
	ToolListVectors:          true,
	ToolCheckSubconscious:    true,
	ToolResolveConflict:      true,
	ToolAskMetaQuestion:      true,
	ToolEvolveMemory:         true,
	ToolGetMemoryEvolution:   true,
	ToolUnpinMemory:          true,
	ToolConfigureAutoLinks:   true,
	ToolRequestKnowledge:     true,
	ToolGetMemory:            true,
	ToolGetMemories:          true,
	ToolDeleteMemory:         true,
	ToolUnlinkEntities:       true,
	ToolListTemplates:        true,
	ToolGetArtifactHistory:   true,
	ToolGetArtifactStaleness: true,
	ToolTriggerReflection:    true,
	ToolAssessBelief:         true,
	ToolSearchWithScores:     true,
	ToolListIndexes:          true,
	ToolGetRelations:         true,
	ToolGetGardenerStatus:    true,
	ToolListArtifacts:        true,
	ToolListReflections:      true,
	ToolForceRecompile:       true,
	ToolSaveSnapshot:         true,
	ToolCompactAOF:           true,
	ToolGetEmbedderStatus:    true,
	ToolKVGet:                true,
	ToolKVSet:                true,
	ToolKVDelete:             true,
	ToolGetStats:             true,
	ToolGetPersistenceStatus: true,
	ToolRefreshUserProfile:   true,
	ToolGetEdgeDetails:       true,
	ToolDiffArtifactVersions: true,
	ToolSummarizeMemories:    true,
	ToolFindPath:             true,
	ToolReinforceMemory:      true,
	ToolListSessions:         true,
	ToolCreateIndex:          true,
	ToolDeleteIndex:          true,
	ToolExtractSubgraph:      true,
}

// ProfileAgent contains tools useful for AI coding agents.
// Filters out admin-only tools (list_vectors, filter_vectors, transfer_memory, etc.)
// to reduce token usage in the agent's context.
var ProfileAgent = map[string]bool{
	ToolSaveMemory:         true,
	ToolRecallMemory:       true,
	ToolScopedRecall:       true,
	ToolCreateEntity:       true,
	ToolConnectEntities:    true,
	ToolExploreConnections: true,
	ToolFindConnection:     true,
	ToolStartSession:       true,
	ToolEndSession:         true,
	ToolGetUserProfile:     true,
	ToolAdaptiveRetrieve:   true,
	ToolCheckSubconscious:  true,
	ToolResolveConflict:    true,
	ToolAskMetaQuestion:    true,
	ToolEvolveMemory:       true,
	ToolGetMemoryEvolution: true,
	ToolRequestKnowledge:   true,
	// P1 expansion tools (all useful for memory agents)
	ToolGetMemory:            true,
	ToolGetMemories:          true,
	ToolDeleteMemory:         true,
	ToolUnlinkEntities:       true,
	ToolListTemplates:        true,
	ToolGetArtifactHistory:   true,
	ToolGetArtifactStaleness: true,
	ToolTriggerReflection:    true,
	ToolAssessBelief:         true,
	ToolSearchWithScores:     true,
	ToolListIndexes:          true,
	// Fase 1 P2 tools
	ToolGetRelations:      true,
	ToolGetGardenerStatus: true,
	ToolListArtifacts:     true,
	ToolListReflections:   true,
	ToolForceRecompile:    true,
	// Fase 2 P2 tools (Batch 1)
	ToolSaveSnapshot:      true,
	ToolCompactAOF:        true,
	ToolGetEmbedderStatus: true,
	ToolKVGet:             true,
	ToolKVSet:             true,
	ToolKVDelete:          true,
	// Fase 2 P2 tools (Batch 2)
	ToolGetStats:             true,
	ToolGetPersistenceStatus: true,
	ToolRefreshUserProfile:   true,
	ToolGetEdgeDetails:       true,
	ToolDiffArtifactVersions: true,
	ToolSummarizeMemories:    true,
	// Fase 3 P2 tools (closing MCP interface)
	ToolFindPath:        true,
	ToolReinforceMemory: true,
	ToolListSessions:    true,
	ToolExtractSubgraph: true,
}

// ProfileAdmin contains tools for administrative tasks and data curation.
var ProfileAdmin = map[string]bool{
	ToolFilterVectors:      true,
	ToolListVectors:        true,
	ToolTransferMemory:     true,
	ToolUnpinMemory:        true,
	ToolConfigureAutoLinks: true,
	ToolListUserProfiles:   true,
	// Fase 3 P2: index lifecycle
	ToolCreateIndex: true,
	ToolDeleteIndex: true,
}

var profiles = map[string]map[string]bool{
	"agent": ProfileAgent,
	"admin": ProfileAdmin,
}

// ResolveTools converts a filter string into an allowlist.
//   - "" or "all" returns nil (all tools registered).
//   - "agent" returns the agent profile.
//   - "admin" returns the admin profile.
//   - "agent,admin" returns the union.
//   - Individual tool names (e.g. "save_memory") are also supported.
func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return nil
	}

	result := make(map[string]bool)
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if profile, ok := profiles[token]; ok {
			for tool := range profile {
				result[tool] = true
			}
		} else if allToolNames[token] {
			result[token] = true
		} else {
			slog.Warn("unknown tool or profile in --tools, ignored", "token", token)
		}
	}
	if len(result) == 0 {
		return nil // empty allowlist = all
	}
	return result
}
