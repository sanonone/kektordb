package llm

// Config holds the connection settings for an LLM provider.
// It is designed to be embedded in YAML configuration files.
type Config struct {
	// BaseURL is the API endpoint.
	// Examples:
	// - OpenAI: "https://api.openai.com/v1"
	// - Ollama: "http://localhost:11434/v1"
	// - LocalAI: "http://localhost:8080/v1"
	BaseURL string `yaml:"base_url" json:"base_url"`

	// APIKey is the authentication token.
	// Required for OpenAI ("sk-..."). Often ignored by local Ollama.
	APIKey string `yaml:"api_key" json:"api_key"`

	// Model is the specific model identifier.
	// Examples: "gpt-4o", "llama3", "mistral".
	Model string `yaml:"model" json:"model"`

	// Temperature controls randomness (0.0 = deterministic, 1.0 = creative).
	Temperature float64 `yaml:"temperature" json:"temperature"`

	// MaxTokens limits the response length (optional).
	MaxTokens int `yaml:"max_tokens" json:"max_tokens"`
}

// DefaultConfig returns safe defaults for a local setup (Ollama).
func DefaultConfig() Config {
	return Config{
		BaseURL:     "http://localhost:11434/v1",
		APIKey:      "kektor", // Placeholder
		Model:       "qwen3:4b",
		Temperature: 0.0,
	}
}

// --- Internal API Payloads (OpenAI Compatible) ---

// ChatRequest represents the payload sent to POST /chat/completions
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

// Message represents a single turn in the chat conversation.
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` // The actual text
}

// ChatResponse represents the standard response from OpenAI-compatible APIs.
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *APIError `json:"error,omitempty"`
}

// APIError captures error details returned by the provider.
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
