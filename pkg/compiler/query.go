package compiler

import (
	"fmt"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/engine"
)

// QuerySources gathers source nodes from the knowledge graph.
func (c *Compiler) QuerySources(spec SourceSpec, indexName string) ([]NodeInfo, error) {
	switch spec.Type {
	case "graph_query", "":
		return c.queryFromGraph(spec, indexName)
	case "semantic_search":
		return c.queryFromSearch(spec, indexName)
	case "all":
		graphNodes, gErr := c.queryFromGraph(spec, indexName)
		searchNodes, sErr := c.queryFromSearch(spec, indexName)
		if gErr != nil && sErr != nil {
			return nil, fmt.Errorf("graph_query: %v; semantic_search: %v", gErr, sErr)
		}
		return deduplicateNodes(append(graphNodes, searchNodes...)), nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", spec.Type)
	}
}

// queryFromGraph gathers nodes via BFS subgraph extraction.
func (c *Compiler) queryFromGraph(spec SourceSpec, indexName string) ([]NodeInfo, error) {
	entityID := fmt.Sprintf("%s:%s", spec.Entity.Type, spec.Entity.ID)

	rootIDs, err := c.eng.VFilter(indexName, fmt.Sprintf(
		"entity_id='%s'", spec.Entity.ID,
	), 100)
	if err != nil || len(rootIDs) == 0 {
		rootIDs = []string{entityID}
	}

	depth := spec.Depth
	if depth <= 0 {
		depth = 2
	}

	subgraph, err := c.eng.VExtractSubgraph(
		indexName, rootIDs[0], nil, depth, 0, nil, 0.0,
	)
	if err != nil {
		return nil, fmt.Errorf("extract subgraph: %w", err)
	}

	return c.subgraphToNodeInfo(subgraph, indexName), nil
}

// queryFromSearch gathers nodes via semantic search using the embedder.
// Falls back to graph query if embedder is unavailable.
func (c *Compiler) queryFromSearch(spec SourceSpec, indexName string) ([]NodeInfo, error) {
	if c.embedder == nil {
		// Fallback to graph query when no embedder available
		spec.Type = "graph_query"
		return c.queryFromGraph(spec, indexName)
	}

	query := spec.Query
	if query == "" {
		query = fmt.Sprintf("%s %s", spec.Entity.Type, spec.Entity.ID)
	}

	vec, err := c.embedder.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	k := 20
	if spec.Depth > 0 && spec.Depth < k {
		k = spec.Depth * 10
	}

	results, err := c.eng.VSearch(indexName, vec, k, "", "", 0, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}

	nodes := make([]NodeInfo, 0, len(results))
	for _, id := range results {
		data, err := c.eng.VGet(indexName, id)
		if err != nil {
			continue
		}
		nodes = append(nodes, vectorDataToNodeInfo(id, data))
	}

	return nodes, nil
}

// subgraphToNodeInfo converts engine.SubgraphResult nodes to NodeInfo.
func (c *Compiler) subgraphToNodeInfo(sg *engine.SubgraphResult, indexName string) []NodeInfo {
	if sg == nil {
		return nil
	}

	nodes := make([]NodeInfo, 0, len(sg.Nodes))
	relationCounts := make(map[string]int)
	relationTypes := make(map[string]map[string]bool)

	for _, edge := range sg.Edges {
		relationCounts[edge.Source]++
		relationCounts[edge.Target]++
		if relationTypes[edge.Source] == nil {
			relationTypes[edge.Source] = make(map[string]bool)
		}
		relationTypes[edge.Source][edge.Relation] = true
		if relationTypes[edge.Target] == nil {
			relationTypes[edge.Target] = make(map[string]bool)
		}
		relationTypes[edge.Target][edge.Relation] = true
	}

	for _, node := range sg.Nodes {
		ni := NodeInfo{
			ID:            node.ID,
			Metadata:      node.Metadata,
			RelationCount: relationCounts[node.ID],
		}

		if content, ok := node.Metadata["content"].(string); ok {
			ni.Content = content
		}
		if ca, ok := node.Metadata["_created_at"].(float64); ok {
			ni.CreatedAt = time.Unix(int64(ca), 0)
		}
		if pinned, ok := node.Metadata["_pinned"].(bool); ok {
			ni.IsPinned = pinned
		}

		for rt := range relationTypes[node.ID] {
			ni.RelationTypes = append(ni.RelationTypes, rt)
		}

		nodes = append(nodes, ni)
	}

	return nodes
}

// vectorDataToNodeInfo converts a core.VectorData to a NodeInfo.
func vectorDataToNodeInfo(id string, data core.VectorData) NodeInfo {
	ni := NodeInfo{
		ID:       id,
		Metadata: data.Metadata,
	}
	if content, ok := data.Metadata["content"].(string); ok {
		ni.Content = content
	}
	if ca, ok := data.Metadata["_created_at"].(float64); ok {
		ni.CreatedAt = time.Unix(int64(ca), 0)
	}
	if pinned, ok := data.Metadata["_pinned"].(bool); ok {
		ni.IsPinned = pinned
	}
	return ni
}
