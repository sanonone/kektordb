// Package client provides a Go client for interacting with the KektorDB API.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// --- Custom Errors ---

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// --- JSON Response Structs ---

type kvResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type searchResponse struct {
	Results []string `json:"results"`
}

type VectorData struct {
	ID       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata"`
}

type IndexInfo struct {
	Name           string `json:"name"`
	Metric         string `json:"metric"`
	Precision      string `json:"precision"`
	M              int    `json:"m"`
	EfConstruction int    `json:"ef_construction"`
	VectorCount    int    `json:"vector_count"`
}

// MaintenanceConfig defines settings for background optimization tasks.
type MaintenanceConfig struct {
	VacuumInterval       string  `json:"vacuum_interval,omitempty"`        // e.g. "60s"
	DeleteThreshold      float64 `json:"delete_threshold,omitempty"`       // 0.0-1.0
	RefineEnabled        bool    `json:"refine_enabled"`                   // true/false
	RefineInterval       string  `json:"refine_interval,omitempty"`        // e.g. "30s"
	RefineBatchSize      int     `json:"refine_batch_size,omitempty"`      // e.g. 100
	RefineEfConstruction int     `json:"refine_ef_construction,omitempty"` // e.g. 200
}

// MemoryConfig defines the time-dependent ranking settings for an index.
type MemoryConfig struct {
	Enabled       bool   `json:"enabled"`
	DecayHalfLife string `json:"decay_half_life,omitempty"` // e.g. "1h", "168h"
}

type VectorAddObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type AutoLinkRule struct {
	MetadataField string `json:"metadata_field"` // The metadata field to inspect
	RelationType  string `json:"relation_type"`  // The relation name (e.g. "belongs_to")
	CreateNode    bool   `json:"create_node,omitempty"`
}

type Task struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ProgressMessage string `json:"progress_message,omitempty"`
	Error           string `json:"error,omitempty"`

	client *Client
}

// --- Graph Structures ---

type graphLinkRequest struct {
	IndexName           string                 `json:"index_name"`
	SourceID            string                 `json:"source_id"`
	TargetID            string                 `json:"target_id"`
	RelationType        string                 `json:"relation_type"`
	InverseRelationType string                 `json:"inverse_relation_type,omitempty"`
	Weight              float32                `json:"weight,omitempty"`
	Props               map[string]interface{} `json:"props,omitempty"`
}

type graphGetLinksRequest struct {
	IndexName    string `json:"index_name"`
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
}

type graphGetLinksResponse struct {
	Targets []string `json:"targets"`
}

