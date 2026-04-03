package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

type Service struct {
	engine   *engine.Engine
	embedder embeddings.Embedder

	// Session management (per-connection)
	sessions   map[string]*SessionContext // key: connection ID or session ID
	sessionsMu sync.RWMutex
}

func NewService(eng *engine.Engine, emb embeddings.Embedder) *Service {
	return &Service{
		engine:   eng,
		embedder: emb,
		sessions: make(map[string]*SessionContext),
	}
}

// --- Session Management Helpers ---

// getSession retrieves a session by ID.
func (s *Service) getSession(sessionID string) *SessionContext {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[sessionID]
}

// setSession stores a session.
func (s *Service) setSession(sess *SessionContext) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.sessions[sess.ID] = sess
}

// removeSession removes a session.
func (s *Service) removeSession(sessionID string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	delete(s.sessions, sessionID)
}

// ensureIndex helper to create the default index if missing
func (s *Service) ensureIndex(name string) {
	if name == "" {
		name = "mcp_memory"
	}
	if !s.engine.IndexExists(name) {
		// Default Memory Config: Enabled, 30 Days Half-Life
		// (Agent memories should last longer than a typical chat cache)
		memConfig := &hnsw.MemoryConfig{
			Enabled:       true,
			DecayHalfLife: hnsw.Duration(30 * 24 * time.Hour),
		}

		// Create index with Memory Config
		s.engine.VCreate(name, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, memConfig)
	}
}

// --- Tool Handlers ---

