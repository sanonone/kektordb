package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failingEmbedder always fails, standing in for a dead Ollama.
type failingEmbedder struct{}

func (failingEmbedder) Embed(string) ([]float32, error) {
	return nil, errors.New("embedder unavailable")
}
func (failingEmbedder) EmbedBatch([]string) ([][]float32, error) {
	return nil, errors.New("embedder unavailable")
}

// TestProxyFailClosedWhenEmbeddingFails covers the silent bypass of the guard.
//
// The semantic firewall needs a vector. When embedding failed, the request was
// forwarded with no check and no trace: a dead embedder looked exactly like a
// healthy quiet one. fail_closed turns that into a refusal.
func TestProxyFailClosedWhenEmbeddingFails(t *testing.T) {
	p := &AIProxy{
		cfg: Config{
			FirewallEnabled:    true,
			FirewallFailClosed: true,
			Embedder:           failingEmbedder{},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hello there"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	defer func() {
		// The proxy may hit a nil reverse proxy further down; what matters here
		// is that it refused before forwarding.
		_ = recover()
	}()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the firewall cannot be evaluated", rec.Code)
	}
}

// TestProxyFailOpenCountsAndLogs keeps the default: the request still goes
// through, but the failure is now counted instead of vanishing.
func TestProxyFailOpenCountsAndLogs(t *testing.T) {
	p := &AIProxy{
		cfg: Config{
			FirewallEnabled: true,
			// FailClosed deliberately false: previous behaviour.
			Embedder: failingEmbedder{},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hello there"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	func() {
		defer func() { _ = recover() }()
		p.ServeHTTP(rec, req)
	}()

	if got := p.embedFailures.Load(); got == 0 {
		t.Error("the embedding failure was not counted: an operator has no way to see it")
	}
}
