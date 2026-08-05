package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type OpenAIEmbedder struct {
	URL    string
	Model  string
	APIKey string
	Client *http.Client
}

func NewOpenAIEmbedder(url, model, apiKey string, timeout time.Duration) *OpenAIEmbedder {
	if url == "" {
		url = "https://api.openai.com/v1/embeddings" // Default ufficiale
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OpenAIEmbedder{
		URL:    url,
		Model:  model,
		APIKey: apiKey,
		Client: &http.Client{Timeout: timeout},
	}
}

func (e *OpenAIEmbedder) Embed(text string) ([]float32, error) {
	// OpenAI Request Format
	payload := map[string]interface{}{
		"input": text,
		"model": e.Model,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", e.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai returned status: %s", resp.Status)
	}

	// OpenAI Response Format
	// { "data": [ { "embedding": [...] } ] }
	var openAIResp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode openai response: %w", err)
	}

	if len(openAIResp.Data) == 0 {
		return nil, fmt.Errorf("openai returned no data")
	}

	return openAIResp.Data[0].Embedding, nil
}

func (e *OpenAIEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	vecs, err := e.embedBatchNative(texts)
	if err == nil {
		return vecs, nil
	}
	// Fall back to serial Embed on any batch failure (unsupported endpoint,
	// shape mismatch, transient error).
	return embedBatchSerial(e, texts)
}

func (e *OpenAIEmbedder) embedBatchNative(texts []string) ([][]float32, error) {
	payload := map[string]interface{}{
		"input": texts,
		"model": e.Model,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", e.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai batch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai batch returned status: %s", resp.Status)
	}

	// OpenAI returns one data entry per input, ordered with an index field.
	var openAIResp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode openai batch response: %w", err)
	}
	if len(openAIResp.Data) != len(texts) {
		return nil, fmt.Errorf("openai batch returned %d embeddings for %d texts", len(openAIResp.Data), len(texts))
	}

	vecs := make([][]float32, len(texts))
	for _, d := range openAIResp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("openai batch returned out-of-range index %d", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}
