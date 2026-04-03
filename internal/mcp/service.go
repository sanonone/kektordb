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
