package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestNewClient_Gemini(t *testing.T) {
	client := NewClient(Config{Provider: "gemini"})
	gc, ok := client.(*GeminiClient)
	if !ok {
		t.Fatal("NewClient did not return *GeminiClient")
	}
	if gc.cfg.BaseURL != "https://generativelanguage.googleapis.com/v1beta" {
		t.Errorf("BaseURL = %q, want Gemini default", gc.cfg.BaseURL)
	}
	if gc.cfg.Model != "gemini-2.0-flash" {
		t.Errorf("Model = %q, want Gemini default", gc.cfg.Model)
	}
}

func TestNewClient_GeminiDetectedFromBaseURL(t *testing.T) {
	client := NewClient(Config{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/interactions",
		Model:   "gemini-3.5-flash",
	})
	if _, ok := client.(*GeminiClient); !ok {
		t.Fatal("NewClient did not detect Gemini from generativelanguage base_url")
	}
}

func TestGeminiClientChat_RequestAndResponse(t *testing.T) {
	var gotReq geminiGenerateContentRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/models/gemini-test:generateContent" {
			t.Errorf("path = %s, want Gemini generateContent path", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Errorf("x-goog-api-key header = %q, want secret", r.Header.Get("x-goog-api-key"))
		}
		if r.URL.RawQuery != "" {
			t.Errorf("URL must not contain query parameters (key leaked in URL): %s", r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {
					"parts": [
						{"text": "hello "},
						{"text": "world"}
					]
				}
			}]
		}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:    "gemini",
		BaseURL:     server.URL,
		APIKey:      "secret",
		Model:       "gemini-test",
		Temperature: 0.2,
		MaxTokens:   64,
	})

	got, err := client.Chat("system prompt", "user prompt")
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("Chat = %q, want %q", got, "hello world")
	}

	if gotReq.SystemInstruction == nil || len(gotReq.SystemInstruction.Parts) != 1 {
		t.Fatal("expected systemInstruction with one text part")
	}
	if gotReq.SystemInstruction.Parts[0].Text != "system prompt" {
		t.Errorf("systemInstruction text = %q", gotReq.SystemInstruction.Parts[0].Text)
	}
	if len(gotReq.Contents) != 1 || gotReq.Contents[0].Role != "user" {
		t.Fatalf("contents = %#v, want one user content", gotReq.Contents)
	}
	if len(gotReq.Contents[0].Parts) != 1 || gotReq.Contents[0].Parts[0].Text != "user prompt" {
		t.Fatalf("user parts = %#v, want user prompt text", gotReq.Contents[0].Parts)
	}
	if gotReq.GenerationConfig == nil {
		t.Fatal("expected generationConfig")
	}
	if gotReq.GenerationConfig.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", gotReq.GenerationConfig.Temperature)
	}
	if gotReq.GenerationConfig.MaxOutputTokens != 64 {
		t.Errorf("maxOutputTokens = %d, want 64", gotReq.GenerationConfig.MaxOutputTokens)
	}
}

func TestGeminiClientChat_InteractionsRequestAndResponse(t *testing.T) {
	var gotReq geminiInteractionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1beta/interactions" {
			t.Errorf("path = %s, want /v1beta/interactions", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Errorf("x-goog-api-key header = %q, want secret", r.Header.Get("x-goog-api-key"))
		}
		if r.URL.RawQuery != "" {
			t.Errorf("URL must not contain query parameters (key leaked in URL): %s", r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"steps": [{
				"type": "model_output",
				"content": [
					{"type": "text", "text": "interaction "},
					{"type": "text", "text": "response"}
				]
			}]
		}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:    "gemini",
		BaseURL:     server.URL + "/v1beta/interactions",
		APIKey:      "secret",
		Model:       "gemini-3.5-flash",
		Temperature: 0.3,
		MaxTokens:   128,
	})

	got, err := client.Chat("system prompt", "user prompt")
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got != "interaction response" {
		t.Errorf("Chat = %q, want %q", got, "interaction response")
	}
	if gotReq.Model != "gemini-3.5-flash" {
		t.Errorf("model = %q, want gemini-3.5-flash", gotReq.Model)
	}
	if gotReq.Input != "user prompt" {
		t.Errorf("input = %#v, want user prompt", gotReq.Input)
	}
	if gotReq.SystemInstruction != "system prompt" {
		t.Errorf("system_instruction = %q, want system prompt", gotReq.SystemInstruction)
	}
	if gotReq.Stream {
		t.Error("stream = true, want false")
	}
	if gotReq.GenerationConfig == nil {
		t.Fatal("expected generation_config")
	}
	if gotReq.GenerationConfig.Temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3", gotReq.GenerationConfig.Temperature)
	}
	if gotReq.GenerationConfig.MaxOutputTokens != 128 {
		t.Errorf("max_output_tokens = %d, want 128", gotReq.GenerationConfig.MaxOutputTokens)
	}
}