type graphGetIncomingRequest struct {
	IndexName    string `json:"index_name"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

type GraphNode struct {
	ID          string                 `json:"id"`
	Vector      []float32              `json:"vector"`
	Metadata    map[string]interface{} `json:"metadata"`
	Connections map[string][]GraphNode `json:"connections,omitempty"`
}

// GraphSearchResult represents a search result enriched with scores and relationships.
type GraphSearchResult struct {
	ID    string    `json:"id"`
	Score float64   `json:"score"`
	Node  GraphNode `json:"node"`
}

type searchGraphResponse struct {
	Results []GraphSearchResult `json:"results"`
}

type graphGetConnectionsRequest struct {
	IndexName    string `json:"index_name"`
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
}

type getConnectionsResponse struct {
	Results []VectorData `json:"results"`
}

type graphTraverseRequest struct {
	IndexName string   `json:"index_name"`
	SourceID  string   `json:"source_id"`
	Paths     []string `json:"paths"`
}

type traverseResponse struct {
	Result GraphNode `json:"result"`
}

// GraphQuery defines the criteria for graph-based filtering.
type GraphQuery struct {
	RootID    string   `json:"root_id"`
	Relations []string `json:"relations"`
	Direction string   `json:"direction,omitempty"` // "out", "in", "both"
	MaxDepth  int      `json:"max_depth"`
}

// Subgraph structures for VExtractSubgraph
type SubgraphNode struct {
	ID       string                 `json:"id"`
	Metadata map[string]interface{} `json:"metadata"`
}

type SubgraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Dir      string `json:"dir"` // "out" or "in"
}

type SubgraphResult struct {
	RootID string         `json:"root_id"`
	Nodes  []SubgraphNode `json:"nodes"`
	Edges  []SubgraphEdge `json:"edges"`
}

type extractSubgraphRequest struct {
	IndexName         string    `json:"index_name"`
	RootID            string    `json:"root_id"`
	Relations         []string  `json:"relations"`
	MaxDepth          int       `json:"max_depth"`
	AtTime            int64     `json:"at_time,omitempty"`
	GuideVector       []float32 `json:"guide_vector,omitempty"`
	SemanticThreshold float64   `json:"semantic_threshold,omitempty"`
}

// Graph Entity types
type graphSetPropertiesRequest struct {
	IndexName  string                 `json:"index_name"`
	NodeID     string                 `json:"node_id"`
	Properties map[string]interface{} `json:"properties"`
}

type graphGetPropertiesRequest struct {
	IndexName string `json:"index_name"`
	NodeID    string `json:"node_id"`
}

type graphSearchNodesRequest struct {
	IndexName      string `json:"index_name"`
	PropertyFilter string `json:"property_filter"`
	Limit          int    `json:"limit"`
}

type graphSearchNodesResponse struct {
	Nodes []struct {
		ID         string                 `json:"id"`
		Properties map[string]interface{} `json:"properties"`
	} `json:"nodes"`
}

type graphGetAllRelationsRequest struct {
	IndexName string `json:"index_name"`
	NodeID    string `json:"node_id"`
}

type graphGetAllRelationsResponse struct {
	NodeID    string              `json:"node_id"`
	Relations map[string][]string `json:"relations"`
}

// --- Client ---

type Client struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

func New(host string, port int, apiKey string) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("http://%s:%d", host, port),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiKey:     apiKey,
	}
}

func (c *Client) jsonRequest(method, endpoint string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal JSON payload: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		if json.Unmarshal(respBody, &errResp) == nil {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: errResp["error"]}
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	return respBody, nil
}

func (t *Task) Refresh() error {
	if t.client == nil {
		return fmt.Errorf("client is not associated with the task")
	}
	updatedTask, err := t.client.GetTaskStatus(t.ID)
	if err != nil {
		return err
	}
	t.Status = updatedTask.Status
	t.ProgressMessage = updatedTask.ProgressMessage
	t.Error = updatedTask.Error
	return nil
}

func (t *Task) Wait(interval, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("timeout exceeded while waiting for task %s", t.ID)
		case <-ticker.C:
			if err := t.Refresh(); err != nil {
				return err
			}
			switch t.Status {
			case "completed":
				return nil
			case "failed":
				return fmt.Errorf("task %s failed with error: %s", t.ID, t.Error)
			case "running", "started":
				continue
			default:
				return fmt.Errorf("unknown task status: %s", t.Status)
			}
		}
	}
}

// --- KV Methods ---

func (c *Client) Set(key string, value []byte) error {
	payload := map[string]string{"value": string(value)}
	_, err := c.jsonRequest(http.MethodPost, "/kv/"+key, payload)
	return err
}

func (c *Client) Get(key string) ([]byte, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/kv/"+key, nil)
	if err != nil {
		return nil, err
	}
	var resp kvResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for GET: %w", err)
	}
	return []byte(resp.Value), nil
}

func (c *Client) Delete(key string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/kv/"+key, nil)
	return err
}

// --- Vector Index Management ---

// VCreate creates a new vector index.
// maintenance can be nil to use defaults.
func (c *Client) VCreate(indexName, metric, precision string, m, efConstruction int, maintenance *MaintenanceConfig) error {
	return c.VCreateFull(indexName, metric, precision, m, efConstruction, maintenance, nil, nil)
}

// VCreateFull creates a new vector index with full configuration options.
// maintenance, autoLinks, and memoryConfig can be nil to use defaults.
func (c *Client) VCreateFull(indexName, metric, precision string, m, efConstruction int, maintenance *MaintenanceConfig, autoLinks []AutoLinkRule, memoryConfig *MemoryConfig) error {
	payload := map[string]interface{}{"index_name": indexName}
	if metric != "" {
		payload["metric"] = metric
	}
	if precision != "" {
		payload["precision"] = precision
	}
	if m > 0 {
		payload["m"] = m
	}
	if efConstruction > 0 {
		payload["ef_construction"] = efConstruction
	}
	if maintenance != nil {
		payload["maintenance"] = maintenance
	}
	if len(autoLinks) > 0 {
		payload["auto_links"] = autoLinks
	}
	if memoryConfig != nil {
		payload["memory_config"] = memoryConfig
	}

	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/create", payload)
	return err
}

// VCreateWithAutoLinks creates a new vector index with auto-linking rules.
func (c *Client) VCreateWithAutoLinks(indexName, metric, precision string, m, efConstruction int, maintenance *MaintenanceConfig, autoLinks []AutoLinkRule) error {
	payload := map[string]interface{}{"index_name": indexName}
	if metric != "" {
		payload["metric"] = metric
	}
	if precision != "" {
		payload["precision"] = precision
	}
	if m > 0 {
		payload["m"] = m
	}
	if efConstruction > 0 {
		payload["ef_construction"] = efConstruction
	}
	if maintenance != nil {
		payload["maintenance"] = maintenance
	}
	if len(autoLinks) > 0 {
		payload["auto_links"] = autoLinks
	}

	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/create", payload)
	return err
}

func (c *Client) ListIndexes() ([]IndexInfo, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/vector/indexes", nil)
	if err != nil {
		return nil, err
	}
	var resp []IndexInfo
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for ListIndexes: %w", err)
	}
	return resp, nil
}

func (c *Client) GetIndexInfo(indexName string) (*IndexInfo, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/vector/indexes/"+indexName, nil)
	if err != nil {
		return nil, err
	}
	var resp IndexInfo
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for GetIndexInfo: %w", err)
	}
	return &resp, nil
}

func (c *Client) DeleteIndex(indexName string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/vector/indexes/"+indexName, nil)
	return err
}

func (c *Client) VCompress(indexName, precision string) (*Task, error) {
	payload := map[string]interface{}{
		"index_name": indexName,
		"precision":  precision,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/compress", payload)
	if err != nil {
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(respBody, &task); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VCompress: %w", err)
	}
	task.client = c
	return &task, nil
}

// VUpdateConfig updates the background maintenance settings for an index.
func (c *Client) VUpdateConfig(indexName string, config MaintenanceConfig) error {
	_, err := c.jsonRequest(http.MethodPost, fmt.Sprintf("/vector/indexes/%s/config", indexName), config)
	return err
}

// VTriggerMaintenance manually triggers a "vacuum" or "refine" task.
func (c *Client) VTriggerMaintenance(indexName string, taskType string) error {
	payload := map[string]string{"type": taskType}
	_, err := c.jsonRequest(http.MethodPost, fmt.Sprintf("/vector/indexes/%s/maintenance", indexName), payload)
	return err
}

// --- Vector Data ---

// VAdd inserts a vector. If vector is nil, creates a "Graph Entity" (Zero Vector).
func (c *Client) VAdd(indexName, id string, vector []float32, metadata map[string]interface{}) error {
	payload := map[string]interface{}{
		"index_name": indexName,
		"id":         id,
	}
	if vector != nil {
		payload["vector"] = vector
	} else {
		payload["vector"] = []float32{} // Empty list for Entity
	}
	if metadata != nil {
		payload["metadata"] = metadata
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/add", payload)
	return err
}

func (c *Client) VAddBatch(indexName string, vectors []VectorAddObject) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"index_name": indexName,
		"vectors":    vectors,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/add-batch", payload)
	if err != nil {
		return nil, err
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VAddBatch: %w", err)
	}
	return resp, nil
}

// VImport performs a bulk import (bypassing AOF).
func (c *Client) VImport(indexName string, vectors []VectorAddObject) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"index_name": indexName,
		"vectors":    vectors,
	}
	// Increase timeout for import as it can be slow
	originalTimeout := c.httpClient.Timeout
	c.httpClient.Timeout = 5 * time.Minute
	defer func() { c.httpClient.Timeout = originalTimeout }()

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/import", payload)
	if err != nil {
		return nil, err
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VImport: %w", err)
	}
	return resp, nil
}

// SearchResultWithScore represents a search result with its relevance score.
type SearchResultWithScore struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type searchWithScoresResponse struct {
	Results []SearchResultWithScore `json:"results"`
}

func (c *Client) VSearch(indexName string, k int, queryVector []float32, filter string, efSearch int, alpha float64, graphFilter *GraphQuery) ([]string, error) {
	payload := map[string]interface{}{
		"index_name":   indexName,
		"k":            k,
		"query_vector": queryVector,
	}
	if filter != "" {
		payload["filter"] = filter
	}
	if efSearch > 0 {
		payload["ef_search"] = efSearch
	}
	if alpha != 0 {
		payload["alpha"] = alpha
	}
	if graphFilter != nil {
		payload["graph_filter"] = graphFilter
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/search", payload)
	if err != nil {
		return nil, err
	}
	var resp searchResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VSearch: %w", err)
	}
	return resp.Results, nil
}

// VSearchWithScores performs a vector search and returns results with their relevance scores.
// This is useful for debugging and when you need to see the actual score values.
// If the index has MemoryConfig enabled, scores will include time decay.
func (c *Client) VSearchWithScores(indexName string, k int, queryVector []float32) ([]SearchResultWithScore, error) {
	payload := map[string]interface{}{
		"index_name":   indexName,
		"k":            k,
		"query_vector": queryVector,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/search-with-scores", payload)
	if err != nil {
		return nil, err
	}
	var resp searchWithScoresResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VSearchWithScores: %w", err)
	}
	return resp.Results, nil
}

func (c *Client) VDelete(indexName, id string) error {
	payload := map[string]string{
		"index_name": indexName,
		"id":         id,
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/delete_vector", payload)
	return err
}

func (c *Client) VGet(indexName, id string) (*VectorData, error) {
	respBody, err := c.jsonRequest(http.MethodGet, fmt.Sprintf("/vector/indexes/%s/vectors/%s", indexName, id), nil)
	if err != nil {
		return nil, err
	}
	var resp VectorData
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VGet: %w", err)
	}
	return &resp, nil
}

func (c *Client) VGetMany(indexName string, ids []string) ([]VectorData, error) {
	payload := map[string]interface{}{
		"index_name": indexName,
		"ids":        ids,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/get-vectors", payload)
	if err != nil {
		return nil, err
	}
	var resp []VectorData
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VGetMany: %w", err)
	}
	return resp, nil
}

// --- Graph Methods ---

// VLink creates a semantic relationship between two vectors (e.g. "chunk_1" -> "doc_A" as "parent").
func (c *Client) VLink(indexName, sourceID, targetID, relationType, inverseRelationType string) error {
	req := graphLinkRequest{
		IndexName:           indexName,
		SourceID:            sourceID,
		TargetID:            targetID,
		RelationType:        relationType,
		InverseRelationType: inverseRelationType,
	}
	_, err := c.jsonRequest(http.MethodPost, "/graph/actions/link", req)
	return err
}

// VUnlink removes a semantic relationship.
func (c *Client) VUnlink(indexName, sourceID, targetID, relationType, inverseRelationType string) error {
	req := graphLinkRequest{
		IndexName:           indexName,
		SourceID:            sourceID,
		TargetID:            targetID,
		RelationType:        relationType,
		InverseRelationType: inverseRelationType,
	}
	_, err := c.jsonRequest(http.MethodPost, "/graph/actions/unlink", req)
	return err
}

// VGetLinks retrieves all target IDs linked to a source ID by a specific relation type.
func (c *Client) VGetLinks(indexName, sourceID, relationType string) ([]string, error) {
	req := graphGetLinksRequest{
		IndexName:    indexName,
		SourceID:     sourceID,
		RelationType: relationType,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-links", req)
	if err != nil {
		return nil, err
	}

	var resp graphGetLinksResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return resp.Targets, nil
}

// VGetIncoming retrieves all source IDs that link TO the target ID with a specific relation.
func (c *Client) VGetIncoming(indexName, targetID, relationType string) ([]string, error) {
	req := graphGetIncomingRequest{
		IndexName:    indexName,
		TargetID:     targetID,
		RelationType: relationType,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-incoming", req)
	if err != nil {
		return nil, err
	}

	// Response format is {"sources": ["id1", "id2"]}
	var resp struct {
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return resp.Sources, nil
}

// VGetAllRelations returns all outgoing links from a node, grouped by relation type.
func (c *Client) VGetAllRelations(indexName, nodeID string) (map[string][]string, error) {
	req := graphGetAllRelationsRequest{
		IndexName: indexName,
		NodeID:    nodeID,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-all-relations", req)
	if err != nil {
		return nil, err
	}

	var resp graphGetAllRelationsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	return resp.Relations, nil
}

// VGetAllIncoming returns all incoming links to a node, grouped by relation type.
func (c *Client) VGetAllIncoming(indexName, nodeID string) (map[string][]string, error) {
	req := graphGetAllRelationsRequest{
		IndexName: indexName,
		NodeID:    nodeID,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-all-incoming", req)
	if err != nil {
		return nil, err
	}

	var resp graphGetAllRelationsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	return resp.Relations, nil
}

// VGetConnections retrieves fully hydrated nodes linked to the source.
func (c *Client) VGetConnections(indexName, sourceID, relationType string) ([]VectorData, error) {
	req := graphGetConnectionsRequest{
		IndexName:    indexName,
		SourceID:     sourceID,
		RelationType: relationType,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-connections", req)
	if err != nil {
		return nil, err
	}

	var resp getConnectionsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return resp.Results, nil
}

// VSearchGraph performs a search and returns detailed results including scores and graph relations.
// 'includeRelations' is a list of relation types to fetch (e.g. []string{"parent", "next"}).
func (c *Client) VSearchGraph(indexName string, queryVector []float32, k int, filter string, efSearch int, alpha float64, includeRelations []string, hydrate bool, graphFilter *GraphQuery) ([]GraphSearchResult, error) {
	payload := map[string]interface{}{
		"index_name":   indexName,
		"k":            k,
		"query_vector": queryVector,
	}

	if filter != "" {
		payload["filter"] = filter
	}
	if efSearch > 0 {
		payload["ef_search"] = efSearch
	}
	if alpha != 0 {
		payload["alpha"] = alpha
	}
	if len(includeRelations) > 0 {
		payload["include_relations"] = includeRelations
	}

	if hydrate {
		payload["hydrate_relations"] = true
	}

	if graphFilter != nil {
		payload["graph_filter"] = graphFilter
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/search", payload)
	if err != nil {
		return nil, err
	}

	var resp searchGraphResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for VSearchGraph: %w", err)
	}

	return resp.Results, nil
}

// VTraverse explores the graph starting from sourceID following the specified paths.
func (c *Client) VTraverse(indexName, sourceID string, paths []string) (*GraphNode, error) {
	req := graphTraverseRequest{
		IndexName: indexName,
		SourceID:  sourceID,
		Paths:     paths,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/traverse", req)
	if err != nil {
		return nil, err
	}

	var resp traverseResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return &resp.Result, nil
}

// VExtractSubgraph retrieves a local subgraph (nodes and edges) around a root ID.
func (c *Client) VExtractSubgraph(indexName, rootID string, relations []string, maxDepth int, atTime int64, guideVector []float32, semanticThreshold float64) (*SubgraphResult, error) {
	req := extractSubgraphRequest{
		IndexName:         indexName,
		RootID:            rootID,
		Relations:         relations,
		MaxDepth:          maxDepth,
		AtTime:            atTime,
		GuideVector:       guideVector,
		SemanticThreshold: semanticThreshold,
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/extract-subgraph", req)
	if err != nil {
		return nil, err
	}

	var resp SubgraphResult
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return &resp, nil
}

// Graph Entities Methods

// SetNodeProperties updates metadata for a node (Upsert behavior).
func (c *Client) SetNodeProperties(indexName, nodeID string, properties map[string]interface{}) error {
	req := graphSetPropertiesRequest{
		IndexName:  indexName,
		NodeID:     nodeID,
		Properties: properties,
	}
	_, err := c.jsonRequest(http.MethodPost, "/graph/actions/set-node-properties", req)
	return err
}

// GetNodeProperties retrieves metadata for a node.
func (c *Client) GetNodeProperties(indexName, nodeID string) (map[string]interface{}, error) {
	req := graphGetPropertiesRequest{
		IndexName: indexName,
		NodeID:    nodeID,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-node-properties", req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Properties map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Properties, nil
}

// SearchNodes finds nodes based only on their properties (metadata filter).
func (c *Client) SearchNodes(indexName, propertyFilter string, limit int) ([]GraphNode, error) {
	req := graphSearchNodesRequest{
		IndexName:      indexName,
		PropertyFilter: propertyFilter,
		Limit:          limit,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/search-nodes", req)
	if err != nil {
		return nil, err
	}

	var resp graphSearchNodesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Convert to GraphNode for consistency
	nodes := make([]GraphNode, len(resp.Nodes))
	for i, n := range resp.Nodes {
		nodes[i] = GraphNode{
			ID:       n.ID,
			Metadata: n.Properties,
		}
	}
	return nodes, nil
}

// --- Administration Methods ---

func (c *Client) AOFRewrite() (*Task, error) {
	respBody, err := c.jsonRequest(http.MethodPost, "/system/aof-rewrite", nil)
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(respBody, &task); err != nil {
		return nil, fmt.Errorf("invalid JSON response for AOFRewrite: %w", err)
	}
	task.client = c
	return &task, nil
}

func (c *Client) GetTaskStatus(taskID string) (*Task, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/system/tasks/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(respBody, &task); err != nil {
		return nil, fmt.Errorf("invalid JSON response for GetTaskStatus: %w", err)
	}
	task.client = c
	return &task, nil
}

// --- UI & Helper Methods ---

type uiSearchRequest struct {
	IndexName        string   `json:"index_name"`
	Query            string   `json:"query"`
	K                int      `json:"k"`
	IncludeRelations []string `json:"include_relations,omitempty"`
	Hydrate          bool     `json:"hydrate"`
}

type uiSearchResponse struct {
	Results []GraphSearchResult `json:"results"`
}

// SearchText performs a search using the server-side embedder (same logic as the UI).
// This is useful for clients that don't want to run a local embedder model.
func (c *Client) SearchText(indexName, query string, k int, relations []string) ([]GraphSearchResult, error) {
	req := uiSearchRequest{
		IndexName:        indexName,
		Query:            query,
		K:                k,
		IncludeRelations: relations,
		Hydrate:          true,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/ui/search", req)
	if err != nil {
		return nil, err
	}
	var resp uiSearchResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Results, nil
}

// GetMetrics returns the raw Prometheus metrics string.
func (c *Client) GetMetrics() (string, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/metrics")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- Session Types ---

type Session struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type StartSessionRequest struct {
	UserID       string                 `json:"user_id,omitempty"`
	IndexName    string                 `json:"index_name,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Conversation []ConversationMessage  `json:"conversation,omitempty"`
}

