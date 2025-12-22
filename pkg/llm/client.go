package llm

import (
	"bytes"
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

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// 3. Construct URL
	// Standard endpoint for chat completions
	url := fmt.Sprintf("%s/chat/completions", c.cfg.BaseURL)

	// 4. Create Request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	// 5. Execute
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm connection failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	// 6. Handle HTTP Errors
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// 7. Decode Response
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
