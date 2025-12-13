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

type VectorAddObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
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
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

type graphGetLinksRequest struct {
	SourceID     string `json:"source_id"`
	RelationType string `json:"relation_type"`
}

type graphGetLinksResponse struct {
	Targets []string `json:"targets"`
}

// GraphSearchResult represents a search result enriched with scores and relationships.
type GraphSearchResult struct {
	ID        string              `json:"id"`
	Score     float64             `json:"score"`
	Relations map[string][]string `json:"relations,omitempty"`
}

type searchGraphResponse struct {
	Results []GraphSearchResult `json:"results"`
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

func (c *Client) VAdd(indexName, id string, vector []float32, metadata map[string]interface{}) error {
	payload := map[string]interface{}{
		"index_name": indexName,
		"id":         id,
		"vector":     vector,
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

func (c *Client) VSearch(indexName string, k int, queryVector []float32, filter string, efSearch int, alpha float64) ([]string, error) {
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
func (c *Client) VLink(sourceID, targetID, relationType string) error {
	req := graphLinkRequest{
		SourceID:     sourceID,
		TargetID:     targetID,
		RelationType: relationType,
	}
	_, err := c.jsonRequest(http.MethodPost, "/graph/actions/link", req)
	return err
}

// VUnlink removes a semantic relationship.
func (c *Client) VUnlink(sourceID, targetID, relationType string) error {
	req := graphLinkRequest{
		SourceID:     sourceID,
		TargetID:     targetID,
		RelationType: relationType,
	}
	_, err := c.jsonRequest(http.MethodPost, "/graph/actions/unlink", req)
	return err
}

// VGetLinks retrieves all target IDs linked to a source ID by a specific relation type.
func (c *Client) VGetLinks(sourceID, relationType string) ([]string, error) {
	req := graphGetLinksRequest{
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

// VSearchGraph performs a search and returns detailed results including scores and graph relations.
// 'includeRelations' is a list of relation types to fetch (e.g. []string{"parent", "next"}).
func (c *Client) VSearchGraph(indexName string, queryVector []float32, k int, filter string, efSearch int, alpha float64, includeRelations []string) ([]GraphSearchResult, error) {
	// Riutilizziamo la struct di richiesta esistente in http_types del server,
	// ma qui la definiamo dinamicamente o estendiamo quella locale se presente.
	// Usiamo una map per flessibilità o estendiamo la struct interna.

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
	// Campo chiave per attivare la logica Graph nel server
	if len(includeRelations) > 0 {
		payload["include_relations"] = includeRelations
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
