package llm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds an OpenAIClient pointed at a mock server.
func newTestClient(t *testing.T, handler http.HandlerFunc) *OpenAIClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &OpenAIClient{
		cfg: Config{
			Provider: "openai",
			BaseURL:  srv.URL,
			Model:    "test-model",
		},
		httpClient: srv.Client(),
	}
}

func chatHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// A 200 response carrying neither content nor reasoning_content is not a
// successful completion: there is no output to hand back. Returning
// ("", nil) makes every caller believe the model answered, which hides
// expired API keys, quota exhaustion and wrong model names behind an
// empty string.
func TestChat_EmptyContentIsAnError(t *testing.T) {
	client := newTestClient(t, chatHandler(`{
		"choices": [{"message": {"content": "", "reasoning_content": ""}, "finish_reason": "stop"}]
	}`))

	got, err := client.Chat("system", "user")
	if err == nil {
		t.Fatalf("Chat returned no error for an empty response; got text %q", got)
	}
	if !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("Chat error = %v, want it to wrap ErrEmptyResponse", err)
	}
	if got != "" {
		t.Errorf("Chat text = %q, want empty alongside the error", got)
	}
}

// A provider error delivered with HTTP 200 and no choices must also fail.
func TestChat_ErrorBodyWith200IsAnError(t *testing.T) {
	client := newTestClient(t, chatHandler(`{
		"error": {"message": "invalid api key"}
	}`))

	_, err := client.Chat("system", "user")
	if err == nil {
		t.Fatal("Chat returned no error for a provider error delivered with status 200")
	}
}

// An empty body with no choices was already an error; keep it that way.
func TestChat_NoChoicesIsAnError(t *testing.T) {
	client := newTestClient(t, chatHandler(`{"choices": []}`))

	_, err := client.Chat("system", "user")
	if err == nil {
		t.Fatal("Chat returned no error for a response with no choices")
	}
}

// The reasoning fallback is deliberate: reasoning models can spend the whole
// budget in reasoning_content. It must keep working.
func TestChat_FallsBackToReasoningContent(t *testing.T) {
	client := newTestClient(t, chatHandler(`{
		"choices": [{"message": {"content": "", "reasoning_content": "the answer"}, "finish_reason": "stop"}]
	}`))

	got, err := client.Chat("system", "user")
	if err != nil {
		t.Fatalf("Chat returned an error for a reasoning-only response: %v", err)
	}
	if got != "the answer" {
		t.Errorf("Chat text = %q, want %q", got, "the answer")
	}
}

// Normal completions must be unaffected.
func TestChat_ContentIsReturned(t *testing.T) {
	client := newTestClient(t, chatHandler(`{
		"choices": [{"message": {"content": "hello"}, "finish_reason": "stop"}]
	}`))

	got, err := client.Chat("system", "user")
	if err != nil {
		t.Fatalf("Chat returned an error for a normal response: %v", err)
	}
	if got != "hello" {
		t.Errorf("Chat text = %q, want %q", got, "hello")
	}
}

// The error must name the finish_reason: "length" means the budget ran out,
// which is a different fix from a bad key.
func TestChat_EmptyErrorMentionsFinishReason(t *testing.T) {
	client := newTestClient(t, chatHandler(`{
		"choices": [{"message": {"content": ""}, "finish_reason": "length"}]
	}`))

	_, err := client.Chat("system", "user")
	if err == nil {
		t.Fatal("Chat returned no error for an empty response")
	}
	if !strings.Contains(err.Error(), "length") {
		t.Errorf("error %q does not mention finish_reason %q", err, "length")
	}
}
