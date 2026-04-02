package llm

import (
	"testing"
)

func TestNewClient_DefaultURLs(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		wantBaseURL string
	}{
		{
			name:        "huggingface default URL",
			cfg:         Config{Provider: "huggingface"},
			wantBaseURL: "https://api-inference.huggingface.co/v1",
		},
		{
			name:        "ollama default URL",
			cfg:         Config{Provider: "ollama"},
			wantBaseURL: "http://localhost:11434/v1",
		},
		{
			name:        "openai default URL",
			cfg:         Config{Provider: "openai"},
			wantBaseURL: "https://api.openai.com/v1",
		},
		{
			name:        "empty provider uses openai default",
			cfg:         Config{},
			wantBaseURL: "https://api.openai.com/v1",
		},
		{
			name:        "custom URL is preserved",
			cfg:         Config{Provider: "huggingface", BaseURL: "https://custom.endpoint.com/v1"},
			wantBaseURL: "https://custom.endpoint.com/v1",
		},
		{
			name:        "trailing slash is trimmed",
			cfg:         Config{Provider: "huggingface", BaseURL: "https://custom.endpoint.com/v1/"},
			wantBaseURL: "https://custom.endpoint.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.cfg)
			if client == nil {
				t.Fatal("NewClient returned nil")
			}
			oc, ok := client.(*OpenAIClient)
			if !ok {
				t.Fatal("NewClient did not return *OpenAIClient")
			}
			if oc.cfg.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", oc.cfg.BaseURL, tt.wantBaseURL)
			}
		})
	}
}

func TestNewClient_ReturnsInterface(t *testing.T) {
	// Verify the return type satisfies the Client interface
	var c Client
	c = NewClient(Config{Provider: "huggingface"})
	if c == nil {
		t.Fatal("expected non-nil Client")
	}
}