type StartSessionResponse struct {
	SessionID    string                `json:"session_id"`
	Conversation []ConversationMessage `json:"conversation,omitempty"`
}

type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type EndSessionRequest struct {
	SessionID string `json:"session_id"`
	IndexName string `json:"index_name"`
}

type EndSessionResponse struct {
	Success bool `json:"success"`
}

// --- User Profile Types ---

type UserProfile struct {
	UserID             string   `json:"user_id"`
	CommunicationStyle string   `json:"communication_style,omitempty"`
	Language           string   `json:"language,omitempty"`
	ExpertiseAreas     []string `json:"expertise_areas,omitempty"`
	Dislikes           []string `json:"dislikes,omitempty"`
	ResponseLength     string   `json:"response_length,omitempty"`
	Confidence         float64  `json:"confidence"`
	LastUpdated        int64    `json:"last_updated"`
	ProfileData        string   `json:"profile_data,omitempty"`
}

type UserProfileItem struct {
	UserID             string  `json:"user_id"`
	CommunicationStyle string  `json:"communication_style,omitempty"`
	Confidence         float64 `json:"confidence"`
	LastUpdated        int64   `json:"last_updated"`
}

// --- Source Attribution Types ---

type GraphPathNode struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

type GraphPathEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
}

type GraphPath struct {
	Nodes     []GraphPathNode `json:"nodes"`
	Edges     []GraphPathEdge `json:"edges"`
	Formatted string          `json:"formatted"`
}

