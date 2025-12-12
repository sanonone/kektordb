package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
			Timeout: timeout, // Usa il valore configurato
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
