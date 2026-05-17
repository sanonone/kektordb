package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TUIClient wraps an HTTP client for calling the KektorDB API on localhost.
type TUIClient struct {
	baseURL string
	http    *http.Client
}

// NewTUIClient creates a new client for the TUI.
func NewTUIClient(addr string) *TUIClient {
	return &TUIClient{
		baseURL: fmt.Sprintf("http://%s", addr),
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *TUIClient) get(path string, out any) error {
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *TUIClient) post(path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.http.Post(c.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %s", resp.Status, path, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ─── Data types ─────────────────────────────────────────────────────────────

// SystemStats is returned by GET /system/stats.
type SystemStats struct {
	UptimeSeconds int          `json:"uptime_seconds"`
	TotalVectors  int          `json:"total_vectors"`
	TotalIndexes  int          `json:"total_indexes"`
	Graph         GraphStats   `json:"graph"`
	Gardener      GardenerInfo `json:"gardener"`
	Memory        MemoryStats  `json:"memory"`
	Embedder      EmbedderInfo `json:"embedder"`
}

type GraphStats struct {
	TotalEdges     int `json:"total_edges"`
	NodesWithLinks int `json:"nodes_with_links"`
	PinnedNodes    int `json:"pinned_nodes"`
}

type GardenerInfo struct {
	Enabled               bool   `json:"enabled"`
	Mode                  string `json:"mode"`
	LastThinkAgoMs        int64  `json:"last_think_ago_ms"`
	TotalReflections      int    `json:"total_reflections"`
	ContradictionsPending int    `json:"contradictions_pending"`
	DecayedTotal          int    `json:"decayed_total"`
}

type MemoryStats struct {
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	AOFSizeMB   float64 `json:"aof_size_mb"`
}

type EmbedderInfo struct {
	Active    string `json:"active"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension"`
}

// GardenerStatus is returned by GET /system/gardener.
type GardenerStatus struct {
	Enabled               bool     `json:"enabled"`
	Mode                  string   `json:"mode"`
	Interval              string   `json:"interval"`
	LastThinkTime         string   `json:"last_think_time"`
	TotalReflections      int      `json:"total_reflections"`
	ContradictionsPending int      `json:"contradictions_pending"`
	MergedToday           int      `json:"merged_today"`
	DecayedTotal          int      `json:"decayed_total"`
	TargetIndexes         []string `json:"target_indexes"`
}

// IndexInfo is returned by GET /vector/indexes.
type IndexInfo struct {
	Name           string `json:"name"`
	Metric         string `json:"metric"`
	Precision      string `json:"precision"`
	M              int    `json:"m"`
	EfConstruction int    `json:"ef_construction"`
	VectorCount    int    `json:"vector_count"`
	TextLanguage   string `json:"text_language"`
}

// GraphNode is a node in the knowledge graph.
type GraphNode struct {
	ID         string         `json:"id"`
	Properties map[string]any `json:"properties"`
}

// GraphRelations is returned by POST /graph/actions/get-all-relations.
type GraphRelations struct {
	NodeID    string              `json:"node_id"`
	Relations map[string][]string `json:"relations"`
}

// GraphNodeRequest is the body for graph node search.
type GraphNodeRequest struct {
	IndexName      string `json:"index_name"`
	SearchTerm     string `json:"search_term,omitempty"`
	PropertyFilter string `json:"property_filter,omitempty"`
}

// NodeSearchResponse is returned by POST /graph/actions/search-nodes.
type NodeSearchResponse struct {
	Nodes []struct {
		ID         string         `json:"id"`
		Properties map[string]any `json:"properties"`
	} `json:"nodes"`
}

// SearchRequest is the body for /vector/actions/search.
type SearchRequest struct {
	IndexName        string    `json:"index_name"`
	K                int       `json:"k"`
	QueryVector      []float32 `json:"query_vector,omitempty"`
	QueryText        string    `json:"query_text,omitempty"`
	Filter           string    `json:"filter,omitempty"`
	Alpha            float64   `json:"alpha,omitempty"`
	IncludeRelations []string  `json:"include_relations,omitempty"`
	Hydrate          bool      `json:"hydrate,omitempty"`
	GraphFilter      any       `json:"graph_filter,omitempty"`
}

// SearchResultWithNode is a hydrated search result with full node metadata.
type SearchResultWithNode struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Node  struct {
		Vector   []float32      `json:"vector"`
		Metadata map[string]any `json:"metadata"`
	} `json:"node"`
}

// SearchResult is a single search result from the engine.
type SearchResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// ScoredResult is from search-with-scores.
type ScoredResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// SSEEvent is a live event from the SSE stream.
type SSEEvent struct {
	Type      string `json:"type"`
	IndexName string `json:"index_name"`
	ID        string `json:"id"`
	TargetID  string `json:"target_id,omitempty"`
	RelType   string `json:"rel_type,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// EmbedderReloadRequest is the body for POST /system/embedder/reload.
type EmbedderReloadRequest struct {
	Mode string `json:"mode"`
}

// EmbedderReloadResponse is the response from POST /system/embedder/reload.
type EmbedderReloadResponse struct {
	Status    string `json:"status"`
	Active    string `json:"active"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension"`
}

// ─── API calls ──────────────────────────────────────────────────────────────

// FetchStats fetches aggregate system stats.
func (c *TUIClient) FetchStats() (*SystemStats, error) {
	var stats SystemStats
	if err := c.get("/system/stats", &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// FetchGardener fetches Gardener status.
func (c *TUIClient) FetchGardener() (*GardenerStatus, error) {
	var gs GardenerStatus
	if err := c.get("/system/gardener", &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// FetchIndexes fetches the list of all vector indexes.
func (c *TUIClient) FetchIndexes() ([]IndexInfo, error) {
	var indexes []IndexInfo
	if err := c.get("/vector/indexes", &indexes); err != nil {
		return nil, err
	}
	return indexes, nil
}

// Search runs a full-featured search (returns IDs + scores only).
func (c *TUIClient) Search(req SearchRequest) ([]SearchResult, error) {
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.post("/vector/actions/search", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// SearchHydrated runs a search and returns full node metadata for each result.
func (c *TUIClient) SearchHydrated(req SearchRequest) ([]SearchResultWithNode, error) {
	var resp struct {
		Results []SearchResultWithNode `json:"results"`
	}
	if err := c.post("/vector/actions/search", req, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// SearchWithScores runs a vector search with transparent scores.
func (c *TUIClient) SearchWithScores(index string, vec []float32, k int) ([]ScoredResult, error) {
	var resp struct {
		Results []ScoredResult `json:"results"`
	}
	body := map[string]any{
		"index_name":   index,
		"query_vector": vec,
		"k":            k,
	}
	if err := c.post("/vector/actions/search-with-scores", body, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// GetRelations fetches all outgoing relations for a node.
func (c *TUIClient) GetRelations(indexName, nodeID string) (*GraphRelations, error) {
	var resp GraphRelations
	body := map[string]string{
		"index_name": indexName,
		"node_id":    nodeID,
	}
	if err := c.post("/graph/actions/get-all-relations", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SearchNodes searches graph nodes by name/properties.
func (c *TUIClient) SearchNodes(indexName, propertyFilter string, limit int) ([]GraphNode, error) {
	var resp NodeSearchResponse
	body := map[string]any{
		"index_name":      indexName,
		"property_filter": propertyFilter,
		"limit":           limit,
	}
	if err := c.post("/graph/actions/search-nodes", body, &resp); err != nil {
		return nil, err
	}
	nodes := make([]GraphNode, len(resp.Nodes))
	for i, n := range resp.Nodes {
		nodes[i] = GraphNode{ID: n.ID, Properties: n.Properties}
	}
	return nodes, nil
}

// GetNodeProperties fetches metadata for a node.
func (c *TUIClient) GetNodeProperties(nodeID string) (map[string]any, error) {
	var resp struct {
		NodeID     string         `json:"node_id"`
		Properties map[string]any `json:"properties"`
	}
	body := map[string]string{"node_id": nodeID}
	if err := c.post("/graph/actions/get-node-properties", body, &resp); err != nil {
		return nil, err
	}
	return resp.Properties, nil
}

// ReloadEmbedder calls /system/embedder/reload to switch embedder mode.
func (c *TUIClient) ReloadEmbedder(mode string) (*EmbedderReloadResponse, error) {
	var resp EmbedderReloadResponse
	body := EmbedderReloadRequest{Mode: mode}
	if err := c.post("/system/embedder/reload", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// HealthCheck checks if the server is reachable.
func (c *TUIClient) HealthCheck() bool {
	resp, err := c.http.Get(c.baseURL + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
