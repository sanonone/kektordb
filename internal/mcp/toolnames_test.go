package mcp

import "testing"

// TestAllToolNamesCoversEveryTool guards the allowlist map against drift.
//
// allToolNames decides whether a bare tool name passed to --tools is accepted.
// A constant missing from the map means `--tools=<that name>` logs "unknown
// tool or profile" and silently registers nothing, so the user gets a server
// with an empty tool surface and no error.
func TestAllToolNamesCoversEveryTool(t *testing.T) {
	// Every constant declared as a tool name. Kept as a literal list rather
	// than reflection because tool names are string constants, not a type.
	constants := map[string]string{
		"ToolSaveMemory":           ToolSaveMemory,
		"ToolRecallMemory":         ToolRecallMemory,
		"ToolScopedRecall":         ToolScopedRecall,
		"ToolCreateEntity":         ToolCreateEntity,
		"ToolConnectEntities":      ToolConnectEntities,
		"ToolExploreConnections":   ToolExploreConnections,
		"ToolFindConnection":       ToolFindConnection,
		"ToolStartSession":         ToolStartSession,
		"ToolEndSession":           ToolEndSession,
		"ToolGetUserProfile":       ToolGetUserProfile,
		"ToolListUserProfiles":     ToolListUserProfiles,
		"ToolTransferMemory":       ToolTransferMemory,
		"ToolAdaptiveRetrieve":     ToolAdaptiveRetrieve,
		"ToolFilterVectors":        ToolFilterVectors,
		"ToolListVectors":          ToolListVectors,
		"ToolCheckSubconscious":    ToolCheckSubconscious,
		"ToolResolveConflict":      ToolResolveConflict,
		"ToolAskMetaQuestion":      ToolAskMetaQuestion,
		"ToolEvolveMemory":         ToolEvolveMemory,
		"ToolGetMemoryEvolution":   ToolGetMemoryEvolution,
		"ToolRestoreMemory":        ToolRestoreMemory,
		"ToolUnpinMemory":          ToolUnpinMemory,
		"ToolConfigureAutoLinks":   ToolConfigureAutoLinks,
		"ToolRequestKnowledge":     ToolRequestKnowledge,
		"ToolGetMemory":            ToolGetMemory,
		"ToolGetMemories":          ToolGetMemories,
		"ToolDeleteMemory":         ToolDeleteMemory,
		"ToolUnlinkEntities":       ToolUnlinkEntities,
		"ToolListTemplates":        ToolListTemplates,
		"ToolGetArtifactHistory":   ToolGetArtifactHistory,
		"ToolGetArtifactStaleness": ToolGetArtifactStaleness,
		"ToolTriggerReflection":    ToolTriggerReflection,
		"ToolAssessBelief":         ToolAssessBelief,
		"ToolSearchWithScores":     ToolSearchWithScores,
		"ToolListIndexes":          ToolListIndexes,
		"ToolGetRelations":         ToolGetRelations,
		"ToolGetGardenerStatus":    ToolGetGardenerStatus,
		"ToolListArtifacts":        ToolListArtifacts,
		"ToolListReflections":      ToolListReflections,
		"ToolForceRecompile":       ToolForceRecompile,
		"ToolSaveSnapshot":         ToolSaveSnapshot,
		"ToolCompactAOF":           ToolCompactAOF,
		"ToolGetEmbedderStatus":    ToolGetEmbedderStatus,
		"ToolKVGet":                ToolKVGet,
		"ToolKVSet":                ToolKVSet,
		"ToolKVDelete":             ToolKVDelete,
		"ToolGetStats":             ToolGetStats,
		"ToolGetLLMUsage":          ToolGetLLMUsage,
		"ToolGetPersistenceStatus": ToolGetPersistenceStatus,
		"ToolRefreshUserProfile":   ToolRefreshUserProfile,
		"ToolGetEdgeDetails":       ToolGetEdgeDetails,
		"ToolDiffArtifactVersions": ToolDiffArtifactVersions,
		"ToolSummarizeMemories":    ToolSummarizeMemories,
		"ToolFindPath":             ToolFindPath,
		"ToolReinforceMemory":      ToolReinforceMemory,
		"ToolListSessions":         ToolListSessions,
		"ToolCreateIndex":          ToolCreateIndex,
		"ToolDeleteIndex":          ToolDeleteIndex,
		"ToolExtractSubgraph":      ToolExtractSubgraph,
	}

	for constant, name := range constants {
		if !allToolNames[name] {
			t.Errorf("%s (%q) is missing from allToolNames: "+
				"--tools=%s is rejected with 'unknown tool or profile'", constant, name, name)
		}
	}

	// The reverse: a name in the map that is no longer a tool.
	valid := map[string]bool{}
	for _, name := range constants {
		valid[name] = true
	}
	for name := range allToolNames {
		if !valid[name] {
			t.Errorf("allToolNames lists %q, which is not a declared tool constant", name)
		}
	}
}

// TestResolveToolsAcceptsEveryTool checks the user-facing path: any tool name
// must be usable on its own with --tools.
func TestResolveToolsAcceptsEveryTool(t *testing.T) {
	for name := range AllToolNames() {
		got := ResolveTools(name)
		if got == nil {
			t.Errorf("ResolveTools(%q) returned nil (all tools) instead of just that tool", name)
			continue
		}
		if !got[name] {
			t.Errorf("ResolveTools(%q) does not enable %q", name, name)
		}
	}
}

// TestProfilesOnlyReferenceRealTools catches a typo in a profile, which would
// silently drop a tool from the profile without any error.
func TestProfilesOnlyReferenceRealTools(t *testing.T) {
	for name, profile := range profiles {
		for tool := range profile {
			if !allToolNames[tool] {
				t.Errorf("profile %q references %q, which is not a registered tool", name, tool)
			}
		}
	}
}
