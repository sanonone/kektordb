package llm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GeminiClient implements Client using Google's native generateContent API.
type GeminiClient struct {
	cfg        Config
	httpClient *http.Client
}

var _ Client = (*GeminiClient)(nil)

type geminiGenerateContentRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiGenerateContentResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

type geminiInteractionRequest struct {
	Model             string                             `json:"model,omitempty"`
	Input             interface{}                        `json:"input"`
	SystemInstruction string                             `json:"system_instruction,omitempty"`
	Stream            bool                               `json:"stream"`
	GenerationConfig  *geminiInteractionGenerationConfig `json:"generation_config,omitempty"`
}

type geminiInteractionGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
}

type geminiInteractionContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type geminiInteractionResponse struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string                     `json:"type"`
		Content []geminiInteractionContent `json:"content"`
	} `json:"steps"`
	Error *APIError `json:"error,omitempty"`
}

// NewGeminiClient creates a Gemini API client.
func NewGeminiClient(cfg Config) *GeminiClient {
	return &GeminiClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Chat performs a text-only generateContent request.
func (c *GeminiClient) Chat(systemPrompt, userQuery string) (string, error) {
	if c.usesInteractionsAPI() {
		reqBody := c.newInteractionRequest(systemPrompt, userQuery)
		return c.sendInteractionRequest(reqBody)
	}

	reqBody := c.newRequest(systemPrompt, textParts(userQuery))
	return c.sendGenerateContentRequest(reqBody)
}

// ChatWithImages sends text and inline image data to a multimodal Gemini model.
func (c *GeminiClient) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	if c.usesInteractionsAPI() {
		reqBody := c.newInteractionRequest(systemPrompt, c.interactionParts(userQuery, images))
		return c.sendInteractionRequest(reqBody)
	}

	parts := textParts(userQuery)
	for _, imgBytes := range images {
		if len(imgBytes) == 0 {
			continue
		}

		parts = append(parts, geminiPart{
			InlineData: &geminiInlineData{
				MimeType: http.DetectContentType(imgBytes),
				Data:     base64.StdEncoding.EncodeToString(imgBytes),
			},
		})
	}

	reqBody := c.newRequest(systemPrompt, parts)
	return c.sendGenerateContentRequest(reqBody)
}

func (c *GeminiClient) newInteractionRequest(systemPrompt string, input interface{}) geminiInteractionRequest {
	if input == nil || input == "" {
		input = " "
	}

	reqBody := geminiInteractionRequest{
		Model:  c.cfg.Model,
		Input:  input,
		Stream: false,
		GenerationConfig: &geminiInteractionGenerationConfig{
			Temperature: c.cfg.Temperature,
		},
	}
	if systemPrompt != "" {
		reqBody.SystemInstruction = systemPrompt
	}
	if c.cfg.MaxTokens > 0 {
		reqBody.GenerationConfig.MaxOutputTokens = c.cfg.MaxTokens
	}

	return reqBody
}

func (c *GeminiClient) interactionParts(userQuery string, images [][]byte) []geminiInteractionContent {
	parts := []geminiInteractionContent{}
	if userQuery != "" {
		parts = append(parts, geminiInteractionContent{
			Type: "text",
			Text: userQuery,
		})
	}

	for _, imgBytes := range images {
		if len(imgBytes) == 0 {
			continue
		}

		parts = append(parts, geminiInteractionContent{
			Type:     "image",
			MimeType: http.DetectContentType(imgBytes),
			Data:     base64.StdEncoding.EncodeToString(imgBytes),
		})
	}

	if len(parts) == 0 {
		return nil
	}
	return parts
}

func (c *GeminiClient) newRequest(systemPrompt string, userParts []geminiPart) geminiGenerateContentRequest {
	if len(userParts) == 0 && systemPrompt != "" {
		userParts = textParts(systemPrompt)
		systemPrompt = ""
	}
	if len(userParts) == 0 {
		userParts = textParts(" ")
	}

	reqBody := geminiGenerateContentRequest{
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: userParts,
			},
		},
		GenerationConfig: &geminiGenerationConfig{
			Temperature: c.cfg.Temperature,
		},
	}

	if systemPrompt != "" {
		reqBody.SystemInstruction = &geminiContent{
			Parts: textParts(systemPrompt),
		}
	}
	if c.cfg.MaxTokens > 0 {
		reqBody.GenerationConfig.MaxOutputTokens = c.cfg.MaxTokens
	}

	return reqBody
}

