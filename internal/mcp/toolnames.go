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
)

// allToolNames maps every known MCP tool name for validation.
var allToolNames = map[string]bool{
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
	ToolListUserProfiles:   true,
	ToolTransferMemory:     true,
	ToolAdaptiveRetrieve:   true,
	ToolFilterVectors:      true,
	ToolListVectors:        true,
	ToolCheckSubconscious:  true,
	ToolResolveConflict:    true,
	ToolAskMetaQuestion:    true,
	ToolEvolveMemory:       true,
	ToolGetMemoryEvolution: true,
	ToolUnpinMemory:        true,
	ToolConfigureAutoLinks: true,
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
}

// ProfileAdmin contains tools for administrative tasks and data curation.
var ProfileAdmin = map[string]bool{
	ToolFilterVectors:      true,
	ToolListVectors:        true,
	ToolTransferMemory:     true,
	ToolUnpinMemory:        true,
	ToolConfigureAutoLinks: true,
	ToolListUserProfiles:   true,
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