type SourceAttribution struct {
	ChunkID    string    `json:"chunk_id"`
	DocumentID string    `json:"document_id"`
	SourceFile string    `json:"source_file"`
	Filename   string    `json:"filename"`
	ChunkIndex int       `json:"chunk_index"`
	PageNumber int       `json:"page_number,omitempty"`
	Content    string    `json:"content"`
	Relevance  float64   `json:"relevance"`
	GraphDepth int       `json:"graph_depth"`
	GraphPath  GraphPath `json:"graph_path,omitempty"`
	Verified   bool      `json:"verified"`
}

// --- Adaptive Retrieval Types ---

type AdaptiveRetrieveRequest struct {
	PipelineName      string  `json:"pipeline_name"`
	Query             string  `json:"query"`
	K                 int     `json:"k,omitempty"`
	MaxTokens         int     `json:"max_tokens,omitempty"`
	Strategy          string  `json:"strategy,omitempty"` // "greedy", "density", "graph"
	ExpansionDepth    int     `json:"expansion_depth,omitempty"`
	SemanticWeight    float64 `json:"semantic_weight,omitempty"`
	GraphWeight       float64 `json:"graph_weight,omitempty"`
	DensityWeight     float64 `json:"density_weight,omitempty"`
	CharsPerToken     float64 `json:"chars_per_token,omitempty"`
	IncludeProvenance bool    `json:"include_provenance,omitempty"`
}

