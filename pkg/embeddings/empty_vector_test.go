package embeddings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbedRejectsEmptyVector covers an empty embedding returned as success.
//
// A provider answering {"embedding": []} or {"data": [{"embedding": []}]} decodes
// cleanly. Returning that with a nil error lets the caller store a zero vector:
// the index accepts it when the dimension is known, and cosine rank becomes
// meaningless. The memory looks saved and is unreachable.
func TestEmbedRejectsEmptyVector(t *testing.T) {
	tests := []struct {
		name string
		body string
		make func(url string) Embedder
	}{
		{
			name: "ollama empty embedding",
			body: `{"embedding": []}`,
			make: func(url string) Embedder { return &OllamaEmbedder{URL: url, Model: "m", Client: &http.Client{}} },
		},
		{
			name: "ollama missing embedding",
			body: `{}`,
			make: func(url string) Embedder { return &OllamaEmbedder{URL: url, Model: "m", Client: &http.Client{}} },
		},
		{
			name: "openai empty embedding",
			body: `{"data": [{"embedding": []}]}`,
			make: func(url string) Embedder {
				return &OpenAIEmbedder{URL: url, Model: "m", Client: &http.Client{}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			e := tt.make(srv.URL)
			vec, err := e.Embed("hello")
			if err == nil {
				t.Fatalf("Embed returned no error for an empty embedding; got %v", vec)
			}
			if len(vec) != 0 {
				t.Errorf("a failed Embed returned a vector of length %d", len(vec))
			}
		})
	}
}

// TestEmbedStillWorks keeps the happy path: a real vector must pass.
func TestEmbedStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding": [0.1, 0.2, 0.3]}`))
	}))
	defer srv.Close()

	e := &OllamaEmbedder{URL: srv.URL, Model: "m", Client: &http.Client{}}
	vec, err := e.Embed("hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("vector length = %d, want 3", len(vec))
	}
}

// TestValidateEmbeddingBatch covers a batch that has the right count but an
// empty vector inside it.
func TestValidateEmbeddingBatch(t *testing.T) {
	tests := []struct {
		name     string
		vecs     [][]float32
		expected int
		wantErr  bool
	}{
		{"all good", [][]float32{{0.1}, {0.2}}, 2, false},
		{"wrong count", [][]float32{{0.1}}, 2, true},
		{"empty in the middle", [][]float32{{0.1}, {}, {0.3}}, 3, true},
		{"nil first", [][]float32{nil, {0.2}}, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateEmbeddingBatch(tt.vecs, tt.expected)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestNoopStillErrors pins the fallback: it must not become a silent empty source.
func TestNoopStillErrors(t *testing.T) {
	var n NoopEmbedder
	if _, err := n.Embed("x"); err == nil {
		t.Fatal("NoopEmbedder.Embed returned no error")
	}
	if _, err := n.EmbedBatch([]string{"x"}); err == nil {
		t.Fatal("NoopEmbedder.EmbedBatch returned no error")
	}
	_ = json.Marshal
}