func (s *Service) SaveMemory(ctx context.Context, req *mcp.CallToolRequest, args SaveMemoryArgs) (*mcp.CallToolResult, SaveMemoryResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	s.ensureIndex(idx)

	// 1. Validate and normalize layer
	layer := args.Layer
	if layer == "" {
		layer = "episodic" // default
	}
	validLayers := map[string]bool{"episodic": true, "semantic": true, "procedural": true}
	if !validLayers[layer] {
		return nil, SaveMemoryResult{}, fmt.Errorf("invalid layer: %s (must be episodic, semantic, or procedural)", layer)
	}

	// 2. Embedding
	vec, err := s.embedder.Embed(args.Content)
	if err != nil {
		return nil, SaveMemoryResult{}, fmt.Errorf("embedding error: %w", err)
	}

	// 3. Metadata
	id := fmt.Sprintf("mem_%d", time.Now().UnixNano())
	meta := map[string]any{
		"content":      args.Content,
		"timestamp":    time.Now().Format(time.RFC3339),
		"source":       "mcp",
		"type":         "memory",
		"memory_layer": layer, // Store the layer
	}
	if len(args.Tags) > 0 {
		meta["tags"] = args.Tags
	}

	// Handle Pinning (ExplicitPinned overrides everything)
	if args.ExplicitPinned != nil {
		meta["_pinned"] = *args.ExplicitPinned
	} else if args.Pin {
		meta["_pinned"] = true
	}
	// Note: layer's pinned_by_default is applied in VAdd via applyLayerDefaults

	// 4. Session handling (explicit session_id from args)
	if args.SessionID != "" {
		meta["session_id"] = args.SessionID
		// Propagate user_id from session if present (for profiling)
		if sess := s.getSession(args.SessionID); sess != nil {
			if userID, ok := sess.Metadata["user_id"].(string); ok && userID != "" {
				meta["user_id"] = userID
			}
		}
	}

	// 5. Explicit user_id (overrides session-based)
	if args.UserID != "" {
		meta["user_id"] = args.UserID
	}

	// 5. Store
	if err := s.engine.VAdd(idx, id, vec, meta); err != nil {
		return nil, SaveMemoryResult{}, err
	}

	// 6. Links (Manual Graph Construction)
	for _, target := range args.Links {
		// Split target if user passed "id:rel", otherwise default to "related_to"
		parts := strings.Split(target, ":")
		targetID := parts[0]
		rel := "related_to"
		if len(parts) > 1 {
			rel = parts[1]
		}
		s.engine.VLink(idx, id, targetID, rel, "", 1.0, nil)
	}

	// 7. Auto-link to session if applicable
	if args.SessionID != "" {
		s.engine.VLink(idx, id, args.SessionID, "belongs_to", "", 1.0, nil)
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
	idx := "mcp_memory"

	if err := s.engine.VLink(idx, args.SourceID, args.TargetID, args.Relation, "", 1.0, nil); err != nil {
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

	// Build layer filter if specific layers requested
	var filter string
	if len(args.Layers) > 0 {
		var parts []string
		for _, layer := range args.Layers {
			parts = append(parts, fmt.Sprintf("memory_layer='%s'", layer))
		}
		filter = strings.Join(parts, " OR ")
	}

	// Hybrid Search with optional layer filter
	ids, err := s.engine.VSearch(idx, vec, limit*2, filter, "", 0, 0.5, nil) // Fetch more for re-ranking
	if err != nil {
		return nil, RecallResult{}, err
	}

	// Apply layer weights if configured
	if len(args.LayerWeights) > 0 && len(ids) > 0 {
		ids = s.applyLayerWeights(idx, ids, args.LayerWeights, limit)
	} else if len(ids) > limit {
		ids = ids[:limit]
	}

	if args.Reinforce && len(ids) > 0 {
		// Fire and forget reinforcement (non-blocking for the response)
		go func() {
			if err := s.engine.VReinforce(idx, ids); err != nil {
				// Log error internally if needed
			}
		}()
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

	// --- DEFAULT RELATIONS ---
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

	// Semantic Navigation Setup
	var guideVec []float32
	if args.GuideQuery != "" {
		v, err := s.embedder.Embed(args.GuideQuery)
		if err != nil {
			return nil, TraverseResult{}, fmt.Errorf("guide embedding failed: %w", err)
		}
		guideVec = v
	}

	threshold := args.Threshold
	if threshold == 0 && len(guideVec) > 0 {
		threshold = 0.4 // Default strictness for Cosine
	}

	// Pass 'relations' instead of 'args.Relations'
	subgraph, err := s.engine.VExtractSubgraph(idx, args.RootID, relations, depth, args.AtTime, guideVec, threshold)
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

func (s *Service) FindConnection(ctx context.Context, req *mcp.CallToolRequest, args FindConnectionArgs) (*mcp.CallToolResult, FindConnectionResult, error) {
	idx := "mcp_memory"
	if !s.engine.IndexExists(idx) {
		return nil, FindConnectionResult{PathDescription: "No memory index found."}, nil
	}

	relations := args.Relations
	if len(relations) == 0 {
		// Default relations to traverse if user didn't specify
		relations = []string{"related_to", "about", "mentions", "parent", "child", "next", "prev", "belongs_to", "authored_by"}
	}

	// Call Engine.FindPath
	// MaxDepth 4 is usually enough for causal links
	res, err := s.engine.FindPath(idx, args.SourceID, args.TargetID, relations, 4, args.AtTime)
	if err != nil {
		return nil, FindConnectionResult{}, err
	}
	if res == nil {
		return nil, FindConnectionResult{PathDescription: fmt.Sprintf("No path found between '%s' and '%s'", args.SourceID, args.TargetID)}, nil
	}

	// Format Path for LLM: "A --[rel]--> B --[rel]--> C"
	// PathResult contains the list of Nodes in order.
	// Reconstructing the edge description is nice for the LLM.

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Connection found (%d steps):\n", len(res.Path)-1))

	// Simple output: Node list
	sb.WriteString(strings.Join(res.Path, " -> "))

	// Detailed output using Edges info if available
	if len(res.Edges) > 0 {
		sb.WriteString("\n\nDetails:\n")
		for _, edge := range res.Edges {
			sb.WriteString(fmt.Sprintf("%s --[%s]--> %s\n", edge.Source, edge.Relation, edge.Target))
		}
	}

	return nil, FindConnectionResult{PathDescription: sb.String()}, nil
}

func (s *Service) FilterVectors(ctx context.Context, req *mcp.CallToolRequest, args FilterVectorsArgs) (*mcp.CallToolResult, FilterVectorsResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	ids, err := s.engine.VFilter(idx, args.Filter, limit)
	if err != nil {
		return nil, FilterVectorsResult{}, fmt.Errorf("filter error: %w", err)
	}

	return nil, FilterVectorsResult{Results: ids}, nil
}

func (s *Service) UnpinMemory(ctx context.Context, req *mcp.CallToolRequest, args UnpinMemoryArgs) (*mcp.CallToolResult, UnpinMemoryResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	data, err := s.engine.VGet(idx, args.MemoryID)
	if err != nil {
		return nil, UnpinMemoryResult{}, fmt.Errorf("memory not found: %w", err)
	}

	delete(data.Metadata, "_pinned")

	err = s.engine.VAdd(idx, args.MemoryID, data.Vector, data.Metadata)
	if err != nil {
		return nil, UnpinMemoryResult{}, fmt.Errorf("failed to unpin: %w", err)
	}

	return nil, UnpinMemoryResult{Status: "unpinned"}, nil
}

func (s *Service) ConfigureAutoLinks(ctx context.Context, req *mcp.CallToolRequest, args ConfigureAutoLinksArgs) (*mcp.CallToolResult, ConfigureAutoLinksResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	rules := make([]hnsw.AutoLinkRule, len(args.Rules))
	for i, r := range args.Rules {
		rules[i] = hnsw.AutoLinkRule{
			MetadataField: r.MetadataField,
			RelationType:  r.RelationType,
			CreateNode:    r.CreateNode,
		}
	}

	err := s.engine.VUpdateAutoLinks(idx, rules)
	if err != nil {
		return nil, ConfigureAutoLinksResult{}, fmt.Errorf("failed to update auto-links: %w", err)
	}

	return nil, ConfigureAutoLinksResult{Status: "updated"}, nil
}

func (s *Service) ListVectors(ctx context.Context, req *mcp.CallToolRequest, args ListVectorsArgs) (*mcp.CallToolResult, ListVectorsResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	var vectors []struct {
		ID       string         `json:"id"`
		Metadata map[string]any `json:"metadata"`
	}

	count := 0
	skipped := 0
	var idsToFetch []string

	idxRef, ok := s.engine.DB.GetVectorIndex(idx)
	if !ok {
		return nil, ListVectorsResult{Vectors: vectors, HasMore: false}, nil
	}
	hnswIdx, ok := idxRef.(*hnsw.Index)
	if !ok {
		return nil, ListVectorsResult{Vectors: vectors, HasMore: false}, nil
	}

	hnswIdx.IterateRaw(func(id string, vec interface{}) {
		if skipped < offset {
			skipped++
			return
		}
		if count >= limit {
			return
		}
		idsToFetch = append(idsToFetch, id)
		count++
	})

	// Safely fetch data in batch without holding the Index Read Lock
	if len(idsToFetch) > 0 {
		vectorDataList, _ := s.engine.VGetMany(idx, idsToFetch)
		for _, vData := range vectorDataList {
			vectors = append(vectors, struct {
				ID       string         `json:"id"`
				Metadata map[string]any `json:"metadata"`
			}{
				ID:       vData.ID,
				Metadata: vData.Metadata,
			})
		}
	}

	hasMore := count == limit

	return nil, ListVectorsResult{
		Vectors: vectors,
		HasMore: hasMore,
	}, nil
}

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

// applyLayerWeights re-ranks results based on per-layer weights.
// Default weights: semantic=0.5, episodic=0.4, procedural=0.1
func (s *Service) applyLayerWeights(idx string, ids []string, weights map[string]float64, limit int) []string {
	// Fetch metadata for all IDs
	data, _ := s.engine.VGetMany(idx, ids)
	if len(data) == 0 {
		return ids[:min(limit, len(ids))]
	}

	// Default weights if not specified
	defaultWeights := map[string]float64{
		"semantic":   0.5,
		"episodic":   0.4,
		"procedural": 0.1,
	}

	// Merge user weights with defaults
	layerWeights := make(map[string]float64)
	for k, v := range defaultWeights {
		layerWeights[k] = v
	}
	for k, v := range weights {
		if v > 0 {
			layerWeights[k] = v
		}
	}

	// Score and sort results
	type weightedResult struct {
		id     string
		weight float64
		index  int // preserve original order for stability
	}

	results := make([]weightedResult, 0, len(data))
	for i, item := range data {
		layer := "episodic" // default
		if l, ok := item.Metadata["memory_layer"].(string); ok && l != "" {
			layer = l
		}

		weight := layerWeights[layer]
		if weight == 0 {
			weight = 0.1 // minimum weight
		}

		results = append(results, weightedResult{
			id:     item.ID,
			weight: weight,
			index:  i,
		})
	}

	// Sort by weight descending, then by original index for stability
	sort.Slice(results, func(i, j int) bool {
		if results[i].weight != results[j].weight {
			return results[i].weight > results[j].weight
		}
		return results[i].index < results[j].index
	})

	// Return top N
	if len(results) > limit {
		results = results[:limit]
	}

	ids = make([]string, len(results))
	for i, r := range results {
		ids[i] = r.id
	}
	return ids
}

// CheckSubconscious retrieves unresolved reflections generated by the Gardener.
func (s *Service) CheckSubconscious(ctx context.Context, req *mcp.CallToolRequest, args CheckSubconsciousArgs) (*mcp.CallToolResult, CheckSubconsciousResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	if !s.engine.IndexExists(idx) {
		return nil, CheckSubconsciousResult{Reflections: []string{"No database found."}}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	// RICERCA PURA SUI METADATI (O(1) via Roaring Bitmaps)
	filter := "type='reflection' OR type='user_profile_insight' OR type='failure_pattern' OR type='knowledge_evolution'"
	reflectionIDs, err := s.engine.VFilter(idx, filter, limit)
	if err != nil {
		return nil, CheckSubconsciousResult{}, fmt.Errorf("failed to query subconscious: %w", err)
	}

	if len(reflectionIDs) == 0 {
		return nil, CheckSubconsciousResult{Reflections: []string{"No reflections found. Your memory is clean."}}, nil
	}

	// Recuperiamo i dati completi
	data, _ := s.engine.VGetMany(idx, reflectionIDs)
	var res []string

	for _, item := range data {
		content := item.Metadata["content"]
		dateStr := ""
		if val, ok := item.Metadata["_created_at"]; ok {
			if ts, ok := val.(float64); ok {
				dateStr = time.Unix(int64(ts), 0).Format("02 Jan 2006 15:04")
			}
		}

		// Gather context: who is involved in this reflection?
		linkedMemories := ""
		relationTypes := []string{"contradicts", "focus_shifted", "suggests_link", "sentiment_shift", "became_central", "knowledge_decay", "failure_pattern_of", "derived_from_interactions", "evolution_of"}
		for _, rel := range relationTypes {
			if targets, found := s.engine.VGetLinks(idx, item.ID, rel); found {
				linkedMemories = fmt.Sprintf(" [%s: %s]", rel, strings.Join(targets, ", "))
				break
			}
		}

		// Show action_required and suggested_resolution if present.
		actionStr := ""
		if ar, ok := item.Metadata["action_required"].(bool); ok && ar {
			actionStr = " [ACTION REQUIRED]"
		}
		severityStr := ""
		if sev, ok := item.Metadata["severity"].(string); ok && sev != "" {
			severityStr = fmt.Sprintf(" [SEVERITY: %s]", strings.ToUpper(sev))
		}
		resolutionStr := ""
		if sr, ok := item.Metadata["suggested_resolution"].(string); ok && sr != "" {
			resolutionStr = fmt.Sprintf(" Suggested: %s", sr)
		}

		res = append(res, fmt.Sprintf("[Reflection ID: %s] (%s) %v%s%s%s%s", item.ID, dateStr, content, linkedMemories, actionStr, severityStr, resolutionStr))
	}

	return nil, CheckSubconsciousResult{Reflections: res}, nil
}

// ResolveConflict applies the AI's decision to a pending reflection, archiving false data.
func (s *Service) ResolveConflict(ctx context.Context, req *mcp.CallToolRequest, args ResolveConflictArgs) (*mcp.CallToolResult, ResolveConflictResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	// 1. Marchiamo la Reflection come "risolta"
	updateProps := map[string]any{
		"status":      "resolved",
		"resolution":  args.Resolution,
		"_updated_at": float64(time.Now().Unix()),
	}

	err := s.engine.VSetMetadata(idx, args.ReflectionID, updateProps)
	if err != nil {
		return nil, ResolveConflictResult{}, fmt.Errorf("failed to update reflection: %w", err)
	}

	// 2. Se l'AI ha deciso che una delle memorie originali era falsa/vecchia, la archiviamo
	if args.DiscardID != "" {
		// Non la cancelliamo fisicamente per mantenere la storia. La nascondiamo (Soft Delete / Archive).
		discardProps := map[string]any{
			"_archived":      true,
			"invalidated_by": args.ReflectionID,
		}
		_ = s.engine.VSetMetadata(idx, args.DiscardID, discardProps)

		// In più, scolleghiamo eventuali archi attivi dal Grafo per pulizia topologica
		// VDelete farà scattare il Cascade Delete sugli archi (Soft Delete).
		_ = s.engine.VDelete(idx, args.DiscardID)
	}

	return nil, ResolveConflictResult{Status: "Conflict resolved successfully. Memory consolidated."}, nil
}

// AskMetaQuestion queries exclusively the meta-knowledge layer of the database.
func (s *Service) AskMetaQuestion(ctx context.Context, req *mcp.CallToolRequest, args AskMetaQuestionArgs) (*mcp.CallToolResult, AskMetaQuestionResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	if !s.engine.IndexExists(idx) {
		return nil, AskMetaQuestionResult{Reflections: []string{"Memory is empty."}}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	// 1. Convertiamo la meta-domanda in un vettore
	vec, err := s.embedder.Embed(args.Query)
	if err != nil {
		return nil, AskMetaQuestionResult{}, err
	}

	// 2. Filtro stringente: SOLO riflessioni e memorie consolidate
	// Le Roaring Bitmaps faranno un'intersezione (OR) in zero nanosecondi
	filter := "type='reflection' OR type='consolidated_memory' OR type='user_profile_insight' OR type='failure_pattern' OR type='knowledge_evolution'"

	// 3. Eseguiamo la ricerca semantica filtrata
	ids, err := s.engine.VSearch(idx, vec, limit, filter, "", 0, 0.5, nil)
	if err != nil {
		return nil, AskMetaQuestionResult{}, err
	}

	if len(ids) == 0 {
		return nil, AskMetaQuestionResult{
			Reflections: []string{"No meta-knowledge found for this query. The agent hasn't generated deep insights on this topic yet."},
		}, nil
	}

	// 4. Formattiamo i risultati
	// Sfruttiamo l'helper formatResults che abbiamo già modificato per includere
	// automaticamente il preambolo temporale "[Memoria del 18 Mar 2026]".
	formatted := s.formatResults(idx, ids)

	return nil, AskMetaQuestionResult{Reflections: formatted.Results}, nil
}

// --- Session Management Tools ---

// StartSession creates a new session entity and tracks it.
func (s *Service) StartSession(ctx context.Context, req *mcp.CallToolRequest, args StartSessionArgs) (*mcp.CallToolResult, StartSessionResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}
	s.ensureIndex(idx)

	// Generate session ID if not provided
	sessionID := args.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session::%d", time.Now().UnixNano())
	}

	// Create session entity metadata
	meta := map[string]any{
		"type":           "session",
		"session_status": "active",
		"started_at":     time.Now().Format(time.RFC3339),
	}
	if args.AgentID != "" {
		meta["agent_id"] = args.AgentID
	}
	if args.UserID != "" {
		meta["user_id"] = args.UserID
	}
	if args.Context != "" {
		meta["context"] = args.Context
	}

	// Try zero-vector insert first (bootstrap if needed)
	err := s.engine.VAdd(idx, sessionID, nil, meta)
	if err != nil && strings.Contains(err.Error(), "dimension unknown") {
		// Bootstrap: create with a dummy embedding from context
		bootstrapContent := fmt.Sprintf("Session %s: %s", sessionID, args.Context)
		vec, errEmbed := s.embedder.Embed(bootstrapContent)
		if errEmbed != nil {
			return nil, StartSessionResult{}, fmt.Errorf("failed to bootstrap session embedding: %w", errEmbed)
		}
		err = s.engine.VAdd(idx, sessionID, vec, meta)
	}
	if err != nil {
		return nil, StartSessionResult{}, fmt.Errorf("failed to create session: %w", err)
	}

	// Track the session
	sess := &SessionContext{
		ID:        sessionID,
		IndexName: idx,
		StartTime: time.Now().UnixNano(),
		Metadata: map[string]any{
			"agent_id": args.AgentID,
			"user_id":  args.UserID,
			"context":  args.Context,
		},
	}
	s.setSession(sess)

	return nil, StartSessionResult{
		SessionID: sessionID,
		Status:    "active",
		Message:   fmt.Sprintf("Session started. All memories will be linked to %s", sessionID),
	}, nil
}

// EndSession closes a session and triggers summarization.
// Note: The actual summarization is done by the Gardener via HTTP API.
func (s *Service) EndSession(ctx context.Context, req *mcp.CallToolRequest, args EndSessionArgs) (*mcp.CallToolResult, EndSessionResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	sessionID := args.SessionID
	if sessionID == "" {
		return nil, EndSessionResult{}, fmt.Errorf("session_id is required")
	}

	// Verify session exists
	data, err := s.engine.VGet(idx, sessionID)
	if err != nil {
		return nil, EndSessionResult{}, fmt.Errorf("session not found: %w", err)
	}

	// Update session status to ended
	updateProps := map[string]any{
		"session_status": "ended",
		"ended_at":       time.Now().Format(time.RFC3339),
	}

	// Preserve existing metadata
	for k, v := range data.Metadata {
		if _, exists := updateProps[k]; !exists {
			updateProps[k] = v
		}
	}

	if err := s.engine.VSetMetadata(idx, sessionID, updateProps); err != nil {
		return nil, EndSessionResult{}, fmt.Errorf("failed to end session: %w", err)
	}

	// Remove from active sessions
	s.removeSession(sessionID)

	return nil, EndSessionResult{
		SessionID: sessionID,
		Status:    "ended",
		Message:   fmt.Sprintf("Session %s ended. Summarization triggered in background.", sessionID),
	}, nil
}

// --- User Profile Tools ---

func (s *Service) GetUserProfile(ctx context.Context, req *mcp.CallToolRequest, args GetUserProfileArgs) (*mcp.CallToolResult, GetUserProfileResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	if args.UserID == "" {
		return nil, GetUserProfileResult{}, fmt.Errorf("user_id is required")
	}

	profileID := fmt.Sprintf("_profile::%s", args.UserID)
	data, err := s.engine.VGet(idx, profileID)
	if err != nil {
		return nil, GetUserProfileResult{
			UserID: args.UserID,
			Exists: false,
		}, nil
	}

	var expertise []string
	if areas, ok := data.Metadata["expertise_areas"].(string); ok && areas != "" {
		expertise = strings.Split(areas, ",")
	}

	var dislikes []string
	if dis, ok := data.Metadata["dislikes"].(string); ok && dis != "" {
		dislikes = strings.Split(dis, ",")
	}

	result := GetUserProfileResult{
		UserID:             args.UserID,
		Exists:             true,
		CommunicationStyle: getString(data.Metadata, "communication_style"),
		Language:           getString(data.Metadata, "language"),
		ExpertiseAreas:     expertise,
		Dislikes:           dislikes,
		ResponseLength:     getString(data.Metadata, "response_length"),
		Confidence:         getFloat(data.Metadata, "confidence"),
		ProfileData:        getString(data.Metadata, "profile_data"),
		LastUpdated:        int64(getFloat(data.Metadata, "last_updated")),
		InteractionCount:   int(getFloat(data.Metadata, "interaction_count")),
	}

	return nil, result, nil
}

func (s *Service) ListUserProfiles(ctx context.Context, req *mcp.CallToolRequest, args ListUserProfilesArgs) (*mcp.CallToolResult, ListUserProfilesResult, error) {
	idx := args.IndexName
	if idx == "" {
		idx = "mcp_memory"
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	ids, err := s.engine.VFilter(idx, "type='user_profile'", 0)
	if err != nil {
		return nil, ListUserProfilesResult{}, fmt.Errorf("failed to list profiles: %w", err)
	}

	if len(ids) == 0 {
		return nil, ListUserProfilesResult{
			Profiles: []UserProfileItem{},
			HasMore:  false,
		}, nil
	}

	start := offset
	if start >= len(ids) {
		return nil, ListUserProfilesResult{
			Profiles: []UserProfileItem{},
			HasMore:  false,
		}, nil
	}

	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}

	pageIDs := ids[start:end]

	var profiles []UserProfileItem
	for _, id := range pageIDs {
		data, err := s.engine.VGet(idx, id)
		if err != nil {
			continue
		}

		userID := getString(data.Metadata, "user_id")
		if userID == "" {
			userID = strings.TrimPrefix(id, "_profile::")
		}

		profiles = append(profiles, UserProfileItem{
			UserID:             userID,
			Confidence:         getFloat(data.Metadata, "confidence"),
			LastUpdated:        int64(getFloat(data.Metadata, "last_updated")),
			CommunicationStyle: getString(data.Metadata, "communication_style"),
		})
	}

	hasMore := (offset + limit) < len(ids)

	return nil, ListUserProfilesResult{
		Profiles: profiles,
		HasMore:  hasMore,
	}, nil
}

// Helper functions for safe type conversion
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// --- Memory Transfer ---

// TransferMemory transfers memories from one index to another.
// It preserves metadata, handles dimension mismatches (zero-vector fallback),
// merges metadata for existing nodes, and optionally copies graph topology.
func (s *Service) TransferMemory(ctx context.Context, req *mcp.CallToolRequest, args TransferMemoryArgs) (*mcp.CallToolResult, TransferMemoryResult, error) {
	// 1. Validation
	if args.SourceIndex == "" || args.TargetIndex == "" {
		return nil, TransferMemoryResult{}, fmt.Errorf("source_index and target_index are required")
	}
	if args.SourceIndex == args.TargetIndex {
		return nil, TransferMemoryResult{}, fmt.Errorf("source and target cannot be the same")
	}
	if args.Query == "" {
		return nil, TransferMemoryResult{}, fmt.Errorf("query is required")
	}

	// 2. Limit validation (hard cap 500)
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// 3. Check indices exist
	if !s.engine.IndexExists(args.SourceIndex) {
		return nil, TransferMemoryResult{}, fmt.Errorf("source index '%s' not found", args.SourceIndex)
	}
	if !s.engine.IndexExists(args.TargetIndex) {
		return nil, TransferMemoryResult{}, fmt.Errorf("target index '%s' not found", args.TargetIndex)
	}

	// 4. Search in source index
	vec, err := s.embedder.Embed(args.Query)
	if err != nil {
		return nil, TransferMemoryResult{}, fmt.Errorf("embedding error: %w", err)
	}

	sourceIDs, err := s.engine.VSearch(args.SourceIndex, vec, limit, "", "", 0, 0.5, nil)
	if err != nil {
		return nil, TransferMemoryResult{}, fmt.Errorf("search failed: %w", err)
	}

	if len(sourceIDs) == 0 {
		return nil, TransferMemoryResult{
			TransferredCount: 0,
			Message:          "No memories found matching query",
		}, nil
	}

	// 5. Fetch source data
	sourceData, _ := s.engine.VGetMany(args.SourceIndex, sourceIDs)

	// 6. Get target dimension
	targetDim := s.getIndexDimension(args.TargetIndex)

	// 7. Prepare transfer
	var toTransfer []types.BatchObject
	var transferredIDs []string
	skippedCount := 0
	idMapping := make(map[string]string) // sourceID -> targetID (for graph copy)

	for _, item := range sourceData {
		// Prepare metadata with provenance
		newMeta := make(map[string]any)
		for k, v := range item.Metadata {
			newMeta[k] = v
		}

		// Add provenance
		newMeta["_transferred_from"] = fmt.Sprintf("%s::%s", args.SourceIndex, item.ID)
		newMeta["_transferred_at"] = time.Now().Format(time.RFC3339)

		// Add transfer reason if provided
		if args.TransferReason != "" {
			newMeta["_transfer_reason"] = args.TransferReason
		}

		// Handle vector (dimension check)
		var newVec []float32
		if len(item.Vector) == targetDim && targetDim > 0 {
			newVec = item.Vector
		} else {
			// Dimension mismatch -> zero-vector (entity node)
			newVec = nil
		}

		// Check if ID already exists in target
		_, err := s.engine.VGet(args.TargetIndex, item.ID)
		if err == nil {
			// ID exists - merge metadata using VSetMetadata
			mergeMeta := make(map[string]any)
			for k, v := range newMeta {
				mergeMeta[k] = v
			}
			if err := s.engine.VSetMetadata(args.TargetIndex, item.ID, mergeMeta); err != nil {
				skippedCount++
				continue
			}
			transferredIDs = append(transferredIDs, item.ID)
			idMapping[item.ID] = item.ID
		} else {
			// New node - add to batch
			toTransfer = append(toTransfer, types.BatchObject{
				Id:       item.ID,
				Vector:   newVec,
				Metadata: newMeta,
			})
			transferredIDs = append(transferredIDs, item.ID)
			idMapping[item.ID] = item.ID
		}
	}

	// 8. Batch insert new nodes
	if len(toTransfer) > 0 {
		// Check if target index is empty and we need to bootstrap dimension
		targetDim := s.getIndexDimension(args.TargetIndex)
		if targetDim == 0 {
			// Target is empty - ensure first item has a valid vector
			// Find first item with non-nil vector
			bootstrapIdx := -1
			for i, item := range toTransfer {
				if len(item.Vector) > 0 {
					bootstrapIdx = i
					break
				}
			}

			if bootstrapIdx > 0 {
				// Move bootstrap item to front
				toTransfer[0], toTransfer[bootstrapIdx] = toTransfer[bootstrapIdx], toTransfer[0]
			} else if bootstrapIdx == -1 {
				// All items are zero-vectors - need to create a bootstrap vector
				// Use embedder to generate a vector from the query
				bootstrapVec, err := s.embedder.Embed(args.Query)
				if err != nil {
					return nil, TransferMemoryResult{}, fmt.Errorf("cannot bootstrap target index: %w", err)
				}
				// Assign to first item
				toTransfer[0].Vector = bootstrapVec
			}
		}

		if err := s.engine.VAddBatch(args.TargetIndex, toTransfer); err != nil {
			return nil, TransferMemoryResult{}, fmt.Errorf("batch transfer failed: %w", err)
		}
	}

	// 9. Deep copy graph topology if requested
	if args.WithGraph && len(transferredIDs) > 0 {
		s.transferGraphTopology(args.SourceIndex, args.TargetIndex, sourceIDs, idMapping)
	}

	// 10. Create agent proxy node
	agentNodeID := fmt.Sprintf("agent::%s", args.SourceIndex)
	s.createAgentProxyNode(args.TargetIndex, agentNodeID, args.SourceIndex, transferredIDs)

	return nil, TransferMemoryResult{
		TransferredCount: len(transferredIDs),
		SkippedCount:     skippedCount,
		TransferredIDs:   transferredIDs,
		Message:          fmt.Sprintf("Transferred %d memories from %s to %s", len(transferredIDs), args.SourceIndex, args.TargetIndex),
	}, nil
}

// getIndexDimension returns the vector dimension of an index
func (s *Service) getIndexDimension(indexName string) int {
	idx, ok := s.engine.DB.GetVectorIndex(indexName)
	if !ok {
		return 0
	}
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		return hnswIdx.GetDimension()
	}
	return 0
}

// transferGraphTopology copies graph edges between transferred nodes
func (s *Service) transferGraphTopology(sourceIdx, targetIdx string, sourceIDs []string, idMapping map[string]string) {
	// Create set for O(1) lookup
	sourceSet := make(map[string]bool)
	for _, id := range sourceIDs {
		sourceSet[id] = true
	}

	// For each node, get its relations in source
	for _, sourceID := range sourceIDs {
		// Get all relations from source node
		outgoing := s.engine.VGetRelations(sourceIdx, sourceID)
		for relType, targets := range outgoing {
			for _, targetID := range targets {
				// Only copy if target is also in transferred set
				if sourceSet[targetID] {
					newSourceID := idMapping[sourceID]
					newTargetID := idMapping[targetID]
					if newSourceID != "" && newTargetID != "" {
						s.engine.VLink(targetIdx, newSourceID, newTargetID, relType, "", 1.0, nil)
					}
				}
			}
		}
	}
}

// createAgentProxyNode creates a proxy node representing the source agent
func (s *Service) createAgentProxyNode(targetIdx, agentID, sourceIdx string, transferredIDs []string) {
	// Check if proxy already exists
	_, err := s.engine.VGet(targetIdx, agentID)
	if err != nil {
		// Create new proxy node (zero-vector entity)
		meta := map[string]any{
			"type":        "agent_proxy",
			"agent_name":  sourceIdx,
			"description": fmt.Sprintf("Proxy node representing agent %s", sourceIdx),
		}
		s.engine.VAdd(targetIdx, agentID, nil, meta)
	}

	// Link all transferred nodes to this agent
	for _, id := range transferredIDs {
		s.engine.VLink(targetIdx, id, agentID, "transferred_from_agent", "", 1.0, nil)
	}
}
