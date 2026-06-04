package compiler

import (
	"fmt"
	"sort"
	"time"
)

// compileFieldDeterministic compiles a single field without LLM,
// using only metadata and graph aggregation.
func (c *Compiler) compileFieldDeterministic(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
) (value any, provenance []Provenance, confidence float64, err error) {
	switch fieldDef.Source {
	case "metadata":
		return c.compileFromMetadata(fieldName, fieldDef, nodes)
	case "graph":
		return c.compileFromGraph(fieldName, fieldDef, nodes)
	case "computed":
		return c.computeField(fieldName, fieldDef, nodes)
	case "graph+llm", "llm":
		return c.compileBestEffort(fieldName, fieldDef, nodes)
	default:
		return c.compileFromMetadata(fieldName, fieldDef, nodes)
	}
}

func (c *Compiler) compileFromMetadata(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
) (any, []Provenance, float64, error) {
	for _, node := range nodes {
		if val, ok := node.Metadata[fieldName]; ok {
			prov := []Provenance{{
				SourceID:   node.ID,
				Confidence: 0.95,
				Evidence:   fmt.Sprintf("metadata field '%s'", fieldName),
				Role:       "primary",
			}}
			return val, prov, 0.95, nil
		}
	}

	// For "name" field, try content as fallback
	if fieldName == "name" {
		for _, node := range nodes {
			if node.Content != "" {
				return node.Content, []Provenance{{
					SourceID:   node.ID,
					Confidence: 0.8,
					Evidence:   node.Content,
					Role:       "primary",
				}}, 0.8, nil
			}
		}
	}

	return nil, nil, 0, nil
}

func (c *Compiler) compileFromGraph(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
) (any, []Provenance, float64, error) {
	switch fieldName {
	case "top_entities", "related_entities":
		return c.graphTopEntities(nodes)
	case "relation_types":
		return c.graphRelationTypes(nodes)
	case "core_facts":
		return c.graphCoreFacts(nodes)
	case "sentiment":
		return c.graphSentiment(nodes)
	default:
		return nil, nil, 0, nil
	}
}

func (c *Compiler) graphTopEntities(nodes []NodeInfo) (any, []Provenance, float64, error) {
	type scoredEntity struct {
		ID    string
		Count int
	}
	var scores []scoredEntity
	for _, n := range nodes {
		scores = append(scores, scoredEntity{ID: n.ID, Count: n.RelationCount})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].Count > scores[j].Count })

	limit := 10
	if len(scores) < limit {
		limit = len(scores)
	}
	result := make([]string, 0, limit)
	var prov []Provenance
	for i := 0; i < limit; i++ {
		result = append(result, scores[i].ID)
		if scores[i].Count > 0 {
			prov = append(prov, Provenance{
				SourceID:   scores[i].ID,
				Confidence: 0.8,
				Evidence:   fmt.Sprintf("connected to %d other nodes", scores[i].Count),
				Role:       "primary",
			})
		}
	}
	return result, prov, 0.8, nil
}

func (c *Compiler) graphRelationTypes(nodes []NodeInfo) (any, []Provenance, float64, error) {
	typeSet := make(map[string]bool)
	var prov []Provenance
	for _, n := range nodes {
		for _, rt := range n.RelationTypes {
			typeSet[rt] = true
		}
		prov = append(prov, Provenance{
			SourceID:   n.ID,
			Confidence: 0.9,
			Evidence:   "graph traversal",
			Role:       "computed",
		})
	}
	types := make([]string, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(types)
	return types, prov, 0.9, nil
}

func (c *Compiler) graphCoreFacts(nodes []NodeInfo) (any, []Provenance, float64, error) {
	var facts []string
	var prov []Provenance
	for _, n := range nodes {
		if n.IsPinned {
			if content, ok := n.Metadata["content"].(string); ok {
				facts = append(facts, content)
				prov = append(prov, Provenance{
					SourceID:   n.ID,
					Confidence: 0.95,
					Evidence:   "pinned core fact",
					Role:       "primary",
				})
			}
		}
	}
	return facts, prov, 0.9, nil
}

func (c *Compiler) graphSentiment(nodes []NodeInfo) (any, []Provenance, float64, error) {
	pos, neg := 0, 0
	for _, n := range nodes {
		if s, ok := n.Metadata["_sentiment"].(string); ok {
			switch s {
			case "positive":
				pos++
			case "negative":
				neg++
			}
		}
	}
	total := pos + neg
	if total == 0 {
		return "neutral", nil, 0.5, nil
	}
	if pos > neg*2 {
		return "positive", nil, 0.7, nil
	}
	if neg > pos*2 {
		return "negative", nil, 0.7, nil
	}
	return "mixed", nil, 0.6, nil
}

func (c *Compiler) computeField(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
) (any, []Provenance, float64, error) {
	var prov []Provenance
	for _, n := range nodes {
		prov = append(prov, Provenance{
			SourceID:   n.ID,
			Confidence: 1.0,
			Evidence:   "computed",
			Role:       "computed",
		})
	}

	switch fieldName {
	case "interaction_count", "memory_count", "node_count":
		return len(nodes), prov, 1.0, nil
	case "relation_count", "connection_count":
		total := 0
		for _, n := range nodes {
			total += n.RelationCount
		}
		return total, prov, 1.0, nil
	case "last_activity", "last_interaction", "last_updated":
		var latest time.Time
		for _, n := range nodes {
			if n.CreatedAt.After(latest) {
				latest = n.CreatedAt
			}
		}
		if latest.IsZero() {
			return nil, nil, 0, nil
		}
		return latest.Format(time.RFC3339), prov, 0.9, nil
	case "duration_minutes":
		if len(nodes) < 2 {
			return nil, nil, 0, nil
		}
		var earliest, latest time.Time
		for _, n := range nodes {
			if n.CreatedAt.IsZero() {
				continue
			}
			if earliest.IsZero() || n.CreatedAt.Before(earliest) {
				earliest = n.CreatedAt
			}
			if n.CreatedAt.After(latest) {
				latest = n.CreatedAt
			}
		}
		if earliest.IsZero() || latest.IsZero() {
			return nil, nil, 0, nil
		}
		return latest.Sub(earliest).Minutes(), prov, 0.8, nil
	default:
		return nil, nil, 0, nil
	}
}

// compileBestEffort attempts to compile an LLM field without LLM,
// collecting raw content snippets with lower confidence.
func (c *Compiler) compileBestEffort(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
) (any, []Provenance, float64, error) {
	var snippets []string
	var prov []Provenance
	for _, n := range nodes {
		if n.Content != "" {
			snippets = append(snippets, n.Content)
			prov = append(prov, Provenance{
				SourceID:   n.ID,
				Confidence: 0.4,
				Evidence:   n.Content,
				Role:       "supporting",
			})
		}
	}
	if len(snippets) == 0 {
		return nil, nil, 0, nil
	}
	if fieldDef.Type == "array" {
		return snippets, prov, 0.4, nil
	}
	if fieldDef.Type == "string" {
		return snippets[0], prov[:1], 0.4, nil
	}
	return nil, nil, 0, nil
}
