package compiler

// enrichProvenance validates LLM-provided source IDs against actual nodes
// and removes any that reference non-existent nodes. If the result is empty,
// it generates a single auto-provenance entry from the first source node.
func (c *Compiler) enrichProvenance(llmSources []Provenance, actualNodes []NodeInfo) []Provenance {
	nodeIDs := make(map[string]bool, len(actualNodes))
	for _, n := range actualNodes {
		nodeIDs[n.ID] = true
	}

	result := make([]Provenance, 0, len(llmSources))
	for _, src := range llmSources {
		if nodeIDs[src.SourceID] {
			if src.Role == "" {
				src.Role = "primary"
			}
			result = append(result, src)
		}
	}

	if len(result) == 0 && len(actualNodes) > 0 {
		result = append(result, Provenance{
			SourceID:   actualNodes[0].ID,
			Confidence: 0.3,
			Evidence:   "auto-generated fallback provenance",
			Role:       "inferred",
		})
	}

	return result
}

// validateProvenance ensures every field in the artifact has at least
// one provenance entry. Fields without provenance get auto-generated entries.
func (c *Compiler) validateProvenance(artifact *Artifact) {
	for field := range artifact.Data {
		if _, ok := artifact.Provenance[field]; !ok || len(artifact.Provenance[field]) == 0 {
			if len(artifact.SourceNodeIDs) > 0 {
				artifact.Provenance[field] = []Provenance{{
					SourceID:   artifact.SourceNodeIDs[0],
					Confidence: 0.3,
					Evidence:   "auto-generated provenance",
					Role:       "inferred",
				}}
			}
		}
	}
}
