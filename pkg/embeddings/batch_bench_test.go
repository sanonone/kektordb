package embeddings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Mock-based benchmarks: serial embeds N texts in N HTTP round trips (with
// simulated provider latency), batch embeds all N in a single round trip.
// The two benchmarks embed the same 32 texts per iteration, so ns/op is
// directly comparable — the ratio is the throughput win for HTTP backends.
func BenchmarkOllamaEmbedSerial(b *testing.B) {
	benchOllamaMock(b, false)
}

func BenchmarkOllamaEmbedBatch(b *testing.B) {
	benchOllamaMock(b, true)
}

func benchOllamaMock(b *testing.B, useBatch bool) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate provider latency: one round trip for the batch, N for serial.
		time.Sleep(2 * time.Millisecond)
		switch r.URL.Path {
		case "/api/embed":
			var req struct {
				Input []string `json:"input"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			embeds := make([][]float32, len(req.Input))
			for i, txt := range req.Input {
				embeds[i] = hashVec(txt)
			}
			writeJSON(w, map[string]any{"embeddings": embeds})
		case "/api/embeddings":
			var req struct {
				Prompt string `json:"prompt"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			writeJSON(w, map[string]any{"embedding": hashVec(req.Prompt)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL+"/api/embeddings", "nomic-embed-text", 30*time.Second)
	texts := make([]string, 32)
	for i := range texts {
		texts[i] = fmt.Sprintf("benchmark chunk text number %d with enough words to be realistic", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if useBatch {
			if _, err := e.EmbedBatch(texts); err != nil {
				b.Fatal(err)
			}
		} else {
			for _, t := range texts {
				if _, err := e.Embed(t); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}
