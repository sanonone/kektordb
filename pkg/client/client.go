// File: pkg/client/client.go
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// --- Errori Custom ---

// APIError rappresenta un errore restituito dall'API di KektorDB (status >= 400).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("errore API (status %d): %s", e.StatusCode, e.Message)
}

// --- Struct per le Risposte JSON ---

// kvResponse modella la risposta per le operazioni GET sul KV store.
type kvResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// searchResponse modella la risposta per le operazioni VSEARCH.
type searchResponse struct {
	Results []string `json:"results"`
}

// VectorData modella i dati completi di un vettore per le API di recupero.
type VectorData struct {
	ID       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata"`
}

// IndexInfo modella le informazioni di un indice per l'API di introspezione.
type IndexInfo struct {
	Name           string `json:"name"`
	Metric         string `json:"metric"`
	Precision      string `json:"precision"`
	M              int    `json:"m"`
	EfConstruction int    `json:"ef_construction"`
	VectorCount    int    `json:"vector_count"`
}

// struct per un singolo oggetto nel batch
type VectorAddObject struct {
	Id       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// --- Client ---

// Client è il client Go per interagire con KektorDB.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New crea un nuovo client KektorDB.
func New(host string, port int) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("http://%s:%d", host, port),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// jsonRequest è l'unico metodo helper per eseguire tutte le richieste all'API.
// Gestisce la serializzazione JSON, le chiamate HTTP e la gestione degli errori.
func (c *Client) jsonRequest(method, endpoint string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("impossibile marshallare il payload JSON: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("impossibile creare la richiesta: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("errore di connessione: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // Per risposte 204 (es. DELETE)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("impossibile leggere il corpo della risposta: %w", err)
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

// --- Metodi KV ---

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
		return nil, fmt.Errorf("risposta JSON invalida per GET: %w", err)
	}
	return []byte(resp.Value), nil
}

func (c *Client) Delete(key string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/kv/"+key, nil)
	return err
}

// --- Metodi Vettoriali ---

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

func (c *Client) ListIndexes() ([]IndexInfo, error) {
	respBody, err := c.jsonRequest(http.MethodGet, "/vector/indexes", nil)
	if err != nil {
		return nil, err
	}
	var resp []IndexInfo
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("risposta JSON invalida per ListIndexes: %w", err)
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
		return nil, fmt.Errorf("risposta JSON invalida per GetIndexInfo: %w", err)
	}
	return &resp, nil
}

func (c *Client) DeleteIndex(indexName string) error {
	_, err := c.jsonRequest(http.MethodDelete, "/vector/indexes/"+indexName, nil)
	return err
}

func (c *Client) VCompress(indexName, precision string) error {
	payload := map[string]interface{}{
		"index_name": indexName,
		"precision":  precision,
	}
	_, err := c.jsonRequest(http.MethodPost, "/vector/actions/compress", payload)
	return err
}

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
		return nil, fmt.Errorf("risposta JSON invalida per VAddBatch: %w", err)
	}
	return resp, nil
}

func (c *Client) VSearch(indexName string, k int, queryVector []float32, filter string, efSearch int) ([]string, error) {
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

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/actions/search", payload)
	if err != nil {
		return nil, err
	}
	var resp searchResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("risposta JSON invalida per VSEARCH: %w", err)
	}
	return resp.Results, nil
}

func (c *Client) VDelete(indexName, id string) error {
	payload := map[string]string{
		"index_name": indexName,
		"id":         id,
	}
	// CORREZIONE: Usa l'endpoint corretto
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
		return nil, fmt.Errorf("risposta JSON invalida per VGet: %w", err)
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
		return nil, fmt.Errorf("risposta JSON invalida per VGetMany: %w", err)
	}
	return resp, nil
}

// --- Metodi di Amministrazione ---

func (c *Client) AOFRewrite() error {
	_, err := c.jsonRequest(http.MethodPost, "/system/aof-rewrite", nil)
	return err
}
