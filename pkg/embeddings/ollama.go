package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OllamaEmbedder implements the Embedder interface using a remote Ollama instance.
type OllamaEmbedder struct {
	URL    string
	Model  string
	Client *http.Client
}

func NewOllamaEmbedder(url, model string, timeout time.Duration) *OllamaEmbedder {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OllamaEmbedder{
		URL:   url,
		Model: model,
		Client: &http.Client{
			Timeout: timeout, // Use the configured value
		},
	}
}

func (e *OllamaEmbedder) Embed(text string) ([]float32, error) {
	payload := map[string]interface{}{
		"model":  e.Model,
		"prompt": text,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := e.Client.Post(e.URL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status: %s", resp.Status)
	}

	var ollamaResp struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode ollama response: %w", err)
	}

	return ollamaResp.Embedding, nil
}

// batchURL derives the native batch endpoint. Ollama's /api/embeddings accepts
// a single prompt; /api/embed accepts an input array in one round trip.
// Custom/OpenAI-compatible URLs (no /api/embeddings) are used as-is, since
// they accept the same {"input": []string} payload.
func (e *OllamaEmbedder) batchURL() string {
	return strings.Replace(e.URL, "/api/embeddings", "/api/embed", 1)
}

func (e *OllamaEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	vecs, err := e.embedBatchNative(texts)
	if err == nil {
		return vecs, nil
	}
	// Fall back to serial Embed on any batch failure (unsupported endpoint,
	// shape mismatch, transient error). Never fail a batch solely because
	// the provider lacks a batch API.
	return embedBatchSerial(e, texts)
}

func (e *OllamaEmbedder) embedBatchNative(texts []string) ([][]float32, error) {
	payload := map[string]interface{}{
		"model": e.Model,
		"input": texts,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := e.Client.Post(e.batchURL(), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ollama batch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama batch returned status: %s", resp.Status)
	}

	// Ollama /api/embed returns {"embeddings": [[...], ...]}; OpenAI-compatible
	// proxies may return {"data": [{"embedding": [...]}, ...]} instead.
	var ollamaBatchResp struct {
		Embeddings [][]float32 `json:"embeddings"`
		Data       []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaBatchResp); err != nil {
		return nil, fmt.Errorf("failed to decode ollama batch response: %w", err)
	}

	var vecs [][]float32
	switch {
	case len(ollamaBatchResp.Embeddings) > 0:
		vecs = ollamaBatchResp.Embeddings
	case len(ollamaBatchResp.Data) > 0:
		vecs = make([][]float32, len(ollamaBatchResp.Data))
		for i, d := range ollamaBatchResp.Data {
			vecs[i] = d.Embedding
		}
	default:
		return nil, fmt.Errorf("ollama batch returned no embeddings")
	}

	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("ollama batch returned %d embeddings for %d texts", len(vecs), len(texts))
	}
	return vecs, nil
}
