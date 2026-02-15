package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

type Service struct {
	engine   *engine.Engine
	embedder embeddings.Embedder
}

func NewService(eng *engine.Engine, emb embeddings.Embedder) *Service {
	return &Service{
		engine:   eng,
		embedder: emb,
	}
}

// ensureIndex helper to create the default index if missing
func (s *Service) ensureIndex(name string) {
	if name == "" {
		name = "mcp_memory"
	}
	if !s.engine.IndexExists(name) {
		// Create default index: Cosine, Float32 (best for compatibility), English
		s.engine.VCreate(name, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	}
}

// --- Tool Handlers ---

func (s *Service) SaveMemory(ctx context.Context, req *mcp.CallToolRequest, args SaveMemoryArgs) (*mcp.CallToolResult, SaveMemoryResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	s.ensureIndex(idx)

	// 1. Embedding
	vec, err := s.embedder.Embed(args.Content)
	if err != nil {
		return nil, SaveMemoryResult{}, fmt.Errorf("embedding error: %w", err)
	}

	// 2. Metadata
	id := fmt.Sprintf("mem_%d", time.Now().UnixNano())
	meta := map[string]any{
		"content":   args.Content,
		"timestamp": time.Now().Format(time.RFC3339),
		"source":    "mcp",
		"type":      "memory",
	}
	if len(args.Tags) > 0 {
		meta["tags"] = args.Tags
	}

	// 3. Store
	if err := s.engine.VAdd(idx, id, vec, meta); err != nil {
		return nil, SaveMemoryResult{}, err
	}

	// 4. Links (Manual Graph Construction)
	for _, target := range args.Links {
		// Split target if user passed "id:rel", otherwise default to "related_to"
		parts := strings.Split(target, ":")
		targetID := parts[0]
		rel := "related_to"
		if len(parts) > 1 {
			rel = parts[1]
		}
		s.engine.VLink(id, targetID, rel, "", 1.0, nil)
	}

	return nil, SaveMemoryResult{MemoryID: id, Status: "saved"}, nil
}

func (s *Service) CreateEntity(ctx context.Context, req *mcp.CallToolRequest, args CreateEntityArgs) (*mcp.CallToolResult, CreateEntityResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	s.ensureIndex(idx)

	meta := map[string]any{
		"type":        args.Type,
		"description": args.Description,
		"is_entity":   true,
	}

	// 1. Try Zero-Vector Insert (Sprint 1.5 Feature)
	err := s.engine.VAdd(idx, args.EntityID, nil, meta)

	// 2. BOOTSTRAP FALLBACK
	// If the index is empty, VAdd fails because it doesn't know the vector dimension.
	// In this case, we generate a real embedding from the description to "bootstrap" the index dimensions.
	if err != nil && strings.Contains(err.Error(), "dimension unknown") {
		// Use ID and Description to generate a semantic vector for this first node
		bootstrapContent := fmt.Sprintf("%s: %s", args.EntityID, args.Description)
		vec, errEmbed := s.embedder.Embed(bootstrapContent)
		if errEmbed != nil {
			return nil, CreateEntityResult{}, fmt.Errorf("failed to bootstrap entity embedding: %w", errEmbed)
		}

		// Retry insert with the real vector
		err = s.engine.VAdd(idx, args.EntityID, vec, meta)
	}

	if err != nil {
		return nil, CreateEntityResult{}, err
	}

	return nil, CreateEntityResult{EntityID: args.EntityID}, nil
}

/*
func (s *Service) CreateEntity(ctx context.Context, req *mcp.CallToolRequest, args CreateEntityArgs) (*mcp.CallToolResult, CreateEntityResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	s.ensureIndex(idx)

	meta := map[string]any{
		"type":        args.Type,
		"description": args.Description,
		"is_entity":   true,
	}

	// Zero-Vector Insert (Sprint 1.5 Feature)
	if err := s.engine.VAdd(idx, args.EntityID, nil, meta); err != nil {
		return nil, CreateEntityResult{}, err
	}

	return nil, CreateEntityResult{EntityID: args.EntityID}, nil
}
*/

func (s *Service) Connect(ctx context.Context, req *mcp.CallToolRequest, args ConnectArgs) (*mcp.CallToolResult, struct{}, error) {
	if err := s.engine.VLink(args.SourceID, args.TargetID, args.Relation, "", 1.0, nil); err != nil {
		return nil, struct{}{}, err
	}
	// Return empty struct as result (success)
	return nil, struct{}{}, nil
}

func (s *Service) Recall(ctx context.Context, req *mcp.CallToolRequest, args RecallArgs) (*mcp.CallToolResult, RecallResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	if !s.engine.IndexExists(idx) {
		return nil, RecallResult{Results: []string{"Memory is empty."}}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	vec, err := s.embedder.Embed(args.Query)
	if err != nil {
		return nil, RecallResult{}, err
	}

	// Hybrid Search (Standard)
	ids, err := s.engine.VSearch(idx, vec, limit, "", "", 0, 0.5, nil)
	if err != nil {
		return nil, RecallResult{}, err
	}

	return nil, s.formatResults(idx, ids), nil
}

func (s *Service) ScopedRecall(ctx context.Context, req *mcp.CallToolRequest, args ScopedRecallArgs) (*mcp.CallToolResult, RecallResult, error) {
	idx := "mcp_memory"
	if !s.engine.IndexExists(idx) {
		return nil, RecallResult{}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	vec, err := s.embedder.Embed(args.Query)
	if err != nil {
		return nil, RecallResult{}, err
	}

	// GRAPH FILTER (Sprint 2 Feature)
	filter := &engine.GraphQuery{
		RootID:    args.RootID,
		Direction: args.Direction,
		MaxDepth:  args.Depth,
	}
	if filter.Direction == "" {
		filter.Direction = "out"
	}
	if filter.MaxDepth == 0 {
		filter.MaxDepth = 2
	}

	ids, err := s.engine.VSearch(idx, vec, limit, "", "", 0, 0.5, filter)
	if err != nil {
		return nil, RecallResult{}, err
	}

	return nil, s.formatResults(idx, ids), nil
}

func (s *Service) Traverse(ctx context.Context, req *mcp.CallToolRequest, args TraverseArgs) (*mcp.CallToolResult, TraverseResult, error) {
	idx := "mcp_memory"
	depth := args.Depth
	if depth <= 0 {
		depth = 1
	}

	// --- FIX: DEFAULT RELATIONS ---
	// If the client doesn't specify which relations to follow, try the standard ones.
	// This ensures "explore" actually finds something without needing perfect knowledge of the schema.
	relations := args.Relations
	if len(relations) == 0 {
		relations = []string{
			"related_to", "about", "mentions", // Standard MCP links
			"parent", "child", "next", "prev", // RAG links
			"belongs_to", "authored_by", // Metadata Auto-linking
		}
	}
	// ------------------------------

	// Pass 'relations' instead of 'args.Relations'
	subgraph, err := s.engine.VExtractSubgraph(idx, args.RootID, relations, depth, 0)
	if err != nil {
		return nil, TraverseResult{}, err
	}

	// Format as a readable description for the LLM
	var sb strings.Builder
	if len(subgraph.Edges) == 0 && len(subgraph.Nodes) <= 1 {
		sb.WriteString(fmt.Sprintf("No connections found around '%s' for relations: %v\n", args.RootID, relations))
	} else {
		sb.WriteString(fmt.Sprintf("Graph context around '%s' (Depth %d):\n", args.RootID, depth))

		for _, edge := range subgraph.Edges {
			if edge.Dir == "out" {
				sb.WriteString(fmt.Sprintf("- [THIS] --(%s)--> %s\n", edge.Relation, edge.Target))
			} else {
				// Incoming edge
				sb.WriteString(fmt.Sprintf("- [THIS] <--(%s)-- %s\n", edge.Relation, edge.Source))
			}
		}

		// Add node details
		sb.WriteString("\nNodes details:\n")
		for _, node := range subgraph.Nodes {
			// Skip printing the root node details again if redundant, or keep it for context
			desc := ""
			if d, ok := node.Metadata["description"]; ok {
				desc = fmt.Sprintf(" (%v)", d)
			} else if c, ok := node.Metadata["content"]; ok {
				strC := fmt.Sprintf("%v", c)
				if len(strC) > 60 {
					strC = strC[:60] + "..."
				}
				desc = fmt.Sprintf(": \"%s\"", strC)
			}

			// Mark the root node visually
			prefix := "- "
			if node.ID == args.RootID {
				prefix = "* "
			}
			sb.WriteString(fmt.Sprintf("%s%s%s\n", prefix, node.ID, desc))
		}
	}

	return nil, TraverseResult{GraphDescription: sb.String()}, nil
}

/*
func (s *Service) Traverse(ctx context.Context, req *mcp.CallToolRequest, args TraverseArgs) (*mcp.CallToolResult, TraverseResult, error) {
	idx := "mcp_memory"
	depth := args.Depth
	if depth <= 0 {
		depth = 1
	}

	// Use the Subgraph Extraction logic (Sprint 1 Feature)
	// Passing specific relations if provided, else empty (all)
	subgraph, err := s.engine.VExtractSubgraph(idx, args.RootID, args.Relations, depth)
	if err != nil {
		return nil, TraverseResult{}, err
	}

	// Format as a readable description for the LLM
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Graph context around '%s':\n", args.RootID))

	for _, edge := range subgraph.Edges {
		if edge.Dir == "out" {
			sb.WriteString(fmt.Sprintf("- %s --[%s]--> %s\n", edge.Source, edge.Relation, edge.Target))
		} else {
			sb.WriteString(fmt.Sprintf("- %s <--[%s]-- %s\n", edge.Target, edge.Relation, edge.Source))
		}
	}

	// Add node details
	sb.WriteString("\nNodes details:\n")
	for _, node := range subgraph.Nodes {
		desc := ""
		if d, ok := node.Metadata["description"]; ok {
			desc = fmt.Sprintf(" (%v)", d)
		} else if c, ok := node.Metadata["content"]; ok {
			// Truncate content
			strC := fmt.Sprintf("%v", c)
			if len(strC) > 50 {
				strC = strC[:50] + "..."
			}
			desc = fmt.Sprintf(": %s", strC)
		}
		sb.WriteString(fmt.Sprintf("- %s%s\n", node.ID, desc))
	}

	return nil, TraverseResult{GraphDescription: sb.String()}, nil
}
*/

// formatResults hydrates the IDs into text strings
func (s *Service) formatResults(idx string, ids []string) RecallResult {
	data, _ := s.engine.VGetMany(idx, ids)
	var res []string
	for _, item := range data {
		content := item.Metadata["content"]
		if content == nil {
			// Fallback for entities
			if desc, ok := item.Metadata["description"]; ok {
				content = fmt.Sprintf("[ENTITY: %s] %s", item.ID, desc)
			} else {
				content = fmt.Sprintf("[NODE: %s]", item.ID)
			}
		} else {
			content = fmt.Sprintf("[%s] %v", item.ID, content)
		}
		res = append(res, fmt.Sprintf("%v", content))
	}
	return RecallResult{Results: res}
}