func textParts(text string) []geminiPart {
	if text == "" {
		return nil
	}
	return []geminiPart{{Text: text}}
}

func (c *GeminiClient) sendGenerateContentRequest(payload geminiGenerateContentRequest) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	endpoint, err := c.endpoint()
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := c.apiKey(); apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini connection failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp geminiGenerateContentResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return "", fmt.Errorf("failed to decode gemini response: %w", err)
	}
	if geminiResp.Error != nil {
		return "", fmt.Errorf("provider error: %s", geminiResp.Error.Message)
	}
	if len(geminiResp.Candidates) == 0 {
		if geminiResp.PromptFeedback != nil && geminiResp.PromptFeedback.BlockReason != "" {
			return "", fmt.Errorf("gemini blocked prompt: %s", geminiResp.PromptFeedback.BlockReason)
		}
		return "", fmt.Errorf("empty response from gemini")
	}

	var out strings.Builder
	for _, part := range geminiResp.Candidates[0].Content.Parts {
		out.WriteString(part.Text)
	}
	if out.Len() == 0 {
		if geminiResp.Candidates[0].FinishReason != "" {
			return "", fmt.Errorf("gemini returned no text (finish reason: %s)", geminiResp.Candidates[0].FinishReason)
		}
		return "", fmt.Errorf("gemini returned no text")
	}

	return out.String(), nil
}

func (c *GeminiClient) sendInteractionRequest(payload geminiInteractionRequest) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini interaction request: %w", err)
	}

	endpoint, err := c.interactionEndpoint()
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create gemini interaction request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := c.apiKey(); apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini interaction connection failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini interaction api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var interactionResp geminiInteractionResponse
	if err := json.Unmarshal(bodyBytes, &interactionResp); err != nil {
		return "", fmt.Errorf("failed to decode gemini interaction response: %w", err)
	}
	if interactionResp.Error != nil {
		return "", fmt.Errorf("provider error: %s", interactionResp.Error.Message)
	}

	var out strings.Builder
	for _, step := range interactionResp.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, content := range step.Content {
			if content.Type == "text" {
				out.WriteString(content.Text)
			}
		}
	}
	if out.Len() == 0 {
		if interactionResp.Status != "" {
			return "", fmt.Errorf("gemini interaction returned no text (status: %s)", interactionResp.Status)
		}
		return "", fmt.Errorf("gemini interaction returned no text")
	}

	return out.String(), nil
}

func (c *GeminiClient) usesInteractionsAPI() bool {
	return strings.Contains(strings.TrimRight(c.cfg.BaseURL, "/"), "/interactions")
}

func (c *GeminiClient) apiKey() string {
	if c.cfg.APIKey != "" {
		return c.cfg.APIKey
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("GOOGLE_API_KEY")
}

func (c *GeminiClient) endpoint() (string, error) {
	baseURL := strings.TrimRight(c.cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}

	endpoint := baseURL
	if !strings.Contains(endpoint, ":generateContent") {
		model := strings.TrimSpace(c.cfg.Model)
		if model == "" {
			return "", fmt.Errorf("gemini model is required")
		}
		model = strings.TrimPrefix(model, "/")
		if !strings.HasPrefix(model, "models/") && !strings.HasPrefix(model, "tunedModels/") {
			model = "models/" + model
		}
		endpoint = fmt.Sprintf("%s/%s:generateContent", baseURL, model)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid gemini endpoint: %w", err)
	}
	if apiKey := c.apiKey(); apiKey != "" && u.Query().Get("key") == "" {
		q := u.Query()
		q.Set("key", apiKey)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

func (c *GeminiClient) interactionEndpoint() (string, error) {
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/")
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta/interactions"
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid gemini interaction endpoint: %w", err)
	}
	if apiKey := c.apiKey(); apiKey != "" && u.Query().Get("key") == "" {
		q := u.Query()
		q.Set("key", apiKey)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}
