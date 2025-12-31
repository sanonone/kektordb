package llm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client defines the interface for interacting with an LLM.
// This abstraction allows for easy mocking in tests.
type Client interface {
	// Chat sends a prompt to the LLM and returns the text response.
	// systemPrompt: Instructions for the AI behavior (e.g., "You are a helpful assistant").
	// userQuery: The actual input from the user.
	Chat(systemPrompt, userQuery string) (string, error)

	// ChatWithImages performs a multi-modal request (text + images).
	// images: A slice of raw byte slices (e.g., file contents).
	ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error)
}

// OpenAIClient implements the Client interface for OpenAI-compatible APIs.
// It works with OpenAI, Ollama, LocalAI, vLLM, etc.
type OpenAIClient struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient initializes a new LLM client.
func NewClient(cfg Config) *OpenAIClient {
	// Robustness: ensure BaseURL does not end with a slash
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")

	return &OpenAIClient{
		cfg: cfg,
		httpClient: &http.Client{
			// Set a reasonable timeout. Generation can be slow.
			Timeout: 120 * time.Second,
		},
	}
}

// Chat performs a completion request.
func (c *OpenAIClient) Chat(systemPrompt, userQuery string) (string, error) {
	// 1. Prepare Messages
	messages := []Message{}
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, Message{Role: "user", Content: userQuery})

	// 2. Prepare Payload
	reqBody := ChatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: c.cfg.Temperature,
		Stream:      false, // We use blocking requests for internal logic (HyDe/Entities)
	}

	if c.cfg.MaxTokens > 0 {
		reqBody.MaxTokens = c.cfg.MaxTokens
	}

	return c.sendRequest(reqBody)

}

// ChatWithImages sends images along with the prompt to a Vision model.
func (c *OpenAIClient) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	// 1. Prepare System Message
	// Note: System messages for vision models are usually just text.
	var messages []interface{}

	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// 2. Prepare User Message (Multi-modal content array)
	// We need to construct a specific JSON structure for mixed content.

	// Start with the text part
	contentParts := []map[string]interface{}{
		{
			"type": "text",
			"text": userQuery,
		},
	}

	// Add images converted to Base64
	for _, imgBytes := range images {
		if len(imgBytes) == 0 {
			continue
		}

		// Detect MIME type (simple detection)
		mimeType := http.DetectContentType(imgBytes)
		base64Str := base64.StdEncoding.EncodeToString(imgBytes)

		// Create the data URI scheme: "data:image/png;base64,..."
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Str)

		imagePart := map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]string{
				"url": dataURI,
			},
		}
		contentParts = append(contentParts, imagePart)
	}

	// Append the complex user message
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": contentParts,
	})

	// 3. Prepare Payload (using a dynamic map because the struct structure changed)
	reqBody := map[string]interface{}{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": c.cfg.Temperature,
		"stream":      false,
	}

	if c.cfg.MaxTokens > 0 {
		reqBody["max_tokens"] = c.cfg.MaxTokens
	}

	return c.sendRequest(reqBody)
}

// sendRequest handles the common HTTP logic for both text and vision requests.
func (c *OpenAIClient) sendRequest(payload interface{}) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", c.cfg.BaseURL)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm connection failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("provider error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty response from llm")
	}

	return chatResp.Choices[0].Message.Content, nil
}
