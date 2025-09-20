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

func (c *Client) VCreate(indexName, metric string, m, efConstruction int) error {
	payload := map[string]interface{}{"index_name": indexName}
	if metric != "" {
		payload["metric"] = metric
	}
	if m > 0 {
		payload["m"] = m
	}
	if efConstruction > 0 {
		payload["ef_construction"] = efConstruction
	}

	_, err := c.jsonRequest(http.MethodPost, "/vector/create", payload)
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
	_, err := c.jsonRequest(http.MethodPost, "/vector/add", payload)
	return err
}

func (c *Client) VSearch(indexName string, k int, queryVector []float32, filter string) ([]string, error) {
	payload := map[string]interface{}{
		"index_name":   indexName,
		"k":            k,
		"query_vector": queryVector,
	}
	if filter != "" {
		payload["filter"] = filter
	}

	respBody, err := c.jsonRequest(http.MethodPost, "/vector/search", payload)
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
	_, err := c.jsonRequest(http.MethodPost, "/vector/delete", payload)
	return err
}

// --- Metodi di Amministrazione ---

func (c *Client) AOFRewrite() error {
	_, err := c.jsonRequest(http.MethodPost, "/system/aof-rewrite", nil)
	return err
}