type AdaptiveRetrieveResponse struct {
	ContextText    string              `json:"context_text"`
	ChunksUsed     int                 `json:"chunks_used"`
	TotalTokens    int                 `json:"total_tokens"`
	DocumentsUsed  int                 `json:"documents_used"`
	Sources        []SourceAttribution `json:"sources,omitempty"`
	Provenance     bool                `json:"provenance"`
	ExpansionStats struct {
		SeedChunks     int `json:"seed_chunks"`
		ExpandedChunks int `json:"expanded_chunks"`
		TotalEvaluated int `json:"total_evaluated"`
	} `json:"expansion_stats"`
}

type RagRetrieveResponse struct {
	Results     []string            `json:"results,omitempty"`
	Response    string              `json:"response"`
	Sources     []SourceAttribution `json:"sources"`
	Confidence  float64             `json:"confidence"`
	TotalTokens int                 `json:"total_tokens"`
	Provenance  bool                `json:"provenance"`
}

// --- New Request/Response Structs ---

type graphFindPathRequest struct {
	IndexName string   `json:"index_name"`
	SourceID  string   `json:"source_id"`
	TargetID  string   `json:"target_id"`
	Relations []string `json:"relations"`
	AtTime    int64    `json:"at_time,omitempty"`
}

type graphGetEdgesRequest struct {
	IndexName    string `json:"index_name"`
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
	AtTime       int64  `json:"at_time,omitempty"`
}

