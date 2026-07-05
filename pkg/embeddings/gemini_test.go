package embeddings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeminiEmbedder_RequestAndResponse(t *testing.T) {
	var gotReq struct {
		Model   string `json:"model"`
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/models/gemini-embedding-2:embedContent" {
			t.Errorf("path = %s, want Gemini embedContent path", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "secret" {
			t.Errorf("key query = %q, want secret", r.URL.Query().Get("key"))
		}
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Errorf("x-goog-api-key header = %q, want secret", r.Header.Get("x-goog-api-key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"embedding": {
				"values": [0.1, 0.2, 0.3]
			}
		}`))
	}))
	defer server.Close()

	embedder := NewGeminiEmbedder(
		server.URL+"/models/gemini-embedding-2:embedContent",
		"gemini-embedding-2",
		"secret",
		time.Second,
	)

	got, err := embedder.Embed("hello")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(got) != 3 || got[0] != 0.1 || got[1] != 0.2 || got[2] != 0.3 {
		t.Fatalf("embedding = %#v, want [0.1 0.2 0.3]", got)
	}
	if gotReq.Model != "models/gemini-embedding-2" {
		t.Errorf("model = %q, want models/gemini-embedding-2", gotReq.Model)
	}
	if len(gotReq.Content.Parts) != 1 || gotReq.Content.Parts[0].Text != "hello" {
		t.Fatalf("parts = %#v, want one text part", gotReq.Content.Parts)
	}
}
