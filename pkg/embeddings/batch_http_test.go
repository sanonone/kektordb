package embeddings

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hashVec returns a deterministic 8-dim vector derived from the text.
// Mock servers use it so batch results can be verified against serial Embed
// results (order + values must match exactly).
func hashVec(text string) []float32 {
	h := fnv.New32a()
	h.Write([]byte(text))
	base := h.Sum32()
	v := make([]float32, 8)
	for i := range v {
		v[i] = float32((base>>(i*3))%1000) / 1000.0
	}
	return v
}

func vecsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// --- Ollama ---

func newOllamaMock(t *testing.T, failBatch bool) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var batchHits, singleHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			atomic.AddInt32(&batchHits, 1)
			if failBatch {
				http.Error(w, "batch unsupported", http.StatusBadRequest)
				return
			}
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			embeds := make([][]float32, len(req.Input))
			for i, txt := range req.Input {
				embeds[i] = hashVec(txt)
			}
			writeJSON(w, map[string]any{"embeddings": embeds})
		case "/api/embeddings":
			atomic.AddInt32(&singleHits, 1)
			var req struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			writeJSON(w, map[string]any{"embedding": hashVec(req.Prompt)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &batchHits, &singleHits
}

func TestOllamaEmbedBatch_SingleRequestAndOrder(t *testing.T) {
	srv, batchHits, singleHits := newOllamaMock(t, false)
	e := NewOllamaEmbedder(srv.URL+"/api/embeddings", "nomic-embed-text", 5*time.Second)

	texts := []string{"alpha", "beta", "gamma", "delta"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if got := atomic.LoadInt32(batchHits); got != 1 {
		t.Errorf("batch endpoint hit %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(singleHits); got != 0 {
		t.Errorf("single endpoint hit %d times, want 0", got)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors for %d texts", len(vecs), len(texts))
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

func TestOllamaEmbedBatch_MatchesSerial(t *testing.T) {
	srv, _, _ := newOllamaMock(t, false)
	e := NewOllamaEmbedder(srv.URL+"/api/embeddings", "nomic-embed-text", 5*time.Second)

	texts := []string{"hello world", "another chunk of text", "kektordb memory engine"}
	batch, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	for i, txt := range texts {
		single, err := e.Embed(txt)
		if err != nil {
			t.Fatalf("Embed(%q): %v", txt, err)
		}
		if !vecsEqual(batch[i], single) {
			t.Errorf("batch vector %d differs from serial Embed", i)
		}
	}
}

func TestOllamaEmbedBatch_FallsBackToSerialOnFailure(t *testing.T) {
	srv, batchHits, singleHits := newOllamaMock(t, true)
	e := NewOllamaEmbedder(srv.URL+"/api/embeddings", "nomic-embed-text", 5*time.Second)

	texts := []string{"one", "two", "three"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch should fall back to serial, got error: %v", err)
	}
	if got := atomic.LoadInt32(batchHits); got != 1 {
		t.Errorf("batch endpoint hit %d times, want 1 (the failed attempt)", got)
	}
	if got := atomic.LoadInt32(singleHits); got != int32(len(texts)) {
		t.Errorf("single endpoint hit %d times, want %d", got, len(texts))
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("fallback vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

// --- OpenAI ---

func newOpenAIMock(t *testing.T, batchFail bool) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&hits, 1)
		var raw struct {
			Input json.RawMessage `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&raw) //nolint:errcheck
		if strings.HasPrefix(strings.TrimSpace(string(raw.Input)), "[") {
			// Batch request (array input).
			if batchFail {
				http.Error(w, "batch unsupported", http.StatusBadRequest)
				return
			}
			var texts []string
			json.Unmarshal(raw.Input, &texts) //nolint:errcheck
			data := make([]map[string]any, len(texts))
			for i, txt := range texts {
				data[i] = map[string]any{"index": i, "embedding": hashVec(txt)}
			}
			writeJSON(w, map[string]any{"data": data})
			return
		}
		// Single request (string input).
		var req struct {
			Input string `json:"input"`
		}
		json.Unmarshal(raw.Input, &req.Input) //nolint:errcheck
		writeJSON(w, map[string]any{"data": []map[string]any{
			{"index": 0, "embedding": hashVec(req.Input)},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestOpenAIEmbedBatch_SingleRequestAndOrder(t *testing.T) {
	srv, hits := newOpenAIMock(t, false)
	e := NewOpenAIEmbedder(srv.URL+"/v1/embeddings", "text-embedding-3-small", "sk-test", 5*time.Second)

	texts := []string{"red", "green", "blue", "yellow"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("endpoint hit %d times, want exactly 1", got)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors for %d texts", len(vecs), len(texts))
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

func TestOpenAIEmbedBatch_MatchesSerial(t *testing.T) {
	srv, _ := newOpenAIMock(t, false)
	e := NewOpenAIEmbedder(srv.URL+"/v1/embeddings", "text-embedding-3-small", "sk-test", 5*time.Second)

	texts := []string{"first text", "second text", "third text"}
	batch, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	for i, txt := range texts {
		single, err := e.Embed(txt)
		if err != nil {
			t.Fatalf("Embed(%q): %v", txt, err)
		}
		if !vecsEqual(batch[i], single) {
			t.Errorf("batch vector %d differs from serial Embed", i)
		}
	}
}

func TestOpenAIEmbedBatch_FallsBackToSerialOnFailure(t *testing.T) {
	srv, hits := newOpenAIMock(t, true)
	e := NewOpenAIEmbedder(srv.URL+"/v1/embeddings", "text-embedding-3-small", "sk-test", 5*time.Second)

	texts := []string{"one", "two", "three"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch should fall back to serial, got error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != int32(1+len(texts)) {
		t.Errorf("endpoint hit %d times, want 1 batch attempt + %d serial calls", got, len(texts))
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("fallback vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

// --- Gemini ---

func newGeminiMock(t *testing.T, failBatch bool) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var batchHits, singleHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":batchEmbedContents"):
			atomic.AddInt32(&batchHits, 1)
			if failBatch {
				http.Error(w, "batch unsupported", http.StatusBadRequest)
				return
			}
			var req struct {
				Requests []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"requests"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			embeds := make([]map[string]any, len(req.Requests))
			for i, rq := range req.Requests {
				text := ""
				if len(rq.Content.Parts) > 0 {
					text = rq.Content.Parts[0].Text
				}
				embeds[i] = map[string]any{"values": hashVec(text)}
			}
			writeJSON(w, map[string]any{"embeddings": embeds})
		case strings.HasSuffix(r.URL.Path, ":embedContent"):
			atomic.AddInt32(&singleHits, 1)
			var req struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			}
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			text := ""
			if len(req.Content.Parts) > 0 {
				text = req.Content.Parts[0].Text
			}
			writeJSON(w, map[string]any{"embedding": map[string]any{"values": hashVec(text)}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &batchHits, &singleHits
}

func TestGeminiEmbedBatch_SingleRequestAndOrder(t *testing.T) {
	srv, batchHits, singleHits := newGeminiMock(t, false)
	e := NewGeminiEmbedder(srv.URL+"/v1beta/models/gemini-embedding-001:embedContent", "gemini-embedding-001", "test-key", 5*time.Second)

	texts := []string{"alpha", "beta", "gamma"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if got := atomic.LoadInt32(batchHits); got != 1 {
		t.Errorf("batch endpoint hit %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(singleHits); got != 0 {
		t.Errorf("single endpoint hit %d times, want 0", got)
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

func TestGeminiEmbedBatch_MatchesSerial(t *testing.T) {
	srv, _, _ := newGeminiMock(t, false)
	e := NewGeminiEmbedder(srv.URL+"/v1beta/models/gemini-embedding-001:embedContent", "gemini-embedding-001", "test-key", 5*time.Second)

	texts := []string{"first text", "second text", "third text"}
	batch, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	for i, txt := range texts {
		single, err := e.Embed(txt)
		if err != nil {
			t.Fatalf("Embed(%q): %v", txt, err)
		}
		if !vecsEqual(batch[i], single) {
			t.Errorf("batch vector %d differs from serial Embed", i)
		}
	}
}

func TestGeminiEmbedBatch_FallsBackToSerialOnFailure(t *testing.T) {
	srv, batchHits, singleHits := newGeminiMock(t, true)
	e := NewGeminiEmbedder(srv.URL+"/v1beta/models/gemini-embedding-001:embedContent", "gemini-embedding-001", "test-key", 5*time.Second)

	texts := []string{"one", "two"}
	vecs, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatalf("EmbedBatch should fall back to serial, got error: %v", err)
	}
	if got := atomic.LoadInt32(batchHits); got != 1 {
		t.Errorf("batch endpoint hit %d times, want 1 (the failed attempt)", got)
	}
	if got := atomic.LoadInt32(singleHits); got != int32(len(texts)) {
		t.Errorf("single endpoint hit %d times, want %d", got, len(texts))
	}
	for i, txt := range texts {
		if !vecsEqual(vecs[i], hashVec(txt)) {
			t.Errorf("fallback vector %d does not match hashVec(%q)", i, txt)
		}
	}
}

// --- Empty input + Noop ---

func TestEmbedBatchEmptyInput(t *testing.T) {
	srv, _, _ := newOllamaMock(t, false)
	ollama := NewOllamaEmbedder(srv.URL+"/api/embeddings", "nomic-embed-text", 5*time.Second)
	vecs, err := ollama.EmbedBatch(nil)
	if err != nil || vecs != nil {
		t.Errorf("Ollama empty batch: vecs=%v err=%v, want nil/nil", vecs, err)
	}

	srv2, _ := newOpenAIMock(t, false)
	openai := NewOpenAIEmbedder(srv2.URL+"/v1/embeddings", "m", "k", 5*time.Second)
	vecs, err = openai.EmbedBatch(nil)
	if err != nil || vecs != nil {
		t.Errorf("OpenAI empty batch: vecs=%v err=%v, want nil/nil", vecs, err)
	}

	srv3, _, _ := newGeminiMock(t, false)
	gemini := NewGeminiEmbedder(srv3.URL+"/v1beta/models/gemini-embedding-001:embedContent", "gemini-embedding-001", "k", 5*time.Second)
	vecs, err = gemini.EmbedBatch(nil)
	if err != nil || vecs != nil {
		t.Errorf("Gemini empty batch: vecs=%v err=%v, want nil/nil", vecs, err)
	}
}

func TestNoopEmbedderBatchReturnsError(t *testing.T) {
	_, err := NoopEmbedder{}.EmbedBatch([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error from NoopEmbedder.EmbedBatch")
	}
}
