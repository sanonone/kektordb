package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GeminiEmbedder implements Embedder using Google's native embedContent API.
type GeminiEmbedder struct {
	URL    string
	Model  string
	APIKey string
	Client *http.Client
}

var _ Embedder = (*GeminiEmbedder)(nil)

func NewGeminiEmbedder(urlStr, model, apiKey string, timeout time.Duration) *GeminiEmbedder {
	if model == "" {
		model = "gemini-embedding-001"
	}
	if urlStr == "" {
		urlStr = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:embedContent", geminiModelResource(model))
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	return &GeminiEmbedder{
		URL:    strings.TrimRight(urlStr, "/"),
		Model:  model,
		APIKey: apiKey,
		Client: &http.Client{Timeout: timeout},
	}
}

func (e *GeminiEmbedder) Embed(text string) ([]float32, error) {
	payload := map[string]interface{}{
		"model": geminiModelResource(e.Model),
		"content": map[string]interface{}{
			"parts": []map[string]string{
				{"text": text},
			},
		},
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint, err := e.endpoint()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini embedder request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey := e.apiKey(); apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini embedder request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embedder returned status: %s", resp.Status)
	}

	var geminiResp struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini embedder response: %w", err)
	}
	if geminiResp.Error != nil {
		return nil, fmt.Errorf("gemini embedder provider error: %s", geminiResp.Error.Message)
	}
	if len(geminiResp.Embedding.Values) == 0 {
		return nil, fmt.Errorf("gemini embedder returned no values")
	}

	return geminiResp.Embedding.Values, nil
}

func (e *GeminiEmbedder) endpoint() (string, error) {
	endpoint := e.URL
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:embedContent", geminiModelResource(e.Model))
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid gemini embedder endpoint: %w", err)
	}

	return u.String(), nil
}

func (e *GeminiEmbedder) apiKey() string {
	if e.APIKey != "" {
		return e.APIKey
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("GOOGLE_API_KEY")
}

func geminiModelResource(model string) string {
	model = strings.TrimSpace(strings.TrimPrefix(model, "/"))
	if model == "" {
		model = "gemini-embedding-001"
	}
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}