type graphGetEdgesResponse struct {
	Edges []map[string]interface{} `json:"edges"`
}

type autoLinksRequest struct {
	Rules []AutoLinkRule `json:"rules"`
}

type autoLinksResponse struct {
	Rules []AutoLinkRule `json:"rules"`
}

type reflectionsResponse struct {
	Reflections []map[string]interface{} `json:"reflections"`
}

type resolveRequest struct {
	Resolution string `json:"resolution"`
	DiscardID  string `json:"discard_id,omitempty"`
}

type apiKeyRequest struct {
	Role      string `json:"role"`
	Namespace string `json:"namespace,omitempty"`
}

type apiKeyResponse struct {
	Token string `json:"token"`
}

type exportResponse struct {
	Data       []VectorData `json:"data"`
	HasMore    bool         `json:"has_more"`
	NextOffset int          `json:"next_offset"`
}

// --- Graph: Pathfinding & Edges ---

// FindPath finds the shortest path between two nodes in the graph.
func (c *Client) FindPath(indexName, source, target string, relations []string, atTime int64) (map[string]interface{}, error) {
	req := graphFindPathRequest{
		IndexName: indexName,
		SourceID:  source,
		TargetID:  target,
		Relations: relations,
		AtTime:    atTime,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/find-path", req)
	if err != nil {
		return nil, err
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp, nil
}

// GetEdges retrieves edges with properties (supports time travel via atTime).
func (c *Client) GetEdges(indexName, sourceID, relationType string, atTime int64) ([]map[string]interface{}, error) {
	req := graphGetEdgesRequest{
		IndexName:    indexName,
		SourceID:     sourceID,
		RelationType: relationType,
		AtTime:       atTime,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/graph/actions/get-edges", req)
	if err != nil {
		return nil, err
	}
	var resp graphGetEdgesResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Edges, nil
}

// --- Vector: Reinforce & Export ---

// VReinforce updates access timestamps for memories, boosting their search relevance.
func (c *Client) VReinforce(indexName string, ids []string) error {
	payload := map[string]interface{}{
		"index_name": indexName,
		"ids":        ids,
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/reinforce", payload)
	return err
}

// VExport exports vectors with pagination.
func (c *Client) VExport(indexName string, limit, offset int) (*exportResponse, error) {
	url := fmt.Sprintf("/vector/indexes/%s/export?limit=%d&offset=%d", indexName, limit, offset)
	respBody, err := c.jsonRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var resp exportResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &resp, nil
}

// VImportCommit commits a bulk import and triggers Turbo Refine optimization.
func (c *Client) VImportCommit(indexName string) error {
	payload := map[string]interface{}{
		"index_name": indexName,
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/import/commit", payload)
	return err
}

// --- Auto-Links ---

// SetAutoLinks updates the auto-linking rules for an index.
func (c *Client) SetAutoLinks(indexName string, rules []AutoLinkRule) error {
	req := autoLinksRequest{Rules: rules}
	_, err := c.jsonRequest(http.MethodPut, fmt.Sprintf("/vector/indexes/%s/auto-links", indexName), req)
	return err
}

// GetAutoLinks retrieves the current auto-linking rules for an index.
func (c *Client) GetAutoLinks(indexName string) ([]AutoLinkRule, error) {
	respBody, err := c.jsonRequest(http.MethodGet, fmt.Sprintf("/vector/indexes/%s/auto-links", indexName), nil)
	if err != nil {
		return nil, err
	}
	var resp autoLinksResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Rules, nil
}

// --- Cognitive Engine ---

// GetReflections retrieves reflection nodes from the cognitive engine.
func (c *Client) GetReflections(indexName, status string) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("/vector/indexes/%s/reflections", indexName)
	if status != "" {
		url += "?status=" + status
	}
	respBody, err := c.jsonRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var resp reflectionsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Reflections, nil
}

// ResolveReflection resolves a cognitive reflection.
func (c *Client) ResolveReflection(indexName, reflectionID, resolution, discardID string) error {
	req := resolveRequest{
		Resolution: resolution,
		DiscardID:  discardID,
	}
	_, err := c.jsonRequest(http.MethodPost, fmt.Sprintf("/vector/indexes/%s/reflections/%s/resolve", indexName, reflectionID), req)
	return err
}

// Think triggers a manual cognitive reflection cycle for an index.
func (c *Client) Think(indexName string) error {
	_, err := c.jsonRequest(http.MethodPost, fmt.Sprintf("/vector/indexes/%s/cognitive/think", indexName), nil)
	return err
}

// --- RAG ---

// RagRetrieve retrieves text chunks via a configured RAG pipeline.
func (c *Client) RagRetrieve(pipelineName, query string, k int) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"pipeline_name": pipelineName,
		"query":         query,
		"k":             k,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/rag/retrieve", payload)
	if err != nil {
		return nil, err
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp, nil
}

// --- System ---

// Save forces a database snapshot.
func (c *Client) Save() error {
	_, err := c.jsonRequest(http.MethodPost, "/system/save", nil)
	return err
}

// --- Auth ---

// CreateApiKey creates a new API key with RBAC permissions.
func (c *Client) CreateApiKey(role, namespace string) (string, error) {
	req := apiKeyRequest{
		Role:      role,
		Namespace: namespace,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/auth/keys", req)
	if err != nil {
		return "", err
	}
	var resp apiKeyResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	return resp.Token, nil
}

// ListApiKeys lists all API key policies.
func (c *Client) ListApiKeys() ([]map[string]interface{}, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/auth/keys", nil)
	if err != nil {
		return nil, err
	}
	var resp []map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return resp, nil
}

// RevokeApiKey revokes an API key.
func (c *Client) RevokeApiKey(keyID string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/auth/keys/"+keyID, nil)
	return err
}

// --- Session Management ---

// StartSession creates a new conversational session.
func (c *Client) StartSession(req StartSessionRequest) (*StartSessionResponse, error) {
	respBody, err := c.jsonRequest(http.MethodPost, "/sessions", req)
	if err != nil {
		return nil, err
	}
	var resp StartSessionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for StartSession: %w", err)
	}
	return &resp, nil
}

// EndSession terminates a conversational session.
func (c *Client) EndSession(sessionID string, indexName string) error {
	req := EndSessionRequest{SessionID: sessionID, IndexName: indexName}
	_, err := c.jsonRequest(http.MethodPost, "/sessions/"+sessionID+"/end", req)
	return err
}

// --- Adaptive Retrieval ---

// AdaptiveRetrieve performs graph-aware context retrieval.
func (c *Client) AdaptiveRetrieve(req AdaptiveRetrieveRequest) (*AdaptiveRetrieveResponse, error) {
	respBody, err := c.jsonRequest(http.MethodPost, "/rag/retrieve-adaptive", req)
	if err != nil {
		return nil, err
	}
	var resp AdaptiveRetrieveResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for AdaptiveRetrieve: %w", err)
	}
	return &resp, nil
}

// RagRetrieveWithProvenance retrieves text chunks with source attribution.
func (c *Client) RagRetrieveWithProvenance(pipelineName, query string, k int, includeProvenance bool) (*RagRetrieveResponse, error) {
	req := map[string]interface{}{
		"pipeline_name":      pipelineName,
		"query":              query,
		"k":                  k,
		"include_provenance": includeProvenance,
	}
	respBody, err := c.jsonRequest(http.MethodPost, "/rag/retrieve", req)
	if err != nil {
		return nil, err
	}
	var resp RagRetrieveResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for RagRetrieveWithProvenance: %w", err)
	}
	return &resp, nil
}

// --- User Profiles ---

// GetUserProfile retrieves a user's personality profile from the specified index.
func (c *Client) GetUserProfile(userID, indexName string) (*UserProfile, error) {
	url := fmt.Sprintf("/users/%s/profile?index_name=%s", userID, indexName)
	respBody, err := c.jsonRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var resp UserProfile
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for GetUserProfile: %w", err)
	}
	return &resp, nil
}

// ListUserProfiles lists all user profiles from the specified index.
func (c *Client) ListUserProfiles(indexName string) ([]UserProfileItem, error) {
	url := fmt.Sprintf("/users?index_name=%s", indexName)
	respBody, err := c.jsonRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Profiles []UserProfileItem `json:"profiles"`
		Count    int               `json:"count"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON response for ListUserProfiles: %w", err)
	}
	return resp.Profiles, nil
}
