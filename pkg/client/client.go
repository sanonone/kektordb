// Package client provides a Go client for interacting with the KektorDB API.
//
// It offers a type-safe way to perform all major operations, including:
//   - Key-Value (KV) store operations (Set, Get, Delete).
//   - Vector index management (Create, List, Delete, Get Info).
//   - Vector data operations (Add, AddBatch, Search, Delete, Get).
//   - System administration tasks (AOF Rewrite, Task Status).
//
// The client handles HTTP communication, JSON serialization/deserialization, and
// standardized error handling.
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

// APIError represents an error returned by the KektorDB API (status >= 400).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// --- JSON Response Structs ---

// kvResponse models the response for GET operations on the KV store.
type kvResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// searchResponse models the response for VSEARCH operations.
type searchResponse struct {
	Results []string `json:"results"`
}

// VectorData models the complete data of a vector for retrieval APIs.
type VectorData struct {
	ID       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata"`
}

// IndexInfo models the information about an index for the introspection API.
type IndexInfo struct {
	Name           string `json:"name"`
	Metric         string `json:"metric"`
	Precision      string `json:"precision"`
	M              int    `json:"m"`
	EfConstruction int    `json:"ef_construction"`
	VectorCount    int    `json:"vector_count"`
}

// VectorAddObject represents a single object within a batch add request.
type VectorAddObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Task represents an asynchronous operation on the KektorDB server.
type Task struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ProgressMessage string `json:"progress_message,omitempty"`
	Error           string `json:"error,omitempty"`

	client *Client // Reference to the client for polling.
}

// --- Client ---

// Client is the Go client for interacting with KektorDB.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new KektorDB client.
func New(host string, port int) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("http://%s:%d", host, port),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// jsonRequest is a helper method to execute all requests to the API.
// It handles JSON serialization, HTTP calls, and error management.
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // For 204 responses (e.g., DELETE).
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

// Refresh updates the task's status by querying the server.
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

// Wait blocks until the task is completed, checking its status at regular intervals.
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
				// Continue waiting.
			default:
				return fmt.Errorf("unknown task status: %s", t.Status)
			}
		}
	}
}

// --- KV Methods ---

// Set sets a key-value pair.
func (c *Client) Set(key string, value []byte) error {
	payload := map[string]string{"value": string(value)}
	_, err := c.jsonRequest(http.MethodPost, "/kv/"+key, payload)
	return err
}

// Get retrieves a value by its key.
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

// Delete removes a key-value pair.
func (c *Client) Delete(key string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/kv/"+key, nil)
	return err
}

// --- Vector Methods ---

// VCreate creates a new vector index.
func (c *Client) VCreate(indexName, metric, precision string, m, efConstruction int) error {
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

	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/create", payload)
	return err
}

// ListIndexes returns a list of all vector indexes.
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

// GetIndexInfo retrieves information about a specific index.
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

// DeleteIndex deletes a vector index.
func (c *Client) DeleteIndex(indexName string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/vector/indexes/"+indexName, nil)
	return err
}

// VCompress starts the compression of an index and returns a Task.
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
	task.client = c // Inject the client to allow polling.
	return &task, nil
}

// VAdd adds a single vector to an index.
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

// VAddBatch adds multiple vectors to an index in a single request.
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

// VSearch performs a vector search with optional filtering.
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

// VDelete deletes a single vector from an index.
func (c *Client) VDelete(indexName, id string) error {
	payload := map[string]string{
		"index_name": indexName,
		"id":         id,
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/delete_vector", payload)
	return err
}

// VGet retrieves a single vector by its ID.
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

// VGetMany retrieves multiple vectors by their IDs in a single request.
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

// --- Administration Methods ---

// AOFRewrite triggers an AOF rewrite and returns a Task.
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

// GetTaskStatus retrieves the status of a long-running task.
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
