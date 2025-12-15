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
